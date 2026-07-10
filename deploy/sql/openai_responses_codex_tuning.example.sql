-- Replay template for Codex/OpenAI Responses chain tuning.
--
-- 1) Run the SELECT backups first and save the results somewhere durable.
-- 2) Replace the example account ids with the ids from the target VPS.
-- 3) Apply only the sections that match the role of that VPS.

-- Downstream backup examples
select id, name, extra::text
from accounts
where id in (22, 23)
order by id;

select id, key, value::text
from settings
where key = 'stream_timeout_settings';

-- Upstream backup examples
select id, name, concurrency, credentials::text, extra::text
from accounts
where id in (4, 5, 6)
order by id;

select id, key, value::text
from settings
where key in ('rate_limit_429_cooldown_settings', 'stream_timeout_settings')
order by key;

-- Enable OpenAI Responses WSv2 for downstream chat lanes.
update accounts
set extra = jsonb_set(
              jsonb_set(
                coalesce(extra, '{}'::jsonb),
                '{openai_apikey_responses_websockets_v2_mode}',
                to_jsonb('ctx_pool'::text),
                true
              ),
              '{openai_apikey_responses_websockets_v2_enabled}',
              'true'::jsonb,
              true
          ),
    updated_at = now()
where id in (22, 23);

-- Enable OpenAI Responses WSv2 for upstream primary/failover lanes.
update accounts
set extra = jsonb_set(
              jsonb_set(
                coalesce(extra, '{}'::jsonb),
                '{openai_apikey_responses_websockets_v2_mode}',
                to_jsonb('ctx_pool'::text),
                true
              ),
              '{openai_apikey_responses_websockets_v2_enabled}',
              'true'::jsonb,
              true
          ),
    updated_at = now()
where id in (4, 5, 6);

-- Same-source upstream guard rails for Codex-heavy traffic.
update accounts
set concurrency = case id
        when 4 then 2
        when 5 then 2
        when 6 then 1
        else concurrency
    end,
    credentials = jsonb_set(
        jsonb_set(
            coalesce(credentials, '{}'::jsonb),
            '{temp_unschedulable_enabled}',
            'true'::jsonb,
            true
        ),
        '{temp_unschedulable_rules}',
        jsonb_build_array(
            jsonb_build_object(
                'error_code', 502,
                'keywords', jsonb_build_array('temporarily unavailable', 'request failed', 'openai_error', 'concurrency limit exceeded'),
                'duration_minutes', 3,
                'description', 'codex upstream 502 cooldown'
            )
        ),
        true
    ),
    updated_at = now()
where id in (4, 5, 6);

insert into settings (key, value, updated_at)
values (
    'rate_limit_429_cooldown_settings',
    '{"enabled":true,"cooldown_seconds":60}'::jsonb,
    now()
)
on conflict (key) do update
set value = excluded.value,
    updated_at = excluded.updated_at;

-- Recommended chained-stream timeout policy for the downstream edge.
update settings
set value = jsonb_build_object(
        'enabled', true,
        'action', 'temp_unsched',
        'temp_unsched_minutes', 5,
        'threshold_count', 3,
        'threshold_window_minutes', 10
    ),
    updated_at = now()
where key = 'stream_timeout_settings';

-- Verify
select
  id,
  name,
  concurrency,
  coalesce(extra->>'openai_apikey_responses_websockets_v2_mode', '') as ws_mode,
  coalesce(extra->>'openai_apikey_responses_websockets_v2_enabled', '') as ws_enabled,
  coalesce(credentials->>'temp_unschedulable_enabled', '') as temp_unsched_enabled,
  coalesce(credentials->'temp_unschedulable_rules', '[]'::jsonb) as temp_unsched_rules
from accounts
where id in (4, 5, 6, 22, 23)
order by id;

select id, key, value::text
from settings
where key in ('rate_limit_429_cooldown_settings', 'stream_timeout_settings')
order by key;
