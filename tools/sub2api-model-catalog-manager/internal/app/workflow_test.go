package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"sub2api-model-catalog-manager/internal/adminapi"
	"sub2api-model-catalog-manager/internal/catalog"
	"sub2api-model-catalog-manager/internal/config"
	"sub2api-model-catalog-manager/internal/report"
	"sub2api-model-catalog-manager/internal/securecred"
	"sub2api-model-catalog-manager/internal/state"
)

func TestDiscoveryGatesBeforeCredentialOrAPI(t *testing.T) {
	t.Run("privacy gate", func(t *testing.T) {
		cfg, _, _ := testWorkflowConfig(t, false, true)
		credentialCalls, clientCalls := 0, 0
		_, err := (Discoverer{
			ReadCredential: func(config.CredentialConfig) (securecred.Value, error) {
				credentialCalls++
				return securecred.Value{}, nil
			},
			NewClient: func(string) (AdminAPI, error) { clientCalls++; return &workflowAPI{}, nil },
		}).Discover(context.Background(), cfg)
		if err == nil || credentialCalls != 0 || clientCalls != 0 {
			t.Fatalf("privacy gate did not stop before access: err=%v credentials=%d clients=%d", err, credentialCalls, clientCalls)
		}
	})

	t.Run("native sync side effect gate", func(t *testing.T) {
		cfg, _, _ := testWorkflowConfig(t, true, false)
		credentialCalls, clientCalls := 0, 0
		_, err := (Discoverer{
			ReadCredential: func(config.CredentialConfig) (securecred.Value, error) {
				credentialCalls++
				return securecred.Value{}, nil
			},
			NewClient: func(string) (AdminAPI, error) { clientCalls++; return &workflowAPI{}, nil },
		}).Discover(context.Background(), cfg)
		if err == nil || credentialCalls != 0 || clientCalls != 0 {
			t.Fatalf("side-effect gate did not stop before access: err=%v credentials=%d clients=%d", err, credentialCalls, clientCalls)
		}
	})
}

func TestDiscoveryRequiresExactSourceBuildAndAPIMatch(t *testing.T) {
	cfg, account, now := testWorkflowConfig(t, true, true)
	rules := workflowRules(account)
	rules.SourceProfiles[0].BuildID = "different-build"
	writeWorkflowRules(t, cfg.RulesFile, rules)
	api := newWorkflowAPI(account, now, adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true})
	result, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if api.syncCalls != 0 || result.Plan != nil {
		t.Fatalf("mismatched profile reached sync or produced a plan: sync=%d plan=%#v", api.syncCalls, result.Plan)
	}
	if !hasCall(api.calls, "account:7") || hasCall(api.calls, "sync:7") {
		t.Fatalf("unexpected mocked API calls: %v", api.calls)
	}
}

func TestAllAccountDiscoveryCoversInventoryAndApplyChecksSnapshot(t *testing.T) {
	cfg, account, now := testWorkflowConfig(t, true, true)
	cfg.AccountScope = "all"
	cfg.Target.RuntimeCompatibility.APIContractID = config.APIContractAllAccountsV1
	cfg.AcceptAllCredentialResponseMemoryExposure = true
	cfg.Accounts = nil
	rules := workflowRules(account)
	rules.SourceProfiles[0].APIContractID = config.APIContractAllAccountsV1
	writeWorkflowRules(t, cfg.RulesFile, rules)
	unsupported := adminapi.Account{ID: 8, Platform: "openai", Type: "apikey", Protocol: "openai", BaseURL: "https://unreviewed.example/v1", Status: "active", ModelMapping: map[string]string{"manual": "preserve"}, APIKeyPresent: boolPointer(true)}
	shadowParent := account.ID
	shadow := adminapi.Account{ID: 9, Platform: account.Platform, Type: account.Type, Protocol: account.Protocol, BaseURL: account.BaseURL, Status: "active", ParentAccountID: &shadowParent, ModelMapping: map[string]string{}, APIKeyPresent: boolPointer(true)}
	api := newWorkflowAPI(account, now, adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true})
	api.accounts[unsupported.ID] = unsupported
	api.accounts[shadow.ID] = shadow
	api.status.Accounts = append(api.status.Accounts, adminapi.AccountRunStatus{AccountID: unsupported.ID, Status: "completed"}, adminapi.AccountRunStatus{AccountID: shadow.ID, Status: "completed"})
	discovery, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
	if err != nil || discovery.Plan == nil {
		t.Fatalf("all-account discovery failed: plan=%#v err=%v", discovery.Plan, err)
	}
	if discovery.Plan.AccountScope != "all" || discovery.Plan.AccountInventoryCount != 3 || len(discovery.Report.Discovery) != 3 || len(discovery.Plan.Accounts) != 1 {
		t.Fatalf("unexpected all-account coverage/plan: scope=%q inventory=%d reports=%d plans=%d", discovery.Plan.AccountScope, discovery.Plan.AccountInventoryCount, len(discovery.Report.Discovery), len(discovery.Plan.Accounts))
	}
	if !hasCall(api.calls, "list_accounts") || !hasCall(api.calls, "sync:7") || hasCall(api.calls, "sync:8") || hasCall(api.calls, "sync:9") {
		t.Fatalf("unsupported/shadow accounts were synced: %v", api.calls)
	}
	statuses := map[int64]string{}
	for _, row := range discovery.Report.Discovery {
		statuses[row.ID] = row.Status
	}
	if statuses[8] != "skipped" || statuses[9] != "skipped_shadow" {
		t.Fatalf("unsupported/shadow accounts were not reported: %v", statuses)
	}

	apply, err := workflowApplier(api, now).Apply(context.Background(), cfg, *discovery.Plan, discovery.PlanSHA256, discovery.PlanSHA256, false)
	if err != nil || !reflect.DeepEqual(apply.AppliedIDs, []int64{account.ID}) {
		t.Fatalf("all-account mapping apply failed: result=%#v err=%v", apply, err)
	}

	cfg2, account2, now2 := testWorkflowConfig(t, true, true)
	cfg2.AccountScope = "all"
	cfg2.Target.RuntimeCompatibility.APIContractID = config.APIContractAllAccountsV1
	cfg2.AcceptAllCredentialResponseMemoryExposure = true
	cfg2.Accounts = nil
	rules2 := workflowRules(account2)
	rules2.SourceProfiles[0].APIContractID = config.APIContractAllAccountsV1
	writeWorkflowRules(t, cfg2.RulesFile, rules2)
	api2 := newWorkflowAPI(account2, now2, adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true})
	discovery2, err := workflowDiscoverer(api2, now2).Discover(context.Background(), cfg2)
	if err != nil || discovery2.Plan == nil {
		t.Fatalf("second inventory discovery failed: %v", err)
	}
	api2.accounts[88] = adminapi.Account{ID: 88, Platform: "other", Type: "apikey", BaseURL: "https://new.example", ModelMapping: map[string]string{}, APIKeyPresent: boolPointer(true)}
	result, err := workflowApplier(api2, now2).Apply(context.Background(), cfg2, *discovery2.Plan, discovery2.PlanSHA256, discovery2.PlanSHA256, false)
	if err == nil || len(result.AppliedIDs) != 0 || len(api2.bulkCalls) != 0 {
		t.Fatalf("inventory drift did not block all writes: result=%#v err=%v writes=%v", result, err, api2.bulkCalls)
	}
}

func TestDiscoverySkipsUnrestrictedMappingsBeforeNativeSync(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mapping map[string]string
	}{
		{name: "absent"},
		{name: "explicit empty", mapping: map[string]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, account, now := testWorkflowConfig(t, true, true)
			account.ModelMapping = tc.mapping
			api := newWorkflowAPI(account, now, adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true})
			result, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if result.Plan != nil || api.syncCalls != 0 || len(api.bulkCalls) != 0 {
				t.Fatalf("unrestricted mapping was planned or synchronized: plan=%#v sync=%d writes=%v", result.Plan, api.syncCalls, api.bulkCalls)
			}
			if len(result.Report.Discovery) != 1 || result.Report.Discovery[0].Status != "skipped_unrestricted_mapping" {
				t.Fatalf("unrestricted mapping was not clearly reported: %#v", result.Report.Discovery)
			}
		})
	}
}

