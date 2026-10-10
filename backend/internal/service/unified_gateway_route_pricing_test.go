package service

import (
	"context"
	"encoding/json"
	"errors"
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

	optionalCache := valid
	optionalCache.Entries = append([]UnifiedGatewayRoutePricingEntry(nil), valid.Entries...)
	optionalCache.Entries[0].TokenBasePrice = &UnifiedGatewayTokenBasePrice{
		InputPerMillion: routePricingFloat(3), OutputPerMillion: routePricingFloat(11), CacheReadPerMillion: routePricingFloat(1),
	}
	_, err = buildUnifiedGatewayRoutePricingSnapshot(optionalCache)
	require.NoError(t, err, "cache-write meters may be explicitly left uncovered")
}

func TestBuildUnifiedGatewayRoutePricingSnapshotScopesDynamicCardsToWokeyModels(t *testing.T) {
	entry := wokeyTimeOfDayEntryForTest(t, "deepseek-flash", "6.9")
	_, err := buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{TargetGroupID: 7, Revision: 1, Entries: []UnifiedGatewayRoutePricingEntry{entry}})
	require.NoError(t, err)

	invalid := cloneUnifiedGatewayRoutePricingEntries([]UnifiedGatewayRoutePricingEntry{entry})[0]
	invalid.Model = "not-a-wokey-dynamic-model"
	_, err = buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{TargetGroupID: 7, Revision: 1, Entries: []UnifiedGatewayRoutePricingEntry{invalid}})
	require.ErrorContains(t, err, "invalid source metadata")

	invalid = cloneUnifiedGatewayRoutePricingEntries([]UnifiedGatewayRoutePricingEntry{entry})[0]
	invalid.TokenBasePrice = routePricingTokenBase(1, 2, 3, 0, 0, 0)
	_, err = buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{TargetGroupID: 7, Revision: 1, Entries: []UnifiedGatewayRoutePricingEntry{invalid}})
	require.ErrorContains(t, err, "invalid source metadata")

	invalid = cloneUnifiedGatewayRoutePricingEntries([]UnifiedGatewayRoutePricingEntry{entry})[0]
	invalid.TimeOfDayTokenPrice.PeakWindowsUTC[1].StartHour = 3
	_, err = buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{TargetGroupID: 7, Revision: 1, Entries: []UnifiedGatewayRoutePricingEntry{invalid}})
	require.ErrorContains(t, err, "invalid token pricing fields")
}

func TestCloneUnifiedGatewayRoutePricingEntriesDeepCopiesTimeOfDayCard(t *testing.T) {
	entry := wokeyTimeOfDayEntryForTest(t, "deepseek-flash", "6.9")
	cloned := cloneUnifiedGatewayRoutePricingEntries([]UnifiedGatewayRoutePricingEntry{entry})
	cloned[0].TimeOfDayTokenPrice.Peak.SourcePricesUSD["input_tokens"] = "9"
	*cloned[0].TimeOfDayTokenPrice.OffPeak.TokenBasePrice.InputPerMillion = 99
	cloned[0].TimeOfDayTokenPrice.PeakWindowsUTC[0].StartHour = 20
	require.Equal(t, "0.224000", entry.TimeOfDayTokenPrice.Peak.SourcePricesUSD["input_tokens"])
	require.InDelta(t, 0.7728, *entry.TimeOfDayTokenPrice.OffPeak.TokenBasePrice.InputPerMillion, 1e-12)
	require.Equal(t, 1, entry.TimeOfDayTokenPrice.PeakWindowsUTC[0].StartHour)
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

func TestPrepareUnifiedGatewayWokeyTimeOfDayReservationCoversTheMoreExpensiveTier(t *testing.T) {
	entry := wokeyTimeOfDayEntryForTest(t, "deepseek-flash", "6.9")
	entry.TimeOfDayTokenPrice.Peak.TokenBasePrice = &UnifiedGatewayTokenBasePrice{
		InputPerMillion: routePricingFloat(0.69), OutputPerMillion: routePricingFloat(6.9), CacheReadPerMillion: routePricingFloat(0.345),
	}
	entry.TimeOfDayTokenPrice.Peak.SourcePricesUSD = map[string]string{"input_tokens": "0.1", "output_tokens": "1", "cache_read_tokens": "0.05"}
	entry.TimeOfDayTokenPrice.OffPeak.TokenBasePrice = &UnifiedGatewayTokenBasePrice{
		InputPerMillion: routePricingFloat(13.8), OutputPerMillion: routePricingFloat(1.38), CacheReadPerMillion: routePricingFloat(0.69),
	}
	entry.TimeOfDayTokenPrice.OffPeak.SourcePricesUSD = map[string]string{"input_tokens": "2", "output_tokens": "0.2", "cache_read_tokens": "0.1"}
	for i := range entry.SourceSKUs {
		switch entry.SourceSKUs[i].Meter {
		case "input_tokens":
			entry.SourceSKUs[i].PriceUSD = "0.1"
		case "output_tokens":
			entry.SourceSKUs[i].PriceUSD = "1"
		case "cache_read_tokens":
			entry.SourceSKUs[i].PriceUSD = "0.05"
		}
	}
	snapshot, err := buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{
		TargetGroupID: 7, Revision: 1, Entries: []UnifiedGatewayRoutePricingEntry{entry},
	})
	require.NoError(t, err)
	settings := &SettingService{}
	settings.routePricingSnapshot.Store(snapshot)
	groupID := int64(7)
	apiKey := &APIKey{GroupID: &groupID, Group: &Group{ID: 7, Platform: PlatformComposite}}
	_, estimate, planned := settings.prepareUnifiedGatewayRoutePricingDecision(
		context.Background(), apiKey,
		InflightEstimateRequest{Model: entry.Model, BodyBytes: 4000, MaxTokens: 100, Kind: InflightEstimateToken}, 0, false, 1,
	)
	require.True(t, planned)
	require.InDelta(t, 0.013938, estimate, 1e-12,
		"off-peak has the larger input meter while peak has the larger output meter, so both complete tiers must be estimated")
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

func TestPrepareUnifiedGatewayRoutePricingReservationIncludesLongContextRates(t *testing.T) {
	base := routePricingTokenBase(10, 20, 5, 0, 0, 0)
	long := routePricingTokenBase(100, 200, 50, 0, 0, 0)
	snapshot, err := buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{
		TargetGroupID: 7,
		Revision:      4,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID: 42, Model: "claude-haiku-5-5", Kind: UnifiedGatewayRoutePricingToken,
			TokenBasePrice: base, LongContextTokenBasePrice: long,
		}},
	})
	require.NoError(t, err)
	settings := &SettingService{}
	settings.routePricingSnapshot.Store(snapshot)
	groupID := int64(7)
	apiKey := &APIKey{GroupID: &groupID, Group: &Group{ID: 7, Platform: PlatformComposite}}

	_, estimate, planned := settings.prepareUnifiedGatewayRoutePricingDecision(
		context.Background(), apiKey,
		InflightEstimateRequest{Model: "claude-haiku-5-5", BodyBytes: 4000, MaxTokens: 100, Kind: InflightEstimateToken}, 0, false, 1,
	)
	require.True(t, planned)
	require.InDelta(t, 0.12, estimate, 1e-12, "the high-context rates remain in the conservative reservation candidates")
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
	aggregateCard := decision
	aggregateCard.Entries = append([]UnifiedGatewayRoutePricingEntry(nil), decision.Entries...)
	aggregateCard.Entries[0].TokenBasePrice = routePricingTokenBase(10, 20, 5, 0, 15, 30)
	aggregateCard.Entries[0].TokenBasePrice.CacheWritePerMillion = nil
	aggregateCtx := WithUnifiedGatewayRoutePricingDecision(context.Background(), aggregateCard)
	ApplyUnifiedGatewayRoutePricingWithTokenUsage(aggregateCtx, apiKey, 42, "glm-5.2", "", "", nil, 0, 0, "", 0, aggregateCacheOnly, 1, 1,
		UnifiedGatewayRouteTokenUsage{
			Tokens:         UsageTokens{InputTokens: 10, CacheCreationTokens: 4},
			RateMultiplier: 1,
			Eligible:       true,
		})
	require.InDelta(t, 0.00008, aggregateCacheOnly.ActualCost, 1e-12,
		"an unclassified cache-write meter falls back when the generic cache-write rate is not configured")

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

