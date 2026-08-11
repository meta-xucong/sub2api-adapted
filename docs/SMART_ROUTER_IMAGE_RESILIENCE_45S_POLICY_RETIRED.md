# 已退役：旧版生图 45 秒动态等待策略

状态：**废弃，不得作为当前配置或开发依据。**

## 问题复盘

旧实现使用：

```text
failure_timeout = last_failure_duration × 0.5^连续失败次数
```

并设置最低等待值 `45s`。这会导致线路越失败，下一次等待越短；慢但仍可用的上游会在生成完成前被 Aiself 取消。

在 Aiself 的历史记录中，多个线路连续出现约 45 秒的 `context deadline exceeded`，而同一批线路此前曾在 80～140 秒内成功出图。

## 退役时间

- 旧策略代码引入：`3ef90d91`，2026-07-14。
- Aiself 旧运行配置写入：2026-07-14 22:38（`standard_min_seconds=45`）。
- 替代规范：`SMART_ROUTER_IMAGE_TIMEOUT_POLICY.md`。

## 正确方向

失败应恢复完整 180 秒窗口，连续成功才逐步减少等待时间；详见新的当前实现规范。
