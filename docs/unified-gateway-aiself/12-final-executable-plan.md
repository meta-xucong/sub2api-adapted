# 统一 API 动态计费：最终可执行方案（Revision 3）

状态：`IMPLEMENTATION_REV2 / LOCAL_SIMULATION_PASS / PRODUCTION_GATE_DISABLED`  
适用环境：aiself；后续可复制到 404token，但必须重新生成运行基线、账号能力和价格证据。  
本文件把 Plus、Pro、生图、KIE/Subrouter/直连等差异正式纳入统一网关设计。本轮已把 route/account 选择、价格快照、预占/结算状态机和本地 HTTP 模拟落成隔离代码；不注册生产路由、不执行线上迁移、不改变旧账务。真实 provider/认证/现有余额适配和媒体任务上线仍需后续生产放行阶段。

## 1. 最终决策

统一入口拆成四层：

```text
Unified Access Group（访问域）
  -> Billing Lane（计费子组/上游产品池）
      -> Route Target（模型、协议、endpoint、provider）
          -> Account Pool（实际账号）
              -> Pricing Profile（规则与倍率）
                  -> Route Price Snapshot（本次不可变收费事实）
```

### 1.1 四层职责

| 层级 | 负责什么 | 不负责什么 |
| --- | --- | --- |
| Unified Access Group | API key、权限、可见模型、用户配额 | 不决定最终 provider 价格 |
| Billing Lane | Plus/Pro/生图/视频/来源渠道等子组、选择策略、账号池 | 不直接代表一次请求的最终费用 |
| Route Target | public model 到 provider/upstream model/endpoint/adapter 的映射 | 不单独承担价格和结算 |
| Pricing Profile/Snapshot | 计量单位、基础价格、倍率、预占、失败语义和版本 | 不改变账号调度和旧 group 行为 |

“子分组”在本方案中是 Billing Lane，不建议为每个 Plus/Pro 再创建一个对外 API group。这样既保留一个统一链接，又能让每个上游产品有独立倍率和计价方式。

### 1.2 V1 优先复用 sub2api 原生分组计价

为了减少重复配置，V1 的每个 Billing Lane 优先绑定一个现有的 `pricing_source_group_id`，把该分组视为计价模板：

- 普通 token 模型：复用来源分组的模型价格和 `groups.rate_multiplier`；
- `image`/`gpt-image-2`：复用来源分组的图片价格、图片计费模式和 `image_rate_multiplier`；
- 视频：复用来源分组的分辨率/时长价格和 `video_rate_multiplier`；
- provider/channel 有明确专属价格时，使用该 lane 显式绑定的 channel/profile 覆盖；
- 只有原生分组字段无法表达的账号级差异，才新增 lane/account override。

这个 `pricing_source_group_id` 只是“价格来源引用”，不是把请求切换到该旧 group，也不改变旧 group 的 API key、模型目录或路由。解析器必须把来源分组的实际价格和倍率复制进本次 `route_price_snapshot`；不能在结算时重新读取可变的旧分组配置。

来源分组价格如果已经是面向用户的最终销售价，必须标记为 `base_price_semantics=final_user_price`，不能再叠加 probe/manual 上游倍率。只有经过确认的 provider/base price 才能按“基础价格 × 上游有效倍率 × lane 用户加价”计算；`pricing_source_group_id` 不得成为隐式重复计价入口。

### 1.3 自动探测优先，手动规则兜底

每个 lane/model/endpoint 必须显式配置上游倍率来源：

| `upstream_rate_mode` | 规则 | 适用场景 |
| --- | --- | --- |
| `probe_preferred` | 新鲜兼容 probe 优先；unsupported、failed 或 stale 时使用手动兜底 | 能探测的中转链路，允许人工容灾 |
| `manual_only` | 始终使用手动倍率、手动单位价格或 provider-specific 公式 | 火山引擎、Ark、非 Sub2API provider、非 ChatGPT 计费模型 |
| `probe_only` | 只接受新鲜 probe；没有则拒绝 | 不允许人工估算的链路 |

