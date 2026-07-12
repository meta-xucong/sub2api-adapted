# Sub2API Smart Router 健康调度与自动校准开发文档

## 1. 文档定位

本文是 Smart Router 的第二阶段开发规格，建立在以下基础能力之上：

- lane、capability、source group 抽象；
- 成本、负载、健康度综合选择；
- 临时冷却和同源线路保护；
- `/v1/images/generations` 与 `/v1/images/edits` 的独立转发链路。

本文新增四个能力：

1. 失败后动态后置，但不修改原始价格优先级；
2. 文生图、图生图按 capability 独立维护健康状态；
3. 每日上海时间 04:00 根据账本自动校准；
4. 把生产请求和校准探针统一写入可审计账本。

目标是让低价线路尽量优先，同时避免 7646881、流云 AI 这类抖动或能力不完整的线路反复拖慢请求。线路恢复后自动灰度回流，不再永久关闭。

## 2. 不变的原则

### 2.1 原始优先级不可被覆盖

账号或线路的原始价格优先级称为 `base_priority`。Smart Router 只能计算临时的 `health_penalty`，不能直接修改价格配置、分组优先级或人工开关。

```text
effective_priority = base_priority + health_penalty
```

恢复时只清除 `health_penalty`，从而回到原始价格排序。

人工关闭使用独立的 `manual_disabled` 状态，自动校准不得覆盖。

### 2.2 健康状态必须按能力拆开

同一个账号可能出现：

- chat 正常，image_generation 失败；
- image_generation 正常，image_edit 失败；
- 文生图能用，图生图需要特殊适配。

因此健康键必须至少包含：

```text
(lane_id, capability, model_family)
```

不能因为流云 AI 文生图失败，就关闭它的图生图能力。

### 2.3 单次请求必须有边界

- image generation 默认最多尝试 2 个不同 source group；
- image edit 默认最多尝试 2 个不同 source group；
- 同一个 source group 默认一次请求只尝试 1 条 lane；
- 客户端已经取消后，不再启动新的上游请求；
- 收到有效图片后立即结束，不能因质量层或调用层误判再发第二个顶层请求。

## 3. 运行状态机

```text
healthy
  -> suspect
  -> cooldown
  -> probe_due
  -> probing
  -> warming_5
  -> warming_25
  -> healthy

capability_error -> quarantined_capability -> probe_due
auth_error       -> quarantined_account  -> manual_or_probe
manual_disabled  -> manual_disabled      -> manual_enable_only
```

### 3.1 状态含义

| 状态 | 生产流量 | 校准行为 |
| --- | --- | --- |
| `healthy` | 按原始优先级和动态权重参与 | 按账本决定是否探测 |
| `suspect` | 降权，仍可少量使用 | 到期后探测 |
| `cooldown` | 降低有效优先级但保留候选资格；全池异常时仍可作为最后兜底 | 冷却/校准后进入恢复探针 |
| `quarantined_capability` | 只跳过该能力 | 单独探测该能力 |
| `warming_5` | 只接收目标权重约 5% | 连续成功后升级 |
| `warming_25` | 只接收目标权重约 25% | 安静窗口后升级 |
| `healthy` | 恢复正常 | 继续记录生产证据 |
| `manual_disabled` | 永不自动选择 | 只能人工启用 |

### 3.2 恢复的滞后保护

一次成功不能立即恢复满流量：

- 一次探针成功：进入 `warming_5`；
- 再一次探针或真实生产成功：进入 `warming_25`；
- 连续成功并经过安静窗口：回到 `healthy`；
- 任一阶段出现同类失败：重新进入冷却。

这样可以避免“刚恢复就被打满、再次失败、再次永久关闭”的循环。

## 4. 失败分类与动作

失败分类必须保存 `failure_class`、`failure_scope` 和 `evidence`，不能只看 HTTP 状态码。

