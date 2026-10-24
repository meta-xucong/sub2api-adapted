# Empty Responses Recovery Design

Status: implementation authorized for local validation, GitHub publication, and the explicitly requested 404token/AISELF sync after all local gates pass.

## Baseline and scope

- Source baseline: deployed 404token commit `dec44ee42f22f2a38e7bd84884fcdc417b62c11e`; the checked-out source tree is clean and matches the deployed commit tree. Official Sub2API v0.2.13 commit `3040209f205472038c1ba745a1bedd2edd9053b1` is an ancestor.
- Preserve the existing workspace and all unrelated features. The implementation is isolated in a worktree created from `origin/main` at the deployed commit.
- D/I/A: D1 resolved from source and the reported request shape; I2 because the change crosses streaming failover and provider-to-Responses conversion; A2 because it affects retry/accounting and public response semantics.

## Goal

Prevent a completed Responses request with no assistant result from being returned as a successful empty turn, including the observed native OpenAI shape with positive input usage and zero output usage. Also prevent the Anthropic-to-Responses adapter from fabricating an empty `output_text` item when the upstream produced no content.

## Frozen behavior

1. Native OpenAI Responses: before any client output is committed, fail over only for a completed terminal event with no error and no semantic output, when output-token usage is zero. Positive input/cache-input usage alone does not make an empty response successful. A terminal response containing text, refusal, tool invocation, reasoning/encrypted reasoning, compaction, image/search output, or an unknown output item remains on the current success path. Explicit failed, incomplete, cancelled, image-missing, and already-committed streams keep their existing handling.
2. Anthropic-to-Responses: scope handling to the Responses adapter entry points; do not change the shared converter's behavior for Chat Completions, Gemini, or Antigravity. For buffered Responses requests, an empty `end_turn` becomes a retryable upstream failure before writing a response. An explicit empty `refusal` becomes a visible non-success response without rotating accounts, so policy refusals cannot be bypassed. For an already-streaming adapter, emit an explicit failed terminal event for either empty result; do not retry after bytes have been committed. Preserve upstream usage and response identity, and keep ordinary text/refusal content, tools, thinking, and `max_tokens` behavior intact.
3. Do not change usage settlement, routing, timeouts, or any non-Responses endpoint. Existing failover billing semantics remain unchanged; upstream input consumption for a failed attempt is not folded into the eventual successful user usage row by this patch.

## Allowed implementation files

- `backend/internal/service/openai_gateway_response_handling.go`
- `backend/internal/service/openai_gateway_passthrough.go`
- `backend/internal/service/openai_silent_refusal.go`
- `backend/internal/service/openai_gateway_responses_empty_completed_test.go`
- `backend/internal/service/openai_gateway_service_test.go` (adjust the existing output-normalization fixture to retain positive output usage, keeping that test focused on normalization rather than empty-completion recovery)
- `backend/internal/service/gateway_forward_as_responses.go` and its focused tests
- `backend/internal/pkg/apicompat/anthropic_to_responses_response.go` only if an adapter-local opt-in/helper is required; shared default semantics must stay unchanged
- `backend/internal/pkg/apicompat/anthropic_responses_test.go` and focused stream-conversion tests
- This design record

## Acceptance evidence

- Focused tests exercise input-only usage and `output:null` in native and passthrough streaming, empty assistant shells, valid output types, prior semantic output without replay, explicit terminal failures, and image-generation missing-artifact precedence.
- Anthropic Responses tests cover empty end-turn/refusal handling, preserve incomplete `max_tokens`, and retain normal text, tools, thinking, identity, and usage; unrelated Chat/Gemini/Antigravity conversion behavior remains unchanged.
- Run the focused affected Go tests, the complete `apicompat` package, a Linux/amd64 server binary build suitable for a binary-only hot update, `git diff --check`, and independent read-only A2 audit of the frozen diff. Attempt the full affected service package and record environment-only blockers; do not use a full Docker build when a tested Linux binary is the intended update artifact.
- Before any VPS update, verify both targets' running commit/container entrypoint and determine whether binary replacement can avoid rebuilding an image. Verify health and targeted logs after each update.

