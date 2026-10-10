package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type wokeyActualBillingLookupStub struct {
	amount    decimal.Decimal
	err       error
	calls     int
	accountID int64
	requestID string
}

func (s *wokeyActualBillingLookupStub) LookupActualAmount(_ context.Context, account *Account, requestID string) (decimal.Decimal, error) {
	s.calls++
	if account != nil {
		s.accountID = account.ID
	}
	s.requestID = requestID
	return s.amount, s.err
}

func wokeyRouteDecisionForTest(groupID, accountID int64, model, source string, lineMultiplier float64) UnifiedGatewayRoutePricingDecision {
	input, output, cache := 2.0, 3.0, 0.5
	return UnifiedGatewayRoutePricingDecision{
		Allowed: true,
		GroupID: groupID,
		Model:   model,
		Kind:    UnifiedGatewayRoutePricingToken,
		Entries: []UnifiedGatewayRoutePricingEntry{{
			AccountID:  accountID,
			Model:      model,
			Kind:       UnifiedGatewayRoutePricingToken,
			Multiplier: &lineMultiplier,
			TokenBasePrice: &UnifiedGatewayTokenBasePrice{
				InputPerMillion:     &input,
				OutputPerMillion:    &output,
				CacheReadPerMillion: &cache,
			},
			Source:   source,
			SourceFX: "6.9",
		}},
	}
}

func newGatewayWokeyActualBillingRecordUsageTest(t *testing.T, source string, lineMultiplier, groupMultiplier float64, lookup *wokeyActualBillingLookupStub) (*GatewayService, *openAIRecordUsageLogRepoStub, *openAIRecordUsageBillingRepoStub, *APIKey, *Account, context.Context) {
	t.Helper()
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1.1
	svc := NewGatewayService(
		nil, nil, usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil, nil,
		cfg, nil, nil, NewBillingService(cfg, nil), nil, &BillingCacheService{}, nil, nil, &DeferredService{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	svc.billingService = NewBillingService(svc.cfg, &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"wokey-test-model": {InputCostPerToken: 1e-6, OutputCostPerToken: 3e-6, CacheReadInputTokenCost: 0.5e-6},
	}})
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	svc.wokeyActualBillingLookup = lookup
	groupID, accountID := int64(16), int64(28)
	group := &Group{ID: groupID, Platform: PlatformComposite, RateMultiplier: groupMultiplier}
	apiKey := &APIKey{ID: 816, Quota: 100, GroupID: &groupID, Group: group}
	account := &Account{
		ID:          accountID,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "wokey-test-secret", "base_url": "https://api.wokey.ai/v1"},
	}
	ctx := WithUnifiedGatewayRoutePricingDecision(context.Background(), wokeyRouteDecisionForTest(groupID, accountID, "wokey-test-model", source, lineMultiplier))
	return svc, usageRepo, billingRepo, apiKey, account, ctx
}

func TestFindWokeyTokenPriceEntryUsesAuthorizedRouteDecisionScope(t *testing.T) {
	groupID, accountID := int64(16), int64(28)
	decision := wokeyRouteDecisionForTest(groupID, accountID, "wokey-test-model", UnifiedGatewayWokeySource, 1)
	for _, tc := range []struct {
		name  string
		group *Group
	}{
		{name: "different group snapshot platform", group: &Group{ID: groupID, Platform: PlatformOpenAI}},
		{name: "missing group snapshot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			apiKey := &APIKey{GroupID: &groupID, Group: tc.group}
			entry, matched := findWokeyTokenPriceEntry(decision, apiKey, accountID, "wokey-test-model")
			require.True(t, matched, "the exact authorized route decision and GroupID are authoritative without a current group snapshot")
			require.Equal(t, UnifiedGatewayWokeySource, entry.Source)
		})
	}
}

