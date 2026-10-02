package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildModelPolicyPreviewReportsExactVerifiedChangesAndMappingConflicts(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	account, snapshot := newModelPolicyPreviewAccount(t, now, map[string]string{
		"alias-kept":    "gpt-5.6-sol",
		"old-public":    "not-in-snapshot",
		"secret-public": "sk-live-do-not-return",
	})

	row, revision := buildUpstreamModelPolicyPreviewAccount(account, now)
	require.False(t, row.Eligible)
	require.Equal(t, "explicit_mapping_conflict", row.IneligibleReason)
	require.Equal(t, []string{"alias-kept", "gpt-5.5", "gpt-5.6-sol"}, row.AvailableModelIDs)
	require.Equal(t, []string{"gpt-5.5", "gpt-5.6-sol"}, row.AddedModelIDs)
	require.Equal(t, []string{"alias-kept"}, row.RetainedModelIDs)
	require.ElementsMatch(t, []string{"old-public", "secret-public"}, row.HiddenModelIDs)
	require.Len(t, row.MappingConflicts, 2)
	require.Equal(t, "target_not_in_snapshot", row.MappingConflicts[0].Reason)
	require.Equal(t, "old-public", row.MappingConflicts[0].PublicModelID)
	require.Equal(t, "invalid_mapping_target", row.MappingConflicts[1].Reason)
	require.Equal(t, "secret-public", row.MappingConflicts[1].PublicModelID)
	require.Equal(t, "[redacted]", row.MappingConflicts[0].ConfiguredTargetPreview)
	require.Equal(t, "[redacted]", row.MappingConflicts[1].ConfiguredTargetPreview)
	require.Equal(t, 2, row.AddedModelCount)
	require.Equal(t, 1, row.RetainedModelCount)
	require.Equal(t, 2, row.HiddenModelCount)
	require.Equal(t, 2, row.MappingConflictCount)
	require.NotEmpty(t, revision.SnapshotDigest)
	require.NotEqual(t, snapshot.RawDigest, revision.SnapshotDigest, "preview revision binds route and lifecycle state, not only raw IDs")

	encoded, err := json.Marshal(row)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "sk-live-do-not-return")
	require.NotContains(t, string(encoded), "api.openai.com")
	require.NotContains(t, string(encoded), "account-api-key")
}

func TestBuildModelPolicyPreviewAllowsOnlyCompleteFreshOrGraceSnapshots(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	account, snapshot := newModelPolicyPreviewAccount(t, now, nil)
	snapshot.Status = "stale"
	lastSuccess := now.Add(-upstreamAvailabilityStaleGrace + time.Minute)
	snapshot.LastSuccessAt = &lastSuccess
	account.SetUpstreamModelAvailabilitySnapshot(*snapshot)

	row, _ := buildUpstreamModelPolicyPreviewAccount(account, now)
	require.True(t, row.Eligible)
	require.Empty(t, row.IneligibleReason)
	require.Equal(t, "stale", row.SnapshotStatus)
	require.Equal(t, []string{"gpt-5.5", "gpt-5.6-sol"}, row.AvailableModelIDs)
	require.ElementsMatch(t, row.AvailableModelIDs, row.AddedModelIDs)
}