## Evidence limits

The USB10 raw upstream SSE was not retained. Its logged `first_token_ms` is within 1–4 ms of total duration, which is consistent with a terminal-only stream, but does not prove the exact event sequence. The code change preserves the existing no-replay guard after client output is committed. If an upstream emits and the adapter flushes an empty structural event (for example, an empty `response.output_item.done`) before the terminal completion, the current guard may still prevent failover and the client may observe an empty result. This patch does not stage or retract already-flushed events; that sequence needs retained SSE or a separate staging/replay design before claiming coverage.

## Local verification record

- Focused `service` tests covering native and passthrough input-only usage, `output:null`, terminal refusal/reasoning semantics, prior output/no replay, image-missing precedence, Anthropic empty end-turn/refusal, and `max_tokens`: PASS, exit 0.
- Full `internal/pkg/apicompat` unit package: PASS, exit 0.
- The first Linux/amd64, CGO-disabled build exited 0 but was not a valid hot-update artifact: the build omitted `-tags=embed`, selecting `backend/internal/web/embed_off.go` and excluding the UI. The repo's documented release build first generates `backend/internal/web/dist`, then builds with `-tags embed`.
- `git diff --check`: PASS.
- Full `internal/service` package was run with identical `-json -timeout=5m -tags=unit -count=1` arguments on the patched worktree and baseline `dec44ee42f22`, using the same Go toolchain, Windows environment, and build cache. Both runs exited 1 with the exact same 44 failing test names (`MOD_ONLY=0`, `BASE_ONLY=0`). Failures include localhost listener/connect restrictions (`127.0.0.1 ... socket ... forbidden`) and TypeSafe mock transport tests (`typesafe transport unavailable`). This establishes the full-package failures predate this patch, but the package remains non-green locally and must not be reported as a PASS.

## Runtime update and rollback record (2026-10-23 to 2026-10-24)

- The target commits have different histories but identical source tree `a5e63d342c5f8941127cf8e2394822a25c97e806`; the patched worktree source tree is `ef3768e9a1f502f49bbf141d79614557c65bab54`.
- Built one Linux/amd64, CGO-disabled server binary with version `0.2.12-empty-responses.20261023`; SHA256 `d6aa5f80de5be7756885b16c2b50d5ddb14573cb20258b535384a10d290ed72c`.
- The deployed binary SHA256 was `d6aa5f80de5be7756885b16c2b50d5ddb14573cb20258b535384a10d290ed72c` and measured 118,988,960 bytes. Both preserved baseline binaries measured 130,990,240 bytes; their hashes were `67cfa61ae440a6b4483705e520d9fac108bd74814f68ccbf7a96ae9ca9b55d3a` (404TOKEN) and `574657cbbb94af2f7b47abe79ba6415e8f53aa0d44a70abbc932d681640c9156` (AISELF).
- After the 2026-10-23 binary replacement, `/` returned 404 while `/health` and API routes remained available. The 404TOKEN Nginx `/assets/` fallback also targeted inactive port 8080 and returned 502. The binary-size difference and `embed_off.go` behavior identify the missing `-tags=embed` build tag as the cause; this is a packaging error, not a regression in the empty-response source changes.
- On 2026-10-24, both servers were restored to their preserved baseline binaries and restarted. The patched binary was retained as `/app/sub2api.failed-site-404-20261024` on each host. 404TOKEN health and public `/` returned 200 after restoration. AISELF health, `https://aiself.vip/`, and `https://aiself.vip/login` returned 200; requests by bare IP are rejected by its configured default Nginx vhost.
- The source fix remains in this local/GitHub branch, but the affected binary is no longer running. Before redeploying it, build the frontend and compile with `-tags=embed`, then verify `GET /`, `GET /login`, and a referenced `/assets/*.js` return 200 through both the app and public Nginx route. Do not treat a successful backend-only `go build` as hot-update acceptance.
- The first 404TOKEN replacement attempt hit a case-sensitive comparison between uppercase and lowercase SHA256 strings; its guard restored the original binary. The later replacement was the one that omitted `-tags=embed` and caused the UI regression.
