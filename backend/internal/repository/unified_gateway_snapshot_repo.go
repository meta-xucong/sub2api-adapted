package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// unifiedGatewaySnapshotRepository is intentionally a raw-SQL repository.  It
// keeps the new isolated tables out of the existing Ent graph, which reduces
// generated-code churn and guarantees that legacy repositories cannot start
// reading unified snapshots accidentally.
type unifiedGatewaySnapshotRepository struct {
	db *sql.DB
}

var _ service.UnifiedGatewayRecoveryStore = (*unifiedGatewaySnapshotRepository)(nil)
var _ service.UnifiedGatewaySnapshotRecoveryStore = (*unifiedGatewaySnapshotRepository)(nil)

func NewUnifiedGatewaySnapshotRepository(db *sql.DB) service.UnifiedGatewayPriceSnapshotStore {
	return &unifiedGatewaySnapshotRepository{db: db}
}

func (r *unifiedGatewaySnapshotRepository) Create(ctx context.Context, record *service.UnifiedGatewayPriceSnapshotRecord) (*service.UnifiedGatewayPriceSnapshotRecord, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("unified gateway snapshot repository db is nil")
	}
	if record == nil || record.APIKeyID <= 0 || record.UserID <= 0 || record.AccessGroupID <= 0 || strings.TrimSpace(record.RequestID) == "" || strings.TrimSpace(record.AttemptID) == "" || record.Snapshot.Digest == "" {
		return nil, service.ErrUnifiedGatewayInvalidRequest
	}
	requestID := strings.TrimSpace(record.RequestID)
	attemptID := strings.TrimSpace(record.AttemptID)
	selectionJSON, err := json.Marshal(record.Selection)
	if err != nil {
		return nil, err
	}
	snapshotJSON, err := json.Marshal(record.Snapshot)
	if err != nil {
		return nil, err
	}
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	status := record.Status
	if status == "" {
		status = service.UnifiedGatewaySnapshotQuoted
	}
	created := *record
	created.RequestID = requestID
	created.AttemptID = attemptID
	created.CreatedAt = createdAt
	created.Status = status
	err = r.db.QueryRowContext(ctx, `
		INSERT INTO unified_route_price_snapshots (
			api_key_id, user_id, request_id, attempt_id, route_target_id, access_group_id, billing_lane_id,
			account_id, provider_identity, public_model, upstream_model, endpoint,
			status, selection_json, snapshot_json, response_body, measured_units,
			user_charge, upstream_request_id, failure_message, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::jsonb, $15::jsonb, $16::jsonb, $17, $18, $19, $20, $21)
		RETURNING id
	`,
		record.APIKeyID,
		record.UserID,
		requestID,
		attemptID,
		record.Snapshot.RouteID,
		record.AccessGroupID,
		record.Snapshot.BillingLaneID,
		record.Snapshot.AccountID,
		record.Snapshot.ProviderIdentity,
		record.Snapshot.PublicModel,
		record.Snapshot.UpstreamModel,
		record.Snapshot.Endpoint,
		status,
		selectionJSON,
		snapshotJSON,
		nil,
		0.0,
		0.0,
		"",
		"",
		createdAt,
	).Scan(&created.ID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") || strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, service.ErrUnifiedGatewaySnapshotConflict
		}
		return nil, err
	}
	return &created, nil
}

