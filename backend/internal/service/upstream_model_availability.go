package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

const (
	UpstreamModelRefreshExtraKey      = "upstream_model_refresh"
	upstreamModelRefreshSchemaVersion = 1
	upstreamModelRefreshSource        = "upstream_models_endpoint"
	upstreamModelRefreshMaxIDs        = 4096
	upstreamModelRefreshMaxIDLength   = 512
)

const (
	UpstreamModelRefreshStatusFresh       = "fresh"
	UpstreamModelRefreshStatusStale       = "stale"
	UpstreamModelRefreshStatusExpired     = "expired"
	UpstreamModelRefreshStatusUnsupported = "unsupported"
	UpstreamModelRefreshStatusError       = "error"
	UpstreamModelRefreshUnsupportedCode   = "upstream_model_refresh_unsupported"
)

// ModelAvailabilityInvalidator is implemented by the gateway model-list cache
// owner. It is deliberately narrow so the account test/sync service does not
// depend on the whole gateway service surface.
type ModelAvailabilityInvalidator interface {
	InvalidateModelAvailabilityForAccount(account *Account)
}

// UpstreamModelRefreshError is the safe, persisted form of a failed refresh.
// It intentionally excludes response bodies, credentials and URLs.
type UpstreamModelRefreshError struct {
	Kind       string `json:"kind"`
	StatusCode int    `json:"status_code,omitempty"`
	Message    string `json:"message"`
}

// UpstreamModelRefreshSnapshot is the account-scoped availability contract.
// RawModels are used only for routing back to the upstream; public models are
// the normalized IDs exposed to downstream clients.
type UpstreamModelRefreshSnapshot struct {
	SchemaVersion    int                        `json:"schema_version"`
	Status           string                     `json:"status"`
	Source           string                     `json:"source"`
	LastAttemptAt    string                     `json:"last_attempt_at"`
	LastSuccessAt    string                     `json:"last_success_at,omitempty"`
	NextRefreshAt    string                     `json:"next_refresh_at"`
	RawModels        []string                   `json:"raw_models"`
	PublicModels     []string                   `json:"public_models"`
	PublicToUpstream map[string]string          `json:"public_to_upstream"`
	RawDigest        string                     `json:"raw_digest"`
	LastError        *UpstreamModelRefreshError `json:"last_error,omitempty"`
}

func (a *Account) SetUpstreamModelRefreshSnapshot(snapshot UpstreamModelRefreshSnapshot) {
	if a == nil {
		return
	}
	if a.Extra == nil {
		a.Extra = make(map[string]any)
	}
	a.Extra[UpstreamModelRefreshExtraKey] = snapshot
}

