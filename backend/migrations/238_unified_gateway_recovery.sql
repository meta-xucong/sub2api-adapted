-- Unified gateway durable recovery tasks.
--
-- A task is discovered from the durable snapshot table instead of relying on
-- process memory.  reservation_key stores the SHA-256 digest used by the
-- ledger table because the gateway's raw reservation key contains a NUL
-- separator, which PostgreSQL text values cannot contain.

CREATE TABLE IF NOT EXISTS unified_gateway_recovery_tasks (
    id BIGSERIAL PRIMARY KEY,
    api_key_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    access_group_id BIGINT NOT NULL,
    request_id VARCHAR(255) NOT NULL,
    attempt_id VARCHAR(128) NOT NULL,
    reservation_key VARCHAR(64) NOT NULL,
    snapshot_status VARCHAR(32) NOT NULL CHECK (snapshot_status IN ('quoted', 'reserved', 'pending', 'captured', 'released', 'settlement_failed')),
    user_charge DECIMAL(20,8) NOT NULL DEFAULT 0 CHECK (user_charge >= 0),
    status VARCHAR(32) NOT NULL CHECK (status IN ('pending', 'processing', 'completed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    locked_until TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT unified_gateway_recovery_tasks_request_key UNIQUE (api_key_id, user_id, access_group_id, request_id, attempt_id)
);

CREATE INDEX IF NOT EXISTS idx_unified_gateway_recovery_tasks_claim
    ON unified_gateway_recovery_tasks (status, next_attempt_at, id);

CREATE INDEX IF NOT EXISTS idx_unified_gateway_recovery_tasks_user_status
    ON unified_gateway_recovery_tasks (user_id, status, updated_at);

INSERT INTO unified_gateway_schema_version (version)
VALUES ('unified_gateway_recovery_v1')
ON CONFLICT (version) DO NOTHING;