func TestApplyWokeyTimeOfDayRoutePriceUsesOriginalUTCRequestTime(t *testing.T) {
	entry := wokeyTimeOfDayEntryForTest(t, "deepseek-flash", "6.9")
	decision := UnifiedGatewayRoutePricingDecision{
		Allowed: true, Revision: 2, GroupID: 7, Model: entry.Model, Kind: UnifiedGatewayRoutePricingToken,
		Entries: []UnifiedGatewayRoutePricingEntry{entry},
	}
	apiKey := &APIKey{GroupID: routePricingInt64Ptr(7)}
	tests := []struct {
		name string
		at   time.Time
		want float64
	}{
		{"just before first peak window", time.Date(2026, 10, 9, 0, 59, 59, 0, time.UTC), 3.879456},
		{"first start included", time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC), 7.758912},
		{"first end excluded", time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC), 3.879456},
		{"second start included", time.Date(2026, 10, 9, 6, 0, 0, 0, time.UTC), 7.758912},
		{"second end excluded", time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC), 3.879456},
		{"last UTC hour is off peak", time.Date(2026, 10, 9, 23, 30, 0, 0, time.UTC), 3.879456},
		{"non-UTC location converted to UTC", time.Date(2026, 10, 9, 9, 0, 0, 0, time.FixedZone("UTC+9", 9*60*60)), 3.879456},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost := &CostBreakdown{TotalCost: 0.001, ActualCost: 9}
			ctx := WithUnifiedGatewayRoutePricingDecision(context.Background(), decision)
			ApplyUnifiedGatewayRoutePricingWithTokenUsage(ctx, apiKey, 42, entry.Model, "", "", nil, 0, 0, "", 0, cost, 1, 1,
				UnifiedGatewayRouteTokenUsage{
					Tokens:         UsageTokens{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000},
					RateMultiplier: 1, Eligible: true, PricingAt: tt.at,
				})
			require.InDelta(t, tt.want, cost.ActualCost, 1e-12)
			require.Equal(t, 0.001, cost.TotalCost, "only user ActualCost is changed")
			require.True(t, cost.routePricingApplied)
		})
	}
}

