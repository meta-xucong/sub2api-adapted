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

// unifiedGatewayAdminRepository owns only the aggregate administration tables
// and the explicitly namespaced rows in the isolated unified route tables.
// It never queries legacy groups/channel pricing as a hidden fallback.
type unifiedGatewayAdminRepository struct{ db *sql.DB }

func NewUnifiedGatewayAdminRepository(db *sql.DB) service.UnifiedGatewayAdminRepository {
	return &unifiedGatewayAdminRepository{db: db}
}

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
	var tables, lanes, profiles, drafts, revisions, idem, schemaTable bool
	err := r.db.QueryRowContext(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'unified_gateway_model_configs'),
			EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'unified_gateway_billing_lanes'),
			EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'unified_gateway_pricing_profiles'),
			EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'unified_gateway_drafts'),
			EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'unified_gateway_config_revisions'),
			EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'unified_gateway_admin_idempotency'),
			EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'unified_gateway_schema_version')
	`).Scan(&tables, &lanes, &profiles, &drafts, &revisions, &idem, &schemaTable)
	if err != nil {
		return service.UnifiedGatewaySchemaReadiness{}, err
	}
	missing := make([]string, 0)
	if schemaTable {
		var schemaVersion bool
		if err := r.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM unified_gateway_schema_version WHERE version = $1)`, service.UnifiedGatewayAdminSchemaVersion).Scan(&schemaVersion); err != nil {
			return service.UnifiedGatewaySchemaReadiness{}, err
		}
		if !schemaVersion {
			missing = append(missing, "schema_version_value")
		}
	} else {
		missing = append(missing, "schema_version", "schema_version_value")
	}
	for name, present := range map[string]bool{"model_configs": tables, "billing_lanes": lanes, "pricing_profiles": profiles, "drafts": drafts, "config_revisions": revisions, "idempotency": idem} {
		if !present {
			missing = append(missing, name)
		}
	}
	requiredColumns := map[string][]string{
		"unified_gateway_model_configs":     {"id", "access_group_id", "public_model", "endpoint", "lifecycle", "enabled", "revision", "document", "created_by", "updated_by"},
		"unified_gateway_billing_lanes":     {"id", "config_id", "code", "name", "selection_strategy", "pricing_source_group_id", "pricing_source_revision", "current_profile_id", "revision", "enabled"},
		"unified_gateway_pricing_profiles":  {"id", "lane_id", "version", "digest", "currency", "rounding_mode", "profile", "created_by"},
		"unified_gateway_drafts":            {"id", "config_id", "revision", "document", "created_by", "updated_by"},
		"unified_gateway_config_revisions":  {"id", "config_id", "revision", "lifecycle", "document", "actor_id", "reason", "digest"},
		"unified_gateway_admin_idempotency": {"actor_id", "operation", "resource_id", "idempotency_key", "request_digest", "response_status", "response_json"},
		"unified_route_targets":             {"unified_config_id", "unified_lane_id", "unified_profile_id", "unified_revision", "currency", "rounding_mode", "fallback_reason", "pricing_schema_id", "source_group_id", "source_group_revision"},
		"unified_route_account_bindings":    {"unified_config_id", "unified_revision", "probe_status", "probe_snapshot_ref"},
	}
	for table, columns := range requiredColumns {
		placeholders := make([]string, 0, len(columns))
		args := make([]any, 0, len(columns)+1)
		args = append(args, table)
		for i, column := range columns {
			placeholders = append(placeholders, "$"+strconv.Itoa(i+2))
			args = append(args, column)
		}
		query := `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name IN (` + strings.Join(placeholders, ",") + `)`
		var count int
		if err := r.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
			return service.UnifiedGatewaySchemaReadiness{}, err
		}
		if count != len(columns) {
			missing = append(missing, table+"_columns")
		}
	}
	type indexExpectation struct {
		columns   string
		predicate string
	}
	requiredIndexes := map[string]indexExpectation{
		"idx_unified_gateway_model_configs_public":                {columns: "access_group_id,public_model,endpoint", predicate: "lifecycle<>'archived'"},
		"unified_gateway_billing_lanes_config_id_code_key":        {columns: "config_id,code"},
		"unified_gateway_pricing_profiles_lane_id_version_key":    {columns: "lane_id,version"},
		"idx_unified_gateway_drafts_config":                       {columns: "config_id"},
		"unified_gateway_config_revisions_config_id_revision_key": {columns: "config_id,revision"},
		"unified_gateway_admin_idempotency_pkey":                  {columns: "actor_id,operation,resource_id,idempotency_key"},
		"idx_unified_route_targets_legacy_unique":                 {columns: "access_group_id,billing_lane_id,public_model,endpoint,provider_identity", predicate: "unified_config_idisnull"},
		"idx_unified_route_targets_admin_unique":                  {columns: "unified_config_id,unified_lane_id,public_model,endpoint,provider_identity", predicate: "unified_config_idisnotnull"},
	}
	for indexName, expectation := range requiredIndexes {
		var definition, predicate string
		var unique bool
		if err := r.db.QueryRowContext(ctx, `SELECT i.indisunique, pg_get_indexdef(i.indexrelid), COALESCE(pg_get_expr(i.indpred, i.indrelid), '') FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname=$1`, indexName).Scan(&unique, &definition, &predicate); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				missing = append(missing, indexName)
				continue
			}
			return service.UnifiedGatewaySchemaReadiness{}, err
		}
		if !unique || !strings.Contains(normalizeUnifiedGatewaySchemaSQL(definition), "("+expectation.columns+")") {
			missing = append(missing, indexName+"_definition")
		}
		if expectation.predicate != "" && normalizeUnifiedGatewaySchemaPredicate(predicate) != expectation.predicate {
			missing = append(missing, indexName+"_predicate")
		}
	}
	var legacyConstraintCount int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_constraint WHERE conrelid='unified_route_targets'::regclass AND contype='u' AND pg_get_constraintdef(oid) = 'UNIQUE (access_group_id, billing_lane_id, public_model, endpoint, provider_identity)'`).Scan(&legacyConstraintCount); err != nil {
		return service.UnifiedGatewaySchemaReadiness{}, err
	}
	if legacyConstraintCount != 0 {
		missing = append(missing, "legacy_route_targets_unique_constraint_removed")
	}
	var restrictFKCount int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_constraint WHERE conrelid='unified_route_account_bindings'::regclass AND conname='unified_route_account_bindings_route_target_id_fkey' AND contype='f' AND pg_get_constraintdef(oid) LIKE 'FOREIGN KEY (route_target_id) REFERENCES unified_route_targets(id) ON DELETE RESTRICT%'`).Scan(&restrictFKCount); err != nil {
		return service.UnifiedGatewaySchemaReadiness{}, err
	}
	if restrictFKCount != 1 {
		missing = append(missing, "unified_route_account_bindings_route_target_id_fkey_definition")
	}
	return service.UnifiedGatewaySchemaReadiness{Ready: len(missing) == 0, Version: service.UnifiedGatewayAdminSchemaVersion, Missing: missing}, nil
}

