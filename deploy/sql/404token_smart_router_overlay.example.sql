-- 404token-specific Smart Router routing overlay.
--
-- This replay preserves every manually configured account priority, group
-- priority, model mapping, status, and credential. It only annotates router
-- lanes so chat/Responses and image traffic are isolated and same-origin
-- retries cannot multiply upstream pressure.

BEGIN;

DO $$
DECLARE
    matched_count integer;
BEGIN
    SELECT COUNT(*) INTO matched_count
    FROM accounts
    WHERE name IN (
        '7646881-爽蹬',
        '7646881-plus',
        '7646881-pro',
        '7646881-兜底',
        '流云AI-plus',
        '流云AI-k12',
        '流云AI-pro直连',
        'YeToken-特惠',
        'YeToken-平价',
        'YeToken-pro',
        'YeToken-兜底',
        'YeToken-1K生图',
        'YeToken-超分生图',
        '流云AI-生图',
        '7646881-生图',
        'aicodexvip生图'
    )
      AND deleted_at IS NULL
      AND platform = 'openai';

    IF matched_count <> 16 THEN
        RAISE EXCEPTION 'expected sixteen active 404token routing accounts, found %', matched_count;
    END IF;
END $$;

WITH policy(name, lane_id, source_group, capabilities, max_concurrency, source_group_max_concurrency, base_weight, cost_multiplier) AS (
    VALUES
        ('7646881-爽蹬', 'chat:7646881-sale', 'route:7646881-sale', '["chat","responses"]'::jsonb, 1, 1, 1.0, 1.0),
        ('7646881-plus', 'chat:7646881-plus', 'route:7646881-plus', '["chat","responses"]'::jsonb, 1, 1, 1.0, 1.0),
        ('7646881-pro', 'chat:7646881-pro', 'route:7646881-pro', '["chat","responses"]'::jsonb, 1, 1, 1.0, 1.0),
        ('7646881-兜底', 'chat:7646881-fallback', 'route:7646881-fallback', '["chat","responses"]'::jsonb, 1, 1, 1.0, 1.0),
        ('流云AI-plus', 'chat:liuyun-plus', 'route:liuyun-plus', '["chat","responses"]'::jsonb, 1, 1, 1.0, 1.0),
        ('流云AI-k12', 'chat:liuyun-k12', 'route:liuyun-k12', '["chat","responses"]'::jsonb, 1, 1, 1.0, 1.0),
        ('流云AI-pro直连', 'chat:liuyun-pro', 'route:liuyun-pro', '["chat","responses"]'::jsonb, 1, 1, 1.0, 1.0),
        ('YeToken-特惠', 'chat:yetoken-sale', 'yetoken-chat', '["chat","responses"]'::jsonb, 2, 2, 1.0, 1.0),
        ('YeToken-平价', 'chat:yetoken-value', 'yetoken-chat', '["chat","responses"]'::jsonb, 2, 2, 1.0, 1.0),
        ('YeToken-pro', 'chat:yetoken-pro', 'yetoken-chat', '["chat","responses"]'::jsonb, 2, 2, 1.0, 1.0),
        ('YeToken-兜底', 'chat:yetoken-fallback', 'yetoken-chat', '["chat","responses"]'::jsonb, 2, 2, 1.0, 1.0),
        ('YeToken-1K生图', 'image:yetoken-1k', 'yetoken-image', '["image_generation"]'::jsonb, 1, 1, 1.0, 1.0),
        ('YeToken-超分生图', 'image:yetoken-super-res', 'yetoken-image', '["image_generation"]'::jsonb, 1, 1, 1.0, 1.0),
        ('流云AI-生图', 'image:liuyun', 'image:liuyun', '["image_generation","image_edit"]'::jsonb, 1, 1, 0.85, 1.2),
        ('7646881-生图', 'image:7646881', 'image:7646881', '["image_generation","image_edit"]'::jsonb, 1, 1, 1.15, 1.0),
        ('aicodexvip生图', 'image:aicodexvip', 'image:aicodexvip', '["image_generation","image_edit"]'::jsonb, 1, 1, 1.05, 1.0)
)
UPDATE accounts AS account
SET extra = jsonb_set(
        COALESCE(account.extra, '{}'::jsonb),
        '{smart_router}',
        jsonb_build_object(
            'enabled', true,
            'lane_id', policy.lane_id,
            'source_group', policy.source_group,
            'capabilities', policy.capabilities,
            'max_concurrency', policy.max_concurrency,
            'source_group_max_concurrency', policy.source_group_max_concurrency,
            'base_weight', policy.base_weight,
            'cost_multiplier', policy.cost_multiplier
        ),
        true
    )
FROM policy
WHERE account.name = policy.name
  AND account.platform = 'openai'
  AND account.deleted_at IS NULL;

COMMIT;
