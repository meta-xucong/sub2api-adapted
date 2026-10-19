# Remaining frontend audit findings

Dependency paths identify installed-tree inclusion, not proof that a vulnerable function is invoked or shipped in the browser bundle. Development findings remain relevant to developer machines and CI.

Audit counts: {"info": 0, "low": 1, "moderate": 4, "high": 1, "critical": 0}

- braces 3.0.3 (high): [GHSA-vfj7-8cjw-p6xm](https://github.com/advisories/GHSA-vfj7-8cjw-p6xm); patched range no published fix.
  - Development tree: tailwindcss → chokidar → braces
- @vitest/mocker 3.2.6 (moderate): [GHSA-82fw-gwwq-j7x9](https://github.com/advisories/GHSA-82fw-gwwq-j7x9); patched range >=4.1.11.
  - Development tree: vitest → @vitest/mocker
- decode-uri-component 0.4.1 (moderate): [GHSA-vcc3-ghjq-m6fr](https://github.com/advisories/GHSA-vcc3-ghjq-m6fr); patched range >=0.5.0.
  - Production tree: @lobehub/icons → @lobehub/ui → query-string → decode-uri-component
- postcss-selector-parser 6.1.3 (moderate): [GHSA-rj75-hqrm-r3gf](https://github.com/advisories/GHSA-rj75-hqrm-r3gf); patched range >=7.1.6.
  - Development tree: eslint-plugin-vue → postcss-selector-parser
- vitest 3.2.6 (moderate): [GHSA-82fw-gwwq-j7x9](https://github.com/advisories/GHSA-82fw-gwwq-j7x9); patched range >=4.1.11.
  - Development tree: vitest
- katex 0.16.45 (low): [GHSA-238p-pmpm-9mq7](https://github.com/advisories/GHSA-238p-pmpm-9mq7); patched range >=0.18.2.
  - Production tree: @lobehub/icons → @lobehub/ui → katex

## Exposure and deployment relevance

- `braces`: crafted, deeply nested glob/brace patterns can exhaust a Node.js stack. This tree reaches it through Tailwind/chokidar and fast-glob/micromatch in build/test tooling, not the Go request handlers. Keep build inputs/configuration trusted and untrusted pull-request execution isolated. There is no published patched version; no exception is created or extended, and the full high-severity audit remains failing. The [maintainer’s October 5 discussion](https://github.com/micromatch/braces/issues/70) disputes the practical attack assumptions and points to `maxLength` and restrictions on untrusted glob patterns. This dispute does not erase the reported advisory or supply a patched release. Attacker-controlled pattern reachability in this project has not been demonstrated.
- `vitest` / `@vitest/mocker`: the remaining moderate advisory concerns redirect mocks in a reachable development-server WebSocket, especially third-party use of the standalone mocker/interceptor plugin. This repository uses headless `vitest run` with no browser/API server or standalone mocker plugin configured. Version 4.1.11 requires a separate major-version compatibility review; do not expose a test server publicly.
- `postcss-selector-parser`: the remaining finding involves excessive work while parsing attacker-controlled CSS selectors. Here it is reached by ESLint's Vue parser during development/CI. The moderate fix requires 7.1.6, outside this bounded major-compatible patch; the separate low advisory was resolved with 6.1.3. Treat untrusted source processing as an isolated CI job.
- `decode-uri-component`: excessive CPU use is possible if malformed URL/query text reaches this decoder. The required 0.5.0 changes the pre-1.0 compatibility line; this patch does not force that override.
- `katex`: exploitation additionally requires pre-existing prototype pollution or attacker influence over the options prototype, attacker-controlled math input, and insertion of rendered HTML without an independent sanitizer. The required 0.18.2 changes the pre-1.0 compatibility line; this patch does not force it.

The latter two packages occur under `@lobehub/icons` → `@lobehub/ui` in the production dependency tree. Source inspection found no application import of `@lobehub/icons`; `ModelIcon.vue` documents that its SVG paths were extracted from those files. That narrows observed reachability but is not a blanket claim that every possible build or downstream consumer is unaffected. Removing unused dependency subtrees is a separate cleanup, not performed in this security patch.

The production report therefore contains 0 critical, 0 high, 1 moderate and 1 low finding. The full report contains 0 critical, 1 high, 4 moderate and 1 low finding. These are npm advisory observations, not proof of exploitability or an all-severity security clearance.