func normalizeUnifiedGatewaySchemaSQL(value string) string {
	value = strings.ToLower(strings.ReplaceAll(value, `"`, ""))
	value = strings.ReplaceAll(value, " ", "")
	value = strings.ReplaceAll(value, "\t", "")
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", "")
	return value
}

func normalizeUnifiedGatewaySchemaPredicate(value string) string {
	value = normalizeUnifiedGatewaySchemaSQL(value)
	for _, cast := range []string{"::text", "::character varying", "::varchar", "::character", "::bpchar"} {
		value = strings.ReplaceAll(value, cast, "")
	}
	value = strings.ReplaceAll(value, "(", "")
	value = strings.ReplaceAll(value, ")", "")
	return value
}

func (r *unifiedGatewayAdminRepository) ListConfigs(ctx context.Context, filter service.UnifiedGatewayConfigListFilter) ([]service.UnifiedGatewayConfig, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}
	whereParts := make([]string, 0, 2)
	args := []any{}
	if strings.TrimSpace(filter.Lifecycle) != "" {
		whereParts = append(whereParts, "lifecycle = $1")
		args = append(args, strings.TrimSpace(filter.Lifecycle))
	}
	if filter.AccessGroupIDs != nil {
		if len(filter.AccessGroupIDs) == 0 {
			whereParts = append(whereParts, "FALSE")
		} else {
			args = append(args, pq.Array(filter.AccessGroupIDs))
			whereParts = append(whereParts, "access_group_id = ANY($"+strconv.Itoa(len(args)+0)+")")
		}
	}
	where := ""
	if len(whereParts) > 0 {
		where = " WHERE " + strings.Join(whereParts, " AND ")
	}
	var total int64
	countQuery := "SELECT COUNT(*) FROM unified_gateway_model_configs" + where
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
	query := `SELECT id, access_group_id, public_model, endpoint, lifecycle, revision, document FROM unified_gateway_model_configs` + where + ` ORDER BY updated_at DESC, id ASC LIMIT $` + strconv.Itoa(len(args)-1) + ` OFFSET $` + strconv.Itoa(len(args))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]service.UnifiedGatewayConfig, 0)
	for rows.Next() {
		var item service.UnifiedGatewayConfig
		var groupID int64
		var raw []byte
		var revision int64
		var id, lifecycle, publicModel, endpoint string
		if err := rows.Scan(&id, &groupID, &publicModel, &endpoint, &lifecycle, &revision, &raw); err != nil {
			return nil, 0, err
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &item)
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
	var groupID int64
	var raw []byte
	var revision int64
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
		FROM unified_gateway_drafts d JOIN unified_gateway_model_configs c ON c.id = d.config_id WHERE d.id = $1
	`, strings.TrimSpace(id)).Scan(&draft.ID, &draft.ConfigID, &draft.Revision, &raw, &draft.CreatedBy, &draft.UpdatedBy, &created, &updated, &groupID)
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
	document, err := json.Marshal(draft.Document)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO unified_gateway_model_configs (id, access_group_id, public_model, endpoint, lifecycle, enabled, revision, document, created_by, updated_by) VALUES ($1,$2,$3,$4,'draft',FALSE,0,$5::jsonb,$6,$6)`, draft.ConfigID, groupID, draft.Document.PublicModel, draft.Document.Endpoint, document, draft.CreatedBy)
	if err != nil {
		return nil, classifyAdminDBError(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO unified_gateway_drafts (id, config_id, revision, document, created_by, updated_by, created_at, updated_at) VALUES ($1,$2,0,$3::jsonb,$4,$4,$5,$5)`, draft.ID, draft.ConfigID, document, draft.CreatedBy, draft.CreatedAt)
	if err != nil {
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
	next := expectedRevision + 1
	now := time.Now().UTC()
	var configID string
	err = tx.QueryRowContext(ctx, `UPDATE unified_gateway_drafts SET revision=$2, document=$3::jsonb, updated_by=$4, updated_at=$5 WHERE id=$1 AND revision=$6 RETURNING config_id`, id, next, raw, actorID, now, expectedRevision).Scan(&configID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrUnifiedGatewayAdminVersion
		}
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	document.ID = configID
	document.Revision = next
	return &service.UnifiedGatewayDraft{ID: id, Document: document, Revision: next, ConfigID: configID, UpdatedBy: actorID, UpdatedAt: now}, nil
}

func (r *unifiedGatewayAdminRepository) PublishConfig(ctx context.Context, configID, draftID string, expectedRevision, expectedDraftRevision int64, document service.UnifiedGatewayConfig, actorID, reason, digest string) (*service.UnifiedGatewayConfig, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	groupID, err := parseNumeric(document.AccessGroupID, "ag")
	if err != nil {
		return nil, service.ErrUnifiedGatewayAdminInvalidRequest
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
	var current int64
	var lifecycle string
	if err := tx.QueryRowContext(ctx, `SELECT revision, lifecycle FROM unified_gateway_model_configs WHERE id=$1 FOR UPDATE`, configID).Scan(&current, &lifecycle); errors.Is(err, sql.ErrNoRows) {
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
	var storedDocument service.UnifiedGatewayConfig
	if err := json.Unmarshal(draftRaw, &storedDocument); err != nil {
		return nil, err
	}
	// The locked draft is authoritative. The service validated the same
	// revision before entering this transaction; using the stored document
	// avoids publishing caller-side hydration changes or stale body fields.
	document = storedDocument
	raw, err = json.Marshal(document)
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
	var current int64
	var raw []byte
	var groupID int64
	var model, endpoint string
	err = tx.QueryRowContext(ctx, `SELECT revision, document, access_group_id, public_model, endpoint FROM unified_gateway_model_configs WHERE id=$1 FOR UPDATE`, configID).Scan(&current, &raw, &groupID, &model, &endpoint)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrUnifiedGatewayAdminNotFound
	}
	if err != nil {
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
	_ = json.Unmarshal(raw, &document)
	document.ID, document.AccessGroupID, document.PublicModel, document.Endpoint, document.Lifecycle, document.Revision, document.Readiness = configID, opaqueNumeric("ag", groupID), model, endpoint, service.UnifiedGatewayLifecycleDisabled, next, service.UnifiedGatewayReadinessBlocked
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
	if err := tx.QueryRowContext(ctx, `SELECT r.document, c.access_group_id, c.public_model, c.endpoint FROM unified_gateway_config_revisions r JOIN unified_gateway_model_configs c ON c.id=r.config_id WHERE r.config_id=$1 AND r.revision=$2`, configID, sourceRevision).Scan(&raw, &groupID, &model, &endpoint); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrUnifiedGatewayAdminNotFound
		}
		return nil, err
	}
	var document service.UnifiedGatewayConfig
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	document.ID, document.AccessGroupID, document.PublicModel, document.Endpoint = configID, opaqueNumeric("ag", groupID), model, endpoint
	next := current + 1
	raw, _ = json.Marshal(document)
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

func (r *unifiedGatewayAdminRepository) ListSnapshots(ctx context.Context, filter service.UnifiedGatewaySnapshotFilter) ([]service.UnifiedGatewaySnapshotView, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}
	where := []string{"1=1"}
	args := []any{}
	add := func(value any) { args = append(args, value); where = append(where, "$"+strconv.Itoa(len(args))) }
	if filter.AccessGroupIDs != nil {
		if len(filter.AccessGroupIDs) == 0 {
			where = append(where, "FALSE")
		} else {
			args = append(args, pq.Array(filter.AccessGroupIDs))
			where = append(where, "access_group_id = ANY($"+strconv.Itoa(len(args))+")")
		}
	}
	if filter.AccessGroupID != "" {
		id, err := parseNumeric(filter.AccessGroupID, "ag")
		if err != nil {
			return nil, 0, service.ErrUnifiedGatewayAdminInvalidRequest
		}
		add(id)
		where[len(where)-1] = "access_group_id = $" + strconv.Itoa(len(args))
	}
	if filter.PublicModel != "" {
		add(filter.PublicModel)
		where[len(where)-1] = "public_model = $" + strconv.Itoa(len(args))
	}
	if filter.Status != "" {
		add(filter.Status)
		where[len(where)-1] = "status = $" + strconv.Itoa(len(args))
	}
	if !filter.From.IsZero() {
		add(filter.From)
		where[len(where)-1] = "created_at >= $" + strconv.Itoa(len(args))
	}
	if !filter.To.IsZero() {
		add(filter.To)
		where[len(where)-1] = "created_at < $" + strconv.Itoa(len(args))
	}
	clause := strings.Join(where, " AND ")
	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM unified_route_price_snapshots WHERE "+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
	query := `SELECT id, request_id, attempt_id, access_group_id, public_model, billing_lane_id, account_id, provider_identity, status, snapshot_json, measured_units, user_charge, created_at, finalized_at FROM unified_route_price_snapshots WHERE ` + clause + ` ORDER BY created_at DESC, id DESC LIMIT $` + strconv.Itoa(len(args)-1) + ` OFFSET $` + strconv.Itoa(len(args))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]service.UnifiedGatewaySnapshotView, 0)
	for rows.Next() {
		var id, requestID, attemptID, publicModel, laneID, provider, status, measured, charge string
		var groupID, accountID int64
		var raw []byte
		var created time.Time
		var finalized sql.NullTime
		if err := rows.Scan(&id, &requestID, &attemptID, &groupID, &publicModel, &laneID, &accountID, &provider, &status, &raw, &measured, &charge, &created, &finalized); err != nil {
			return nil, 0, err
		}
		var snap service.UnifiedRoutePriceSnapshot
		_ = json.Unmarshal(raw, &snap)
		item := service.UnifiedGatewaySnapshotView{ID: opaqueNumeric("snapshot", parseInt64(id)), RequestID: requestID, AttemptID: attemptID, AccessGroupID: opaqueNumeric("ag", groupID), PublicModel: publicModel, BillingLaneID: laneID, AccountID: opaqueNumeric("acct", accountID), ProviderIdentity: provider, Status: status, RateSource: string(snap.RateSource), PolicyVersion: snap.PolicyVersion, MeasuredUnits: measured, UserCharge: charge, Currency: "USD", CreatedAt: created}
		if finalized.Valid {
			item.FinalizedAt = &finalized.Time
		}
		out = append(out, item)
	}
	return out, total, rows.Err()
}

func (r *unifiedGatewayAdminRepository) BindingExists(ctx context.Context, bindingID string) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	var exists bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM unified_gateway_model_configs c,
				 jsonb_array_elements(c.document->'lanes') lane,
				 jsonb_array_elements(lane->'targets') target,
				 jsonb_array_elements(target->'bindings') binding
			WHERE binding->>'id' = $1
		) OR EXISTS (
			SELECT 1
			FROM unified_gateway_drafts d,
				 jsonb_array_elements(d.document->'lanes') lane,
				 jsonb_array_elements(lane->'targets') target,
				 jsonb_array_elements(target->'bindings') binding
			WHERE binding->>'id' = $1
		)`, strings.TrimSpace(bindingID)).Scan(&exists)
	return exists, err
}

