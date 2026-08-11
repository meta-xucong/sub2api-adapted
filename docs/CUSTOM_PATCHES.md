# Maintained custom overlay

This repository tracks official Sub2API and keeps only the overlays that remain useful after each upstream upgrade. See `UPSTREAM_0.1.151_AUDIT.md` for the current three-way audit. `UPSTREAM_0.1.150_AUDIT.md` is retained as the previous release record.

## GPT-5.6 model compatibility

Design document: `docs/SMART_ROUTER_GPT56_MODEL_COMPATIBILITY.md`.

The runtime keeps the GPT-5.6 aliases independent: bare `gpt-5.6` is a
compatibility alias for `gpt-5.6-sol`, while explicit `gpt-5.6-sol`,
`gpt-5.6-terra`, and `gpt-5.6-luna` remain unchanged. Smart Router health and
compact evidence are keyed by the exact explicit model, so a Terra failure
does not quarantine Luna or Sol.

The replayable template
`deploy/sql/gpt56_chat_model_compatibility.example.sql` is deliberately
conservative. It backs up credentials and only fills an absent bare alias on
accounts that already advertise explicit Sol support. It does not identify
accounts by deployment-specific names, copy credentials or health state, alter
groups, priority, concurrency, schedulability, or image-only lanes. Operators
must validate Terra and Luna independently before adding those explicit
mappings.

## Exact-model Smart Router recovery

Design document: `docs/SMART_ROUTER_EXACT_MODEL_RECOVERY.md`.

Smart Router health is isolated by lane, capability, and exact requested model
for every route, not only streaming GPT routes. Temporary upstream failures
therefore cool `gpt-5.6-luna`, `gpt-image-2`, or any other affected model
without disabling the account or penalizing its other models. Recovery probes
at 04:00 Asia/Shanghai reuse the exact failed model before restoring normal
priority. This keeps transient provider channel outages recoverable without
model-specific hard-coded rules.

## Responses image-generation protocol bridge

Design document: `docs/RESPONSES_IMAGE_GENERATION_BRIDGE_DEVELOPMENT.md`.

This overlay translates only explicit `/v1/responses` requests that
contain `image_generation` when their selected lane supports only
`/v1/images/generations`. It leaves ordinary Responses, compact, and existing
Images API traffic unchanged. The protocol adapter belongs in the OpenAI
gateway layer; Smart Router remains responsible for image lane selection,
health penalties, concurrency, failover budgets, and 04:00 recovery probes.

The implementation is opt-in with `gateway.responses_image_bridge.enabled`,
and additionally requires account `extra.responses_image_mode=images_api`.
Unknown or missing account mode means native/legacy behavior. The bridge uses
the existing Images forwarding service inside an isolated response capture,
then emits a Responses JSON or SSE response only after a complete image result
is available. This preserves failover safety and downstream URL/API-key
compatibility without a second public request or prompt-text heuristics.

## OpenAI model catalog hygiene

Automatic upstream model sync and group `/v1/models` candidates are filtered for
OpenAI accounts before they are shown as selectable/latest models. The filter
removes dated OpenAI snapshots, deprecated `gpt-image-1` / `gpt-image-1.5` /
`chatgpt-image-latest` aliases, old GPT-5.1/5.2 chat/codex aliases, and
fine-tuned model IDs. Current stable aliases such as `gpt-5.6-sol`,
`gpt-5.6-terra`, `gpt-5.6-luna`, `gpt-5.5`, `gpt-5.5-pro`, `gpt-5.4`,
`gpt-5.4-mini`, and `gpt-image-2` remain visible.

The filter is intentionally applied to automatic discovery and advertised model
lists only. Existing manual `model_mapping` entries are not deleted, so unusual
provider-specific aliases can still be called explicitly if an operator chooses
to keep them.

## Wokey Grok video API adapter

