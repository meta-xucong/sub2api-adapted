package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

const (
	UpstreamModelAvailabilityExtraKey = "upstream_model_availability"
	UpstreamModelPolicyExtraKey       = "upstream_model_policy"
	UpstreamModelPolicyManual         = "manual"
	UpstreamModelPolicyFollow         = "follow_upstream"
	upstreamAvailabilitySchemaVersion = 1
	upstreamModelNormalizerVersion    = "model-catalog-v2"
	upstreamAvailabilityMaxIDs        = 2048
	upstreamAvailabilityMaxIDBytes    = 256
	upstreamAvailabilityMaxBytes      = 2 << 20
	upstreamAvailabilityStaleGrace    = 48 * time.Hour
)

type UpstreamModelAvailabilitySnapshot struct {
	SchemaVersion       int                              `json:"schema_version"`
	RefreshToken        int64                            `json:"refresh_token,omitempty"`
	SourceProfileID     string                           `json:"source_profile_id"`
	SourceIdentity      string                           `json:"source_identity_fingerprint,omitempty"`
	Status              string                           `json:"status"`
	LastAttemptAt       time.Time                        `json:"last_attempt_at"`
	LastSuccessAt       *time.Time                       `json:"last_success_at,omitempty"`
	NextDueAt           time.Time                        `json:"next_due_at"`
	RawDigest           string                           `json:"raw_digest"`
	Normalizer          string                           `json:"normalizer_version"`
	RawModels           []string                         `json:"raw_models"`
	PublicModels        []string                         `json:"public_models"`
	PublicToUpstream    map[string]string                `json:"public_to_upstream"`
	Lifecycle           map[string]ModelLifecycle        `json:"lifecycle,omitempty"`
	ConfirmedAbsent     map[string]string                `json:"confirmed_absent,omitempty"`
	UnsupportedEndpoint *UnsupportedModelCatalogEndpoint `json:"unsupported_endpoint,omitempty"`
	LastError           string                           `json:"last_error,omitempty"`
}

// UnsupportedModelCatalogEndpoint records a source-scoped 404/405 for the
// model-list endpoint. It suppresses automatic daily retries only while both
// the source credentials/entitlements and normalizer contract remain equal.
type UnsupportedModelCatalogEndpoint struct {
	SourceIdentity string `json:"source_identity_fingerprint"`
	Normalizer     string `json:"normalizer_version"`
	StatusCode     int    `json:"status_code"`
}

// UpstreamModelRefreshFenceRepository makes refresh ownership durable across
// gateway instances. A snapshot is applied only by the most recently issued
// token for that account.
type UpstreamModelRefreshFenceRepository interface {
	IssueUpstreamModelRefreshToken(ctx context.Context, accountID int64) (int64, error)
	ApplyUpstreamModelRefreshSnapshot(ctx context.Context, accountID, token int64, snapshot UpstreamModelAvailabilitySnapshot) (bool, error)
}

type UpstreamModelPolicyAccountRevision struct {
	AccountID       int64                         `json:"account_id"`
	UpdatedAt       time.Time                     `json:"updated_at"`
	CurrentPolicy   string                        `json:"current_policy"`
	SourceProfileID string                        `json:"source_profile_id"`
	Eligible        bool                          `json:"eligible"`
	MappingDigest   string                        `json:"mapping_digest"`
	SnapshotDigest  string                        `json:"snapshot_digest,omitempty"`
	GroupRevisions  []UpstreamModelPolicyGroupRev `json:"group_revisions,omitempty"`
}

type UpstreamModelPolicyGroupRev struct {
	GroupID         int64     `json:"group_id"`
	UpdatedAt       time.Time `json:"updated_at"`
	AllowlistDigest string    `json:"allowlist_digest"`
}

type UpstreamModelPolicyPreviewPlan struct {
	PreviewID  string                               `json:"preview_id"`
	PlanHash   string                               `json:"plan_hash"`
	ExpiresAt  time.Time                            `json:"expires_at"`
	AccountIDs []int64                              `json:"account_ids"`
	Revisions  []UpstreamModelPolicyAccountRevision `json:"revisions"`
}

type UpstreamModelRefreshRunRecord struct {
	RunID       string                          `json:"run_id"`
	Status      string                          `json:"status"`
	ScheduledAt time.Time                       `json:"scheduled_at"`
	StartedAt   time.Time                       `json:"started_at"`
	FinishedAt  *time.Time                      `json:"finished_at,omitempty"`
	Eligible    int                             `json:"eligible"`
	Succeeded   int                             `json:"succeeded"`
	Failed      int                             `json:"failed"`
	Unsupported int                             `json:"unsupported"`
	Skipped     int                             `json:"skipped"`
	Accounts    []UpstreamModelAccountRunStatus `json:"accounts,omitempty"`
	ErrorKind   string                          `json:"error_kind,omitempty"`
}

