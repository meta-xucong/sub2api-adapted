# Veyra debit audit and minimal repair

Date: 2026-10-19 UTC.

## Reviewed identities and scope

- Upstream PR: https://github.com/meta-xucong/sub2api-adapted/pull/12
- Fixed reviewed head: `a9157b962616f7ca1dc07d8b95666edebf8a716b`.
- PR base: `2bedb713b6b0b9cf968468decae36732f390574a`.
- Candidate starts with the exact PR delta applied to cloud commit
  `079e766020e45d350776a40f91302cd1254973c3`.
- This repair changes debit canonicalization and generic idempotency cleanup,
  with unit and real-PostgreSQL regressions. It does not change usage ownership,
  caller identity, provider pricing, deployment switches, or gateway billing.
- Findings below refer to line numbers in the fixed upstream PR, before repairs.

## Verified code findings

### P1: successful monetary receipt expires and permits another debit

`backend/internal/repository/user_repo.go:1095,1137` gives the only durable debit
receipt a 30-day expiry. `idempotency_repo.go:217-231` deletes every expired scope.
After cleanup, retrying the same successful key reaches the balance UPDATE again.
The original response or the caller's local receipt can be lost, so a later task
recovery must still be safe. No bounded retry window is enforced by this endpoint.

Reproduction sequence: debit key K; expire its receipt; run normal cleanup; retry
the identical request. The original implementation charges twice and returns a
new non-replayed receipt. This is a source-proven defect; execution results must
be recorded separately after tests actually run.

Native usage billing already preserves deduplication through an archive:
`usage_billing_repo.go:94-104` and
`dashboard_aggregation_repo.go:349-365`. Reusing that table directly would require
new API-key identity and response-replay semantics, so it is not a minimal repair.

Chosen repair:

1. Name the existing `veyra.billing.debit` scope with one repository constant.
2. Exclude that scope from generic expiration selection, before ORDER BY/LIMIT.
3. Preserve the existing transactional claim, debit, and completed receipt.

The transaction makes unsuccessful claims roll back. Normal operation does not
leave committed partial claims needing a TTL-based takeover. Successful receipts
are now intentionally retained financial records, one per successful debit.
The existing expires_at value is retained for schema compatibility but does not
authorize deleting this scope. This is durable record growth, not a cache leak.
Operators must account for its storage and cleanup-scan cost. Any future archive
must preserve atomic claim and replay/conflict semantics before deleting receipts.
Generic cleanup still expires every other scope normally.

### P2: amount fingerprint and persisted debit disagree at fractional boundaries

`backend/internal/veyra/billing.go:195-196` formats the fingerprint at eight decimal
places, while `user_repo.go:1115-1128` subtracts and returns the original float.
`users.balance` is NUMERIC(20,8). An accepted positive `0.000000004` debit can leave
the stored balance unchanged; different raw amounts can share the rounded
fingerprint while returning an unrounded receipt.

Chosen repair: reuse `service.QuantizeUsageBillingAmount`, the existing native
eight-place, decimal half-away-from-zero helper. Normalize at the bridge boundary
before positivity/finiteness validation and fingerprinting, and enforce the same
idempotent normalization at the repository boundary for direct callers. The SQL
guard, subtraction, and receipt all use that canonical amount. Reject a value
that rounds to zero. No new monetary helper, rate rule, or amount ceiling is added.

Canonical-equivalent raw inputs now intentionally replay the same canonical
debit. For example, `1.000000004` and `1` both mean a one-unit debit. `1.000000006`
means `1.00000001` and conflicts with that key. `0.000000005` becomes one minimum
unit; `0.000000004` is rejected.

The helper explicitly preserves NaN/infinity, which validation rejects. It uses
decimal.NewFromFloat and decimal rounding rather than multiply/divide rounding.
The wire format remains float64, so JSON numbers have already lost precision
beyond float64's representable values. This repair does not claim arbitrary
precision at very large magnitudes. A decimal-string wire contract would require
a separate compatibility change. PostgreSQL still guards insufficient balance;
SQL failures roll back the claim and debit together.

References:
- https://www.postgresql.org/docs/current/datatype-numeric.html
- `backend/internal/service/usage_billing.go:60-102`.

## Confirmed boundaries retained

- The production route passes UserService, which implements the atomic debit
  interface; it does not execute the memory-ledger balance mutation fallback.
- HTTP debit callers cannot supply RequestFingerprint. The bridge constructs it
  from key, user, canonical amount, source, and reference. No current cross-user
  exploit was established from the repository's optional fingerprint fallback.
