# 上游倍率来源与手动兜底规范

状态：`IMPLEMENTATION_REV2_LOCAL / REVISION_4 / PRODUCTION_GATE_DISABLED`  
适用范围：统一 API 的 Billing Lane、Route Target、Pricing Profile 和 Route Price Snapshot。  
本文件不改变旧入口的 `accounts.rate_multiplier`、group 计费或现有 provider 链路。本轮已将解析规则接入隔离的本地 unified handler、内存快照状态机和 PostgreSQL raw-SQL snapshot/catalog 仓储；真实线上 handler 和 provider 仍保持关闭。

## 1. 结论

统一网关必须同时支持两类上游价格来源：

1. 能够访问兼容 `/v1/sub2api/billing` 的上游，优先使用新鲜的自动探测结果；
2. 不能自动获取倍率或不是 ChatGPT/Sub2API 计费语义的上游，使用管理员为该链路填写的手动倍率、手动单位价格或按次规则。

因此不能把“没有探测结果”解释成倍率 `1.0`、免费或沿用另一个 provider 的价格。没有有效自动值，也没有审核过的手动规则时，统一入口必须在发送上游前拒绝请求。

本规范采用两个独立概念：

- `provider_rate_policy`：本条链路如何得到上游成本基准；
- `user_charge_policy`：本条链路如何把上游成本基准转换成下游用户收费。

术语冻结：旧版设计中使用的 `upstream_rate_mode`/`upstream_rate_source` 在管理 API 和后端 DTO 中统一简称为 `rate_mode`/`rate_source`。其中 `rate_mode` 是链路配置（`probe_preferred`、`manual_only`、`probe_only`），`rate_source` 是本次解析实际采用的来源（`probe_declared`、`manual_fallback`、`manual_only`）；两者不能互换。

现有 `accounts.rate_multiplier` 继续只用于账号/上游成本统计。自动探测可以继续更新它，但用户收费必须读取统一网关的 lane/profile 规则，不得直接把该字段当成下游倍率。

## 2. 现有 sub2api 能力的复用边界

候选源码当前已经具备以下能力：

| 能力 | 当前语义 | 统一网关的用法 |
| --- | --- | --- |
| `/v1/sub2api/billing` 探测 | 获取上游 group/user/resolved/effective 倍率和高峰信息 | 作为 `probe_preferred` 链路的上游倍率来源 |
| `accounts.extra.upstream_billing_probe` | 保存探测状态、原始声明、`fresh_until` 和错误状态 | 用于判断自动值是否新鲜、是否可用于本次价格快照 |
| `accounts.rate_multiplier` | 账号维度成本/配额统计倍率 | 继续用于内部成本；不能直接用于用户收费 |
| `groups.rate_multiplier`、模型价格、图片/视频独立倍率 | 旧 group 的用户计价配置 | 作为 `pricing_source_group_id` 的价格模板复制到新 snapshot |
| `BillingMode` | token、per_request、image、video | 直接复用计量模式，但必须匹配对应的单位价格 |

当前自动 rate sync 开启后，管理员手工编辑账号倍率会被拒绝；这意味着原生字段本身不能同时表达“自动探测优先、探测失败时手动兜底”。手动兜底必须放在新的 lane/profile 规则中，不能覆盖或改写旧字段语义。

## 3. 上游倍率来源模式

每个 lane 或 lane-account 价格规则必须明确一个 `rate_mode`（旧文档称 `upstream_rate_mode`）：

| 模式 | 新鲜自动探测可用 | 探测 unsupported/failed/stale 时 | 适用场景 |
| --- | --- | --- | --- |
| `probe_preferred` | 使用探测值 | 使用已审核的手动兜底；没有兜底则 fail closed | 能探测的 Sub2API 中转、但需要容灾手工价 |
| `manual_only` | 不依赖探测 | 始终使用手动倍率/单位价格 | 火山引擎、Ark、非 Sub2API provider、自定义收费链路 |
| `probe_only` | 使用探测值 | fail closed | 对价格准确性要求高且不允许人工估算的链路 |

推荐默认值：

- 可确认返回兼容 billing schema 的链路：`probe_preferred`；
- 火山引擎、Doubao/Ark、Kimi、DeepSeek、Gemini 或其他没有兼容 billing endpoint 的账号：`manual_only`，除非后续已验证其探测协议确实兼容；
- 不应根据 transport 是 OpenAI-compatible 就自动判定可探测。

## 4. 价格规则的粒度

价格规则必须至少支持以下层级，越具体的优先级越高：

