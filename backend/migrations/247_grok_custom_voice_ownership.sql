-- Durable local ownership for provider custom voices. Provider IDs may collide
-- across accounts: the global key rejects collisions instead of reassigning them.
-- Do not cascade-delete ownership on user/account removal. Tombstones must remain
-- to prevent a formerly authorized provider ID from being adopted by another user.
CREATE TABLE IF NOT EXISTS grok_custom_voices (
    voice_id TEXT PRIMARY KEY CHECK (voice_id ~ '^[A-Za-z0-9_-]{1,128}$'),
    user_id BIGINT NOT NULL CHECK (user_id > 0),
    account_id BIGINT NOT NULL CHECK (account_id > 0),
    -- Metadata only, with canonical JSONB expansion margin over the 8 KiB API limit.
    profile JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (octet_length(profile::text) <= 16384),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_grok_custom_voices_live_user
    ON grok_custom_voices (user_id, voice_id)
    WHERE deleted_at IS NULL;
