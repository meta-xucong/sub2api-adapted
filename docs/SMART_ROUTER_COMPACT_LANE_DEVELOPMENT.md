# Smart Router Compact Lane 开发文档

## 1. 文档目的

本文定义 Sub2API Smart Router 对 OpenAI Responses 远程压缩请求的通用适配方案。
目标是在不修改上游实现的情况下，由 aiself 或任意 Sub2API 部署完成以下工作：

- 正确识别 `responses` 与 `responses_compact` 两类请求；
- 将远程压缩与普通聊天使用独立的能力健康状态；
- 上游抖动时软降权、限次切换，不永久关闭账号；
- 每日按账本决定是否探测以及探测几次；
- 上游恢复后逐步恢复原始价格优先级；
- 新增线路时无需手工修改复杂调度代码。

本文不是上游供应商兼容性承诺。若上游没有真正的 compact 能力，Smart Router 只能快速识别、隔离和恢复，不能凭空生成合法的 OpenAI 加密压缩结果。

## 2. 官方协议边界

OpenAI 官方将远程压缩定义为独立操作：

```text
POST /v1/responses/compact
```

成功结果是 `response.compaction`，输出中必须包含一个 `type: "compaction"` 项，以及供后续请求使用的 `encrypted_content`。官方示例也是直接调用 `/v1/responses/compact`，而不是把请求改写成普通 `/v1/responses`。

参考：

