# 路由级计费与结算契约

## 1. 计费目标

统一入口的用户收费必须与“本次请求实际选中的上游链路”一致：provider、upstream model、endpoint、account 和计费 profile 共同决定价格。不能因为所有请求都进入同一个 composite group，就用 group 的单一价格覆盖 DeepSeek、Kimi、Doubao、OpenAI、Gemini、Grok 等不同来源。

本契约区分三类数据：

1. **用户应付费用**：从用户余额/订阅扣除的金额。
2. **上游/账号成本统计**：用于核算 provider 成本、账号倍率、利润和运营统计，不自动等同于用户应付费用。
3. **审计事实**：实际选择了什么 route、账号、上游模型、价格版本和计量结果。

`account_rate_multiplier`、`account_stats_cost` 等现有字段可以继续用于成本统计，但不能替代用户 charge 的 route pricing snapshot。

## 2. 不可变解析与计费上下文

请求一旦选定候选并准备发送上游，必须生成以下逻辑对象：

```yaml
request_id: stable-request-id
public_model: client-visible-model
route_id: selected-composite-route
target_platform: transport-adapter
provider_identity: actual-provider
source_channel: direct-or-kie-or-subrouter
account_id: selected-account
billing_lane_id: selected-billing-lane
upstream_model: actual-upstream-model
upstream_endpoint: actual-endpoint
pricing_profile_id: versioned-profile
pricing_profile_version: integer-or-digest
billing_cost_model: provider_metered|amortized_subscription|fixed_request|image|video
lane_user_charge_multiplier: decimal
customer_multiplier: decimal
provider_cost_multiplier: decimal
billing_mode: token|per_request|image|video|provider_specific
upstream_rate_mode: probe_preferred|manual_only|probe_only
upstream_rate_source: probe_declared|manual_fallback|manual_only
upstream_rate_basis: token|per_request|image|video|provider_specific
probe_snapshot_ref: nullable-probe-snapshot-and-fresh-until
manual_fallback_used: boolean
manual_upstream_multiplier: nullable-decimal
manual_base_unit_price: nullable-decimal
base_price_semantics: provider_base|final_user_price
user_markup_multiplier: decimal
charge_trigger: completed|delivered
selection_reason: explicit-route|capability|health|priority
route_price_snapshot_id: durable-immutable-snapshot-id
created_at: timestamp
```

上游请求、usage log、媒体 pending record、最终 billing entry 必须引用同一份 snapshot 或其不可变摘要。`route_price_snapshot_id` 必须在发出上游请求前持久化；若采用 outbox，必须能在进程崩溃后恢复并继续对账。后续默认模型、账号状态、group 价格、route priority 变化不得改写已经创建的 snapshot。

## 3. 建议的价格 profile

### 3.1 逻辑实体

建议增加独立的 `composite_route_pricing_profiles` 概念（具体表名可在实现阶段根据项目迁移规范确定），至少覆盖：

| 字段 | 作用 |
| --- | --- |
| `id` | profile 身份 |
| `route_id` | 绑定复合 route |
| `account_id` 或 `account_pool_id` | 绑定具体账号或可选账号池 |
| `public_model` / `upstream_model` | 防止一个 profile 跨模型误用 |
| `endpoint` / `billing_mode` | 区分 text、image、video 和 provider-specific 计量 |
| `input_price`、`output_price`、`cached_price` | token 类价格 |
| `request_price` | per-request 价格 |
| `image_price` / `resolution_price` | 图片或分辨率维度价格 |
| `video_price` / `unit_seconds` / `duration_policy` | 视频计价 |
| `currency`、`rate_multiplier` | 货币与折算规则 |
| `upstream_rate_mode` / `upstream_rate_basis` | 自动探测、手动或自动优先手动兜底，以及倍率适用的计量单位 |
| `upstream_rate_source` / `probe_snapshot_ref` | 本次实际使用的来源和探测快照新鲜度 |
| `manual_upstream_multiplier` | 探测不可用时的人工倍率；按 lane、账号池或账号覆盖 |
| `manual_base_unit_price` / `manual_pricing_rules` | 非 token、按次、图片、视频或 provider-specific 链路的人工基础单价/规则 |
| `base_price_semantics` | 标记基础价格是 provider 成本基准还是已经是最终销售价 |
| `user_markup_multiplier` / `fixed_fee` | 下游加价和可选固定附加费用 |
| `charge_trigger` | `completed`/`delivered`；`accepted` 仅记录 provider 内部成本，不触发用户扣款 |
| `version`、`effective_from`、`effective_to` | 价格版本与生效窗口 |
| `status`、`source`、`notes` | 审核、来源和人工说明 |

如果一个 route 下账号的 provider 价格完全相同，可以绑定到 account pool；只要价格、额度或来源不同，就必须绑定到 account 或可区分的 profile。

