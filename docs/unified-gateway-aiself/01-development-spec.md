# 统一上游网关开发规范

## 1. 目标与边界

### 1.1 目标

对外提供一个稳定的统一 API 基址，使调用方可以通过一套认证和 OpenAI 风格的入口发现并调用多个上游 provider 的可用模型，例如 OpenAI、DeepSeek、Kimi、Doubao/Volcengine、Gemini，以及 Grok 视频。调用方只需要选择公开模型名；系统负责在内部选择满足能力、协议、健康度和计费条件的 route/account。

“一个链接”指统一入口的公共 base URL，不代表所有上游都使用同一个协议或同一种计价单位。文本可以优先收敛到 OpenAI-compatible；视频、图片等异步能力必须保留任务创建、查询、结果获取和结算的生命周期。

### 1.2 零干扰边界

统一网关不是对现有入口的覆盖式改造。第一阶段必须采用独立 composite group、独立 key/测试身份和独立开关；现有 key、现有 group、现有 account、现有 `/v1/models` 目录和现有账务不改变。新路由没有可用上游时不能 fallback 到旧 group，以免出现越权、错价或不可解释的链路切换。

推荐先使用独立 hostname；若基础设施不允许，再使用明确隔离的 path（例如 `/unified/v1`），并由反向代理和后端同时做路径回归。不能直接把原有 `/v1` 指向新 resolver。

## 2. 现状与能力差距

### 2.1 已有可复用能力

| 领域 | 当前能力 | 可复用方式 |
| --- | --- | --- |
| API key/group | key 绑定 group，group 决定模型与 provider 作用域 | 新建独立 composite group，不改变既有绑定 |
| 模型目录 | `/v1/models` 具备按 key/group 返回列表的基础 | 改为由启用的 route candidate 聚合，不读取全局所有账号秘密 |
| 复合路由 | 已有 `composite_model_routes`、平台识别、显式/前缀匹配和 endpoint-aware 解析 | 保留解析优先级，但补齐账号池、能力和价格快照 |
| 账号调度 | 现有 schedulable、priority、concurrency、健康/错误状态和 model mapping | route candidate 只引用符合资格的账号 |
| 协议适配 | OpenAI、Anthropic、Gemini、Grok、Kimi、Zhipu、DeepSeek 等已有平台抽象 | 以 adapter 能力矩阵约束可用 endpoint，不把平台名当作协议名 |
| 用量记录 | `usage_logs` 已记录请求模型、上游模型、账号、group、endpoint、token/媒体字段 | 增加最小的 route/pricing snapshot 关联，保留原字段兼容 |
| 账务幂等 | `billing_usage_entries`、usage log 和一次性结算基础已存在 | 扩展到 route/account/pricing snapshot 维度 |
| Grok 视频 | 已有 pending billing、账号绑定、Redis TTL、claim/release 和成功后结算基础 | 在现有生命周期上增加 route/pricing 版本快照，不另起一套重复结算 |

### 2.2 当前不能直接复用的部分

| 风险点 | 原因 | 处理原则 |
| --- | --- | --- |
| group 单一价格 | 一个统一 group 无法表达不同 provider/账号的不同 token、请求、图片或视频价格；现有价格缺失路径还可能告警后写零成本继续 | 引入 route/account 级 pricing profile；group 仅作访问域和兼容默认；新入口未知价格 fail closed |
| route 只绑定 target platform | 现有 route 不能保证最终选中的 account，也没有价格 profile 身份 | 增加 eligible account pool 或等价的 route-account binding |
| composite 模型聚合偏硬编码 | 平台集合与默认模型可能滞后，不能代表实时账号能力 | 从启用 candidate + capability snapshot 聚合，并保留显式白名单 |
| 完成阶段重新取价 | 异步任务期间价格或账号状态可能改变 | 创建任务时保存不可变 billing snapshot；GET status/content 无 model 时也必须按 task binding 找回原账号 |
| 三类账务事实非原子 | 先扣费后 best-effort 写 usage/provider cost 记录可能出现部分事实落库 | 使用同事务或 durable outbox，确保 user charge、usage/audit、provider/account cost 和 settlement 可恢复对账 |
| 失败回退到其他 group | 可能使旧链路被新入口隐式使用，且账务归属不清 | 新 group 内部只在明确候选池内重试；跨 group 禁止隐式 fallback |

### 2.3 统一入口下的计费子组（Billing Lane）

统一 Access Group 只负责 API key、权限和可见范围；Plus、Pro、生图、Grok/KIE/直连等差异必须建模为统一入口内部的 Billing Lane。Lane 绑定账号池、provider/source、能力、选择策略和一个或多个 Pricing Profile，但不复制账号凭据，也不改变既有 group。