Wokey's documented video API differs from xAI in one narrow but important way:
video creation is `POST /v1/videos`, not `POST /v1/videos/generations`. A Grok
API-key account whose Base URL is exactly `https://api.wokey.ai/v1` is therefore
recognized as a Wokey video account. For that account only, Sub2API sends video
creation to `/v1/videos`, preserves `grok-imagine-video-1.5`, and converts the
standard `resolution` field to Wokey's `video_resolution` field. Missing mode
and ratio receive the documented text-video defaults of `text_to_video` and
`16:9`. Native Wokey fields continue to pass through unchanged.

The adapter does not alter any other Grok, OpenAI, image, Responses, compact, or
Smart Router path. Create the account in a separate `grok` group, set its model
mapping to `grok-imagine-video-1.5`, and keep concurrency at `1`. Video status
uses Wokey's compatible `GET /v1/videos/{id}` endpoint; completed assets are
downloaded through Sub2API's authenticated `GET /v1/videos/{id}/content` proxy,
bound to the same upstream account that created the task. The adapter is purpose-
built for Wokey's documented API, not a generic bypass for arbitrary Grok
proxies.

## Codex auto-review model overlay

Codex approval/review traffic can arrive as the synthetic model
`codex-auto-review`. Most third-party OpenAI-compatible upstreams do not expose
that exact model id, so a Sub2API deployment can fail at routing time even
though the selected chat group has healthy GPT lanes. The maintained replay
template is
`deploy/sql/codex_auto_review_model_overlay.example.sql`.

The overlay is configuration-only. It adds `codex-auto-review` to OpenAI chat
groups that already advertise `gpt-5.5`, and maps the synthetic
`codex-auto-review` request to the account's explicit `gpt-5.5` mapping. This is
only a conservative compatibility fallback: the synthetic model does not carry
the user's selected model, so choosing the account's newest GPT-5.6 lane here
would silently change the user's model and can break compact/review traffic.
Explicit `gpt-5.6-sol`, `gpt-5.6-terra`, and `gpt-5.6-luna` requests are not
rewritten by this overlay and keep their own mapping and Smart Router health.
The overlay also deliberately avoids the bare `gpt-5.6` alias because upstream
distributors can reject it while accepting the explicit Sol/Terra/Luna ids.

The former policy selected `gpt-5.6-sol` before `gpt-5.5`; that policy is
retired because it caused a user who selected `gpt-5.5` to observe hidden
review/compact traffic on a GPT-5.6 lane.

The overlay does not change image-only lanes, account priority, group
membership, schedulability, credentials, balances, or concurrency. After direct
SQL, restart the application or invalidate auth/scheduler caches before testing
`/v1/models` and `/v1/responses`.

## Smart Router

### Provider model auto-detection

Smart Router does not use a deployment-specific group name as its routing
boundary. An OpenAI account enters runtime ordering and daily calibration only
when its effective model mapping contains a recognizable GPT/ChatGPT/Codex or
GPT-Image family model. An unmapped API key is accepted only for the native
OpenAI endpoint; other API-key endpoints are excluded until their mapping shows
a GPT-family target. Native OpenAI OAuth accounts use their native OpenAI model
capability as the fallback evidence. Kimi, Volcengine, Claude, and other
non-GPT model families therefore remain on the ordinary scheduler even when a
VPS renames their groups.

Code:

- `backend/internal/smartrouter/core`
- `backend/internal/service/smart_router_adapter.go`
- scheduler hooks in `openai_account_scheduler.go`

Enable with `gateway.smart_router.enabled: true`. Existing accounts work without manual metadata. Optional `account.extra.smart_router` fields can override `source_group`, capabilities, cost multiplier, and concurrency. `image_size_tiers: ["1K", "2K", "4K"]` optionally marks a lane as a specialist for explicit OpenAI Images output sizes: matching specialists are chosen before generic lanes, while generic lanes remain automatic fallbacks when every specialist is unavailable. Implicit image sizes retain the original routing behavior.

