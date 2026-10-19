> Current cumulative handoff and validation limits: [Cloud integration acceptance](../cloud-integration-20261019.md). The checkpoint-specific status and evidence below are retained as historical records.

# Veyra PR #12 integration review — 2026-10-19

## Status

**Candidate under repair; not accepted, pushed, merged, enabled, or deployed.**

The user requested independent cloud review of PR #12 and integration only after defects are resolved. This candidate combines the verified PR source with the existing cloud repair checkpoint. Prior PR-local test reports are supplied evidence, not this review's executions.

## Provenance

- Repository: `meta-xucong/sub2api-adapted`
- PR: https://github.com/meta-xucong/sub2api-adapted/pull/12
- PR head: `a9157b962616f7ca1dc07d8b95666edebf8a716b`
- PR base: `2bedb713b6b0b9cf968468decae36732f390574a`
- Verified PR source tree: `bd56732a7a6c9c80e0d9fc3162ecb1fca17a80ef`
- Cloud checkpoint: `079e766020e45d350776a40f91302cd1254973c3`
- Cloud checkpoint tree: `0fee2a5d0b3109ed571015ccd6a80fdee86296da`
- Pre-repair combined staged tree: `5354eff591f06a9b8c180692ec09be39e41d832d`

All 4,414 source blobs, file modes, and 294 trees were independently reconstructed and checked against the fixed GitHub tree. The audit snapshot is a verified source-tree reconstruction, not a transfer of the original Git commit object. All 43 changed files, including binary assets, were applied to an isolated cloud candidate. Reversing that exact PR delta reproduces the cloud checkpoint tree exactly; this preserves SmartRouter, media CSP, H2C compatibility, avatar guards, and other existing cloud repairs.

The primary checkpoint remains immutable during its full regression run. Its receipts do not establish correctness of this combined candidate. The combined source needs fresh receipts with its own identity.

## Scope and compatibility

- Keep optional Veyra feature and portal defaults disabled.
- Preserve existing text/image/video routing and billing; no production flags or credentials are changed by source integration.
- Retain Alchemy desktop root and H5 destinations; preserve intentional Video OS POST handoff.
- Reuse native authentication, eight-place monetary normalization, and existing SQL transactions.
- Do not weaken account ownership filtering to work around mismatched external caller identity.
- Do not replace the cloud tree wholesale with the older PR branch.
- No local Codex, user worktree, VPS, real Provider, real login, or paid request is part of these cloud tests.

## Confirmed repair targets

1. Successful debit deduplication must survive ordinary expiry cleanup and long-lived task recovery.
2. Canonical monetary amount must be identical in validation, fingerprint, SQL mutation, and receipt.
3. A consumed login ticket must never become redeemable again before ticket expiry, including fractional-second TTL boundaries and concurrent exchanges.
4. Portal Video handoff must work under default CSP without relaxing unrelated responses or allowing arbitrary form destinations.
5. Every supported OAuth completion must preserve supported return targets, including Video OS, through the strict backend callback navigation helper. The legacy stale-SPA fallback remains Alchemy-only; it is not a substitute for correct backend/proxy routing.
6. Mobile redirect must execute under default script CSP without unsafe-inline; mobile login return must preserve its intended action.
7. Internal-token middleware must not mutate captured shared state during concurrent requests.

Per-area audit and implementation documents provide exact code evidence and regression cases. Discovery is not equivalent to execution: new regressions must be run, and browser/SQL claims require their respective runtime evidence.

## Required acceptance evidence

- Candidate source identity before/after each test group; unchanged source or an explicit superseding run.
- Focused bridge, ticket, shared-session, cleanup, monetary, and frontend tests; targeted race tests.
- Real isolated PostgreSQL: same-key replay/concurrency, different-key insufficient combined balance, transaction failure rollback, expiry cleanup followed by replay, decimal boundary behavior.
- Real local Chromium: default security middleware, desktop and mobile return flow, fixed callback POST allowed, untrusted callback blocked, feature-disabled routing preserved. All outside network destinations intercepted; no live credentials.
- Full relevant Go and frontend validation against the integrated version, not the earlier PR or SmartRouter-only checkpoint.
- A final read-only review against the exact accepted candidate and truthful list of environment-gated tests.

## Deployment gates, not cloud source acceptance claims

Enabling the portal changes `/` ownership; `/home` remains the ordinary frontend. Once enabled, existing active authenticated users can request bridge tickets. The shared internal credential authorizes privileged account/usage lookup and debit operations. Deployment requires an explicit trust/access decision and secure credential provisioning, not merely a source merge.

Alchemy and Video receivers must verify ticket intent, account state, and their expected callback/Origin contracts. Their currently deployed versions have not been validated by this PR review. An older inspected Video OS snapshot uses a shared provider API key but asks usage lookup for the logged-in customer; those identities must be reconciled in the current receiver architecture without removing Sub2API's ownership filter. A zero-cost or absent usage record remains fail-closed and is not proof of completed settlement.

No claim of universal compatibility, upstream exactly-once charging, production acceptance, or absence of all defects is made from synthetic tests.
