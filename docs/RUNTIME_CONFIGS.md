# Production Runtime Configs

This file records verified non-secret settings that live outside the source tree. Never store API keys, passwords, cookies, private keys, or internal tokens here.

## Repository Baseline

- Current official baseline: `Wei-Shaw/sub2api` `v0.1.151` (`deff3123`).
- The production aiself image remains pinned to its last verified image until
  that deployment is deliberately upgraded; repository baseline and deployed
  image are recorded separately to keep rollback decisions explicit.

## aiself.vip

Verified: 2026-07-12

- Deploy directory: `/opt/sub2api/deploy`
- Persistent runtime config: `/app/data/config.yaml`
- Host volume path: `/var/lib/docker/volumes/deploy_sub2api_data/_data/config.yaml`
- Upgrade backup root: `/opt/sub2api/backups/upgrade-v0.1.150-20260710T075612Z`
- Latest image backup: `/opt/sub2api/deploy/backups/smart-router-image-policy-9c600574-20260712T130228Z`
- Deployed image: `sub2api-adapted:v0.1.151-smart-router-image-recovery-9c600574`
- Deployed code commit: `9c600574`

Active Smart Router settings:

- `gateway.smart_router.enabled=true`
- `gateway.smart_router.top_k=8`
- `gateway.smart_router.max_attempts_image=6`
- `gateway.smart_router.max_attempts_chat=3`
- `gateway.smart_router.max_attempts_default=3`
- `gateway.smart_router.same_source_group_attempts=1`
- `gateway.smart_router.cost_bias_max=3`
- `gateway.image_edit_transient_cooldown_seconds=30`
- `gateway.image_upstream_timeout_seconds=180`
- `gateway.image_request_timeout_seconds=600` (shared total budget across image failover attempts)
- `gateway.smart_router.image_total_budget_seconds=600`
- `gateway.smart_router.image_attempt_seconds=180`
- `gateway.smart_router.image_finalization_reserve_seconds=15`
- `gateway.smart_router.recovery.second_failure_cooldown_seconds=600`
- `gateway.smart_router.recovery.sustained_failure_threshold=3`
- `gateway.smart_router.recovery.image_sustained_failure_threshold=2`
- `gateway.smart_router.calibration.enabled=true`, scheduled for `04:00 Asia/Shanghai`
- `gateway.smart_router.calibration.total_budget_seconds=1800`
- `gateway.smart_router.calibration.probe_timeout_seconds=180`

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

### YeToken Capability Lanes

Configured and verified: 2026-07-12.

- The `YeToken` chat accounts retain their existing account and group priorities;
  they are explicitly limited to the `chat` and `responses` capabilities in the
  shared `yetoken-chat` source group, capped at two concurrent requests across
  that upstream.
- The `YeToken` image accounts retain their existing priorities and are limited
  to `image_generation` in the separate `yetoken-image` source group. Both the
  per-lane and source-group ceiling are one concurrent request.
- Image edit is deliberately absent from these two lanes until it is verified
  against their upstream; an image-edit failure or cooldown elsewhere cannot
  alter their text-to-image eligibility.
- Replay after a future official update with
  [`deploy/sql/aiself_yetoken_smart_router_overlay.example.sql`](../deploy/sql/aiself_yetoken_smart_router_overlay.example.sql).

Post-upgrade verification:

- `/health`, `/login`, and `/_veyra/` returned HTTP 200.
- A Codex-style streaming `gpt-5.5` request completed successfully.
- A `gpt-image-2` request failed over from account `88` after upstream HTTP 524 to account `89`, then returned one image with HTTP 200.
- A body-signal compact request failed over after two upstream HTTP 503 responses, succeeded on account `83`, and completed without a recovered panic or container restart.
- After the total image budget patch, a minimal `gpt-image-2` generation returned HTTP 200
  with image data in about 33 seconds; the application remained healthy with restart count `0`.
- After the Smart Router overlay deployment, `/health` returned HTTP 200, `/v1/models`
  returned HTTP 200 with six configured GPT model ids, and a Codex-style `gpt-5.5`
  `/responses` smoke request returned HTTP 200 with a response id.