func TestWokeyTimeOfDayMissingPricingAtLeavesNativeActualCost(t *testing.T) {
	entry := wokeyTimeOfDayEntryForTest(t, "deepseek-flash", "6.9")
	entry.Multiplier = routePricingFloat(0.5)
	decision := UnifiedGatewayRoutePricingDecision{Allowed: true, GroupID: 7, Model: entry.Model, Kind: UnifiedGatewayRoutePricingToken, Entries: []UnifiedGatewayRoutePricingEntry{entry}}
	cost := &CostBreakdown{TotalCost: 0.002, ActualCost: 0.003}
	ApplyUnifiedGatewayRoutePricingWithTokenUsage(
		WithUnifiedGatewayRoutePricingDecision(context.Background(), decision),
		&APIKey{GroupID: routePricingInt64Ptr(7)}, 42, entry.Model, "", "", nil, 0, 0, "", 0, cost, 1, 1,
		UnifiedGatewayRouteTokenUsage{Tokens: UsageTokens{InputTokens: 1_000_000}, RateMultiplier: 1, Eligible: true},
	)
	require.Equal(t, 0.003, cost.ActualCost)
	require.Equal(t, 0.002, cost.TotalCost)
	require.False(t, cost.routePricingApplied)
}

func TestUnifiedGatewayTokenBasePriceOptionalCacheMetersRequireCoverageOnlyWhenUsed(t *testing.T) {
	base := &UnifiedGatewayTokenBasePrice{
		InputPerMillion: routePricingFloat(10), OutputPerMillion: routePricingFloat(20), CacheReadPerMillion: routePricingFloat(5),
	}
	_, covered := calculateUnifiedGatewayTokenBasePrice(base, UsageTokens{InputTokens: 10}, 1)
	require.True(t, covered, "an omitted cache meter is harmless when the request has no cache-write usage")

	for _, tc := range []struct {
		name  string
		card  *UnifiedGatewayTokenBasePrice
		usage UsageTokens
	}{
		{name: "unspecified cache write", card: base, usage: UsageTokens{CacheCreationTokens: 10}},
		{name: "unspecified five minute write", card: base, usage: UsageTokens{CacheCreationTokens: 10, CacheCreation5mTokens: 10}},
		{name: "one hour zero sentinel", card: routePricingTokenBase(10, 20, 5, 10, 10, 0), usage: UsageTokens{CacheCreationTokens: 10, CacheCreation1hTokens: 10}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, covered := calculateUnifiedGatewayTokenBasePrice(tc.card, tc.usage, 1)
			require.False(t, covered, "an uncovered meter must make the whole card inapplicable")
		})
	}

	free := *base
	free.CacheWritePerMillion = routePricingFloat(0)
	cost, covered := calculateUnifiedGatewayTokenBasePrice(&free, UsageTokens{CacheCreationTokens: 10}, 1)
	require.True(t, covered, "an explicitly zero generic cache-write rate is a free covered meter")
	require.Zero(t, cost.CacheCreationCost)
}

func TestUnifiedGatewayTokenBasePriceFallsBackWhenCacheCreationResidualIsUnpriced(t *testing.T) {
	base := routePricingTokenBase(10, 20, 5, 0, 3, 4)
	base.CacheWritePerMillion = nil
	entry := UnifiedGatewayRoutePricingEntry{
		AccountID: 42, Model: "glm-5.2", Kind: UnifiedGatewayRoutePricingToken,
		Multiplier: routePricingFloat(0.4), TokenBasePrice: base,
	}
	decision := UnifiedGatewayRoutePricingDecision{Allowed: true, GroupID: 7, Model: entry.Model, Kind: entry.Kind, Entries: []UnifiedGatewayRoutePricingEntry{entry}}
	ctx := WithUnifiedGatewayRoutePricingDecision(context.Background(), decision)
	apiKey := &APIKey{GroupID: routePricingInt64Ptr(7)}
	cost := &CostBreakdown{TotalCost: 0.0002, ActualCost: 0.0002, BillingMode: string(BillingModeToken)}

	ApplyUnifiedGatewayRoutePricingWithTokenUsage(ctx, apiKey, 42, entry.Model, "", "", nil, 0, 0, "", 0, cost, 1, 1,
		UnifiedGatewayRouteTokenUsage{
			Tokens:         UsageTokens{CacheCreationTokens: 10, CacheCreation5mTokens: 5},
			RateMultiplier: 1,
			Eligible:       true,
		})

	require.InDelta(t, 0.00008, cost.ActualCost, 1e-12, "the residual five cache-write tokens require a generic rate or the entire card falls back")
	require.True(t, cost.routePricingApplied, "the legacy line multiplier remains the active fallback")
}

func TestUnifiedGatewayTokenBasePricePricesCacheCreationResidualWithGenericRate(t *testing.T) {
	base := routePricingTokenBase(10, 20, 5, 20, 3, 4)
	cost, covered := calculateUnifiedGatewayTokenBasePrice(base, UsageTokens{CacheCreationTokens: 10, CacheCreation5mTokens: 5}, 1)

	require.True(t, covered)
	require.InDelta(t, 0.000115, cost.CacheCreationCost, 1e-12,
		"five 5m tokens use the 5m rate and the five-token aggregate residual uses the generic rate")
}

