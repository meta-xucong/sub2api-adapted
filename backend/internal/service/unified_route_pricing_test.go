package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func unifiedFloat(value float64) *float64 {
	return &value
}

func unifiedBaseRule(mode string, basis UnifiedRateBasis) *UnifiedRateRule {
	return &UnifiedRateRule{
		ProfileID:             "profile-lane",
		Version:               "v1",
		BillingMode:           mode,
		UpstreamRateBasis:     basis,
		BasePriceSemantics:    UnifiedBasePriceProviderBase,
		ProviderBaseUnitPrice: unifiedFloat(0.01),
		UserMarkupMultiplier:  unifiedFloat(1.2),
	}
}

func unifiedPricingInput(now time.Time, mode UnifiedRateMode, basis UnifiedRateBasis, billingMode string, lane *UnifiedRateRule) UnifiedRoutePricingInput {
	return UnifiedRoutePricingInput{
		RouteID:          17,
		BillingLaneID:    "plus-text",
		AccountID:        42,
		ProviderIdentity: "openai-plus",
		PublicModel:      "gpt-5.5",
		UpstreamModel:    "gpt-5.5",
		Endpoint:         "chat_completions",
		BillingMode:      billingMode,
		RateMode:         mode,
		RateBasis:        basis,
		Now:              now,
		LaneRule:         lane,
	}
}

func TestResolveUnifiedRoutePriceUsesFreshProbe(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	rule := unifiedBaseRule(string(BillingModeToken), UnifiedRateBasisToken)
	rule.ManualUpstreamMultiplier = unifiedFloat(1.3)
	probe := &UnifiedProbeSnapshot{
		Status:                 UnifiedProbeStatusOK,
		Basis:                  UnifiedRateBasisToken,
		ResolvedRateMultiplier: unifiedFloat(1.5),
		SnapshotRef:            "probe-1",
		ReceivedAt:             now.Add(-time.Minute),
		FreshUntil:             now.Add(time.Hour),
	}
	in := unifiedPricingInput(now, UnifiedRateModeProbePreferred, UnifiedRateBasisToken, string(BillingModeToken), rule)
	in.Probe = probe

	snapshot, err := ResolveUnifiedRoutePrice(in)
	require.NoError(t, err)
	require.Equal(t, UnifiedRateSourceProbeDeclared, snapshot.RateSource)
	require.False(t, snapshot.ManualFallbackUsed)
	require.Equal(t, 1.5, *snapshot.EffectiveRateMultiplier)
	require.InDelta(t, 0.018, snapshot.UserUnitPrice, 1e-12)
	require.NotEmpty(t, snapshot.Digest)
}

func TestResolveUnifiedRoutePriceFallsBackWhenProbeIsStale(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	rule := unifiedBaseRule(string(BillingModeToken), UnifiedRateBasisToken)
	rule.ManualUpstreamMultiplier = unifiedFloat(1.35)
	in := unifiedPricingInput(now, UnifiedRateModeProbePreferred, UnifiedRateBasisToken, string(BillingModeToken), rule)
	in.Probe = &UnifiedProbeSnapshot{
		Status:                 UnifiedProbeStatusOK,
		Basis:                  UnifiedRateBasisToken,
		ResolvedRateMultiplier: unifiedFloat(1.5),
		FreshUntil:             now.Add(-time.Second),
	}

	snapshot, err := ResolveUnifiedRoutePrice(in)
	require.NoError(t, err)
	require.Equal(t, UnifiedRateSourceManualFallback, snapshot.RateSource)
	require.True(t, snapshot.ManualFallbackUsed)
	require.Equal(t, 1.35, *snapshot.EffectiveRateMultiplier)
}

func TestResolveUnifiedRoutePriceManualBaseOverridesSourceGroupBase(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	rule := unifiedBaseRule(string(BillingModePerRequest), UnifiedRateBasisProviderSpecific)
	rule.UpstreamRateBasis = UnifiedRateBasisProviderSpecific
	rule.ManualBaseUnitPrice = unifiedFloat(0.002)
	in := unifiedPricingInput(now, UnifiedRateModeProbePreferred, UnifiedRateBasisProviderSpecific, string(BillingModePerRequest), rule)
	in.Probe = &UnifiedProbeSnapshot{Status: UnifiedProbeStatusUnsupported, Basis: UnifiedRateBasisProviderSpecific}

	snapshot, err := ResolveUnifiedRoutePrice(in)
	require.NoError(t, err)
	require.Equal(t, UnifiedRateSourceManualFallback, snapshot.RateSource)
	require.InDelta(t, 0.002, snapshot.ProviderBaseUnitPrice, 1e-12)
	require.InDelta(t, 0.0024, snapshot.UserUnitPrice, 1e-12)
}