func TestDiscoveryReportsSnapshotLiveAndMappingDifferencesWithoutChangingMappings(t *testing.T) {
	cfg, account, now := testWorkflowConfig(t, true, true)
	rules, err := catalog.LoadRules(cfg.RulesFile)
	if err != nil {
		t.Fatal(err)
	}
	rules.SourceProfiles[0].IdentityAddPolicy = "report_only"
	writeWorkflowRules(t, cfg.RulesFile, rules)
	account.PublishedSnapshot = &adminapi.PublishedSnapshot{
		ContractStatus: adminapi.PublishedSnapshotContractStatusSupported,
		Status:         "stale",
		Source:         "upstream_models_endpoint",
		LastAttemptAt:  timePtr(now),
		NextRefreshAt:  timePtr(now.Add(time.Hour)),
		RawDigest:      "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		RawModels:      []string{"gpt-5.2-chat-latest", "gpt-4o-audio-preview"},
		PublicModels:   []string{"gpt-5.2-chat-latest", "gpt-4o-audio-preview"},
		PublicToUpstream: map[string]string{
			"gpt-5.2-chat-latest":  "gpt-5.2-chat-latest",
			"gpt-4o-audio-preview": "gpt-4o-audio-preview",
		},
		RawCount: 2, PublicCount: 2, PublicToUpstreamCount: 2,
	}
	api := newWorkflowAPI(account, now, adminapi.Catalog{
		Models:            []string{"gpt-5.2-chat-latest", "gpt-4o-audio-preview", "gpt-4o-realtime-preview"},
		LiveListAvailable: true,
	})
	result, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.Plan == nil || len(result.Plan.Accounts) != 1 {
		t.Fatalf("expected a preview plan for the exact account: %#v", result.Plan)
	}
	if len(result.Plan.Accounts[0].Changes) != 0 || len(result.Report.Discovery) != 1 || len(result.Report.Discovery[0].Changes) != 0 {
		t.Fatalf("catalog diagnostics unexpectedly created mapping changes: plan=%#v report=%#v", result.Plan.Accounts[0].Changes, result.Report.Discovery)
	}
	d := result.Report.Discovery[0].CatalogDiagnostics
	if d == nil || d.SnapshotContractStatus != adminapi.PublishedSnapshotContractStatusSupported || !d.HasLiveCatalog {
		t.Fatalf("snapshot/live catalog comparison missing: %#v", d)
	}
	if !reflect.DeepEqual(d.PublicOnlyVsMappingPublic, []string{"gpt-4o-audio-preview", "gpt-5.2-chat-latest"}) || !reflect.DeepEqual(d.LiveOnlyVsPublic, []string{"gpt-4o-realtime-preview"}) {
		t.Fatalf("exact snapshot/live/mapping differences are incorrect: %#v", d)
	}
	if api.syncCalls != 1 || len(api.bulkCalls) != 0 {
		t.Fatalf("diagnostic preview unexpectedly wrote account mappings: calls=%v writes=%v", api.calls, api.bulkCalls)
	}
}

func TestSnapshotSentinelsDoNotEscapeInspectOrDiscoverReports(t *testing.T) {
	cfg, account, now := testWorkflowConfig(t, true, true)
	account.PublishedSnapshot = &adminapi.PublishedSnapshot{
		ContractStatus:        adminapi.PublishedSnapshotContractStatusSupported,
		Status:                "SNAPSHOT_STATUS_SENTINEL_MUST_NOT_ESCAPE",
		Source:                "SNAPSHOT_SOURCE_SENTINEL_MUST_NOT_ESCAPE",
		RawDigest:             "SNAPSHOT_DIGEST_SENTINEL_MUST_NOT_ESCAPE",
		LastAttemptAt:         timePtr(now),
		NextRefreshAt:         timePtr(now.Add(time.Hour)),
		RawModels:             []string{"SNAPSHOT_MODEL_SENTINEL_MUST_NOT_ESCAPE"},
		PublicModels:          []string{"SNAPSHOT_MODEL_SENTINEL_MUST_NOT_ESCAPE"},
		PublicToUpstream:      map[string]string{"SNAPSHOT_MODEL_SENTINEL_MUST_NOT_ESCAPE": "SNAPSHOT_MODEL_SENTINEL_MUST_NOT_ESCAPE"},
		RawCount:              1,
		PublicCount:           1,
		PublicToUpstreamCount: 1,
	}
	api := newWorkflowAPI(account, now, adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true})
	newClient := func(string) (AdminAPI, error) { return api, nil }
	inspectDoc, err := (Inspector{ReadCredential: workflowCredential, NewClient: newClient}).Inspect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("inspect failed: %v", err)
	}
	inspectDir := filepath.Join(t.TempDir(), "inspect")
	inspectPath, err := report.Write(inspectDir, inspectDoc)
	if err != nil {
		t.Fatalf("write inspect report: %v", err)
	}
	inspectBody, err := os.ReadFile(inspectPath)
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	discoverDir := filepath.Join(t.TempDir(), "discover")
	discoverPath, err := report.Write(discoverDir, discovery.Report)
	if err != nil {
		t.Fatalf("write discover report: %v", err)
	}
	discoverBody, err := os.ReadFile(discoverPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, sentinel := range []string{
		"SNAPSHOT_STATUS_SENTINEL_MUST_NOT_ESCAPE",
		"SNAPSHOT_SOURCE_SENTINEL_MUST_NOT_ESCAPE",
		"SNAPSHOT_DIGEST_SENTINEL_MUST_NOT_ESCAPE",
		"SNAPSHOT_MODEL_SENTINEL_MUST_NOT_ESCAPE",
	} {
		if strings.Contains(string(inspectBody), sentinel) || strings.Contains(string(discoverBody), sentinel) {
			t.Fatalf("snapshot sentinel escaped inspect/discover report: %s", sentinel)
		}
	}
}

func TestSameAccountSourceDetectsMappingPresenceDrift(t *testing.T) {
	base := adminapi.Account{ID: 7, Platform: "openai", Type: "apikey", Protocol: "openai", BaseURL: "https://provider.example/v1", Status: "active"}
	explicitEmpty := base
	explicitEmpty.ModelMapping = map[string]string{}
	if sameAccountSource(base, explicitEmpty) || sameAccountSource(explicitEmpty, base) {
		t.Fatal("mapping presence drift was treated as an unchanged account source")
	}
}

func TestApplyBlocksAbsentOrEmptyMappingBeforeWrite(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mapping map[string]string
	}{
		{name: "absent"},
		{name: "explicit empty", mapping: map[string]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, account, now := testWorkflowConfig(t, true, true)
			api := newWorkflowAPI(account, now, adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true})
			discovery, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
			if err != nil || discovery.Plan == nil {
				t.Fatalf("fixture discovery failed: plan=%#v err=%v", discovery.Plan, err)
			}
			current := api.accounts[account.ID]
			current.ModelMapping = tc.mapping
			api.accounts[account.ID] = current
			result, err := workflowApplier(api, now).Apply(context.Background(), cfg, *discovery.Plan, discovery.PlanSHA256, discovery.PlanSHA256, false)
			if err == nil || len(result.AppliedIDs) != 0 || len(api.bulkCalls) != 0 {
				t.Fatalf("apply did not reject absent/empty mapping before write: result=%#v err=%v writes=%v", result, err, api.bulkCalls)
			}
		})
	}
}