func TestUnifiedGatewayTokenBasePriceRejectsNegativeUsageMetersAndPreservesFallback(t *testing.T) {
	entry := UnifiedGatewayRoutePricingEntry{
		AccountID: 42, Model: "glm-5.2", Kind: UnifiedGatewayRoutePricingToken,
		TokenBasePrice: routePricingTokenBase(10, 20, 5, 2, 3, 4),
	}
	ctx := WithUnifiedGatewayRoutePricingDecision(context.Background(), UnifiedGatewayRoutePricingDecision{
		Allowed: true, GroupID: 7, Model: entry.Model, Kind: entry.Kind, Entries: []UnifiedGatewayRoutePricingEntry{entry},
	})
	apiKey := &APIKey{GroupID: routePricingInt64Ptr(7)}

	for _, tc := range []struct {
		name   string
		mutate func(*UsageTokens)
	}{
		{name: "input", mutate: func(tokens *UsageTokens) { tokens.InputTokens = -1 }},
		{name: "output", mutate: func(tokens *UsageTokens) { tokens.OutputTokens = -1 }},
		{name: "cache read", mutate: func(tokens *UsageTokens) { tokens.CacheReadTokens = -1 }},
		{name: "cache creation aggregate", mutate: func(tokens *UsageTokens) { tokens.CacheCreationTokens = -1 }},
		{name: "cache creation 5m detail", mutate: func(tokens *UsageTokens) { tokens.CacheCreation5mTokens = -1 }},
		{name: "cache creation 1h detail", mutate: func(tokens *UsageTokens) { tokens.CacheCreation1hTokens = -1 }},
		{name: "image input", mutate: func(tokens *UsageTokens) { tokens.ImageInputTokens = -1 }},
		{name: "image cache read", mutate: func(tokens *UsageTokens) { tokens.ImageCacheReadTokens = -1 }},
		{name: "image output", mutate: func(tokens *UsageTokens) { tokens.ImageOutputTokens = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := UsageTokens{InputTokens: 10, OutputTokens: 2, CacheReadTokens: 3}
			tc.mutate(&usage)
			cost := &CostBreakdown{TotalCost: 0.000123, ActualCost: 0.0002, BillingMode: string(BillingModeToken)}

			ApplyUnifiedGatewayRoutePricingWithTokenUsage(ctx, apiKey, 42, entry.Model, "", "", nil, 0, 0, "", 0, cost, 1, 1,
				UnifiedGatewayRouteTokenUsage{Tokens: usage, RateMultiplier: 1, Eligible: true})

			require.False(t, cost.routePricingApplied, "negative usage meters make the token base card unsupported")
			require.Equal(t, 0.0002, cost.ActualCost, "without another configured route layer, fallback must preserve native actual cost")
			require.Equal(t, 0.000123, cost.TotalCost, "route pricing must leave native total cost unchanged")
		})
	}
}

func TestUnifiedGatewayHaikuLongContextUsesActualPromptTokensAndScopesCachedFallbackToWokey(t *testing.T) {
	entry := UnifiedGatewayRoutePricingEntry{
		AccountID: 42, Model: "claude-haiku-5-5", Kind: UnifiedGatewayRoutePricingToken,
		TokenBasePrice:            routePricingTokenBase(10, 20, 5, 2, 3, 4),
		LongContextTokenBasePrice: routePricingTokenBase(100, 200, 50, 20, 30, 40),
	}
	decision := UnifiedGatewayRoutePricingDecision{Allowed: true, GroupID: 7, Model: entry.Model, Kind: entry.Kind, Entries: []UnifiedGatewayRoutePricingEntry{entry}}
	ctx := WithUnifiedGatewayRoutePricingDecision(context.Background(), decision)
	apiKey := &APIKey{GroupID: routePricingInt64Ptr(7)}

	for _, tc := range []struct {
		name          string
		usage         UsageTokens
		wantActual    float64
		wantRouteCard bool
	}{
		{name: "exactly 100000 uses base", usage: UsageTokens{InputTokens: 100_000}, wantActual: 1, wantRouteCard: true},
		{name: "100001 uses long context", usage: UsageTokens{InputTokens: 100_001}, wantActual: 10.0001, wantRouteCard: true},
		{name: "manual cache crosses 100000 and uses configured long-context price", usage: UsageTokens{InputTokens: 90_000, CacheReadTokens: 10_001}, wantActual: 9.50005, wantRouteCard: true},
		{name: "manual cache details cross 100000 and use configured long-context price", usage: UsageTokens{InputTokens: 90_000, CacheCreation5mTokens: 10_001}, wantActual: 9.30003, wantRouteCard: true},
		{name: "cache at 100000 remains base card", usage: UsageTokens{InputTokens: 90_000, CacheReadTokens: 10_000}, wantActual: 0.95, wantRouteCard: true},
		{name: "aggregate and cache details are not double counted", usage: UsageTokens{InputTokens: 95_000, CacheCreationTokens: 4_000, CacheCreation5mTokens: 10_000}, wantActual: 0.962, wantRouteCard: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cost := &CostBreakdown{ActualCost: 77, TotalCost: 0.123}
			ApplyUnifiedGatewayRoutePricingWithTokenUsage(ctx, apiKey, 42, entry.Model, "", "", nil, 0, 0, "", 0, cost, 1, 1,
				UnifiedGatewayRouteTokenUsage{Tokens: tc.usage, RateMultiplier: 1, Eligible: true})
			require.InDelta(t, tc.wantActual, cost.ActualCost, 1e-12)
			require.Equal(t, tc.wantRouteCard, cost.routePricingApplied)
			require.Equal(t, 0.123, cost.TotalCost, "route pricing must not change the native cost-side total")
		})
	}

	wokeyEntry := entry
	wokeyEntry.Source = UnifiedGatewayWokeySource
	wokeyDecision := UnifiedGatewayRoutePricingDecision{Allowed: true, GroupID: 7, Model: wokeyEntry.Model, Kind: wokeyEntry.Kind, Entries: []UnifiedGatewayRoutePricingEntry{wokeyEntry}}
	wokeyContext := WithUnifiedGatewayRoutePricingDecision(context.Background(), wokeyDecision)
	for _, usage := range []UsageTokens{
		{InputTokens: 90_000, CacheReadTokens: 10_001},
		{InputTokens: 90_000, CacheCreation5mTokens: 10_001},
	} {
		cost := &CostBreakdown{ActualCost: 77}
		ApplyUnifiedGatewayRoutePricingWithTokenUsage(wokeyContext, apiKey, 42, wokeyEntry.Model, "", "", nil, 0, 0, "", 0, cost, 1, 1,
			UnifiedGatewayRouteTokenUsage{Tokens: usage, RateMultiplier: 1, Eligible: true})
		require.Equal(t, 77.0, cost.ActualCost, "unverified cached prompt threshold applies only to Wokey-managed cards")
		require.False(t, cost.routePricingApplied)
	}

	withoutLongContextCard := decision
	withoutLongContextCard.Entries = append([]UnifiedGatewayRoutePricingEntry(nil), decision.Entries...)
	withoutLongContextCard.Entries[0].LongContextTokenBasePrice = nil
	withoutLongContext := WithUnifiedGatewayRoutePricingDecision(context.Background(), withoutLongContextCard)
	cost := &CostBreakdown{ActualCost: 77}
	ApplyUnifiedGatewayRoutePricingWithTokenUsage(withoutLongContext, apiKey, 42, entry.Model, "", "", nil, 0, 0, "", 0, cost, 1, 1,
		UnifiedGatewayRouteTokenUsage{Tokens: UsageTokens{InputTokens: 100_001}, RateMultiplier: 1, Eligible: true})
	require.Equal(t, 77.0, cost.ActualCost, "a base card without the advertised high-context tier must not be used above the threshold")
	require.False(t, cost.routePricingApplied)

	nonHaiku := entry
	nonHaiku.Model = "gpt-5.6-sol"
	nonHaikuDecision := UnifiedGatewayRoutePricingDecision{Allowed: true, GroupID: 7, Model: nonHaiku.Model, Kind: nonHaiku.Kind, Entries: []UnifiedGatewayRoutePricingEntry{nonHaiku}}
	nonHaikuCost := &CostBreakdown{ActualCost: 77}
	ApplyUnifiedGatewayRoutePricingWithTokenUsage(WithUnifiedGatewayRoutePricingDecision(context.Background(), nonHaikuDecision), apiKey, 42, nonHaiku.Model, "", "", nil, 0, 0, "", 0, nonHaikuCost, 1, 1,
		UnifiedGatewayRouteTokenUsage{Tokens: UsageTokens{InputTokens: 100_001}, RateMultiplier: 1, Eligible: true})
	require.InDelta(t, 1.00001, nonHaikuCost.ActualCost, 1e-12, "the 100K tier applies only to the explicitly configured Haiku model")
	require.True(t, nonHaikuCost.routePricingApplied)
}

