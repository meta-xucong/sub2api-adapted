CREATE TABLE IF NOT EXISTS upstream_model_refresh_runs (
    run_id TEXT PRIMARY KEY,
    status VARCHAR(32) NOT NULL,
    scheduled_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    eligible INTEGER NOT NULL DEFAULT 0,
    succeeded INTEGER NOT NULL DEFAULT 0,
    failed INTEGER NOT NULL DEFAULT 0,
    unsupported INTEGER NOT NULL DEFAULT 0,
    skipped INTEGER NOT NULL DEFAULT 0,
    accounts JSONB NOT NULL DEFAULT '[]'::jsonb,
    error_kind VARCHAR(64) NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_upstream_model_refresh_runs_started_at
    ON upstream_model_refresh_runs (started_at DESC);

CREATE TABLE IF NOT EXISTS upstream_model_policy_previews (
    preview_id TEXT PRIMARY KEY,
    plan_hash VARCHAR(80) NOT NULL,
    account_ids JSONB NOT NULL,
    plan JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_upstream_model_policy_previews_expires_at
    ON upstream_model_policy_previews (expires_at);

CREATE TABLE IF NOT EXISTS upstream_model_policy_audits (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    actor_user_id BIGINT NOT NULL,
    preview_id TEXT NOT NULL,
    plan_hash VARCHAR(80) NOT NULL,
    account_ids JSONB NOT NULL,
    previous_policies JSONB NOT NULL,
    policy VARCHAR(32) NOT NULL,
    CONSTRAINT upstream_model_policy_audits_policy_check
        CHECK (policy IN ('manual', 'follow_upstream'))
);

CREATE INDEX IF NOT EXISTS idx_upstream_model_policy_audits_created_at
    ON upstream_model_policy_audits (created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_upstream_model_policy_audits_actor
    ON upstream_model_policy_audits (actor_user_id, created_at DESC);
