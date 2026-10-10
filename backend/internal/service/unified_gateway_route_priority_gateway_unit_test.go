//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestGatewayLegacySelectorAppliesRoutePriceAfterNativeFilters(t *testing.T) {
	groupID := int64(47)
	pricing := &unifiedGatewayRoutePricingSnapshot{config: UnifiedGatewayRoutePricingConfig{TargetGroupID: groupID}, entries: make(map[string]UnifiedGatewayRoutePricingEntry)}
	for id, multiplier := range map[int64]float64{1: 1, 2: 0.4} {
		entry := UnifiedGatewayRoutePricingEntry{AccountID: id, Model: "claude-sonnet-4-5", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePriorityFloat(multiplier)}
		pricing.entries[unifiedGatewayRoutePricingKey(groupID, id, entry.Model, entry.Kind, "", "", "", "", 0)] = entry
	}
	settings := &SettingService{}
	settings.routePricingSnapshot.Store(pricing)
	repoFor := func() *mockAccountRepoForPlatform {
		return &mockAccountRepoForPlatform{accounts: []Account{
			{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 1},
			{ID: 2, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 50},
		}}
	}
	group := groupID
	groupRepo := func() *mockGroupRepoForGateway {
		return &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: {ID: groupID, Platform: PlatformComposite}}}
	}

	enabled := &GatewayService{accountRepo: repoFor(), groupRepo: groupRepo(), cfg: testConfig(), settingService: settings}
	enabled.cfg.Gateway.UnifiedRoutePriority.Enabled = true
	selected, err := enabled.selectAccountForModelWithPlatform(context.Background(), &group, "", "claude-sonnet-4-5", nil, PlatformAnthropic)
	require.NoError(t, err)
	require.Equal(t, int64(2), selected.ID)

	disabled := &GatewayService{accountRepo: repoFor(), groupRepo: groupRepo(), cfg: testConfig(), settingService: settings}
	selected, err = disabled.selectAccountForModelWithPlatform(context.Background(), &group, "", "claude-sonnet-4-5", nil, PlatformAnthropic)
	require.NoError(t, err)
	require.Equal(t, int64(1), selected.ID)
}

