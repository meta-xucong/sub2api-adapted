-- Example replay for a transient OpenAI image lane that should fail over
-- temporarily instead of being disabled permanently.
--
-- Replace REPLACE_ACCOUNT_NAME before running.
-- Backup first:
--   SELECT id, name, status, schedulable, credentials->'temp_unschedulable_rules'
--   FROM accounts
--   WHERE name = 'REPLACE_ACCOUNT_NAME' AND deleted_at IS NULL;
--
--   SELECT id, account_id, model_id, cron_expression, enabled, auto_recover
--   FROM scheduled_test_plans
--   WHERE account_id = (
--     SELECT id FROM accounts
--     WHERE name = 'REPLACE_ACCOUNT_NAME' AND deleted_at IS NULL
--   );

BEGIN;

WITH target AS (
  SELECT id
  FROM accounts
  WHERE name = 'REPLACE_ACCOUNT_NAME'
    AND deleted_at IS NULL
)
UPDATE accounts
SET credentials = jsonb_set(
      jsonb_set(
        COALESCE(credentials, '{}'::jsonb),
        '{temp_unschedulable_enabled}',
        'true'::jsonb,
        true
      ),
      '{temp_unschedulable_rules}',
      '[
        {
          "error_code": 403,
          "keywords": [
            "forbidden",
            "access denied",
            "please contact administrator",
            "upstream access forbidden"
          ],
          "duration_minutes": 15
        },
        {
          "error_code": 502,
          "keywords": [
            "upstream access forbidden",
            "please contact administrator"
          ],
          "duration_minutes": 15
        },
        {
          "error_code": 503,
          "keywords": [
            "temporarily unavailable",
            "upstream service temporarily unavailable"
          ],
          "duration_minutes": 10
        }
      ]'::jsonb,
      true
    ),
    updated_at = NOW()
FROM target
WHERE accounts.id = target.id;

WITH target AS (
  SELECT id
  FROM accounts
  WHERE name = 'REPLACE_ACCOUNT_NAME'
    AND deleted_at IS NULL
)
INSERT INTO scheduled_test_plans (
  account_id,
  model_id,
  cron_expression,
  enabled,
  max_results,
  auto_recover,
  next_run_at,
  created_at,
  updated_at
)
SELECT
  target.id,
  'gpt-image-2#edits',
  '*/5 * * * *',
  true,
  50,
  true,
  NOW(),
  NOW(),
  NOW()
FROM target
WHERE NOT EXISTS (
  SELECT 1
  FROM scheduled_test_plans stp
  WHERE stp.account_id = target.id
    AND stp.model_id = 'gpt-image-2#edits'
);

COMMIT;
