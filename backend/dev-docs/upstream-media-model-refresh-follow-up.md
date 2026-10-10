# Media-model discovery in the daily refresh — follow-up TaskSpec

Status: local acceptance passed on 2026-10-09. This is a narrow owner-authorized follow-up to accepted Phase 4; it does not reopen or rewrite Phase 4's acceptance.

## Goal

Extend the existing 04:00 Asia/Shanghai native refresh plus 04:10 Windows sidecar so a successful live model-directory response can add newly listed text, image, and video model IDs to the account mapping. The refresh must distinguish a live directory from the native sync endpoint's configured-model fallback, which is not availability evidence.

## Hard boundaries

- The discovery code sends only model-directory requests; it does not test whether listed models can generate. A changed account mapping is saved through the native update API, which may launch the pre-existing OpenAI `/v1/responses` capability probe. The owner explicitly accepted that possible one-probe-per-changed-account side effect on 2026-10-07. No image or video generation request is part of this follow-up.
- Keep the native 04:00 scheduler and existing 04:10 sidecar schedule. Do not add another scheduler, provider registry, price updater, alias system, routing rule, allowlist change, or UI.
- Keep model IDs exactly as returned. Do not infer that `gpt-image-2`, `gpt-image-2.5`, dated IDs, or video IDs are interchangeable.
- For Wokey, modify only the model-catalog GET for an API-key account whose configured base URL is the exact documented HTTPS origin `api.wokey.ai` (default/443 port). Request all output modalities using `output_modalities=all`. Generation calls and other providers must remain untouched.
- For YeToken/NewAPI-compatible accounts and other providers, keep the existing native model-list path. Add no provider-specific inference or alternate endpoint.
- The sidecar may save only a successful, nonempty live catalog. An explicit configured-model fallback is skipped; identical live results are logged as unchanged. Older server responses lacking provenance retain the prior conservative fallback check.
- Preserve aliases and wildcard mapping entries. Leave billing, account credentials, groups, model allowlists, and upstream settings unchanged.
- Preserve existing user changes and the pushed checkpoint `95f96da876159575d142a3cfb028f3f52b5ae6fc`.

## Frozen baseline and change boundary

- Repository: `D:\AI\SSH\sub2api-official-v0.2.13-20261003`
- Branch/HEAD at freeze: `upgrade/v0213-phase5-20261005` / `95f96da876159575d142a3cfb028f3f52b5ae6fc`
- Worktree at freeze: two in-progress files from this follow-up (`upstream_models.go`, `upstream_models_test.go`); pre-existing untracked `.wokey-pnpm-lock-sync/` and `backend/dev-docs/account-delete-scheduler-outbox-atomicity.md` must remain untouched.
- Current controlling docs inspected: adaptation blueprint, Phase 0 mapping, Phase 4 record. Their current hashes and the AGENTS-provided hash discrepancy are recorded in the final follow-up result section, not treated as a reason to silently change baseline.

Allowed implementation files:

1. `backend/internal/service/upstream_models.go`
2. `backend/internal/service/upstream_models_test.go`
3. `frontend/pnpm-lock.yaml` only to reconcile the existing `package.json` overrides after the full Docker build exposed a frozen-lockfile mismatch; no dependency or manifest versions were changed.
4. `D:\AI\SSH\sub2api-model-refresh\Invoke-Sub2ApiAutoModelRefresh.ps1`
5. This TaskSpec, the Phase 4 follow-up record, and a clearly additive blueprint note.

No unrelated backend, frontend, migration, deployment, VPS, credential, pricing, or account-data changes.

## Source facts frozen for this follow-up

Wokey's official API documentation at `https://wokey.ai/docs` was retrieved on 2026-10-09. It says `GET /models` returns text models by default and supports `output_modalities=image`, `video`, or `all`; it documents `/v1/models` compatibility routes. The retained citation is [Wokey API Documentation](https://wokey.ai/docs). This is a current web source, not a versioned repository commit.

