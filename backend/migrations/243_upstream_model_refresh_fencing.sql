CREATE TABLE IF NOT EXISTS upstream_model_refresh_fences (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    latest_issued_token BIGINT NOT NULL DEFAULT 0,
    last_applied_token BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT upstream_model_refresh_fences_token_order_check
        CHECK (latest_issued_token >= 0 AND last_applied_token >= 0 AND last_applied_token <= latest_issued_token)
);
