# Billing profile template

## Identity

- profile id:
- version:
- provider identity:
- source channel:
- route id / account pool:
- billing lane id:
- public model:
- upstream model:
- endpoint:
- effective from / to:
- owner / approval ref:

## Upstream rate source

- upstream rate mode: `probe_preferred | manual_only | probe_only`
- upstream rate basis: `token | per_request | image | video | provider_specific`
- upstream rate source used by new requests: `probe_declared | manual_fallback | manual_only`
- probe endpoint / schema:
- last probe status / received at / fresh until:
- manual upstream multiplier:
- manual base unit price:
- manual pricing rules (resolution/duration/provider-specific):
- manual fallback reason:
- rate owner / approval ref:
- account-specific overrides:
- base price semantics: `provider_base | final_user_price`

## Meter

- billing mode: `token | per_request | image | video | provider_specific`
- cost model: `provider_metered | amortized_subscription | fixed_request | image | video`
- input price:
- output price:
- cached input price:
- request price:
- image/resolution price:
- video unit and duration policy:
- currency:
- lane user charge multiplier / user markup multiplier:
- customer multiplier:
- provider cost multiplier (internal only):
- fixed fee:
- rounding mode/precision:
- account/provider cost fields (separate from user charge):

## Settlement

- charge trigger: `completed | delivered`（仅成功完成/交付产生用户费用；`accepted` 只记录 provider 内部成本）
- reservation policy: `quoted | reserved | capture | release`
- partial output policy:
- upstream failure policy:
- retry/attempt policy:
- idempotency key:
- unknown usage policy:
- missing price policy: `fail closed`
- probe unsupported/failed/stale policy: `manual fallback | fail closed`
- token probe allowed for non-token mode: `no` unless explicitly justified and approved

## Evidence

- provider price source:
- verification request ref:
- last checked at:
- manual price evidence/ref:
- test fixture ref:
- audit approval:
