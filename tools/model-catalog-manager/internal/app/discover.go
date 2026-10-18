package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"time"
	_ "time/tzdata"

	"sub2api-model-catalog-manager/internal/adminapi"
	"sub2api-model-catalog-manager/internal/catalog"
	"sub2api-model-catalog-manager/internal/config"
	"sub2api-model-catalog-manager/internal/report"
	"sub2api-model-catalog-manager/internal/state"
)

type DiscoveryResult struct {
	Report     report.Document
	Plan       *catalog.ApprovalPlan
	PlanPath   string
	PlanSHA256 string
}

type Discoverer struct {
	ReadCredential CredentialReader
	NewClient      ClientFactory
	Now            func() time.Time
	WaitForRefresh bool
	// ManualSyncAuthorized permits an explicitly authorized all-account
	// preview to call native SyncUpstream even when today's scheduled refresh
	// has not completed. It also retries accounts marked failed by the prior
	// scheduled run; it never enables mapping apply.
	ManualSyncAuthorized bool
	Sleep                func(context.Context, time.Duration) error
	RefreshWait          time.Duration
	PollInterval         time.Duration
}

var safeWarningCodePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,80}$`)

const (
	// These native Sub2API warnings describe capability metadata enrichment,
	// not failure or truncation of the model-ID list.
	warningModelMetadataIncomplete = "upstream_model_metadata_incomplete"
	warningModelMetadataPartial    = "upstream_model_metadata_partial"
)

func (d Discoverer) Discover(ctx context.Context, cfg config.Config) (DiscoveryResult, error) {
	var result DiscoveryResult
	if err := cfg.Validate(); err != nil {
		return result, err
	}
	if !cfg.RuntimeCompatible() {
		return result, errors.New("discovery blocked: target runtime compatibility is not verified")
	}
	if d.ManualSyncAuthorized && (!cfg.AllAccounts() || cfg.ApplyModeValue() != "preview") {
		return result, errors.New("manual discovery requires all-account scope and preview-only mode")
	}
	if !cfg.AllowNativeSyncMetadataWrite {
		return result, errors.New("discovery blocked: native sync metadata side effect is disabled")
	}
	rules, rulesHash, err := catalog.LoadRulesWithHash(cfg.RulesFile)
	if err != nil {
		return result, err
	}
	eligible := cfg.EligibleAccountIDs()
	if !cfg.AllAccounts() && len(eligible) == 0 {
		return result, errors.New("discovery blocked: no account passed the credential-response memory gate")
	}
	if cfg.AllAccounts() {
		if !cfg.AcceptAllCredentialResponseMemoryExposure {
			return result, errors.New("discovery blocked: all-account credential-response memory exposure was not explicitly accepted")
		}
	} else if err := cfg.ExposureGateFor(eligible); err != nil {
		return result, err
	}
	if d.ReadCredential == nil || d.NewClient == nil {
		return result, errors.New("discovery dependencies are unavailable")
	}
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	lock, err := state.AcquireLock(filepath.Join(cfg.StateDir, "run.lock"))
	if err != nil {
		return result, err
	}
	defer lock.Release()
	statePath := filepath.Join(cfg.StateDir, "state.json")
	localState, err := state.Load(statePath)
	if err != nil {
		return result, err
	}
	cred, err := d.ReadCredential(cfg.Credential)
	if err != nil {
		return result, errors.New("administrator credential could not be read from the configured OS store")
	}
	defer cred.Clear()
	client, err := d.NewClient(cfg.Target.BaseURL)
	if err != nil {
		return result, err
	}
	defer client.Close()
	if err := client.SetAPIKey(cred.APIKey); err != nil {
		return result, err
	}
	status, err := client.RefreshStatus(ctx)
	if err != nil {
		return result, err
	}
	if d.WaitForRefresh {
		status, err = d.waitForCurrentRefresh(ctx, client, status, now)
		if err != nil {
			return result, err
		}
	}
	if !NativeRefreshCurrent(status, now()) && !d.ManualSyncAuthorized {
		return result, errors.New("discovery blocked: today's Asia/Shanghai native refresh is not complete; no account sync was sent")
	}
	scope, err := resolveAccountScope(ctx, cfg, client)
	if err != nil {
		return result, err
	}
	doc, err := report.New("discover")
	if err != nil {
		return result, err
	}
	doc.TargetFingerprint = cfg.Target.RuntimeCompatibility.BuildID
	doc.AccountScope = cfg.AccountScopeValue()
	if cfg.AllAccounts() {
		doc.AccountScanStatus = "complete"
	}
	doc.AccountInventoryCount = scope.Count
	doc.AccountInventorySHA256 = scope.SHA256
	doc.RefreshRunStatus = safeStatus(status.RunStatus)
	doc.RefreshFinishedAt = status.LastFinishedAt
	doc.NativeSyncSideEffect = true
	doc.ManualNativeSyncOverride = d.ManualSyncAuthorized
	accountsByID := make(map[int64]adminapi.AccountRunStatus, len(status.Accounts))
	for _, item := range status.Accounts {
		accountsByID[item.AccountID] = item
	}
	approval := catalog.ApprovalPlan{
		SchemaVersion: catalog.ApprovalPlanSchemaVersion,
		CreatedAt:     now().UTC(),
		ExpiresAt:     now().UTC().Add(catalog.ApprovalPlanTTL),
		Target:        catalog.TargetBinding{Origin: normalizedOrigin(cfg.Target.BaseURL), BuildID: cfg.Target.RuntimeCompatibility.BuildID, APIContractID: cfg.Target.RuntimeCompatibility.APIContractID},
		AccountScope:  cfg.AccountScopeValue(),
		RulesSHA256:   rulesHash,
	}
	if cfg.AllAccounts() {
		approval.AccountInventoryCount = scope.Count
		approval.AccountInventorySHA256 = scope.SHA256
	}
	accountIDs := cfg.IDs()
	if cfg.AllAccounts() {
		accountIDs = scope.IDs
	}
	accountReadFailure := false
	for _, accountID := range accountIDs {
		if !cfg.AllAccounts() && !cfg.AcceptsCredentialMemoryExposure(accountID) {
			doc.Discovery = append(doc.Discovery, report.DiscoveryAccount{ID: accountID, SourceProfileStatus: "not_checked", Status: "blocked", Reason: "credential-response memory exposure was not accepted", CatalogCompleteness: string(catalog.CompletenessUnknown)})
			continue
		}
		if status.RunStatus == "partial_failure" && !d.ManualSyncAuthorized {
			row, found := accountsByID[accountID]
			if !found || (row.Status != "completed" && row.Status != "succeeded") {
				doc.Discovery = append(doc.Discovery, report.DiscoveryAccount{ID: accountID, SourceProfileStatus: "not_checked", Status: "skipped_refresh_failure", Reason: "selected account did not report a successful native refresh", CatalogCompleteness: string(catalog.CompletenessUnknown)})
				continue
			}
		}
		account, err := client.Account(ctx, accountID)
		if err != nil {
			if cfg.AllAccounts() {
				accountReadFailure = true
				doc.AccountScanStatus = "partial"
				doc.Discovery = append(doc.Discovery, report.DiscoveryAccount{
					ID: accountID, SourceProfileStatus: "not_checked", Status: "account_read_failed",
					Reason:              "account details could not be read; no plan will be generated for a partial all-account scan",
					CatalogCompleteness: string(catalog.CompletenessUnknown),
				})
				continue
			}
			return result, err
		}
		baseRow := report.DiscoveryAccount{ID: accountID, SourceProfileStatus: "not_checked", Status: "skipped", CatalogCompleteness: string(catalog.CompletenessUnknown)}
		if account.ParentAccountID != nil {
			baseRow.Status, baseRow.Reason = "skipped_shadow", "credential shadow accounts are not eligible for discovery"
			doc.Discovery = append(doc.Discovery, baseRow)
			continue
		}
		if !account.HasModelMapping() || len(account.ModelMapping) == 0 {
			baseRow.Status = "skipped_unrestricted_mapping"
			baseRow.Reason = "account mapping is absent or explicitly empty; native unrestricted behavior is preserved and no mapping plan is generated"
			doc.Discovery = append(doc.Discovery, baseRow)
			continue
		}
		source, err := catalog.SourceForAccount(account.Platform, account.Type, account.Protocol, account.BaseURL, account.ID)
		if err != nil {
			baseRow.Reason = "account source identity is incomplete or unsafe"
			doc.Discovery = append(doc.Discovery, baseRow)
			continue
		}
		baseRow.SourceFingerprint = catalog.SourceFingerprint(source)
		profile, matched := rules.MatchSourceProfile(source, cfg.Target.RuntimeCompatibility.BuildID, cfg.Target.RuntimeCompatibility.APIContractID)
		if !matched {
			baseRow.SourceProfileStatus = "not_allowlisted_for_target"
			baseRow.Reason = "no exact approved source profile matches source, build, and API contract; no sync request sent"
			doc.Discovery = append(doc.Discovery, baseRow)
			continue
		}
		baseRow.SourceProfileStatus = "approved"
		baseRow.IdentityAddPolicy = profile.IdentityAddPolicy
		catalogResult, err := client.SyncUpstream(ctx, accountID)
		if err != nil {
			baseRow.Status, baseRow.Reason = "sync_failed", "native upstream catalog sync failed; mapping was not changed"
			doc.Discovery = append(doc.Discovery, baseRow)
			continue
		}
		warningCodes := warningCodesOnly(catalogResult.Warnings)
		baseRow.LiveListAvailable = catalogResult.LiveListAvailable
		baseRow.Models = append([]string{}, catalogResult.Models...)
		baseRow.Warnings = warningCodes
		if !catalogResult.LiveListAvailable || len(catalogResult.Models) == 0 {
			baseRow.Status, baseRow.Reason = "catalog_unknown", "response was fallback, unavailable, or empty; no mapping mutation is planned"
			doc.Discovery = append(doc.Discovery, baseRow)
			continue
		}
		directoryHash, err := catalog.DirectoryDigest(catalogResult.Models, catalogResult.LiveListAvailable, warningCodes)
		if err != nil {
			baseRow.Status, baseRow.Reason = "catalog_invalid", "catalog IDs or warning metadata are invalid; no mapping mutation is planned"
			doc.Discovery = append(doc.Discovery, baseRow)
			continue
		}
		observedAt := now().UTC()
		baseRow.CatalogHash = directoryHash
		baseRow.CatalogObservedAt = &observedAt
		baseRow.Status = "candidate_reported"
		completeProfile, hasCompleteProfile := rules.MatchCompleteCatalogProfile(source)
		completeness := catalog.CompletenessUnknown
		completeProfileID := ""
		if hasCompleteProfile {
			completeness = catalog.CompletenessComplete
			completeProfileID = completeProfile.ProfileID
		}
		baseRow.CatalogCompleteness = string(completeness)
		local := localState.Accounts[catalog.AccountKey(account.ID)]
		plan, err := catalog.Normalize(rules, catalog.Input{
			AccountID: account.ID, Source: source, SourceIdentityAddPolicy: profile.IdentityAddPolicy,
			CurrentMapping: account.ModelMapping, ManagedMappings: local.ManagedMappings,
			LiveModels: catalogResult.Models, LiveListAvailable: catalogResult.LiveListAvailable,
			Completeness: completeness, CompletenessProfileID: completeProfileID, DirectoryHash: directoryHash,
		}, local)
		if err != nil {
			return result, err
		}
		plan.CatalogObservedAt = observedAt
		plan.CompletenessProfileID = completeProfileID
		plan.CatalogWarningCodes = append([]string{}, warningCodes...)
		if catalogWarningCodesBlockMappingApply(warningCodes) {
			plan.ApplyEligible = false
			plan.DiscoveryReason = "catalog response contains warnings that may affect the model-ID list"
		}
		baseRow.Changes = make([]report.MappingChange, 0, len(plan.Changes))
		for _, change := range plan.Changes {
			baseRow.Changes = append(baseRow.Changes, report.MappingChange{Action: change.Action, PublicID: change.PublicID, Before: change.Before, After: change.After, RuleKind: change.RuleKind, Evidence: change.Evidence, Reason: change.Reason})
		}
		baseRow.Conflicts = append([]string{}, plan.Conflicts...)
		baseRow.UnreviewedModels = append([]string{}, plan.UnreviewedModels...)
		baseRow.CatalogCompleteness = string(plan.Completeness)
		doc.Discovery = append(doc.Discovery, baseRow)
		local.DirectoryHash = directoryHash
		local.RulesHash = rulesHash
		local.AbsenceCounts = cloneCounterMap(plan.AbsenceCountsAfter)
		local.CatalogSnapshot = &state.CatalogSnapshot{Hash: directoryHash, ObservedAt: observedAt, Completeness: string(plan.Completeness), CompleteProfileID: completeProfileID, LiveListAvailable: catalogResult.LiveListAvailable, SourceFingerprint: plan.SourceFingerprint}
		successAt := observedAt
		local.LastSuccessAt = &successAt
		localState.Accounts[catalog.AccountKey(account.ID)] = local
		approval.Accounts = append(approval.Accounts, plan)
	}
	if !accountReadFailure {
		if err := state.Save(statePath, localState); err != nil {
			return result, err
		}
		stateHash, err := state.Hash(statePath)
		if err != nil {
			return result, err
		}
		approval.StateSHA256 = stateHash
		if len(approval.Accounts) > 0 {
			if err := approval.ValidateShape(now().UTC()); err != nil {
				return result, fmt.Errorf("generated plan failed its own validation: %w", err)
			}
			body, err := json.MarshalIndent(approval, "", "  ")
			if err != nil {
				return result, errors.New("approval plan could not be serialized")
			}
			path, digest, err := report.WritePlan(cfg.ReportDir, body)
			if err != nil {
				return result, err
			}
			result.Plan = &approval
			result.PlanPath = path
			result.PlanSHA256 = digest
		}
	}
	doc.Sort()
	result.Report = doc
	return result, nil
}

func cloneCounterMap(src map[string]int) map[string]int {
	if src == nil {
		return nil
	}
	dst := make(map[string]int, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func (d Discoverer) waitForCurrentRefresh(ctx context.Context, client AdminAPI, current adminapi.RefreshStatus, now func() time.Time) (adminapi.RefreshStatus, error) {
	maxWait := d.RefreshWait
	if maxWait <= 0 {
		maxWait = 20 * time.Minute
	}
	poll := d.PollInterval
	if poll <= 0 {
		poll = 30 * time.Second
	}
	deadline := now().Add(maxWait)
	for !NativeRefreshCurrent(current, now()) {
		if !now().Before(deadline) {
			return adminapi.RefreshStatus{}, errors.New("run stopped: today's Asia/Shanghai native refresh did not complete before the bounded wait expired")
		}
		sleep := d.Sleep
		if sleep == nil {
			sleep = func(ctx context.Context, duration time.Duration) error {
				timer := time.NewTimer(duration)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-timer.C:
					return nil
				}
			}
		}
		if err := sleep(ctx, poll); err != nil {
			return adminapi.RefreshStatus{}, errors.New("run stopped while waiting for native refresh")
		}
		var err error
		current, err = client.RefreshStatus(ctx)
		if err != nil {
			return adminapi.RefreshStatus{}, err
		}
	}
	return current, nil
}

func NativeRefreshCurrent(status adminapi.RefreshStatus, now time.Time) bool {
	if !status.Enabled || status.LastFinishedAt == nil || status.Timezone != "Asia/Shanghai" {
		return false
	}
	if status.RunStatus != "completed" && status.RunStatus != "partial_failure" {
		return false
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return false
	}
	finished := status.LastFinishedAt.In(loc)
	return !finished.After(now.In(loc)) && finished.Format("2006-01-02") == now.In(loc).Format("2006-01-02")
}

func warningCodesOnly(warnings []adminapi.CatalogWarning) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, warning := range warnings {
		code := warning.Code
		if !safeWarningCodePattern.MatchString(code) {
			code = "unclassified_warning"
		}
		if !seen[code] {
			seen[code] = true
			out = append(out, code)
		}
	}
	sort.Strings(out)
	return out
}

func catalogWarningCodesBlockMappingApply(codes []string) bool {
	for _, code := range codes {
		switch code {
		case warningModelMetadataIncomplete, warningModelMetadataPartial:
			// These native warnings describe capability metadata only. They do not
			// block an exact reviewed mapping, but never prove catalog completeness.
		default:
			return true
		}
	}
	return false
}

func normalizedOrigin(raw string) string {
	u, err := config.ValidateBaseURL(raw)
	if err != nil {
		return ""
	}
	return u.String()
}
