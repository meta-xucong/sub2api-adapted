package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// unifiedGatewayAdminRepository owns the Phase 5 administration aggregate and
// its publication projection. Runtime settlement and recovery remain outside
// this repository slice.
type unifiedGatewayAdminRepository struct{ db *sql.DB }

func NewUnifiedGatewayAdminRepository(db *sql.DB) service.UnifiedGatewayAdminRepository {
	return &unifiedGatewayAdminRepository{db: db}
}

var (
	_ service.UnifiedGatewayAdminRepository                    = (*unifiedGatewayAdminRepository)(nil)
	_ service.UnifiedGatewayAdminAtomicRepository              = (*unifiedGatewayAdminRepository)(nil)
	_ service.UnifiedGatewayAdminPricingImportAtomicRepository = (*unifiedGatewayAdminRepository)(nil)
)

func (r *unifiedGatewayAdminRepository) ready() error {
	if r == nil || r.db == nil {
		return errors.New("unified gateway admin repository db is nil")
	}
	return nil
}

func (r *unifiedGatewayAdminRepository) CheckSchema(ctx context.Context) (service.UnifiedGatewaySchemaReadiness, error) {
	if err := r.ready(); err != nil {
		return service.UnifiedGatewaySchemaReadiness{}, err
	}
	readiness, err := checkUnifiedGatewayAdminSchema(ctx, r.db)
	if err != nil {
		return service.UnifiedGatewaySchemaReadiness{}, err
	}
	return service.UnifiedGatewaySchemaReadiness{
		Ready: readiness.Ready, Version: readiness.Version, Missing: readiness.Missing,
	}, nil
}

func (r *unifiedGatewayAdminRepository) ListConfigs(ctx context.Context, filter service.UnifiedGatewayConfigListFilter) ([]service.UnifiedGatewayConfig, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}
	whereParts := make([]string, 0, 2)
	args := make([]any, 0, 4)
	if lifecycle := strings.TrimSpace(filter.Lifecycle); lifecycle != "" {
		args = append(args, lifecycle)
		whereParts = append(whereParts, "lifecycle = $"+strconv.Itoa(len(args)))
	}
	if filter.AccessGroupIDs != nil {
		if len(filter.AccessGroupIDs) == 0 {
			whereParts = append(whereParts, "FALSE")
		} else {
			args = append(args, pq.Array(filter.AccessGroupIDs))
			whereParts = append(whereParts, "access_group_id = $"+strconv.Itoa(len(args))+"::bigint[]")
			whereParts[len(whereParts)-1] = "access_group_id = ANY($" + strconv.Itoa(len(args)) + ")"
		}
	}
	where := ""
	if len(whereParts) > 0 {
		where = " WHERE " + strings.Join(whereParts, " AND ")
	}
	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM unified_gateway_model_configs"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
	query := `SELECT id, access_group_id, public_model, endpoint, lifecycle, revision, document
		FROM unified_gateway_model_configs` + where + ` ORDER BY updated_at DESC, id ASC LIMIT $` + strconv.Itoa(len(args)-1) + ` OFFSET $` + strconv.Itoa(len(args))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]service.UnifiedGatewayConfig, 0)
	for rows.Next() {
		var item service.UnifiedGatewayConfig
		var groupID, revision int64
		var raw []byte
		var id, lifecycle, publicModel, endpoint string
		if err := rows.Scan(&id, &groupID, &publicModel, &endpoint, &lifecycle, &revision, &raw); err != nil {
			return nil, 0, err
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &item); err != nil {
				return nil, 0, err
			}
		}
		item.ID, item.AccessGroupID, item.PublicModel, item.Endpoint, item.Lifecycle, item.Revision = id, opaqueNumeric("ag", groupID), publicModel, endpoint, lifecycle, revision
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (r *unifiedGatewayAdminRepository) GetConfig(ctx context.Context, id string) (*service.UnifiedGatewayConfig, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var item service.UnifiedGatewayConfig
	var groupID, revision int64
	var raw []byte
	var dbID, lifecycle, publicModel, endpoint string
	err := r.db.QueryRowContext(ctx, `SELECT id, access_group_id, public_model, endpoint, lifecycle, revision, document FROM unified_gateway_model_configs WHERE id = $1`, strings.TrimSpace(id)).Scan(&dbID, &groupID, &publicModel, &endpoint, &lifecycle, &revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrUnifiedGatewayAdminNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
	}
	item.ID, item.AccessGroupID, item.PublicModel, item.Endpoint, item.Lifecycle, item.Revision = dbID, opaqueNumeric("ag", groupID), publicModel, endpoint, lifecycle, revision
	return &item, nil
}