type UpstreamModelAccountRunStatus struct {
	AccountID       int64      `json:"account_id"`
	SourceProfileID string     `json:"source_profile_id"`
	Policy          string     `json:"catalog_policy"`
	Status          string     `json:"status"`
	LastAttemptAt   *time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt   *time.Time `json:"last_success_at,omitempty"`
	NextDueAt       *time.Time `json:"next_due_at,omitempty"`
	RawCount        int        `json:"raw_count"`
	CanonicalCount  int        `json:"canonical_count"`
	DigestPrefix    string     `json:"digest_prefix,omitempty"`
	ErrorKind       string     `json:"error_kind,omitempty"`
}

// UpstreamModelRefreshStateRepository persists operator previews and run
// diagnostics across the two gateway instances. Implementations must commit
// policy updates, their audit row, preview consumption, and scheduler events
// atomically.
type UpstreamModelRefreshStateRepository interface {
	StoreUpstreamModelPolicyPreview(ctx context.Context, plan UpstreamModelPolicyPreviewPlan) error
	ApplyUpstreamModelPolicyPreview(ctx context.Context, previewID, planHash string, confirmIDs []int64, actorID int64) error
	StartUpstreamModelRefreshRun(ctx context.Context, run UpstreamModelRefreshRunRecord) error
	FinishUpstreamModelRefreshRun(ctx context.Context, run UpstreamModelRefreshRunRecord) error
	GetLastUpstreamModelRefreshRun(ctx context.Context) (UpstreamModelRefreshRunRecord, error)
}

type ModelLifecycle struct {
	State       string `json:"state"`
	CutoffDate  string `json:"cutoff_date,omitempty"`
	Source      string `json:"source,omitempty"`
	EvidenceRev string `json:"evidence_revision,omitempty"`
}

type UpstreamModelSourceProfile struct {
	ID                  string
	Kind                string
	BaseURL             string
	IdentityFingerprint string
	ManualOnly          bool
	Paginated           bool
}

func (a *Account) GetUpstreamModelPolicy() string {
	if a == nil || a.Extra == nil {
		return UpstreamModelPolicyManual
	}
	policy, _ := a.Extra[UpstreamModelPolicyExtraKey].(string)
	if policy != UpstreamModelPolicyFollow {
		return UpstreamModelPolicyManual
	}
	return policy
}

func (a *Account) SetUpstreamModelAvailabilitySnapshot(snapshot UpstreamModelAvailabilitySnapshot) {
	if a == nil {
		return
	}
	if a.Extra == nil {
		a.Extra = make(map[string]any)
	}
	a.Extra[UpstreamModelAvailabilityExtraKey] = snapshot
}

func (a *Account) GetUpstreamModelAvailabilitySnapshot() *UpstreamModelAvailabilitySnapshot {
	if a == nil || a.Extra == nil || a.Extra[UpstreamModelAvailabilityExtraKey] == nil {
		return nil
	}
	body, err := json.Marshal(a.Extra[UpstreamModelAvailabilityExtraKey])
	if err != nil || len(body) > upstreamAvailabilityMaxBytes {
		return nil
	}
	var snapshot UpstreamModelAvailabilitySnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil || snapshot.SchemaVersion != upstreamAvailabilitySchemaVersion {
		return nil
	}
	migrateLegacyConfirmedAbsentLifecycle(&snapshot)
	return &snapshot
}

func migrateLegacyConfirmedAbsentLifecycle(snapshot *UpstreamModelAvailabilitySnapshot) {
	if snapshot == nil {
		return
	}
	for modelID, lifecycle := range snapshot.Lifecycle {
		if lifecycle.State != "withdrawn" || lifecycle.Source != "complete_catalog_negative" {
			continue
		}
		if snapshot.ConfirmedAbsent == nil {
			snapshot.ConfirmedAbsent = make(map[string]string)
		}
		evidence := strings.TrimSpace(lifecycle.EvidenceRev)
		if evidence == "" {
			evidence = strings.TrimSpace(snapshot.RawDigest)
		}
		if evidence == "" {
			evidence = "legacy-confirmed-absent"
		}
		snapshot.ConfirmedAbsent[modelID] = evidence
		delete(snapshot.Lifecycle, modelID)
	}
}

