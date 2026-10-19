# Veyra PR12 authentication and portal repair plan

Reviewed revision: a9157b962616f7ca1dc07d8b95666edebf8a716b, against base 2bedb713. The isolated candidate implements the repairs below. The focused frontend run passes 9 suites and 127 tests after two new-test fixture corrections. A bounded 768 MiB typecheck attempt exhausted its heap before completion. Backend, browser and aggregate results are not claimed here; see the validation receipt below.

## Confirmed defects

1. **P2: Default CSP blocks Video OS login.** `backend/internal/veyra/portal_dist/app.js:247-259` posts to `https://video.aiself.vip/auth/veyra/callback`, but the enabled default policy has `form-action 'self'`. Security headers run before portal middleware.
2. **P2: Redis ticket replay near expiry.** `backend/internal/veyra/ticket_redis.go:81` uses `redissession.Store.TryConsume`; `backend/internal/pkg/redissession/store.go:108-112` derives the used-marker lifetime from rounded whole-second TTL. The marker can expire before the ticket. Redis source: https://github.com/redis/redis/blob/7.2/src/expire.c#L632-L652.
3. **P2: OAuth completion loses Video return intent.** OAuth callbacks use SPA navigation, while `frontend/src/router/index.ts:65-72` only recognizes Alchemy targets. Password/passkey/2FA completion in LoginView already uses the strict hard-navigation helper and is not defective.

## Minimal changes

### One-time consumption

Keep the shared Store API, key namespace, JSON format, Get and Delete behavior unchanged. Its only production consumers are Veyra tickets and xAI OAuth. xAI validates state and redirect before claiming, then deletes on completion; a retained-data claim marker preserves this behavior.

Replace the multi-command TTL/SET-NX/EXISTS sequence with one fixed Lua script:
- Read the data key's PTTL. Reject missing, expired/sub-millisecond, or unexpectedly persistent data.
- Atomically set the used marker with NX and PX.
- Marker lifetime is the larger of the remaining data PTTL and this store's configured TTL in milliseconds. This provides a bounded tombstone which cannot expire earlier than the observed data; it also handles instances with differing configured TTLs.
- Do not refresh/delete the data key, overwrite an existing marker, revive missing sessions, or fall back to local memory on errors.
- A script/Redis error returns false plus the error. The script takes only the existing keys and a numeric TTL, not user-provided code.

### Portal-only CSP exception

Keep all Veyra enable flags false by default. Modify an already-present CSP header only when the enabled portal serves an exact portal HTML route: `/`, `/_veyra/return`, `/_veyra/`, `/_veyra/index.html`, or `/_veyra/mobile.html`. Add only the fixed HTTPS callback source `https://video.aiself.vip/auth/veyra/callback` to form-action. Retain all other directives, nonce values, and cloud media-src hardening. No wildcard, general HTTPS permission, connect-src change, or exception on login/admin/API/static-asset responses. An explicit custom `form-action 'none'` remains a denial; a missing CSP header stays missing. Reject broad prefix/path-alias matching.

### OAuth completion navigation

Reuse `completeLoginRedirect` after each successful login completion, preserving existing sanitization and router.replace/push behavior for ordinary app paths. Hard navigation uses window.location.replace so one-time OAuth callback material is not retained as a useful Back target. Cover Google/GitHub, LinuxDo, OIDC, WeChat, DingTalk, DingTalk email completion, and the pending OAuth email-verification completion. Preserve account-binding redirects, intermediate flows, failed logins, and login-required navigation.

The existing strict helper already supports Video. Keep its exact relative-path/target validation unchanged. Do not turn arbitrary URL query values into hard navigation. The stale-SPA fallback itself is unchanged: direct SPA navigation to `/_veyra/return` only has an Alchemy compatibility route; Video/home/console fall back to `/home`. The repair removes every supported successful-login caller of that fallback by hard-navigating validated portal callbacks. This is deliberately narrower than claiming general stale-SPA/reverse-proxy fallback support. No automatic reload is added, avoiding loops when the portal is disabled or a reverse proxy incorrectly serves the SPA for portal paths. Separately test all five allowed return targets.

## Exact regression plan

### Redis/shared helper
- A 120-second data key: advance miniredis 800 ms, consume, advance another 119,050 ms, and reject replay while data still has 150 ms. Verify original data TTL was not extended.
- The same timing through RedisTicketStore with caller-supplied now = ticket.CreatedAt + the simulated advances.
- Thirty-two concurrent claims across two Store instances produce exactly one success.
- Unknown, expired, persistent-without-TTL, and already-used keys reject without creating a usable claim.
- Closed Redis, cancelled context, or script failure cannot produce success or memory fallback.
- Existing Get-after-claim and explicit Delete continue working. Run xAI remote-session single-use/fallback tests to verify compatibility.