- The atomic UPDATE still requires the correct user, active status, non-deletion,
  and enough balance. PostgreSQL rechecks a concurrent UPDATE's condition against
  the updated row under its default Read Committed isolation.
- The usage lookup remains read-only and exact-user/exact-request scoped. There
  is no relaxation of that filter or invented cross-service identity mapping.
- Native gateway usage/context handling and its archival dedup remain unchanged.
- Existing primary-branch avatar defenses and other integration fixes must remain
  in the candidate; integrating a whole older snapshot is not permitted.

Concurrency reference: https://www.postgresql.org/docs/current/transaction-iso.html

## Tests added or extended

Unit tests cover:
- Canonical amount reaching the atomic service, SQL, and stored receipt.
- Canonical-equivalent replay and distinct canonical-amount conflict.
- Positive half-unit rounding and zero-after-round rejection.
- NaN/infinity rejection before transaction/mutation.
- User/source/reference fingerprint binding.
- Cleanup's monetary-scope exclusion before its batch limit and default batching.

New real PostgreSQL tests in
`backend/internal/repository/user_repo_veyra_debit_integration_test.go`:

1. `TestVeyraDebitPostgresExpiredReceiptSurvivesCleanupAndReplays`: an expired
   monetary receipt survives cleanup; a newer ordinary expired record is still
   removed with batch size one; a fresh repository/ledger replays without charging.
2. `TestVeyraDebitPostgresConcurrentSameKeyChargesOnce`: sixteen simultaneous
   attempts yield one debit, fifteen replays, and one durable receipt.
3. `TestVeyraDebitPostgresConcurrentDifferentKeysCannotOverdraw`: two seven-unit
   attempts against ten units yield one success and one insufficient-balance
   result; the failed claim rolls back.
4. `TestVeyraDebitPostgresCanonicalPrecisionAndReplay`: three fractional boundary
   cases verify exact stored NUMERIC text, replay, and canonical-amount conflict.
5. `TestVeyraDebitPostgresDirectRepositoryCanonicalizesAndRejectsZero`: direct
   callers get canonical SQL/receipts; a zero-after-round attempt leaves no claim.
6. `TestVeyraDebitPostgresFailedDebitRollsBackClaimAndCanRetry`: insufficient funds
   leave no receipt; the same key succeeds after a synthetic fixture top-up.

The tests use isolated integrationDB/testEntClient fixtures, users, and the existing
idempotency table. They do not call providers, Redis, deployed services, or real
payment systems. Exact stored balances are compared as NUMERIC text rather than
with a floating-point tolerance.

Suggested focused selections, only after the execution lane is authorized:

- `go test ./internal/veyra -run 'TestDebitBalance'`
- `go test ./internal/repository -run 'TestUserRepositoryDebitBalanceIfSufficient|TestIdempotencyRepoDeleteExpired'`
- `go test -tags integration ./internal/repository -run '^TestVeyraDebitPostgres'`

The normal integration harness needs disposable PostgreSQL and Redis containers.
A separate approved loopback PostgreSQL-only bootstrap may run this selected
subset because these tests do not access Redis. A missing Docker skip is not
evidence that real PostgreSQL tests passed.

## Separate interoperability caveats, not deployed findings

The inspected local Video OS snapshot `44da8428d2c1b73648eb1616cd0350dac798eba8`
uses one worker-level SUB2API_VIDEO_API_KEY for provider calls but sends the
logged-in customer's external_user_id to usage lookup. Native usage belongs to
the API-key owner. If these identities differ, usage-priced billing cannot find
that customer's native row. This is evidence about that local snapshot only;
it is not proof about the current deployed Video OS. Fixed-tier billing bypasses
the usage lookup. Verify the current integration contract rather than weakening
the ownership filter or inventing impersonation behavior.

The same local contract treats Veyra debit as an extra service fee, not a second
charge of native actual_cost. The read-only lookup itself cannot double-charge.

A pre-existing native recovery caveat is also separate: a failed native bill may
write actual_cost=0, while usage-log insertion later uses ON CONFLICT DO NOTHING.
The bridge will reject that row even if later billing succeeds. This repair does
not modify that producer/reconciliation contract. Cache invalidation after debit
exists; auth invalidation still uses the request context, whereas Redis balance
invalidation is detached. These concerns need their own targeted validation.

## Validation status

At initial implementation handoff, no compiler, unit/integration test, database,
provider, deployment, commit, or push has been run by this repair worker. Source
formatting and diff inspection are not test passes. Append exact validation
results and the tested candidate identity when the authorized lane completes.
