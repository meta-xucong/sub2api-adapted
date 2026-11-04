package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestWokeyActualBillingClientAcceptsAnthropicAccount(t *testing.T) {
	account := &Account{ID: 37, Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.wokey.ai", "api_key": "test-secret"}}
	client := newUnifiedGatewayWokeyActualBillingClient()
	client.client.Transport = wokeyActualRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, wokeyActualBillingRequestURL, req.URL.String())
		require.Equal(t, "Bearer test-secret", req.Header.Get("Authorization"))
		return wokeyActualHTTPResponse(req, http.StatusOK, `{"data":[{"id":"exact","status":"succeeded","usageSource":"upstream","actualAmount":"0.000540"}]}`, nil), nil
	})
	amount, err := client.LookupActualAmount(context.Background(), account, "exact")
	require.NoError(t, err)
	require.True(t, amount.Equal(decimal.RequireFromString("0.000540")))
}

func TestGatewayRecordUsageWokeyAnthropicSettlesExactBill(t *testing.T) {
	lookup := &wokeyActualBillingLookupStub{amount: decimal.RequireFromString("0.000540")}
	svc, usageRepo, billingRepo, apiKey, account, _ := newGatewayWokeyActualBillingRecordUsageTest(t, UnifiedGatewayWokeySource, 1, 1.25, nil)
	svc.wokeyActualBillingLookup = nil
	account.ID, account.Platform = 37, PlatformAnthropic
	const model = "claude-sonnet-5-5"
	svc.billingService = NewBillingService(svc.cfg, &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		model: {InputCostPerToken: 2e-6, OutputCostPerToken: 10e-6, CacheReadInputTokenCost: 0.1e-6},
	}})
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	ctx := WithUnifiedGatewayRoutePricingDecision(context.Background(), wokeyRouteDecisionForTest(*apiKey.GroupID, account.ID, model, UnifiedGatewayWokeySource, 1))
	input := &RecordUsageInput{Result: &ForwardResult{RequestID: "local-claude", Model: model,
		Usage:           ClaudeUsage{InputTokens: 300, OutputTokens: 20, CacheReadInputTokens: 100},
		UpstreamHeaders: http.Header{"X-Wokey-Request-Id": []string{"exact-claude"}}},
		APIKey: apiKey, User: &User{ID: 2000}, Account: account, APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{}}
	require.NoError(t, svc.RecordUsage(ctx, input))
	nativeTotal, nativeAccountCost := usageRepo.lastLog.TotalCost, billingRepo.lastCmd.AccountQuotaCost
	svc.wokeyActualBillingLookup = lookup
	require.NoError(t, svc.RecordUsage(ctx, input))
	require.Equal(t, 1, lookup.calls)
	require.Equal(t, int64(37), lookup.accountID)
	require.Equal(t, "exact-claude", lookup.requestID)
	require.Equal(t, "exact-claude", *usageRepo.lastLog.UpstreamRequestID)
	const expected = 0.00465750 // upstream USD × existing FX 6.9 × group markup 1.25
	require.InDelta(t, expected, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, expected, billingRepo.lastCmd.BalanceCost, 1e-12)
	require.InDelta(t, expected, billingRepo.lastCmd.APIKeyQuotaCost, 1e-12)
	require.Equal(t, nativeTotal, usageRepo.lastLog.TotalCost)
	require.Equal(t, nativeAccountCost, billingRepo.lastCmd.AccountQuotaCost)
	require.Equal(t, 300, usageRepo.lastLog.InputTokens)
	require.Equal(t, 100, usageRepo.lastLog.CacheReadTokens)
	require.Equal(t, 20, usageRepo.lastLog.OutputTokens)
}

func TestOpenAIRecordUsageWokeyAnthropicResponsesAndCompaction(t *testing.T) {
	for _, compact := range []bool{false, true} {
		name := "responses"
		if compact {
			name = "compaction"
		}
		t.Run(name, func(t *testing.T) {
			lookup := &wokeyActualBillingLookupStub{amount: decimal.RequireFromString("0.000540")}
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			const model = "claude-sonnet-5-5"
			svc.billingService = NewBillingService(svc.cfg, &PricingService{pricingData: map[string]*LiteLLMModelPricing{
				model: {InputCostPerToken: 2e-6, OutputCostPerToken: 10e-6},
			}})
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
			groupID := int64(16)
			apiKey := &APIKey{ID: 850, Quota: 100, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformComposite, RateMultiplier: 1.25}}
			account := &Account{ID: 37, Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": "https://api.wokey.ai", "api_key": "test-secret"}}
			ctx := WithUnifiedGatewayRoutePricingDecision(context.Background(), wokeyRouteDecisionForTest(groupID, account.ID, model, UnifiedGatewayWokeySource, 1))
			input := &OpenAIRecordUsageInput{Result: &OpenAIForwardResult{RequestID: "local-claude-response", Model: model,
				Usage:           OpenAIUsage{InputTokens: 300, OutputTokens: 20},
				UpstreamHeaders: http.Header{"X-Wokey-Request-Id": []string{"exact-response"}}},
				APIKey: apiKey, User: &User{ID: 2000}, Account: account, APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{}, NativeCompactionV2: compact}
			svc.wokeyActualBillingLookup = nil
			require.NoError(t, svc.RecordUsage(ctx, input))
			nativeTotal, nativeAccountCost := usageRepo.lastLog.TotalCost, billingRepo.lastCmd.AccountQuotaCost
			svc.wokeyActualBillingLookup = lookup
			require.NoError(t, svc.RecordUsage(ctx, input))
			require.Equal(t, 1, lookup.calls)
			require.Equal(t, "exact-response", *usageRepo.lastLog.UpstreamRequestID)
			require.Equal(t, compact, usageRepo.lastLog.NativeCompactionV2)
			require.InDelta(t, 0.00465750, usageRepo.lastLog.ActualCost, 1e-12)
			require.InDelta(t, 0.00465750, billingRepo.lastCmd.BalanceCost, 1e-12)
			require.InDelta(t, 0.00465750, billingRepo.lastCmd.APIKeyQuotaCost, 1e-12)
			require.Equal(t, nativeTotal, usageRepo.lastLog.TotalCost)
			require.Equal(t, nativeAccountCost, billingRepo.lastCmd.AccountQuotaCost)
		})
	}
}
