# Verification matrix

All tests in this branch use local fixtures, mocks, pipes, or isolated databases. No real provider request, production credential, balance update, or VPS deployment is authorized by this plan.

| Invariant | Regression evidence required | Release evidence still required |
|---|---|---|
| No image charge without delivered artifact | Empty/error/malformed 2xx, strict string artifact count, zero-image result with nonzero token usage, explicit token pricing | Staging usage ledger reconciliation using test accounts |
| Official retry compatibility | Existing OAuth empty-output/body-read retry tests remain unchanged | Replay/accounting ambiguity is a documented residual, not certified fixed |
| Correct image parameters | Explicit 1K versus default 2K, actual dimensions win; multipart preserves supported scalar/structured values or rejects explicitly | Each enabled provider's documented fields |
| Stream integrity | EOF, usage-only, malformed events, in-band errors, read errors, valid finish + EOF, length/incomplete | Staging slow/disconnected clients and concurrency |
| Bounded upstream lifetime | Header wait, idle body, pinging canceled stream, writer failure, body close, no late writes | Worker/request capacity during repeated disconnections |
| Private voices (deferred) | Experimental tests excluded from candidate; do not count as passing coverage | Shared-account isolation unresolved; no new schema or protocol |
| Download isolation | Mixed/private DNS, repeat resolution, numeric pinned dial, TLS hostname checks, redirect, strict proxy refusal | Deployed proxy/egress configuration and image storage compatibility |
| Bounded image resources | Encoded/decoded byte limit, pixel dimensions before decode, malformed image, per-image allocation limits | Representative valid avatars and image provider output sizes |
| Official IP configuration | Original compatibility behavior preserved | Effective persisted false, exact trusted peers and forged-header test before secure deployment |
| Dependency and lint gates | Exact patched dependency locks, frozen install, real XLSX roundtrip, Vue escaping, Go vulnerability/lint jobs | CI on the final SHA; full-tree advisory review is separate from production gate |

Test counts must state build tags and skips. A synthetic reproduction that passes by asserting the old defect is not repair acceptance. Results on an earlier SHA do not certify a later commit. Keep failed attempts and their causes visible in PROGRESS.md.
