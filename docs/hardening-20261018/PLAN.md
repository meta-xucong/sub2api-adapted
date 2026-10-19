# Sub2API cloud hardening plan

Fork baseline: `2bedb713b6b0b9cf968468decae36732f390574a`.
Official comparison: `b0a02a3304a0a78cdd003ddda1dd23ba02c03084`.
Branch: `dot/gateway-hardening-20261018`.

## Scope rule

The user's latest instruction is authoritative: preserve official Sub2API architecture and behavior; prefer official fixes. A repair of official-origin code requires a demonstrated uncovered defect and credible public discussion/advisory. Fork-added features can be repaired based on independent source audit and reproduction. Restored snapshot history is not proof that a file is fork-added. See UPSTREAM_PROVENANCE.md for exact provenance and decisions.

## Retained implementation

1. Actual-image accounting: finish the official actual-artifact-counting intent, with strict string artifacts and zero-image billing protection. Explicit token prices retain observed token charges. Do not change official OAuth empty-output or post-header retry policy.
2. Fork image deadline: respect the absolute request deadline even before timer cancellation is published, preserving independent attempt timeout retries while total budget remains. This is a correction to the fork-only existing total-timeout guards, not a new official retry policy.
3. Grok/Wokey compatibility: resolve the independently reproduced explicit-resolution billing mismatch; preserve required multipart fields only in the fork's Wokey image-edit adapter. Actual output dimensions remain authoritative.
4. Stream integrity: adapt public upstream terminal/accounting/cancellation fixes to affected CC and Anthropic-to-CC adapters. Never claim success after error/truncation; retain metered usage exactly once without replay after output. Use existing handler cancellation/health conventions. Narrow bounded reads must not alter unrelated native Responses behavior.
5. Untrusted image downloads: use the official socket-pinning algorithm already present in the monitor, adapted to the shared client without import cycles or a monitor rewrite. Validate every new connection and redirect, preserve TLS Host/SNI, reject unsafe/mixed addresses, and fail explicitly where a proxy prevents strict destination enforcement. Byte and image geometry limits remain bounded.
6. Avatar decoder allocation: use Go's documented DecodeConfig-before-Decode pattern before existing compression. Preserve existing output/compression and small-avatar behavior. Exact limits are a local conservative resource policy, not an official Sub2API constant.
7. Dependencies/validation: port official Vue/source-map-js updates; apply published x/image and SheetJS fixes with verified source/integrity; narrowly remediate critical/high development-tool advisories with compatibility tests. Repair lint toolchain incompatibility, never suppress gates.

## Deferred, not fixed

- New custom-voice durable ownership schema and realtime protocol: no matching credible public isolation repair found, and these routes are official-origin. Experimental patch is outside the candidate. Shared-account custom-voice isolation remains a risk.
- Forwarded-IP default/migration changes: official intentionally retains compatibility. Use its existing secure configuration operationally; verify effective persisted setting and trusted peer list. No live settings changed here.
- Broad empty-image outcome/error rewriting and no-replay policy: conflicts with deliberate merged official retry behavior. Do not claim this risk eliminated.
- New global/total Grok media timeout: conflicts with official long-media compatibility. Existing timeout policy retained.
- Real provider success, live billing, VPS resource acceptance and deployment remain separate.

## Work and review sequence

- Source provenance and independent reproductions establish each retained change.
- Implementation is scoped by files, then independently cross-reviewed.
- Run focused regressions followed by service/handler/repository and full feasible suites, frontend tests/build, dependency audit and lint on the final snapshot.
- Keep failed attempts, skipped environment-dependent tests and remaining advisories visible.
- Final delivery identifies fixed commit/base and synchronization material. No merge, VPS deployment, production migration, real paid generation or credential access is performed.