func (r *unifiedGatewayAdminRepository) GetDraft(ctx context.Context, id string) (*service.UnifiedGatewayDraft, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var draft service.UnifiedGatewayDraft
	var raw []byte
	var created, updated time.Time
	var groupID int64
	err := r.db.QueryRowContext(ctx, `
		SELECT d.id, d.config_id, d.revision, d.document, d.created_by, d.updated_by, d.created_at, d.updated_at,
			c.access_group_id
		FROM unified_gateway_drafts d JOIN unified_gateway_model_configs c ON c.id = d.config_id WHERE d.id = $1`, strings.TrimSpace(id)).
		Scan(&draft.ID, &draft.ConfigID, &draft.Revision, &raw, &draft.CreatedBy, &draft.UpdatedBy, &created, &updated, &groupID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrUnifiedGatewayAdminNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &draft.Document); err != nil {
		return nil, err
	}
	draft.Document.AccessGroupID = opaqueNumeric("ag", groupID)
	draft.Document.ID = draft.ConfigID
	draft.Document.Revision = draft.Revision
	draft.CreatedAt, draft.UpdatedAt = created, updated
	return &draft, nil
}

func (r *unifiedGatewayAdminRepository) CreateDraft(ctx context.Context, draft *service.UnifiedGatewayDraft) (*service.UnifiedGatewayDraft, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if draft == nil {
		return nil, service.ErrUnifiedGatewayAdminInvalidRequest
	}
	groupID, err := parseNumeric(draft.Document.AccessGroupID, "ag")
	if err != nil || groupID <= 0 {
		return nil, service.ErrUnifiedGatewayAdminInvalidRequest
	}
	raw, err := json.Marshal(draft.Document)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO unified_gateway_model_configs (id, access_group_id, public_model, endpoint, lifecycle, enabled, revision, document, created_by, updated_by) VALUES ($1,$2,$3,$4,'draft',FALSE,0,$5::jsonb,$6,$6)`, draft.ConfigID, groupID, draft.Document.PublicModel, draft.Document.Endpoint, raw, draft.CreatedBy); err != nil {
		return nil, classifyAdminDBError(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO unified_gateway_drafts (id, config_id, revision, document, created_by, updated_by, created_at, updated_at) VALUES ($1,$2,0,$3::jsonb,$4,$4,$5,$5)`, draft.ID, draft.ConfigID, raw, draft.CreatedBy, draft.CreatedAt); err != nil {
		return nil, classifyAdminDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return draft, nil
}

