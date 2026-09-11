# Route catalog template

| route_id | billing_lane | pricing_source_group_id | public_model | provider_identity | source_channel | transport | endpoint | upstream_model | account_ref/pool | schedulable | priority/weight | pricing_profile | upstream_rate_policy | capability_evidence | status |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| ROUTE-0001 | LANE-0001 | SOURCE-GROUP-PLUS | model | provider | direct/kie/... | openai/... | `/v1/...` | upstream-model | logical-ref | yes/no | 1/100 | PRICE-0001:v1 | RATE-0001:v1 | CAP-0001 | candidate |

Rules:

- `account_ref` 不得填写 credential、邮箱、token 或完整账号名称。
- `status=enabled` 之前必须同时存在 capability evidence 和有效 pricing profile。
- `status=enabled` 之前必须存在有效 upstream rate policy；manual-only route 必须有人工价格证据。
- `schedulable=no` 的账号不能进入 enabled pool。
- token probe 不能覆盖 image/video/per-request/provider-specific route，除非 profile 有明确批准的单位映射。
- 同一个 public model 的多个候选必须能区分 provider/source/route/pricing。