func (a *Account) usableUpstreamModelAvailabilitySnapshot(now time.Time) (*UpstreamModelAvailabilitySnapshot, UpstreamModelSourceProfile, bool) {
	profile := DetectUpstreamModelSourceProfile(a)
	if profile.ManualOnly {
		return nil, profile, false
	}
	snapshot := a.GetUpstreamModelAvailabilitySnapshot()
	if snapshot == nil || snapshot.SourceProfileID != profile.ID || snapshot.LastSuccessAt == nil {
		return snapshot, profile, false
	}
	if snapshot.Normalizer != upstreamModelNormalizerVersion {
		return nil, profile, false
	}
	if profile.IdentityFingerprint == "" || snapshot.SourceIdentity == "" || snapshot.SourceIdentity != profile.IdentityFingerprint {
		return nil, profile, false
	}
	if snapshot.Status != "fresh" && snapshot.Status != "stale" {
		return snapshot, profile, false
	}
	age := now.Sub(*snapshot.LastSuccessAt)
	if age < 0 || age > upstreamAvailabilityStaleGrace {
		return snapshot, profile, false
	}
	return snapshot, profile, true
}

// resolveAvailabilityCatalogModel resolves only reviewed, source-scoped input
// aliases and IDs present in a complete trusted snapshot. Private fine-tuned
// IDs are accepted only for an explicit account mapping; internal probe names
// and the ambiguous bare gpt-6 ID are never routable here.
func resolveAvailabilityCatalogModel(snapshot *UpstreamModelAvailabilitySnapshot, profile UpstreamModelSourceProfile, model string, allowFineTuned bool) (string, bool) {
	if snapshot == nil {
		return "", false
	}
	model = strings.TrimSpace(model)
	if model == "" || strings.HasPrefix(strings.ToLower(model), "codex-auto-") ||
		(profile.Kind == "openai" && strings.EqualFold(model, "gpt-6")) {
		return "", false
	}
	if profile.Kind == "deepseek" && model == "deepseek-v4-pro-0813" {
		return "", false
	}
	candidate := canonicalAvailabilityModelID(profile, model)
	if upstreamSnapshotModelConfirmedAbsent(snapshot, profile, candidate) {
		return "", false
	}
	if !upstreamSnapshotModelLifecycleRoutable(snapshot, profile, candidate, time.Now()) {
		return "", false
	}
	if route, exists := snapshot.PublicToUpstream[candidate]; exists && strings.TrimSpace(route) != "" {
		return route, true
	}
	if profile.Kind == "openai" && allowFineTuned && strings.HasPrefix(strings.ToLower(candidate), "ft:") {
		for _, raw := range snapshot.RawModels {
			if raw == candidate {
				return raw, true
			}
		}
	}
	return "", false
}

func canonicalAvailabilityModelID(profile UpstreamModelSourceProfile, model string) string {
	model = strings.TrimSpace(model)
	if rule, ok := reviewedModelIDAlias(profile.Kind, model); ok {
		return rule.CanonicalTarget
	}
	return model
}

type reviewedModelAliasRule struct {
	State            string
	CanonicalTarget  string
	RouteMode        string
	EvidenceRevision string
}

// This is a deliberately small, source-family-scoped registry. Adding an alias
// requires first-party evidence and a normalizer-version bump so old snapshots
// are refreshed instead of reusing stale canonical bindings.
var reviewedModelAliases = map[string]map[string]reviewedModelAliasRule{
	"openai": {
		"gpt-5.6": {State: "accepted_compatibility", CanonicalTarget: "gpt-5.6-sol", RouteMode: "canonical_target", EvidenceRevision: "openai-gpt-5.6-sol-model-page-2026-10-02"},
	},
	"deepseek": {
		"deepseek-v4-flash":            {State: "accepted_compatibility", CanonicalTarget: "deepseek-flash", RouteMode: "canonical_target", EvidenceRevision: "deepseek-models-pricing-updates-2026-10-02"},
		"deepseek-v4-flash-vision-exp": {State: "accepted_compatibility", CanonicalTarget: "deepseek-flash", RouteMode: "canonical_target", EvidenceRevision: "deepseek-models-pricing-updates-2026-10-02"},
	},
}

func reviewedModelIDAlias(sourceFamily, alias string) (reviewedModelAliasRule, bool) {
	rule, ok := reviewedModelAliases[strings.ToLower(strings.TrimSpace(sourceFamily))][strings.ToLower(strings.TrimSpace(alias))]
	return rule, ok && rule.State == "accepted_compatibility" && rule.RouteMode == "canonical_target" && rule.CanonicalTarget != "" && rule.EvidenceRevision != ""
}

