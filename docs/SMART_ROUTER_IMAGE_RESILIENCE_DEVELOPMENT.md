# Smart Router 图片韧性调度开发文档

## 1. 目标与边界

本模块专门处理 OpenAI 兼容图片链路的抖动、长尾超时、同源线路联动失败和
文生图/图生图能力差异。它是现有 Smart Router 的图片插件，不是新的全局
调度器。

覆盖的能力只有：

- `image_generation`：`/v1/images/generations` 和等价的纯文生图请求；
- `image_edit`：`/v1/images/edits` 和等价的图生图/参考图请求。

明确不覆盖：

- 普通 `chat/completions`；
- 普通 `responses`；
- `responses/compact`；
- embedding、计费、用户并发队列和人工启停状态。

### 不可破坏的兼容性约束

1. 不修改账号原始价格优先级、分组、模型映射和人工 `schedulable` 状态。
2. 失败只修改图片能力的运行时状态；同一账号的聊天、Responses、compact
   健康状态完全不受影响。
3. 图片生成和图生图使用独立账本、独立失败计数、独立等待时间和独立恢复。
4. 现有图片请求总预算保持不变，默认仍为 600 秒；本模块只决定单次上游
   尝试等待多久，不得把总预算延长成多个请求的无限等待。
5. 客户端取消、人工关闭、删除账号、明确的认证失效不被当作普通上游抖动。
6. 成功返回有效图片后立即结束请求，不能因为质量判断或路由层逻辑再发起
   第二个生成请求。
7. 功能开关关闭时，所有现有图片调度行为保持不变。

## 2. 当前问题模型

今天的 Aiself 记录显示，图片失败主要由以下几类问题组成：

| 现象 | 典型原因 | 处理原则 |
| --- | --- | --- |
| 502/503/524、EOF、连接超时 | 上游或反代抖动、请求悬挂 | 图片能力软降权并切换线路 |
| 同一来源多条线路一起失败 | 共享供应商、共享出口或共享配额 | 使用 `source_group` 作为故障域，避免连续轰炸同源线路 |
| 成功也超过 100 秒 | 超分、2K/4K 或上游异步处理较慢 | 使用按线路/能力/尺寸的动态等待，不使用一个全局阈值 |
| 文生图成功、图生图失败 | endpoint、multipart、MIME 或能力映射差异 | 两个 capability 独立校准和调度 |
| 403 | 权限、来源、余额或供应商策略 | 先分类，不能把所有 403 当成普通抖动 |
| 400/404 | 参数或 endpoint 不兼容 | 标记能力不兼容，停止无意义重试 |
| 429 | 并发或短时限流 | 使用独立指数退避，不立即占用 30/31/32 降权槽位 |
| 200 但没有有效图片 | 上游假成功或适配器解析失败 | 作为当前图片 lane 失败并切换 |

这些证据说明，单纯缩短固定超时或永久关闭失败账号都会误伤正常的慢线路。
正确做法是把“等待、故障域、恢复和能力”拆开管理。

## 3. 模块结构

建议放在现有 `backend/internal/smartrouter` 下，保持核心无供应商依赖：

```text
backend/internal/smartrouter/
  core/
    image_resilience.go          # 图片专用策略与纯数据类型
    image_timeout.go             # 按 lane/capability 的等待决策
    image_failure_policy.go      # 图片错误分类与动作
    image_recovery.go            # 半开探测和恢复状态机
  image/
    classifier.go                # endpoint、输入模式、尺寸分桶
    source_group.go              # 同源故障域归一化
    ledger.go                    # 账本投影和脱敏记录
```

Sub2API 适配层继续负责账号、数据库、代理、模型映射和真正的 HTTP 调用：

```text
backend/internal/service/smart_router_adapter.go
backend/internal/service/smart_router_calibration_service.go
backend/internal/handler/openai_images.go
```

核心策略只能收到 lane 元数据和结果元数据，不得收到 API key、cookie、完整
Prompt、原始图片或完整上游响应体。

## 4. 图片 lane 标识

图片健康状态不能只按账号记录，至少使用以下逻辑键：

```text
(lane_id, capability, model_family, size_tier, input_mode)
```

其中：

- `capability`：`image_generation` 或 `image_edit`；
- `model_family`：例如 `gpt-image-2`；
- `size_tier`：`1K`、`2K`、`4K`，未声明尺寸使用 `default`；
- `input_mode`：`text_only` 或 `reference_image`。

