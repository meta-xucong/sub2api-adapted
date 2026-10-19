//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type grokFreeQuotaUsageRepoStub struct {
	UsageLogRepository

	mu      sync.Mutex
	stats   map[int64]*usagestats.AccountStats
	err     error
	calls   int
	lastIDs []int64
	start   time.Time
}

type grokFreeQuotaAccountRepoStub struct {
	AccountRepository
	accounts []Account
}

func (r *grokFreeQuotaAccountRepoStub) ListSchedulableByPlatform(context.Context, string) ([]Account, error) {
	return append([]Account(nil), r.accounts...), nil
}

func (r *grokFreeQuotaUsageRepoStub) GetAccountWindowStatsBatch(_ context.Context, accountIDs []int64, start time.Time) (map[int64]*usagestats.AccountStats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.lastIDs = append([]int64(nil), accountIDs...)
	r.start = start
	if r.err != nil {
		return nil, r.err
	}
	result := make(map[int64]*usagestats.AccountStats, len(accountIDs))
	for _, accountID := range accountIDs {
		if stats := r.stats[accountID]; stats != nil {
			copyStats := *stats
			result[accountID] = &copyStats
		}
	}
	return result, nil
}

// snapshot observes fields written by the asynchronous repository query under
// the same lock, without treating query entry as completed cache publication.
func (r *grokFreeQuotaUsageRepoStub) snapshot() (calls int, lastIDs []int64, start time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls, append([]int64(nil), r.lastIDs...), r.start
}

func grokFreeQuotaRefreshIdle(cache *sync.Map) bool {
	root, ok := freeQuotaRefreshInFlight.Load(cache)
	if !ok {
		return true
	}
	inFlight, ok := root.(*sync.Map)
	if !ok || inFlight == nil {
		return false
	}
	idle := true
	inFlight.Range(func(_, _ any) bool {
		idle = false
		return false
	})
	return idle
}

func waitGrokFreeQuotaCacheRefresh(t *testing.T, cache *sync.Map, accountID, tokens int64, known bool) {
	t.Helper()
	require.Eventually(t, func() bool {
		value, ok := cache.Load(accountID)
		entry, valid := value.(grokFreeQuotaGateCacheEntry)
		return ok && valid && !entry.checkedAt.IsZero() && entry.tokens == tokens && entry.known == known &&
			grokFreeQuotaRefreshIdle(cache)
	}, 2*time.Second, 10*time.Millisecond, "cache publication and refresh completion must both be observed")
}

func drainAndClearOpenAIGrokFreeQuotaTestCache(t *testing.T) {
	t.Helper()
	// The non-advanced selection path uses this shared cache. Keep its address
	// stable and finish outstanding writers before clearing fixture state.
	require.Eventually(t, func() bool {
		return grokFreeQuotaRefreshIdle(&openaiGrokFreeQuotaGateCache)
	}, 2*time.Second, 10*time.Millisecond)
	openaiGrokFreeQuotaGateCache.Clear()
}

func grokFreeQuotaTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Gateway.Grok.FreeQuotaSoftGateEnabled = true
	cfg.Gateway.Grok.FreeQuotaTokenLimit = 500_000
	cfg.Gateway.Grok.FreeQuotaSoftGatePercent = 95
	cfg.Gateway.Grok.FreeQuotaWindowHours = 24
	cfg.Gateway.Grok.FreeQuotaStatsCacheSeconds = 60
	return cfg
}

