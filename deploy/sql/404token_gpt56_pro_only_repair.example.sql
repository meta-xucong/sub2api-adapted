-- Replay template for the 404token GPT-5.6 Pro-only policy.
-- Back up PostgreSQL before applying. This template is deployment-specific.
-- It never changes account_groups: billing-group membership is preserved.

BEGIN;

-- aicodexvip has no available balance. Keep its accounts and mappings for
-- later recovery, but prevent them from being selected in the live scheduler.
UPDATE accounts
SET schedulable = false,
    updated_at = now()
WHERE platform = 'openai'
  AND credentials->>'base_url' ILIKE 'https://ai.aicodexvip.top%';

-- Do not keep probing an exhausted source every five minutes. The plan is
-- disabled rather than deleted so it can be re-enabled after replenishment.
UPDATE scheduled_test_plans
SET enabled = false,
    updated_at = now()
WHERE account_id IN (
    SELECT id
    FROM accounts
    WHERE platform = 'openai'
      AND credentials->>'base_url' ILIKE 'https://ai.aicodexvip.top%'
);

-- Liuyun remains a chat fallback, but its GPT-5.6 behavior was not verified.
-- Keep the lane conservative and cap the account at one concurrent request.
UPDATE accounts
SET concurrency = 1,
    credentials = jsonb_set(
        COALESCE(credentials, '{}'::jsonb),
        '{model_mapping}',
        '{"gpt-5.4":"gpt-5.4","gpt-5.5":"gpt-5.5","gpt-5.4-mini":"gpt-5.4-mini"}'::jsonb,
        true
    ),
    updated_at = now()
WHERE platform = 'openai'
  AND credentials->>'base_url' ILIKE 'https://www.chenhijsdjklasdjkapsojdoa.asia%';

-- 7646881 Pro is the verified GPT-5.6-Sol lane. Keep it in its existing
-- Pro-priced groups and protect the source from a second simultaneous call.
UPDATE accounts
SET schedulable = true,
    concurrency = 1,
    credentials = jsonb_set(
        COALESCE(credentials, '{}'::jsonb),
        '{model_mapping}',
        '{"gpt-5.4":"gpt-5.4","gpt-5.5":"gpt-5.5","gpt-5.6-sol":"gpt-5.6-sol","gpt-5.4-mini":"gpt-5.4-mini"}'::jsonb,
        true
    ),
    updated_at = now()
WHERE platform = 'openai'
  AND credentials->>'base_url' ILIKE 'https://7646881.cloud%';

-- Cheaper/non-Pro groups do not advertise GPT-5.6. Users must switch to a
-- Pro group when a verified GPT-5.6 lane is required. Preserve other JSON
-- settings, including the group's enabled flag.
UPDATE groups
SET models_list_config = jsonb_set(
        COALESCE(models_list_config, '{}'::jsonb),
        '{models}',
        to_jsonb(ARRAY['gpt-5.4-mini','gpt-5.4','gpt-5.5']::text[]),
        true
    ),
    updated_at = now()
WHERE id IN (2, 5, 9);

UPDATE groups
SET models_list_config = jsonb_set(
        COALESCE(models_list_config, '{}'::jsonb),
        '{models}',
        to_jsonb(ARRAY['gpt-5.4-mini','gpt-5.4','gpt-5.5','gpt-image-2']::text[]),
        true
    ),
    updated_at = now()
WHERE id IN (7, 10);

-- Only the existing 7646881/Pro billing groups expose GPT-5.6-Sol.
UPDATE groups
SET models_list_config = jsonb_set(
        COALESCE(models_list_config, '{}'::jsonb),
        '{models}',
        to_jsonb(ARRAY['gpt-5.4-mini','gpt-5.4','gpt-5.5','gpt-5.6-sol']::text[]),
        true
    ),
    updated_at = now()
WHERE id IN (4, 8);

COMMIT;
