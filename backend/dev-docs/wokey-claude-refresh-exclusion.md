# Wokey Claude refresh exclusion — implementation TaskSpec

Status: TaskSpec A2 audit PASS; implementation and focused verification complete; independent repository code audit PASS; external sidecar behavior audit PASS with historical-diff provenance limitation.

## Goal

Keep Wokey-native Claude model IDs off Wokey OpenAI/Grok account routes when automatic refresh runs. Wokey Anthropic accounts remain eligible. Apply the same narrow rule to Wokey route-price-card refresh so it does not recreate managed Claude cards on OpenAI/Grok accounts.

This enforces the owner’s route policy: use the native Anthropic path for Claude. It does **not** claim Wokey cannot accept Claude over Responses: Wokey documents a lossy Responses-to-provider conversion path. This is an intentional local routing boundary.

## Frozen baseline and workspace

- Repository: `D:\AI\SSH\sub2api-official-v0.2.13-20261003`
- HEAD at task start: `0036e1248cd4b1a10acf5982100a755b6be5e43c`
- Initial `git status --short`: only the pre-existing untracked `.wokey-pnpm-lock-sync/`, `backend/dev-docs/account-delete-scheduler-outbox-atomicity.md`, and `backend/dev-docs/gpt-responses-v2-compaction-routing-design.md`.
- The current Wokey price design, price-sync TaskSpec and coverage-record hashes match the parent instruction. Preserve them as historical evidence; this TaskSpec is a narrow additive policy guard.
- No runtime account, route, price card, API key, balance, image, container, VPS, GitHub, or scheduler setting changes. No generation requests, commit, or push.

## Source facts and limit

- Wokey model-list and public price-catalog parsers currently provide model IDs and prices, but no per-model `vendor` or supported-API-form field. Therefore these refresh paths cannot make a dynamic capability decision from their current machine-readable payloads.
- Wokey’s public model pages identify native Claude SKUs with IDs beginning `claude-` and list `/v1/messages` as their native API form. A separate Wokey Codex guide says Responses requests can be converted to provider protocols with feature loss. The local filter is a deliberate account-route policy, not a statement that conversion is impossible.
- Use the anchored, case-insensitive `^claude-` model-ID family only. Do not match the substring `claude` elsewhere; in particular, retain IDs such as `cursor-claude-opus-5`.

## D/I/A and frozen behavior

- D0: target and policy are explicit.
- I2: the price-card synchronizer, server-side model catalog endpoint, and scheduled Windows sidecar are separate refresh/write boundaries.
- A2: an accidental route/price card could affect user routing or billing; only deterministic fixtures and local tests are authorized here.
- **Match predicate:** exact Wokey HTTPS base origin (`api.wokey.ai`, default/443 port and the existing accepted base paths), API-key account, platform `openai` or `grok`, and model ID prefix `claude-` (case-insensitive).
- **Model discovery:** the backend catalog returned for a matching account excludes those IDs. If the upstream returned a nonempty live list but all IDs were excluded, return a successful live catalog with zero eligible IDs; a genuinely empty raw list remains an error. The 04:10 sidecar independently applies the same predicate before generating identity mappings, and drops any existing key or target in the excluded family for that matching account. It may save the resulting empty mapping only for a successful live Wokey catalog emptied by this rule; failed, raw-empty, and non-live catalogs remain skipped. It preserves all existing alias/wildcard behavior for every other account/model.
- **Price sync:** pass the selected accounts’ current platform/type into the card merge decision. Omit new managed `wokey_catalog` Claude cards for matching selected OpenAI/Grok accounts and remove their existing managed Claude cards when publishing a successful sync. Keep cards for Wokey Anthropic accounts, other Wokey model IDs (including `cursor-claude-*`), unselected accounts, other providers, and manual cards unchanged.
- Native 04:00 availability refresh remains untouched. It treats Wokey as `manual_only`; the scheduled account mapping writer is the existing 04:10 sidecar. Add no new scheduler or network call.

## Allowed files

1. `backend/internal/service/upstream_models.go`
2. `backend/internal/service/upstream_models_test.go`
3. `backend/internal/service/composite_platform_test.go` (regression proof only; production resolver unchanged)
4. `backend/internal/service/unified_gateway_wokey_price_sync.go`
5. `backend/internal/service/unified_gateway_wokey_price_sync_test.go`
6. `D:\AI\SSH\sub2api-model-refresh\Invoke-Sub2ApiAutoModelRefresh.ps1`
7. This TaskSpec only. Do not edit the existing Wokey price coverage or model-refresh follow-up records; this file is the sole test/audit record for this narrow change.

No other files or runtime state may change. In particular, no edits to billing settlement, generic routing, provider defaults, UI, migrations, credentials, account configuration, or the native 04:00 schedule.

