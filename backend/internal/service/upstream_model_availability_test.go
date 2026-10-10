package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDetectUpstreamModelSourceProfileRequiresTrustedOrigin(t *testing.T) {
	tests := []struct {
		name    string
		account *Account
		wantID  string
		manual  bool
	}{
		{
			name:    "official openai",
			account: &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.openai.com/v1"}},
			wantID:  "openai-platform-api-key",
		},
		{
			name:    "implicit official origin is not trusted",
			account: &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
			wantID:  "manual_only", manual: true,
		},
		{
			name: "deepseek official",
			account: &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": "https://api.deepseek.com/v1"}},
			wantID: "deepseek-official",
		},
		{
			name: "reseller is manual only",
			account: &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": "https://gateway.example/v1"}},
			wantID: "manual_only", manual: true,
		},
		{
			name:    "codex oauth is manual only",
			account: &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth},
			wantID:  "manual_only", manual: true,
		},
		{
			name:    "anthropic direct",
			account: &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey},
			wantID:  "anthropic-official",
		},
		{
			name: "aliyun model studio",
			account: &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"}},
			wantID: "aliyun-model-studio",
		},
		{
			name: "aliyun model studio default https port",
			account: &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": "https://dashscope-intl.aliyuncs.com:443/compatible-mode/v1"}},
			wantID: "aliyun-model-studio",
		},
		{
			name: "aliyun model studio nonstandard port is not trusted",
			account: &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": "https://dashscope-intl.aliyuncs.com:8443/compatible-mode/v1"}},
			wantID: "manual_only", manual: true,
		},
		{
			name: "credential URL query is not trusted",
			account: &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": "https://api.openai.com/v1?tenant=other"}},
			wantID: "manual_only", manual: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := DetectUpstreamModelSourceProfile(tt.account)
			require.Equal(t, tt.wantID, profile.ID)
			require.Equal(t, tt.manual, profile.ManualOnly)
		})
	}
}

func TestBuildTrustedAvailabilitySnapshotUsesExactProviderRules(t *testing.T) {
	now := time.Date(2026, 10, 2, 1, 0, 0, 0, time.UTC)
	profile := UpstreamModelSourceProfile{ID: "openai-platform-api-key", Kind: "openai"}
	snapshot, err := buildTrustedAvailabilitySnapshot(profile, []string{
		"gpt-5.6", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-sol-2026-07-09", "gpt-6-astra", "gpt-6", "codex-auto-review", "ft:gpt-5.6:org:private",
	}, nil, now)
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-6-astra"}, snapshot.PublicModels)
	require.Equal(t, "gpt-5.6-sol", snapshot.PublicToUpstream["gpt-5.6-sol"])
	require.NotContains(t, snapshot.PublicModels, "gpt-6")
	require.NotContains(t, snapshot.PublicModels, "gpt-5.6-sol-2026-07-09")
	require.NotContains(t, snapshot.PublicModels, "codex-auto-review")
	require.NotContains(t, snapshot.PublicModels, "ft:gpt-5.6:org:private")
}

func TestOfficialOpenAICatalogNormalizationIsSourceScoped(t *testing.T) {
	rawSet := map[string]string{
		"gpt-5.6":                "gpt-5.6",
		"gpt-5.6-sol":            "gpt-5.6-sol",
		"gpt-5.6-sol-2026-07-09": "gpt-5.6-sol-2026-07-09",
	}
	publicID, routeID, publish := normalizeTrustedModelID(UpstreamModelSourceProfile{Kind: "openai"}, "gpt-5.6", rawSet)
	require.True(t, publish)
	require.Equal(t, "gpt-5.6-sol", publicID)
	require.Equal(t, "gpt-5.6-sol", routeID)
	_, _, publish = normalizeTrustedModelID(UpstreamModelSourceProfile{Kind: "openai"}, "gpt-5.6-sol-2026-07-09", rawSet)
	require.False(t, publish, "official dated snapshot IDs must not be published")

	custom := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://gateway.example/v1"}}
	publicID, routeID, publish = normalizeListedModelIDForAccount(custom, "gpt-5.6-sol-2026-07-09", rawSet)
	require.True(t, publish, "custom provider IDs must not inherit OpenAI lifecycle rules")
	require.Equal(t, "gpt-5.6-sol-2026-07-09", publicID)
	require.Equal(t, publicID, routeID)
}