func TestGatewayLoadAwareRoutePrioritySelectsCheaperAndRetriesNextLayer(t *testing.T) {
	newFixture := func(t *testing.T, modelRouting bool, acquireResults map[int64]bool) (*GatewayService, context.Context, int64, *mockConcurrencyCache) {
		t.Helper()
		groupID := int64(53)
		accounts := []Account{
			{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 5},
			{ID: 2, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 50, Concurrency: 5},
		}
		accountRepo := &mockAccountRepoForPlatform{accounts: accounts, accountsByID: map[int64]*Account{}}
		for i := range accountRepo.accounts {
			accountRepo.accountsByID[accountRepo.accounts[i].ID] = &accountRepo.accounts[i]
		}
		group := &Group{
			ID: groupID, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true,
			ModelRoutingEnabled: modelRouting,
			ModelRouting:        map[string][]int64{"claude-sonnet-4-5": {1, 2}},
		}
		groupRepo := &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: group}}
		pricing := &unifiedGatewayRoutePricingSnapshot{config: UnifiedGatewayRoutePricingConfig{TargetGroupID: groupID}, entries: make(map[string]UnifiedGatewayRoutePricingEntry)}
		for id, multiplier := range map[int64]float64{1: 1, 2: 0.4} {
			entry := UnifiedGatewayRoutePricingEntry{AccountID: id, Model: "claude-sonnet-4-5", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePriorityFloat(multiplier)}
			pricing.entries[unifiedGatewayRoutePricingKey(groupID, id, entry.Model, entry.Kind, "", "", "", "", 0)] = entry
		}
		settings := &SettingService{}
		settings.routePricingSnapshot.Store(pricing)
		cfg := testConfig()
		cfg.Gateway.UnifiedRoutePriority.Enabled = true
		cfg.Gateway.Scheduling.LoadBatchEnabled = true
		concurrencyCache := &mockConcurrencyCache{acquireResults: acquireResults}
		svc := &GatewayService{
			accountRepo: accountRepo, groupRepo: groupRepo, cache: &mockGatewayCacheForPlatform{}, cfg: cfg,
			settingService: settings, concurrencyService: NewConcurrencyService(concurrencyCache),
		}
		return svc, context.WithValue(context.Background(), ctxkey.Group, group), groupID, concurrencyCache
	}

	t.Run("ordinary load batch picks cheap route before native priority", func(t *testing.T) {
		svc, ctx, groupID, _ := newFixture(t, false, nil)
		selection, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", "claude-sonnet-4-5", nil, "", 0)
		require.NoError(t, err)
		require.Equal(t, int64(2), selection.Account.ID)
	})

	t.Run("slot failure advances from current cheap layer to next layer", func(t *testing.T) {
		svc, ctx, groupID, concurrencyCache := newFixture(t, false, map[int64]bool{2: false, 1: true})
		selection, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", "claude-sonnet-4-5", nil, "", 0)
		require.NoError(t, err)
		require.Equal(t, int64(1), selection.Account.ID)
		require.Equal(t, 2, concurrencyCache.acquireAccountCalls)
	})

	t.Run("ModelRouting candidate set also uses route price", func(t *testing.T) {
		svc, ctx, groupID, _ := newFixture(t, true, nil)
		selection, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", "claude-sonnet-4-5", nil, "", 0)
		require.NoError(t, err)
		require.Equal(t, int64(2), selection.Account.ID)
	})

	t.Run("Layer 3 wait fallback keeps the cheapest route layer first", func(t *testing.T) {
		svc, ctx, groupID, concurrencyCache := newFixture(t, false, nil)
		concurrencyCache.loadMap = map[int64]*AccountLoadInfo{
			1: {AccountID: 1, LoadRate: 100},
			2: {AccountID: 2, LoadRate: 100},
		}
		selection, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", "claude-sonnet-4-5", nil, "", 0)
		require.NoError(t, err)
		require.False(t, selection.Acquired)
		require.Equal(t, int64(2), selection.Account.ID)
	})
}

func TestGatewayMixedSchedulingLegacySelectorUsesRoutePrice(t *testing.T) {
	groupID := int64(61)
	accounts := []Account{
		{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 1},
		{ID: 2, Platform: PlatformAntigravity, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 50, Extra: map[string]any{"mixed_scheduling": true}},
	}
	accountRepo := &mockAccountRepoForPlatform{accounts: accounts, accountsByID: map[int64]*Account{}}
	for i := range accountRepo.accounts {
		accountRepo.accountsByID[accountRepo.accounts[i].ID] = &accountRepo.accounts[i]
	}
	group := &Group{ID: groupID, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true}
	groupRepo := &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: group}}
	pricing := &unifiedGatewayRoutePricingSnapshot{config: UnifiedGatewayRoutePricingConfig{TargetGroupID: groupID}, entries: make(map[string]UnifiedGatewayRoutePricingEntry)}
	for id, multiplier := range map[int64]float64{1: 1, 2: 0.4} {
		entry := UnifiedGatewayRoutePricingEntry{AccountID: id, Model: "claude-sonnet-4-5", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePriorityFloat(multiplier)}
		pricing.entries[unifiedGatewayRoutePricingKey(groupID, id, entry.Model, entry.Kind, "", "", "", "", 0)] = entry
	}
	settings := &SettingService{}
	settings.routePricingSnapshot.Store(pricing)
	cfg := testConfig()
	cfg.Gateway.UnifiedRoutePriority.Enabled = true
	svc := &GatewayService{accountRepo: accountRepo, groupRepo: groupRepo, cfg: cfg, settingService: settings}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)

	selected, err := svc.selectAccountWithMixedScheduling(ctx, &groupID, "", "claude-sonnet-4-5", nil, PlatformAnthropic)
	require.NoError(t, err)
	require.Equal(t, int64(2), selected.ID)
}
