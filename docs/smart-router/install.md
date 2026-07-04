# Smart Router 安装与启用

## 模块边界

Smart Router 第一阶段以源码补丁方式接入 Sub2API：

```text
backend/internal/smartrouter/
backend/internal/service/openai_account_scheduler.go
backend/internal/config/config.go
```

`backend/internal/smartrouter` 是独立核心模块，不依赖 Gin、Ent 或 Sub2API 的账号结构。Sub2API 只在 service adapter 层把账号候选转换成 lane 快照。

## 默认状态

默认配置：

```yaml
gateway:
  smart_router:
    enabled: false
```

关闭时不改变现有调度行为。启用 Smart Router 仍要求现有 OpenAI advanced scheduler 处于启用状态，因为第一阶段接在 OpenAI load-balance scheduler 内。

## 最小启用配置

```yaml
gateway:
  smart_router:
    enabled: true
    top_k: 5
    max_attempts_image: 2
    max_attempts_chat: 3
    same_source_group_attempts: 1
    cost_bias_max: 3
```

账号可选配置在 `extra.smart_router`：

```json
{
  "smart_router": {
    "enabled": true,
    "lane_id": "cheap-image-primary",
    "source_group": "cheap-image-pool",
    "base_weight": 1.2,
    "cost_multiplier": 0.45,
    "max_concurrency": 1,
    "source_group_max_concurrency": 1,
    "capabilities": ["image_generation", "image_edit"]
  }
}
```

未配置 `extra.smart_router` 的账号会按旧账号字段生成默认 lane，`source_group` 默认为 `account:<id>`。

## 回滚

最小回滚：

```yaml
gateway:
  smart_router:
    enabled: false
```

如果已经部署到 VPS，可以只改配置并重启 Sub2API；无需改数据库。更彻底的回滚是切回部署前备份镜像。

## 生产试运行建议

1. 先在下游或上游其中一层启用，不要两层同时首次启用。
2. 首次只给一组低风险线路配置 `source_group` 和低并发。
3. 观察 30-60 分钟：
   - HTTP 200 成功率
   - 502/503/504 数量
   - 单线路并发
   - source group 是否被连续打穿
4. 稳定后再扩大到更多线路。
