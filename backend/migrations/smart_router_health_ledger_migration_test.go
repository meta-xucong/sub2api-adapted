package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSmartRouterHealthLedgerMigrationIsForwardCompatible(t *testing.T) {
	content, err := FS.ReadFile("241_smart_router_health_ledger.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS smart_router_health_events")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS smart_router_lane_state")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS smart_router_calibration_runs")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS smart_router_calibration_results")
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS smart_router_health_events_lane_capability_idx")
	require.Contains(t, sql, "recovery_priority INTEGER NOT NULL DEFAULT 0")
	require.NotContains(t, strings.ToUpper(sql), "DROP TABLE")
	require.NotContains(t, strings.ToUpper(sql), "ALTER TABLE")
}