- Durable Smart Router health ledger deployment: `/health` returned HTTP 200;
  restart count remained `0`; migration `174_smart_router_health_ledger.sql`
  created all four ledger tables; and the application logged its internal
  `0 4 * * * Asia/Shanghai` calibration schedule. No live calibration probe was
  forced during deployment.
- The timestamp write-path repair was then deployed after PostgreSQL rejected
  ledger events with a `timestamp with time zone` versus `text` type mismatch.
  The repaired container returned `/health` HTTP 200 with restart count `0`, and
  its scheduler logged the same daily calibration schedule without ledger-write
  errors.
- The image-only recovery policy and YeToken capability overlay were deployed
  with container restart count `0`. The router again registered its daily
  `0 4 * * * Asia/Shanghai` calibration, with no ledger, panic, or runtime
  errors in its post-restart logs.

Post-build host hygiene:

- Repeated upgrade builds temporarily grew unused BuildKit cache to 11.75 GB and pushed the root filesystem to 93% usage.
- Unused builder cache was pruned while retaining about 1 GB of recent cache. Root filesystem usage returned to 60% with about 13 GB available.
- The running image and `sub2api-adapted:rollback-20260710T075612Z` rollback image were verified present after cleanup.

### GPT-5.6 Chat Lanes

Configured and verified: 2026-07-10

- Backup: `/opt/sub2api/backups/gpt56-config-20260710T115521Z`
- Group `2` (`chatgpt`) exposes `gpt-5.6-sol`, `gpt-5.6-terra`, and `gpt-5.6-luna` alongside the existing models.
- Account `84` (`liuyun plus`) maps all three GPT-5.6 models and remains schedulable.
- Account `83` (`7646881 pro`) maps only `gpt-5.6-sol`, the only GPT-5.6 model that completed direct verification on that line.
- Account `19` (`aicodexvip pro`) maps all three models but remains unschedulable as a prepared cold spare.

Gateway verification with the ordinary user API path:

- `/v1/models` returned all three GPT-5.6 model ids.
- Streaming Responses with `reasoning.effort=xhigh` completed for Sol, Terra, and Luna; all three selected account `84`.
- A body-signal `gpt-5.6-sol` compact request completed on account `84` without a recovered panic or container restart.

Replay after a future official upgrade:

- Use [`deploy/sql/gpt56_chat_lane_overlay.example.sql`](../deploy/sql/gpt56_chat_lane_overlay.example.sql) after a database backup. It is name-based, validates that the expected accounts and group exist exactly once, merges only `credentials.model_mapping`, preserves the current `schedulable` state of the cold spare, and queues scheduler refresh events.
- After direct SQL, restart the application or invalidate the API-key auth cache so `/v1/models` and group permissions do not remain stale. Prefer the normal admin save path when available; it performs the same cache and scheduler notifications.
- The overlay is an aiself-specific replay template. For another Sub2API deployment, change only the names and lane policy after inspecting that deployment; never copy account credentials into the repository.

Repository/deployment boundary:

- The aiself production runtime is pinned to `9c600574` in image
  `sub2api-adapted:v0.1.151-smart-router-image-recovery-9c600574`; later repository commits may
  contain documentation, replay SQL, build limits, or audit metadata only.

## 404token

Verified and upgraded: 2026-07-12. The correct SSH path is through the
Philippines jump host; the target is not reachable through the common direct SSH
ports.