func (r *unifiedGatewayAdminRepository) CreateDraftFromConfig(ctx context.Context, configID string, draft *service.UnifiedGatewayDraft) (*service.UnifiedGatewayDraft, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if draft == nil || strings.TrimSpace(configID) == "" {
		return nil, service.ErrUnifiedGatewayAdminInvalidRequest
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var raw []byte
	var groupID int64
	var publicModel, endpoint string
	if err := tx.QueryRowContext(ctx, `SELECT access_group_id, public_model, endpoint, document FROM unified_gateway_model_configs WHERE id=$1 FOR UPDATE`, configID).Scan(&groupID, &publicModel, &endpoint, &raw); errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrUnifiedGatewayAdminNotFound
	} else if err != nil {
		return nil, err
	}
	var existing service.UnifiedGatewayDraft
	var existingRaw []byte
	var created, updated time.Time
	err = tx.QueryRowContext(ctx, `SELECT id, revision, document, created_by, updated_by, created_at, updated_at FROM unified_gateway_drafts WHERE config_id=$1`, configID).Scan(&existing.ID, &existing.Revision, &existingRaw, &existing.CreatedBy, &existing.UpdatedBy, &created, &updated)
	if err == nil {
		if err := json.Unmarshal(existingRaw, &existing.Document); err != nil {
			return nil, err
		}
		existing.ConfigID, existing.CreatedAt, existing.UpdatedAt = configID, created, updated
		existing.Document.ID, existing.Document.AccessGroupID, existing.Document.PublicModel, existing.Document.Endpoint = configID, opaqueNumeric("ag", groupID), publicModel, endpoint
		existing.Document.Revision = existing.Revision
		return &existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	draft.Document.ID, draft.Document.AccessGroupID, draft.Document.PublicModel, draft.Document.Endpoint = configID, opaqueNumeric("ag", groupID), publicModel, endpoint
	draft.Document.Revision = draft.Revision
	insertedRaw, err := json.Marshal(draft.Document)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO unified_gateway_drafts (id, config_id, revision, document, created_by, updated_by, created_at, updated_at) VALUES ($1,$2,0,$3::jsonb,$4,$4,$5,$5)`, draft.ID, configID, insertedRaw, draft.CreatedBy, draft.CreatedAt); err != nil {
		return nil, classifyAdminDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return draft, nil
}

func (r *unifiedGatewayAdminRepository) UpdateDraft(ctx context.Context, id string, expectedRevision int64, document service.UnifiedGatewayConfig, actorID string) (*service.UnifiedGatewayDraft, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	next, now := expectedRevision+1, time.Now().UTC()
	var configID string
	err = tx.QueryRowContext(ctx, `UPDATE unified_gateway_drafts SET revision=$2, document=$3::jsonb, updated_by=$4, updated_at=$5 WHERE id=$1 AND revision=$6 RETURNING config_id`, id, next, raw, actorID, now, expectedRevision).Scan(&configID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrUnifiedGatewayAdminVersion
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	document.ID, document.Revision = configID, next
	return &service.UnifiedGatewayDraft{ID: id, Document: document, Revision: next, ConfigID: configID, UpdatedBy: actorID, UpdatedAt: now}, nil
}

func (r *unifiedGatewayAdminRepository) PublishConfig(ctx context.Context, configID, draftID string, expectedRevision, expectedDraftRevision int64, document service.UnifiedGatewayConfig, actorID, reason, digest string) (*service.UnifiedGatewayConfig, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	groupID, err := parseNumeric(document.AccessGroupID, "ag")
	if err != nil || groupID <= 0 {
		return nil, service.ErrUnifiedGatewayAdminInvalidRequest
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var current int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM unified_gateway_model_configs WHERE id=$1 FOR UPDATE`, configID).Scan(&current); errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrUnifiedGatewayAdminNotFound
	} else if err != nil {
		return nil, err
	}
	if current != expectedRevision {
		return nil, service.ErrUnifiedGatewayAdminVersion
	}
	var draftRevision int64
	var draftRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT revision, document FROM unified_gateway_drafts WHERE id=$1 AND config_id=$2 FOR UPDATE`, draftID, configID).Scan(&draftRevision, &draftRaw); errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrUnifiedGatewayAdminNotFound
	} else if err != nil {
		return nil, err
	}
	if draftRevision != expectedDraftRevision {
		return nil, service.ErrUnifiedGatewayAdminVersion
	}
	if err := json.Unmarshal(draftRaw, &document); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	next := current + 1
	if _, err := tx.ExecContext(ctx, `UPDATE unified_gateway_model_configs SET access_group_id=$2, public_model=$3, endpoint=$4, lifecycle='published', enabled=TRUE, revision=$5, document=$6::jsonb, updated_by=$7, updated_at=NOW() WHERE id=$1`, configID, groupID, document.PublicModel, document.Endpoint, next, raw, actorID); err != nil {
		return nil, err
	}
	if err := r.insertRevision(ctx, tx, configID, next, service.UnifiedGatewayLifecyclePublished, raw, actorID, reason, digest); err != nil {
		return nil, err
	}
	if err := r.materialize(ctx, tx, configID, next, document); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	document.ID, document.Revision, document.Lifecycle, document.Readiness = configID, next, service.UnifiedGatewayLifecyclePublished, service.UnifiedGatewayReadinessReady
	return &document, nil
}

func (r *unifiedGatewayAdminRepository) DisableConfig(ctx context.Context, configID string, expectedRevision int64, actorID, reason string) (*service.UnifiedGatewayConfig, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var current, groupID int64
	var raw []byte
	var model, endpoint string
	if err := tx.QueryRowContext(ctx, `SELECT revision, document, access_group_id, public_model, endpoint FROM unified_gateway_model_configs WHERE id=$1 FOR UPDATE`, configID).Scan(&current, &raw, &groupID, &model, &endpoint); errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrUnifiedGatewayAdminNotFound
	} else if err != nil {
		return nil, err
	}
	if current != expectedRevision {
		return nil, service.ErrUnifiedGatewayAdminVersion
	}
	next := current + 1
	if _, err := tx.ExecContext(ctx, `UPDATE unified_gateway_model_configs SET lifecycle='disabled', enabled=FALSE, revision=$2, updated_by=$3, updated_at=NOW() WHERE id=$1`, configID, next, actorID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE unified_route_targets SET enabled=FALSE, updated_at=NOW() WHERE unified_config_id=$1`, configID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE unified_route_account_bindings SET enabled=FALSE, updated_at=NOW() WHERE unified_config_id=$1`, configID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE unified_gateway_billing_lanes SET enabled=FALSE, updated_at=NOW() WHERE config_id=$1`, configID); err != nil {
		return nil, err
	}
	if err := r.insertRevision(ctx, tx, configID, next, service.UnifiedGatewayLifecycleDisabled, raw, actorID, reason, digestForRaw(raw)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	var document service.UnifiedGatewayConfig
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	document.ID, document.AccessGroupID, document.PublicModel, document.Endpoint = configID, opaqueNumeric("ag", groupID), model, endpoint
	document.Lifecycle, document.Revision, document.Readiness = service.UnifiedGatewayLifecycleDisabled, next, service.UnifiedGatewayReadinessBlocked
	return &document, nil
}

