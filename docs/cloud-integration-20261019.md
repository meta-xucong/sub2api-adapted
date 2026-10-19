# Cloud integration acceptance — 2026-10-19

## Decision and scope

This is a cumulative review candidate based on `main` at `2bedb713b6b0b9cf968468decae36732f390574a`, incorporating the previously reviewed cloud repairs and Veyra PR #12 at `a9157b962616f7ca1dc07d8b95666edebf8a716b` with its integration fixes. It is for a **Draft PR**, not production enablement or deployment approval.

The current implementation follows the existing SmartRouter cooldown and health model. The separate `image_poll_timeout` opt-in switch is removed. Healthy eligible routes and bounded waits retain priority; when normal options are exhausted, the user-requested bounded last resort may probe ordinary cooling routes. Existing account restrictions and request retry limits still apply. Recovery leases are process-local, not a distributed exactly-once guarantee.

Veyra remains disabled by default. Its integration preserves portal return flows while repairing ticket replay, concurrent token validation, canonical debit amounts and durable debit receipts. Shared Redis consumption and generic receipt cleanup also change; default-off does not imply those shared paths are unchanged.

Product scope is frozen. No new routing engine, provider contract, distributed scheduler, broad upstream cleanup or production configuration change is included in this final acceptance pass.

## Source-bound local evidence

Application checkpoint: `62b687a2ca3db313d5eddeb4784c97e93da2efab`, tree `b4a29da36d1f88412f08ea5e67a1fa2f7768b6f5`. The later delivery change is limited to the separately reviewed browser CI harness and documentation. Connector-created commit metadata may produce a different commit SHA; exact Git tree equality is required before publication is reported successful.

Validation is **composite**, not a claim that every command ran on one final commit:

- Full ordinary and integration-tag suites passed on checkpoint `2096f94c`; changed service tests were subsequently replaced with fresh passing executions. Exact source/object comparisons preserve unaffected coverage. Three named Unix-socket broker tests were excluded because the cloud sandbox cannot provide their transport.
- Full service ordinary tests passed on `81022e64`; all 37 top-level tests in the changed first-output/lifecycle files also passed under integration tags. The expanded service race selector passed, replacing its earlier two fixture-race failures. The other six race packages passed on the unchanged source components.
- All 79 top-level tests in the four final unit-tagged cleanup files passed in ordinary and race modes on `62b687a2`. Their source changes do not affect production or integration-tag inputs.
- Default full-tree Go lint passed on the equivalent production scope. Supplemental unit-tagged service lint found 229 test findings; 219 map to unchanged lines in the publication base, and the 10 introduced findings were corrected. Final publication-diff unit-tagged service lint passed with **zero new-line findings**. This is not a full tagged-lint zero-warning claim. The historical baseline was classified by source provenance, not rerun under that lint configuration.
- Frontend: 347 test files / 2,702 tests passed, zero failures/skips; typecheck and affected-file lint passed. These ran on `b7c173f9`; the frontend and both portal component trees are byte-identical in the application checkpoint. A fresh production build produced 181 embedded assets, all checked against the backend build inputs.
- Isolated native PostgreSQL: 21 top-level tests and 25 nested test terminal passes, zero failures/skips, including durable debit replay, concurrency, balance isolation, precision and rollback. This ran on `d57bbd81`; the tested production/database components are unchanged. It does not turn the Docker-dependent repository TestMain early exit into a pass. The new account-qualified image-health identity has not been validated end to end against PostgreSQL by these selected tests.
- Final embedded Go build passed on clean `62b687a2`, with `vcs.modified=false`. Binary SHA-256: `40077eec5e86d0b9c641a1dffa0dd8480cdfb47757808a3271153e2839ea58e6`. The binary is not part of the source PR.
- Authentication controls: the three unmodified positive tests passed under the race detector. Restoring the old ticket implementation produced the exact two TTL/replay assertion failures; restoring the captured-token mutation produced the expected race-detector failure. These expected failures are separate negative evidence, not failed production tests. The original ticket-control wrapper misclassified the wording of an expected assertion; its raw receipt is preserved alongside an independent reconciliation, without rerunning or relabeling it.

Skip inventories remain explicit: the aggregate ordinary suite had 18 skips and integration had 22, unchanged from the earlier checkpoint. They include environment-dependent tests, a historical fixture limitation and an intentional sentinel; not all are environment skips. Test groups overlap. Raw terminal events include parent tests and subtests and must not be summed as independent cases. The earlier name-prefix “leaf” summaries are not an exact independent-case count.

## Browser and CI gate

The cloud Chromium attempt failed before browser assertions because the environment denied a required socket operation: **0/22 cases executed**, not a browser pass. The reviewed PR-only workflow runs those 22 local-only cases on GitHub's runner, checks out the exact PR head, uses synthetic fixtures and intercepted external requests, and uploads its receipts. Runtime browser acceptance and existing repository CI are pending the newly published commit.

The local harness uses the real security middleware and portal routes. Its disabled-mode SPA sentinel is not full Vue rendering, and mocked callback reception is not real cross-product SSO or Provider acceptance.

## Release constraints

1. Verify and adopt the Alchemy consumer intent check before enabling cross-product tickets. The inspected base exchanged a bare ticket; Alchemy PR #36 proposed explicit `alchemy` intent and rejection of missing/wrong returned intent. Verify the deployed consumer, and separately check Video's intent, account and Origin contracts. Do not weaken ownership filtering to accommodate a shared provider account.
2. Every cleanup writer, including a rollback build, must retain the `veyra.billing.debit` cleanup exemption. An older active cleanup process can still delete expired financial receipts; a backup alone does not stop that. Already-deleted receipts are not reconstructed by this change.
3. The internal bridge credential remains privileged. Enabling portal root ownership and access by active authenticated users requires a deliberate deployment decision and secure credential provisioning.
4. Synthetic tests do not establish real text/image/video generation, production billing reconciliation, VPS resource capacity, network configuration or migration acceptance. Retry after an upstream timeout can duplicate upstream work/cost even when local charging is idempotent.
5. Existing background quota refresh uses its existing asynchronous lifecycle; this work fixes demonstrated test synchronization races without claiming a new global shutdown/join or distributed execution guarantee.

Older phase documents are historical receipts for their stated checkpoints. This document governs the current handoff; the new PR's exact-head CI result governs CI status. No merge, release, production enablement or VPS deployment is authorized by these local results.