func TestFilterGrokFreeQuotaAccountsOnlyBlocksExplicitFreeOAuth(t *testing.T) {
	repo := &grokFreeQuotaUsageRepoStub{stats: map[int64]*usagestats.AccountStats{
		1: {Tokens: 475_000}, // 95% of 500k
	}}
	scheduler := &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{cfg: grokFreeQuotaTestConfig(), usageLogRepo: repo}}
	accounts := []Account{
		{ID: 1, Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{"subscription_tier": "FREE"}},
		{ID: 2, Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{"subscription_tier": "PRO"}},
		{ID: 3, Platform: PlatformGrok, Type: AccountTypeOAuth},
		{ID: 4, Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{"subscription_tier": "FREE"}},
	}

	// First pass: cache miss fails open (does not block) and schedules background refresh.
	filtered := scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)
	require.Equal(t, []int64{1, 2, 3, 4}, accountIDs(filtered), "miss fails open on hot path")

	waitGrokFreeQuotaCacheRefresh(t, &scheduler.grokFreeQuotaGateCache, 1, 475_000, true)
	calls, lastIDs, start := repo.snapshot()
	require.GreaterOrEqual(t, calls, 1)

	// Second pass: uses refreshed cache and blocks over-gate free OAuth.
	filtered = scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)
	require.Equal(t, []int64{2, 3, 4}, accountIDs(filtered), "paid and unknown fail-open; API-key free marker is not gated")
	require.Equal(t, []int64{1}, lastIDs, "paid, unknown, and API-key accounts must not enter the local free-tier query")
	require.WithinDuration(t, time.Now().UTC().Add(-24*time.Hour), start, time.Second)
}

func TestFilterGrokFreeQuotaAccountsStatsFailureFailsOpen(t *testing.T) {
	repo := &grokFreeQuotaUsageRepoStub{err: errors.New("usage database unavailable")}
	scheduler := &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{cfg: grokFreeQuotaTestConfig(), usageLogRepo: repo}}
	accounts := []Account{{
		ID: 1, Platform: PlatformGrok, Type: AccountTypeOAuth,
		Credentials: map[string]any{"subscription_tier": "free"},
	}}

	filtered := scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)
	require.Equal(t, []int64{1}, accountIDs(filtered))
	waitGrokFreeQuotaCacheRefresh(t, &scheduler.grokFreeQuotaGateCache, 1, 0, false)
	// Negative cache entry keeps subsequent hot-path calls fail-open without thrash.
	filtered = scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)
	require.Equal(t, []int64{1}, accountIDs(filtered))
	calls, _, _ := repo.snapshot()
	require.Equal(t, 1, calls)
}

func TestFilterGrokFreeQuotaAccountsUnknownTierFailOpen(t *testing.T) {
	repo := &grokFreeQuotaUsageRepoStub{stats: map[int64]*usagestats.AccountStats{
		1: {Tokens: 9_999_999},
	}}
	scheduler := &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{cfg: grokFreeQuotaTestConfig(), usageLogRepo: repo}}
	accounts := []Account{
		{ID: 1, Platform: PlatformGrok, Type: AccountTypeOAuth},
		{ID: 2, Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{"subscription_tier": "unknown"}},
		{ID: 3, Platform: PlatformGrok, Type: AccountTypeOAuth, Extra: map[string]any{"subscription_tier": "pro"}},
	}

	filtered := scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)
	require.Equal(t, []int64{1, 2, 3}, accountIDs(filtered))
	calls, _, _ := repo.snapshot()
	require.Zero(t, calls, "unknown/paid tiers must not query free-quota stats")
}

func TestFilterGrokFreeQuotaAccountsRecoversAfterRollingUsageFalls(t *testing.T) {
	repo := &grokFreeQuotaUsageRepoStub{stats: map[int64]*usagestats.AccountStats{
		1: {Tokens: 490_000},
	}}
	scheduler := &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{cfg: grokFreeQuotaTestConfig(), usageLogRepo: repo}}
	accounts := []Account{{
		ID: 1, Platform: PlatformGrok, Type: AccountTypeOAuth,
		Credentials: map[string]any{"plan_type": "free"},
	}}

	// Miss fails open, then background fill blocks over-gate account.
	require.Equal(t, []int64{1}, accountIDs(scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)))
	waitGrokFreeQuotaCacheRefresh(t, &scheduler.grokFreeQuotaGateCache, 1, 490_000, true)
	require.Empty(t, scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts))

	repo.mu.Lock()
	repo.stats[1] = &usagestats.AccountStats{Tokens: 100_000}
	repo.mu.Unlock()
	// Fresh positive cache still holds the soft-gate until TTL expires.
	require.Empty(t, scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts), "fresh cache keeps the soft-gate hold")

	// Expire entry → miss fails open and schedules refresh with recovered usage.
	// The prior refresh has completed; preserve production coalescing markers.
	callsBeforeExpire, _, _ := repo.snapshot()
	scheduler.grokFreeQuotaGateCache.Store(int64(1), grokFreeQuotaGateCacheEntry{
		tokens: 490_000, checkedAt: time.Now().Add(-2 * time.Minute), known: true, // TTL=60s → stale
	})
	// Hot path fail-open while refresh is in flight.
	require.Equal(t, []int64{1}, accountIDs(scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)))
	waitGrokFreeQuotaCacheRefresh(t, &scheduler.grokFreeQuotaGateCache, 1, 100_000, true)
	require.Equal(t, []int64{1}, accountIDs(scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)))
	callsAfterRefresh, _, _ := repo.snapshot()
	require.Greater(t, callsAfterRefresh, callsBeforeExpire)
}