## Acceptance tests

- Go model-list regression: matching Wokey OpenAI and Grok IDs exclude `claude-*`; Wokey Anthropic, non-Wokey OpenAI/Grok, non-API-key, and near-match IDs remain unchanged. Verify a raw-empty catalog still errors while a nonempty list filtered to zero returns a successful live catalog.
- Composite ownership regression: a synthetic active Wokey OpenAI/Grok mapping filtered by this policy plus an existing Anthropic Claude mapping resolves only to Anthropic and is not ambiguous. No production resolver behavior changes.
- Go price-card regression: for a successful catalog publish, OpenAI/Grok selected accounts have no Wokey-managed `claude-*` cards (including previously managed cards); Wokey Anthropic keeps them; `cursor-claude-*`, other providers, unselected accounts, and manual cards are unchanged.
- PowerShell `-SelfTest`: matching Wokey OpenAI/Grok refresh removes prior Claude mappings (including aliases with an excluded key or target) and cannot save Claude IDs even if an older/unfiltered catalog response includes them. A successful live catalog containing only excluded IDs clears only those Claude mappings; raw-empty, failed, and non-live catalogs still do not save. `cursor-claude-*`, aliases/wildcards outside the excluded family, Anthropic, and non-Wokey accounts remain unchanged.
- Run focused Go tests for the two changed service files and the existing PowerShell self-test; run `gofmt` and `git diff --check`.
- Independent read-only audit checks the frozen diff against this TaskSpec, confirms provider/account isolation and price-card ownership, and checks no runtime/deployment/credential files changed.

No full suite, Docker build, live model sync, scheduled run, or upstream generation is required for this bounded policy filter.

## Implementation and verification status

- Implementation files: the two model-list service files, the two Wokey price-sync files, the synthetic Composite ownership regression test, and the existing Windows sidecar script listed above.
- Passed with exit 0: `go test -count=1 -timeout=300s ./internal/service -run Claude`.
- Passed with exit 0: `go test -count=1 -timeout=300s ./internal/service -run TestFetchUpstreamSupportedModelsForWokeyRequestsAllModalities`.
- Passed with exit 0: `go test -count=1 -timeout=300s ./internal/service -run TestParseWokeyCatalogAndBuildsTokenImageVideoCards`.
- Passed with exit 0: `go test -count=1 -timeout=300s ./internal/service -run TestResolveCompositeModelOwnership`.
- Passed with exit 0: Windows PowerShell 5.1 `Invoke-Sub2ApiAutoModelRefresh.ps1 -SelfTest`.
- `go fmt` completed with exit 0; `git diff --check` completed with exit 0.
- No live request was needed: this patch changes only catalog/mapping refresh decisions and managed price-card synchronization; it does not change the Anthropic Messages handler or generation path. The authoritative Claude Code native Anthropic baseline already passed on 2026-10-06. The current local runtime predates this code and its database has no Wokey #27/#28 accounts, so a live request there would not exercise the Wokey exclusion and a Claude generation would only repeat an unchanged path. No live request, refresh, account enablement, or container replacement was performed.
- The synthetic Composite regression reproduces the Wokey OpenAI/Grok + Anthropic duplicate claim, then verifies the filtered mapping leaves Anthropic as the sole owner. This proves the resolver no longer sees the cross-platform duplicate after the automatic mapping refresh; it does not claim the current runtime database was updated.
- Independent code audit: repository implementation PASS. The auditor confirmed exact Wokey/API-key/OpenAI-or-Grok scoping, anchored Claude ID filtering, managed-card ownership, empty-live-catalog handling, preservation of Anthropic/manual/unselected/cursor entries, and no edits to production resolver, native handlers, settlement, or generic routing.
- Independent audit of the external PowerShell sidecar's current behavior PASS, including removal of matching Claude mapping keys/targets and preservation of unrelated aliases/wildcards. Its directory has no Git metadata or prior copy, so the auditor could not verify the complete historical diff against the pre-task script; this is a provenance limitation, not a behavioral test failure. The script was edited through narrow targeted patches and its `-SelfTest` passed.
- Final disposition: the code-level duplicate claim is removed on the next successful Wokey model-map refresh and the next successful Wokey price-card sync. Flowing AI and other non-Wokey accounts do not match the exclusion predicate and remain on their existing native paths. The currently running image/database was not refreshed or redeployed, so no claim is made that a live runtime's old mapping has already disappeared.

## Known limitation

The current Wokey catalogs do not expose machine-readable vendor/protocol capabilities. The durable local family rule is the anchored `claude-` prefix, not a general capability registry. If Wokey changes Claude IDs or adds machine-readable per-model API-form metadata, revise this explicit policy and its tests before widening the filter. `cursor-claude-*` remains in scope for neither removal nor suppression.
