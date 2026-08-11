# Smart Router SSE 断流重罚开发文档

## 背景

部分第三方 OpenAI-compatible 上游会先接受 `/v1/responses` 流式请求，
向客户端写出若干 SSE 事件后，再返回 `response.failed`、断开连接或长时间
空闲。下游 Codex 通常表现为：

```text
stream disconnected before completion
idle timeout waiting for SSE
upstream response failed: ... request ID ...
```

这类错误和普通 `503` 不同：网关已经把流式响应写给客户端，不能再在同一
HTTP 请求中无感切换到另一条线路。否则会造成重复输出、重复计费、工具调用
状态错乱，甚至污染客户端会话。

## 目标

1. 不重放已经开始写流的请求。
2. 将 SSE 开流后失败识别为比普通瞬时错误更重的线路健康失败。
3. 失败只影响对应 `capability + exact model + lane`，不改账号状态、
   人工优先级、分组、余额或模型映射。
4. 复用现有 Smart Router 软降权、恢复槽位和 04:00 Asia/Shanghai 校准，
   不新增需要逐线路手工配置的规则。
5. 适用于所有 OpenAI chat / Responses / compact 流式线路，不写死流云、
   YeToken、7646881 或任何单一上游名称。

## 非目标

- 不在已开流请求中并发请求第二条线路。
- 不把用户主动取消、客户端断网、请求体参数错误当成上游健康失败。
- 不改变图片 `/v1/images/*` 和 Responses image bridge 的调度语义。
- 不把失败线路永久关闭；恢复由真实成功流量和每日校准决定。

## 失败分类

新增 Smart Router 失败类型：

```text
stream_interrupted
```

触发范围仅限：

- `chat`
- `responses`
- `responses_compact`

典型匹配摘要：

- `upstream response failed: ...`
- `stream read error`
- `stream data interval timeout`
- `stream usage incomplete`
- `stream disconnected before completion`
- `stream ended before a terminal event`
- `missing terminal event`
- `idle timeout waiting for SSE`

`clientCancelled=true` 优先级最高，仍然分类为 `cancelled`，不惩罚线路。

## 健康策略

文本流式能力使用精确模型健康键，而不是粗略的 GPT-5 大池：

```text
(lane_id, responses, gpt-5.6-sol)
(lane_id, responses, gpt-5.6-terra)
(lane_id, responses, gpt-5.6-luna)
(lane_id, responses, gpt-5.5)
(lane_id, responses, gpt-5.4)
```

因此 404token 某条线路在 `gpt-5.6-terra` 上出现
`idle timeout waiting for SSE` 时，只会降权该线路的 terra 流式能力；
`luna`、`5.5`、`5.4` 仍按自己的健康证据调度。这样可以避免某个模型或
供应商子路由抖动时，把整个账号或整组 GPT-5 模型都拖慢。

`stream_interrupted` 复用现有配置：

- `gateway.smart_router.recovery.second_failure_cooldown_seconds`
- `gateway.smart_router.recovery.sustained_failure_threshold`
- `gateway.smart_router.recovery.recovery_priority_step`
- 04:00 Asia/Shanghai calibration

处理规则：

1. 一次 `stream_interrupted` 记为更重失败，连续失败计数增加 `2`。
2. 第一次进入较长冷却，默认使用 `second_failure_cooldown_seconds`。
3. 若累计失败达到 `sustained_failure_threshold`，冻结到下一次 04:00 校准。
4. 同时进入 Smart Router 恢复槽位，例如人工优先级 `6` 的线路临时变为
   `36`，但数据库中的账号优先级仍为 `6`。
5. 校准成功会立即移除恢复槽位，回到人工优先级；生产成功流量则按已有
   warming 流程逐步恢复。

## 与现有机制的关系

- 普通 `503`、`502`、超时仍走原 `upstream_5xx` / `timeout` 逻辑。
- `responses_compact` 仍是独立能力，不影响普通 `responses`；二者都按具体
  GPT-5 模型保留独立健康状态。
- 图片生图和图生图仍使用 `image_generation` / `image_edit` 的独立账本。
- 线路没有被硬删除；当所有线路都差时，软降权策略仍允许选择相对健康者。

## 验证要求

本补丁至少覆盖：

1. 分类器将开流后错误识别为 `stream_interrupted`。
2. 用户取消仍识别为 `cancelled`。
3. 图片能力不被该分类误伤。
4. 第一次断流进入长冷却和恢复槽位。
5. 第二次断流冻结到 04:00 校准。
6. 校准成功恢复原始优先级。

## 运维判断

如果生产日志出现：

```text
upstream_error_response_already_written=true
error="upstream response failed: ..."
```

说明这条请求已经无法在同一 HTTP 连接中切线。应检查
`smart_router_health_events` 是否出现 `failure_class=stream_interrupted`，
以及对应 `action=stream_interrupted_cooldown` 或
`stream_interrupted_quarantine`。这就是 Smart Router 正确接管后续调度的证据。
