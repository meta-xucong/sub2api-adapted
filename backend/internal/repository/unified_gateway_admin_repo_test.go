package repository

import (
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUnifiedGatewayAdminRepoIDHelpers(t *testing.T) {
	for _, input := range []string{"42", "ag_42"} {
		got, err := parseNumeric(input, "ag")
		require.NoError(t, err)
		require.EqualValues(t, 42, got)
	}
	if _, err := parseNumeric("acct_42", "ag"); err == nil {
		t.Fatal("expected mismatched prefix to be rejected by numeric parsing")
	}
	require.Equal(t, "ag_42", opaqueNumeric("ag", 42))
	require.EqualValues(t, 3, parseVersion("3"))
	require.EqualValues(t, 1, parseVersion("invalid"))
	require.EqualValues(t, 0, numericSourceGroup(""))
	require.EqualValues(t, 42, numericSourceGroup("ag_42"))
}

func TestUnifiedGatewayAdminRepoIdempotencyLockKeyPreservesFieldBoundaries(t *testing.T) {
	first := unifiedGatewayAdvisoryLockKey("ab", "c", "d", "e")
	second := unifiedGatewayAdvisoryLockKey("a", "bc", "d", "e")
	require.NotEqual(t, first, second)
	require.True(t, strings.HasPrefix(first, "sha256:"))
	require.Len(t, strings.TrimPrefix(first, "sha256:"), 64)
}

func TestUnifiedGatewayAdminRepoMaterializedRateRuleKeepsDecimalStrings(t *testing.T) {
	providerPrice, userPrice := "0.0000123400", "0.0000199999"
	raw, err := materializedUnifiedRateRuleJSON(service.UnifiedGatewayPricingProfile{
		ID: "profile-one", Version: "7", BillingMode: "token", RateBasis: "per_token",
		ProviderBaseUnitPrice: &providerPrice, FinalUserUnitPrice: &userPrice, Precision: 10,
	})
	require.NoError(t, err)
	require.Contains(t, string(raw), `"provider_base_unit_price":"0.0000123400"`)
	require.Contains(t, string(raw), `"final_user_unit_price":"0.0000199999"`)
}
