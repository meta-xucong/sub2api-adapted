# Runtime Configs

This file records production runtime settings that live in the database or
provider consoles rather than in source code. Do not store secrets here.

## aiself.vip OpenAI Image Smart Router Runtime

- Date updated: 2026-07-07
- Production host: aiself.vip downstream Sub2API VPS
- Runtime config file: `/app/data/config.yaml`
- Host volume path:
  `/var/lib/docker/volumes/deploy_sub2api_data/_data/config.yaml`
- Backup before update:
  `/var/lib/docker/volumes/deploy_sub2api_data/_data/config.yaml.bak_codex_20260707-005106`
- Deployed code commit:
  `f87c82ab` (`Stabilize OpenAI image fallback routing`)
- Goal: keep native OAuth/K12 image accounts preferred while allowing
  lower-priority 404token and aiai image accounts to take over after transient
  native image failures.

### Image Routing Shape

The production image lanes are intentionally layered by priority:

- priority `1`: native OAuth/K12 image-capable accounts, concurrency `2`
- priority `2`: 404token image account, concurrency `1`
- priority `3`: aiai image account, concurrency `4`

All lanes that can serve image edits should expose only image-capable models
and capabilities such as `image_generation` / `image_edit`. Chat-only accounts
should not be mixed into the image lane.

### Smart Router Settings

The active production config values are:

- `gateway.smart_router.enabled=true`
- `gateway.smart_router.top_k=8`
- `gateway.smart_router.max_attempts_image=6`
- `gateway.smart_router.max_attempts_chat=3`
- `gateway.smart_router.max_attempts_default=3`
- `gateway.smart_router.same_source_group_attempts=1`
- `gateway.smart_router.cost_bias_max=3`
- `gateway.image_edit_transient_cooldown_seconds=30`

Scoring weights:

- `priority=0.8`
- `cost=1.0`
- `health=1.2`
- `load=1.0`
- `queue=0.6`
- `latency=0.4`
- `recovery=0.8`

### Incident Note

Before this update, the live Docker volume still had
`gateway.smart_router.max_attempts_image=2`. Complex `/v1/images/edits`
requests could consume both attempts on two native OAuth/K12 accounts after
OpenAI returned a completed response with no image output. The request then
failed with no account available before lower-priority fallback lanes were
selected.

Code commit `f87c82ab` adds a fresh database retry when image selection fails
from a stale scheduler snapshot, so newly available fallback lanes are not
hidden by the in-memory snapshot. It also sanitizes no-output image failover
errors before they enter short cooldown reasons.

Reapply this runtime section after rebuilding or upgrading from upstream
Sub2API, because these values live in production config rather than source.

## Veyra Portal VPS Network And Nginx Tuning

- Date updated: 2026-06-20
- Production host: sub2api VPS
- Goal: improve connection stability and repeat-visit latency without
  restarting the sub2api app container or changing account/data state.

### Nginx Hot-Reload Settings

The production Nginx host config was hot-reloaded after `nginx -t` passed.
Backups were stored under `/root/nginx-config-backups/`.

`/etc/nginx/nginx.conf` now includes:

- `tcp_nodelay on`
- `keepalive_timeout 65`
- `keepalive_requests 1000`
- gzip coverage includes `application/wasm`

`/etc/nginx/sites-enabled/sub2api.conf` now has a Veyra static asset location:

- matches `/_veyra/*.(js|css|png|jpg|jpeg|webp|ico)`
- forwards to the local sub2api app on `127.0.0.1:8080`
- sets `Cache-Control: public, max-age=600`
- deliberately does not cache Veyra HTML or `/_veyra/return`

This reduces repeated fetches of the portal JS/CSS while keeping login-return
state and homepage HTML fresh.

### TCP Congestion Control

The host kernel supports BBR after loading `tcp_bbr`.

Hot-applied and persisted:

- `/etc/modules-load.d/veyra-tcp-bbr.conf`
  - `tcp_bbr`
- `/etc/sysctl.d/99-veyra-network.conf`
  - `net.core.default_qdisc = fq`
  - `net.ipv4.tcp_congestion_control = bbr`

Verification after applying:

- `sysctl net.ipv4.tcp_congestion_control` -> `bbr`
- `sysctl net.core.default_qdisc` -> `fq`
- `curl http://127.0.0.1:8080/health` -> OK
- `docker inspect sub2api` -> `healthy`, `restart=0`

### DNS Observation

Authoritative DNS for `aiself.vip` is served by `ns1.dyna-ns.net` and
`ns2.dyna-ns.net`; NS/SOA TTL was observed as `300`. Local resolver A-record
TTL can fluctuate and was observed as both `1` and higher values. Do not change
VPS code to solve this; adjust the DNS provider record TTL in the external DNS
panel if repeated resolver checks confirm the authoritative A record is set too
low.

### Blue-Green Deployment Note

The current deploy script force-recreates the single app container after a new
image is built and verified. That is safer than the old manual flow, but it can
still create a short upstream gap because Nginx points directly at
`127.0.0.1:8080`.

Do not blindly run two full `sub2api` containers against the same database as a
blue-green fix until the app's background workers, schedulers, outbox handling,
and migrations are audited for multi-instance safety. A safer future path is:

- add a deploy mode that starts the candidate app on a different host port;
- disable or isolate background workers in the candidate when possible;
- run HTTP health and Veyra portal smoke checks against the candidate port;
- switch Nginx upstream only after the candidate is healthy;
- keep the old app container alive as rollback until post-switch checks pass.

