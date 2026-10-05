package repository

import (
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestUnifiedGatewayAdminMigrationsAreOrderedUniqueAndPhase5Scoped(t *testing.T) {
	files, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err)
	sort.Strings(files)

	const projection = "245_unified_gateway_admin_projection.sql"
	const config = "246_unified_gateway_admin_config.sql"
	require.Contains(t, files, projection)
	require.Contains(t, files, config)
	require.Equal(t, projection, files[len(files)-2])
	require.Equal(t, config, files[len(files)-1])

	seen := make(map[string]struct{}, len(files))
	for _, name := range files {
		_, duplicate := seen[name]
		require.False(t, duplicate, "duplicate full migration filename %q", name)
		seen[name] = struct{}{}
	}

	for _, name := range []string{projection, config} {
		content, err := fs.ReadFile(migrations.FS, name)
		require.NoError(t, err)
		lower := strings.ToLower(string(content))
		for _, excluded := range []string{
			"create table if not exists unified_route_price_snapshots",
			"create table if not exists unified_gateway_charge_ledger",
			"create table if not exists unified_gateway_recovery_tasks",
			"alter table users",
			"frozen_balance",
			"unified_gateway_recovery_v1",
		} {
			require.NotContains(t, lower, excluded, "%s contains out-of-phase DDL %q", name, excluded)
		}
	}
}

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
