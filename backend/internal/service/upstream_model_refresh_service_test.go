package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"
)

func TestNextUpstreamModelRefreshAtUsesDailyFourAMUTCPlus8(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)

	before := time.Date(2026, 9, 26, 3, 59, 0, 0, location)
	require.Equal(t, "2026-09-25T20:00:00Z", nextUpstreamModelRefreshAt(before).Format(time.RFC3339))

	after := time.Date(2026, 9, 26, 4, 1, 0, 0, location)
	require.Equal(t, "2026-09-26T20:00:00Z", nextUpstreamModelRefreshAt(after).Format(time.RFC3339))

	exact := time.Date(2026, 9, 26, 4, 0, 0, 0, location)
	require.Equal(t, "2026-09-26T20:00:00Z", nextUpstreamModelRefreshAt(exact).Format(time.RFC3339))
}

func TestCanonicalUpstreamModelSnapshotFiltersRetiredAndNormalizesIDs(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI}
	snapshot := canonicalUpstreamModelSnapshot(account, []string{
		"gpt-5.5",
		"gpt-5.6",
		"gpt-5.6-luna",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-5.5-20260901",
		"retired-model",
		"bad-*",
	}, time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC))

	require.Equal(t, UpstreamModelRefreshStatusFresh, snapshot.Status)
	require.Equal(t, []string{"gpt-5.5", "gpt-5.5-20260901", "gpt-5.6", "gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "retired-model"}, snapshot.RawModels)
	require.Equal(t, []string{"gpt-5.5", "gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "retired-model"}, snapshot.PublicModels)
	require.NotContains(t, snapshot.PublicModels, "gpt-5.6")
	require.Equal(t, "gpt-5.5", snapshot.PublicToUpstream["gpt-5.5"])
	require.NotEmpty(t, snapshot.RawDigest)
}

func TestAccountAvailabilitySnapshotControlsRoutingAndCanonicalAlias(t *testing.T) {
	account := &Account{
		Platform: PlatformDeepseek,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"deepseek-v4-flash-0731": "deepseek-v4-flash-0731",
			},
		},
	}
	account.SetUpstreamModelRefreshSnapshot(canonicalUpstreamModelSnapshot(account, []string{"deepseek-v4-flash-0731"}, time.Now()))

	require.True(t, account.IsModelSupported("deepseek-v4-flash"))
	require.Equal(t, "deepseek-v4-flash-0731", account.GetMappedModel("deepseek-v4-flash"))
	require.False(t, account.IsModelSupported("deepseek-v4-pro"))

	snapshot := *account.GetUpstreamModelRefreshSnapshot()
	snapshot.Status = UpstreamModelRefreshStatusExpired
	account.SetUpstreamModelRefreshSnapshot(snapshot)
	require.False(t, account.IsModelSupported("deepseek-v4-flash"))
}

func TestAccountAvailabilitySnapshotRejectsHiddenRawModelIDs(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI}
	account.SetUpstreamModelRefreshSnapshot(canonicalUpstreamModelSnapshot(account, []string{
		"gpt-5.6",
		"gpt-5.6-luna",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
	}, time.Now()))

	// The parent is retained only as upstream evidence. It is not a public or
	// routable ID when the formal sibling names are available.
	require.False(t, account.IsModelSupported("gpt-5.6"))
	require.Equal(t, "gpt-5.6-sol", account.GetMappedModel("gpt-5.6-sol"))
	require.True(t, account.IsModelSupported("gpt-5.6-sol"))
}

func TestAccountAvailabilitySnapshotFiltersConfiguredRetiredModels(t *testing.T) {
	account := &Account{
		Platform: PlatformAnthropic,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"claude-live":    "claude-live",
				"claude-retired": "claude-retired",
			},
		},
	}
	account.SetUpstreamModelRefreshSnapshot(canonicalUpstreamModelSnapshot(account, []string{"claude-live"}, time.Now()))

	models, authoritative := account.AvailablePublicModelIDs()
	require.True(t, authoritative)
	require.Equal(t, []string{"claude-live"}, models)
	require.True(t, account.IsModelSupported("claude-live"))
	require.False(t, account.IsModelSupported("claude-retired"))
}

