# Frontend dependency security updates

## Upstream provenance

Compared with `upstream-audit/main` at `b0a02a3304a0a78cdd003ddda1dd23ba02c03084` from the official `Wei-Shaw/sub2api` repository. [Upstream fix 0f9d460dc65d2509107aff542459e18677e06291](https://github.com/Wei-Shaw/sub2api/commit/0f9d460dc65d2509107aff542459e18677e06291) supplies the Vue and source-map-js remediation adopted here. That commit renews SheetJS exceptions temporarily and explicitly names migration to the SheetJS CDN build as the subsequent mitigation. This patch performs that migration with verified integrity and removes the resolved exceptions. Upstream still carries the development-tool versions listed in the pre-change audit; their published advisories justify the bounded follow-on updates below.

## Bounded changes

- Adopt upstream Vue `^3.5.43` (locked `3.5.43`), exceeding the stable patched floor `3.5.42` for [GHSA-g2v6-rqmx-r4w6](https://github.com/advisories/GHSA-g2v6-rqmx-r4w6). Update its matching compiler/runtime/server-renderer packages together.
- Adopt upstream's `source-map-js@<1.2.2` override to `>=1.2.2` (locked `1.2.2`) for [GHSA-68fv-2mgg-jv7q](https://github.com/advisories/GHSA-68fv-2mgg-jv7q).
- Replace the obsolete npm `xlsx@0.18.5` with the pinned official SheetJS Community Edition `0.20.3` tarball. Keep the existing dynamic import, workbook generation and `.xlsx` download behavior unchanged.
- Regenerate the existing v9 lockfile with pnpm `9.15.9`, matching CI's pnpm major. Do not suppress audit findings, extend exception expirations, or change audit policy.

## SheetJS source and integrity

[Official installation instructions](https://docs.sheetjs.com/docs/getting-started/installation/nodejs/) identify the SheetJS CDN as the authoritative source because the npm registry package is outdated. The package URL is:

`https://cdn.sheetjs.com/xlsx-0.20.3/xlsx-0.20.3.tgz`

The manifest pins this exact URL. pnpm 9 did not populate integrity for this remote tarball automatically, so its independently calculated SHA-512 was explicitly added to the lock resolution. A fresh-store frozen install verified that checksum against a new download from the official CDN. A regression test requires this verified checksum to remain present. It caught pnpm dropping the SRI during a later non-frozen lock regeneration; the verified SRI was restored after the final regeneration, then the exact tree was frozen and revalidated. Future dependency updates must preserve this integrity field. No third-party mirror or TLS bypass is used. Installation requires access to the official CDN; an internal cache must preserve the verified bytes and license rather than substitute the old npm package.

Verified archive metadata: package `xlsx`, version `0.20.3`, license `Apache-2.0`; the archive includes the complete `package/LICENSE` and bundled `package/dist/LICENSE` notices.

- SHA-256: `8dc73fc3b00203e72d176e85b50938627c7b086e607c682e8d3c22c02bb99fe8`
- SHA-512 SRI: `sha512-oLDq3jw7AcLqKWH2AhCpVTZl8mf6X2YReP+Neh0SJUzV/BdZYjth94tG5toiMB1PPrYtxOCfaoUCkvtuH+3AJA==`

This version exceeds both patched floors: `0.19.3` for [CVE-2023-30533](https://github.com/advisories/GHSA-4r6h-8v6p-xvw6) and `0.20.2` for [CVE-2024-22363](https://github.com/advisories/GHSA-5pgg-2g8v-p4x9). The application exports trusted-schema rows and does not import user workbooks; dependency patching still replaces the expired exceptions.

## Regression coverage

`frontend/src/utils/__tests__/dependencySecurity.spec.ts` exercises the actual libraries:

- The official SheetJS archive URL, version and SHA-512 remain pinned in the lockfile.
- Vue SSR rejects carriage-return attribute-name injection while preserving a normal attribute.
- Dynamic SheetJS loading, header creation, paged row append, workbook creation, and array-buffer export round-trip Unicode text, numbers, decimal text, and a formula-looking string as a literal cell.

Existing admin usage-view tests separately exercise export controls and column selection. They mock SheetJS; the new test verifies the actual upgraded library APIs.

## Verification

Final frozen-tree validation passed on Node 20.20.2 / pnpm 9.15.9: frozen install, lint, typecheck, critical suite (22 files / 311 tests), full suite (344 files / 2,657 tests), and production build. The 30 stdlib audit-checker tests also pass after normalizing the edited checker to LF; ordinary `git diff --check` passes. Existing large-chunk and test-stub warnings remain nonfatal. Baseline production audit reported 4 high, 13 moderate and 4 low findings; the final report contains 0 critical/high, 1 moderate and 1 low. Baseline full-tree audit reported 3 critical, 37 high, 31 moderate and 5 low; the final report contains 0 critical, 1 high, 4 moderate and 1 low. The production high/critical gate passes with an empty exception list, while the full-tree high gate correctly fails on unpatched braces. pnpm 9 emits nonzero status for the remaining lower-severity findings even with `--audit-level high`; the JSON report plus strict policy checker determines the production gate. This is not an all-severity clean audit.

## Bounded development-tool follow-on

Published advisories justify retaining this small divergence from upstream's older tooling:

- Vitest and `@vitest/coverage-v8` move together to `3.2.6` for [GHSA-5xrq-8626-4rwp](https://github.com/advisories/GHSA-5xrq-8626-4rwp). The documented attack conditions concern exposed API/UI servers or Windows UI/browser mode; this repository's CI uses headless Linux `vitest run`, without API/UI configuration. That narrower current exposure does not make the dependency finding disappear.
- Vite moves to `6.4.3` for [GHSA-fx2h-pf6j-xcff](https://github.com/advisories/GHSA-fx2h-pf6j-xcff). Existing plugin-vue `5.2.x` explicitly supports Vite 6. The advisory concerns network-exposed Windows development servers; production serves the built frontend rather than the Vite development server.
- A constrained `tinypool@<2.1.2` override selects `2.1.2`, covering [worker-option pollution](https://github.com/advisories/GHSA-5gmw-xhrv-c9v3) and [run-option filename pollution](https://github.com/advisories/GHSA-85c8-ppgw-ccpr). These gadgets require an existing prototype-pollution primitive in the test/build process. Vitest 3.2.6 normally requests `^1.1.1`, so this cross-major override was reviewed explicitly: 2.x drops Node 18; exported types/default and named classes remain compatible, with additive options only. Node 20.20.2 smoke tests verified repeated isolated tasks, run options, constructor and destruction in both `worker_threads` and `child_process` modes. The manifest states the resulting Node 20 or 22+ engine requirement.

The existing application test selection, assertions and coverage configuration are preserved. The audit checker receives a separately justified fail-closed validation repair described below. Full suite, critical suite, typecheck, lint and production build are required on the final dependency tree; no weakened tests or new audit exceptions are used to accept a tooling upgrade.

## Targeted transitive security floors

Use scoped pnpm overrides rather than a broad recursive dependency update. Apart from the explicitly named tools above, direct packages retain their prior versions; DOMPurify is the additional direct security patch to 3.4.16. Transitive overrides stay on the affected major line except the explicitly reviewed tinypool bridge. Lock churn includes peer-resolution identities, Rollup's matching optional binaries, and dependencies required by the patched Mermaid/Browserslist versions.

| Package | Selected version | Affected path |
| --- | --- | --- |
| lodash | 4.18.1 (already used elsewhere in production tree) | ESLint → Vue parser |
| minimatch | 3.1.4 / 9.0.7 | ESLint; Vue test-utils → js-beautify |
| brace-expansion | 1.1.21; minimatch 9's own dependency 5.0.12 | minimatch |
| browserslist | 4.28.7 | Autoprefixer |
| flatted | 3.4.2 | ESLint → file-entry-cache |
| js-yaml | 4.3.2 | ESLint |
| ws | 8.21.0 | jsdom |
| rollup | 4.59.0 | Vite |
| picomatch | 2.3.2 / 4.0.4 | build/test globbing |
| DOMPurify | 3.4.16 | direct sanitization; UI dependencies |
| Mermaid | 11.16.1 | @lobehub/icons → @lobehub/ui |
| uuid | 11.1.1 / 13.0.1 | Mermaid / @lobehub/ui |
| yaml | 1.10.3 | @lobehub/icons → Emotion → cosmiconfig |
| colord | 2.9.4 | @lobehub/ui → leva |
| ajv | 6.14.0 | ESLint |
| postcss-selector-parser | 6.1.3 | ESLint Vue plugin |

`minimatch@9.0.7` itself declares `brace-expansion@^5.0.2`; its normal dependency resolution selects 5.0.12 and balanced-match 4.0.4. This is not a manually forced cross-major brace-expansion override. Their Node 20 requirements are covered by the same Node 20 verification.

All six old exception entries are removed because their installed affected versions/advisories are resolved. No new exception or expiration extension is introduced. Exact resolved advisory identifiers are listed in [FRONTEND_RESOLVED_ADVISORIES.md](FRONTEND_RESOLVED_ADVISORIES.md). Exact unresolved advisories and practical exposure are listed in [FRONTEND_REMAINING_ADVISORIES.md](FRONTEND_REMAINING_ADVISORIES.md).

## Audit-report validation gap

The original checker is byte-identical to official `upstream-audit/main`; this change is a locally reproduced uncovered gap, not a port of an upstream fix. Its CI command intentionally captures `pnpm audit` output despite nonzero status so the exception policy can evaluate findings. Actual pnpm 9.15.9 output against an unavailable local registry is an error object (`ECONNREFUSED`), which the original checker incorrectly accepted as an empty report. [pnpm audit documentation](https://pnpm.io/cli/audit) distinguishes registry failures from vulnerability results, and [official issue 13033](https://github.com/pnpm/pnpm/issues/13033) records real registry-error scenarios.

The minimal repair validates the report before existing high/critical exception matching: error objects, unknown/malformed shapes, missing or invalid severity metadata, and malformed advisory fields fail closed. Boolean values do not count as integer totals. Positive severe totals require a corresponding severe record; advisory totals are not naively equated with dependency-path counts. Valid pnpm 9 reports and supported alternate report shapes remain accepted. Standard-library unit tests run in the security workflow before the checker. Exception severity, expiration and matching policy are unchanged.

## Reproduction

Use Node 20 (verified with 20.20.2) and pnpm 9 (verified with 9.15.9), matching CI's supported major versions. A resource-constrained runner may set `VITEST_MAX_THREADS=2 VITEST_MIN_THREADS=1 VITEST_MAX_FORKS=2 VITEST_MIN_FORKS=1`; this limits concurrency, not test selection or assertions.

```sh
pnpm --dir frontend install --frozen-lockfile
pnpm --dir frontend run lint:check
pnpm --dir frontend run typecheck
make test-frontend-critical
pnpm --dir frontend run test:run
pnpm --dir frontend run build
python3 -m unittest discover -s tools -p 'test_check_pnpm_audit_exceptions.py' -v
# Preserve audit's status and JSON; a nonzero exit can mean real findings or a failed audit.
pnpm --dir frontend audit --prod --audit-level high --json > /tmp/sub2api-audit-prod.json
python3 tools/check_pnpm_audit_exceptions.py --audit /tmp/sub2api-audit-prod.json --exceptions .github/audit-exceptions.yml
pnpm --dir frontend audit --json > /tmp/sub2api-audit-full.json
python3 tools/check_pnpm_audit_exceptions.py --audit /tmp/sub2api-audit-full.json --exceptions .github/audit-exceptions.yml
```

The last full-tree checker command is expected to fail on the unresolved braces advisory. A production pass is scoped to high/critical advisories and a structurally valid report. Do not discard or relabel registry errors as an empty successful audit.

Verified manifest SHA-256: `1e609b910a10d9bf308d56448a808172b39415f4442465bdb39d0d38565ee798`. Verified lockfile SHA-256: `411b24e06e809482c5feb183f348151802e2fe22d1b91b6d092882378bd252c8`. Both were checked before and after the complete final validation run.
