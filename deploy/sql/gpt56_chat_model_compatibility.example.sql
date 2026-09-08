-- Safe, deployment-independent GPT-5.6 compatibility overlay.
--
-- This template intentionally does not identify accounts by name, URL, group,
-- or numeric id. It only adds the bare gpt-5.6 alias to an OpenAI account that
-- already has an explicit gpt-5.6-sol mapping. Explicit Sol/Terra/Luna entries
-- are never overwritten or cross-mapped.
--
-- Review the SELECT result and take a database backup before applying this
-- template in a production deployment. Do not use it for image-only accounts.

BEGIN;

-- Optional operator backup. If the deployment already has this table, keep
-- its existing schema and use the deployment's normal backup procedure.
CREATE TABLE IF NOT EXISTS codex_ops_model_mapping_backup (
    account_id bigint NOT NULL,
    credentials jsonb NOT NULL,
    backed_up_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO codex_ops_model_mapping_backup (account_id, credentials)
SELECT a.id, a.credentials
FROM accounts AS a
WHERE a.deleted_at IS NULL
  AND a.platform = 'openai'
  AND a.credentials -> 'model_mapping' ? 'gpt-5.6-sol'
  AND NOT EXISTS (
      SELECT 1
      FROM codex_ops_model_mapping_backup AS b
      WHERE b.account_id = a.id
  );

-- Add only an absent bare alias. Existing manual mappings are left untouched.
UPDATE accounts AS a
SET credentials = jsonb_set(
        COALESCE(a.credentials, '{}'::jsonb),
        '{model_mapping,gpt-5.6}',
        to_jsonb('gpt-5.6-sol'::text),
        true
    ),
    updated_at = now()
WHERE a.deleted_at IS NULL
  AND a.platform = 'openai'
  AND a.credentials -> 'model_mapping' ? 'gpt-5.6-sol'
  AND NOT (a.credentials -> 'model_mapping' ? 'gpt-5.6');

-- Refresh the in-process scheduler without changing priority or group policy.
INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
SELECT 'account_changed', a.id, NULL, NULL
FROM accounts AS a
WHERE a.deleted_at IS NULL
  AND a.platform = 'openai'
  AND a.credentials -> 'model_mapping' ->> 'gpt-5.6' = 'gpt-5.6-sol'
  AND NOT EXISTS (
      SELECT 1
      FROM scheduler_outbox AS pending
      WHERE pending.event_type = 'account_changed'
        AND pending.account_id = a.id
        AND pending.created_at > now() - interval '5 minutes'
  );

COMMIT;

-- Secret-free audit after applying:
SELECT id,
       credentials -> 'model_mapping' AS model_mapping,
       priority,
       concurrency,
       schedulable
FROM accounts
WHERE deleted_at IS NULL
  AND platform = 'openai'
  AND credentials -> 'model_mapping' ? 'gpt-5.6-sol'
ORDER BY id;