// A last-good lifecycle decision remains authoritative after its availability
// snapshot ages out. Expiration permits only an explicit mapping to be treated
// as manual-unverified; it must never make a known retired ID routable again.
func upstreamSnapshotModelLifecycleRoutable(snapshot *UpstreamModelAvailabilitySnapshot, profile UpstreamModelSourceProfile, model string, now time.Time) bool {
	if snapshot == nil || snapshot.SourceProfileID != profile.ID {
		return true
	}
	model = strings.TrimSpace(model)
	if upstreamSnapshotModelConfirmedAbsent(snapshot, profile, model) {
		return false
	}
	lifecycle, exists := snapshot.Lifecycle[model]
	if !exists {
		canonical := canonicalAvailabilityModelID(profile, model)
		lifecycle, exists = snapshot.Lifecycle[canonical]
	}
	return !exists || modelLifecycleRoutable(lifecycle, now)
}

func upstreamSnapshotModelConfirmedAbsent(snapshot *UpstreamModelAvailabilitySnapshot, profile UpstreamModelSourceProfile, model string) bool {
	if snapshot == nil || snapshot.SourceProfileID != profile.ID || snapshot.SourceIdentity == "" || profile.IdentityFingerprint == "" || snapshot.SourceIdentity != profile.IdentityFingerprint || snapshot.Normalizer != upstreamModelNormalizerVersion {
		return false
	}
	model = strings.TrimSpace(model)
	if snapshot.ConfirmedAbsent[model] != "" {
		return true
	}
	canonical := canonicalAvailabilityModelID(profile, model)
	return snapshot.ConfirmedAbsent[canonical] != ""
}

func (a *Account) upstreamAvailabilityListingModels(now time.Time) []string {
	if a == nil {
		return nil
	}
	if a.IsOpenAIPassthroughEnabled() {
		if a.GetUpstreamModelPolicy() != UpstreamModelPolicyFollow {
			return nil
		}
		snapshot, _, usable := a.usableUpstreamModelAvailabilitySnapshot(now)
		if !usable {
			return nil
		}
		models := make([]string, 0, len(snapshot.PublicModels))
		for _, model := range snapshot.PublicModels {
			if modelLifecycleRoutable(snapshot.Lifecycle[model], now) {
				models = append(models, model)
			}
		}
		return dedupeAndSortModelIDs(models)
	}
	mapping := a.GetModelMapping()
	snapshot, profile, usable := a.usableUpstreamModelAvailabilitySnapshot(now)
	if a.GetUpstreamModelPolicy() == UpstreamModelPolicyFollow {
		if !usable {
			return nil
		}
		models := make([]string, 0, len(snapshot.PublicModels))
		for _, model := range snapshot.PublicModels {
			if modelLifecycleRoutable(snapshot.Lifecycle[model], now) {
				models = append(models, model)
			}
		}
		for alias := range mapping {
			alias = canonicalizeAccountRequestedModelID(a, alias)
			if isForbiddenPublicModelIDForAccount(a, alias) {
				continue
			}
			target, matched := resolveRequestedModelInMapping(mapping, alias)
			if !matched {
				continue
			}
			if _, ok := resolveAvailabilityCatalogModel(snapshot, profile, target, true); ok {
				models = append(models, alias)
			}
		}
		return dedupeAndSortModelIDs(models)
	}

	models := make([]string, 0, len(mapping))
	for alias := range mapping {
		alias = canonicalizeAccountRequestedModelID(a, alias)
		if isForbiddenPublicModelIDForAccount(a, alias) {
			continue
		}
		mapped, matched := resolveRequestedModelInMapping(mapping, alias)
		if !matched || strings.TrimSpace(alias) == "" || strings.Contains(alias, "*") || isForbiddenPublicModelIDForAccount(a, mapped) {
			continue
		}
		profile := DetectUpstreamModelSourceProfile(a)
		if !upstreamSnapshotModelLifecycleRoutable(snapshot, profile, mapped, now) {
			continue
		}
		if usable {
			if _, ok := resolveAvailabilityCatalogModel(snapshot, profile, mapped, true); !ok {
				continue
			}
		}
		models = append(models, alias)
	}
	return dedupeAndSortModelIDs(models)
}

func modelLifecycleRoutable(lifecycle ModelLifecycle, now time.Time) bool {
	if lifecycle.State == "retired" {
		return false
	}
	if lifecycle.State != "deprecated" && lifecycle.State != "scheduled_shutdown" {
		return true
	}
	cutoff := strings.TrimSpace(lifecycle.CutoffDate)
	if cutoff == "" {
		return lifecycle.State != "retired"
	}
	cutoffDate, err := time.Parse("2006-01-02", cutoff)
	if err != nil {
		return true
	}
	// A date is the final supported calendar day; reject beginning the next day.
	return now.UTC().Before(cutoffDate.AddDate(0, 0, 1))
}

