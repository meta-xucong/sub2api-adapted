package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

func routePricingFloat(value float64) *float64 { return &value }

func routePricingTokenBase(input, output, cacheRead, cacheWrite, cache5m, cache1h float64) *UnifiedGatewayTokenBasePrice {
	return &UnifiedGatewayTokenBasePrice{
		InputPerMillion: routePricingFloat(input), OutputPerMillion: routePricingFloat(output),
		CacheReadPerMillion: routePricingFloat(cacheRead), CacheWritePerMillion: routePricingFloat(cacheWrite),
		CacheWrite5mPerMillion: routePricingFloat(cache5m), CacheWrite1hPerMillion: routePricingFloat(cache1h),
	}
}

func TestUnifiedGatewayTokenBasePriceRejectsDynamicGroupPeak(t *testing.T) {
	group := &Group{
		SubscriptionType: SubscriptionTypeSubscription,
		PeakRateEnabled:  true,
		PeakStart:        "10:00", PeakEnd: "11:00", PeakRateMultiplier: 1.5,
	}
	apiKey := &APIKey{Group: group}
	peakTime := time.Date(2026, time.October, 8, 10, 30, 0, 0, timezone.Location())
	offPeakTime := time.Date(2026, time.October, 8, 11, 30, 0, 0, timezone.Location())

	require.True(t, unifiedGatewayTokenBasePriceHasDynamicGroupPeak(apiKey, peakTime))
	require.False(t, unifiedGatewayTokenBasePriceHasDynamicGroupPeak(apiKey, offPeakTime))
	require.False(t, unifiedGatewayTokenBasePriceHasDynamicGroupPeak(&APIKey{Group: &Group{Platform: PlatformComposite}}, peakTime),
		"the unified Composite group has no native peak rate")
}

func TestFlatUnifiedGatewayServiceTierAcceptsOnlyBasePriceLabels(t *testing.T) {
	for _, tier := range []*string{nil, routePricingStringPtr(""), routePricingStringPtr("default"), routePricingStringPtr("standard")} {
		require.True(t, flatUnifiedGatewayServiceTier(tier))
	}
	for _, tier := range []string{"priority", "fast", "flex", "ultrafast"} {
		require.False(t, flatUnifiedGatewayServiceTier(routePricingStringPtr(tier)))
	}
}

func routePricingStringPtr(value string) *string { return &value }

func TestBuildUnifiedGatewayRoutePricingSnapshotValidatesEntries(t *testing.T) {
	valid := UnifiedGatewayRoutePricingConfig{
		TargetGroupID: 7,
		Revision:      1,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID:  42,
			Model:      "glm-5.2",
			Kind:       UnifiedGatewayRoutePricingToken,
			Multiplier: routePricingFloat(0.4),
		}},
	}
	_, err := buildUnifiedGatewayRoutePricingSnapshot(valid)
	require.NoError(t, err)

	duplicate := valid
	duplicate.Entries = append(append([]UnifiedGatewayRoutePricingEntry(nil), valid.Entries...), valid.Entries[0])
	_, err = buildUnifiedGatewayRoutePricingSnapshot(duplicate)
	require.ErrorContains(t, err, "duplicates")

	invalid := valid
	invalid.Entries = []UnifiedGatewayRoutePricingEntry{{AccountID: 42, Model: "glm-5.2", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePricingFloat(math.NaN())}}
	_, err = buildUnifiedGatewayRoutePricingSnapshot(invalid)
	require.ErrorContains(t, err, "invalid token")

	invalid.Entries = []UnifiedGatewayRoutePricingEntry{{AccountID: 42, Model: "gpt-image-2", Kind: UnifiedGatewayRoutePricingImage, UnitPrice: routePricingFloat(0.02), ImageSize: "1024x1024"}}
	_, err = buildUnifiedGatewayRoutePricingSnapshot(invalid)
	require.ErrorContains(t, err, "image size, quality")

	normalized, err := buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{
		TargetGroupID: 7,
		Revision:      2,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID: 42, Model: " glm-5.2 ", Kind: UnifiedGatewayRoutePricingImage,
			UnitPrice: routePricingFloat(0.02), ImageSize: " 1024x1024 ", ImageQuality: " HIGH ",
		}},
	})
	require.NoError(t, err)
	require.Equal(t, "glm-5.2", normalized.config.Entries[0].Model)
	require.Equal(t, "1K", normalized.config.Entries[0].ImageSize)
	require.Equal(t, "high", normalized.config.Entries[0].ImageQuality)

	video, err := buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{
		TargetGroupID: 7,
		Revision:      3,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID: 42, Model: "grok-imagine-video-1.5", Kind: UnifiedGatewayRoutePricingVideo,
			UnitPrice: routePricingFloat(0.049), VideoResolution: " HD ", VideoDurationSeconds: 5,
		}},
	})
	require.NoError(t, err)
	require.Equal(t, VideoBillingResolution720P, video.config.Entries[0].VideoResolution)

	invalidVideo := UnifiedGatewayRoutePricingConfig{
		TargetGroupID: 7,
		Revision:      4,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID: 42, Model: "grok-imagine-video-1.5", Kind: UnifiedGatewayRoutePricingVideo,
			UnitPrice: routePricingFloat(0.049), VideoResolution: "4k", VideoDurationSeconds: 5,
		}},
	}
	_, err = buildUnifiedGatewayRoutePricingSnapshot(invalidVideo)
	require.Error(t, err, "unsupported resolution must not silently bind to native 480p")
	invalidVideo.Entries[0].VideoResolution = "720p"
	invalidVideo.Entries[0].VideoDurationSeconds = VideoBillingMaxDurationSeconds + 1
	_, err = buildUnifiedGatewayRoutePricingSnapshot(invalidVideo)
	require.Error(t, err, "route duration must stay within native billing range")
}