func TestGatewayServiceRecordUsage_WokeyActualBillingSettlesUserCostFromExactUpstreamRequest(t *testing.T) {
	lookup := &wokeyActualBillingLookupStub{amount: decimal.RequireFromString("0.000059")}
	svc, usageRepo, billingRepo, apiKey, account, ctx := newGatewayWokeyActualBillingRecordUsageTest(t, UnifiedGatewayWokeySource, 1.1, 1.2, lookup)
	apiKey.Key = "test-downstream-key"
	quotaSvc := &openAIRecordUsageAPIKeyQuotaStub{}
	headers := make(http.Header)
	headers.Set("X-Wokey-Request-Id", "wokey-exact-123")

	err := svc.RecordUsage(ctx, &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:       "local-request-123",
			Model:           "wokey-test-model",
			UpstreamModel:   "wokey-test-model",
			Usage:           ClaudeUsage{InputTokens: 17, OutputTokens: 13},
			UpstreamHeaders: headers,
			Duration:        time.Second,
		},
		APIKey:        apiKey,
		User:          &User{ID: 2000},
		Account:       account,
		APIKeyService: quotaSvc,
	})
	require.NoError(t, err)
	require.Equal(t, 1, lookup.calls)
	require.Equal(t, int64(28), lookup.accountID)
	require.Equal(t, "wokey-exact-123", lookup.requestID)
	require.NotNil(t, usageRepo.lastLog.UpstreamRequestID)
	require.Equal(t, "wokey-exact-123", *usageRepo.lastLog.UpstreamRequestID)

	const expected = 0.00053737 // 0.000059 USD × 6.9 FX × 1.1 line × 1.2 group, quantized to native settlement precision
	require.InDelta(t, expected, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, expected, billingRepo.lastCmd.BalanceCost, 1e-12)
	require.InDelta(t, expected, billingRepo.lastCmd.APIKeyQuotaCost, 1e-12)
	require.InDelta(t, 0.000056, usageRepo.lastLog.TotalCost, 1e-12, "native TotalCost must remain unchanged by Wokey actual billing")
	require.Equal(t, 17, usageRepo.lastLog.InputTokens, "upstream usage fields remain unchanged")
	require.Equal(t, 13, usageRepo.lastLog.OutputTokens)
}

func TestOpenAIGatewayServiceRecordUsage_WokeyActualBillingUsesExactRequestAndPreservesNativeCost(t *testing.T) {
	lookup := &wokeyActualBillingLookupStub{amount: decimal.RequireFromString("0.000062")}
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	quotaSvc := &openAIRecordUsageAPIKeyQuotaStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)
	svc.wokeyActualBillingLookup = lookup
	svc.billingService = NewBillingService(svc.cfg, &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"grok-4.5": {InputCostPerToken: 1e-6, OutputCostPerToken: 3e-6, CacheReadInputTokenCost: 0.5e-6},
	}})
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)

	groupID, accountID := int64(16), int64(28)
	group := &Group{ID: groupID, Platform: PlatformComposite, RateMultiplier: 1}
	apiKey := &APIKey{ID: 816, Quota: 100, GroupID: &groupID, Group: group}
	account := &Account{
		ID: accountID, Platform: PlatformGrok, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "wokey-test-secret", "base_url": "https://api.wokey.ai/v1"},
	}
	ctx := WithUnifiedGatewayRoutePricingDecision(context.Background(), wokeyRouteDecisionForTest(
		groupID, accountID, "grok-4.5", UnifiedGatewayWokeySource, 1,
	))
	err := svc.RecordUsage(ctx, &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID: "local-grok-request", Model: "grok-4.5", UpstreamModel: "grok-4.5",
			Usage:           OpenAIUsage{InputTokens: 508, CacheReadInputTokens: 384, OutputTokens: 20},
			UpstreamHeaders: http.Header{"X-Wokey-Request-Id": []string{"2026100903073570v4x"}},
			Duration:        time.Second,
		},
		APIKey: apiKey, User: &User{ID: 1}, Account: account, APIKeyService: quotaSvc,
	})
	require.NoError(t, err)
	require.Equal(t, 1, lookup.calls)
	require.Equal(t, accountID, lookup.accountID)
	require.Equal(t, "2026100903073570v4x", lookup.requestID)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, 124, usageRepo.lastLog.InputTokens, "ordinary input excludes the 384 cached tokens")
	require.Equal(t, 384, usageRepo.lastLog.CacheReadTokens)
	require.Equal(t, 20, usageRepo.lastLog.OutputTokens)
	require.InDelta(t, 0.0004278, usageRepo.lastLog.ActualCost, 1e-12, "$0.000062 × FX 6.9")
	require.InDelta(t, 0.000376, usageRepo.lastLog.TotalCost, 1e-12, "native TotalCost remains based on the configured model pricing")
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, usageRepo.lastLog.ActualCost, billingRepo.lastCmd.BalanceCost, 1e-12)
}