### CSP
- Compose SecurityHeaders and enabled PortalMiddleware in production order. Exact portal HTML pages allow only the fixed callback in form-action.
- Either flag false preserves the original root and unmodified CSP.
- `/login`, `/admin/dashboard`, `/api/veyra/portal/config`, `/_veyra/app.js`, near-match/aliased paths do not gain the exception.
- Verify nonce/script-src, frame-ancestors, base-uri, media-src and unrelated directives survive unchanged; repeated application must not duplicate the callback.
- Custom 'none' stays closed; CSP-disabled responses do not acquire a new policy.
- When browser validation is authorized, mock ticket/config calls and intercept the fixed callback before any outbound connection. Confirm an actual browser form submission is permitted under the emitted policy, and a different origin remains blocked.

### Frontend
- Every OAuth finalization family calls hard navigation exactly once for `/_veyra/return?target=video`, and does not call router.replace for that target.
- Include pending OAuth exchange, legacy fragment completion and OAuth 2FA, plus DingTalk email and email-verification continuations.
- Ordinary `/dashboard` and `/profile` redirects remain SPA navigations. Binding/intermediate/error paths retain their prior behavior.
- External URLs, protocol-relative URLs, dot segments, duplicate targets, extra query parameters and unknown targets are never hard navigations.
- Run existing auth callback suites, LoginView/EmailVerify tests, strict helper tests, frontend typecheck and lint check.

### Sequencing

All implementation belongs in an isolated candidate, not the verified snapshot or frozen primary. Run no compiler, test process or browser until the gate owner releases execution. Then run targeted backend tests (including race detection for concurrent claims), frontend targeted suites, and the repository's aggregate checks sequentially against the final candidate. No live provider, production, login, publication or deployment is required.

## Adjacent risks and boundaries

- Issuance uses the existing JWT middleware, including active-user, token-version and configured session-binding checks. The new SessionAdapter is unused.
- Enabled issuance permits all authenticated active users and does not inherit BackendModeUserGuard or panel rate limiting. This is a deployment-policy question; this repair does not silently change eligibility.
- The shared internal token authorizes cross-user account/usage reads and debit operations. Per-service/per-user authorization redesign is outside these three repairs.
- Exchange has no independent current account/session revalidation. The inspected local Video callback checks exact portal Origin and active account; deployed receiver state and the Alchemy receiver were not verified.
- The original internal-token middleware wrote its captured normalized string on every request. The candidate normalizes once during handler construction and includes a concurrent valid/invalid request regression; this repairs the data race without changing authorization semantics.
- Keep the cloud checkout's existing media-src policy and documented H2C compatibility annotations during integration. Apply PR patch hunks rather than replacing entire files.

## Additional confirmed portal issues repaired

- The inline mobile-UA redirect was blocked by the nonce-based script-src. It is now an external same-origin `mobile-redirect.js`, copied byte-for-byte to the source and embedded asset directories. No unsafe-inline permission was added.
- Mobile redirects now skip the exact `/_veyra/return` callback. This keeps the explicit return target intact rather than moving it to a mobile path that the callback parser does not recognize. Normal mobile root navigation still preserves query/hash and `desktop=1`.
- Desktop brand navigation now uses `/`, avoiding a relative `/index.html` route into the original SPA.
- Internal-token normalization now happens once before constructing the request handler. A mixed valid/invalid concurrent-request regression is included for race-detector execution.

## Implemented regression coverage

- Shared Redis helper: fractional expiry, concurrent multi-instance claims, original payload TTL preservation/Get-after-claim, missing/expired/persistent keys, cancelled context and unavailable Redis.
- Veyra Redis tickets: fractional expiry replay, concurrent exchanges, unavailable Redis.
- Portal CSP: production middleware ordering, exact route matrix, both feature flags, full policy equality (including nonce/media-src), explicit custom denial and idempotence.
- Frontend: all five exact return targets, no hard navigation for malformed destinations, mobile redirect behavior and asset parity. Component-level OAuth tests exercise Google/GitHub, LinuxDo, OIDC, WeChat and DingTalk pending/legacy success, ordinary SPA destinations, provider 2FA, DingTalk email completion and pending email verification.

## Negative-control execution inputs

Evidence-only Go overlays provide isolated negative controls without modifying either source tree. Ticket and guard controls replace the repaired file with its immutable PR12 version; the CSP control disables only the portal policy-repair invocation in an external candidate copy:
- `negative-ticket.overlay.json`: run `TestStoreConsumeMarkerOutlivesFractionalDataTTL` and `TestRedisTicketStoreRejectsReplayAtFractionalExpiry`; original code should fail because replay succeeds.
- `negative-guard.overlay.json`: run `TestInternalTokenGuardConcurrentRequests` under `-race`; original captured-string mutation should be reported.
- `negative-csp.overlay.json`: an external copy of the candidate portal.go removes only the call to `allowPortalVideoFormAction`, retaining declarations required by the new tests. This controlled disabling reproduces the original portal CSP behavior without compile failures in the Go test-based browser harness. It is not labeled a raw-snapshot overlay.

