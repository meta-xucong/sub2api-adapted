package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/routepriority"
	"github.com/stretchr/testify/require"
)

func TestGatewayRoutePriorityPrecedesNativeAccountPriority(t *testing.T) {
	cheap := &Account{ID: 1, Priority: 50}
	dear := &Account{ID: 2, Priority: 1}
	ranks := map[int64]routepriority.Rank{
		cheap.ID: {HealthLayer: 0, PriceLayer: 0},
		dear.ID:  {HealthLayer: 0, PriceLayer: 1},
	}
	selected := selectGatewayAccountByNativeOrder(routePriorityMinLayerAccounts([]*Account{dear, cheap}, ranks), false, false)
	require.Equal(t, cheap.ID, selected.ID)
}

func TestGatewayRoutePrioritySlotFailureAdvancesToNextPriceLayer(t *testing.T) {
	accounts := []accountWithLoad{
		{account: &Account{ID: 1, Priority: 5}, loadInfo: &AccountLoadInfo{AccountID: 1, LoadRate: 10}},
		{account: &Account{ID: 2, Priority: 1}, loadInfo: &AccountLoadInfo{AccountID: 2, LoadRate: 0}},
	}
	ranks := map[int64]routepriority.Rank{
		1: {HealthLayer: 0, PriceLayer: 0},
		2: {HealthLayer: 0, PriceLayer: 1},
	}
	first := filterGatewayAccountLoadsByMinRoutePriority(accounts, ranks)
	require.Len(t, first, 1)
	require.Equal(t, int64(1), first[0].account.ID)
	remaining := []accountWithLoad{accounts[1]}
	next := filterGatewayAccountLoadsByMinRoutePriority(remaining, ranks)
	require.Len(t, next, 1)
	require.Equal(t, int64(2), next[0].account.ID)
}

func TestGatewayRoutePriorityLoadSortingKeepsNativeSortingInsideLayer(t *testing.T) {
	items := []accountWithLoad{
		{account: &Account{ID: 1, Priority: 2}, loadInfo: &AccountLoadInfo{AccountID: 1, LoadRate: 20}},
		{account: &Account{ID: 2, Priority: 1}, loadInfo: &AccountLoadInfo{AccountID: 2, LoadRate: 10}},
		{account: &Account{ID: 3, Priority: 1}, loadInfo: &AccountLoadInfo{AccountID: 3, LoadRate: 5}},
	}
	ranks := map[int64]routepriority.Rank{
		1: {HealthLayer: 0, PriceLayer: 0},
		2: {HealthLayer: 0, PriceLayer: 0},
		3: {HealthLayer: 1, PriceLayer: 0},
	}
	ordered := sortGatewayAccountLoadsWithRoutePriority(items, ranks)
	require.Equal(t, int64(2), ordered[0].account.ID)
	require.Equal(t, int64(1), ordered[1].account.ID)
	require.Equal(t, int64(3), ordered[2].account.ID)
}

func TestGatewayRoutePriorityFallbackStableSortDoesNotCrossLayers(t *testing.T) {
	accounts := []*Account{{ID: 3, Priority: 1}, {ID: 2, Priority: 1}, {ID: 1, Priority: 5}}
	ranks := map[int64]routepriority.Rank{
		1: {HealthLayer: 0, PriceLayer: 0},
		2: {HealthLayer: 0, PriceLayer: 1},
		3: {HealthLayer: 1, PriceLayer: 0},
	}
	stableSortAccountsByRoutePriority(accounts, ranks)
	require.Equal(t, []int64{1, 2, 3}, []int64{accounts[0].ID, accounts[1].ID, accounts[2].ID})
}

func TestOpenAIRoutePriorityControlsTopKAndBoundsWeightedRandomness(t *testing.T) {
	candidates := []openAIAccountCandidateScore{
		{account: &Account{ID: 1}, score: 1, routePrioritySet: true, routeHealthLayer: 0, routePriceLayer: 0},
		{account: &Account{ID: 2}, score: 2, routePrioritySet: true, routeHealthLayer: 0, routePriceLayer: 0},
		{account: &Account{ID: 3}, score: 100, routePrioritySet: true, routeHealthLayer: 0, routePriceLayer: 1},
	}
	top := selectTopKOpenAICandidates(candidates, 1)
	require.Len(t, top, 1)
	require.NotEqual(t, int64(3), top[0].account.ID)
	ordered := buildOpenAIWeightedSelectionOrder(candidates, OpenAIAccountScheduleRequest{RequestedModel: "m", StickyAccountID: 0})
	require.Len(t, ordered, 3)
	require.NotEqual(t, int64(3), ordered[0].account.ID)
	require.Equal(t, int64(3), ordered[2].account.ID)
}

func TestOpenAISmartRouterPostOrderCannotCrossRouteLayers(t *testing.T) {
	candidates := []openAIAccountCandidateScore{
		{account: &Account{ID: 2}, routePrioritySet: true, routeHealthLayer: 0, routePriceLayer: 1},
		{account: &Account{ID: 1}, routePrioritySet: true, routeHealthLayer: 0, routePriceLayer: 0},
		{account: &Account{ID: 3}, routePrioritySet: true, routeHealthLayer: 1, routePriceLayer: 0},
	}
	var service *OpenAIGatewayService
	ordered := service.reorderSmartRouterSelectionCandidates(nil, OpenAIAccountScheduleRequest{}, candidates)
	require.Equal(t, []int64{1, 2, 3}, []int64{ordered[0].account.ID, ordered[1].account.ID, ordered[2].account.ID})
}