现有 `composite_model_routes` 的唯一性和字段模型不能直接表达“同一 group/endpoint/public model 下多个不同 target/account/price”。实现时应冻结新的 `route → versioned target → account eligibility → pricing profile` 关系，不得悄悄改变旧表唯一索引的语义来承载新池。

在统一入口中，`route` 下还必须有明确的 Billing Lane。Lane 是 Plus、Pro、生图、Grok/KIE/直连等“上游产品/账号池”维度；统一 Access Group 只负责访问权限，不提供统一价格。建议将 lane 作为 route target 的显式外键或等价不可变引用。

### 3.1.2 原生分组计价的复用边界

sub2api 现有能力可以作为 V1 的价格来源，但不是自动的链路级动态计费：

- `groups.rate_multiplier`：用户/API key 的分组倍率；
- `groups.image_rate_multiplier` 与 `groups.video_rate_multiplier`：来源分组启用独立图片/视频倍率时生效；
- `groups.model_pricing`：普通模型、按次、图片或视频的分组级价格配置；
- `accounts.rate_multiplier`：账号统计/上游成本维度，源码语义明确不影响用户/API key 扣费；
- channel/account-stats pricing：可作为来源或成本统计规则，但不能代替统一入口的 route/lane 用户收费快照。

因此 V1 应让每个 lane/route target 带 `pricing_source_group_id`，解析实际 lane 后读取该来源分组的原生规则，并把规则、倍率和最终单价写进 snapshot。不能仅把多个账号放在一个 unified group 里，期待原生计费自动知道账号来自 Plus 还是 Pro。

来源分组的 `model_pricing`、`groups.rate_multiplier` 只作为模板读取，不能未经语义标注就与 probe/manual 上游倍率相乘。如果复制出的价格已经是该来源分组对用户的最终销售价，snapshot 必须使用 `base_price_semantics=final_user_price`；只有在价格被明确标记为 provider/base price 时，才应用上游倍率和 lane 用户加价。

### 3.1.1 计价模型与倍率边界

每个 lane/model/endpoint 必须选择一种 cost model：`provider_metered`、`amortized_subscription`、`fixed_request`、`image` 或 `video`。Plus/Pro 没有逐请求 provider 账单时，使用订阅摊销或按次模型，不能伪造 token 价。

倍率必须拆分为：

- `lane_user_charge_multiplier`：用户选中该 lane 后的销售倍率；
- `customer_multiplier`：用户/套餐层倍率，可选；
- `provider_cost_multiplier`：内部成本统计倍率，不直接扣用户余额。

推荐公式：

```text
provider_unit_price = provider_base_price × effective_upstream_multiplier
lane_price = provider_unit_price × lane_user_charge_multiplier
user_charge = round_up(lane_price × customer_multiplier + fixed_fee)
provider_cost = provider_meter × provider_cost_unit_price × provider_cost_multiplier
```

如果 profile 的 `base_price_semantics=final_user_price`，则直接使用最终销售价，不得再次乘 `effective_upstream_multiplier` 或 `lane_user_charge_multiplier`。`route_price_snapshot` 必须同时保存倍率组件、来源、兜底原因和最终有效单价，防止重复乘倍率。统一 group 的全局 `rate_multiplier` 建议在动态 lane 中固定为 1.0；现有 `account.rate_multiplier` 不得在没有语义迁移的情况下直接当作用户收费倍率。

`lane_user_charge_multiplier` 是 lane 默认值；如果 profile 有更具体的 `user_markup_multiplier`，它是替代值而不是额外乘数。`customer_multiplier` 才是独立的客户/套餐层倍率。

### 3.1.3 上游倍率来源与手动兜底

每个 lane/model/endpoint 必须声明 `upstream_rate_mode`：

- `probe_preferred`：探测快照状态为 `ok` 且未超过 `fresh_until` 时使用 `resolved_rate_multiplier`；探测 unsupported、failed 或过期时使用已审核的手动兜底；两者都没有则 fail closed。
- `manual_only`：不依赖探测，直接使用 lane/account 的手动倍率、手动单位价格或 provider-specific 公式，适用于火山引擎、Ark 和其他非 Sub2API 计费链路。
- `probe_only`：只接受有效探测，适用于不允许人工估算的链路。

解析必须在实际账号确定后进行。账号级手动规则优先于 account pool/lane 默认规则；自动探测不会覆盖手动兜底值。`manual_fallback_used`、`upstream_rate_source`、探测时间和 `fresh_until` 必须写入 snapshot。

token 探测倍率只能用于与 token 基础价格匹配的 token 计量。图片、视频、按次或 provider-specific 计量必须提供对应的手动单位价格或独立公式；不能把 ChatGPT token 倍率直接套用到火山引擎等 provider。

### 3.2 价格解析优先级

