# Smart Router Compact Resilience

## 1. 目标

解决 OpenAI Responses remote compact 在长时间等待、反向代理 keepalive、上游
502/503 和 Codex 重连叠加时的两个问题：

1. compact 失败两条线路后提前结束，未给其他可用线路机会；
2. keepalive 已经把 HTTP 状态固定为 200 后，最终失败没有被可靠记录，后台可能显示
   `codex.remote_compact.succeeded`。

本补丁只改变 Smart Router 的 compact 路由预算和 compact 失败观测，不改变普通
Responses、chat、图片路由、手动价格优先级或每日 04:00 校准规则。

## 2. 当前代码已完善的部分

- compact 使用独立能力键 `responses_compact`，不会与普通聊天或图片健康状态混用；
- compact 能力候选按支持、未知能力分层；
- 线路失败后会写入独立 Smart Router 健康账本；
- 失败线路是软降权/恢复队列，不是永久关闭；
- 每日上海时间 04:00 校准仍会重新探测被降权线路；
- keepalive 会定期发送 SSE 注释，降低反代空闲超时导致的客户端重连；
- 同一逻辑请求内已失败账号会进入排除集合，避免重复轰炸同一账号；
- 正常的 compact 响应仍要求有效的 `encrypted_content`，不能用空响应冒充成功。

## 3. 本次补丁

### 3.1 动态 compact 候选预算

`gateway.smart_router.max_attempts_compact` 的语义调整为：

- `0`：动态模式。每轮根据当前请求的候选数量和剩余时间，依次尝试候选；
- `>0`：运维人员显式设置的最大尝试次数，保留硬上限；
- 仍遵守请求总超时、剩余预算和同源线路并发限制；
- 同一账号失败后由 handler 放入 `ExcludedLaneIDs`，下一轮不会再次选择它；
- 冷却中的线路仍可作为低优先级后备，不因冷却被永久过滤。

动态模式同时绕过普通请求的 `top_k` 截断，让 compact 候选能够完整进入排序。排序仍
遵守当前有效优先级、健康分、负载、延迟、来源组和恢复阶段；因此不是无序广播，也
不会同时向所有上游发请求。

### 3.2 失败结果可观测性

body-signal compact 的 keepalive 首次写出后，HTTP 状态码固定为 200。所有后续错误
路径必须额外写入 `OpsStreamError`，由结尾日志将结果记为 `failed`。这样：

- 客户端仍收到合法的 `response.failed` 终止事件；
- 运维日志不再把“HTTP 200 keepalive + 实际上游失败”记录成成功；
- Smart Router 仍依据实际 `Forward` 错误更新 compact 健康账本。

## 4. 重连边界

keepalive 只能防止代理因长时间无字节而主动断开，不能让 Codex 重连自动接回已经
取消的 HTTP 请求。若客户端主动断开，旧请求上下文会收到 `context canceled`；新的
重连请求是新的 compact 逻辑请求，会重新选择线路。

本补丁不引入后台任务接管或结果缓存，因为这会涉及：

- 上游请求是否继续运行；
- 重连时是否重复计费；
- `encrypted_content` 是否允许安全复用；
- 用户级任务幂等键和过期时间。

如果未来要做到“重连接回原压缩任务”，应另做 handler/service 层的短期任务协调器，
不能仅在 Smart Router core 中伪造实现。

## 5. 配置

默认配置：

```yaml
gateway:
  smart_router:
    enabled: true
    max_attempts_compact: 0
```

部署时若配置文件已经明确写了 `2`，代码默认值不会覆盖它；应将该项改为 `0` 才能
启用动态候选模式。其他 `max_attempts_*` 不变。

## 6. 验收标准

1. 三条 compact 候选中前两条分别返回 503/502 时，第三条可以被选择；
2. 每条失败账号只在同一请求中尝试一次；
3. 设置 `max_attempts_compact=2` 时仍只尝试两条；
4. keepalive 已提交 200 后发生选择失败，日志必须是
   `codex.remote_compact.failed`，并带有 `OpsStreamError`；
5. 普通 Responses/chat、图片路由和 04:00 校准相关测试保持通过；
6. 二进制热更新后 `/health`、`/login` 和容器状态正常。

## 7. 部署方式

本补丁通过编译后的 Linux 静态二进制热更新容器内 `/app/sub2api`，保留原镜像、数据
卷、配置和数据库。更新前备份当前二进制与配置，更新后执行健康检查和日志检查；不
执行镜像重建，不删除运行数据。