func TestTrustedCatalogRulesDoNotRewriteOrRejectOtherSources(t *testing.T) {
	profile := UpstreamModelSourceProfile{ID: "aliyun-model-studio", Kind: "aliyun"}
	snapshot, err := buildTrustedAvailabilitySnapshot(profile, []string{"gpt-6", "ft:tenant:model", "codex-auto-review"}, nil, time.Now())
	require.NoError(t, err)
	require.Equal(t, []string{"ft:tenant:model", "gpt-6"}, snapshot.PublicModels,
		"provider-specific OpenAI ambiguity rules must not rewrite unrelated providers' exact IDs")
	require.NotContains(t, snapshot.PublicModels, "codex-auto-review", "internal probe names are never public")

	route, ok := resolveAvailabilityCatalogModel(&snapshot, profile, "gpt-6", false)
	require.True(t, ok)
	require.Equal(t, "gpt-6", route)
	route, ok = resolveAvailabilityCatalogModel(&snapshot, profile, "ft:tenant:model", false)
	require.True(t, ok)
	require.Equal(t, "ft:tenant:model", route)
}

func TestCodexInternalModelsStayRoutableButNeverAppearInCatalogs(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"codex-auto-review": "codex-auto-review"},
		},
	}
	require.True(t, account.IsModelSupported("codex-auto-review"),
		"hiding an internal Codex mode from discovery must not break explicit/internal routing")
	require.Empty(t, account.upstreamAvailabilityListingModels(time.Now()))
	require.True(t, isForbiddenPublicModelIDForAccount(account, "codex-auto-review"))
}

func TestBuildTrustedAvailabilitySnapshotDoesNotInventDeepSeekRoutes(t *testing.T) {
	profile := UpstreamModelSourceProfile{ID: "deepseek-official", Kind: "deepseek"}
	snapshot, err := buildTrustedAvailabilitySnapshot(profile, []string{
		"deepseek-flash", "deepseek-v4-flash", "deepseek-v4-flash-vision-exp", "deepseek-v4-pro", "deepseek-v4-pro-0813",
	}, nil, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, []string{"deepseek-flash", "deepseek-v4-pro"}, snapshot.PublicModels)
	require.Equal(t, "deepseek-flash", snapshot.PublicToUpstream["deepseek-flash"])
	require.NotContains(t, snapshot.PublicModels, "deepseek-v4-pro-0813")

	withoutCanonical := []string{"deepseek-v4-flash"}
	_, err = buildTrustedAvailabilitySnapshot(profile, withoutCanonical, nil, time.Now())
	require.Error(t, err, "an all-quarantined catalog must not replace a last-good snapshot")
}

func TestBuildTrustedAvailabilitySnapshotAppliesSourceScopedLifecycle(t *testing.T) {
	profile := UpstreamModelSourceProfile{ID: "anthropic-official", Kind: "anthropic"}
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	snapshot, err := buildTrustedAvailabilitySnapshot(profile, []string{
		"claude-opus-4-1-20250805", "claude-sonnet-4-5-20250929", "claude-sonnet-5-5",
	}, nil, now)
	require.NoError(t, err)
	require.NotContains(t, snapshot.PublicModels, "claude-opus-4-1-20250805")
	require.Equal(t, "retired", snapshot.Lifecycle["claude-opus-4-1-20250805"].State)
	require.Contains(t, snapshot.PublicModels, "claude-sonnet-4-5-20250929", "deprecated is still callable before its cutoff")
	require.Equal(t, "deprecated", snapshot.Lifecycle["claude-sonnet-4-5-20250929"].State)
}