func TestUnifiedGatewayFlatPerImageMatchesWithoutImageSpecification(t *testing.T) {
	snapshot, err := buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{
		TargetGroupID: 7,
		Revision:      2,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID: 42, Model: "gpt-image-2.5", Kind: UnifiedGatewayRoutePricingImage,
			ImagePricingMode: UnifiedGatewayImagePricingFlatPerImage, UnitPrice: routePricingFloat(0.07),
		}},
	})
	require.NoError(t, err)
	settings := &SettingService{}
	settings.routePricingSnapshot.Store(snapshot)
	groupID := int64(7)
	apiKey := &APIKey{GroupID: &groupID, Group: &Group{ID: 7, Platform: PlatformComposite}}

	for _, tc := range []struct {
		name    string
		size    string
		quality string
	}{
		{name: "missing size and quality"},
		{name: "auto size and quality", size: "auto", quality: "auto"},
		{name: "requested size and quality", size: "2K", quality: "high"},
		{name: "concrete dimensions and explicit quality", size: "1024x1024", quality: "low"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, estimate, planned := settings.prepareUnifiedGatewayRoutePricingDecision(
				context.Background(), apiKey,
				InflightEstimateRequest{Model: "gpt-image-2.5", Kind: InflightEstimateImage, Units: 2, ImageSize: tc.size, ImageQuality: tc.quality},
				0, false, 1.5,
			)
			require.True(t, planned, "flat image cards can reserve without a requested spec")
			require.InDelta(t, 0.21, estimate, 1e-12)
			ctx = WithUnifiedGatewayRoutePricingDecisionAllowed(ctx, true)
			cost := &CostBreakdown{ActualCost: 1, TotalCost: 0.25}
			ApplyUnifiedGatewayRoutePricing(ctx, apiKey, 42, "gpt-image-2.5", "4K", "draft", map[string]int{"1K": 1, "4K": 1}, 2, 0, "", 0, cost, 1.5, 1)
			require.InDelta(t, 0.21, cost.ActualCost, 1e-12, "a flat card ignores requested/returned size and quality")
			require.Equal(t, 0.25, cost.TotalCost)
			require.True(t, cost.routePricingApplied)
		})
	}
}