| 类型 | 典型例子 | 作用范围 | 默认动作 |
| --- | --- | --- | --- |
| `transient_upstream` | 408、500、502、503、504、524、EOF | lane + capability | 降低有效优先级并记录冷却提示，不硬关闭 |
| `rate_limited` | 429、明确限流文案 | lane/capability/source group | 冷却并降低并发 |
| `capability_mismatch` | 不支持模型、参数或 endpoint | lane + capability | 长退避，等待能力探针 |
| `upstream_text_reply` | 生图返回“请上传参考图”等文字 | lane + capability | 判定能力不兼容，不反复重试 |
| `auth_forbidden` | 持续 401、确定性 403 | account 或 lane | 长退避，必要时告警 |
| `payload_rejected` | 尺寸、格式、请求体过大 | 请求特征或 capability | 只标记对应特征，不惩罚整个账号 |
| `content_rejected` | Prompt 或图片内容策略拒绝 | request | 不惩罚上游 |
| `client_cancelled` | `context canceled`、客户端断开 | 无 | 不惩罚上游，不再切线 |
| `empty_output` | HTTP 200 但没有有效图片 | lane + capability | 作为上游失败处理 |

### 4.1 7646881 的默认策略

7646881 的 502、超时、EOF 应视为抖动：

```text
第一次失败       -> 有效优先级后置一档
15 分钟内两次失败 -> 再后置一档并降低并发
连续三次失败     -> 继续后置，但仍保留最低权重尝试机会
校准成功         -> 5% 灰度回流
连续成功         -> 恢复原始优先级
```

它不能因为一次失败被永久改成 `error` 或永久从分组删除。

### 4.2 流云 AI 的默认策略

流云出现 `upstream_text_reply` 时，只把 `image_generation` 标为不兼容；`image_edit` 仍需单独测试。对于这种确定性能力错误，30 秒冷却通常过短，应进入长退避或直接等次日校准。

## 5. 调度算法

### 5.1 候选过滤

按以下顺序过滤：

1. 用户、分组、模型映射和人工开关；
2. capability 与模型支持；
3. lane/account 是否可调度；
4. capability、账号和 source group 的健康惩罚；
5. lane、账号和 source group 并发；
6. 当前请求已尝试的 lane/source group。

### 5.2 价格和健康的关系

先按 `effective_priority` 分层，再在可用层内按负载和健康分布：

```text
健康的低价线路
  > 低价线路临时退避
  > 健康的高价兜底线路
```

同一价格层内使用加权选择或平滑加权轮询，避免固定命中第一条线路。健康线路也应保留少量真实流量，保持热身。

建议有效分数：

```text
score = base_weight
      * health_score
      * recovery_weight
      * availability_weight
      / (1 + current_concurrency)
      / (1 + queue_length)
```

`cost_multiplier` 只影响同层选择和成本排序，不允许把便宜但明确不兼容的线路强行选中。

### 5.3 同源线路保护

plus、pro、fallback 如果来自同一供应商或同一上游账号池，必须共享 `source_group`。同一次请求中，一个 source group 发生源头级失败后，默认跳过该组其它 lane。

这能避免：

```text
7646881-plus 失败
-> 7646881-pro 失败
-> 7646881-fallback 失败
```

被错误地当成三次独立高可用尝试。

## 6. 健康账本

### 6.1 记录模型

建议以追加事件为事实源，以状态表为当前投影：

```text
smart_router_health_events
smart_router_lane_state
smart_router_calibration_runs
smart_router_calibration_results
```

`smart_router_health_events` 至少包含：

```text
event_id
occurred_at_utc
source                 -- production | calibration | manual
request_id
client_request_id
lane_id
account_id
source_group
capability
model_family
status_code
failure_class
failure_scope
latency_ms
first_token_ms
attempt_index
switch_count
response_has_image
request_size_bucket
image_size
has_reference_image
error_summary
policy_revision
```

禁止保存 token、key、cookie、密码、完整 credential JSON、完整 Prompt 和原始图片。Prompt 只保存哈希及特征分档。

### 6.2 状态投影

`smart_router_lane_state` 至少包含：

```text
lane_id
capability
model_family
base_priority
health_penalty
health_score
consecutive_failures
consecutive_successes
cooldown_until_utc
recovery_stage
last_success_at_utc
last_failure_at_utc
last_probe_at_utc
last_probe_status
manual_disabled
updated_at_utc
```

状态表可以使用 Redis 或数据库，但恢复、审计和跨进程校准必须有持久来源。两台不同 VPS 默认不共享运行时健康状态，因为网络出口和上游路径不同；最多共享能力标签，不共享“已健康”结论。