## JP Relay Watchdog

- Date updated: 2026-06-08
- Production host: sub2api VPS
- Source-controlled assets: `deploy/relay/`

The OpenAI OAuth accounts use host-side SOCKS relay lanes through HAProxy:

- `proxy_id=2` -> `172.18.0.1:21081` -> JP relay 1 primary
- `proxy_id=3` -> `172.18.0.1:21082` -> JP relay 2 primary
- `proxy_id=4` -> `172.18.0.1:21083` -> JP relay 3 primary

On 2026-06-08, GPT-5.5 `unexpected EOF` failures aligned with a
`jp-relay-1-tunnel.service` watchdog restart at `22:59:02 CST`. The watchdog
had restarted the tunnel after a single failed functional probe, cutting active
long-lived OpenAI/Codex streams.

The host watchdog was changed to:

- require `FAIL_THRESHOLD=3` consecutive probe failures before considering a
  restart;
- defer restarts while the SOCKS port has active or recent traffic
  (`BUSY_DEFER_THRESHOLD=20`, `RECENT_CONN_SECONDS=45`);
- keep per-tunnel transient state in `/run/jp-relay-watchdog`;
- clear failure state after a healthy probe;
- stop fixed 12-hour tunnel recycling by setting `RuntimeMaxSec=infinity`.

The current source of truth for these files is under `deploy/relay/`. Reapply
those files to the VPS relay layer after rebuilding or reprovisioning the host.

Temporary account scheduling change:

- Account `3` (`197286184@qq.com`) was lowered from `priority=1` to
  `priority=5` on 2026-06-08 to prefer account `1` while account `3` was near
  Codex 7-day quota and bound to the relay lane that had restarted mid-stream.
- A systemd timer on the VPS restores account `3` to `priority=1` at
  `2026-06-11 10:00:00 CST` and enqueues a scheduler outbox refresh.

## Volcengine Ark Free Lab

- Date configured: 2026-06-07
- Production group: `volcengine-ark-free-lab`
- Group id: `6`
- Platform in sub2api: `openai`
- Account type: `apikey`
- Base URL: `https://ark.cn-beijing.volces.com/api/v3`
- Downstream test API key name: `volcengine-ark-free-lab-test`
- Scheduling scope: isolated test group, not merged into the main OpenAI group
- Image generation: disabled
- OpenAI Chat Completions path: force Chat Completions compatibility
- Anthropic Messages path: enabled for Claude Code fallback through the OpenAI
  `/v1/messages` bridge; this path forwards to Ark Responses with a
  provider-specific sanitizer
- RPM limit: `10`

### Model Exposure Policy

Do not expose every model returned by Ark `/api/v3/models`. The endpoint can
list models that the current API key cannot invoke through Chat Completions.
Expose only models that are both listed upstream and verified with the current
key through the intended API path.

### Current Whitelist

These models are exposed through group `models_list_config` and account
`credentials.model_mapping` as 1:1 mappings:

- `doubao-seed-2-0-lite-260215`
- `doubao-seed-2-0-lite-260428`
- `doubao-seed-1-6-lite-251015`
- `doubao-lite-128k-240428`
- `doubao-lite-32k-240428`
- `doubao-lite-4k-240328`
- `deepseek-v3-2-251201`
- `deepseek-v4-flash-260425`
- `deepseek-v4-pro-260425`
- `glm-4-7-251222`

### Verification

On 2026-06-07, the Ark upstream model list returned HTTP 200 with 119 model
ids. The following representative calls succeeded through sub2api
`/v1/chat/completions` using the isolated downstream test key:

- `doubao-seed-2-0-lite-260215`
- `deepseek-v3-2-251201`
- `deepseek-v4-flash-260425`
- `deepseek-v4-pro-260425`
- `glm-4-7-251222`

The sub2api `/v1/models` response for the test key returned the 10 whitelisted
models after refreshing the scheduler/model-list cache for group `6`.

On 2026-06-08, Claude Code compatibility was enabled for group `6`:

- `allow_messages_dispatch=true`
- `messages_dispatch_model_config` maps the 10 whitelisted Ark model ids to
  themselves
- Claude-family fallback aliases map to Ark models:
  - Opus -> `deepseek-v4-pro-260425`
  - Sonnet -> `deepseek-v4-flash-260425`
  - Haiku -> `deepseek-v3-2-251201`

Ark Responses accepts the bridge request only after stripping OpenAI-specific
request fields that Ark rejects:

- `reasoning.summary`
- `text.verbosity`

This sanitizer is source-controlled in
`backend/internal/service/openai_volcengine_ark.go` and is scoped to OpenAI
API-key accounts with `extra.provider=volcengine_ark`.

### Models Not Exposed Yet

These model families appeared in the upstream model list but returned upstream
`404` authorization or missing-endpoint errors with the current API key during
small Chat Completions tests, so they are intentionally not exposed:

- Kimi: `kimi-k2-250905`, `kimi-k2-thinking-251104`
- Qwen: `qwen3-32b-20250429`
- Mistral: `mistral-7b-instruct-v0.2`

Re-test these before adding them to `models_list_config` or `model_mapping`.

### Pricing Caveat

The Ark/Doubao/DeepSeek/GLM models currently trigger
`openai_usage.pricing_missing_record_zero_cost` in production logs. They are
usable, but usage accounting can be zero-cost until model pricing is added or
mapped to a custom pricing rule.

Do not make this group broadly available until pricing behavior is explicitly
accepted or configured.
