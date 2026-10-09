package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/routepriority"
	"github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/stretchr/testify/require"
)

func routePriorityFloat(value float64) *float64 { return &value }

func routePriorityTestContext(groupID int64, model string, entries []UnifiedGatewayRoutePricingEntry, at time.Time) context.Context {
	pricing := &unifiedGatewayRoutePricingSnapshot{
		config:  UnifiedGatewayRoutePricingConfig{TargetGroupID: groupID},
		entries: make(map[string]UnifiedGatewayRoutePricingEntry, len(entries)),
	}
	for _, entry := range entries {
		pricing.entries[unifiedGatewayRoutePricingKey(groupID, entry.AccountID, entry.Model, entry.Kind,
			entry.ImagePricingMode, entry.ImageSize, entry.ImageQuality, entry.VideoResolution, entry.VideoDurationSeconds)] = entry
	}
	return context.WithValue(context.Background(), unifiedGatewayRoutePriorityRequestKey{}, &unifiedGatewayRoutePriorityRequest{
		groupID: groupID, model: model, pricing: pricing, pricingAt: at.UTC(),
	})
}

func TestUnifiedGatewayRoutePriorityRanksMultiplierCardsAndMissingCardAtOne(t *testing.T) {
	ctx := routePriorityTestContext(7, "gpt-5.6", []UnifiedGatewayRoutePricingEntry{{
		AccountID: 2, Model: "gpt-5.6", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePriorityFloat(0.4),
	}}, time.Date(2026, 10, 9, 2, 0, 0, 0, time.UTC))
	accounts := []*Account{{ID: 1}, {ID: 2}}
	ranks := unifiedGatewayRoutePriorityRanks(ctx, accounts, func(_ *Account, model string) string { return model }, nil)
	require.Equal(t, 0, ranks[2].PriceLayer)
	require.Equal(t, 1, ranks[1].PriceLayer)
}

func TestUnifiedGatewayRoutePriorityRanksDirectBasePriceVectors(t *testing.T) {
	base := func(input, output, cache float64, cacheWrite *float64) *UnifiedGatewayTokenBasePrice {
		return &UnifiedGatewayTokenBasePrice{
			InputPerMillion: routePriorityFloat(input), OutputPerMillion: routePriorityFloat(output),
			CacheReadPerMillion: routePriorityFloat(cache), CacheWritePerMillion: cacheWrite,
		}
	}
	ctx := routePriorityTestContext(3, "glm-5.2", []UnifiedGatewayRoutePricingEntry{
		{AccountID: 1, Model: "glm-5.2", Kind: UnifiedGatewayRoutePricingToken, TokenBasePrice: base(1, 1, 1, nil)},
		{AccountID: 2, Model: "glm-5.2", Kind: UnifiedGatewayRoutePricingToken, TokenBasePrice: base(2, 2, 2, nil)},
		{AccountID: 3, Model: "glm-5.2", Kind: UnifiedGatewayRoutePricingToken, TokenBasePrice: base(0.5, 4, 1, nil)},
		{AccountID: 4, Model: "glm-5.2", Kind: UnifiedGatewayRoutePricingToken, TokenBasePrice: base(0.5, 0.5, 0.5, routePriorityFloat(0.1))},
	}, time.Now())
	accounts := []*Account{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}}
	ranks := unifiedGatewayRoutePriorityRanks(ctx, accounts, func(_ *Account, model string) string { return model }, nil)
	require.Equal(t, 0, ranks[1].PriceLayer)
	require.Equal(t, 0, ranks[3].PriceLayer) // crosses account 1 on input/output
	require.Equal(t, 0, ranks[4].PriceLayer) // incompatible optional-meter coverage
	require.Equal(t, 1, ranks[2].PriceLayer)
}

func TestUnifiedGatewayRoutePriorityBaseCardAndMultiplierCardAreIncomparable(t *testing.T) {
	ctx := routePriorityTestContext(3, "m", []UnifiedGatewayRoutePricingEntry{{
		AccountID: 2, Model: "m", Kind: UnifiedGatewayRoutePricingToken,
		TokenBasePrice: &UnifiedGatewayTokenBasePrice{InputPerMillion: routePriorityFloat(0.01), OutputPerMillion: routePriorityFloat(0.02), CacheReadPerMillion: routePriorityFloat(0.001)},
	}}, time.Now())
	ranks := unifiedGatewayRoutePriorityRanks(ctx, []*Account{{ID: 1}, {ID: 2}}, func(_ *Account, model string) string { return model }, nil)
	require.Equal(t, 0, ranks[1].PriceLayer)
	require.Equal(t, 0, ranks[2].PriceLayer)
}