1. `route_target + lane + account + model + endpoint`；
2. `route_target + lane + account_pool + model + endpoint`；
3. `lane + model + endpoint` 默认规则。

如果同一 lane 内的所有账号实际收费相同，可以使用 lane/account pool 规则。如果账号成本或倍率不同，必须在选中实际账号后读取 account override，并将该值写入 snapshot。

同一个账号被多个 lane 使用时，各 lane 可以拥有不同的用户价格，但必须通过显式的 lane-account 规则区分；不能依赖一个全局 `accounts.rate_multiplier` 推断不同链路的售价。

## 5. 必备字段

建议在统一入口专用的 pricing profile 或独立关联表中增加以下逻辑字段：

| 字段 | 说明 |
| --- | --- |
| `rate_mode`（旧名 `upstream_rate_mode`） | `probe_preferred`、`manual_only`、`probe_only` |
| `upstream_rate_basis` | `token`、`per_request`、`image`、`video`、`provider_specific` |
| `rate_source`（旧名 `upstream_rate_source`） | `probe_declared`、`manual_fallback`、`manual_only` |
| `probe_snapshot_ref` | 使用的探测快照/接收时间/freshness 信息 |
| `manual_upstream_multiplier` | 探测不可用时的倍率；可为空但启用链路时不能缺失 |
| `manual_base_unit_price` | 非 token 链路的手工基础单价或按次价格 |
| `manual_pricing_rules` | 图片分辨率、视频时长、provider-specific 计价 JSON |
| `base_price_semantics` | `provider_base` 或 `final_user_price`，防止重复乘倍率 |
| `user_markup_multiplier` | 下游加价倍率，例如 20% 为 `1.20` |
| `fixed_fee` | 可选的每次固定附加费用 |
| `rounding_mode` / `precision` | 最终用户费用的舍入规则 |
| `fallback_reason` | 为什么必须人工配置，供审计和后续复核 |
| `version` / `effective_from` | 价格变更和历史 snapshot 边界 |

`lane_user_charge_multiplier` 是 lane 默认销售倍率，`user_markup_multiplier` 是 profile/account 更具体的覆盖值；解析时二选一，不能把两者相乘。只有在另一个字段明确表示客户/套餐倍率时，才作为独立的 `customer_multiplier` 继续计算。

### 5.1 手动倍率与手动单价的区别

- provider 有稳定的官方/基础价格，只是没有自动倍率接口：填写 `manual_upstream_multiplier`；
- provider 没有可复用的 token 基础价，或本身按次、按图片、按视频、按秒收费：填写 `manual_base_unit_price` 或 `manual_pricing_rules`；
- 管理员直接给出最终下游售价：将 `base_price_semantics` 设为 `final_user_price`，不得再次乘上游倍率或销售倍率；
- 不允许同时填写含义冲突的“最终售价”和“成本倍率”而不标记优先级。

## 6. 解析优先级与失效规则

请求必须先完成 route/lane/account 选择，再解析价格。推荐算法如下：

1. 读取实际选中的 account、lane、model、endpoint 和版本化 profile；
2. 如果模式为 `probe_preferred` 或 `probe_only`，检查探测快照：状态必须为 `ok`、当前时间不超过 `fresh_until`、计费 scope 与本次 `upstream_rate_basis` 一致；
3. 若探测值有效，使用 `resolved_rate_multiplier`；只有 profile 明确启用高峰计价时，才使用按当前时间计算的 `effective_rate_multiplier`；
4. 若探测值无效且模式为 `probe_preferred`，依次查找 account 手动兜底、account pool 手动兜底、lane 手动兜底；
5. 若模式为 `manual_only`，直接读取对应手动倍率/单位价格，不等待探测；
6. 根据 `base_price_semantics` 计算 provider base、下游加价和最终单位价格；
7. 把全部来源、原始值、兜底原因、最终单价和公式摘要冻结到 `route_price_snapshot`；
8. 只有 snapshot 成功持久化后才允许发送上游。

以下情况不能继续转发：

- 自动探测状态为 `unsupported`、`failed` 或已过 `fresh_until`，且没有手动兜底；
- `manual_only` 链路没有手动倍率或手动单位价格；
- token 探测值被错误地用于 image/video/per-request 规则；
- base price 已是最终销售价却再次套用倍率；
- profile 没有说明倍率单位、计量单位或舍入规则。

## 7. 统一公式

### 7.1 有基础价格的 token 链路

```text
provider_unit_price = provider_base_price × effective_upstream_multiplier
user_unit_price = provider_unit_price × user_markup_multiplier
user_charge = round(user_unit_price × measured_units + fixed_fee)
```