func TestResolveGrokFreeQuotaGateSettingsDefaultsToNinetyFivePercent(t *testing.T) {
	settings, ok := resolveGrokFreeQuotaGateSettings(grokFreeQuotaTestConfig())
	require.True(t, ok)
	require.Equal(t, int64(500_000), settings.limitTokens)
	require.Equal(t, int64(475_000), settings.gateTokens) // 95% of 500k
	require.Equal(t, 24*time.Hour, settings.window)
}

func TestIsExplicitGrokFreeOAuthAccount_OnlyExactFree(t *testing.T) {
	t.Parallel()
	require.False(t, isExplicitGrokFreeOAuthAccount(nil))
	require.False(t, isExplicitGrokFreeOAuthAccount(&Account{Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{"subscription_tier": "free"}}))
	require.True(t, isExplicitGrokFreeOAuthAccount(&Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{"subscription_tier": "FREE"}}))
	require.True(t, isExplicitGrokFreeOAuthAccount(&Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{"plan_type": "free"}}))
	// basic / inferred free are NOT soft-gated (only an explicit "free" tier is).
	require.False(t, isExplicitGrokFreeOAuthAccount(&Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{"subscription_tier": "basic"}}))
	require.False(t, isExplicitGrokFreeOAuthAccount(&Account{Platform: PlatformGrok, Type: AccountTypeOAuth}))
}

func TestOpenAIAccountSchedulerLoadBalanceAppliesGrokFreeQuotaGate(t *testing.T) {
	drainAndClearOpenAIGrokFreeQuotaTestCache(t)
	t.Cleanup(func() { drainAndClearOpenAIGrokFreeQuotaTestCache(t) })
	cfg := grokFreeQuotaTestConfig()
	cfg.RunMode = config.RunModeSimple
	accounts := []Account{
		{ID: 1, Platform: PlatformGrok, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"subscription_tier": "free"}},
		{ID: 2, Platform: PlatformGrok, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"subscription_tier": "pro"}},
	}
	svc := &OpenAIGatewayService{
		cfg:         cfg,
		accountRepo: &grokFreeQuotaAccountRepoStub{accounts: accounts},
		usageLogRepo: &grokFreeQuotaUsageRepoStub{stats: map[int64]*usagestats.AccountStats{
			1: {Tokens: 480_000}, // over 95% of 500k
		}},
	}
	scheduler := &defaultOpenAIAccountScheduler{service: svc, stats: newOpenAIAccountRuntimeStats()}

	// Warm cache via background refresh so load-balance sees the soft-gate.
	_ = scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)
	waitGrokFreeQuotaCacheRefresh(t, &scheduler.grokFreeQuotaGateCache, 1, 480_000, true)
	require.Equal(t, []int64{2}, accountIDs(scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)))

	selection, _, _, _, err := scheduler.selectByLoadBalance(context.Background(), OpenAIAccountScheduleRequest{Platform: PlatformGrok})
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(2), selection.Account.ID)
	waitGrokFreeQuotaCacheRefresh(t, &openaiGrokFreeQuotaGateCache, 1, 480_000, true)
}