func (r *unifiedGatewayAdminRepository) RestoreConfig(ctx context.Context, configID string, sourceRevision, expectedRevision int64, actorID, reason string) (*service.UnifiedGatewayConfig, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var current int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM unified_gateway_model_configs WHERE id=$1 FOR UPDATE`, configID).Scan(&current); errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrUnifiedGatewayAdminNotFound
	} else if err != nil {
		return nil, err
	}
	if current != expectedRevision {
		return nil, service.ErrUnifiedGatewayAdminVersion
	}
	var raw []byte
	var groupID int64
	var model, endpoint string
	if err := tx.QueryRowContext(ctx, `SELECT r.document, c.access_group_id, c.public_model, c.endpoint FROM unified_gateway_config_revisions r JOIN unified_gateway_model_configs c ON c.id=r.config_id WHERE r.config_id=$1 AND r.revision=$2`, configID, sourceRevision).Scan(&raw, &groupID, &model, &endpoint); errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrUnifiedGatewayAdminNotFound
	} else if err != nil {
		return nil, err
	}
	var document service.UnifiedGatewayConfig
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	document.ID, document.AccessGroupID, document.PublicModel, document.Endpoint = configID, opaqueNumeric("ag", groupID), model, endpoint
	next := current + 1
	raw, err = json.Marshal(document)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE unified_gateway_model_configs SET lifecycle='published', enabled=TRUE, revision=$2, document=$3::jsonb, updated_by=$4, updated_at=NOW() WHERE id=$1`, configID, next, raw, actorID); err != nil {
		return nil, err
	}
	if err := r.insertRevision(ctx, tx, configID, next, service.UnifiedGatewayLifecyclePublished, raw, actorID, reason, digestForRaw(raw)); err != nil {
		return nil, err
	}
	if err := r.materialize(ctx, tx, configID, next, document); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	document.Lifecycle, document.Revision, document.Readiness = service.UnifiedGatewayLifecyclePublished, next, service.UnifiedGatewayReadinessReady
	return &document, nil
}