The image timeout policy is maintained with the core module. Each image lane
starts at 180s, steps down by 10s after each consecutive success, and never
drops below the successful EWMA latency plus 30s (or 60s, whichever is higher).
Transient failure resets the next attempt to the full 180s window. The policy
never changes stored account priority or `schedulable`; soft demotion, 429
backoff, source-group exclusion, and 04:00 calibration remain independent.

The router first considers the lowest numeric priority layer. It advances only after that layer has no eligible lane. Retries can exclude an entire source group so multiple accounts backed by the same upstream are not hammered repeatedly.

### Image timeout policy

The current implementation is documented in
`SMART_ROUTER_IMAGE_TIMEOUT_POLICY.md`. The previous 45-second failure-backoff
design is explicitly retired in
`SMART_ROUTER_IMAGE_RESILIENCE_45S_POLICY_RETIRED.md` and must not be restored.

### Upstream 429 backoff overlay

Upstream concurrency-style 429 responses are handled by the independent
`SMART_ROUTER_429_BACKOFF_DEVELOPMENT.md` policy. They use bounded exponential
backoff with jitter (`5s -> 10s -> 20s -> 40s` by default), honor a bounded
`Retry-After`, and do not consume the `30/31/32` recovery slots on the first
signal. Quota/rate-limit 429 responses retain the existing longer health
cooldown behavior. A 429 generated by Sub2API's own user concurrency queue is
not an upstream lane signal and is left to that queue.

### Responses compact lane regression overlay

`responses/compact` is a separate Smart Router capability, `responses_compact`,
with its own attempt budget (`gateway.smart_router.max_attempts_compact`, default
`0`, meaning dynamic: try each eligible lane once), health ledger, failure
classification, and 04:00 calibration probe. Compact
requests keep the existing supported/unknown capability tiers, but Smart Router
now ranks lanes inside each tier. This fixes the regression where compact requests
were filtered by the OpenAI scheduler but bypassed Smart Router, causing a failing
lane to look like a model-wide or upstream-wide failure. Compact failures never
change ordinary `responses` health for the same account; client cancellation and
request-specific `400` errors do not penalize a lane.

Compact health is deliberately stricter than ordinary chat health and is keyed
by the exact requested model. `gpt-5.6-sol`, `gpt-5.6-terra`,
`gpt-5.6-luna`, `gpt-5.5`, and `gpt-5.4` do not share one broad `gpt-5`
compact health slot. Any non-client compact lane failure, including upstream
5xx, rate limits, capability errors, protocol failures, and stream
interruptions, is cooled until the next 04:00 Asia/Shanghai calibration on the
first signal. This avoids repeatedly trying a flaky compact lane inside Codex's
context-compression path, where one failure can hide or stall the task. The
account's ordinary `responses` lane and configured priority remain untouched.

The compact probe accepts only a real compaction response with non-empty
`encrypted_content`; an HTTP 2xx response containing only usage or ordinary text is
not marked healthy. A transient probe failure records evidence but does not
permanently disable the account, so the next scheduled calibration can retry it.
The internal calibration probe is explicitly marked as an HTTP client request;
it must never inherit an account's Responses WebSocket setting. This prevents a
WS-enabled API-key account from producing a false compact failure from a WebSocket
404/1011 handshake when its HTTP `/responses/compact` endpoint is usable.

For client-triggered Codex compact requests, a `200 text/event-stream` response is
also insufficient on its own. The gateway must see a terminal
`response.completed` event whose response contains exactly one compaction output
item, or a valid upstream JSON response that can be bridged into that SSE shape.
An empty/comment-only SSE stream, a stream that closes before
`response.completed`, or a malformed completed event is treated as a compact
protocol failure and is fed back into Smart Router failover and compact-lane
health. Ordinary non-compact Responses streams remain unchanged.

### Streaming response interruption penalty

Development document: `docs/SMART_ROUTER_STREAM_FAILURE_DEVELOPMENT.md`.