func TestFollowPolicyUsesSnapshotWhileManualPolicyPreservesRouting(t *testing.T) {
	now := time.Now().UTC()
	follow := &Account{
		ID: 7001, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.openai.com/v1", "api_key": "test", "model_mapping": map[string]any{}},
		Extra:       map[string]any{UpstreamModelPolicyExtraKey: UpstreamModelPolicyFollow},
	}
	profile := DetectUpstreamModelSourceProfile(follow)
	snapshot, err := buildTrustedAvailabilitySnapshot(profile, []string{"gpt-5.6-sol", "gpt-5.6-terra"}, nil, now)
	require.NoError(t, err)
	follow.SetUpstreamModelAvailabilitySnapshot(snapshot)
	require.True(t, follow.IsModelSupported("gpt-5.6-sol"))
	require.True(t, follow.IsModelSupported("gpt-5.6"), "reviewed input alias resolves only when its canonical target is in the account snapshot")
	require.Equal(t, "gpt-5.6-sol", follow.GetMappedModel("gpt-5.6"))
	require.False(t, follow.IsModelSupported("gpt-6"))
	require.False(t, follow.IsModelSupported("codex-auto-review"))
	require.False(t, follow.IsModelSupported("codex-auto-internal-probe"))
	require.False(t, follow.IsModelSupported("gpt-5.6-luna"))
	require.Equal(t, []string{"gpt-5.6-sol", "gpt-5.6-terra"}, follow.upstreamAvailabilityListingModels(now))
	follow.Credentials["model_mapping"] = map[string]any{"gpt-*": "gpt-5.6-sol"}
	require.Equal(t, "gpt-5.6-sol", follow.GetMappedModel("gpt-5.7"), "wildcard mapping remains a request-routing rule")
	require.Equal(t, []string{"gpt-5.6-sol", "gpt-5.6-terra"}, follow.upstreamAvailabilityListingModels(now), "wildcard mapping keys must not be exposed as concrete model IDs")

	manual := &Account{
		ID: 7002, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.openai.com/v1", "api_key": "test", "model_mapping": map[string]any{"custom-sol": "gpt-5.6-sol", "removed": "gpt-5.6-luna"}},
		Extra:       map[string]any{UpstreamModelAvailabilityExtraKey: snapshot},
	}
	require.True(t, manual.IsModelSupported("custom-sol"))
	require.False(t, manual.IsModelSupported("removed"), "fresh complete catalog negative is authoritative even in manual policy")
	require.Equal(t, []string{"custom-sol"}, manual.upstreamAvailabilityListingModels(now))

	noMapping := &Account{
		ID: 7003, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.openai.com/v1", "api_key": "test", "model_mapping": map[string]any{}},
		Extra:       map[string]any{UpstreamModelAvailabilityExtraKey: snapshot},
	}
	require.True(t, noMapping.IsModelSupported("provider-private-custom"), "manual unmapped accounts retain the official allow-all routing behavior")
	require.Empty(t, noMapping.upstreamAvailabilityListingModels(now))
	require.False(t, noMapping.managesUpstreamModelAvailabilityListing(), "manual mode without mappings must retain the official model-list behavior")

	manual.Credentials["model_mapping"] = map[string]any{"custom-sol": "gpt-5.6-sol"}
	require.True(t, manual.managesUpstreamModelAvailabilityListing(), "manual mapped accounts may use a fresh snapshot to qualify configured routes")
}

func TestFollowPolicyFailsClosedWhenSnapshotExpiresButManualMappingRemainsExplicit(t *testing.T) {
	old := time.Now().UTC().Add(-upstreamAvailabilityStaleGrace - time.Hour)
	snapshot := UpstreamModelAvailabilitySnapshot{
		SchemaVersion: upstreamAvailabilitySchemaVersion, SourceProfileID: "openai-platform-api-key", Status: "stale",
		LastSuccessAt: &old, PublicModels: []string{"gpt-5.6-sol"}, PublicToUpstream: map[string]string{"gpt-5.6-sol": "gpt-5.6-sol"}, RawModels: []string{"gpt-5.6-sol"},
	}
	base := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.openai.com/v1", "api_key": "test", "model_mapping": map[string]any{"gpt-5.6-sol": "gpt-5.6-sol"}}}
	snapshot.SourceIdentity = DetectUpstreamModelSourceProfile(base).IdentityFingerprint
	base.SetUpstreamModelAvailabilitySnapshot(snapshot)
	base.Extra[UpstreamModelPolicyExtraKey] = UpstreamModelPolicyFollow
	require.False(t, base.IsModelSupported("gpt-5.6-sol"))
	require.False(t, base.IsModelSupported("gpt-5.6"))

	base.Extra[UpstreamModelPolicyExtraKey] = UpstreamModelPolicyManual
	require.True(t, base.IsModelSupported("gpt-5.6-sol"), "expired availability evidence falls back to an explicit manual mapping")
}

