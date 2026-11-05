package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"sub2api-model-catalog-manager/internal/adminapi"
	"sub2api-model-catalog-manager/internal/app"
	"sub2api-model-catalog-manager/internal/catalog"
	"sub2api-model-catalog-manager/internal/config"
	"sub2api-model-catalog-manager/internal/inventorysummary"
	"sub2api-model-catalog-manager/internal/securecred"
)

func TestPlanLoaderBindsRawHashAndRejectsUnknownSecretFields(t *testing.T) {
	dir := t.TempDir()
	plan := validApprovalPlan()
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, digest, err := loadPlan(path)
	if err != nil || loaded.Accounts[0].AccountID != 9001 || digest == "" {
		t.Fatalf("valid plan rejected: %#v %q %v", loaded, digest, err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSuffix(string(data), "}")+`,"provider_api_key":"TOP-SECRET"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadPlan(path); err == nil {
		t.Fatal("plan with an unknown credential-like field was accepted")
	}
}

func TestOfficialReviewCommandIsReadOnlyAndUsesWhitelistInput(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "inspect.json")
	outputDir := filepath.Join(dir, "reviews")
	const sentinel = "OFFICIAL-REVIEW-SECRET-SENTINEL"
	input := `{"mode":"inspect","accounts":[{"account_id":9001,"platform":"openai","account_type":"apikey","api_protocol":"openai","source_fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_url":"https://user:` + sentinel + `@example.invalid/v1?token=` + sentinel + `#` + sentinel + `","admin_key":"` + sentinel + `","mappings":[{"public_id":"public-model","upstream_id":"upstream-model","headers":"` + sentinel + `"}]}]}`
	if err := os.WriteFile(inputPath, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	doer := &mainNeverDoer{}
	lookup := func(string) (string, bool) { return "", false }
	err := runOfficialReviewWith([]string{"--input", inputPath, "--out-dir", outputDir}, lookup, doer)
	if err != nil {
		t.Fatal(err)
	}
	if len(doer.requests) != 0 {
		t.Fatalf("missing official credentials must not create network requests: %d", len(doer.requests))
	}
	files, err := filepath.Glob(filepath.Join(outputDir, "official-review-*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("official-review output was not written once: %v %v", files, err)
	}
	body, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), sentinel) || strings.Contains(string(body), "example.invalid") || strings.Contains(string(body), "token=") || strings.Contains(string(body), "user:") {
		t.Fatalf("official-review report contains unapproved input fields: %s", body)
	}
	info, err := os.Stat(files[0])
	if err != nil {
		t.Fatal(err)
	}
	// Go's chmod does not set a Windows DACL; Windows deployments must keep the
	// output directory ACL private. On POSIX, verify owner-only output mode.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatalf("report permissions are broader than owner-only: %v", info.Mode().Perm())
	}
	if err := runOfficialReviewWith([]string{"--input", inputPath, "--out-dir", outputDir, "--base-url", "https://attacker.invalid"}, lookup, doer); err == nil {
		t.Fatal("official-review accepted a caller-supplied endpoint")
	}
	if len(doer.requests) != 0 {
		t.Fatal("caller-supplied endpoint option caused a request")
	}
}

func TestInventorySummaryCommandReadsOnlyPairedLocalReports(t *testing.T) {
	dir := t.TempDir()
	inspectPath := filepath.Join(dir, "inspect.json")
	discoverPath := filepath.Join(dir, "discover.json")
	outDir := filepath.Join(dir, "summary")
	hash, err := catalog.AccountIDsDigest([]int64{9001, 9002})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := strings.Repeat("a", 64)
	inspect := `{"mode":"inspect","created_at":"2026-10-12T11:00:00Z","account_scope":"all","account_scan_status":"complete","account_inventory_count":2,"account_inventory_sha256":"` + hash + `","target_build_fingerprint":"build-test","accounts":[{"account_id":9001,"platform":"openai","account_type":"apikey","api_protocol":"openai","source_fingerprint":"` + fingerprint + `","base_url":"https://SECRET-SENTINEL.invalid/v1?token=SECRET-SENTINEL","mappings":[{"public_id":"gpt","upstream_id":"gpt-a"}]},{"account_id":9002,"platform":"anthropic","account_type":"apikey","api_protocol":"anthropic","source_fingerprint":"` + strings.Repeat("b", 64) + `","mappings":[{"public_id":"gpt","upstream_id":"gpt-b"}]}]}`
	discover := `{"mode":"discover","created_at":"2026-10-12T11:05:00Z","account_scope":"all","account_scan_status":"complete","account_inventory_count":2,"account_inventory_sha256":"` + hash + `","target_build_fingerprint":"build-test","discovery":[{"account_id":9001,"source_fingerprint":"` + fingerprint + `","status":"candidate_reported","live_list_available":true,"models":["shared","only-9001"]},{"account_id":9002,"source_fingerprint":"` + strings.Repeat("b", 64) + `","status":"candidate_reported","live_list_available":true,"models":["shared"]}]}`
	if err := os.WriteFile(inspectPath, []byte(inspect), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(discoverPath, []byte(discover), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runInventorySummary([]string{"--inspect", inspectPath, "--discover", discoverPath, "--out-dir", outDir}); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(outDir, "inventory-summary-*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("summary report was not written once: %v %v", files, err)
	}
	body, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "SECRET-SENTINEL") || strings.Contains(string(body), "base_url") || strings.Contains(string(body), "secret.invalid") {
		t.Fatal("summary report contains unapproved input data")
	}
	var report inventorysummary.Report
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if report.Counts.Accounts != 2 || len(report.PublicIDRoutes) != 1 || len(report.DiscoveredIDReuse) != 1 || report.CatalogCoverage != "all_accounts_returned_live_lists" {
		t.Fatalf("unexpected summary result: %#v", report)
	}
}

func TestInventorySummaryRejectsPartialInputBeforePublishing(t *testing.T) {
	dir := t.TempDir()
	inspectPath := filepath.Join(dir, "inspect.json")
	discoverPath := filepath.Join(dir, "discover.json")
	outDir := filepath.Join(dir, "summary")
	hash, _ := catalog.AccountIDsDigest([]int64{9001})
	inspect := `{"mode":"inspect","created_at":"2026-10-12T11:00:00Z","account_scope":"all","account_scan_status":"partial","account_inventory_count":1,"account_inventory_sha256":"` + hash + `","target_build_fingerprint":"build-test","accounts":[{"account_id":9001,"platform":"openai","account_type":"apikey","source_fingerprint":"` + strings.Repeat("a", 64) + `","mappings":[]}]}`
	discover := `{"mode":"discover","created_at":"2026-10-12T11:05:00Z","account_scope":"all","account_scan_status":"complete","account_inventory_count":1,"account_inventory_sha256":"` + hash + `","target_build_fingerprint":"build-test","discovery":[{"account_id":9001,"source_fingerprint":"` + strings.Repeat("a", 64) + `","status":"skipped","live_list_available":false,"models":[]}]}`
	if err := os.WriteFile(inspectPath, []byte(inspect), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(discoverPath, []byte(discover), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runInventorySummary([]string{"--inspect", inspectPath, "--discover", discoverPath, "--out-dir", outDir}); err == nil {
		t.Fatal("partial all-account inspect was accepted")
	}
	files, err := filepath.Glob(filepath.Join(outDir, "inventory-summary-*.json"))
	if err != nil || len(files) != 0 {
		t.Fatalf("partial input left a summary report: %v %v", files, err)
	}
}

type mainNeverDoer struct{ requests []*http.Request }

func (d *mainNeverDoer) Do(request *http.Request) (*http.Response, error) {
	d.requests = append(d.requests, request)
	return nil, errors.New("network access was not expected")
}

func TestPlanLoaderRejectsOversizeAndTrailingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1} {}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadPlan(path); err == nil {
		t.Fatal("trailing JSON value was accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", maxPlanBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadPlan(path); err == nil {
		t.Fatal("oversized plan was accepted")
	}
}

func TestPlanLoaderRejectsExpiredApproval(t *testing.T) {
	plan := validApprovalPlan()
	plan.CreatedAt = time.Now().UTC().Add(-time.Hour)
	plan.ExpiresAt = plan.CreatedAt.Add(-time.Second)
	body, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "expired-plan.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadPlan(path); err == nil {
		t.Fatal("expired plan was accepted")
	}
}

func TestManualApplyRawPlanHashMismatchStopsBeforeCredentialRead(t *testing.T) {
	configPath, _, _ := automaticFixture(t, strings.Repeat("0", 64))
	plan := validApprovalPlan()
	planBytes, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(planPath, planBytes, 0600); err != nil {
		t.Fatal(err)
	}
	err = runApply([]string{"--config", configPath, "--plan", planPath, "--confirm-plan-sha256", strings.Repeat("0", 64)})
	if err == nil || !strings.Contains(err.Error(), "does not match the plan file raw bytes") {
		t.Fatalf("raw plan hash mismatch was not rejected before apply: %v", err)
	}
}

func TestDiscoverAndRunFailClosedWithoutRulesOrSideEffectApproval(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	configBody := `{"target":{"base_url":"https://404token.xyz","runtime_compatibility":{"build_id":"verified-image","api_contract_id":"admin-catalog-v1","evidence":"target-verification","reviewed_at":"2026-10-11"}},"accounts":[{"id":9001,"accept_credential_response_memory_exposure":true}],"allow_native_sync_metadata_write":false,"apply_mode":"approved_rules","approved_rules_sha256":"` + strings.Repeat("a", 64) + `","credential":{"windows_credential_target":"test"},"state_dir":"state","report_dir":"reports","backup_dir":"backups","rules_file":"rules.json"}`
	if err := os.WriteFile(configPath, []byte(configBody), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runDiscover([]string{"--config", configPath}); err == nil || !strings.Contains(err.Error(), "native sync metadata side effect is disabled") {
		t.Fatalf("discovery did not fail closed: %v", err)
	}
	if err := runAutomatic([]string{"--config", configPath}); err == nil || !strings.Contains(err.Error(), "native sync metadata side effect is disabled") {
		t.Fatalf("automatic run did not fail closed: %v", err)
	}
}

func TestManualDiscoverRequiresAllAccountPreview(t *testing.T) {
	configPath, _, _ := automaticFixture(t, "")
	credentialCalls, clientCalls := 0, 0
	err := runDiscoverWith([]string{"--config", configPath, "--manual-sync-authorized"}, func(config.CredentialConfig) (securecred.Value, error) {
		credentialCalls++
		return securecred.Value{APIKey: []byte("test-admin-key")}, nil
	}, func(string) (app.AdminAPI, error) {
		clientCalls++
		return nil, errors.New("client should not be created")
	})
	if err == nil || !strings.Contains(err.Error(), "requires account_scope=all and apply_mode=preview") || credentialCalls != 0 || clientCalls != 0 {
		t.Fatalf("manual discover did not reject allowlist scope before credential/API access: err=%v credentials=%d clients=%d", err, credentialCalls, clientCalls)
	}
}

func TestAutomaticRunRulesHashMismatchPreviewsWithoutWrite(t *testing.T) {
	configPath, api, rawRulesHash := automaticFixture(t, strings.Repeat("0", 64))
	if err := runAutomaticWith([]string{"--config", configPath}, mockCredentialReader, func(string) (app.AdminAPI, error) { return api, nil }); err != nil {
		t.Fatal(err)
	}
	if api.bulkCalls != 0 || api.syncCalls != 1 {
		t.Fatalf("hash mismatch did not preview only: bulk=%d sync=%d current_rules_sha=%s", api.bulkCalls, api.syncCalls, rawRulesHash)
	}
}

func TestAutomaticRunMatchingRawRulesHashAppliesOnlyReviewedDelta(t *testing.T) {
	configPath, api, rawRulesHash := automaticFixture(t, "")
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.ApplyMode = "approved_rules"
	cfg.ApprovedRulesSHA256 = rawRulesHash
	configBytes, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runAutomaticWith([]string{"--config", configPath}, mockCredentialReader, func(string) (app.AdminAPI, error) { return api, nil }); err != nil {
		t.Fatal(err)
	}
	if api.bulkCalls != 1 || api.lastBulkID != 9001 || api.lastMapping["canonical-name"] != "upstream-known" || api.lastMapping["manual"] != "keep" {
		t.Fatalf("approved run did not apply exactly the reviewed mapping: bulk=%d id=%d mapping=%v", api.bulkCalls, api.lastBulkID, api.lastMapping)
	}
}

func automaticFixture(t *testing.T, approvedHash string) (string, *mainWorkflowAPI, string) {
	t.Helper()
	dir := t.TempDir()
	now := time.Now()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	date := now.In(loc).Format("2006-01-02")
	account := adminapi.Account{ID: 9001, Platform: "openai", Type: "apikey", Protocol: "openai", BaseURL: "https://provider.example/v1", Status: "active", ModelMapping: map[string]string{"manual": "keep"}, APIKeyPresent: mainBoolPointer(true)}
	source, err := catalog.SourceForAccount(account.Platform, account.Type, account.Protocol, account.BaseURL, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	rules := catalog.Rules{
		SchemaVersion:   1,
		Aliases:         []catalog.AliasRule{{Source: source, UpstreamID: "upstream-known", PublicID: "canonical-name", Decision: "approved", Evidence: "reviewed exact mock rule", ReviewedAt: date}},
		SourceProfiles:  []catalog.SourceProfile{{Source: source, BuildID: "verified-image", APIContractID: "admin-catalog-v1", Decision: "approved", IdentityAddPolicy: "add_exact", Evidence: "mock-only build/API scope", ReviewedAt: date}},
		VersionFamilies: []catalog.VersionFamily{}, RetiredModels: []catalog.RetiredRule{}, CompleteCatalogProfiles: []catalog.CompleteCatalogProfile{},
	}
	rulesBytes, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	rulesPath := filepath.Join(dir, "rules.json")
	if err := os.WriteFile(rulesPath, rulesBytes, 0600); err != nil {
		t.Fatal(err)
	}
	rulesSum := sha256.Sum256(rulesBytes)
	rulesHash := hex.EncodeToString(rulesSum[:])
	if approvedHash == "" {
		approvedHash = rulesHash
	}
	cfg := config.Config{
		Target:                       config.TargetConfig{BaseURL: "https://404token.xyz", RuntimeCompatibility: config.RuntimeCompatibility{BuildID: "verified-image", APIContractID: "admin-catalog-v1", Evidence: "mock-only test fixture", ReviewedAt: date}},
		Accounts:                     []config.AccountConfig{{ID: 9001, AcceptCredentialResponseMemoryExposure: mainBoolPointer(true)}},
		AllowNativeSyncMetadataWrite: true, ApplyMode: "approved_rules", ApprovedRulesSHA256: approvedHash,
		Credential: config.CredentialConfig{WindowsCredentialTarget: "mock-only"}, StateDir: filepath.Join(dir, "state"), ReportDir: filepath.Join(dir, "reports"), BackupDir: filepath.Join(dir, "backups"), RulesFile: rulesPath,
	}
	configBytes, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, configBytes, 0600); err != nil {
		t.Fatal(err)
	}
	api := &mainWorkflowAPI{
		account: account,
		status:  adminapi.RefreshStatus{Enabled: true, Timezone: "Asia/Shanghai", RunStatus: "completed", LastFinishedAt: timePtrMain(now), Accounts: []adminapi.AccountRunStatus{{AccountID: 9001, Status: "completed"}}},
		catalog: adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true},
	}
	return configPath, api, rulesHash
}

func mockCredentialReader(config.CredentialConfig) (securecred.Value, error) {
	return securecred.Value{APIKey: []byte("test-admin-key")}, nil
}

type mainWorkflowAPI struct {
	account     adminapi.Account
	status      adminapi.RefreshStatus
	catalog     adminapi.Catalog
	syncCalls   int
	bulkCalls   int
	lastBulkID  int64
	lastMapping map[string]string
}

func (m *mainWorkflowAPI) SetAPIKey([]byte) error { return nil }
func (m *mainWorkflowAPI) RefreshStatus(context.Context) (adminapi.RefreshStatus, error) {
	return m.status, nil
}
func (m *mainWorkflowAPI) ListAllAccounts(context.Context) ([]adminapi.AccountSummary, error) {
	return []adminapi.AccountSummary{{ID: m.account.ID, Platform: m.account.Platform, Type: m.account.Type, Status: m.account.Status, ParentAccountID: m.account.ParentAccountID}}, nil
}
func (m *mainWorkflowAPI) Account(_ context.Context, id int64) (adminapi.Account, error) {
	if id != m.account.ID {
		return adminapi.Account{}, errors.New("unexpected mock account id")
	}
	copy := m.account
	copy.ModelMapping = make(map[string]string, len(m.account.ModelMapping))
	for key, value := range m.account.ModelMapping {
		copy.ModelMapping[key] = value
	}
	return copy, nil
}
func (m *mainWorkflowAPI) SyncUpstream(_ context.Context, id int64) (adminapi.Catalog, error) {
	if id != m.account.ID {
		return adminapi.Catalog{}, errors.New("unexpected sync id")
	}
	m.syncCalls++
	return m.catalog, nil
}
func (m *mainWorkflowAPI) BulkUpdateMapping(_ context.Context, id int64, mapping map[string]string) (adminapi.BulkUpdateResult, error) {
	m.bulkCalls++
	m.lastBulkID = id
	m.lastMapping = map[string]string{}
	for key, value := range mapping {
		m.lastMapping[key] = value
	}
	m.account.ModelMapping = m.lastMapping
	return adminapi.BulkUpdateResult{SuccessIDs: []int64{id}, FailedIDs: []int64{}}, nil
}
func (m *mainWorkflowAPI) Close() {}

func mainBoolPointer(v bool) *bool       { return &v }
func timePtrMain(v time.Time) *time.Time { return &v }

func validApprovalPlan() catalog.ApprovalPlan {
	now := time.Now().UTC()
	source := catalog.Source{Platform: "openai", AccountType: "apikey", APIProtocol: "openai", BaseURL: "https://provider.example/v1", AccountID: 9001}
	models := []string{"raw-v2"}
	directoryHash, _ := catalog.DirectoryDigest(models, true, nil)
	return catalog.ApprovalPlan{
		SchemaVersion: catalog.ApprovalPlanSchemaVersion,
		CreatedAt:     now,
		ExpiresAt:     now.Add(catalog.ApprovalPlanTTL),
		Target:        catalog.TargetBinding{Origin: "https://404token.xyz", BuildID: "verified-image", APIContractID: "admin-catalog-v1"},
		AccountScope:  "allowlist",
		RulesSHA256:   strings.Repeat("a", 64),
		StateSHA256:   strings.Repeat("b", 64),
		Accounts: []catalog.Plan{{
			AccountID: 9001, Source: source, SourceFingerprint: catalog.SourceFingerprint(source),
			DirectoryHash: directoryHash, CatalogObservedAt: now, RulesHash: strings.Repeat("c", 64),
			Completeness: catalog.CompletenessUnknown, LiveListAvailable: true,
			AbsenceCountsBefore: map[string]int{}, AbsenceCountsAfter: map[string]int{},
			Before: map[string]string{"manual": "raw"}, After: map[string]string{"manual": "raw", "public": "raw-v2"},
			Changes:   []catalog.Change{{Action: "add", PublicID: "public", After: "raw-v2", RuleKind: "alias", Reason: "reviewed exact rule"}},
			Conflicts: []string{}, UnreviewedModels: []string{}, CatalogModels: models, CatalogWarningCodes: []string{}, ApplyEligible: true,
		}},
	}
}
