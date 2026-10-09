package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/shopspring/decimal"
)

const (
	UnifiedGatewayWokeySource       = "wokey_catalog"
	UnifiedGatewayWokeyBaseURL      = "https://api.wokey.ai"
	UnifiedGatewayWokeyDefaultFX    = "6.9"
	UnifiedGatewayWokeyDefaultEvery = 15
	UnifiedGatewayWokeyMinInterval  = 5
	UnifiedGatewayWokeyMaxInterval  = 60
	UnifiedGatewayWokeyStaleAfter   = 60 * time.Minute
	wokeyCatalogTimeout             = 10 * time.Second
	wokeyCatalogBodyLimit           = 2 << 20
	wokeyPricePrecision             = 9
)

var (
	ErrUnifiedGatewayWokeySyncDisabled = infraerrors.BadRequest("WOKEY_PRICE_SYNC_DISABLED", "Wokey price sync is disabled or has no selected accounts")
	ErrUnifiedGatewayWokeySyncBusy     = infraerrors.Conflict("WOKEY_PRICE_SYNC_BUSY", "Wokey price sync is already running")
	ErrUnifiedGatewayWokeySyncScope    = infraerrors.BadRequest("WOKEY_PRICE_SYNC_SCOPE_INVALID", "Wokey price sync has no eligible selected account")
)

type UnifiedGatewayWokeySourceSKU struct {
	SKUID    string `json:"sku_id"`
	Meter    string `json:"meter"`
	Unit     string `json:"unit"`
	Quantity string `json:"quantity"`
	PriceUSD string `json:"price_usd"`
}

type wokeyPriceSyncFailure struct {
	Code       string
	HTTPStatus int
}

func (e *wokeyPriceSyncFailure) Error() string { return e.Code }

type wokeyCatalogFetcher interface {
	Fetch(context.Context) (*wokeyCatalog, error)
}

type wokeyHTTPFetcher struct {
	client  *http.Client
	baseURL string
}

type wokeyPriceSyncWorker struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type wokeyPriceSyncTicker interface {
	Ticks() <-chan time.Time
	Stop()
}

type realWokeyPriceSyncTicker struct {
	ticker *time.Ticker
}

func (t realWokeyPriceSyncTicker) Ticks() <-chan time.Time { return t.ticker.C }
func (t realWokeyPriceSyncTicker) Stop()                   { t.ticker.Stop() }

func (s *SettingService) newWokeyPriceSyncTicker(interval time.Duration) wokeyPriceSyncTicker {
	if s != nil && s.wokeySyncTickerFactory != nil {
		return s.wokeySyncTickerFactory(interval)
	}
	return realWokeyPriceSyncTicker{ticker: time.NewTicker(interval)}
}

type wokeyCatalog struct {
	Models      map[string]wokeyCatalogModel
	Images      map[string]wokeyImageModel
	Videos      map[string]wokeyVideoModel
	Hash        string
	Unsupported map[string]string
}

type wokeyCatalogModel struct {
	ID                   string
	Available            bool
	Currency             string
	PricingMode          string
	HasTimeOfDay         bool
	TimeOfDay            *wokeyCatalogTimeOfDay
	TimeOfDayUnsupported string
	SKUs                 []wokeyCatalogSKU
	PromptTiers          []wokeyPromptTier
}

type wokeyCatalogTimeOfDay struct {
	CurrentTier    string
	PeakWindowsUTC []UnifiedGatewayWokeyTimeOfDayWindow
	Peak           wokeyCatalogTimeOfDayTier
	OffPeak        wokeyCatalogTimeOfDayTier
}

type wokeyCatalogTimeOfDayTier struct {
	PricesUSD map[string]string
}

type wokeyRawTimeOfDay struct {
	CurrentTier       string                     `json:"current_tier"`
	OffPeakMultiplier json.RawMessage            `json:"off_peak_multiplier"`
	PeakWindowsUTC    []wokeyRawTimeOfDayWindow  `json:"peak_windows_utc"`
	Tiers             map[string]json.RawMessage `json:"tiers"`
}

type wokeyRawTimeOfDayWindow struct {
	StartHour *int `json:"start_hour"`
	EndHour   *int `json:"end_hour"`
}

type wokeyRawTimeOfDayTier struct {
	InputPriceUSD               string          `json:"input_price_usd"`
	OutputPriceUSD              string          `json:"output_price_usd"`
	CacheReadPriceUSD           string          `json:"cache_read_price_usd"`
	CacheWritePriceUSD          json.RawMessage `json:"cache_write_price_usd"`
	ReferenceInputPriceUSD      json.RawMessage `json:"reference_input_price_usd"`
	ReferenceOutputPriceUSD     json.RawMessage `json:"reference_output_price_usd"`
	ReferenceCacheReadPriceUSD  json.RawMessage `json:"reference_cache_read_price_usd"`
	ReferenceCacheWritePriceUSD json.RawMessage `json:"reference_cache_write_price_usd"`
}

type wokeyCatalogSKU struct {
	SKUID                  string
	Meter                  string
	Unit                   string
	Quantity               decimal.Decimal
	QuantityRaw            string
	PriceUSD               decimal.Decimal
	PriceRaw               string
	Constraints            wokeySKUConstraints
	ConstraintsUnsupported bool
}

type wokeyPromptTier struct {
	Threshold int
	Prices    map[string]string
}

type wokeySKUConstraints struct {
	Tier               string `json:"tier"`
	Quality            string `json:"quality"`
	Mode               string `json:"mode"`
	Resolution         string `json:"resolution"`
	MinDurationSeconds int    `json:"min_duration_seconds"`
	MaxDurationSeconds int    `json:"max_duration_seconds"`
}

type wokeyImageModel struct {
	ID   string
	SKUs []wokeyImageSKU
}

type wokeyImageSKU struct {
	Tier     string          `json:"tier"`
	PriceUSD wokeyJSONNumber `json:"price_usd_per_image"`
	MaxSide  int             `json:"max_longest_side"`
	Sizes    []string        `json:"sizes"`
}

type wokeyVideoModel struct {
	ID   string
	SKUs []wokeyVideoSKU
}

type wokeyVideoSKU struct {
	VariantID  string          `json:"variant_id"`
	Quality    string          `json:"quality"`
	Mode       string          `json:"mode"`
	Resolution string          `json:"resolution"`
	MinSeconds int             `json:"min_duration_seconds"`
	MaxSeconds int             `json:"max_duration_seconds"`
	PriceUSD   wokeyJSONNumber `json:"price_usd_per_second"`
}

type wokeyJSONNumber string

func (n *wokeyJSONNumber) UnmarshalJSON(raw []byte) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] == '"' || string(trimmed) == "null" {
		return errors.New("expected a JSON number")
	}
	var number json.Number
	if err := json.Unmarshal(trimmed, &number); err != nil || number.String() == "" {
		return errors.New("expected a JSON number")
	}
	*n = wokeyJSONNumber(number.String())
	return nil
}

func (n wokeyJSONNumber) String() string { return string(n) }

type wokeyPriceCard struct {
	Entry       UnifiedGatewayRoutePricingEntry
	Unsupported string
}

type wokeyCardCandidates struct {
	Cards       []UnifiedGatewayRoutePricingEntry
	Unsupported []string
}

type wokeyMergeCounts struct {
	Managed     int
	Conflict    int
	Unsupported int
	NotReturned int
	Reasons     []string
}

func defaultUnifiedGatewayWokeySyncConfig() UnifiedGatewayWokeySyncConfig {
	return UnifiedGatewayWokeySyncConfig{
		AccountIDs:      []int64{},
		FX:              UnifiedGatewayWokeyDefaultFX,
		IntervalMinutes: UnifiedGatewayWokeyDefaultEvery,
		Status:          UnifiedGatewayWokeySyncStatus{Reasons: []string{}},
	}
}