OpenAI chat, Responses, and compact streams that fail after SSE output has
already started cannot be safely retried on another lane inside the same HTTP
request. Smart Router now classifies these cases as `stream_interrupted` instead
of a generic transient failure. The first interruption gets a heavier health
penalty and longer cooldown; a repeated interruption reaches the existing
sustained-failure quarantine and waits for the next 04:00 Asia/Shanghai
calibration. The account's configured priority, schedulable flag, group
membership, and credentials remain unchanged.

Text streaming health is keyed by exact GPT model rather than one broad GPT-5
bucket. A `gpt-5.6-terra` SSE interruption on 404token therefore demotes only
that lane/model pair; `gpt-5.6-luna`, `gpt-5.5`, and `gpt-5.4` keep their own
health state and can continue to be selected if they are healthy.

This applies generically to upstream messages such as `upstream response
failed`, `stream read error`, `stream data interval timeout`, and `idle timeout
waiting for SSE`. User/client cancellation remains `cancelled` and does not
penalize the lane. Image generation and image edit lanes keep their independent
image health policy.

### Compact candidate enrollment and legacy image-cooldown isolation

Compact production routing and auto-enrollment both treat
`openai_compact_supported=false` as historical health evidence, not as a
permanent capability verdict. Accounts with an existing
`smart_router.capabilities` list such as `["chat", "responses"]` remain
eligible for the independent `responses_compact` lane unless compact is
explicitly disabled with `openai_compact_mode=force_off` or the account is not
a ChatGPT/Responses text lane. A 404/503 probe failure records
`openai_compact_last_status` and a short error summary; it no longer writes a
new permanent `openai_compact_supported=false`. A later successful probe writes
`openai_compact_supported=true` and lets Smart Router restore the lane through
the warming path.

Auto-enrollment and daily calibration probe legacy text lanes unless the
operator explicitly sets `force_off`. The calibration probe expands the account
model mapping into concrete compact models, so `gpt-5.6-sol`, `gpt-5.6-terra`,
`gpt-5.6-luna`, `gpt-5.5`, and `gpt-5.4` get separate evidence instead of
sharing a single broad GPT-5 state.

Compact enrollment is limited to accounts that also declare or infer chat/
Responses capability. Image-only lanes are excluded from compact probes, so an
image endpoint returning 404 cannot poison the compact ledger or make the
chat pool look smaller than it is. Compact selection also refreshes its
candidate set from the current database instead of relying only on a stale
Redis scheduler snapshot, allowing a recovered lane to re-enter immediately.

Older image handlers could persist an account-level temporary cooldown for an
image-only failure. The scheduler now ignores that legacy image cooldown for
ordinary chat/Responses and compact requests, while still honoring it for
image requests. Smart Router's capability-scoped health state remains the
source of image routing penalties; no account priority or `schedulable` flag is
changed.

### Durable image health and 04:00 calibration

The image lane health overlay is durable across container restarts. It records only
safe routing metadata in PostgreSQL: lane/account ids, capability, status class,
health score, cooldown/recovery state, and short error summaries. It never records
prompts, images, API keys, cookies, or credentials.

For each `gpt-image-*` lane and capability independently:

- first transient failure: short cooldown and dynamic priority penalty;
- second consecutive transient failure: longer cooldown;
- third consecutive transient failure: capability-only freeze until the next
  04:00 Asia/Shanghai calibration; the account and its chat/image-edit capability
  stay untouched;
- a successful calibration returns the lane through the existing warming stages,
  then successful production traffic removes the remaining penalty.

The scheduler is an internal application cron (`robfig/cron`) rather than a host
script. It uses the real Sub2API account adapter, proxy, and model mapping, with a
unique database run record to prevent duplicate probes after restarts. At 04:00 it
uses the health ledger to decide whether a lane needs only a text-to-image probe or
also an image-edit probe. When its evidence needs renewal, a stable lane gets the
lightweight generation check; an unknown, failed, or generation/edit-divergent lane
also gets an edit check.

The ledger projection explicitly casts its event timestamp parameters to
`timestamptz`. This is required by PostgreSQL's `CASE` expression inference; without
the casts, a route could continue to fail over in memory while its durable health
event is rejected and the next calibration has no evidence to restore.

