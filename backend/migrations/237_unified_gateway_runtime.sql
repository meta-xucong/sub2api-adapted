-- Unified gateway runtime persistence.
--
-- This migration is additive and isolated from the legacy billing path.  The
-- snapshot status update is deliberately resolved by constraint definition so
-- it also works when PostgreSQL generated a truncated/default constraint name
-- for migration 235.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS frozen_balance DECIMAL(20,8) NOT NULL DEFAULT 0;

-- Responses may be JSON, SSE, or binary media metadata.  Keep the original
-- JSONB column for compatibility with existing admin/read tooling, but use
-- this opaque byte column for the runtime's exact response replay.  This
-- prevents a successful streamed response from becoming an unbillable
-- settlement error merely because it is not valid JSON.
ALTER TABLE unified_route_price_snapshots
    ADD COLUMN IF NOT EXISTS response_body_bytes BYTEA;

DO $$
DECLARE
    status_constraint_name TEXT;
    status_constraint_definition TEXT;
BEGIN
    IF to_regclass('unified_route_price_snapshots') IS NOT NULL THEN
        SELECT c.conname, pg_get_constraintdef(c.oid)
          INTO status_constraint_name, status_constraint_definition
          FROM pg_constraint AS c
         WHERE c.conrelid = 'unified_route_price_snapshots'::regclass
           AND c.contype = 'c'
           AND (
               c.conname = 'unified_route_price_snapshots_status_check'
               OR (
                   pg_get_constraintdef(c.oid) ILIKE '%status%'
                   AND pg_get_constraintdef(c.oid) ILIKE '%quoted%'
                   AND pg_get_constraintdef(c.oid) ILIKE '%reserved%'
                   AND pg_get_constraintdef(c.oid) ILIKE '%captured%'
                   AND pg_get_constraintdef(c.oid) ILIKE '%released%'
                   AND pg_get_constraintdef(c.oid) ILIKE '%settlement_failed%'
               )
           )
         ORDER BY (c.conname = 'unified_route_price_snapshots_status_check') DESC
         LIMIT 1;

        IF status_constraint_name IS NOT NULL AND NOT (
            status_constraint_definition ILIKE '%quoted%'
            AND status_constraint_definition ILIKE '%reserved%'
            AND status_constraint_definition ILIKE '%pending%'
            AND status_constraint_definition ILIKE '%captured%'
            AND status_constraint_definition ILIKE '%released%'
            AND status_constraint_definition ILIKE '%settlement_failed%'
        ) THEN
            EXECUTE format(
                'ALTER TABLE unified_route_price_snapshots DROP CONSTRAINT %I',
                status_constraint_name
            );
            status_constraint_name := NULL;
        END IF;

        IF status_constraint_name IS NULL THEN
            ALTER TABLE unified_route_price_snapshots
                ADD CONSTRAINT unified_route_price_snapshots_status_check
                CHECK (status IN ('quoted', 'reserved', 'pending', 'captured', 'released', 'settlement_failed'));
        END IF;
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS unified_gateway_charge_ledger (
    id BIGSERIAL PRIMARY KEY,
    -- The repository stores a SHA-256 digest here because the existing
    -- service reservation key uses a NUL separator, which PostgreSQL text
    -- columns cannot store.
    reservation_key VARCHAR(64) NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reserved_amount DECIMAL(20,8) NOT NULL DEFAULT 0 CHECK (reserved_amount >= 0),
    captured_amount DECIMAL(20,8) NOT NULL DEFAULT 0 CHECK (captured_amount >= 0),
    status VARCHAR(32) NOT NULL CHECK (status IN ('reserved', 'captured', 'released')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_unified_gateway_charge_ledger_reservation_key
    ON unified_gateway_charge_ledger (reservation_key);

CREATE INDEX IF NOT EXISTS idx_unified_gateway_charge_ledger_user_status
    ON unified_gateway_charge_ledger (user_id, status, updated_at);

CREATE INDEX IF NOT EXISTS idx_unified_route_price_snapshots_upstream_request
    ON unified_route_price_snapshots (api_key_id, user_id, access_group_id, upstream_request_id)
    WHERE upstream_request_id <> '';
