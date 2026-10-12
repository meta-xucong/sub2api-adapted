package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestCatalogDiagnosticsReportsExactSnapshotDifferencesAndDuplicates(t *testing.T) {
	attempt := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)
	next := attempt.Add(time.Hour)
	snapshot := &adminapi.PublishedSnapshot{
		ContractStatus: adminapi.PublishedSnapshotContractStatusSupported,
		Status:         "stale",
		Source:         "upstream_models_endpoint",
		LastAttemptAt:  &attempt,
		NextRefreshAt:  &next,
		RawDigest:      "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		RawModels:      []string{"raw-only", "shared", "shared"},
		PublicModels:   []string{"public-only", "shared", "shared"},
		PublicToUpstream: map[string]string{
			"public-only": "raw-only",
			"shared":      "shared",
		},
		RawCount:              3,
		PublicCount:           3,
		PublicToUpstreamCount: 2,
		DuplicateRawIDs:       []string{"shared"},
		DuplicatePublicIDs:    []string{"shared"},
	}
	d := catalogDiagnostics(map[string]string{"mapped-only": "upstream"}, snapshot, []string{"live-only", "shared"}, true, attempt)
	if d.SnapshotEffectivePublicCount != 2 || d.MappingPublicOnlyVsPublic[0] != "mapped-only" || d.PublicOnlyVsMappingPublic[0] != "public-only" {
		t.Fatalf("mapping/public differences were not exact: %#v", d)
	}
	if d.MappingUpstreamOnlyVsRaw[0] != "upstream" || d.RawOnlyVsMappingUpstream[0] != "raw-only" || d.RawOnlyVsPublic[0] != "raw-only" || d.PublicOnlyVsRaw[0] != "public-only" {
		t.Fatalf("raw/public/mapping differences were not exact: %#v", d)
	}
	if d.LiveOnlyVsPublic[0] != "live-only" || d.PublicOnlyVsLive[0] != "public-only" {
		t.Fatalf("live/public differences were not exact: %#v", d)
	}
	if len(d.DuplicateRawIDs) != 1 || d.DuplicateRawIDs[0] != "shared" || len(d.DuplicatePublicIDs) != 1 || d.DuplicatePublicIDs[0] != "shared" {
		t.Fatalf("exact duplicate IDs were not reported: %#v", d)
	}
	if d.Observation != "observations_only_no_mapping_change_proposed" {
		t.Fatalf("diagnostics were incorrectly framed as a mutation: %#v", d)
	}
}

func TestCatalogDiagnosticsSeparatesRetiredMappingFromPublishedSnapshot(t *testing.T) {
	// Synthetic logic case only: 404token account #22 was observed with the
	// retired Claude IDs in raw_models but not public_models. This test models a
	// separate account/state where a removed mapping ID remains in public_models.
	const oldID = "gpt-5.2-chat-latest"
	attempt := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)
	next := attempt.Add(time.Hour)
	snapshot := &adminapi.PublishedSnapshot{
		ContractStatus:        adminapi.PublishedSnapshotContractStatusSupported,
		Status:                "fresh",
		Source:                "upstream_models_endpoint",
		LastAttemptAt:         &attempt,
		NextRefreshAt:         &next,
		RawDigest:             "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		RawModels:             []string{oldID, "current-model"},
		PublicModels:          []string{oldID, "current-model"},
		PublicToUpstream:      map[string]string{oldID: oldID, "current-model": "current-model"},
		RawCount:              2,
		PublicCount:           2,
		PublicToUpstreamCount: 2,
	}
	d := catalogDiagnostics(map[string]string{"current-model": "current-model"}, snapshot, nil, false, attempt)
	if d.SnapshotPublicToUpstreamCount != 2 {
		t.Fatalf("snapshot public-to-upstream count missing: %#v", d)
	}
	if !reflect.DeepEqual(d.PublicOnlyVsMappingPublic, []string{oldID}) || len(d.MappingPublicOnlyVsPublic) != 0 {
		t.Fatalf("removed mapping versus residual public ID was not reported exactly: %#v", d)
	}
	if !reflect.DeepEqual(d.PublicOnlyVsRaw, []string{}) || !reflect.DeepEqual(d.MappingUpstreamOnlyVsRaw, []string{}) {
		t.Fatalf("upstream/public ID comparisons were conflated with public mapping keys: %#v", d)
	}
}

