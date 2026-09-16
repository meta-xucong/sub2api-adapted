# Smart Router 图像链路超时切线加固

## 目的

当 OpenAI-compatible 图像 generation/edit 已收到上游响应头、但在读取响应体时发生“本次上游尝试超时”，且客户端尚未收到语义图像数据时，把该失败交回现有 Smart Router failover 循环。这样同一请求可以继续尝试备用账号，而不改变现有调度排序、每次尝试超时、总请求预算或最大切换次数。

## 处理边界

- 仅覆盖 OpenAI 图像 generation/edit 的 API-key 与 OAuth 适配路径。
- 单次 attempt deadline 超时：返回内部 `UpstreamFailoverError`，Reason 为 `openai_image_attempt_timeout`，外部仍保持脱敏的 502 `upstream_error`。
- 请求总 deadline 已耗尽、客户端取消、响应已写出语义字节：不重放。
- JSON/SSE keepalive 不视为语义图像输出；真实图像/事件写出后仍禁止重放。
- generation 与 edit 继续使用各自健康键；超时健康事件归类为 `FailureTimeout`，不改持久化账号优先级、启用状态或视频链路。

## 实现要点

1. API-key 路径使用保留请求总 deadline 的 detached context，避免单个慢账号绕过总预算。
2. 成功 HTTP 响应不再在“只收到响应头”时提前写入自适应健康样本；在完整 body/stream 处理后记录成功，读取失败记录失败。
3. body-read timeout 只有在 attempt context 已超时且 request context 仍存活时才转换为 failover。
4. Smart Router 健康适配器为该内部 Reason 和未包装的 deadline/net timeout 补充 timeout 分类；客户端取消仍不惩罚线路。

## 验收

```powershell
Set-Location D:\AI\SSH\sub2api\backend
go test -tags unit ./internal/service
go test -tags unit ./internal/handler -run 'TestOpenAIGatewayHandlerImages' -count=1
```

现有 attempt budget、source-group 排除、同账号重试和后续软冷却策略保持不变；本补丁不执行部署或生产配置修改。