func normalizeUnifiedGatewayWokeySyncConfig(cfg UnifiedGatewayWokeySyncConfig) (UnifiedGatewayWokeySyncConfig, error) {
	if strings.TrimSpace(cfg.FX) == "" {
		cfg.FX = UnifiedGatewayWokeyDefaultFX
	}
	fx, err := decimal.NewFromString(cfg.FX)
	if err != nil || !fx.GreaterThan(decimal.Zero) {
		return cfg, errors.New("Wokey FX must be a positive decimal number")
	}
	cfg.FX = fx.String()
	if cfg.IntervalMinutes == 0 {
		cfg.IntervalMinutes = UnifiedGatewayWokeyDefaultEvery
	}
	if cfg.IntervalMinutes < UnifiedGatewayWokeyMinInterval || cfg.IntervalMinutes > UnifiedGatewayWokeyMaxInterval {
		return cfg, fmt.Errorf("Wokey sync interval must be between %d and %d minutes", UnifiedGatewayWokeyMinInterval, UnifiedGatewayWokeyMaxInterval)
	}
	ids := append([]int64(nil), cfg.AccountIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	unique := ids[:0]
	for _, id := range ids {
		if id <= 0 {
			return cfg, errors.New("Wokey account IDs must be positive")
		}
		if len(unique) == 0 || unique[len(unique)-1] != id {
			unique = append(unique, id)
		}
	}
	if unique == nil {
		unique = []int64{}
	}
	cfg.AccountIDs = unique
	if cfg.Status.Reasons == nil {
		cfg.Status.Reasons = []string{}
	}
	return cfg, nil
}

func validateUnifiedGatewayWokeySyncConfig(cfg UnifiedGatewayWokeySyncConfig) error {
	_, err := normalizeUnifiedGatewayWokeySyncConfig(cfg)
	return err
}

func validateUnifiedGatewayWokeyEntryMetadata(entry UnifiedGatewayRoutePricingEntry) error {
	if entry.Source == "" {
		if entry.SourceFX != "" || entry.SourceFetchedAt != nil || entry.SourceCatalogSHA256 != "" || len(entry.SourceSKUs) != 0 || entry.SyncState != "" || entry.SyncReason != "" || entry.TimeOfDayTokenPrice != nil {
			return errors.New("manual entry cannot carry Wokey source metadata")
		}
		return nil
	}
	if entry.Source != UnifiedGatewayWokeySource || entry.SourceFX == "" || entry.SourceFetchedAt == nil || len(entry.SourceCatalogSHA256) != 64 || len(entry.SourceSKUs) == 0 {
		return errors.New("incomplete Wokey source metadata")
	}
	if _, err := hex.DecodeString(entry.SourceCatalogSHA256); err != nil {
		return errors.New("invalid catalog hash")
	}
	fx, err := decimal.NewFromString(entry.SourceFX)
	if err != nil || !fx.GreaterThan(decimal.Zero) {
		return errors.New("invalid source FX")
	}
	seen := make(map[string]struct{}, len(entry.SourceSKUs))
	for _, sku := range entry.SourceSKUs {
		if sku.SKUID == "" || sku.Meter == "" || sku.Unit == "" || sku.Quantity == "" || sku.PriceUSD == "" {
			return errors.New("incomplete source SKU")
		}
		if _, ok := seen[sku.SKUID]; ok {
			return errors.New("duplicate source SKU")
		}
		seen[sku.SKUID] = struct{}{}
		quantity, qErr := decimal.NewFromString(sku.Quantity)
		price, pErr := decimal.NewFromString(sku.PriceUSD)
		if qErr != nil || !quantity.GreaterThan(decimal.Zero) || !isWokeyIntegerLiteral(sku.Quantity) || pErr != nil || price.IsNegative() {
			return errors.New("invalid source SKU quantity or price")
		}
	}
	if entry.SyncState != "current" && entry.SyncState != "stale" && entry.SyncState != "unsupported" && entry.SyncState != "not_returned" {
		return errors.New("invalid Wokey sync state")
	}
	if entry.TimeOfDayTokenPrice != nil {
		if entry.Kind != UnifiedGatewayRoutePricingToken || !isSupportedWokeyTimeOfDayModel(entry.Model) || entry.TokenBasePrice != nil || entry.LongContextTokenBasePrice != nil {
			return errors.New("Wokey time-of-day price is outside its supported model scope")
		}
		current := entry.TimeOfDayTokenPrice.Peak
		if entry.TimeOfDayTokenPrice.CurrentTier == "off_peak" {
			current = entry.TimeOfDayTokenPrice.OffPeak
		}
		seenMeters := make(map[string]struct{}, len(entry.SourceSKUs))
		for _, source := range entry.SourceSKUs {
			if source.Unit != "token" || source.Quantity != "1000000" || strings.HasPrefix(source.SKUID, "prompt_tier:") || !wokeyKnownTokenMeter(source.Meter) {
				return errors.New("invalid Wokey time-of-day source SKU")
			}
			if _, duplicate := seenMeters[source.Meter]; duplicate {
				return errors.New("duplicate Wokey time-of-day source meter")
			}
			seenMeters[source.Meter] = struct{}{}
			price, exists := current.SourcePricesUSD[source.Meter]
			priceDecimal, priceErr := decimal.NewFromString(price)
			sourceDecimal, sourceErr := decimal.NewFromString(source.PriceUSD)
			if !exists || priceErr != nil || sourceErr != nil || !priceDecimal.Equal(sourceDecimal) {
				return errors.New("Wokey time-of-day source SKU does not match current tier")
			}
		}
		if len(seenMeters) != len(current.SourcePricesUSD) {
			return errors.New("incomplete Wokey time-of-day source SKU snapshot")
		}
		for _, required := range []string{"input_tokens", "output_tokens", "cache_read_tokens"} {
			if _, exists := seenMeters[required]; !exists {
				return errors.New("required Wokey time-of-day source SKU is missing")
			}
		}
	}
	return nil
}

func validateUnifiedGatewayWokeyTimeOfDayTier(tier UnifiedGatewayWokeyTimeOfDayTier, sourceFX string) bool {
	if tier.TokenBasePrice == nil || !tier.TokenBasePrice.validate() || len(tier.SourcePricesUSD) < 3 {
		return false
	}
	fx, err := decimal.NewFromString(sourceFX)
	if err != nil || !fx.GreaterThan(decimal.Zero) {
		return false
	}
	fields := map[string]*float64{
		"input_tokens":       tier.TokenBasePrice.InputPerMillion,
		"output_tokens":      tier.TokenBasePrice.OutputPerMillion,
		"cache_read_tokens":  tier.TokenBasePrice.CacheReadPerMillion,
		"cache_write_tokens": tier.TokenBasePrice.CacheWritePerMillion,
	}
	for meter, raw := range tier.SourcePricesUSD {
		field, known := fields[meter]
		if !known || field == nil || validateWokeyNonNegativeDecimalString(raw) != nil {
			return false
		}
		usd, parseErr := decimal.NewFromString(raw)
		if parseErr != nil {
			return false
		}
		expected, quantizeErr := quantizedWokeyPrice(usd.Mul(fx))
		if quantizeErr != nil || expected != *field {
			return false
		}
	}
	for meter, field := range fields {
		_, exists := tier.SourcePricesUSD[meter]
		if exists != (field != nil) {
			return false
		}
	}
	for _, meter := range []string{"input_tokens", "output_tokens", "cache_read_tokens"} {
		if _, exists := tier.SourcePricesUSD[meter]; !exists {
			return false
		}
	}
	return true
}

func isWokeyBaseURL(raw string) bool {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || !strings.EqualFold(u.Hostname(), "api.wokey.ai") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if port := u.Port(); port != "" && port != "443" {
		return false
	}
	switch u.EscapedPath() {
	case "", "/", "/v1", "/v1/":
		return true
	default:
		return false
	}
}

func newWokeyHTTPFetcher() *wokeyHTTPFetcher {
	return &wokeyHTTPFetcher{
		client: &http.Client{
			Timeout: wokeyCatalogTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		baseURL: UnifiedGatewayWokeyBaseURL,
	}
}

func (f *wokeyHTTPFetcher) Fetch(ctx context.Context) (*wokeyCatalog, error) {
	if f == nil || f.client == nil || f.baseURL == "" {
		return nil, &wokeyPriceSyncFailure{Code: "fetcher_unavailable"}
	}
	ctx, cancel := context.WithTimeout(ctx, wokeyCatalogTimeout)
	defer cancel()
	paths := []string{"/v1/models/pricing", "/v1/images/models", "/v1/videos/models"}
	bodies := make([][]byte, len(paths))
	errs := make([]error, len(paths))
	var wg sync.WaitGroup
	for i, path := range paths {
		wg.Add(1)
		go func(i int, path string) {
			defer wg.Done()
			bodies[i], errs[i] = f.get(ctx, path)
		}(i, path)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return parseWokeyCatalog(bodies[0], bodies[1], bodies[2])
}

func (f *wokeyHTTPFetcher) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(f.baseURL, "/")+path, nil)
	if err != nil {
		return nil, &wokeyPriceSyncFailure{Code: "request_invalid"}
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, &wokeyPriceSyncFailure{Code: "upstream_unavailable"}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &wokeyPriceSyncFailure{Code: "upstream_http_error", HTTPStatus: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, wokeyCatalogBodyLimit+1))
	if err != nil {
		return nil, &wokeyPriceSyncFailure{Code: "upstream_read_error", HTTPStatus: resp.StatusCode}
	}
	if len(body) > wokeyCatalogBodyLimit {
		return nil, &wokeyPriceSyncFailure{Code: "upstream_body_too_large", HTTPStatus: resp.StatusCode}
	}
	return body, nil
}

type wokeyListEnvelope[T any] struct {
	Object string `json:"object"`
	Data   []T    `json:"data"`
}

type wokeyRawPricingModel struct {
	ID          string          `json:"id"`
	Object      string          `json:"object"`
	Available   *bool           `json:"available"`
	Currency    string          `json:"currency"`
	PricingMode string          `json:"pricing_mode"`
	PricingSKUs []wokeyRawSKU   `json:"pricing_skus"`
	PromptTiers []wokeyRawTier  `json:"prompt_tiers"`
	TimeOfDay   json.RawMessage `json:"time_of_day"`
}

type wokeyRawSKU struct {
	SKUID       string          `json:"sku_id"`
	Meter       string          `json:"meter"`
	Unit        string          `json:"unit"`
	Quantity    json.Number     `json:"quantity"`
	PriceUSD    string          `json:"price_usd"`
	Constraints json.RawMessage `json:"constraints"`
}

type wokeyRawTier struct {
	AbovePromptTokens    int    `json:"above_prompt_tokens"`
	InputPriceUSD        string `json:"input_price_usd"`
	OutputPriceUSD       string `json:"output_price_usd"`
	CacheReadPriceUSD    string `json:"cache_read_price_usd"`
	CacheWritePriceUSD   string `json:"cache_write_price_usd"`
	CacheWrite5mPriceUSD string `json:"cache_write_5m_price_usd"`
	CacheWrite1hPriceUSD string `json:"cache_write_1h_price_usd"`
}

type wokeyRawImageModel struct {
	ID          string          `json:"id"`
	PricingSKUs []wokeyImageSKU `json:"pricing_skus"`
}

type wokeyRawVideoModel struct {
	ID          string          `json:"id"`
	PricingSKUs []wokeyVideoSKU `json:"pricing_skus"`
}

func decodeWokeyList[T any](body []byte, out *wokeyListEnvelope[T]) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		return &wokeyPriceSyncFailure{Code: "invalid_json"}
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return &wokeyPriceSyncFailure{Code: "invalid_json"}
	}
	if out.Object != "list" || len(out.Data) == 0 {
		return &wokeyPriceSyncFailure{Code: "invalid_list_envelope"}
	}
	return nil
}