func TestBuildUnifiedGatewayRoutePricingSnapshotRejectsFlatAndExactImageConflict(t *testing.T) {
	flat := UnifiedGatewayRoutePricingEntry{
		AccountID: 42, Model: "gpt-image-2.5", Kind: UnifiedGatewayRoutePricingImage,
		ImagePricingMode: UnifiedGatewayImagePricingFlatPerImage, UnitPrice: routePricingFloat(0.07),
	}
	exact := UnifiedGatewayRoutePricingEntry{
		AccountID: 42, Model: "gpt-image-2.5", Kind: UnifiedGatewayRoutePricingImage,
		ImageSize: "1K", ImageQuality: "high", UnitPrice: routePricingFloat(0.07),
	}
	for _, entries := range [][]UnifiedGatewayRoutePricingEntry{{flat, exact}, {exact, flat}} {
		_, err := buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{TargetGroupID: 7, Revision: 1, Entries: entries})
		require.ErrorContains(t, err, "cannot combine flat per-image")
	}

	invalidFlat := flat
	invalidFlat.ImageSize = "1K"
	_, err := buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{TargetGroupID: 7, Revision: 1, Entries: []UnifiedGatewayRoutePricingEntry{invalidFlat}})
	require.ErrorContains(t, err, "cannot include an image size or quality")
}

func TestOpenAILongContextBillingMarkerDoesNotHideExplicitRouteLongContextCard(t *testing.T) {
	model := "claude-haiku-5-5"
	entry := UnifiedGatewayRoutePricingEntry{
		AccountID: 42, Model: model, Kind: UnifiedGatewayRoutePricingToken,
		TokenBasePrice:            routePricingTokenBase(10, 20, 5, 0, 0, 0),
		LongContextTokenBasePrice: routePricingTokenBase(100, 200, 50, 0, 0, 0),
	}
	decision := UnifiedGatewayRoutePricingDecision{Allowed: true, GroupID: 7, Model: model, Kind: UnifiedGatewayRoutePricingToken, Entries: []UnifiedGatewayRoutePricingEntry{entry}}
	ctx := WithUnifiedGatewayRoutePricingDecision(context.Background(), decision)
	cost := &CostBreakdown{ActualCost: 77, TotalCost: 0.5, LongContextBillingApplied: true}
	apiKey := &APIKey{GroupID: routePricingInt64Ptr(7)}
	eligible := openAIUnifiedGatewayRouteTokenPricingEligible(ctx, &OpenAIForwardResult{}, cost, apiKey, time.Time{}, 42, model)
	require.True(t, eligible, "the explicit route long-context tier is not blocked by native long-context pricing")

	ApplyUnifiedGatewayRoutePricingWithTokenUsage(ctx, apiKey, 42, model, "", "", nil, 0, 0, "", 0, cost, 1, 1,
		UnifiedGatewayRouteTokenUsage{Tokens: UsageTokens{InputTokens: 100_001}, RateMultiplier: 1, Eligible: eligible})
	require.InDelta(t, 10.0001, cost.ActualCost, 1e-12)
	require.Equal(t, 0.5, cost.TotalCost)
	require.True(t, cost.LongContextBillingApplied, "the existing native-tier marker remains intact")
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
	raw             string
	exists          bool
	forceCASFailure bool
}

func (r *routePricingSettingRepoFake) GetValue(context.Context, string) (string, error) {
	if !r.exists {
		return "", ErrSettingNotFound
	}
	return r.raw, nil
}