为了防止状态过度碎片化，只有在以下条件成立时才建立独立 profile：

- 该尺寸或输入模式至少有 3 次有效样本；或
- 账本已证明该线路在文生图和图生图之间表现不同；或
- 线路声明为超分/特殊尺寸专线。

否则回退到 `(lane_id, capability, model_family)` 的通用 profile。

普通聊天和 compact 不得复用这些键，也不得读取图片 penalty。

## 5. 调度顺序

每次图片请求按以下顺序处理：

1. 校验用户、分组、模型映射、人工开关和 endpoint 能力。
2. 根据 endpoint 和请求体识别 `generation` 或 `edit`，不得根据 Prompt
   中出现“人物”“参考”等自然语言改变能力类型。
3. 识别尺寸、是否有参考图、输出数量和适配 profile。
4. 删除本次请求已尝试的 lane；瞬时失败后优先排除本次请求的整个
   `source_group`。
5. 在最低有效价格优先级层内，按健康、负载、动态等待和恢复状态排序。
6. 只有当前层没有可用候选时，才进入下一层；不把高价兜底线路提前到健康的
   低价线路之前。
7. 在同一 `source_group` 内最多尝试一条线路。只有没有其他故障域且总预算
   仍足够时，才允许同源救援尝试一次。
8. 任何一次有效图片返回后立即结束；没有图片的 2xx 视为上游失败。

### 原始优先级与软降权

```text
健康线路：effective_priority = base_priority
第一个被软降权的线路：effective_priority = 30
第二个被软降权的线路：effective_priority = 31
第三个被软降权的线路：effective_priority = 32
```

`base_priority` 永远保存原始价格顺序，30/31/32 只属于对应的图片
`capability + source_group` 运行时恢复槽位。新线路验证成功后直接使用自己的
原始优先级，不需要人工修改代码。

## 6. 图片专用动态等待

### 6.1 单次尝试与总预算分离

现有 600 秒是整条用户请求的总预算。本模块每次选择线路时计算：

```text
attempt_timeout = min(
    lane_profile_timeout,
    remaining_total_budget - fallback_reserve
)
```

`fallback_reserve` 至少保留一个后续候选的最小尝试窗口；如果已经没有其他
故障域，则不再为不可用候选盲目保留时间。

默认建议值，仅在图片插件启用后生效：

| profile | 初始等待 | 最小 | 最大 | 适用场景 |
| --- | ---: | ---: | ---: | --- |
| standard generation | 150s | 45s | 240s | 普通 1K/2K 文生图 |
| standard edit | 150s | 45s | 240s | 普通图生图 |
| slow/specialist | 210s | 75s | 360s | 超分、4K、已知慢线路 |

这些是策略上限，不改变 600 秒总预算。部署初期应保留旧固定等待作为回退，
先以观测结果确认 profile 后再启用路由效果。

### 6.2 计算方法

每个 profile 保存最近最多 32 个脱敏样本，成功样本用于延迟分布，瞬时失败
用于失败 streak。建议计算：

```text
healthy_timeout = clamp(p90_success * 1.25 + 20s, min, max)
flapping_timeout = clamp(last_failure_duration * 0.50, min, healthy_timeout)
```

规则：

- 有至少 5 个成功样本时使用 p90；
- 样本不足时使用 profile 默认值；
- 连续瞬时失败时缩短当前 lane 的下一次等待，给备用线路留预算；
- 慢线路只在它自己的 profile 中变慢，不拖长其他线路；
- 成功返回后逐步恢复 timeout，不根据一次成功立即放大到最大值；
- 400/401/404、人工取消和客户端断开不参与延迟失败计算；
- 总预算不足时，不启动新的上游请求。

## 7. 错误分类与动作

| 类别 | 例子 | 当前请求 | lane 状态 | 其他 capability |
| --- | --- | --- | --- | --- |
| transient upstream | 408/500/502/503/504/524、EOF、连接超时 | 切线 | 短退避 + 软降权 | 不变 |
| concurrency 429 | 并发超限、pending 超限 | 指数退避后再切线 | 不占 30 槽位 | 不变 |
| quota/rate 429 | quota、credits、rate limit | 按供应商窗口退避 | 较长退避 | 不变 |
| transient 403 | 明确写出临时限流/容量不足 | 短退避 + 观察 | 只影响图片 lane | 不变 |
| auth/capability 403 | 权限、余额、来源或能力拒绝 | 跳过当前 lane | 能力级隔离，等待校准 | 不变 |
| payload 400 | 不支持尺寸、格式、参数或体积 | 不对同一请求盲目重试 | 标记请求特征/能力 | 不变 |
| endpoint 404 | 生成/编辑路径不兼容 | 立即跳过 | 标记对应 capability 不兼容 | 不变 |
| client cancelled | `context canceled`、客户端断开 | 立即结束 | 不惩罚 | 不变 |
| empty output | 2xx 但没有图片 | 切线 | 作为上游失败 | 不变 |

