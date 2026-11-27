CREATE TABLE IF NOT EXISTS veyra_node_reward_redemptions (
    user_id BIGINT NOT NULL,
    redeemed_images BIGINT NOT NULL CHECK (redeemed_images > 0),
    credited_images BIGINT NOT NULL CHECK (credited_images > 0 AND credited_images <= redeemed_images),
    balance_after NUMERIC(20, 8) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, redeemed_images)
);
