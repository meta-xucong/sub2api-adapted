# Re-audit: queued usage request snapshots

Reviewed candidate `2c756fbef09e130c525528600f5420076d8785b2` against
baseline `2bedb713b6b0b9cf968468decae36732f390574a` on 2026-10-18 UTC.

## Finding and provenance

**P1, preexisting official-origin gap:** usage worker callbacks evaluated
`clientRequestedUsageFields(c, ...)` after queueing. The helper dereferences
`c.Request` to recover a composite/public model alias. Once the handler returns,
Gin can reuse that context for another request. A queued bill can therefore
record the next request's model; requested-model pricing can also select that
other model (`openai_gateway_usage.go`, `BillingModelSourceRequested` branch).

This is not attributed to the candidate's new partial-usage handling. Both the
baseline success callback and official reference have the same hazard:

- [Official helper at b0a02a3](https://github.com/Wei-Shaw/sub2api/blob/b0a02a3304a0a78cdd003ddda1dd23ba02c03084/backend/internal/handler/content_moderation_helper.go#L26-L39), fetched and compared directly; helper file blob `c7e7ff9b0f608e4ef9837639664d3e2d8ba87de9`.
- [Official Chat callback at b0a02a3](https://github.com/Wei-Shaw/sub2api/blob/b0a02a3304a0a78cdd003ddda1dd23ba02c03084/backend/internal/handler/openai_chat_completions.go#L284-L313), fetched and verified to call the helper inside the worker callback.
- [Gin's primary lifecycle guidance](https://gin-gonic.com/en/docs/middleware/goroutines-inside-a-middleware/) explains that original contexts are pooled and cannot be retained for asynchronous use.
- [Gin issue #3931](https://github.com/gin-gonic/gin/issues/3931) demonstrates the matching `c.Request` read versus `ServeHTTP` context-reuse write race. This is framework-level evidence, not a claim of an official Sub2API fix.

## Minimal repair

Compute the existing `ChannelUsageFields` value synchronously before submitting
each of the 12 affected callbacks, then capture that value. Covered entries:
Gateway Messages (two paths), Gateway Chat, Gateway Responses, System One,
Gemini v1beta, Grok audio, OpenAI Chat, Embeddings, Responses, Messages and Images.
No queue, pricing, or whole-context copying behavior is changed. Source
cross-review also found the same hazard in Alpha Search's queued `PricingAt`
lookup. That timestamp is now captured before submission. The official
[Alpha Search callback](https://github.com/Wei-Shaw/sub2api/blob/b0a02a3304a0a78cdd003ddda1dd23ba02c03084/backend/internal/handler/openai_alpha_search.go#L289-L314)
was independently fetched and confirmed identical, so this is also a preexisting
official-origin gap under the same primary Gin evidence.

The Gateway/OpenAI Chat partial-usage tests now occupy the sole worker, run the
handler, replace `c.Request` with a different public-model alias, and only then
release the queued usage task. Assertions require the original requested model
while retaining the existing token-count, exactly-once and cancellation-health
checks. This is a deterministic reproduction of context reuse, not a timing-based
race-detector test. A separate queued Alpha Search regression replaces the Gin
request with a context that counts reads, requiring zero reads from that later
request while retaining one usage record.

## Verification status at handoff

- Source reviewed and formatted with the prepared Go toolchain.
- `git diff --check` passed.
- Tests deliberately not run during the audit-first stage. Existing candidate
  test receipts are not evidence for this additional repair; the parent task must
  run the changed handler regressions and aggregate checks after source review.

## Separate cancellation observation

The candidate's new Chat/Responses request-context and `ClientDisconnect`
guards prevent cancellation-only health observations. No new defect was found
in those guards by this source review.

A preexisting fork-specific Images gap was also confirmed: the handler reported Smart Router
health before distinguishing the shared request deadline from an attempt-owned
timeout. If prior retries consume the shared budget, its expiry can penalize the
last account even though that account did not exhaust its own attempt budget.
The timeout classifier prevents another retry but does not suppress that health
observation. This was present in the baseline. `smart_router_runtime.go` is absent
from the pinned official reference (verified with its GitHub contents API), so the
minimal repair is confined to custom Smart Router production reporting.

`ReportSmartRouterImageResult` now accepts the existing request-budget context and
returns without observing health when `OpenAIImageRequestContextErr` reports
cancellation or expiry, including the existing lagging-timer deadline case. Direct
Images passes the `requestCtx` constructed by `WithOpenAIImageRequestTimeout`;
the Responses image bridge passes `c.Request.Context()`, whose budget is installed
by `applyResponsesImageBridgeRequestTimeout` before forwarding. Calibration remains
on its separate reporter and is unchanged. No upstream deadline or retry behavior
changes.

Deterministic table regressions cover expired shared budgets, pending timer delivery,
caller cancellation, independent attempt timeouts, typed attempt-failover timeouts,
and unchanged calibration penalties. They use already-expired or distant-future
deadlines and do not wait for real image timeouts. Like the queued-usage regressions,
they await the parent's post-review test run.

Embeddings and Compact do not become proven bugs merely
because they lack the new guard: their upstream calls are detached, so provider
outcomes must be distinguished from downstream cancellation before changing them.