- Deploy directory: `/opt/sub2api-deploy`
- Persistent data mount: `/opt/sub2api-deploy/data` -> `/app/data`
- Official baseline: `v0.1.151` (`deff3123`)
- Deployed image: `sub2api-adapted:v0.1.151-smart-router-b05c986a`
- Deployment backup: `/opt/sub2api-deploy/backups/smart-router-b05c986a-20260712-174643`
- Rollback image tag: `sub2api-adapted:rollback-before-smart-router-b05c986a-20260712-174643`
- Initial upgrade backup: `/opt/sub2api-deploy/backups/upgrade-v0.1.151-20260710`
- Latest compose backup: `/opt/sub2api-deploy/docker-compose.yml.before-gpt56-20260710-222245`
- Latest rollback tag: `sub2api-adapted:rollback-before-gpt56-20260710-222245`
- Aiself dispatch sync backup: `/opt/sub2api-deploy/backups/aiself-dispatch-sync-20260710-230311`
- GPT-5.6 route repair backup: `/opt/sub2api-deploy/backups/gpt56-route-repair-20260711-155223`
- Liuyun GPT-5.6 removal backup: `/opt/sub2api-deploy/backups/gpt56-route-repair-liuyun-off-20260711-155548`
- Billing-group correction backup: `/opt/sub2api-deploy/backups/gpt56-group-policy-correct-20260711-155702`
- aicodex scheduled-probe backup: `/opt/sub2api-deploy/backups/aicodex-scheduled-plan-off-20260711-160500`
- `veyra.enabled=false` and `veyra.portal_enabled=false`; `/` and `/login`
  must remain the official Sub2API default pages.
- Smart Router target values match the verified downstream policy:
  `enabled=true`, `top_k=8`, `max_attempts_image=6`,
  `max_attempts_chat=3`, `max_attempts_default=3`,
  `same_source_group_attempts=1`, `cost_bias_max=3`, and image-edit
  transient cooldown `30` seconds.
- Image gateway budgets are explicit: `image_total_budget_seconds=600`,
  `image_attempt_seconds=180`, and `image_finalization_reserve_seconds=15`.

The live scheduled image probe was returning upstream `403
INSUFFICIENT_BALANCE` before this upgrade. That is an upstream account balance
condition; the new router should classify and isolate it without treating the
generic Sub2API page or the deployment itself as broken.

Post-deploy verification:

- `sub2api`, Postgres, and Redis are healthy; the application restart count is
  `0` after the new container became healthy.
- Local and public `/health` returned HTTP 200.
- The official default `/` and `/login` page policy remains unchanged for
  404token; no aiself/Veyra portal page was introduced.
- Migration `173_allow_cyber_blocked_usage_request_type.sql` was applied.
- The last five minutes of application logs contained no panic, fatal, or
  runtime-error signatures.
- BuildKit cleanup reclaimed about `6.95 GB`; the host returned to about 42%
  disk usage while the deployed and rollback images remained present.

GPT-5.6 test-selector patch deployed: `44eb5aaa`.

- The admin account-test and scheduled-test model endpoint now exposes
  `gpt-5.6-sol`, `gpt-5.6-terra`, and `gpt-5.6-luna` for chat-capable OpenAI
  mappings.
- This deployment did not modify account `model_mapping`, group priorities,
  or scheduling eligibility. The three models can be tested from the admin
  selector before being enabled for live routing.
- The new container was healthy with restart count `0`; Postgres and Redis
  remained healthy and application logs had no panic/fatal/runtime-error
  markers in the final ten-minute check.

Current access evidence: target `141.11.138.220:14161` is reachable through
jump host `141.11.138.152:13226`; `https://404token.xyz/health` returns HTTP
200. Credentials and private keys are intentionally not recorded here.

### Aiself source-aligned routing sync (historical)

Applied: 2026-07-10. Matching was performed by upstream URL plus account role.
This was the initial source-alignment state; the GPT-5.6 policy below is the
authoritative current 404token state. Account health/error states were not
copied across hosts; the database backup above is the rollback point.

### GPT-5.6 Pro-only repair

Applied: 2026-07-11. The aicodexvip source is schedulable-disabled because its
upstream balance is exhausted. Liuyun remains available for 5.4/5.5 but is no
longer a GPT-5.6 lane after its 429/hanging behavior. `7646881-pro` is limited
to one concurrent request and retains `gpt-5.6-sol` in the existing
`chatgpt-7646881` and `chatgpt-pro` groups. No account was moved into the
cheaper `chatgpt-plus` or `chatgpt-特惠` groups. The aicodexvip scheduled image
probe (`scheduled_test_plans.id=1`) is disabled, not deleted, to stop repeated
balance-error noise while preserving a future recovery switch.