这里的手动配置属于统一入口的 lane/profile，不改写 `accounts.rate_multiplier` 的既有内部成本语义。`accounts.rate_multiplier` 可以继续由原生 upstream rate sync 更新，但不能直接参与用户扣费。自动探测不会覆盖手动兜底值；它只改变新请求选择的来源。

非 token provider 必须手动说明自己的基础单位：按次、按图片/分辨率、按视频秒数、按字符、按 provider-specific usage 等。不能因为 HTTP transport 兼容 OpenAI，就套用 ChatGPT token 倍率。

## 2. Billing Lane 设计

### 2.1 示例

| lane code | provider/source | 账号池 | 能力 | billing mode | 倍率/规则 |
| --- | --- | --- | --- | --- | --- |
| `chatgpt-plus-text` | OpenAI/直连 | Plus 文本账号 | text/stream/tools | token 或摊销 token | Plus lane multiplier |
| `chatgpt-pro-text` | OpenAI/直连 | Pro 文本账号 | text/stream/tools | token 或摊销 token | Pro lane multiplier |
| `chatgpt-image` | OpenAI/生图账号 | 生图账号 | image | image/resolution | 生图 lane price |
| `ark-coding` | Ark/直连 | Ark 账号池 | text/tools | token/per request | Ark lane multiplier |
| `grok-video-kie` | Grok/KIE | KIE Grok 账号 | video/polling | resolution-second | KIE lane multiplier |
| `grok-video-direct` | Grok/直连 | 直连 Grok 账号 | video/polling | resolution-second | 直连 lane multiplier |

一个账号在同一 public model + endpoint 下只能属于一个 enabled lane，除非 route target 有明确的 scope 条件。这样可以避免账号同时被两个 lane 选中而导致价格不可解释。

### 2.2 Lane 的必备字段

建议新增独立的数据域，具体表名按项目迁移规范确定：

`unified_billing_lanes`

- `id`, `code`, `name`, `unified_group_id`
- `provider_identity`, `source_channel`, `transport_platform`
- `selection_policy`: `fixed_priority | weighted | capacity_aware | cheapest_valid`
- `default_billing_mode`
- `default_user_charge_multiplier`
- `pricing_source_group_id`：优先复用的现有分组计价模板
- `upstream_rate_mode`：`probe_preferred | manual_only | probe_only`
- `upstream_rate_basis`：`token | per_request | image | video | provider_specific`
- `manual_upstream_multiplier`：探测失败时的 lane 默认倍率
- `manual_base_unit_price` / `manual_pricing_rules`：非 token 或 provider-specific 链路的手动基础价格
- `user_markup_multiplier`、`fixed_fee`、`rounding_mode`
- `enabled`, `version`, `notes`

`unified_lane_accounts`

- `lane_id`, `account_id`, `priority`, `weight`, `enabled`
- `schedulable_required=true`
- 可选 `account_user_multiplier_override`
- 可选 `manual_upstream_multiplier_override`、`manual_base_unit_price_override`
- `fallback_reason`、`rate_owner`、`effective_version`
- 该 override 仅在业务确认账号确实需要独立用户售价时启用；不能把现有 `accounts.rate_multiplier` 直接当作它
- 不保存凭据副本；凭据仍由现有 accounts 管理

Lane 只引用账号，不复制 credential。`schedulable=false`、错误状态、无能力或无有效价格的账号不得进入 enabled candidate pool。

## 3. Pricing Profile 设计

### 3.1 计价模型

每个 lane/model/endpoint 至少绑定一种 `cost_model`：