func TestApplyRejectsPlanWithEmptyResultMappingBeforeAPI(t *testing.T) {
	cfg, account, now := testWorkflowConfig(t, true, true)
	api := newWorkflowAPI(account, now, adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true})
	discovery, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
	if err != nil || discovery.Plan == nil {
		t.Fatalf("fixture discovery failed: plan=%#v err=%v", discovery.Plan, err)
	}
	plan := *discovery.Plan
	plan.Accounts = append([]catalog.Plan(nil), discovery.Plan.Accounts...)
	plan.Accounts[0].After = map[string]string{}
	apiStart := len(api.calls)
	result, err := workflowApplier(api, now).Apply(context.Background(), cfg, plan, discovery.PlanSHA256, discovery.PlanSHA256, false)
	if err == nil || !strings.Contains(err.Error(), "resulting model mapping is empty") || len(result.AppliedIDs) != 0 || len(api.bulkCalls) != 0 {
		t.Fatalf("empty resulting mapping was not rejected before write: result=%#v err=%v writes=%v", result, err, api.bulkCalls)
	}
	if len(api.calls) != apiStart {
		t.Fatalf("empty mapping plan contacted the API before rejection: %v", api.calls[apiStart:])
	}
}

func TestApplyRejectsInactiveAccountBeforeMappingWrite(t *testing.T) {
	cfg, account, now := testWorkflowConfig(t, true, true)
	api := newWorkflowAPI(account, now, adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true})
	discovery, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
	if err != nil || discovery.Plan == nil {
		t.Fatalf("fixture discovery failed: plan=%#v err=%v", discovery.Plan, err)
	}
	inactive := api.accounts[account.ID]
	inactive.Status = "disabled"
	api.accounts[account.ID] = inactive
	result, err := workflowApplier(api, now).Apply(context.Background(), cfg, *discovery.Plan, discovery.PlanSHA256, discovery.PlanSHA256, false)
	if err == nil || !strings.Contains(err.Error(), "account is not active") || len(result.AppliedIDs) != 0 || len(api.bulkCalls) != 0 {
		t.Fatalf("inactive account was not blocked before write: result=%#v err=%v writes=%v", result, err, api.bulkCalls)
	}
}

func TestAllAccountDiscoveryReportsReadFailureAndBlocksPartialPlanAndState(t *testing.T) {
	cfg, account, now := testWorkflowConfig(t, true, true)
	cfg.AccountScope = "all"
	cfg.Target.RuntimeCompatibility.APIContractID = config.APIContractAllAccountsV1
	cfg.AcceptAllCredentialResponseMemoryExposure = true
	cfg.Accounts = nil
	rules := workflowRules(account)
	rules.SourceProfiles[0].APIContractID = config.APIContractAllAccountsV1
	writeWorkflowRules(t, cfg.RulesFile, rules)
	shadowParent := account.ID
	shadow := adminapi.Account{ID: 9, Platform: account.Platform, Type: account.Type, Protocol: account.Protocol, BaseURL: account.BaseURL, Status: "active", ParentAccountID: &shadowParent, ModelMapping: map[string]string{}, APIKeyPresent: boolPointer(true)}
	api := newWorkflowAPI(account, now, adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true})
	api.accounts[shadow.ID] = shadow
	api.inventoryIDs = []int64{account.ID, 8, shadow.ID}
	discovery, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if discovery.Plan != nil || discovery.Report.AccountScanStatus != "partial" || len(discovery.Report.Discovery) != 3 {
		t.Fatalf("partial all-account discovery must report every ID and withhold its plan: plan=%#v report=%#v", discovery.Plan, discovery.Report)
	}
	statuses := map[int64]string{}
	for _, row := range discovery.Report.Discovery {
		statuses[row.ID] = row.Status
	}
	if statuses[account.ID] != "candidate_reported" || statuses[8] != "account_read_failed" || statuses[shadow.ID] != "skipped_shadow" {
		t.Fatalf("unexpected all-account discovery statuses: %v", statuses)
	}
	if !hasCall(api.calls, "account:9") || !hasCall(api.calls, "sync:7") {
		t.Fatalf("discovery did not continue after one account detail failure: %v", api.calls)
	}
	if _, err := os.Stat(filepath.Join(cfg.StateDir, "state.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial inventory scan persisted local catalog state: err=%v", err)
	}
}

func TestApplyBlocksUnknownCatalogWarning(t *testing.T) {
	cfg, account, now := testWorkflowConfig(t, true, true)
	api := newWorkflowAPI(account, now, adminapi.Catalog{
		Models: []string{"upstream-known"}, LiveListAvailable: true,
		Warnings: []adminapi.CatalogWarning{{Code: "partial_page", Message: "opaque catalog warning"}},
	})
	discovery, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
	if err != nil || discovery.Plan == nil {
		t.Fatalf("discovery failed: %#v %v", discovery.Plan, err)
	}
	if discovery.Plan.Accounts[0].ApplyEligible {
		t.Fatal("unknown catalog warning produced an apply-eligible plan")
	}
	apiStart := len(api.calls)
	result, err := workflowApplier(api, now).Apply(context.Background(), cfg, *discovery.Plan, discovery.PlanSHA256, discovery.PlanSHA256, false)
	if err == nil || len(result.AppliedIDs) != 0 || len(api.bulkCalls) != 0 {
		t.Fatalf("unknown warning was not fail-closed: result=%#v err=%v bulk=%v", result, err, api.bulkCalls)
	}
	if len(api.calls) <= apiStart {
		t.Fatal("apply did not reach account preflight")
	}
}

func TestDiscoveryPartialRefreshSkipsSelectedAccountThatFailed(t *testing.T) {
	cfg, account, now := testWorkflowConfig(t, true, true)
	api := newWorkflowAPI(account, now, adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true})
	api.status.RunStatus = "partial_failure"
	api.status.Accounts[0].Status = "failed"
	result, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.Plan != nil || api.syncCalls != 0 || hasCall(api.calls, "account:7") {
		t.Fatalf("failed account was used after partial refresh: plan=%#v sync=%d calls=%v", result.Plan, api.syncCalls, api.calls)
	}
}

func TestManualDiscoveryOverridesStalePartialRefreshOnlyInAllAccountPreview(t *testing.T) {
	cfg, account, now := testWorkflowConfig(t, true, true)
	cfg.AccountScope = "all"
	cfg.Target.RuntimeCompatibility.APIContractID = config.APIContractAllAccountsV1
	cfg.AcceptAllCredentialResponseMemoryExposure = true
	cfg.Accounts = nil
	rules := workflowRules(account)
	rules.SourceProfiles[0].APIContractID = config.APIContractAllAccountsV1
	writeWorkflowRules(t, cfg.RulesFile, rules)
	api := newWorkflowAPI(account, now, adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true})
	previousDay := now.Add(-24 * time.Hour)
	api.status.RunStatus = "partial_failure"
	api.status.LastFinishedAt = &previousDay
	api.status.Accounts[0].Status = "failed"

	result, err := (Discoverer{
		ReadCredential:       workflowCredential,
		NewClient:            func(string) (AdminAPI, error) { return api, nil },
		Now:                  func() time.Time { return now },
		ManualSyncAuthorized: true,
	}).Discover(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Report.ManualNativeSyncOverride || result.Report.RefreshRunStatus != "partial_failure" || result.Report.RefreshFinishedAt == nil || !result.Report.RefreshFinishedAt.Equal(previousDay) {
		t.Fatalf("manual override was not recorded transparently: %#v", result.Report)
	}
	if api.syncCalls != 1 || api.bulkCalls != nil || result.Plan == nil {
		t.Fatalf("manual preview did not perform exactly one native sync without mapping writes: sync=%d bulk=%v plan=%#v", api.syncCalls, api.bulkCalls, result.Plan)
	}
}