func decodeStrictWokeyJSON(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func parseWokeyTimeOfDay(raw []byte) (*wokeyCatalogTimeOfDay, string) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, "unsupported_time_of_day_schema"
	}
	var parsed wokeyRawTimeOfDay
	if err := decodeStrictWokeyJSON(raw, &parsed); err != nil {
		return nil, "unsupported_time_of_day_schema"
	}
	if parsed.CurrentTier != "peak" && parsed.CurrentTier != "off_peak" {
		return nil, "unsupported_time_of_day_tier"
	}
	if len(parsed.OffPeakMultiplier) > 0 {
		value := strings.TrimSpace(string(parsed.OffPeakMultiplier))
		if value == "" || value[0] == '"' {
			return nil, "invalid_off_peak_multiplier"
		}
		multiplier, err := decimal.NewFromString(value)
		if err != nil || multiplier.IsNegative() {
			return nil, "invalid_off_peak_multiplier"
		}
		floatValue, _ := multiplier.Float64()
		if !finiteNonNegative(floatValue) {
			return nil, "invalid_off_peak_multiplier"
		}
	}
	if len(parsed.Tiers) != 2 {
		return nil, "unsupported_time_of_day_tiers"
	}
	peakRaw, peakOK := parsed.Tiers["peak"]
	offPeakRaw, offPeakOK := parsed.Tiers["off_peak"]
	if !peakOK || !offPeakOK {
		return nil, "unsupported_time_of_day_tiers"
	}
	peak, reason := parseWokeyTimeOfDayTier(peakRaw)
	if reason != "" {
		return nil, reason
	}
	offPeak, reason := parseWokeyTimeOfDayTier(offPeakRaw)
	if reason != "" {
		return nil, reason
	}
	if len(parsed.PeakWindowsUTC) == 0 {
		return nil, "invalid_peak_windows"
	}
	windows := make([]UnifiedGatewayWokeyTimeOfDayWindow, 0, len(parsed.PeakWindowsUTC))
	for _, window := range parsed.PeakWindowsUTC {
		if window.StartHour == nil || window.EndHour == nil || *window.StartHour < 0 || *window.StartHour >= *window.EndHour || *window.EndHour > 24 {
			return nil, "invalid_peak_windows"
		}
		windows = append(windows, UnifiedGatewayWokeyTimeOfDayWindow{StartHour: *window.StartHour, EndHour: *window.EndHour})
	}
	sort.Slice(windows, func(i, j int) bool { return windows[i].StartHour < windows[j].StartHour })
	lastEnd := -1
	for _, window := range windows {
		if window.StartHour < lastEnd {
			return nil, "invalid_peak_windows"
		}
		lastEnd = window.EndHour
	}
	return &wokeyCatalogTimeOfDay{
		CurrentTier: parsed.CurrentTier, PeakWindowsUTC: windows,
		Peak: peak, OffPeak: offPeak,
	}, ""
}

func parseWokeyTimeOfDayTier(raw []byte) (wokeyCatalogTimeOfDayTier, string) {
	var parsed wokeyRawTimeOfDayTier
	if err := decodeStrictWokeyJSON(raw, &parsed); err != nil {
		return wokeyCatalogTimeOfDayTier{}, "unsupported_time_of_day_tier_schema"
	}
	prices := map[string]string{
		"input_tokens":      parsed.InputPriceUSD,
		"output_tokens":     parsed.OutputPriceUSD,
		"cache_read_tokens": parsed.CacheReadPriceUSD,
	}
	for _, meter := range []string{"input_tokens", "output_tokens", "cache_read_tokens"} {
		if err := validateWokeyNonNegativeDecimalString(prices[meter]); err != nil {
			return wokeyCatalogTimeOfDayTier{}, "invalid_time_of_day_price"
		}
	}
	if len(parsed.CacheWritePriceUSD) > 0 {
		value, present, err := parseWokeyOptionalPriceString(parsed.CacheWritePriceUSD, true)
		if err != nil {
			return wokeyCatalogTimeOfDayTier{}, "invalid_time_of_day_price"
		}
		if present {
			prices["cache_write_tokens"] = value
		}
	}
	for _, reference := range []struct {
		raw      json.RawMessage
		allowNil bool
	}{
		{parsed.ReferenceInputPriceUSD, false},
		{parsed.ReferenceOutputPriceUSD, false},
		{parsed.ReferenceCacheReadPriceUSD, false},
		{parsed.ReferenceCacheWritePriceUSD, true},
	} {
		if len(reference.raw) == 0 {
			continue
		}
		value, present, err := parseWokeyOptionalPriceString(reference.raw, reference.allowNil)
		if err != nil || (present && validateWokeyNonNegativeDecimalString(value) != nil) {
			return wokeyCatalogTimeOfDayTier{}, "invalid_time_of_day_reference_price"
		}
	}
	return wokeyCatalogTimeOfDayTier{PricesUSD: prices}, ""
}

func parseWokeyOptionalPriceString(raw json.RawMessage, allowNull bool) (string, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", false, nil
	}
	if string(trimmed) == "null" && allowNull {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(trimmed, &value); err != nil || strings.TrimSpace(value) == "" {
		return "", false, errors.New("expected a non-empty decimal string")
	}
	return value, true, nil
}

func validateWokeyNonNegativeDecimalString(value string) error {
	if strings.TrimSpace(value) != value || value == "" {
		return errors.New("invalid decimal string")
	}
	price, err := decimal.NewFromString(value)
	if err != nil || price.IsNegative() {
		return errors.New("invalid non-negative decimal string")
	}
	return nil
}

func parseWokeyCatalog(pricingBody, imageBody, videoBody []byte) (*wokeyCatalog, error) {
	var pricing wokeyListEnvelope[wokeyRawPricingModel]
	var imageList wokeyListEnvelope[wokeyRawImageModel]
	var videoList wokeyListEnvelope[wokeyRawVideoModel]
	if err := decodeWokeyList(pricingBody, &pricing); err != nil {
		return nil, err
	}
	if err := decodeWokeyList(imageBody, &imageList); err != nil {
		return nil, err
	}
	if err := decodeWokeyList(videoBody, &videoList); err != nil {
		return nil, err
	}
	catalog := &wokeyCatalog{
		Models:      make(map[string]wokeyCatalogModel, len(pricing.Data)),
		Images:      make(map[string]wokeyImageModel, len(imageList.Data)),
		Videos:      make(map[string]wokeyVideoModel, len(videoList.Data)),
		Unsupported: map[string]string{},
	}
	h := sha256.New()
	for _, body := range [][]byte{pricingBody, imageBody, videoBody} {
		_, _ = h.Write(body)
		_, _ = h.Write([]byte{0})
	}
	catalog.Hash = hex.EncodeToString(h.Sum(nil))
	for _, raw := range pricing.Data {
		if raw.ID == "" || raw.Object != "model" || raw.Available == nil || raw.Currency == "" {
			return nil, &wokeyPriceSyncFailure{Code: "invalid_model_schema"}
		}
		if _, exists := catalog.Models[raw.ID]; exists {
			return nil, &wokeyPriceSyncFailure{Code: "duplicate_model_id"}
		}
		if raw.Currency != "USD" {
			return nil, &wokeyPriceSyncFailure{Code: "unsupported_currency"}
		}
		timeOfDayRaw := bytes.TrimSpace(raw.TimeOfDay)
		hasTimeOfDay := len(timeOfDayRaw) > 0
		model := wokeyCatalogModel{ID: raw.ID, Available: *raw.Available, Currency: raw.Currency, PricingMode: raw.PricingMode, HasTimeOfDay: hasTimeOfDay, PromptTiers: make([]wokeyPromptTier, 0, len(raw.PromptTiers))}
		if hasTimeOfDay {
			model.TimeOfDay, model.TimeOfDayUnsupported = parseWokeyTimeOfDay(timeOfDayRaw)
		}
		seenSKU := make(map[string]struct{}, len(raw.PricingSKUs))
		for _, sku := range raw.PricingSKUs {
			if sku.SKUID == "" || sku.Meter == "" || sku.Unit == "" || sku.Quantity == "" || sku.PriceUSD == "" {
				return nil, &wokeyPriceSyncFailure{Code: "invalid_sku_schema"}
			}
			if _, exists := seenSKU[sku.SKUID]; exists {
				return nil, &wokeyPriceSyncFailure{Code: "duplicate_sku_id"}
			}
			seenSKU[sku.SKUID] = struct{}{}
			quantity, err := decimal.NewFromString(sku.Quantity.String())
			if err != nil || !quantity.GreaterThan(decimal.Zero) || !isWokeyIntegerLiteral(sku.Quantity.String()) {
				return nil, &wokeyPriceSyncFailure{Code: "invalid_sku_quantity"}
			}
			price, err := decimal.NewFromString(sku.PriceUSD)
			if err != nil || price.IsNegative() {
				return nil, &wokeyPriceSyncFailure{Code: "invalid_sku_price"}
			}
			var constraints wokeySKUConstraints
			constraintsUnsupported := false
			if len(bytes.TrimSpace(sku.Constraints)) > 0 && !bytes.Equal(bytes.TrimSpace(sku.Constraints), []byte("null")) {
				if err := decodeStrictWokeyJSON(sku.Constraints, &constraints); err != nil {
					constraintsUnsupported = true
					if err := json.Unmarshal(sku.Constraints, &constraints); err != nil {
						constraints = wokeySKUConstraints{}
					}
				}
			}
			model.SKUs = append(model.SKUs, wokeyCatalogSKU{
				SKUID: sku.SKUID, Meter: sku.Meter, Unit: sku.Unit, Quantity: quantity, QuantityRaw: sku.Quantity.String(),
				PriceUSD: price, PriceRaw: sku.PriceUSD, Constraints: constraints, ConstraintsUnsupported: constraintsUnsupported,
			})
		}
		for _, tier := range raw.PromptTiers {
			if tier.AbovePromptTokens <= 0 {
				return nil, &wokeyPriceSyncFailure{Code: "invalid_prompt_tier"}
			}
			prices := map[string]string{
				"input_tokens": tier.InputPriceUSD, "output_tokens": tier.OutputPriceUSD, "cache_read_tokens": tier.CacheReadPriceUSD,
				"cache_write_tokens": tier.CacheWritePriceUSD, "cache_write_5m_tokens": tier.CacheWrite5mPriceUSD, "cache_write_1h_tokens": tier.CacheWrite1hPriceUSD,
			}
			for meter, priceText := range prices {
				if priceText == "" {
					if meter == "input_tokens" || meter == "output_tokens" || meter == "cache_read_tokens" {
						return nil, &wokeyPriceSyncFailure{Code: "invalid_prompt_tier"}
					}
					continue
				}
				price, err := decimal.NewFromString(priceText)
				if err != nil || price.IsNegative() {
					return nil, &wokeyPriceSyncFailure{Code: "invalid_prompt_tier"}
				}
			}
			model.PromptTiers = append(model.PromptTiers, wokeyPromptTier{Threshold: tier.AbovePromptTokens, Prices: prices})
		}
		if len(model.SKUs) == 0 {
			catalog.Unsupported[raw.ID] = "no_pricing_skus"
		}
		catalog.Models[raw.ID] = model
	}
	for _, raw := range imageList.Data {
		if raw.ID == "" || len(raw.PricingSKUs) == 0 {
			return nil, &wokeyPriceSyncFailure{Code: "invalid_image_model_schema"}
		}
		if _, exists := catalog.Images[raw.ID]; exists {
			return nil, &wokeyPriceSyncFailure{Code: "duplicate_image_model_id"}
		}
		model := wokeyImageModel{ID: raw.ID}
		seen := make(map[string]struct{}, len(raw.PricingSKUs))
		for _, sku := range raw.PricingSKUs {
			if sku.Tier == "" || sku.PriceUSD == "" {
				return nil, &wokeyPriceSyncFailure{Code: "invalid_image_sku_schema"}
			}
			if _, exists := seen[sku.Tier]; exists {
				return nil, &wokeyPriceSyncFailure{Code: "duplicate_image_sku"}
			}
			seen[sku.Tier] = struct{}{}
			price, err := decimal.NewFromString(sku.PriceUSD.String())
			if err != nil || price.IsNegative() {
				return nil, &wokeyPriceSyncFailure{Code: "invalid_image_price"}
			}
			model.SKUs = append(model.SKUs, sku)
		}
		catalog.Images[raw.ID] = model
	}
	for _, raw := range videoList.Data {
		if raw.ID == "" || len(raw.PricingSKUs) == 0 {
			return nil, &wokeyPriceSyncFailure{Code: "invalid_video_model_schema"}
		}
		if _, exists := catalog.Videos[raw.ID]; exists {
			return nil, &wokeyPriceSyncFailure{Code: "duplicate_video_model_id"}
		}
		model := wokeyVideoModel{ID: raw.ID}
		seen := make(map[string]struct{}, len(raw.PricingSKUs))
		for _, sku := range raw.PricingSKUs {
			if sku.VariantID == "" || sku.Quality == "" || sku.Mode == "" || sku.Resolution == "" || sku.MinSeconds <= 0 || sku.MaxSeconds < sku.MinSeconds || sku.PriceUSD == "" {
				return nil, &wokeyPriceSyncFailure{Code: "invalid_video_sku_schema"}
			}
			if _, exists := seen[sku.VariantID]; exists {
				return nil, &wokeyPriceSyncFailure{Code: "duplicate_video_sku"}
			}
			seen[sku.VariantID] = struct{}{}
			price, err := decimal.NewFromString(sku.PriceUSD.String())
			if err != nil || price.IsNegative() {
				return nil, &wokeyPriceSyncFailure{Code: "invalid_video_price"}
			}
			model.SKUs = append(model.SKUs, sku)
		}
		catalog.Videos[raw.ID] = model
	}
	return catalog, nil
}

