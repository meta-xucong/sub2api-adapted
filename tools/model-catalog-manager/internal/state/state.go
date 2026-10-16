package state

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const CurrentVersion = 1
const maxStateBytes = 8 << 20

type State struct {
	Version       int                     `json:"version"`
	Accounts      map[string]Account      `json:"accounts"`
	ConsumedPlans map[string]ConsumedPlan `json:"consumed_plans,omitempty"`
}

type Account struct {
	ManagedMappings map[string]ManagedMapping `json:"managed_mappings,omitempty"`
	DirectoryHash   string                    `json:"directory_hash,omitempty"`
	RulesHash       string                    `json:"rules_hash,omitempty"`
	CatalogSnapshot *CatalogSnapshot          `json:"catalog_snapshot,omitempty"`
	LastSuccessAt   *time.Time                `json:"last_success_at,omitempty"`
	AbsenceCounts   map[string]int            `json:"absence_counts,omitempty"`
}

type CatalogSnapshot struct {
	Hash              string    `json:"hash"`
	ObservedAt        time.Time `json:"observed_at"`
	Completeness      string    `json:"completeness"`
	CompleteProfileID string    `json:"complete_profile_id,omitempty"`
	LiveListAvailable bool      `json:"live_list_available"`
	SourceFingerprint string    `json:"source_fingerprint"`
}

type ConsumedPlan struct {
	ConsumedAt time.Time `json:"consumed_at"`
	AccountIDs []int64   `json:"account_ids"`
}

type ManagedMapping struct {
	OriginalValue     string `json:"original_value"`
	LastAppliedValue  string `json:"last_applied_value"`
	SourceFingerprint string `json:"source_fingerprint"`
}

func Empty() State {
	return State{Version: CurrentVersion, Accounts: map[string]Account{}, ConsumedPlans: map[string]ConsumedPlan{}}
}

func Load(path string) (State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Empty(), nil
	}
	if err != nil {
		return State{}, errors.New("state could not be read")
	}
	if len(data) > maxStateBytes {
		return State{}, errors.New("state exceeds size limit")
	}
	defer zero(data)
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s State
	if err := dec.Decode(&s); err != nil {
		return State{}, errors.New("state is corrupt; apply is blocked")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return State{}, errors.New("state has trailing data; apply is blocked")
	}
	if s.Version != CurrentVersion || s.Accounts == nil {
		return State{}, fmt.Errorf("unsupported state schema version; apply is blocked")
	}
	if s.ConsumedPlans == nil {
		s.ConsumedPlans = map[string]ConsumedPlan{}
	}
	for id, account := range s.Accounts {
		if n, err := strconv.ParseInt(id, 10, 64); err != nil || n <= 0 || account.ManagedMappings == nil && account.AbsenceCounts != nil {
			return State{}, errors.New("state contains an invalid account entry; apply is blocked")
		}
	}
	return s, nil
}

// Hash returns the SHA256 of the exact on-disk bytes. Apply plans bind this raw
// hash so a reformat or any state change invalidates prior approval.
func Hash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("state hash is unavailable")
	}
	if len(data) > maxStateBytes {
		zero(data)
		return "", errors.New("state exceeds size limit")
	}
	defer zero(data)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

// WriteMappingBackup writes a new, mapping-only recovery artifact. The unique
// file is created exclusively and flushed before any remote write is allowed.
func WriteMappingBackup(dir string, accountID int64, planHash, sourceFingerprint string, mapping map[string]string, createdAt time.Time) (string, error) {
	if accountID <= 0 || mapping == nil || len(planHash) != 64 || sourceFingerprint == "" {
		return "", errors.New("mapping backup input is invalid")
	}
	hash, err := mappingHash(mapping)
	if err != nil {
		return "", err
	}
	body, err := json.MarshalIndent(struct {
		SchemaVersion     int               `json:"schema_version"`
		AccountID         int64             `json:"account_id"`
		PlanSHA256        string            `json:"plan_sha256"`
		SourceFingerprint string            `json:"source_fingerprint"`
		MappingSHA256     string            `json:"mapping_sha256"`
		CreatedAt         time.Time         `json:"created_at"`
		ModelMapping      map[string]string `json:"model_mapping"`
	}{1, accountID, planHash, sourceFingerprint, hash, createdAt.UTC(), mapping}, "", "  ")
	if err != nil {
		return "", errors.New("mapping backup serialization failed")
	}
	defer zero(body)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", errors.New("mapping backup directory could not be created")
	}
	name := fmt.Sprintf("mapping-plan-%s-account-%d.json", planHash, accountID)
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", errors.New("mapping backup already exists or could not be created")
	}
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", errors.New("mapping backup permissions could not be restricted")
	}
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", errors.New("mapping backup write failed")
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", errors.New("mapping backup flush failed")
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", errors.New("mapping backup close failed")
	}
	if err := syncDirectory(dir); err != nil {
		return "", errors.New("mapping backup directory flush failed")
	}
	return path, nil
}

func mappingHash(mapping map[string]string) (string, error) {
	encoded, err := json.Marshal(mapping)
	if err != nil {
		return "", errors.New("mapping backup hash failed")
	}
	h := sha256.Sum256(encoded)
	return hex.EncodeToString(h[:]), nil
}

func Save(path string, s State) error {
	if s.Version != CurrentVersion || s.Accounts == nil {
		return errors.New("refusing to write invalid state")
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return errors.New("state serialization failed")
	}
	defer zero(data)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("state directory could not be created")
	}
	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return errors.New("temporary state file could not be created")
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return errors.New("state file permissions could not be restricted")
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return errors.New("state write failed")
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return errors.New("state flush failed")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("state close failed")
	}
	if err := atomicReplace(tmpName, path); err != nil {
		return fmt.Errorf("state atomic replace failed: %w", err)
	}
	return syncDirectory(dir)
}

type Lock struct{ path string }

func AcquireLock(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, errors.New("lock directory could not be created")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, errors.New("another run may be active or a prior lock needs manual review")
	}
	_, writeErr := fmt.Fprintf(f, "pid=%d\nstarted_at=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return nil, errors.New("lock file could not be initialized")
	}
	return &Lock{path: path}, nil
}

func (l *Lock) Release() error {
	if l == nil || l.path == "" {
		return nil
	}
	if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("lock file could not be removed")
	}
	return nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