func (r *unifiedGatewayAdminRepository) BindingExistsInGroups(ctx context.Context, bindingID string, accessGroupIDs []int64) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	if len(accessGroupIDs) == 0 {
		return false, nil
	}
	var exists bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM unified_gateway_model_configs c,
				 jsonb_array_elements(c.document->'lanes') lane,
				 jsonb_array_elements(lane->'targets') target,
				 jsonb_array_elements(target->'bindings') binding
			WHERE c.access_group_id = ANY($2)
			  AND binding->>'id' = $1
		) OR EXISTS (
			SELECT 1
			FROM unified_gateway_drafts d
			JOIN unified_gateway_model_configs c ON c.id = d.config_id,
				 jsonb_array_elements(d.document->'lanes') lane,
				 jsonb_array_elements(lane->'targets') target,
				 jsonb_array_elements(target->'bindings') binding
			WHERE c.access_group_id = ANY($2)
			  AND binding->>'id' = $1
		)`, strings.TrimSpace(bindingID), pq.Array(accessGroupIDs)).Scan(&exists)
	return exists, err
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

func (r *unifiedGatewayAdminRepository) materialize(ctx context.Context, tx *sql.Tx, configID string, revision int64, document service.UnifiedGatewayConfig) error {
	if _, err := tx.ExecContext(ctx, `UPDATE unified_route_targets SET enabled=FALSE, updated_at=NOW() WHERE unified_config_id=$1`, configID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE unified_route_account_bindings SET enabled=FALSE, updated_at=NOW() WHERE unified_config_id=$1`, configID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE unified_gateway_billing_lanes SET enabled=FALSE, updated_at=NOW() WHERE config_id=$1`, configID); err != nil {
		return err
	}
	for _, lane := range document.Lanes {
		groupID, err := parseNumeric(document.AccessGroupID, "ag")
		if err != nil {
			return err
		}
		profileRaw, err := json.Marshal(lane.Profile)
		if err != nil {
			return err
		}
		profileDigest := digestForRaw(profileRaw)
		profileVersion := parseVersion(lane.Profile.Version)
		if _, err := tx.ExecContext(ctx, `INSERT INTO unified_gateway_billing_lanes (id, config_id, code, name, selection_strategy, pricing_source_group_id, pricing_source_revision, current_profile_id, revision, enabled, updated_at) VALUES ($1,$2,$3,$4,$5,NULLIF($6,0),$7,$8,$9,TRUE,NOW()) ON CONFLICT (config_id, code) DO UPDATE SET name=EXCLUDED.name, selection_strategy=EXCLUDED.selection_strategy, pricing_source_group_id=EXCLUDED.pricing_source_group_id, pricing_source_revision=EXCLUDED.pricing_source_revision, current_profile_id=EXCLUDED.current_profile_id, revision=EXCLUDED.revision, enabled=TRUE, updated_at=NOW()`, lane.ID, configID, lane.Code, lane.Name, lane.SelectionStrategy, numericSourceGroup(lane.PricingSourceGroupID), lane.PricingSourceRevision, lane.Profile.ID, revision); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO unified_gateway_pricing_profiles (id, lane_id, version, digest, currency, rounding_mode, profile, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8) ON CONFLICT (lane_id, digest) DO NOTHING`, lane.Profile.ID, lane.ID, profileVersion, profileDigest, lane.Profile.Currency, lane.Profile.RoundingMode, profileRaw, "materializer"); err != nil {
			return err
		}
		for _, target := range lane.Targets {
			var targetID int64
			err = tx.QueryRowContext(ctx, `INSERT INTO unified_route_targets (access_group_id, billing_lane_id, public_model, provider_identity, upstream_model, endpoint, pool_id, billing_mode, rate_mode, rate_basis, lane_rule, pool_rule, priority, enabled, unified_config_id, unified_lane_id, unified_profile_id, unified_revision, currency, rounding_mode, fallback_reason, pricing_schema_id, source_group_id, source_group_revision, updated_at) VALUES ($1,$2,$3,$4,$5,$6,'',$7,$8,$9,$10::jsonb,'{}'::jsonb,$11,TRUE,$12,$13,$14,$15,$16,$17,$18,$19,NULLIF($20,0),$21,NOW()) ON CONFLICT (unified_config_id, unified_lane_id, public_model, endpoint, provider_identity) WHERE unified_config_id IS NOT NULL DO UPDATE SET upstream_model=EXCLUDED.upstream_model, endpoint=EXCLUDED.endpoint, billing_mode=EXCLUDED.billing_mode, rate_mode=EXCLUDED.rate_mode, rate_basis=EXCLUDED.rate_basis, lane_rule=EXCLUDED.lane_rule, pool_rule=EXCLUDED.pool_rule, priority=EXCLUDED.priority, enabled=TRUE, unified_profile_id=EXCLUDED.unified_profile_id, unified_revision=EXCLUDED.unified_revision, currency=EXCLUDED.currency, rounding_mode=EXCLUDED.rounding_mode, fallback_reason=EXCLUDED.fallback_reason, pricing_schema_id=EXCLUDED.pricing_schema_id, source_group_id=EXCLUDED.source_group_id, source_group_revision=EXCLUDED.source_group_revision, updated_at=NOW() RETURNING id`, groupID, lane.ID, document.PublicModel, target.ProviderIdentity, target.UpstreamModel, target.Endpoint, lane.Profile.BillingMode, lane.Profile.RateMode, lane.Profile.RateBasis, profileRaw, target.Priority, configID, lane.ID, lane.Profile.ID, revision, lane.Profile.Currency, lane.Profile.RoundingMode, fallbackReason(lane.Profile.FallbackReason), lane.Profile.PricingSchemaID, numericSourceGroup(lane.PricingSourceGroupID), lane.PricingSourceRevision).Scan(&targetID)
			if err != nil {
				return err
			}
			for _, binding := range target.Bindings {
				accountID, err := parseNumeric(binding.AccountID, "acct")
				if err != nil {
					return err
				}
				probeRaw, _ := json.Marshal(binding.Probe)
				if _, err := tx.ExecContext(ctx, `INSERT INTO unified_route_account_bindings (route_target_id, account_id, provider_identity, upstream_model, endpoint, account_rule, probe_snapshot, priority, enabled, unified_config_id, unified_revision, probe_status, probe_snapshot_ref) VALUES ($1,$2,$3,$4,$5,'{}'::jsonb,$6::jsonb,$7,$8,$9,$10,$11,$12) ON CONFLICT (route_target_id, account_id) DO UPDATE SET provider_identity=EXCLUDED.provider_identity, upstream_model=EXCLUDED.upstream_model, endpoint=EXCLUDED.endpoint, probe_snapshot=EXCLUDED.probe_snapshot, priority=EXCLUDED.priority, enabled=EXCLUDED.enabled, unified_config_id=EXCLUDED.unified_config_id, unified_revision=EXCLUDED.unified_revision, probe_status=EXCLUDED.probe_status, probe_snapshot_ref=EXCLUDED.probe_snapshot_ref, updated_at=NOW()`, targetID, accountID, target.ProviderIdentity, target.UpstreamModel, target.Endpoint, probeRaw, binding.Priority, binding.Enabled, configID, revision, probeStatus(binding.Probe), probeRef(binding.Probe)); err != nil {
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
func parseInt64(value string) int64                { n, _ := strconv.ParseInt(value, 10, 64); return n }
func parseVersion(value string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || n < 1 {
		return 1
	}
	return n
}
func numericSourceGroup(value string) int64 {
	if value == "" {
		return 0
	}
	n, _ := parseNumeric(value, "ag")
	return n
}
func fallbackReason(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
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
func digestForRaw(value []byte) string { sum := sha256Sum(value); return fmt.Sprintf("sha256:%x", sum) }
func sha256Sum(value []byte) [32]byte  { return sha256.Sum256(value) }
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