func quantizedWokeyPrice(value decimal.Decimal) (float64, error) {
	rounded := value.Round(wokeyPricePrecision)
	if value.GreaterThan(decimal.Zero) && rounded.IsZero() {
		return 0, errors.New("positive price rounds to zero")
	}
	f, _ := rounded.Float64()
	if !finiteNonNegative(f) {
		return 0, errors.New("price is outside the supported range")
	}
	return f, nil
}

func isWokeyIntegerLiteral(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func wokeyTokenRate(sku wokeyCatalogSKU, fx decimal.Decimal) (decimal.Decimal, error) {
	if sku.Unit != "token" || !sku.Quantity.GreaterThan(decimal.Zero) {
		return decimal.Zero, errors.New("unsupported token SKU unit")
	}
	return sku.PriceUSD.DivRound(sku.Quantity, 18).Mul(decimal.NewFromInt(1_000_000)).Mul(fx), nil
}

func sourceSKU(sku wokeyCatalogSKU) UnifiedGatewayWokeySourceSKU {
	return UnifiedGatewayWokeySourceSKU{SKUID: sku.SKUID, Meter: sku.Meter, Unit: sku.Unit, Quantity: sku.QuantityRaw, PriceUSD: sku.PriceRaw}
}

func buildWokeyPriceCards(catalog *wokeyCatalog, fx decimal.Decimal) (map[string]wokeyCardCandidates, map[string]string) {
	candidates := make(map[string]wokeyCardCandidates)
	unsupported := make(map[string]string)
	if catalog == nil || !fx.GreaterThan(decimal.Zero) {
		return candidates, map[string]string{"*": "invalid_catalog_or_fx"}
	}
	for modelID, model := range catalog.Models {
		if !model.Available {
			unsupported[modelID] = "model_unavailable"
			continue
		}
		if isSupportedWokeyTimeOfDayModel(modelID) {
			if model.TimeOfDayUnsupported != "" {
				unsupported[modelID] = model.TimeOfDayUnsupported
				continue
			}
			if model.PricingMode != "dynamic_discount" || !model.HasTimeOfDay || model.TimeOfDay == nil || len(model.PromptTiers) != 0 {
				unsupported[modelID] = "unsupported_time_of_day_shape"
				continue
			}
			card, reason := wokeyBuildTimeOfDayTokenCard(model, fx)
			if reason != "" {
				unsupported[modelID] = reason
			}
			if card != nil {
				candidates[modelID] = wokeyCardCandidates{Cards: []UnifiedGatewayRoutePricingEntry{*card}}
			}
			continue
		}
		if model.PricingMode == "time_of_day" || model.HasTimeOfDay {
			unsupported[modelID] = "unsupported_time_of_day_model"
			continue
		}
		if model.PricingMode != "" && model.PricingMode != "dynamic_discount" {
			unsupported[modelID] = "unsupported_pricing_mode"
			continue
		}
		hasTokenSKU := false
		for _, sku := range model.SKUs {
			if sku.Unit == "token" && wokeyKnownTokenMeter(sku.Meter) {
				hasTokenSKU = true
				break
			}
		}
		if !hasTokenSKU {
			continue
		}
		card, reason := wokeyBuildTokenCard(model, fx)
		if reason != "" {
			unsupported[modelID] = reason
		}
		if card != nil {
			candidates[modelID] = wokeyCardCandidates{Cards: []UnifiedGatewayRoutePricingEntry{*card}}
		}
	}
	for modelID, model := range catalog.Images {
		card, reason := wokeyBuildImageCard(catalog, model, fx)
		if reason != "" {
			unsupported[modelID] = reason
		}
		if card != nil {
			current := candidates[modelID]
			current.Cards = append(current.Cards, *card)
			candidates[modelID] = current
		}
	}
	for modelID, model := range catalog.Videos {
		cards, reasons := wokeyBuildVideoCards(catalog, model, fx)
		current := candidates[modelID]
		current.Cards = append(current.Cards, cards...)
		if len(reasons) > 0 {
			current.Unsupported = append(current.Unsupported, reasons...)
			unsupported[modelID] = strings.Join(reasons, ",")
		}
		if len(current.Cards) > 0 || len(current.Unsupported) > 0 {
			candidates[modelID] = current
		}
	}
	for modelID, reason := range catalog.Unsupported {
		unsupported[modelID] = reason
	}
	return candidates, unsupported
}

func isSupportedWokeyTimeOfDayModel(modelID string) bool {
	switch modelID {
	case "deepseek-flash", "deepseek-v4-flash", "deepseek-v4-pro":
		return true
	default:
		return false
	}
}

func wokeyBuildTimeOfDayTier(source wokeyCatalogTimeOfDayTier, fx decimal.Decimal) (UnifiedGatewayWokeyTimeOfDayTier, string) {
	base := &UnifiedGatewayTokenBasePrice{}
	fieldTargets := map[string]**float64{
		"input_tokens":       &base.InputPerMillion,
		"output_tokens":      &base.OutputPerMillion,
		"cache_read_tokens":  &base.CacheReadPerMillion,
		"cache_write_tokens": &base.CacheWritePerMillion,
	}
	for meter, raw := range source.PricesUSD {
		target, known := fieldTargets[meter]
		if !known || validateWokeyNonNegativeDecimalString(raw) != nil {
			return UnifiedGatewayWokeyTimeOfDayTier{}, "unsupported_time_of_day_meter"
		}
		price, _ := decimal.NewFromString(raw)
		value, err := quantizedWokeyPrice(price.Mul(fx))
		if err != nil {
			return UnifiedGatewayWokeyTimeOfDayTier{}, "unsupported_precision"
		}
		valueCopy := value
		*target = &valueCopy
	}
	if !base.validate() {
		return UnifiedGatewayWokeyTimeOfDayTier{}, "required_time_of_day_meter_missing"
	}
	return UnifiedGatewayWokeyTimeOfDayTier{TokenBasePrice: base, SourcePricesUSD: cloneStringMap(source.PricesUSD)}, ""
}

func wokeyTimeOfDayCurrentSKUsMatch(model wokeyCatalogModel) bool {
	if model.TimeOfDay == nil {
		return false
	}
	current := model.TimeOfDay.Peak
	if model.TimeOfDay.CurrentTier == "off_peak" {
		current = model.TimeOfDay.OffPeak
	}
	seen := make(map[string]struct{}, len(model.SKUs))
	for _, sku := range model.SKUs {
		if sku.Unit != "token" || sku.Quantity.Cmp(decimal.NewFromInt(1_000_000)) != 0 || !wokeyKnownTokenMeter(sku.Meter) || sku.ConstraintsUnsupported || sku.Constraints != (wokeySKUConstraints{}) {
			return false
		}
		if _, duplicate := seen[sku.Meter]; duplicate {
			return false
		}
		seen[sku.Meter] = struct{}{}
		price, expected := current.PricesUSD[sku.Meter]
		if !expected {
			return false
		}
		priceDecimal, pErr := decimal.NewFromString(price)
		if pErr != nil || !priceDecimal.Equal(sku.PriceUSD) {
			return false
		}
	}
	if len(seen) != len(current.PricesUSD) {
		return false
	}
	for _, meter := range []string{"input_tokens", "output_tokens", "cache_read_tokens"} {
		if _, exists := seen[meter]; !exists {
			return false
		}
	}
	return true
}

func wokeyBuildTimeOfDayTokenCard(model wokeyCatalogModel, fx decimal.Decimal) (*UnifiedGatewayRoutePricingEntry, string) {
	if !isSupportedWokeyTimeOfDayModel(model.ID) || model.TimeOfDay == nil || len(model.PromptTiers) != 0 {
		return nil, "unsupported_time_of_day_shape"
	}
	if !wokeyTimeOfDayCurrentSKUsMatch(model) {
		return nil, "time_of_day_current_sku_mismatch"
	}
	peak, reason := wokeyBuildTimeOfDayTier(model.TimeOfDay.Peak, fx)
	if reason != "" {
		return nil, reason
	}
	offPeak, reason := wokeyBuildTimeOfDayTier(model.TimeOfDay.OffPeak, fx)
	if reason != "" {
		return nil, reason
	}
	sources := make([]UnifiedGatewayWokeySourceSKU, 0, len(model.SKUs))
	for _, sku := range model.SKUs {
		sources = append(sources, sourceSKU(sku))
	}
	entry := &UnifiedGatewayRoutePricingEntry{
		Model: model.ID, Kind: UnifiedGatewayRoutePricingToken,
		TimeOfDayTokenPrice: &UnifiedGatewayWokeyTimeOfDayTokenPrice{
			CurrentTier:    model.TimeOfDay.CurrentTier,
			PeakWindowsUTC: append([]UnifiedGatewayWokeyTimeOfDayWindow(nil), model.TimeOfDay.PeakWindowsUTC...),
			Peak:           peak, OffPeak: offPeak,
		},
		Source: UnifiedGatewayWokeySource, SourceFX: fx.String(), SourceFetchedAt: wokeyTimePtr(time.Now().UTC()),
		SourceSKUs: sources, SyncState: "current",
	}
	return entry, ""
}

func wokeyBuildTokenCard(model wokeyCatalogModel, fx decimal.Decimal) (*UnifiedGatewayRoutePricingEntry, string) {
	if len(model.SKUs) == 0 {
		return nil, "no_pricing_skus"
	}
	base := &UnifiedGatewayTokenBasePrice{}
	requiredMeters := []string{"input_tokens", "output_tokens", "cache_read_tokens"}
	// Required fields are pointers in the public card. Build into a separate map
	// first so a missing source meter cannot be mistaken for an explicitly free rate.
	values := make(map[string]float64, len(model.SKUs))
	sources := make([]UnifiedGatewayWokeySourceSKU, 0, len(model.SKUs))
	for _, sku := range model.SKUs {
		if sku.Meter == "output_images" || sku.Meter == "output_video" {
			continue
		}
		if sku.Unit != "token" {
			return nil, "unsupported_sku_unit"
		}
		if _, known := wokeyTokenMeterField(sku.Meter); !known {
			return nil, "unsupported_token_meter"
		}
		if _, duplicate := values[sku.Meter]; duplicate {
			return nil, "duplicate_token_meter"
		}
		rate, err := wokeyTokenRate(sku, fx)
		if err != nil {
			return nil, "unsupported_token_unit"
		}
		value, err := quantizedWokeyPrice(rate)
		if err != nil {
			return nil, "unsupported_precision"
		}
		values[sku.Meter] = value
		sources = append(sources, sourceSKU(sku))
	}
	for _, meter := range requiredMeters {
		value, ok := values[meter]
		if !ok {
			return nil, "required_token_meter_missing"
		}
		ptr := value
		switch meter {
		case "input_tokens":
			base.InputPerMillion = &ptr
		case "output_tokens":
			base.OutputPerMillion = &ptr
		case "cache_read_tokens":
			base.CacheReadPerMillion = &ptr
		}
	}
	optional := map[string]**float64{
		"cache_write_tokens":    &base.CacheWritePerMillion,
		"cache_write_5m_tokens": &base.CacheWrite5mPerMillion,
		"cache_write_1h_tokens": &base.CacheWrite1hPerMillion,
	}
	for meter, target := range optional {
		if value, ok := values[meter]; ok {
			ptr := value
			*target = &ptr
		}
	}
	entry := UnifiedGatewayRoutePricingEntry{Model: model.ID, Kind: UnifiedGatewayRoutePricingToken, TokenBasePrice: base, Source: UnifiedGatewayWokeySource, SourceFX: fx.String(), SourceFetchedAt: wokeyTimePtr(time.Now().UTC()), SourceSKUs: sources, SyncState: "current"}
	if model.ID == "claude-haiku-5-5" && len(model.PromptTiers) > 0 {
		if len(model.PromptTiers) != 1 || model.PromptTiers[0].Threshold != UnifiedGatewayHaikuLongContextThreshold {
			return &entry, "unsupported_prompt_tier"
		}
		long, longSources, reason := wokeyBuildPromptTierCard(model.PromptTiers[0], fx)
		if reason != "" {
			return &entry, reason
		}
		entry.LongContextTokenBasePrice = long
		entry.SourceSKUs = append(entry.SourceSKUs, longSources...)
	} else if len(model.PromptTiers) > 0 {
		return &entry, "unsupported_prompt_tier"
	}
	return &entry, ""
}

func wokeyTokenMeterField(meter string) (string, bool) {
	switch meter {
	case "input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "cache_write_5m_tokens", "cache_write_1h_tokens":
		return meter, true
	default:
		return "", false
	}
}

func wokeyKnownTokenMeter(meter string) bool {
	_, ok := wokeyTokenMeterField(meter)
	return ok
}

func wokeyBuildPromptTierCard(tier wokeyPromptTier, fx decimal.Decimal) (*UnifiedGatewayTokenBasePrice, []UnifiedGatewayWokeySourceSKU, string) {
	base := &UnifiedGatewayTokenBasePrice{}
	fieldTargets := map[string]**float64{
		"input_tokens": &base.InputPerMillion, "output_tokens": &base.OutputPerMillion,
		"cache_read_tokens": &base.CacheReadPerMillion, "cache_write_tokens": &base.CacheWritePerMillion,
		"cache_write_5m_tokens": &base.CacheWrite5mPerMillion, "cache_write_1h_tokens": &base.CacheWrite1hPerMillion,
	}
	sources := make([]UnifiedGatewayWokeySourceSKU, 0, len(tier.Prices))
	for meter, raw := range tier.Prices {
		if raw == "" {
			continue
		}
		price, err := decimal.NewFromString(raw)
		if err != nil || price.IsNegative() {
			return nil, nil, "invalid_prompt_tier_price"
		}
		rate := price.Mul(fx)
		value, err := quantizedWokeyPrice(rate)
		if err != nil {
			return nil, nil, "unsupported_precision"
		}
		ptr := value
		*fieldTargets[meter] = &ptr
		id := fmt.Sprintf("prompt_tier:%d:%s", tier.Threshold, meter)
		sources = append(sources, UnifiedGatewayWokeySourceSKU{SKUID: id, Meter: meter, Unit: "token", Quantity: "1000000", PriceUSD: raw})
	}
	if !base.validate() {
		return nil, nil, "invalid_prompt_tier"
	}
	return base, sources, ""
}

func wokeyBuildImageCard(catalog *wokeyCatalog, model wokeyImageModel, fx decimal.Decimal) (*UnifiedGatewayRoutePricingEntry, string) {
	priced, ok := catalog.Models[model.ID]
	if !ok || !priced.Available {
		return nil, "image_price_model_missing"
	}
	if len(model.SKUs) == 0 {
		return nil, "image_skus_missing"
	}
	for _, imageSKU := range model.SKUs {
		for _, size := range imageSKU.Sizes {
			tier, known := ClassifyImageBillingTier(size)
			if !known || tier != imageSKU.Tier {
				return nil, "unsupported_image_size"
			}
		}
	}
	byTier := make(map[string]wokeyCatalogSKU)
	for _, sku := range priced.SKUs {
		if sku.Meter != "output_images" || sku.Unit != "image" || sku.Constraints.Tier == "" {
			continue
		}
		if _, exists := byTier[sku.Constraints.Tier]; exists {
			return nil, "duplicate_normalized_image_tier"
		}
		byTier[sku.Constraints.Tier] = sku
	}
	var first decimal.Decimal
	sources := make([]UnifiedGatewayWokeySourceSKU, 0, len(model.SKUs))
	for i, imageSKU := range model.SKUs {
		normalized, exists := byTier[imageSKU.Tier]
		if !exists {
			return nil, "image_catalog_price_mismatch"
		}
		imagePrice, err := decimal.NewFromString(imageSKU.PriceUSD.String())
		if err != nil || imagePrice.IsNegative() {
			return nil, "invalid_image_price"
		}
		normalizedPrice, err := wokeyUnitPrice(normalized, "image")
		if err != nil || !normalizedPrice.Equal(imagePrice) {
			return nil, "image_catalog_price_mismatch"
		}
		if i == 0 {
			first = imagePrice
		} else if !first.Equal(imagePrice) {
			return nil, "unsupported_image_tier_price_difference"
		}
		sources = append(sources, sourceSKU(normalized))
		delete(byTier, imageSKU.Tier)
	}
	if len(byTier) != 0 {
		return nil, "normalized_image_sku_missing_from_media_catalog"
	}
	perImage := first.Mul(fx)
	value, err := quantizedWokeyPrice(perImage)
	if err != nil {
		return nil, "unsupported_precision"
	}
	return &UnifiedGatewayRoutePricingEntry{Model: model.ID, Kind: UnifiedGatewayRoutePricingImage, UnitPrice: &value, ImagePricingMode: UnifiedGatewayImagePricingFlatPerImage, Source: UnifiedGatewayWokeySource, SourceFX: fx.String(), SourceFetchedAt: wokeyTimePtr(time.Now().UTC()), SourceSKUs: sources, SyncState: "current"}, ""
}

func wokeyBuildVideoCards(catalog *wokeyCatalog, model wokeyVideoModel, fx decimal.Decimal) ([]UnifiedGatewayRoutePricingEntry, []string) {
	priced, ok := catalog.Models[model.ID]
	if !ok || !priced.Available {
		return nil, []string{"video_price_model_missing"}
	}
	mediaVariants := make(map[string]wokeyVideoSKU, len(model.SKUs))
	for _, sku := range model.SKUs {
		key := wokeyVideoVariantKey(sku.Quality, sku.Mode, sku.Resolution, sku.MinSeconds, sku.MaxSeconds)
		if _, exists := mediaVariants[key]; exists {
			return nil, []string{"duplicate_video_variant"}
		}
		mediaVariants[key] = sku
	}
	normalizedVariants := make(map[string]wokeyCatalogSKU, len(priced.SKUs))
	for _, sku := range priced.SKUs {
		if sku.Meter != "output_video" || sku.Unit != "second" {
			continue
		}
		key := wokeyVideoVariantKey(sku.Constraints.Quality, sku.Constraints.Mode, sku.Constraints.Resolution, sku.Constraints.MinDurationSeconds, sku.Constraints.MaxDurationSeconds)
		if _, exists := normalizedVariants[key]; exists {
			return nil, []string{"duplicate_normalized_video_variant"}
		}
		normalizedVariants[key] = sku
	}
	if len(mediaVariants) == 0 || len(mediaVariants) != len(normalizedVariants) {
		return nil, []string{"video_catalog_sku_mismatch"}
	}
	for key := range mediaVariants {
		if _, exists := normalizedVariants[key]; !exists {
			return nil, []string{"video_catalog_sku_mismatch"}
		}
	}
	byResolution := make(map[string][]wokeyCatalogSKU)
	for key, media := range mediaVariants {
		normalized := normalizedVariants[key]
		mediaPrice, err := decimal.NewFromString(media.PriceUSD.String())
		if err != nil || mediaPrice.IsNegative() {
			return nil, []string{"invalid_video_price"}
		}
		normalizedPrice, err := wokeyUnitPrice(normalized, "second")
		if err != nil || !normalizedPrice.Equal(mediaPrice) {
			return nil, []string{"video_catalog_price_mismatch"}
		}
		byResolution[media.Resolution] = append(byResolution[media.Resolution], normalized)
	}
	cards := make([]UnifiedGatewayRoutePricingEntry, 0)
	reasons := make([]string, 0)
	resolutions := make([]string, 0, len(byResolution))
	for resolution := range byResolution {
		resolutions = append(resolutions, resolution)
	}
	sort.Strings(resolutions)
	for _, resolution := range resolutions {
		variants := byResolution[resolution]
		price, err := wokeyUnitPrice(variants[0], "second")
		if err != nil {
			reasons = append(reasons, "unsupported_video_unit")
			continue
		}
		covered := true
		sources := make([]UnifiedGatewayWokeySourceSKU, 0, len(variants))
		for _, variant := range variants {
			variantPrice, priceErr := wokeyUnitPrice(variant, "second")
			if priceErr != nil || !price.Equal(variantPrice) {
				reasons = append(reasons, "unsupported_video_variant_price_difference")
				covered = false
				break
			}
			if variant.Constraints.MinDurationSeconds > VideoBillingMinDurationSeconds || variant.Constraints.MaxDurationSeconds < VideoBillingMaxDurationSeconds {
				reasons = append(reasons, "unsupported_video_duration_range")
				covered = false
				break
			}
			sources = append(sources, sourceSKU(variant))
		}
		if !covered {
			continue
		}
		for duration := VideoBillingMinDurationSeconds; duration <= VideoBillingMaxDurationSeconds; duration++ {
			value, priceErr := quantizedWokeyPrice(price.Mul(decimal.NewFromInt(int64(duration))).Mul(fx))
			if priceErr != nil {
				reasons = append(reasons, "unsupported_precision")
				break
			}
			entrySources := append([]UnifiedGatewayWokeySourceSKU(nil), sources...)
			cards = append(cards, UnifiedGatewayRoutePricingEntry{Model: model.ID, Kind: UnifiedGatewayRoutePricingVideo, VideoResolution: resolution, VideoDurationSeconds: duration, UnitPrice: &value, Source: UnifiedGatewayWokeySource, SourceFX: fx.String(), SourceFetchedAt: wokeyTimePtr(time.Now().UTC()), SourceSKUs: entrySources, SyncState: "current"})
		}
	}
	return cards, reasons
}

func wokeyVideoVariantKey(quality, mode, resolution string, minSeconds, maxSeconds int) string {
	return strings.ToLower(strings.TrimSpace(quality)) + "\x00" + strings.ToLower(strings.TrimSpace(mode)) + "\x00" + strings.ToLower(strings.TrimSpace(resolution)) + fmt.Sprintf("\x00%d\x00%d", minSeconds, maxSeconds)
}

func wokeyUnitPrice(sku wokeyCatalogSKU, expectedUnit string) (decimal.Decimal, error) {
	if sku.Unit != expectedUnit || !sku.Quantity.GreaterThan(decimal.Zero) {
		return decimal.Zero, errors.New("unsupported SKU unit")
	}
	return sku.PriceUSD.DivRound(sku.Quantity, 18), nil
}

func recalculateWokeyManagedEntry(entry UnifiedGatewayRoutePricingEntry, fx decimal.Decimal) (UnifiedGatewayRoutePricingEntry, error) {
	if entry.Source != UnifiedGatewayWokeySource || !fx.GreaterThan(decimal.Zero) || len(entry.SourceSKUs) == 0 {
		return entry, errors.New("entry has no complete Wokey source snapshot")
	}
	if entry.TimeOfDayTokenPrice != nil {
		if entry.Kind != UnifiedGatewayRoutePricingToken || !isSupportedWokeyTimeOfDayModel(entry.Model) {
			return entry, errors.New("unsupported saved Wokey time-of-day card")
		}
		price := *entry.TimeOfDayTokenPrice
		peak, reason := wokeyBuildTimeOfDayTier(wokeyCatalogTimeOfDayTier{PricesUSD: price.Peak.SourcePricesUSD}, fx)
		if reason != "" {
			return entry, fmt.Errorf("cannot recalculate Wokey peak tier: %s", reason)
		}
		offPeak, reason := wokeyBuildTimeOfDayTier(wokeyCatalogTimeOfDayTier{PricesUSD: price.OffPeak.SourcePricesUSD}, fx)
		if reason != "" {
			return entry, fmt.Errorf("cannot recalculate Wokey off-peak tier: %s", reason)
		}
		price.Peak = peak
		price.OffPeak = offPeak
		entry.TimeOfDayTokenPrice = &price
		entry.SourceFX = fx.String()
		return entry, nil
	}
	rates := make([]decimal.Decimal, 0, len(entry.SourceSKUs))
	for _, source := range entry.SourceSKUs {
		price, err := decimal.NewFromString(source.PriceUSD)
		quantity, qErr := decimal.NewFromString(source.Quantity)
		if err != nil || qErr != nil || !quantity.GreaterThan(decimal.Zero) {
			return entry, errors.New("invalid Wokey source SKU")
		}
		rates = append(rates, price.DivRound(quantity, 18))
	}
	switch entry.Kind {
	case UnifiedGatewayRoutePricingToken:
		base := &UnifiedGatewayTokenBasePrice{}
		long := &UnifiedGatewayTokenBasePrice{}
		longFound := false
		for i, source := range entry.SourceSKUs {
			target := base
			meter := source.Meter
			if strings.HasPrefix(source.SKUID, "prompt_tier:") {
				parts := strings.Split(source.SKUID, ":")
				if len(parts) != 3 || strings.TrimSpace(parts[1]) != fmt.Sprint(UnifiedGatewayHaikuLongContextThreshold) || parts[2] != meter {
					return entry, errors.New("unsupported saved prompt tier")
				}
				target = long
				longFound = true
			}
			rate, err := quantizedWokeyPrice(rates[i].Mul(decimal.NewFromInt(1_000_000)).Mul(fx))
			if err != nil {
				return entry, err
			}
			ptr := rate
			switch meter {
			case "input_tokens":
				target.InputPerMillion = &ptr
			case "output_tokens":
				target.OutputPerMillion = &ptr
			case "cache_read_tokens":
				target.CacheReadPerMillion = &ptr
			case "cache_write_tokens":
				target.CacheWritePerMillion = &ptr
			case "cache_write_5m_tokens":
				target.CacheWrite5mPerMillion = &ptr
			case "cache_write_1h_tokens":
				target.CacheWrite1hPerMillion = &ptr
			default:
				return entry, errors.New("unsupported saved Wokey token meter")
			}
		}
		if !base.validate() || (longFound && !long.validate()) {
			return entry, errors.New("incomplete saved Wokey token card")
		}
		entry.TokenBasePrice = base
		entry.LongContextTokenBasePrice = nil
		if longFound {
			entry.LongContextTokenBasePrice = long
		}
	case UnifiedGatewayRoutePricingImage:
		var unitPrice *decimal.Decimal
		for i, source := range entry.SourceSKUs {
			if source.Unit != "image" {
				return entry, errors.New("unsupported saved Wokey image unit")
			}
			value := rates[i].Mul(fx)
			if unitPrice != nil && !unitPrice.Equal(value) {
				return entry, errors.New("conflicting saved Wokey image rates")
			}
			unitPrice = &value
		}
		if unitPrice == nil {
			return entry, errors.New("missing saved Wokey image rate")
		}
		value, err := quantizedWokeyPrice(*unitPrice)
		if err != nil {
			return entry, err
		}
		entry.UnitPrice = &value
	case UnifiedGatewayRoutePricingVideo:
		var perSecond *decimal.Decimal
		for i, source := range entry.SourceSKUs {
			if source.Unit != "second" {
				return entry, errors.New("unsupported saved Wokey video unit")
			}
			value := rates[i]
			if perSecond != nil && !perSecond.Equal(value) {
				return entry, errors.New("conflicting saved Wokey video rates")
			}
			perSecond = &value
		}
		if perSecond == nil {
			return entry, errors.New("missing saved Wokey video rate")
		}
		amount := perSecond.Mul(decimal.NewFromInt(int64(entry.VideoDurationSeconds))).Mul(fx)
		value, err := quantizedWokeyPrice(amount)
		if err != nil {
			return entry, err
		}
		entry.UnitPrice = &value
	default:
		return entry, errors.New("unsupported saved Wokey card kind")
	}
	entry.SourceFX = fx.String()
	return entry, nil
}

func mergeWokeyPriceCards(cfg UnifiedGatewayRoutePricingConfig, accountIDs []int64, candidates map[string]wokeyCardCandidates, unsupported map[string]string, catalog *wokeyCatalog, catalogHash string, fx decimal.Decimal) ([]UnifiedGatewayRoutePricingEntry, wokeyMergeCounts) {
	selected := make(map[int64]struct{}, len(accountIDs))
	for _, id := range accountIDs {
		selected[id] = struct{}{}
	}
	counts := wokeyMergeCounts{Reasons: []string{}}
	result := make([]UnifiedGatewayRoutePricingEntry, 0, len(cfg.Entries)+len(accountIDs))
	managedByKey := make(map[string]struct{})
	manualExactImageByAccountModel := make(map[string]struct{})
	for _, entry := range cfg.Entries {
		if entry.Source == "" && entry.Kind == UnifiedGatewayRoutePricingImage && entry.ImagePricingMode == "" {
			manualExactImageByAccountModel[fmt.Sprintf("%d\x00%s", entry.AccountID, entry.Model)] = struct{}{}
		}
		if entry.Source == UnifiedGatewayWokeySource {
			if _, inScope := selected[entry.AccountID]; inScope {
				continue
			}
			result = append(result, entry)
			continue
		}
		result = append(result, entry)
	}
	candidateLists := make(map[int64]map[string]UnifiedGatewayRoutePricingEntry, len(accountIDs))
	for _, accountID := range accountIDs {
		byKey := make(map[string]UnifiedGatewayRoutePricingEntry)
		for _, set := range candidates {
			for _, template := range set.Cards {
				entry := template
				entry.AccountID = accountID
				entry.SourceCatalogSHA256 = catalogHash
				entry.SourceFX = fx.String()
				if entry.SourceFetchedAt == nil {
					now := time.Now().UTC()
					entry.SourceFetchedAt = &now
				}
				key := unifiedGatewayRoutePricingKey(cfg.TargetGroupID, entry.AccountID, entry.Model, entry.Kind, entry.ImagePricingMode, entry.ImageSize, entry.ImageQuality, entry.VideoResolution, entry.VideoDurationSeconds)
				byKey[key] = entry
			}
		}
		candidateLists[accountID] = byKey
	}
	for _, entry := range cfg.Entries {
		if entry.Source != UnifiedGatewayWokeySource {
			continue
		}
		if _, inScope := selected[entry.AccountID]; !inScope {
			continue
		}
		key := unifiedGatewayRoutePricingKey(cfg.TargetGroupID, entry.AccountID, entry.Model, entry.Kind, entry.ImagePricingMode, entry.ImageSize, entry.ImageQuality, entry.VideoResolution, entry.VideoDurationSeconds)
		if candidate, ok := candidateLists[entry.AccountID][key]; ok {
			if candidate.Kind == UnifiedGatewayRoutePricingToken && candidate.LongContextTokenBasePrice == nil && entry.LongContextTokenBasePrice != nil {
				if reason, partial := unsupported[candidate.Model]; partial {
					candidate.LongContextTokenBasePrice = entry.LongContextTokenBasePrice
					for _, source := range entry.SourceSKUs {
						if strings.HasPrefix(source.SKUID, "prompt_tier:") {
							candidate.SourceSKUs = append(candidate.SourceSKUs, source)
						}
					}
					candidate.SyncState = "unsupported"
					candidate.SyncReason = reason
					counts.Unsupported++
					counts.Reasons = append(counts.Reasons, fmt.Sprintf("%s:%s", candidate.Model, reason))
				}
			}
			result = append(result, candidate)
			delete(candidateLists[entry.AccountID], key)
			continue
		}
		if entry.TimeOfDayTokenPrice == nil {
			entry.SourceFX = fx.String()
		}
		if reason, present := unsupported[entry.Model]; present {
			entry.SyncState = "unsupported"
			entry.SyncReason = reason
			counts.Unsupported++
			counts.Reasons = append(counts.Reasons, fmt.Sprintf("%s:%s", entry.Model, reason))
		} else if !catalogModelReturned(catalog, entry.Model) {
			entry.SyncState = "not_returned"
			entry.SyncReason = "model_not_returned"
			counts.NotReturned++
			counts.Reasons = append(counts.Reasons, fmt.Sprintf("%s:model_not_returned", entry.Model))
		} else {
			entry.SyncState = "unsupported"
			entry.SyncReason = "route_card_not_expressible"
			counts.Unsupported++
			counts.Reasons = append(counts.Reasons, fmt.Sprintf("%s:route_card_not_expressible", entry.Model))
		}
		result = append(result, entry)
	}
	for _, accountID := range accountIDs {
		for key, candidate := range candidateLists[accountID] {
			if candidate.Kind == UnifiedGatewayRoutePricingImage && candidate.ImagePricingMode == UnifiedGatewayImagePricingFlatPerImage {
				accountModelKey := fmt.Sprintf("%d\x00%s", candidate.AccountID, candidate.Model)
				if _, conflict := manualExactImageByAccountModel[accountModelKey]; conflict {
					counts.Conflict++
					counts.Reasons = append(counts.Reasons, fmt.Sprintf("%s:manual_conflict", candidate.Model))
					continue
				}
			}
			manualKey := unifiedGatewayRoutePricingKey(cfg.TargetGroupID, candidate.AccountID, candidate.Model, candidate.Kind, candidate.ImagePricingMode, candidate.ImageSize, candidate.ImageQuality, candidate.VideoResolution, candidate.VideoDurationSeconds)
			if hasManualExactKey(cfg.Entries, manualKey, cfg.TargetGroupID) {
				counts.Conflict++
				counts.Reasons = append(counts.Reasons, fmt.Sprintf("%s:manual_conflict", candidate.Model))
				continue
			}
			if _, exists := managedByKey[key]; exists {
				continue
			}
			managedByKey[key] = struct{}{}
			result = append(result, candidate)
		}
	}
	for _, entry := range result {
		if entry.Source == UnifiedGatewayWokeySource {
			counts.Managed++
		}
	}
	for modelID, reason := range unsupported {
		counts.Unsupported++
		counts.Reasons = append(counts.Reasons, fmt.Sprintf("%s:%s", modelID, reason))
	}
	sort.Slice(result, func(i, j int) bool {
		return unifiedGatewayRoutePricingKey(cfg.TargetGroupID, result[i].AccountID, result[i].Model, result[i].Kind, result[i].ImagePricingMode, result[i].ImageSize, result[i].ImageQuality, result[i].VideoResolution, result[i].VideoDurationSeconds) < unifiedGatewayRoutePricingKey(cfg.TargetGroupID, result[j].AccountID, result[j].Model, result[j].Kind, result[j].ImagePricingMode, result[j].ImageSize, result[j].ImageQuality, result[j].VideoResolution, result[j].VideoDurationSeconds)
	})
	sort.Strings(counts.Reasons)
	return result, counts
}

func catalogModelReturned(catalog *wokeyCatalog, model string) bool {
	if catalog == nil {
		return false
	}
	_, inPricing := catalog.Models[model]
	_, inImages := catalog.Images[model]
	_, inVideos := catalog.Videos[model]
	return inPricing || inImages || inVideos
}

func hasManualExactKey(entries []UnifiedGatewayRoutePricingEntry, candidateKey string, groupID int64) bool {
	for _, entry := range entries {
		if entry.Source != "" {
			continue
		}
		key := unifiedGatewayRoutePricingKey(groupID, entry.AccountID, entry.Model, entry.Kind, entry.ImagePricingMode, entry.ImageSize, entry.ImageQuality, entry.VideoResolution, entry.VideoDurationSeconds)
		if key == candidateKey {
			return true
		}
	}
	return false
}

func (s *SettingService) StartWokeyPriceSync() error {
	return s.reconfigureWokeyPriceSync(context.Background())
}

func (s *SettingService) StopWokeyPriceSync() {
	if s == nil {
		return
	}
	s.wokeySyncLifecycleMu.Lock()
	defer s.wokeySyncLifecycleMu.Unlock()
	s.stopWokeyPriceSyncLocked()
}

func (s *SettingService) stopWokeyPriceSyncLocked() {
	s.wokeySyncWorkerMu.Lock()
	worker := s.wokeySyncWorker
	s.wokeySyncWorker = nil
	if worker != nil {
		worker.cancel()
	}
	if worker != nil {
		<-worker.done
	}
	s.wokeySyncWorkerMu.Unlock()
}

func (s *SettingService) reconfigureWokeyPriceSync(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.wokeySyncLifecycleMu.Lock()
	defer s.wokeySyncLifecycleMu.Unlock()
	cfg := s.ActiveUnifiedGatewayRoutePricing()
	var syncCfg UnifiedGatewayWokeySyncConfig
	if cfg != nil && cfg.WokeySync != nil {
		syncCfg = *cfg.WokeySync
	} else {
		syncCfg = defaultUnifiedGatewayWokeySyncConfig()
	}
	syncCfg, err := normalizeUnifiedGatewayWokeySyncConfig(syncCfg)
	if err != nil {
		s.stopWokeyPriceSyncLocked()
		return err
	}
	eligible := syncCfg.Enabled && len(syncCfg.AccountIDs) > 0 && cfg != nil
	if eligible {
		_, err = s.validatedWokeyAccountIDs(ctx, cfg.TargetGroupID, syncCfg.AccountIDs)
		if err != nil {
			s.stopWokeyPriceSyncLocked()
			return err
		}
	}
	s.stopWokeyPriceSyncLocked()
	if !eligible {
		return nil
	}
	interval := time.Duration(syncCfg.IntervalMinutes) * time.Minute
	workerCtx, cancel := context.WithCancel(context.Background())
	worker := &wokeyPriceSyncWorker{cancel: cancel, done: make(chan struct{})}
	s.wokeySyncWorkerMu.Lock()
	s.wokeySyncWorker = worker
	s.wokeySyncWorkerMu.Unlock()
	go func() {
		defer close(worker.done)
		ticker := s.newWokeyPriceSyncTicker(interval)
		defer ticker.Stop()
		s.runWokeyPriceSync(workerCtx)
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.Ticks():
				s.runWokeyPriceSync(workerCtx)
			}
		}
	}()
	return nil
}