func TestBuildUnifiedGatewayRoutePricingSnapshotValidatesCompleteTokenBasePrice(t *testing.T) {
	valid := UnifiedGatewayRoutePricingConfig{
		TargetGroupID: 7,
		Revision:      1,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID: 42, Model: "glm-5.2", Kind: UnifiedGatewayRoutePricingToken,
			TokenBasePrice: routePricingTokenBase(3.2, 11.2, 0.8, 3.2, 3.2, 3.2),
		}},
	}
	_, err := buildUnifiedGatewayRoutePricingSnapshot(valid)
	require.NoError(t, err, "a base-price-only token entry is valid")

	withFallback := valid
	withFallback.Entries = append([]UnifiedGatewayRoutePricingEntry(nil), valid.Entries...)
	withFallback.Entries[0].Multiplier = routePricingFloat(0.4)
	_, err = buildUnifiedGatewayRoutePricingSnapshot(withFallback)
	require.NoError(t, err, "a legacy multiplier may coexist as fallback")

	missing := valid
	missing.Entries = append([]UnifiedGatewayRoutePricingEntry(nil), valid.Entries...)
	missing.Entries[0].TokenBasePrice.CacheReadPerMillion = nil
	_, err = buildUnifiedGatewayRoutePricingSnapshot(missing)
	require.ErrorContains(t, err, "invalid token")

	invalid := valid
	invalid.Entries = append([]UnifiedGatewayRoutePricingEntry(nil), valid.Entries...)
	invalid.Entries[0].TokenBasePrice.OutputPerMillion = routePricingFloat(-1)
	_, err = buildUnifiedGatewayRoutePricingSnapshot(invalid)
	require.ErrorContains(t, err, "invalid token")

	zero := valid
	zero.Entries = append([]UnifiedGatewayRoutePricingEntry(nil), valid.Entries...)
	zero.Entries[0].TokenBasePrice = routePricingTokenBase(0, 0, 0, 0, 0, 0)
	_, err = buildUnifiedGatewayRoutePricingSnapshot(zero)
	require.NoError(t, err, "explicit zero is a valid rate")
}

func TestPrepareUnifiedGatewayRoutePricingReservationUsesHighestTokenBasePrice(t *testing.T) {
	snapshot, err := buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{
		TargetGroupID: 7,
		Revision:      1,
		Entries: []UnifiedGatewayRoutePricingEntry{
			{AccountID: 42, Model: "glm-5.2", Kind: UnifiedGatewayRoutePricingToken, TokenBasePrice: routePricingTokenBase(10, 50, 20, 30, 100, 40)},
			{AccountID: 43, Model: "glm-5.2", Kind: UnifiedGatewayRoutePricingToken, TokenBasePrice: routePricingTokenBase(8, 60, 15, 20, 30, 30)},
		},
	})
	require.NoError(t, err)
	settings := &SettingService{}
	settings.routePricingSnapshot.Store(snapshot)
	groupID := int64(7)
	apiKey := &APIKey{GroupID: &groupID, Group: &Group{ID: 7, Platform: PlatformComposite}}

	_, estimate, planned := settings.prepareUnifiedGatewayRoutePricingDecision(
		context.Background(), apiKey, InflightEstimateRequest{Model: "glm-5.2", BodyBytes: 4000, MaxTokens: 100, Kind: InflightEstimateToken}, 0.05, true, 1,
	)
	require.True(t, planned)
	require.InDelta(t, 0.105, estimate, 1e-12, "input uses max possible input-side rate plus output upper bound")
}

func TestPrepareUnifiedGatewayRoutePricingReservationMultipliesBaseCardByLineAndGroupRates(t *testing.T) {
	snapshot, err := buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{
		TargetGroupID: 7,
		Revision:      1,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID: 42, Model: "glm-5.2", Kind: UnifiedGatewayRoutePricingToken,
			Multiplier: routePricingFloat(0.4), TokenBasePrice: routePricingTokenBase(10, 50, 20, 30, 100, 40),
		}},
	})
	require.NoError(t, err)
	settings := &SettingService{}
	settings.routePricingSnapshot.Store(snapshot)
	groupID := int64(7)
	apiKey := &APIKey{GroupID: &groupID, Group: &Group{ID: 7, Platform: PlatformComposite}}

	_, estimate, planned := settings.prepareUnifiedGatewayRoutePricingDecision(
		context.Background(), apiKey, InflightEstimateRequest{Model: "glm-5.2", BodyBytes: 4000, MaxTokens: 100, Kind: InflightEstimateToken}, 0, false, 1.5,
	)
	require.True(t, planned)
	require.InDelta(t, 0.063, estimate, 1e-12, "reserve must apply the line multiplier and then the effective group/user multiplier to the base card")
}

