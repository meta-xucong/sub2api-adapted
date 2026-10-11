package app

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"sub2api-model-catalog-manager/internal/adminapi"
	"sub2api-model-catalog-manager/internal/catalog"
	"sub2api-model-catalog-manager/internal/config"
	"sub2api-model-catalog-manager/internal/securecred"
)

func TestInspectExposureGateRunsBeforeCredentialOrClient(t *testing.T) {
	cfg := config.Config{Target: config.TargetConfig{BaseURL: "https://target.example"}, Accounts: []config.AccountConfig{{ID: 9001, AcceptCredentialResponseMemoryExposure: boolPointer(false)}}, Credential: config.CredentialConfig{WindowsCredentialTarget: "test"}}
	credCalled, clientCalled := false, false
	i := Inspector{
		ReadCredential: func(config.CredentialConfig) (securecred.Value, error) {
			credCalled = true
			return securecred.Value{}, nil
		},
		NewClient: func(string) (AdminAPI, error) { clientCalled = true; return &fakeAdminAPI{}, nil },
	}
	doc, err := i.Inspect(context.Background(), cfg)
	if err != nil || len(doc.Accounts) != 1 || doc.Accounts[0].InspectionStatus != "blocked_credential_memory_exposure_not_accepted" {
		t.Fatalf("expected a local blocked report, got doc=%#v err=%v", doc, err)
	}
	if credCalled || clientCalled {
		t.Fatalf("gate did not stop before side effects: credential=%v client=%v", credCalled, clientCalled)
	}
}

func TestInspectUsesOnlyLoginStatusAndAllowlistedAccountGets(t *testing.T) {
	client := &fakeAdminAPI{account: adminapi.Account{ID: 9001, Platform: "openai", Type: "apikey", Protocol: "openai", BaseURL: "https://user:password@provider.example/v1?api_key=TOP-SECRET#private", ModelMapping: map[string]string{"public": "upstream"}}}
	cfg := config.Config{Target: config.TargetConfig{BaseURL: "https://target.example"}, Accounts: []config.AccountConfig{{ID: 9001, AcceptCredentialResponseMemoryExposure: boolPointer(true)}}, Credential: config.CredentialConfig{WindowsCredentialTarget: "test"}}
	i := Inspector{
		ReadCredential: func(config.CredentialConfig) (securecred.Value, error) {
			return securecred.Value{APIKey: []byte("test-admin-key")}, nil
		},
		NewClient: func(string) (AdminAPI, error) { return client, nil },
	}
	doc, err := i.Inspect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Accounts) != 1 || doc.Accounts[0].Mappings[0].UpstreamID != "upstream" {
		t.Fatalf("unexpected report: %#v", doc)
	}
	wantHash, _ := catalog.MappingFingerprint(client.account.ModelMapping)
	if doc.Accounts[0].MappingSHA256 != wantHash || doc.Accounts[0].SourceFingerprint != "" {
		t.Fatalf("unexpected mapping/source fingerprints: %#v", doc.Accounts[0])
	}
	serialized, _ := json.Marshal(doc)
	for _, secret := range []string{"password", "TOP-SECRET", "private"} {
		if strings.Contains(string(serialized), secret) {
			t.Fatalf("inspect report leaked URL secret %q", secret)
		}
	}
	if strings.Join(client.calls, ",") != "api_key,status,account:9001" {
		t.Fatalf("inspect call sequence unexpected: %v", client.calls)
	}
}

func TestInspectReportsAbsentMappingWithoutInitializingIt(t *testing.T) {
	client := &fakeAdminAPI{account: adminapi.Account{ID: 12, Platform: "openai", Type: "apikey", Protocol: "openai", BaseURL: "https://provider.example/v1"}}
	cfg := config.Config{Target: config.TargetConfig{BaseURL: "https://404token.xyz"}, Accounts: []config.AccountConfig{{ID: 12, AcceptCredentialResponseMemoryExposure: boolPointer(true)}}, Credential: config.CredentialConfig{WindowsCredentialTarget: "test"}}
	i := Inspector{
		ReadCredential: func(config.CredentialConfig) (securecred.Value, error) {
			return securecred.Value{APIKey: []byte("test-admin-key")}, nil
		},
		NewClient: func(string) (AdminAPI, error) { return client, nil },
	}
	doc, err := i.Inspect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Accounts) != 1 || doc.Accounts[0].InspectionStatus != "inspected" || doc.Accounts[0].MappingStatus != "no_explicit_mapping" || doc.Accounts[0].MappingSHA256 != "" || len(doc.Accounts[0].Mappings) != 0 {
		t.Fatalf("absent mapping was not reported distinctly: %#v", doc.Accounts)
	}
}

