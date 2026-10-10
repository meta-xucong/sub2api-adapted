package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

const wokeyPricingFixture = `{"object":"list","data":[
{"id":"claude-haiku-5-5","object":"model","available":true,"currency":"USD","pricing_mode":"dynamic_discount","pricing_skus":[
{"sku_id":"haiku:input","meter":"input_tokens","unit":"token","quantity":1000000,"price_usd":"0.080000"},
{"sku_id":"haiku:output","meter":"output_tokens","unit":"token","quantity":1000000,"price_usd":"0.400000"},
{"sku_id":"haiku:cache-read","meter":"cache_read_tokens","unit":"token","quantity":1000000,"price_usd":"0.008000"},
{"sku_id":"haiku:cache-write","meter":"cache_write_tokens","unit":"token","quantity":1000000,"price_usd":"0.160000"},
{"sku_id":"haiku:cache-write-5m","meter":"cache_write_5m_tokens","unit":"token","quantity":1000000,"price_usd":"0.100000"},
{"sku_id":"haiku:cache-write-1h","meter":"cache_write_1h_tokens","unit":"token","quantity":1000000,"price_usd":"0.160000"}],
"prompt_tiers":[{"above_prompt_tokens":100000,"input_price_usd":"0.100000","output_price_usd":"0.500000","cache_read_price_usd":"0.010000","cache_write_price_usd":"0.200000","cache_write_5m_price_usd":"0.125000","cache_write_1h_price_usd":"0.200000"}]},
{"id":"gpt-image-2.5","object":"model","available":true,"currency":"USD","pricing_skus":[
{"sku_id":"gpt-image-2.5:1k","meter":"output_images","unit":"image","quantity":1,"price_usd":"0.010000","constraints":{"tier":"1K"}},
{"sku_id":"gpt-image-2.5:2k","meter":"output_images","unit":"image","quantity":1,"price_usd":"0.010000","constraints":{"tier":"2K"}}]},
{"id":"grok-imagine-video-1.5","object":"model","available":true,"currency":"USD","pricing_skus":[
{"sku_id":"video:text","meter":"output_video","unit":"second","quantity":1,"price_usd":"0.009800","constraints":{"quality":"official","mode":"text_to_video","resolution":"720p","min_duration_seconds":1,"max_duration_seconds":15}},
{"sku_id":"video:image","meter":"output_video","unit":"second","quantity":1,"price_usd":"0.009800","constraints":{"quality":"official","mode":"image_to_video","resolution":"720p","min_duration_seconds":1,"max_duration_seconds":15}}]},
{"id":"deepseek-flash","object":"model","available":true,"currency":"USD","pricing_mode":"dynamic_discount","time_of_day":{"current_tier":"off_peak"},"pricing_skus":[
{"sku_id":"deepseek:input","meter":"input_tokens","unit":"token","quantity":1000000,"price_usd":"0.112000"},
{"sku_id":"deepseek:output","meter":"output_tokens","unit":"token","quantity":1000000,"price_usd":"0.448000"},
{"sku_id":"deepseek:cache","meter":"cache_read_tokens","unit":"token","quantity":1000000,"price_usd":"0.002240"}]}
]}`

const wokeyImagesFixture = `{"object":"list","data":[{"id":"gpt-image-2.5","pricing_skus":[{"tier":"1K","price_usd_per_image":0.01,"sizes":["1024x1024"]},{"tier":"2K","price_usd_per_image":0.01,"sizes":["2048x2048"]}]}]}`
const wokeyVideosFixture = `{"object":"list","data":[{"id":"grok-imagine-video-1.5","pricing_skus":[
{"variant_id":"video:text","quality":"official","mode":"text_to_video","resolution":"720p","min_duration_seconds":1,"max_duration_seconds":15,"price_usd_per_second":0.0098},
{"variant_id":"video:image","quality":"official","mode":"image_to_video","resolution":"720p","min_duration_seconds":1,"max_duration_seconds":15,"price_usd_per_second":0.0098}]}]}`

const wokeyTimeOfDayPricingFixture = `{"object":"list","data":[
{"id":"deepseek-flash","object":"model","available":true,"currency":"USD","pricing_mode":"dynamic_discount","pricing_skus":[
{"sku_id":"deepseek-flash:input_tokens","meter":"input_tokens","unit":"token","quantity":1000000,"price_usd":"0.224000"},
{"sku_id":"deepseek-flash:output_tokens","meter":"output_tokens","unit":"token","quantity":1000000,"price_usd":"0.896000"},
{"sku_id":"deepseek-flash:cache_read_tokens","meter":"cache_read_tokens","unit":"token","quantity":1000000,"price_usd":"0.004480"}],"time_of_day":{"current_tier":"peak","off_peak_multiplier":0.5,"peak_windows_utc":[{"start_hour":1,"end_hour":4},{"start_hour":6,"end_hour":10}],"tiers":{
"peak":{"input_price_usd":"0.224000","output_price_usd":"0.896000","cache_read_price_usd":"0.004480","cache_write_price_usd":null,"reference_input_price_usd":"0.280000","reference_output_price_usd":"1.120000","reference_cache_read_price_usd":"0.005600","reference_cache_write_price_usd":null},
"off_peak":{"input_price_usd":"0.112000","output_price_usd":"0.448000","cache_read_price_usd":"0.002240","cache_write_price_usd":null,"reference_input_price_usd":"0.140000","reference_output_price_usd":"0.560000","reference_cache_read_price_usd":"0.002800","reference_cache_write_price_usd":null}}}},
{"id":"deepseek-v4-flash","object":"model","available":true,"currency":"USD","pricing_mode":"dynamic_discount","pricing_skus":[
{"sku_id":"deepseek-v4-flash:input_tokens","meter":"input_tokens","unit":"token","quantity":1000000,"price_usd":"0.140000"},
{"sku_id":"deepseek-v4-flash:output_tokens","meter":"output_tokens","unit":"token","quantity":1000000,"price_usd":"0.560000"},
{"sku_id":"deepseek-v4-flash:cache_read_tokens","meter":"cache_read_tokens","unit":"token","quantity":1000000,"price_usd":"0.002800"}],"time_of_day":{"current_tier":"peak","off_peak_multiplier":0.5,"peak_windows_utc":[{"start_hour":1,"end_hour":4},{"start_hour":6,"end_hour":10}],"tiers":{
"peak":{"input_price_usd":"0.140000","output_price_usd":"0.560000","cache_read_price_usd":"0.002800"},
"off_peak":{"input_price_usd":"0.112000","output_price_usd":"0.448000","cache_read_price_usd":"0.002240"}}}},
{"id":"deepseek-v4-pro","object":"model","available":true,"currency":"USD","pricing_mode":"dynamic_discount","pricing_skus":[
{"sku_id":"deepseek-v4-pro:input_tokens","meter":"input_tokens","unit":"token","quantity":1000000,"price_usd":"0.660000"},
{"sku_id":"deepseek-v4-pro:output_tokens","meter":"output_tokens","unit":"token","quantity":1000000,"price_usd":"1.980000"},
{"sku_id":"deepseek-v4-pro:cache_read_tokens","meter":"cache_read_tokens","unit":"token","quantity":1000000,"price_usd":"0.022000"}],"time_of_day":{"current_tier":"peak","off_peak_multiplier":0.5,"peak_windows_utc":[{"start_hour":1,"end_hour":4},{"start_hour":6,"end_hour":10}],"tiers":{
"peak":{"input_price_usd":"0.660000","output_price_usd":"1.980000","cache_read_price_usd":"0.022000"},
"off_peak":{"input_price_usd":"0.528000","output_price_usd":"1.584000","cache_read_price_usd":"0.017600"}}}}
]}`

func parseWokeyFixture(t *testing.T) *wokeyCatalog {
	t.Helper()
	catalog, err := parseWokeyCatalog([]byte(wokeyPricingFixture), []byte(wokeyImagesFixture), []byte(wokeyVideosFixture))
	require.NoError(t, err)
	return catalog
}

func parseWokeyTimeOfDayFixture(t *testing.T, pricing string) *wokeyCatalog {
	t.Helper()
	catalog, err := parseWokeyCatalog([]byte(pricing), []byte(wokeyImagesFixture), []byte(wokeyVideosFixture))
	require.NoError(t, err)
	return catalog
}

func replaceWokeyModelTimeOfDay(t *testing.T, pricing string, modelID string, value *json.RawMessage) string {
	t.Helper()
	var envelope struct {
		Object string                       `json:"object"`
		Data   []map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(pricing), &envelope))
	found := false
	for _, model := range envelope.Data {
		var id string
		require.NoError(t, json.Unmarshal(model["id"], &id))
		if id != modelID {
			continue
		}
		found = true
		if value == nil {
			delete(model, "time_of_day")
		} else {
			model["time_of_day"] = *value
		}
	}
	require.True(t, found, "fixture contains target model")
	result, err := json.Marshal(envelope)
	require.NoError(t, err)
	return string(result)
}

func wokeyTimeOfDayEntryForTest(t *testing.T, modelID string, fx string) UnifiedGatewayRoutePricingEntry {
	t.Helper()
	catalog := parseWokeyTimeOfDayFixture(t, wokeyTimeOfDayPricingFixture)
	candidates, unsupported := buildWokeyPriceCards(catalog, decimal.RequireFromString(fx))
	require.Empty(t, unsupported[modelID])
	require.Len(t, candidates[modelID].Cards, 1)
	entry := candidates[modelID].Cards[0]
	entry.AccountID = 42
	entry.SourceCatalogSHA256 = strings.Repeat("d", 64)
	entry.SourceFetchedAt = routePricingTestTime()
	return entry
}

