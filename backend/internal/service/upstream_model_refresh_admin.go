package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrUpstreamModelRefreshRunNotFound = errors.New("upstream model refresh run not found")

type UpstreamModelRefreshStatus struct {
	Enabled         bool                            `json:"enabled"`
	Schedule        string                          `json:"schedule"`
	Timezone        string                          `json:"timezone"`
	LeaderActive    bool                            `json:"leader_active"`
	RunStatus       string                          `json:"last_run_status"`
	StatusSource    string                          `json:"status_source"`
	LastRunID       string                          `json:"last_run_id,omitempty"`
	LastScheduledAt *time.Time                      `json:"last_scheduled_at,omitempty"`
	LastStartedAt   *time.Time                      `json:"last_started_at,omitempty"`
	LastFinishedAt  *time.Time                      `json:"last_finished_at,omitempty"`
	Eligible        int                             `json:"eligible"`
	Succeeded       int                             `json:"succeeded"`
	Failed          int                             `json:"failed"`
	Unsupported     int                             `json:"unsupported"`
	Skipped         int                             `json:"skipped"`
	Accounts        []UpstreamModelAccountRunStatus `json:"accounts,omitempty"`
}

type UpstreamModelPolicyPreviewAccount struct {
	AccountID            int64                                `json:"account_id"`
	CurrentPolicy        string                               `json:"current_policy"`
	SourceProfileID      string                               `json:"source_profile_id"`
	Eligible             bool                                 `json:"eligible"`
	IneligibleReason     string                               `json:"ineligible_reason,omitempty"`
	AdapterReady         bool                                 `json:"adapter_ready"`
	SnapshotStatus       string                               `json:"snapshot_status"`
	SnapshotAgeSeconds   int64                                `json:"snapshot_age_seconds,omitempty"`
	ManualMappingCount   int                                  `json:"manual_mapping_count"`
	AvailableModelCount  int                                  `json:"available_model_count"`
	AvailableModelIDs    []string                             `json:"available_model_ids_preview,omitempty"`
	AddedModelCount      int                                  `json:"added_model_count"`
	AddedModelIDs        []string                             `json:"added_model_ids_preview,omitempty"`
	RetainedModelCount   int                                  `json:"retained_model_count"`
	RetainedModelIDs     []string                             `json:"retained_model_ids_preview,omitempty"`
	HiddenModelCount     int                                  `json:"hidden_model_count"`
	HiddenModelIDs       []string                             `json:"hidden_model_ids_preview,omitempty"`
	MappingConflictCount int                                  `json:"mapping_conflict_count"`
	MappingConflicts     []UpstreamModelPolicyMappingConflict `json:"mapping_conflicts,omitempty"`
	Groups               []UpstreamModelPolicyPreviewGroup    `json:"groups,omitempty"`
}

type UpstreamModelPolicyMappingConflict struct {
	PublicModelID           string `json:"public_model_id,omitempty"`
	ConfiguredTargetPreview string `json:"configured_target_preview,omitempty"`
	Reason                  string `json:"reason"`
}

type UpstreamModelPolicyPreviewGroup struct {
	GroupID               int64 `json:"group_id"`
	AllowlistEnabled      bool  `json:"allowlist_enabled"`
	AvailableModelCount   int   `json:"available_model_count"`
	AllowlistedModelCount int   `json:"allowlisted_model_count"`
}

type UpstreamModelPolicyPreview struct {
	PreviewID  string                              `json:"preview_id"`
	PlanHash   string                              `json:"plan_hash"`
	ExpiresAt  time.Time                           `json:"expires_at"`
	AccountIDs []int64                             `json:"account_ids"`
	Eligible   int                                 `json:"eligible"`
	Ineligible int                                 `json:"ineligible"`
	Accounts   []UpstreamModelPolicyPreviewAccount `json:"accounts"`
}

type policyPlanHashBody struct {
	PreviewID  string                               `json:"preview_id"`
	ExpiresAt  time.Time                            `json:"expires_at"`
	AccountIDs []int64                              `json:"account_ids"`
	Revisions  []UpstreamModelPolicyAccountRevision `json:"revisions"`
}

