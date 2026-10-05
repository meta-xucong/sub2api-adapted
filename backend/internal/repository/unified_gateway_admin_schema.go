package repository

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
)

const unifiedGatewayAdminSchemaVersion = "unified_gateway_admin_v1"

type unifiedGatewayAdminSchemaQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type unifiedGatewayAdminSchemaReadiness struct {
	Ready   bool
	Version string
	Missing []string
}

// checkUnifiedGatewayAdminSchema verifies only the Phase 5 management schema.
// It intentionally has no dependency on runtime snapshots, billing settlement,
// frozen-balance, or recovery schema.
func checkUnifiedGatewayAdminSchema(ctx context.Context, db unifiedGatewayAdminSchemaQueryer) (unifiedGatewayAdminSchemaReadiness, error) {
	if db == nil {
		return unifiedGatewayAdminSchemaReadiness{}, errors.New("unified gateway admin repository db is nil")
	}

	var modelConfigs, billingLanes, pricingProfiles, drafts, revisions, idempotency, schemaVersion, routeTargets, routeBindings bool
	err := db.QueryRowContext(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'unified_gateway_model_configs'),
			EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'unified_gateway_billing_lanes'),
			EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'unified_gateway_pricing_profiles'),
			EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'unified_gateway_drafts'),
			EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'unified_gateway_config_revisions'),
			EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'unified_gateway_admin_idempotency'),
			EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'unified_gateway_schema_version'),
			EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'unified_route_targets'),
			EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'unified_route_account_bindings')
	`).Scan(&modelConfigs, &billingLanes, &pricingProfiles, &drafts, &revisions, &idempotency, &schemaVersion, &routeTargets, &routeBindings)
	if err != nil {
		return unifiedGatewayAdminSchemaReadiness{}, err
	}

	missing := make([]string, 0)
	if schemaVersion {
		var adminVersion bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM unified_gateway_schema_version WHERE version = $1)`, unifiedGatewayAdminSchemaVersion).Scan(&adminVersion); err != nil {
			return unifiedGatewayAdminSchemaReadiness{}, err
		}
		if !adminVersion {
			missing = append(missing, "schema_version_value")
		}
	} else {
		missing = append(missing, "schema_version", "schema_version_value")
	}

	for _, table := range []struct {
		name    string
		present bool
	}{
		{"model_configs", modelConfigs},
		{"billing_lanes", billingLanes},
		{"pricing_profiles", pricingProfiles},
		{"drafts", drafts},
		{"config_revisions", revisions},
		{"idempotency", idempotency},
		{"unified_route_targets", routeTargets},
		{"unified_route_account_bindings", routeBindings},
	} {
		if !table.present {
			missing = append(missing, table.name)
		}
	}

	requiredColumns := []struct {
		table   string
		columns []string
	}{
		{"unified_gateway_model_configs", []string{"id", "access_group_id", "public_model", "endpoint", "lifecycle", "enabled", "revision", "document", "created_by", "updated_by"}},
		{"unified_gateway_billing_lanes", []string{"id", "config_id", "code", "name", "selection_strategy", "pricing_source_group_id", "pricing_source_revision", "current_profile_id", "revision", "enabled"}},
		{"unified_gateway_pricing_profiles", []string{"id", "lane_id", "version", "digest", "currency", "rounding_mode", "profile", "created_by"}},
		{"unified_gateway_drafts", []string{"id", "config_id", "revision", "document", "created_by", "updated_by"}},
		{"unified_gateway_config_revisions", []string{"id", "config_id", "revision", "lifecycle", "document", "actor_id", "reason", "digest"}},
		{"unified_gateway_admin_idempotency", []string{"actor_id", "operation", "resource_id", "idempotency_key", "request_digest", "response_status", "response_json"}},
		{"unified_route_targets", []string{"unified_config_id", "unified_lane_id", "unified_profile_id", "unified_revision", "currency", "rounding_mode", "fallback_reason", "pricing_schema_id", "source_group_id", "source_group_revision"}},
		{"unified_route_account_bindings", []string{"unified_config_id", "unified_revision", "probe_snapshot", "probe_status", "probe_snapshot_ref"}},
	}
	for _, table := range requiredColumns {
		placeholders := make([]string, 0, len(table.columns))
		args := make([]any, 0, len(table.columns)+1)
		args = append(args, table.table)
		for i, column := range table.columns {
			placeholders = append(placeholders, "$"+strconv.Itoa(i+2))
			args = append(args, column)
		}
		query := `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name IN (` + strings.Join(placeholders, ",") + `)`
		var count int
		if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
			return unifiedGatewayAdminSchemaReadiness{}, err
		}
		if count != len(table.columns) {
			missing = append(missing, table.table+"_columns")
		}
	}

	type indexExpectation struct {
		columns   string
		predicate string
	}
	requiredIndexes := []struct {
		name string
		want indexExpectation
	}{
		{"idx_unified_gateway_model_configs_public", indexExpectation{columns: "access_group_id,public_model,endpoint", predicate: "lifecycle<>'archived'"}},
		{"unified_gateway_billing_lanes_config_id_code_key", indexExpectation{columns: "config_id,code"}},
		{"unified_gateway_pricing_profiles_lane_id_version_key", indexExpectation{columns: "lane_id,version"}},
		{"idx_unified_gateway_drafts_config", indexExpectation{columns: "config_id"}},
		{"unified_gateway_config_revisions_config_id_revision_key", indexExpectation{columns: "config_id,revision"}},
		{"unified_gateway_admin_idempotency_pkey", indexExpectation{columns: "actor_id,operation,resource_id,idempotency_key"}},
		{"idx_unified_route_targets_legacy_unique", indexExpectation{columns: "access_group_id,billing_lane_id,public_model,endpoint,provider_identity", predicate: "unified_config_idisnull"}},
		{"idx_unified_route_targets_admin_unique", indexExpectation{columns: "unified_config_id,unified_lane_id,public_model,endpoint,provider_identity", predicate: "unified_config_idisnotnull"}},
	}
	for _, index := range requiredIndexes {
		var definition, predicate string
		var unique bool
		if err := db.QueryRowContext(ctx, `SELECT i.indisunique, pg_get_indexdef(i.indexrelid), COALESCE(pg_get_expr(i.indpred, i.indrelid), '') FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname=$1`, index.name).Scan(&unique, &definition, &predicate); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				missing = append(missing, index.name)
				continue
			}
			return unifiedGatewayAdminSchemaReadiness{}, err
		}
		if !unique || !strings.Contains(normalizeUnifiedGatewaySchemaSQL(definition), "("+index.want.columns+")") {
			missing = append(missing, index.name+"_definition")
		}
		if index.want.predicate != "" && normalizeUnifiedGatewaySchemaPredicate(predicate) != index.want.predicate {
			missing = append(missing, index.name+"_predicate")
		}
	}

	if routeTargets {
		var legacyConstraintCount int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_constraint WHERE conrelid='unified_route_targets'::regclass AND contype='u' AND pg_get_constraintdef(oid) = 'UNIQUE (access_group_id, billing_lane_id, public_model, endpoint, provider_identity)'`).Scan(&legacyConstraintCount); err != nil {
			return unifiedGatewayAdminSchemaReadiness{}, err
		}
		if legacyConstraintCount != 0 {
			missing = append(missing, "legacy_route_targets_unique_constraint_removed")
		}
	}
	if routeBindings {
		var restrictFKCount int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_constraint WHERE conrelid='unified_route_account_bindings'::regclass AND conname='unified_route_account_bindings_route_target_id_fkey' AND contype='f' AND pg_get_constraintdef(oid) LIKE 'FOREIGN KEY (route_target_id) REFERENCES unified_route_targets(id) ON DELETE RESTRICT%'`).Scan(&restrictFKCount); err != nil {
			return unifiedGatewayAdminSchemaReadiness{}, err
		}
		if restrictFKCount != 1 {
			missing = append(missing, "unified_route_account_bindings_route_target_id_fkey_definition")
		}
	}

	return unifiedGatewayAdminSchemaReadiness{Ready: len(missing) == 0, Version: unifiedGatewayAdminSchemaVersion, Missing: missing}, nil
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