func (s *SettingService) runWokeyPriceSync(ctx context.Context) {
	if _, err := s.SyncWokeyPriceCatalog(ctx); err != nil && !errors.Is(err, ErrUnifiedGatewayWokeySyncBusy) && ctx.Err() == nil {
		// Error codes are intentionally fixed; upstream response bodies and account credentials are never logged.
		logger.LegacyPrintf("service.setting", "Wokey price refresh failed: %s", sanitizedWokeyErrorCode(err))
	}
}

func sanitizedWokeyErrorCode(err error) string {
	var failure *wokeyPriceSyncFailure
	if errors.As(err, &failure) {
		return failure.Code
	}
	if errors.Is(err, ErrUnifiedGatewayWokeySyncScope) {
		return "invalid_scope"
	}
	return "refresh_failed"
}

func WokeyPriceSyncErrorCode(err error) string {
	return sanitizedWokeyErrorCode(err)
}

func (s *SettingService) SyncWokeyPriceCatalog(ctx context.Context) (*UnifiedGatewayRoutePricingAdminState, error) {
	if s == nil || !s.wokeySyncRunMu.TryLock() {
		return nil, ErrUnifiedGatewayWokeySyncBusy
	}
	defer s.wokeySyncRunMu.Unlock()
	fetcher := s.wokeyCatalogFetcher
	if fetcher == nil {
		fetcher = newWokeyHTTPFetcher()
	}
	active := s.ActiveUnifiedGatewayRoutePricing()
	if active == nil || active.WokeySync == nil || !active.WokeySync.Enabled || len(active.WokeySync.AccountIDs) == 0 {
		return nil, ErrUnifiedGatewayWokeySyncDisabled
	}
	if _, err := s.validatedWokeyAccountIDs(ctx, active.TargetGroupID, active.WokeySync.AccountIDs); err != nil {
		_ = s.recordWokeySyncFailure(ctx, err)
		return nil, err
	}
	catalog, err := fetcher.Fetch(ctx)
	if err != nil {
		statusErr := s.recordWokeySyncFailure(ctx, err)
		if statusErr != nil {
			return nil, statusErr
		}
		return nil, err
	}
	state, err := s.publishWokeyCatalog(ctx, catalog)
	if err != nil {
		statusErr := s.recordWokeySyncFailure(ctx, err)
		if statusErr != nil {
			return nil, statusErr
		}
		return nil, err
	}
	return state, nil
}

