Fresh re-audit: see REAUDIT_20261018.md for six additional repairs and new verification receipts. Existing release gates below remain in force.

# Acceptance and synchronization

Status: **REVIEW CANDIDATE; NOT READY FOR MERGE OR PRODUCTION**. Final uncapped lint exits 1 with 107 baseline-attributed entries; the full frontend development-tree audit retains one high-severity finding. Docker/database paths remain unverified. Public GitHub publication is pending approval. See ACCEPTANCE.md for exact receipts, skips and source identity; baseline lint attribution is static, not a historical lint rerun.

## Candidate acceptance

1. Review final diff against fork baseline `2bedb713b6b0b9cf968468decae36732f390574a`; consult UPSTREAM_PROVENANCE.md before treating any change as an official port.
2. Use an isolated local worktree if there are existing edits. Never reset/clean unrelated local changes. Verify exact candidate SHA after applying the provided patch/bundle or approved GitHub branch. On Windows with autocrlf, compare Git blob identity for source fidelity rather than hashing CRLF working-tree bytes against LF source snapshots. Artifact bundle/ZIP checksums still cover their exact downloaded bytes.
3. Run backend unit/integration, frontend typecheck/build/critical tests, vulnerability audit and lint on the candidate SHA. Mock tests do not prove real provider compatibility. Record skips, failed attempts and residual findings accurately.
4. Confirm actual-image and explicit-token accounting with test accounts before production use; do not alter historical balances or usage rows.
5. Verify long streaming, client disconnect, idle timeout and partial/error terminal behavior with staging clients. Clients must not blindly replay after output.
6. Inventory strict image download proxies, signed URLs and resolutions. Strict downloads fail when a proxy cannot enforce the destination; no silent direct fallback. NAT64/6to4 and other special-use ranges are deliberately excluded. Default stored-image dimensions are capped at 16 megapixels. Confirm enabled providers fit.
7. Avatar compression now rejects excessive decoded dimensions before allocating pixels (4096 per side, 4 megapixels); the prior 100 KiB byte cap, compression algorithm and small-avatar behavior remain. This is not an aggregate concurrency memory cap.
8. Verify effective persisted forwarded-IP mode, trusted peers and origin restrictions. Official compatibility defaults are preserved. To use trusted-proxy-based resolution, configure the existing switch false, exact directly connected proxy addresses, and validate forged-header behavior. YAML alone may not override a persisted setting.
9. Shared-account custom-voice isolation remains unresolved and is not certified by this patch. The proposed schema/protocol was explicitly deferred. Restricting such endpoints operationally requires a separate approved change.
10. Check live disk headroom, image/WAL growth and rollback backups. No live production capacity measurement was performed in this task.

## Deployment and rollback boundaries

No deployment or migration occurs in this task. Before a later rollout, retain previous image/config backups and record SHA, image digest, health, billing reconciliation and resource metrics. Health alone is not correctness evidence.

This final candidate has no custom-voice ownership migration or new voice protocol. Do not accidentally apply the separately archived experimental voice or IP-default patches. Official image retry and long-media timeout policies remain, with their documented residual risks.

Rollback should use the recorded previous image/config and preserve existing data. Do not weaken security gates or reinstate vulnerable dependencies simply to report green CI. No blanket claim of complete vulnerability elimination or production readiness is made.

## Resource claims deliberately excluded

Read-idle/cancellation deadlines do not establish an aggregate stream-memory cap. Existing maximum SSE line configuration can fall back to 500 MiB, and accumulated tool/content output remains an independent resource risk. This patch does not redesign official buffering or change that default. Review existing supported limits against actual VPS memory before deployment; do not describe this candidate as complete OOM protection.

Image accounting here counts structurally valid, non-empty string artifact fields. It does not certify that every provider-returned URL stays reachable or that arbitrary base64 content decodes to a visually correct image. The separate storage downloader validates its own fetched image path; live provider output and ledger reconciliation remain acceptance requirements.
