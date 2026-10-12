package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"sub2api-model-catalog-manager/internal/catalog"
	"sub2api-model-catalog-manager/internal/config"
	"sub2api-model-catalog-manager/internal/state"
)

const maxAdoptKeysBytes = 64 << 10

var sha256Pattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

type Adopter struct {
	ReadCredential CredentialReader
	NewClient      ClientFactory
}

type AdoptionPreflight struct {
	Keys                []string
	LocalState          state.State
	StateHash           string
	AccountID           int64
	ExpectedMappingHash string
	ConfigFingerprint   string
	KeysFingerprint     string
}

// Adopt transfers local ownership of exact mapping keys. It never calls a
// Sub2API write endpoint; the only persistent mutation is the local state file.
func (a Adopter) Adopt(ctx context.Context, cfg config.Config, accountID int64, keysFile, expectedMappingHash string) error {
	prepared, err := a.Preflight(cfg, accountID, keysFile, expectedMappingHash)
	if err != nil {
		return err
	}
	return a.AdoptPrepared(ctx, cfg, accountID, expectedMappingHash, prepared)
}

func (a Adopter) Preflight(cfg config.Config, accountID int64, keysFile, expectedMappingHash string) (AdoptionPreflight, error) {
	var prepared AdoptionPreflight
	if accountID <= 0 || strings.TrimSpace(keysFile) == "" || !sha256Pattern.MatchString(expectedMappingHash) {
		return prepared, errors.New("adoption requires an account, a key list, and a SHA256 mapping hash")
	}
	if err := cfg.ExposureGateFor([]int64{accountID}); err != nil {
		return prepared, err
	}
	keys, err := readKeysFile(keysFile)
	if err != nil {
		return prepared, err
	}
	if a.ReadCredential == nil || a.NewClient == nil {
		return prepared, errors.New("adoption dependencies are unavailable")
	}
	statePath := filepath.Join(cfg.StateDir, "state.json")
	localState, err := state.Load(statePath)
	if err != nil {
		return prepared, err
	}
	stateHash, err := optionalStateHash(statePath)
	if err != nil {
		return prepared, err
	}
	configFingerprint, err := jsonFingerprint(cfg)
	if err != nil {
		return prepared, errors.New("adoption config fingerprint could not be computed")
	}
	keysFingerprint, err := jsonFingerprint(keys)
	if err != nil {
		return prepared, errors.New("adoption key selection fingerprint could not be computed")
	}
	return AdoptionPreflight{Keys: keys, LocalState: localState, StateHash: stateHash, AccountID: accountID,
		ExpectedMappingHash: strings.ToLower(expectedMappingHash), ConfigFingerprint: configFingerprint, KeysFingerprint: keysFingerprint}, nil
}

func (a Adopter) AdoptPrepared(ctx context.Context, cfg config.Config, accountID int64, expectedMappingHash string, prepared AdoptionPreflight) error {
	if accountID <= 0 || !sha256Pattern.MatchString(expectedMappingHash) || len(prepared.Keys) == 0 || prepared.LocalState.Accounts == nil || prepared.StateHash == "" || prepared.AccountID != accountID || prepared.ExpectedMappingHash != strings.ToLower(expectedMappingHash) {
		return errors.New("adoption preflight snapshot is unavailable")
	}
	configFingerprint, configErr := jsonFingerprint(cfg)
	keysFingerprint, keysErr := jsonFingerprint(prepared.Keys)
	if configErr != nil || keysErr != nil || configFingerprint != prepared.ConfigFingerprint || keysFingerprint != prepared.KeysFingerprint {
		return errors.New("adoption stopped: config or selected key list changed after preflight")
	}
	if err := cfg.ExposureGateFor([]int64{accountID}); err != nil {
		return err
	}
	if a.ReadCredential == nil || a.NewClient == nil {
		return errors.New("adoption dependencies are unavailable")
	}
	lock, err := state.AcquireLock(filepath.Join(cfg.StateDir, "run.lock"))
	if err != nil {
		return err
	}
	defer lock.Release()
	statePath := filepath.Join(cfg.StateDir, "state.json")
	currentStateHash, err := optionalStateHash(statePath)
	if err != nil || currentStateHash != prepared.StateHash {
		return errors.New("adoption stopped: local state changed after preflight")
	}
	cred, err := a.ReadCredential(cfg.Credential)
	if err != nil {
		return errors.New("administrator credential could not be read from the configured OS store")
	}
	defer cred.Clear()
	client, err := a.NewClient(cfg.Target.BaseURL)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.SetAPIKey(cred.APIKey); err != nil {
		return err
	}
	account, err := client.Account(ctx, accountID)
	if err != nil {
		return err
	}
	if account.ParentAccountID != nil {
		return errors.New("credential shadow accounts cannot be adopted")
	}
	source, err := catalog.SourceForAccount(account.Platform, account.Type, account.Protocol, account.BaseURL, account.ID)
	if err != nil {
		return errors.New("account source fingerprint is incomplete")
	}
	actualHash, err := catalog.MappingFingerprint(account.ModelMapping)
	if err != nil || !strings.EqualFold(actualHash, expectedMappingHash) {
		return errors.New("current mapping does not match the expected SHA256; adoption was not made")
	}
	for _, key := range prepared.Keys {
		if _, exists := account.ModelMapping[key]; !exists {
			return errors.New("one or more selected mapping keys are absent; adoption was not made")
		}
	}

	current := prepared.LocalState
	accountKey := strconv.FormatInt(accountID, 10)
	local := current.Accounts[accountKey]
	if local.ManagedMappings == nil {
		local.ManagedMappings = map[string]state.ManagedMapping{}
	}
	sourceFingerprint := catalog.SourceFingerprint(source)
	for _, key := range prepared.Keys {
		value := account.ModelMapping[key]
		if prior, exists := local.ManagedMappings[key]; exists {
			if prior.SourceFingerprint != sourceFingerprint || prior.LastAppliedValue != value {
				return errors.New("a selected mapping is already managed but its source or value drifted; adoption was not made")
			}
			continue
		}
		local.ManagedMappings[key] = state.ManagedMapping{
			OriginalValue: value, LastAppliedValue: value, SourceFingerprint: sourceFingerprint,
		}
	}
	current.Accounts[accountKey] = local
	return state.Save(statePath, current)
}

func readKeysFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("adoption key list is unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxAdoptKeysBytes {
		return nil, errors.New("adoption key list must be a small regular JSON file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxAdoptKeysBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxAdoptKeysBytes {
		return nil, errors.New("adoption key list could not be read safely")
	}
	defer zero(data)
	dec := json.NewDecoder(bytes.NewReader(data))
	var keys []string
	if err := dec.Decode(&keys); err != nil || keys == nil {
		return nil, errors.New("adoption key list must be a JSON string array")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("adoption key list contains trailing data")
	}
	if len(keys) == 0 {
		return nil, errors.New("adoption key list is empty")
	}
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if strings.TrimSpace(key) == "" || key != strings.TrimSpace(key) || strings.ContainsAny(key, "\x00\r\n") || seen[key] {
			return nil, fmt.Errorf("adoption key list has an invalid or duplicate entry")
		}
		seen[key] = true
	}
	return keys, nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