建议冻结为：

1. 有效的 route + account + model + endpoint 专属 profile；
2. 有效的 route + account pool profile；
3. 明确授权的 route 级 profile；
4. 仅在兼容旧 group 的隔离场景使用 group/channel price；
5. 没有覆盖则 fail closed。

禁止用一个全局默认价、旧 group 默认价或“最接近模型名”的价格静默兜底。价格 profile 的覆盖范围和版本必须在目录发布前通过校验。

价格解析的第一优先级应具体到 `route + lane + account(or account_pool) + public/upstream model + endpoint`；V1 在 lane 没有专属 override 时使用其 `pricing_source_group_id` 的原生模型价格/倍率。只有在账号池内价格明确相同，才允许 profile 绑定 pool。不同 lane 的同名模型不能共享一个无 lane 维度的 unified group 默认价。

上游倍率来源的解析顺序固定为：实际账号专属规则 → account pool 规则 → lane 默认规则；在 `probe_preferred` 模式下，每一层先尝试新鲜 probe，再使用该层的手动兜底。`unsupported`、`failed` 或 stale probe 不能转化为 `1.0`，也不能沿用其他 provider 的倍率。

### 3.3 预占与动态计费

长文本、图片和视频可以先建立 `QUOTED → RESERVED` 预占，再在实际 usage/成功结果确认后进入 `CAPTURED`；多余金额进入 `RELEASED`。失败、取消、超时、过期、无有效结果或 provider 未接受时，用户费用一律为 0 并释放预占；若 provider 已产生内部成本，只进入 provider/account cost 或人工对账，不进入用户 charge。预占和最终扣款必须引用同一个 `route_price_snapshot_id`，不能在结算时重新选择 lane。

## 4. 文本计费契约

### 4.1 成功请求

- 先以 snapshot 决定计量单位和单价。
- 解析上游 usage；若 provider 不返回 usage，必须有经过审核的估算策略，且在 profile 中显式标记，不得默默按另一 provider 的 token 规则计费。
- usage log 记录 requested/public model 与 upstream model 两套值，并关联 route、account、pricing profile version。
- usage log 还必须记录实际采用的 `billing_model`、单位价格或 snapshot digest，避免事后只能用 `result.Model` 猜计费依据。
- 用户扣款与 `billing_usage_entries` 使用稳定 idempotency key，建议为 `request_id + settlement_phase + route_price_snapshot_id`。
- 扣款、usage log、settlement 状态应在同一事务中完成，或通过 durable outbox 保证“已扣费但无用量行”可以自动恢复和对账。

### 4.2 失败、重试和部分输出

建议默认规则：

- 上游在产生可交付结果前失败：无论 provider 是否已经产生内部副作用，用户均不扣款；每次尝试仍记录 provider/account 成本和错误审计。
- 流式请求已经产生部分上游输出但最终未成功交付：按失败处理，用户费用为 0；部分输出只进入 provider/account 成本和审计，不得向用户扣款。
- 同一逻辑请求在候选池内换账号重试：每次尝试单独记录实际 route/account；失败尝试用户费用为 0，只有最终成功交付的尝试按自己的 snapshot 结算一次。
- 任何跨 group fallback 都视为禁止路径，不参与重试。

即使 provider 在 `accepted/created` 阶段已产生不可退内部成本，也只能记录 provider/account cost；本统一入口的用户收费契约固定为“成功完成或成功交付才收费，失败不收费”。

## 5. 图片和 Grok 视频契约

### 5.1 创建阶段

视频 `POST` 成功得到 provider task id 时，立即保存：

- client request id / internal media request id；
- public model、route、provider、source channel、account、upstream model、endpoint；
- 分辨率、时长、帧率/其他影响价格的参数；
- pricing profile id/version、charge trigger、价格快照摘要；
- provider task id、状态和创建时间。

建议的持久状态机为：`CREATING → PENDING → SETTLING → SETTLED`，并支持 `FAILED`、`CANCELLED`、`EXPIRED`、`ORPHANED`。`CREATING` 阶段如果上游已接受但本地 snapshot 写入失败，必须进入可恢复的异常状态并人工/自动对账，不能重新发起一个未知是否重复的任务。

此时不允许在完成轮询时重新 route，也不允许重新读取当前 group 默认价格。创建阶段只允许预占，不产生最终用户扣款；确认有可交付视频 URL 后才按 snapshot 结算。

### 5.2 完成与失败