func TestPrepareUnifiedGatewayRoutePricingReservationAllowsZeroEffectiveRateForPositiveCard(t *testing.T) {
	snapshot, err := buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{
		TargetGroupID: 7,
		Revision:      1,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID: 42, Model: "glm-5.2", Kind: UnifiedGatewayRoutePricingToken,
			TokenBasePrice: routePricingTokenBase(10, 50, 20, 30, 100, 40),
		}},
	})
	require.NoError(t, err)
	settings := &SettingService{}
	settings.routePricingSnapshot.Store(snapshot)
	groupID := int64(7)
	apiKey := &APIKey{GroupID: &groupID, Group: &Group{ID: 7, Platform: PlatformComposite}}

	ctx, estimate, planned := settings.prepareUnifiedGatewayRoutePricingDecision(
		context.Background(), apiKey,
		InflightEstimateRequest{Model: "glm-5.2", BodyBytes: 4000, MaxTokens: 100, Kind: InflightEstimateToken}, 0, false, 0,
	)
	require.True(t, planned, "the configured route remains an exact matched price card")
	require.Zero(t, estimate, "a valid effective group/user multiplier of zero produces no reservation amount")
	decision, ok := UnifiedGatewayRoutePricingDecisionFromContext(ctx)
	require.True(t, ok)
	require.True(t, decision.HasPositiveTokenBasePrice(), "the zero estimate is caused by the effective multiplier, not zero card rates")
}

func TestPrepareUnifiedGatewayRoutePricingReservationUsesMaximumRouteEstimate(t *testing.T) {
	snapshot, err := buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{
		TargetGroupID: 7,
		Revision:      8,
		Entries: []UnifiedGatewayRoutePricingEntry{
			{AccountID: 42, Model: "glm-5.2", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePricingFloat(0.4)},
			{AccountID: 43, Model: "glm-5.2", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePricingFloat(2)},
		},
	})
	require.NoError(t, err)
	settings := &SettingService{}
	settings.routePricingSnapshot.Store(snapshot)
	groupID := int64(7)
	apiKey := &APIKey{GroupID: &groupID, Group: &Group{ID: 7, Platform: PlatformComposite}}

	ctx, estimate, planned := settings.prepareUnifiedGatewayRoutePricingDecision(
		context.Background(), apiKey,
		InflightEstimateRequest{Model: "glm-5.2", Kind: InflightEstimateToken},
		0.1, true, 1,
	)
	require.True(t, planned)
	require.InDelta(t, 0.2, estimate, 1e-12, "reserve must cover the highest matching line multiplier")
	decision, ok := UnifiedGatewayRoutePricingDecisionFromContext(ctx)
	require.True(t, ok)
	require.False(t, decision.Allowed, "the handler must enable pricing only after reservation succeeds")
	require.Len(t, decision.Entries, 2)

	wrongGroupID := int64(8)
	_, _, planned = settings.prepareUnifiedGatewayRoutePricingDecision(
		context.Background(), &APIKey{GroupID: &wrongGroupID, Group: &Group{ID: 8, Platform: PlatformComposite}},
		InflightEstimateRequest{Model: "glm-5.2", Kind: InflightEstimateToken}, 0.1, true, 1,
	)
	require.False(t, planned, "route tariffs must not cross the configured Composite group")
}

func TestPrepareUnifiedGatewayRoutePricingImageEstimateUsesHighestFixedPrice(t *testing.T) {
	snapshot, err := buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{
		TargetGroupID: 7,
		Revision:      1,
		Entries: []UnifiedGatewayRoutePricingEntry{
			{AccountID: 42, Model: "gpt-image-2", Kind: UnifiedGatewayRoutePricingImage, ImageSize: "1K", ImageQuality: "high", UnitPrice: routePricingFloat(0.02)},
			{AccountID: 43, Model: "gpt-image-2", Kind: UnifiedGatewayRoutePricingImage, ImageSize: "1K", ImageQuality: "high", UnitPrice: routePricingFloat(0.07)},
		},
	})
	require.NoError(t, err)
	settings := &SettingService{}
	settings.routePricingSnapshot.Store(snapshot)
	groupID := int64(7)
	apiKey := &APIKey{GroupID: &groupID, Group: &Group{ID: 7, Platform: PlatformComposite, ImageRateIndependent: true, ImageRateMultiplier: 1.5}}

	_, estimate, planned := settings.prepareUnifiedGatewayRoutePricingDecision(
		context.Background(), apiKey,
		InflightEstimateRequest{Model: "gpt-image-2", Kind: InflightEstimateImage, Units: 2, ImageSize: "1024x1024", ImageQuality: "HIGH"},
		0.1, true, 1,
	)
	require.True(t, planned)
	require.InDelta(t, 0.21, estimate, 1e-12)

	_, _, planned = settings.prepareUnifiedGatewayRoutePricingDecision(
		context.Background(), apiKey,
		InflightEstimateRequest{Model: "gpt-image-2", Kind: InflightEstimateImage, Units: 2, ImageSize: "auto", ImageQuality: "high"},
		0.1, true, 1,
	)
	require.False(t, planned, "an unknown request size cannot reserve a route-specific price")
}

