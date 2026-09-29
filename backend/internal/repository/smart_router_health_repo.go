package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
)

// smartRouterHealthRepository is the shared, authoritative ledger. Redis
// remains the shared scheduler-load snapshot; health state is kept in SQL so
// every instance restores the same capability-scoped state after restart.
type smartRouterHealthRepository struct{ db *sql.DB }

func NewSmartRouterHealthRepository(db *sql.DB) service.SmartRouterHealthLedger {
	return &smartRouterHealthRepository{db: db}
}

func (r *smartRouterHealthRepository) configured() error {
	if r == nil || r.db == nil {
		return errors.New("smart router health repository is not configured")
	}
	return nil
}

func (r *smartRouterHealthRepository) LoadStates(ctx context.Context) ([]service.SmartRouterHealthState, error) {
	if err := r.configured(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT lane_id, account_id, source_group, capability, model_family,
       health_penalty, recovery_priority, health_score, error_rate_ewma,
       consecutive_failures, consecutive_successes, cooldown_until,
       recovery_stage, last_success_at, last_failure_at, last_failure_unix
FROM smart_router_lane_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var states []service.SmartRouterHealthState
	for rows.Next() {
		var state service.SmartRouterHealthState
		var cooldown, lastSuccess, lastFailure sql.NullTime
		var recovery string
		if err := rows.Scan(&state.LaneID, &state.AccountID, &state.SourceGroup, &state.Capability,
			&state.ModelFamily, &state.Snapshot.HealthPenalty, &state.Snapshot.RecoveryPriority,
			&state.Snapshot.HealthScore, &state.Snapshot.ErrorRateEWMA,
			&state.Snapshot.ConsecutiveFailures, &state.Snapshot.ConsecutiveSuccesses,
			&cooldown, &recovery, &lastSuccess, &lastFailure, &state.LastFailureUnix); err != nil {
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
	if err := r.configured(); err != nil {
		return err
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
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`,
		occurredAt, event.Source, event.Key.LaneID, event.AccountID, event.SourceGroup,
		event.Key.Capability, event.Key.Model, event.Success, event.StatusCode,
		event.FailureClass, event.Action, cooldown, event.HealthPenalty, event.HealthScore,
		event.ErrorRateEWMA, event.ConsecutiveFailures, event.ConsecutiveSuccesses,
		event.RecoveryStage, event.RecoveryPriority, event.LatencyMs, event.ErrorSummary)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO smart_router_lane_state (
    lane_id, capability, model_family, account_id, source_group, health_penalty,
    recovery_priority, health_score, error_rate_ewma, consecutive_failures,
    consecutive_successes, cooldown_until, recovery_stage, last_success_at,
    last_failure_at, last_failure_unix, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,
    $12, $13, CASE WHEN $14::boolean THEN $15::timestamptz ELSE NULL::timestamptz END,
    CASE WHEN $14::boolean THEN NULL::timestamptz ELSE $15::timestamptz END,
    CASE WHEN $14::boolean THEN 0 ELSE $16::bigint END, NOW())
ON CONFLICT (lane_id, capability, model_family) DO UPDATE SET
    account_id = EXCLUDED.account_id, source_group = EXCLUDED.source_group,
    health_penalty = EXCLUDED.health_penalty, recovery_priority = EXCLUDED.recovery_priority,
    health_score = EXCLUDED.health_score, error_rate_ewma = EXCLUDED.error_rate_ewma,
    consecutive_failures = EXCLUDED.consecutive_failures,
    consecutive_successes = EXCLUDED.consecutive_successes,
    cooldown_until = EXCLUDED.cooldown_until, recovery_stage = EXCLUDED.recovery_stage,
    last_success_at = CASE WHEN $14::boolean THEN EXCLUDED.last_success_at ELSE smart_router_lane_state.last_success_at END,
    last_failure_at = CASE WHEN $14::boolean THEN smart_router_lane_state.last_failure_at ELSE EXCLUDED.last_failure_at END,
    last_failure_unix = CASE WHEN $14::boolean THEN EXCLUDED.last_failure_unix ELSE smart_router_lane_state.last_failure_unix END,
    updated_at = NOW()`,
		event.Key.LaneID, event.Key.Capability, event.Key.Model, event.AccountID, event.SourceGroup,
		event.HealthPenalty, event.RecoveryPriority, event.HealthScore, event.ErrorRateEWMA,
		event.ConsecutiveFailures, event.ConsecutiveSuccesses, cooldown, event.RecoveryStage,
		event.Success, occurredAt, event.OccurredAtUnix)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *smartRouterHealthRepository) ListCapabilityEvidence(ctx context.Context) ([]service.SmartRouterCapabilityEvidence, error) {
	if err := r.configured(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT lane_id,
       BOOL_OR(capability = 'chat') AS chat_known,
       MAX(last_success_at) FILTER (WHERE capability = 'chat') AS chat_last_success,
       MAX(last_failure_at) FILTER (WHERE capability = 'chat') AS chat_last_failure,
       MAX(recovery_priority) FILTER (WHERE capability = 'chat') AS chat_recovery_priority,
       BOOL_OR(capability = 'responses') AS responses_known,
       MAX(last_success_at) FILTER (WHERE capability = 'responses') AS responses_last_success,
       MAX(last_failure_at) FILTER (WHERE capability = 'responses') AS responses_last_failure,
       MAX(recovery_priority) FILTER (WHERE capability = 'responses') AS responses_recovery_priority,
       BOOL_OR(capability = 'responses_compact') AS compact_known,
       MAX(last_success_at) FILTER (WHERE capability = 'responses_compact') AS compact_last_success,
       MAX(last_failure_at) FILTER (WHERE capability = 'responses_compact') AS compact_last_failure,
       MAX(recovery_priority) FILTER (WHERE capability = 'responses_compact') AS compact_recovery_priority
FROM smart_router_lane_state
WHERE capability IN ('chat', 'responses', 'responses_compact')
GROUP BY lane_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var evidence []service.SmartRouterCapabilityEvidence
	for rows.Next() {
		var item service.SmartRouterCapabilityEvidence
		var chatSuccess, chatFailure, responsesSuccess, responsesFailure, compactSuccess, compactFailure sql.NullTime
		var chatPriority, responsesPriority, compactPriority sql.NullInt64
		if err := rows.Scan(&item.LaneID, &item.ChatKnown, &chatSuccess, &chatFailure, &chatPriority,
			&item.ResponsesKnown, &responsesSuccess, &responsesFailure, &responsesPriority,
			&item.CompactKnown, &compactSuccess, &compactFailure, &compactPriority); err != nil {
			return nil, err
		}
		item.ChatRecoveryPriority, item.ResponsesRecoveryPriority, item.CompactRecoveryPriority = int(chatPriority.Int64), int(responsesPriority.Int64), int(compactPriority.Int64)
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
	if err := r.configured(); err != nil {
		return nil, false, err
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
	if err := r.configured(); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `
INSERT INTO smart_router_calibration_results (
    calibration_run_id, lane_id, account_id, source_group, capability, model_family,
    success, status_code, latency_ms, error_summary, reason)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, runID, result.LaneID, result.AccountID,
		result.SourceGroup, result.Capability, result.ModelFamily, result.Success, result.StatusCode,
		result.LatencyMs, result.ErrorSummary, result.Reason)
	return err
}

func (r *smartRouterHealthRepository) FinishCalibrationRun(ctx context.Context, runID int64, success bool, summary string) error {
	if err := r.configured(); err != nil {
		return err
	}
	status := "completed"
	if !success {
		status = "completed_with_failures"
	}
	_, err := r.db.ExecContext(ctx, `UPDATE smart_router_calibration_runs SET finished_at = NOW(), status = $2, summary = LEFT($3, 512) WHERE id = $1`, runID, status, summary)
	return err
}
