//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestSmartRouterHealthPostgresPersistenceAndFreshProcessRestore(t *testing.T) {
	ctx := context.Background()
	require.NotEmpty(t, integrationDSN)

	ledgerSQL, err := migrations.FS.ReadFile("242_smart_router_health_ledger.sql")
	require.NoError(t, err)
	require.NoError(t, ApplyMigrations(ctx, integrationDB))
	var got string
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT checksum FROM schema_migrations WHERE filename = $1", "242_smart_router_health_ledger.sql").Scan(&got))
	require.Equal(t, migrationTestChecksum(ledgerSQL), got, "migration checksum for the consolidated ledger migration")

	const laneID = "phase2-postgres-roundtrip-lane"
	const accountID int64 = 880_021
	const sourceGroup = "isolated-integration-fixture"
	const model = "gpt-5.6-sol"
	now := time.Now().UTC().Truncate(time.Second)
	cooldownUntil := now.Add(17 * time.Minute)
	event := smartrouter.HealthEvent{
		OccurredAtUnix:      now.Unix(),
		Source:              "production",
		Key:                 smartrouter.NewHealthKey(laneID, smartrouter.CapabilityResponsesCompact, model),
		AccountID:           accountID,
		SourceGroup:         sourceGroup,
		StatusCode:          503,
		FailureClass:        smartrouter.FailureUpstream5xx,
		Action:              "bounded_cooldown",
		CooldownUntilUnix:   cooldownUntil.Unix(),
		HealthPenalty:       2,
		HealthScore:         0.37,
		ErrorRateEWMA:       0.63,
		ConsecutiveFailures: 2,
		RecoveryStage:       smartrouter.RecoveryModelUnavailable,
		RecoveryPriority:    19,
		LatencyMs:           840,
		ErrorSummary:        "isolated test failure",
	}
	repo := NewSmartRouterHealthRepository(integrationDB)
	require.NoError(t, repo.RecordEvent(ctx, event))
	states, err := repo.LoadStates(ctx)
	require.NoError(t, err)
	var persisted *struct {
		accountID  int64
		capability smartrouter.Capability
		model      string
		cooldown   int64
		recovery   smartrouter.RecoveryStage
		priority   int
	}
	for _, state := range states {
		if state.LaneID == laneID {
			persisted = &struct {
				accountID  int64
				capability smartrouter.Capability
				model      string
				cooldown   int64
				recovery   smartrouter.RecoveryStage
				priority   int
			}{state.AccountID, state.Capability, state.ModelFamily, state.Snapshot.CooldownUntilUnix, state.Snapshot.RecoveryStage, state.Snapshot.RecoveryPriority}
			break
		}
	}
	require.NotNil(t, persisted)
	require.Equal(t, accountID, persisted.accountID)
	require.Equal(t, smartrouter.CapabilityResponsesCompact, persisted.capability)
	require.Equal(t, model, persisted.model)
	require.Equal(t, cooldownUntil.Unix(), persisted.cooldown)
	require.Equal(t, smartrouter.RecoveryModelUnavailable, persisted.recovery)
	require.Equal(t, 19, persisted.priority)

	evidence, err := repo.ListCapabilityEvidence(ctx)
	require.NoError(t, err)
	var compactEvidence bool
	for _, item := range evidence {
		if item.LaneID == laneID {
			compactEvidence = item.CompactKnown && item.CompactRecoveryPriority == 19 && item.CompactLastFailure.Equal(now)
			break
		}
	}
	require.True(t, compactEvidence, "compact capability evidence must survive the PostgreSQL projection")

	scheduledFor := now.Add(2 * time.Hour)
	run, acquired, err := repo.BeginCalibrationRun(ctx, scheduledFor)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, run)
	duplicate, acquired, err := repo.BeginCalibrationRun(ctx, scheduledFor)
	require.NoError(t, err)
	require.False(t, acquired)
	require.Nil(t, duplicate)
	require.NoError(t, repo.RecordCalibrationResult(ctx, run.ID, serviceCalibrationResult(laneID, accountID, sourceGroup, model)))
	require.NoError(t, repo.FinishCalibrationRun(ctx, run.ID, true, "roundtrip ok"))
	var status, summary, reason string
	var resultCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT r.status, r.summary,
       (SELECT COUNT(*) FROM smart_router_calibration_results cr WHERE cr.calibration_run_id = r.id),
       (SELECT reason FROM smart_router_calibration_results cr WHERE cr.calibration_run_id = r.id LIMIT 1)