func (r *routePricingSettingRepoFake) CompareAndSetValue(_ context.Context, _ string, expected *string, value string) (bool, error) {
	if r.forceCASFailure {
		return false, nil
	}
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

type routePricingFailAfterFirstReadRepo struct {
	routePricingSettingRepoFake
	getValueCalls int
}

func (r *routePricingFailAfterFirstReadRepo) GetValue(ctx context.Context, key string) (string, error) {
	r.getValueCalls++
	if r.getValueCalls > 1 {
		return "", errors.New("settings read unavailable after CAS")
	}
	return r.routePricingSettingRepoFake.GetValue(ctx, key)
}

type routePricingBlockingCASRepo struct {
	routePricingSettingRepoFake
	casPersisted chan struct{}
	releaseCAS   chan struct{}
}

func (r *routePricingBlockingCASRepo) CompareAndSetValue(ctx context.Context, key string, expected *string, value string) (bool, error) {
	updated, err := r.routePricingSettingRepoFake.CompareAndSetValue(ctx, key, expected, value)
	close(r.casPersisted)
	<-r.releaseCAS
	return updated, err
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

func TestUpdateUnifiedGatewayRoutePricingUsesCASAndHotPublishesTheSameRevision(t *testing.T) {
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
	require.EqualValues(t, 1, state.ActiveRevision)
	require.False(t, state.RestartNeeded)

	var persisted UnifiedGatewayRoutePricingConfig
	require.NoError(t, json.Unmarshal([]byte(repo.raw), &persisted))
	require.EqualValues(t, 1, persisted.Revision)
	require.EqualValues(t, 1, svc.ActiveUnifiedGatewayRoutePricing().Revision, "a successful CAS immediately hot-publishes the matching snapshot")

	_, err = svc.UpdateUnifiedGatewayRoutePricing(context.Background(), UnifiedGatewayRoutePricingUpdate{ExpectedRevision: 0, TargetGroupID: 7})
	require.ErrorIs(t, err, ErrUnifiedGatewayRoutePricingRevisionConflict)

	_, err = svc.UpdateUnifiedGatewayRoutePricing(context.Background(), UnifiedGatewayRoutePricingUpdate{
		ExpectedRevision: 1, TargetGroupID: 7,
		Entries: []UnifiedGatewayRoutePricingEntry{{AccountID: 999, Model: "x", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePricingFloat(1)}},
	})
	require.ErrorContains(t, err, "not schedulable")
}

func TestUpdateUnifiedGatewayRoutePricingHasNoFallibleReadAfterSuccessfulCAS(t *testing.T) {
	repo := &routePricingFailAfterFirstReadRepo{}
	svc := &SettingService{
		settingRepo:             repo,
		routePricingGroupRepo:   &routePricingGroupRepoFake{groups: []Group{{ID: 7, Name: "unified", Platform: PlatformComposite, Status: StatusActive}}},
		routePricingAccountRepo: &routePricingAccountRepoFake{accounts: []Account{{ID: 42, Name: "Wokey"}}},
	}
	state, err := svc.UpdateUnifiedGatewayRoutePricing(context.Background(), UnifiedGatewayRoutePricingUpdate{
		ExpectedRevision: 0, TargetGroupID: 7,
		Entries: []UnifiedGatewayRoutePricingEntry{{AccountID: 42, Model: "gpt-5.4-nano", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePricingFloat(1)}},
	})
	require.NoError(t, err, "after CAS succeeds, response construction must not depend on another repository read")
	require.Equal(t, 1, repo.getValueCalls)
	require.EqualValues(t, 1, state.Saved.Revision)
	require.EqualValues(t, 1, state.ActiveRevision)
}

func TestUpdateUnifiedGatewayRoutePricingCASFailureDoesNotPublishCandidate(t *testing.T) {
	repo := &routePricingSettingRepoFake{forceCASFailure: true}
	svc := &SettingService{
		settingRepo:             repo,
		routePricingGroupRepo:   &routePricingGroupRepoFake{groups: []Group{{ID: 7, Name: "unified", Platform: PlatformComposite, Status: StatusActive}}},
		routePricingAccountRepo: &routePricingAccountRepoFake{accounts: []Account{{ID: 42, Name: "Wokey"}}},
	}
	old, err := buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{TargetGroupID: 7, Revision: 8})
	require.NoError(t, err)
	svc.routePricingSnapshot.Store(old)

	_, err = svc.UpdateUnifiedGatewayRoutePricing(context.Background(), UnifiedGatewayRoutePricingUpdate{
		ExpectedRevision: 0, TargetGroupID: 7,
		Entries: []UnifiedGatewayRoutePricingEntry{{AccountID: 42, Model: "gpt-5.4-nano", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePricingFloat(1)}},
	})
	require.ErrorIs(t, err, ErrUnifiedGatewayRoutePricingRevisionConflict)
	require.Same(t, old, svc.getActiveUnifiedGatewayRoutePricingSnapshot())
	require.False(t, repo.exists)
}

func TestConcurrentRoutePricingSavesSerializeByRevision(t *testing.T) {
	repo := &routePricingSettingRepoFake{}
	svc := &SettingService{
		settingRepo:             repo,
		routePricingGroupRepo:   &routePricingGroupRepoFake{groups: []Group{{ID: 7, Name: "unified", Platform: PlatformComposite, Status: StatusActive}}},
		routePricingAccountRepo: &routePricingAccountRepoFake{accounts: []Account{{ID: 42, Name: "Wokey"}}},
	}
	type result struct {
		state *UnifiedGatewayRoutePricingAdminState
		err   error
	}
	results := make(chan result, 2)
	for _, model := range []string{"model-a", "model-b"} {
		model := model
		go func() {
			state, err := svc.UpdateUnifiedGatewayRoutePricing(context.Background(), UnifiedGatewayRoutePricingUpdate{
				ExpectedRevision: 0, TargetGroupID: 7,
				Entries: []UnifiedGatewayRoutePricingEntry{{AccountID: 42, Model: model, Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePricingFloat(1)}},
			})
			results <- result{state: state, err: err}
		}()
	}
	oneSucceeded, oneConflicted := false, false
	for range 2 {
		result := <-results
		if result.err == nil {
			oneSucceeded = true
			require.EqualValues(t, 1, result.state.Saved.Revision)
			require.EqualValues(t, 1, result.state.ActiveRevision)
		} else {
			oneConflicted = errors.Is(result.err, ErrUnifiedGatewayRoutePricingRevisionConflict)
		}
	}
	require.True(t, oneSucceeded)
	require.True(t, oneConflicted)
	state, err := svc.GetUnifiedGatewayRoutePricingAdminState(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, state.Saved.Revision, state.ActiveRevision)
}

func TestConcurrentRoutePricingGetCannotObservePersistedButUnpublishedRevision(t *testing.T) {
	repo := &routePricingBlockingCASRepo{casPersisted: make(chan struct{}), releaseCAS: make(chan struct{})}
	svc := &SettingService{
		settingRepo:             repo,
		routePricingGroupRepo:   &routePricingGroupRepoFake{groups: []Group{{ID: 7, Name: "unified", Platform: PlatformComposite, Status: StatusActive}}},
		routePricingAccountRepo: &routePricingAccountRepoFake{accounts: []Account{{ID: 42, Name: "Wokey"}}},
	}
	type result struct {
		state *UnifiedGatewayRoutePricingAdminState
		err   error
	}
	updateDone := make(chan result, 1)
	go func() {
		state, err := svc.UpdateUnifiedGatewayRoutePricing(context.Background(), UnifiedGatewayRoutePricingUpdate{
			ExpectedRevision: 0, TargetGroupID: 7,
			Entries: []UnifiedGatewayRoutePricingEntry{{AccountID: 42, Model: "model-a", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePricingFloat(1)}},
		})
		updateDone <- result{state: state, err: err}
	}()
	<-repo.casPersisted
	getStarted := make(chan struct{})
	getDone := make(chan result, 1)
	go func() {
		close(getStarted)
		state, err := svc.GetUnifiedGatewayRoutePricingAdminState(context.Background())
		getDone <- result{state: state, err: err}
	}()
	<-getStarted
	select {
	case got := <-getDone:
		close(repo.releaseCAS)
		require.NoError(t, got.err)
		updated := <-updateDone
		require.NoError(t, updated.err)
		t.Fatalf("GET observed while CAS had persisted but snapshot publication was blocked: saved=%d active=%d", got.state.Saved.Revision, got.state.ActiveRevision)
	case <-time.After(20 * time.Millisecond):
	}
	close(repo.releaseCAS)
	updated := <-updateDone
	got := <-getDone
	require.NoError(t, updated.err)
	require.NoError(t, got.err)
	require.EqualValues(t, 1, updated.state.Saved.Revision)
	require.EqualValues(t, 1, updated.state.ActiveRevision)
	require.EqualValues(t, 1, got.state.Saved.Revision)
	require.EqualValues(t, 1, got.state.ActiveRevision)
}

func TestRoutePricingDecisionRetainsCapturedRevisionAfterHotUpdate(t *testing.T) {
	initialConfig := UnifiedGatewayRoutePricingConfig{
		TargetGroupID: 7, Revision: 1,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID: 42, Model: "model-a", Kind: UnifiedGatewayRoutePricingToken, TokenBasePrice: routePricingTokenBase(10, 20, 5, 0, 0, 0),
		}},
	}
	initialRaw, err := json.Marshal(initialConfig)
	require.NoError(t, err)
	repo := &routePricingSettingRepoFake{raw: string(initialRaw), exists: true}
	svc := &SettingService{
		settingRepo:             repo,
		routePricingGroupRepo:   &routePricingGroupRepoFake{groups: []Group{{ID: 7, Name: "unified", Platform: PlatformComposite, Status: StatusActive}}},
		routePricingAccountRepo: &routePricingAccountRepoFake{accounts: []Account{{ID: 42, Name: "Wokey"}}},
	}
	require.NoError(t, svc.LoadUnifiedGatewayRoutePricingAtStartup(context.Background()))
	groupID := int64(7)
	apiKey := &APIKey{GroupID: &groupID, Group: &Group{ID: 7, Platform: PlatformComposite}}
	oldCtx, _, planned := svc.prepareUnifiedGatewayRoutePricingDecision(context.Background(), apiKey,
		InflightEstimateRequest{Model: "model-a", Kind: InflightEstimateToken, MaxTokens: 1}, 0, false, 1)
	require.True(t, planned)
	oldDecision, ok := UnifiedGatewayRoutePricingDecisionFromContext(oldCtx)
	require.True(t, ok)
	require.EqualValues(t, 1, oldDecision.Revision)
	oldCtx = WithUnifiedGatewayRoutePricingDecisionAllowed(oldCtx, true)

	state, err := svc.UpdateUnifiedGatewayRoutePricing(context.Background(), UnifiedGatewayRoutePricingUpdate{
		ExpectedRevision: 1, TargetGroupID: 7,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID: 42, Model: "model-a", Kind: UnifiedGatewayRoutePricingToken, TokenBasePrice: routePricingTokenBase(20, 40, 10, 0, 0, 0),
		}},
	})
	require.NoError(t, err)
	require.EqualValues(t, 2, state.Saved.Revision)
	require.EqualValues(t, 2, state.ActiveRevision)

	oldCost := &CostBreakdown{ActualCost: 99}
	ApplyUnifiedGatewayRoutePricingWithTokenUsage(oldCtx, apiKey, 42, "model-a", "", "", nil, 0, 0, "", 0, oldCost, 1, 1,
		UnifiedGatewayRouteTokenUsage{Tokens: UsageTokens{InputTokens: 1}, RateMultiplier: 1, Eligible: true})
	require.InDelta(t, 0.00001, oldCost.ActualCost, 1e-12, "an in-flight request uses the captured revision")

	newCtx, _, planned := svc.prepareUnifiedGatewayRoutePricingDecision(context.Background(), apiKey,
		InflightEstimateRequest{Model: "model-a", Kind: InflightEstimateToken, MaxTokens: 1}, 0, false, 1)
	require.True(t, planned)
	newDecision, ok := UnifiedGatewayRoutePricingDecisionFromContext(newCtx)
	require.True(t, ok)
	require.EqualValues(t, 2, newDecision.Revision)
	newCtx = WithUnifiedGatewayRoutePricingDecisionAllowed(newCtx, true)
	newCost := &CostBreakdown{ActualCost: 99}
	ApplyUnifiedGatewayRoutePricingWithTokenUsage(newCtx, apiKey, 42, "model-a", "", "", nil, 0, 0, "", 0, newCost, 1, 1,
		UnifiedGatewayRouteTokenUsage{Tokens: UsageTokens{InputTokens: 1}, RateMultiplier: 1, Eligible: true})
	require.InDelta(t, 0.00002, newCost.ActualCost, 1e-12, "the next request uses the newly active revision")
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