func TestOpenAINativeCostReportsTheModelThatActuallyPricedTheRequest(t *testing.T) {
	svc := newOpenAIRecordUsageServiceForTest(nil, nil, nil, nil)
	result := &OpenAIForwardResult{Model: "unlisted-request-model"}
	apiKey := &APIKey{}
	tokens := UsageTokens{InputTokens: 100, OutputTokens: 10}

	cost, selectedModel, err := svc.calculateOpenAIRecordUsageCostWithModel(
		context.Background(), result, apiKey,
		[]string{"unlisted-request-model", "gpt-5.4-nano"},
		1, 1, 1, 1, tokens, "", nil, time.Time{},
	)
	require.NoError(t, err)
	require.NotNil(t, cost)
	require.Equal(t, "gpt-5.4-nano", selectedModel)

	groupID := int64(7)
	decision := UnifiedGatewayRoutePricingDecision{
		Allowed: true, GroupID: groupID, Model: "unlisted-request-model", Kind: UnifiedGatewayRoutePricingToken,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID: 42, Model: "unlisted-request-model", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePricingFloat(0.4),
			TokenBasePrice: routePricingTokenBase(10, 20, 5, 10, 10, 10),
		}},
	}
	unchanged := ApplyUnifiedGatewayRoutePricingWithTokenUsage(
		WithUnifiedGatewayRoutePricingDecision(context.Background(), decision),
		&APIKey{GroupID: &groupID}, 42, selectedModel, "", "", nil, 0, 0, "", 0, cost, 1, 1,
		UnifiedGatewayRouteTokenUsage{Tokens: tokens, RateMultiplier: 1, Eligible: true},
	)
	require.InDelta(t, cost.ActualCost, unchanged.ActualCost, 1e-12, "a tariff must not be applied to a different native fallback model")
}

func TestOpenAIRecordUsageAppliesRouteMultiplierOnlyToUserCharge(t *testing.T) {
	const model = "gpt-5.4-nano"
	const accountID int64 = 42
	const groupID int64 = 7
	usage := OpenAIUsage{InputTokens: 20, OutputTokens: 10}
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
	nativeCost := expectedOpenAICost(t, svc, model, usage, 1)

	decision := UnifiedGatewayRoutePricingDecision{
		Allowed: true, GroupID: groupID, Model: model, Kind: UnifiedGatewayRoutePricingToken,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID: accountID, Model: model, Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePricingFloat(0.4),
		}},
	}
	ctx := WithUnifiedGatewayRoutePricingDecision(context.Background(), decision)
	apiKey := &APIKey{
		ID: 10, GroupID: routePricingInt64Ptr(groupID),
		Group: &Group{ID: groupID, Platform: PlatformComposite, RateMultiplier: 1},
	}
	err := svc.RecordUsage(ctx, &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID:    "route_pricing_record_usage",
			Model:        model,
			BillingModel: model,
			Usage:        usage,
			Duration:     time.Second,
		},
		APIKey:  apiKey,
		User:    &User{ID: 20},
		Account: &Account{ID: accountID, Type: AccountTypeAPIKey},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, nativeCost.ActualCost*0.4, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, nativeCost.TotalCost, usageRepo.lastLog.TotalCost, 1e-12, "route tariff must not replace native cost-side total")
	require.InDelta(t, usageRepo.lastLog.ActualCost, userRepo.lastAmount, 1e-12, "the amount actually deducted from the user must match the usage row")
}

func TestOpenAIRecordUsageAppliesRouteBasePriceToUserChargeOnly(t *testing.T) {
	const model = "gpt-5.4-nano"
	const accountID int64 = 42
	const groupID int64 = 7
	usage := OpenAIUsage{InputTokens: 20, OutputTokens: 10}
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
	groupMultiplier := 1.5
	input, output, cacheRead := 10.0, 20.0, 5.0
	cacheWrite := 10.0
	decision := UnifiedGatewayRoutePricingDecision{
		Allowed: true, GroupID: groupID, Model: model, Kind: UnifiedGatewayRoutePricingToken,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID: accountID, Model: model, Kind: UnifiedGatewayRoutePricingToken,
			Multiplier: routePricingFloat(0.4),
			TokenBasePrice: &UnifiedGatewayTokenBasePrice{
				InputPerMillion: &input, OutputPerMillion: &output, CacheReadPerMillion: &cacheRead,
				CacheWritePerMillion: &cacheWrite, CacheWrite5mPerMillion: &cacheWrite, CacheWrite1hPerMillion: &cacheWrite,
			},
		}},
	}
	ctx := WithUnifiedGatewayRoutePricingDecision(context.Background(), decision)
	apiKey := &APIKey{
		ID: 10, GroupID: routePricingInt64Ptr(groupID),
		Group: &Group{ID: groupID, Platform: PlatformComposite, RateMultiplier: groupMultiplier},
	}
	nativeCost := expectedOpenAICost(t, svc, model, usage, groupMultiplier)

	err := svc.RecordUsage(ctx, &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID: "route_pricing_base_card_record_usage",
			Model:     model, BillingModel: model, Usage: usage, Duration: time.Second,
		},
		APIKey: apiKey, User: &User{ID: 20}, Account: &Account{ID: accountID, Type: AccountTypeAPIKey},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, 0.00024, usageRepo.lastLog.ActualCost, 1e-12, "base card usage cost is multiplied by the line rate and group rate")
	require.InDelta(t, nativeCost.TotalCost, usageRepo.lastLog.TotalCost, 1e-12, "native upstream-cost-side total remains unchanged")
	require.InDelta(t, usageRepo.lastLog.ActualCost, userRepo.lastAmount, 1e-12, "the user deduction matches the route-card usage row")
}