func (r *unifiedGatewayAdminRepository) ListRevisions(ctx context.Context, configID string) ([]service.UnifiedGatewayRevision, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT revision, lifecycle, digest, actor_id, reason, created_at, document FROM unified_gateway_config_revisions WHERE config_id=$1 ORDER BY revision DESC`, configID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]service.UnifiedGatewayRevision, 0)
	for rows.Next() {
		var item service.UnifiedGatewayRevision
		var raw []byte
		if err := rows.Scan(&item.Revision, &item.Lifecycle, &item.Digest, &item.ActorID, &item.Reason, &item.CreatedAt, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &item.Document); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *unifiedGatewayAdminRepository) GetIdempotency(ctx context.Context, actorID, operation, resourceID, key string) (*service.UnifiedGatewayIdempotencyRecord, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var item service.UnifiedGatewayIdempotencyRecord
	err := r.db.QueryRowContext(ctx, `SELECT actor_id, operation, resource_id, idempotency_key, request_digest, response_status, response_json, created_at FROM unified_gateway_admin_idempotency WHERE actor_id=$1 AND operation=$2 AND resource_id=$3 AND idempotency_key=$4`, actorID, operation, resourceID, key).Scan(&item.ActorID, &item.Operation, &item.ResourceID, &item.Key, &item.RequestDigest, &item.StatusCode, &item.ResponseJSON, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *unifiedGatewayAdminRepository) PutIdempotency(ctx context.Context, record *service.UnifiedGatewayIdempotencyRecord) error {
	if err := r.ready(); err != nil {
		return err
	}
	if record == nil {
		return service.ErrUnifiedGatewayAdminInvalidRequest
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO unified_gateway_admin_idempotency (actor_id, operation, resource_id, idempotency_key, request_digest, response_status, response_json, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8) ON CONFLICT (actor_id, operation, resource_id, idempotency_key) DO NOTHING`, record.ActorID, record.Operation, record.ResourceID, record.Key, record.RequestDigest, record.StatusCode, record.ResponseJSON, record.CreatedAt)
	return classifyAdminDBError(err)
}

