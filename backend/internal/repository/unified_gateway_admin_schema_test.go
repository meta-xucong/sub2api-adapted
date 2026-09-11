package repository

import (
	"testing"

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
