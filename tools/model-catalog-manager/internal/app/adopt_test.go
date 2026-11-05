package app

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"sub2api-model-catalog-manager/internal/adminapi"
	"sub2api-model-catalog-manager/internal/catalog"
	"sub2api-model-catalog-manager/internal/config"
	"sub2api-model-catalog-manager/internal/securecred"
	"sub2api-model-catalog-manager/internal/state"
)

func TestAdoptWritesOnlyLocalOwnershipState(t *testing.T) {
	stateDir := t.TempDir()
	keysFile := writeKeysFile(t, stateDir, `["public-a", "public-b"]`)
	mapping := map[string]string{"public-a": "raw-a", "public-b": "raw-b"}
	expected, err := catalog.MappingFingerprint(mapping)
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeAdminAPI{account: adminapi.Account{ID: 9001, Platform: "openai", Type: "apikey", Protocol: "openai", BaseURL: "https://provider.example/v1", ModelMapping: mapping}}
	cfg := config.Config{
		Target: config.TargetConfig{BaseURL: "https://404token.xyz"}, Accounts: []config.AccountConfig{{ID: 9001, AcceptCredentialResponseMemoryExposure: boolPointer(true)}},
		Credential: config.CredentialConfig{WindowsCredentialTarget: "test"}, StateDir: stateDir,
	}
	credRead := 0
	adopter := Adopter{
		ReadCredential: func(config.CredentialConfig) (securecred.Value, error) {
			credRead++
			return securecred.Value{APIKey: []byte("test-admin-key")}, nil
		},
		NewClient: func(string) (AdminAPI, error) { return client, nil },
	}
	if err := adopter.Adopt(context.Background(), cfg, 9001, keysFile, expected); err != nil {
		t.Fatal(err)
	}
	got, err := state.Load(filepath.Join(stateDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	managed := got.Accounts["9001"].ManagedMappings
	if len(managed) != 2 || managed["public-a"].OriginalValue != "raw-a" || managed["public-b"].LastAppliedValue != "raw-b" {
		t.Fatalf("unexpected ownership state: %#v", managed)
	}
	if credRead != 1 || strings.Join(client.calls, ",") != "api_key,account:9001" {
		t.Fatalf("adoption made unexpected calls or credential reads: reads=%d calls=%v", credRead, client.calls)
	}
	stateBytes, err := os.ReadFile(filepath.Join(stateDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stateBytes), "local-secret") {
		t.Fatal("administrator secret was written to local state")
	}
}

func TestAdoptPreservesEmptyProtocolSourceIdentity(t *testing.T) {
	stateDir := t.TempDir()
	keysFile := writeKeysFile(t, stateDir, `["model-a"]`)
	account := adminapi.Account{ID: 9001, Platform: "openai", Type: "apikey", Protocol: "", BaseURL: "https://provider.example/v1", ModelMapping: map[string]string{"model-a": "model-a"}}
	expected, err := catalog.MappingFingerprint(account.ModelMapping)
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeAdminAPI{account: account}
	cfg := config.Config{
		Target: config.TargetConfig{BaseURL: "https://404token.xyz"}, Accounts: []config.AccountConfig{{ID: 9001, AcceptCredentialResponseMemoryExposure: boolPointer(true)}},
		Credential: config.CredentialConfig{WindowsCredentialTarget: "test"}, StateDir: stateDir,
	}
	adopter := Adopter{
		ReadCredential: func(config.CredentialConfig) (securecred.Value, error) {
			return securecred.Value{APIKey: []byte("test-admin-key")}, nil
		},
		NewClient: func(string) (AdminAPI, error) { return client, nil },
	}
	if err := adopter.Adopt(context.Background(), cfg, 9001, keysFile, expected); err != nil {
		t.Fatalf("adoption rejected an account with an unconfigured protocol: %v", err)
	}
	got, err := state.Load(filepath.Join(stateDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	source, err := catalog.SourceForAccount(account.Platform, account.Type, account.Protocol, account.BaseURL, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	managed := got.Accounts["9001"].ManagedMappings["model-a"]
	if managed.SourceFingerprint != catalog.SourceFingerprint(source) {
		t.Fatalf("adoption did not store the original empty protocol fingerprint: %#v", managed)
	}
}

func TestAdoptRejectsDriftAndAllowlistBeforeSideEffects(t *testing.T) {
	stateDir := t.TempDir()
	keysFile := writeKeysFile(t, stateDir, `["public-a"]`)
	client := &fakeAdminAPI{account: adminapi.Account{ID: 9001, Platform: "openai", Type: "apikey", Protocol: "openai", BaseURL: "https://provider.example/v1", ModelMapping: map[string]string{"public-a": "raw-a"}}}
	cfg := config.Config{
		Target: config.TargetConfig{BaseURL: "https://404token.xyz"}, Accounts: []config.AccountConfig{{ID: 9001, AcceptCredentialResponseMemoryExposure: boolPointer(true)}},
		Credential: config.CredentialConfig{WindowsCredentialTarget: "test"}, StateDir: stateDir,
	}
	credentialCalls := 0
	clientCalls := 0
	adopter := Adopter{
		ReadCredential: func(config.CredentialConfig) (securecred.Value, error) {
			credentialCalls++
			return securecred.Value{APIKey: []byte("test-admin-key")}, nil
		},
		NewClient: func(string) (AdminAPI, error) { clientCalls++; return client, nil },
	}
	if err := adopter.Adopt(context.Background(), cfg, 9002, keysFile, strings.Repeat("a", 64)); err == nil {
		t.Fatal("non-allowlisted account was adopted")
	}
	if clientCalls != 0 || credentialCalls != 0 {
		t.Fatalf("allowlist gate ran too late: clients=%d credential reads=%d", clientCalls, credentialCalls)
	}
	if err := adopter.Adopt(context.Background(), cfg, 9001, keysFile, strings.Repeat("a", 64)); err == nil {
		t.Fatal("wrong mapping hash was accepted")
	}
	if _, err := os.Stat(filepath.Join(stateDir, "state.json")); !os.IsNotExist(err) {
		t.Fatalf("failed adoption must not create state: %v", err)
	}
}

func TestAdoptRejectsShadowAndAlreadyManagedDrift(t *testing.T) {
	stateDir := t.TempDir()
	keysFile := writeKeysFile(t, stateDir, `["public-a"]`)
	cfg := config.Config{Accounts: []config.AccountConfig{{ID: 9001, AcceptCredentialResponseMemoryExposure: boolPointer(true)}}, Credential: config.CredentialConfig{WindowsCredentialTarget: "test"}, StateDir: stateDir}
	cred := func(config.CredentialConfig) (securecred.Value, error) {
		return securecred.Value{APIKey: []byte("test-admin-key")}, nil
	}
	shadow := int64(1)
	shadowClient := &fakeAdminAPI{account: adminapi.Account{ID: 9001, Platform: "openai", Type: "apikey", Protocol: "openai", BaseURL: "https://provider.example", ParentAccountID: &shadow, ModelMapping: map[string]string{"public-a": "raw-a"}}}
	if err := (Adopter{ReadCredential: cred, NewClient: func(string) (AdminAPI, error) { return shadowClient, nil }}).Adopt(context.Background(), cfg, 9001, keysFile, strings.Repeat("a", 64)); err == nil {
		t.Fatal("credential shadow adoption was accepted")
	}
	if _, err := os.Stat(filepath.Join(stateDir, "state.json")); !os.IsNotExist(err) {
		t.Fatalf("shadow adoption wrote state: %v", err)
	}

	source, _ := catalog.SourceForAccount("openai", "apikey", "openai", "https://provider.example", 9001)
	prior := state.Empty()
	prior.Accounts["9001"] = state.Account{ManagedMappings: map[string]state.ManagedMapping{"public-a": {OriginalValue: "old", LastAppliedValue: "old", SourceFingerprint: catalog.SourceFingerprint(source)}}}
	if err := state.Save(filepath.Join(stateDir, "state.json"), prior); err != nil {
		t.Fatal(err)
	}
	client := &fakeAdminAPI{account: adminapi.Account{ID: 9001, Platform: "openai", Type: "apikey", Protocol: "openai", BaseURL: "https://provider.example", ModelMapping: map[string]string{"public-a": "changed"}}}
	expected, _ := catalog.MappingFingerprint(client.account.ModelMapping)
	if err := (Adopter{ReadCredential: cred, NewClient: func(string) (AdminAPI, error) { return client, nil }}).Adopt(context.Background(), cfg, 9001, keysFile, expected); err == nil {
		t.Fatal("drifted existing managed entry was silently adopted")
	}
	got, err := state.Load(filepath.Join(stateDir, "state.json"))
	if err != nil || got.Accounts["9001"].ManagedMappings["public-a"].LastAppliedValue != "old" {
		t.Fatalf("failed adoption modified state: state=%#v err=%v", got, err)
	}
}

func TestReadKeysFileRequiresExactUniqueStringArray(t *testing.T) {
	dir := t.TempDir()
	for i, body := range []string{`[]`, `["a", "a"]`, `[" a"]`, `[1]`, `["a"] {}`} {
		path := writeKeysFile(t, filepath.Join(dir, strconv.Itoa(i)), body)
		if _, err := readKeysFile(path); err == nil {
			t.Errorf("accepted invalid key list: %s", body)
		}
	}
	path := writeKeysFile(t, filepath.Join(dir, "valid"), `["one", "two"]`)
	keys, err := readKeysFile(path)
	if err != nil || len(keys) != 2 || keys[0] != "one" {
		t.Fatalf("valid list rejected: %v, %v", keys, err)
	}
}

func writeKeysFile(t *testing.T, dir, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "selected-keys.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