func (r *unifiedGatewayAdminRepository) insertRevision(ctx context.Context, tx *sql.Tx, configID string, revision int64, lifecycle string, raw []byte, actorID, reason, digest string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO unified_gateway_config_revisions (config_id, revision, lifecycle, document, actor_id, reason, digest) VALUES ($1,$2,$3,$4::jsonb,$5,$6,$7)`, configID, revision, lifecycle, raw, actorID, reason, digest)
	return err
}

// materialize writes only the admin-owned publication projection. It does not
// create snapshots, ledger entries, frozen balances, or recovery work.
func (r *unifiedGatewayAdminRepository) materialize(ctx context.Context, tx *sql.Tx, configID string, revision int64, document service.UnifiedGatewayConfig) error {
	for _, statement := range []string{
		`UPDATE unified_route_targets SET enabled=FALSE, updated_at=NOW() WHERE unified_config_id=$1`,
		`UPDATE unified_route_account_bindings SET enabled=FALSE, updated_at=NOW() WHERE unified_config_id=$1`,
		`UPDATE unified_gateway_billing_lanes SET enabled=FALSE, updated_at=NOW() WHERE config_id=$1`,
	} {
		if _, err := tx.ExecContext(ctx, statement, configID); err != nil {
			return err
		}
	}
	groupID, err := parseNumeric(document.AccessGroupID, "ag")
	if err != nil || groupID <= 0 {
		return service.ErrUnifiedGatewayAdminInvalidRequest
	}
	for _, lane := range document.Lanes {
		profileRaw, err := json.Marshal(lane.Profile)
		if err != nil {
			return err
		}
		ruleRaw, err := materializedUnifiedRateRuleJSON(lane.Profile)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO unified_gateway_billing_lanes (id, config_id, code, name, selection_strategy, pricing_source_group_id, pricing_source_revision, current_profile_id, revision, enabled, updated_at) VALUES ($1,$2,$3,$4,$5,NULLIF($6,0),$7,$8,$9,TRUE,NOW()) ON CONFLICT (config_id, code) DO UPDATE SET name=EXCLUDED.name, selection_strategy=EXCLUDED.selection_strategy, pricing_source_group_id=EXCLUDED.pricing_source_group_id, pricing_source_revision=EXCLUDED.pricing_source_revision, current_profile_id=EXCLUDED.current_profile_id, revision=EXCLUDED.revision, enabled=TRUE, updated_at=NOW()`, lane.ID, configID, lane.Code, lane.Name, lane.SelectionStrategy, numericSourceGroup(lane.PricingSourceGroupID), lane.PricingSourceRevision, lane.Profile.ID, revision); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO unified_gateway_pricing_profiles (id, lane_id, version, digest, currency, rounding_mode, profile, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8) ON CONFLICT (lane_id, digest) DO NOTHING`, lane.Profile.ID, lane.ID, parseVersion(lane.Profile.Version), digestForRaw(profileRaw), lane.Profile.Currency, lane.Profile.RoundingMode, profileRaw, "materializer"); err != nil {
			return err
		}
		for _, target := range lane.Targets {
			var targetID int64
			err = tx.QueryRowContext(ctx, `INSERT INTO unified_route_targets (access_group_id, billing_lane_id, public_model, provider_identity, upstream_model, endpoint, pool_id, billing_mode, rate_mode, rate_basis, lane_rule, pool_rule, priority, enabled, unified_config_id, unified_lane_id, unified_profile_id, unified_revision, currency, rounding_mode, fallback_reason, pricing_schema_id, source_group_id, source_group_revision, updated_at) VALUES ($1,$2,$3,$4,$5,$6,'',$7,$8,$9,$10::jsonb,'{}'::jsonb,$11,TRUE,$12,$13,$14,$15,$16,$17,$18,$19,NULLIF($20,0),$21,NOW()) ON CONFLICT (unified_config_id, unified_lane_id, public_model, endpoint, provider_identity) WHERE unified_config_id IS NOT NULL DO UPDATE SET upstream_model=EXCLUDED.upstream_model, endpoint=EXCLUDED.endpoint, billing_mode=EXCLUDED.billing_mode, rate_mode=EXCLUDED.rate_mode, rate_basis=EXCLUDED.rate_basis, lane_rule=EXCLUDED.lane_rule, pool_rule=EXCLUDED.pool_rule, priority=EXCLUDED.priority, enabled=TRUE, unified_profile_id=EXCLUDED.unified_profile_id, unified_revision=EXCLUDED.unified_revision, currency=EXCLUDED.currency, rounding_mode=EXCLUDED.rounding_mode, fallback_reason=EXCLUDED.fallback_reason, pricing_schema_id=EXCLUDED.pricing_schema_id, source_group_id=EXCLUDED.source_group_id, source_group_revision=EXCLUDED.source_group_revision, updated_at=NOW() RETURNING id`, groupID, lane.ID, document.PublicModel, target.ProviderIdentity, target.UpstreamModel, target.Endpoint, lane.Profile.BillingMode, lane.Profile.RateMode, lane.Profile.RateBasis, ruleRaw, target.Priority, configID, lane.ID, lane.Profile.ID, revision, lane.Profile.Currency, lane.Profile.RoundingMode, fallbackReason(lane.Profile.FallbackReason), lane.Profile.PricingSchemaID, numericSourceGroup(lane.PricingSourceGroupID), lane.PricingSourceRevision).Scan(&targetID)
			if err != nil {
				return err
			}
			for _, binding := range target.Bindings {
				accountID, err := parseNumeric(binding.AccountID, "acct")
				if err != nil || accountID <= 0 {
					return service.ErrUnifiedGatewayAdminInvalidRequest
				}
				probeRaw, err := json.Marshal(binding.Probe)
				if err != nil {
					return err
				}
				bindingEndpoint := strings.TrimSpace(binding.Endpoint)
				if bindingEndpoint == "" {
					bindingEndpoint = target.Endpoint
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO unified_route_account_bindings (route_target_id, account_id, provider_identity, upstream_model, endpoint, account_rule, probe_snapshot, priority, enabled, unified_config_id, unified_revision, probe_status, probe_snapshot_ref) VALUES ($1,$2,$3,$4,$5,'{}'::jsonb,$6::jsonb,$7,$8,$9,$10,$11,$12) ON CONFLICT (route_target_id, account_id) DO UPDATE SET provider_identity=EXCLUDED.provider_identity, upstream_model=EXCLUDED.upstream_model, endpoint=EXCLUDED.endpoint, probe_snapshot=EXCLUDED.probe_snapshot, priority=EXCLUDED.priority, enabled=EXCLUDED.enabled, unified_config_id=EXCLUDED.unified_config_id, unified_revision=EXCLUDED.unified_revision, probe_status=EXCLUDED.probe_status, probe_snapshot_ref=EXCLUDED.probe_snapshot_ref, updated_at=NOW()`, targetID, accountID, target.ProviderIdentity, target.UpstreamModel, bindingEndpoint, probeRaw, binding.Priority, binding.Enabled, configID, revision, probeStatus(binding.Probe), probeRef(binding.Probe)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func parseNumeric(value, prefix string) (int64, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, prefix+"_") {
		value = strings.TrimPrefix(value, prefix+"_")
	}
	return strconv.ParseInt(value, 10, 64)
}