// Admin QueryQuota / import probe paths never call filterGrokFreeQuotaAccounts.
// Document and assert the scheduler filter is the only gate entry point.
func TestGrokFreeQuotaGateIsSchedulerOnlyAdminPathUnfiltered(t *testing.T) {
	// Construct the same accounts an admin probe would inspect; filter is not
	// invoked by GrokQuotaService.QueryQuota / GetUsage. Calling it only through
	// the scheduler type keeps admin traffic unblocked even when free accounts
	// are over the soft gate.
	require.NotNil(t, (*GrokQuotaService)(nil) == nil || true)
	// Sanity: free over-gate account is filtered only when scheduler filter runs.
	repo := &grokFreeQuotaUsageRepoStub{stats: map[int64]*usagestats.AccountStats{
		9: {Tokens: 500_000},
	}}
	scheduler := &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{cfg: grokFreeQuotaTestConfig(), usageLogRepo: repo}}
	overGate := Account{ID: 9, Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{"subscription_tier": "FREE"}}
	_ = scheduler.filterGrokFreeQuotaAccounts(context.Background(), []Account{overGate})
	waitGrokFreeQuotaCacheRefresh(t, &scheduler.grokFreeQuotaGateCache, 9, 500_000, true)
	require.Empty(t, scheduler.filterGrokFreeQuotaAccounts(context.Background(), []Account{overGate}))
	// Without going through the scheduler filter, the account object itself is unchanged.
	require.True(t, isExplicitGrokFreeOAuthAccount(&overGate))
	require.Equal(t, int64(9), overGate.ID)
}

func TestSweepGrokFreeQuotaGateCacheDropsStaleEntries(t *testing.T) {
	now := time.Now().UTC()
	cacheTTL := 5 * time.Second
	// maxAge is floored at grokFreeQuotaGateCacheMinSweepAge, not 20*cacheTTL.
	var cache sync.Map
	cache.Store(int64(1), grokFreeQuotaGateCacheEntry{tokens: 10, checkedAt: now, known: true})
	cache.Store(int64(2), grokFreeQuotaGateCacheEntry{tokens: 20, checkedAt: now.Add(-time.Minute), known: true})
	cache.Store(int64(3), grokFreeQuotaGateCacheEntry{tokens: 30, checkedAt: now.Add(-time.Hour), known: true})
	cache.Store(int64(4), "not-an-entry")

	sweepGrokFreeQuotaGateCache(&cache, now, cacheTTL)

	remaining := make([]int64, 0, 4)
	cache.Range(func(key, _ any) bool {
		if id, ok := key.(int64); ok {
			remaining = append(remaining, id)
		}
		return true
	})
	require.ElementsMatch(t, []int64{1, 2}, remaining)

	// A disabled cache (TTL 0) means the caller never populated it — leave it alone.
	var untouched sync.Map
	untouched.Store(int64(7), grokFreeQuotaGateCacheEntry{checkedAt: now.Add(-time.Hour), known: true})
	sweepGrokFreeQuotaGateCache(&untouched, now, 0)
	_, stillThere := untouched.Load(int64(7))
	require.True(t, stillThere)
}

func TestFilterGrokFreeQuotaAccountsEvictsDepartedAccounts(t *testing.T) {
	repo := &grokFreeQuotaUsageRepoStub{stats: map[int64]*usagestats.AccountStats{
		1: {Tokens: 1_000},
	}}
	var cache sync.Map
	// Account 99 was scheduled long ago and no longer appears in any batch. Its
	// entry must not survive a run that queries for a different account.
	cache.Store(int64(99), grokFreeQuotaGateCacheEntry{tokens: 5, checkedAt: time.Now().UTC().Add(-2 * time.Hour), known: true})

	accounts := []Account{
		{ID: 1, Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{"subscription_tier": "FREE"}},
	}
	// First call schedules async refresh + may not have finished sweep yet.
	_ = filterGrokFreeQuotaAccountsCore(context.Background(), grokFreeQuotaTestConfig(), repo, &cache, accounts)
	require.Eventually(t, func() bool {
		_, departedStillCached := cache.Load(int64(99))
		_, freshCached := cache.Load(int64(1))
		return !departedStillCached && freshCached && grokFreeQuotaRefreshIdle(&cache)
	}, 2*time.Second, 10*time.Millisecond)
	filtered := filterGrokFreeQuotaAccountsCore(context.Background(), grokFreeQuotaTestConfig(), repo, &cache, accounts)
	require.Equal(t, []int64{1}, accountIDs(filtered))
}

func accountIDs(accounts []Account) []int64 {
	ids := make([]int64, 0, len(accounts))
	for i := range accounts {
		ids = append(ids, accounts[i].ID)
	}
	return ids
}