| cost model | 适用场景 | 计量方式 |
| --- | --- | --- |
| `provider_metered` | provider 返回 token/调用量成本 | input/output/cache token 或 request |
| `amortized_subscription` | Plus/Pro 等订阅账号没有逐请求上游账单 | 月成本/预计可用量、并发或人工核定单位成本 |
| `fixed_request` | 上游按次收费或内部按次定价 | 每次请求固定费用 |
| `image` | 生图 | 数量 × 分辨率/质量/模式价格 |
| `video` | Grok/KIE/视频 | 秒数 × 分辨率/模式价格，或 provider-specific 公式 |

Plus/Pro 如果没有真实 token 成本，不能假装使用 OpenAI token 价；必须使用 `amortized_subscription` 或 `fixed_request`，并在 profile 中记录摊销依据和负责人。

### 3.2 倍率边界

必须拆开以下三个概念：

1. `provider_cost_multiplier`：内部成本统计使用，不直接扣用户余额。
2. `lane_user_charge_multiplier`：用户选择到该 lane 时使用的销售倍率。
3. `customer_multiplier`：用户/套餐/客户等级倍率，可选。

统一 group 的全局 `rate_multiplier` 在动态 lane 场景建议固定为 `1.0` 或关闭；旧来源 group 的倍率保持原样，由 lane 的 `pricing_source_group_id` 引用。现有 `account.rate_multiplier` 只作为账号/上游成本统计口径，不得直接当作用户收费倍率。上游倍率来源、手动兜底和计量单位必须进入 profile，而不是隐含在 group 默认值中。

### 3.3 公式

```text
provider_unit_price = provider_base_price × effective_upstream_multiplier
lane_price = provider_unit_price × lane_user_charge_multiplier
user_charge = round_up(lane_price × customer_multiplier + fixed_fee)
provider_cost = provider_meter × provider_cost_unit_price × provider_cost_multiplier
```

如果 profile 已经保存了最终销售单价，则不要再次乘上游倍率或 lane 倍率。每次 snapshot 必须保存倍率来源、是否使用手动兜底、基础价格语义和最终有效单价，防止重复乘倍率。

`default_user_charge_multiplier`/`lane_user_charge_multiplier` 是 lane 默认销售倍率；`user_markup_multiplier` 是更具体的 profile/account 覆盖值，二者只取其一。`customer_multiplier` 才是独立的客户或套餐倍率。

### 3.4 价格版本

每个 profile 必须有：

- `pricing_profile_id`
- `version`
- `upstream_rate_mode/source/basis`
- `probe_snapshot_ref`、`probe_received_at`、`probe_fresh_until`
- `manual_fallback_used`、`manual_fallback_reason`
- `manual_upstream_multiplier` 或 `manual_base_unit_price`
- `base_price_semantics`、`user_markup_multiplier`、`fixed_fee`
- `effective_from/effective_to`
- `currency`
- `rounding_mode/precision`
- `charge_trigger`: `completed | delivered`；仅成功完成或成功交付时产生用户费用；`accepted` 只能记录 provider/account 内部成本，不能触发用户扣款
- `partial_output_policy`
- `failure_policy`: 失败、取消、超时、过期、无有效结果时用户费用为 0
- `approved_by/source`

修改倍率只影响新请求，历史 snapshot 永不回算。

## 4. Route Target 与动态选路

现有 `composite_model_routes` 不足以表达同一 public model 下多个 lane/provider/account/price 候选，且其旧唯一索引语义不能直接改写。建议新增统一入口专用的 route target 数据域，不修改旧 route 表含义：

`unified_route_targets`

- `unified_group_id`, `lane_id`
- `public_model`, `match_type`, `endpoint`
- `target_platform`, `provider_identity`, `source_channel`
- `upstream_model`, `adapter_version`
- `pricing_source_group_id`
- `pricing_profile_id`
- `capability_snapshot_id`
- `priority`, `weight`, `enabled`
- `created_version`, `notes`

唯一性至少应允许同一个 `public_model + endpoint` 存在多个 lane；重复 target 必须以 lane/provider/upstream model/version 区分。

### 4.1 V1 选择策略

