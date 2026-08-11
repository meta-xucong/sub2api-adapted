# Sub2API Smart Router Adaptive Policy

## Goal

Keep the configured price order while avoiding repeated calls to a flapping
upstream. A failed lane is temporarily deprioritized, not permanently
disabled. A later scheduled probe can restore its normal position when it is
healthy again.

The policy is provider-neutral. Only the Sub2API adapter knows about accounts,
groups, database fields, and upstream-specific image adapters.

## Modules

| Module | Responsibility | State |
| --- | --- | --- |
| Capability filter | Separates chat, responses, image generation, and image edit | lane metadata |
| Priority layer | Keeps the lowest numeric configured priority first | account priority |
| Soft penalty | Assigns a temporary sequential recovery priority after recent failures | runtime ledger |
| Load guard | Applies lane and source-group concurrency limits | scheduler snapshot |
| Adaptive timeout | Chooses a timeout per lane and capability from successful latency samples | bounded memory |
| Budget guard | Clamps each attempt to the remaining request budget | request context |
| Failover | Excludes the failed lane/source group for this request | request-local |
| Ledger | Records status, duration, capability, and sanitized error summary | bounded memory; usage logs remain durable |
| Auto-enrollment | Detects newly added or changed lanes, probes declared capabilities, and activates healthy lanes | account-change event + periodic reconciliation |
| Calibration hook | Runs text-to-image and image-to-image probes when a lane needs separate validation | scheduled-test integration |

The strategy chain in `backend/internal/smartrouter/core/plugins.go` is the
extension point. A deployment can add or remove a plugin without changing the
account scheduler. Plugins receive metadata only; request bodies, credentials,
cookies, and URLs are never passed to them.

## Automatic enrollment of new lines

Adding a new account or upstream line must not require a Codex code change or a
new Smart Router rule. The account scheduler should discover it from the normal
Sub2API account/group data, while an enrollment worker handles verification:

1. Detect an inserted account, a changed base URL, model mapping, capability
   declaration, group, or enabled state. An event trigger is preferred; a
   periodic reconciliation scan is the fallback.
2. Normalize and validate the base URL, group membership, model mapping,
   source group, and declared capabilities.
3. Put the lane in `pending_verification` and run only the probes required by
   its declared capabilities. New image lines run generation; lines whose
   history or declaration distinguishes edit behavior run both generation and
   edit. Chat/Responses and compact are probed independently.
4. On a valid probe result, mark that capability `ready`, clear any old
   Smart Router penalty, and use the saved configured price priority as its
   effective priority.
5. On a transient failure, mark that capability `degraded`, assign the next
   `30/31/32...` recovery priority, and retain the lane as a low-priority
   fallback. Do not permanently disable the account.
6. On a deterministic configuration, authentication, or manual-disable result,
   record the reason and do not automatically promote the lane until the
   account is changed or manually re-enabled.

The normal user workflow is therefore: add the line in Sub2API, set its group,
model mapping, and configured price priority, then enable it. Smart Router
automatically tests it; once it passes, it participates at the configured
priority. The operator does not edit router code, add a hard-coded URL, or
manually rebuild a routing table for each new upstream.

## Ordering rules

1. Reject capability or model mismatches.
2. Reject lanes that are full or excluded for this request.
3. Apply the lane's temporary recovery priority without changing the configured price priority.
4. Select within the best remaining priority layer using cost, health, load,
   queue, latency, and recovery scores.
5. On failure, move to the next lane or source group within the total budget.
6. Never persist the temporary penalty as `account.priority`.

The recovery priority is sequential within the same group and capability lane:

```text
healthy: configured priority (for example 1, 2, 3)
first newly demoted lane: 30
second newly demoted lane: 31
third newly demoted lane: 32
...
```

The next newly demoted lane receives the next free recovery slot. A successful
04:00 probe releases that slot and restores the lane's saved configured
priority. The configured price priority is never overwritten. Image generation,
image edit, ordinary chat/Responses, and compact maintain separate recovery
slots so a failure in one capability cannot reorder another capability.

Existing short transient cooldowns are still allowed as a very brief
request-storm guard; they expire automatically and do not delete or permanently
disable the account. A cooldown is not a substitute for the recovery priority.

## Adaptive timeout algorithm

The previous failure-backoff algorithm in this document is retired. It made a
transient failure shorten the next image attempt and could collapse a lane to
45 seconds. The current image policy is specified in
`SMART_ROUTER_IMAGE_TIMEOUT_POLICY.md`.

The active rules are intentionally small:

- no successful history: wait 180 seconds;
- each consecutive success subtracts 10 seconds;
- a transient timeout/transport/5xx/temporary-403/429 resets the next attempt
  to 180 seconds;
- the floor is `max(successful_EWMA + 30 seconds, 60 seconds)`;
- deterministic 4xx, capability, authentication, balance, and client-cancel
  outcomes do not modify the timeout profile;
- the 600-second image request budget and one finalization reserve still cap
  the whole failover chain.

The old behavior is retained only as a historical record in
`SMART_ROUTER_IMAGE_RESILIENCE_45S_POLICY_RETIRED.md`; it must not be used for
new deployments.

## Calibration and ledger policy

The durable usage log is the calibration source. A daily 04:00 Asia/Shanghai
job should first inspect the last 24 hours by lane and capability:

- stable generation and edit behavior: run one generation probe;
- different generation/edit behavior or recent edit failures: run both probes;
- no recent evidence or a newly added lane: run both probes;
- for ordinary traffic, probe the protocol actually used (`responses` and
  `chat/completions` are separate capabilities);
- for compact, call `/responses/compact` and require valid `encrypted_content`;
- success restores that capability lane's saved configured priority and clears
  its recovery slot;
- failure keeps the lane available at its current recovery priority and retries
  at the next calibration window;
- manually disabled, deleted, credential-invalid, or deterministic
  configuration-error lanes are skipped and are not automatically reopened.

The runtime ledger is intentionally bounded and contains only operational
metadata. It is useful for immediate decisions and debugging; it is not a
replacement for durable usage history.

## Compatibility and rollout

- Existing `gateway.smart_router` settings remain valid.
- With `adaptive_timeout.enabled: false`, timeout behavior remains the existing
  fixed per-upstream timeout.
- The core module is capability-neutral. Soft demotion and 04:00 recovery apply
  to every enabled capability lane; adaptive timeout remains separately
  opt-in per capability, and the current controlled rollout is limited to
  image generation and image edit.
- No account group, billing model, upstream URL, or client URL is changed.