FROM smart_router_calibration_runs r WHERE r.id = $1`, run.ID).Scan(&status, &summary, &resultCount, &reason))
	require.Equal(t, "completed", status)
	require.Equal(t, "roundtrip ok", summary)
	require.Equal(t, 1, resultCount)
	require.Equal(t, "scheduled", reason)

	// A second Go test process loads the same disposable database through the
	// production repository and injects it into the service before the first
	// Smart Router event, exercising the normal lazy restore path end to end.
	backendDir, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	goBinary, err := exec.LookPath("go")
	require.NoError(t, err)
	cmd := exec.Command(goBinary, "test", "-tags=integration", "-v", "./internal/smartrouter/core", "-run", "^TestSmartRouterHealthRestoreThroughPostgresLedgerInChildProcess$", "-count=1")
	cmd.Dir = backendDir
	cmd.Env = append(os.Environ(),
		"SUB2API_SMART_ROUTER_RESTORE_TEST_DSN="+integrationDSN,
		"SUB2API_SMART_ROUTER_RESTORE_TEST_LANE="+laneID,
		"SUB2API_SMART_ROUTER_RESTORE_TEST_MODEL="+model,
		"SUB2API_SMART_ROUTER_RESTORE_TEST_ACCOUNT="+fmt.Sprint(accountID),
		"SUB2API_SMART_ROUTER_RESTORE_TEST_SOURCE_GROUP="+sourceGroup,
	)
	output, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "fresh-process Smart Router restore failed:\n%s", output)
	require.Containsf(t, string(output), "fresh-process repository-backed state restored and extended",
		"fresh-process restore test must actually execute:\n%s", output)
}

func TestSmartRouterLedgerMigrationLegacyFixtures(t *testing.T) {
	ctx := context.Background()
	ledgerSQL, err := migrations.FS.ReadFile("242_smart_router_health_ledger.sql")
	require.NoError(t, err)
	fixtures := []struct {
		name                  string
		alreadyHasPriorityCol bool
	}{
		{name: "ledger_only"},
		{name: "ledger_with_existing_priority", alreadyHasPriorityCol: true},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			db := newSmartRouterMigrationFixtureDB(t)
			_, err := db.ExecContext(ctx, schemaMigrationsTableDDL)
			require.NoError(t, err)
			require.NoError(t, execMigrationFixture(ctx, db, []byte(smartRouterLegacyLedgerSchemaSQL)))
			_, err = db.ExecContext(ctx, `INSERT INTO smart_router_health_events
