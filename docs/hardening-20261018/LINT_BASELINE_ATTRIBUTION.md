# Backend lint: static baseline attribution

## Compared sources and limits

- Fork baseline: `2bedb713b6b0b9cf968468decae36732f390574a`.
- Official comparison: `Wei-Shaw/sub2api@b0a02a3304a0a78cdd003ddda1dd23ba02c03084`, locally `refs/remotes/upstream-audit/main`.
- Diagnostic sources under `sub2api-audit-delivery/final-backend/`: `lint-before-four-fixes.log` (first capped sample), `lint.log` (post-four-fix capped sample), `lint-uncapped.log` (first complete inventory), `acceptance-lint-uncapped.log` (post-correction inventory), and `delivery-lint-uncapped.log` (final delivery inventory), all produced with golangci-lint 2.15.0 after resolving the earlier toolchain incompatibility.

This is **static source attribution**, not a baseline lint rerun. Reported statements were compared with both Git trees; changed files and relevant references were checked separately. A statement already present upstream does not establish that upstream would emit the same diagnostic. No baseline or official lint execution is claimed.

## First capped sample

The first log reports **59 capped diagnostic entries**, not the full inventory: 4 introduced by this patch and 55 attributable to pre-existing baseline source. One entry is the related assignment location for `SA4009` at `internal/service/upstream_model_refresh_service.go:531`, paired with the primary diagnostic at line 520. Thus this sample has **58 primary locations: 4 new and 54 pre-existing**, plus that related-information location.

| Checker | Reported entries | New | Pre-existing |
| --- | ---: | ---: | ---: |
| errcheck | 13 | 2 | 11 |
| ineffassign | 4 | 0 | 4 |
| staticcheck | 32 | 2 | 30 |
| unused | 10 | 0 | 10 |
| Total | 59 | 4 | 55 |

Practical provenance of these 59 sampled entries: 4 new; 16 unchanged official-origin HTTP/2 deprecations; 39 associated with pre-existing fork state, including the related-information location. Exact-statement matching alone finds 18 official matches, but two of those are unused-function declarations that still have production callers upstream, as detailed below.

The post-four-fix capped run still reports 59 entries because previously hidden entries replace the corrected ones: unchecked expressions at `openai_volcengine_ark_test.go:143` and `openai_ws_client_read_test.go:139`, and capitalized errors at `gateway_forward_as_responses.go:40,43`. QF1001 output also rotates from one Wokey predicate to `smartrouter/core/health.go:383`. All of these replacement statements already exist at the baseline. Equal capped totals do not mean the four fixes failed.

## Uncapped inventory and additional test-only corrections

The run with `--max-same-issues=0 --max-issues-per-linter=0` removes output limits without disabling checks. It reports **110 diagnostic entries** after the first four fixes: **3 additional patch-introduced findings and 107 pre-existing entries**. There are 109 primary locations plus the same SA4009 related-information location. Both exact baseline statements and added-line ranges in the current diff were checked, so repeated old text in a newly added location was not automatically classified as baseline.

| Checker | Uncapped entries | Additional new | Pre-existing |
| --- | ---: | ---: | ---: |
| errcheck | 35 | 3 | 32 |
| ineffassign | 4 | 0 | 4 |
| staticcheck | 61 | 0 | 61 |
| unused | 10 | 0 | 10 |
| Total | 110 | 3 | 107 |

The three additional findings are unchecked `NewHTTPUpstream(nil).(*httpUpstreamService)` assertions in new tests at `internal/repository/http_upstream_test.go:1059,1079,1095` in the uncapped log. Each is corrected with a comma-ok assertion and `require.True(t, ok)` before use. This is test-only; downloader behavior is unchanged. These corrections have been source-reviewed, but the uncapped log predates them. It is not evidence of a post-correction green lint gate. No attempt is made here to repair or suppress the other 107 entries.

## Final uncapped verification

`delivery-lint-uncapped.log` completed normally on 2026-10-17 at 21:33:58 UTC with **exit 1 and 107 entries**: errcheck 32, ineffassign 4, staticcheck 61, unused 10. These are **106 primary locations plus one SA4009 related-information location**. All seven identified patch-introduced entries are absent. All 107 diagnostic tuples, including locations, are identical to `acceptance-lint-uncapped.log`, which completed at 21:14:58 UTC. The residual findings match the previously attributed baseline inventory. The final command retained all configured checks and used `--max-same-issues=0 --max-issues-per-linter=0`, with GOMAXPROCS 2, GOGC 50 and GOMEMLIMIT 3 GiB. The full lint gate remains failing; this attribution is not an exemption.

