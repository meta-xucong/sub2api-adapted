package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
)

type smartRouterHealthRepository struct {
	db *sql.DB
}

func NewSmartRouterHealthRepository(db *sql.DB) service.SmartRouterHealthLedger {
	return &smartRouterHealthRepository{db: db}
}

func (r *smartRouterHealthRepository) LoadStates(ctx context.Context) ([]service.SmartRouterHealthState, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("smart router health repository is not configured")
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT lane_id, account_id, source_group, capability, model_family,
       health_penalty, health_score, error_rate_ewma,
       consecutive_failures, consecutive_successes, cooldown_until,
       recovery_stage, recovery_priority, last_success_at, last_failure_at, last_failure_unix
FROM smart_router_lane_state`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	states := make([]service.SmartRouterHealthState, 0)
	for rows.Next() {
		var state service.SmartRouterHealthState
		var cooldown, lastSuccess, lastFailure sql.NullTime
		var recovery string
		if err := rows.Scan(
			&state.LaneID, &state.AccountID, &state.SourceGroup, &state.Capability, &state.ModelFamily,
			&state.Snapshot.HealthPenalty, &state.Snapshot.HealthScore, &state.Snapshot.ErrorRateEWMA,
			&state.Snapshot.ConsecutiveFailures, &state.Snapshot.ConsecutiveSuccesses, &cooldown,
			&recovery, &state.Snapshot.RecoveryPriority, &lastSuccess, &lastFailure, &state.LastFailureUnix,
		); err != nil {
			return nil, err
		}
		state.Snapshot.RecoveryStage = smartrouter.RecoveryStage(recovery)
		if cooldown.Valid {
			state.Snapshot.CooldownUntilUnix = cooldown.Time.Unix()
		}
		if lastSuccess.Valid {
			state.LastSuccessAt = lastSuccess.Time
		}
		if lastFailure.Valid {
			state.LastFailureAt = lastFailure.Time
		}
		states = append(states, state)
	}
	return states, rows.Err()
}

func (r *smartRouterHealthRepository) RecordEvent(ctx context.Context, event smartrouter.HealthEvent) error {
	if r == nil || r.db == nil {
		return errors.New("smart router health repository is not configured")
	}
	occurredAt := time.Unix(event.OccurredAtUnix, 0).UTC()
	var cooldown any
	if event.CooldownUntilUnix > 0 {
		cooldown = time.Unix(event.CooldownUntilUnix, 0).UTC()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
INSERT INTO smart_router_health_events (
    occurred_at, source, lane_id, account_id, source_group, capability, model_family,
    success, status_code, failure_class, action, cooldown_until, health_penalty,
    health_score, error_rate_ewma, consecutive_failures, consecutive_successes,
    recovery_stage, recovery_priority, latency_ms, error_summary
) VALUES (
    $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21
)`,
		occurredAt, event.Source, event.Key.LaneID, event.AccountID, event.SourceGroup,
		event.Key.Capability, event.Key.Model, event.Success, event.StatusCode,
		event.FailureClass, event.Action, cooldown, event.HealthPenalty,
		event.HealthScore, event.ErrorRateEWMA, event.ConsecutiveFailures,
		event.ConsecutiveSuccesses, event.RecoveryStage, event.RecoveryPriority, event.LatencyMs, event.ErrorSummary,
	)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO smart_router_lane_state (
    lane_id, capability, model_family, account_id, source_group, health_penalty,
    health_score, error_rate_ewma, consecutive_failures, consecutive_successes,
    cooldown_until, recovery_stage, recovery_priority, last_success_at, last_failure_at,
    last_failure_unix, updated_at
) VALUES (
    $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,
    CASE WHEN $14::boolean THEN $15::timestamptz ELSE NULL::timestamptz END,
    CASE WHEN $14::boolean THEN NULL::timestamptz ELSE $15::timestamptz END,
    CASE WHEN $14::boolean THEN 0 ELSE $16::bigint END,
    NOW()
)
ON CONFLICT (lane_id, capability, model_family) DO UPDATE SET
    account_id = EXCLUDED.account_id,
    source_group = EXCLUDED.source_group,
    health_penalty = EXCLUDED.health_penalty,
    health_score = EXCLUDED.health_score,
    error_rate_ewma = EXCLUDED.error_rate_ewma,
    consecutive_failures = EXCLUDED.consecutive_failures,
    consecutive_successes = EXCLUDED.consecutive_successes,
    cooldown_until = EXCLUDED.cooldown_until,
    recovery_stage = EXCLUDED.recovery_stage,
    recovery_priority = EXCLUDED.recovery_priority,
    last_success_at = CASE WHEN $14::boolean THEN EXCLUDED.last_success_at ELSE smart_router_lane_state.last_success_at END,
    last_failure_at = CASE WHEN $14::boolean THEN smart_router_lane_state.last_failure_at ELSE EXCLUDED.last_failure_at END,
    last_failure_unix = CASE WHEN $14::boolean THEN 0 ELSE EXCLUDED.last_failure_unix END,
    updated_at = NOW()`,
		event.Key.LaneID, event.Key.Capability, event.Key.Model, event.AccountID, event.SourceGroup,
		event.HealthPenalty, event.HealthScore, event.ErrorRateEWMA, event.ConsecutiveFailures,
		event.ConsecutiveSuccesses, cooldown, event.RecoveryStage, event.RecoveryPriority, event.Success, occurredAt, event.OccurredAtUnix,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *smartRouterHealthRepository) ListCapabilityEvidence(ctx context.Context) ([]service.SmartRouterCapabilityEvidence, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("smart router health repository is not configured")
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT lane_id,
       BOOL_OR(capability = 'chat') AS chat_known,
       BOOL_OR(capability = 'responses') AS responses_known,
       BOOL_OR(capability = 'image_generation') AS generation_known,
       BOOL_OR(capability = 'image_edit') AS edit_known,
       BOOL_OR(capability = 'responses_compact') AS compact_known,
       COALESCE(MAX(recovery_priority) FILTER (WHERE capability = 'chat'), 0) AS chat_recovery_priority,
       COALESCE(MAX(recovery_priority) FILTER (WHERE capability = 'responses'), 0) AS responses_recovery_priority,
       COALESCE(MAX(recovery_priority) FILTER (WHERE capability = 'image_generation'), 0) AS generation_recovery_priority,
       COALESCE(MAX(recovery_priority) FILTER (WHERE capability = 'image_edit'), 0) AS edit_recovery_priority,
       COALESCE(MAX(recovery_priority) FILTER (WHERE capability = 'responses_compact'), 0) AS compact_recovery_priority,
       MAX(last_success_at) FILTER (WHERE capability = 'chat') AS chat_last_success,
       MAX(last_failure_at) FILTER (WHERE capability = 'chat') AS chat_last_failure,
       MAX(last_success_at) FILTER (WHERE capability = 'responses') AS responses_last_success,
       MAX(last_failure_at) FILTER (WHERE capability = 'responses') AS responses_last_failure,
       MAX(last_success_at) FILTER (WHERE capability = 'image_generation') AS generation_last_success,
       MAX(last_failure_at) FILTER (WHERE capability = 'image_generation') AS generation_last_failure,
       MAX(last_success_at) FILTER (WHERE capability = 'image_edit') AS edit_last_success,
       MAX(last_failure_at) FILTER (WHERE capability = 'image_edit') AS edit_last_failure,
       MAX(last_success_at) FILTER (WHERE capability = 'responses_compact') AS compact_last_success,
       MAX(last_failure_at) FILTER (WHERE capability = 'responses_compact') AS compact_last_failure
FROM smart_router_lane_state
WHERE capability IN ('chat', 'responses', 'image_generation', 'image_edit', 'responses_compact')
GROUP BY lane_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	evidence := make([]service.SmartRouterCapabilityEvidence, 0)
	for rows.Next() {
		var item service.SmartRouterCapabilityEvidence
		var chatSuccess, chatFailure, responsesSuccess, responsesFailure sql.NullTime
		var generationSuccess, generationFailure, editSuccess, editFailure, compactSuccess, compactFailure sql.NullTime
		if err := rows.Scan(
			&item.LaneID, &item.ChatKnown, &item.ResponsesKnown, &item.GenerationKnown, &item.EditKnown,
			&item.CompactKnown, &item.ChatRecoveryPriority, &item.ResponsesRecoveryPriority,
			&item.GenerationRecoveryPriority, &item.EditRecoveryPriority, &item.CompactRecoveryPriority,
			&chatSuccess, &chatFailure, &responsesSuccess, &responsesFailure,
			&generationSuccess, &generationFailure, &editSuccess, &editFailure,
			&compactSuccess, &compactFailure,
		); err != nil {
			return nil, err
		}
		if chatSuccess.Valid {
			item.ChatLastSuccess = chatSuccess.Time
		}
		if chatFailure.Valid {
			item.ChatLastFailure = chatFailure.Time
		}
		if responsesSuccess.Valid {
			item.ResponsesLastSuccess = responsesSuccess.Time
		}
		if responsesFailure.Valid {
			item.ResponsesLastFailure = responsesFailure.Time
		}
		if generationSuccess.Valid {
			item.GenerationLastSuccess = generationSuccess.Time
		}
		if generationFailure.Valid {
			item.GenerationLastFailure = generationFailure.Time
		}
		if editSuccess.Valid {
			item.EditLastSuccess = editSuccess.Time
		}
		if editFailure.Valid {
			item.EditLastFailure = editFailure.Time
		}
		if compactSuccess.Valid {
			item.CompactLastSuccess = compactSuccess.Time
		}
		if compactFailure.Valid {
			item.CompactLastFailure = compactFailure.Time
		}
		evidence = append(evidence, item)
	}
	return evidence, rows.Err()
}

func (r *smartRouterHealthRepository) BeginCalibrationRun(ctx context.Context, scheduledFor time.Time) (*service.SmartRouterCalibrationRun, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, errors.New("smart router health repository is not configured")
	}
	run := &service.SmartRouterCalibrationRun{ScheduledFor: scheduledFor.UTC()}
	err := r.db.QueryRowContext(ctx, `