func TestOpenAIRecordUsageAppliesRouteBasePriceWhenNativeModelPriceIsMissing(t *testing.T) {
	const model = "unlisted-route-priced-model"
	const accountID int64 = 42
	const groupID int64 = 7
	usage := OpenAIUsage{InputTokens: 20, OutputTokens: 10}
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
	decision := UnifiedGatewayRoutePricingDecision{
		Allowed: true, GroupID: groupID, Model: model, Kind: UnifiedGatewayRoutePricingToken,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID: accountID, Model: model, Kind: UnifiedGatewayRoutePricingToken,
			TokenBasePrice: routePricingTokenBase(10, 20, 5, 10, 10, 10),
		}},
	}
	apiKey := &APIKey{
		ID: 10, GroupID: routePricingInt64Ptr(groupID),
		Group: &Group{ID: groupID, Platform: PlatformComposite, RateMultiplier: 1.5},
	}

	err := svc.RecordUsage(WithUnifiedGatewayRoutePricingDecision(context.Background(), decision), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID: "route_pricing_unlisted_model",
			Model:     model, BillingModel: model, Usage: usage, Duration: time.Second,
		},
		APIKey: apiKey, User: &User{ID: 20}, Account: &Account{ID: accountID, Type: AccountTypeAPIKey},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, 0.0006, usageRepo.lastLog.ActualCost, 1e-12,
		"a complete exact route card must be able to price a model missing from the shared catalog")
	require.Zero(t, usageRepo.lastLog.TotalCost, "the route card must not fabricate the native cost-side amount")
	require.InDelta(t, usageRepo.lastLog.ActualCost, userRepo.lastAmount, 1e-12,
		"the charged user amount must match the base card when native pricing is unavailable")
}

func TestOpenAIRecordUsageKeepsUnpricedModelAtZeroWithoutMatchingBaseCard(t *testing.T) {
	const model = "unlisted-without-route-card"
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID: "route_pricing_unlisted_without_card",
			Model:     model, BillingModel: model, Usage: OpenAIUsage{InputTokens: 20, OutputTokens: 10}, Duration: time.Second,
		},
		APIKey: &APIKey{ID: 10, GroupID: routePricingInt64Ptr(7), Group: &Group{ID: 7, Platform: PlatformComposite}},
		User:   &User{ID: 20}, Account: &Account{ID: 42, Type: AccountTypeAPIKey},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Zero(t, usageRepo.lastLog.ActualCost)
	require.Zero(t, usageRepo.lastLog.TotalCost)
	require.Zero(t, userRepo.lastAmount)
}

func TestApplyUnifiedGatewayRoutePricingChangesOnlyUserActualCost(t *testing.T) {
	apiKey := &APIKey{GroupID: routePricingInt64Ptr(7)}
	decision := UnifiedGatewayRoutePricingDecision{
		Allowed:  true,
		Revision: 3,
		GroupID:  7,
		Model:    "glm-5.2",
		Kind:     UnifiedGatewayRoutePricingToken,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID:  42,
			Model:      "glm-5.2",
			Kind:       UnifiedGatewayRoutePricingToken,
			Multiplier: routePricingFloat(0.4),
		}},
	}
	ctx := WithUnifiedGatewayRoutePricingDecision(context.Background(), decision)
	cost := &CostBreakdown{TotalCost: 0.08, ActualCost: 0.12}
	updated := ApplyUnifiedGatewayRoutePricing(ctx, apiKey, 42, "glm-5.2", "", "", nil, 0, 0, "", 0, cost, 1, 1)
	require.Same(t, cost, updated)
	require.InDelta(t, 0.048, updated.ActualCost, 1e-12)
	require.Equal(t, 0.08, updated.TotalCost)
	require.True(t, updated.routePricingApplied, "a matched tariff marks the user-cost field for subscription billing")

	wrongAccount := ApplyUnifiedGatewayRoutePricing(ctx, apiKey, 99, "glm-5.2", "", "", nil, 0, 0, "", 0, &CostBreakdown{TotalCost: 0.08, ActualCost: 0.12}, 1, 1)
	require.InDelta(t, 0.12, wrongAccount.ActualCost, 1e-12)
	require.False(t, wrongAccount.routePricingApplied)

	withoutDecision := ApplyUnifiedGatewayRoutePricing(context.Background(), apiKey, 42, "glm-5.2", "", "", nil, 0, 0, "", 0, &CostBreakdown{ActualCost: 0.12}, 1, 1)
	require.InDelta(t, 0.12, withoutDecision.ActualCost, 1e-12)
	require.False(t, withoutDecision.routePricingApplied)
}

