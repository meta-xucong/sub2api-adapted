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

## GPT-5.6 admin test discovery

For OpenAI accounts whose explicit mapping contains GPT-5 chat models, the
admin account-test and scheduled-test model endpoint also exposes the
registered `gpt-5.6-sol`, `gpt-5.6-terra`, and `gpt-5.6-luna` entries. This is
test discovery only: it does not write `model_mapping`, change gateway
scheduling eligibility, or add the models to image-only accounts. It lets an
operator probe a newly supported upstream before enabling it for production
routing.

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