func (s *SettingService) validatedWokeyAccountIDs(ctx context.Context, groupID int64, ids []int64) ([]int64, error) {
	_, accounts, err := s.wokeyAdminOptions(ctx, groupID)
	if err != nil {
		return nil, err
	}
	return validateWokeyAccountIDsFromAccounts(accounts, ids)
}

func (s *SettingService) wokeyAdminOptions(ctx context.Context, groupID int64) ([]Group, []Account, error) {
	if groupID <= 0 || s.routePricingGroupRepo == nil || s.routePricingAccountRepo == nil {
		return nil, nil, ErrUnifiedGatewayWokeySyncScope
	}
	groups, err := s.routePricingGroupRepo.ListActiveByPlatform(ctx, PlatformComposite)
	if err != nil || len(groups) != 1 || groups[0].ID != groupID || groups[0].Platform != PlatformComposite || !groups[0].IsActive() {
		return nil, nil, ErrUnifiedGatewayWokeySyncScope
	}
	accounts, err := s.routePricingAccountRepo.ListSchedulableByGroupID(ctx, groupID)
	if err != nil {
		return nil, nil, err
	}
	return groups, accounts, nil
}

func validateWokeyAccountIDsFromAccounts(accounts []Account, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, ErrUnifiedGatewayWokeySyncScope
	}
	byID := make(map[int64]Account, len(accounts))
	for _, account := range accounts {
		byID[account.ID] = account
	}
	validated := make([]int64, 0, len(ids))
	for _, id := range ids {
		account, ok := byID[id]
		if !ok || !isWokeyBaseURL(account.GetBaseURL()) {
			return nil, ErrUnifiedGatewayWokeySyncScope
		}
		validated = append(validated, id)
	}
	sort.Slice(validated, func(i, j int) bool { return validated[i] < validated[j] })
	return validated, nil
}