`gateway.smart_router.recovery.image_sustained_failure_threshold` optionally
overrides this threshold for `image_generation` and `image_edit` only. It is
zero by default, preserving the generic threshold. A value of `2` freezes a
repeatedly failing image lane at its second consecutive upstream failure while
leaving chat and Responses lanes on the generic policy.

This means Docker's normal `restart: unless-stopped` is the only process supervisor
needed. Do not add a systemd timer that calls an image API independently: it would
bypass the protected account adapter and can duplicate chargeable probes.

## Image compatibility

- `/v1/images/edits` transient `403/408/500/502/503/504` failures receive a short temporary scheduling cooldown.
- AIAI reference images are submitted through its asynchronous generation shape and polled to completion.
- Seedream image edits are translated to Volcengine Ark generation references.
- URL-only image results are normalized to `b64_json` when requested.
- Generic multipart uploads marked `application/octet-stream` are content-sniffed before building data URLs.

- OpenAI image `403` now cools only the model-scoped image capability for
  10 minutes instead of escalating the whole account into `error`.
- Image recovery uses a capability/model-family FIFO overlay. The first three
  consecutive image failures move a lane from base priority `P` to `P+30`;
  each further three-failure round adds another `30`. Slots are global across
  source groups, collisions are resolved FIFO-style, and calibration/success
  releases the overlay without changing the account's configured priority.
- OpenAI images responses that finish without any image output now return a
  failover signal instead of a terminal generic `502`, so any `gpt-image-*`
  account can be skipped temporarily when its upstream task silently produces
  no image.
- The no-image-output failover body is reduced to a sanitized structured
  summary (event types, response status, upstream model, tool model, output
  count, and whether an image call appeared), avoiding raw SSE response ids or
  large upstream bodies in cooldown reasons.
- OpenAI image edit transient account failures (`403`, `408`, `500`, `502`,
  `503`, `504`) now apply a short per-account scheduling cooldown via
  `gateway.image_edit_transient_cooldown_seconds` (default `12`) before
  failover, so a flapping image-to-image fallback account is not immediately
  reselected by the next user request.
- OpenAI image generation transient account failures (`403`, `408`, `500`,
  `502`, `503`, `504`) and structured `upstream_text_reply` image responses
  now apply a separate short per-account scheduling cooldown via
  `gateway.image_generation_transient_cooldown_seconds` (default `30`) before
  failover. The account stays active and is automatically eligible again after
  the cooldown expires; repeated recent failures expand the temporary backoff
  up to `10` minutes, while successful traffic naturally lowers the health
  penalty. Deterministic parameter errors are not cooled down.
- OpenAI-compatible image upstream calls have a bounded request timeout via
  `gateway.image_upstream_timeout_seconds` (default `180`). A stuck API-key or
  OAuth image upstream becomes a failover event before the client-side timeout,
  giving Smart Router time to move to the next eligible lane instead of ending
  as a client `context canceled` with no switch.
- One image request now has a separate total wall-clock budget via
  `gateway.image_request_timeout_seconds` (default `600`). The deadline is
  shared by `/v1/images/*` and image-generation `/v1/responses` requests, so
  each failover attempt receives only the remaining time and the handler stops
  instead of starting another upstream request after the total budget expires.
  The response-path detachment preserves this deadline while retaining its
  existing non-stream client-cancellation behavior.
- OpenAI image API-key transport failures where no HTTP response is received
  (for example SOCKS EOF, TCP/TLS, DNS, or proxy routing errors) now return the
  same `UpstreamFailoverError` used by chat/responses forwarding. This lets the
  image handler fail over to the next compatible lane instead of ending the
  request on the first broken connection attempt.
- OpenAI-compatible image providers may put a `data:image/...;base64,...` value
  in the response `url` field. The image normalizer decodes that inline asset
  locally instead of trying to fetch a `data:` URI over HTTP.