func (r *unifiedGatewaySnapshotRepository) Get(ctx context.Context, apiKeyID, userID, accessGroupID int64, requestID, attemptID string) (*service.UnifiedGatewayPriceSnapshotRecord, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("unified gateway snapshot repository db is nil")
	}
	var (
		record                                           service.UnifiedGatewayPriceSnapshotRecord
		selectionJSON, snapshotJSON, bodyJSON, bodyBytes []byte
		status, upstreamRequestID, failure               string
		measuredUnits, userCharge                        float64
		createdAt                                        time.Time
		finalizedAt                                      sql.NullTime
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT id, api_key_id, user_id, access_group_id, request_id, attempt_id, status, selection_json, snapshot_json,
			response_body, response_body_bytes, measured_units, user_charge, upstream_request_id,
			failure_message, created_at, finalized_at
		FROM unified_route_price_snapshots
		WHERE api_key_id = $1 AND user_id = $2 AND access_group_id = $3 AND request_id = $4 AND attempt_id = $5
	`, apiKeyID, userID, accessGroupID, strings.TrimSpace(requestID), strings.TrimSpace(attemptID)).Scan(
		&record.ID, &record.APIKeyID, &record.UserID, &record.AccessGroupID, &record.RequestID, &record.AttemptID, &status, &selectionJSON, &snapshotJSON,
		&bodyJSON, &bodyBytes, &measuredUnits, &userCharge, &upstreamRequestID, &failure, &createdAt, &finalizedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrUnifiedGatewaySnapshotNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(selectionJSON, &record.Selection); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(snapshotJSON, &record.Snapshot); err != nil {
		return nil, err
	}
	record.Status = service.UnifiedGatewaySnapshotStatus(status)
	if len(bodyBytes) > 0 {
		record.ResponseBody = append([]byte(nil), bodyBytes...)
	} else {
		record.ResponseBody = append([]byte(nil), bodyJSON...)
	}
	record.MeasuredUnits = measuredUnits
	record.UserCharge = userCharge
	record.UpstreamRequestID = upstreamRequestID
	record.FailureMessage = failure
	record.CreatedAt = createdAt
	if finalizedAt.Valid {
		record.FinalizedAt = finalizedAt.Time
	}
	return &record, nil
}

func (r *unifiedGatewaySnapshotRepository) Finalize(ctx context.Context, apiKeyID, userID, accessGroupID int64, requestID, attemptID string, status service.UnifiedGatewaySnapshotStatus, measuredUnits, userCharge float64, upstreamRequestID string, responseBody []byte, failureMessage string) error {
	if r == nil || r.db == nil {
		return errors.New("unified gateway snapshot repository db is nil")
	}
	responseValue := any(nil)
	if len(responseBody) > 0 && json.Valid(responseBody) {
		responseValue = responseBody
	}
	responseBytes := append([]byte(nil), responseBody...)
	result, err := r.db.ExecContext(ctx, `
		UPDATE unified_route_price_snapshots
		SET status = $6,
			measured_units = $7,
			user_charge = $8,
			upstream_request_id = $9,
			response_body = $10::jsonb,
			response_body_bytes = $11::bytea,
			failure_message = $12,
			finalized_at = NOW()
		WHERE api_key_id = $1 AND user_id = $2 AND access_group_id = $3 AND request_id = $4 AND attempt_id = $5
			AND status IN ('quoted', 'reserved', 'pending')
	`, apiKeyID, userID, accessGroupID, strings.TrimSpace(requestID), strings.TrimSpace(attemptID), string(status), measuredUnits, userCharge, strings.TrimSpace(upstreamRequestID), responseValue, responseBytes, strings.TrimSpace(failureMessage))
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows > 0 {
		return nil
	}
	existing, err := r.Get(ctx, apiKeyID, userID, accessGroupID, requestID, attemptID)
	if err != nil {
		return err
	}
	if existing.Status == status {
		return nil
	}
	return service.ErrUnifiedGatewaySnapshotConflict
}

// RecoverSnapshot is a guarded repair used by the durable reconciler. Unlike
// Finalize, it can repair a terminal row after the other side of a ledger
// transition committed before the process stopped. The expected status keeps
// a stale worker from overwriting a newer request completion.
func (r *unifiedGatewaySnapshotRepository) RecoverSnapshot(ctx context.Context, apiKeyID, userID, accessGroupID int64, requestID, attemptID string, expectedStatus, status service.UnifiedGatewaySnapshotStatus, measuredUnits, userCharge float64, upstreamRequestID string, responseBody []byte, failureMessage string) error {
	if r == nil || r.db == nil {
		return errors.New("unified gateway snapshot repository db is nil")
	}
	if userCharge < 0 {
		return service.ErrUnifiedGatewayInvalidRequest
	}
	if !unifiedGatewayRecoverySnapshotTransitionAllowed(expectedStatus, status) {
		return service.ErrUnifiedGatewaySnapshotConflict
	}
	responseValue := any(nil)
	if len(responseBody) > 0 && json.Valid(responseBody) {
		responseValue = responseBody
	}
	responseBytes := append([]byte(nil), responseBody...)
	result, err := r.db.ExecContext(ctx, `
		UPDATE unified_route_price_snapshots
		SET status = $6,
			measured_units = $7,
			user_charge = $8,
			upstream_request_id = $9,
			response_body = $10::jsonb,
			response_body_bytes = $11::bytea,
			failure_message = $12,
			finalized_at = NOW()
		WHERE api_key_id = $1 AND user_id = $2 AND access_group_id = $3 AND request_id = $4 AND attempt_id = $5
			AND status = $13
	`, apiKeyID, userID, accessGroupID, strings.TrimSpace(requestID), strings.TrimSpace(attemptID), string(status), measuredUnits, userCharge, strings.TrimSpace(upstreamRequestID), responseValue, responseBytes, strings.TrimSpace(failureMessage), string(expectedStatus))
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows > 0 {
		return nil
	}
	existing, err := r.Get(ctx, apiKeyID, userID, accessGroupID, requestID, attemptID)
	if err != nil {
		return err
	}
	if existing.Status == status {
		return nil
	}
	return service.ErrUnifiedGatewaySnapshotConflict
}

func unifiedGatewayRecoverySnapshotTransitionAllowed(from, to service.UnifiedGatewaySnapshotStatus) bool {
	if from == to {
		return true
	}
	switch from {
	case service.UnifiedGatewaySnapshotQuoted:
		return to == service.UnifiedGatewaySnapshotReserved || to == service.UnifiedGatewaySnapshotCaptured || to == service.UnifiedGatewaySnapshotReleased || to == service.UnifiedGatewaySnapshotSettlementFailed
	case service.UnifiedGatewaySnapshotReserved:
		return to == service.UnifiedGatewaySnapshotPending || to == service.UnifiedGatewaySnapshotCaptured || to == service.UnifiedGatewaySnapshotReleased || to == service.UnifiedGatewaySnapshotSettlementFailed
	case service.UnifiedGatewaySnapshotPending:
		return to == service.UnifiedGatewaySnapshotCaptured || to == service.UnifiedGatewaySnapshotReleased || to == service.UnifiedGatewaySnapshotSettlementFailed
	case service.UnifiedGatewaySnapshotCaptured:
		return to == service.UnifiedGatewaySnapshotSettlementFailed
	default:
		return false
	}
}

func (r *unifiedGatewaySnapshotRepository) DiscoverRecoveryTasks(ctx context.Context, limit int) error {
	if r == nil || r.db == nil {
		return errors.New("unified gateway snapshot repository db is nil")
	}
	if limit <= 0 {
		limit = service.UnifiedGatewayDefaultRecoveryBatchSize
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT api_key_id, user_id, access_group_id, request_id, attempt_id, status, user_charge
		FROM unified_route_price_snapshots
		WHERE status IN ('quoted', 'reserved', 'pending', 'captured', 'released', 'settlement_failed')
			AND NOT EXISTS (
				SELECT 1
				FROM unified_gateway_recovery_tasks t
				WHERE t.api_key_id = unified_route_price_snapshots.api_key_id
					AND t.user_id = unified_route_price_snapshots.user_id
					AND t.access_group_id = unified_route_price_snapshots.access_group_id
					AND t.request_id = unified_route_price_snapshots.request_id
					AND t.attempt_id = unified_route_price_snapshots.attempt_id
			)
		ORDER BY created_at ASC, id ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return err
	}
	type recoveryCandidate struct {
		apiKeyID, userID, accessGroupID int64
		requestID, attemptID, status    string
		userCharge                      float64
	}
	candidates := make([]recoveryCandidate, 0, limit)
	for rows.Next() {
		var apiKeyID, userID, accessGroupID int64
		var requestID, attemptID, status string
		var userCharge float64
		if err := rows.Scan(&apiKeyID, &userID, &accessGroupID, &requestID, &attemptID, &status, &userCharge); err != nil {
			_ = rows.Close()
			return err
		}
		candidates = append(candidates, recoveryCandidate{apiKeyID: apiKeyID, userID: userID, accessGroupID: accessGroupID, requestID: requestID, attemptID: attemptID, status: status, userCharge: userCharge})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, candidate := range candidates {
		reservationKey := service.UnifiedGatewayReservationKey(candidate.apiKeyID, candidate.userID, candidate.accessGroupID, candidate.requestID, candidate.attemptID)
		_, err := r.db.ExecContext(ctx, `
			INSERT INTO unified_gateway_recovery_tasks (
				api_key_id, user_id, access_group_id, request_id, attempt_id,
				reservation_key, snapshot_status, user_charge, status, next_attempt_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', NOW())
			ON CONFLICT (api_key_id, user_id, access_group_id, request_id, attempt_id) DO NOTHING
		`, candidate.apiKeyID, candidate.userID, candidate.accessGroupID, strings.TrimSpace(candidate.requestID), strings.TrimSpace(candidate.attemptID), unifiedGatewayLedgerKeyDigest(reservationKey), candidate.status, candidate.userCharge)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *unifiedGatewaySnapshotRepository) ClaimRecoveryTasks(ctx context.Context, limit int, now time.Time) ([]service.UnifiedGatewayRecoveryTask, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("unified gateway snapshot repository db is nil")
	}
	if limit <= 0 {
		limit = service.UnifiedGatewayDefaultRecoveryBatchSize
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `
		SELECT id, api_key_id, user_id, access_group_id, request_id, attempt_id,
			reservation_key, snapshot_status, user_charge, attempts
		FROM unified_gateway_recovery_tasks
		WHERE (status = 'pending' AND next_attempt_at <= $1)
			OR (status = 'processing' AND (locked_until IS NULL OR locked_until <= $1))
		ORDER BY next_attempt_at ASC, id ASC
		LIMIT $2
		FOR UPDATE SKIP LOCKED
	`, now, limit)
	if err != nil {
		return nil, err
	}
	var tasks []service.UnifiedGatewayRecoveryTask
	for rows.Next() {
		var task service.UnifiedGatewayRecoveryTask
		var status string
		if err := rows.Scan(&task.ID, &task.APIKeyID, &task.UserID, &task.AccessGroupID, &task.RequestID, &task.AttemptID, &task.ReservationDigest, &status, &task.UserCharge, &task.Attempts); err != nil {
			_ = rows.Close()
			return nil, err
		}
		task.SnapshotStatus = service.UnifiedGatewaySnapshotStatus(status)
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	leaseUntil := now.Add(5 * time.Minute)
	for i := range tasks {
		if _, err := tx.ExecContext(ctx, `
			UPDATE unified_gateway_recovery_tasks
			SET status = 'processing', attempts = attempts + 1, locked_until = $2, updated_at = $3
			WHERE id = $1
		`, tasks[i].ID, leaseUntil, now); err != nil {
			return nil, err
		}
		tasks[i].Attempts++
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (r *unifiedGatewaySnapshotRepository) CompleteRecoveryTask(ctx context.Context, taskID int64, attempts int) error {
	if r == nil || r.db == nil {
		return errors.New("unified gateway snapshot repository db is nil")
	}
	if taskID <= 0 || attempts <= 0 {
		return service.ErrUnifiedGatewayInvalidRequest
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE unified_gateway_recovery_tasks
		SET status = 'completed', locked_until = NULL, updated_at = NOW()
		WHERE id = $1 AND status = 'processing' AND attempts = $2
	`, taskID, attempts)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		return service.ErrUnifiedGatewayRecoveryLeaseLost
	}
	return nil
}