同一个 public model 可以属于多个 lane。例如 `gpt-5.5` 可以同时有 `chatgpt-plus-text` 和 `chatgpt-pro-text`；实际请求选中哪个 lane，就使用哪个 lane 的价格 profile。`gpt-image-2` 走 image profile，不能套用 token 价格。Plus/Pro 若无逐请求 provider 账单，应使用 `amortized_subscription` 或 `fixed_request`，并记录摊销依据。

动态 lane 的倍率必须与用户/套餐倍率和内部成本倍率分开：`lane_user_charge_multiplier` 用于用户收费，`customer_multiplier` 可选用于客户层，`provider_cost_multiplier` 仅用于内部成本统计。统一 group 的全局倍率和旧 account 倍率不能在新 lane 中被默默重复相乘。

实施优先级上，V1 不重复录入你已经在 ChatGPT Plus/Pro/生图等现有分组中配置的原生价格：每个 lane 绑定 `pricing_source_group_id`，从来源分组读取普通模型倍率、图片独立倍率、视频独立倍率和模型计价；解析完成后将其冻结到 snapshot。只有来源分组无法表达的链路/账号差异，才添加 lane/profile override。

具体数据域、迁移顺序和预占结算见 [12-final-executable-plan.md](./12-final-executable-plan.md)。

### 2.4 上游倍率来源与手动兜底

统一网关必须允许每条 lane/route 独立声明上游倍率来源：`probe_preferred`、`manual_only` 或 `probe_only`。能够返回兼容 `/v1/sub2api/billing` 且快照仍在 `fresh_until` 内的链路，可以使用自动探测值；火山引擎、Ark、没有该 endpoint 的自定义中转或计费单位与 ChatGPT 不同的 provider，使用 `manual_only` 或 `probe_preferred + manual fallback`。

手动配置不是把 `accounts.rate_multiplier` 改成用户售价，而是写入统一入口专用的 lane/profile 规则。该规则至少要说明 `upstream_rate_basis`、手动倍率或手动单位价格、基础价格是否已含倍率、下游加价倍率、计量单位、舍入方式和生效版本。自动探测失败、unsupported 或过期时，如果没有有效手动兜底，必须在发送上游前 fail closed。

解析器必须在实际账号确定后再生成价格快照，并记录本次采用的是 `probe_declared`、`manual_fallback` 还是 `manual_only`。token 探测值不能直接套用到图片、视频或按次 provider-specific 计费；这些链路必须有自己的手动单位价格或明确的 profile 公式。完整规则见 [13-upstream-rate-and-manual-fallback.md](./13-upstream-rate-and-manual-fallback.md)。

## 3. 逻辑架构

```text
Client
  -> Unified API key / isolated group
  -> Request normalizer + capability guard
  -> Public model catalog
  -> Route resolver
       -> public_model / endpoint / protocol
       -> provider adapter
       -> eligible account pool
       -> health, concurrency, priority, sticky session
       -> immutable route + pricing snapshot
  -> Upstream request
  -> usage / media settlement
       -> user charge
       -> provider/account cost record
       -> audit log and metrics
```

### 3.1 解析上下文

每个请求必须生成一个可审计的 `ResolvedRouteContext`，至少包含：

| 字段 | 说明 |
| --- | --- |
| `request_id` | 一次客户端请求的稳定标识 |
| `public_model` | 客户端提交的公开模型名 |
| `route_id` | 最终选中的复合 route |
| `target_platform` | 实际适配平台/协议族，例如 `openai`、`anthropic`、`grok` |
| `provider_identity` | 真实上游来源/品牌；不能只用传输协议代替 |
| `upstream_model` | 实际发送给上游的模型名 |
| `upstream_endpoint` | 实际调用的 endpoint |
| `account_id` | 最终选中的上游账号；不能只记录 group |
| `adapter_version` | 适配器契约版本 |
| `pricing_profile_id/version` | 本次采用的价格规则及版本 |
| `billing_mode` | token、per_request、image、video 或 provider-specific |
| `upstream_rate_mode/source` | probe_preferred/manual_only/probe_only，以及 probe_declared/manual_fallback/manual_only |
| `upstream_rate_basis` | token、per_request、image、video 或 provider-specific；必须与计量单位一致 |
| `manual_fallback_used` | 是否因为 probe 不可用而使用了人工规则 |
| `selection_reason` | 显式 route、能力匹配、健康/优先级等可审计原因 |
| `route_price_snapshot_id` | 持久化、不可变的路由/价格快照身份 |

