package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type wokeyActualRoundTripFunc func(*http.Request) (*http.Response, error)

func (f wokeyActualRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type wokeyActualLookupFunc func(context.Context, *Account, string) (decimal.Decimal, error)

func (f wokeyActualLookupFunc) LookupActualAmount(ctx context.Context, account *Account, requestID string) (decimal.Decimal, error) {
	return f(ctx, account, requestID)
}

func wokeyActualHTTPResponse(req *http.Request, status int, body string, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func wokeyActualTestAccount() *Account {
	return &Account{
		ID:          28,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "wokey-test-secret", "base_url": "https://api.wokey.ai/v1"},
	}
}

func TestWokeyActualBillingClient_UsesFixedReadOnlyEndpointAndExactID(t *testing.T) {
	const apiKey = "wokey-test-secret"
	calls := 0
	client := &unifiedGatewayWokeyActualBillingClient{client: &http.Client{
		Transport: wokeyActualRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			require.Equal(t, http.MethodGet, req.Method)
			require.Equal(t, wokeyActualBillingRequestURL, req.URL.String())
			require.Equal(t, "Bearer "+apiKey, req.Header.Get("Authorization"))
			require.NotContains(t, req.URL.String(), apiKey)
			return wokeyActualHTTPResponse(req, http.StatusOK, `{"data":[{"id":"other","status":"succeeded","usageSource":"upstream","actualAmount":9},{"id":"wanted","status":"succeeded","usageSource":"upstream","actualAmount":0.000054}]}`, nil), nil
		}),
	}}
	amount, err := client.LookupActualAmount(context.Background(), wokeyActualTestAccount(), "wanted")
	require.NoError(t, err)
	require.Equal(t, decimal.RequireFromString("0.000054"), amount)
	require.Equal(t, 1, calls)
}

func TestWokeyActualBillingClient_RetriesOnlyPendingExactRecord(t *testing.T) {
	calls := 0
	client := &unifiedGatewayWokeyActualBillingClient{client: &http.Client{
		Transport: wokeyActualRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			body := `{"data":[]}`
			if calls == 2 {
				body = `{"data":[{"id":"wanted","status":"processing","usageSource":"upstream","actualAmount":0}]}`
			}
			if calls == 3 {
				body = `{"data":[{"id":"wanted","status":"succeeded","usageSource":"upstream","actualAmount":"0.000054"}]}`
			}
			return wokeyActualHTTPResponse(req, http.StatusOK, body, nil), nil
		}),
	}}
	amount, err := client.LookupActualAmount(context.Background(), wokeyActualTestAccount(), "wanted")
	require.NoError(t, err)
	require.Equal(t, decimal.RequireFromString("0.000054"), amount)
	require.Equal(t, wokeyActualBillingMaxAttempts, calls)
}

func TestWokeyActualBillingClient_RejectsRedirectWithoutFollowing(t *testing.T) {
	calls := 0
	client := newUnifiedGatewayWokeyActualBillingClient()
	client.client.Transport = wokeyActualRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		headers := make(http.Header)
		headers.Set("Location", "https://evil.example/collect")
		return wokeyActualHTTPResponse(req, http.StatusFound, "", headers), nil
	})
	_, err := client.LookupActualAmount(context.Background(), wokeyActualTestAccount(), "wanted")
	require.EqualError(t, err, "wokey_actual_api_status")
	require.Equal(t, 1, calls)
}

func TestWokeyActualBillingClient_MapsFailuresToSanitizedCodes(t *testing.T) {
	const apiKey = "wokey-test-secret"
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "api status", status: http.StatusBadGateway, body: `secret body`, want: "wokey_actual_api_status"},
		{name: "invalid json", status: http.StatusOK, body: `{`, want: "wokey_actual_response_invalid"},
		{name: "oversized", status: http.StatusOK, body: strings.Repeat("x", wokeyActualBillingBodyLimit+1), want: "wokey_actual_response_too_large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &unifiedGatewayWokeyActualBillingClient{client: &http.Client{
				Transport: wokeyActualRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					return wokeyActualHTTPResponse(req, tt.status, tt.body, nil), nil
				}),
			}}
			_, err := client.LookupActualAmount(context.Background(), wokeyActualTestAccount(), "wanted")
			require.EqualError(t, err, tt.want)
			require.NotContains(t, err.Error(), apiKey)
		})
	}
}