func TestDiscoveryKeepsCompletenessUnknownAndNeverAddsUnknownIdentity(t *testing.T) {
	cfg, account, now := testWorkflowConfig(t, true, true)
	api := newWorkflowAPI(account, now, adminapi.Catalog{Models: []string{"upstream-known", "brand-new-opaque-id"}, LiveListAvailable: true})
	result, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.Plan == nil || api.syncCalls != 1 {
		t.Fatalf("expected one mocked discovery and a candidate plan: sync=%d plan=%#v", api.syncCalls, result.Plan)
	}
	plan := result.Plan.Accounts[0]
	if plan.Completeness != catalog.CompletenessUnknown || plan.CompletenessProfileID != "" {
		t.Fatalf("completeness was inferred without an implemented profile: %#v", plan)
	}
	if got := plan.After["brand-new-opaque-id"]; got != "" {
		t.Fatalf("unknown identity was auto-added without proven completeness: %q", got)
	}
	if !reflect.DeepEqual(plan.UnreviewedModels, []string{"brand-new-opaque-id"}) {
		t.Fatalf("unknown catalog ID was not reported as unreviewed: %v", plan.UnreviewedModels)
	}
	if got := plan.After["canonical-name"]; got != "upstream-known" {
		t.Fatalf("exact reviewed alias missing from candidate: %q", got)
	}
	if !result.Report.NativeSyncSideEffect {
		t.Fatal("report did not disclose native sync metadata side effect")
	}
}

func TestDiscoveryFallbackEmptyAndWarningsDoNotCreateWritableCatalogPlan(t *testing.T) {
	cases := []struct {
		name              string
		catalog           adminapi.Catalog
		wantPlan          bool
		wantApplyEligible bool
	}{
		{name: "fallback", catalog: adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: false}},
		{name: "empty live", catalog: adminapi.Catalog{Models: []string{}, LiveListAvailable: true}},
		{name: "unknown warning", catalog: adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true, Warnings: []adminapi.CatalogWarning{{Code: "partial_page", Message: "opaque message"}}}, wantPlan: true},
		{name: "capability metadata incomplete", catalog: adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true, Warnings: []adminapi.CatalogWarning{{Code: warningModelMetadataIncomplete, Message: "Model IDs were synced, but capability metadata is incomplete."}}}, wantPlan: true, wantApplyEligible: true},
		{name: "capability metadata partial", catalog: adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true, Warnings: []adminapi.CatalogWarning{{Code: warningModelMetadataPartial, Message: "Some model capabilities were saved; remaining models are still incomplete."}}}, wantPlan: true, wantApplyEligible: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, account, now := testWorkflowConfig(t, true, true)
			api := newWorkflowAPI(account, now, tc.catalog)
			result, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if api.syncCalls != 1 {
				t.Fatalf("sync calls=%d, want one", api.syncCalls)
			}
			if !tc.wantPlan && result.Plan != nil {
				t.Fatalf("fallback/empty catalog produced a plan: %#v", result.Plan)
			}
			if tc.wantPlan {
				if result.Plan == nil {
					t.Fatal("live catalog did not produce a candidate plan")
				}
				plan := result.Plan.Accounts[0]
				if plan.ApplyEligible != tc.wantApplyEligible || len(plan.CatalogWarningCodes) != len(tc.catalog.Warnings) {
					t.Fatalf("unexpected warning handling: eligible=%v warnings=%v", plan.ApplyEligible, plan.CatalogWarningCodes)
				}
				if strings.Contains(tc.name, "capability metadata") && (plan.Completeness != catalog.CompletenessUnknown || len(plan.Changes) != 1) {
					t.Fatalf("metadata warning changed catalog completeness or blocked an exact reviewed mapping: %#v", plan)
				}
			}
			stored, err := state.Load(filepath.Join(cfg.StateDir, "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			entry := stored.Accounts[catalog.AccountKey(account.ID)]
			if tc.name == "fallback" || tc.name == "empty live" {
				if entry.CatalogSnapshot != nil {
					t.Fatalf("fallback/empty catalog changed local snapshot state: %#v", entry.CatalogSnapshot)
				}
			}
		})
	}
}

func TestApplyOneIDMappingOnlyWriteReadbackAndPlanConsumption(t *testing.T) {
	cfg, account, now := testWorkflowConfig(t, true, true)
	api := newWorkflowAPI(account, now, adminapi.Catalog{
		Models: []string{"upstream-known"}, LiveListAvailable: true,
		Warnings: []adminapi.CatalogWarning{{Code: warningModelMetadataIncomplete, Message: "Model IDs were synced, but capability metadata is incomplete."}},
	})
	discovery, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
	if err != nil || discovery.Plan == nil {
		t.Fatalf("discovery failed: %#v %v", discovery.Plan, err)
	}
	plan := *discovery.Plan
	if !plan.Accounts[0].ApplyEligible || plan.Accounts[0].Completeness != catalog.CompletenessUnknown || !reflect.DeepEqual(plan.Accounts[0].CatalogWarningCodes, []string{warningModelMetadataIncomplete}) {
		t.Fatalf("metadata warning did not preserve the reviewed-plan boundary: %#v", plan.Accounts[0])
	}
	result, err := workflowApplier(api, now).Apply(context.Background(), cfg, plan, discovery.PlanSHA256, discovery.PlanSHA256, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Consumed || !reflect.DeepEqual(result.AppliedIDs, []int64{7}) || len(api.bulkCalls) != 1 {
		t.Fatalf("unexpected apply result/call count: %#v bulk=%v", result, api.bulkCalls)
	}
	if api.bulkCalls[0] != 7 || len(api.bulkMappings) != 1 || api.bulkMappings[0]["canonical-name"] != "upstream-known" || api.bulkMappings[0]["untouched"] != "keep-me" {
		t.Fatalf("write was not one-ID complete mapping-only payload: ids=%v mappings=%#v", api.bulkCalls, api.bulkMappings)
	}
	if got := api.accounts[7].ModelMapping["canonical-name"]; got != "upstream-known" {
		t.Fatalf("mapping was not read back: %q", got)
	}
	beforeCalls := len(api.calls)
	if _, err := workflowApplier(api, now).Apply(context.Background(), cfg, plan, discovery.PlanSHA256, discovery.PlanSHA256, false); err == nil {
		t.Fatal("consumed plan was accepted a second time")
	}
	if len(api.calls) != beforeCalls || len(api.bulkCalls) != 1 {
		t.Fatalf("consumed plan reached API: calls=%v bulk=%v", api.calls[beforeCalls:], api.bulkCalls)
	}
}

func TestDiscoveryPersistsReturnedAbsenceCountersInLocalState(t *testing.T) {
	cfg, account, now := testWorkflowConfig(t, true, true)
	source, err := catalog.SourceForAccount(account.Platform, account.Type, account.Protocol, account.BaseURL, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	prior := state.Empty()
	prior.Accounts[catalog.AccountKey(account.ID)] = state.Account{
		ManagedMappings: map[string]state.ManagedMapping{
			"untouched": {OriginalValue: "keep-me", LastAppliedValue: "keep-me", SourceFingerprint: catalog.SourceFingerprint(source)},
		},
		AbsenceCounts: map[string]int{"untouched": 1},
	}
	statePath := filepath.Join(cfg.StateDir, "state.json")
	if err := state.Save(statePath, prior); err != nil {
		t.Fatal(err)
	}
	api := newWorkflowAPI(account, now, adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true})
	result, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
	if err != nil || result.Plan == nil {
		t.Fatalf("discovery failed: %#v %v", result.Plan, err)
	}
	stored, err := state.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Accounts[catalog.AccountKey(account.ID)].AbsenceCounts["untouched"] != 1 {
		t.Fatalf("unknown catalog did not preserve returned absence count: %#v", stored.Accounts[catalog.AccountKey(account.ID)].AbsenceCounts)
	}
	if !reflect.DeepEqual(result.Plan.Accounts[0].AbsenceCountsBefore, map[string]int{"untouched": 1}) || !reflect.DeepEqual(result.Plan.Accounts[0].AbsenceCountsAfter, map[string]int{"untouched": 1}) {
		t.Fatalf("plan did not bind unchanged counter before/after: %#v", result.Plan.Accounts[0])
	}
}

func TestApplyPrivacyGateRunsBeforeCredentialOrAPI(t *testing.T) {
	cfg, account, now := testWorkflowConfig(t, true, true)
	api := newWorkflowAPI(account, now, adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true})
	discovery, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
	if err != nil || discovery.Plan == nil {
		t.Fatalf("discovery failed: %#v %v", discovery.Plan, err)
	}
	cfg.Accounts[0].AcceptCredentialResponseMemoryExposure = boolPointer(false)
	apiStart := len(api.calls)
	credentialCalls := 0
	_, err = (Applier{
		ReadCredential: func(config.CredentialConfig) (securecred.Value, error) {
			credentialCalls++
			return securecred.Value{}, nil
		},
		NewClient: func(string) (AdminAPI, error) { return api, nil },
		Now:       func() time.Time { return now },
	}).Apply(context.Background(), cfg, *discovery.Plan, discovery.PlanSHA256, discovery.PlanSHA256, false)
	if err == nil || credentialCalls != 0 || len(api.calls) != apiStart || len(api.bulkCalls) != 0 {
		t.Fatalf("privacy gate did not block before access: err=%v credentials=%d calls=%v", err, credentialCalls, api.calls[apiStart:])
	}
}