func TestInspectDistinguishesExplicitEmptyMapping(t *testing.T) {
	client := &fakeAdminAPI{account: adminapi.Account{ID: 14, Platform: "openai", Type: "apikey", Protocol: "openai", BaseURL: "https://provider.example/v1", ModelMapping: map[string]string{}}}
	cfg := config.Config{Target: config.TargetConfig{BaseURL: "https://404token.xyz"}, Accounts: []config.AccountConfig{{ID: 14, AcceptCredentialResponseMemoryExposure: boolPointer(true)}}, Credential: config.CredentialConfig{WindowsCredentialTarget: "test"}}
	i := Inspector{
		ReadCredential: func(config.CredentialConfig) (securecred.Value, error) {
			return securecred.Value{APIKey: []byte("test-admin-key")}, nil
		},
		NewClient: func(string) (AdminAPI, error) { return client, nil },
	}
	doc, err := i.Inspect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Accounts) != 1 || doc.Accounts[0].MappingStatus != "explicit_empty" || doc.Accounts[0].MappingSHA256 == "" {
		t.Fatalf("explicit empty mapping was not distinguished: %#v", doc.Accounts)
	}
}

func TestInspectEnumeratesAllAccountsOnlyAfterGlobalPrivacyGate(t *testing.T) {
	client := &fakeAdminAPI{account: adminapi.Account{ID: 9001, Platform: "openai", Type: "apikey", Protocol: "openai", BaseURL: "https://provider.example", ModelMapping: map[string]string{"public": "upstream"}}}
	cfg := config.Config{Target: config.TargetConfig{BaseURL: "https://404token.xyz", RuntimeCompatibility: config.RuntimeCompatibility{BuildID: "test-build", APIContractID: config.APIContractAllAccountsV1, Evidence: "mock-only fixture", ReviewedAt: "2026-10-11"}}, AccountScope: "all", AcceptAllCredentialResponseMemoryExposure: true, Credential: config.CredentialConfig{WindowsCredentialTarget: "test"}}
	i := Inspector{
		ReadCredential: func(config.CredentialConfig) (securecred.Value, error) {
			return securecred.Value{APIKey: []byte("test-admin-key")}, nil
		},
		NewClient: func(string) (AdminAPI, error) { return client, nil },
	}
	doc, err := i.Inspect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Accounts) != 1 || doc.Accounts[0].ID != 9001 || doc.AccountScope != "all" || doc.AccountInventoryCount != 1 || doc.AccountInventorySHA256 == "" {
		t.Fatalf("all-account inventory was not reported: %#v", doc)
	}
	if strings.Join(client.calls, ",") != "api_key,status,list_accounts,account:9001" {
		t.Fatalf("all-account call sequence unexpected: %v", client.calls)
	}

	blockedClientCalls, credentialCalls := false, false
	cfg.AcceptAllCredentialResponseMemoryExposure = false
	i.ReadCredential = func(config.CredentialConfig) (securecred.Value, error) {
		credentialCalls = true
		return securecred.Value{}, nil
	}
	i.NewClient = func(string) (AdminAPI, error) { blockedClientCalls = true; return &fakeAdminAPI{}, nil }
	if _, err := i.Inspect(context.Background(), cfg); err == nil || credentialCalls || blockedClientCalls {
		t.Fatalf("global privacy gate was not enforced before access: err=%v credential=%v client=%v", err, credentialCalls, blockedClientCalls)
	}
}

func TestInspectContinuesAllAccountInventoryWhenOneDetailReadFails(t *testing.T) {
	client := &fakeAdminAPI{
		account:          adminapi.Account{ID: 9001, Platform: "openai", Type: "apikey", Protocol: "openai", BaseURL: "https://provider.example", ModelMapping: map[string]string{"public": "upstream"}},
		accounts:         map[int64]adminapi.Account{9001: {ID: 9001, Platform: "openai", Type: "apikey", Protocol: "openai", BaseURL: "https://provider.example", ModelMapping: map[string]string{"public": "upstream"}}, 9003: {ID: 9003, Platform: "anthropic", Type: "apikey", Protocol: "anthropic", BaseURL: "https://provider.example", ModelMapping: map[string]string{"claude": "claude"}}},
		accountSummaries: []adminapi.AccountSummary{{ID: 9001}, {ID: 9002}, {ID: 9003}},
		accountErrors:    map[int64]error{9002: errors.New("mock detail read failure")},
	}
	cfg := config.Config{
		Target:       config.TargetConfig{BaseURL: "https://404token.xyz", RuntimeCompatibility: config.RuntimeCompatibility{BuildID: "test-build", APIContractID: config.APIContractAllAccountsV1, Evidence: "mock-only fixture", ReviewedAt: "2026-10-11"}},
		AccountScope: "all", AcceptAllCredentialResponseMemoryExposure: true,
		Credential: config.CredentialConfig{WindowsCredentialTarget: "test"},
	}
	i := Inspector{
		ReadCredential: func(config.CredentialConfig) (securecred.Value, error) {
			return securecred.Value{APIKey: []byte("test-admin-key")}, nil
		},
		NewClient: func(string) (AdminAPI, error) { return client, nil },
	}
	doc, err := i.Inspect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if doc.AccountScanStatus != "partial" || doc.AccountInventoryCount != 3 || len(doc.Accounts) != 3 {
		t.Fatalf("scan did not report every inventory ID: %#v", doc)
	}
	statuses := map[int64]string{}
	for _, row := range doc.Accounts {
		statuses[row.ID] = row.InspectionStatus
	}
	if statuses[9001] != "inspected" || statuses[9002] != "account_read_failed" || statuses[9003] != "inspected" {
		t.Fatalf("unexpected per-account inspection statuses: %v", statuses)
	}
	if strings.Join(client.calls, ",") != "api_key,status,list_accounts,account:9001,account:9002,account:9003" {
		t.Fatalf("account detail reads did not continue across the inventory: %v", client.calls)
	}
}