(occurred_at, source, lane_id, capability, model_family, success)
VALUES (NOW(), 'production', 'legacy-fixture-lane', 'responses', 'gpt-5.5', FALSE)`)
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, `INSERT INTO smart_router_lane_state
(lane_id, capability, model_family, health_score)
VALUES ('legacy-fixture-lane', 'responses', 'gpt-5.5', 0.4)`)
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, "INSERT INTO schema_migrations(filename, checksum) VALUES ($1, $2)", "174_smart_router_health_ledger.sql", "legacy-fixture")
			require.NoError(t, err)
			if fixture.alreadyHasPriorityCol {
				for _, table := range []string{"smart_router_health_events", "smart_router_lane_state"} {
					_, err := db.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN recovery_priority INTEGER NOT NULL DEFAULT 0")
					require.NoError(t, err)
				}
				_, err = db.ExecContext(ctx, "INSERT INTO schema_migrations(filename, checksum) VALUES ($1, $2)", "175_smart_router_recovery_priority.sql", "legacy-fixture")
				require.NoError(t, err)
			}

			migrationFS := fstest.MapFS{
				"242_smart_router_health_ledger.sql": &fstest.MapFile{Data: ledgerSQL},
			}
			require.NoError(t, applyMigrationsFS(ctx, db, migrationFS))
			require.NoError(t, applyMigrationsFS(ctx, db, migrationFS), "legacy migration must be repeatable")
			for _, table := range []string{"smart_router_health_events", "smart_router_lane_state"} {
				require.True(t, postgresColumnExists(t, db, table, "recovery_priority"), "%s.recovery_priority", table)
			}
			var eventRows int
			require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM smart_router_health_events WHERE lane_id = 'legacy-fixture-lane'").Scan(&eventRows))
			require.Equal(t, 1, eventRows, "legacy health events must remain intact")
			var healthScore float64
			var recoveryPriority int
			require.NoError(t, db.QueryRowContext(ctx, "SELECT health_score, recovery_priority FROM smart_router_lane_state WHERE lane_id = 'legacy-fixture-lane'").Scan(&healthScore, &recoveryPriority))
			require.Equal(t, 0.4, healthScore, "legacy projection must remain intact")
			require.Equal(t, 0, recoveryPriority, "the added source-compatible priority column defaults to zero")
			var checksum string
			require.NoError(t, db.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE filename = $1", "242_smart_router_health_ledger.sql").Scan(&checksum))
			require.Equal(t, migrationTestChecksum(ledgerSQL), checksum)
			var applied int
			require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE filename = $1", "242_smart_router_health_ledger.sql").Scan(&applied))
			require.Equal(t, 1, applied)
		})
	}
}

// This is the existing T0 174 ledger schema, pinned at 95122c08037053f3947424004272ebc992b1a3d5.
// The target migration must advance these tables without replacing their rows.
const smartRouterLegacyLedgerSchemaSQL = `
CREATE TABLE IF NOT EXISTS smart_router_health_events (
    id BIGSERIAL PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL,
    source VARCHAR(32) NOT NULL,
    lane_id VARCHAR(255) NOT NULL,
    account_id BIGINT NOT NULL DEFAULT 0,
    source_group VARCHAR(255) NOT NULL DEFAULT '',
    capability VARCHAR(64) NOT NULL,
    model_family VARCHAR(128) NOT NULL,
    success BOOLEAN NOT NULL,
    status_code INTEGER NOT NULL DEFAULT 0,
    failure_class VARCHAR(64) NOT NULL DEFAULT '',
    action VARCHAR(96) NOT NULL DEFAULT '',
    cooldown_until TIMESTAMPTZ NULL,
    health_penalty INTEGER NOT NULL DEFAULT 0,
    health_score DOUBLE PRECISION NOT NULL DEFAULT 1,
    error_rate_ewma DOUBLE PRECISION NOT NULL DEFAULT 0,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    consecutive_successes INTEGER NOT NULL DEFAULT 0,
    recovery_stage VARCHAR(64) NOT NULL DEFAULT 'normal',
    latency_ms BIGINT NOT NULL DEFAULT 0,
    error_summary VARCHAR(256) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS smart_router_lane_state (
    lane_id VARCHAR(255) NOT NULL,
    capability VARCHAR(64) NOT NULL,
    model_family VARCHAR(128) NOT NULL,
    account_id BIGINT NOT NULL DEFAULT 0,
    source_group VARCHAR(255) NOT NULL DEFAULT '',
    health_penalty INTEGER NOT NULL DEFAULT 0,
    health_score DOUBLE PRECISION NOT NULL DEFAULT 1,
    error_rate_ewma DOUBLE PRECISION NOT NULL DEFAULT 0,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    consecutive_successes INTEGER NOT NULL DEFAULT 0,
    cooldown_until TIMESTAMPTZ NULL,
    recovery_stage VARCHAR(64) NOT NULL DEFAULT 'normal',
    last_success_at TIMESTAMPTZ NULL,
    last_failure_at TIMESTAMPTZ NULL,
    last_failure_unix BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (lane_id, capability, model_family)
);
CREATE TABLE IF NOT EXISTS smart_router_calibration_runs (
    id BIGSERIAL PRIMARY KEY,
    scheduled_for TIMESTAMPTZ NOT NULL UNIQUE,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'running',
    summary VARCHAR(512) NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS smart_router_calibration_results (
    id BIGSERIAL PRIMARY KEY,
    calibration_run_id BIGINT NOT NULL REFERENCES smart_router_calibration_runs(id) ON DELETE CASCADE,
    lane_id VARCHAR(255) NOT NULL,
    account_id BIGINT NOT NULL DEFAULT 0,
    source_group VARCHAR(255) NOT NULL DEFAULT '',
    capability VARCHAR(64) NOT NULL,
    model_family VARCHAR(128) NOT NULL,
    success BOOLEAN NOT NULL,
    status_code INTEGER NOT NULL DEFAULT 0,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    error_summary VARCHAR(256) NOT NULL DEFAULT '',
    reason VARCHAR(128) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS smart_router_health_events_lane_capability_idx
    ON smart_router_health_events (lane_id, capability, model_family, occurred_at DESC);
CREATE INDEX IF NOT EXISTS smart_router_calibration_results_run_idx
    ON smart_router_calibration_results (calibration_run_id, created_at);
`

func newSmartRouterMigrationFixtureDB(t *testing.T) *sql.DB {
	t.Helper()
	dbName := fmt.Sprintf("sr_migration_%x", time.Now().UnixNano())
	_, err := integrationDB.Exec("CREATE DATABASE " + dbName)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.Exec("DROP DATABASE IF EXISTS " + dbName + " WITH (FORCE)")
	})
	parsed, err := url.Parse(integrationDSN)
	require.NoError(t, err)
	parsed.Path = "/" + dbName
	db, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Ping())
	return db
}

func execMigrationFixture(ctx context.Context, db *sql.DB, content []byte) error {
	_, err := db.ExecContext(ctx, strings.TrimSpace(string(content)))
	return err
}

func migrationTestChecksum(content []byte) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(string(content))))
	return hex.EncodeToString(sum[:])
}

func postgresColumnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var exists bool
	err := db.QueryRow(`SELECT EXISTS (
SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
)
`, table, column).Scan(&exists)
	require.NoError(t, err)
	return exists
}

func serviceCalibrationResult(laneID string, accountID int64, sourceGroup, model string) service.SmartRouterCalibrationResult {
	return service.SmartRouterCalibrationResult{
		LaneID: laneID, AccountID: accountID, SourceGroup: sourceGroup,
		Capability: smartrouter.CapabilityResponsesCompact, ModelFamily: model,
		Success: true, StatusCode: 200, LatencyMs: 710, Reason: "scheduled",
	}
}