func TestResolveUnifiedRoutePriceProbeOnlyFailsClosed(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	rule := unifiedBaseRule(string(BillingModeToken), UnifiedRateBasisToken)
	rule.ManualUpstreamMultiplier = unifiedFloat(1.35)
	in := unifiedPricingInput(now, UnifiedRateModeProbeOnly, UnifiedRateBasisToken, string(BillingModeToken), rule)
	in.Probe = &UnifiedProbeSnapshot{Status: UnifiedProbeStatusUnsupported, Basis: UnifiedRateBasisToken}

	_, err := ResolveUnifiedRoutePrice(in)
	require.ErrorIs(t, err, ErrUnifiedPricingProbeInvalid)
}

func TestResolveUnifiedRoutePriceProbePreferredWithoutFallbackFailsClosed(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	rule := unifiedBaseRule(string(BillingModeToken), UnifiedRateBasisToken)
	in := unifiedPricingInput(now, UnifiedRateModeProbePreferred, UnifiedRateBasisToken, string(BillingModeToken), rule)
	in.Probe = &UnifiedProbeSnapshot{Status: UnifiedProbeStatusFailed, Basis: UnifiedRateBasisToken}

	_, err := ResolveUnifiedRoutePrice(in)
	require.ErrorIs(t, err, ErrUnifiedPricingProbeInvalid)
}

func TestResolveUnifiedRoutePriceManualOnlyPerRequest(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	rule := &UnifiedRateRule{
		ProfileID:            "ark-v1",
		Version:              "2026-09-10",
		BillingMode:          string(BillingModePerRequest),
		UpstreamRateBasis:    UnifiedRateBasisProviderSpecific,
		BasePriceSemantics:   UnifiedBasePriceProviderBase,
		ManualBaseUnitPrice:  unifiedFloat(0.002),
		UserMarkupMultiplier: unifiedFloat(1.2),
		FixedFee:             unifiedFloat(0.0005),
		RoundingPrecision:    6,
	}
	in := unifiedPricingInput(now, UnifiedRateModeManualOnly, UnifiedRateBasisProviderSpecific, string(BillingModePerRequest), rule)

	snapshot, err := ResolveUnifiedRoutePrice(in)
	require.NoError(t, err)
	require.Equal(t, UnifiedRateSourceManualOnly, snapshot.RateSource)
	require.Nil(t, snapshot.EffectiveRateMultiplier)
	require.InDelta(t, 0.0024, snapshot.UserUnitPrice, 1e-12)
	require.InDelta(t, 0.0053, snapshot.Charge(2, true), 1e-12)
	require.Zero(t, snapshot.Charge(2, false))
}

func TestResolveUnifiedRoutePriceRejectsBasisMismatch(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	rule := unifiedBaseRule(string(BillingModeToken), UnifiedRateBasisToken)
	in := unifiedPricingInput(now, UnifiedRateModeManualOnly, UnifiedRateBasisImage, string(BillingModeImage), rule)

	_, err := ResolveUnifiedRoutePrice(in)
	require.ErrorIs(t, err, ErrUnifiedPricingBasisMismatch)
}

func TestResolveUnifiedRoutePriceRejectsTokenProbeForImage(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	rule := &UnifiedRateRule{
		BillingMode:              string(BillingModeImage),
		UpstreamRateBasis:        UnifiedRateBasisImage,
		BasePriceSemantics:       UnifiedBasePriceProviderBase,
		ProviderBaseUnitPrice:    unifiedFloat(0.04),
		UserMarkupMultiplier:     unifiedFloat(1.2),
		ManualUpstreamMultiplier: unifiedFloat(1.3),
	}
	in := unifiedPricingInput(now, UnifiedRateModeProbePreferred, UnifiedRateBasisImage, string(BillingModeImage), rule)
	in.Probe = &UnifiedProbeSnapshot{
		Status:                 UnifiedProbeStatusOK,
		Basis:                  UnifiedRateBasisToken,
		ResolvedRateMultiplier: unifiedFloat(2),
		FreshUntil:             now.Add(time.Hour),
	}

	snapshot, err := ResolveUnifiedRoutePrice(in)
	require.NoError(t, err)
	require.Equal(t, UnifiedRateSourceManualFallback, snapshot.RateSource)
	require.Equal(t, 1.3, *snapshot.EffectiveRateMultiplier)
}

