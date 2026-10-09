package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

const (
	wokeyActualBillingRequestURL  = UnifiedGatewayWokeyBaseURL + "/v1/requests?page=1"
	wokeyActualBillingTimeout     = 3 * time.Second
	wokeyActualBillingHTTPTimeout = 2 * time.Second
	wokeyActualBillingRetryDelay  = 150 * time.Millisecond
	wokeyActualBillingMaxAttempts = 3
	wokeyActualBillingBodyLimit   = 1 << 20
)

var (
	errWokeyActualRecordNotVisible = errors.New("wokey_actual_record_not_visible")
	errWokeyActualRecordNotReady   = errors.New("wokey_actual_record_not_ready")
)

type unifiedGatewayWokeyActualBillingLookup interface {
	LookupActualAmount(context.Context, *Account, string) (decimal.Decimal, error)
}

type unifiedGatewayWokeyActualBillingClient struct {
	client *http.Client
}

type wokeyActualRequestPage struct {
	Data []wokeyActualRequestRecord `json:"data"`
}

type wokeyActualRequestRecord struct {
	ID           string          `json:"id"`
	Status       string          `json:"status"`
	UsageSource  string          `json:"usageSource"`
	ActualAmount json.RawMessage `json:"actualAmount"`
}

func newUnifiedGatewayWokeyActualBillingClient() *unifiedGatewayWokeyActualBillingClient {
	return &unifiedGatewayWokeyActualBillingClient{
		client: &http.Client{
			Timeout: wokeyActualBillingHTTPTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func isWokeyAPIKeyAccount(account *Account) bool {
	if account == nil || account.Type != AccountTypeAPIKey {
		return false
	}
	var baseURL string
	switch account.Platform {
	case PlatformOpenAI:
		baseURL = account.GetOpenAIBaseURL()
	case PlatformGrok:
		baseURL = account.GetGrokBaseURL()
	default:
		return false
	}
	return isWokeyBaseURL(baseURL)
}

func (c *unifiedGatewayWokeyActualBillingClient) LookupActualAmount(ctx context.Context, account *Account, requestID string) (decimal.Decimal, error) {
	if c == nil || c.client == nil || !isWokeyAPIKeyAccount(account) || strings.TrimSpace(requestID) == "" {
		return decimal.Zero, errors.New("wokey_actual_scope_invalid")
	}
	apiKey := strings.TrimSpace(account.GetCredential("api_key"))
	if apiKey == "" {
		return decimal.Zero, errors.New("wokey_api_key_missing")
	}

	lookupCtx, cancel := context.WithTimeout(ctx, wokeyActualBillingTimeout)
	defer cancel()
	for attempt := 0; attempt < wokeyActualBillingMaxAttempts; attempt++ {
		amount, err := c.lookupOnce(lookupCtx, apiKey, requestID)
		if err == nil {
			return amount, nil
		}
		if !errors.Is(err, errWokeyActualRecordNotVisible) && !errors.Is(err, errWokeyActualRecordNotReady) {
			return decimal.Zero, err
		}
		if attempt+1 == wokeyActualBillingMaxAttempts {
			return decimal.Zero, err
		}
		timer := time.NewTimer(wokeyActualBillingRetryDelay)
		select {
		case <-lookupCtx.Done():
			timer.Stop()
			return decimal.Zero, errors.New("wokey_actual_lookup_timeout")
		case <-timer.C:
		}
	}
	return decimal.Zero, errors.New("wokey_actual_record_not_visible")
}

func (c *unifiedGatewayWokeyActualBillingClient) lookupOnce(ctx context.Context, apiKey, requestID string) (decimal.Decimal, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wokeyActualBillingRequestURL, nil)
	if err != nil {
		return decimal.Zero, errors.New("wokey_actual_request_invalid")
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return decimal.Zero, errors.New("wokey_actual_lookup_timeout")
		}
		return decimal.Zero, errors.New("wokey_actual_api_unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decimal.Zero, errors.New("wokey_actual_api_status")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, wokeyActualBillingBodyLimit+1))
	if err != nil {
		return decimal.Zero, errors.New("wokey_actual_response_read_failed")
	}
	if len(body) > wokeyActualBillingBodyLimit {
		return decimal.Zero, errors.New("wokey_actual_response_too_large")
	}
	var page wokeyActualRequestPage
	if err := json.Unmarshal(body, &page); err != nil {
		return decimal.Zero, errors.New("wokey_actual_response_invalid")
	}
	return wokeyActualRecordAmount(page.Data, requestID)
}

func wokeyActualRecordAmount(records []wokeyActualRequestRecord, requestID string) (decimal.Decimal, error) {
	var matched *wokeyActualRequestRecord
	for i := range records {
		if records[i].ID != requestID {
			continue
		}
		if matched != nil {
			return decimal.Zero, errors.New("wokey_actual_record_duplicate")
		}
		matched = &records[i]
	}
	if matched == nil {
		return decimal.Zero, errWokeyActualRecordNotVisible
	}
	if !strings.EqualFold(strings.TrimSpace(matched.Status), "succeeded") {
		status := strings.ToLower(strings.TrimSpace(matched.Status))
		if status == "pending" || status == "processing" || status == "in_progress" || status == "queued" {
			return decimal.Zero, errWokeyActualRecordNotReady
		}
		return decimal.Zero, errors.New("wokey_actual_record_not_successful")
	}
	if !strings.EqualFold(strings.TrimSpace(matched.UsageSource), "upstream") {
		return decimal.Zero, errors.New("wokey_actual_usage_source_invalid")
	}
	amount, err := parseWokeyActualAmount(matched.ActualAmount)
	if err != nil || amount.IsNegative() {
		return decimal.Zero, errors.New("wokey_actual_amount_invalid")
	}
	return amount, nil
}

func parseWokeyActualAmount(raw json.RawMessage) (decimal.Decimal, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return decimal.Zero, errors.New("wokey_actual_amount_missing")
	}
	value := strings.TrimSpace(string(raw))
	if strings.HasPrefix(value, `"`) {
		var decoded string
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return decimal.Zero, err
		}
		value = strings.TrimSpace(decoded)
	}
	return decimal.NewFromString(value)
}

func calculateWokeyActualUserCost(amount decimal.Decimal, sourceFX string, lineMultiplier, groupMultiplier float64) (float64, error) {
	fx, err := decimal.NewFromString(strings.TrimSpace(sourceFX))
	if err != nil || !fx.IsPositive() || !finiteNonNegative(lineMultiplier) || !finiteNonNegative(groupMultiplier) {
		return 0, errors.New("wokey_actual_pricing_invalid")
	}
	line := decimal.NewFromFloat(lineMultiplier)
	group := decimal.NewFromFloat(groupMultiplier)
	cost := amount.Mul(fx).Mul(line).Mul(group)
	result, _ := cost.Float64()
	if !finiteNonNegative(result) || math.IsInf(result, 0) || math.IsNaN(result) {
		return 0, errors.New("wokey_actual_cost_out_of_range")
	}
	// Align the user-facing usage row with the native billing command, which
	// quantizes balance and quota amounts to NUMERIC(20,8) before settlement.
	return QuantizeUsageBillingAmount(result), nil
}

func findWokeyTokenPriceEntry(decision UnifiedGatewayRoutePricingDecision, apiKey *APIKey, accountID int64, billingModel string) (*UnifiedGatewayRoutePricingEntry, bool) {
	// The route decision is only issued after composite-group validation. Its exact
	// group ID, model, and account scope are authoritative here; rechecking the
	// API-key Group.Platform snapshot can reject a valid decision carried into
	// asynchronous usage recording.
	if !decision.Allowed || decision.Kind != UnifiedGatewayRoutePricingToken ||
		apiKey == nil ||
		apiKey.GroupID == nil || *apiKey.GroupID != decision.GroupID ||
		billingModel == "" || decision.Model != billingModel || accountID <= 0 {
		return nil, false
	}
	var matched *UnifiedGatewayRoutePricingEntry
	for i := range decision.Entries {
		entry := &decision.Entries[i]
		if entry.AccountID != accountID || entry.Model != billingModel || entry.Kind != UnifiedGatewayRoutePricingToken {
			continue
		}
		if matched != nil {
			return nil, false
		}
		matched = entry
	}
	if matched == nil || matched.Source != UnifiedGatewayWokeySource || (matched.TokenBasePrice == nil && matched.TimeOfDayTokenPrice == nil) {
		return nil, false
	}
	return matched, true
}

func applyUnifiedGatewayWokeyActualBillingCost(
	ctx context.Context,
	lookup unifiedGatewayWokeyActualBillingLookup,
	requestID string,
	upstreamHeaders http.Header,
	apiKey *APIKey,
	account *Account,
	billingModel string,
	cost *CostBreakdown,
	tokenPricingEligible bool,
	groupMultiplier float64,
) {
	if lookup == nil || cost == nil || account == nil ||
		!isWokeyAPIKeyAccount(account) || !tokenPricingEligible {
		return
	}
	decision, ok := UnifiedGatewayRoutePricingDecisionFromContext(ctx)
	if !ok {
		return
	}
	entry, matched := findWokeyTokenPriceEntry(decision, apiKey, account.ID, billingModel)
	if !matched {
		return
	}
	requestID = strings.TrimSpace(requestID)
	warnUnverified := func(code string) {
		logger.L().With(
			zap.String("component", "service.gateway"),
			zap.Int64("account_id", account.ID),
			zap.String("request_id", requestID),
			zap.String("code", code),
		).Warn("wokey_actual_billing.unverified_card_fallback")
	}
	lineMultiplier := 1.0
	if entry.Multiplier != nil {
		lineMultiplier = *entry.Multiplier
	}
	upstreamID := UpstreamRequestIDFromHeaders(account, upstreamHeaders)
	if upstreamID == "" {
		warnUnverified("upstream_request_id_missing")
		return
	}
	amount, err := lookup.LookupActualAmount(ctx, account, upstreamID)
	if err != nil {
		warnUnverified(err.Error())
		return
	}
	actualCost, err := calculateWokeyActualUserCost(amount, entry.SourceFX, lineMultiplier, groupMultiplier)
	if err != nil {
		warnUnverified(err.Error())
		return
	}
	cost.ActualCost = actualCost
	// This remains a configured route-card charge even if the native TotalCost is zero.
	cost.routePricingApplied = true
}