func (s *SettingService) recordWokeySyncFailure(ctx context.Context, cause error) error {
	code := sanitizedWokeyErrorCode(cause)
	status := UnifiedGatewayWokeySyncStatus{LastAttemptAt: wokeyTimePtr(time.Now().UTC()), LastErrorCode: code, Reasons: []string{code}}
	var failure *wokeyPriceSyncFailure
	if errors.As(cause, &failure) {
		status.LastHTTPStatus = failure.HTTPStatus
	}
	return s.mutateWokeyConfig(ctx, func(cfg *UnifiedGatewayRoutePricingConfig) error {
		if cfg.WokeySync == nil || !cfg.WokeySync.Enabled {
			return ErrUnifiedGatewayWokeySyncDisabled
		}
		previous := cfg.WokeySync.Status
		status.LastSuccessAt = previous.LastSuccessAt
		status.CatalogSHA256 = previous.CatalogSHA256
		status.ManagedCardCount = previous.ManagedCardCount
		status.ManualConflictCount = previous.ManualConflictCount
		status.UnsupportedCount = previous.UnsupportedCount
		status.NotReturnedCount = previous.NotReturnedCount
		cfg.WokeySync.Status = status
		return nil
	})
}

func (s *SettingService) mutateWokeyConfig(ctx context.Context, mutate func(*UnifiedGatewayRoutePricingConfig) error) error {
	for attempt := 0; attempt < 3; attempt++ {
		s.routePricingMu.Lock()
		raw, err := s.settingRepo.GetValue(ctx, UnifiedGatewayRoutePricingSettingKey)
		if err != nil {
			s.routePricingMu.Unlock()
			return err
		}
		var cfg UnifiedGatewayRoutePricingConfig
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			s.routePricingMu.Unlock()
			return &wokeyPriceSyncFailure{Code: "stored_config_invalid"}
		}
		if err := mutate(&cfg); err != nil {
			s.routePricingMu.Unlock()
			return err
		}
		cfg.Revision++
		snapshot, err := buildUnifiedGatewayRoutePricingSnapshot(cfg)
		if err != nil {
			s.routePricingMu.Unlock()
			return err
		}
		encoded, err := json.Marshal(snapshot.config)
		if err != nil {
			s.routePricingMu.Unlock()
			return err
		}
		casRepo, ok := s.settingRepo.(SettingCompareAndSetRepository)
		if !ok {
			s.routePricingMu.Unlock()
			return errors.New("settings repository does not support atomic route pricing updates")
		}
		updated, err := casRepo.CompareAndSetValue(ctx, UnifiedGatewayRoutePricingSettingKey, &raw, string(encoded))
		if err == nil && updated {
			s.routePricingSnapshot.Store(snapshot)
			s.routePricingMu.Unlock()
			return nil
		}
		s.routePricingMu.Unlock()
		if err != nil {
			return err
		}
	}
	return ErrUnifiedGatewayRoutePricingRevisionConflict
}

