# Smart Router 运维与审计

## 上线前审计

- `gateway.smart_router.enabled=false` 时，调度结果必须保持原行为。
- `active=false`、`schedulable=false`、`rate_limit_reset_at`、`temp_unschedulable_until` 仍然优先于 Smart Router。
- sticky previous response 和 sticky session 不应被 Smart Router 打散。
- source group 默认值必须是 `account:<id>`，不能把未配置账号错误合并。
- 日志只允许输出 request id、account id、lane id、source group、模型、能力、状态码和短错误摘要。
- 禁止输出 token、key、password、cookie、refresh token、完整 credential JSON。

## 试运行观察

推荐观察指标：

- `smart_router.select` 数量和候选数。
- 每个 lane 的命中次数。
- 每个 source group 的当前并发。
- 失败后是否跳过同 source group。
- image edit 短冷却后是否不再连续命中同账号。
- 502/503/504 是否下降。

## 常见问题

### 启用后完全没有变化

检查：

- `gateway.smart_router.enabled` 是否为 `true`。
- OpenAI advanced scheduler 是否启用。
- 候选账号数量是否大于 1。
- 账号是否都没有 `extra.smart_router`，且默认策略不足以改变排序。

### 低价线路仍被打爆

检查：

- 低价线路是否配置 `max_concurrency`。
- 同源线路是否配置相同 `source_group`。
- `source_group_max_concurrency` 是否过高。
- 下游和上游是否同时在重复撞同一个 endpoint。

### fallback 永远不回来

检查：

- 冷却是否已经过期。
- 探针是否成功。
- `base_weight` 是否太低。
- source group 是否仍在冷却或满并发。

## VPS 试部署流程

1. 备份当前 compose、镜像 tag、配置文件和关键表。
2. 构建新镜像并保留 rollback tag。
3. 首次启动时保持 `gateway.smart_router.enabled=false`。
4. 健康检查通过后，只在一层 Sub2API 上启用。
5. 用真实小流量观察 30-60 分钟。
6. 如果 502/503 增加或调度异常，立即关闭配置或切 rollback tag。
