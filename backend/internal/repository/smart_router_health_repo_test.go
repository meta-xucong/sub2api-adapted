package repository

import (
	"context"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/stretchr/testify/require"
)

func TestSmartRouterHealthRepositoryLoadStatesRestoresCooldown(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT lane_id, account_id").WillReturnRows(sqlmock.NewRows([]string{
		"lane_id", "account_id", "source_group", "capability", "model_family",
		"health_penalty", "health_score", "error_rate_ewma", "consecutive_failures",
		"consecutive_successes", "cooldown_until", "recovery_stage", "last_success_at",
		"last_failure_at", "last_failure_unix",
	}).AddRow(
		"764-generation", int64(764), "7646881", "image_generation", "gpt-image",
		2, 0.42, 0.6, 3, 0, now.Add(time.Hour), "cooling", now.Add(-2*time.Hour), now.Add(-time.Minute), now.Add(-time.Minute).Unix(),
	))

	states, err := NewSmartRouterHealthRepository(db).LoadStates(context.Background())
	require.NoError(t, err)
	require.Len(t, states, 1)
	require.Equal(t, "764-generation", states[0].LaneID)
	require.Equal(t, smartrouter.CapabilityImageGeneration, states[0].Capability)
	require.Equal(t, now.Add(time.Hour).Unix(), states[0].Snapshot.CooldownUntilUnix)
	require.Equal(t, smartrouter.RecoveryCooling, states[0].Snapshot.RecoveryStage)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSmartRouterHealthRepositoryRecordsEventAndProjectionAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO smart_router_health_events").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO smart_router_lane_state").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	event := smartrouter.HealthEvent{
		OccurredAtUnix:      time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC).Unix(),
		Source:              "production",
		Key:                 smartrouter.NewHealthKey("aiai-image", smartrouter.CapabilityImageGeneration, "gpt-image-2"),
		AccountID:           89,
		SourceGroup:         "aiai",
		StatusCode:          502,
		FailureClass:        smartrouter.FailureUpstream5xx,
		Action:              "sustained_failure_quarantine",
		CooldownUntilUnix:   time.Date(2026, 7, 12, 20, 0, 0, 0, time.UTC).Unix(),
		HealthPenalty:       3,
		HealthScore:         0.2,
		ErrorRateEWMA:       0.8,
		ConsecutiveFailures: 3,
		RecoveryStage:       smartrouter.RecoveryCooling,
		LatencyMs:           180000,
		ErrorSummary:        "upstream timeout",
	}

	require.NoError(t, NewSmartRouterHealthRepository(db).RecordEvent(context.Background(), event))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSmartRouterHealthRepositoryListsCompactCapabilityEvidence(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Date(2026, 7, 13, 4, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT lane_id,").WillReturnRows(sqlmock.NewRows([]string{
		"lane_id", "generation_known", "edit_known", "compact_known",
		"generation_last_success", "generation_last_failure", "edit_last_success", "edit_last_failure",
		"compact_last_success", "compact_last_failure",
	}).AddRow(
		"oauth-compact", false, false, true,
		nil, nil, nil, nil,
		now.Add(-time.Hour), now.Add(-2*time.Hour),
	))

	evidence, err := NewSmartRouterHealthRepository(db).ListCapabilityEvidence(context.Background())
	require.NoError(t, err)
	require.Len(t, evidence, 1)
	require.Equal(t, "oauth-compact", evidence[0].LaneID)
	require.True(t, evidence[0].CompactKnown)
	require.Equal(t, now.Add(-time.Hour), evidence[0].CompactLastSuccess)
	require.Equal(t, now.Add(-2*time.Hour), evidence[0].CompactLastFailure)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSmartRouterHealthRepositoryCalibrationRunIsIdempotent(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	scheduledFor := time.Date(2026, 7, 12, 20, 0, 0, 0, time.UTC)
	mock.ExpectQuery("INSERT INTO smart_router_calibration_runs").
		WithArgs(scheduledFor).
		WillReturnRows(sqlmock.NewRows([]string{"id", "scheduled_for", "started_at"}))
	run, acquired, err := NewSmartRouterHealthRepository(db).BeginCalibrationRun(context.Background(), scheduledFor)
	require.NoError(t, err)
	require.False(t, acquired)
	require.Nil(t, run)
	require.NoError(t, mock.ExpectationsWereMet())
}