- [OpenAI API Reference: Compact a response](https://developers.openai.com/api/reference/resources/responses/methods/compact)
- [OpenAI API Guide: Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)

### 2.1 上游内部别名不能当作官方模型

当前 aiself 日志中的 `gpt-5.6-terra-openai-compact` 不在官方 compact 接口公开模型列表中。它应被视为供应商内部模型映射或路由别名，而不是 Smart Router 的默认模型名。

因此必须遵守：

1. 普通 `gpt-5.6-terra` 成功，不代表 compact 成功；
2. compact 失败，不得把普通聊天账号整体禁用；
3. 只有真实 compact 探测返回合法 `response.compaction` 和 `encrypted_content` 后，才能标记该账号的 compact 能力为可用；
4. 不得把普通模型盲目改写成 `*-openai-compact`；映射只能由账号级配置或探测结果决定。

## 3. 当前代码审计结论

当前 Sub2API 已经具备一部分可复用能力：

| 已有能力 | 当前状态 | 处理方式 |
| --- | --- | --- |
| `/responses/compact` 路径识别 | 已有 | OpenAI service 层识别并透传 |
| compact 模型映射 | 已有 | 账号级 `compact_model_mapping` |
| compact 支持状态 | 已有 | `openai_compact_mode`、`openai_compact_supported` |
| compact 连接测试 | 已有 | 管理端测试模式和持久化结果 |
| compaction SSE item 补全 | 已有 | 保留 `compaction` 和 `encrypted_content` |
| Smart Router 软降权 | 已有 | lane 级临时 penalty，不改原始 priority |
| 每日校准账本 | 已有 | 图片 generation/edit 与 compact 共用 04:00 账本 |
| Smart Router 核心能力枚举 | 已完成 | 已加入 `responses_compact` |
| compact 独立健康账本 | 已完成 | 使用统一 `(lane_id, capability, model_family)` 键 |

结论：**compact 已经统一进 Smart Router。** OpenAI service 仍负责协议识别和账号资格过滤，Smart Router 负责 compact lane 的排序、软降权、健康隔离和校准；两层边界清晰，且不影响普通 responses 与图片 lane。

### 3.1 本次回归定位与修复

在 2026-07-13 的 5.5/5.6 复现中，问题不依赖具体模型：compact 账号资格筛选仍然存在，但 `buildOpenAISelectionOrder` 曾明确跳过 Smart Router，`RequireCompact` 也没有传递为独立能力键。结果是更新后 compact 请求可能沿普通聊天/静态 compact 顺序命中错误线路，失败会被误认为模型或上游普遍不可用。

已修复：

- compact 请求强制使用 `responses_compact` 能力键；
- 已验证支持和未知支持仍分层，但每一层内部进入 Smart Router；
- compact 健康状态与普通 `responses`、图片 generation/edit 隔离；
- 已有显式 Smart Router capability map 的 OpenAI 账号会自动兼容 compact，除非明确 `force_off`；
- 生产请求和 04:00 探针都会写 compact 健康账本；
- 探针只在响应含合法 compaction item 和非空 `encrypted_content` 时记成功。

## 4. 目标架构

### 4.1 新增统一能力

在 `internal/smartrouter/core` 增加：

```go
const CapabilityResponsesCompact Capability = "responses_compact"
```

请求分类结果应当是：

```text
POST /v1/responses                  -> responses
POST /v1/responses/compact          -> responses_compact
POST /v1/images/generations         -> image_generation
POST /v1/images/edits                -> image_edit
```

若某些客户端把 compact 作为特殊 `/responses` 请求发送，必须依据请求结构和预期响应协议识别；不能只根据模型名猜测。

### 4.2 统一健康键

所有 Smart Router 健康状态使用同一套键结构：

```text
(lane_id, capability, model_family)
```

例如：

```text
(aiself-account-84, responses, gpt-5.6-terra)
(aiself-account-84, responses_compact, gpt-5.6-terra)
(aiself-account-84, image_generation, gpt-image-2)
```

这保证了：

- compact 失败只影响 compact lane；
- 普通聊天成功不会错误治愈 compact；
- 图片生图和图生图仍然可以继续独立；
- 同一个账号可以同时拥有多个能力状态。

### 4.3 统一插件链

`responses_compact` 进入和图片、聊天相同的策略链：

```text
CapabilityFilter
  -> ModelMapping
  -> ManualDisableFilter
  -> SoftPenalty
  -> LoadGuard
  -> AdaptiveTimeout
  -> BudgetGuard
  -> Failover
  -> Ledger
```

插件只接收 lane、模型族、能力、剩余预算、当前负载和错误分类等元数据，不接收 token、key、cookie、完整 prompt 或图片内容。

## 5. compact 调度规则

### 5.1 候选过滤

按以下顺序处理：

1. 过滤人工关闭、不可调度和模型不匹配的账号；
2. 过滤不支持 `responses_compact` 的账号；
3. 应用该能力的临时 penalty 和 cooldown；
4. 保持原始价格 priority 层级；
5. 在同一有效 priority 层内按健康、负载、队列、延迟和成本择优；
6. 当前请求已经尝试过的 lane 和 source group 不再重复尝试。

未知 compact 能力的账号不能直接永久排除。首次部署或新账号接入时，状态为 `unknown`，允许一次受控真实请求或校准探针来建立证据。

### 5.2 失败动作

| 失败 | 影响范围 | 动作 |
| --- | --- | --- |
| `502/503/504` | 当前 lane + `responses_compact` | 软降权、短暂冷却、允许备用 lane |
| 连接 EOF、stream disconnect | 当前 lane + `responses_compact` | 软降权，不并发重放 |
| `No available channel` | 当前 lane + `responses_compact` | 标记能力不可用，等待校准 |
| 合法但缺失 compaction item | 当前 lane + `responses_compact` | 协议失败，禁止继续使用该能力 |
| `401`、持续性 `403` | 账号或凭据 | 长退避并告警，不由普通聊天自动恢复 |
| 确定性参数 `400` | 请求特征或能力映射 | 不重试同一请求，记录 payload 特征 |
| `context canceled` | 请求本身 | 不惩罚上游，不触发降权 |

### 5.3 软降权规则

软降权只改变运行时有效顺序：

```text
effective_priority = configured_priority + health_penalty
```

原始 `account.priority` 不被修改。建议 penalty 分段为 `0、10、20、30`；因此一个原始 priority 为 1 的线路，在连续失败期间会表现为 11、21 或 31，但不会被删除，也不会永久关闭。

同一请求最多使用一个主 compact 上游和一个备用 compact 上游。禁止并发向多个账号发送同一压缩请求，避免重复消耗和产生多个不可预测的压缩结果。

## 6. 官方协议透传要求

### 6.1 请求侧

Smart Router 必须保留：

- `model`；
- `input` 中的消息、assistant item 和既有 compaction item；
- 客户端的请求 ID、认证上下文和流式协商头；
- `store`、`stream` 等与协议有关的字段。

仅允许做账号级 compact 模型映射，且映射结果必须在该账号的配置范围内。

### 6.2 响应侧

不得把响应压缩成普通文本，也不得用自定义摘要替代加密内容。终态校验至少包括：

```text
HTTP 2xx
object == response.compaction，或供应商已验证的等价兼容对象
output 中恰好一个 compaction item
compaction.type == compaction
encrypted_content 非空
```

SSE 转 JSON、反代桥接或错误包装都必须保留 `encrypted_content`。若供应商返回 `compaction_summary` 等非官方别名，只能在明确兼容测试通过后做内部归一化，不能把任意文本 item 当成压缩结果。

## 7. 探测与账本

### 7.1 compact 专用探针

探针必须调用真实 `/v1/responses/compact`，使用最小、安全、固定的输入，不使用用户 prompt。每个候选账号低并发顺序执行，记录：

```text
account_id
lane_id
requested_model
mapped_model
endpoint
status_code
response_object
has_compaction_item
has_encrypted_content
latency_ms
error_class
error_summary
probe_at
```

不得记录 token、key、cookie、完整输入或完整加密内容。

### 7.2 每日 04:00 校准

使用 `Asia/Shanghai` 计算每日 04:00，数据库保存 UTC。校准开始前先读最近 24 小时账本：

| 账本状态 | compact 探测策略 |
| --- | --- |
| 新账号或没有证据 | 探测 1 次，失败再按策略决定是否重测 |
| 最近出现 502/503/连接断开 | 探测 2 次，低并发 |
| 最近出现 `No available channel` | 先探测 1 次，仍失败继续降权 |
| 连续稳定成功 | 1 次轻量探针 |
| 最近发生协议格式错误 | 重新做完整响应结构校验 |

成功后进入 `warming_5`，再逐步进入 `warming_25` 和 `healthy`。刚恢复的线路不能立刻承接全部 compact 流量。

## 8. 配置设计

建议将 compact 配置纳入 Smart Router，而不是让用户逐账号手工开关：

```yaml
gateway:
  smart_router:
    enabled: true
    compact:
      enabled: true
      max_attempts: 2
      max_parallel_attempts: 1
      unknown_accounts_allowed: true
      soft_penalty_step: 10
      soft_penalty_max: 30
      recovery_probe_timezone: Asia/Shanghai
      recovery_probe_hour: 4
      recovery_probe_minute: 0
      probe_timeout_seconds: 180
      validate_response_schema: true
```

配置优先级：

1. 人工 `force_off` 最高，表示明确不允许 compact；
2. 人工 `force_on` 只表示允许尝试，不得伪造成功能力；
3. 自动探测结果决定 `auto` 模式下的实际优先级；
4. Smart Router 运行时 penalty 不写回人工 priority。

## 9. 是否可以“部署后默认生效”

### 9.1 架构结论

**可以统一进 Smart Router，而且适合做成默认能力。** compact 只是另一种 capability，不应该维护一套独立调度器。

### 9.2 当前版本结论

**当前代码还不能仅靠部署就完全默认生效。** 原因是：

- `gateway.smart_router.enabled` 当前默认值仍是 `false`；
- 新增 `gateway.smart_router.max_attempts_compact`，默认值为 `2`；
- compact 证据字段和 compact 探针策略已经进入统一账本；
- 现有 `openai_compact_model` 默认值属于兼容性映射，不等于每个上游都支持该操作。

### 9.3 推荐的默认启用方式

分两层实现：

**第一层：代码内置、无配置即可生效**

- 路径识别、协议透传、响应结构校验始终开启；
- `responses_compact` 作为核心 capability 注册；
- 未知账号自动进入受控候选，不需要逐账号改配置；
- 失败自动软降权，成功自动恢复；
- 普通聊天、图片和 compact 的健康状态相互隔离。

**第二层：Smart Router 运行策略默认开启**

- 新版安装模板默认 `gateway.smart_router.enabled=true`；
- 已有部署升级时通过配置迁移显式写入，而不是静默覆盖用户的全局开关；
- 用户手动关闭 Smart Router 时，仍保留 compact 协议透传和结构校验，但不启用动态切线。

这样既能做到“新部署默认生效”，又不会在官方升级时无提示改变旧实例的全局调度行为。

## 10. 兼容性与安全边界

- 不修改用户下游 URL；
- 不修改人工分组、计费倍率和原始 priority；
- 不把 compact 失败扩散为普通聊天失败；
- 不把普通 `gpt-5.6` 成功当成 compact 成功；
- 不打印 token、key、cookie、完整 prompt 和 `encrypted_content`；
- 不把上游确定性 400 重试数分钟；
- 不因为客户端 `context canceled` 惩罚上游；
- Smart Router 关闭时可以回滚到旧调度器；
- 删除或重建容器前必须备份 Smart Router 账本和运行配置。

## 11. 测试验收

### 单元测试

- `/responses` 分类为 `responses`；
- `/responses/compact` 分类为 `responses_compact`；
- compact 失败不会改变普通 responses 的健康状态；
- 502/503/EOF 只产生 compact penalty；
- `context canceled` 不产生 penalty；
- 原始 priority 不被修改；
- `force_off` 永远不进入 compact 候选；
- unknown 账号可以被一次受控探测选中；
- 合法 compaction 响应必须包含非空 `encrypted_content`；
- SSE 转 JSON 不丢失 compaction item；
- 单个逻辑请求不会并发创建多个 compact 上游请求。

### 集成测试

1. 两个 compact 账号：第一条返回 503，第二条返回合法 `response.compaction`，最终请求成功；
2. 所有 compact 账号返回 `No available channel`，快速返回结构化 503，不长时间悬挂；
3. 普通 `gpt-5.6-terra` 成功、compact 失败，下一次普通聊天仍走原线路；
4. 04:00 校准成功后，线路从 penalty 状态进入 warming；
5. 校准失败后，线路仍保留，不写入永久禁用；
6. 重启容器后，账本状态和 soft penalty 恢复；
7. Smart Router 关闭时，旧调度行为和 compact 透传仍可回滚。

### aiself 实机验收

必须输出脱敏后的：

```text
client_request_id
operation
selected_account_id
mapped_model
upstream_status
response_schema_valid
attempt_count
total_latency_ms
```

不能输出密钥、账号密文、完整请求体或加密内容。

## 12. 开发顺序

1. 将 `responses_compact` 加入 Smart Router core capability；
2. 将 compact 分类、模型映射和响应校验接到统一插件链；
3. 扩展健康账本和每日校准计划；
4. 加入软降权和有界 failover；
5. 完成单元、集成和重启恢复测试；
6. 新建部署模板默认开启，旧部署通过迁移显式开启；
7. 先在 aiself 灰度，再复制到 404token；
8. 官方升级后运行补丁审计，若官方已收录某项能力则删除重复覆盖。

## 13. 最终判断

这套方案可以统一进入 Smart Router，并且从长期维护角度看，这是正确的归属位置。需要保留的独立边界只有两个：

1. compact 的 OpenAI 协议适配器，负责 `/responses/compact` 的请求和响应完整性；
2. Smart Router 的通用调度器，负责能力隔离、软降权、限次切换、账本和恢复。

二者合并后，新线路只要被 Sub2API 识别为 OpenAI-compatible account，就会自动进入 `auto` 探测和调度，不需要手工修改复杂代码。唯一不能自动解决的是：上游根本没有 compact 通道，或者返回的不是合法 compaction 响应；这种情况只能被快速识别并转给另一条真正支持的线路。