func TestWokeyActualRecordAmountRequiresOneSuccessfulUpstreamRecord(t *testing.T) {
	valid := wokeyActualRequestRecord{ID: "wanted", Status: "succeeded", UsageSource: "upstream", ActualAmount: []byte(`0.000054`)}
	amount, err := wokeyActualRecordAmount([]wokeyActualRequestRecord{valid}, "wanted")
	require.NoError(t, err)
	require.Equal(t, decimal.RequireFromString("0.000054"), amount)

	tests := []struct {
		name    string
		records []wokeyActualRequestRecord
		want    string
	}{
		{name: "not visible", records: nil, want: "wokey_actual_record_not_visible"},
		{name: "duplicate id", records: []wokeyActualRequestRecord{valid, valid}, want: "wokey_actual_record_duplicate"},
		{name: "failed request", records: []wokeyActualRequestRecord{{ID: "wanted", Status: "failed", UsageSource: "upstream", ActualAmount: []byte(`0`)}}, want: "wokey_actual_record_not_successful"},
		{name: "non-upstream amount", records: []wokeyActualRequestRecord{{ID: "wanted", Status: "succeeded", UsageSource: "estimated", ActualAmount: []byte(`0.1`)}}, want: "wokey_actual_usage_source_invalid"},
		{name: "negative amount", records: []wokeyActualRequestRecord{{ID: "wanted", Status: "succeeded", UsageSource: "upstream", ActualAmount: []byte(`-0.1`)}}, want: "wokey_actual_amount_invalid"},
		{name: "missing amount", records: []wokeyActualRequestRecord{{ID: "wanted", Status: "succeeded", UsageSource: "upstream"}}, want: "wokey_actual_amount_invalid"},
		{name: "only similar id", records: []wokeyActualRequestRecord{{ID: "another", Status: "succeeded", UsageSource: "upstream", ActualAmount: []byte(`0.1`)}}, want: "wokey_actual_record_not_visible"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := wokeyActualRecordAmount(tt.records, "wanted")
			require.EqualError(t, err, tt.want)
		})
	}
	_, err = wokeyActualRecordAmount([]wokeyActualRequestRecord{{ID: "wanted", Status: "processing"}}, "wanted")
	require.ErrorIs(t, err, errWokeyActualRecordNotReady)
}

func TestCalculateWokeyActualUserCost_UsesFXAndBothMultipliersOnce(t *testing.T) {
	amount := decimal.RequireFromString("0.000054")
	cost, err := calculateWokeyActualUserCost(amount, "6.9", 1.1, 1.2)
	require.NoError(t, err)
	require.InDelta(t, 0.00049183, cost, 1e-12, "native balance and quota settlement quantizes to 8 decimals")

	base, err := calculateWokeyActualUserCost(amount, "6.9", 1, 1)
	require.NoError(t, err)
	require.InDelta(t, 0.0003726, base, 1e-12)
	for _, tc := range []struct {
		fx    string
		line  float64
		group float64
	}{
		{fx: "0", line: 1, group: 1},
		{fx: "invalid", line: 1, group: 1},
		{fx: "6.9", line: -1, group: 1},
		{fx: "6.9", line: 1e308, group: 1e308},
	} {
		_, err := calculateWokeyActualUserCost(amount, tc.fx, tc.line, tc.group)
		require.Error(t, err)
	}
}

func TestWokeyExactBillStillOverridesDynamicCardWhenPricingAtIsMissing(t *testing.T) {
	entry := wokeyTimeOfDayEntryForTest(t, "deepseek-flash", "6.9")
	decision := UnifiedGatewayRoutePricingDecision{
		Allowed: true, GroupID: 7, Model: entry.Model, Kind: UnifiedGatewayRoutePricingToken,
		Entries: []UnifiedGatewayRoutePricingEntry{entry},
	}
	ctx := WithUnifiedGatewayRoutePricingDecision(context.Background(), decision)
	apiKey := &APIKey{GroupID: routePricingInt64Ptr(7)}
	cost := &CostBreakdown{TotalCost: 0.0004, ActualCost: 0.0003}
	ApplyUnifiedGatewayRoutePricingWithTokenUsage(ctx, apiKey, entry.AccountID, entry.Model, "", "", nil, 0, 0, "", 0, cost, 1, 1,
		UnifiedGatewayRouteTokenUsage{Tokens: UsageTokens{InputTokens: 50}, RateMultiplier: 1, Eligible: true})
	require.Equal(t, 0.0003, cost.ActualCost, "without original PricingAt the local dynamic formula must not run")
	require.False(t, cost.routePricingApplied)

	lookup := wokeyActualLookupFunc(func(_ context.Context, _ *Account, requestID string) (decimal.Decimal, error) {
		require.Equal(t, "wokey-request-7", requestID)
		return decimal.RequireFromString("0.00001"), nil
	})
	headers := http.Header{"X-Wokey-Request-Id": []string{"wokey-request-7"}}
	account := wokeyActualTestAccount()
	account.ID = entry.AccountID
	applyUnifiedGatewayWokeyActualBillingCost(ctx, lookup, "local-request", headers, apiKey, account, entry.Model, cost, true, 1)
	require.InDelta(t, 0.000069, cost.ActualCost, 1e-12)
	require.Equal(t, 0.0004, cost.TotalCost, "exact user actual charge must not rewrite native account cost")
	require.True(t, cost.routePricingApplied)
}

func TestWokeyActualBillingClient_ScopeRejectsNonWokeyAccounts(t *testing.T) {
	calls := 0
	client := &unifiedGatewayWokeyActualBillingClient{client: &http.Client{
		Transport: wokeyActualRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			return wokeyActualHTTPResponse(req, http.StatusOK, `{"data":[]}`, nil), nil
		}),
	}}
	for _, account := range []*Account{
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"base_url": "https://api.wokey.ai"}},
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://not-wokey.example"}},
		{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://api.wokey.ai"}},
	} {
		_, err := client.LookupActualAmount(context.Background(), account, "wanted")
		require.EqualError(t, err, "wokey_actual_scope_invalid")
	}
	_, err := client.LookupActualAmount(context.Background(), &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.wokey.ai"}}, "wanted")
	require.EqualError(t, err, "wokey_api_key_missing")
	require.Zero(t, calls)

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	_, err = client.LookupActualAmount(ctx, wokeyActualTestAccount(), "wanted")
	require.Error(t, err)
}