func upstreamModelRefreshDue(account *Account, now time.Time) bool {
	if account == nil {
		return false
	}
	policy := account.GetUpstreamModelPolicy()
	if policy != UpstreamModelPolicyFollow && policy != UpstreamModelPolicyManual {
		return false
	}
	profile := DetectUpstreamModelSourceProfile(account)
	if profile.ManualOnly {
		return false
	}
	snapshot := account.GetUpstreamModelAvailabilitySnapshot()
	if upstreamModelCatalogEndpointUnsupported(snapshot, profile) {
		// 404/405 is a stable endpoint capability result, not a transient
		// outage. An admin force-refresh still bypasses this due check.
		return false
	}
	// Manual policy never expands public models, but trusted sources still need
	// periodic negative evidence so an explicitly mapped removed model can be
	// hidden and rejected. Do not fetch a never-configured manual account.
	if policy == UpstreamModelPolicyManual && snapshot == nil && len(account.GetModelMapping()) == 0 {
		return false
	}
	return snapshot == nil || profile.IdentityFingerprint == "" || snapshot.SourceIdentity == "" || snapshot.Normalizer != upstreamModelNormalizerVersion ||
		snapshot.SourceIdentity != profile.IdentityFingerprint || !snapshot.NextDueAt.After(now)
}

func upstreamModelCatalogEndpointUnsupported(snapshot *UpstreamModelAvailabilitySnapshot, profile UpstreamModelSourceProfile) bool {
	return snapshot != nil && snapshot.UnsupportedEndpoint != nil &&
		snapshot.UnsupportedEndpoint.SourceIdentity != "" &&
		snapshot.UnsupportedEndpoint.SourceIdentity == profile.IdentityFingerprint &&
		snapshot.UnsupportedEndpoint.Normalizer == upstreamModelNormalizerVersion &&
		(snapshot.UnsupportedEndpoint.StatusCode == 404 || snapshot.UnsupportedEndpoint.StatusCode == 405)
}

// applyCompleteCatalogMappingNegatives pins explicit mappings that a complete,
// trusted catalog cannot resolve. This prevents expiration of the positive
// snapshot from silently reverting a confirmed missing/non-routable model to
// manual routing. A later complete snapshot rebuilds lifecycle from scratch
// and clears the negative if the model becomes available again.
func applyCompleteCatalogMappingNegatives(snapshot *UpstreamModelAvailabilitySnapshot, account *Account, profile UpstreamModelSourceProfile) error {
	if snapshot == nil || account == nil || snapshot.Status != "fresh" || snapshot.SourceIdentity == "" || snapshot.SourceIdentity != profile.IdentityFingerprint {
		return nil
	}
	if snapshot.ConfirmedAbsent == nil {
		snapshot.ConfirmedAbsent = make(map[string]string)
	}
	for _, target := range account.GetModelMapping() {
		target = strings.TrimSpace(target)
		if target == "" || strings.Contains(target, "*") {
			continue
		}
		if _, err := validateUpstreamModelIDs([]string{target}); err != nil {
			continue
		}
		if _, routable := resolveAvailabilityCatalogModel(snapshot, profile, target, true); routable {
			continue
		}
		canonical := canonicalAvailabilityModelID(profile, target)
		snapshot.ConfirmedAbsent[canonical] = snapshot.RawDigest
	}
	return validateUpstreamModelAvailabilitySnapshotSize(*snapshot)
}