func TestUnifiedGatewayRoutePriorityUsesSingleCapturedUTCTier(t *testing.T) {
	tier := func(input float64) UnifiedGatewayWokeyTimeOfDayTier {
		return UnifiedGatewayWokeyTimeOfDayTier{TokenBasePrice: &UnifiedGatewayTokenBasePrice{
			InputPerMillion: routePriorityFloat(input), OutputPerMillion: routePriorityFloat(input), CacheReadPerMillion: routePriorityFloat(input),
		}}
	}
	entry := UnifiedGatewayRoutePricingEntry{AccountID: 1, Model: "m", Kind: UnifiedGatewayRoutePricingToken,
		TimeOfDayTokenPrice: &UnifiedGatewayWokeyTimeOfDayTokenPrice{
			PeakWindowsUTC: []UnifiedGatewayWokeyTimeOfDayWindow{{StartHour: 1, EndHour: 4}}, Peak: tier(0.5), OffPeak: tier(2),
		}}
	ctx := routePriorityTestContext(5, "m", []UnifiedGatewayRoutePricingEntry{entry}, time.Date(2026, 10, 9, 2, 0, 0, 0, time.UTC))
	key, prices := unifiedGatewayRoutePriorityPrice(unifiedGatewayRoutePriorityRequestFromContext(ctx), 1, "m")
	require.NotEmpty(t, key)
	require.Equal(t, []float64{0.5, 0.5, 0.5}, prices)
	request := unifiedGatewayRoutePriorityRequestFromContext(ctx)
	request.pricingAt = time.Date(2026, 10, 9, 5, 0, 0, 0, time.UTC)
	_, prices = unifiedGatewayRoutePriorityPrice(request, 1, "m")
	require.Equal(t, []float64{2, 2, 2}, prices)
}

func TestUnifiedGatewayRoutePriorityMappedModelsDoNotCompare(t *testing.T) {
	ctx := routePriorityTestContext(9, "public-model", []UnifiedGatewayRoutePricingEntry{
		{AccountID: 1, Model: "provider-a-model", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePriorityFloat(0.1)},
		{AccountID: 2, Model: "provider-b-model", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePriorityFloat(0.9)},
	}, time.Now())
	ranks := unifiedGatewayRoutePriorityRanks(ctx, []*Account{{ID: 1}, {ID: 2}}, func(account *Account, _ string) string {
		if account.ID == 1 {
			return "provider-a-model"
		}
		return "provider-b-model"
	}, nil)
	require.Equal(t, ranks[1].PriceLayer, ranks[2].PriceLayer)
}

func TestUnifiedGatewayRoutePriorityIgnoresAccountCostMultiplier(t *testing.T) {
	ctx := routePriorityTestContext(12, "m", nil, time.Now())
	accounts := []*Account{{ID: 1, RateMultiplier: routePriorityFloat(0.1)}, {ID: 2, RateMultiplier: routePriorityFloat(9)}}
	ranks := unifiedGatewayRoutePriorityRanks(ctx, accounts, func(_ *Account, model string) string { return model }, nil)
	require.Equal(t, ranks[1].PriceLayer, ranks[2].PriceLayer)
}

func TestUnifiedGatewayRoutePriorityRequestRequiresEnabledTargetAndTextModel(t *testing.T) {
	groupID := int64(7)
	cfg := &config.Config{}
	ctx := withUnifiedGatewayRoutePriorityRequest(context.Background(), cfg, nil, &groupID, "gpt-5.6", false)
	require.Nil(t, unifiedGatewayRoutePriorityRequestFromContext(ctx))
	cfg.Gateway.UnifiedRoutePriority.Enabled = true
	ctx = withUnifiedGatewayRoutePriorityRequest(context.Background(), cfg, nil, &groupID, "gpt-image-2", false)
	require.Nil(t, unifiedGatewayRoutePriorityRequestFromContext(ctx))
	ctx = withUnifiedGatewayRoutePriorityRequest(context.Background(), cfg, nil, &groupID, "gpt-5.6", true)
	require.Nil(t, unifiedGatewayRoutePriorityRequestFromContext(ctx))

	settings := &SettingService{}
	settings.routePricingSnapshot.Store(&unifiedGatewayRoutePricingSnapshot{config: UnifiedGatewayRoutePricingConfig{TargetGroupID: groupID}})
	ctx = withUnifiedGatewayRoutePriorityRequest(WithOpenAIImageGenerationIntent(context.Background()), cfg, settings, &groupID, "gpt-5.6", false)
	require.Nil(t, unifiedGatewayRoutePriorityRequestFromContext(ctx))
	ctx = withUnifiedGatewayRoutePriorityRequest(context.Background(), cfg, settings, &groupID, "gpt-5.6", false)
	require.NotNil(t, unifiedGatewayRoutePriorityRequestFromContext(ctx))
	otherGroup := groupID + 1
	ctx = withUnifiedGatewayRoutePriorityRequest(context.Background(), cfg, settings, &otherGroup, "gpt-5.6", false)
	require.Nil(t, unifiedGatewayRoutePriorityRequestFromContext(ctx))
}

