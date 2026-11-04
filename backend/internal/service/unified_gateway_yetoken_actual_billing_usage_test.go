package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestGatewayServiceRecordUsage_YeTokenExactBillPreservesSonnetUsageAndAccountCost(t *testing.T) {
	lookup := &wokeyActualBillingLookupStub{amount: decimal.RequireFromString("0.000092")}
	svc, usageRepo, billingRepo, apiKey, account, _ := newGatewayWokeyActualBillingRecordUsageTest(t, "manual", 0.15, 1.25, nil)
	*account = *yeTokenAccountForTest()
	svc.yeTokenActualBillingLookup = lookup
	const model = "claude-sonnet-5-5"
	svc.billingService = NewBillingService(svc.cfg, &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		model: {InputCostPerToken: 2e-6, OutputCostPerToken: 10e-6, CacheCreationInputTokenCost: 2.5e-6},
	}})
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	ctx := WithUnifiedGatewayRoutePricingDecision(context.Background(), wokeyRouteDecisionForTest(*apiKey.GroupID, account.ID, model, "manual", 0.15))
	input := &RecordUsageInput{
		Result: &ForwardResult{RequestID: "sonnet-local", Model: model, UpstreamModel: model,
			Usage:           ClaudeUsage{CacheCreationInputTokens: 605, OutputTokens: 3},
			UpstreamHeaders: http.Header{"X-Oneapi-Request-Id": []string{"sonnet-exact"}}},
		APIKey: apiKey, User: &User{ID: 2000}, Account: account, APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{},
	}
	// Compare to the same native settlement with lookup disabled.
	svc.yeTokenActualBillingLookup = nil
	require.NoError(t, svc.RecordUsage(ctx, input))
	nativeTotal := usageRepo.lastLog.TotalCost
	nativeAccountCost := billingRepo.lastCmd.AccountQuotaCost
	svc.yeTokenActualBillingLookup = lookup
	require.NoError(t, svc.RecordUsage(ctx, input))
	require.Equal(t, 1, lookup.calls)
	require.Equal(t, "sonnet-exact", *usageRepo.lastLog.UpstreamRequestID)
	require.InDelta(t, 0.000115, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, 0.000115, billingRepo.lastCmd.BalanceCost, 1e-12)
	require.InDelta(t, 0.000115, billingRepo.lastCmd.APIKeyQuotaCost, 1e-12)
	require.Equal(t, nativeTotal, usageRepo.lastLog.TotalCost)
	require.Equal(t, nativeAccountCost, billingRepo.lastCmd.AccountQuotaCost)
	require.Equal(t, 605, usageRepo.lastLog.CacheCreationTokens)
	require.Zero(t, usageRepo.lastLog.CacheCreation5mTokens, "do not invent missing upstream usage")
	require.Equal(t, 3, usageRepo.lastLog.OutputTokens)
}

func TestOpenAIGatewayServiceRecordUsage_YeTokenExactBillIncludesGPTSearchFees(t *testing.T) {
	lookup := &wokeyActualBillingLookupStub{amount: decimal.RequireFromString("0.0112")}
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	const model = "gpt-6-sol"
	svc.billingService = NewBillingService(svc.cfg, &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		model: {InputCostPerToken: 2e-6, OutputCostPerToken: 10e-6, CacheReadInputTokenCost: 0.2e-6},
	}})
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	account := yeTokenAccountForTest()
	account.Platform = PlatformOpenAI
	groupID := int64(16)
	apiKey := &APIKey{ID: 816, Quota: 100, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformComposite, RateMultiplier: 1.25}}
	ctx := WithUnifiedGatewayRoutePricingDecision(context.Background(), wokeyRouteDecisionForTest(groupID, account.ID, model, "manual", 0.15))
	input := &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{RequestID: "gpt-local", Model: model, UpstreamModel: model,
			Usage:           OpenAIUsage{InputTokens: 1000, CacheReadInputTokens: 500, OutputTokens: 20},
			UpstreamHeaders: http.Header{"X-Oneapi-Request-Id": []string{"gpt-exact"}}},
		APIKey: apiKey, User: &User{ID: 2000}, Account: account, APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{},
	}
	svc.yeTokenActualBillingLookup = nil
	require.NoError(t, svc.RecordUsage(ctx, input))
	nativeTotal := usageRepo.lastLog.TotalCost
	nativeAccountCost := billingRepo.lastCmd.AccountQuotaCost
	svc.yeTokenActualBillingLookup = lookup
	require.NoError(t, svc.RecordUsage(ctx, input))
	require.Equal(t, 1, lookup.calls)
	require.Equal(t, "gpt-exact", *usageRepo.lastLog.UpstreamRequestID)
	require.InDelta(t, 0.014, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, 0.014, billingRepo.lastCmd.BalanceCost, 1e-12)
	require.InDelta(t, 0.014, billingRepo.lastCmd.APIKeyQuotaCost, 1e-12)
	require.Equal(t, nativeTotal, usageRepo.lastLog.TotalCost)
	require.Equal(t, nativeAccountCost, billingRepo.lastCmd.AccountQuotaCost)
	require.Equal(t, 500, usageRepo.lastLog.InputTokens)
	require.Equal(t, 500, usageRepo.lastLog.CacheReadTokens)
	require.Equal(t, 20, usageRepo.lastLog.OutputTokens)
}