func (s *UpstreamModelRefreshService) PreviewAccountPolicies(ctx context.Context, accountIDs []int64) (*UpstreamModelPolicyPreview, error) {
	if s == nil || s.accounts == nil || s.state == nil || len(accountIDs) == 0 || len(accountIDs) > 100 {
		return nil, errors.New("model policy preview is unavailable")
	}
	ids := append([]int64(nil), accountIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for i, id := range ids {
		if id <= 0 || (i > 0 && id == ids[i-1]) {
			return nil, errors.New("invalid model policy preview account set")
		}
	}
	previewID, err := newRefreshLeaseOwner()
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Second)
	preview := &UpstreamModelPolicyPreview{
		PreviewID: previewID, ExpiresAt: expiresAt, AccountIDs: ids,
		Accounts: make([]UpstreamModelPolicyPreviewAccount, 0, len(ids)),
	}
	plan := UpstreamModelPolicyPreviewPlan{PreviewID: previewID, ExpiresAt: expiresAt, AccountIDs: ids}
	for _, id := range ids {
		account, err := s.accounts.GetByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("load model policy preview account: %w", err)
		}
		row, revision := buildUpstreamModelPolicyPreviewAccount(account, time.Now().UTC())
		preview.Accounts = append(preview.Accounts, row)
		plan.Revisions = append(plan.Revisions, revision)
		if row.Eligible {
			preview.Eligible++
		} else {
			preview.Ineligible++
		}
	}
	hashBody := policyPlanHashBody{PreviewID: plan.PreviewID, ExpiresAt: plan.ExpiresAt, AccountIDs: plan.AccountIDs, Revisions: plan.Revisions}
	encoded, err := json.Marshal(hashBody)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	plan.PlanHash = "sha256:" + hex.EncodeToString(digest[:])
	preview.PlanHash = plan.PlanHash
	if err := s.state.StoreUpstreamModelPolicyPreview(ctx, plan); err != nil {
		return nil, fmt.Errorf("store model policy preview: %w", err)
	}
	return preview, nil
}

