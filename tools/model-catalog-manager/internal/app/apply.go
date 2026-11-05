package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"time"

	"sub2api-model-catalog-manager/internal/adminapi"
	"sub2api-model-catalog-manager/internal/catalog"
	"sub2api-model-catalog-manager/internal/config"
	"sub2api-model-catalog-manager/internal/state"
)

type Applier struct {
	ReadCredential CredentialReader
	NewClient      ClientFactory
	Now            func() time.Time
}

type ApplyResult struct {
	PlanSHA256 string
	AppliedIDs []int64
	NoopIDs    []int64
	Consumed   bool
}

// Apply requires an exact raw plan digest for manual use. Automatic use is
// available only to Run after the config's approved raw rules hash matched.
func (a Applier) Apply(ctx context.Context, cfg config.Config, plan catalog.ApprovalPlan, planSHA256, confirmation string, automatic bool) (ApplyResult, error) {
	var result ApplyResult
	result.PlanSHA256 = planSHA256
	if err := cfg.Validate(); err != nil {
		return result, err
	}
	if !cfg.RuntimeCompatible() {
		return result, errors.New("apply blocked: target runtime compatibility is not verified")
	}
	if !automatic && (confirmation == "" || confirmation != planSHA256) {
		return result, errors.New("apply blocked: exact --confirm-plan-sha256 is required")
	}
	if automatic && cfg.ApplyModeValue() != "approved_rules" {
		return result, errors.New("automatic apply blocked: apply_mode is not approved_rules")
	}
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	if err := plan.ValidateShape(now().UTC()); err != nil {
		return result, err
	}
	targetOrigin := normalizedOrigin(cfg.Target.BaseURL)
	if plan.Target.Origin != targetOrigin || plan.Target.BuildID != cfg.Target.RuntimeCompatibility.BuildID || plan.Target.APIContractID != cfg.Target.RuntimeCompatibility.APIContractID {
		return result, errors.New("apply blocked: plan target binding differs from current compatibility record")
	}
	if plan.AccountScope != cfg.AccountScopeValue() {
		return result, errors.New("apply blocked: plan account scope differs from current configuration")
	}
	rules, rulesHash, err := catalog.LoadRulesWithHash(cfg.RulesFile)
	if err != nil {
		return result, err
	}
	if rulesHash != plan.RulesSHA256 {
		return result, errors.New("apply blocked: raw rules hash drifted")
	}
	if automatic && (cfg.ApprovedRulesSHA256 == "" || cfg.ApprovedRulesSHA256 != rulesHash) {
		return result, errors.New("automatic apply blocked: approved_rules_sha256 does not match current raw rules file")
	}
	accountIDs := make([]int64, 0, len(plan.Accounts))
	for _, p := range plan.Accounts {
		accountIDs = append(accountIDs, p.AccountID)
	}
	if err := cfg.ExposureGateFor(accountIDs); err != nil {
		return result, err
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
	if _, used := localState.ConsumedPlans[planSHA256]; used {
		return result, errors.New("apply blocked: this exact plan was already consumed")
	}
	stateHash, err := state.Hash(statePath)
	if err != nil || stateHash != plan.StateSHA256 {
		return result, errors.New("apply blocked: local state hash drifted")
	}
	if a.ReadCredential == nil || a.NewClient == nil {
		return result, errors.New("apply dependencies are unavailable")
	}
	cred, err := a.ReadCredential(cfg.Credential)
	if err != nil {
		return result, errors.New("administrator credential could not be read from the configured OS store")
	}
	defer cred.Clear()
	client, err := a.NewClient(cfg.Target.BaseURL)
	if err != nil {
		return result, err
	}
	defer client.Close()
	if err := client.SetAPIKey(cred.APIKey); err != nil {
		return result, err
	}
	if cfg.AllAccounts() {
		inventory, inventoryErr := client.ListAllAccounts(ctx)
		if inventoryErr != nil {
			return result, errors.New("apply blocked: complete account inventory could not be re-read")
		}
		ids := make([]int64, len(inventory))
		for i := range inventory {
			ids[i] = inventory[i].ID
		}
		digest, digestErr := catalog.AccountIDsDigest(ids)
		if digestErr != nil || len(inventory) != plan.AccountInventoryCount || digest != plan.AccountInventorySHA256 {
			return result, errors.New("apply blocked: account inventory changed after discovery")
		}
		present := make(map[int64]bool, len(inventory))
		for _, account := range inventory {
			present[account.ID] = true
		}
		for _, planned := range plan.Accounts {
			if !present[planned.AccountID] {
				return result, errors.New("apply blocked: plan includes an account outside the current inventory")
			}
		}
	}
	prepared := make([]preparedApply, 0, len(plan.Accounts))
	seen := map[int64]bool{}
	for _, p := range plan.Accounts {
		if seen[p.AccountID] || len(p.Conflicts) > 0 || !p.ApplyEligible {
			return result, errors.New("apply blocked: plan has conflicts, unsafe catalog status, or duplicate account")
		}
		seen[p.AccountID] = true
		account, err := client.Account(ctx, p.AccountID)
		if err != nil {
			return result, err
		}
		if !account.HasModelMapping() || len(account.ModelMapping) == 0 {
			return result, errors.New("apply blocked: account mapping is absent or empty; automatic initialization is not allowed")
		}
		if account.ParentAccountID != nil {
			return result, errors.New("apply blocked: credential shadow account")
		}
		source, err := catalog.SourceForAccount(account.Platform, account.Type, account.Protocol, account.BaseURL, account.ID)
		if err != nil || catalog.SourceFingerprint(source) != p.SourceFingerprint || !reflect.DeepEqual(source, p.Source) {
			return result, errors.New("apply blocked: account source fingerprint drifted")
		}
		profile, ok := rules.MatchSourceProfile(source, cfg.Target.RuntimeCompatibility.BuildID, cfg.Target.RuntimeCompatibility.APIContractID)
		if !ok || profile.IdentityAddPolicy != p.SourceIdentityAddPolicy {
			return result, errors.New("apply blocked: source profile no longer matches plan")
		}
		if !reflect.DeepEqual(account.ModelMapping, p.Before) {
			return result, errors.New("apply blocked: complete model mapping drifted")
		}
		local := localState.Accounts[catalog.AccountKey(account.ID)]
		if !snapshotMatches(local.CatalogSnapshot, p) {
			return result, errors.New("apply blocked: latest local catalog snapshot differs from plan")
		}
		if !sameCountMap(local.AbsenceCounts, p.AbsenceCountsAfter) {
			return result, errors.New("apply blocked: persisted absence counters differ from approved plan")
		}
		directoryHash, err := catalog.DirectoryDigest(p.CatalogModels, p.LiveListAvailable, p.CatalogWarningCodes)
		if err != nil || directoryHash != p.DirectoryHash {
			return result, errors.New("apply blocked: catalog content hash differs from plan")
		}
		complete := catalog.CompletenessUnknown
		profileID := ""
		if completeProfile, found := rules.MatchCompleteCatalogProfile(source); found {
			complete = catalog.CompletenessComplete
			profileID = completeProfile.ProfileID
		}
		priorForNormalize := local
		priorForNormalize.AbsenceCounts = cloneCounterMap(p.AbsenceCountsBefore)
		verified, err := catalog.Normalize(rules, catalog.Input{
			AccountID: account.ID, Source: source, SourceIdentityAddPolicy: profile.IdentityAddPolicy,
			CurrentMapping: account.ModelMapping, ManagedMappings: local.ManagedMappings,
			LiveModels: p.CatalogModels, LiveListAvailable: p.LiveListAvailable,
			Completeness: complete, CompletenessProfileID: profileID, DirectoryHash: p.DirectoryHash,
		}, priorForNormalize)
		if err != nil {
			return result, err
		}
		verified.CatalogObservedAt = p.CatalogObservedAt
		verified.CompletenessProfileID = profileID
		verified.CatalogWarningCodes = append([]string{}, p.CatalogWarningCodes...)
		if catalogWarningCodesBlockMappingApply(p.CatalogWarningCodes) {
			verified.ApplyEligible = false
			verified.DiscoveryReason = "catalog response contains warnings that may affect the model-ID list"
		}
		if !samePlanDelta(verified, p) {
			return result, errors.New("apply blocked: mapping delta does not match current exact rules and state")
		}
		prepared = append(prepared, preparedApply{plan: p, beforeAccount: account})
	}

	changedIDs := make([]int64, 0, len(prepared))
	for _, item := range prepared {
		if !reflect.DeepEqual(item.plan.Before, item.plan.After) {
			changedIDs = append(changedIDs, item.plan.AccountID)
		}
	}
	if len(changedIDs) == 0 {
		for _, item := range prepared {
			result.NoopIDs = append(result.NoopIDs, item.plan.AccountID)
		}
		return result, nil
	}
	// Mapping-only backups for the whole plan are durable before the consumption
	// marker and before any remote mutation.
	for _, item := range prepared {
		if reflect.DeepEqual(item.plan.Before, item.plan.After) {
			continue
		}
		if _, err := state.WriteMappingBackup(cfg.BackupDir, item.plan.AccountID, planSHA256, item.plan.SourceFingerprint, item.plan.Before, now()); err != nil {
			return result, err
		}
	}
	localState.ConsumedPlans[planSHA256] = state.ConsumedPlan{ConsumedAt: now().UTC(), AccountIDs: append([]int64{}, changedIDs...)}
	if err := state.Save(statePath, localState); err != nil {
		return result, err
	}
	stateHash, err = state.Hash(statePath)
	if err != nil {
		return result, err
	}
	result.Consumed = true
	for _, item := range prepared {
		p := item.plan
		if reflect.DeepEqual(p.Before, p.After) {
			result.NoopIDs = append(result.NoopIDs, p.AccountID)
			continue
		}
		currentStateHash, hashErr := state.Hash(statePath)
		if hashErr != nil || currentStateHash != stateHash {
			return result, errors.New("apply stopped: local state changed during the approved write window")
		}
		// The initial preflight covered every plan entry, but earlier writes can
		// leave a later account exposed to an external edit while this plan is in
		// progress. Re-read the complete source and mapping immediately before
		// each independent single-account write.
		immediate, readErr := client.Account(ctx, p.AccountID)
		if readErr != nil {
			return result, errors.New("apply stopped: account could not be re-read immediately before mapping update")
		}
		if immediate.ParentAccountID != nil || !sameAccountSource(item.beforeAccount, immediate) || !reflect.DeepEqual(immediate.ModelMapping, p.Before) {
			return result, errors.New("apply stopped: account source or complete mapping drifted immediately before mapping update")
		}
		response, writeErr := client.BulkUpdateMapping(ctx, p.AccountID, p.After)
		if errors.Is(writeErr, adminapi.ErrUnexpectedBulkResult) {
			return result, errors.New("apply stopped: bulk-update returned failed or unexpected account IDs; no retry was sent")
		}
		readback, readErr := client.Account(ctx, p.AccountID)
		if readErr != nil {
			return result, errors.New("apply stopped: write result could not be verified by account readback")
		}
		if !sameAccountSource(item.beforeAccount, readback) {
			return result, errors.New("apply stopped: account identity or connection metadata changed during write")
		}
		if !reflect.DeepEqual(readback.ModelMapping, p.After) {
			if writeErr != nil {
				return result, errors.New("apply stopped: write outcome is uncertain and mapping does not match; plan consumed, no retry sent")
			}
			return result, errors.New("apply stopped: account readback does not match approved mapping")
		}
		if writeErr == nil && (len(response.SuccessIDs) != 1 || response.SuccessIDs[0] != p.AccountID || len(response.FailedIDs) != 0) {
			return result, errors.New("apply stopped: bulk-update result did not identify exactly the selected account")
		}
		// The remote mapping is now verified even if the local ownership-state
		// save below fails. Preserve that partial-success evidence in the result.
		result.AppliedIDs = append(result.AppliedIDs, p.AccountID)
		applyManagedChanges(&localState, p)
		if err := state.Save(statePath, localState); err != nil {
			return result, errors.New("mapping was verified, but local ownership state could not be saved; plan remains consumed")
		}
		stateHash, err = state.Hash(statePath)
		if err != nil {
			return result, errors.New("mapping was verified, but updated state hash could not be read; plan remains consumed")
		}
	}
	return result, nil
}

type preparedApply struct {
	plan          catalog.Plan
	beforeAccount adminapi.Account
}

func snapshotMatches(snapshot *state.CatalogSnapshot, p catalog.Plan) bool {
	return snapshot != nil && snapshot.Hash == p.DirectoryHash && snapshot.ObservedAt.Equal(p.CatalogObservedAt) && snapshot.Completeness == string(p.Completeness) && snapshot.CompleteProfileID == p.CompletenessProfileID && snapshot.LiveListAvailable == p.LiveListAvailable && snapshot.SourceFingerprint == p.SourceFingerprint
}

func samePlanDelta(a, b catalog.Plan) bool {
	return a.AccountID == b.AccountID && a.SourceFingerprint == b.SourceFingerprint && a.BeforeHash == b.BeforeHash && a.DirectoryHash == b.DirectoryHash && a.RulesHash == b.RulesHash && a.Completeness == b.Completeness && a.LiveListAvailable == b.LiveListAvailable && a.ApplyEligible == b.ApplyEligible && a.SourceIdentityAddPolicy == b.SourceIdentityAddPolicy && a.CompletenessProfileID == b.CompletenessProfileID && reflect.DeepEqual(a.Before, b.Before) && reflect.DeepEqual(a.After, b.After) && reflect.DeepEqual(a.AbsenceCountsBefore, b.AbsenceCountsBefore) && reflect.DeepEqual(a.AbsenceCountsAfter, b.AbsenceCountsAfter) && reflect.DeepEqual(a.Changes, b.Changes) && reflect.DeepEqual(a.Conflicts, b.Conflicts) && reflect.DeepEqual(a.UnreviewedModels, b.UnreviewedModels) && reflect.DeepEqual(a.CatalogModels, b.CatalogModels) && reflect.DeepEqual(a.CatalogWarningCodes, b.CatalogWarningCodes)
}

func sameCountMap(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if other, ok := b[key]; !ok || other != value {
			return false
		}
	}
	return true
}

