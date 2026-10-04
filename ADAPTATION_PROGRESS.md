# Sub2API v0.2.13 Adaptation Progress

## Phase 1 — Accepted

**Status:** `ACCEPTED_PHASE_1_WITH_UNRELATED_BASELINE_TEST_CAVEAT`
**Official base:** Sub2API `v0.2.13`, commit `3040209f205472038c1ba745a1bedd2edd9053b1`
**Snapshot branch:** `upgrade/v0213-phase1-20261004`

### Scope completed

- Admin model selector filtering/presentation, without changing routing IDs, billing IDs, or upstream discovery.
- Third-party OpenAI Fast priority policy.
- Opt-in operator test-key guard on the four approved asynchronous image submit routes.
- Veyra/Alchemy frontend login-return handling.
- Image request failover, cooldown, timeout, incomplete-result handling, and the explicit Responses image bridge.
- Account-scoped AIAI asynchronous image edit and Volcengine Ark adaptations.
- Account-scoped Wokey and KIE video provider paths.

Official default image, Responses, and ordinary Grok paths remain covered by negative/regression tests. This phase does not include Smart Router, model discovery refresh, unified gateway, production account/configuration migration, or deployment.

### Verification

All checks below were run locally against the pinned target worktree; no live provider calls or production accounts were used.

| Check | Result |
|---|---|
| Scoped Go packages: `go test -count=1 -timeout=360s ./internal/pkg/openai ./internal/config ./internal/pkg/xai ./internal/handler/admin ./internal/handler ./internal/service ./internal/server/middleware ./internal/server/routes` | Exit 0 |
| Phase 1 `unit`-tagged regression selection across affected Go packages (image providers/bridge, failover/timeouts, Wokey/KIE, model selector, and operator guard) | Exit 0 |
| Frontend router tests: `corepack pnpm@9.15.9 exec vitest run src/router/__tests__/feature-access.spec.ts src/router/__tests__/veyraReturn.spec.ts` | 22/22 passed |
| Frontend typecheck: `corepack pnpm@9.15.9 typecheck` | Exit 0 |
| Go formatting / whitespace checks | 50 Go files formatted; `git diff --check` exit 0 |
| Source Fidelity and independent code audits | PASS on the frozen Phase 1 implementation and r3 file manifest |

The phase-one implementation manifest contains 56 files. The full focused `unit`-tagged suite is not green: `TestOllamaProbeCallback_StaleLongDoesNotOverrideNewShort` also fails on a clean worktree at the pinned official baseline. A previous full-backend run also failed in untouched repository tests (`TestAliyunCaptchaVerifier_TransportError` and Windows PgDumper cases requiring `sh`). These baseline failures are recorded, not counted as Phase 1 passes.

### Explicit limitations

- Veyra/Alchemy in this phase restores the frontend return handler only. v0.2.13 has no corresponding backend callback/portal subsystem, so end-to-end Veyra login return is not claimed.
- The Responses image bridge has focused dispatch/timeout tests, but no new database-backed integration fixture covering scheduler account selection through the final handler dispatch.
- Live AISelf/provider behavior and deployment remain unverified; this is a local source migration and regression-test acceptance only.

The detailed source mapping and audit receipts are maintained with the local adaptation records. No production data, credentials, or VPS configuration were included in this snapshot.
