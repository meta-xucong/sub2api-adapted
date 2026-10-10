# Account deletion and scheduler outbox atomicity

Date: 2026-10-06  
Code commit: `100dbd9122074392885c9ab54e445cc7ed9591b6`  
Branch: `upgrade/v0213-phase5-20261005` (pushed; no merge or deployment)

## Finding and change

The account repository soft-deleted an account and committed its transaction, then attempted to enqueue the durable scheduler invalidation event as a best-effort operation. If that enqueue failed, the database no longer considered the account active but the outbox had no durable event to make other scheduler snapshots converge. This could leave a deleted account visible to scheduling until another refresh or invalidation.

`accountRepository.Delete` now enqueues `account_changed` in the same transaction as the account/group/scheduled-plan deletion. An outbox insert failure returns an error and rolls back the deletion. After a successful commit, the existing immediate single-account cache deletion still runs. No Smart Router selection, provider/model, compact, billing, or protocol behavior was changed.

## Changed files

- `backend/internal/repository/account_repo.go`
- `backend/internal/repository/account_repo_integration_test.go`

## Verification evidence

- `go test -tags=integration ./internal/repository -run TestDelete -count=1` — exit code 0. The focused PostgreSQL integration case injects an outbox insert failure and verifies the account remains active and schedulable.
- `go test -p 1 ./internal/service --run=TestSmartRouter --count=1` — exit code 0; a focused regression check that the unrelated scheduler/router surface remains green.
- `go test -p 1 ./internal/repository --run=TestDoesNotExist -count=1` — exit code 0, compile-only (`[no tests to run]`), not counted as a behavioral pass.
- The broader service/repository test attempt was not a clean pass: it reported the existing Aliyun transport-error test and PgDumper tests requiring `sh`; a later concurrent linker attempt ran out of memory. These are not counted as passes or as regressions from this two-file change.

## Review scope and limits

- The checked-in production wiring constructs repositories with the root Ent client and the base SQL DB; normal account deletion therefore uses the repository-owned transaction path covered by the failure-injection integration test.
- The account repository integration suite also constructs a transaction-bound Ent client together with a transaction-bound SQL executor. No production call site was found that combines a transaction-bound Ent client with a base, non-transactional SQL executor. That mixed construction is not established by the current test evidence; if introduced later, its outbox executor must remain bound to the same transaction.
- Cache deletion is still best-effort according to the existing cache interface; durable outbox delivery remains the recovery mechanism. This change does not prove live multi-instance Redis convergence.
- No VPS deployment or production runtime verification was performed. No independent audit receipt is claimed here.
- This is a cross-cutting maintenance fix, not a change to any phase's scope or acceptance status. Phase 5's historical execution record and its original implementation commit remain unchanged; Phase 6 remains not started.
