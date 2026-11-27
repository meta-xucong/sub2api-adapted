-- Durable operation routing only. No prompt, result, token, billing or dispatch.
-- Do not delete these tombstones or cascade deletion from API keys/users: key
-- rotation is intentionally a new scope, and lost history must not enable replay.
CREATE TABLE IF NOT EXISTS native_harness_operations (
 user_id BIGINT NOT NULL,
 api_key_id BIGINT NOT NULL,
 client_key VARCHAR(128) NOT NULL,
 request_digest CHAR(64) NOT NULL,
 operation_id VARCHAR(128) NOT NULL UNIQUE,
 route JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY (user_id, api_key_id, client_key),
 CHECK (user_id > 0 AND api_key_id > 0),
 CHECK (jsonb_typeof(route) = 'object')
);
