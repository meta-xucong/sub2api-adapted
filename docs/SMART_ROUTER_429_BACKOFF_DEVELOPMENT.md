# Smart Router 429 Backoff

## 目标

为上游 HTTP 429 增加独立的、可插拔的指数退避策略，避免把一次短暂的并发拥塞误判为线路故障。

这是一层请求级策略，和 Smart Router 的健康降权、04:00 校准、图片文生图/图生图能力账本相互独立。

## 429 分类

### 并发型 429

匹配明确的并发/忙碌信号，例如：

- `Concurrency limit exceeded`
- `Too many concurrent requests`
- `Too many pending requests`
- `maximum concurrent`

处理方式：

1. 当前请求等待指数退避并带随机抖动。
2. 等待后切换到下一条线路；不在同一请求中并行打爆多条线路。
3. 不修改线路原始优先级。
4. 不分配 `30/31/32` 恢复槽。
5. 不写入旧的永久禁用或临时禁用状态。

### 额度/频率型 429

普通 `rate_limit_exceeded`、quota、credits 等 429 继续使用现有的健康冷却和软降权策略，因为它们可能不是短时并发拥塞。

### 本机用户并发 429

`Concurrency limit exceeded for user` 如果由 Sub2API 自己的用户槽位产生，不会进入上游 Smart Router 退避。它由用户并发等待队列处理；等待超时后直接返回 429，不能靠切换上游解决。

## 退避算法

默认参数：

```yaml
gateway:
  smart_router:
    rate_limit_backoff:
      enabled: true
      initial_seconds: 5
      max_seconds: 60
      max_attempts: 4
      jitter_ratio: 0.25
      retry_after_max_seconds: 90
```

无 `Retry-After` 时，等待窗口约为：

```text
5s -> 10s -> 20s -> 40s
```

每次加入 ±25% 抖动；超过 4 次后不再继续等待，由当前请求的总预算决定是否结束或切线。

上游提供 `Retry-After` 时优先参考，但仍受 `retry_after_max_seconds` 和请求总预算限制。

图像请求不会因为这个策略复制一个已经可能产生图片的请求。只有在上游明确返回可 failover 的 429、且响应尚未写回客户端时才进入下一条线路。

## 实现边界

- 核心策略：`backend/internal/smartrouter/core/rate_limit.go`
- 429 分类：`FailureConcurrencyLimited`
- 健康账本：并发型 429 只记录 `rate_limit_backoff`，不降权
- 图片接入：`backend/internal/handler/openai_images.go`
- 配置映射：`backend/internal/config/config.go`

该模块不修改：

- 账号的手动 `priority`
- `schedulable` 状态
- 04:00 校准和恢复槽分配
- compact、普通 Responses、Chat 的能力隔离
- 本机用户并发等待队列

## 验收标准

1. 单次并发型 429 不产生 `30/31/32` 优先级。
2. 退避时间按 5、10、20、40 秒增长，并受最大值限制。
3. `Retry-After` 秒数和 HTTP 日期均可解析。
4. 普通额度型 429 仍进入原有 `rate_limit_cooldown`。
5. 客户端取消、确定性 400 和本机用户并发 429 不被误判为上游线路健康问题。
6. Smart Router 关闭时，旧行为保持不变。
