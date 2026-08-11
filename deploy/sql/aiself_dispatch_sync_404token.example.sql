-- Replay template for the 404token runtime sync from aiself.
--
-- This file intentionally contains no credentials. Inspect the target account
-- names and base URLs before running it on another deployment. Back up the
-- database first. Health/status fields are deliberately not copied across
-- deployments; only model routing, lane policy, and local capacity settings
-- are replayed.

BEGIN;

UPDATE accounts
SET credentials = jsonb_set(COALESCE(credentials, '{}'::jsonb), '{model_mapping}', '{"gpt-5.4":"gpt-5.4","gpt-5.5":"gpt-5.5","gpt-5.6-sol":"gpt-5.6-sol","gpt-5.4-mini":"gpt-5.4-mini","gpt-5.6-luna":"gpt-5.6-luna","gpt-5.6-terra":"gpt-5.6-terra"}'::jsonb, true), concurrency = 3, priority = 20
WHERE name = 'aicodexvip-pro' AND platform = 'openai' AND credentials->>'base_url' ILIKE 'https://ai.aicodexvip.top%';

UPDATE accounts
SET credentials = jsonb_set(COALESCE(credentials, '{}'::jsonb), '{model_mapping}', '{"gpt-5.4":"gpt-5.4","gpt-5.5":"gpt-5.5","gpt-5.4-mini":"gpt-5.4-mini"}'::jsonb, true), concurrency = 10, priority = 10
WHERE name = 'aicodexvip-plus' AND platform = 'openai' AND credentials->>'base_url' ILIKE 'https://ai.aicodexvip.top%';

UPDATE accounts
SET credentials = jsonb_set(COALESCE(credentials, '{}'::jsonb), '{model_mapping}', '{"gpt-5.4":"gpt-5.4","gpt-5.5":"gpt-5.5","gpt-5.6-sol":"gpt-5.6-sol","gpt-5.4-mini":"gpt-5.4-mini"}'::jsonb, true), concurrency = 10, priority = 19
WHERE name = '7646881-pro' AND platform = 'openai' AND credentials->>'base_url' ILIKE 'https://7646881.cloud%';

UPDATE accounts
SET credentials = jsonb_set(COALESCE(credentials, '{}'::jsonb), '{model_mapping}', '{"gpt-5.4":"gpt-5.4","gpt-5.5":"gpt-5.5","gpt-5.6-sol":"gpt-5.6-sol","gpt-5.4-mini":"gpt-5.4-mini","gpt-5.6-luna":"gpt-5.6-luna","gpt-5.6-terra":"gpt-5.6-terra"}'::jsonb, true), concurrency = 10, priority = 9
WHERE name = '流云AI-plus' AND platform = 'openai' AND credentials->>'base_url' ILIKE 'https://www.chenhijsdjklasdjkapsojdoa.asia%';

UPDATE accounts
SET credentials = jsonb_set(COALESCE(credentials, '{}'::jsonb), '{model_mapping}', '{"gpt-5.4":"gpt-5.4","gpt-5.5":"gpt-5.5","gpt-5.4-mini":"gpt-5.4-mini"}'::jsonb, true), concurrency = 1, priority = 25
WHERE name = '流云AI-pro直连' AND platform = 'openai' AND credentials->>'base_url' ILIKE 'https://www.chenhijsdjklasdjkapsojdoa.asia%';

UPDATE accounts
SET extra = jsonb_set(COALESCE(extra, '{}'::jsonb), '{smart_router}', '{"enabled":true,"lane_id":"image:aicodexvip","base_weight":1.05,"capabilities":["image_generation","image_edit"],"source_group":"image:aicodexvip","cost_multiplier":1,"max_concurrency":1,"source_group_max_concurrency":1}'::jsonb, true), schedulable = true, concurrency = 1, priority = 2
WHERE name = 'aicodexvip生图' AND platform = 'openai' AND credentials->>'base_url' ILIKE 'https://ai.aicodexvip.top%';

UPDATE accounts
SET extra = jsonb_set(COALESCE(extra, '{}'::jsonb), '{smart_router}', '{"enabled":true,"lane_id":"image:7646881","base_weight":1.15,"capabilities":["image_generation","image_edit"],"source_group":"image:7646881","cost_multiplier":1,"max_concurrency":2,"source_group_max_concurrency":2}'::jsonb, true), schedulable = true, concurrency = 2, priority = 2
WHERE name = '7646881-生图' AND platform = 'openai' AND credentials->>'base_url' ILIKE 'https://7646881.cloud%';

UPDATE accounts
SET extra = jsonb_set(COALESCE(extra, '{}'::jsonb), '{smart_router}', '{"enabled":true,"lane_id":"image:liuyun","base_weight":0.85,"capabilities":["image_generation","image_edit"],"source_group":"image:liuyun","cost_multiplier":1.2,"max_concurrency":1,"source_group_max_concurrency":1}'::jsonb, true), schedulable = true, concurrency = 1, priority = 2
WHERE name = '流云AI-生图' AND platform = 'openai' AND credentials->>'base_url' ILIKE 'https://www.chenhijsdjklasdjkapsojdoa.asia%';

UPDATE groups SET models_list_config = '{"models":["gpt-5.4-mini","gpt-5.4","gpt-5.5","gpt-5.6-sol","gpt-5.6-terra","gpt-5.6-luna"],"enabled":false}'::jsonb WHERE name = 'chatgpt-aicodexvip' AND deleted_at IS NULL;
UPDATE groups SET models_list_config = '{"models":["gpt-image-2"],"enabled":false}'::jsonb WHERE name = 'gpt image 2生图专用' AND deleted_at IS NULL;
UPDATE groups SET models_list_config = '{"models":["gpt-5.4-mini","gpt-5.4","gpt-5.5","gpt-5.6-sol"],"enabled":false}'::jsonb WHERE name = 'chatgpt-7646881' AND deleted_at IS NULL;
UPDATE groups SET models_list_config = '{"models":["gpt-5.4-mini","gpt-5.4","gpt-5.5","gpt-5.6-sol","gpt-5.6-terra","gpt-5.6-luna"],"enabled":false}'::jsonb WHERE name = '流云AI' AND deleted_at IS NULL;
UPDATE groups SET models_list_config = '{"models":["gpt-5.4-mini","gpt-5.4","gpt-5.5","gpt-5.6-sol","gpt-5.6-terra","gpt-5.6-luna","gpt-image-2"],"enabled":false}'::jsonb WHERE name = 'chatgpt-plus' AND deleted_at IS NULL;
UPDATE groups SET models_list_config = '{"models":["gpt-5.4-mini","gpt-5.4","gpt-5.5","gpt-5.6-sol"],"enabled":false}'::jsonb WHERE name = 'chatgpt-pro' AND deleted_at IS NULL;
UPDATE groups SET models_list_config = '{"models":["gpt-5.4-mini","gpt-5.4","gpt-5.5"],"enabled":false}'::jsonb WHERE name = 'chatgpt-pro直连兜底' AND deleted_at IS NULL;
UPDATE groups SET models_list_config = '{"models":["gpt-5.4-mini","gpt-5.4","gpt-5.5","gpt-5.6-sol","gpt-5.6-terra","gpt-5.6-luna","gpt-image-2"],"enabled":false}'::jsonb WHERE name = 'chatgpt-特惠（不保证稳定）' AND deleted_at IS NULL;
UPDATE groups SET models_list_config = '{"models":["gpt-image-2"],"enabled":false}'::jsonb WHERE name = 'chatgpt-image2生图' AND deleted_at IS NULL;

COMMIT;