func TestInspectPreservesEmptyProtocolInSourceFingerprint(t *testing.T) {
	account := adminapi.Account{ID: 9001, Platform: "openai", Type: "apikey", Protocol: "", BaseURL: "https://provider.example/v1", ModelMapping: map[string]string{"model-a": "model-a"}}
	client := &fakeAdminAPI{account: account}
	cfg := config.Config{Target: config.TargetConfig{BaseURL: "https://target.example"}, Accounts: []config.AccountConfig{{ID: 9001, AcceptCredentialResponseMemoryExposure: boolPointer(true)}}, Credential: config.CredentialConfig{WindowsCredentialTarget: "test"}}
	i := Inspector{
		ReadCredential: func(config.CredentialConfig) (securecred.Value, error) {
			return securecred.Value{APIKey: []byte("test-admin-key")}, nil
		},
		NewClient: func(string) (AdminAPI, error) { return client, nil },
	}
	doc, err := i.Inspect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	wantSource, err := catalog.SourceForAccount(account.Platform, account.Type, account.Protocol, account.BaseURL, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Accounts) != 1 || doc.Accounts[0].Protocol != "" || doc.Accounts[0].SourceFingerprint != catalog.SourceFingerprint(wantSource) {
		t.Fatalf("inspect did not preserve the original empty protocol identity: %#v", doc.Accounts)
	}
}

type fakeAdminAPI struct {
	calls            []string
	account          adminapi.Account
	accounts         map[int64]adminapi.Account
	accountSummaries []adminapi.AccountSummary
	accountErrors    map[int64]error
}

func (f *fakeAdminAPI) SetAPIKey([]byte) error {
	f.calls = append(f.calls, "api_key")
	return nil
}

func (f *fakeAdminAPI) RefreshStatus(context.Context) (adminapi.RefreshStatus, error) {
	f.calls = append(f.calls, "status")
	return adminapi.RefreshStatus{Enabled: true, RunStatus: "succeeded"}, nil
}

func (f *fakeAdminAPI) ListAllAccounts(context.Context) ([]adminapi.AccountSummary, error) {
	f.calls = append(f.calls, "list_accounts")
	if f.accountSummaries != nil {
		return append([]adminapi.AccountSummary(nil), f.accountSummaries...), nil
	}
	if f.account.ID == 0 {
		return nil, errors.New("missing account")
	}
	return []adminapi.AccountSummary{{ID: f.account.ID}}, nil
}

func (f *fakeAdminAPI) Account(_ context.Context, id int64) (adminapi.Account, error) {
	f.calls = append(f.calls, "account:"+intString(id))
	if err := f.accountErrors[id]; err != nil {
		return adminapi.Account{}, err
	}
	if f.accounts != nil {
		account, ok := f.accounts[id]
		if !ok {
			return adminapi.Account{}, errors.New("missing account")
		}
		return account, nil
	}
	if f.account.ID != id {
		return adminapi.Account{}, errors.New("missing account")
	}
	return f.account, nil
}

func (f *fakeAdminAPI) SyncUpstream(context.Context, int64) (adminapi.Catalog, error) {
	f.calls = append(f.calls, "sync")
	return adminapi.Catalog{}, errors.New("unexpected sync")
}

func (f *fakeAdminAPI) BulkUpdateMapping(context.Context, int64, map[string]string) (adminapi.BulkUpdateResult, error) {
	f.calls = append(f.calls, "bulk")
	return adminapi.BulkUpdateResult{}, errors.New("unexpected write")
}

func (f *fakeAdminAPI) Close() {}

func intString(id int64) string { return strconv.FormatInt(id, 10) }

func boolPointer(v bool) *bool { return &v }