429 继续复用现有独立指数退避模块。图片专用策略只负责把它挂到图片 lane，
不把一次并发 429 误判成线路永久故障。

## 8. 半开恢复与 04:00 校准

### 8.1 白天半开探测

软降权不是永久关闭。每条图片 lane 在 cooldown 到期后进入 `probe_due`，
但同一 lane 同时只能有一个探测：

```text
cooling -> probe_due -> probing -> warming_5 -> warming_25 -> normal
                         \-> cooling
```

白天半开探测要求：

- 只用真实账号适配器、真实代理和真实 endpoint；
- 使用最小、低成本、无副作用的有效图片请求；
- 同一 source group 同一时间最多一个探测；
- 探测失败只延长该图片能力的退避，不永久关闭账号；
- 探测成功先进入 5% warming，后续成功再升到 25% 和 normal；
- 所有线路都在退避时，只允许一个救援探测，禁止并发探测风暴。

### 8.2 上海时间 04:00 校准

04:00 的目的不是只测试当前健康线路，而是恢复之前被软降权、后来可能已
恢复的线路：

1. 先读取过去 24 小时生产账本和图片健康事件。
2. 稳定且文生图/图生图表现一致的 lane，只测一次文生图。
3. 新线路、无有效证据、近期失败或两种能力表现不同的 lane，分别测试
   文生图和图生图。
4. 通过的 capability 清除 Smart Router 临时 penalty，恢复该 lane 保存的
   `base_priority`。
5. 失败的 capability 继续保留 30/31/32 后置状态，设置下一次探测时间，
   不永久关闭账号。
6. 人工关闭、删除、明确认证失效、余额耗尽和确定性配置错误的 lane 不由
   04:00 自动重新打开。

校准任务必须是应用内任务，复用现有账号适配器和唯一运行记录；不能额外用
systemd 直接调用图片 API，否则会绕过调度并产生重复扣费。

## 9. 账本与可观测性

每次生产尝试和校准尝试都写入图片健康账本。至少包含：

```text
request_id
lane_id
account_id
source_group
capability
model_family
size_tier
input_mode
output_count
attempt_index
switch_count
status_code
failure_class
latency_ms
timeout_budget_ms
cooldown_until
effective_priority
base_priority_snapshot
action
sanitized_error_summary
policy_revision
```

禁止保存 token、key、cookie、账号密文、完整 Prompt、原始图片和完整上游响应。

管理端和日志应能直接回答：

- 一次用户请求实际尝试了哪些 source group；
- 每条线路等待多久后失败或成功；
- 是否发生了同源连续尝试；
- 当前线路是健康、冷却、半开还是 30/31/32 后置；
- 文生图和图生图各自的成功率、p90/p95 和失败类型；
- 是否因为候选池耗尽而最终返回 502。

## 10. 新线路自动接入

新增或修改账号、base URL、模型映射、图片能力、分组或启用状态时，自动
enrollment 负责发现并验证：

1. 进入 `pending_verification`，不改变人工配置的价格优先级。
2. 声明或推断支持图片时先测文生图。
3. 账本证明文生图/图生图可能不同，或显式声明支持编辑时，再测图生图。
4. 成功后清除旧的 Smart Router 临时状态，以原始价格优先级参与调度。
5. 瞬时失败后进入 30/31/32 低优先级候选，仍保留自动恢复机会。
6. 确定性 400/404/认证错误只标记相应 capability，不影响聊天和 compact。

事件触发负责快速接入，定时 reconciliation 负责补偿数据库手工修改、旧版本
导入和事件丢失。整个流程不需要为新线路修改代码、增加 URL 白名单或手工
编辑路由表。

## 11. 分阶段实施

### Phase 0：只读审计

- 增加图片 profile、source group、输入模式和尺寸分桶的日志投影；
- 只计算建议 timeout 和候选顺序，不改变实际路由；
- 连续观察至少 24 小时；
- 确认普通 chat、Responses、compact 的日志和健康状态完全不变。

### Phase 1：单 capability 灰度

