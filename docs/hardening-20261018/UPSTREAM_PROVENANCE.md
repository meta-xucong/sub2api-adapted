# Official-first scope and provenance

User scope update (2026-10-18 Beijing): prefer official Sub2API implementations and repairs. New logic in official-origin code requires a verified uncovered defect and credible public discussion. User-added fork features may be audited and repaired directly. This document supersedes broader provisional decisions in the initial plan.

## Reference points

- Fork baseline: `meta-xucong/sub2api-adapted@2bedb713b6b0b9cf968468decae36732f390574a`.
- Official reference fetched: `Wei-Shaw/sub2api@b0a02a3304a0a78cdd003ddda1dd23ba02c03084`.
- There is no common ancestor in this fork's restored history. A file introduced by restore commit `6d47306` is NOT thereby a fork invention. Compare exact blobs and function hunks against official history.
- No wholesale upstream merge is performed; unrelated behavior changes are excluded.

## Decisions

### Custom voice ownership: deferred

Pre-patch `grok_audio.go` service and handler are byte-identical to official current main (service blob `c726b339df033c595e4bb3b61b6bc6e0ba3c8344`, handler `7455485d7b810e1ff7806d62085055e8ea00f697`). Official feature/CRUD sources:

- https://github.com/Wei-Shaw/sub2api/commit/99ad01f6de1ede51e8a43ff76a3a85c07cae5fc9
- https://github.com/Wei-Shaw/sub2api/commit/35faaa6d2100ae7ed94982d29c1e23882c709563
- https://github.com/Wei-Shaw/sub2api/pull/5408

The audit demonstrates missing tenant ownership in shared-account voice operations, but no matching credible public isolation repair/discussion was found. The proposed durable ownership table and `gateway_voice_id` protocol extension are therefore excluded from this candidate. Their experimental patch is preserved separately, not counted as fixed or tested release functionality. Do not claim shared custom voices are tenant-safe. Operational restriction of these endpoints requires a separately approved deployment change.

### Forwarded-IP defaults: preserve official behavior

Official issue https://github.com/Wei-Shaw/sub2api/issues/6112 describes spoofing under raw forwarded-header trust. Official nevertheless deliberately retains a compatibility mode and provides secure `false` + exact trusted proxies configuration. The experimental default/migration/UI changes are excluded to minimize divergence. Deployment acceptance must inspect actual persisted settings and origin access; a stored true may originate from old defaults or migration, not deliberate administrator approval. Merely changing YAML is not proof that persisted settings use it.

### Stream terminal/accounting/lifetime: public-backed minimal adaptation

Relevant upstream reports/candidates:
- https://github.com/Wei-Shaw/sub2api/issues/7274
- https://github.com/Wei-Shaw/sub2api/issues/7876
- https://github.com/Wei-Shaw/sub2api/pull/7791
- https://github.com/Wei-Shaw/sub2api/pull/7862

PRs are candidates, not proof of an official shipped repair. Independent local reproductions corroborate the gaps. Prefer existing handler accounting/cancellation conventions. A narrow synchronous bounded scanner avoids introducing a read goroutine or changing the unrelated native Responses pump. Each divergence must retain regression evidence.

### Image resource limits and dependencies

The official Go image package documents that untrusted image dimensions must be checked with DecodeConfig before Decode, and provides an untrusted-decode example: https://pkg.go.dev/image#hdr-Security_Considerations . The baseline avatar function is identical to official and lacks that precheck. Any local limits are an explicit product compatibility choice, not an upstream Sub2API fix; final acceptance must state them.

Published Go x/image and frontend package advisories provide the dependency repair basis. Vue and source-map-js should follow verified official current versions; SheetJS official CDN and development-tool changes must retain exact integrity and test evidence. Remaining advisories must stay visible.

Exact retained image and downloader provenance is documented below and in DOWNLOAD_SECURITY.md.

Avatar refinement: retained only encoded-byte precheck (preserving CR/LF wrapping) and DecodeConfig dimension/pixel checks before the existing compression decoder. Removed new 512px resizing, semaphore/busy API behavior, MIME-policy tightening and small-image full-decode changes. Public same-root-cause evidence: https://github.com/golang/go/issues/12512 and https://github.com/cloudreve/cloudreve/security/advisories/GHSA-g9j2-8w95-3vwv . These establish the resource-exhaustion class, not a published Sub2API-specific CVE. The 4096-side/4MP limits are this candidate's conservative bounds; no claim of an aggregate memory cap.

