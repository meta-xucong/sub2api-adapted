# Upstream v0.1.151 Overlay Audit

Audit date: 2026-07-10

Baseline: `Wei-Shaw/sub2api` `v0.1.151` (`deff3123`).

The adapted branch is based on the official v0.1.151 tree. The previous
v0.1.150 overlay was merged into this baseline and the result passed the full
Go test suite before deployment work began.

## Retained custom behavior

- Smart Router core and OpenAI scheduler adapter, disabled by default in the
  source configuration and enabled only on deployments that opt in.
- Priority-layer routing, source-group isolation, adaptive lane concurrency,
  cost/health/load scoring, and separate chat/responses/image/image-edit lanes.
- OpenAI image-edit transient cooldown and bounded fallback scheduling.
- Fresh database fallback when a scheduler snapshot temporarily misses an
  image fallback account.
- AIAI async reference-image adaptation, mask forwarding, task polling, and
  URL-to-base64 normalization.
- Volcengine Ark/Seedream image and multimodal adapters.
- Responses image-intent routing used only for account selection.
- Veyra API/portal code remains available but is disabled by default. The
  ordinary Sub2API root and login pages remain the default generic UI.
- Gemini native model allowlist enforcement, ops alert sample thresholds,
  compact SSE writer lifecycle protection, relay/deployment assets, and
  low-memory build controls.

## Superseded by upstream v0.1.151

The official update is kept intact for:

- Codex originator/final User-Agent pairing.
- User-scoped OpenAI Fast/Flex policy forwarding.
- GPT-5.6 billing and usage integrity fixes.
- `image_gen` namespace normalization.
- Setup-token background refresh coverage.
- All official v0.1.151 model, pricing, identity, and API compatibility work.

No custom patch replaces these upstream paths.

## 404token page policy

404token is a generic Sub2API deployment. Its runtime configuration must keep:

- `veyra.enabled=false`
- `veyra.portal_enabled=false`

With those flags disabled, `/` and `/login` are served by the official
Sub2API frontend. The Veyra portal remains reachable only on deployments that
explicitly enable it and is never used as the generic login page.

## Upgrade safety

- Back up `/opt/sub2api-deploy/data`, `.env`, compose files, and PostgreSQL
  before replacing the image.
- Keep the previous image tag for rollback.
- Apply Smart Router runtime values only after confirming the account lanes in
  the target database; source defaults remain safe and disabled.
- Do not store provider keys, cookies, passwords, or internal tokens in this
  repository.