func (a *Account) GetUpstreamModelRefreshSnapshot() *UpstreamModelRefreshSnapshot {
	if a == nil || a.Extra == nil {
		return nil
	}
	raw, ok := a.Extra[UpstreamModelRefreshExtraKey]
	if !ok || raw == nil {
		return nil
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var snapshot UpstreamModelRefreshSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil || snapshot.SchemaVersion <= 0 {
		return nil
	}
	snapshot.RawModels = cloneRefreshStringSlice(snapshot.RawModels)
	snapshot.PublicModels = cloneRefreshStringSlice(snapshot.PublicModels)
	snapshot.PublicToUpstream = cloneStringMap(snapshot.PublicToUpstream)
	return &snapshot
}

func cloneRefreshStringSlice(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func (a *Account) hasAuthoritativeUpstreamModelRefresh() bool {
	snapshot := a.GetUpstreamModelRefreshSnapshot()
	if snapshot == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(snapshot.Status)) {
	case UpstreamModelRefreshStatusFresh, UpstreamModelRefreshStatusStale, UpstreamModelRefreshStatusExpired:
		return true
	default:
		return false
	}
}

// AvailablePublicModelIDs returns the last successful normalized directory and
// whether the snapshot is authoritative for this account. An expired snapshot
// is authoritative with an empty directory, so callers do not fall back to
// stale credentials.model_mapping entries.
func (a *Account) AvailablePublicModelIDs() ([]string, bool) {
	snapshot := a.GetUpstreamModelRefreshSnapshot()
	if snapshot == nil {
		return nil, false
	}
	if !a.hasAuthoritativeUpstreamModelRefresh() {
		return nil, false
	}
	if snapshot.Status == UpstreamModelRefreshStatusExpired {
		return nil, true
	}
	return cloneRefreshStringSlice(snapshot.PublicModels), true
}

// ResolveAvailableModel resolves a public/requested model through the live
// snapshot. The third return value says the snapshot is authoritative even
// when the requested model is unavailable.
func (a *Account) ResolveAvailableModel(requestedModel string) (upstreamModel string, available bool, authoritative bool) {
	if !a.hasAuthoritativeUpstreamModelRefresh() {
		return strings.TrimSpace(requestedModel), false, false
	}
	snapshot := a.GetUpstreamModelRefreshSnapshot()
	if snapshot == nil || snapshot.Status == UpstreamModelRefreshStatusExpired {
		return "", false, true
	}
	upstreamModel, available = a.resolveLiveSnapshotModel(snapshot, requestedModel)
	return upstreamModel, available, true
}

func (a *Account) resolveLiveSnapshotModel(snapshot *UpstreamModelRefreshSnapshot, requestedModel string) (string, bool) {
	requestedModel = strings.TrimSpace(requestedModel)
	if snapshot == nil || requestedModel == "" {
		return "", false
	}

	rawModel := ""
	mapping := a.GetModelMapping()
	if len(mapping) > 0 {
		if mapped, matched := resolveRequestedModelInMapping(mapping, requestedModel); matched {
			rawModel = strings.TrimSpace(mapped)
		}
		if rawModel == "" {
			normalized := normalizeRequestedModelForLookup(a.Platform, requestedModel)
			if normalized != requestedModel {
				if mapped, matched := resolveRequestedModelInMapping(mapping, normalized); matched {
					rawModel = strings.TrimSpace(mapped)
				}
			}
		}
	} else {
		rawModel = lookupPublicModelMapping(snapshot.PublicToUpstream, requestedModel)
		if rawModel == "" {
			for publicID, upstreamID := range snapshot.PublicToUpstream {
				if !safeModelFamilyMatch(publicID, requestedModel) {
					continue
				}
				if rawModel != "" && !strings.EqualFold(rawModel, upstreamID) {
					return "", false
				}
				rawModel = upstreamID
			}
		}
	}

	if rawModel == "" || strings.Contains(rawModel, "*") {
		return "", false
	}
	if liveModelID(snapshot.RawModels, rawModel) == "" {
		return "", false
	}
	return liveModelID(snapshot.RawModels, rawModel), true
}

func lookupPublicModelMapping(mapping map[string]string, requestedModel string) string {
	for publicID, upstreamID := range mapping {
		if strings.EqualFold(strings.TrimSpace(publicID), requestedModel) {
			return strings.TrimSpace(upstreamID)
		}
	}
	return ""
}

func lookupRawModel(models []string, requestedModel string) string {
	for _, model := range models {
		if strings.EqualFold(strings.TrimSpace(model), requestedModel) {
			return strings.TrimSpace(model)
		}
	}
	return ""
}

func liveModelID(models []string, requestedModel string) string {
	return lookupRawModel(models, requestedModel)
}

func canonicalUpstreamModelSnapshot(account *Account, rawModels []string, now time.Time) UpstreamModelRefreshSnapshot {
	rawModels = sanitizeUpstreamModelIDs(rawModels)
	publicToUpstream := make(map[string]string)
	mapping := account.GetModelMapping()
	var publicModels []string

	if len(mapping) > 0 {
		candidateIDs := make([]string, 0, len(mapping))
		for publicID := range mapping {
			publicID = strings.TrimSpace(publicID)
			if publicID != "" && !strings.Contains(publicID, "*") {
				candidateIDs = append(candidateIDs, publicID)
			}
		}
		publicModels = NormalizePublicModelIDs(account.Platform, candidateIDs)
		for _, publicID := range publicModels {
			upstreamID, matched := resolveRequestedModelInMapping(mapping, publicID)
			if !matched {
				continue
			}
			if live := liveModelID(rawModels, upstreamID); live != "" {
				publicToUpstream[publicID] = live
			}
		}
	} else {
		publicModels = NormalizePublicModelIDs(account.Platform, rawModels)
		for _, publicID := range publicModels {
			if live := liveModelID(rawModels, publicID); live != "" {
				publicToUpstream[publicID] = live
				continue
			}
			candidates := make([]string, 0, 2)
			for _, rawID := range rawModels {
				if safeModelFamilyMatch(rawID, publicID) {
					candidates = append(candidates, rawID)
				}
			}
			if len(candidates) == 1 {
				publicToUpstream[publicID] = candidates[0]
			}
		}
	}

	filteredPublicModels := make([]string, 0, len(publicModels))
	for _, publicID := range publicModels {
		if _, ok := publicToUpstream[publicID]; ok {
			filteredPublicModels = append(filteredPublicModels, publicID)
		}
	}
	publicModels = filteredPublicModels
	for key := range publicToUpstream {
		if !containsRefreshString(publicModels, key) {
			delete(publicToUpstream, key)
		}
	}

	stamp := now.UTC().Format(time.RFC3339)
	return UpstreamModelRefreshSnapshot{
		SchemaVersion:    upstreamModelRefreshSchemaVersion,
		Status:           UpstreamModelRefreshStatusFresh,
		Source:           upstreamModelRefreshSource,
		LastAttemptAt:    stamp,
		LastSuccessAt:    stamp,
		NextRefreshAt:    nextUpstreamModelRefreshAt(now).Format(time.RFC3339),
		RawModels:        rawModels,
		PublicModels:     publicModels,
		PublicToUpstream: publicToUpstream,
		RawDigest:        digestUpstreamModelIDs(rawModels),
	}
}

func sanitizeUpstreamModelIDs(models []string) []string {
	seen := make(map[string]struct{}, len(models))
	cleaned := make([]string, 0, minInt(len(models), upstreamModelRefreshMaxIDs))
	for _, modelID := range models {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" || len(modelID) > upstreamModelRefreshMaxIDLength || strings.Contains(modelID, "*") {
			continue
		}
		key := strings.ToLower(modelID)
		if _, exists := seen[key]; exists {
			continue
		}
		if len(cleaned) >= upstreamModelRefreshMaxIDs {
			break
		}
		seen[key] = struct{}{}
		cleaned = append(cleaned, modelID)
	}
	sort.Slice(cleaned, func(i, j int) bool { return strings.ToLower(cleaned[i]) < strings.ToLower(cleaned[j]) })
	return cleaned
}