- 先只对 `image_generation` 开启动态等待和同源保护；
- `image_edit` 继续使用当前策略，避免同时改变两种请求；
- 只允许已存在有效成功样本的线路进入自适应 timeout；
- 没有样本的线路使用现有 180 秒默认值。

### Phase 2：图生图独立启用

- 验证 multipart、MIME、endpoint 和参考图格式；
- 对已证明生成/编辑表现不同的线路分别建立 profile；
- 成功后再启用图生图的动态等待和半开恢复。

### Phase 3：恢复和自动接入

- 启用白天半开探测；
- 启用 04:00 账本驱动校准；
- 启用新增图片线路自动 enrollment；
- 保留一键关闭图片插件的回退开关。

## 12. 验收与回归

### 图片专用回归

1. generation 与 edit 的健康状态、失败计数和 timeout profile 互不污染。
2. 同源第一条线路 502 后，本次请求优先切换不同 source group。
3. 没有其他 source group 时，同源救援最多一次。
4. 502/524/EOF 会切线；400/404 不会对所有线路盲目重试。
5. 并发型 429 使用 5/10/20/40 秒有界退避，不首次占用 30/31/32。
6. 单条慢线路不会把其他线路的 timeout 一起拉长。
7. 600 秒总预算内不会启动预算不足的新请求。
8. 200 但无图片时会切线，不能把空结果当成功。
9. 客户端 `context canceled` 不降权、不重放。
10. 成功校准恢复原始优先级，失败校准仍保留低优先级自动恢复机会。
11. 新增图片线路测通后自动进入原始优先级，不需要代码修改。

### 非图片不变性回归

1. chat、Responses、compact 的候选集合、优先级和健康账本不受图片 502/403/
   404/超时影响。
2. compact 仍要求真实、非空的 `encrypted_content`；图片探测不能被当作
   compact 探测。
3. 普通链路的 429 仍走现有策略，不读取图片专用 timeout。
4. 关闭图片插件后，图片也恢复到当前正式行为。
5. 不修改账号 `priority`、`schedulable`、分组和模型映射。

## 13. 推荐配置形态

具体字段名称以代码实现为准，建议保持图片插件独立、默认关闭：

```yaml
gateway:
  smart_router:
    image_resilience:
      enabled: false
      generation_enabled: true
      edit_enabled: true
      standard_default_seconds: 150
      standard_min_seconds: 45
      standard_max_seconds: 240
      specialist_default_seconds: 210
      specialist_min_seconds: 75
      specialist_max_seconds: 360
      p90_multiplier: 1.25
      safety_margin_seconds: 20
      fallback_reserve_seconds: 45
      sample_window_size: 32
      max_same_source_attempts: 1
      half_open_enabled: false
```

启用顺序必须是 `image_resilience.enabled`、`generation_enabled`、
`edit_enabled` 分开控制。任何新字段缺失时使用当前正式行为，而不是改变
普通链路默认值。

## 14. Current implementation mapping

The first implementation keeps the module inside the existing Sub2API Smart
Router package so it can be hot-updated without introducing a second service:

- `backend/internal/smartrouter/core/adaptive.go`: image model/size/input keyed
  timeout profiles and the bounded metadata ledger.
- `backend/internal/smartrouter/core/router.go`: image-only same-source guard
  and optional single half-open probe after the current priority layer.
- `backend/internal/service/smart_router_adapter.go`: feature flags, profile
  selection, request budget overlay, and automatic account metadata wiring.
- `backend/internal/service/openai_image_upstream_timeout.go`: per-attempt
  timeout and observation hooks for both Images and Responses image paths.
- `backend/internal/config/config.go`: validated `image_resilience` settings,
  disabled by default for compatibility.

The existing health tracker and calibration service remain the source of truth
for soft recovery and the 04:00 Shanghai calibration. This patch does not
persist account priority 30/31/32 or alter non-image health keys.

## 15. 成功标准

在不降低普通聊天、Responses、compact 成功率的前提下，图片链路应达到：

- 同一逻辑请求不再因同源线路连续失败而浪费全部尝试预算；
- 502/524/EOF 的失败能在总预算内及时切换；
- 慢但真实可用的超分线路不会被全局固定超时误杀；
- 文生图和图生图能够分别识别、分别恢复；
- 临时失败的线路不会永久消失，04:00 或半开探测后可自动回归；
- 新增并测试通过的图片线路无需修改代码即可进入调度；
- 所有决策都有可审计账本，但不泄露密钥和用户内容。