func buildUpstreamModelPolicyPreviewAccount(account *Account, now time.Time) (UpstreamModelPolicyPreviewAccount, UpstreamModelPolicyAccountRevision) {
	row := UpstreamModelPolicyPreviewAccount{CurrentPolicy: UpstreamModelPolicyManual, SnapshotStatus: "never_fetched"}
	revision := UpstreamModelPolicyAccountRevision{CurrentPolicy: UpstreamModelPolicyManual}
	if account == nil {
		row.IneligibleReason = "account_unavailable"
		return row, revision
	}
	profile := DetectUpstreamModelSourceProfile(account)
	row.AccountID = account.ID
	row.CurrentPolicy = account.GetUpstreamModelPolicy()
	row.SourceProfileID = profile.ID
	row.AdapterReady = !profile.ManualOnly
	row.ManualMappingCount = len(account.GetModelMapping())
	revision.AccountID = account.ID
	revision.UpdatedAt = account.UpdatedAt.UTC()
	revision.CurrentPolicy = row.CurrentPolicy
	revision.SourceProfileID = profile.ID
	revision.MappingDigest = digestJSON(account.GetModelMapping())
	snapshotPayload, snapshotPresent := account.Extra[UpstreamModelAvailabilityExtraKey]
	snapshot := account.GetUpstreamModelAvailabilitySnapshot()
	if snapshot != nil {
		row.SnapshotStatus = safePolicyPreviewSnapshotStatus(snapshot.Status)
		if snapshot.LastSuccessAt != nil {
			if age := now.Sub(*snapshot.LastSuccessAt); age >= 0 {
				row.SnapshotAgeSeconds = int64(age.Seconds())
			}
		}
		// The plan must become stale if route/lifecycle data changes while raw IDs
		// stay the same, so bind it to the complete snapshot value.
		revision.SnapshotDigest = digestJSON(snapshot)
	} else if snapshotPresent && snapshotPayload != nil {
		row.SnapshotStatus = "incomplete"
		revision.SnapshotDigest = digestJSON(snapshotPayload)
	}
	setIneligible := func(reason string) {
		if row.IneligibleReason == "" {
			row.IneligibleReason = reason
		}
	}
	if profile.ManualOnly {
		setIneligible("source_manual_only")
	} else if account.Type != AccountTypeAPIKey {
		setIneligible("account_type_unsupported")
	} else if !account.IsActive() {
		setIneligible("account_inactive")
	}

	var safeCatalogModels []string
	snapshotTrusted := false
	var err error
	if row.IneligibleReason == "" {
		switch {
		case snapshot == nil:
			if snapshotPresent && snapshotPayload != nil {
				setIneligible("snapshot_incomplete")
			} else {
				setIneligible("snapshot_never_fetched")
			}
		case snapshot.SourceProfileID != profile.ID:
			setIneligible("snapshot_source_mismatch")
		case snapshot.Status != "fresh" && snapshot.Status != "stale":
			setIneligible("snapshot_not_successful")
		case snapshot.LastSuccessAt == nil:
			setIneligible("snapshot_incomplete")
		default:
			age := now.Sub(*snapshot.LastSuccessAt)
			switch {
			case age < 0:
				setIneligible("snapshot_incomplete")
			case age > upstreamAvailabilityStaleGrace:
				row.SnapshotStatus = "expired"
				setIneligible("snapshot_expired")
			default:
				var hidden, quarantined []string
				safeCatalogModels, hidden, quarantined, err = trustedPolicyPreviewModels(snapshot, profile, now)
				if err != nil {
					setIneligible("snapshot_incomplete")
				} else {
					snapshotTrusted = true
					row.HiddenModelIDs = append(row.HiddenModelIDs, hidden...)
					row.HiddenModelIDs = append(row.HiddenModelIDs, quarantined...)
					if len(hidden) > 0 {
						setIneligible("snapshot_contains_retired_model")
					} else if len(quarantined) > 0 {
						setIneligible("snapshot_contains_quarantined_model")
					} else if len(safeCatalogModels) == 0 {
						setIneligible("snapshot_no_routable_models")
					}
				}
			}
		}
	}

	// Do not present model IDs as available unless the account has a complete,
	// source-matched, successful snapshot still inside the stale grace window.
	if row.IneligibleReason == "" || snapshotTrusted {
		available := make(map[string]struct{}, len(safeCatalogModels))
		for _, modelID := range safeCatalogModels {
			available[modelID] = struct{}{}
		}
		retained := make(map[string]struct{})
		retainedRoutes := make(map[string]string)
		conflictedPublicIDs := make(map[string]struct{})
		hidden := make(map[string]struct{}, len(row.HiddenModelIDs))
		for _, modelID := range row.HiddenModelIDs {
			hidden[modelID] = struct{}{}
		}
		mapping := account.GetModelMapping()
		mappingIDs := make([]string, 0, len(mapping))
		for modelID := range mapping {
			mappingIDs = append(mappingIDs, modelID)
		}
		sort.Strings(mappingIDs)
		for _, configuredID := range mappingIDs {
			publicID := canonicalizeAccountRequestedModelID(account, configuredID)
			routeID, reason := policyPreviewMappingTarget(snapshot, profile, mapping[configuredID], now)
			if !isSafePolicyPreviewModelID(publicID) {
				reason = "invalid_public_model_id"
			} else if reason == "" && isForbiddenPublicModelIDForAccount(account, publicID) {
				reason = "public_model_id_not_allowed"
			}
			if reason == "" {
				if _, alreadyConflicted := conflictedPublicIDs[publicID]; alreadyConflicted {
					reason = "canonical_mapping_collision"
				}
			}
			if reason == "" {
				if priorRoute, exists := retainedRoutes[publicID]; exists && priorRoute != routeID {
					reason = "canonical_mapping_collision"
				}
			}
			if reason == "" {
				retained[publicID] = struct{}{}
				retainedRoutes[publicID] = routeID
				available[publicID] = struct{}{}
				continue
			}
			if isSafePolicyPreviewModelID(publicID) {
				hidden[publicID] = struct{}{}
				conflictedPublicIDs[publicID] = struct{}{}
				delete(available, publicID)
				delete(retained, publicID)
				delete(retainedRoutes, publicID)
			}
			conflict := UpstreamModelPolicyMappingConflict{Reason: reason}
			if isSafePolicyPreviewModelID(publicID) {
				conflict.PublicModelID = publicID
			}
			// Mapping targets are account configuration, not catalog output. Keep
			// the conflict actionable by model ID/reason without echoing its value.
			conflict.ConfiguredTargetPreview = "[redacted]"
			row.MappingConflicts = append(row.MappingConflicts, conflict)
		}

		row.AvailableModelIDs = sortedModelIDSet(available)
		row.AvailableModelCount = len(row.AvailableModelIDs)
		row.RetainedModelIDs = sortedModelIDSet(retained)
		row.HiddenModelIDs = sortedModelIDSet(hidden)
		for _, modelID := range row.AvailableModelIDs {
			if _, exists := retained[modelID]; !exists {
				row.AddedModelIDs = append(row.AddedModelIDs, modelID)
			}
		}
		if len(row.MappingConflicts) > 0 {
			setIneligible("explicit_mapping_conflict")
		}
		row.MappingConflictCount = len(row.MappingConflicts)
		if row.IneligibleReason == "snapshot_contains_retired_model" || row.IneligibleReason == "snapshot_contains_quarantined_model" || row.IneligibleReason == "snapshot_no_routable_models" {
			// Keep exact hidden-model and mapping-conflict diagnostics, but never
			// represent a directory rejected for lifecycle/quarantine as usable.
			row.AvailableModelIDs = nil
			row.AvailableModelCount = 0
			row.AddedModelIDs = nil
			row.RetainedModelIDs = nil
		}
	}
	row.Eligible = row.IneligibleReason == ""
	revision.Eligible = row.Eligible

	groupByID := make(map[int64]*Group, len(account.Groups))
	for _, group := range account.Groups {
		if group != nil {
			groupByID[group.ID] = group
		}
	}
	groupIDs := append([]int64(nil), account.GroupIDs...)
	if len(groupIDs) == 0 {
		for groupID := range groupByID {
			groupIDs = append(groupIDs, groupID)
		}
	}
	sort.Slice(groupIDs, func(i, j int) bool { return groupIDs[i] < groupIDs[j] })
	groupIDs = uniqueInt64s(groupIDs)
	missingGroupSnapshot := false
	for _, groupID := range groupIDs {
		group := groupByID[groupID]
		if group == nil {
			missingGroupSnapshot = true
			continue
		}
		groupRevision := UpstreamModelPolicyGroupRev{GroupID: groupID, UpdatedAt: group.UpdatedAt.UTC(), AllowlistDigest: digestJSON(group.ModelAllowlist)}
		revision.GroupRevisions = append(revision.GroupRevisions, groupRevision)
		groupPreview := UpstreamModelPolicyPreviewGroup{GroupID: groupID, AllowlistEnabled: group.ModelAllowlistEnabled(), AvailableModelCount: row.AvailableModelCount}
		if !group.ModelAllowlistEnabled() {
			groupPreview.AllowlistedModelCount = row.AvailableModelCount
		} else {
			for _, modelID := range row.AvailableModelIDs {
				if group.ModelAllowlist.Allows(modelID) {
					groupPreview.AllowlistedModelCount++
				}
			}
		}
		row.Groups = append(row.Groups, groupPreview)
	}
	if missingGroupSnapshot && (row.Eligible || row.IneligibleReason == "snapshot_never_fetched") {
		row.Eligible = false
		row.IneligibleReason = "group_snapshot_incomplete"
		revision.Eligible = false
	}
	if !row.Eligible && row.IneligibleReason != "explicit_mapping_conflict" {
		row.AvailableModelCount = 0
		row.AvailableModelIDs = nil
	}
	row.AddedModelCount = len(row.AddedModelIDs)
	row.RetainedModelCount = len(row.RetainedModelIDs)
	row.HiddenModelCount = len(row.HiddenModelIDs)
	row.MappingConflictCount = len(row.MappingConflicts)
	revision.Eligible = row.Eligible
	return row, revision
}

