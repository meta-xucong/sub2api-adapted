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
	"github.com/lib/pq"
)

// The service performs a fast read before calling these methods.  The SQL
// transaction still rechecks the key while holding a transaction-scoped
// advisory lock, because two application instances can pass that fast read
// concurrently.  The state mutation and response row then commit together.
func (r *unifiedGatewayAdminRepository) beginAtomicIdempotency(ctx context.Context, tx *sql.Tx, record *service.UnifiedGatewayIdempotencyRecord) (bool, []byte, error) {
	if record == nil || strings.TrimSpace(record.Key) == "" || strings.TrimSpace(record.RequestDigest) == "" {
		return false, nil, service.ErrUnifiedGatewayAdminInvalidRequest
	}
	lockKey := strings.Join([]string{record.ActorID, record.Operation, record.ResourceID, record.Key}, "\x00")
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return false, nil, err
	}
	var digest string
	var response []byte
	err := tx.QueryRowContext(ctx, `SELECT request_digest, response_json FROM unified_gateway_admin_idempotency WHERE actor_id=$1 AND operation=$2 AND resource_id=$3 AND idempotency_key=$4 FOR UPDATE`, record.ActorID, record.Operation, record.ResourceID, record.Key).Scan(&digest, &response)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	if digest != record.RequestDigest {
		return false, nil, service.ErrUnifiedGatewayAdminIdempotency
	}
	if len(response) == 0 {
		return false, nil, fmt.Errorf("unified gateway idempotency response is empty")
	}
	return true, response, nil
}

func (r *unifiedGatewayAdminRepository) ProbeBindingAtomic(ctx context.Context, actorID, bindingID string, accessGroupIDs []int64, result map[string]any, record *service.UnifiedGatewayIdempotencyRecord) (map[string]any, bool, error) {
	if err := r.ready(); err != nil {
		return nil, false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if replay, response, err := r.beginAtomicIdempotency(ctx, tx, record); err != nil {
		return nil, false, err
	} else if replay {
		var out map[string]any
		if err := json.Unmarshal(response, &out); err != nil {
			return nil, false, err
		}
		return out, true, nil
	}
	var exists bool
	var existsErr error
	if accessGroupIDs == nil {
		existsErr = tx.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM unified_gateway_model_configs c,
				jsonb_array_elements(c.document->'lanes') lane,
				jsonb_array_elements(lane->'targets') target,
				jsonb_array_elements(target->'bindings') binding
			WHERE binding->>'id'=$1
		) OR EXISTS (
			SELECT 1 FROM unified_gateway_drafts d,
				jsonb_array_elements(d.document->'lanes') lane,
				jsonb_array_elements(lane->'targets') target,
				jsonb_array_elements(target->'bindings') binding
			WHERE binding->>'id'=$1
		)`, strings.TrimSpace(bindingID)).Scan(&exists)
	} else if len(accessGroupIDs) > 0 {
		existsErr = tx.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM unified_gateway_model_configs c,
				jsonb_array_elements(c.document->'lanes') lane,
				jsonb_array_elements(lane->'targets') target,
				jsonb_array_elements(target->'bindings') binding
			WHERE c.access_group_id = ANY($2) AND binding->>'id'=$1
		) OR EXISTS (
			SELECT 1 FROM unified_gateway_drafts d
			JOIN unified_gateway_model_configs c ON c.id=d.config_id,
				jsonb_array_elements(d.document->'lanes') lane,
				jsonb_array_elements(lane->'targets') target,
				jsonb_array_elements(target->'bindings') binding
			WHERE c.access_group_id = ANY($2) AND binding->>'id'=$1
		)`, strings.TrimSpace(bindingID), pq.Array(accessGroupIDs)).Scan(&exists)
	}
	if existsErr != nil {
		return nil, false, existsErr
	}
	if !exists {
		return nil, false, service.ErrUnifiedGatewayAdminNotFound
	}
	if err := finishAtomicIdempotency(ctx, tx, record, 200, result); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return result, false, nil
}

func finishAtomicIdempotency(ctx context.Context, tx *sql.Tx, record *service.UnifiedGatewayIdempotencyRecord, status int, result any) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	record.StatusCode = status
	record.ResponseJSON = encoded
	_, err = tx.ExecContext(ctx, `INSERT INTO unified_gateway_admin_idempotency (actor_id, operation, resource_id, idempotency_key, request_digest, response_status, response_json, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8)`, record.ActorID, record.Operation, record.ResourceID, record.Key, record.RequestDigest, record.StatusCode, record.ResponseJSON, record.CreatedAt)
	return classifyAdminDBError(err)
}