func TestParseWokeyCatalogAndBuildsTokenImageVideoCards(t *testing.T) {
	catalog := parseWokeyFixture(t)
	candidates, unsupported := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
	require.Empty(t, unsupported["claude-haiku-5-5"])
	require.Equal(t, "unsupported_time_of_day_tiers", unsupported["deepseek-flash"])

	haiku := candidates["claude-haiku-5-5"].Cards[0]
	require.Equal(t, float64(0.552), *haiku.TokenBasePrice.InputPerMillion)
	require.Equal(t, float64(2.76), *haiku.TokenBasePrice.OutputPerMillion)
	require.Equal(t, float64(0.0552), *haiku.TokenBasePrice.CacheReadPerMillion)
	require.Equal(t, float64(1.104), *haiku.TokenBasePrice.CacheWritePerMillion)
	require.NotNil(t, haiku.LongContextTokenBasePrice)
	require.Equal(t, float64(0.69), *haiku.LongContextTokenBasePrice.InputPerMillion)
	require.Len(t, haiku.SourceSKUs, 12, "the base and prompt tier retain every source meter")

	image := candidates["gpt-image-2.5"].Cards[0]
	require.Equal(t, UnifiedGatewayImagePricingFlatPerImage, image.ImagePricingMode)
	require.InDelta(t, 0.069, *image.UnitPrice, 1e-12)
	require.Len(t, image.SourceSKUs, 2)
	require.Equal(t, []string{"1024x1024"}, catalog.Images["gpt-image-2.5"].SKUs[0].Sizes)

	videos := candidates["grok-imagine-video-1.5"].Cards
	require.Len(t, videos, 15)
	var fiveSecond *UnifiedGatewayRoutePricingEntry
	for i := range videos {
		if videos[i].VideoDurationSeconds == 5 {
			fiveSecond = &videos[i]
		}
	}
	require.NotNil(t, fiveSecond)
	require.InDelta(t, 0.3381, *fiveSecond.UnitPrice, 1e-12)
	require.Len(t, fiveSecond.SourceSKUs, 2, "all equal-cost video variants contribute to the card")
	require.Equal(t, 1, videos[0].VideoDurationSeconds)
	require.Equal(t, 15, videos[len(videos)-1].VideoDurationSeconds)
}

func TestWokeyTimeOfDayCatalogBuildsExactDualTierCards(t *testing.T) {
	catalog := parseWokeyTimeOfDayFixture(t, wokeyTimeOfDayPricingFixture)
	candidates, unsupported := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
	require.Empty(t, unsupported)
	require.Len(t, candidates, 3)

	for model, want := range map[string]struct {
		peakInput, peakOutput, peakCacheRead float64
		offInput, offOutput, offCacheRead    float64
	}{
		"deepseek-flash":    {1.5456, 6.1824, 0.030912, 0.7728, 3.0912, 0.015456},
		"deepseek-v4-flash": {0.966, 3.864, 0.01932, 0.7728, 3.0912, 0.015456},
		"deepseek-v4-pro":   {4.554, 13.662, 0.1518, 3.6432, 10.9296, 0.12144},
	} {
		t.Run(model, func(t *testing.T) {
			require.Len(t, candidates[model].Cards, 1, "both tiers belong in one route card")
			card := candidates[model].Cards[0]
			require.Nil(t, card.TokenBasePrice, "a time-of-day card must not flatten into the static price field")
			require.NotNil(t, card.TimeOfDayTokenPrice)
			tod := card.TimeOfDayTokenPrice
			require.Equal(t, "peak", tod.CurrentTier)
			require.Equal(t, []UnifiedGatewayWokeyTimeOfDayWindow{{StartHour: 1, EndHour: 4}, {StartHour: 6, EndHour: 10}}, tod.PeakWindowsUTC)
			require.InDelta(t, want.peakInput, *tod.Peak.TokenBasePrice.InputPerMillion, 1e-12)
			require.InDelta(t, want.peakOutput, *tod.Peak.TokenBasePrice.OutputPerMillion, 1e-12)
			require.InDelta(t, want.peakCacheRead, *tod.Peak.TokenBasePrice.CacheReadPerMillion, 1e-12)
			require.InDelta(t, want.offInput, *tod.OffPeak.TokenBasePrice.InputPerMillion, 1e-12)
			require.InDelta(t, want.offOutput, *tod.OffPeak.TokenBasePrice.OutputPerMillion, 1e-12)
			require.InDelta(t, want.offCacheRead, *tod.OffPeak.TokenBasePrice.CacheReadPerMillion, 1e-12)
			require.Equal(t, "6.9", card.SourceFX)
			require.Equal(t, "0.140000", candidates["deepseek-v4-flash"].Cards[0].TimeOfDayTokenPrice.Peak.SourcePricesUSD["input_tokens"])
			require.Len(t, card.SourceSKUs, 3, "source SKUs must retain the actual current-tier identifiers")
		})
	}
}

func TestWokeyTimeOfDayCatalogFailsClosedPerModel(t *testing.T) {
	tests := []struct {
		name, body, modelID, want string
	}{
		{
			name:    "unknown time-of-day field",
			body:    strings.Replace(wokeyTimeOfDayPricingFixture, `"current_tier":"peak"`, `"current_tier":"peak","future_schedule":true`, 1),
			modelID: "deepseek-flash", want: "unsupported_time_of_day_schema",
		},
		{
			name:    "unknown nested tier price field",
			body:    strings.Replace(wokeyTimeOfDayPricingFixture, `"input_price_usd":"0.224000"`, `"input_price_usd":"0.224000","context_128k_input_price_usd":"0.1"`, 1),
			modelID: "deepseek-flash", want: "unsupported_time_of_day_tier_schema",
		},
		{
			name:    "active SKU mismatch",
			body:    strings.Replace(wokeyTimeOfDayPricingFixture, `"price_usd":"0.224000"`, `"price_usd":"0.225000"`, 1),
			modelID: "deepseek-flash", want: "time_of_day_current_sku_mismatch",
		},
		{
			name:    "overlapping UTC windows",
			body:    strings.Replace(wokeyTimeOfDayPricingFixture, `"start_hour":6`, `"start_hour":3`, 1),
			modelID: "deepseek-flash", want: "invalid_peak_windows",
		},
		{
			name:    "missing UTC window start hour",
			body:    strings.Replace(wokeyTimeOfDayPricingFixture, `"start_hour":1,`, ``, 1),
			modelID: "deepseek-flash", want: "invalid_peak_windows",
		},
		{
			name:    "reference price must be a decimal string",
			body:    strings.Replace(wokeyTimeOfDayPricingFixture, `"reference_input_price_usd":"0.280000"`, `"reference_input_price_usd":0.28`, 1),
			modelID: "deepseek-flash", want: "invalid_time_of_day_reference_price",
		},
		{
			name:    "new model remains outside allowlist",
			body:    strings.Replace(wokeyTimeOfDayPricingFixture, `"id":"deepseek-flash"`, `"id":"deepseek-flash-new"`, 1),
			modelID: "deepseek-flash-new", want: "unsupported_time_of_day_model",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := parseWokeyTimeOfDayFixture(t, tt.body)
			candidates, unsupported := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
			require.Empty(t, candidates[tt.modelID].Cards)
			require.Equal(t, tt.want, unsupported[tt.modelID])
		})
	}
}

func TestWokeyTimeOfDayTargetModelsNeverFlattenIncompleteCatalogData(t *testing.T) {
	missing := replaceWokeyModelTimeOfDay(t, wokeyTimeOfDayPricingFixture, "deepseek-flash", nil)
	null := json.RawMessage("null")
	nulled := replaceWokeyModelTimeOfDay(t, wokeyTimeOfDayPricingFixture, "deepseek-flash", &null)

	for _, tt := range []struct {
		name, body, want string
	}{
		{name: "missing field", body: missing, want: "unsupported_time_of_day_shape"},
		{name: "null field", body: nulled, want: "unsupported_time_of_day_schema"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			catalog := parseWokeyTimeOfDayFixture(t, tt.body)
			candidates, unsupported := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
			require.Empty(t, candidates["deepseek-flash"].Cards, "never treat current pricing_skus as an all-day static price")
			require.Equal(t, tt.want, unsupported["deepseek-flash"])
		})
	}
}

