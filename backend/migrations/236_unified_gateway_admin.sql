-- Unified gateway admin configuration domain.
--
-- This migration is intentionally additive and isolated.  Existing groups,
-- channels and legacy billing rows are not rewritten.  The runtime gate is
-- false by default, so these rows are configuration-only until an explicit
-- production rollout approves the runtime materializer.

CREATE TABLE IF NOT EXISTS unified_gateway_schema_version (
    version VARCHAR(128) PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS unified_gateway_model_configs (
    id VARCHAR(128) PRIMARY KEY,
    access_group_id BIGINT NOT NULL,
    public_model VARCHAR(255) NOT NULL,
    endpoint VARCHAR(64) NOT NULL,
    lifecycle VARCHAR(32) NOT NULL CHECK (lifecycle IN ('draft', 'published', 'disabled', 'archived')),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    revision BIGINT NOT NULL DEFAULT 0,
    document JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by VARCHAR(128) NOT NULL DEFAULT '',
    updated_by VARCHAR(128) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_unified_gateway_model_configs_public
    ON unified_gateway_model_configs (access_group_id, public_model, endpoint)
    WHERE lifecycle <> 'archived';

CREATE TABLE IF NOT EXISTS unified_gateway_billing_lanes (
    id VARCHAR(128) PRIMARY KEY,
    config_id VARCHAR(128) NOT NULL REFERENCES unified_gateway_model_configs(id) ON DELETE RESTRICT,
    code VARCHAR(128) NOT NULL,
    name VARCHAR(255) NOT NULL,
    selection_strategy VARCHAR(64) NOT NULL DEFAULT 'fixed_priority',
    pricing_source_group_id BIGINT,
    pricing_source_revision VARCHAR(255) NOT NULL DEFAULT '',
    current_profile_id VARCHAR(128) NOT NULL DEFAULT '',
    revision BIGINT NOT NULL DEFAULT 1,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (config_id, code)
);

CREATE TABLE IF NOT EXISTS unified_gateway_pricing_profiles (
    id VARCHAR(128) PRIMARY KEY,
    lane_id VARCHAR(128) NOT NULL REFERENCES unified_gateway_billing_lanes(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL,
    digest VARCHAR(128) NOT NULL,
    currency VARCHAR(8) NOT NULL DEFAULT 'USD',
    rounding_mode VARCHAR(32) NOT NULL DEFAULT 'half_up',
    profile JSONB NOT NULL,
    created_by VARCHAR(128) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (lane_id, version),
    UNIQUE (lane_id, digest)
);

CREATE TABLE IF NOT EXISTS unified_gateway_drafts (
    id VARCHAR(128) PRIMARY KEY,
    config_id VARCHAR(128) NOT NULL REFERENCES unified_gateway_model_configs(id) ON DELETE RESTRICT,
    revision BIGINT NOT NULL DEFAULT 0,
    document JSONB NOT NULL,
    created_by VARCHAR(128) NOT NULL DEFAULT '',
    updated_by VARCHAR(128) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_unified_gateway_drafts_config
    ON unified_gateway_drafts (config_id);

CREATE TABLE IF NOT EXISTS unified_gateway_config_revisions (
    id BIGSERIAL PRIMARY KEY,
    config_id VARCHAR(128) NOT NULL REFERENCES unified_gateway_model_configs(id) ON DELETE RESTRICT,
    revision BIGINT NOT NULL,
    lifecycle VARCHAR(32) NOT NULL,
    document JSONB NOT NULL,
    actor_id VARCHAR(128) NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    digest VARCHAR(128) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (config_id, revision)
);

CREATE TABLE IF NOT EXISTS unified_gateway_admin_idempotency (
    actor_id VARCHAR(128) NOT NULL,
    operation VARCHAR(128) NOT NULL,
    resource_id VARCHAR(128) NOT NULL DEFAULT '',
    idempotency_key VARCHAR(128) NOT NULL,
    request_digest VARCHAR(128) NOT NULL,
    response_status INTEGER NOT NULL,
    response_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (actor_id, operation, resource_id, idempotency_key)
);

ALTER TABLE unified_route_targets
    ADD COLUMN IF NOT EXISTS unified_config_id VARCHAR(128),
    ADD COLUMN IF NOT EXISTS unified_lane_id VARCHAR(128),
    ADD COLUMN IF NOT EXISTS unified_profile_id VARCHAR(128),
    ADD COLUMN IF NOT EXISTS unified_revision BIGINT,
    ADD COLUMN IF NOT EXISTS currency VARCHAR(8),
    ADD COLUMN IF NOT EXISTS rounding_mode VARCHAR(32),
    ADD COLUMN IF NOT EXISTS fallback_reason VARCHAR(128),
    ADD COLUMN IF NOT EXISTS pricing_schema_id VARCHAR(64),
    ADD COLUMN IF NOT EXISTS source_group_id BIGINT,
    ADD COLUMN IF NOT EXISTS source_group_revision VARCHAR(255);

ALTER TABLE unified_route_account_bindings
    ADD COLUMN IF NOT EXISTS unified_config_id VARCHAR(128),
    ADD COLUMN IF NOT EXISTS unified_revision BIGINT,
    ADD COLUMN IF NOT EXISTS probe_status VARCHAR(32),
    ADD COLUMN IF NOT EXISTS probe_snapshot_ref VARCHAR(255);

-- Legacy route rows have a different namespace.  New aggregate rows can be
-- published more than once without colliding with a prior revision, while
-- rows created by the old local route catalog retain their old uniqueness.
-- PostgreSQL truncates long auto-generated constraint names at 63 bytes.
-- Resolve the old unique constraint by its definition so this remains valid
-- on databases where the generated name was truncated differently.
DO $$
DECLARE
    old_constraint TEXT;
BEGIN
    SELECT conname
      INTO old_constraint
      FROM pg_constraint
     WHERE conrelid = 'unified_route_targets'::regclass
       AND contype = 'u'
       AND pg_get_constraintdef(oid) = 'UNIQUE (access_group_id, billing_lane_id, public_model, endpoint, provider_identity)';
    IF old_constraint IS NOT NULL THEN
        EXECUTE format('ALTER TABLE unified_route_targets DROP CONSTRAINT %I', old_constraint);
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS idx_unified_route_targets_legacy_unique
    ON unified_route_targets (access_group_id, billing_lane_id, public_model, endpoint, provider_identity)
    WHERE unified_config_id IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_unified_route_targets_admin_unique
    ON unified_route_targets (unified_config_id, unified_lane_id, public_model, endpoint, provider_identity)
    WHERE unified_config_id IS NOT NULL;

ALTER TABLE unified_route_account_bindings
    DROP CONSTRAINT IF EXISTS unified_route_account_bindings_route_target_id_fkey;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM pg_constraint
         WHERE conrelid = 'unified_route_account_bindings'::regclass
           AND conname = 'unified_route_account_bindings_route_target_id_fkey'
    ) THEN
        ALTER TABLE unified_route_account_bindings
            ADD CONSTRAINT unified_route_account_bindings_route_target_id_fkey
            FOREIGN KEY (route_target_id) REFERENCES unified_route_targets(id) ON DELETE RESTRICT;
    END IF;
END $$;

INSERT INTO unified_gateway_schema_version (version)
VALUES ('unified_gateway_admin_v1')
ON CONFLICT (version) DO NOTHING;
