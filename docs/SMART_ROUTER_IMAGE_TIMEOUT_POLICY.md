# Smart Router 生图等待时间策略

状态：当前实现规范

## 目标

只优化 `image_generation` 和 `image_edit` 的单线路等待时间，不改变：

- 账号原始优先级、分组和计费模型；
- Smart Router 的软降权、FIFO 恢复和 04:00 校准；
- 429 独立指数退避；
- 聊天、Responses、compact 等非图片链路；
- 单次生图请求的 600 秒总预算。

## 简化算法

每个线路按以下键独立维护状态：

```text
lane + capability(generation/edit) + size_tier + input_mode + model_family
```

### 初始和失败

- 没有成功样本时，单线路默认等待 `180s`。
- 暂时性失败（timeout、EOF、502/503/504/524、临时 403、429）后，下一次恢复完整的 `180s`。
- 参数错误、endpoint 错误、余额耗尽、永久认证失败、明确能力不支持和内容政策拒绝，不改变等待时间；由能力/健康调度策略处理。
- 客户端取消不改变线路等待状态。

### 成功

- 记录成功请求耗时，并用 EWMA 维护该线路的平均成功耗时。
- 每连续成功一次，下一次等待时间减少 `10s`。
- 等待时间不得低于：

```text
max(平均成功耗时 + 30s, 60s)
```

一次失败会清零连续成功计数并恢复到 180 秒；再次成功后从 170 秒重新逐步下降。

## 总预算和切线

600 秒是整条用户请求（包括切线）的总预算，不是每条线路都保证 180 秒。

```text
attempt_timeout = min(current_lane_timeout, remaining_budget - finalization_reserve)
```

图片策略只保留一次收尾预留，默认 `30s`。当剩余预算不足时，使用剩余预算，不创建新的无限等待。

同一 `source_group` 在一次请求中默认只尝试一次，避免同一个上游的多个账号重复消耗预算。

## 状态隔离

等待时间、健康软降权、429 退避和 04:00 校准分别记录、分别恢复：

- 等待失败不会永久关闭账号；
- 软降权只影响排序，不删除候选；
- 429 使用独立退避，不把一次并发 429 当作永久故障；
- 04:00 校准成功后恢复原始优先级；
- 图片失败不会污染聊天、Responses 或 compact 状态。

## 账本字段

每次尝试只记录脱敏路由数据：

```text
request_id, lane_id, account_id, source_group
capability, model_family, size_tier, input_mode
attempt_index, status_code, failure_class
latency_ms, timeout_budget_ms, cooldown_until
```

不记录 prompt、图片、Cookie、API key 或账号密文。

## 验收标准

1. 新线路首次请求等待 180 秒。
2. 连续成功后按 10 秒逐步下降。
3. 最低不低于平均成功耗时加 30 秒，且不低于 60 秒。
4. 任一次暂时性失败后恢复 180 秒。
5. 不能再出现失败后自动降到 45 秒的行为。
6. 600 秒总预算、同源去重、软降权和 04:00 校准继续通过原有测试。