- 只有满足 profile 定义的成功条件才进入用户 charge，例如 status done 且 video URL 非空。
- provider 返回失败、超时、取消、过期、无有效 URL 或网关未成功交付时，用户费用一律为 0 并释放预占；provider 已产生的内部成本单独记录，不进入用户 charge。
- 结果轮询、webhook、客户端重试必须共享同一 media request id 和 settlement idempotency key。
- 既有 Redis pending TTL、claimed TTL、claim/release 和账号绑定能力必须保留；扩展 snapshot 字段，不得另起一套会重复扣费的结算器。
- status/content 查询可能不携带 model；必须通过持久化 task binding 找回原 route/account，不能重新走普通 resolver，也不能在账号失效后随机切到另一个账号。
- 结果 URL 失效、重复回调、任务状态倒退或 provider task id 冲突都必须落审计并拒绝重复结算。

### 5.3 Grok 特别规则

现有 Grok group 已有分辨率/时长价格基础，但统一入口必须把价格从 group 静态规则提升为 route/account/profile 快照。不同 Grok 来源（直连、KIE、Subrouter）应分别建 candidate，至少记录 source channel 和 upstream endpoint；不能用“都叫 Grok”作为同价依据。

## 6. 账务数据最小扩展

优先复用现有 `usage_logs`、`billing_usage_entries` 和 Grok pending billing；建议最小增加或关联：

- `route_id`；
- `billing_lane_id`、`pricing_source_group_id`；
- `provider_identity` / `source_channel`；
- `pricing_profile_id`、`pricing_profile_version`；
- `settlement_id` 或稳定 idempotency key；
- `charge_trigger`、`partial_output_policy`；
- media 场景的 snapshot digest 和 provider task id。

如果现有字段可以承载，应优先新增结构化关联/JSON 的明确版本，而不是复制一套平行 usage 表。用户 charge、usage/audit、provider/account cost 三类事实必须共享同一事务或 durable outbox 的恢复边界，但仍保持字段和报表语义分离；分别定义各自 idempotency key、状态和补偿规则。任何 schema 变更必须先在数据库副本演练，并提供向后兼容和回滚策略。

当前源码存在“价格缺失时告警并写零成本/继续流程”的兼容行为风险；该行为不能复用为统一入口默认策略。统一入口必须在上游调用前确认有效 profile，否则 fail closed。

## 7. 可解释性与对账

每笔用户扣款至少能够回答：

> 用户提交了什么模型？实际走了哪个 provider、哪个来源、哪个账号、哪个上游模型、哪个 endpoint？使用了哪一版价格？计量是多少？最终扣了多少？是否经过重试或异步任务？

对账需要至少支持：

- 按 public model、provider、source channel、account、route、pricing version 汇总；
- 用户 charge 与 account/provider cost 分开汇总；
- 成功、失败、取消、超时、重复回调分别统计；
- 发现 `pricing_profile_id` 为空、`account_id` 与 route 不匹配、charge 重复或 usage 缺失时报警。

## 8. 必须拒绝的情况

- route 有效但没有有效价格 profile；
- 账号未 schedulable 或不属于 route candidate pool；
- upstream model/endpoint 与 profile 不匹配；
- provider usage 无法解析且没有明确估算策略；
- `manual_only` 链路没有手动倍率、手动单位价格或 provider-specific 计价公式；
- probe 不可用且 `probe_preferred` 链路没有手动兜底；
- `upstream_rate_basis` 与实际 billing mode 不匹配，或 token 倍率被用于 image/video/per-request；
- 基础价格已是最终销售价却再次套用上游/销售倍率；
- media task 没有保存 snapshot 就进入异步完成流程；
- settlement idempotency 冲突或 snapshot digest 不一致；
- 试图跨旧 group fallback；
- 试图使用被历史禁用的账号作为新候选。

## 9. 账务验收样例（设计级）

以下是未来测试夹具，不是线上实际价格：

| 场景 | 预期 |
| --- | --- |
| 同一 public model 选到 OpenAI route | 使用 OpenAI route profile；日志同时保留 provider/account/upstream model |
| 同一 public model 因健康状态选到 Ark route | 使用 Ark route profile，不沿用 OpenAI group price |
| Grok 480p 15 秒成功 | 使用创建时的 Grok 480p/秒 profile，结算一次 |
| Grok 720p 15 秒重复回调 | 仍只产生一笔用户 charge |
| Grok 创建成功、完成失败 | 用户费用为 0、释放预占；provider 已产生的内部成本单独记录 |
| 价格 profile 缺失 | 请求在发送前 fail closed，不产生假账 |
| 上游第一次失败、第二候选成功 | 记录两次实际尝试；第一次用户费用为 0，只有第二次成功交付按第二条 snapshot 收费 |
| 自动 probe 返回 unsupported、存在手动兜底 | snapshot 标记 `manual_fallback`，按该 lane 的手动规则计费 |
| 火山引擎/Ark 无 probe endpoint | `manual_only` 按手动倍率或手动单位价格计费，不套 ChatGPT token 价格 |
| probe 恢复 | 仅新请求切回 probe；历史 snapshot 不回算，手动兜底值不被覆盖 |