func sameAccountSource(before, after adminapi.Account) bool {
	if before.ID != after.ID || before.Platform != after.Platform || before.Type != after.Type || before.Protocol != after.Protocol || before.BaseURL != after.BaseURL || before.Status != after.Status || before.HasModelMapping() != after.HasModelMapping() {
		return false
	}
	// The account DTO's redacted marker is the only safe way to prove that the
	// credentials-presence state survived a mapping-only update. Missing markers
	// are not evidence of preservation, including when both reads omit them.
	if before.APIKeyPresent == nil || after.APIKeyPresent == nil || *before.APIKeyPresent != *after.APIKeyPresent {
		return false
	}
	if before.ParentAccountID == nil || after.ParentAccountID == nil {
		return before.ParentAccountID == nil && after.ParentAccountID == nil
	}
	return *before.ParentAccountID == *after.ParentAccountID
}

func applyManagedChanges(s *state.State, p catalog.Plan) {
	key := catalog.AccountKey(p.AccountID)
	account := s.Accounts[key]
	if account.ManagedMappings == nil {
		account.ManagedMappings = map[string]state.ManagedMapping{}
	}
	for _, change := range p.Changes {
		switch change.Action {
		case "remove":
			delete(account.ManagedMappings, change.PublicID)
			delete(account.AbsenceCounts, change.PublicID)
		case "add", "update":
			prior, exists := account.ManagedMappings[change.PublicID]
			original := prior.OriginalValue
			if !exists {
				original = ""
			}
			account.ManagedMappings[change.PublicID] = state.ManagedMapping{OriginalValue: original, LastAppliedValue: change.After, SourceFingerprint: p.SourceFingerprint}
			if account.AbsenceCounts != nil {
				account.AbsenceCounts[change.PublicID] = 0
			}
		}
	}
	if len(account.AbsenceCounts) == 0 {
		account.AbsenceCounts = nil
	}
	account.DirectoryHash = p.DirectoryHash
	account.RulesHash = p.RulesHash
	s.Accounts[key] = account
}

func (r ApplyResult) String() string {
	return fmt.Sprintf("applied=%d no_op=%d consumed=%v", len(r.AppliedIDs), len(r.NoopIDs), r.Consumed)
}
