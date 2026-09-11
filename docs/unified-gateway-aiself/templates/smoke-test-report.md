# Smoke test report template

## Test identity

- test id:
- environment:
- date/time:
- dedicated key reference (never print key):
- budget limit:
- stop condition:

## Request

- endpoint:
- public model:
- request type:
- max tokens / media duration:
- request id:

## Resolved route

- route id:
- billing lane id/code:
- provider identity:
- source channel:
- account reference:
- upstream model:
- upstream endpoint:
- adapter version:
- pricing profile/version:
- cost model:
- lane user multiplier:
- customer multiplier:
- provider cost multiplier (internal only):
- upstream rate mode:
- upstream rate basis:
- upstream rate source used: `probe_declared | manual_fallback | manual_only`
- probe status / received at / fresh until:
- manual fallback used:
- manual upstream multiplier / unit price:
- base price semantics:
- route_price_snapshot_id:
- snapshot persisted before upstream call at:
- settlement reference/idempotency key:
- snapshot digest:

## Result

- HTTP/status:
- upstream request ref:
- output delivered:
- usage:
- expected user charge:
- actual user charge:
- account/provider cost:
- charge calculation trace:
- billing entry/idempotency key:
- provider/account cost record and recovery status:
- logs/metrics evidence:

## Regression check

- old key check:
- old endpoint check:
- catalog diff:
- conclusion:
