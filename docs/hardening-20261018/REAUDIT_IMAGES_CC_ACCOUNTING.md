# Images, Wokey and raw-CC accounting source re-audit

Date: 2026-10-18. Candidate: `2c756fbef09e130c525528600f5420076d8785b2`.
Comparison baseline: `2bedb713b6b0b9cf968468decae36732f390574a`.

This receipt records source inspection and narrow repairs. It is **not a test-execution receipt**. The root audit coordinates fresh execution after source cross-review. All proposed provider interactions are local mocks. No live generation, publication or deployment was performed for this scope.

## 1. Wokey multipart `images` moderation/forwarding mismatch

**Priority: P1. Candidate-introduced, custom Wokey adapter.**

- Trigger: a Wokey image-edit multipart request containing `model`, `prompt`, and scalar `images=["data:image/png;base64,..."]`, without an `image` file.
- The request parser in `backend/internal/service/grok_media.go:301-304` recognizes multipart `image` and `image_url`, but not `images`. `backend/internal/handler/grok_media.go:107,135-140` moderates that parsed projection before forwarding. For the trigger, the projection contains only the prompt.
- Candidate `grok_wokey_image_edit_multipart.go:64-71` preserved/JSON-decoded `images`; its account-specific overlay could subsequently introduce the image. `grok_wokey_image_edit.go:128-132` accepts the resulting singleton array. Thus an image absent from the moderation input could reach the provider.
- Baseline comparison: this scalar-preservation file is new in the candidate. The old normalization dropped this scalar; an images-only multipart request then failed the adapter's missing-source validation. This is a newly reachable mismatch, not a claim that every existing Wokey path bypasses moderation.
- Minimal remedy: reject unsupported multipart `images` with a typed input error before normalization/overlay. Current repair is `grok_wokey_image_edit_multipart.go:51-55`. Preserve single-file plus supported scalar input and the already-normalized JSON image route. Do not expand the shared moderation protocol.
- Source regressions: `TestGrokWokeyMultipartRejectsImagesMissingFromModeration` covers images-only and images-plus-file rejection, typed error and no upstream call; `TestGrokWokeyJSONImagesRemainAuditedAndSupported` preserves the JSON route. `TestWokeyMultipartImagesRejectedBeforeUpstreamWith400` checks real service forwarding plus the handler's error mapper and useful HTTP 400. The latter is not a full routed moderation-pipeline test.
- Independent source cross-review passed before execution.

## 2. Withheld malformed raw-CC frame creates a failed-request charge

**Priority: P1. Candidate-introduced parser-error/accounting interaction.**

- Trigger: raw Chat Completions streaming with a request body at least 64 KiB, enabling existing silent-refusal buffering, and upstream HTTP 200 containing `data: {broken}\n` followed by EOF. No response output is released and no usage is reported.
- Candidate `openai_gateway_chat_completions_raw.go:497-498` returned a nonnil zero-usage result from its newly added parser-error branch, before the no-output guard. The handler submits nonnil failed results for accounting (`openai_chat_completions.go`, `submitChatUsage` and the nonretry error branch).
- Exact monetary path: `openai_gateway_usage.go:801-804` sets `CostInput.RequestCount=1`; `billing_service.go:1518-1519` dispatches configured per-request pricing; `billing_service.go:1749-1776` uses one unit and the configured unit price. A zero-token result therefore still produces a per-request charge. A zero-token-only pricing fixture would miss this issue.
- Baseline comparison: the same invalid buffered payload never released output; baseline raw CC lines 421-436 returned a nil result with pre-output truncation failover. This attempt did not produce a billing row. Do not broaden this finding to all upstream error-only streams: some of those already released bytes and could be billed in the baseline.
- Minimal remedy: in the candidate's parser-error branch, return a nil accounting result only if no client output started and `usage == (OpenAIUsage{})`. Keep the same terminal, nonretry error. Already released partial output or nonzero observed metering still returns a result. No pricing fallback, account-switch or general retry policy is changed.
- Source regressions: `TestGatewayStreamIntegrityRawErrorFrameBoundaries/withheld_invalid_eof` asserts nil result and no failover; its metered-error control preserves 7 input/3 output tokens. `TestOpenAIChatWithheldParserFailureDoesNotBill` exercises a real standard-mode handler/billing service with group per-request and token pricing, mocks the upstream and billing repositories, asserts zero rows/commands for the withheld empty failure, and exactly one charge for metered partial and cancelled metered partial output.
- Fixture units were independently rechecked: `billing_service.go:1424-1437` assigns `ChannelModelPricing.InputPrice/OutputPrice` directly to per-token prices, and `:1663,1671` multiplies by token counts. Fixture prices 0.001 and 0.002 therefore imply `7*0.001 + 3*0.002 = 0.013`; no per-million conversion belongs in this service-layer fixture. The per-request control is 0.25. Production pricing was not modified.
- Independent source cross-review passed before execution.

## 3. Existing zero-image failed-stream token-loss limitation (not repaired)

**Priority: P2 residual. Already present in the comparison baseline.**

- Image stream parsing can accumulate upstream usage before a later failure (`openai_images.go:1102`). However, the API-key forwarding path returns that usage with an error only if `streamCount > 0` (`openai_images.go:793-816`). The OAuth/Responses path likewise returns an accounting result only if `imageCount > 0` (`openai_images_responses.go:1949-1984`). Both conditions are present in baseline `2bedb713`.
- The Images handler also admits failed-result accounting only when `result != nil && result.ImageCount > 0` (`handler/openai_images.go:334`). Thus an observed-token stream failure before any completed image can lose its usage result, even if explicit token pricing is configured.
- This limits the candidate's claim: explicit token pricing preserves metering on results that reach accounting; it does not establish preservation on every failed image attempt.
- No repair was made here. Changing result propagation also interacts with pre-output OAuth/body-error/timeout retry behavior. A future scoped change needs an explicit policy for metered, zero-artifact failure and official/upstream provenance before redesigning those established paths.
- Proposed future regression: emit nonzero usage, then an image-stream error before a completed artifact, and inspect actual billing commands and attempt count under both token and image-unit pricing, for API-key and OAuth/Responses routes. Expected money/retry behavior must first be agreed rather than invented by the test.

## Additional source checks and limits

- Returned Images results use actual strict-string artifact counts and no requested-`n` fallback. `ImageRequest` keeps zero-artifact image requests out of the generic one-request pricing fallback. Explicit token pricing deliberately uses observed tokens instead of image-unit count; see `openai_gateway_usage.go:676-680`.
- Image total-budget checking examines the absolute deadline as well as `ctx.Err()`, so lagging timer delivery does not authorize another attempt. Attempt timeout classification distinguishes an independent attempt timeout from an exhausted request budget. These checks do not establish a whole-handler wall-clock deadline: the budget starts after initial admission waits, and account-slot acquisition still uses the handler request context (`handler/openai_images.go:294`).
- Independent cross-review of the subsequent raw choice-state repair passed: use final outbound request `n` (default one) to bound observed indexes, reject invalid indexes before map allocation and before advancing buffered-output release detection, update unfinished count incrementally, and preserve observed usage before rejection. No arbitrary provider maximum or eager allocation was introduced. Existing completion policy concerns observed choices, not every requested slot; the repair intentionally does not redesign it.
- The raw index-error tests include unmetered withheld failure (nil result, no bytes, no retry) and metered failure (7/3 tokens retained). This ordering is important to avoid making a newly rejected buffered frame itself release output and become billable.

Line references above identify the inspected source revision or explicitly named candidate/baseline. Later edits can shift working-tree line numbers. This receipt does not attribute all surrounding code to official upstream: baseline presence alone establishes pre-existence in this fork, not upstream ownership.
