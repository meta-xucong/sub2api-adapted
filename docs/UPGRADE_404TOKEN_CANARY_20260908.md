# 404token Upgrade Canary Record

Date: 2026-09-08

Target: the Philippines `sub2api` deployment (`404token.xyz`).

## Candidate

- Base: official Sub2API v0.2.1.
- Overlays retained: Smart Router, Ark/Doubao, Veyra, KIE/Wokey media, Image Resilience, and the default-off Operator Test Guard.
- Candidate image: `sub2api-adapted:upgrade-v021-merged-20260908`.
- Image ID: `sha256:730b0090a9cf41667722780e0f9d1df2be89cad2275f3aa153c9312e8ad706b1`.

## Safety evidence

- A PostgreSQL custom-format dump, Redis RDB, Compose/config copies, and the old image summary were saved before switching.
- Only the application container was recreated. PostgreSQL and Redis were not recreated, flushed, or migrated as containers.
- The old image `sub2api-adapted:kie-mime-19308bad1-404token` remains available for application rollback.

## Verification

- PostgreSQL migration state advanced from the old schema to `283`, latest file `234_group_codex_models_manifest_config.sql`.
- Key counts remained stable: accounts=23, groups=14, composite_model_routes=0, usage_billing_dedup=21295, smart_router_health_events=8784.
- `https://404token.xyz/health` returned 200; the application stayed `running/healthy`; Redis returned `PONG`.
- POST requests to Chat Completions, Responses, Anthropic Messages, image generations, and image edits reached the authentication layer and returned the expected `API_KEY_REQUIRED` response without a key.
- A 30-second stability check found no panic, fatal, migration, database, or Redis errors.

## Scope limitation

At the initial deployment canary no production API key was used for live upstream calls. Provider-level smoke tests for OpenAI/Responses, Anthropic, DeepSeek/Kimi/智谱, Ark/Doubao, Grok/KIE/Wokey, and Veyra require separately authorized low-quota keys. The follow-up key smoke below adds partial OpenAI evidence; it does not prove every provider or upstream billing path.

## Follow-up key smoke

A user-provided test key was later used without persisting it. The key exposed only OpenAI models. Chat Completions succeeded for `gpt-5.4`, `gpt-5.5`, `gpt-5.6`, `gpt-5.6-sol`, and `gpt-5.6-terra`; Responses succeeded for `gpt-5.5`. `gpt-5.4-mini` returned 502/503, and application logs attributed that to upstream account failures and model-support filtering. The image generation endpoint passed authenticated request validation without starting a generation task. Non-OpenAI providers were not tested because this key did not expose them.

The test key should be revoked or rotated after testing because it was shared in chat.

## aiself follow-up switch

After the user confirmed that `gpt-5.4-mini` is retired upstream and should not block this release, the same candidate was applied to the direct `aiself.vip` deployment.

- A fresh PostgreSQL/Redis/Compose/old-image backup was created before switching.
- Only the application container was recreated; PostgreSQL and Redis were left running.
- The legacy Docker Engine produced a different image ID, but the loaded image had identical layers, architecture, Entrypoint/Cmd, environment, and labels to the candidate.
- Migration reached schema `283` / `234_group_codex_models_manifest_config.sql`; key counts remained accounts=110, groups=10, composite_model_routes=0, usage_billing_dedup=224896, smart_router_health_events=106211.
- The public root, health, and setup endpoints returned 200; Redis returned `PONG`; unauthenticated API POST routes returned the expected 401. The application stayed `running/healthy`.