The official New API relay routes `GET /v1/models` to its model-list controller; see [QuantumNous/new-api relay-router.go](https://github.com/QuantumNous/new-api/blob/main/router/relay-router.go). The returned list remains subject to the API key's available/allowed models. This implementation does not enable a model upstream or bypass that policy.

## D/I/A and acceptance

- D0: source and intended behavior are explicit; no design choice is delegated.
- I1: bounded service/parser contract plus an existing PowerShell sidecar; no new persistence or schedule.
- A1: review the cross-process contract (catalog provenance -> safe sidecar save) and exact Wokey host boundary.
- Required evidence: focused Go tests for both OpenAI and Grok account platforms on Wokey, exact-origin negative cases, explicit fallback provenance, PowerShell `-SelfTest`, syntax/format and `git diff --check`, live read-only model-list sync for selected YeToken and Wokey accounts after an upgraded local runtime, and independent read-only source/implementation reviews.
- Live sync may retain the native account metadata-enrichment side effect. The discovery endpoint itself issues no generation request; mapping saves may trigger the previously accepted native capability probe described above. Any actual mapping save remains limited to a changed nonempty live model catalog and uses the existing API path.

## Verification progress

- Focused Go tests passed: `TestFetchUpstreamSupportedModelsForWokeyRequestsAllModalities`, `TestWokeyAllModalitiesCatalogQueryIsExactHostScoped`, `TestSyncUpstreamModelCatalogUsesConfiguredModelsWhenListEndpointUnsupported`, and `TestSyncUpstreamModelCatalogPrefersDirectUpstreamMetadata` (each with `go test -count=1 -timeout=180s ./internal/service -run <test-name>`).
- Windows PowerShell 5.1 `-SelfTest` passed, including exact image/video IDs, live-vs-fallback distinction, unchanged live catalogs, aliases/wildcards, and failure skips.
- Full `go test -count=1 -timeout=300s ./internal/service` completed with exit 1. Its only failing test was `TestWokeyTimeOfDayCatalogBuildsExactDualTierCards`; the failure reports unrelated existing missing Wokey image/video pricing-card fixtures. No model-refresh test failed. Do not expand this follow-up to repair that independent price-sync test.
- `go vet ./internal/service` passed; both changed Go files are gofmt-clean; `git diff --check` passed.
- The Windows sidecar's real daily schedule remains at 04:10 after the native 04:00 run; no Task Scheduler definition was changed.
- Full Docker build passed after regenerating the lockfile with Dockerfile-matched pnpm 9.15.9; the frozen install reported `Lockfile is up to date`, then frontend checks/build and backend image build passed. The only lockfile change is four manifest-matching overrides plus the PostCSS importer constraint; `package.json` is unchanged. Independent read-only lockfile audit passed.
- Independent code audit passed. The source review found no mismatch against current Wokey/NewAPI documentation, but does not claim immutable/reproducible source fidelity because those references are mutable.
- Replaced the local 18081 app with `sub2api:local-media-model-refresh-20261009` (image `sha256:b617510c0f47cc33eb48a4dff0bd7ed4cf1d89b45ee666a83ef0328882dd7312`); the prior app container remains stopped for rollback. The new app is healthy and uses the same local data mount, network, port, database, and Redis configuration.
- Ran the existing sidecar once against the completed native 04:00 catalog run: 18 `manual_only` accounts processed, 2 saved (Wokey #27 and #28, 45 model IDs each), 16 unchanged, 0 failed. No user-initiated text/image/video generation test was sent. The native account update for changed OpenAI API-key account #27 may have launched its previously accepted Responses capability probe; that probe's usage/result was outside this catalog acceptance.
- On the upgraded runtime, live sync returned `live_list_available=true` for YeToken #18/#25 and Wokey #27/#28. YeToken #18 returned the exact image IDs `gpt-image-2`, `gpt-image-2.5-flare`, and `gpt-image-2.5-sunburst`; its saved identity mappings match. YeToken #25 returned 13 text models and no media IDs. Wokey #27/#28 each returned 45 IDs, including exact `gpt-image-2.5`, `grok-imagine-video-1.5`, and `grok-imagine-video-1.5-lite`; readback confirms those IDs map to themselves.
- Live sync returned the non-blocking warning `Model IDs were synced, but capability metadata is incomplete.` This is capability metadata, not catalog provenance; it does not invalidate the nonempty live model list. Model listing still does not prove generation availability.
- The existing Windows task remains daily 04:10 after the native 04:00 run; its next run is 2026-10-10 04:10 +08, with last scheduled execution result 0. The manual run verifies the sidecar flow; the next clock-triggered recurrence has not yet occurred.
- No VPS, GitHub, commit, push, route, price, or API key was changed/performed in this follow-up. The two Wokey `model_mapping` values were updated through the existing account API as intended. The previously pushed checkpoint remains `95f96da876159575d142a3cfb028f3f52b5ae6fc`.

## Initial review surface (before runtime acceptance)

- Candidate base remains `95f96da876159575d142a3cfb028f3f52b5ae6fc`.
- Changed code: `backend/internal/service/upstream_models.go`, `backend/internal/service/upstream_models_test.go`, `frontend/pnpm-lock.yaml` (manifest lock consistency only), and `D:\AI\SSH\sub2api-model-refresh\Invoke-Sub2ApiAutoModelRefresh.ps1`.
- Changed docs: this TaskSpec, the additive Phase 4 follow-up section in `D:\AI\SSH\SUB2API-V0213-PHASE4-UPSTREAM-MODEL-REFRESH.md`, and the additive follow-up note at the end of `D:\AI\SSH\SUB2API-V0213-ADAPTATION-BLUEPRINT.md`.
- The pushed checkpoint, `.wokey-pnpm-lock-sync/`, and `backend/dev-docs/account-delete-scheduler-outbox-atomicity.md` are unchanged.

## Known limitation

This can discover only what the upstream model-list endpoint exposes to that account/key. A model not enabled/visible upstream, or omitted from its directory endpoint, cannot be added automatically by this mechanism. A live directory proves the provider listed the model; it does not prove generation succeeds.

The received `D:\AI\SSH\AGENTS.md` and its on-disk copy specify different historical blueprint hashes, and neither equals the current on-disk blueprint hash. The current blueprint and Phase 4 record were reread before this follow-up and agree with this additive scope; the parent directory has no Git history to establish the older text. This acceptance does not claim that missing historical diff.
