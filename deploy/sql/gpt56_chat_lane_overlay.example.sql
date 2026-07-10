-- Replayable GPT-5.6 chat-lane overlay for the aiself-style deployment.
--
-- This file contains no credentials. It is intentionally keyed by account and
-- group names so the numeric ids can change after an import or an upgrade.
-- Adapt the names for another deployment, then run inside a transaction after
-- taking a database backup.
--
-- Current aiself policy (2026-07-10):
--   - group chatgpt exposes sol, terra, and luna;
--   - liuyun plus is the active all-model lane;
--   - 7646881 pro is enabled for sol only;
--   - aicodexvip pro is a prepared cold spare and keeps its current
--     schedulable flag (currently false).
--
-- Do not replace the model ids with a bare "gpt-5.6" alias. The official
-- runtime model ids are gpt-5.6-sol, gpt-5.6-terra, and gpt-5.6-luna.

BEGIN;

DO $$
DECLARE
  account_count integer;
  group_count integer;
BEGIN
  SELECT COUNT(*) INTO account_count
  FROM accounts
  WHERE deleted_at IS NULL
    AND platform = 'openai'
    AND name IN (
      U&'\3010\81ea\7528\3011\6d41\4e91AI-plus',
      U&'\3010\81ea\7528\3011' || '7646881-pro',
      U&'\3010\81ea\7528\3011' || 'aicodexvip-pro'
    );

  IF account_count <> 3 THEN
    RAISE EXCEPTION
      'GPT-5.6 overlay stopped: expected 3 named OpenAI accounts, found %',
      account_count;
  END IF;

  SELECT COUNT(*) INTO group_count
  FROM groups
  WHERE deleted_at IS NULL
    AND platform = 'openai'
    AND name = 'chatgpt';

  IF group_count <> 1 THEN
    RAISE EXCEPTION
      'GPT-5.6 overlay stopped: expected exactly one OpenAI group named chatgpt, found %',
      group_count;
  END IF;
END $$;

-- Merge only model_mapping. Other credentials fields, including auth data,
-- are preserved byte-for-byte by this update.
UPDATE accounts
SET credentials = jsonb_set(
      COALESCE(credentials, '{}'::jsonb),
      '{model_mapping}',
      COALESCE(credentials -> 'model_mapping', '{}'::jsonb) ||
        CASE name
          WHEN U&'\3010\81ea\7528\3011\6d41\4e91AI-plus' THEN
            '{"gpt-5.6-sol":"gpt-5.6-sol","gpt-5.6-terra":"gpt-5.6-terra","gpt-5.6-luna":"gpt-5.6-luna"}'::jsonb
          WHEN U&'\3010\81ea\7528\3011' || '7646881-pro' THEN
            '{"gpt-5.6-sol":"gpt-5.6-sol"}'::jsonb
          WHEN U&'\3010\81ea\7528\3011' || 'aicodexvip-pro' THEN
            '{"gpt-5.6-sol":"gpt-5.6-sol","gpt-5.6-terra":"gpt-5.6-terra","gpt-5.6-luna":"gpt-5.6-luna"}'::jsonb
          ELSE '{}'::jsonb
        END,
      true
    ),
    updated_at = NOW()
WHERE deleted_at IS NULL
  AND platform = 'openai'
  AND name IN (
    U&'\3010\81ea\7528\3011\6d41\4e91AI-plus',
    U&'\3010\81ea\7528\3011' || '7646881-pro',
    U&'\3010\81ea\7528\3011' || 'aicodexvip-pro'
  );

-- Keep the custom model-list switch disabled, matching the verified runtime.
-- The account mappings remain the source of actual eligibility and scheduling.
UPDATE groups
SET models_list_config =
      '{"enabled":false,"models":["gpt-5.4-mini","gpt-5.4","gpt-5.5","gpt-5.6-sol","gpt-5.6-terra","gpt-5.6-luna","gpt-image-2"]}'::jsonb,
    updated_at = NOW()
WHERE deleted_at IS NULL
  AND platform = 'openai'
  AND name = 'chatgpt';

-- Wake the scheduler after direct SQL changes. The short time guard makes a
-- repeated replay safe while an earlier refresh event is still pending.
INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
SELECT 'account_changed', a.id, NULL, NULL
FROM accounts a
WHERE a.deleted_at IS NULL
  AND a.platform = 'openai'
  AND a.name IN (
    U&'\3010\81ea\7528\3011\6d41\4e91AI-plus',
    U&'\3010\81ea\7528\3011' || '7646881-pro',
    U&'\3010\81ea\7528\3011' || 'aicodexvip-pro'
  )
  AND NOT EXISTS (
    SELECT 1
    FROM scheduler_outbox pending
    WHERE pending.event_type = 'account_changed'
      AND pending.account_id = a.id
      AND pending.created_at > NOW() - INTERVAL '5 minutes'
  );

INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
SELECT 'group_changed', NULL, g.id, NULL
FROM groups g
WHERE g.deleted_at IS NULL
  AND g.platform = 'openai'
  AND g.name = 'chatgpt'
  AND NOT EXISTS (
    SELECT 1
    FROM scheduler_outbox pending
    WHERE pending.event_type = 'group_changed'
      AND pending.group_id = g.id
      AND pending.created_at > NOW() - INTERVAL '5 minutes'
  );

INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
SELECT 'full_rebuild', NULL, NULL, NULL
WHERE NOT EXISTS (
  SELECT 1
  FROM scheduler_outbox pending
  WHERE pending.event_type = 'full_rebuild'
    AND pending.created_at > NOW() - INTERVAL '5 minutes'
);

-- Secret-free verification.
SELECT id, name, schedulable,
       credentials -> 'model_mapping' AS model_mapping
FROM accounts
WHERE deleted_at IS NULL
  AND platform = 'openai'
  AND name IN (
    U&'\3010\81ea\7528\3011\6d41\4e91AI-plus',
    U&'\3010\81ea\7528\3011' || '7646881-pro',
    U&'\3010\81ea\7528\3011' || 'aicodexvip-pro'
  )
ORDER BY name;

SELECT id, name, models_list_config
FROM groups
WHERE deleted_at IS NULL
  AND platform = 'openai'
  AND name = 'chatgpt';

COMMIT;