func TestCompleteCatalogNegativeSurvivesExpiryButIsScopedToSourceIdentity(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	account := &Account{
		ID: 7010, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://api.openai.com/v1", "api_key": "identity-a",
			"model_mapping": map[string]any{"kept": "gpt-5.6", "removed": "gpt-5.6-luna"},
		},
	}
	profile := DetectUpstreamModelSourceProfile(account)
	snapshot, err := buildTrustedAvailabilitySnapshot(profile, []string{"gpt-5.6-sol"}, nil, now)
	require.NoError(t, err)
	require.NoError(t, applyCompleteCatalogMappingNegatives(&snapshot, account, profile))
	require.Equal(t, snapshot.RawDigest, snapshot.ConfirmedAbsent["gpt-5.6-luna"])
	require.NotContains(t, snapshot.Lifecycle, "gpt-5.6-luna", "catalog absence is availability evidence, not lifecycle")
	account.SetUpstreamModelAvailabilitySnapshot(snapshot)
	require.True(t, account.IsModelSupported("kept"), "reviewed aliases whose canonical model is present stay routable")
	require.False(t, account.IsModelSupported("removed"))

	lastSuccess := now.Add(-upstreamAvailabilityStaleGrace - time.Hour)
	snapshot.Status = "expired"
	snapshot.LastSuccessAt = &lastSuccess
	snapshot.NextDueAt = now.Add(-time.Minute)
	account.SetUpstreamModelAvailabilitySnapshot(snapshot)
	require.False(t, account.IsModelSupported("removed"), "a complete-catalog negative remains authoritative after positive snapshot expiry")
	resolved, matched := account.ResolveMappedModel("removed")
	require.Equal(t, "removed", resolved)
	require.False(t, matched, "the actual route resolver must not restore the expired mapping")
	snapshot.Normalizer = "older-normalizer"
	account.SetUpstreamModelAvailabilitySnapshot(snapshot)
	require.True(t, account.IsModelSupported("removed"), "a normalizer change invalidates the prior negative and permits revalidation")
	snapshot.Normalizer = upstreamModelNormalizerVersion

	changedIdentity := *account
	changedIdentity.Credentials = map[string]any{
		"base_url": "https://api.openai.com/v1", "api_key": "identity-b",
		"model_mapping": map[string]any{"removed": "gpt-5.6-luna"},
	}
	changedIdentity.SetUpstreamModelAvailabilitySnapshot(snapshot)
	require.True(t, changedIdentity.IsModelSupported("removed"), "negative evidence from another credential identity is not inherited")

	returned, err := buildTrustedAvailabilitySnapshot(profile, []string{"gpt-5.6-sol", "gpt-5.6-luna"}, nil, now)
	require.NoError(t, err)
	require.NoError(t, applyCompleteCatalogMappingNegatives(&returned, account, profile))
	require.NotContains(t, returned.ConfirmedAbsent, "gpt-5.6-luna", "a new positive complete snapshot clears the old negative")
}

func TestUnsupportedCatalogEndpointSuppressesOnlyMatchingAutomaticRefresh(t *testing.T) {
	now := time.Now().UTC()
	account := &Account{
		ID: 7011, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.openai.com/v1", "api_key": "identity-a", "model_mapping": map[string]any{"m": "gpt-5.6-sol"}},
	}
	profile := DetectUpstreamModelSourceProfile(account)
	snapshot, err := buildTrustedAvailabilitySnapshot(profile, []string{"gpt-5.6-sol"}, nil, now.Add(-48*time.Hour))
	require.NoError(t, err)
	snapshot.NextDueAt = now.Add(-time.Minute)
	snapshot.UnsupportedEndpoint = &UnsupportedModelCatalogEndpoint{
		SourceIdentity: profile.IdentityFingerprint, Normalizer: upstreamModelNormalizerVersion, StatusCode: 405,
	}
	account.SetUpstreamModelAvailabilitySnapshot(snapshot)
	require.False(t, upstreamModelRefreshDue(account, now), "confirmed 405 is not retried by the daily scheduler")

	account.Credentials["api_key"] = "identity-b"
	require.True(t, upstreamModelRefreshDue(account, now), "credential rotation invalidates the unsupported result")
	account.Credentials["api_key"] = "identity-a"
	snapshot.UnsupportedEndpoint.Normalizer = "older-normalizer"
	account.SetUpstreamModelAvailabilitySnapshot(snapshot)
	require.True(t, upstreamModelRefreshDue(account, now), "normalizer changes permit a new endpoint probe")
}