// DetectUpstreamModelSourceProfile is intentionally origin-scoped. OpenAI-
// compatible protocol alone is not evidence that a reseller has a complete or
// authoritative model catalog.
func DetectUpstreamModelSourceProfile(account *Account) UpstreamModelSourceProfile {
	if account == nil || account.Type != AccountTypeAPIKey {
		return UpstreamModelSourceProfile{ID: "manual_only", Kind: "manual_only", ManualOnly: true}
	}

	var profile UpstreamModelSourceProfile
	switch {
	case account.IsOpenAIApiKey():
		// Trust is based on the account's explicit origin, not the runtime
		// platform default. OpenAI-compatible gateways also use PlatformOpenAI.
		baseURL := strings.TrimSpace(account.GetCredential("base_url"))
		if baseURL != "" && exactHTTPSOrigin(baseURL, "api.openai.com", "/", "/v1") {
			profile = UpstreamModelSourceProfile{ID: "openai-platform-api-key", Kind: "openai", BaseURL: baseURL}
			break
		}
		if isAlibabaModelStudioURL(baseURL) {
			profile = UpstreamModelSourceProfile{ID: "aliyun-model-studio", Kind: "aliyun", BaseURL: baseURL, Paginated: true}
			break
		}
	case account.Platform == PlatformDeepseek:
		baseURL := strings.TrimSpace(account.GetOpenAIBaseURL())
		if baseURL == "" {
			baseURL = DefaultDeepseekBaseURL
		}
		if exactHTTPSOrigin(baseURL, "api.deepseek.com", "/", "/v1") {
			profile = UpstreamModelSourceProfile{ID: "deepseek-official", Kind: "deepseek", BaseURL: baseURL}
			break
		}
	case account.IsAnthropic() && account.Type == AccountTypeAPIKey:
		baseURL := strings.TrimSpace(account.GetCredential("base_url"))
		if baseURL == "" {
			baseURL = "https://api.anthropic.com"
		}
		if exactHTTPSOrigin(baseURL, "api.anthropic.com", "/", "/v1") {
			profile = UpstreamModelSourceProfile{ID: "anthropic-official", Kind: "anthropic", BaseURL: baseURL, Paginated: true}
			break
		}
	}
	if profile.ID == "" {
		return UpstreamModelSourceProfile{ID: "manual_only", Kind: "manual_only", ManualOnly: true}
	}
	profile.IdentityFingerprint = upstreamModelSourceIdentityFingerprint(account, profile)
	return profile
}

// upstreamModelSourceIdentityFingerprint binds a snapshot to the actual source
// account, not only its broad provider profile. Raw credentials never enter the
// snapshot; only a one-way digest and non-secret entitlement identifiers do.
func upstreamModelSourceIdentityFingerprint(account *Account, profile UpstreamModelSourceProfile) string {
	if account == nil || profile.ID == "" || profile.ManualOnly {
		return ""
	}
	keyDigest := sha256.Sum256([]byte(strings.TrimSpace(account.GetCredential("api_key"))))
	parts := []string{
		profile.ID,
		strings.ToLower(strings.TrimSpace(account.Platform)),
		strings.ToLower(strings.TrimSpace(account.Type)),
		strings.TrimRight(strings.TrimSpace(profile.BaseURL), "/"),
		hex.EncodeToString(keyDigest[:]),
	}
	for _, field := range []string{"plan_type", "tier_id", "region", "workspace_id", "project_id", "organization_id", "model_access", "model_permissions"} {
		parts = append(parts, field+"="+strings.TrimSpace(account.GetCredential(field)))
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func exactHTTPSOrigin(rawURL, host string, paths ...string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Hostname(), host) || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return false
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	for _, allowed := range paths {
		if path == strings.TrimRight(allowed, "/") {
			return true
		}
	}
	return false
}

func isAlibabaModelStudioURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	if path != "/compatible-mode/v1" && path != "/v1" {
		return false
	}
	if host == "dashscope-intl.aliyuncs.com" || host == "cn-hongkong.dashscope.aliyuncs.com" {
		return true
	}
	for _, suffix := range []string{".cn-beijing.maas.aliyuncs.com", ".eu-central-1.maas.aliyuncs.com", ".ap-northeast-1.maas.aliyuncs.com", ".us-east-1.maas.aliyuncs.com"} {
		if strings.HasSuffix(host, suffix) && len(strings.TrimSuffix(host, suffix)) > 0 {
			return true
		}
	}
	return false
}

