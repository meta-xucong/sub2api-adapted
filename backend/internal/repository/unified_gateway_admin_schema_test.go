package repository

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestNormalizeUnifiedGatewaySchemaPredicate(t *testing.T) {
	require.Equal(t, "lifecycle<>'archived'", normalizeUnifiedGatewaySchemaPredicate(`((lifecycle)::text <> 'archived'::text)`))
	require.Equal(t, "unified_config_idisnull", normalizeUnifiedGatewaySchemaPredicate(`(unified_config_id IS NULL)`))
	require.Equal(t, "unified_config_idisnotnull", normalizeUnifiedGatewaySchemaPredicate(`(unified_config_id IS NOT NULL)`))
}

func TestNormalizeUnifiedGatewaySchemaSQLPreservesColumnOrder(t *testing.T) {
	definition := normalizeUnifiedGatewaySchemaSQL(`CREATE UNIQUE INDEX idx ON public.unified_route_targets USING btree (unified_config_id, unified_lane_id, public_model, endpoint, provider_identity)`)
	require.Contains(t, definition, "(unified_config_id,unified_lane_id,public_model,endpoint,provider_identity)")
	require.NotContains(t, definition, "(public_model,endpoint,unified_config_id,unified_lane_id,provider_identity)")
}

func TestMaterializedUnifiedRateRulePreservesAdminPricingShape(t *testing.T) {
	profile := service.UnifiedGatewayPricingProfile{
		ID: "profile_1", Version: "7", BillingMode: "image", RateBasis: "image",
		BasePriceSemantics: "provider_base", ProviderBaseUnitPrice: stringPointer("0.01000000"),
		ManualUpstreamMultiplier: stringPointer("1.250000"), UserMarkupMultiplier: stringPointer("1.200000"),
		MinimumCharge: stringPointer("0.05000000"), Precision: 8,
		ManualPricingRules: &service.UnifiedGatewayManualRule{FormulaID: "flat_unit_price", Unit: "image", UnitPrice: stringPointer("0.08000000")},
	}
	raw, err := materializedUnifiedRateRuleJSON(profile)
	require.NoError(t, err)

	var rule service.UnifiedRateRule
	require.NoError(t, json.Unmarshal(raw, &rule))
	require.Equal(t, profile.ID, rule.ProfileID)
	require.Equal(t, service.UnifiedRateBasisImage, rule.UpstreamRateBasis)
	require.Equal(t, 8, rule.RoundingPrecision)
	require.Equal(t, 0.01, *rule.ProviderBaseUnitPrice)
	require.Equal(t, 1.25, *rule.ManualUpstreamMultiplier)
	require.Equal(t, 0.05, *rule.MinimumCharge)
}

func TestUnifiedGatewayAdvisoryLockKeyIsPrintableAndDeterministic(t *testing.T) {
	first := unifiedGatewayAdvisoryLockKey("1", "draft.create", "", "debug-key")
	second := unifiedGatewayAdvisoryLockKey("1", "draft.create", "", "debug-key")
	other := unifiedGatewayAdvisoryLockKey("1", "draft.create", "", "other-key")

	require.Equal(t, first, second)
	require.NotEqual(t, first, other)
	require.NotContains(t, first, "\x00")
	require.True(t, strings.HasPrefix(first, "sha256:"))
}

func TestFallbackReasonFitsRuntimeCatalogColumn(t *testing.T) {
	longReason := strings.Repeat("上游价格需要人工确认；", 20)
	got := fallbackReason(&longReason)

	require.Len(t, []rune(got), 128)
	require.Equal(t, []rune(longReason)[:128], []rune(got))
	require.Equal(t, "", fallbackReason(nil))
}

func stringPointer(value string) *string { return &value }