Resolver 和 billing 必须共享这份上下文；禁止 billing 重新根据 `public_model` 猜 provider 或 price。`route_price_snapshot_id` 必须在发送上游前持久化，或由等价 durable outbox 保证可恢复；不能只存在内存、Redis pending 或最终 usage log 的推导字段中。

### 3.2 provider identity 与 transport 分离

Ark/Doubao 可能走 OpenAI-compatible transport，但计费和能力应识别为 Volcengine/Ark；Kimi 可能存在 OpenAI 或 Anthropic 入口；Grok 视频是媒体任务协议，不应被当成普通 Chat Completions。字段上要区分：

- `target_platform`：代码 adapter/transport 需要什么。
- `provider_identity`：实际上游是谁。
- `source_channel`：上游账号来自直连、KIE、Subrouter 等哪个来源。
- `pricing_owner`：由谁定义本次用户收费规则。

这样才能避免“同一 OpenAI 协议 = 同一价格”的错误假设。

## 4. 模型目录与公开契约

### 4.1 目录来源

`/v1/models` 只返回以下交集，也就是“可执行且有价格的 route target 闭包”：

1. 统一 group 显式允许的公开模型；
2. 至少存在一个 enabled route candidate；
3. candidate 绑定的账号 schedulable、未被禁用且 adapter 能力满足 endpoint；
4. 该 route 有有效的 pricing profile；
5. provider 模型能力快照未过期，或已通过允许的健康探测。

没有价格或没有可调度账号的模型不得仅因为历史配置残留而出现在统一目录中。

不能把所有账号的 `model_mapping` 并集直接当成目录；如果同名模型跨 provider 形成歧义，目录必须引用明确的 route target，或在无法选择时隐藏/拒绝，不能出现“看得到但 ownership resolver 无法调用”的状态。

### 4.2 公开模型名策略

第一阶段建议保留 provider 语义清晰的公开 ID，例如 `gpt-5.5`、`deepseek-v4-flash`、`doubao-seed-2.0-pro`、`kimi-for-coding`、`grok-imagine-video-1.5`。如果一个 public model 有多个 provider 候选，候选差异只在内部解析和计费中体现；不能用同一个 alias 把不同能力、不同输出格式或不同价格强行伪装成相同模型。

若两个上游都声称支持相同模型名，必须先冻结以下规则之一：

- `[PROPOSED]` 同名模型允许多候选，实际路由价格随 resolved route 结算；或
- `[PROPOSED]` 对外拆成 provider-qualified alias，保证一个公开 ID 对应一个价格域。

本包推荐第一种，但要求返回 usage/审计中明确 provider 和价格版本；若业务必须价格稳定，则采用第二种。

### 4.3 模型能力字段

每个 candidate 至少标记：`text`、`stream`、`vision`、`tools`、`reasoning`、`image_generation`、`video_generation`、`video_polling`、`max_input`、`max_output`、`request_endpoint`、`last_verified_at`。能力未知时应隐藏或阻断，而不是猜测支持。

## 5. 协议分层

### 5.1 文本

第一阶段优先实现 OpenAI-compatible 请求规范：`/chat/completions`、`/responses`、stream、工具调用、错误归一化和 usage 解析。OpenAI transport 只表示传输格式，不代表最终 provider 或价格。

### 5.2 非 OpenAI provider

对于 Anthropic、Gemini、Kimi 原生或其他 provider，adapter 必须明确转换边界、消息/工具/多模态损失、usage 计量和错误码。若无法无损转换，应在 capability catalog 中关闭对应能力，而不是静默降级。

### 5.3 图片与 Grok 视频

图片/视频采用独立 media task contract。Grok 视频至少包含创建、任务状态查询、结果 URL 获取、失败/超时、账号粘性和结算。KIE/Subrouter/直连只是不同的来源链路，不能把它们混成一个无差别 upstream。

Grok 任务建议采用持久状态机：`CREATING → PENDING → SETTLING → SETTLED`，并支持 `FAILED`、`CANCELLED`、`EXPIRED`、`ORPHANED`。创建成功前先保存 route/account/price snapshot；状态查询、结果查询、轮询和未来 callback 都使用 task binding，不重新触发 composite resolver。当前实现中视频状态接口可能不携带 model，因此不能依赖普通请求的 model 解析来找回账号。

### 5.4 对外 API 契约草案

统一入口的第一版公共契约建议保持 OpenAI 风格，同时为媒体保留任务接口：