V1 推荐使用 `fixed_priority`：

1. 先按 public model、endpoint、能力、价格有效性过滤。
2. 再按 lane priority 选择。
3. 在 lane 内按账号 health、schedulable、concurrency、priority 选择账号。
4. 账号确定后再生成 route price snapshot。

后续再增加 cheapest/capacity-aware。自动选择更便宜的 lane 会使同一模型的最终价格变化，必须在客户端显示动态计价或提供报价接口。

### 4.2 同名模型

同一个 `gpt-5.5` 可以同时存在 Plus 和 Pro lane，但 `/v1/models` 不显示一个虚假的固定价格，只显示模型可用和 `billing_mode=dynamic`。如果业务必须价格固定，提供 provider/lane-qualified alias，例如 `gpt-5.5-plus`、`gpt-5.5-pro`。

可选增加：

- `GET /v1/pricing`：返回模型的动态计价规则/价格范围，不泄露账号信息；
- `POST /v1/billing/quote`：根据模型、预计 token、图片参数或视频参数返回报价；
- 响应 usage/管理审计中返回 provider、lane、pricing profile version。

报价不能替代实际 snapshot；最终收费仍以实际选中的账号和 snapshot 为准。

## 5. 一次请求的执行算法

### 5.1 文本/图片

1. 校验 API key 属于统一 Access Group。
2. 根据 public model、endpoint 和请求能力查找 enabled route targets。
3. 过滤没有能力、没有 schedulable 账号或没有有效 profile/倍率来源的 lane。
4. 按 selection policy 选 lane，再选实际账号。
5. 账号确定后，按 `probe_preferred`、`manual_only` 或 `probe_only` 解析上游倍率/单位价格。
6. 从 lane/account/`pricing_source_group_id`/profile 生成并持久化 `route_price_snapshot_id`；快照中保存倍率来源、手动兜底状态和最终有效单价。
7. 若需要预占，按最大预计费用创建 `RESERVED` 记录。
8. 将请求发送给上游。
9. 解析真实 usage/图片结果，按 snapshot 计算用户费用和 provider cost。
10. 原子完成 `usage/audit`、user charge、provider/account cost 和 settlement；或者由 durable outbox 保证崩溃后恢复。
11. 释放多余预占，写入可审计的 route/lane/account/profile 信息。

### 5.2 Grok 视频

1. 创建时完成 lane、账号和 profile 选择。
2. 在向上游发起任务前持久化 snapshot 和 task binding。
3. 预占预计视频费用。
4. 任务状态进入 `CREATING → PENDING`。
5. status/content/poll/callback 均通过 task binding 找原账号，不重新选 lane。
6. 成功得到可交付 URL 后进入 `SETTLING → SETTLED`；按创建时 profile 结算用户费用。
7. 失败、取消、超时、过期或没有有效交付结果时进入对应终态；用户费用一律为 0，释放预占。若 provider 已产生内部成本，只记录 provider/account cost，不向用户扣费。
8. 任何重复轮询/回调只能命中同一 settlement idempotency key。

## 6. 预占、失败和重试

### 6.1 预占状态

建议统一使用：

`QUOTED → RESERVED → CAPTURED / RELEASED / REFUNDED / DISPUTED`

- 文本可按最大输出 token 预占，也可只在余额足够时即时结算。
- 图片通常按预计图片数量/分辨率预占。
- 视频必须在创建阶段预占，完成后按实际结果结算。

### 6.2 重试

- 上游失败：可以在统一入口候选池内切换 lane/account，但失败尝试用户费用一律为 0。
- 切换 lane 后必须重新建立新的实际尝试记录和 snapshot；只有最终成功交付的尝试按其 snapshot 计费。
- 上游已接受任务或已经产生可计量输出后：禁止随机切换；媒体必须保持原账号粘性。
- 统一入口禁止 fallback 到旧 group。

## 7. 实施顺序

### Phase 0：冻结配置

