# Upstream rate policy template

此模板记录一条 lane、route 或 lane-account 的上游倍率来源。它只保存规则和证据引用，不保存 API key、cookie、Authorization、credential JSON 或完整账号名称。

## Identity

- policy id:
- version:
- billing lane id/code:
- route target ref:
- account ref / account pool ref:
- provider identity:
- source channel:
- public model / upstream model:
- endpoint / billing mode:
- effective from / to:
- owner / approval ref:

## Rate source

- upstream rate mode: `probe_preferred | manual_only | probe_only`
- upstream rate basis: `token | per_request | image | video | provider_specific`
- probe endpoint/schema:
- probe status: `ok | unsupported | failed | stale | not_configured`
- probe snapshot reference:
- received at / fresh until:
- selected source for new requests: `probe_declared | manual_fallback | manual_only`
- selected multiplier:
- manual upstream multiplier:
- manual base unit price:
- manual pricing rules:
- manual fallback reason:
- account-specific override:

## User charge rule

- base price semantics: `provider_base | final_user_price`
- provider/base price reference:
- lane user charge multiplier:
- customer multiplier:
- fixed fee:
- rounding mode/precision:
- expected formula:

```text
provider_unit_price = provider_base_price × selected_upstream_multiplier
user_unit_price = provider_unit_price × lane_user_charge_multiplier
user_charge = round(user_unit_price × measured_units + fixed_fee)
```

If `base_price_semantics=final_user_price`, document why no upstream/user multiplier is applied and set the formula explicitly.

## Safety and evidence

- missing/invalid rate action: `disable lane | fail closed`
- token probe used for non-token billing: `no | approved exception`
- manual change audit reference:
- capability evidence:
- price evidence:
- test fixture:
- last reviewed at/by:

