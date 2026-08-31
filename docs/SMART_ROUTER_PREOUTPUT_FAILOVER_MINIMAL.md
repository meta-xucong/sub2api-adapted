# Smart Router 首个有效输出前故障切换：最小改动开发文档

## 1. 状态与范围

- 状态：已落代码并通过定向后端测试（2026-08-31）。
- 目标：处理 `/v1/responses` 的 GPT 流在首个有效思考/正文前出现
  `codex upstream stalled`、EOF、连接重置或明确 5xx 时，安全切换到其他线路。
- 原则：只修复流式故障边界，不重写 Smart Router 调度算法。

本方案不修改：

- 全局请求超时；
- 最大切换次数；
- 价格优先级、负载均衡和 sticky 调度策略；
- 图片链路和非流式请求行为。

## 2. 当前缺口

当前 Smart Router 已能记录 `stream_interrupted`/上游 5xx 并进行健康降权，
但部分 Responses/WS 故障在网关中已经把 `response.failed` 或错误终止事件写给
客户端，随后才返回错误。

`openai_gateway_handler.go` 会据此认为响应已经开始，阻止同一请求切换账号，
最终表现为：客户端等待很久、没有思考内容、连接结束后再重连。

另一个缺口是 `openai_gateway_response_handling.go` 的非 Grok 流间隔超时路径
仍返回普通 `error`，没有统一进入可 failover 的错误类型。

## 3. 最小方案

### 3.1 首个有效输出前定义

把以下内容视为“非语义前导”，不算用户可见输出：

- `response.created`、`response.in_progress`；
- SSE 注释心跳；
- 上游错误前导帧。

只有真实思考/正文增量、工具调用或最终语义结果写出后，才设置：

```text
semantic_output_started = true
```

### 3.2 安全切换条件

仅当以下条件全部满足时，允许当前请求切换：

1. `semantic_output_started == false`；
2. 客户端没有取消；
3. 错误属于明确上游失败：stall、EOF、连接重置、首输出前 5xx 或流间隔超时；
4. 错误不是参数、策略、上下文窗口或鉴权错误。

满足条件时：

1. 不向客户端发送原线路的 `response.failed`/通用错误事件；
2. 返回现有 `UpstreamFailoverError`；
3. 复用现有 `failedAccountIDs` 和 `ExcludedSourceGroups` 机制；
4. 选择下一条候选线路并继续原请求。

如果已经有语义输出，则维持现有行为，不切换、不拼接两条 SSE 流。

### 3.3 复用现有健康机制

不新增熔断算法。现有 Smart Router 健康账本继续按：

```text
lane + capability=responses + exact_model
```

记录失败。

单次明确首输出前失败：

- 当前请求排除该账号；
- 继续使用现有 `stream_interrupted` 冷却和恢复槽位；
- 不永久禁用账号。

客户端主动取消仍然不计入健康失败。

## 4. 避免误判的边界

本补丁不根据“暂时没有 token”立即切换，也不新增固定短超时。这样不会把正常的
长思考误判为故障。

只有上游已经明确返回失败/断开，或者现有流间隔超时机制确认失败时，才触发切换。

特别规则：

- 一次抖动只影响当前请求和已有短冷却；
- 不因一次失败隔离整个 source group；
- 同一 source group 的多个账号在同一请求中仍由现有排除逻辑处理；
- 语义输出开始后绝不切换；
- 失败事件必须去重，不能因同一请求的多条日志重复加罚。

## 5. 实现文件与行为

### 已修改

`backend/internal/service/openai_gateway_response_handling.go`

- 流间隔超时且尚无语义输出时，返回 `UpstreamFailoverError`；
- 不要先发送 `stream_timeout` 或 `response.failed` 再请求切换。

`backend/internal/service/openai_gateway_forward.go`

- WS V2 重连耗尽后，如果请求仍未写出任何流内容且失败原因可重试，
  通过 `openAIWSFallbackToUpstreamFailoverError` 返回统一的
  `UpstreamFailoverError`，交由现有 handler 排除账号并切换线路。
- 非流式请求和不可重试错误继续使用原 JSON fallback。

`backend/internal/service/openai_gateway_service.go`

- 新增 `openAIWSFallbackToUpstreamFailoverError`：保留 WS 拨号状态/请求头，
  对无 HTTP 状态的读错误统一使用 502，并拒绝 policy/auth/参数类错误进入切换。

### 回归测试

`backend/internal/handler/openai_gateway_handler.go`

- 保持现有“已写语义内容不切换”保护；
- 增加首输出前错误可切换的回归测试。

Smart Router 核心算法无需改动；新增 WS fallback 转换测试，并覆盖流间隔超时的
“首输出前可切换、输出后保持旧错误事件”两条边界。

## 6. 本地验证结果

使用 fake SSE/WS，不发起真实模型调用：

已通过：

```text
go test ./internal/service ./internal/handler ./internal/smartrouter/core
```

结果：service、handler、smartrouter/core 全部通过；测试使用 fake SSE/WS，未发起
真实模型调用。覆盖首输出前 WS/间隔超时 failover、输出后不切换、客户端取消和
既有 Smart Router 健康行为。

## 7. 上线验收

修复后的生产日志应能看到：

```text
openai.upstream_failover_switching
```

并且该类请求不再出现：

```text
upstream_error_response_already_written=true
```

验收重点是：当前 GPT‑5.5 stall 在首个有效输出前可以换线路，正常慢思考和
已开始输出的流不被误切。
