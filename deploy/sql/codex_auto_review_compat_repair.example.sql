-- One-time repair for the retired "prefer GPT-5.6" auto-review overlay.
--
-- Scope:
--   - Only the synthetic codex-auto-review mapping is changed.
--   - Explicit gpt-5.5 / gpt-5.6-* mappings, priority, concurrency, group
--     membership, balances, and schedulable flags are left untouched.
--   - Accounts with an explicit gpt-5.5 mapping fall back to that mapping.
--   - Accounts without gpt-5.5 lose only the stale synthetic mapping, so they
--     cannot be selected for an auto-review request they cannot serve safely.
--
-- The backup table remains in PostgreSQL and may contain credential JSON.
-- Do not export it into source control.

BEGIN;

CREATE TABLE IF NOT EXISTS codex_ops_auto_review_compat_repair_backup_20260804 (
    account_id bigint PRIMARY KEY,
    credentials jsonb NOT NULL,
    updated_at timestamptz,
    backed_up_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO codex_ops_auto_review_compat_repair_backup_20260804 (
    account_id, credentials, updated_at
)
SELECT a.id, a.credentials, a.updated_at
FROM accounts a
WHERE a.deleted_at IS NULL
  AND a.platform = 'openai'
  AND a.credentials -> 'model_mapping' ? 'codex-auto-review'
  AND lower(a.credentials -> 'model_mapping' ->> 'codex-auto-review')
        LIKE 'gpt-5.6-%'
ON CONFLICT (account_id) DO NOTHING;

WITH targets AS (
    SELECT a.id,
           a.credentials -> 'model_mapping' ->> 'gpt-5.5' AS fallback_model
    FROM accounts a
    WHERE a.deleted_at IS NULL
      AND a.platform = 'openai'
      AND a.credentials -> 'model_mapping' ? 'codex-auto-review'
      AND lower(a.credentials -> 'model_mapping' ->> 'codex-auto-review')
            LIKE 'gpt-5.6-%'
      AND a.credentials -> 'model_mapping' ? 'gpt-5.5'
), updated_accounts AS (
    UPDATE accounts a
    SET credentials = jsonb_set(
            COALESCE(a.credentials, '{}'::jsonb),
            '{model_mapping,codex-auto-review}',
            to_jsonb(t.fallback_model),
            true
        ),
        updated_at = now()
    FROM targets t
    WHERE a.id = t.id
      AND NULLIF(trim(t.fallback_model), '') IS NOT NULL
      AND a.credentials -> 'model_mapping' ->> 'codex-auto-review'
            IS DISTINCT FROM t.fallback_model
    RETURNING a.id
)
SELECT 'mapped_to_gpt_5_5' AS section, count(*) AS rows_changed
FROM updated_accounts;

WITH removed_accounts AS (
    UPDATE accounts a
    SET credentials = jsonb_set(
            COALESCE(a.credentials, '{}'::jsonb),
            '{model_mapping}',
            (a.credentials -> 'model_mapping') - 'codex-auto-review',
            true
        ),
        updated_at = now()
    WHERE a.deleted_at IS NULL
      AND a.platform = 'openai'
      AND a.credentials -> 'model_mapping' ? 'codex-auto-review'
      AND lower(a.credentials -> 'model_mapping' ->> 'codex-auto-review')
            LIKE 'gpt-5.6-%'
      AND NOT (a.credentials -> 'model_mapping' ? 'gpt-5.5')
    RETURNING a.id
)
SELECT 'removed_without_gpt_5_5' AS section, count(*) AS rows_changed
FROM removed_accounts;

DO $$
BEGIN
    IF to_regclass('public.scheduler_outbox') IS NOT NULL THEN
        INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
        SELECT 'account_changed', b.account_id, NULL, NULL
        FROM codex_ops_auto_review_compat_repair_backup_20260804 b
        WHERE NOT EXISTS (
            SELECT 1
            FROM scheduler_outbox pending
            WHERE pending.event_type = 'account_changed'
              AND pending.account_id = b.account_id
              AND pending.created_at > now() - interval '5 minutes'
        );

        INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
        SELECT 'full_rebuild', NULL, NULL, NULL
        WHERE NOT EXISTS (
            SELECT 1
            FROM scheduler_outbox pending
            WHERE pending.event_type = 'full_rebuild'
              AND pending.created_at > now() - interval '5 minutes'
        );
    END IF;
END $$;

SELECT a.id,
       a.name,
       a.credentials -> 'model_mapping' ->> 'codex-auto-review' AS auto_review_target,
       a.credentials -> 'model_mapping' ->> 'gpt-5.5' AS gpt_5_5_target
FROM accounts a
WHERE a.deleted_at IS NULL
  AND a.platform = 'openai'
  AND a.credentials -> 'model_mapping' ? 'codex-auto-review'
ORDER BY a.id;

COMMIT;