其中 `effective_upstream_multiplier` 的来源是新鲜 probe 或手动兜底。`provider_base_price` 可以来自经过授权的来源分组/官方价格模板，但必须在 snapshot 中注明它是否已经含有任何倍率。

### 7.2 非 token、按次或媒体链路

```text
provider_unit_price = manual_base_unit_price
user_unit_price = provider_unit_price × user_markup_multiplier
user_charge = round(user_unit_price × request_count_or_media_units + fixed_fee)
```

图片分辨率、视频时长、质量档位和 provider-specific 参数必须由 `manual_pricing_rules` 明确列出。不能因为某个 provider 使用 OpenAI-compatible HTTP，就把它归入 ChatGPT token 计费。

### 7.3 最终销售价覆盖

```text
if base_price_semantics == final_user_price:
    user_charge = round(final_user_price × measured_units + fixed_fee)
```

该模式下不再乘 `effective_upstream_multiplier` 或 `user_markup_multiplier`。snapshot 必须保存 `calculation_mode=final_price_override`，防止后台报表重复计算。

## 8. 示例

### 8.1 自动探测成功

```text
lane: openai-relay-plus
rate_mode: probe_preferred
probe resolved multiplier: 1.50
user markup: 1.20
```

新鲜探测有效时：`user price = base price × 1.50 × 1.20`。

### 8.2 探测失败，使用手动兜底

```text
lane: custom-openai-relay
rate_mode: probe_preferred
probe status: unsupported
manual fallback multiplier: 1.35
user markup: 1.20
```

本次 snapshot 标记 `rate_source=manual_fallback`，最终按 `base price × 1.35 × 1.20` 计算。后续探测恢复只影响新请求，不回算本次或历史账务。

### 8.3 火山引擎/Ark 手动按次

```text
lane: volcengine-ark
rate_mode: manual_only
billing_mode: per_request
manual base unit price: 0.002 USD/request
user markup: 1.20
```

本次收费为 `0.002 × 1.20` 美元/次。这里不需要伪造 ChatGPT token 倍率；如果火山引擎按输入输出 token、图片或其他单位收费，则分别填写对应的手动单价和计量规则。

### 8.4 同一模型多链路

`gpt-5.5` 可以同时命中自动探测的 Plus lane、手动配置的 Ark lane 或按次的订阅 lane。实际选中的 lane/account 决定本次 snapshot 和收费，统一 Access Group 不再提供一个覆盖所有来源的默认价。

## 9. 管理与审计要求

- 手动倍率、手动单位价格和兜底规则必须由管理员通过受审计接口修改；不能直接编辑 `accounts.extra` 或数据库 JSON 绕过版本管理。
- 自动探测更新不会覆盖手动兜底值；它只更新当前可用的 probe snapshot。
- 手动规则修改必须生成新 profile version；已存在的 snapshot 不变。
- 管理界面必须明确显示：当前生效来源是 `probe_declared`、`manual_fallback` 还是 `manual_only`，以及最近一次探测状态和过期时间。
- `accounts.rate_multiplier` 的自动同步开关与统一入口用户计费规则分离；关闭或开启 rate sync 不得改变既有旧入口收费。
- usage、billing、provider cost 和审计必须记录倍率来源与 `manual_fallback_used`，不能只记录最终结果。

## 10. 实施顺序与零干扰

1. 先增加逻辑字段、校验器和 profile snapshot，不启用任何新 lane；
2. 先用 `manual_only` 的非 ChatGPT fixture 验证价格解析，不调用真实上游；
3. 再用一个已验证 probe lane 验证自动值与手动兜底切换；
4. 统一入口 feature gate、group、key、route 和新账务路径全部独立；
5. 旧入口继续使用原有 group/account/billing resolver，不读取新的 lane rate policy；
6. 任一 lane 的自动值、手动值或计量单位不完整时，只禁用该 lane，不影响其他 lane 和旧入口。

## 11. 验收门槛

- 非 ChatGPT/火山引擎链路可以在没有 probe endpoint 的情况下启用，但必须有审核过的手动倍率或单位价格；
- probe 成功、probe unsupported、probe failed、probe stale、manual-only 五种来源都能在 snapshot 中区分；
- 手动兜底不会被自动同步覆盖，自动恢复后新请求切回 probe；
- 同一模型命中不同 lane 时，收费规则和账务明细分别正确；
- token 探测倍率不会污染 image、video、per-request 价格；
- 失败、取消、超时、过期或无有效交付结果仍然不产生用户费用；
- 关闭统一入口 feature gate 后，旧 key、旧 group、旧账号、旧 `/v1` 和旧计费结果与基线一致。
