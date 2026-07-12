-- Aiself-specific YeToken Smart Router overlay.
--
-- This replay contains no credentials and intentionally preserves every
-- account and group priority. It only sets capability-scoped router metadata.
-- Review the named accounts and back up PostgreSQL before applying it.

BEGIN;

DO $$
DECLARE
    matched_count integer;
BEGIN
    SELECT COUNT(*) INTO matched_count
    FROM accounts
    WHERE name IN (
        '【自用】YeToken-特惠',
        '【自用】YeToken-平价',
        '【自用】YeToken-pro',
        '【自用】YeToken-兜底',
        '【自用】YeToken-1K生图',
        '【自用】YeToken-超分生图'
    )
      AND deleted_at IS NULL;

    IF matched_count <> 6 THEN
        RAISE EXCEPTION 'expected six active YeToken accounts, found %', matched_count;
    END IF;
END $$;

WITH policy(name, lane_id, source_group, capabilities, max_concurrency, source_group_max_concurrency) AS (
    VALUES
        ('【自用】YeToken-特惠', 'chat:yetoken-sale', 'yetoken-chat', '["chat","responses"]'::jsonb, 3, 2),
        ('【自用】YeToken-平价', 'chat:yetoken-value', 'yetoken-chat', '["chat","responses"]'::jsonb, 3, 2),
        ('【自用】YeToken-pro', 'chat:yetoken-pro', 'yetoken-chat', '["chat","responses"]'::jsonb, 3, 2),
        ('【自用】YeToken-兜底', 'chat:yetoken-fallback', 'yetoken-chat', '["chat","responses"]'::jsonb, 1, 2),
        ('【自用】YeToken-1K生图', 'image:yetoken-1k', 'yetoken-image', '["image_generation"]'::jsonb, 1, 1),
        ('【自用】YeToken-超分生图', 'image:yetoken-super-res', 'yetoken-image', '["image_generation"]'::jsonb, 1, 1)
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
            'source_group_max_concurrency', policy.source_group_max_concurrency
        ),
        true
    )
FROM policy
WHERE account.name = policy.name
  AND account.deleted_at IS NULL;

COMMIT;