func buildTrustedAvailabilitySnapshot(profile UpstreamModelSourceProfile, rawModels []string, shutdownDates map[string]string, now time.Time) (UpstreamModelAvailabilitySnapshot, error) {
	clean, err := validateUpstreamModelIDs(rawModels)
	if err != nil {
		return UpstreamModelAvailabilitySnapshot{}, err
	}
	if len(clean) == 0 {
		return UpstreamModelAvailabilitySnapshot{}, fmt.Errorf("upstream model catalog is empty")
	}
	rawSet := make(map[string]string, len(clean))
	for _, id := range clean {
		rawSet[strings.ToLower(id)] = id
	}
	publicToRaw := make(map[string]string, len(clean))
	lifecycle := make(map[string]ModelLifecycle)
	for _, rawID := range clean {
		if profile.Kind == "anthropic" {
			if item, tracked := anthropicRetirementRegistry[strings.ToLower(rawID)]; tracked {
				lifecycle[rawID] = item
				if item.State == "retired" || (item.State == "deprecated" && item.CutoffDate != "" && !now.Before(dateAfterUTC(item.CutoffDate))) {
					continue
				}
			}
		}
		publicID, routeID, publish := normalizeTrustedModelID(profile, rawID, rawSet)
		if !publish {
			continue
		}
		if profile.Kind == "openai" {
			if cutoff, exists := shutdownDates[rawID]; exists {
				if _, parseErr := time.Parse("2006-01-02", cutoff); parseErr == nil {
					lifecycle[publicID] = ModelLifecycle{State: "scheduled_shutdown", CutoffDate: cutoff, Source: "openai-platform-models-api", EvidenceRev: "shutdown_date:date"}
					cutoffTime, _ := time.Parse("2006-01-02", cutoff)
					if !now.Before(cutoffTime.AddDate(0, 0, 1)) {
						continue
					}
				}
			}
		}
		if lifecycleItem, tracked := lifecycle[rawID]; tracked {
			lifecycle[publicID] = lifecycleItem
		}
		if prior, exists := publicToRaw[publicID]; exists && !strings.EqualFold(prior, routeID) {
			// Ambiguous raw aliases do not establish a deterministic upstream route.
			delete(publicToRaw, publicID)
			continue
		}
		publicToRaw[publicID] = routeID
	}
	publicIDs := make([]string, 0, len(publicToRaw))
	for id := range publicToRaw {
		publicIDs = append(publicIDs, id)
	}
	sort.Strings(publicIDs)
	if len(publicIDs) > upstreamAvailabilityMaxIDs {
		return UpstreamModelAvailabilitySnapshot{}, fmt.Errorf("normalized model catalog exceeds %d IDs", upstreamAvailabilityMaxIDs)
	}
	if len(publicIDs) == 0 {
		return UpstreamModelAvailabilitySnapshot{}, fmt.Errorf("upstream model catalog contains no publishable models")
	}
	digest := sha256.Sum256([]byte(strings.Join(clean, "\x00")))
	nextDue := nextModelRefreshAt(now)
	lastSuccess := now.UTC()
	snapshot := UpstreamModelAvailabilitySnapshot{
		SchemaVersion:    upstreamAvailabilitySchemaVersion,
		SourceProfileID:  profile.ID,
		SourceIdentity:   profile.IdentityFingerprint,
		Status:           "fresh",
		LastAttemptAt:    now.UTC(),
		LastSuccessAt:    &lastSuccess,
		NextDueAt:        nextDue,
		RawDigest:        "sha256:" + hex.EncodeToString(digest[:]),
		Normalizer:       upstreamModelNormalizerVersion,
		RawModels:        clean,
		PublicModels:     publicIDs,
		PublicToUpstream: publicToRaw,
		Lifecycle:        lifecycle,
	}
	if err := validateUpstreamModelAvailabilitySnapshotSize(snapshot); err != nil {
		return UpstreamModelAvailabilitySnapshot{}, err
	}
	return snapshot, nil
}

func validateUpstreamModelAvailabilitySnapshotSize(snapshot UpstreamModelAvailabilitySnapshot) error {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if len(encoded) > upstreamAvailabilityMaxBytes {
		return fmt.Errorf("model catalog snapshot exceeds %d bytes", upstreamAvailabilityMaxBytes)
	}
	return nil
}

func validateUpstreamModelIDs(models []string) ([]string, error) {
	if len(models) > upstreamAvailabilityMaxIDs {
		return nil, fmt.Errorf("upstream catalog exceeds %d models", upstreamAvailabilityMaxIDs)
	}
	seen := make(map[string]string, len(models))
	clean := make([]string, 0, len(models))
	for _, id := range models {
		id = strings.TrimSpace(id)
		if id == "" || !utf8.ValidString(id) || len(id) > upstreamAvailabilityMaxIDBytes || strings.ContainsAny(id, "\r\n\t*") {
			return nil, fmt.Errorf("upstream catalog contains an invalid model ID")
		}
		key := strings.ToLower(id)
		if previous, exists := seen[key]; exists {
			if previous != id {
				return nil, fmt.Errorf("upstream catalog contains case-conflicting model IDs")
			}
			return nil, fmt.Errorf("upstream catalog contains duplicate model IDs")
		}
		seen[key] = id
		clean = append(clean, id)
	}
	sort.Slice(clean, func(i, j int) bool { return strings.ToLower(clean[i]) < strings.ToLower(clean[j]) })
	return clean, nil
}

func normalizeTrustedModelID(profile UpstreamModelSourceProfile, rawID string, rawSet map[string]string) (publicID, routeID string, publish bool) {
	lower := strings.ToLower(rawID)
	if strings.HasPrefix(lower, "codex-auto-") {
		return "", "", false
	}
	switch profile.Kind {
	case "openai":
		if strings.HasPrefix(lower, "ft:") {
			return "", "", false
		}
		return normalizeOfficialOpenAIModelID(rawID, rawSet)
	case "deepseek":
		if rule, ok := reviewedModelIDAlias(profile.Kind, rawID); ok {
			if canonical, exists := rawSet[strings.ToLower(rule.CanonicalTarget)]; exists {
				return canonical, canonical, true
			}
			return "", "", false
		}
		if lower == "deepseek-v4-pro-0813" {
			return "", "", false
		}
	case "anthropic":
		if item, exists := anthropicRetirementRegistry[lower]; exists && item.State == "retired" {
			return "", "", false
		}
	}
	return rawID, rawID, true
}