func TestApplyPreflightFailureHasNoBulkWrite(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*config.Config, *catalog.ApprovalPlan, *workflowAPI)
		confirm string
	}{
		{name: "confirmation mismatch", confirm: "wrong"},
		{name: "mapping drift", mutate: func(_ *config.Config, _ *catalog.ApprovalPlan, api *workflowAPI) {
			api.accounts[7].ModelMapping["untouched"] = "changed"
		}},
		{name: "account no longer active", mutate: func(_ *config.Config, _ *catalog.ApprovalPlan, api *workflowAPI) {
			a := api.accounts[7]
			a.Status = "disabled"
			api.accounts[7] = a
		}},
		{name: "source drift", mutate: func(_ *config.Config, _ *catalog.ApprovalPlan, api *workflowAPI) {
			a := api.accounts[7]
			a.BaseURL = "https://other.example/v1"
			api.accounts[7] = a
		}},
		{name: "rules hash drift", mutate: func(cfg *config.Config, _ *catalog.ApprovalPlan, _ *workflowAPI) {
			appendFixtureByte(t, cfg.RulesFile)
		}},
		{name: "state hash drift", mutate: func(cfg *config.Config, _ *catalog.ApprovalPlan, _ *workflowAPI) {
			appendFixtureByte(t, filepath.Join(cfg.StateDir, "state.json"))
		}},
		{name: "absence counter plan drift", mutate: func(_ *config.Config, plan *catalog.ApprovalPlan, _ *workflowAPI) {
			plan.Accounts[0].AbsenceCountsAfter["unexpected-counter"] = 1
		}},
		{name: "catalog content drift", mutate: func(_ *config.Config, plan *catalog.ApprovalPlan, _ *workflowAPI) {
			plan.Accounts[0].CatalogModels = append(plan.Accounts[0].CatalogModels, "unbound-catalog-id")
		}},
		{name: "target compatibility drift", mutate: func(cfg *config.Config, _ *catalog.ApprovalPlan, _ *workflowAPI) {
			cfg.Target.RuntimeCompatibility.BuildID = "other-build"
		}},
		{name: "expired plan", mutate: func(_ *config.Config, p *catalog.ApprovalPlan, _ *workflowAPI) {
			p.ExpiresAt = p.CreatedAt.Add(-time.Second)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, account, now := testWorkflowConfig(t, true, true)
			api := newWorkflowAPI(account, now, adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true})
			discovery, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
			if err != nil || discovery.Plan == nil {
				t.Fatalf("discovery failed: %#v %v", discovery.Plan, err)
			}
			plan := *discovery.Plan
			confirm := discovery.PlanSHA256
			if tc.confirm != "" {
				confirm = tc.confirm
			}
			apiStart := len(api.calls)
			if tc.mutate != nil {
				tc.mutate(&cfg, &plan, api)
			}
			_, err = workflowApplier(api, now).Apply(context.Background(), cfg, plan, discovery.PlanSHA256, confirm, false)
			if err == nil || len(api.bulkCalls) != 0 {
				t.Fatalf("preflight did not fail closed: err=%v bulk=%v", err, api.bulkCalls)
			}
			if tc.name == "confirmation mismatch" || tc.name == "expired plan" || tc.name == "catalog content drift" {
				if len(api.calls) != apiStart {
					t.Fatalf("local approval/plan validation contacted API: %v", api.calls[apiStart:])
				}
			}
		})
	}
}

func TestSameCountMapTreatsEmptyPersistedMapAsCanonicalAndRejectsDrift(t *testing.T) {
	if !sameCountMap(nil, map[string]int{}) {
		t.Fatal("nil persisted counters and an approved empty counter map should be equivalent")
	}
	if sameCountMap(map[string]int{"model": 1}, map[string]int{"model": 2}) {
		t.Fatal("counter drift was accepted")
	}
	approved := catalog.Plan{AbsenceCountsBefore: map[string]int{"model": 1}, AbsenceCountsAfter: map[string]int{"model": 2}}
	drifted := approved
	drifted.AbsenceCountsAfter = map[string]int{"model": 1}
	if samePlanDelta(approved, drifted) {
		t.Fatal("plan delta equality ignored a changed after-counter snapshot")
	}
}

func TestApplyTransportUncertaintyUsesOneReadbackNoBlindRetry(t *testing.T) {
	cfg, account, now := testWorkflowConfig(t, true, true)
	api := newWorkflowAPI(account, now, adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true})
	discovery, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
	if err != nil || discovery.Plan == nil {
		t.Fatalf("discovery failed: %#v %v", discovery.Plan, err)
	}
	api.bulkErr = errors.New("mock transport loss")
	api.applyBeforeError = true
	applyStart := len(api.calls)
	result, err := workflowApplier(api, now).Apply(context.Background(), cfg, *discovery.Plan, discovery.PlanSHA256, discovery.PlanSHA256, false)
	if err != nil {
		t.Fatalf("successful readback did not resolve uncertain result: result=%#v err=%v", result, err)
	}
	if len(api.bulkCalls) != 1 || countCall(api.calls[applyStart:], "account:7") != 3 {
		t.Fatalf("uncertain write was retried or not read back once: bulk=%v calls=%v", api.bulkCalls, api.calls)
	}
}

func TestApplyStopsOnReadbackMismatchOrUnexpectedBulkResult(t *testing.T) {
	for _, mode := range []string{"mapping mismatch", "key presence changed", "failed or unexpected account IDs"} {
		name := mode
		t.Run(name, func(t *testing.T) {
			cfg, account, now := testWorkflowConfig(t, true, true)
			api := newWorkflowAPI(account, now, adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true})
			discovery, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
			if err != nil || discovery.Plan == nil {
				t.Fatalf("discovery failed: %#v %v", discovery.Plan, err)
			}
			switch mode {
			case "failed or unexpected account IDs":
				api.unexpectedBulkResult = true
			case "mapping mismatch":
				api.ignoreBulkWrite = true
			case "key presence changed":
				api.flipAPIKeyPresenceOnBulk = true
			}
			_, err = workflowApplier(api, now).Apply(context.Background(), cfg, *discovery.Plan, discovery.PlanSHA256, discovery.PlanSHA256, false)
			if err == nil || len(api.bulkCalls) != 1 {
				t.Fatalf("unsafe write outcome did not stop: err=%v calls=%v", err, api.bulkCalls)
			}
			if len(api.bulkCalls) > 1 {
				t.Fatalf("write outcome triggered blind retry: %v", api.bulkCalls)
			}
		})
	}
}

