# Upstream rate policy matrix template

| lane_id | route_id | account_ref/pool | provider/source | billing_mode | upstream_rate_mode | upstream_rate_basis | probe_status | probe_fresh_until | manual_multiplier | manual_unit_price | selected_source | user_markup | base_price_semantics | version | owner | status |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| LANE-0001 | ROUTE-0001 | logical-ref | provider/source | token/per_request/image/video | probe_preferred/manual_only/probe_only | token/per_request/image/video/provider_specific | not_configured | — | — | — | manual_only | 1.20 | provider_base | v1 | owner-ref | candidate |

## Matrix rules

- `selected_source` 是当前新请求实际会使用的来源，不是最近一次探测结果的简单展示。
- `probe_status=unsupported|failed|stale` 时，只有存在经批准的手动倍率/单位价格，`status` 才能为 `enabled`。
- `manual_only` 链路必须填写 `manual_multiplier`、`manual_unit_price` 或 provider-specific 规则之一。
- `upstream_rate_basis` 必须与实际计量匹配；token probe 不得直接用于 image/video/per-request。
- `provider_base` 与 `final_user_price` 必须二选一，不能重复套用上游倍率和用户加价。
- 同一 lane 内不同账号的费率不一致时，拆成 account override 行，不能只写 pool 默认值。
- 任何变更都递增 `version`；历史 snapshot 不回写。

## Review

- generated at:
- source inventory ref:
- pricing approval:
- capability approval:
- audit ref:
- unresolved rows:

