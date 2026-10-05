//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnifiedGatewayAdminSchemaReadyWithoutPhase6Objects(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()

	readiness, err := checkUnifiedGatewayAdminSchema(ctx, tx)
	require.NoError(t, err)
	require.True(t, readiness.Ready, "missing: %v", readiness.Missing)
	require.Equal(t, unifiedGatewayAdminSchemaVersion, readiness.Version)
	require.Empty(t, readiness.Missing)

	// TestMain starts a fresh PostgreSQL database and applies the complete target
	// migration set, including Phase 5. No Phase 6 migrations are in this target.
	// The official v0.2.13 migration 160 independently adds users.frozen_balance;
	// remove that official column in this rollback-only transaction so this test
	// also proves Phase 5 readiness does not depend on it.
	_, err = tx.ExecContext(ctx, "ALTER TABLE users DROP COLUMN IF EXISTS frozen_balance")
	require.NoError(t, err)
	var frozenBalanceExists bool
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema='public' AND table_name='users' AND column_name='frozen_balance'
	)`).Scan(&frozenBalanceExists))
	require.False(t, frozenBalanceExists, "Phase 5 readiness test must run without users.frozen_balance")

	// The fresh target schema has Phase 5 admin objects but no Phase 6 price
	// snapshots, settlement ledger, or recovery table.
	for _, table := range []string{
		"unified_route_price_snapshots",
		"unified_gateway_charge_ledger",
		"unified_gateway_recovery_tasks",
	} {
		var relation sql.NullString
		require.NoError(t, tx.QueryRowContext(ctx, "SELECT to_regclass('public.' || $1)", table).Scan(&relation))
		require.False(t, relation.Valid, "Phase 5 must not create Phase 6 object %s", table)
	}

	// The checker must fail when an admin-required index is missing; this is
	// isolated in the test transaction and rolled back by testTx cleanup.
	_, err = tx.ExecContext(ctx, "DROP INDEX idx_unified_route_targets_admin_unique")
	require.NoError(t, err)
	readiness, err = checkUnifiedGatewayAdminSchema(ctx, tx)
	require.NoError(t, err)
	require.False(t, readiness.Ready)
	require.Contains(t, readiness.Missing, "idx_unified_route_targets_admin_unique")
}

func TestUnifiedGatewayAdminProjectionMigrationColumns(t *testing.T) {
	tx := testTx(t)

	requireColumn(t, tx, "unified_route_targets", "unified_config_id", "character varying", 128, true)
	requireColumn(t, tx, "unified_route_targets", "source_group_revision", "character varying", 255, true)
	requireColumn(t, tx, "unified_route_account_bindings", "probe_snapshot", "jsonb", 0, false)
	requireColumn(t, tx, "unified_route_account_bindings", "probe_status", "character varying", 32, true)
	requireColumn(t, tx, "unified_route_account_bindings", "probe_snapshot_ref", "character varying", 255, true)
}

func TestUnifiedGatewayAdminSchemaRejectsMissingProbeSnapshotColumn(t *testing.T) {
	tx := testTx(t)
	_, err := tx.ExecContext(context.Background(), "ALTER TABLE unified_route_account_bindings DROP COLUMN probe_snapshot")
	require.NoError(t, err)

	readiness, err := checkUnifiedGatewayAdminSchema(context.Background(), tx)
	require.NoError(t, err)
	require.False(t, readiness.Ready)
	require.Contains(t, readiness.Missing, "unified_route_account_bindings_columns")
}
