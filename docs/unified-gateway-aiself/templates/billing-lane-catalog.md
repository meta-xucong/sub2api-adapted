# Billing Lane catalog template

Billing Lane 是统一 API 内部的计费子组，不是新的对外 API group，也不复制账号凭据。

| lane_id | lane_code | pricing_source_group_id | provider | source_channel | transport | unified_group | account_refs/pool | capability | selection_policy | cost_model | upstream_rate_mode | upstream_rate_basis | manual_fallback | lane_user_charge_multiplier | provider_cost_multiplier | status |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| LANE-0001 | chatgpt-plus-text | SOURCE-GROUP-PLUS | OpenAI | direct | openai | unified_v1 | logical-account-ref | text/stream/tools | fixed_priority | amortized_subscription | probe_preferred | token | RATE-0001 | 1.20 | internal-only | candidate |

## Lane rules

- 同一 public model + endpoint 下，一个 account 默认只能属于一个 enabled lane。
- `lane_user_charge_multiplier` 是用户收费倍率；`provider_cost_multiplier` 只用于内部成本统计。
- `upstream_rate_mode` 必须是 `probe_preferred`、`manual_only` 或 `probe_only`。
- `upstream_rate_basis` 必须与实际计量一致；token probe 不能覆盖 image、video、per_request 或 provider-specific 规则。
- `manual_fallback` 必须引用已审核的手动倍率/单位价格规则；`manual_only` lane 没有该引用时不能启用。
- 火山引擎、Ark 和其他没有兼容 `/v1/sub2api/billing` 的链路默认填 `manual_only`，不能因为 transport 为 OpenAI-compatible 就填 probe。
- V1 优先复用 `pricing_source_group_id` 的原生模型价格、`rate_multiplier`、图片/视频独立倍率；只有无法表达的差异才填写 lane override。
- 自动 probe 不覆盖手动兜底值；实际采用的来源必须在 route price snapshot 中记录为 `probe_declared`、`manual_fallback` 或 `manual_only`。
- 统一 group 的全局倍率在动态 lane 场景必须明确为 `1.0` 或不参与计算。
- `schedulable=false`、错误状态、无能力或无价格的账号不能进入 enabled pool。
- `cost_model` 必须与对应 pricing profile 一致：`provider_metered`、`amortized_subscription`、`fixed_request`、`image`、`video`。
- 每个 lane 必须能说明 `base_price_semantics` 是 `provider_base` 还是 `final_user_price`，避免重复乘倍率。
- 变更 lane 成员、倍率或 selection policy 必须递增版本；历史 route price snapshot 不回写。

## Approval

- owner:
- pricing owner:
- capability evidence:
- pricing profile refs:
- upstream rate policy refs:
- manual fallback owner/reason:
- effective version:
- approved at/by:
