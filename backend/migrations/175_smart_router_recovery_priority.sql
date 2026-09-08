-- Persist the absolute temporary recovery priority assigned by Smart Router.
-- This is runtime state only; account.priority remains the configured price order.

ALTER TABLE smart_router_health_events
    ADD COLUMN IF NOT EXISTS recovery_priority INTEGER NOT NULL DEFAULT 0;

ALTER TABLE smart_router_lane_state
    ADD COLUMN IF NOT EXISTS recovery_priority INTEGER NOT NULL DEFAULT 0;