- 建立 lane catalog：Plus、Pro、生图、各 provider/source channel。
- 确定每个 lane 的账号池、模型、endpoint、cost model、倍率和负责人。
- 为每个 lane/account 填写 upstream-rate policy：probe 状态、manual-only/fallback 规则、计量单位、手动倍率或单位价格及有效版本。
- 确定同名模型动态价格还是 qualified alias。
- 确定预占、失败、重试和视频 charge trigger。

### Phase 1：数据库与后台模型

- 新增 unified lane、lane account、route target、pricing profile、snapshot/reservation 数据域。
- V1 先让 route target/lane 引用现有 `pricing_source_group_id`，复用原生 token/image/video 价格和分组倍率；只有无法表达的差异才增加 override/profile 字段。
- 不修改旧 group、旧 route、旧 key、旧价格字段语义。
- 在副本执行 migration forward/down 和崩溃恢复演练。

### Phase 2：目录与解析器

- 统一 `/models` 从可执行、有价格的 route target 闭包生成。
- 解析器返回 lane、account、provider、upstream model、profile。
- 旧入口继续使用原解析器。

### Phase 3：文本/图片结算

- 接入 snapshot、预占、usage、用户 charge 和 provider cost。
- 先上线固定优先级，不启用 cheapest 动态优化。
- 用 Plus/Pro 同名模型和生图模型做隔离 canary。

### Phase 4：Grok 视频

- 接入创建时 snapshot、task binding、状态机、claim/dedup 和预占结算。
- 单独预算和验收，不与文本通过互相替代。

### Phase 5：扩大 provider

- 按 capability、价格和无回归证据逐个加入 DeepSeek、Kimi、Ark/Doubao、Gemini 等 lane。
- 每加入一个 lane，都必须新增 profile、upstream-rate policy、route catalog、测试夹具和回滚项。

## 8. 失败关闭条件

以下任一情况必须在发送上游前拒绝：

- 没有明确 lane；
- 没有实际 schedulable 账号；
- 没有有效 pricing profile；
- 计量单位或价格版本缺失；
- 倍率来源不明确或可能重复计算；
- `manual_only` 链路没有手动倍率、单位价格或 provider-specific 公式；
- `probe_preferred`/`probe_only` 链路的 probe 无效且没有符合优先级的手动兜底；
- token probe 值被用于不匹配的 image/video/per-request 计费；
- snapshot 无法持久化；
- 统一入口 gate/group/key 任一不匹配；
- 试图将旧 group 作为 fallback。

## 9. 最终验收样例

至少完成以下用例后，才可宣布动态计费可用：

1. 同一个 `gpt-5.5` 分别命中 Plus 和 Pro，费用分别使用两个 lane 的倍率。
2. Plus 失败、Pro 成功，最终费用按 Pro snapshot，且两次 attempt 均可审计。
3. `gpt-image-2` 使用 image profile，不走 token 价格。
4. Plus/Pro 倍率修改后，旧请求仍按旧 snapshot，新请求按新版本。
5. 同一账号不能在同一模型/endpoint 下被两个 enabled lane 同时选中。
6. provider cost、user charge、usage/audit 任一写入失败时可由 outbox 恢复，不出现部分账务事实。
7. Grok 创建后改价、停账号、进程重启、重复回调，仍按创建时 lane/account/profile 只结算一次。
8. 统一 gate 关闭后，旧 key、旧 group、旧 `/v1` 和旧计费与基线一致。
9. probe 成功时使用 `probe_declared`；probe unsupported/failed/stale 时使用已审核的 `manual_fallback`。
10. 火山引擎/Ark `manual_only` 链路按手动单位价格或手动倍率计费，不套 ChatGPT token 规则。
11. 自动探测恢复后只影响新请求，手动兜底值保留，历史 snapshot 不回算。
12. 同一模型命中两个不同 lane 时，两个 lane 的倍率来源、单位价格和最终收费均可独立复算。
