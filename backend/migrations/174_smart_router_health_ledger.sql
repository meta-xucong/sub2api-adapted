-- Durable, capability-scoped Smart Router health state. These tables store
-- only routing metadata and safe error summaries; no prompts or credentials.

CREATE TABLE IF NOT EXISTS smart_router_health_events (
    id BIGSERIAL PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL,
    source VARCHAR(32) NOT NULL,
    lane_id VARCHAR(255) NOT NULL,
    account_id BIGINT NOT NULL DEFAULT 0,
    source_group VARCHAR(255) NOT NULL DEFAULT '',
    capability VARCHAR(64) NOT NULL,
    model_family VARCHAR(128) NOT NULL,
    success BOOLEAN NOT NULL,
    status_code INTEGER NOT NULL DEFAULT 0,
    failure_class VARCHAR(64) NOT NULL DEFAULT '',
    action VARCHAR(96) NOT NULL DEFAULT '',
    cooldown_until TIMESTAMPTZ NULL,
    health_penalty INTEGER NOT NULL DEFAULT 0,
    health_score DOUBLE PRECISION NOT NULL DEFAULT 1,
    error_rate_ewma DOUBLE PRECISION NOT NULL DEFAULT 0,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    consecutive_successes INTEGER NOT NULL DEFAULT 0,
    recovery_stage VARCHAR(64) NOT NULL DEFAULT 'normal',
    latency_ms BIGINT NOT NULL DEFAULT 0,
    error_summary VARCHAR(256) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS smart_router_health_events_lane_capability_idx
    ON smart_router_health_events (lane_id, capability, model_family, occurred_at DESC);

CREATE TABLE IF NOT EXISTS smart_router_lane_state (
    lane_id VARCHAR(255) NOT NULL,
    capability VARCHAR(64) NOT NULL,
    model_family VARCHAR(128) NOT NULL,
    account_id BIGINT NOT NULL DEFAULT 0,
    source_group VARCHAR(255) NOT NULL DEFAULT '',
    health_penalty INTEGER NOT NULL DEFAULT 0,
    health_score DOUBLE PRECISION NOT NULL DEFAULT 1,
    error_rate_ewma DOUBLE PRECISION NOT NULL DEFAULT 0,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    consecutive_successes INTEGER NOT NULL DEFAULT 0,
    cooldown_until TIMESTAMPTZ NULL,
    recovery_stage VARCHAR(64) NOT NULL DEFAULT 'normal',
    last_success_at TIMESTAMPTZ NULL,
    last_failure_at TIMESTAMPTZ NULL,
    last_failure_unix BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (lane_id, capability, model_family)
);

CREATE TABLE IF NOT EXISTS smart_router_calibration_runs (
    id BIGSERIAL PRIMARY KEY,
    scheduled_for TIMESTAMPTZ NOT NULL UNIQUE,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'running',
    summary VARCHAR(512) NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS smart_router_calibration_results (
    id BIGSERIAL PRIMARY KEY,
    calibration_run_id BIGINT NOT NULL REFERENCES smart_router_calibration_runs(id) ON DELETE CASCADE,
    lane_id VARCHAR(255) NOT NULL,
    account_id BIGINT NOT NULL DEFAULT 0,
    source_group VARCHAR(255) NOT NULL DEFAULT '',
    capability VARCHAR(64) NOT NULL,
    model_family VARCHAR(128) NOT NULL,
    success BOOLEAN NOT NULL,
    status_code INTEGER NOT NULL DEFAULT 0,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    error_summary VARCHAR(256) NOT NULL DEFAULT '',
    reason VARCHAR(128) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS smart_router_calibration_results_run_idx
    ON smart_router_calibration_results (calibration_run_id, created_at);
