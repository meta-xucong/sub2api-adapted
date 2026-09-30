-- Unified gateway is an opt-in, isolated domain.  Legacy groups, routes,
-- accounts and usage logs do not read these tables.
CREATE TABLE IF NOT EXISTS unified_route_targets (
    id BIGSERIAL PRIMARY KEY,
    access_group_id BIGINT NOT NULL,
    billing_lane_id VARCHAR(128) NOT NULL,
    public_model VARCHAR(255) NOT NULL,
    provider_identity VARCHAR(255) NOT NULL,
    upstream_model VARCHAR(255) NOT NULL,
    endpoint VARCHAR(128) NOT NULL,
    pool_id VARCHAR(128) NOT NULL DEFAULT '',
    billing_mode VARCHAR(64) NOT NULL,
    rate_mode VARCHAR(32) NOT NULL,
    rate_basis VARCHAR(64) NOT NULL,
    lane_rule JSONB NOT NULL DEFAULT '{}'::jsonb,
    pool_rule JSONB NOT NULL DEFAULT '{}'::jsonb,
    priority INTEGER NOT NULL DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (access_group_id, billing_lane_id, public_model, endpoint, provider_identity)
);

CREATE TABLE IF NOT EXISTS unified_route_account_bindings (
    id BIGSERIAL PRIMARY KEY,
    route_target_id BIGINT NOT NULL REFERENCES unified_route_targets(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL,
    provider_identity VARCHAR(255) NOT NULL DEFAULT '',
    upstream_model VARCHAR(255) NOT NULL DEFAULT '',
    endpoint VARCHAR(128) NOT NULL DEFAULT '',
    account_rule JSONB NOT NULL DEFAULT '{}'::jsonb,
    probe_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
    priority INTEGER NOT NULL DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (route_target_id, account_id)
);

CREATE INDEX IF NOT EXISTS idx_unified_route_targets_lookup
    ON unified_route_targets (access_group_id, public_model, endpoint, enabled, priority, id);

CREATE INDEX IF NOT EXISTS idx_unified_route_account_bindings_lookup
    ON unified_route_account_bindings (route_target_id, enabled, priority, id);

CREATE TABLE IF NOT EXISTS unified_route_price_snapshots (
    id BIGSERIAL PRIMARY KEY,
    api_key_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    request_id VARCHAR(255) NOT NULL,
    attempt_id VARCHAR(128) NOT NULL,
    route_target_id BIGINT NOT NULL,
    access_group_id BIGINT NOT NULL,
    billing_lane_id VARCHAR(128) NOT NULL,
    account_id BIGINT NOT NULL,
    provider_identity VARCHAR(255) NOT NULL,
    public_model VARCHAR(255) NOT NULL,
    upstream_model VARCHAR(255) NOT NULL,
    endpoint VARCHAR(128) NOT NULL,
    status VARCHAR(32) NOT NULL CHECK (status IN ('quoted', 'reserved', 'captured', 'released', 'settlement_failed')),
    selection_json JSONB NOT NULL,
    snapshot_json JSONB NOT NULL,
    response_body JSONB,
    measured_units NUMERIC(30,12) NOT NULL DEFAULT 0,
    user_charge NUMERIC(30,12) NOT NULL DEFAULT 0,
    upstream_request_id VARCHAR(255) NOT NULL DEFAULT '',
    failure_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finalized_at TIMESTAMPTZ,
    UNIQUE (api_key_id, user_id, access_group_id, request_id, attempt_id)
);

CREATE INDEX IF NOT EXISTS idx_unified_route_price_snapshots_status
    ON unified_route_price_snapshots (status, created_at);

CREATE INDEX IF NOT EXISTS idx_unified_route_price_snapshots_account
    ON unified_route_price_snapshots (account_id, created_at);