func (r *unifiedGatewayAdminRepository) CreateDraftAtomic(ctx context.Context, draft *service.UnifiedGatewayDraft, record *service.UnifiedGatewayIdempotencyRecord) (*service.UnifiedGatewayDraft, bool, error) {
	if err := r.ready(); err != nil {
		return nil, false, err
	}
	if draft == nil {
		return nil, false, service.ErrUnifiedGatewayAdminInvalidRequest
	}
	groupID, err := parseNumeric(draft.Document.AccessGroupID, "ag")
	if err != nil || groupID <= 0 {
		return nil, false, service.ErrUnifiedGatewayAdminInvalidRequest
	}
	raw, err := json.Marshal(draft.Document)
	if err != nil {
		return nil, false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if replay, response, err := r.beginAtomicIdempotency(ctx, tx, record); err != nil {
		return nil, false, err
	} else if replay {
		var out service.UnifiedGatewayDraft
		if err := json.Unmarshal(response, &out); err != nil {
			return nil, false, err
		}
		return &out, true, nil
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO unified_gateway_model_configs (id, access_group_id, public_model, endpoint, lifecycle, enabled, revision, document, created_by, updated_by) VALUES ($1,$2,$3,$4,'draft',FALSE,0,$5::jsonb,$6,$6)`, draft.ConfigID, groupID, draft.Document.PublicModel, draft.Document.Endpoint, raw, draft.CreatedBy); err != nil {
		return nil, false, classifyAdminDBError(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO unified_gateway_drafts (id, config_id, revision, document, created_by, updated_by, created_at, updated_at) VALUES ($1,$2,0,$3::jsonb,$4,$4,$5,$5)`, draft.ID, draft.ConfigID, raw, draft.CreatedBy, draft.CreatedAt); err != nil {
		return nil, false, classifyAdminDBError(err)
	}
	if err := finishAtomicIdempotency(ctx, tx, record, 201, draft); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return draft, false, nil
}

func (r *unifiedGatewayAdminRepository) CreateDraftFromConfigAtomic(ctx context.Context, configID string, draft *service.UnifiedGatewayDraft, record *service.UnifiedGatewayIdempotencyRecord) (*service.UnifiedGatewayDraft, bool, error) {
	if err := r.ready(); err != nil {
		return nil, false, err
	}
	if draft == nil || strings.TrimSpace(configID) == "" {
		return nil, false, service.ErrUnifiedGatewayAdminInvalidRequest
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if replay, response, err := r.beginAtomicIdempotency(ctx, tx, record); err != nil {
		return nil, false, err
	} else if replay {
		var out service.UnifiedGatewayDraft
		if err := json.Unmarshal(response, &out); err != nil {
			return nil, false, err
		}
		return &out, true, nil
	}
	var raw []byte
	var groupID int64
	var publicModel, endpoint string
	if err := tx.QueryRowContext(ctx, `SELECT access_group_id, public_model, endpoint, document FROM unified_gateway_model_configs WHERE id=$1 FOR UPDATE`, configID).Scan(&groupID, &publicModel, &endpoint, &raw); errors.Is(err, sql.ErrNoRows) {
		return nil, false, service.ErrUnifiedGatewayAdminNotFound
	} else if err != nil {
		return nil, false, err
	}
	var existing service.UnifiedGatewayDraft
	var existingRaw []byte
	var created, updated time.Time
	err = tx.QueryRowContext(ctx, `SELECT id, revision, document, created_by, updated_by, created_at, updated_at FROM unified_gateway_drafts WHERE config_id=$1`, configID).Scan(&existing.ID, &existing.Revision, &existingRaw, &existing.CreatedBy, &existing.UpdatedBy, &created, &updated)
	if err == nil {
		if err := json.Unmarshal(existingRaw, &existing.Document); err != nil {
			return nil, false, err
		}
		existing.ConfigID, existing.CreatedAt, existing.UpdatedAt = configID, created, updated
		existing.Document.ID, existing.Document.AccessGroupID, existing.Document.PublicModel, existing.Document.Endpoint = configID, opaqueNumeric("ag", groupID), publicModel, endpoint
		existing.Document.Revision = existing.Revision
		if err := finishAtomicIdempotency(ctx, tx, record, 201, &existing); err != nil {
			return nil, false, err
		}
		if err := tx.Commit(); err != nil {
			return nil, false, err
		}
		return &existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	draft.Document.ID, draft.Document.AccessGroupID, draft.Document.PublicModel, draft.Document.Endpoint = configID, opaqueNumeric("ag", groupID), publicModel, endpoint
	draft.Document.Revision = draft.Revision
	insertedRaw, err := json.Marshal(draft.Document)
	if err != nil {
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO unified_gateway_drafts (id, config_id, revision, document, created_by, updated_by, created_at, updated_at) VALUES ($1,$2,0,$3::jsonb,$4,$4,$5,$5)`, draft.ID, configID, insertedRaw, draft.CreatedBy, draft.CreatedAt); err != nil {
		return nil, false, classifyAdminDBError(err)
	}
	if err := finishAtomicIdempotency(ctx, tx, record, 201, draft); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return draft, false, nil
}

func (r *unifiedGatewayAdminRepository) UpdateDraftAtomic(ctx context.Context, id string, expectedRevision int64, document service.UnifiedGatewayConfig, actorID string, record *service.UnifiedGatewayIdempotencyRecord) (*service.UnifiedGatewayDraft, bool, error) {
	if err := r.ready(); err != nil {
		return nil, false, err
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if replay, response, err := r.beginAtomicIdempotency(ctx, tx, record); err != nil {
		return nil, false, err
	} else if replay {
		var out service.UnifiedGatewayDraft
		if err := json.Unmarshal(response, &out); err != nil {
			return nil, false, err
		}
		return &out, true, nil
	}
	next := expectedRevision + 1
	now := r.nowUTC()
	var configID string
	if err := tx.QueryRowContext(ctx, `UPDATE unified_gateway_drafts SET revision=$2, document=$3::jsonb, updated_by=$4, updated_at=$5 WHERE id=$1 AND revision=$6 RETURNING config_id`, id, next, raw, actorID, now, expectedRevision).Scan(&configID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, service.ErrUnifiedGatewayAdminVersion
		}
		return nil, false, err
	}
	document.ID, document.Revision = configID, next
	out := &service.UnifiedGatewayDraft{ID: id, Document: document, Revision: next, ConfigID: configID, UpdatedBy: actorID, UpdatedAt: now}
	if err := finishAtomicIdempotency(ctx, tx, record, 200, out); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return out, false, nil
}

func (r *unifiedGatewayAdminRepository) ApplyPricingImportAtomic(ctx context.Context, id string, expectedRevision int64, document service.UnifiedGatewayConfig, actorID string, result *service.UnifiedGatewayPricingImportResult, record *service.UnifiedGatewayIdempotencyRecord) (*service.UnifiedGatewayPricingImportResult, bool, error) {
	if err := r.ready(); err != nil {
		return nil, false, err
	}
	if result == nil {
		return nil, false, service.ErrUnifiedGatewayAdminInvalidRequest
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if replay, response, err := r.beginAtomicIdempotency(ctx, tx, record); err != nil {
		return nil, false, err
	} else if replay {
		var out service.UnifiedGatewayPricingImportResult
		if err := json.Unmarshal(response, &out); err != nil {
			return nil, false, err
		}
		return &out, true, nil
	}
	next := expectedRevision + 1
	now := r.nowUTC()
	var configID string
	if err := tx.QueryRowContext(ctx, `UPDATE unified_gateway_drafts SET revision=$2, document=$3::jsonb, updated_by=$4, updated_at=$5 WHERE id=$1 AND revision=$6 RETURNING config_id`, id, next, raw, actorID, now, expectedRevision).Scan(&configID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, service.ErrUnifiedGatewayAdminVersion
		}
		return nil, false, err
	}
	document.ID, document.Revision = configID, next
	result.Draft = &service.UnifiedGatewayDraft{ID: id, Document: document, Revision: next, ConfigID: configID, UpdatedBy: actorID, UpdatedAt: now}
	if err := finishAtomicIdempotency(ctx, tx, record, 200, result); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return result, false, nil
}

func (r *unifiedGatewayAdminRepository) PublishConfigAtomic(ctx context.Context, configID, draftID string, expectedRevision, expectedDraftRevision int64, document service.UnifiedGatewayConfig, actorID, reason, digest string, record *service.UnifiedGatewayIdempotencyRecord) (*service.UnifiedGatewayConfig, bool, error) {
	if err := r.ready(); err != nil {
		return nil, false, err
	}
	groupID, err := parseNumeric(document.AccessGroupID, "ag")
	if err != nil || groupID <= 0 {
		return nil, false, service.ErrUnifiedGatewayAdminInvalidRequest
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if replay, response, err := r.beginAtomicIdempotency(ctx, tx, record); err != nil {
		return nil, false, err
	} else if replay {
		var out service.UnifiedGatewayConfig
		if err := json.Unmarshal(response, &out); err != nil {
			return nil, false, err
		}
		return &out, true, nil
	}
	var current int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM unified_gateway_model_configs WHERE id=$1 FOR UPDATE`, configID).Scan(&current); errors.Is(err, sql.ErrNoRows) {
		return nil, false, service.ErrUnifiedGatewayAdminNotFound
	} else if err != nil {
		return nil, false, err
	}
	if current != expectedRevision {
		return nil, false, service.ErrUnifiedGatewayAdminVersion
	}
	var draftRevision int64
	var draftRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT revision, document FROM unified_gateway_drafts WHERE id=$1 AND config_id=$2 FOR UPDATE`, draftID, configID).Scan(&draftRevision, &draftRaw); errors.Is(err, sql.ErrNoRows) {
		return nil, false, service.ErrUnifiedGatewayAdminNotFound
	} else if err != nil {
		return nil, false, err
	}
	if draftRevision != expectedDraftRevision {
		return nil, false, service.ErrUnifiedGatewayAdminVersion
	}
	var storedDocument service.UnifiedGatewayConfig
	if err := json.Unmarshal(draftRaw, &storedDocument); err != nil {
		return nil, false, err
	}
	// The locked draft is authoritative. The caller's document was validated
	// before the transaction; publishing the stored row prevents stale or
	// hydrated request fields from bypassing the draft revision check.
	document = storedDocument
	raw, err = json.Marshal(document)
	if err != nil {
		return nil, false, err
	}
	next := current + 1
	if _, err := tx.ExecContext(ctx, `UPDATE unified_gateway_model_configs SET access_group_id=$2, public_model=$3, endpoint=$4, lifecycle='published', enabled=TRUE, revision=$5, document=$6::jsonb, updated_by=$7, updated_at=NOW() WHERE id=$1`, configID, groupID, document.PublicModel, document.Endpoint, next, raw, actorID); err != nil {
		return nil, false, err
	}
	if err := r.insertRevision(ctx, tx, configID, next, service.UnifiedGatewayLifecyclePublished, raw, actorID, reason, digest); err != nil {
		return nil, false, err
	}
	if err := r.materialize(ctx, tx, configID, next, document); err != nil {
		return nil, false, err
	}
	document.ID, document.Revision, document.Lifecycle, document.Readiness = configID, next, service.UnifiedGatewayLifecyclePublished, service.UnifiedGatewayReadinessReady
	if err := finishAtomicIdempotency(ctx, tx, record, 200, &document); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return &document, false, nil
}

func (r *unifiedGatewayAdminRepository) DisableConfigAtomic(ctx context.Context, configID string, expectedRevision int64, actorID, reason string, record *service.UnifiedGatewayIdempotencyRecord) (*service.UnifiedGatewayConfig, bool, error) {
	if err := r.ready(); err != nil {
		return nil, false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if replay, response, err := r.beginAtomicIdempotency(ctx, tx, record); err != nil {
		return nil, false, err
	} else if replay {
		var out service.UnifiedGatewayConfig
		if err := json.Unmarshal(response, &out); err != nil {
			return nil, false, err
		}
		return &out, true, nil
	}
	var current, groupID int64
	var raw []byte
	var model, endpoint string
	if err := tx.QueryRowContext(ctx, `SELECT revision, document, access_group_id, public_model, endpoint FROM unified_gateway_model_configs WHERE id=$1 FOR UPDATE`, configID).Scan(&current, &raw, &groupID, &model, &endpoint); errors.Is(err, sql.ErrNoRows) {
		return nil, false, service.ErrUnifiedGatewayAdminNotFound
	} else if err != nil {
		return nil, false, err
	}
	if current != expectedRevision {
		return nil, false, service.ErrUnifiedGatewayAdminVersion
	}
	next := current + 1
	if _, err := tx.ExecContext(ctx, `UPDATE unified_gateway_model_configs SET lifecycle='disabled', enabled=FALSE, revision=$2, updated_by=$3, updated_at=NOW() WHERE id=$1`, configID, next, actorID); err != nil {
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE unified_route_targets SET enabled=FALSE, updated_at=NOW() WHERE unified_config_id=$1`, configID); err != nil {
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE unified_route_account_bindings SET enabled=FALSE, updated_at=NOW() WHERE unified_config_id=$1`, configID); err != nil {
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE unified_gateway_billing_lanes SET enabled=FALSE, updated_at=NOW() WHERE config_id=$1`, configID); err != nil {
		return nil, false, err
	}
	if err := r.insertRevision(ctx, tx, configID, next, service.UnifiedGatewayLifecycleDisabled, raw, actorID, reason, digestForRaw(raw)); err != nil {
		return nil, false, err
	}
	var document service.UnifiedGatewayConfig
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, false, err
	}
	document.ID, document.AccessGroupID, document.PublicModel, document.Endpoint, document.Lifecycle, document.Revision, document.Readiness = configID, opaqueNumeric("ag", groupID), model, endpoint, service.UnifiedGatewayLifecycleDisabled, next, service.UnifiedGatewayReadinessBlocked
	if err := finishAtomicIdempotency(ctx, tx, record, 200, &document); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return &document, false, nil
}

func (r *unifiedGatewayAdminRepository) RestoreConfigAtomic(ctx context.Context, configID string, sourceRevision, expectedRevision int64, actorID, reason string, record *service.UnifiedGatewayIdempotencyRecord) (*service.UnifiedGatewayConfig, bool, error) {
	if err := r.ready(); err != nil {
		return nil, false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if replay, response, err := r.beginAtomicIdempotency(ctx, tx, record); err != nil {
		return nil, false, err
	} else if replay {
		var out service.UnifiedGatewayConfig
		if err := json.Unmarshal(response, &out); err != nil {
			return nil, false, err
		}
		return &out, true, nil
	}
	var current int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM unified_gateway_model_configs WHERE id=$1 FOR UPDATE`, configID).Scan(&current); errors.Is(err, sql.ErrNoRows) {
		return nil, false, service.ErrUnifiedGatewayAdminNotFound
	} else if err != nil {
		return nil, false, err
	}
	if current != expectedRevision {
		return nil, false, service.ErrUnifiedGatewayAdminVersion
	}
	var raw []byte
	var groupID int64
	var model, endpoint string
	if err := tx.QueryRowContext(ctx, `SELECT r.document, c.access_group_id, c.public_model, c.endpoint FROM unified_gateway_config_revisions r JOIN unified_gateway_model_configs c ON c.id=r.config_id WHERE r.config_id=$1 AND r.revision=$2`, configID, sourceRevision).Scan(&raw, &groupID, &model, &endpoint); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, service.ErrUnifiedGatewayAdminNotFound
		}
		return nil, false, err
	}
	var document service.UnifiedGatewayConfig
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, false, err
	}
	document.ID, document.AccessGroupID, document.PublicModel, document.Endpoint = configID, opaqueNumeric("ag", groupID), model, endpoint
	next := current + 1
	raw, _ = json.Marshal(document)
	if _, err := tx.ExecContext(ctx, `UPDATE unified_gateway_model_configs SET lifecycle='published', enabled=TRUE, revision=$2, document=$3::jsonb, updated_by=$4, updated_at=NOW() WHERE id=$1`, configID, next, raw, actorID); err != nil {
		return nil, false, err
	}
	if err := r.insertRevision(ctx, tx, configID, next, service.UnifiedGatewayLifecyclePublished, raw, actorID, reason, digestForRaw(raw)); err != nil {
		return nil, false, err
	}
	if err := r.materialize(ctx, tx, configID, next, document); err != nil {
		return nil, false, err
	}
	document.Lifecycle, document.Revision, document.Readiness = service.UnifiedGatewayLifecyclePublished, next, service.UnifiedGatewayReadinessReady
	if err := finishAtomicIdempotency(ctx, tx, record, 200, &document); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return &document, false, nil
}

func (r *unifiedGatewayAdminRepository) nowUTC() (out time.Time) {
	return time.Now().UTC()
}