func TestAvailabilitySnapshotMigratesLegacyConfirmedAbsentLifecycleOnRead(t *testing.T) {
	lastSuccess := time.Now().UTC().Add(-upstreamAvailabilityStaleGrace - time.Hour)
	account := &Account{
		ID: 7012, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://api.openai.com/v1", "api_key": "legacy-identity",
			"model_mapping": map[string]any{"removed": "gpt-5.6-luna"},
		},
	}
	profile := DetectUpstreamModelSourceProfile(account)
	legacy := UpstreamModelAvailabilitySnapshot{
		SchemaVersion: upstreamAvailabilitySchemaVersion, SourceProfileID: profile.ID,
		SourceIdentity: profile.IdentityFingerprint, Status: "expired", LastSuccessAt: &lastSuccess,
		Normalizer: upstreamModelNormalizerVersion, RawDigest: "sha256:legacy-catalog",
		Lifecycle: map[string]ModelLifecycle{
			"gpt-5.6-luna": {State: "withdrawn", Source: "complete_catalog_negative", EvidenceRev: "sha256:legacy-catalog"},
		},
	}
	account.SetUpstreamModelAvailabilitySnapshot(legacy)

	migrated := account.GetUpstreamModelAvailabilitySnapshot()
	require.NotNil(t, migrated)
	require.Equal(t, "sha256:legacy-catalog", migrated.ConfirmedAbsent["gpt-5.6-luna"])
	require.NotContains(t, migrated.Lifecycle, "gpt-5.6-luna")
	stored := account.Extra[UpstreamModelAvailabilityExtraKey].(UpstreamModelAvailabilitySnapshot)
	require.Equal(t, "withdrawn", stored.Lifecycle["gpt-5.6-luna"].State, "read-time compatibility does not persist or mutate the old snapshot")
	require.False(t, account.IsModelSupported("removed"), "an expired legacy tombstone must not re-enable the mapping")
	resolved, matched := account.ResolveMappedModel("removed")
	require.Equal(t, "removed", resolved)
	require.False(t, matched, "the route resolver must honor the migrated negative")
}

func TestAvailabilitySnapshotRejectsIncompleteOrOversizedIDs(t *testing.T) {
	profile := UpstreamModelSourceProfile{ID: "openai-platform-api-key", Kind: "openai"}
	_, err := buildTrustedAvailabilitySnapshot(profile, []string{"model-a", ""}, nil, time.Now())
	require.Error(t, err)
	_, err = buildTrustedAvailabilitySnapshot(profile, []string{"model-" + string(make([]byte, upstreamAvailabilityMaxIDBytes+1))}, nil, time.Now())
	require.Error(t, err)
	_, err = buildTrustedAvailabilitySnapshot(profile, []string{"Model-A", "model-a"}, nil, time.Now())
	require.Error(t, err, "case-colliding source IDs are ambiguous and must not be silently merged")
	_, err = buildTrustedAvailabilitySnapshot(profile, []string{"model-a", "model-a"}, nil, time.Now())
	require.Error(t, err, "duplicate IDs make a supposedly complete catalog ambiguous")
}

func TestAvailabilitySnapshotAcceptsExactModelCountAndIDByteLimits(t *testing.T) {
	profile := UpstreamModelSourceProfile{ID: "custom-test-source", Kind: "custom"}
	models := make([]string, upstreamAvailabilityMaxIDs)
	for i := range models {
		models[i] = fmt.Sprintf("m-%04d", i)
	}
	snapshot, err := buildTrustedAvailabilitySnapshot(profile, models, nil, time.Now().UTC())
	require.NoError(t, err)
	require.Len(t, snapshot.RawModels, upstreamAvailabilityMaxIDs)
	require.Len(t, snapshot.PublicModels, upstreamAvailabilityMaxIDs)

	maxByteID := "model-" + strings.Repeat("x", upstreamAvailabilityMaxIDBytes-len("model-"))
	require.Len(t, maxByteID, upstreamAvailabilityMaxIDBytes)
	_, err = buildTrustedAvailabilitySnapshot(profile, []string{maxByteID}, nil, time.Now().UTC())
	require.NoError(t, err)
}

