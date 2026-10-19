# Parallel image timeout branch: read-only scope reconciliation

Observed 2026-10-18 14:12 UTC. This review made only GitHub reads and local source reads. No fetch, merge, cherry-pick, ref change, CI dispatch/rerun, or frozen-repository edit was performed.

## Acceptance boundary

- Frozen cloud candidate: `ef5187a2f252302c80ea461b0df2060a0104b937`, branch `dot/gateway-reaudit-20261018`. Working tree was clean before review.
- Separate GitHub branch: `codex/image-poll-timeout-failover-20261018` points to `9c638df774c22252ed1b7d25a9dcf9e930fdfe8c`.
- That commit's sole parent is `37a5d260abbcddb3e40749cfcd1a158fa82c0330`; local merge-base confirms this parent is an ancestor of the frozen cloud candidate.
- GitHub `main` still points to `2bedb713b6b0b9cf968468decae36732f390574a` at observation time.
- The optional image-poll-timeout feature is **excluded from cloud acceptance**: frozen-source search finds neither its config key, new helper, nor special failure reason. It must not be described as already incorporated or tested by cloud evidence. No conclusion about the author's intent is made.

## Verified separate feature

Commit `9c638df7` changes 10 files, with 346 additions and 2 deletions:

- Adds `gateway.image_poll_timeout_failover_enabled`, default `false`, separately from the existing `failover_on_400` option.
- When enabled, recognizes actual upstream HTTP 400 with valid JSON and exact trimmed code `image_poll_timeout` at `error.code`, `response.error.code`, or `code`.
- Inserts the exception in `handleOpenAIImagesErrorResponse` after admin passthrough, account status-code handling, and OAuth main-model error gates. It returns `UpstreamFailoverError` with reason `openai_image_poll_timeout` and retains upstream HTTP status 400.
- Uses the existing image failover loop and transient cooldown for both generation and edit capabilities, while classifying this special error as timeout only in image Smart Router reporting. Its sanitized cooldown/ops reason uses the fixed code rather than an upstream message.
- The new special exception does not classify HTTP-200 SSE/model-text errors or JSON embedded only inside `error.message`. This describes the new classifier; it does not remove any independently existing retry logic for those response paths.
- Adds classifier/gate/cooldown tests, an API-key handler two-account exhaustion test, default-config checks, and an image-only Smart Router classification test. Test presence was verified; no tests were executed in this review.

## Overlap and future manual reconciliation

Five files changed independently since the common parent are shared with the cloud work: `backend/internal/config/config.go`, `backend/internal/handler/openai_images_failover_test.go`, `backend/internal/service/openai_images_responses.go`, `backend/internal/service/smart_router_runtime.go`, and `backend/internal/service/smart_router_runtime_test.go`.

1. **Concrete API mismatch:** the new remote Smart Router test calls `ReportSmartRouterImageResult` and `ReportSmartRouterTextResult` without a context. Cloud candidate requires a leading request context for both methods. Copying that test unchanged into the frozen candidate would not compile. Future integration must adapt the test while retaining cloud cancellation/request-budget guards.
2. **Image error-routing and billing interaction:** the remote exception uses the same failover loop and response handling touched by cloud accounting work. Cloud code preserves metering on terminal no-output errors while maintaining nil-result behavior for failover attempts. The special HTTP-400 path needs fresh combined tests for no double billing, exhausted/successful retries, and distinction from HTTP-200 SSE/model-text errors. There is no demonstrated conflict in the specific insertion hunk; same-file overlap is not proof of a textual merge conflict.
3. **Scheduling/health interaction:** retain cloud request-wide deadline and cancellation checks, no scheduling after budget expiry, image/text health isolation, and ordered persistence. The remote timeout classification must not turn downstream cancellation into provider-health penalties.
4. **Other overlap:** config changes are at different areas (cloud CSP versus remote option); handler tests add scenarios around the same existing failover tests. Both need preservation rather than replacement. Existing cloud concurrency/usage regression tests should accompany the new timeout tests.
5. Any future inclusion is a new code candidate requiring independent review and rerun of affected and aggregate gates. This report does not authorize integration or publication.

## Reported CI failure, verified independently

Run `39134452079` is a push run for **remote commit `9c638df7`**, workflow `.github/workflows/backend-ci.yml`; it is not a run of cloud `ef5187a2`.

- Job `117696010903` (`golangci-lint`) completed with `failure`.
- Logs show Go `1.27.2`, configured golangci-lint `v2.13` resolving to binary `v2.13.2`.
- Three `typecheck` diagnostics arise while importing Go standard-library packages from `backend/internal/pkg/sysutil/restart.go`: the importer reports export-data version **5**, greater than maximum supported **4**, through `internal/cpu` / `internal/goarch`.
- This directly supports a linter/toolchain compatibility failure. It does not establish a defect in the new image code, nor prove that code otherwise passes lint.
- At observation, release helpers, shell, and frontend jobs were successful; backend unit tests were still running and integration tests pending. No complete green-run claim is justified.
- Frozen cloud workflow independently pins `v2.15.0`, with `GOMEMLIMIT=3GiB`, `GOMAXPROCS=2`, `GOFLAGS=-p=1`, and lint arguments `--timeout=45m --concurrency=1 --max-issues-per-linter=0 --max-same-issues=0 ./...`. Its current validation must be assessed from the cloud evidence for its exact SHA, not this unrelated remote CI failure.

## Sources

- [Remote commit and complete file diff](https://github.com/meta-xucong/sub2api-adapted/commit/9c638df774c22252ed1b7d25a9dcf9e930fdfe8c)
- [Remote branch ref read](https://api.github.com/repos/meta-xucong/sub2api-adapted/git/ref/heads/codex/image-poll-timeout-failover-20261018)
- [Main branch read](https://api.github.com/repos/meta-xucong/sub2api-adapted/branches/main)
- [Workflow run](https://github.com/meta-xucong/sub2api-adapted/actions/runs/39134452079)
- [Failed lint job](https://github.com/meta-xucong/sub2api-adapted/actions/runs/39134452079/job/117696010903)
- Local evidence: read-only `git rev-parse`, `git status --short`, `git merge-base`, `git diff 37a5d260 HEAD`, and `git grep` against the frozen candidate; local `.github/workflows/backend-ci.yml` and affected source files.