func TestBuildModelPolicyPreviewRejectsUntrustedSnapshotsWithoutAvailableModels(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*Account, **UpstreamModelAvailabilitySnapshot)
		want   string
	}{
		{
			name: "never fetched",
			mutate: func(account *Account, snapshot **UpstreamModelAvailabilitySnapshot) {
				*snapshot = nil
				delete(account.Extra, UpstreamModelAvailabilityExtraKey)
			},
			want: "snapshot_never_fetched",
		},
		{
			name: "malformed stored snapshot is not never fetched",
			mutate: func(account *Account, snapshot **UpstreamModelAvailabilitySnapshot) {
				*snapshot = nil
				account.Extra[UpstreamModelAvailabilityExtraKey] = map[string]any{"schema_version": 99, "status": "fresh"}
			},
			want: "snapshot_incomplete",
		},
		{
			name: "no complete successful snapshot",
			mutate: func(_ *Account, snapshot **UpstreamModelAvailabilitySnapshot) {
				(*snapshot).Status = "fresh"
				(*snapshot).LastSuccessAt = nil
			},
			want: "snapshot_incomplete",
		},
		{
			name: "snapshot source mismatch",
			mutate: func(_ *Account, snapshot **UpstreamModelAvailabilitySnapshot) {
				(*snapshot).SourceProfileID = "unverified-reseller"
			},
			want: "snapshot_source_mismatch",
		},
		{
			name: "stale grace expired",
			mutate: func(_ *Account, snapshot **UpstreamModelAvailabilitySnapshot) {
				(*snapshot).Status = "stale"
				lastSuccess := now.Add(-upstreamAvailabilityStaleGrace - time.Second)
				(*snapshot).LastSuccessAt = &lastSuccess
			},
			want: "snapshot_expired",
		},
		{
			name: "failed refresh status",
			mutate: func(_ *Account, snapshot **UpstreamModelAvailabilitySnapshot) {
				(*snapshot).Status = "error"
			},
			want: "snapshot_not_successful",
		},
		{
			name: "raw digest mismatch",
			mutate: func(_ *Account, snapshot **UpstreamModelAvailabilitySnapshot) {
				(*snapshot).RawDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
			},
			want: "snapshot_incomplete",
		},
		{
			name: "public model without route",
			mutate: func(_ *Account, snapshot **UpstreamModelAvailabilitySnapshot) {
				delete((*snapshot).PublicToUpstream, "gpt-5.5")
			},
			want: "snapshot_incomplete",
		},
		{
			name: "future last success",
			mutate: func(_ *Account, snapshot **UpstreamModelAvailabilitySnapshot) {
				lastSuccess := now.Add(time.Minute)
				(*snapshot).LastSuccessAt = &lastSuccess
			},
			want: "snapshot_incomplete",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, snapshot := newModelPolicyPreviewAccount(t, now, nil)
			tt.mutate(account, &snapshot)
			if snapshot != nil {
				account.SetUpstreamModelAvailabilitySnapshot(*snapshot)
			}

			row, revision := buildUpstreamModelPolicyPreviewAccount(account, now)
			require.False(t, row.Eligible)
			require.Equal(t, tt.want, row.IneligibleReason)
			require.Empty(t, row.AvailableModelIDs)
			require.Empty(t, row.AddedModelIDs)
			require.Empty(t, row.RetainedModelIDs)
			require.False(t, revision.Eligible)
		})
	}
}

func TestBuildModelPolicyPreviewHidesRetiredAndQuarantinedOnlyCatalog(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	account, snapshot := newModelPolicyPreviewAccount(t, now, nil)
	snapshot.Lifecycle["gpt-5.5"] = ModelLifecycle{State: "retired"}
	account.SetUpstreamModelAvailabilitySnapshot(*snapshot)
	account.Extra[UpstreamModelPolicyExtraKey] = UpstreamModelPolicyManual

	// Keep only a retired published ID and a quarantined raw ID in the directory.
	snapshot.RawModels = []string{"gpt-5.5", "gpt-6"}
	snapshot.PublicModels = []string{"gpt-5.5"}
	snapshot.PublicToUpstream = map[string]string{"gpt-5.5": "gpt-5.5"}
	snapshot.RawDigest = modelCatalogRawDigest(snapshot.RawModels)
	account.SetUpstreamModelAvailabilitySnapshot(*snapshot)

	row, _ := buildUpstreamModelPolicyPreviewAccount(account, now)
	require.False(t, row.Eligible)
	require.Equal(t, "snapshot_contains_retired_model", row.IneligibleReason)
	require.Empty(t, row.AvailableModelIDs)
	require.Empty(t, row.AddedModelIDs)
	require.ElementsMatch(t, []string{"gpt-5.5", "gpt-6"}, row.HiddenModelIDs)
}