func digestUpstreamModelIDs(models []string) string {
	digest := sha256.Sum256([]byte(strings.Join(models, "\x00")))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func containsRefreshString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func nextUpstreamModelRefreshAt(now time.Time) time.Time {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil || location == nil {
		location = time.FixedZone("UTC+8", 8*60*60)
	}
	localNow := now.In(location)
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 4, 0, 0, 0, location)
	if !next.After(localNow) {
		next = next.AddDate(0, 0, 1)
	}
	return next.UTC()
}

func (s *AccountTestService) SetModelAvailabilityInvalidator(invalidator ModelAvailabilityInvalidator) {
	if s != nil {
		s.modelAvailabilityInvalidator = invalidator
	}
}

func (s *AccountTestService) persistUpstreamModelRefreshSuccess(ctx context.Context, account *Account, rawModels []string, now time.Time) (*UpstreamModelRefreshSnapshot, error) {
	return s.persistUpstreamModelRefreshSuccessWithUpdates(ctx, account, rawModels, now, nil)
}

func (s *AccountTestService) persistUpstreamModelRefreshSuccessWithUpdates(
	ctx context.Context,
	account *Account,
	rawModels []string,
	now time.Time,
	updates map[string]any,
) (*UpstreamModelRefreshSnapshot, error) {
	if account == nil {
		return nil, newUpstreamModelSyncConfigError("Account is required", nil)
	}
	snapshot := canonicalUpstreamModelSnapshot(account, rawModels, now)
	if len(snapshot.RawModels) == 0 {
		return nil, newUpstreamModelSyncUpstreamError("Upstream returned no supported models", nil)
	}
	if account.ID > 0 && s != nil && s.accountRepo != nil {
		if updates == nil {
			updates = make(map[string]any, 1)
		}
		updates[UpstreamModelRefreshExtraKey] = snapshot
		if err := s.accountRepo.UpdateExtra(ctx, account.ID, updates); err != nil {
			return nil, newUpstreamModelSyncInternalError("Failed to save upstream model refresh snapshot", err)
		}
	}
	account.SetUpstreamModelRefreshSnapshot(snapshot)
	if s != nil && s.modelAvailabilityInvalidator != nil && account.ID > 0 {
		s.modelAvailabilityInvalidator.InvalidateModelAvailabilityForAccount(account)
	}
	return &snapshot, nil
}