func safePolicyPreviewSnapshotStatus(status string) string {
	switch status {
	case "fresh", "stale", "expired", "error", "unsupported", "incomplete":
		return status
	default:
		return "incomplete"
	}
}

func trustedPolicyPreviewModels(snapshot *UpstreamModelAvailabilitySnapshot, profile UpstreamModelSourceProfile, now time.Time) (available, retired, quarantined []string, err error) {
	if snapshot == nil || snapshot.SchemaVersion != upstreamAvailabilitySchemaVersion || snapshot.SourceProfileID != profile.ID {
		return nil, nil, nil, errors.New("snapshot source or schema mismatch")
	}
	rawModels, err := validateUpstreamModelIDs(snapshot.RawModels)
	if err != nil || len(rawModels) == 0 {
		return nil, nil, nil, errors.New("snapshot raw catalog is incomplete")
	}
	rawDigest := sha256.Sum256([]byte(strings.Join(rawModels, "\x00")))
	if snapshot.RawDigest != "sha256:"+hex.EncodeToString(rawDigest[:]) {
		return nil, nil, nil, errors.New("snapshot digest mismatch")
	}
	if len(snapshot.PublicModels) == 0 || len(snapshot.PublicToUpstream) == 0 {
		return nil, nil, nil, errors.New("snapshot public catalog is incomplete")
	}
	rawSet := make(map[string]struct{}, len(rawModels))
	for _, modelID := range rawModels {
		rawSet[modelID] = struct{}{}
	}
	publicModels, err := validateUpstreamModelIDs(snapshot.PublicModels)
	if err != nil || len(publicModels) != len(snapshot.PublicToUpstream) {
		return nil, nil, nil, errors.New("snapshot public catalog is invalid")
	}
	for publicID, routeID := range snapshot.PublicToUpstream {
		if !isSafePolicyPreviewModelID(publicID) || !isSafePolicyPreviewModelID(routeID) {
			return nil, nil, nil, errors.New("snapshot route map is invalid")
		}
		if _, exists := rawSet[routeID]; !exists {
			return nil, nil, nil, errors.New("snapshot route target is absent")
		}
	}
	publicSet := make(map[string]struct{}, len(publicModels))
	for _, publicID := range publicModels {
		if _, exists := snapshot.PublicToUpstream[publicID]; !exists {
			return nil, nil, nil, errors.New("snapshot public model has no route")
		}
		publicSet[publicID] = struct{}{}
	}
	for publicID := range snapshot.PublicToUpstream {
		if _, exists := publicSet[publicID]; !exists {
			return nil, nil, nil, errors.New("snapshot route map contains an unpublished model")
		}
	}
	rawSetByLower := rawStringSet(rawModels)
	for _, modelID := range rawModels {
		if lifecycle, exists := snapshot.Lifecycle[modelID]; exists && !modelLifecycleRoutable(lifecycle, now) {
			retired = append(retired, modelID)
			continue
		}
		publicID, routeID, publish := normalizeTrustedModelID(profile, modelID, rawSetByLower)
		if !publish {
			quarantined = append(quarantined, modelID)
			continue
		}
		mappedRoute, exists := snapshot.PublicToUpstream[publicID]
		if !exists || mappedRoute != routeID {
			quarantined = append(quarantined, modelID)
			continue
		}
		if lifecycle, exists := snapshot.Lifecycle[publicID]; exists && !modelLifecycleRoutable(lifecycle, now) {
			retired = append(retired, modelID)
		}
	}
	for _, publicID := range publicModels {
		if !modelLifecycleRoutable(snapshot.Lifecycle[publicID], now) {
			retired = append(retired, publicID)
			continue
		}
		available = append(available, publicID)
	}
	return uniqueSortedModelIDs(available), uniqueSortedModelIDs(retired), uniqueSortedModelIDs(quarantined), nil
}

