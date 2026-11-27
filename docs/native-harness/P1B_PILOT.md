# P1b native harness integration seam (default OFF)

This is a **synthetic, non-billing, single-user/single-key development pilot**.
It is not a production model provider, a WorkBuddy adapter, or an ordinary
`/v1/chat/completions` account-channel implementation. No real model capability
or commercial redistribution permission is claimed.

## Actual path and authority

`POST /v1/native-harness/chat/completions` accepts the restricted Chat Completions
body and response shapes. It uses the real APIKeyService/auth middleware and a
separate handler. It does not invoke ordinary model/account selection, provider
retries, failover, token-usage accounting, balance reservations or billing workers.
The ordinary Chat Completions path is unchanged.

Authentication, user/key disablement, group membership/availability and IP
restrictions still apply. Only this exact POST route, when enabled for the exact
configured user/key, skips billing enforcement. Expired or quota-exhausted keys
can only request replay; their outbound admission flag is false. Current group
model allowlists apply to new execution, not existing result retrieval.

Every request requires exactly one `Idempotency-Key` (1–128 identifier characters).
The authenticated user ID becomes `user-<id>`; API key ID becomes `key-<id>`.
Key rotation is intentionally a different scope. Body `user` is ignored after
validation. Client `X-Native-*` headers and account custom headers are never
forwarded. Trusted headers are generated only from authenticated IDs and the
frozen database row, including `X-Native-Source` and `X-Native-Ledger`.

## Durable route, not another task ledger

Migration `248_native_harness_operations.sql` adds a routing table to the existing
PostgreSQL database. A unique `(user_id, api_key_id, client_key)` key freezes one
operation ID, normalized-request digest, and route snapshot before outbound HTTP.
The snapshot includes Facade origin, credential **environment reference** (not its
value), source, Bridge ledger ID and exact binding. No prompt, result, token or
executable task is stored. Bridge remains the sole execution ledger.

Concurrent inserts use `ON CONFLICT DO NOTHING` and read the committed winner.
A changed normalized request returns 409. Timeout, 429, 5xx, malformed/truncated
200 and refusal never delete or replace the row or choose another target.
An uncertain database write makes no HTTP request. A later request must read the
same row. No TTL/deletion endpoint exists; tombstone loss or database rollback is
not safe permission to execute again and is outside this pilot's recovery proof.

Removing/changing a configured route does not change old rows. Old operations can
only query their frozen destination, with admission false unless the full frozen
route still equals the current allowed route. Missing credential references fail
closed. Credential rotation does not change source/ledger headers; the P1b Facade
must verify both against its token-bound service before lookup or submission.
Disabling the whole feature or removing the user/key allowlist disables retrieval
as well; use `accepting_new: false` to drain while keeping replay available.

## Configuration example (intentionally disabled)

```yaml
gateway:
  native_harness:
    enabled: false
    accepting_new: false
    allowed_user_id: 7
    allowed_api_key_id: 9
    routes:
      synthetic-test:
        origin: http://127.0.0.1:8790
        credential_env: NATIVE_FACADE_SERVICE_TOKEN
        source: source-test
        ledger_instance: ledger-test
        binding_id: binding-test
        revision: 1
        model_alias: synthetic-test
        native_model: fake-text
```

Only `synthetic-*` aliases with `fake-*` native models are accepted. Configured
aliases must match binding aliases. Non-loopback Facade origins require HTTPS;
userinfo, path, query and fragment are rejected. No token is generated or saved by
this feature. The environment reference must already be provisioned by the operator.
No production service should be enabled from this example.

HTTP is one attempt, with no environment proxy, redirects, body replay or
Idempotency-Key forwarding to `net/http`. Response framing must have a bounded
Content-Length and all declared bytes; chunked/close-delimited responses are not
supported in this pilot. Unknown usage is omitted, never replaced with zero.
Successful completion DTOs use exact field allowlists: top-level
`id/object/created/model/choices`, choice `index/message/finish_reason`, and message
`role/content` plus optional `refusal`. IDs require `chatcmpl-` with a nonempty
identifier suffix; created is a nonnegative integer. Content must be string or
null, and null requires a nonempty refusal. Optional refusal must be a nonempty
string. Text, refusal-only and mixed results are accepted. Every unknown field,
including `function_call`, `tool_calls`, `usage` and null-valued capability fields,
is rejected rather than forwarded. A final HTTP result does not assert native process quiescence.

