package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestEmptyImageRequestPreservesUsageWithoutInventingImageCharge(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{RequestID: "empty_image_observed_usage", Model: "gpt-image-2", ImageRequest: true, ImageCount: 0, Usage: OpenAIUsage{InputTokens: 100, OutputTokens: 50}, Duration: time.Second},
		APIKey: &APIKey{ID: 1007}, User: &User{ID: 2007}, Account: &Account{ID: 3007},
	})
	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, 100, usageRepo.lastLog.InputTokens)
	require.Equal(t, 50, usageRepo.lastLog.OutputTokens)
	require.Zero(t, usageRepo.lastLog.ImageCount)
	require.Zero(t, usageRepo.lastLog.TotalCost)
	require.Zero(t, usageRepo.lastLog.ActualCost)
	require.Equal(t, string(BillingModeImage), *usageRepo.lastLog.BillingMode)
	require.Zero(t, userRepo.deductCalls)
}

func TestCalculateImageCostZeroDeliveryDoesNotConsultTariff(t *testing.T) {
	svc := &OpenAIGatewayService{}
	for _, count := range []int{0, -1} {
		cost := svc.calculateOpenAIImageCost(context.Background(), "gpt-image-2", nil, &OpenAIForwardResult{ImageRequest: true, ImageCount: count}, 1)
		require.Zero(t, cost.TotalCost)
		require.Zero(t, cost.ActualCost)
		require.Equal(t, string(BillingModeImage), cost.BillingMode)
	}
	require.False(t, openAIUnifiedGatewayRouteTokenPricingEligible(context.Background(), &OpenAIForwardResult{ImageRequest: true}, &CostBreakdown{}, nil, time.Now(), 1, "gpt-image-2"))
}

func TestEmptyImageRequestTariffModes(t *testing.T) {
	for _, mode := range []string{"group-image", "channel-image", "channel-token"} {
		t.Run(mode, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			price := 0.25
			group := &Group{ID: 12, RateMultiplier: 1, ImagePrice1K: &price}
			if mode == "channel-image" {
				svc.resolver = newOpenAIImageChannelPricingResolverForTest(t, 12, "gpt-image-2", price)
				group.ImagePrice1K = nil
			}
			if mode == "channel-token" {
				svc.resolver = newOpenAITokenImageChannelPricingResolverForTest(t, 12, "gpt-image-2")
				group.ImagePrice1K = nil
			}
			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{RequestID: "zero_" + mode, Model: "gpt-image-2", ImageRequest: true, ImageSize: "1K", Usage: OpenAIUsage{InputTokens: 100, OutputTokens: 50}, Duration: time.Second},
				APIKey: &APIKey{ID: 1007, GroupID: i64p(12), Group: group}, User: &User{ID: 2007}, Account: &Account{ID: 3007},
			})
			require.NoError(t, err)
			require.NotNil(t, usageRepo.lastLog)
			require.Equal(t, 100, usageRepo.lastLog.InputTokens)
			require.Zero(t, usageRepo.lastLog.ImageCount)
			if mode == "channel-token" {
				require.Equal(t, string(BillingModeToken), *usageRepo.lastLog.BillingMode)
				require.InDelta(t, 100*3e-6+50*15e-6, usageRepo.lastLog.TotalCost, 1e-12)
			} else {
				require.Equal(t, string(BillingModeImage), *usageRepo.lastLog.BillingMode)
				require.Zero(t, usageRepo.lastLog.TotalCost)
			}
		})
	}
}