func normalizeOfficialOpenAIModelID(rawID string, rawSet map[string]string) (publicID, routeID string, publish bool) {
	lower := strings.ToLower(strings.TrimSpace(rawID))
	if rule, ok := reviewedModelIDAlias("openai", lower); ok {
		if canonical, exists := rawSet[strings.ToLower(rule.CanonicalTarget)]; exists {
			return canonical, canonical, true
		}
		return "", "", false
	}
	if isForbiddenOfficialOpenAICodexModelID(rawID) {
		return "", "", false
	}
	if !openai.IsAutoDiscoveredModelID(rawID) {
		return "", "", false
	}
	return rawID, rawID, true
}

func isForbiddenOfficialOpenAICodexModelID(modelID string) bool {
	normalized := strings.ToLower(codexProviderQualifiedModelID(modelID))
	return normalized == "gpt-5.6" || normalized == "gpt-6"
}

func normalizeListedModelIDForAccount(account *Account, rawID string, rawSet map[string]string) (publicID, routeID string, publish bool) {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(rawID)), "codex-auto-") {
		return "", "", false
	}
	if account != nil && account.IsOpenAIOAuth() {
		return normalizeOfficialOpenAIModelID(rawID, rawSet)
	}
	profile := DetectUpstreamModelSourceProfile(account)
	if profile.Kind == "openai" {
		return normalizeOfficialOpenAIModelID(rawID, rawSet)
	}
	return strings.TrimSpace(rawID), strings.TrimSpace(rawID), strings.TrimSpace(rawID) != ""
}

func nextModelRefreshAt(now time.Time) time.Time {
	return nextModelRefreshAtForSchedule(now, 4, 0)
}

func nextModelRefreshAtForSchedule(now time.Time, hour, minute int) time.Time {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("UTC+8", 8*60*60)
	}
	hour, minute = normalizeModelRefreshSchedule(hour, minute)
	localNow := now.In(loc)
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, loc)
	if !next.After(localNow) {
		next = next.AddDate(0, 0, 1)
	}
	return next.UTC()
}

func dateAfterUTC(date string) time.Time {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		return time.Time{}
	}
	return parsed.AddDate(0, 0, 1)
}

// Based on the Anthropic-operated API lifecycle table. Partner-operated
// platforms are intentionally excluded from this source-scoped registry.
var anthropicRetirementRegistry = map[string]ModelLifecycle{
	"claude-opus-4-1-20250805":   {State: "retired", CutoffDate: "2026-08-05", Source: "https://platform.claude.com/docs/en/about-claude/model-deprecations", EvidenceRev: "2026-10-02"},
	"claude-opus-4-20250514":     {State: "retired", CutoffDate: "2026-06-15", Source: "https://platform.claude.com/docs/en/about-claude/model-deprecations", EvidenceRev: "2026-10-02"},
	"claude-sonnet-4-20250514":   {State: "retired", CutoffDate: "2026-06-15", Source: "https://platform.claude.com/docs/en/about-claude/model-deprecations", EvidenceRev: "2026-10-02"},
	"claude-3-7-sonnet-20250219": {State: "retired", CutoffDate: "2026-02-19", Source: "https://platform.claude.com/docs/en/about-claude/model-deprecations", EvidenceRev: "2026-10-02"},
	"claude-3-5-haiku-20241022":  {State: "retired", CutoffDate: "2026-02-19", Source: "https://platform.claude.com/docs/en/about-claude/model-deprecations", EvidenceRev: "2026-10-02"},
	"claude-3-haiku-20240307":    {State: "retired", CutoffDate: "2026-04-20", Source: "https://platform.claude.com/docs/en/about-claude/model-deprecations", EvidenceRev: "2026-10-02"},
	"claude-sonnet-4-5-20250929": {State: "deprecated", CutoffDate: "2026-11-30", Source: "https://platform.claude.com/docs/en/about-claude/model-deprecations", EvidenceRev: "2026-10-02"},
	"claude-mythos-preview":      {State: "deprecated", CutoffDate: "2026-06-09", Source: "https://platform.claude.com/docs/en/about-claude/model-deprecations", EvidenceRev: "2026-10-02"},
}
