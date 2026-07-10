# Maintained custom overlay

This repository tracks official Sub2API and keeps only the overlays that remain useful after each upstream upgrade. See `UPSTREAM_0.1.150_AUDIT.md` for the current three-way audit.

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

## Veyra

Veyra is disabled by default. `veyra.enabled` enables its API bridge and `veyra.portal_enabled` enables the embedded aiself portal. The ordinary Sub2API login page is never replaced.

Billing debits use the existing `idempotency_records` table and update user balance in the same PostgreSQL transaction.

## Upgrade workflow

1. Fetch `upstream/main` and identify the latest release tag.
2. Start a clean branch from upstream; do not merge the legacy tree into it.
3. Compare each overlay against current upstream behavior and tests.
4. Reapply the maintained overlay commit, resolve against current interfaces, and run `scripts/verify-adapted-overlay.ps1`.
5. Build a uniquely tagged image, back up the database/config, deploy, and keep the previous image tag for rollback.