### Image policy and counting

Official merged PR #5404 / commit `9763765eb` deliberately retries OAuth HTTP-200 body-read failures (issue #5400). Official commit `cb8dabc12` deliberately retries empty terminal image output. Our provisional no-replay policy would reverse that behavior; it is excluded. Broad 2xx outcome/error rewriting and new total media deadlines are also excluded. Official PR #5925 explicitly preserves long-running media and disabled timeout compatibility.

Official merged PR #3215 / commit `0da1fe28e` requires billing actual artifacts rather than arbitrary data entries or result-less completion events. This provides a direct anchor for retained strict string artifact counting, removal of requested-N fallback, and an internal ImageRequest marker to preserve zero-unit billing semantics. Explicit token-priced models retain observed-token accounting. This does not change official replay policy or claim every empty response is now a correctly classified API error.

Other retained image scope: explicit-resolution billing gap supported by official #5917/#5925 plus local reproduction; multipart fields needed by the fork's Wokey image-edit adapter, confined to that adapter. No cross-attempt accounting redesign.

Exact retained image sources:
- https://github.com/Wei-Shaw/sub2api/pull/3215 ; commit `0da1fe28efd4b539181864692ea02b5320862a20`, fixes issue #3196. Wrapper requested-N fallback and non-string coercion remain in official b0a02a3; the candidate closes those residual counting gaps.
- https://github.com/Wei-Shaw/sub2api/issues/5917 and https://github.com/Wei-Shaw/sub2api/pull/5925 ; explicit resolution drives upstream geometry but fallback billing still read size alone. Actual returned dimensions remain higher priority.
- Fork Wokey adapter introduced at `2bedb713`, absent from official reference. Preservation wrapper is gated by `isWokeyGrokImageEdit`.
- Deferred policy sources: https://github.com/Wei-Shaw/sub2api/pull/5404 , https://github.com/Wei-Shaw/sub2api/issues/5400 , https://github.com/Wei-Shaw/sub2api/pull/6065 , and #5925. No retry/cooldown/long-media deadline changes remain.

## Final fork-only image deadline race

The existing fork `openai_image_upstream_timeout.go` helpers and Images total-budget guards are absent from the official comparison `b0a02a3304a0a78cdd003ddda1dd23ba02c03084`. The restored snapshot commit `6d47306` is only historical context and is not the ownership proof; direct function/file comparison is.

A full-suite run reproduced the detached attempt timer expiring before the request timer published `Err()`, allowing another account to be selected after the shared deadline. The minimal fix checks the existing absolute deadline as well as cancellation in the classifier and two pre-existing handler budget guards. Deterministic lagging-timer contexts test expiry admission; future-budget independent attempt retries remain allowed. No official empty-output retry, media timeout, provider transport or global scheduling mechanism is replaced.

A separate existing prewarm test used the real dialer with `example.com` despite intending to verify a missing-pool connection close. Its test-only repair injects the existing official in-memory fake and asserts one connection was created and closed before cleanup. This improves isolation and coverage; it does not change production networking or retry the denied external request.

## Existing official allocation guard: sampling only

The sanitizer and its test are byte-identical to the fork baseline and official comparison before this test-only adjustment. [Official Sub2API commit269a409](https://github.com/Wei-Shaw/sub2api/commit/269a4092418b57b36312991fdc35afdc918761eb), merged through PR6136, already identifies process-wide allocation noise from package background work. A final aggregate here measured1792 allocations against the existing200 limit; the unchanged test then passed20 isolated repetitions. This supports measurement interference, without identifying every allocation in the failed interval.

The minimal adjustment follows [Go's own testing fix](https://go.googlesource.com/text/+/3115f89c4b99a620c7f1a4395a2b4405e95b82b6): increase AllocsPerRun samples to100. The200 threshold and all2,000 output assertions stay unchanged; the sanitizer production implementation is untouched. More samples reduce occasional bursts, not sustained noise. A true per-call linear allocation regression still fails the same bound. This is a test sampling repair, not a production performance claim or suppression.