func TestAvailabilitySnapshotSizeAcceptsExactLimitAndRejectsLimitPlusOne(t *testing.T) {
	snapshot := UpstreamModelAvailabilitySnapshot{SchemaVersion: 1, SourceProfileID: "test", Status: "fresh"}
	base, err := json.Marshal(snapshot)
	require.NoError(t, err)
	snapshot.LastError = "x"
	withOne, err := json.Marshal(snapshot)
	require.NoError(t, err)
	keyAndQuotes := len(withOne) - len(base) - 1
	snapshot.LastError = strings.Repeat("x", upstreamAvailabilityMaxBytes-len(base)-keyAndQuotes)
	require.NoError(t, validateUpstreamModelAvailabilitySnapshotSize(snapshot), "the inclusive snapshot byte ceiling is valid")
	snapshot.LastError += "x"
	require.ErrorContains(t, validateUpstreamModelAvailabilitySnapshotSize(snapshot), "snapshot exceeds")
}

func TestNextModelRefreshAtUsesFourAMShanghai(t *testing.T) {
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC) // 08:00 UTC+8
	require.Equal(t, time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC), nextModelRefreshAt(now))
	now = time.Date(2026, 10, 1, 19, 59, 0, 0, time.UTC) // 03:59 UTC+8
	require.Equal(t, time.Date(2026, 10, 1, 20, 0, 0, 0, time.UTC), nextModelRefreshAt(now))
}

func TestConfiguredModelRefreshScheduleDrivesDueAndNextTimes(t *testing.T) {
	now := time.Date(2026, 10, 2, 1, 0, 0, 0, time.UTC) // 09:00 UTC+8
	require.Equal(t, time.Date(2026, 10, 1, 1, 30, 0, 0, time.UTC), latestModelRefreshAtForSchedule(now, 9, 30))
	require.Equal(t, time.Date(2026, 10, 2, 1, 30, 0, 0, time.UTC), nextModelRefreshAtForSchedule(now, 9, 30))
}

func TestTrustedManualMappedAccountRefreshesForRemovalEvidence(t *testing.T) {
	now := time.Now().UTC()
	account := &Account{
		ID: 8801, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://api.openai.com/v1", "api_key": "test",
			"model_mapping": map[string]any{"official-alias": "gpt-5.6-sol"},
		},
		Extra: map[string]any{UpstreamModelPolicyExtraKey: UpstreamModelPolicyManual},
	}
	require.True(t, upstreamModelRefreshDue(account, now), "manual must not suppress trusted refresh needed to confirm mapped model removal")

	profile := DetectUpstreamModelSourceProfile(account)
	snapshot, err := buildTrustedAvailabilitySnapshot(profile, []string{"gpt-5.6-sol"}, nil, now.Add(-48*time.Hour))
	require.NoError(t, err)
	snapshot.NextDueAt = now.Add(24 * time.Hour)
	account.SetUpstreamModelAvailabilitySnapshot(snapshot)
	require.False(t, upstreamModelRefreshDue(account, now), "a fresh manual snapshot is not due")
	snapshot.NextDueAt = now.Add(-time.Second)
	account.SetUpstreamModelAvailabilitySnapshot(snapshot)
	require.True(t, upstreamModelRefreshDue(account, now), "the next daily refresh updates negative availability evidence")

	neverConfigured := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.openai.com/v1", "api_key": "test"}}
	require.False(t, upstreamModelRefreshDue(neverConfigured, now), "manual account without mappings/snapshot should not be fetched")
	unsupported := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://gateway.example/v1", "api_key": "test", "model_mapping": map[string]any{"model": "model"}}}
	require.False(t, upstreamModelRefreshDue(unsupported, now), "unverified compatible source remains manual-only")
}