## 7. 每日自动校准

### 7.1 调度时间

- 使用 `Asia/Shanghai` 计算每天 04:00；
- 数据库存 UTC；
- 使用分布式锁，保证同一部署只执行一次；
- 多台 VPS 采用随机错峰，不能同时压测同一上游；
- 校准有总预算和单线路预算，超预算立即停止。

### 7.2 先查账本，再决定测试

每天校准前读取最近 24 小时账本，并按 capability 建立测试计划：

| 账本状态 | 文生图 | 图生图 |
| --- | --- | --- |
| 新线路/未知能力 | 测 | 测 |
| 文生图和图生图历史表现不同 | 测 | 测 |
| 两种能力长期统一正常 | 测 | 可跳过 |
| 只文生图失败 | 重测文生图 | 保留原状态，必要时测 |
| 只图生图失败 | 保留原状态 | 重测图生图 |
| 认证失败 | 先测最小认证请求 | 不盲测 |

### 7.3 探针规范

- 文生图：固定短 Prompt，`n=1`，固定小尺寸；
- 图生图：使用仓库内固定无敏感参考图，验证输入图确实被接受；
- 先验证 HTTP 状态，再验证返回体中存在有效图片；
- 记录延迟、响应大小、图片 MIME 和尺寸；
- 不使用用户真实 Prompt 或用户图片作为健康探针；
- 探针失败只影响对应 capability，不自动删除账号。

### 7.4 校准结果

- 成功：清除对应动态惩罚，进入 `warming_5`；
- 连续成功：逐步恢复到 `warming_25`、`healthy`；
- 继续失败：延长冷却或保持 `quarantined_capability`；
- 结果必须关联 `calibration_run_id`，便于追溯“为什么恢复或后置”。

## 8. V2 与本地 V3 时间线取证

### 8.1 证据

| 时间 | 请求 | 结果 |
| --- | --- | --- |
| 13:21 左右 | 旧 V3 Doc103 风景测试 | 768.17 秒后 blocked，无图片 |
| 14:25:58 | VPS/aiself 小型生图测试 | 7646881，约 33 秒，HTTP 200 |
| 14:33:48 | V3 Doc103 风景请求进入 aiself | 请求体约 6010 字节，`1536x1024`，无参考图 |
| 14:36:48 | 同一请求 | 7646881 返回 502，切换一次 |
| 14:39:15 | 同一请求 | 流云返回 400 `upstream_text_reply`，切换两次 |
| 14:40:18 | 同一请求 | aiai 返回 HTTP 200，总耗时约 390 秒 |
| 14:40:21 | V3 产物落盘 | 一张 `1536x1024` PNG，单次上游请求，最终成功 |
| 14:41:35 | 后续另一顶层请求 | 新请求再次命中 7646881 |
| 14:43:15 | 后续另一顶层请求 | `context canceled`，502，未走完后续切线 |

### 8.2 规律

1. **不是 V2 成功、V3 协议完全不兼容。** 同一条 V3 实际链路最终已经产出正确尺寸的 PNG，证明 V3 到 aiself 的基本协议可用。
2. **VPS 小测试与 V3 质量测试不是同一负载。** 小测试是短 Prompt、`1024x1024`、直接命中 7646881；V3 是约 5961 字符的编译 Prompt、`1536x1024`，并且前两条线路先后失败，最终才到 aiai。
3. **真实的慢点在上游切换，不在图片解析。** 这次 aiself 约 390 秒返回，V3 成功落盘；不是“V3 不能吃返回体”。
4. **旧失败和新成功之间存在预算竞争。** 之前 V3 与 aiself 都使用约 600 秒预算，存在 V3 先取消、抢不到最后切换结果的风险；后来 V3 deadline 调到 660 秒，重跑最终成功。
5. **第二次失败是客户端生命周期问题。** 后续请求在 7646881 约 100 秒时收到 `context canceled`，没有机会继续切到其它线路；这不应被记作 7646881 的一次完整失败链路，也不应触发上游惩罚。

### 8.3 结论

当前“VPS V2 基本成功、本地 V3 经常失败”的表象，主要由三件事叠加：