INSERT INTO smart_router_calibration_runs (scheduled_for, started_at, status)
VALUES ($1, NOW(), 'running')
ON CONFLICT (scheduled_for) DO NOTHING
RETURNING id, scheduled_for, started_at`, run.ScheduledFor).Scan(&run.ID, &run.ScheduledFor, &run.StartedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return run, true, nil
}

func (r *smartRouterHealthRepository) RecordCalibrationResult(ctx context.Context, runID int64, result service.SmartRouterCalibrationResult) error {
	if r == nil || r.db == nil {
		return errors.New("smart router health repository is not configured")
	}
	_, err := r.db.ExecContext(ctx, `
INSERT INTO smart_router_calibration_results (
    calibration_run_id, lane_id, account_id, source_group, capability, model_family,
    success, status_code, latency_ms, error_summary, reason
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		runID, result.LaneID, result.AccountID, result.SourceGroup, result.Capability,
		result.ModelFamily, result.Success, result.StatusCode, result.LatencyMs,
		result.ErrorSummary, result.Reason,
	)
	return err
}

func (r *smartRouterHealthRepository) FinishCalibrationRun(ctx context.Context, runID int64, success bool, summary string) error {
	if r == nil || r.db == nil {
		return errors.New("smart router health repository is not configured")
	}
	status := "completed"
	if !success {
		status = "completed_with_failures"
	}
	_, err := r.db.ExecContext(ctx, `
UPDATE smart_router_calibration_runs
SET finished_at = NOW(), status = $2, summary = LEFT($3, 512)
WHERE id = $1`, runID, status, summary)
	return err
}
