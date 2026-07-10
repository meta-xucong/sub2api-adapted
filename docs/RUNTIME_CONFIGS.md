# Production Runtime Configs

This file records verified non-secret settings that live outside the source tree. Never store API keys, passwords, cookies, private keys, or internal tokens here.

## aiself.vip

Verified: 2026-07-10

- Deploy directory: `/opt/sub2api/deploy`
- Persistent runtime config: `/app/data/config.yaml`
- Host volume path: `/var/lib/docker/volumes/deploy_sub2api_data/_data/config.yaml`
- Upgrade backup root: `/opt/sub2api/backups/upgrade-v0.1.150-20260710T075612Z`
- Deployed image: `sub2api-adapted:v0.1.150-audited-a5426d14`
- Deployed code commit: `a5426d14`

Active Smart Router settings:

- `gateway.smart_router.enabled=true`
- `gateway.smart_router.top_k=8`
- `gateway.smart_router.max_attempts_image=6`
- `gateway.smart_router.max_attempts_chat=3`
- `gateway.smart_router.max_attempts_default=3`
- `gateway.smart_router.same_source_group_attempts=1`
- `gateway.smart_router.cost_bias_max=3`
- `gateway.image_edit_transient_cooldown_seconds=30`

Active Smart Router scoring weights:

- `priority=0.8`
- `cost=1.0`
- `health=1.2`
- `load=1.0`
- `queue=0.6`
- `latency=0.4`
- `recovery=0.8`

Veyra is enabled through the same persistent config:

- `veyra.enabled=true`
- `veyra.portal_enabled=true`

Account priorities, concurrency, model mappings, and `extra.smart_router` lane metadata live in PostgreSQL. Preserve them with a database backup; do not duplicate credentials or account payloads in this repository.

Post-upgrade verification:

- `/health`, `/login`, and `/_veyra/` returned HTTP 200.
- A Codex-style streaming `gpt-5.5` request completed successfully.
- A `gpt-image-2` request failed over from account `88` after upstream HTTP 524 to account `89`, then returned one image with HTTP 200.
- A body-signal compact request failed over after two upstream HTTP 503 responses, succeeded on account `83`, and completed without a recovered panic or container restart.

Post-build host hygiene:

- Repeated upgrade builds temporarily grew unused BuildKit cache to 11.75 GB and pushed the root filesystem to 93% usage.
- Unused builder cache was pruned while retaining about 1 GB of recent cache. Root filesystem usage returned to 60% with about 13 GB available.
- The running image and `sub2api-adapted:rollback-20260710T075612Z` rollback image were verified present after cleanup.

## 404token

The 404token deployment was intentionally not changed or re-audited during the 2026-07-10 aiself upgrade. Re-read its live compose, persistent config, and database before reusing historical runtime values.
