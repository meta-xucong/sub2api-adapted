package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

const yeTokenActualBillingURL = "https://yetoken.vip/api/log/token"
const yeTokenActualBillingTimeout = 3 * time.Second

type unifiedGatewayYeTokenActualBillingLookup interface {
	LookupActualAmount(context.Context, *Account, string) (decimal.Decimal, error)
}

type unifiedGatewayYeTokenActualBillingClient struct {
	client *http.Client
}

func newUnifiedGatewayYeTokenActualBillingClient() *unifiedGatewayYeTokenActualBillingClient {
	return &unifiedGatewayYeTokenActualBillingClient{client: &http.Client{
		Timeout: yeTokenActualBillingTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func isYeTokenAPIKeyAccount(account *Account) bool {
	if account == nil || account.Type != AccountTypeAPIKey {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(account.GetCredential("base_url")))
	return err == nil && (strings.EqualFold(u.Hostname(), "yetoken.vip") || strings.EqualFold(u.Hostname(), "www.yetoken.vip"))
}

func (c *unifiedGatewayYeTokenActualBillingClient) LookupActualAmount(ctx context.Context, account *Account, requestID string) (decimal.Decimal, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, yeTokenActualBillingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(lookupCtx, http.MethodGet, yeTokenActualBillingURL, nil)
	if err != nil {
		return decimal.Zero, errors.New("yetoken_actual_request_invalid")
	}
	req.Header.Set("Authorization", "Bearer "+account.GetCredential("api_key"))
	req.Header.Set("Accept", "application/json")
	// The default transport negotiates and decodes gzip. Do not set
	// Accept-Encoding manually or download the entire historical page.
	resp, err := c.client.Do(req)
	if err != nil {
		return decimal.Zero, errors.New("yetoken_actual_api_unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decimal.Zero, errors.New("yetoken_actual_api_status")
	}
	return readYeTokenActualAmount(io.LimitReader(resp.Body, 4<<20), requestID)
}

// YeToken ignores request_id filters and returns the latest 1000 records.
// Decode only through the exact consumption record, then close the body.
func readYeTokenActualAmount(body io.Reader, requestID string) (decimal.Decimal, error) {
	d := json.NewDecoder(body)
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return decimal.Zero, errors.New("yetoken_actual_response_invalid")
	}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return decimal.Zero, errors.New("yetoken_actual_response_invalid")
		}
		if key != "data" {
			var ignored json.RawMessage
			if err := d.Decode(&ignored); err != nil {
				return decimal.Zero, errors.New("yetoken_actual_response_invalid")
			}
			continue
		}
		if token, err := d.Token(); err != nil || token != json.Delim('[') {
			return decimal.Zero, errors.New("yetoken_actual_response_invalid")
		}
		for d.More() {
			var record struct {
				RequestID string `json:"request_id"`
				Type      int    `json:"type"`
				Quota     *int64 `json:"quota"`
			}
			if err := d.Decode(&record); err != nil {
				return decimal.Zero, errors.New("yetoken_actual_response_invalid")
			}
			if record.RequestID == requestID && record.Type == 2 {
				if record.Quota == nil || *record.Quota < 0 {
					return decimal.Zero, errors.New("yetoken_actual_quota_missing")
				}
				return decimal.NewFromInt(*record.Quota).Div(decimal.NewFromInt(500000)), nil
			}
		}
		return decimal.Zero, errors.New("yetoken_actual_record_not_visible")
	}
	return decimal.Zero, errors.New("yetoken_actual_record_not_visible")
}

func applyUnifiedGatewayYeTokenActualBillingCost(ctx context.Context, lookup unifiedGatewayYeTokenActualBillingLookup, requestID string, headers http.Header, apiKey *APIKey, account *Account, billingModel string, cost *CostBreakdown, tokenPricingEligible bool, groupMultiplier float64) {
	if lookup == nil || cost == nil || !tokenPricingEligible || !isYeTokenAPIKeyAccount(account) {
		return
	}
	if !strings.HasPrefix(billingModel, "gpt-") && !strings.HasPrefix(billingModel, "claude-sonnet-") {
		return
	}
	decision, ok := UnifiedGatewayRoutePricingDecisionFromContext(ctx)
	if !ok || !decision.Allowed || decision.Kind != UnifiedGatewayRoutePricingToken || apiKey == nil || apiKey.GroupID == nil ||
		*apiKey.GroupID != decision.GroupID || decision.Model != billingModel {
		return
	}
	matched := false
	for _, entry := range decision.Entries {
		if entry.AccountID == account.ID && entry.Model == billingModel && entry.Kind == UnifiedGatewayRoutePricingToken {
			matched = true
			break
		}
	}
	if !matched {
		return
	}
	warn := func(code string) {
		logger.L().With(zap.String("component", "service.gateway"), zap.Int64("account_id", account.ID), zap.String("request_id", requestID), zap.String("code", code)).Warn("yetoken_actual_billing.unverified_existing_price")
	}
	upstreamID := UpstreamRequestIDFromHeaders(account, headers)
	if upstreamID == "" {
		warn("upstream_request_id_missing")
		return
	}
	amount, err := lookup.LookupActualAmount(ctx, account, upstreamID)
	if err != nil {
		warn(err.Error())
		return
	}
	// quota already includes YeToken's purchase/line discount and tool charges.
	// Only apply the request's user/group multiplier, once; no extra line/FX.
	actualCost, _ := amount.Mul(decimal.NewFromFloat(groupMultiplier)).Float64()
	cost.ActualCost = QuantizeUsageBillingAmount(actualCost)
	cost.routePricingApplied = true
}