func TestWokeyTimeOfDayCurrentSKUsRejectConstraints(t *testing.T) {
	for _, tt := range []struct {
		name, constraints string
	}{
		{name: "known conditional constraint", constraints: `"constraints":{"tier":"1K"}`},
		{name: "unknown constraint field", constraints: `"constraints":{"future_dimension":"special"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := strings.Replace(wokeyTimeOfDayPricingFixture, `"price_usd":"0.224000"`, `"price_usd":"0.224000",`+tt.constraints, 1)
			catalog := parseWokeyTimeOfDayFixture(t, body)
			candidates, unsupported := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
			require.Empty(t, candidates["deepseek-flash"].Cards)
			require.Equal(t, "time_of_day_current_sku_mismatch", unsupported["deepseek-flash"])
		})
	}
}

func TestWokeyTimeOfDayUnsupportedRefreshPreservesLastGoodCardAndFX(t *testing.T) {
	lastGood := wokeyTimeOfDayEntryForTest(t, "deepseek-flash", "6.9")
	config := UnifiedGatewayRoutePricingConfig{TargetGroupID: 7, Revision: 1, Entries: []UnifiedGatewayRoutePricingEntry{lastGood}}
	badCatalog := parseWokeyTimeOfDayFixture(t, strings.Replace(wokeyTimeOfDayPricingFixture, `"price_usd":"0.224000"`, `"price_usd":"0.225000"`, 1))
	candidates, unsupported := buildWokeyPriceCards(badCatalog, decimal.RequireFromString("13.8"))
	merged, _ := mergeWokeyPriceCards(config, []int64{42}, candidates, unsupported, badCatalog, badCatalog.Hash, decimal.RequireFromString("13.8"))
	var kept *UnifiedGatewayRoutePricingEntry
	for i := range merged {
		if merged[i].AccountID == 42 && merged[i].Model == "deepseek-flash" && merged[i].Kind == UnifiedGatewayRoutePricingToken {
			kept = &merged[i]
			break
		}
	}
	require.NotNil(t, kept)
	require.Equal(t, "unsupported", kept.SyncState)
	require.Equal(t, "time_of_day_current_sku_mismatch", kept.SyncReason)
	require.Equal(t, lastGood.SourceFX, kept.SourceFX, "last-good formula and its FX basis must stay paired")
	require.Equal(t, lastGood.TimeOfDayTokenPrice.Peak.TokenBasePrice.InputPerMillion, kept.TimeOfDayTokenPrice.Peak.TokenBasePrice.InputPerMillion)
	require.Equal(t, lastGood.TimeOfDayTokenPrice.OffPeak.TokenBasePrice.InputPerMillion, kept.TimeOfDayTokenPrice.OffPeak.TokenBasePrice.InputPerMillion)
	_, err := buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{TargetGroupID: 7, Revision: 2, Entries: merged})
	require.NoError(t, err, "an unsupported model's preserved last-good card remains a valid snapshot")
}

func TestParseWokeyCatalogRejectsGlobalSchemaAndPriceTypeDrift(t *testing.T) {
	_, err := parseWokeyCatalog([]byte(`{"object":"list","data":[]}`), []byte(wokeyImagesFixture), []byte(wokeyVideosFixture))
	require.ErrorContains(t, err, "invalid_list_envelope")

	wrongType := strings.Replace(wokeyPricingFixture, `"price_usd":"0.080000"`, `"price_usd":0.08`, 1)
	_, err = parseWokeyCatalog([]byte(wrongType), []byte(wokeyImagesFixture), []byte(wokeyVideosFixture))
	require.ErrorContains(t, err, "invalid_json")

	wrongMediaPrice := strings.Replace(wokeyImagesFixture, `"price_usd_per_image":0.01`, `"price_usd_per_image":"0.01"`, 1)
	_, err = parseWokeyCatalog([]byte(wokeyPricingFixture), []byte(wrongMediaPrice), []byte(wokeyVideosFixture))
	require.ErrorContains(t, err, "invalid_json")

	wrongMediaSizes := strings.Replace(wokeyImagesFixture, `"sizes":["1024x1024"]`, `"sizes":"1024x1024"`, 1)
	_, err = parseWokeyCatalog([]byte(wokeyPricingFixture), []byte(wrongMediaSizes), []byte(wokeyVideosFixture))
	require.ErrorContains(t, err, "invalid_json")
}

func TestParseWokeyCatalogRejectsDuplicateIdentityCurrencyAndInvalidPrices(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"duplicate model id", strings.Replace(wokeyPricingFixture, `"id":"deepseek-flash"`, `"id":"claude-haiku-5-5"`, 1), "duplicate_model_id"},
		{"duplicate sku id", strings.Replace(wokeyPricingFixture, `"sku_id":"haiku:output"`, `"sku_id":"haiku:input"`, 1), "duplicate_sku_id"},
		{"unsupported currency", strings.Replace(wokeyPricingFixture, `"currency":"USD"`, `"currency":"CNY"`, 1), "unsupported_currency"},
		{"negative price", strings.Replace(wokeyPricingFixture, `"price_usd":"0.080000"`, `"price_usd":"-0.080000"`, 1), "invalid_sku_price"},
		{"non finite price", strings.Replace(wokeyPricingFixture, `"price_usd":"0.080000"`, `"price_usd":"NaN"`, 1), "invalid_sku_price"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseWokeyCatalog([]byte(tt.body), []byte(wokeyImagesFixture), []byte(wokeyVideosFixture))
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestWokeyMediaPriceCatalogMismatchDoesNotBuildCard(t *testing.T) {
	t.Run("image", func(t *testing.T) {
		images := strings.Replace(wokeyImagesFixture, `"price_usd_per_image":0.01`, `"price_usd_per_image":0.02`, 1)
		catalog, err := parseWokeyCatalog([]byte(wokeyPricingFixture), []byte(images), []byte(wokeyVideosFixture))
		require.NoError(t, err)
		candidates, unsupported := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
		require.NotContains(t, candidates, "gpt-image-2.5")
		require.Equal(t, "image_catalog_price_mismatch", unsupported["gpt-image-2.5"])
	})

	t.Run("video", func(t *testing.T) {
		videos := strings.Replace(wokeyVideosFixture, `"variant_id":"video:image","quality":"official","mode":"image_to_video","resolution":"720p","min_duration_seconds":1,"max_duration_seconds":15,"price_usd_per_second":0.0098`, `"variant_id":"video:image","quality":"official","mode":"image_to_video","resolution":"720p","min_duration_seconds":1,"max_duration_seconds":15,"price_usd_per_second":0.0108`, 1)
		catalog, err := parseWokeyCatalog([]byte(wokeyPricingFixture), []byte(wokeyImagesFixture), []byte(videos))
		require.NoError(t, err)
		candidates, unsupported := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
		require.Empty(t, candidates["grok-imagine-video-1.5"].Cards)
		require.Contains(t, unsupported["grok-imagine-video-1.5"], "video_catalog_price_mismatch")
	})

	t.Run("unknown image tier", func(t *testing.T) {
		images := strings.Replace(wokeyImagesFixture, `"tier":"2K"`, `"tier":"3K"`, 1)
		catalog, err := parseWokeyCatalog([]byte(wokeyPricingFixture), []byte(images), []byte(wokeyVideosFixture))
		require.NoError(t, err)
		candidates, unsupported := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
		require.Empty(t, candidates["gpt-image-2.5"].Cards)
		require.Equal(t, "unsupported_image_size", unsupported["gpt-image-2.5"])
	})

	t.Run("unknown concrete image size", func(t *testing.T) {
		images := strings.Replace(wokeyImagesFixture, `"sizes":["1024x1024"]`, `"sizes":["mystery-size"]`, 1)
		catalog, err := parseWokeyCatalog([]byte(wokeyPricingFixture), []byte(images), []byte(wokeyVideosFixture))
		require.NoError(t, err)
		candidates, unsupported := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
		require.Empty(t, candidates["gpt-image-2.5"].Cards)
		require.Equal(t, "unsupported_image_size", unsupported["gpt-image-2.5"])
	})

	t.Run("concrete image size must agree with its tier", func(t *testing.T) {
		images := strings.Replace(wokeyImagesFixture, `"sizes":["1024x1024"]`, `"sizes":["2048x2048"]`, 1)
		catalog, err := parseWokeyCatalog([]byte(wokeyPricingFixture), []byte(images), []byte(wokeyVideosFixture))
		require.NoError(t, err)
		candidates, unsupported := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
		require.Empty(t, candidates["gpt-image-2.5"].Cards)
		require.Equal(t, "unsupported_image_size", unsupported["gpt-image-2.5"])
	})

	t.Run("different image tier prices", func(t *testing.T) {
		pricing := strings.Replace(wokeyPricingFixture, `"sku_id":"gpt-image-2.5:2k","meter":"output_images","unit":"image","quantity":1,"price_usd":"0.010000"`, `"sku_id":"gpt-image-2.5:2k","meter":"output_images","unit":"image","quantity":1,"price_usd":"0.020000"`, 1)
		images := strings.Replace(wokeyImagesFixture, `"tier":"2K","price_usd_per_image":0.01`, `"tier":"2K","price_usd_per_image":0.02`, 1)
		catalog, err := parseWokeyCatalog([]byte(pricing), []byte(images), []byte(wokeyVideosFixture))
		require.NoError(t, err)
		candidates, unsupported := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
		require.Empty(t, candidates["gpt-image-2.5"].Cards)
		require.Contains(t, unsupported["gpt-image-2.5"], "unsupported_image_tier_price_difference")
	})

	t.Run("video range missing one second", func(t *testing.T) {
		pricing := strings.ReplaceAll(wokeyPricingFixture, `"min_duration_seconds":1`, `"min_duration_seconds":2`)
		videos := strings.ReplaceAll(wokeyVideosFixture, `"min_duration_seconds":1`, `"min_duration_seconds":2`)
		catalog, err := parseWokeyCatalog([]byte(pricing), []byte(wokeyImagesFixture), []byte(videos))
		require.NoError(t, err)
		candidates, unsupported := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
		require.Empty(t, candidates["grok-imagine-video-1.5"].Cards)
		require.Contains(t, unsupported["grok-imagine-video-1.5"], "unsupported_video_duration_range")
	})

	t.Run("video range extends beyond supported card range", func(t *testing.T) {
		pricing := strings.ReplaceAll(wokeyPricingFixture, `"max_duration_seconds":15`, `"max_duration_seconds":20`)
		videos := strings.ReplaceAll(wokeyVideosFixture, `"max_duration_seconds":15`, `"max_duration_seconds":20`)
		catalog, err := parseWokeyCatalog([]byte(pricing), []byte(wokeyImagesFixture), []byte(videos))
		require.NoError(t, err)
		candidates, unsupported := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
		require.Len(t, candidates["grok-imagine-video-1.5"].Cards, 15, "the source range covers all native 1–15 second cards")
		require.Empty(t, unsupported["grok-imagine-video-1.5"])
	})
}

func TestWokeyHighPrecisionJSONImagePriceFlowsThroughToCard(t *testing.T) {
	const precise = "0.013456789123"
	pricing := strings.ReplaceAll(wokeyPricingFixture, `"price_usd":"0.010000"`, `"price_usd":"`+precise+`"`)
	images := strings.ReplaceAll(wokeyImagesFixture, `"price_usd_per_image":0.01`, `"price_usd_per_image":`+precise)
	catalog, err := parseWokeyCatalog([]byte(pricing), []byte(images), []byte(wokeyVideosFixture))
	require.NoError(t, err)
	candidates, unsupported := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
	require.Empty(t, unsupported["gpt-image-2.5"])
	require.Len(t, candidates["gpt-image-2.5"].Cards, 1)
	card := candidates["gpt-image-2.5"].Cards[0]
	require.Equal(t, 0.092851845, *card.UnitPrice)
	require.Equal(t, precise, card.SourceSKUs[0].PriceUSD)
}

func TestWokeyOptionalCacheWriteMeterCanRemainUnconfigured(t *testing.T) {
	pricing := strings.Replace(wokeyPricingFixture, `,
{"sku_id":"haiku:cache-write-1h","meter":"cache_write_1h_tokens","unit":"token","quantity":1000000,"price_usd":"0.160000"}`, "", 1)
	pricing = strings.Replace(pricing, `,"cache_write_1h_price_usd":"0.200000"`, "", 1)
	catalog, err := parseWokeyCatalog([]byte(pricing), []byte(wokeyImagesFixture), []byte(wokeyVideosFixture))
	require.NoError(t, err)
	candidates, unsupported := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
	require.Empty(t, unsupported["claude-haiku-5-5"])
	card := candidates["claude-haiku-5-5"].Cards[0]
	require.Nil(t, card.TokenBasePrice.CacheWrite1hPerMillion, "a missing optional meter must not be invented as zero")
	require.Nil(t, card.LongContextTokenBasePrice.CacheWrite1hPerMillion)
	require.Len(t, card.SourceSKUs, 10, "source evidence only includes meters actually returned")
}

func TestManualizeWokeyPriceCardPreservesPricingAndClearsOnlySourceMetadata(t *testing.T) {
	catalog := parseWokeyFixture(t)
	candidates, _ := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
	entry := candidates["claude-haiku-5-5"].Cards[0]
	entry.AccountID = 42
	originalBase := *entry.TokenBasePrice
	originalLongContext := *entry.LongContextTokenBasePrice
	cfg := UnifiedGatewayRoutePricingConfig{TargetGroupID: 7, Revision: 4, Entries: []UnifiedGatewayRoutePricingEntry{entry}}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &routePricingSettingRepoFake{raw: string(raw), exists: true}
	svc := &SettingService{
		settingRepo:             repo,
		routePricingGroupRepo:   &routePricingGroupRepoFake{groups: []Group{{ID: 7, Name: "unified", Platform: PlatformComposite, Status: StatusActive}}},
		routePricingAccountRepo: &routePricingAccountRepoFake{accounts: []Account{{ID: 42, Name: "Wokey"}}},
	}

	state, err := svc.ManualizeUnifiedGatewayWokeyPriceCard(context.Background(), UnifiedGatewayWokeyManualizeRequest{
		ExpectedRevision: 4,
		Key:              UnifiedGatewayRoutePricingKey{AccountID: 42, Model: "claude-haiku-5-5", Kind: UnifiedGatewayRoutePricingToken},
	})
	require.NoError(t, err)
	require.EqualValues(t, 5, state.Saved.Revision)
	require.EqualValues(t, 5, state.ActiveRevision)
	require.Len(t, state.Saved.Entries, 1)
	manual := state.Saved.Entries[0]
	require.Equal(t, *entry.TokenBasePrice.InputPerMillion, *manual.TokenBasePrice.InputPerMillion)
	require.Equal(t, originalBase, *manual.TokenBasePrice)
	require.Equal(t, originalLongContext, *manual.LongContextTokenBasePrice)
	require.Empty(t, manual.Source)
	require.Empty(t, manual.SourceFX)
	require.Nil(t, manual.SourceFetchedAt)
	require.Empty(t, manual.SourceCatalogSHA256)
	require.Empty(t, manual.SourceSKUs)
	require.Empty(t, manual.SyncState)
	require.Empty(t, manual.SyncReason)
	require.NoError(t, validateUnifiedGatewayWokeyEntryMetadata(manual))
}

func TestWokeyPriceQuantizationPrecisionAndPositiveRoundToZero(t *testing.T) {
	value := decimal.RequireFromString("1.2345678914")
	got, err := quantizedWokeyPrice(value)
	require.NoError(t, err)
	require.Equal(t, 1.234567891, got)
	actual := decimal.NewFromFloat(got)
	require.LessOrEqual(t, actual.Sub(value).Abs().InexactFloat64(), 5e-10)

	_, err = quantizedWokeyPrice(decimal.RequireFromString("0.0000000004"))
	require.ErrorContains(t, err, "rounds to zero")
}

func TestWokeyPriceCardMergePreservesManualUnselectedAndLastGoodCards(t *testing.T) {
	catalog := parseWokeyFixture(t)
	fx := decimal.RequireFromString("6.9")
	candidates, unsupported := buildWokeyPriceCards(catalog, fx)
	oldNotReturned := UnifiedGatewayRoutePricingEntry{
		AccountID: 42, Model: "retired-model", Kind: UnifiedGatewayRoutePricingToken,
		TokenBasePrice: routePricingTokenBase(1, 1, 1, 0, 0, 0), Source: UnifiedGatewayWokeySource,
		SourceFX: "6.9", SourceFetchedAt: routePricingTestTime(), SourceCatalogSHA256: strings.Repeat("a", 64),
		SourceSKUs: []UnifiedGatewayWokeySourceSKU{{SKUID: "old", Meter: "input_tokens", Unit: "token", Quantity: "1000000", PriceUSD: "1"}}, SyncState: "current",
	}
	unselected := oldNotReturned
	unselected.AccountID = 43
	manualConflict := UnifiedGatewayRoutePricingEntry{AccountID: 42, Model: "claude-haiku-5-5", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePricingFloat(1)}
	cfg := UnifiedGatewayRoutePricingConfig{TargetGroupID: 7, Revision: 4, Entries: []UnifiedGatewayRoutePricingEntry{oldNotReturned, unselected, manualConflict}}
	entries, counts := mergeWokeyPriceCards(cfg, []int64{42}, candidates, unsupported, catalog, catalog.Hash, fx)

	require.GreaterOrEqual(t, counts.Conflict, 1)
	require.Equal(t, 1, counts.NotReturned)
	require.Contains(t, counts.Reasons, "retired-model:model_not_returned")
	var retainedNotReturned, retainedUnselected *UnifiedGatewayRoutePricingEntry
	for i := range entries {
		if entries[i].AccountID == 42 && entries[i].Model == "retired-model" {
			retainedNotReturned = &entries[i]
		}
		if entries[i].AccountID == 43 && entries[i].Model == "retired-model" {
			retainedUnselected = &entries[i]
		}
	}
	require.NotNil(t, retainedNotReturned)
	require.Equal(t, "not_returned", retainedNotReturned.SyncState)
	require.Equal(t, oldNotReturned.TokenBasePrice, retainedNotReturned.TokenBasePrice)
	require.Equal(t, oldNotReturned.SourceSKUs, retainedNotReturned.SourceSKUs)
	require.NotNil(t, retainedUnselected)
	require.Equal(t, "current", retainedUnselected.SyncState, "an unselected account is untouched")
	for _, entry := range entries {
		require.False(t, entry.AccountID == 42 && entry.Model == "claude-haiku-5-5" && entry.Source == UnifiedGatewayWokeySource,
			"the manual key conflict must not create a managed card")
	}
}

func TestWokeyClaudePriceCardsAreExcludedPerOpenAICompatibleAccount(t *testing.T) {
	catalog := parseWokeyFixture(t)
	claudeModel := catalog.Models["claude-haiku-5-5"]
	claudeModel.ID = "cursor-claude-opus-5"
	catalog.Models[claudeModel.ID] = claudeModel
	fx := decimal.RequireFromString("6.9")
	candidates, unsupported := buildWokeyPriceCards(catalog, fx)
	claudeCard := candidates["claude-haiku-5-5"].Cards[0]
	cursorClaudeCard := candidates["cursor-claude-opus-5"].Cards[0]

	managedClaude := func(accountID int64) UnifiedGatewayRoutePricingEntry {
		entry := claudeCard
		entry.AccountID = accountID
		entry.Source = UnifiedGatewayWokeySource
		entry.SyncState = "current"
		entry.SourceFX = "6.9"
		return entry
	}
	managedCursorClaude := cursorClaudeCard
	managedCursorClaude.AccountID = 42
	managedCursorClaude.Source = UnifiedGatewayWokeySource
	manualClaude := UnifiedGatewayRoutePricingEntry{
		AccountID: 42, Model: "claude-haiku-5-5", Kind: UnifiedGatewayRoutePricingToken,
		TokenBasePrice: routePricingTokenBase(1, 1, 1, 0, 0, 0),
	}
	unselectedClaude := managedClaude(45)

	accountsByID := map[int64]Account{
		42: {ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.wokey.ai/v1"}},
		43: {ID: 43, Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.wokey.ai/v1"}},
		44: {ID: 44, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.wokey.ai/v1"}},
	}
	cfg := UnifiedGatewayRoutePricingConfig{
		TargetGroupID: 7,
		Entries: []UnifiedGatewayRoutePricingEntry{
			managedClaude(42), managedClaude(43), managedClaude(44),
			managedCursorClaude, manualClaude, unselectedClaude,
		},
	}
	entries, _ := mergeWokeyPriceCardsForAccounts(cfg, []int64{42, 43, 44}, accountsByID, candidates, unsupported, catalog, catalog.Hash, fx)

	var openAIClaudeManaged, grokClaudeManaged, anthropicClaudeManaged bool
	var openAICursorClaudeManaged, grokCursorClaudeManaged, manualClaudePreserved, unselectedClaudePreserved bool
	for _, entry := range entries {
		switch {
		case entry.AccountID == 42 && entry.Model == "claude-haiku-5-5" && entry.Source == UnifiedGatewayWokeySource:
			openAIClaudeManaged = true
		case entry.AccountID == 43 && entry.Model == "claude-haiku-5-5" && entry.Source == UnifiedGatewayWokeySource:
			grokClaudeManaged = true
		case entry.AccountID == 44 && entry.Model == "claude-haiku-5-5" && entry.Source == UnifiedGatewayWokeySource:
			anthropicClaudeManaged = true
		case entry.AccountID == 42 && entry.Model == "cursor-claude-opus-5" && entry.Source == UnifiedGatewayWokeySource:
			openAICursorClaudeManaged = true
		case entry.AccountID == 43 && entry.Model == "cursor-claude-opus-5" && entry.Source == UnifiedGatewayWokeySource:
			grokCursorClaudeManaged = true
		case entry.AccountID == 42 && entry.Model == "claude-haiku-5-5" && entry.Source == "":
			manualClaudePreserved = true
		case entry.AccountID == 45 && entry.Model == "claude-haiku-5-5" && entry.Source == UnifiedGatewayWokeySource:
			unselectedClaudePreserved = true
		}
	}
	require.False(t, openAIClaudeManaged, "selected Wokey OpenAI routes must not receive managed native-Claude cards")
	require.False(t, grokClaudeManaged, "selected Wokey Grok routes must not receive managed native-Claude cards")
	require.True(t, anthropicClaudeManaged, "the native Wokey Anthropic route retains its Claude card")
	require.True(t, openAICursorClaudeManaged, "a non-Claude-prefix model containing claude remains eligible")
	require.True(t, grokCursorClaudeManaged, "a non-Claude-prefix model containing claude remains eligible")
	require.True(t, manualClaudePreserved, "price refresh does not take ownership of manual cards")
	require.True(t, unselectedClaudePreserved, "unselected account cards are untouched")
}

func TestWokeyImageCardDoesNotConflictWithManualDifferentKindsForSameModel(t *testing.T) {
	catalog := parseWokeyFixture(t)
	candidates, unsupported := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
	cfg := UnifiedGatewayRoutePricingConfig{TargetGroupID: 7, Entries: []UnifiedGatewayRoutePricingEntry{
		{AccountID: 42, Model: "gpt-image-2.5", Kind: UnifiedGatewayRoutePricingToken, TokenBasePrice: routePricingTokenBase(1, 1, 1, 0, 0, 0)},
		{AccountID: 42, Model: "gpt-image-2.5", Kind: UnifiedGatewayRoutePricingVideo, VideoResolution: "720p", VideoDurationSeconds: 5, UnitPrice: routePricingFloat(0.2)},
	}}
	entries, counts := mergeWokeyPriceCards(cfg, []int64{42}, candidates, unsupported, catalog, catalog.Hash, decimal.RequireFromString("6.9"))

	var managedImage bool
	for _, entry := range entries {
		if entry.AccountID == 42 && entry.Model == "gpt-image-2.5" && entry.Kind == UnifiedGatewayRoutePricingImage && entry.Source == UnifiedGatewayWokeySource {
			managedImage = true
		}
	}
	require.True(t, managedImage, "only an exact kind/spec manual key should conflict with a Wokey image card")
	require.Zero(t, counts.Conflict)
}

func TestWokeyFlatImageConflictSkipsOnlyCandidateAndPreservesSnapshot(t *testing.T) {
	catalog := parseWokeyFixture(t)
	candidates, unsupported := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
	manualImage := UnifiedGatewayRoutePricingEntry{
		AccountID: 42, Model: "gpt-image-2.5", Kind: UnifiedGatewayRoutePricingImage,
		ImageSize: "1K", ImageQuality: "high", UnitPrice: routePricingFloat(0.5),
	}
	cfg := UnifiedGatewayRoutePricingConfig{TargetGroupID: 7, Revision: 4, Entries: []UnifiedGatewayRoutePricingEntry{manualImage}}
	entries, counts := mergeWokeyPriceCards(cfg, []int64{42}, candidates, unsupported, catalog, catalog.Hash, decimal.RequireFromString("6.9"))

	require.Equal(t, 1, counts.Conflict)
	require.Contains(t, counts.Reasons, "gpt-image-2.5:manual_conflict")
	var retainedManual, managedImage, managedToken, managedVideo bool
	for _, entry := range entries {
		if entry.AccountID == 42 && entry.Model == "gpt-image-2.5" {
			if entry.Source == "" {
				retainedManual = entry.Kind == UnifiedGatewayRoutePricingImage && entry.ImageSize == "1K" && entry.ImageQuality == "high" && *entry.UnitPrice == 0.5
			}
			if entry.Source == UnifiedGatewayWokeySource && entry.Kind == UnifiedGatewayRoutePricingImage {
				managedImage = true
			}
		}
		managedToken = managedToken || entry.Source == UnifiedGatewayWokeySource && entry.Kind == UnifiedGatewayRoutePricingToken
		managedVideo = managedVideo || entry.Source == UnifiedGatewayWokeySource && entry.Kind == UnifiedGatewayRoutePricingVideo
	}
	require.True(t, retainedManual, "keep the conflicting manual card unchanged")
	require.False(t, managedImage, "skip only the incompatible flat image candidate")
	require.True(t, managedToken, "publish unrelated token cards from the same catalog")
	require.True(t, managedVideo, "publish unrelated video cards from the same catalog")

	_, err := buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{TargetGroupID: cfg.TargetGroupID, Revision: cfg.Revision + 1, Entries: entries})
	require.NoError(t, err, "a local image conflict must not invalidate the merged snapshot")
}

func TestRecalculateWokeyManagedCardUsesStoredRawSourceValues(t *testing.T) {
	catalog := parseWokeyFixture(t)
	candidates, _ := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
	entry := candidates["claude-haiku-5-5"].Cards[0]
	entry.AccountID = 42
	entry.SourceCatalogSHA256 = strings.Repeat("b", 64)
	updated, err := recalculateWokeyManagedEntry(entry, decimal.RequireFromString("13.8"))
	require.NoError(t, err)
	require.Equal(t, "13.8", updated.SourceFX)
	require.InDelta(t, 1.104, *updated.TokenBasePrice.InputPerMillion, 1e-12)
	require.InDelta(t, 1.38, *updated.LongContextTokenBasePrice.InputPerMillion, 1e-12)
	require.Equal(t, entry.SourceSKUs, updated.SourceSKUs)
}

func TestRecalculateWokeyTimeOfDayCardRepricesBothTiersFromRawUSD(t *testing.T) {
	entry := wokeyTimeOfDayEntryForTest(t, "deepseek-v4-flash", "6.9")
	updated, err := recalculateWokeyManagedEntry(entry, decimal.RequireFromString("13.8"))
	require.NoError(t, err)
	require.Equal(t, "13.8", updated.SourceFX)
	require.InDelta(t, 1.932, *updated.TimeOfDayTokenPrice.Peak.TokenBasePrice.InputPerMillion, 1e-12)
	require.InDelta(t, 1.5456, *updated.TimeOfDayTokenPrice.OffPeak.TokenBasePrice.InputPerMillion, 1e-12)
	require.InDelta(t, 7.728, *updated.TimeOfDayTokenPrice.Peak.TokenBasePrice.OutputPerMillion, 1e-12)
	require.InDelta(t, 6.1824, *updated.TimeOfDayTokenPrice.OffPeak.TokenBasePrice.OutputPerMillion, 1e-12)
	require.Equal(t, entry.TimeOfDayTokenPrice.Peak.SourcePricesUSD, updated.TimeOfDayTokenPrice.Peak.SourcePricesUSD)
	require.Equal(t, entry.TimeOfDayTokenPrice.OffPeak.SourcePricesUSD, updated.TimeOfDayTokenPrice.OffPeak.SourcePricesUSD)
	require.Equal(t, entry.TimeOfDayTokenPrice.PeakWindowsUTC, updated.TimeOfDayTokenPrice.PeakWindowsUTC)
	require.Equal(t, entry.SourceSKUs, updated.SourceSKUs)

	invalid := entry
	invalid.TimeOfDayTokenPrice = cloneUnifiedGatewayRoutePricingEntries([]UnifiedGatewayRoutePricingEntry{entry})[0].TimeOfDayTokenPrice
	invalid.TimeOfDayTokenPrice.OffPeak.SourcePricesUSD["input_tokens"] = ""
	failed, err := recalculateWokeyManagedEntry(invalid, decimal.RequireFromString("13.8"))
	require.Error(t, err)
	require.Equal(t, "6.9", failed.SourceFX)
	require.Equal(t, entry.TimeOfDayTokenPrice.Peak.TokenBasePrice.InputPerMillion, failed.TimeOfDayTokenPrice.Peak.TokenBasePrice.InputPerMillion)
	require.Equal(t, "", failed.TimeOfDayTokenPrice.OffPeak.SourcePricesUSD["input_tokens"])

	tooSmall := cloneUnifiedGatewayRoutePricingEntries([]UnifiedGatewayRoutePricingEntry{entry})[0]
	tooSmall.TimeOfDayTokenPrice.OffPeak.SourcePricesUSD["cache_read_tokens"] = "0.00000000001"
	_, err = recalculateWokeyManagedEntry(tooSmall, decimal.RequireFromString("13.8"))
	require.ErrorContains(t, err, "unsupported_precision")
}

func TestWokeyTimeOfDayFXUpdateIsAtomicForBothTiers(t *testing.T) {
	entry := wokeyTimeOfDayEntryForTest(t, "deepseek-v4-pro", "6.9")
	config := UnifiedGatewayRoutePricingConfig{
		Revision: 4, Entries: []UnifiedGatewayRoutePricingEntry{entry},
		WokeySync: &UnifiedGatewayWokeySyncConfig{Enabled: false, AccountIDs: []int64{42}, FX: "6.9", IntervalMinutes: 15, Status: UnifiedGatewayWokeySyncStatus{ManagedCardCount: 1, Reasons: []string{}}},
	}
	svc, _ := wokeyTestService(t, config)
	state, err := svc.UpdateUnifiedGatewayRoutePricing(context.Background(), UnifiedGatewayRoutePricingUpdate{
		ExpectedRevision: 4, TargetGroupID: 7, Entries: []UnifiedGatewayRoutePricingEntry{},
		WokeySync: &UnifiedGatewayWokeySyncConfig{Enabled: false, AccountIDs: []int64{42}, FX: "13.8", IntervalMinutes: 15},
	})
	require.NoError(t, err)
	require.EqualValues(t, 5, state.Saved.Revision)
	require.Equal(t, "13.8", state.Saved.Entries[0].SourceFX)
	require.InDelta(t, 9.108, *state.Saved.Entries[0].TimeOfDayTokenPrice.Peak.TokenBasePrice.InputPerMillion, 1e-12)
	require.InDelta(t, 7.2864, *state.Saved.Entries[0].TimeOfDayTokenPrice.OffPeak.TokenBasePrice.InputPerMillion, 1e-12)
	require.Equal(t, entry.TimeOfDayTokenPrice.Peak.SourcePricesUSD, state.Saved.Entries[0].TimeOfDayTokenPrice.Peak.SourcePricesUSD)
	require.Equal(t, entry.TimeOfDayTokenPrice.OffPeak.SourcePricesUSD, state.Saved.Entries[0].TimeOfDayTokenPrice.OffPeak.SourcePricesUSD)
	require.Equal(t, entry.TimeOfDayTokenPrice.PeakWindowsUTC, state.Saved.Entries[0].TimeOfDayTokenPrice.PeakWindowsUTC)

	broken := config
	broken.Entries = cloneUnifiedGatewayRoutePricingEntries(config.Entries)
	broken.Entries[0].TimeOfDayTokenPrice.OffPeak.SourcePricesUSD["input_tokens"] = ""
	raw, marshalErr := json.Marshal(broken)
	require.NoError(t, marshalErr)
	failedRepo := &routePricingSettingRepoFake{raw: string(raw), exists: true}
	failedSvc := &SettingService{
		settingRepo:             failedRepo,
		routePricingGroupRepo:   &routePricingGroupRepoFake{groups: []Group{{ID: 7, Name: "unified", Platform: PlatformComposite, Status: StatusActive}}},
		routePricingAccountRepo: &routePricingAccountRepoFake{accounts: []Account{{ID: 42, Name: "Wokey", Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": "https://api.wokey.ai/v1"}}}},
	}
	before := failedRepo.raw
	_, err = failedSvc.UpdateUnifiedGatewayRoutePricing(context.Background(), UnifiedGatewayRoutePricingUpdate{
		ExpectedRevision: 4, TargetGroupID: 7, Entries: []UnifiedGatewayRoutePricingEntry{},
		WokeySync: &UnifiedGatewayWokeySyncConfig{Enabled: false, AccountIDs: []int64{42}, FX: "13.8", IntervalMinutes: 15},
	})
	require.Error(t, err)
	require.Equal(t, before, failedRepo.raw, "a failure in one tier must leave persisted revision and both tiers unchanged")
}

func TestWokeyBaseURLScopeIsExactAndCredentialFree(t *testing.T) {
	for _, url := range []string{"https://api.wokey.ai", "https://api.wokey.ai/v1", "https://API.WOKEY.AI/v1/", "https://api.wokey.ai:443/v1"} {
		require.True(t, isWokeyBaseURL(url), url)
	}
	for _, url := range []string{"http://api.wokey.ai", "https://api.wokey.ai.evil.test", "https://api.wokey.ai/v2", "https://user@api.wokey.ai", "https://api.wokey.ai?x=1", "https://api.wokey.ai:444"} {
		require.False(t, isWokeyBaseURL(url), url)
	}
}

func TestWokeyHTTPFetcherReadsOnlyFixedPublicGETEndpoints(t *testing.T) {
	seen := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Method + " " + r.URL.Path
		switch r.URL.Path {
		case "/v1/models/pricing":
			_, _ = w.Write([]byte(wokeyPricingFixture))
		case "/v1/images/models":
			_, _ = w.Write([]byte(wokeyImagesFixture))
		case "/v1/videos/models":
			_, _ = w.Write([]byte(wokeyVideosFixture))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	fetcher := newWokeyHTTPFetcher()
	fetcher.baseURL = server.URL
	catalog, err := fetcher.Fetch(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, catalog.Hash)
	got := []string{<-seen, <-seen, <-seen}
	require.ElementsMatch(t, []string{"GET /v1/models/pricing", "GET /v1/images/models", "GET /v1/videos/models"}, got)
}

func TestWokeyHTTPFetcherRejectsHTTPFailuresRedirectsAndOversizedBodies(t *testing.T) {
	t.Run("non 2xx does not expose response body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("private upstream diagnostic"))
		}))
		defer server.Close()

		fetcher := newWokeyHTTPFetcher()
		fetcher.baseURL = server.URL
		_, err := fetcher.get(context.Background(), "/v1/models/pricing")
		var failure *wokeyPriceSyncFailure
		require.ErrorAs(t, err, &failure)
		require.Equal(t, "upstream_http_error", failure.Code)
		require.Equal(t, http.StatusServiceUnavailable, failure.HTTPStatus)
		require.NotContains(t, err.Error(), "private upstream diagnostic")
	})

	t.Run("redirect is not followed", func(t *testing.T) {
		var targetHits atomic.Int32
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			targetHits.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		defer target.Close()
		server := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
		defer server.Close()

		fetcher := newWokeyHTTPFetcher()
		fetcher.baseURL = server.URL
		_, err := fetcher.get(context.Background(), "/v1/models/pricing")
		var failure *wokeyPriceSyncFailure
		require.ErrorAs(t, err, &failure)
		require.Equal(t, "upstream_http_error", failure.Code)
		require.Equal(t, http.StatusFound, failure.HTTPStatus)
		require.Zero(t, targetHits.Load(), "the public catalog fetcher must never follow a redirect")
	})

	t.Run("body limit is enforced", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", wokeyCatalogBodyLimit+1)))
		}))
		defer server.Close()

		fetcher := newWokeyHTTPFetcher()
		fetcher.baseURL = server.URL
		_, err := fetcher.get(context.Background(), "/v1/models/pricing")
		var failure *wokeyPriceSyncFailure
		require.ErrorAs(t, err, &failure)
		require.Equal(t, "upstream_body_too_large", failure.Code)
	})

	t.Run("request timeout fails closed", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer server.Close()

		fetcher := newWokeyHTTPFetcher()
		fetcher.baseURL = server.URL
		fetcher.client.Timeout = 20 * time.Millisecond
		_, err := fetcher.get(context.Background(), "/v1/models/pricing")
		var failure *wokeyPriceSyncFailure
		require.ErrorAs(t, err, &failure)
		require.Equal(t, "upstream_unavailable", failure.Code)
	})

	t.Run("untrusted TLS certificate fails closed", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		fetcher := newWokeyHTTPFetcher()
		fetcher.baseURL = server.URL
		_, err := fetcher.get(context.Background(), "/v1/models/pricing")
		var failure *wokeyPriceSyncFailure
		require.ErrorAs(t, err, &failure)
		require.Equal(t, "upstream_unavailable", failure.Code)
	})
}

type wokeyStaticFetcher struct {
	catalog *wokeyCatalog
	err     error
}

func (f wokeyStaticFetcher) Fetch(context.Context) (*wokeyCatalog, error) { return f.catalog, f.err }

type wokeyFailReadAfterCASSuccessRepo struct {
	routePricingSettingRepoFake
	failRead bool
}

func (r *wokeyFailReadAfterCASSuccessRepo) GetValue(ctx context.Context, key string) (string, error) {
	if r.failRead {
		return "", errors.New("settings read failed after successful CAS")
	}
	return r.routePricingSettingRepoFake.GetValue(ctx, key)
}

func (r *wokeyFailReadAfterCASSuccessRepo) CompareAndSetValue(ctx context.Context, key string, expected *string, value string) (bool, error) {
	updated, err := r.routePricingSettingRepoFake.CompareAndSetValue(ctx, key, expected, value)
	if updated && err == nil {
		r.failRead = true
	}
	return updated, err
}

func wokeyTestService(t *testing.T, config UnifiedGatewayRoutePricingConfig) (*SettingService, *routePricingSettingRepoFake) {
	t.Helper()
	config.TargetGroupID = 7
	if config.Entries == nil {
		config.Entries = []UnifiedGatewayRoutePricingEntry{}
	}
	raw, err := json.Marshal(config)
	require.NoError(t, err)
	repo := &routePricingSettingRepoFake{raw: string(raw), exists: true}
	groupRepo := &routePricingGroupRepoFake{groups: []Group{{ID: 7, Name: "unified", Platform: PlatformComposite, Status: StatusActive}}}
	accountRepo := &routePricingAccountRepoFake{accounts: []Account{
		{ID: 42, Name: "Wokey", Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": "https://api.wokey.ai/v1", "api_key": "never-return-this"}},
		{ID: 43, Name: "Other provider", Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": "https://api.example.test/v1"}},
	}}
	svc := &SettingService{settingRepo: repo, routePricingGroupRepo: groupRepo, routePricingAccountRepo: accountRepo}
	require.NoError(t, svc.LoadUnifiedGatewayRoutePricingAtStartup(context.Background()))
	return svc, repo
}

func TestSyncWokeyPriceCatalogPublishesOnlySelectedAccountAndNeverReturnsCredentials(t *testing.T) {
	config := UnifiedGatewayRoutePricingConfig{TargetGroupID: 7, Revision: 1, Entries: []UnifiedGatewayRoutePricingEntry{}, WokeySync: &UnifiedGatewayWokeySyncConfig{
		Enabled: true, AccountIDs: []int64{42}, FX: "6.9", IntervalMinutes: 15,
		Status: UnifiedGatewayWokeySyncStatus{Reasons: []string{}},
	}}
	svc, repo := wokeyTestService(t, config)
	svc.wokeyCatalogFetcher = wokeyStaticFetcher{catalog: parseWokeyFixture(t)}

	state, err := svc.SyncWokeyPriceCatalog(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 2, state.Saved.Revision)
	require.EqualValues(t, 2, state.ActiveRevision)
	require.Equal(t, 17, state.Saved.WokeySync.Status.ManagedCardCount)
	require.Equal(t, 1, state.Saved.WokeySync.Status.UnsupportedCount)
	require.Len(t, state.Saved.Entries, 17)
	for _, entry := range state.Saved.Entries {
		require.EqualValues(t, 42, entry.AccountID)
		require.Equal(t, UnifiedGatewayWokeySource, entry.Source)
		require.Len(t, entry.SourceCatalogSHA256, 64)
	}
	responseJSON, err := json.Marshal(state)
	require.NoError(t, err)
	require.NotContains(t, string(responseJSON), "never-return-this")
	require.NotContains(t, string(repo.raw), "never-return-this")
}

func TestSyncWokeyPriceCatalogReturnsPublishedStateWithoutPostCASRead(t *testing.T) {
	config := UnifiedGatewayRoutePricingConfig{TargetGroupID: 7, Revision: 1, Entries: []UnifiedGatewayRoutePricingEntry{}, WokeySync: &UnifiedGatewayWokeySyncConfig{
		Enabled: true, AccountIDs: []int64{42}, FX: "6.9", IntervalMinutes: 15,
		Status: UnifiedGatewayWokeySyncStatus{Reasons: []string{}},
	}}
	raw, err := json.Marshal(config)
	require.NoError(t, err)
	repo := &wokeyFailReadAfterCASSuccessRepo{routePricingSettingRepoFake: routePricingSettingRepoFake{raw: string(raw), exists: true}}
	svc := &SettingService{
		settingRepo:             repo,
		routePricingGroupRepo:   &routePricingGroupRepoFake{groups: []Group{{ID: 7, Name: "unified", Platform: PlatformComposite, Status: StatusActive}}},
		routePricingAccountRepo: &routePricingAccountRepoFake{accounts: []Account{{ID: 42, Name: "Wokey", Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": "https://api.wokey.ai/v1"}}}},
		wokeyCatalogFetcher:     wokeyStaticFetcher{catalog: parseWokeyFixture(t)},
	}
	require.NoError(t, svc.LoadUnifiedGatewayRoutePricingAtStartup(context.Background()))

	state, err := svc.SyncWokeyPriceCatalog(context.Background())
	require.NoError(t, err, "a successful publication response must not depend on a fallible read after CAS")
	require.EqualValues(t, 2, state.Saved.Revision)
	require.EqualValues(t, 2, state.ActiveRevision)
	require.True(t, repo.failRead)
}

func TestWokeyGeneralRouteUpdatePreservesManagedCardsAndRepricesOnFXChange(t *testing.T) {
	catalog := parseWokeyFixture(t)
	candidates, _ := buildWokeyPriceCards(catalog, decimal.RequireFromString("6.9"))
	managed := candidates["claude-haiku-5-5"].Cards[0]
	managed.AccountID = 42
	managed.SourceCatalogSHA256 = strings.Repeat("c", 64)
	managed.SourceFetchedAt = routePricingTestTime()
	config := UnifiedGatewayRoutePricingConfig{
		Revision: 3, Entries: []UnifiedGatewayRoutePricingEntry{managed},
		WokeySync: &UnifiedGatewayWokeySyncConfig{Enabled: false, AccountIDs: []int64{42}, FX: "6.9", IntervalMinutes: 15, Status: UnifiedGatewayWokeySyncStatus{ManagedCardCount: 1, Reasons: []string{}}},
	}
	svc, repo := wokeyTestService(t, config)
	manual := UnifiedGatewayRoutePricingEntry{AccountID: 42, Model: "manual-model", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePricingFloat(0.8)}
	state, err := svc.UpdateUnifiedGatewayRoutePricing(context.Background(), UnifiedGatewayRoutePricingUpdate{
		ExpectedRevision: 3, TargetGroupID: 7, Entries: []UnifiedGatewayRoutePricingEntry{manual},
		WokeySync: &UnifiedGatewayWokeySyncConfig{Enabled: false, AccountIDs: []int64{42}, FX: "13.8", IntervalMinutes: 15},
	})
	require.NoError(t, err)
	require.EqualValues(t, 4, state.Saved.Revision)
	require.EqualValues(t, 4, state.ActiveRevision)
	require.Equal(t, 2, len(state.Saved.Entries))
	var persisted UnifiedGatewayRoutePricingConfig
	require.NoError(t, json.Unmarshal([]byte(repo.raw), &persisted))
	require.Equal(t, "13.8", persisted.WokeySync.FX)
	var updatedManaged *UnifiedGatewayRoutePricingEntry
	for i := range persisted.Entries {
		if persisted.Entries[i].Source == UnifiedGatewayWokeySource {
			updatedManaged = &persisted.Entries[i]
		}
	}
	require.NotNil(t, updatedManaged)
	require.InDelta(t, 1.104, *updatedManaged.TokenBasePrice.InputPerMillion, 1e-12)
	require.Equal(t, managed.SourceSKUs, updatedManaged.SourceSKUs)
	require.Equal(t, UnifiedGatewayWokeySource, state.Saved.Entries[1].Source)
}

func TestWokeyStartStopIsIdempotentAndDisabledConfigDoesNotFetch(t *testing.T) {
	svc, _ := wokeyTestService(t, UnifiedGatewayRoutePricingConfig{Revision: 1})
	fetcher := wokeyStaticFetcher{catalog: parseWokeyFixture(t)}
	svc.wokeyCatalogFetcher = fetcher
	require.NoError(t, svc.StartWokeyPriceSync())
	require.Nil(t, svc.wokeySyncWorker)
	svc.StopWokeyPriceSync()
	svc.StopWokeyPriceSync()
}

func TestWokeyStartLeavesNoWorkerForEmptyOrInvalidAccountScope(t *testing.T) {
	tests := []struct {
		name         string
		config       func() UnifiedGatewayRoutePricingConfig
		accounts     func(*routePricingAccountRepoFake)
		wantStartErr bool
	}{
		{
			name: "empty account scope",
			config: func() UnifiedGatewayRoutePricingConfig {
				cfg := wokeyEnabledSyncConfig()
				cfg.WokeySync.AccountIDs = []int64{}
				return cfg
			},
		},
		{
			name:         "account not schedulable",
			config:       wokeyEnabledSyncConfig,
			accounts:     func(repo *routePricingAccountRepoFake) { repo.accounts = nil },
			wantStartErr: true,
		},
		{
			name:   "account base url outside Wokey",
			config: wokeyEnabledSyncConfig,
			accounts: func(repo *routePricingAccountRepoFake) {
				repo.accounts[0].Credentials["base_url"] = "https://api.example.test/v1"
			},
			wantStartErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := wokeyTestService(t, tt.config())
			accountRepo := svc.routePricingAccountRepo.(*routePricingAccountRepoFake)
			if tt.accounts != nil {
				tt.accounts(accountRepo)
			}
			fetcher := &wokeyCountingFetcher{catalog: parseWokeyFixture(t), entered: make(chan int, 1)}
			svc.wokeyCatalogFetcher = fetcher
			var timerCalls atomic.Int32
			svc.wokeySyncTickerFactory = func(time.Duration) wokeyPriceSyncTicker {
				timerCalls.Add(1)
				return &wokeyManualTicker{ticks: make(chan time.Time)}
			}
			err := svc.StartWokeyPriceSync()
			if tt.wantStartErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			time.Sleep(25 * time.Millisecond)
			require.Zero(t, fetcher.calls.Load(), "invalid scope must not fetch the public catalog")
			require.Zero(t, timerCalls.Load(), "invalid scope must not create a refresh timer")
			svc.StopWokeyPriceSync()
		})
	}
}

func TestWokeyStartRejectsInvalidTargetGroupWithoutFetchOrTimer(t *testing.T) {
	svc, _ := wokeyTestService(t, wokeyEnabledSyncConfig())
	groupRepo := svc.routePricingGroupRepo.(*routePricingGroupRepoFake)
	groupRepo.groups = []Group{{ID: 8, Name: "wrong", Platform: PlatformComposite, Status: StatusActive}}
	fetcher := &wokeyCountingFetcher{catalog: parseWokeyFixture(t), entered: make(chan int, 1)}
	svc.wokeyCatalogFetcher = fetcher
	var timerCalls atomic.Int32
	svc.wokeySyncTickerFactory = func(time.Duration) wokeyPriceSyncTicker {
		timerCalls.Add(1)
		return &wokeyManualTicker{ticks: make(chan time.Time)}
	}
	require.ErrorIs(t, svc.StartWokeyPriceSync(), ErrUnifiedGatewayWokeySyncScope)
	time.Sleep(25 * time.Millisecond)
	require.Zero(t, fetcher.calls.Load())
	require.Zero(t, timerCalls.Load())
	svc.StopWokeyPriceSync()
}

type wokeyManualTicker struct {
	ticks   chan time.Time
	stopped atomic.Bool
}

func (t *wokeyManualTicker) Ticks() <-chan time.Time { return t.ticks }
func (t *wokeyManualTicker) Stop()                   { t.stopped.Store(true) }

type wokeyCountingFetcher struct {
	catalog    *wokeyCatalog
	calls      atomic.Int32
	entered    chan int
	blockFirst <-chan struct{}
}

func (f *wokeyCountingFetcher) Fetch(ctx context.Context) (*wokeyCatalog, error) {
	call := int(f.calls.Add(1))
	if f.entered != nil {
		f.entered <- call
	}
	if call == 1 && f.blockFirst != nil {
		select {
		case <-f.blockFirst:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.catalog, nil
}

type wokeyCancelFetcher struct {
	entered chan struct{}
}

func (f wokeyCancelFetcher) Fetch(ctx context.Context) (*wokeyCatalog, error) {
	close(f.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

func wokeyEnabledSyncConfig() UnifiedGatewayRoutePricingConfig {
	return UnifiedGatewayRoutePricingConfig{
		Revision: 1,
		Entries:  []UnifiedGatewayRoutePricingEntry{},
		WokeySync: &UnifiedGatewayWokeySyncConfig{
			Enabled: true, AccountIDs: []int64{42}, FX: "6.9", IntervalMinutes: 5,
			Status: UnifiedGatewayWokeySyncStatus{Reasons: []string{}},
		},
	}
}

func TestWokeyWorkerRunsImmediatelyAndOnTickWithoutOverlappingRefresh(t *testing.T) {
	svc, _ := wokeyTestService(t, wokeyEnabledSyncConfig())
	releaseFirstFetch := make(chan struct{})
	fetcher := &wokeyCountingFetcher{catalog: parseWokeyFixture(t), entered: make(chan int, 4), blockFirst: releaseFirstFetch}
	ticker := &wokeyManualTicker{ticks: make(chan time.Time, 2)}
	tickerIntervals := make(chan time.Duration, 1)
	svc.wokeyCatalogFetcher = fetcher
	svc.wokeySyncTickerFactory = func(interval time.Duration) wokeyPriceSyncTicker {
		tickerIntervals <- interval
		return ticker
	}

	require.NoError(t, svc.StartWokeyPriceSync())
	select {
	case interval := <-tickerIntervals:
		require.Equal(t, 5*time.Minute, interval)
	case <-time.After(time.Second * 2):
		t.Fatal("worker did not create its periodic ticker")
	}
	select {
	case call := <-fetcher.entered:
		require.Equal(t, 1, call, "the worker starts with an immediate refresh")
	case <-time.After(time.Second * 2):
		t.Fatal("worker did not start its immediate refresh")
	}
	_, err := svc.SyncWokeyPriceCatalog(context.Background())
	require.ErrorIs(t, err, ErrUnifiedGatewayWokeySyncBusy, "manual refresh must not overlap the worker fetch")
	require.EqualValues(t, 1, fetcher.calls.Load())

	close(releaseFirstFetch)
	require.Eventually(t, func() bool {
		state := svc.ActiveUnifiedGatewayRoutePricing()
		return state != nil && state.Revision == 2
	}, time.Second*2, time.Millisecond*10, "the first refresh publishes before the scheduled tick")
	ticker.ticks <- time.Now()
	select {
	case call := <-fetcher.entered:
		require.Equal(t, 2, call, "one timer tick starts one refresh")
	case <-time.After(time.Second * 2):
		t.Fatal("worker did not start the refresh after the timer tick")
	}
	require.Eventually(t, func() bool {
		state := svc.ActiveUnifiedGatewayRoutePricing()
		return state != nil && state.Revision == 3
	}, time.Second*2, time.Millisecond*10, "the periodic refresh publishes")

	_, err = svc.UpdateUnifiedGatewayRoutePricing(context.Background(), UnifiedGatewayRoutePricingUpdate{
		ExpectedRevision: 3,
		TargetGroupID:    7,
		Entries:          []UnifiedGatewayRoutePricingEntry{},
		WokeySync:        &UnifiedGatewayWokeySyncConfig{Enabled: false, AccountIDs: []int64{42}, FX: "6.9", IntervalMinutes: 5},
	})
	require.NoError(t, err)
	require.Nil(t, svc.wokeySyncWorker, "disabling sync stops and joins the existing worker")
	require.True(t, ticker.stopped.Load())
	ticker.ticks <- time.Now()
	require.EqualValues(t, 2, fetcher.calls.Load(), "a disabled worker must not react to later ticks")
}

func TestWokeyWorkerStopCancelsInflightFetchAndWaitsForExit(t *testing.T) {
	svc, _ := wokeyTestService(t, wokeyEnabledSyncConfig())
	entered := make(chan struct{})
	svc.wokeyCatalogFetcher = wokeyCancelFetcher{entered: entered}
	ticker := &wokeyManualTicker{ticks: make(chan time.Time, 1)}
	svc.wokeySyncTickerFactory = func(time.Duration) wokeyPriceSyncTicker { return ticker }
	require.NoError(t, svc.StartWokeyPriceSync())
	select {
	case <-entered:
	case <-time.After(time.Second * 2):
		t.Fatal("worker did not start its immediate refresh")
	}

	stopped := make(chan struct{})
	go func() {
		svc.StopWokeyPriceSync()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second * 2):
		t.Fatal("StopWokeyPriceSync did not cancel and join the in-flight fetch")
	}
	require.Nil(t, svc.wokeySyncWorker)
	require.True(t, ticker.stopped.Load())
}

func TestWokeyWorkerReconfiguresOnIntervalUpdate(t *testing.T) {
	svc, _ := wokeyTestService(t, wokeyEnabledSyncConfig())
	fetcher := &wokeyCountingFetcher{catalog: parseWokeyFixture(t), entered: make(chan int, 4)}
	tickers := []*wokeyManualTicker{{ticks: make(chan time.Time, 1)}, {ticks: make(chan time.Time, 1)}}
	intervals := make(chan time.Duration, 2)
	var factoryCalls atomic.Int32
	svc.wokeyCatalogFetcher = fetcher
	svc.wokeySyncTickerFactory = func(interval time.Duration) wokeyPriceSyncTicker {
		index := int(factoryCalls.Add(1)) - 1
		intervals <- interval
		if index >= len(tickers) {
			return &wokeyManualTicker{ticks: make(chan time.Time, 1)}
		}
		return tickers[index]
	}

	require.NoError(t, svc.StartWokeyPriceSync())
	select {
	case interval := <-intervals:
		require.Equal(t, 5*time.Minute, interval)
	case <-time.After(2 * time.Second):
		t.Fatal("initial worker did not create its ticker")
	}
	select {
	case call := <-fetcher.entered:
		require.Equal(t, 1, call)
	case <-time.After(2 * time.Second):
		t.Fatal("initial worker did not run its immediate refresh")
	}
	require.Eventually(t, func() bool {
		state := svc.ActiveUnifiedGatewayRoutePricing()
		return state != nil && state.Revision == 2
	}, 2*time.Second, 10*time.Millisecond)

	_, err := svc.UpdateUnifiedGatewayRoutePricing(context.Background(), UnifiedGatewayRoutePricingUpdate{
		ExpectedRevision: 2,
		TargetGroupID:    7,
		Entries:          []UnifiedGatewayRoutePricingEntry{},
		WokeySync:        &UnifiedGatewayWokeySyncConfig{Enabled: true, AccountIDs: []int64{42}, FX: "6.9", IntervalMinutes: 10},
	})
	require.NoError(t, err)
	require.True(t, tickers[0].stopped.Load(), "the prior interval worker is stopped and joined before replacement")
	select {
	case interval := <-intervals:
		require.Equal(t, 10*time.Minute, interval)
	case <-time.After(2 * time.Second):
		t.Fatal("updated worker did not create a ticker with the new interval")
	}
	select {
	case call := <-fetcher.entered:
		require.Equal(t, 2, call, "the replacement worker performs one immediate refresh")
	case <-time.After(2 * time.Second):
		t.Fatal("replacement worker did not run its immediate refresh")
	}
	require.Eventually(t, func() bool {
		state := svc.ActiveUnifiedGatewayRoutePricing()
		return state != nil && state.Revision == 4
	}, 2*time.Second, 10*time.Millisecond)
	require.EqualValues(t, 2, factoryCalls.Load(), "one old worker is replaced by exactly one new worker")

	svc.StopWokeyPriceSync()
	require.True(t, tickers[1].stopped.Load())
}

type wokeySequenceFetcher struct {
	results []struct {
		catalog *wokeyCatalog
		err     error
	}
	calls atomic.Int32
}

func (f *wokeySequenceFetcher) Fetch(context.Context) (*wokeyCatalog, error) {
	index := int(f.calls.Add(1)) - 1
	if index >= len(f.results) {
		return nil, errors.New("unexpected extra Wokey catalog fetch")
	}
	return f.results[index].catalog, f.results[index].err
}

func TestWokeyFailedRefreshPreservesLastGoodCardsAndSuccessMetadata(t *testing.T) {
	svc, _ := wokeyTestService(t, wokeyEnabledSyncConfig())
	fetcher := &wokeySequenceFetcher{results: []struct {
		catalog *wokeyCatalog
		err     error
	}{
		{catalog: parseWokeyFixture(t)},
		{err: &wokeyPriceSyncFailure{Code: "catalog_unavailable", HTTPStatus: http.StatusServiceUnavailable}},
	}}
	svc.wokeyCatalogFetcher = fetcher

	first, err := svc.SyncWokeyPriceCatalog(context.Background())
	require.NoError(t, err)
	require.NotNil(t, first.Saved.WokeySync.Status.LastSuccessAt)
	lastSuccess := *first.Saved.WokeySync.Status.LastSuccessAt
	cardsJSON, err := json.Marshal(first.Saved.Entries)
	require.NoError(t, err)

	_, err = svc.SyncWokeyPriceCatalog(context.Background())
	require.Error(t, err)
	active := svc.ActiveUnifiedGatewayRoutePricing()
	require.NotNil(t, active)
	require.Equal(t, first.Saved.WokeySync.Status.CatalogSHA256, active.WokeySync.Status.CatalogSHA256)
	require.Equal(t, first.Saved.WokeySync.Status.ManagedCardCount, active.WokeySync.Status.ManagedCardCount)
	require.Equal(t, lastSuccess, *active.WokeySync.Status.LastSuccessAt)
	require.Equal(t, "catalog_unavailable", active.WokeySync.Status.LastErrorCode)
	require.NotNil(t, active.WokeySync.Status.LastAttemptAt)
	require.False(t, active.WokeySync.Status.LastAttemptAt.Before(*active.WokeySync.Status.LastSuccessAt))
	activeCardsJSON, err := json.Marshal(active.Entries)
	require.NoError(t, err)
	require.JSONEq(t, string(cardsJSON), string(activeCardsJSON), "a failed fetch must not partially replace last-good cards")
}

func TestWokeyRefreshDoesNotOverwriteConcurrentAdminDisable(t *testing.T) {
	svc, repo := wokeyTestService(t, wokeyEnabledSyncConfig())
	entered := make(chan int, 1)
	releaseFetch := make(chan struct{})
	svc.wokeyCatalogFetcher = &wokeyCountingFetcher{catalog: parseWokeyFixture(t), entered: entered, blockFirst: releaseFetch}
	refreshDone := make(chan error, 1)
	go func() {
		_, err := svc.SyncWokeyPriceCatalog(context.Background())
		refreshDone <- err
	}()
	select {
	case call := <-entered:
		require.Equal(t, 1, call)
	case <-time.After(2 * time.Second):
		t.Fatal("manual refresh did not begin its catalog fetch")
	}

	adminState, err := svc.UpdateUnifiedGatewayRoutePricing(context.Background(), UnifiedGatewayRoutePricingUpdate{
		ExpectedRevision: 1,
		TargetGroupID:    7,
		Entries:          []UnifiedGatewayRoutePricingEntry{},
		WokeySync:        &UnifiedGatewayWokeySyncConfig{Enabled: false, AccountIDs: []int64{42}, FX: "6.9", IntervalMinutes: 5},
	})
	require.NoError(t, err)
	require.False(t, adminState.Saved.WokeySync.Enabled)
	close(releaseFetch)
	select {
	case err = <-refreshDone:
		require.ErrorIs(t, err, ErrUnifiedGatewayWokeySyncDisabled)
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not finish after the catalog fetch was released")
	}

	var persisted UnifiedGatewayRoutePricingConfig
	require.NoError(t, json.Unmarshal([]byte(repo.raw), &persisted))
	require.False(t, persisted.WokeySync.Enabled, "the refresh must preserve the newer admin configuration")
	require.Empty(t, persisted.Entries, "an in-flight refresh must not publish cards after sync was disabled")
}

type wokeyConcurrentAdminEditRepo struct {
	*routePricingSettingRepoFake
	injected bool
}

func (r *wokeyConcurrentAdminEditRepo) CompareAndSetValue(ctx context.Context, key string, expected *string, value string) (bool, error) {
	if !r.injected {
		r.injected = true
		var concurrent UnifiedGatewayRoutePricingConfig
		if err := json.Unmarshal([]byte(r.raw), &concurrent); err != nil {
			return false, err
		}
		concurrent.Revision++
		concurrent.WokeySync.FX = "13.8"
		concurrent.Entries = append(concurrent.Entries, UnifiedGatewayRoutePricingEntry{
			AccountID: 43, Model: "manual-preserve", Kind: UnifiedGatewayRoutePricingToken,
			Multiplier: routePricingFloat(1),
		})
		updated, err := json.Marshal(concurrent)
		if err != nil {
			return false, err
		}
		r.raw = string(updated)
		return false, nil
	}
	return r.routePricingSettingRepoFake.CompareAndSetValue(ctx, key, expected, value)
}

func TestWokeyCASConflictReReadsAndPreservesNewAdminSettings(t *testing.T) {
	config := wokeyEnabledSyncConfig()
	svc, baseRepo := wokeyTestService(t, config)
	concurrentRepo := &wokeyConcurrentAdminEditRepo{routePricingSettingRepoFake: baseRepo}
	svc.settingRepo = concurrentRepo
	svc.wokeyCatalogFetcher = wokeyStaticFetcher{catalog: parseWokeyFixture(t)}

	state, err := svc.SyncWokeyPriceCatalog(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 3, state.Saved.Revision, "sync retries once on a compare-and-set collision")
	require.EqualValues(t, 3, state.ActiveRevision)
	require.Equal(t, "13.8", state.Saved.WokeySync.FX, "the retry uses the latest admin FX")
	require.Contains(t, state.Saved.Entries, UnifiedGatewayRoutePricingEntry{
		AccountID: 43, Model: "manual-preserve", Kind: UnifiedGatewayRoutePricingToken,
		Multiplier: routePricingFloat(1),
	})
	var haiku *UnifiedGatewayRoutePricingEntry
	for i := range state.Saved.Entries {
		if state.Saved.Entries[i].AccountID == 42 && state.Saved.Entries[i].Model == "claude-haiku-5-5" {
			haiku = &state.Saved.Entries[i]
			break
		}
	}
	require.NotNil(t, haiku)
	require.InDelta(t, 1.104, *haiku.TokenBasePrice.InputPerMillion, 1e-12, "the retry recalculates managed prices at the new FX")
	var persisted UnifiedGatewayRoutePricingConfig
	require.NoError(t, json.Unmarshal([]byte(baseRepo.raw), &persisted))
	require.EqualValues(t, 3, persisted.Revision, "the persisted setting uses the retried revision")
	require.Equal(t, "13.8", persisted.WokeySync.FX)
	require.Contains(t, persisted.Entries, UnifiedGatewayRoutePricingEntry{
		AccountID: 43, Model: "manual-preserve", Kind: UnifiedGatewayRoutePricingToken,
		Multiplier: routePricingFloat(1),
	})
}

func routePricingTestTime() *time.Time {
	value := time.Date(2026, time.October, 9, 0, 0, 0, 0, time.UTC)
	return &value
}