func TestGatewayServiceRecordUsage_WokeyActualBillingFeedsSubscriptionQuota(t *testing.T) {
	lookup := &wokeyActualBillingLookupStub{amount: decimal.RequireFromString("0.000054")}
	svc, usageRepo, billingRepo, apiKey, account, ctx := newGatewayWokeyActualBillingRecordUsageTest(t, UnifiedGatewayWokeySource, 1, 1, lookup)
	apiKey.Group.SubscriptionType = SubscriptionTypeSubscription
	subscription := &UserSubscription{ID: 300}
	err := svc.RecordUsage(ctx, &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:       "local-subscription-request",
			Model:           "wokey-test-model",
			Usage:           ClaudeUsage{OutputTokens: 13},
			UpstreamHeaders: http.Header{"X-Wokey-Request-Id": []string{"wokey-exact-subscription"}},
		},
		APIKey:       apiKey,
		User:         &User{ID: 2001},
		Account:      account,
		Subscription: subscription,
	})
	require.NoError(t, err)
	require.Equal(t, 1, lookup.calls)
	require.InDelta(t, 0.0003726, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, 0.0003726, billingRepo.lastCmd.SubscriptionCost, 1e-12)
	require.Greater(t, usageRepo.lastLog.TotalCost, 0.0, "native account cost must remain unchanged for subscription usage")
}

func TestGatewayServiceRecordUsage_WokeyActualBillingFailureKeepsRouteCardFallback(t *testing.T) {
	lookup := &wokeyActualBillingLookupStub{err: errors.New("wokey_actual_record_not_visible")}
	svc, usageRepo, billingRepo, apiKey, account, ctx := newGatewayWokeyActualBillingRecordUsageTest(t, UnifiedGatewayWokeySource, 1, 1, lookup)
	err := svc.RecordUsage(ctx, &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:       "local-fallback-request",
			Model:           "wokey-test-model",
			Usage:           ClaudeUsage{OutputTokens: 13},
			UpstreamHeaders: http.Header{"X-Wokey-Request-Id": []string{"wokey-missing-id"}},
		},
		APIKey:  apiKey,
		User:    &User{ID: 2002},
		Account: account,
	})
	require.NoError(t, err, "upstream billing lookup failure must not fail the inference request")
	require.Equal(t, 1, lookup.calls)
	require.InDelta(t, 39.0/1_000_000, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, usageRepo.lastLog.ActualCost, billingRepo.lastCmd.BalanceCost, 1e-12)
}

func TestGatewayServiceRecordUsage_DoesNotUseWokeyActualBillingOutsideWokeyTokenCard(t *testing.T) {
	for _, tc := range []struct {
		name    string
		source  string
		baseURL string
	}{
		{name: "non Wokey provider", source: UnifiedGatewayWokeySource, baseURL: "https://other.example"},
		{name: "non Wokey card", source: "manual", baseURL: "https://api.wokey.ai"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookup := &wokeyActualBillingLookupStub{amount: decimal.RequireFromString("0.000054")}
			svc, _, _, apiKey, account, ctx := newGatewayWokeyActualBillingRecordUsageTest(t, tc.source, 1, 1, lookup)
			account.Credentials["base_url"] = tc.baseURL
			err := svc.RecordUsage(ctx, &RecordUsageInput{
				Result: &ForwardResult{
					RequestID:       "local-unrelated-request",
					Model:           "wokey-test-model",
					Usage:           ClaudeUsage{OutputTokens: 13},
					UpstreamHeaders: http.Header{"X-Wokey-Request-Id": []string{"wokey-unrelated"}},
				},
				APIKey:  apiKey,
				User:    &User{ID: 2003},
				Account: account,
			})
			require.NoError(t, err)
			require.Zero(t, lookup.calls)
		})
	}
}
