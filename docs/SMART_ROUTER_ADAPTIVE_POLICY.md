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
| Soft penalty | Adds a temporary in-memory priority shift after recent failures | runtime EWMA |
| Load guard | Applies lane and source-group concurrency limits | scheduler snapshot |
| Adaptive timeout | Chooses a timeout per lane and capability from successful latency samples | bounded memory |
| Budget guard | Clamps each attempt to the remaining request budget | request context |
| Failover | Excludes the failed lane/source group for this request | request-local |
| Ledger | Records status, duration, capability, and sanitized error summary | bounded memory; usage logs remain durable |
| Calibration hook | Runs text-to-image and image-to-image probes when a lane needs separate validation | scheduled-test integration |

The strategy chain in `backend/internal/smartrouter/core/plugins.go` is the
extension point. A deployment can add or remove a plugin without changing the
account scheduler. Plugins receive metadata only; request bodies, credentials,
cookies, and URLs are never passed to them.

## Ordering rules

1. Reject capability or model mismatches.
2. Reject lanes that are full or excluded for this request.
3. Apply temporary health penalty to the configured priority.
4. Select within the best remaining priority layer using cost, health, load,
   queue, latency, and recovery scores.
5. On failure, move to the next lane or source group within the total budget.
6. Never persist the temporary penalty as `account.priority`.

The current adapter maps a high recent error EWMA to a temporary penalty of
10, 20, or 30. Thus a priority-1 lane can temporarily behave like priority 31,
while its configured value remains 1. Existing short transient cooldowns are
still allowed as a very brief request-storm guard; they expire automatically
and do not delete or permanently disable the account.

## Adaptive timeout algorithm

The key is `(lane_id, capability)`. Generation and edit traffic never share a
latency profile. Each successful attempt contributes to a bounded recent
window. Transient failures and timeouts also enter a failure streak for the
same lane and capability. The next timeout is:

```text
min(max(p95(recent_successes) * multiplier + safety_margin, min_timeout), max_timeout)
```

When a lane has transient failures but no successful samples, the first retry
uses `last_failure_duration * failure_backoff_multiplier`; repeated transient
failures contract it again, subject to `min_timeout`. This prevents a lane
that just consumed a full 180-second attempt from consuming the same budget
repeatedly. Client cancellations and deterministic 4xx/auth failures do not
trigger this backoff.

When the request has a deadline, the result is clamped to the remaining budget.
When there are known fallback attempts, the engine reserves a small minimum
window for each remaining attempt. If the caller does not provide the count,
one reserve window is still kept. With no samples or failures, the configured
default is used, preserving current behavior.

The default feature flag is off for upgrade compatibility. Recommended image
settings for a later controlled deployment are:

```yaml
gateway:
  smart_router:
    enabled: true
    adaptive_timeout:
      enabled: true
      default_seconds: 180
      min_seconds: 30
      max_seconds: 300
      safety_margin_seconds: 20
      multiplier: 1.25
      failure_backoff_multiplier: 0.5
      window_size: 32
      reserve_seconds: 30
```

This does not replace `gateway.image_request_timeout_seconds` (the total
request budget). It only decides how long to wait for the current upstream
attempt.

## Calibration and ledger policy

The durable usage log is the calibration source. A daily 04:00 Asia/Shanghai
job should first inspect the last 24 hours by lane and capability:

- stable generation and edit behavior: run one generation probe;
- different generation/edit behavior or recent edit failures: run both probes;
- no recent evidence or a newly added lane: run both probes;
- success restores the normal configured priority;
- failure keeps the lane active but applies a temporary penalty and retries at
  the next calibration window.

The runtime ledger is intentionally bounded and contains only operational
metadata. It is useful for immediate decisions and debugging; it is not a
replacement for durable usage history.

## Compatibility and rollout

- Existing `gateway.smart_router` settings remain valid.
- With `adaptive_timeout.enabled: false`, timeout behavior remains the existing
  fixed per-upstream timeout.
- The core module is capability-neutral. The adaptive timeout integration is
  currently wired to image generation and image edit; chat/responses retain
  the existing fixed timeout until a separate capability policy is enabled.
- No account group, billing model, upstream URL, or client URL is changed.