func (s *SettingService) publishWokeyCatalog(ctx context.Context, catalog *wokeyCatalog) (*UnifiedGatewayRoutePricingAdminState, error) {
	if catalog == nil {
		return nil, &wokeyPriceSyncFailure{Code: "invalid_catalog"}
	}
	for attempt := 0; attempt < 3; attempt++ {
		s.routePricingMu.Lock()
		raw, err := s.settingRepo.GetValue(ctx, UnifiedGatewayRoutePricingSettingKey)
		if err != nil {
			s.routePricingMu.Unlock()
			return nil, err
		}
		var cfg UnifiedGatewayRoutePricingConfig
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			s.routePricingMu.Unlock()
			return nil, &wokeyPriceSyncFailure{Code: "stored_config_invalid"}
		}
		if cfg.WokeySync == nil || !cfg.WokeySync.Enabled || len(cfg.WokeySync.AccountIDs) == 0 {
			s.routePricingMu.Unlock()
			return nil, ErrUnifiedGatewayWokeySyncDisabled
		}
		groups, accounts, err := s.wokeyAdminOptions(ctx, cfg.TargetGroupID)
		if err != nil {
			s.routePricingMu.Unlock()
			return nil, err
		}
		ids, err := validateWokeyAccountIDsFromAccounts(accounts, cfg.WokeySync.AccountIDs)
		if err != nil {
			s.routePricingMu.Unlock()
			return nil, err
		}
		fx, err := decimal.NewFromString(cfg.WokeySync.FX)
		if err != nil || !fx.GreaterThan(decimal.Zero) {
			s.routePricingMu.Unlock()
			return nil, &wokeyPriceSyncFailure{Code: "invalid_fx"}
		}
		candidateByModel, unsupported := buildWokeyPriceCards(catalog, fx)
		entries, counts := mergeWokeyPriceCards(cfg, ids, candidateByModel, unsupported, catalog, catalog.Hash, fx)
		cfg.Entries = entries
		now := time.Now().UTC()
		cfg.WokeySync.Status = UnifiedGatewayWokeySyncStatus{
			LastAttemptAt: &now, LastSuccessAt: &now, CatalogSHA256: catalog.Hash,
			ManagedCardCount: counts.Managed, ManualConflictCount: counts.Conflict,
			UnsupportedCount: counts.Unsupported, NotReturnedCount: counts.NotReturned, Reasons: counts.Reasons,
		}
		cfg.Revision++
		snapshot, err := buildUnifiedGatewayRoutePricingSnapshot(cfg)
		if err != nil {
			s.routePricingMu.Unlock()
			return nil, err
		}
		encoded, err := json.Marshal(snapshot.config)
		if err != nil {
			s.routePricingMu.Unlock()
			return nil, err
		}
		casRepo, ok := s.settingRepo.(SettingCompareAndSetRepository)
		if !ok {
			s.routePricingMu.Unlock()
			return nil, errors.New("settings repository does not support atomic route pricing updates")
		}
		updated, err := casRepo.CompareAndSetValue(ctx, UnifiedGatewayRoutePricingSettingKey, &raw, string(encoded))
		if err != nil {
			s.routePricingMu.Unlock()
			return nil, err
		}
		if !updated {
			s.routePricingMu.Unlock()
			continue
		}
		s.routePricingSnapshot.Store(snapshot)
		state := buildUnifiedGatewayRoutePricingAdminState(snapshot.config, groups, accounts, snapshot.config.Revision)
		s.routePricingMu.Unlock()
		return state, nil
	}
	return nil, ErrUnifiedGatewayRoutePricingRevisionConflict
}

func wokeyTimePtr(t time.Time) *time.Time { return &t }
