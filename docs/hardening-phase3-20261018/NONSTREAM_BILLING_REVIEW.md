> 本文件保留该次独立代码审查时点的证据与范围。后续完整执行已完成，最终结果以 [PREUPLOAD_ACCEPTANCE.md](PREUPLOAD_ACCEPTANCE.md) 为准。

# Independent billing/resource/protocol review, extended repair

Initial inspected commit: 1e54bf4f64b000695cc0efed4eebf47aa61e10f1.
Previously tested executable source: a3f0fc956. Phase-three base: ff27dd9c.
Original fork comparison: 2bedb713.

This began as a read-only review. The parent subsequently authorized narrowly repairing the concrete nonstream Images defects below. Only two production files and one new test file are owned by this review. No GitHub, provider, or production actions were taken.

## Findings repaired in the working tree

### P2: Downstream nonstream OAuth Images drops explicit terminal-failure metering

Production path: authenticated POST /v1/images/generations, OAuth account, model gpt-image-1, stream=false or omitted. `buildOpenAIImagesResponsesRequest` still sends upstream stream=true (openai_images_responses.go:385). A complete SSE body containing response.created with usage input_tokens=7/output_tokens=3, then error invalid_request_error/content_policy_violation, reaches `handleOpenAIImagesOAuthNonStreamingResponse`.

In inspected HEAD, lines 1348–1350 parse the usage, line 1362 throws it away, and forwarding line 2002 returns a nil result regardless of the metering. The typed 400 error is terminal, not a failover, so the Images handler records neither row nor charge. The client merely selecting a nonstream response changes accounting for the same upstream stream.

The same complete-body loss exists on direct OAuth model gpt-image-2 with HTTP 200 application/json body:

    {"type":"error","error":{"type":"invalid_request_error","code":"content_policy_violation","message":"blocked"},"usage":{"input_tokens":7,"output_tokens":3}}

`parseCodexDirectImagesResponse` detects the typed 400 before `codexDirectImagesUsage` runs. The same payload framed as a streaming event already retains metering under this phase's policy.

Minimal correction:
- Return usage already parsed by the Responses-to-JSON reader together with terminal errors.
- For direct complete JSON, parse usage only after `gjson.ValidBytes`; preserve it on semantic errors. Invalid JSON cannot create a charge from partially parseable numeric fragments.
- In shared nonstream forwarding, keep attempt-timeout and established error classification first. Classified failover still returns nil; only positive observed metering can accompany terminal errors.
- Keep all image counts, pricing, HTTP>=400 handling, partial-body read semantics, and cross-attempt successful-only accounting unchanged.

Policy provenance: local official merge b22f73e725236790f97d89bf0c3b908a48e591d5 (#5154), implementation bd52e5d770decc26677a6da7eed45dcae45a7d85, explicitly preserves observed stream usage with its error and requires failover results to remain nil. It originally repairs Anthropic; applying its invariant to this concrete Images adapter omission does not introduce a new pricing or retry policy.

### P2: Direct nonstream typed user error returns HTTP 200 with an empty body

For the exact direct JSON above, the reader detects the typed error but writes no response. The forwarding classifier returns nonretry errors unchanged; the Images handler's typed-error branch returns immediately. Inspected HEAD therefore emits an empty success response, including when usage is absent or zero.

Minimal correction: the direct nonstream reader uses the existing `writeOpenAIImagesUpstreamErrorResponse` for typed nonretry semantic failures, matching the Responses nonstream reader. Retryable failures remain unwritten so failover stays available. Normal errors become the existing JSON 400 response. If whitespace keepalive already committed HTTP 200, the helper retains that status and writes a valid JSON error, respecting the existing keepalive tradeoff.

## Regression design

`TestImagesNonStreamingObservedUsageBilling`: 75 subcases, two OAuth paths and token/image/per-request pricing. Metered failure, committed keepalive, isolated input/output/cache read/cache creation, empty/zero usage, failover then success, successful image, partial read, HTTP 400, plus direct invalid JSON controls.

Assertions exercise the actual handler/service/pricing/usage/billing-command chain with synthetic transport and repository doubles. Token fixture 7*0.001+3*0.002=0.013; zero-artifact image/per-request amount stays zero. Failover first reports 99/33 and successful second reports 7/3; precisely one row/charge with the second attempt's values is required. Responses/direct terminal JSON status and body are checked, including keepalive's already-committed 200. Slot release and absence of recovered handler panic are also asserted.

The frozen final red overlays only the two original HEAD production files. All other working-tree changes, including neighboring voice/core work, remain present. It is a controlled two-file regression, not a historical-tree test. Initial exploratory runs were made while tests were being refined and are not the final receipt.

Execution receipt (final frozen owned-file runs):
- Old-source overlay red, 13:16:52–13:20:58Z: exit 1; 42 expected failing subcases and 33 passing controls, no skips. Thirty-six failures demonstrate lost positive usage across both adapters/pricing modes; six direct no/zero-usage cases demonstrate the empty HTTP 200 response defect.
- Current source green, 13:20:58–13:24:50Z: exit 0; four top-level tests and 177 subcases pass, no failures/skips. Includes all 75 new nonstream and 102 existing stream billing cases, plus the subscription-context and async-snapshot controls.
- Before/after SHA256 verification passes for all three owned files. Their Git blobs exactly match final combined candidate ef5187a2f252302c80ea461b0df2060a0104b937. The parent committed that combined candidate while the green command was running; neighboring voice/core/CI work existed during these targeted checks, so this is not presented as the whole-tree final gate for that SHA.
- Exact commands, JSONL hashes, exits, timestamps, original overlay blob provenance, source hashes, and worktree listing are in sibling `images-nonstream/`; `summary.json` is the machine-readable receipt.
- Compilation channel released to the parent at 13:24:54Z. No further source edits or test compilation by this reviewer.

## Other requested review areas

- Three stream=true paths: source-traced the single terminal settlement path and failover exclusions. Newly retained positive cache-creation tokens use the shared positive-field merge. No additional concrete regression found in this scoped pass.
- Async context: the task closure captures request fields before queueing; the reusable Gin context is not referenced by the deferred billing closure. Request context values are carried through the existing wrapper. The alpha-search snapshot regression is included in the targeted green selector.
- Subscription route-pricing context: corrected assignment updates the outer context before return; the stored decision and allowed flag survive. Its regression is included in the green selector.
- Wokey allocation: source-reviewed the encoded-size and decoded-size prechecks, bounded wrapped-base64 decode, and percent-decoding preflight. The previous resource tests/receipts are not represented as freshly run here; no global resource bound is claimed.
- HTTP/2: compared migration with installed x/net v0.60.0 `transport_wrap.go`: HTTP1/HTTP2 protocol selection and ReadIdleTimeout→SendPingTimeout/PingTimeout mapping match the wrapper. Server wrapper remains for independent H1/H2 idle timeout. No new networking test was executed by this review.

No claim of real-provider behavior, live monetary validation, production deployability, or all-repository correctness is made. The parent must freeze and re-run aggregate gates for the new combined candidate.