func TestApplyUnifiedGatewayTokenBasePriceMultipliesLineAndGroupRatesOnlyForSupportedUsage(t *testing.T) {
	apiKey := &APIKey{GroupID: routePricingInt64Ptr(7)}
	decision := UnifiedGatewayRoutePricingDecision{
		Allowed: true, GroupID: 7, Model: "glm-5.2", Kind: UnifiedGatewayRoutePricingToken,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID: 42, Model: "glm-5.2", Kind: UnifiedGatewayRoutePricingToken,
			Multiplier: routePricingFloat(0.4), TokenBasePrice: routePricingTokenBase(10, 20, 5, 7, 15, 30),
		}},
	}
	ctx := WithUnifiedGatewayRoutePricingDecision(context.Background(), decision)
	cost := &CostBreakdown{TotalCost: 0.000123, ActualCost: 0.0002, BillingMode: string(BillingModeToken)}
	updated := ApplyUnifiedGatewayRoutePricingWithTokenUsage(ctx, apiKey, 42, "glm-5.2", "", "", nil, 0, 0, "", 0, cost, 1, 1,
		UnifiedGatewayRouteTokenUsage{
			Tokens:         UsageTokens{InputTokens: 10, OutputTokens: 2, CacheReadTokens: 3, CacheCreationTokens: 4, CacheCreation5mTokens: 2, CacheCreation1hTokens: 2},
			RateMultiplier: 1.5, Eligible: true,
		})
	require.InDelta(t, 0.000147, updated.ActualCost, 1e-12, "the card prices actual usage, then applies the line and user/group multipliers")
	require.Equal(t, 0.000123, updated.TotalCost, "the native cost-side amount must remain unchanged")
	require.True(t, updated.routePricingApplied)

	unsupported := &CostBreakdown{TotalCost: 0.000123, ActualCost: 0.0002, BillingMode: string(BillingModeToken)}
	ApplyUnifiedGatewayRoutePricingWithTokenUsage(ctx, apiKey, 42, "glm-5.2", "", "", nil, 0, 0, "", 0, unsupported, 1, 1,
		UnifiedGatewayRouteTokenUsage{Tokens: UsageTokens{InputTokens: 10, ImageInputTokens: 2}, RateMultiplier: 1.5, Eligible: true})
	require.InDelta(t, 0.00008, unsupported.ActualCost, 1e-12, "unsupported image token categories fall back to the legacy multiplier")
	require.True(t, unsupported.routePricingApplied, "an unsupported base card falls back to the configured legacy multiplier")

	baseOnly := decision
	baseOnly.Entries = append([]UnifiedGatewayRoutePricingEntry(nil), decision.Entries...)
	baseOnly.Entries[0].Multiplier = nil
	baseOnlyCtx := WithUnifiedGatewayRoutePricingDecision(context.Background(), baseOnly)
	native := &CostBreakdown{TotalCost: 0.000123, ActualCost: 0.0002}
	ApplyUnifiedGatewayRoutePricingWithTokenUsage(baseOnlyCtx, apiKey, 42, "glm-5.2", "", "", nil, 0, 0, "", 0, native, 1, 1,
		UnifiedGatewayRouteTokenUsage{Tokens: UsageTokens{InputTokens: 10}, RateMultiplier: 1, Eligible: false})
	require.InDelta(t, 0.0002, native.ActualCost, 1e-12, "an ineligible base card falls back to native pricing when no legacy multiplier exists")

	aggregateCacheOnly := &CostBreakdown{ActualCost: 0.0002, BillingMode: string(BillingModeToken)}
	ApplyUnifiedGatewayRoutePricingWithTokenUsage(ctx, apiKey, 42, "glm-5.2", "", "", nil, 0, 0, "", 0, aggregateCacheOnly, 1, 1,
		UnifiedGatewayRouteTokenUsage{
			Tokens:         UsageTokens{InputTokens: 10, CacheCreationTokens: 4},
			RateMultiplier: 1,
			Eligible:       true,
		})
	require.InDelta(t, 0.00008, aggregateCacheOnly.ActualCost, 1e-12,
		"a card with different 5m/1h cache prices must fall back when usage does not identify the TTL")

	unsupportedOneHourCard := decision
	unsupportedOneHourCard.Entries = append([]UnifiedGatewayRoutePricingEntry(nil), decision.Entries...)
	unsupportedOneHourCard.Entries[0].TokenBasePrice = routePricingTokenBase(10, 20, 5, 7, 15, 0)
	unsupportedOneHourCtx := WithUnifiedGatewayRoutePricingDecision(context.Background(), unsupportedOneHourCard)
	oneHourCacheUsage := UsageTokens{InputTokens: 10, CacheCreationTokens: 2, CacheCreation1hTokens: 2}

	legacyFallback := &CostBreakdown{ActualCost: 0.0002, BillingMode: string(BillingModeToken)}
	ApplyUnifiedGatewayRoutePricingWithTokenUsage(unsupportedOneHourCtx, apiKey, 42, "glm-5.2", "", "", nil, 0, 0, "", 0, legacyFallback, 1, 1,
		UnifiedGatewayRouteTokenUsage{Tokens: oneHourCacheUsage, RateMultiplier: 1, Eligible: true})
	require.InDelta(t, 0.00008, legacyFallback.ActualCost, 1e-12,
		"a zero 1h cache-write rate is an unsupported sentinel, so usage must fall back to the legacy multiplier")
	require.True(t, legacyFallback.routePricingApplied, "the legacy multiplier remains the active fallback")

	nativeFallback := &CostBreakdown{ActualCost: 0.0002, BillingMode: string(BillingModeToken)}
	unsupportedOneHourCard.Entries[0].Multiplier = nil
	nativeFallbackCtx := WithUnifiedGatewayRoutePricingDecision(context.Background(), unsupportedOneHourCard)
	ApplyUnifiedGatewayRoutePricingWithTokenUsage(nativeFallbackCtx, apiKey, 42, "glm-5.2", "", "", nil, 0, 0, "", 0, nativeFallback, 1, 1,
		UnifiedGatewayRouteTokenUsage{Tokens: oneHourCacheUsage, RateMultiplier: 1, Eligible: true})
	require.InDelta(t, 0.0002, nativeFallback.ActualCost, 1e-12,
		"without a legacy multiplier, an unsupported 1h cache-write card must leave native pricing intact")
	require.False(t, nativeFallback.routePricingApplied)
}