func TestCatalogDiagnosticsDoesNotTreatExpiredOrInvalidSnapshotAsPublished(t *testing.T) {
	for _, tc := range []struct {
		name           string
		contractStatus string
		status         string
		wantIDs        bool
	}{
		{name: "expired", contractStatus: adminapi.PublishedSnapshotContractStatusSupported, status: "expired", wantIDs: false},
		{name: "incomplete", contractStatus: adminapi.PublishedSnapshotContractStatusIncomplete, status: "stale", wantIDs: false},
		{name: "unsupported", contractStatus: adminapi.PublishedSnapshotContractStatusUnsupported, status: "fresh", wantIDs: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempt := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)
			next := attempt.Add(time.Hour)
			snapshot := &adminapi.PublishedSnapshot{ContractStatus: tc.contractStatus, Status: tc.status, Source: "upstream_models_endpoint", LastAttemptAt: &attempt, NextRefreshAt: &next, RawModels: []string{"raw-id"}, PublicModels: []string{"public-id"}, RawCount: 1, PublicCount: 1, RawDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000"}
			d := catalogDiagnostics(map[string]string{"mapping-id": "upstream-id"}, snapshot, nil, false, next.Add(time.Second))
			if d.SnapshotEffectivePublicCount != 0 {
				t.Fatalf("non-current/invalid snapshot IDs were used as publication evidence: %#v", d)
			}
			if tc.name == "expired" && (len(d.PublicOnlyVsMappingPublic) != 1 || d.PublicOnlyVsMappingPublic[0] != "public-id") {
				t.Fatalf("expired historical snapshot differences were not retained as labeled observations: %#v", d)
			}
			if tc.name == "expired" && (d.SnapshotRefreshOverdue == nil || !*d.SnapshotRefreshOverdue || d.SnapshotStatus != "expired") {
				t.Fatalf("schedule-due observation replaced or omitted native expired status: %#v", d)
			}
			if tc.contractStatus != adminapi.PublishedSnapshotContractStatusSupported && d.Observation != "snapshot_ids_withheld_due_to_unsupported_or_incomplete_contract" {
				t.Fatalf("invalid snapshot status did not fail closed: %#v", d)
			}
		})
	}
}

func TestCatalogDiagnosticsRefreshOverdueIsIndependentOfNativeStatus(t *testing.T) {
	lastAttempt := time.Date(2026, 9, 26, 20, 0, 2, 0, time.UTC)
	nextRefresh := time.Date(2026, 9, 27, 20, 0, 0, 0, time.UTC)
	reportAt := time.Date(2026, 10, 12, 4, 0, 0, 0, time.UTC)
	snapshot := &adminapi.PublishedSnapshot{
		ContractStatus: adminapi.PublishedSnapshotContractStatusSupported,
		Status:         "fresh",
		Source:         "upstream_models_endpoint",
		LastAttemptAt:  &lastAttempt,
		LastSuccessAt:  &lastAttempt,
		NextRefreshAt:  &nextRefresh,
		RawDigest:      "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		RawModels:      []string{"model"},
		PublicModels:   []string{"model"},
		PublicToUpstream: map[string]string{
			"model": "model",
		},
		RawCount: 1, PublicCount: 1, PublicToUpstreamCount: 1,
	}
	d := catalogDiagnostics(map[string]string{"model": "model"}, snapshot, nil, false, reportAt)
	if d.SnapshotStatus != "fresh" || d.SnapshotRefreshOverdue == nil || !*d.SnapshotRefreshOverdue || d.SnapshotEffectivePublicCount != 1 {
		t.Fatalf("fresh server status and overdue refresh schedule were not reported independently: %#v", d)
	}
	d = catalogDiagnostics(map[string]string{"model": "model"}, snapshot, nil, false, nextRefresh)
	if d.SnapshotStatus != "fresh" || d.SnapshotRefreshOverdue == nil || !*d.SnapshotRefreshOverdue {
		t.Fatalf("next_refresh_at equality should count as due without changing server status: %#v", d)
	}
	beforeDeadline := nextRefresh.Add(-time.Nanosecond)
	d = catalogDiagnostics(map[string]string{"model": "model"}, snapshot, nil, false, beforeDeadline)
	if d.SnapshotStatus != "fresh" || d.SnapshotRefreshOverdue == nil || *d.SnapshotRefreshOverdue {
		t.Fatalf("schedule was marked overdue before next_refresh_at: %#v", d)
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