func TestGatewayAvailableModelsUsesAuthoritativeSnapshot(t *testing.T) {
	groupID := int64(9101)
	account := Account{
		ID:       7,
		Platform: PlatformAnthropic,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"claude-live":    "claude-live",
				"claude-retired": "claude-retired",
			},
		},
	}
	account.SetUpstreamModelRefreshSnapshot(canonicalUpstreamModelSnapshot(&account, []string{"claude-live"}, time.Now()))
	repo := &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {account}}}
	svc := &GatewayService{
		accountRepo:        repo,
		modelsListCache:    gocache.New(time.Minute, time.Minute),
		modelsListCacheTTL: time.Minute,
	}

	models := svc.GetAvailableModels(context.Background(), &groupID, PlatformAnthropic)
	require.Equal(t, []string{"claude-live"}, models)

	snapshot := *account.GetUpstreamModelRefreshSnapshot()
	snapshot.Status = UpstreamModelRefreshStatusExpired
	account.SetUpstreamModelRefreshSnapshot(snapshot)
	repo.byGroup[groupID] = []Account{account}
	svc.InvalidateAvailableModelsCache(&groupID, PlatformAnthropic)
	models = svc.GetAvailableModels(context.Background(), &groupID, PlatformAnthropic)
	require.NotNil(t, models)
	require.Empty(t, models)
}

func TestPersistUpstreamModelRefreshFailureKeepsThenExpiresLastSuccess(t *testing.T) {
	repo := &upstreamModelMetadataRepoStub{}
	svc := &AccountTestService{accountRepo: repo}
	account := &Account{ID: 123, Platform: PlatformAnthropic}
	lastSuccess := time.Now().Add(-time.Hour)
	snapshot := canonicalUpstreamModelSnapshot(account, []string{"claude-live"}, lastSuccess)
	account.SetUpstreamModelRefreshSnapshot(snapshot)

	err := newUpstreamModelSyncUpstreamError("upstream unavailable", nil)
	now := lastSuccess.Add(2 * time.Hour)
	require.NoError(t, svc.persistUpstreamModelRefreshFailure(context.Background(), account, err, now, 24*time.Hour))
	require.Equal(t, UpstreamModelRefreshStatusStale, account.GetUpstreamModelRefreshSnapshot().Status)
	require.Equal(t, []string{"claude-live"}, account.GetUpstreamModelRefreshSnapshot().PublicModels)

	now = lastSuccess.Add(26 * time.Hour)
	require.NoError(t, svc.persistUpstreamModelRefreshFailure(context.Background(), account, err, now, 24*time.Hour))
	require.Equal(t, UpstreamModelRefreshStatusExpired, account.GetUpstreamModelRefreshSnapshot().Status)
}

type upstreamModelRefreshAccountRepoStub struct {
	AccountRepository
	accounts []Account
	updates  map[string]any
}

func (r *upstreamModelRefreshAccountRepoStub) ListActive(context.Context) ([]Account, error) {
	return append([]Account(nil), r.accounts...), nil
}

func (r *upstreamModelRefreshAccountRepoStub) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.updates = updates
	return nil
}

func TestUpstreamModelRefreshRunOnceUsesSharedSyncCore(t *testing.T) {
	repo := &upstreamModelRefreshAccountRepoStub{accounts: []Account{{
		ID:       321,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "test-key",
			"base_url": "https://provider.example/v1",
		},
	}}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"live-model","reasoning":false,"input_modalities":["text"],"context_window":1000,"max_output_tokens":100}]}`)),
	}}
	accountTestSvc := &AccountTestService{
		accountRepo:  repo,
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	refreshSvc := NewUpstreamModelRefreshService(repo, accountTestSvc, nil, nil, &config.Config{
		Gateway: config.GatewayConfig{
			UpstreamModelRefreshEnabled:               true,
			UpstreamModelRefreshRequestTimeoutSeconds: 5,
			UpstreamModelRefreshStaleGraceHours:       48,
			UpstreamModelRefreshMaxConcurrency:        1,
		},
	})

	result, err := refreshSvc.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Accounts)
	require.Equal(t, 1, result.Succeeded)
	require.Equal(t, 0, result.Failed)
	_, ok := repo.updates[UpstreamModelRefreshExtraKey]
	require.True(t, ok)
	require.Equal(t, "https://provider.example/v1/models", upstream.lastReq.URL.String())
}

func TestUpstreamModelRefreshRunOnceFailsClosedOnMissingDependencies(t *testing.T) {
	service := NewUpstreamModelRefreshService(nil, nil, nil, nil, &config.Config{Gateway: config.GatewayConfig{UpstreamModelRefreshEnabled: true}})
	_, err := service.RunOnce(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "account repository")
}