func TestApplyUnifiedGatewayBailianBasePriceAndYeTokenLineRate(t *testing.T) {
	apiKey := &APIKey{GroupID: routePricingInt64Ptr(7)}
	decision := UnifiedGatewayRoutePricingDecision{
		Allowed: true, GroupID: 7, Model: "glm-5.2", Kind: UnifiedGatewayRoutePricingToken,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID: 42, Model: "glm-5.2", Kind: UnifiedGatewayRoutePricingToken,
			Multiplier: routePricingFloat(0.4), TokenBasePrice: routePricingTokenBase(8, 28, 2, 0, 0, 0),
		}},
	}
	cost := &CostBreakdown{TotalCost: 0.00025, ActualCost: 9}
	ApplyUnifiedGatewayRoutePricingWithTokenUsage(
		WithUnifiedGatewayRoutePricingDecision(context.Background(), decision),
		apiKey, 42, "glm-5.2", "", "", nil, 0, 0, "", 0, cost, 1, 1,
		UnifiedGatewayRouteTokenUsage{
			Tokens:         UsageTokens{InputTokens: 1000, OutputTokens: 2000, CacheReadTokens: 3000},
			RateMultiplier: 1, Eligible: true,
		},
	)

	// (8*1,000 + 28*2,000 + 2*3,000) / 1,000,000 * 0.4 = 0.028 stars.
	require.InDelta(t, 0.028, cost.ActualCost, 1e-12)
	require.Equal(t, 0.00025, cost.TotalCost, "the route card changes only user-facing charge")
}

func TestApplyUnifiedGatewayRoutePricingUsesFixedMediaPriceAndExactSpecs(t *testing.T) {
	apiKey := &APIKey{GroupID: routePricingInt64Ptr(7)}
	imageDecision := UnifiedGatewayRoutePricingDecision{
		Allowed: true, GroupID: 7, Model: "gpt-image-2", Kind: UnifiedGatewayRoutePricingImage,
		ImageSize: "1K", ImageQuality: "high",
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID: 42, Model: "gpt-image-2", Kind: UnifiedGatewayRoutePricingImage,
			ImageSize: "1K", ImageQuality: "high", UnitPrice: routePricingFloat(0.02),
		}},
	}
	imageCost := ApplyUnifiedGatewayRoutePricing(WithUnifiedGatewayRoutePricingDecision(context.Background(), imageDecision), apiKey, 42, "gpt-image-2", "1K", "high", map[string]int{"1K": 2}, 2, 0, "", 0, &CostBreakdown{TotalCost: 0.01, ActualCost: 0.01}, 1.5, 1)
	require.InDelta(t, 0.06, imageCost.ActualCost, 1e-12)
	require.Equal(t, 0.01, imageCost.TotalCost)
	wrongOutputSize := ApplyUnifiedGatewayRoutePricing(WithUnifiedGatewayRoutePricingDecision(context.Background(), imageDecision), apiKey, 42, "gpt-image-2", "2K", "high", map[string]int{"2K": 2}, 2, 0, "", 0, &CostBreakdown{ActualCost: 0.01}, 1, 1)
	require.InDelta(t, 0.01, wrongOutputSize.ActualCost, 1e-12, "different native final size must retain native pricing")
	mixedOutputSizes := ApplyUnifiedGatewayRoutePricing(WithUnifiedGatewayRoutePricingDecision(context.Background(), imageDecision), apiKey, 42, "gpt-image-2", "1K", "high", map[string]int{"1K": 1, "2K": 1}, 2, 0, "", 0, &CostBreakdown{ActualCost: 0.01}, 1, 1)
	require.InDelta(t, 0.01, mixedOutputSizes.ActualCost, 1e-12, "mixed native output tiers must retain native pricing")

	videoDecision := UnifiedGatewayRoutePricingDecision{
		Allowed: true, GroupID: 7, Model: "grok-imagine-video-1.5", Kind: UnifiedGatewayRoutePricingVideo,
		VideoResolution: "720p", VideoDurationSeconds: 5,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID: 42, Model: "grok-imagine-video-1.5", Kind: UnifiedGatewayRoutePricingVideo,
			VideoResolution: "720p", VideoDurationSeconds: 5, UnitPrice: routePricingFloat(0.049),
		}},
	}
	videoCost := ApplyUnifiedGatewayRoutePricing(WithUnifiedGatewayRoutePricingDecision(context.Background(), videoDecision), apiKey, 42, "grok-imagine-video-1.5", "", "", nil, 0, 1, "720p", 5, &CostBreakdown{TotalCost: 0.03, ActualCost: 0.03}, 1, 1)
	require.InDelta(t, 0.049, videoCost.ActualCost, 1e-12)
	require.Equal(t, 0.03, videoCost.TotalCost)
	require.True(t, videoCost.routePricingApplied)

	mismatch := ApplyUnifiedGatewayRoutePricing(WithUnifiedGatewayRoutePricingDecision(context.Background(), videoDecision), apiKey, 42, "grok-imagine-video-1.5", "", "", nil, 0, 1, "720p", 10, &CostBreakdown{ActualCost: 0.03}, 1, 1)
	require.InDelta(t, 0.03, mismatch.ActualCost, 1e-12)
}

