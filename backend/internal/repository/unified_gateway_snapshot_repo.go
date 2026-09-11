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
		record                             service.UnifiedGatewayPriceSnapshotRecord
		selectionJSON, snapshotJSON, body  []byte
		status, upstreamRequestID, failure string
		measuredUnits, userCharge          float64
		createdAt                          time.Time
		finalizedAt                        sql.NullTime
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT id, api_key_id, user_id, access_group_id, request_id, attempt_id, status, selection_json, snapshot_json,
			response_body, measured_units, user_charge, upstream_request_id,
			failure_message, created_at, finalized_at
		FROM unified_route_price_snapshots
		WHERE api_key_id = $1 AND user_id = $2 AND access_group_id = $3 AND request_id = $4 AND attempt_id = $5
	`, apiKeyID, userID, accessGroupID, strings.TrimSpace(requestID), strings.TrimSpace(attemptID)).Scan(
		&record.ID, &record.APIKeyID, &record.UserID, &record.AccessGroupID, &record.RequestID, &record.AttemptID, &status, &selectionJSON, &snapshotJSON,
		&body, &measuredUnits, &userCharge, &upstreamRequestID, &failure, &createdAt, &finalizedAt,
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
	record.ResponseBody = append([]byte(nil), body...)
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
	responseValue, err := nullableJSON(responseBody)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE unified_route_price_snapshots
		SET status = $6,
			measured_units = $7,
			user_charge = $8,
			upstream_request_id = $9,
			response_body = $10::jsonb,
			failure_message = $11,
			finalized_at = NOW()
		WHERE api_key_id = $1 AND user_id = $2 AND access_group_id = $3 AND request_id = $4 AND attempt_id = $5
			AND status IN ('quoted', 'reserved')
	`, apiKeyID, userID, accessGroupID, strings.TrimSpace(requestID), strings.TrimSpace(attemptID), string(status), measuredUnits, userCharge, strings.TrimSpace(upstreamRequestID), responseValue, strings.TrimSpace(failureMessage))
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

func nullableJSON(body []byte) (any, error) {
	if len(body) == 0 {
		return nil, nil
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("unified gateway response body is not valid JSON")
	}
	return body, nil
}