func (r *unifiedGatewaySnapshotRepository) RetryRecoveryTask(ctx context.Context, taskID int64, attempts int, nextAttemptAt time.Time, lastError string) error {
	if r == nil || r.db == nil {
		return errors.New("unified gateway snapshot repository db is nil")
	}
	if taskID <= 0 || attempts <= 0 || nextAttemptAt.IsZero() {
		return service.ErrUnifiedGatewayInvalidRequest
	}
	lastError = strings.TrimSpace(lastError)
	if len(lastError) > 2048 {
		lastError = lastError[:2048]
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE unified_gateway_recovery_tasks
		SET status = 'pending', next_attempt_at = $2, locked_until = NULL, last_error = $3, updated_at = NOW()
		WHERE id = $1 AND status = 'processing' AND attempts = $4
	`, taskID, nextAttemptAt, lastError, attempts)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		return service.ErrUnifiedGatewayRecoveryLeaseLost
	}
	return nil
}

// FindByUpstreamRequestID is the optional durable lookup used by asynchronous
// provider callbacks and pollers.  Owner scope remains part of the lookup so a
// provider job id can never disclose another user's snapshot.
func (r *unifiedGatewaySnapshotRepository) FindByUpstreamRequestID(ctx context.Context, apiKeyID, userID, accessGroupID int64, upstreamRequestID string) (*service.UnifiedGatewayPriceSnapshotRecord, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("unified gateway snapshot repository db is nil")
	}
	upstreamRequestID = strings.TrimSpace(upstreamRequestID)
	if upstreamRequestID == "" {
		return nil, service.ErrUnifiedGatewaySnapshotNotFound
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, api_key_id, user_id, access_group_id, request_id, attempt_id, status, selection_json, snapshot_json,
			response_body, response_body_bytes, measured_units, user_charge, upstream_request_id,
			failure_message, created_at, finalized_at
		FROM unified_route_price_snapshots
		WHERE api_key_id = $1 AND user_id = $2 AND access_group_id = $3 AND upstream_request_id = $4
	`, apiKeyID, userID, accessGroupID, upstreamRequestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var found *service.UnifiedGatewayPriceSnapshotRecord
	for rows.Next() {
		var (
			record                                           service.UnifiedGatewayPriceSnapshotRecord
			selectionJSON, snapshotJSON, bodyJSON, bodyBytes []byte
			status, foundUpstreamRequestID, failure          string
			measuredUnits, userCharge                        float64
			createdAt                                        time.Time
			finalizedAt                                      sql.NullTime
		)
		if err := rows.Scan(
			&record.ID, &record.APIKeyID, &record.UserID, &record.AccessGroupID, &record.RequestID, &record.AttemptID, &status, &selectionJSON, &snapshotJSON,
			&bodyJSON, &bodyBytes, &measuredUnits, &userCharge, &foundUpstreamRequestID, &failure, &createdAt, &finalizedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(selectionJSON, &record.Selection); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(snapshotJSON, &record.Snapshot); err != nil {
			return nil, err
		}
		record.Status = service.UnifiedGatewaySnapshotStatus(status)
		if len(bodyBytes) > 0 {
			record.ResponseBody = append([]byte(nil), bodyBytes...)
		} else {
			record.ResponseBody = append([]byte(nil), bodyJSON...)
		}
		record.MeasuredUnits = measuredUnits
		record.UserCharge = userCharge
		record.UpstreamRequestID = foundUpstreamRequestID
		record.FailureMessage = failure
		record.CreatedAt = createdAt
		if finalizedAt.Valid {
			record.FinalizedAt = finalizedAt.Time
		}
		if found != nil && (found.RequestID != record.RequestID || found.AttemptID != record.AttemptID) {
			return nil, fmt.Errorf("%w: duplicate upstream request id", service.ErrUnifiedGatewaySnapshotConflict)
		}
		copyRecord := record
		found = &copyRecord
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if found == nil {
		return nil, service.ErrUnifiedGatewaySnapshotNotFound
	}
	return found, nil
}