| 方法 | 路径（相对统一 base URL） | 用途 | 计费注意 |
| --- | --- | --- | --- |
| `GET` | `/v1/models` | 返回统一 group 可见模型 | 只返回有 candidate、能力和价格的模型 |
| `POST` | `/v1/chat/completions` | 文本/流式文本 | 由 adapter 解析实际 usage |
| `POST` | `/v1/responses` | Responses 风格文本 | 保留 public/upstream model 双值 |
| `POST` | `/v1/images/generations` | 图片任务 | 以 image profile 的单位/分辨率计费 |
| `POST` | `/v1/videos` 或 provider task path | 创建视频任务 | 创建时保存 route/account/pricing snapshot |
| `GET` | `/v1/videos/{id}` 或 task status path | 查询视频状态 | 不重新选路、不重新取价 |

媒体路径的最终命名要与现有 sub2api handler 兼容性核对；这里是契约方向，不是已经部署的路由。统一 API 错误至少要区分：无模型、无能力、无价格、无可调度账号、provider 限流、余额不足、上游失败和任务超时；不能把这些情况全部转成同一个 500。

## 6. 路由选择规则（建议冻结）

候选过滤顺序：

1. 公开模型与 endpoint 精确匹配；
2. route enabled 且属于统一 group；
3. provider adapter 能力满足请求；
4. pricing profile 有效且覆盖本次计费维度；
5. 账号 schedulable、未 rate-limited/overload、凭据健康；
6. 遵守 concurrency、priority、sticky session 和 provider-specific 限流；
7. 在候选池内按既定权重/优先级选择；
8. 立即生成不可变 route/pricing snapshot。

同一请求的重试只能发生在候选池内。若第一次尝试已经产生上游副作用（例如视频任务已创建），重试必须带有同一逻辑 request/task id，不能造成双重用户收费。

统一链路的隔离必须同时满足三项：全局 feature gate 开启、请求 key 显式绑定 `unified_v1` composite group、route candidate 显式属于该 group。统一入口请求缺少任一条件时必须直接拒绝；只有原入口请求才继续走原实现。任何 detector、catalog cache、账号不可用降级或旧 fallback 都不得把统一请求带入旧 group。

## 7. 实施分层

### Phase 0：契约冻结

冻结公开模型、route/account 绑定、价格 profile、异步媒体状态机、失败语义、日志字段和隔离方式。此阶段不写代码。

### Phase 1：文本只读目录与内部 dry-run

只在独立环境/数据库副本中生成 candidate catalog，验证模型目录、路由解析和价格覆盖，不向上游发付费请求。

### Phase 2：独立 key 的文本 canary

仅开放少量文本模型和独立 key；先验证 OpenAI-compatible，之后逐 provider 增加。任何未覆盖价格的模型 fail closed。

### Phase 3：媒体小流量

单独为 Grok 视频做受控预算、单独测试账号/route、单独验收 pending/claim/dedup 和失败结算。媒体测试不作为文本“通过”的默认推论。

### Phase 4：扩展与用户验收

通过无回归和账务对账后，才讨论扩大模型范围或切换正式统一域名。旧入口保持原样。

## 8. 未来实现影响面（仅作开发分工输入）

以下是进入编码阶段时需要逐模块设计/审查的影响面；本阶段没有修改这些文件：

| 模块 | 未来责任 | 不能改变的旧行为 |
| --- | --- | --- |
| migration/model | route-account binding、pricing profile、snapshot 关联 | 旧表数据和旧索引语义 |
| catalog | 根据统一 group 生成有能力/有价格的目录 | 旧 key 的 `/v1/models` |
| resolver | 生成 `ResolvedRouteContext`、候选池重试和选择原因 | 旧 group 的 resolver 结果 |
| adapter | provider/transport 转换和 usage 归一化 | 旧 provider adapter 默认路径 |
| billing | route/account/profile 计量、幂等和对账 | 旧 usage log/旧 group 计费结果 |
| media | Grok snapshot、状态机、claim/dedup、失败语义 | 旧视频 pending/settlement 行为 |
| admin | 新 route/profile 的最小权限 CRUD、审计和 preview | 旧 group/route 管理接口的含义 |
| gateway/proxy | 新 hostname/path 与旧入口隔离 | 旧 `/v1`、health、前端路径 |
| tests | contract、unit、integration、canary、rollback | 现有回归测试和旧链路可用性 |

## 9. 成功标准

- 一个统一 key 能看到的模型，均有真实可调度 candidate 和有效价格 profile。
- 每次成功请求都能从 usage log 追溯到 public model → route → provider → account → upstream model → pricing version。
- 同一 provider 的不同账号可按各自规则收费；不存在 group 统一价覆盖实际上游的路径。
- Grok 视频创建后即使完成时账号状态、价格或默认 route 改变，仍按创建时 snapshot 结算且只结算一次。
- 新入口关闭或回滚后，所有既有 key/route/group 的行为与基线一致。
