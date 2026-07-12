# Maintained custom overlay

This repository tracks official Sub2API and keeps only the overlays that remain useful after each upstream upgrade. See `UPSTREAM_0.1.151_AUDIT.md` for the current three-way audit. `UPSTREAM_0.1.150_AUDIT.md` is retained as the previous release record.

## Smart Router

Code:

- `backend/internal/smartrouter/core`
- `backend/internal/service/smart_router_adapter.go`
- scheduler hooks in `openai_account_scheduler.go`

Enable with `gateway.smart_router.enabled: true`. Existing accounts work without manual metadata. Optional `account.extra.smart_router` fields can override `source_group`, capabilities, cost multiplier, and concurrency.

The router first considers the lowest numeric priority layer. It advances only after that layer has no eligible lane. Retries can exclude an entire source group so multiple accounts backed by the same upstream are not hammered repeatedly.

## Image compatibility

- `/v1/images/edits` transient `403/408/500/502/503/504` failures receive a short temporary scheduling cooldown.
- AIAI reference images are submitted through its asynchronous generation shape and polled to completion.
- Seedream image edits are translated to Volcengine Ark generation references.
- URL-only image results are normalized to `b64_json` when requested.
- Generic multipart uploads marked `application/octet-stream` are content-sniffed before building data URLs.

- OpenAI image `403` now cools only the model-scoped image capability for
  10 minutes instead of escalating the whole account into `error`.
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
  the cooldown expires; deterministic parameter errors are not cooled down.
- OpenAI-compatible image upstream calls have a bounded request timeout via
  `gateway.image_upstream_timeout_seconds` (default `180`). A stuck API-key or
  OAuth image upstream becomes a failover event before the client-side timeout,
  giving Smart Router time to move to the next eligible lane instead of ending
  as a client `context canceled` with no switch.
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

## GPT-5.6 admin test discovery

For OpenAI accounts whose explicit mapping contains GPT-5 chat models, the
admin account-test and scheduled-test model endpoint also exposes the
registered `gpt-5.6-sol`, `gpt-5.6-terra`, and `gpt-5.6-luna` entries. This is
test discovery only: it does not write `model_mapping`, change gateway
scheduling eligibility, or add the models to image-only accounts. It lets an
operator probe a newly supported upstream before enabling it for production
routing.

The deployment-specific replay template for aligning a downstream 404token
instance with the matching aiself lanes is
`deploy/sql/aiself_dispatch_sync_404token.example.sql`. It matches account
names together with normalized upstream URLs, preserves credentials, and does
not copy health/error status across deployments.

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