func TestOpenAIAdvancedSelectorAppliesRoutePriceBeforeTopKOnlyWhenEnabled(t *testing.T) {
	groupID := int64(23)
	pricing := &unifiedGatewayRoutePricingSnapshot{
		config:  UnifiedGatewayRoutePricingConfig{TargetGroupID: groupID},
		entries: make(map[string]UnifiedGatewayRoutePricingEntry),
	}
	for id, multiplier := range map[int64]float64{1: 1, 2: 0.4} {
		entry := UnifiedGatewayRoutePricingEntry{AccountID: id, Model: "gpt-5.6", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePriorityFloat(multiplier)}
		pricing.entries[unifiedGatewayRoutePricingKey(groupID, id, entry.Model, entry.Kind, "", "", "", "", 0)] = entry
	}
	settings := &SettingService{}
	settings.routePricingSnapshot.Store(pricing)
	req := OpenAIAccountScheduleRequest{GroupID: &groupID, RequestedModel: "gpt-5.6", RequiredCapability: OpenAIEndpointCapabilityChatCompletions}
	plan := openAIAccountLoadPlan{topK: 1, candidates: []openAIAccountCandidateScore{
		{account: &Account{ID: 1}, score: 100},
		{account: &Account{ID: 2}, score: 1},
	}}

	enabled := &OpenAIGatewayService{cfg: &config.Config{}, settingService: settings}
	enabled.cfg.Gateway.UnifiedRoutePriority.Enabled = true
	scheduler := &defaultOpenAIAccountScheduler{service: enabled}
	ordered := scheduler.buildOpenAISelectionOrder(context.Background(), req, plan)
	require.Len(t, ordered, 1)
	require.Equal(t, int64(2), ordered[0].account.ID)
	plan.includeOverflowFallback = true
	ordered = scheduler.buildOpenAISelectionOrder(context.Background(), req, plan)
	require.Len(t, ordered, 2)
	require.Equal(t, []int64{2, 1}, []int64{ordered[0].account.ID, ordered[1].account.ID})

	// Isolate the disabled-path comparison from the native overflow fallback
	// that the enabled path above explicitly exercised.
	plan.includeOverflowFallback = false
	disabled := &OpenAIGatewayService{cfg: &config.Config{}, settingService: settings}
	scheduler = &defaultOpenAIAccountScheduler{service: disabled}
	ordered = scheduler.buildOpenAISelectionOrder(context.Background(), req, plan)
	require.Len(t, ordered, 1)
	require.Equal(t, int64(1), ordered[0].account.ID)

	// The captured UTC instant is attached once per selection request.
	ctx := withUnifiedGatewayRoutePriorityRequest(context.Background(), enabled.cfg, settings, &groupID, req.RequestedModel, false)
	priorityRequest := unifiedGatewayRoutePriorityRequestFromContext(ctx)
	require.WithinDuration(t, time.Now().UTC(), priorityRequest.pricingAt, time.Second)
}

func TestOpenAILegacySelectorUsesRoutePriceAfterNativeEligibility(t *testing.T) {
	groupID := int64(31)
	pricing := &unifiedGatewayRoutePricingSnapshot{config: UnifiedGatewayRoutePricingConfig{TargetGroupID: groupID}, entries: make(map[string]UnifiedGatewayRoutePricingEntry)}
	for id, multiplier := range map[int64]float64{1: 1, 2: 0.4} {
		entry := UnifiedGatewayRoutePricingEntry{AccountID: id, Model: "gpt-5.6", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePriorityFloat(multiplier)}
		pricing.entries[unifiedGatewayRoutePricingKey(groupID, id, entry.Model, entry.Kind, "", "", "", "", 0)] = entry
	}
	settings := &SettingService{}
	settings.routePricingSnapshot.Store(pricing)
	account := func(id int64, priority int) Account {
		return Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: "active", Schedulable: true, Priority: priority, Credentials: map[string]any{"base_url": "https://example.invalid", "api_key": "test"}}
	}
	accounts := []Account{account(1, 1), account(2, 50)}

	enabled := &OpenAIGatewayService{cfg: &config.Config{}, settingService: settings}
	enabled.cfg.Gateway.UnifiedRoutePriority.Enabled = true
	selected, _, stats := enabled.selectBestAccount(context.Background(), &groupID, PlatformOpenAI, accounts, "gpt-5.6", nil, false, OpenAIEndpointCapabilityChatCompletions, false)
	require.NotNil(t, selected)
	require.Equal(t, int64(2), selected.ID)
	require.Equal(t, 2, stats.pool)

	disabled := &OpenAIGatewayService{cfg: &config.Config{}, settingService: settings}
	selected, _, _ = disabled.selectBestAccount(context.Background(), &groupID, PlatformOpenAI, accounts, "gpt-5.6", nil, false, OpenAIEndpointCapabilityChatCompletions, false)
	require.NotNil(t, selected)
	require.Equal(t, int64(1), selected.ID)
}