func TestAvailabilitySnapshotIsBoundToSourceOriginAndCredential(t *testing.T) {
	now := time.Now().UTC()
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"base_url": "https://api.openai.com/v1", "api_key": "first-secret",
	}}
	profile := DetectUpstreamModelSourceProfile(account)
	snapshot, err := buildTrustedAvailabilitySnapshot(profile, []string{"gpt-5.6-sol"}, nil, now)
	require.NoError(t, err)
	account.SetUpstreamModelAvailabilitySnapshot(snapshot)
	got, _, usable := account.usableUpstreamModelAvailabilitySnapshot(now)
	require.True(t, usable)
	require.NotNil(t, got)
	require.True(t, snapshot.NextDueAt.After(now))
	changedEntitlement := *account
	changedEntitlement.Credentials = map[string]any{
		"base_url": "https://api.openai.com/v1", "api_key": "first-secret", "plan_type": "different-tier",
	}
	got, _, usable = changedEntitlement.usableUpstreamModelAvailabilitySnapshot(now)
	require.False(t, usable, "plan/entitlement changes invalidate the snapshot even when endpoint and credential are unchanged")
	require.Nil(t, got)
	legacySnapshot := snapshot
	legacySnapshot.Normalizer = "model-catalog-v1"
	legacyAccount := *account
	legacyAccount.Extra = map[string]any{}
	legacyAccount.SetUpstreamModelAvailabilitySnapshot(legacySnapshot)
	got, _, usable = legacyAccount.usableUpstreamModelAvailabilitySnapshot(now)
	require.False(t, usable, "a prior normalizer registry cannot reuse an old public-to-upstream binding")
	require.Nil(t, got)

	account.Credentials["api_key"] = "rotated-secret"
	got, _, usable = account.usableUpstreamModelAvailabilitySnapshot(now)
	require.False(t, usable, "credential rotation must invalidate a prior account-entitlement snapshot")
	require.Nil(t, got)
	require.True(t, upstreamModelRefreshDue(&Account{
		Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.openai.com/v1", "api_key": "rotated-secret"},
		Extra:       map[string]any{UpstreamModelPolicyExtraKey: UpstreamModelPolicyFollow, UpstreamModelAvailabilityExtraKey: snapshot},
	}, now), "a changed source identity must be eligible for immediate refresh")

	aliyun := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"base_url": "https://dashscope-intl.aliyuncs.com/compatible-mode/v1", "api_key": "same-secret",
	}}
	regionProfile := DetectUpstreamModelSourceProfile(aliyun)
	regionSnapshot, err := buildTrustedAvailabilitySnapshot(regionProfile, []string{"qwen-max"}, nil, now)
	require.NoError(t, err)
	aliyun.SetUpstreamModelAvailabilitySnapshot(regionSnapshot)
	aliyun.Credentials["base_url"] = "https://cn-hongkong.dashscope.aliyuncs.com/compatible-mode/v1"
	changedRegionProfile := DetectUpstreamModelSourceProfile(aliyun)
	require.Equal(t, regionProfile.ID, changedRegionProfile.ID, "both endpoints intentionally use the same adapter profile")
	require.NotEqual(t, regionProfile.IdentityFingerprint, changedRegionProfile.IdentityFingerprint)
	got, _, usable = aliyun.usableUpstreamModelAvailabilitySnapshot(now)
	require.False(t, usable, "same-family region changes must not reuse another endpoint's availability")
	require.Nil(t, got)
}

func TestUpstreamModelRefreshPolicyDefaultsManual(t *testing.T) {
	account := &Account{Extra: map[string]any{}}
	require.Equal(t, UpstreamModelPolicyManual, account.GetUpstreamModelPolicy())
	account.Extra[UpstreamModelPolicyExtraKey] = "unrecognized"
	require.Equal(t, UpstreamModelPolicyManual, account.GetUpstreamModelPolicy())
	account.Extra[UpstreamModelPolicyExtraKey] = UpstreamModelPolicyFollow
	require.Equal(t, UpstreamModelPolicyFollow, account.GetUpstreamModelPolicy())
}

func TestModelPolicyPreviewRejectsIncompleteGroupSnapshot(t *testing.T) {
	account := &Account{
		ID: 77, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
		GroupIDs:    []int64{10},
		Credentials: map[string]any{"base_url": "https://api.openai.com/v1", "api_key": "test"},
	}
	row, revision := buildUpstreamModelPolicyPreviewAccount(account, time.Now().UTC())
	require.False(t, row.Eligible)
	require.Equal(t, "group_snapshot_incomplete", row.IneligibleReason)
	require.False(t, revision.Eligible)
	require.Empty(t, revision.GroupRevisions)
}