- Image endpoints that receive a structured `upstream_text_reply` 400 now
  fail over to the next compatible image lane instead of returning that lane's
  deterministic text response as the final image error. The generic chat and
  embedding error policy is unchanged.
- Scheduled tests accept `gpt-image-2#edits`, which runs an in-memory
  `/v1/images/edits` probe using a tiny embedded PNG instead of a plain
  text-to-image probe.
- The admin Scheduled Tests panel exposes an `edit probe` option for image
  models so operators can enable `auto_recover` without inserting rows by hand.

## Admin model selector hygiene

The account model whitelist selector builds options from the selected platform
catalog directly, then merges already-selected and upstream-synced custom
models. It does not intersect a platform catalog with a separate global list,
so Antigravity-specific aliases and deployment-specific provider models remain
selectable without leaking unrelated platform models.

The admin account-test and scheduled-test model endpoint now exposes only the
models the account actually advertises through `model_mapping` (or the platform
defaults when no mapping is configured). It no longer injects GPT-5.6 variants
into a GPT-5 text account just because the account looks chat-capable; selecting
a model in the UI should mean the account is eligible to serve that exact
request model.

The deployment-specific replay template for aligning a downstream 404token
instance with the matching aiself lanes is
`deploy/sql/aiself_dispatch_sync_404token.example.sql`. It matches account
names together with normalized upstream URLs, preserves credentials, and does
not copy health/error status across deployments.

`deploy/sql/aiself_yetoken_smart_router_overlay.example.sql` is the aiself
replay for the current YeToken pool. It preserves manual account/group
priorities, keeps chat/Responses and image generation in separate source
groups, limits a single YeToken image lane and the shared image source group to
one concurrent request, and deliberately does not enable image edits until an
upstream proves that capability.

`deploy/sql/404token_smart_router_overlay.example.sql` is the matching
404token replay. It preserves the pricing order configured in the admin UI,
separates its 7646881, Liuyun, YeToken chat pools from image lanes, and applies
same-source limits without enabling a disabled account or copying any aiself
credential, priority, or model-mapping data. The 7646881 and Liuyun price
tiers intentionally receive separate retry fault domains: those tiers coexist
inside the same downstream group and must remain eligible for ordered fallback.

Why the image overlay stays maintained:

- some low-cost image providers intermittently return a provider-side `403`
  wrapped as outward `502` / `503`, and the generic OpenAI auth handler is too
  aggressive for that failure mode;
- generation-only health checks are too weak because these lanes often recover
  text-to-image before image-to-image becomes stable again.
- image-to-image fallback accounts can fail with intermittent `500` while still
  passing a later probe, so they need a short cooling window rather than a
  permanent disable.
- image providers behind local SOCKS/Japan relay or upstream reverse proxies
  can fail before returning an HTTP status; those failures should be treated as
  lane failures and routed onward, not as final user-visible `502` responses.
- web/OAuth image providers can occasionally return a completed response with
  usage metadata but no image payload; that should be treated as an upstream
  lane failure and routed onward, not as a final user-facing error.

The follow-up safety overlay is
`deploy/sql/404token_gpt56_pro_only_repair.example.sql`. It disables the empty
aicodexvip source, removes unstable GPT-5.6 mappings from Liuyun, and leaves
GPT-5.6-Sol only in the existing 7646881/Pro billing groups. It never moves an
account between billing groups. It also disables recurring scheduled probes for
the exhausted aicodexvip accounts without deleting those plans.

## Compact streaming compatibility

Ops response capture always restores the writer that existed before its middleware before returning the capture wrapper to the pool. This prevents compact SSE keepalive wrappers from retaining a released capture writer and panicking when outer access loggers read status or size.

## Compact default pass-through and failure-only routing

Compact is a separate endpoint capability, but it is not a separate priority
system. The gateway forwards the model selected by the user and preserves the
operator's normal account priority for the first attempt. Compact probe results
are telemetry only: unknown and previously failed probes remain eligible by
default so transient errors and new model aliases do not require a code change.
Only an explicit `openai_compact_mode=force_off` is a hard exclusion.