func TestResolveUnifiedRoutePriceAppliesAccountPoolLanePrecedence(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	lane := unifiedBaseRule(string(BillingModeToken), UnifiedRateBasisToken)
	lane.ManualUpstreamMultiplier = unifiedFloat(1.1)
	pool := &UnifiedRateRule{ManualUpstreamMultiplier: unifiedFloat(1.2), Version: "pool-v2"}
	account := &UnifiedRateRule{ManualUpstreamMultiplier: unifiedFloat(1.4), Version: "account-v3"}
	in := unifiedPricingInput(now, UnifiedRateModeManualOnly, UnifiedRateBasisToken, string(BillingModeToken), lane)
	in.PoolRule = pool
	in.AccountRule = account

	snapshot, err := ResolveUnifiedRoutePrice(in)
	require.NoError(t, err)
	require.Equal(t, "account", snapshot.RuleScope)
	require.Equal(t, "account-v3", snapshot.PolicyVersion)
	require.Equal(t, 1.4, *snapshot.EffectiveRateMultiplier)
}

func TestResolveUnifiedRoutePriceFinalUserPriceDoesNotDoubleCount(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	rule := &UnifiedRateRule{
		ProfileID:          "fixed-final-v1",
		Version:            "v1",
		BillingMode:        string(BillingModePerRequest),
		UpstreamRateBasis:  UnifiedRateBasisProviderSpecific,
		BasePriceSemantics: UnifiedBasePriceFinalUser,
		FinalUserUnitPrice: unifiedFloat(0.01),
	}
	in := unifiedPricingInput(now, UnifiedRateModeManualOnly, UnifiedRateBasisProviderSpecific, string(BillingModePerRequest), rule)

	snapshot, err := ResolveUnifiedRoutePrice(in)
	require.NoError(t, err)
	require.Equal(t, "final_price_override", snapshot.CalculationMode)
	require.InDelta(t, 0.03, snapshot.Charge(3, true), 1e-12)

	rule.UserMarkupMultiplier = unifiedFloat(1.2)
	_, err = ResolveUnifiedRoutePrice(in)
	require.ErrorIs(t, err, ErrUnifiedPricingDoubleCounting)
}

func TestResolveUnifiedRoutePriceRequiresExplicitUpstreamPrice(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	rule := &UnifiedRateRule{
		BillingMode:          string(BillingModeToken),
		UpstreamRateBasis:    UnifiedRateBasisToken,
		BasePriceSemantics:   UnifiedBasePriceProviderBase,
		UserMarkupMultiplier: unifiedFloat(1.2),
	}
	in := unifiedPricingInput(now, UnifiedRateModeManualOnly, UnifiedRateBasisToken, string(BillingModeToken), rule)

	_, err := ResolveUnifiedRoutePrice(in)
	require.ErrorIs(t, err, ErrUnifiedPricingBaseMissing)
}

func TestUnifiedRoutePriceDigestIsStableAndVersioned(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	rule := unifiedBaseRule(string(BillingModeToken), UnifiedRateBasisToken)
	rule.ManualUpstreamMultiplier = unifiedFloat(1.3)
	in := unifiedPricingInput(now, UnifiedRateModeManualOnly, UnifiedRateBasisToken, string(BillingModeToken), rule)

	first, err := ResolveUnifiedRoutePrice(in)
	require.NoError(t, err)
	second, err := ResolveUnifiedRoutePrice(in)
	require.NoError(t, err)
	require.Equal(t, first.Digest, second.Digest)

	rule.Version = "v2"
	third, err := ResolveUnifiedRoutePrice(in)
	require.NoError(t, err)
	require.NotEqual(t, first.Digest, third.Digest)
}

func TestUnifiedRoutePriceSnapshotContextRoundTrip(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	rule := unifiedBaseRule(string(BillingModeToken), UnifiedRateBasisToken)
	rule.ManualUpstreamMultiplier = unifiedFloat(1.3)
	snapshot, err := ResolveUnifiedRoutePrice(unifiedPricingInput(now, UnifiedRateModeManualOnly, UnifiedRateBasisToken, string(BillingModeToken), rule))
	require.NoError(t, err)

	ctx := WithUnifiedRoutePriceSnapshot(context.Background(), snapshot)
	got, ok := UnifiedRoutePriceSnapshotFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, snapshot, got)
	_, ok = UnifiedRoutePriceSnapshotFromContext(context.Background())
	require.False(t, ok)
}

func TestResolveUnifiedRoutePriceErrorsAreClassifiable(t *testing.T) {
	_, err := ResolveUnifiedRoutePrice(UnifiedRoutePricingInput{})
	require.True(t, errors.Is(err, ErrUnifiedPricingPolicyMissing))
}
