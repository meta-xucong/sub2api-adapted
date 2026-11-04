package service

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func yeTokenAccountForTest() *Account {
	return &Account{ID: 21, Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://yetoken.vip", "api_key": "test-secret"},
		Extra:       map[string]any{AccountExtraUpstreamRequestIDHeader: "x-oneapi-request-id"}}
}

func TestYeTokenActualBillingClientReadsGzipExactRecordAndClosesEarly(t *testing.T) {
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/log/token" || r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("unexpected lookup request")
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		_, _ = io.WriteString(gz, `{"data":[{"request_id":"wanted","type":1,"quota":999},{"request_id":"other","type":2,"quota":888},{"request_id":"wanted","type":2,"quota":46}`)
		_ = gz.Flush()
		w.(http.Flusher).Flush()
		// The rest of the historical page never arrives. Finding the exact
		// record must finish the lookup and close this request immediately.
		<-r.Context().Done()
		close(closed)
	}))
	defer server.Close()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := newUnifiedGatewayYeTokenActualBillingClient()
	client.client.Transport = wokeyActualRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, yeTokenActualBillingURL, req.URL.String())
		require.Empty(t, req.Header.Get("Accept-Encoding"))
		local := req.Clone(req.Context())
		local.URL.Scheme = "http"
		local.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return transport.RoundTrip(local)
	})
	amount, err := client.LookupActualAmount(context.Background(), yeTokenAccountForTest(), "wanted")
	require.NoError(t, err)
	require.True(t, amount.Equal(decimal.RequireFromString("0.000092")))
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("lookup kept the historical response open")
	}
}

func TestYeTokenActualBillingClientTimeoutDoesNotCancelParent(t *testing.T) {
	client := newUnifiedGatewayYeTokenActualBillingClient()
	client.client.Transport = wokeyActualRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), yeTokenActualBillingTimeout)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := client.LookupActualAmount(parent, yeTokenAccountForTest(), "wanted")
	require.EqualError(t, err, "yetoken_actual_api_unavailable")
	require.NoError(t, parent.Err())
}

func TestYeTokenActualBillingRequiresExactConsumptionRecord(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"different ID despite same model", `{"data":[{"request_id":"other","type":2,"quota":46,"model_name":"claude-sonnet-5-5"}]}`, "yetoken_actual_record_not_visible"},
		{"not consumption", `{"data":[{"request_id":"wanted","type":1,"quota":46}]}`, "yetoken_actual_record_not_visible"},
		{"missing quota", `{"data":[{"request_id":"wanted","type":2}]}`, "yetoken_actual_quota_missing"},
		{"invalid response", `{"data":null}`, "yetoken_actual_response_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readYeTokenActualAmount(strings.NewReader(tc.body), "wanted")
			require.EqualError(t, err, tc.want)
		})
	}
}

func TestYeTokenActualBillingScopeAndMissingBillKeepExistingCost(t *testing.T) {
	for _, name := range []string{"exact", "missing header", "missing record", "timeout", "Wokey", "Kimi", "Opus", "ineligible", "no route"} {
		t.Run(name, func(t *testing.T) {
			account := yeTokenAccountForTest()
			model := "claude-sonnet-5-5"
			eligible := true
			headers := http.Header{"X-Oneapi-Request-Id": []string{"wanted"}}
			lookup := &wokeyActualBillingLookupStub{amount: decimal.RequireFromString("0.000092")}
			switch name {
			case "missing header":
				headers = nil
			case "missing record":
				lookup.err = errors.New("yetoken_actual_record_not_visible")
			case "timeout":
				lookup.err = context.DeadlineExceeded
			case "Wokey":
				account.Credentials["base_url"] = "https://api.wokey.ai"
			case "Kimi":
				model = "kimi-k3"
			case "Opus":
				model = "claude-opus-5-5"
			case "ineligible":
				eligible = false
			}
			decision := wokeyRouteDecisionForTest(16, account.ID, model, "manual", 0.15)
			ctx := WithUnifiedGatewayRoutePricingDecision(context.Background(), decision)
			if name == "no route" {
				ctx = context.Background()
			}
			cost := &CostBreakdown{TotalCost: 0.01, ActualCost: 0.02}
			applyUnifiedGatewayYeTokenActualBillingCost(ctx, lookup, "local", headers, &APIKey{GroupID: routePricingInt64Ptr(16)}, account, model, cost, eligible, 1.25)
			require.Equal(t, 0.01, cost.TotalCost)
			if name == "exact" {
				require.InDelta(t, 0.000115, cost.ActualCost, 1e-12)
				require.True(t, cost.routePricingApplied)
				require.Equal(t, "wanted", lookup.requestID)
			} else {
				require.Equal(t, 0.02, cost.ActualCost)
				require.False(t, cost.routePricingApplied)
				if name != "missing record" && name != "timeout" {
					require.Zero(t, lookup.calls)
				}
			}
		})
	}
}

func TestYeTokenActualBillingQuotaIncludesSearchFeeOnce(t *testing.T) {
	// Exact historical GPT bill: quota includes token usage and three searches.
	amount, err := readYeTokenActualAmount(strings.NewReader(fmt.Sprintf(`{"data":[{"request_id":"gpt","type":2,"quota":%d}]}`, 5600)), "gpt")
	require.NoError(t, err)
	lookup := &wokeyActualBillingLookupStub{amount: amount}
	account := yeTokenAccountForTest()
	ctx := WithUnifiedGatewayRoutePricingDecision(context.Background(), wokeyRouteDecisionForTest(16, account.ID, "gpt-6-sol", "manual", 0.15))
	cost := &CostBreakdown{TotalCost: 0.05, ActualCost: 0.0083748375}
	applyUnifiedGatewayYeTokenActualBillingCost(ctx, lookup, "local", http.Header{"X-Oneapi-Request-Id": []string{"gpt"}}, &APIKey{GroupID: routePricingInt64Ptr(16)}, account, "gpt-6-sol", cost, true, 1.25)
	require.InDelta(t, 0.014, cost.ActualCost, 1e-12)
	require.Equal(t, 0.05, cost.TotalCost)
}
