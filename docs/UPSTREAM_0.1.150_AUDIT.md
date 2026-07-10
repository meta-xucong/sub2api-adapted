# Upstream v0.1.150 overlay audit

Audit date: 2026-07-10

Baseline: `Wei-Shaw/sub2api` `v0.1.150` (`0dec1ad2`, plus the main-branch version sync commit).

The adapted branch is rebuilt from this baseline. Legacy history is not merged into the source tree wholesale.

## Retained and rewritten

- Smart Router core and OpenAI scheduler adapter. It is disabled by default and augments, rather than replaces, the official scheduler.
- Priority-layer routing, source-group isolation, adaptive lane concurrency, cost/health/load scoring, and image-edit capability isolation.
- OpenAI image-edit transient cooldown and one bounded capacity wait before returning `Retry-After`.
- Fresh database fallback when the scheduler snapshot temporarily misses an image fallback account.
- AIAI edit-to-async-generation adapter, task polling, mask forwarding, and URL-to-base64 response normalization.
- Volcengine Ark/Seedream image and multimodal adapters.
- Responses image-intent routing model, used only for account selection; the forwarded text model is unchanged.
- Veyra portal/API overlay and transactional, persistent debit idempotency. The official login page is retained, with only the safe Veyra return redirect added.
- Gemini native endpoint enforcement of the group custom-model allowlist.
- Ops alert minimum request/error sample thresholds. The legacy auto-resolve-on-skip behavior is intentionally omitted because repository/query failures share the same skip signal.
- JP relay, Nginx, SQL recovery, and runtime policy examples.

## Replaced by upstream

- Preferred/latest Codex model lists and retired-model removal. Upstream now owns GPT-5.6 and later model definitions.
- Kimi fallback pricing.
- OpenAI non-stream connection reset and unexpected-EOF failover.
- OpenAI model-not-found fast failover.
- Context-cancelled SLA filtering.
- Sticky binding preservation and configurable sticky TTL.
- OpenAI image no-output detection and retry classification.
- Kimi passthrough detached stream handling.
- General image upload limits and validation.
- OpenAI image rate-limit classification.
- Codex quota window normalization.
- Group/channel mutation auth-cache invalidation. Upstream now invalidates API-key auth snapshots by group, so the legacy request-time `GetByKeyFresh` database fallback is no longer replayed.

## Removed as unsafe or obsolete

- Fixed Kimi `max_tokens=1600` cap: provider behavior and upstream protocol support have changed; the cap truncates valid requests.
- Process-local full image response cache: up to 64 entries at 32 MiB each could approach 2 GiB and amplify OOM risk on small VPS instances.
- Full Veyra replacement of `LoginView.vue`: it caused product branding to leak into generic deployments. The official login UI now remains intact.
- Legacy GPT-5.5-only model policy and old OAuth model sync overrides.
- Old whole-file frontend/payment/chart/i18n overlays that no longer match the current upstream architecture.

## Compatibility rules

- Every custom feature has a default-off switch or an account/provider discriminator.
- Smart Router never mixes capabilities: chat, responses, image generation, image edit, and embeddings are distinct lanes.
- Compact requests keep upstream's supported/unknown tier ordering.
- AIAI and Volcengine request rewriting is limited to explicitly detected provider accounts.
- No deployment secrets are stored in this repository.