- V2 测试负载较轻，常直接命中 7646881；
- V3 质量请求更大、更慢，更容易暴露上游 502 和流云能力错误；
- V3 某些失败尝试在上游切换完成前被 `context canceled` 终止。

因此修复重点不是把 V3 改成另一套图片协议，而是：上游 capability 隔离、动态退避、足够的端到端预算、单次请求不重放，以及准确区分客户端取消和上游失败。

## 9. 配置草案

```yaml
gateway:
  smart_router:
    enabled: true
    image_total_budget_seconds: 600
    image_attempt_seconds: 180
    image_finalization_reserve_seconds: 15
    timezone: Asia/Shanghai
    calibration:
      enabled: true
      daily_at: "04:00"
      jitter_seconds: 900
      max_parallel_probes: 1
      total_budget_seconds: 1800
      require_distributed_lock: true
    attempts:
      image_generation: 2
      image_edit: 2
      same_source_group: 1
    recovery:
      first_success_weight: 0.05
      second_success_weight: 0.25
      success_to_normal: 3
      quiet_window_seconds: 900
    failure_policy:
      transient_cooldown_seconds: 30
      capability_quarantine_seconds: 86400
      rate_limit_cooldown_seconds: 300
      auth_quarantine_seconds: 86400
      client_cancel_penalty: false
```

## 10. 开发阶段与验收

### Phase 1：核心状态和分类

- 增加 `FailureClass`、`CapabilityHealthKey`、状态机；
- 增加有效优先级计算；
- 增加失败分类单测和状态迁移单测。

### Phase 2：Sub2API 适配

- 映射账号、分组、并发、临时不可调度；
- 保持原始 `priority` 不变；
- Smart Router 关闭时行为完全不变；
- 接入 image generation/edit 两个独立 lane。

### Phase 3：账本和校准

- 增加事件表、状态表和校准结果表；
- 实现 04:00 调度、分布式锁、错峰、探针；
- 支持按账本动态决定文生图/图生图测试次数。

### Phase 4：生产灰度

- 先只记录，不改变调度；
- 再只对 image generation 开启动态后置；
- 验证 7646881 失败时不会永久关闭；
- 验证流云文生图失败不会影响图生图；
- 验证 aiai 兜底和恢复回流。

### 必测用例

- 7646881 连续 502 后临时后置；
- 7646881 校准成功后 5% 回流；
- 流云 `upstream_text_reply` 只隔离文生图；
- 图生图失败不影响文生图；
- 客户端 `context canceled` 不降低上游健康分；
- 账本判断为稳定线路时只执行一次文生图探针；
- 新线路自动执行文生图和图生图探针；
- 同源 plus/pro/fallback 不在一次请求中连续打穿；
- 600 秒下游预算和 660 秒客户端预算不发生抢先取消；
- Smart Router 关闭后旧调度测试全部通过。

## 11. 兼容性与回滚

- 不修改人工优先级、分组、模型价格和人工禁用状态；
- Smart Router 开启时，瞬时生图失败不再写入 legacy `temp_unschedulable_until`，改由有效优先级和健康权重软降权；
- 非 Smart Router 账号继续兼容已有 `temp_unschedulable_until`；
- 已有 `image_edit_transient_cooldown_seconds` 作为兼容别名保留；
- 未配置 Smart Router 的账号继续使用原 scheduler；
- 两层 Sub2API 各自维护运行时健康状态，不把内部账号 ID 暴露给下游；
- 回滚只需关闭 `gateway.smart_router.enabled`，不删除账本；
- 删除或重建容器前先备份状态表、策略和校准记录。

## 12. 完成标准

本方案只有同时满足以下条件才算完成：

1. 价格原始优先级不被永久改变；
2. transient、能力不兼容、认证失败、客户端取消能正确区分；
3. 文生图与图生图可以独立恢复；
4. 失败线路不会永久关闭，除非人工关闭；
5. 每日校准可根据账本决定测试范围；
6. 生产请求和校准探针可按 request ID 完整复盘；
7. 7646881、流云 AI、aiai 三类线路均有对应回归用例；
8. Smart Router 关闭时，Sub2API 原有行为不变。