func rawStringSet(values []string) map[string]string {
	result := make(map[string]string, len(values))
	for _, value := range values {
		result[strings.ToLower(value)] = value
	}
	return result
}

func policyPreviewMappingTarget(snapshot *UpstreamModelAvailabilitySnapshot, profile UpstreamModelSourceProfile, target string, now time.Time) (string, string) {
	target = strings.TrimSpace(target)
	if !isSafePolicyPreviewModelID(target) {
		return "", "invalid_mapping_target"
	}
	lowerTarget := strings.ToLower(target)
	if strings.HasPrefix(lowerTarget, "codex-auto-") || lowerTarget == "gpt-6" {
		return "", "target_quarantined"
	}
	candidate := target
	switch profile.Kind {
	case "openai":
		if lowerTarget == "gpt-5.6" {
			candidate = "gpt-5.6-sol"
		}
	case "deepseek":
		switch lowerTarget {
		case "deepseek-v4-flash", "deepseek-v4-flash-vision-exp":
			candidate = "deepseek-flash"
		case "deepseek-v4-pro-0813":
			return "", "target_quarantined"
		}
	}
	if lifecycle, exists := snapshot.Lifecycle[candidate]; exists && !modelLifecycleRoutable(lifecycle, now) {
		return "", "target_retired"
	}
	if route, ok := snapshot.PublicToUpstream[candidate]; ok && strings.TrimSpace(route) != "" {
		if lifecycle, exists := snapshot.Lifecycle[route]; exists && !modelLifecycleRoutable(lifecycle, now) {
			return "", "target_retired"
		}
		return route, ""
	}
	if strings.HasPrefix(strings.ToLower(candidate), "ft:") {
		for _, rawID := range snapshot.RawModels {
			if rawID == candidate {
				return rawID, ""
			}
		}
	}
	for _, rawID := range snapshot.RawModels {
		if rawID == target {
			return "", "target_quarantined"
		}
	}
	return "", "target_not_in_snapshot"
}

