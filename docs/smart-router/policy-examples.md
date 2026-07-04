# Smart Router 策略示例

## 低价图片线路优先

```json
{
  "smart_router": {
    "enabled": true,
    "lane_id": "aicodexvip-image",
    "source_group": "aicodexvip",
    "capabilities": ["image_generation", "image_edit"],
    "base_weight": 1.4,
    "cost_multiplier": 0.45,
    "max_concurrency": 1,
    "source_group_max_concurrency": 1
  }
}
```

适合便宜但容易抖动的图片线路。它仍会被优先选择，但并发和 source group 会阻止它被高频打爆。

## 稳定兜底图片线路

```json
{
  "smart_router": {
    "enabled": true,
    "lane_id": "stable-image-fallback",
    "source_group": "stable-image-fallback",
    "capabilities": ["image_generation", "image_edit"],
    "base_weight": 1.0,
    "cost_multiplier": 1.0,
    "max_concurrency": 3,
    "source_group_max_concurrency": 3
  }
}
```

适合 aiai 这类成本更高但需要保持热状态的备用线路。

## 三条同源 chat 线路

```json
{
  "smart_router": {
    "enabled": true,
    "source_group": "7646881",
    "capabilities": ["chat", "responses"],
    "max_concurrency": 1,
    "source_group_max_concurrency": 1
  }
}
```

把 plus、pro、fallback 都设成同一个 `source_group`，避免一次请求内连续打穿同源上游。

## 第五条新线路灰度接入

```json
{
  "smart_router": {
    "enabled": true,
    "lane_id": "new-provider-gray",
    "source_group": "new-provider",
    "base_weight": 0.1,
    "cost_multiplier": 0.8,
    "max_concurrency": 1
  }
}
```

新线路先用低 `base_weight` 和低并发接入，真实成功率稳定后再逐步提高。