func (s *AccountTestService) persistUpstreamModelRefreshFailure(
	ctx context.Context,
	account *Account,
	err error,
	now time.Time,
	staleGrace time.Duration,
) error {
	if account == nil {
		return newUpstreamModelSyncConfigError("Account is required", nil)
	}
	previous := account.GetUpstreamModelRefreshSnapshot()
	snapshot := UpstreamModelRefreshSnapshot{
		SchemaVersion:    upstreamModelRefreshSchemaVersion,
		Status:           UpstreamModelRefreshStatusError,
		Source:           upstreamModelRefreshSource,
		LastAttemptAt:    now.UTC().Format(time.RFC3339),
		NextRefreshAt:    nextUpstreamModelRefreshAt(now).Format(time.RFC3339),
		PublicToUpstream: map[string]string{},
	}
	if previous != nil {
		snapshot = *previous
		snapshot.RawModels = cloneRefreshStringSlice(previous.RawModels)
		snapshot.PublicModels = cloneRefreshStringSlice(previous.PublicModels)
		snapshot.PublicToUpstream = cloneStringMap(previous.PublicToUpstream)
		snapshot.LastAttemptAt = now.UTC().Format(time.RFC3339)
		snapshot.NextRefreshAt = nextUpstreamModelRefreshAt(now).Format(time.RFC3339)
	}

	refreshErr := &UpstreamModelRefreshError{Kind: "upstream", Message: "upstream model refresh failed"}
	var syncErr *UpstreamModelSyncError
	if errors.As(err, &syncErr) {
		refreshErr.Kind = string(syncErr.Kind)
		refreshErr.StatusCode = syncErr.StatusCode
		refreshErr.Message = syncErr.SafeMessage()
		if syncErr.Kind == UpstreamModelSyncErrorUnsupported || syncErr.StatusCode == 404 || syncErr.StatusCode == 405 {
			snapshot.Status = UpstreamModelRefreshStatusUnsupported
		}
	}
	if snapshot.Status != UpstreamModelRefreshStatusUnsupported {
		snapshot.Status = UpstreamModelRefreshStatusError
		if previous != nil && previous.LastSuccessAt != "" {
			if lastSuccess, parseErr := time.Parse(time.RFC3339, previous.LastSuccessAt); parseErr == nil && now.Sub(lastSuccess) <= staleGrace {
				snapshot.Status = UpstreamModelRefreshStatusStale
			} else if len(previous.RawModels) > 0 {
				snapshot.Status = UpstreamModelRefreshStatusExpired
			}
		}
	}
	snapshot.LastError = refreshErr
	if account.ID > 0 && s != nil && s.accountRepo != nil {
		if persistErr := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{UpstreamModelRefreshExtraKey: snapshot}); persistErr != nil {
			return newUpstreamModelSyncInternalError("Failed to save upstream model refresh failure", persistErr)
		}
	}
	account.SetUpstreamModelRefreshSnapshot(snapshot)
	if snapshot.Status == UpstreamModelRefreshStatusExpired && s != nil && s.modelAvailabilityInvalidator != nil && account.ID > 0 {
		s.modelAvailabilityInvalidator.InvalidateModelAvailabilityForAccount(account)
	}
	return nil
}
