-- Replayable Codex auto-review compatibility overlay.
--
-- Purpose:
--   Codex may send approval/review traffic as model "codex-auto-review" even
--   when the user's main model is a normal GPT model. Third-party Sub2API
--   deployments often do not expose a native upstream model with that exact
--   name, so the scheduler can reject the request before it reaches any
--   healthy chat lane.
--
-- Policy:
--   - Add codex-auto-review to OpenAI chat groups that already expose gpt-5.5.
--   - When a client sends the synthetic codex-auto-review model, use the
--     account's explicit gpt-5.5 mapping as a conservative compatibility
--     fallback. The synthetic model does not carry the user's selected model,
--     so choosing a newer GPT-5.6 lane here is unsafe and changes behavior.
--   - Never use this overlay to rewrite an explicit gpt-5.6-sol,
--     gpt-5.6-terra, or gpt-5.6-luna request. Those model ids remain governed
--     by their own account mappings and Smart Router health.
--   - Do not use the bare gpt-5.6 alias; several upstream distributors expose
--     only the explicit Sol/Terra/Luna ids and reject "gpt-5.6".
--   - Do not touch image-only lanes, account priorities, group membership,
--     schedulable flags, credentials, balances, or concurrency.
--
-- Run after a PostgreSQL backup. The local backup tables created below stay in
-- the target database and may contain credential JSON, so do not export them
-- into this repository.

BEGIN;

CREATE TABLE IF NOT EXISTS codex_auto_review_model_overlay_backup_accounts AS
SELECT id, name, platform, type, credentials, extra, priority, concurrency,
       status, schedulable, updated_at
FROM accounts
WHERE deleted_at IS NULL
  AND platform = 'openai'
  AND credentials -> 'model_mapping' ? 'gpt-5.5';

CREATE TABLE IF NOT EXISTS codex_auto_review_model_overlay_backup_groups AS
SELECT id, name, platform, models_list_config, status, updated_at
FROM groups
WHERE deleted_at IS NULL
  AND platform = 'openai'
  AND models_list_config -> 'models' ? 'gpt-5.5';

WITH targets AS (
  SELECT id,
         credentials -> 'model_mapping' ->> 'gpt-5.5' AS target_model
  FROM accounts
  WHERE deleted_at IS NULL
    AND platform = 'openai'
    AND credentials -> 'model_mapping' ? 'gpt-5.5'
), updated_accounts AS (
  UPDATE accounts a
  SET credentials = jsonb_set(
        COALESCE(a.credentials, '{}'::jsonb),
        '{model_mapping,codex-auto-review}',
        to_jsonb(t.target_model),
        true
      ),
      updated_at = NOW()
  FROM targets t
  WHERE a.id = t.id
    AND t.target_model IS NOT NULL
    AND (a.credentials -> 'model_mapping' ->> 'codex-auto-review')
          IS DISTINCT FROM t.target_model
  RETURNING a.id
)
SELECT 'updated_accounts' AS section, count(*) AS rows_changed
FROM updated_accounts;

WITH updated_groups AS (
  UPDATE groups g
  SET models_list_config = jsonb_set(
        COALESCE(g.models_list_config, '{}'::jsonb),
        '{models}',
        (
          SELECT jsonb_agg(value ORDER BY sort_key)
          FROM (
            SELECT elem.value, elem.ord * 2 AS sort_key
            FROM jsonb_array_elements(COALESCE(g.models_list_config -> 'models', '[]'::jsonb))
              WITH ORDINALITY AS elem(value, ord)
            UNION ALL
            SELECT to_jsonb('codex-auto-review'::text),
                   COALESCE((
                     SELECT elem.ord * 2 + 1
                     FROM jsonb_array_elements_text(COALESCE(g.models_list_config -> 'models', '[]'::jsonb))
                       WITH ORDINALITY AS elem(model, ord)
                     WHERE elem.model = 'gpt-5.5'
                     LIMIT 1
                   ), 999999)
          ) s
        ),
        true
      ),
      updated_at = NOW()
  WHERE g.deleted_at IS NULL
    AND g.platform = 'openai'
    AND g.models_list_config -> 'models' ? 'gpt-5.5'
    AND NOT (g.models_list_config -> 'models' ? 'codex-auto-review')
  RETURNING g.id
)
SELECT 'updated_groups' AS section, count(*) AS rows_changed
FROM updated_groups;

DO $$
BEGIN
  IF to_regclass('public.scheduler_outbox') IS NOT NULL THEN
    INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
    SELECT 'account_changed', a.id, NULL, NULL
    FROM accounts a
    WHERE a.deleted_at IS NULL
      AND a.platform = 'openai'
      AND a.credentials -> 'model_mapping' ? 'codex-auto-review'
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
      AND g.models_list_config -> 'models' ? 'codex-auto-review'
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
  END IF;
END $$;

-- Secret-free verification.
SELECT credentials -> 'model_mapping' ->> 'codex-auto-review' AS auto_review_target,
       count(*) AS account_count
FROM accounts
WHERE deleted_at IS NULL
  AND platform = 'openai'
  AND credentials -> 'model_mapping' ? 'codex-auto-review'
GROUP BY 1
ORDER BY 1;

SELECT id, name, models_list_config -> 'models' AS models
FROM groups
WHERE deleted_at IS NULL
  AND platform = 'openai'
  AND models_list_config -> 'models' ? 'codex-auto-review'
ORDER BY id;

COMMIT;
