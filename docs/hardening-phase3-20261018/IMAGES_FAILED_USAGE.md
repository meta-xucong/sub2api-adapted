# Images failed-stream observed usage

## Proven defect and contract

At baseline `ff27dd9c`, Images readers can observe positive token usage and subsequently fail with no completed image. Both API-key and OAuth forwarders discard that accounting result solely because image count is zero. The handler also returns before submitting zero-image failed results. This is a reachable loss of explicit-token usage, previously documented in `docs/hardening-20261018/REAUDIT_IMAGES_CC_ACCOUNTING.md` section 3.

Upstream merged [PR #5154](https://github.com/Wei-Shaw/sub2api/pull/5154) establishes the relevant existing interrupted-stream contract: retain observed tokens for terminal failures, preserve original error behavior, and keep failover results nil so only the successful attempt settles. That PR directly fixes Anthropic, not Images; applying its invariant to this confirmed Images propagation defect is the scope of this correction. [PR #3215](https://github.com/Wei-Shaw/sub2api/pull/3215) separately anchors actual-artifact image counting. Neither source authorizes charging all failed attempts or inventing image artifacts.

## Minimal correction

- API-key streams retain positive observed metering when a terminal read fails without completed images. Existing pre-output attempt-timeout failover takes precedence.
- OAuth/Responses and OAuth direct Images first run their existing error classification for zero-image failures. A classified failover still returns no accounting result. Other metered terminal errors retain usage and the original error.
- The handler reuses its existing usage submission block for metered zero-artifact terminal failures, then follows the original error, response, and scheduler path. These failures are not reported as successful requests.
- Reuse the existing positive-metering merge helper in the API-key reader so parsed cache-creation usage is not dropped. Existing positive input/output/cache-read/image buckets retain their previous overwrite semantics; no additional schema is inferred.
- Existing `ImageRequest` pricing stays unchanged: zero artifacts cost zero in image-unit/per-request image pricing; explicit token pricing uses measured tokens. Positive image results and model mapping keep existing behavior.
- No cross-attempt aggregation, new durable settlement protocol, provider calls, pricing edits, or retry-policy changes.

## Regression scope

`TestImagesFailedStreamObservedUsageBilling` exercises the actual handler, service forwarding, pricing resolver, usage records and billing commands with local transport/repository doubles. It covers API keys, OAuth Responses and OAuth direct Images with token, image and per-request pricing; metered terminal failure, isolated input/output/cache-read/cache-creation metering, empty/explicit-zero usage, cancellation, request deadline, produced artifact, and failover followed by success. OAuth Responses produces its artifact at completion; API/direct artifact cases retain metering through a later read error. Retry-success checks one settlement with exactly the successful attempt's metering.

Generic Responses usage does not parse an isolated image-cache field; direct Images has a separate supported cached-image breakdown parser. This change does not claim new upstream schema coverage.

The token-price fixture is 0.001 per input token and 0.002 per output token: 7 input + 3 output costs 0.013. The image-unit fixture is 0.25. Zero-artifact image pricing must remain 0. The test examines actual billing-command amounts, not only HTTP status.

Validation receipts are recorded separately after execution. A mock matrix does not certify real provider behavior, live charging, or deployment readiness. Requests that fail over continue to follow upstream's successful-attempt-only accounting; this is intentional, not a claim that every upstream-consumed token across retries is billed locally.

Typed server-error followed by EOF exercises existing failover. In direct Images, a subsequent read error takes precedence before stream finalization; the existing terminal/no-retry behavior is separately tested with observed-usage settlement. This change does not alter that error precedence.

Fixture validation: the final targeted matrix has 102 subcases. An earlier fixture omitted the concurrency cache and recovered a nil-cache panic before provider dispatch; it was corrected using the existing slot-cache fake and real concurrency services, with slot-release and panic-free assertions. An unrelated synthetic read error was removed from the intended typed-failover EOF case and retained as a distinct direct-path control. The three-file original-production overlay fails exactly 66 missing-usage-row assertions while 36 controls pass; this is controlled regression evidence, not a full historical-tree test. Final aggregate gate evidence is recorded separately.

## Non-streaming closure in the final pre-upload candidate

The later review also closes the non-streaming Responses/direct adapter gap for complete HTTP200 responses carrying explicit usage. The existing error classifier remains authoritative; retry/failover still returns nil rather than independently settling a failed attempt. Invalid JSON, partial body reads and HTTP>=400 handling were not redefined. Direct non-retry semantic errors now use the established JSON error response writer, avoiding an empty HTTP200 response; already-committed whitespace keepalive cannot retroactively change its HTTP200 status. The 75 new cases, 102 existing stream cases and negative controls are recorded in PREUPLOAD_ACCEPTANCE.md.