Smart Router is a recovery path: a real transport/5xx/timeout failure, or a
completed response without the required compaction output item, excludes the
failed account for the current request and applies the existing soft health
penalty. Repeated failures can enter the normal sustained-failure cooldown;
the 04:00 Asia/Shanghai calibration is for previously degraded lanes, not a
default gate on every compact request.

The `gateway.openai_compact_model` setting is intentionally empty by default.
It is an explicit provider-specific override, not an automatic downgrade from
newer user-selected models.

## OpenAI fast mode compatibility

Codex fast mode can arrive as `service_tier=priority` on `/v1/responses` or
Responses WebSocket `response.create` frames. Official OpenAI accounts keep that
field unchanged. Third-party OpenAI API-key/upstream accounts with a non-official
`base_url` filter `priority` by default when no explicit fast-policy rule
matches, so providers that reject OpenAI priority tiers still receive a standard
compatible request. Explicit admin fast-policy rules continue to override this
default.

Development document: `docs/OPENAI_FAST_MODE_ADAPTER_DEVELOPMENT.md`.

For Codex and other OpenAI-compatible clients, `/v1/models` now advertises Fast
metadata on OpenAI chat models (`additional_speed_tiers=["fast"]` plus a
`priority` service tier). This lets the client display Fast mode without
requiring every upstream provider to support OpenAI's raw `service_tier` field.
The inbound `fast`/`priority` value is preserved as an internal routing intent
before the Fast Policy strips it for third-party URLs. Smart Router then applies
the hint only to chat and ordinary Responses lanes, increasing the weight of
latency, health, queue, and load while reducing cost bias. Image, embedding,
and compact routes stay isolated.

## Operator test key guard

Local maintenance probes must never spend or attribute traffic to a customer
API key. The 2026-07-22 404token bridge verification exposed the risk: an SSH
operator script selected a normal user key because it matched the image-capable
group, then sent local `curl` smoke requests through `/v1/responses` and
`/v1/images/generations`. This was not a Sub2API self-scheduled task and not
the customer's client traffic; the source was the VPS/jump-host maintenance
path. It still produced usage log rows, so the mechanism is wrong even when the
billing ledger is not debited.

`gateway.operator_test_guard` is an opt-in runtime guard for production hosts.
When enabled, script-like requests from trusted local/operator IPs to gateway
API paths are rejected unless the API key belongs to an admin user and matches
a dedicated ops-test identity. External downstream users, browser panel tests,
ordinary scheduling, and account priorities are unchanged. Enable it only after
creating a dedicated admin-owned smoke-test key, and keep that key out of
customer billing plans.

## Veyra

Veyra is disabled by default. `veyra.enabled` enables its API bridge and `veyra.portal_enabled` enables the embedded aiself portal. The ordinary Sub2API login page is never replaced.

Billing debits use the existing `idempotency_records` table and update user balance in the same PostgreSQL transaction.

## Upgrade workflow

1. Fetch `upstream/main` and identify the latest release tag.
2. Start a clean branch from upstream; do not merge the legacy tree into it.
3. Compare each overlay against current upstream behavior and tests.
4. Reapply the maintained overlay commit, resolve against current interfaces, and run `scripts/verify-adapted-overlay.ps1`.
5. Build a uniquely tagged image, back up the database/config, deploy, and keep the previous image tag for rollback.

On low-memory build hosts, set `--build-arg FRONTEND_NODE_OPTIONS=--max-old-space-size=1536` and build the `frontend-builder` target first. The final build then reuses that stage instead of compiling the frontend concurrently with the Go stage. If generated Ent packages still force heavy swapping, pass `--build-arg BACKEND_GOGC=20 --build-arg BACKEND_GOMEMLIMIT=900MiB`; both backend limits are inert unless explicitly supplied.

The backend build uses a BuildKit Go build-cache mount. Keep BuildKit enabled so small overlay updates can reuse compiled packages.
