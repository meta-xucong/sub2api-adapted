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

No production API key was used for live upstream calls. Provider-level smoke tests for OpenAI/Responses, Anthropic, DeepSeek/Kimi/智谱, Ark/Doubao, Grok/KIE/Wokey, and Veyra require a separately authorized low-quota test key. This record therefore proves deployment, migration, routing, and runtime health—not upstream billing or provider success.