func opaqueNumeric(prefix string, id int64) string { return prefix + "_" + strconv.FormatInt(id, 10) }

func parseVersion(value string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

func numericSourceGroup(value string) int64 {
	if strings.TrimSpace(value) == "" {
		return 0
	}
	n, _ := parseNumeric(value, "ag")
	return n
}

func fallbackReason(value *string) string {
	if value == nil {
		return ""
	}
	runes := []rune(strings.TrimSpace(*value))
	if len(runes) > 128 {
		return string(runes[:128])
	}
	return string(runes)
}

func probeStatus(value *service.UnifiedGatewayAdminProbe) string {
	if value == nil {
		return "missing"
	}
	return value.Status
}

func probeRef(value *service.UnifiedGatewayAdminProbe) string {
	if value == nil {
		return ""
	}
	return value.SnapshotRef
}

func materializedUnifiedRateRuleJSON(profile service.UnifiedGatewayPricingProfile) ([]byte, error) {
	return json.Marshal(map[string]any{
		"profile_id": profile.ID, "version": profile.Version, "billing_mode": profile.BillingMode,
		"upstream_rate_basis": profile.RateBasis, "base_price_semantics": profile.BasePriceSemantics,
		"provider_base_unit_price": profile.ProviderBaseUnitPrice, "manual_base_unit_price": profile.ManualBaseUnitPrice,
		"final_user_unit_price": profile.FinalUserUnitPrice, "manual_upstream_multiplier": profile.ManualUpstreamMultiplier,
		"user_markup_multiplier": profile.UserMarkupMultiplier, "fixed_fee": profile.FixedFee,
		"minimum_charge": profile.MinimumCharge, "rounding_precision": profile.Precision,
		"manual_pricing_rules": profile.ManualPricingRules,
	})
}

func digestForRaw(value []byte) string {
	sum := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", sum)
}

func classifyAdminDBError(err error) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "unique") || strings.Contains(lower, "duplicate") {
		return service.ErrUnifiedGatewayAdminVersion
	}
	return err
}