func TestBuildModelPolicyPreviewRejectsMixedRetiredOrQuarantinedCatalog(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name          string
		rawModels     []string
		publicID      string
		lifecycle     map[string]ModelLifecycle
		mappingTarget string
		wantReason    string
		wantHidden    string
		wantConflict  string
	}{
		{
			name:      "retired entry rejects opt-in despite another valid model",
			rawModels: []string{"gpt-5.5", "old-model"}, publicID: "gpt-5.5",
			lifecycle:     map[string]ModelLifecycle{"old-model": {State: "retired"}},
			mappingTarget: "old-model",
			wantReason:    "snapshot_contains_retired_model", wantHidden: "old-model", wantConflict: "target_retired",
		},
		{
			name:      "quarantined entry rejects opt-in despite another valid model",
			rawModels: []string{"gpt-5.5", "gpt-6"}, publicID: "gpt-5.5",
			lifecycle:     map[string]ModelLifecycle{},
			mappingTarget: "gpt-6",
			wantReason:    "snapshot_contains_quarantined_model", wantHidden: "gpt-6", wantConflict: "target_quarantined",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, snapshot := newModelPolicyPreviewAccount(t, now, nil)
			snapshot.RawModels = tt.rawModels
			snapshot.PublicModels = []string{tt.publicID}
			snapshot.PublicToUpstream = map[string]string{tt.publicID: tt.publicID}
			snapshot.Lifecycle = tt.lifecycle
			snapshot.RawDigest = modelCatalogRawDigest(tt.rawModels)
			account.SetUpstreamModelAvailabilitySnapshot(*snapshot)
			account.Credentials["model_mapping"] = map[string]any{"legacy-public": tt.mappingTarget}

			row, _ := buildUpstreamModelPolicyPreviewAccount(account, now)
			require.False(t, row.Eligible)
			require.Equal(t, tt.wantReason, row.IneligibleReason)
			require.Empty(t, row.AvailableModelIDs)
			require.Empty(t, row.AddedModelIDs)
			require.Contains(t, row.HiddenModelIDs, tt.wantHidden)
			require.Contains(t, row.HiddenModelIDs, "legacy-public")
			require.Len(t, row.MappingConflicts, 1)
			require.Equal(t, tt.wantConflict, row.MappingConflicts[0].Reason)
		})
	}
}

func TestBuildModelPolicyPreviewPreservesFullIDListsAndExactCounts(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	models := make([]string, 25)
	for i := range models {
		models[i] = fmt.Sprintf("model-%02d", i)
	}
	profile := UpstreamModelSourceProfile{ID: "openai-platform-api-key", Kind: "openai"}
	snapshot, err := buildTrustedAvailabilitySnapshot(profile, models, nil, now)
	require.NoError(t, err)
	account := &Account{
		ID: 51, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
		Credentials: map[string]any{"base_url": "https://api.openai.com/v1", "api_key": "test"},
	}
	account.SetUpstreamModelAvailabilitySnapshot(snapshot)

	row, _ := buildUpstreamModelPolicyPreviewAccount(account, now)
	require.True(t, row.Eligible)
	require.Equal(t, 25, row.AvailableModelCount)
	require.Equal(t, models, row.AvailableModelIDs)
	require.Equal(t, 25, row.AddedModelCount)
	require.Equal(t, models, row.AddedModelIDs)
}

func newModelPolicyPreviewAccount(t *testing.T, now time.Time, mapping map[string]string) (*Account, *UpstreamModelAvailabilitySnapshot) {
	t.Helper()
	profile := UpstreamModelSourceProfile{ID: "openai-platform-api-key", Kind: "openai"}
	snapshot, err := buildTrustedAvailabilitySnapshot(profile, []string{"gpt-5.5", "gpt-5.6-sol"}, nil, now)
	require.NoError(t, err)
	modelMapping := make(map[string]any, len(mapping))
	for publicID, target := range mapping {
		modelMapping[publicID] = target
	}
	account := &Account{
		ID: 51, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
		Credentials: map[string]any{
			"base_url":      "https://api.openai.com/v1",
			"api_key":       "account-api-key-must-not-leak",
			"model_mapping": modelMapping,
		},
	}
	account.SetUpstreamModelAvailabilitySnapshot(snapshot)
	return account, &snapshot
}

func modelCatalogRawDigest(models []string) string {
	clean, err := validateUpstreamModelIDs(models)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256([]byte(strings.Join(clean, "\x00")))
	return "sha256:" + hex.EncodeToString(digest[:])
}