## Tests and limits

```sh
cd backend
go test -race ./internal/nativeharness
go test -tags unit ./internal/handler ./internal/server/middleware ./internal/server/routes ./internal/config
go build ./cmd/server
```

- Service tests use explicit in-memory route fixtures; SQL statement tests use
  sqlmock. Neither proves PostgreSQL migration/concurrency/durability.
- `TestNativeHarnessRealMiddlewareAndHandler` uses real APIKeyService/middleware
  and Handler but fixture credential lookup, route storage and HTTP upstream.
- `TestNativeHarnessExternalBridgeMemoryRoute` only runs when
  `NATIVE_HARNESS_TEST_FACADE` points to the separately managed real
  Bridge+Facade+FakeHarness fixture. It still uses fixture credential lookup and
  in-memory route storage. This is a three-repository HTTP test, not full durable
  end-to-end acceptance.
- `TestSQLStorePostgres` requires an explicitly authorized disposable PostgreSQL
  via `NATIVE_HARNESS_TEST_DSN` with migration 248 already applied.
- `TestNativeHarnessExternalBridge` requires both that DSN and Facade fixture.
  These tests skip by default and do not start a database or apply migrations.
- No real PostgreSQL tests were executed; environment/authorization constraints
  remain. No alternative database path is used for acceptance. Real PostgreSQL
  migration, concurrent inserts, restart recovery and full persistent three-repo
  integration are **NOT_RUN/BLOCKED**, not passes.

No real WorkBuddy execution, paid inference, production credentials/database,
Windows verification, deployment, merge, or complete production authorization,
rollback fencing, moderation/account-routing/billing integration is covered.

## Validation snapshot

Source base: `2695b585aa7c2b9fed6fa50b9b8455c0ac2c1a14`; independent working
branch `work/native-harness-p1b`. Official Go 1.27.2, Linux. No changes to go.mod
or go.sum and no substitutes for the required compiler/dependencies.

- `go test -race ./internal/nativeharness`: 12 top-level tests pass; the actual
  PostgreSQL test is skipped. Includes 100 concurrent synthetic route freezes,
  real socket truncated Content-Length, credential/source/ledger rotation,
  SQL statement contract, refusal, no redirects/retries, malformed UTF and JSON.
- Real middleware/handler targeted tests pass, including `-race`; auth matrix covers wrong/missing
  credentials, disabled key/user, wrong allowlist, group/IP restrictions, quota
  replay and unchanged ordinary-path balance enforcement.
- Full handler, middleware and routes unit packages pass. Config's first full run
  caught missing Viper defaults for four new options; defaults were added, then
  the full config package passed.
- `go build ./cmd/server` passes, including the application DI wiring.
- Parent coordinator executed `TestNativeHarnessExternalBridgeMemoryRoute`
  against real Facade/Bridge/Node HTTP: PASS, synthetic adapter starts = 1.
  Auth lookup and route persistence remained explicit fixtures.
- PostgreSQL migration execution, real concurrent uniqueness, database restart
  and production credential lookup are not tested. These block any claim of a
  fully persistent three-repository P1b end-to-end acceptance.

### Independent audit response-schema fix

The independent malformed-completion probe originally demonstrated acceptance of
missing `id/created`, numeric content, and a legacy function call. The response
validator now rejects that exact input with `NATIVE_INVALID_COMPLETION`. The
original audit test expects acceptance, so its unchanged reproduction now exits
with failure due to the expected new rejection; this is not counted as a passing
regression test. The new `TestCompletionDTOHTTPContract` runs 39 HTTP positive and
negative subcases, each checking no automatic retry and a stable operation on a
manual same-key retry. These tests pass under the race detector.