The earlier `final-deadline-lint-uncapped.log` was interrupted for shared-memory scheduling and is not a lint pass/fail result. The final completed invocation supersedes it. Baseline and official attribution remains static: neither historical tree was linted in this review.

## Four new findings and minimal corrections

All paths below are relative to `backend/`. Line numbers in the first column are from the original lint log.

| Original location | Finding | Reviewed correction |
| --- | --- | --- |
| `internal/service/image_storage_test.go:367` | Unchecked assertion to `*imageDownloadTransport` | Comma-ok assertion, then `require.True(t, ok)` before dereference |
| `internal/service/image_storage_test.go:389` | Same assertion in redirect test | Same checked assertion; now at lines 390–391 |
| `internal/service/gateway_bounded_sse.go:243` | Capitalized error string | `upstream Anthropic SSE event is missing a data field` |
| `internal/service/gateway_bounded_sse.go:265` | Capitalized error string | `upstream Anthropic event arrived before message_start` |

These corrections are safe by source review: the assertions fail the test before a wrong-type dereference; the two diagnostic prefixes do not change conditions, error types, stream terminal behavior, or billing. No checks were disabled and no lint suppressions were added. The initial review of these four edits was static; the completed final lint invocation is recorded above, and test results belong in the acceptance receipt.

## Sixteen official-origin deprecations in the first capped sample

The affected statements exist in both the fork baseline and official comparison. The bounded patch does not migrate these APIs.

| Current lint locations | Baseline / official locations | Count |
| --- | --- | ---: |
| `internal/repository/http_upstream.go:1479,1480,1485,1486,1488,1489` | Both: `1416,1417,1422,1423,1425,1426` | 6 |
| `internal/repository/http_upstream_http2_keepalive_test.go:55,56` | Both: same lines | 2 |
| `internal/server/http.go:139–144` | Both: same lines | 6 |
| `internal/service/openai_codex_models_service.go:1549` | Baseline: 1549; official: 1547 | 1 |
| `internal/service/openai_codex_models_service_test.go:1662` | Baseline: 1662; official: 1622 | 1 |

These concern `http2.Transport`, `ConfigureTransports`, keepalive fields, `ConfigureServer`/server fields, and `GoAwayError`. Replacing them requires a separate transport/server compatibility review, rather than a broad migration hidden in this security patch.

## Baseline evidence for the first capped sample

- All 11 residual errcheck statements predate the patch: route-pricing repository tests (2), model-refresh fence repository (1), KIE download/upload helpers (3), Responses image bridge and its test (4), and Smart Router scheduler (1). The changed KIE helpers only add strict downloader options; their unchecked `Close` statements are unchanged.
- All 4 ineffassign findings predate the patch in inflight reservation, KIE jobs, unified route pricing, and the Smart Router core. Their affected files are unchanged.
- Beyond the 16 HTTP/2 deprecations, the 14 residual staticcheck entries concern existing fork statements: inflight reservation (1), Responses/Anthropic request conversion (1), Wokey field-name predicates (2), response usage parsing (2), native terminal test signature (1), embedded usage selectors (3), nil-context test call (1), model-refresh argument overwrite and its related location (2), and model-source suffix handling (1).
- All 10 unused findings predate the patch. Eight declarations are absent from the official comparison: `billingBreakdownRows`, `routePricingReservationEstimator.allowed`, `saveResponsesCompatResponseFromBody`, `unifiedGatewayRoutePricingEntry`, `wokeyPriceCard`, `snapshotPublicModels`, `latestModelRefreshAt`, and `filterWokeyNativeClaudeModelIDs`. Their declaration/reference state is unchanged here.
- `isResponsesEndpointSupportedByStatus` exists at line 300 in baseline and official `internal/service/openai_apikey_responses_probe.go`. It already has no baseline calls, while official `internal/service/openai_gateway_chat_completions.go:386` calls it.
- `calculateOpenAIRecordUsageCost` exists at baseline/current `internal/service/openai_gateway_usage.go:612` and official line 578. Official calls it at lines 241, 285 and 306. Baseline already uses the `WithModel` variant directly in production. Its eight remaining callers are in four unchanged `//go:build unit` test files. The current image-billing guard removes none of those calls. Its unused status is therefore lint-build-configuration-specific, not literal absence from every build configuration.

Fixing the seven identified patch-introduced entries does **not** establish a green full lint gate. The final uncapped run still exits 1 with the 107 residual entries. This attribution is not an exemption or suppression; the acceptance receipt should cite that completed run.