func TestAccountKeyPresenceMustBeAvailableAndUnchangedForReadback(t *testing.T) {
	trueValue, falseValue := true, false
	cases := []struct {
		name  string
		prior *bool
		next  *bool
		want  bool
	}{
		{"true preserved", &trueValue, &trueValue, true},
		{"false preserved", &falseValue, &falseValue, true},
		{"marker missing", nil, nil, false},
		{"marker lost", &trueValue, nil, false},
		{"presence changed", &trueValue, &falseValue, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := adminapi.Account{ID: 7, Platform: "openai", Type: "apikey", Protocol: "openai", BaseURL: "https://provider.example/v1", Status: "active", APIKeyPresent: tc.prior, ModelMapping: map[string]string{}}
			after := before
			after.APIKeyPresent = tc.next
			if got := sameAccountSource(before, after); got != tc.want {
				t.Fatalf("sameAccountSource()=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestApplyRechecksEachAccountImmediatelyBeforeWriteAndReportsPartialSuccess(t *testing.T) {
	cfg, account1, now := testWorkflowConfig(t, true, true)
	account2 := adminapi.Account{ID: 8, Platform: account1.Platform, Type: account1.Type, Protocol: account1.Protocol, BaseURL: account1.BaseURL, Status: account1.Status, ModelMapping: map[string]string{"untouched": "keep-me"}, APIKeyPresent: boolPointer(true)}
	cfg.Accounts = append(cfg.Accounts, config.AccountConfig{ID: account2.ID, AcceptCredentialResponseMemoryExposure: boolPointer(true)})
	rules := workflowRules(account1)
	rules.Aliases[0].Source.AccountID = 0
	rules.SourceProfiles[0].Source.AccountID = 0
	writeWorkflowRules(t, cfg.RulesFile, rules)
	api := newWorkflowAPI(account1, now, adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true})
	api.accounts[account2.ID] = account2
	api.catalogs[account2.ID] = adminapi.Catalog{Models: []string{"upstream-known"}, LiveListAvailable: true}
	api.status.Accounts = append(api.status.Accounts, adminapi.AccountRunStatus{AccountID: account2.ID, Status: "completed"})
	discovery, err := workflowDiscoverer(api, now).Discover(context.Background(), cfg)
	if err != nil || discovery.Plan == nil || len(discovery.Plan.Accounts) != 2 {
		t.Fatalf("two-account discovery failed: %#v %v", discovery.Plan, err)
	}
	api.afterBulk = func(id int64, api *workflowAPI) {
		if id == account1.ID {
			a := api.accounts[account2.ID]
			a.ModelMapping["untouched"] = "external-edit"
			api.accounts[account2.ID] = a
		}
	}
	result, err := workflowApplier(api, now).Apply(context.Background(), cfg, *discovery.Plan, discovery.PlanSHA256, discovery.PlanSHA256, false)
	if err == nil || !result.Consumed || !reflect.DeepEqual(result.AppliedIDs, []int64{account1.ID}) {
		t.Fatalf("partial success was not recorded after account 2 drift: result=%#v err=%v", result, err)
	}
	if !reflect.DeepEqual(api.bulkCalls, []int64{account1.ID}) {
		t.Fatalf("drifted second account was written: bulk IDs=%v", api.bulkCalls)
	}
	if api.accounts[account2.ID].ModelMapping["untouched"] != "external-edit" {
		t.Fatalf("external edit was overwritten: %#v", api.accounts[account2.ID].ModelMapping)
	}
}

func TestApplyingRetirementClearsPersistedAbsenceCounter(t *testing.T) {
	s := state.Empty()
	s.Accounts["9001"] = state.Account{
		ManagedMappings: map[string]state.ManagedMapping{
			"public-model": {OriginalValue: "old", LastAppliedValue: "upstream-model", SourceFingerprint: "source-fingerprint"},
		},
		AbsenceCounts: map[string]int{"public-model": 2},
	}
	applyManagedChanges(&s, catalog.Plan{
		AccountID: 9001,
		Changes:   []catalog.Change{{Action: "remove", PublicID: "public-model", Before: "upstream-model", RuleKind: "complete_catalog_absence"}},
	})
	account := s.Accounts["9001"]
	if _, ok := account.ManagedMappings["public-model"]; ok {
		t.Fatal("applied retirement retained managed ownership")
	}
	if _, ok := account.AbsenceCounts["public-model"]; ok {
		t.Fatalf("applied retirement retained its absence counter: %v", account.AbsenceCounts)
	}
}

func TestNativeRefreshCurrentRequiresTodayShanghaiSuccess(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 11, 4, 10, 0, 0, loc)
	previous := now.AddDate(0, 0, -1)
	future := now.Add(time.Minute)
	for _, tc := range []struct {
		name   string
		status adminapi.RefreshStatus
		want   bool
	}{
		{"current complete", adminapi.RefreshStatus{Enabled: true, Timezone: "Asia/Shanghai", RunStatus: "completed", LastFinishedAt: timePtr(now.Add(-time.Minute))}, true},
		{"current partial", adminapi.RefreshStatus{Enabled: true, Timezone: "Asia/Shanghai", RunStatus: "partial_failure", LastFinishedAt: timePtr(now.Add(-time.Minute))}, true},
		{"previous day", adminapi.RefreshStatus{Enabled: true, Timezone: "Asia/Shanghai", RunStatus: "completed", LastFinishedAt: timePtr(previous)}, false},
		{"future", adminapi.RefreshStatus{Enabled: true, Timezone: "Asia/Shanghai", RunStatus: "completed", LastFinishedAt: timePtr(future)}, false},
		{"wrong timezone", adminapi.RefreshStatus{Enabled: true, Timezone: "UTC", RunStatus: "completed", LastFinishedAt: timePtr(now)}, false},
		{"running", adminapi.RefreshStatus{Enabled: true, Timezone: "Asia/Shanghai", RunStatus: "running", LastFinishedAt: timePtr(now)}, false},
		{"disabled", adminapi.RefreshStatus{Enabled: false, Timezone: "Asia/Shanghai", RunStatus: "completed", LastFinishedAt: timePtr(now)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NativeRefreshCurrent(tc.status, now); got != tc.want {
				t.Fatalf("NativeRefreshCurrent()=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestManualOnlyRefreshRequiresExactCompletedFourAMPostgresRun(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 4, 10, 0, 0, loc)
	good := adminapi.RefreshStatus{Enabled: true, Schedule: "04:00", Timezone: "Asia/Shanghai", StatusSource: "postgres", RunStatus: "completed", LastScheduledAt: timePtr(time.Date(2026, 10, 12, 4, 0, 0, 0, loc)), LastFinishedAt: timePtr(time.Date(2026, 10, 12, 4, 2, 0, 0, loc))}
	if !ManualOnlyRefreshCurrent(good, now) {
		t.Fatal("current completed 04:00 PostgreSQL run was rejected")
	}
	cases := []struct {
		name   string
		change func(*adminapi.RefreshStatus)
	}{
		{"wrong schedule", func(s *adminapi.RefreshStatus) { s.Schedule = "05:00" }},
		{"wrong source", func(s *adminapi.RefreshStatus) { s.StatusSource = "memory" }},
		{"partial", func(s *adminapi.RefreshStatus) { s.RunStatus = "partial_failure" }},
		{"previous scheduled date", func(s *adminapi.RefreshStatus) { s.LastScheduledAt = timePtr(time.Date(2026, 10, 11, 4, 0, 0, 0, loc)) }},
		{"missing finish time", func(s *adminapi.RefreshStatus) { s.LastFinishedAt = nil }},
		{"finish before scheduled time", func(s *adminapi.RefreshStatus) { s.LastFinishedAt = timePtr(time.Date(2026, 10, 12, 3, 59, 0, 0, loc)) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			copy := good
			tc.change(&copy)
			if ManualOnlyRefreshCurrent(copy, now) {
				t.Fatal("invalid native refresh state was accepted")
			}
		})
	}
}

func TestManualOnlySkippedAccountSelectionIsExactAndFailClosed(t *testing.T) {
	status := adminapi.RefreshStatus{Accounts: []adminapi.AccountRunStatus{
		{AccountID: 1, SourceProfileID: "manual_only", Status: "skipped"},
		{AccountID: 2, SourceProfileID: "openai-official", Status: "completed"},
		{AccountID: 3, SourceProfileID: "manual_only", Status: "failed"},
	}}
	ids, err := ManualOnlySkippedAccountIDs(status)
	if err != nil || len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("unexpected selected manual-only IDs: ids=%v err=%v", ids, err)
	}
	status.Accounts = append(status.Accounts, status.Accounts[0])
	if _, err := ManualOnlySkippedAccountIDs(status); err == nil {
		t.Fatal("duplicate native account rows must fail closed")
	}
	status.Accounts = []adminapi.AccountRunStatus{{AccountID: 1, SourceProfileID: "manual_only", Status: "future_unknown_state"}}
	if _, err := ManualOnlySkippedAccountIDs(status); err == nil {
		t.Fatal("unknown native account refresh statuses must fail closed")
	}
}

func TestManualOnlyRunSyncsAndAppliesOnlyExactSkippedAccounts(t *testing.T) {
	cfg, selected, now := testWorkflowConfig(t, true, true)
	now = time.Date(2026, 10, 12, 4, 10, 0, 0, now.Location())
	selected.ModelMapping = map[string]string{"old-model": "old-model", "still-live": "still-live", "keep-alias": "provider-alias"}
	cfg.AccountScope = "all"
	cfg.Accounts = nil
	cfg.AcceptAllCredentialResponseMemoryExposure = true
	cfg.Target.RuntimeCompatibility.APIContractID = config.APIContractAllAccountsV1
	cfg.ApplyMode = "preview"
	cfg.ApprovedRulesSHA256 = ""

	rules := workflowRules(selected)
	rules.Aliases = []catalog.AliasRule{}
	rules.SourceProfiles[0].APIContractID = config.APIContractAllAccountsV1
	rules.SourceProfiles[0].IdentityAddPolicy = "report_only"
	rules.SourceProfiles[0].IdentityCatalogPolicy = "replace_identity_from_live_list"
	writeWorkflowRules(t, cfg.RulesFile, rules)
	_, ruleHash, err := catalog.LoadRulesWithHash(cfg.RulesFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ApplyMode = "approved_rules"
	cfg.ApprovedRulesSHA256 = ruleHash
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	other := selected
	other.ID = 8
	other.ModelMapping = map[string]string{"other-model": "other-model"}
	api := &workflowAPI{
		status: adminapi.RefreshStatus{
			Enabled: true, Schedule: "04:00", Timezone: "Asia/Shanghai", StatusSource: "postgres", RunStatus: "completed",
			LastScheduledAt: timePtr(time.Date(2026, 10, 12, 4, 0, 0, 0, now.Location())),
			LastFinishedAt:  timePtr(time.Date(2026, 10, 12, 4, 2, 0, 0, now.Location())),
			Accounts: []adminapi.AccountRunStatus{
				{AccountID: selected.ID, SourceProfileID: "manual_only", Status: "skipped"},
				{AccountID: other.ID, SourceProfileID: "official", Status: "completed"},
			},
		},
		accounts: map[int64]adminapi.Account{selected.ID: selected, other.ID: other},
		catalogs: map[int64]adminapi.Catalog{selected.ID: {Models: []string{"new-model", "still-live"}, LiveListAvailable: true}},
	}
	discoverer := Discoverer{ReadCredential: workflowCredential, NewClient: func(string) (AdminAPI, error) { return api, nil }, Now: func() time.Time { return now }, ManualOnlyOnly: true}
	discovery, err := discoverer.Discover(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if api.syncCalls != 1 || discovery.Plan == nil || len(discovery.Plan.Accounts) != 1 || discovery.Plan.Accounts[0].AccountID != selected.ID {
		t.Fatalf("manual-only discovery selected wrong accounts: sync=%d plan=%#v", api.syncCalls, discovery.Plan)
	}
	if len(discovery.Report.Discovery) != 2 || discovery.Report.Discovery[1].Status != "skipped_not_manual_only" {
		t.Fatalf("non-manual account was not reported as skipped: %#v", discovery.Report.Discovery)
	}
	if api.bulkCalls != nil {
		t.Fatal("discovery must not write mapping before Apply")
	}

	apply := workflowApplier(api, now)
	apply.ManualOnlyOnly = true
	result, err := apply.Apply(context.Background(), cfg, *discovery.Plan, discovery.PlanSHA256, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.AppliedIDs) != 1 || result.AppliedIDs[0] != selected.ID || fmt.Sprint(api.bulkCalls) != fmt.Sprint([]int64{selected.ID}) {
		t.Fatalf("manual-only apply changed wrong accounts: result=%#v calls=%v", result, api.bulkCalls)
	}
	updated := api.accounts[selected.ID].ModelMapping
	if updated["old-model"] != "" || updated["new-model"] != "new-model" || updated["still-live"] != "still-live" || updated["keep-alias"] != "provider-alias" {
		t.Fatalf("selected account mapping was not synchronized safely: %#v", updated)
	}
	if api.accounts[other.ID].ModelMapping["other-model"] != "other-model" || countCall(api.calls, "sync:8") != 0 {
		t.Fatal("non-manual account was probed or modified")
	}
}

func TestManualOnlyRequiredConfigBlocksOrdinaryDiscoverAndApplyBeforeAccess(t *testing.T) {
	cfg, _, _ := testWorkflowConfig(t, true, true)
	cfg.AccountScope = "all"
	cfg.Accounts = nil
	cfg.AcceptAllCredentialResponseMemoryExposure = true
	cfg.RequireManualOnlyMode = true
	cfg.Target.RuntimeCompatibility.APIContractID = config.APIContractAllAccountsV1
	credentialCalls, clientCalls := 0, 0
	readCredential := func(config.CredentialConfig) (securecred.Value, error) {
		credentialCalls++
		return securecred.Value{APIKey: []byte("test-admin-key")}, nil
	}
	newClient := func(string) (AdminAPI, error) {
		clientCalls++
		return nil, errors.New("client should not be created")
	}
	if _, err := (Discoverer{ReadCredential: readCredential, NewClient: newClient}).Discover(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "requires manual-only mode") {
		t.Fatalf("ordinary discovery was not blocked: %v", err)
	}
	if _, err := (Applier{ReadCredential: readCredential, NewClient: newClient}).Apply(context.Background(), cfg, catalog.ApprovalPlan{}, "", "", false); err == nil || !strings.Contains(err.Error(), "requires manual-only mode") {
		t.Fatalf("ordinary apply was not blocked: %v", err)
	}
	if credentialCalls != 0 || clientCalls != 0 {
		t.Fatalf("manual-only mode gate ran after external access: credentials=%d clients=%d", credentialCalls, clientCalls)
	}
}

func TestLiveIdentityCatalogRulesRequireManualOnlyBeforeExternalAccess(t *testing.T) {
	cfg, account, now := testWorkflowConfig(t, true, true)
	rules := workflowRules(account)
	rules.SourceProfiles[0].IdentityCatalogPolicy = "replace_identity_from_live_list"
	writeWorkflowRules(t, cfg.RulesFile, rules)
	credentialCalls, clientCalls := 0, 0
	_, err := (Discoverer{
		ReadCredential: func(config.CredentialConfig) (securecred.Value, error) {
			credentialCalls++
			return securecred.Value{APIKey: []byte("test-admin-key")}, nil
		},
		NewClient: func(string) (AdminAPI, error) {
			clientCalls++
			return nil, errors.New("client should not be created")
		},
		Now: func() time.Time { return now },
	}).Discover(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "live identity catalog rules require manual-only mode") || credentialCalls != 0 || clientCalls != 0 {
		t.Fatalf("identity sync rules did not require manual-only mode before external access: err=%v credentials=%d clients=%d", err, credentialCalls, clientCalls)
	}
}

func testWorkflowConfig(t *testing.T, expose, syncSideEffect bool) (config.Config, adminapi.Account, time.Time) {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 11, 4, 10, 0, 0, loc)
	account := adminapi.Account{ID: 7, Platform: "openai", Type: "apikey", Protocol: "openai", BaseURL: "https://provider.example/v1", Status: "active", ModelMapping: map[string]string{"untouched": "keep-me"}, APIKeyPresent: boolPointer(true)}
	base := t.TempDir()
	cfg := config.Config{
		Target:                       config.TargetConfig{BaseURL: "https://404token.xyz", RuntimeCompatibility: config.RuntimeCompatibility{BuildID: "test-build", APIContractID: "admin-catalog-v1", Evidence: "mock-only fixture", ReviewedAt: now.Format("2006-01-02")}},
		Accounts:                     []config.AccountConfig{{ID: 7, AcceptCredentialResponseMemoryExposure: boolPointer(expose)}},
		AllowNativeSyncMetadataWrite: syncSideEffect,
		ApplyMode:                    "preview",
		Credential:                   config.CredentialConfig{WindowsCredentialTarget: "mock-only"},
		StateDir:                     filepath.Join(base, "state"),
		ReportDir:                    filepath.Join(base, "reports"),
		BackupDir:                    filepath.Join(base, "backups"),
		RulesFile:                    filepath.Join(base, "model-rules.json"),
	}
	writeWorkflowRules(t, cfg.RulesFile, workflowRules(account))
	return cfg, account, now
}

func workflowRules(account adminapi.Account) catalog.Rules {
	source, _ := catalog.SourceForAccount(account.Platform, account.Type, account.Protocol, account.BaseURL, account.ID)
	return catalog.Rules{
		SchemaVersion:           1,
		Aliases:                 []catalog.AliasRule{{Source: source, UpstreamID: "upstream-known", PublicID: "canonical-name", Decision: "approved", Evidence: "exact reviewed fixture", ReviewedAt: "2026-10-11"}},
		SourceProfiles:          []catalog.SourceProfile{{Source: source, BuildID: "test-build", APIContractID: "admin-catalog-v1", Decision: "approved", IdentityAddPolicy: "add_exact", Evidence: "mock source/build/API compatibility fixture", ReviewedAt: "2026-10-11"}},
		CompleteCatalogProfiles: []catalog.CompleteCatalogProfile{},
		VersionFamilies:         []catalog.VersionFamily{},
		RetiredModels:           []catalog.RetiredRule{},
	}
}

func writeWorkflowRules(t *testing.T, path string, rules catalog.Rules) {
	t.Helper()
	body, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
}

func appendFixtureByte(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write([]byte(" "))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("could not mutate fixture: write=%v close=%v", writeErr, closeErr)
	}
}

func newWorkflowAPI(account adminapi.Account, now time.Time, catalogResult adminapi.Catalog) *workflowAPI {
	return &workflowAPI{
		status:   adminapi.RefreshStatus{Enabled: true, Timezone: "Asia/Shanghai", RunStatus: "completed", LastFinishedAt: timePtr(now.Add(-time.Minute)), Accounts: []adminapi.AccountRunStatus{{AccountID: account.ID, Status: "completed"}}},
		accounts: map[int64]adminapi.Account{account.ID: account}, catalogs: map[int64]adminapi.Catalog{account.ID: catalogResult},
	}
}

func workflowDiscoverer(api *workflowAPI, now time.Time) Discoverer {
	return Discoverer{ReadCredential: workflowCredential, NewClient: func(string) (AdminAPI, error) { return api, nil }, Now: func() time.Time { return now }}
}

func workflowApplier(api *workflowAPI, now time.Time) Applier {
	return Applier{ReadCredential: workflowCredential, NewClient: func(string) (AdminAPI, error) { return api, nil }, Now: func() time.Time { return now }}
}

func workflowCredential(config.CredentialConfig) (securecred.Value, error) {
	return securecred.Value{APIKey: []byte("test-admin-key")}, nil
}

func (f *workflowAPI) SetAPIKey([]byte) error {
	f.calls = append(f.calls, "api_key")
	return nil
}

func (f *workflowAPI) RefreshStatus(context.Context) (adminapi.RefreshStatus, error) {
	f.calls = append(f.calls, "status")
	return f.status, nil
}

func (f *workflowAPI) ListAllAccounts(context.Context) ([]adminapi.AccountSummary, error) {
	f.calls = append(f.calls, "list_accounts")
	ids := append([]int64(nil), f.inventoryIDs...)
	if f.inventoryIDs == nil {
		ids = make([]int64, 0, len(f.accounts))
		for id := range f.accounts {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	items := make([]adminapi.AccountSummary, 0, len(ids))
	for _, id := range ids {
		account := f.accounts[id]
		items = append(items, adminapi.AccountSummary{ID: id, Platform: account.Platform, Type: account.Type, Status: account.Status, ParentAccountID: account.ParentAccountID})
	}
	return items, nil
}

func (f *workflowAPI) Account(_ context.Context, id int64) (adminapi.Account, error) {
	f.calls = append(f.calls, "account:"+intString(id))
	a, ok := f.accounts[id]
	if !ok {
		return adminapi.Account{}, errors.New("mock account missing")
	}
	a.ModelMapping = cloneWorkflowMapping(a.ModelMapping)
	return a, nil
}

func (f *workflowAPI) AccountWithPublishedSnapshot(ctx context.Context, id int64, _, _ string) (adminapi.Account, error) {
	return f.Account(ctx, id)
}

func (f *workflowAPI) SyncUpstream(_ context.Context, id int64) (adminapi.Catalog, error) {
	f.calls = append(f.calls, "sync:"+intString(id))
	f.syncCalls++
	result, ok := f.catalogs[id]
	if !ok {
		return adminapi.Catalog{}, errors.New("mock catalog missing")
	}
	result.Models = append([]string{}, result.Models...)
	result.Warnings = append([]adminapi.CatalogWarning{}, result.Warnings...)
	return result, nil
}

func (f *workflowAPI) BulkUpdateMapping(_ context.Context, id int64, mapping map[string]string) (adminapi.BulkUpdateResult, error) {
	f.calls = append(f.calls, "bulk:"+intString(id))
	f.bulkCalls = append(f.bulkCalls, id)
	f.bulkMappings = append(f.bulkMappings, cloneWorkflowMapping(mapping))
	if f.applyBeforeError {
		a := f.accounts[id]
		a.ModelMapping = cloneWorkflowMapping(mapping)
		f.accounts[id] = a
	}
	if f.unexpectedBulkResult {
		return adminapi.BulkUpdateResult{}, adminapi.ErrUnexpectedBulkResult
	}
	if f.bulkErr != nil {
		return adminapi.BulkUpdateResult{}, f.bulkErr
	}
	if !f.ignoreBulkWrite {
		a := f.accounts[id]
		a.ModelMapping = cloneWorkflowMapping(mapping)
		f.accounts[id] = a
	}
	if f.flipAPIKeyPresenceOnBulk {
		a := f.accounts[id]
		value := false
		a.APIKeyPresent = &value
		f.accounts[id] = a
	}
	if f.afterBulk != nil {
		f.afterBulk(id, f)
	}
	return adminapi.BulkUpdateResult{SuccessIDs: []int64{id}, FailedIDs: []int64{}}, nil
}

func (f *workflowAPI) Close() {}

type workflowAPI struct {
	status                   adminapi.RefreshStatus
	accounts                 map[int64]adminapi.Account
	inventoryIDs             []int64
	catalogs                 map[int64]adminapi.Catalog
	calls                    []string
	syncCalls                int
	bulkCalls                []int64
	bulkMappings             []map[string]string
	bulkErr                  error
	applyBeforeError         bool
	ignoreBulkWrite          bool
	unexpectedBulkResult     bool
	afterBulk                func(int64, *workflowAPI)
	flipAPIKeyPresenceOnBulk bool
}

func cloneWorkflowMapping(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func timePtr(v time.Time) *time.Time { return &v }

func hasCall(calls []string, want string) bool { return countCall(calls, want) > 0 }

func countCall(calls []string, want string) int {
	n := 0
	for _, call := range calls {
		if call == want {
			n++
		}
	}
	return n
}