The shared Lua claim uses the existing standalone `redis.Client`. It does not add Redis Cluster support; existing keys are not hash-tagged. The tombstone can outlive an expired payload by up to the configured session TTL, bounded by the existing expiration policy. Nonexpiring payloads now fail closed because Store.Set always supplies an expiry.

No live services, provider calls, production credentials, commits, pushes, deployments, or feature enablement are part of these changes.

## Return-navigation reachability audit

The remaining SPA fallback is not claimed fixed. These supported successful-login paths now call the strict hard-navigation helper before any SPA navigation can occur for a valid Veyra callback:

- Password, passkey, and password 2FA: unchanged LoginView.vue calls at lines 606, 650 and 721.
- Google/GitHub: OAuthCallbackView.vue finalizer at line 291, shared by pending exchange, legacy fragment and registration completion.
- LinuxDo: finalizer, OAuth 2FA, and legacy fragment at lines 599, 751 and 775.
- OIDC: corresponding calls at lines 623, 775 and 801.
- WeChat: corresponding calls at lines 833, 986 and 1047.
- DingTalk: corresponding calls at lines 598, 753 and 777; email completion at DingTalkEmailCompletionView.vue line 112.
- Pending OAuth email verification: EmailVerifyView.vue line 755.

The remaining direct router calls in those files are account-binding completion, requests to log in, intermediate pending/email-completion screens, or ordinary non-login navigation. These are not successful login returns. The separate WeChat payment callback navigates to a payment result; direct registration navigates to verification/dashboard and did not carry a portal-return contract in the reviewed baseline. Neither is presented as a newly supported portal-return flow.

Source inspection found no remaining `router.replace(redirect)`, `router.replace(redirectTo.value)`, or `router.push(pendingRedirect...)` successful-login call in the affected views. The parameterized component suite is the runtime proof for pending, legacy and OAuth-2FA paths; it must pass before completion is claimed. A deployment that routes all portal URLs to an SPA still needs routing correction, not automatic browser reloads.

## Frontend validation receipt (2026-10-19)

Execution used Node v20.20.2 and the existing installed dependency tree, with no installation or network dependency resolution. Vitest v3.2.6 ran with one fork worker (`--pool=forks --maxWorkers=1 --minWorkers=1`) and `NODE_OPTIONS=--max-old-space-size=768`.

Initial run: exit 1; 7 suites passed, 2 failed; 115 tests passed and 3 failed, with the mobile suite failing collection. The new component fixture lacked Pinia for the existing useClipboard composable; the mobile fixture used a Vite-transformed import.meta.url rather than a file path. Neither failure required a production-source change. The corrected fixtures provide Pinia and resolve assets from the frontend working directory. The initial failure log is retained rather than replaced by the retest.

Retest: exit 0; **9 suites and 127 tests passed**, including:
- OAuth return integration: 21
- Mobile surface/return preservation and asset parity: 9 (all collected and executed on retest)
- Strict return helper: 16
- Existing WeChat 25, OIDC 15, LinuxDo 17, generic OAuth 6, EmailVerify 16, Login 2

Typecheck: `vue-tsc --noEmit` exited 134 after V8 exhausted the explicitly authorized 768 MiB heap. This is an incomplete check, not a typecheck pass or a TypeScript diagnostic. It requires a new appropriately sized validation lane. No production frontend build, Go compiler or browser run was started by this worker.

Evidence is retained in `veyra-pr12-audit/evidence/auth-validation/` outside the candidate:
- `frontend-focused-initial.log` / `.exit`: preserved first-run failure
- `frontend-focused-retest.log` / `.exit`: passing retest
- `frontend-typecheck.log` / `.exit`: bounded heap failure
- `frontend-before.json`, `frontend-after.json`, `frontend-fixture-delta.json`: per-file SHA-256 identity, proving the only frontend source differences during this validation were the two new regression fixtures

Final fixture SHA-256 values:
- veyraPortal.spec.ts: `7573b3771a7c781ac666d2b1952a52f1680daf531fff387b5f9bf0bbc772f590`
- VeyraOAuthRedirects.spec.ts: `7779e14616abc90e102e9b8dcea4ced4ae9d80719e592adf1e7e3339c2323c96`

The temporary candidate frontend/node_modules symlink reuses the primary dependency directory. It is not a source change and must not be included in integration. `git diff --check` passed after fixture correction. Full backend race/negative controls, browser acceptance, larger-heap frontend typecheck and the final aggregate gate remain separately owned validation steps.