type routePricingSettingRepoFake struct {
	SettingRepository
	raw    string
	exists bool
}

func (r *routePricingSettingRepoFake) GetValue(context.Context, string) (string, error) {
	if !r.exists {
		return "", ErrSettingNotFound
	}
	return r.raw, nil
}

func (r *routePricingSettingRepoFake) CompareAndSetValue(_ context.Context, _ string, expected *string, value string) (bool, error) {
	if expected == nil {
		if r.exists {
			return r.raw == value, nil
		}
		r.raw, r.exists = value, true
		return true, nil
	}
	if !r.exists || r.raw != *expected {
		return false, nil
	}
	r.raw = value
	return true, nil
}

type routePricingGroupRepoFake struct {
	GroupRepository
	groups []Group
}

func (r *routePricingGroupRepoFake) ListActiveByPlatform(_ context.Context, platform string) ([]Group, error) {
	result := make([]Group, 0)
	for _, group := range r.groups {
		if group.Platform == platform && group.IsActive() {
			result = append(result, group)
		}
	}
	return result, nil
}

type routePricingAccountRepoFake struct {
	AccountRepository
	accounts []Account
}

func (r *routePricingAccountRepoFake) ListSchedulableByGroupID(context.Context, int64) ([]Account, error) {
	return append([]Account(nil), r.accounts...), nil
}

func TestUpdateUnifiedGatewayRoutePricingUsesRevisionCASAndRestartSnapshot(t *testing.T) {
	repo := &routePricingSettingRepoFake{}
	groupRepo := &routePricingGroupRepoFake{groups: []Group{{ID: 7, Name: "unified", Platform: PlatformComposite, Status: StatusActive}}}
	accountRepo := &routePricingAccountRepoFake{accounts: []Account{{ID: 42, Name: "Yetoken"}}}
	svc := &SettingService{settingRepo: repo, routePricingGroupRepo: groupRepo, routePricingAccountRepo: accountRepo}

	state, err := svc.UpdateUnifiedGatewayRoutePricing(context.Background(), UnifiedGatewayRoutePricingUpdate{
		ExpectedRevision: 0,
		TargetGroupID:    7,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID: 42, Model: "glm-5.2", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePricingFloat(0.4),
		}},
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, state.Saved.Revision)
	require.EqualValues(t, 0, state.ActiveRevision)
	require.True(t, state.RestartNeeded)

	var persisted UnifiedGatewayRoutePricingConfig
	require.NoError(t, json.Unmarshal([]byte(repo.raw), &persisted))
	require.EqualValues(t, 1, persisted.Revision)
	require.Nil(t, svc.ActiveUnifiedGatewayRoutePricing(), "saving must not hot-swap the active snapshot")

	_, err = svc.UpdateUnifiedGatewayRoutePricing(context.Background(), UnifiedGatewayRoutePricingUpdate{ExpectedRevision: 0, TargetGroupID: 7})
	require.ErrorIs(t, err, ErrUnifiedGatewayRoutePricingRevisionConflict)

	_, err = svc.UpdateUnifiedGatewayRoutePricing(context.Background(), UnifiedGatewayRoutePricingUpdate{
		ExpectedRevision: 1, TargetGroupID: 7,
		Entries: []UnifiedGatewayRoutePricingEntry{{AccountID: 999, Model: "x", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePricingFloat(1)}},
	})
	require.ErrorContains(t, err, "not schedulable")
}

func TestUnifiedGatewayRoutePricingAdminRequiresOneActiveCompositeGroup(t *testing.T) {
	groups := &routePricingGroupRepoFake{groups: []Group{
		{ID: 7, Name: "unified", Platform: PlatformComposite, Status: StatusActive},
		{ID: 8, Name: "other", Platform: PlatformComposite, Status: StatusActive},
	}}
	svc := &SettingService{
		settingRepo:             &routePricingSettingRepoFake{},
		routePricingGroupRepo:   groups,
		routePricingAccountRepo: &routePricingAccountRepoFake{},
	}
	_, err := svc.GetUnifiedGatewayRoutePricingAdminState(context.Background())
	require.ErrorContains(t, err, "exactly one active Composite group")
	_, err = svc.UpdateUnifiedGatewayRoutePricing(context.Background(), UnifiedGatewayRoutePricingUpdate{TargetGroupID: 7})
	require.ErrorContains(t, err, "exactly one active Composite group")
}

func routePricingInt64Ptr(value int64) *int64 { return &value }