func isSafePolicyPreviewModelID(modelID string) bool {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" || len(modelID) > upstreamAvailabilityMaxIDBytes || !utf8.ValidString(modelID) || strings.ContainsAny(modelID, "\r\n\t*?#") {
		return false
	}
	for _, r := range modelID {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	lower := strings.ToLower(modelID)
	return !strings.Contains(lower, "://") && !strings.HasPrefix(lower, "sk-") && !strings.HasPrefix(lower, "sk_") && !strings.HasPrefix(lower, "bearer:")
}

func sortedModelIDSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func uniqueSortedModelIDs(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return sortedModelIDSet(set)
}

func digestJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func snapshotPublicModels(account *Account) []string {
	if snapshot := account.GetUpstreamModelAvailabilitySnapshot(); snapshot != nil {
		return snapshot.PublicModels
	}
	return nil
}

func uniqueInt64s(values []int64) []int64 {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func (s *UpstreamModelRefreshService) SetAccountPolicies(ctx context.Context, previewID, planHash string, confirmIDs []int64, actorID int64) error {
	if s == nil || s.state == nil || actorID <= 0 || strings.TrimSpace(previewID) == "" || !strings.HasPrefix(planHash, "sha256:") {
		return errors.New("model policy confirmation is unavailable")
	}
	ids := append([]int64(nil), confirmIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for i, id := range ids {
		if id <= 0 || (i > 0 && id == ids[i-1]) {
			return errors.New("invalid model policy confirmation account set")
		}
	}
	if err := s.state.ApplyUpstreamModelPolicyPreview(ctx, previewID, planHash, ids, actorID); err != nil {
		return err
	}
	// The policy transaction is already committed. A due-only catch-up is
	// asynchronous and protected by the same distributed coordinator as 04:00.
	go s.RunDue(context.Background(), time.Now())
	return nil
}

// RefreshAccountCatalogNow is the coordinated admin on-demand refresh path.
// It returns the same canonical snapshot used by listings and routing, and
// does not invoke the legacy catalog fetcher a second time.
func (s *UpstreamModelRefreshService) RefreshAccountCatalogNow(ctx context.Context, account *Account) (*UpstreamModelCatalog, error) {
	if err := s.RefreshAccountNow(ctx, account); err != nil {
		return nil, err
	}
	snapshot := account.GetUpstreamModelAvailabilitySnapshot()
	if snapshot == nil || snapshot.Status != "fresh" {
		return nil, errors.New("complete upstream model snapshot was not produced")
	}
	catalog := &UpstreamModelCatalog{Models: append([]string(nil), snapshot.RawModels...), Metadata: make(map[string]UpstreamModelMetadata)}
	if metadata := account.GetUpstreamModelMetadataSnapshot(); metadata != nil {
		for id, item := range metadata.Models {
			catalog.Metadata[id] = item
		}
	}
	if upstreamCatalogNeedsRegistry(capabilitySyncModelIDs(catalog.Models), catalog.Metadata) {
		catalog.Warnings = append(catalog.Warnings, UpstreamModelSyncWarning{
			Code: UpstreamModelMetadataIncompleteCode, Message: "Model IDs were refreshed, but capability metadata is incomplete.",
		})
	}
	return catalog, nil
}