func TestUnifiedGatewayRoutePriorityRankLessUsesHealthThenPrice(t *testing.T) {
	ranks := map[int64]routepriority.Rank{
		1: {HealthLayer: 1, PriceLayer: 0},
		2: {HealthLayer: 0, PriceLayer: 3},
		3: {HealthLayer: 0, PriceLayer: 1},
	}
	require.True(t, unifiedGatewayRoutePriorityRankLess(3, 2, ranks))
	require.True(t, unifiedGatewayRoutePriorityRankLess(2, 1, ranks))
	require.False(t, unifiedGatewayRoutePriorityRankLess(1, 2, ranks))
}

func TestOpenAIRoutePriorityExcludesStickyAndImageRequests(t *testing.T) {
	groupID := int64(18)
	cfg := &config.Config{}
	cfg.Gateway.UnifiedRoutePriority.Enabled = true
	settings := &SettingService{}
	settings.routePricingSnapshot.Store(&unifiedGatewayRoutePricingSnapshot{config: UnifiedGatewayRoutePricingConfig{TargetGroupID: groupID}})
	service := &OpenAIGatewayService{cfg: cfg, settingService: settings}
	accounts := []*Account{{ID: 1}, {ID: 2}}
	require.Nil(t, service.openAIRoutePriorityRanksForRequest(context.Background(), OpenAIAccountScheduleRequest{
		GroupID: &groupID, RequestedModel: "gpt-5.6", RequiredCapability: OpenAIEndpointCapabilityChatCompletions, StickyAccountID: 1,
	}, accounts))
	require.Nil(t, service.openAIRoutePriorityRanksForRequest(context.Background(), OpenAIAccountScheduleRequest{
		GroupID: &groupID, RequestedModel: "gpt-5.6", RequiredCapability: OpenAIEndpointCapabilityChatCompletions, PreviousResponseID: "resp_123",
	}, accounts))
	require.Nil(t, service.openAIRoutePriorityRanksForRequest(context.Background(), OpenAIAccountScheduleRequest{
		GroupID: &groupID, RequestedModel: "gpt-5.6", RequiredCapability: OpenAIEndpointCapabilityResponses, RequireCompact: true,
	}, accounts))
	require.Nil(t, service.openAIRoutePriorityRanksForRequest(context.Background(), OpenAIAccountScheduleRequest{
		GroupID: &groupID, RequestedModel: "gpt-image-2", RequiredImageCapability: OpenAIImagesCapabilityBasic,
	}, accounts))
	for _, capability := range []OpenAIEndpointCapability{
		OpenAIEndpointCapabilityLive,
		OpenAIEndpointCapabilitySeedance,
		OpenAIEndpointCapabilityGrokMediaGeneration,
		OpenAIEndpointCapabilityEmbeddings,
		OpenAIEndpointCapabilityAlphaSearch,
		OpenAIEndpointCapability("future_endpoint"),
	} {
		require.Nil(t, service.openAIRoutePriorityRanksForRequest(context.Background(), OpenAIAccountScheduleRequest{
			GroupID: &groupID, RequestedModel: "gpt-5.6", RequiredCapability: capability,
		}, accounts), "capability %q must fail open", capability)
	}
}

func TestOpenAIRoutePriorityHealthLayerMovesDegradedLineBehindHealthyAndRecovers(t *testing.T) {
	previous, hadPrevious := openAIAdvancedSchedulerSettingCache.Load().(*cachedOpenAIAdvancedSchedulerSetting)
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{enabled: true, expiresAt: time.Now().Add(time.Hour).UnixNano()})
	t.Cleanup(func() {
		if hadPrevious && previous != nil {
			openAIAdvancedSchedulerSettingCache.Store(previous)
			return
		}
		openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{expiresAt: time.Now().Add(-time.Hour).UnixNano()})
	})

	cfg := &config.Config{}
	cfg.Gateway.SmartRouter.Enabled = true
	tracker := core.NewHealthTracker(core.DefaultHealthPolicy(), time.Now, nil)
	tracker.Observe(core.RouteResult{
		LaneID: "account:1", AccountID: 1, Capability: core.CapabilityChat, Model: "gpt-5.6",
		Success: false, StatusCode: 503, ErrorClass: core.FailureUpstream5xx, BasePriority: 1,
	})
	service := &OpenAIGatewayService{cfg: cfg, smartRouterHealthTracker: tracker}
	service.smartRouterHealthOnce.Do(func() {})
	req := OpenAIAccountScheduleRequest{
		RequestedModel: "gpt-5.6", RequiredCapability: OpenAIEndpointCapabilityChatCompletions, SmartRouterCapability: core.CapabilityChat,
	}
	accounts := []*Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Priority: 1},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Priority: 2},
	}
	health := openAIRoutePriorityHealthLayers(service, context.Background(), req, accounts)
	require.Equal(t, 1, health[1])
	require.Zero(t, health[2])

	tracker.Observe(core.RouteResult{
		Source: "calibration", LaneID: "account:1", AccountID: 1, Capability: core.CapabilityChat, Model: "gpt-5.6",
		Success: true, BasePriority: 1,
	})
	health = openAIRoutePriorityHealthLayers(service, context.Background(), req, accounts)
	require.Zero(t, health[1])
	require.Zero(t, health[2])
}
