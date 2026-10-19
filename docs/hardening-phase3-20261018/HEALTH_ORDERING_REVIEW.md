> 本文件保留该次独立代码审查时点的证据与范围。后续完整执行已完成，最终结果以 [PREUPLOAD_ACCEPTANCE.md](PREUPLOAD_ACCEPTANCE.md) 为准。

# Health persistence ordering: independent read-only review

Reviewed frozen working-tree files on 2026-10-18 13:20 UTC. No compilation, tests, database operations, or repository edits performed. This records static review, not executed validation.

## Verdict

No new blocking defect found in the frozen implementation. The cumulative timestamp omission identified in the first review is resolved. Execution evidence must still be supplied by the coordinating test runner.

## Reviewed production file identities (SHA256)

- `backend/internal/smartrouter/core/health.go`: `c83dbe39b4b6a72a67781db7f678b878aeccf2d7c2a47a7e2723fc3e6950621f`
- `backend/internal/smartrouter/core/health_persistence.go`: `e0f9e9ed718a74a1c01f46a8100051600dd9c7a3f1fa8eaa233c3e1d93cded7a`
- `backend/internal/service/smart_router_runtime.go`: `9da24edad18019ecd2c81d0d58ba178578be3179c3ebdc132547e46a21bce47a`
- `backend/internal/repository/smart_router_health_repo.go`: `77a9ba40f09b76add4166da06ee37cc73550341b14c03dcd532a5e6d08882b7b`

## Correctness checks

1. Each existing tracker health state owns one persistence token and sequence counter. Sequence assignment occurs while holding the tracker mutex. The token's attempted high-water mark is separately serialized by its one-slot gate. No second per-key registry, goroutine, schema column, or global serialization was added.
2. The service creates its existing three-second context before acquiring the event gate, then passes the same context to `RecordEvent`. Waiting does not hold the tracker mutex. Deferred idempotent release executes on normal return, returned error, and stack unwinding after a ledger panic.
3. The gate's post-acquisition `ctx.Err` check and absolute deadline check return the token without advancing the high-water mark. This covers both ready-gate/canceled-context select races and delayed context timer publication.
4. Admission advances the attempted high-water mark before the database call. A late older event therefore cannot restore an obsolete health snapshot even when a newer write failed or its commit outcome is uncertain. Future higher-sequence events can attempt persistence again. This is intentionally a safety fence, not a durability/retry guarantee.
5. Restoring an existing tracker key preserves its token and sequence identity. New process/tracker instances remain independent. This does not establish a database-global sequence across processes, nor does it turn arbitrary concurrent Restore calls into global linearizable operations.
6. Internal ordering and projection decision fields are private. JSON decoding cannot set them and encoding omits them. Untagged/legacy constructed events retain their previous projection behavior. The production adapter is the only production `RecordEvent` call path found and invokes acquisition first.
7. The repository always attempts the event-history insert within the transaction. Superseded events execute only a timestamp UPDATE, followed by commit. They cannot UPSERT a missing lane row or overwrite health penalty/score, EWMA, counters, cooldown, recovery state/priority, account/source group, `last_failure_unix`, or `updated_at`.
8. Independent success/failure timestamps use PostgreSQL `GREATEST` both in the timestamp-only UPDATE and in normal UPSERT. NULL historical values can be populated, existing newer values are retained, and wall-clock rollback does not regress those two evidence fields. `last_failure_unix` remains part of the ordered health projection, rather than being incorrectly advanced by a late failed event after a newer success.
9. Transaction rollback/error semantics are unchanged: event insertion and timestamp/projection update commit together, or fail together. A newer failed insert can leave no state row; a late old event deliberately does not recreate an obsolete snapshot. Evidence/durability remains best effort.

## Finding raised and resolved

The first draft returned immediately after inserting a superseded event. For a previously unseen key, failure then success with reversed persistence arrival produced a success snapshot but permanently omitted `last_failure_at`; the reverse direction omitted `last_success_at`. Those columns feed capability/calibration evidence and are separate from the replaceable health snapshot.

The final timestamp-only UPDATE preserves that independent evidence while keeping snapshot-order safety. Normal UPSERT was also changed to use monotonic timestamp merging so a subsequent current event with a rolled-back wall clock cannot undo this evidence. This resolves the identified omission without schema expansion or changing stale-event health state.

## Test source coverage reviewed, not executed here

- Core: same-second and reversed-clock event arrival; shared caller wait budget; expired absolute deadline before Err; failed/uncertain attempt fencing; panic release; other-key and Snapshot independence; Restore token preservation; tracker independence; legacy events; JSON injection/serialization boundary.
- Service: calls the actual `persistSmartRouterHealthEvent`, confirms a bounded write context, passes projection decisions to the ledger in newer/older/future order, and checks release after success/error/panic.
- Repository SQL mock: both success/failure directions and an uncertain newer commit, with an event INSERT plus timestamp-only UPDATE and no stale-state UPSERT.
- PostgreSQL integration source: both outcome directions, reversed delivery at equal timestamps, both event records retained, current snapshot fields preserved, both cumulative timestamps populated, `last_failure_unix` unchanged by the late event, `updated_at` unchanged by timestamp-only repair, and a subsequent rolled-back-clock observation retaining both timestamps.

## Retained limits

- No executed test, race, SQL, or lint pass is claimed by this review.
- The gate orders only events from one tracker/key; other tracker instances and legacy direct events are not globally fenced.
- Three seconds is a cooperative best-effort context budget, not an unconditional hard elapsed-time cap for arbitrary ledger implementations or database commit internals.
- Sequence state is in memory and lost on process restart. No durable outbox, retry queue, exactly-once history, or globally atomic memory/database transaction was introduced.
- `last_success_at` and `last_failure_at` remain timestamp evidence; a clock rollback can make them differ from local observation sequence. The new health projection ordering does not redefine that existing calibration contract.
