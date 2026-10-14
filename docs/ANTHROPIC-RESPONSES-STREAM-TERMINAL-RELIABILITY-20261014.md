# Native Anthropic → Responses 流终态可靠性修复

日期：2026-10-14  
状态：文档审计 PASS；实现、代码审计和本地测试 PASS  
目标源码副本：`.tmp-sub2api-trace/backend`（Sub2API 源码快照；目录不含 Git 元数据，不能证明其提交号或与 VPS 部署二进制一致）

## 1. 目标与边界

### 目标

修复 OpenAI Responses 请求经原生 Anthropic `/v1/messages` 上游时，SSE 异常中断、解析损坏或 Anthropic `error` 事件被转换成成功终态的问题。修复只作用于 `OpenAIGatewayService` 的流式与缓冲两条原生 Anthropic 路径。

### 非目标

- 不实现自动重试、上游切换、熔断、路由调度或账户降权。
- 不修改共享 `FinalizeAnthropicResponsesStream` 的全局语义；它被多个其他协议适配器使用。
- 不修改 Responses 请求字段/工具调用输入转换、U 盘安装器、404token VPS 或任何远端服务。
- 不把本地测试结果表述为已证明具体历史复读事故必由该缺陷造成。

### 风险与来源级别

- **D1 — 源码来源不确定**：该目录是本地源码快照，缺少 `.git`，无法固定上游提交，也无法证明与 `404token` 部署版本一致。可证明当前快照存在行为缺口；部署对应关系须另行核实。
- **I2 — 协议转换语义**：原生 Anthropic 流只有收到 `message_stop` 才算正常结束；Responses 的 `response.completed` / `response.incomplete` 是成功终态，异常必须发 `response.failed`。依据 Anthropic 和 OpenAI 官方事件文档。
- **A2 — 本地代码修改**：只改本地源码副本并运行离线 Go 测试；不连接或变更 VPS，不访问生产 API，不发布构建。

实施前基线：目标源码 `openai_gateway_responses_anthropic_native.go` SHA-256 `289AF289DF1ED5B02055C4702181F99AB5ED08AC16587AD230C451E54D65E0E7`。外层 `codex-desktop-usb-installer` 工作树已有多项未提交改动；本任务不触碰这些既有文件。

## 2. 证据与问题定位

1. `backend/internal/service/openai_gateway_responses_anthropic_native.go` 的流式处理已先写 HTTP 200。上游 EOF、扫描错误或超时后，当前代码仍调用 `apicompat.FinalizeAnthropicResponsesStream`。
2. `backend/internal/pkg/apicompat/anthropic_to_responses_response.go` 的共享 finalizer 会在没有 `message_stop` 时合成结束帧；若 `stop_reason` 为空，会把它映射为 `completed`。这会把中断的部分结果冒充成功。
3. Anthropic `type:error` 事件不在 Responses 转换器的已知类型中，会被忽略，随后 EOF 路径同样可能补出成功终态。
4. 缓冲路径也会忽略 `error` 和坏 JSON，并在任意 EOF 后返回已组装响应；缓冲模式尚未提交响应头，因此应返回 502 错误而非部分成功响应。
5. 共享 finalizer 被其他适配器复用，故不能全局改变其语义。本次在原生 Anthropic 的入口显式要求完整事件生命周期。

这证明源码存在协议缺口；没有上游事件级事故样本时，不构成某次历史复读的单独因果证明。

## 3. 实现规则

### 3.1 流式响应

- 仅上游合法 `message_stop` 表示正常终结。该事件已有转换器负责输出 `response.completed` 或 `response.incomplete`；正常 `message_delta.stop_reason=tool_use` 后的 `message_stop` 必须继续表示成功的工具调用响应。
- 在 `message_stop` 前遇到 EOF、scanner/read 错误、数据间隔超时、Anthropic `error` 事件或无法解析的事件 JSON：不得调用成功 finalizer；调用已有 `writeResponsesAdapterFailure` 输出单个 `response.failed`，状态码维持已经提交的 HTTP 200，并返回带 `upstream response failed:` 前缀的错误及已累计 usage。此错误前缀用于让 handler 识别客户端已经收到错误终态、避免追加泛化错误，并供 SmartRouter 分类流启动后的失败；不实现重试/重放。
- 只有先收到合法 `message_start`（含非空 response ID），再收到 JSON payload 中 `type="message_stop"`，才视为合法完整终结。缺失 start/ID 的孤立 stop 按格式错误处理。若失败发生在 `message_start` 之前，生成非空 `resp_<uuid>` 作为失败终态 ID。
- 失败事件使用固定、脱敏的客户端错误文案，不透传原始上游 error.message；日志记录 request id、错误类别和必要的脱敏诊断，不记录密钥或请求正文。
- 如果客户端已断开，不再写任何终态。仍返回上游失败结果/错误以便内部结算和诊断；不得补发 `response.completed`。
- 失败时不能通过合成 `output_item.done`、`function_call_arguments.done` 或 completed 工具项来闭合未完成内容。已有的增量仍可保留，最终态明确失败。
- 收到 `message_stop` 后立即停止读取，不处理终态之后的多余帧。

### 3.2 缓冲响应

- 只有先收到含非空 response ID 的合法 `message_start`，再收到 JSON payload 中 `type="message_stop"`，才将组装响应以 HTTP 200 返回；孤立的 stop 按格式错误处理。
- 在 `message_stop` 前遇到 EOF、read/scanner 错误、超时、Anthropic `error` 或坏 JSON：调用现有错误响应机制返回 502；不得序列化已收集的局部内容为成功响应。
- 缓冲失败仍返回已累计的 usage 结果对象供内部结算；不会把局部输出当成功正文返回。
- `message_stop` 后立即停止读取。保留正常 stop reason、工具调用和 usage 转换行为。

### 3.3 SSE 解析范围

- 保留当前上游 `event:` 后跟 `data:` 的行泵和最大行长/空闲超时机制。
- 对成功解析的 Anthropic event payload 检查 `type`；`error` 为上游失败。
- 事件 data 不是合法 JSON，或 event 行后缺失 data 行并以 EOF 结束，均按流格式损坏处理。
- 忽略普通空行和非事件注释行；不新增多行 data 聚合或任意供应商协议猜测。

## 4. 代码清单与复用

| 文件 | 预期改动 | 复用/说明 |
|---|---|---|
| `backend/internal/service/openai_gateway_responses_anthropic_native.go` | 在缓冲及流式循环跟踪 `message_stop`；识别 Anthropic `error` / JSON 解码失败 / read 错误；流异常时输出失败终态并返回错误；缓冲异常返回 502；成功结束后停止读取 | 复用 `newAnthropicNativeLinePump`、`writeResponsesAdapterFailure`、`writeResponsesError`、usage/result 结构；不改共享转换器 |
| `backend/internal/service/openai_gateway_responses_anthropic_native_terminal_test.go` | 新增 EOF、read error、坏 JSON、上游错误、无 ID、成功文本/工具调用、客户端断开和缓冲错误回归测试 | 复用 `newNativeAnthropicHangTestService`、现有 Anthropic SSE fixture 与 Gin 测试上下文 |
| `backend/internal/service/openai_gateway_anthropic_native_pump_test.go` | 将既有 Responses 空闲超时测试断言调整为失败终态 | 同一缺陷的旧测试原先只验返回超时，现需验证不得遗漏终态 |
| `backend/internal/service/openai_gateway_cn_fixes_test.go` | 将既有 EOF usage 测试改为断言保留用量且失败终态，而不是把 EOF 当成功 | 测试覆盖 Responses 原生 Anthropic 流的 usage 归一化 |
| 本文档 | 记录决策和验收 | 本地快照文档，不代表上游已合并或部署 |

禁止改动：`anthropic_to_responses_response.go` 的共享 finalizer 语义、其他协议路径、installer/VPS 文件。

## 5. 验收矩阵

| 场景 | 流式期望 | 缓冲期望 |
|---|---|---|
| 正常文本 + `message_stop` | `response.completed`，不出现 `response.failed` | HTTP 200，完整文本 |
| `tool_use` + `message_stop` | completed 工具调用；不能误判失败 | HTTP 200，function_call 完整 |
| 首事件前 EOF / read error | `response.failed` 且 ID 非空 | HTTP 502 |
| 部分文本后 EOF，未 `message_stop` | `response.failed`，无 completed/incomplete 合成终态 | HTTP 502，不回局部成功响应 |
| Anthropic `error` event | `response.failed`，不透传上游原始错误 | HTTP 502 |
| 非法 JSON / 缺失 data 行 | `response.failed` | HTTP 502 |
| 非 EOF read error / idle timeout | `response.failed`，返回非重放错误 | HTTP 502，保留已累计 usage |
| 客户端断开后上游异常结束 | 不写终态，不合成成功；保留 usage 与错误结果 | 不适用 |
| 孤立 `message_stop`（无有效 `message_start`/ID） | `response.failed` | HTTP 502 |
| 终态后额外事件 | 忽略终态后帧 | 忽略终态后帧 |

本地验收命令：

```powershell
Set-Location .tmp-sub2api-trace\backend
go test ./internal/service -run NativeAnthropic -count=1
go test ./internal/service -count=1
go test ./internal/pkg/apicompat -count=1
```

若运行时 Go 版本低于 `go.mod` 声明的 `1.27.2` 或依赖不可用，应记录为环境阻塞，不能把未运行表述为通过。可在成功后补跑 `go test ./internal/pkg/apicompat`，用于证明未回归共享转换器。

## 6. 审计与发布门槛

1. **文档审计**：独立审查协议规则、精确范围、复用和验收矩阵；通过后冻结本文档。
2. **实现**：只允许写上述四个 Go 文件；先记录其基线哈希，完成后检查 diff 与 scope。
3. **代码审计**：独立审查精确冻结 diff 和测试证据，结果须为 PASS；FAIL 只允许有界修复并重新审计。
4. **本地测试**：定向 service tests 与 apicompat tests 全过。测试通过只能证明本地快照行为。
5. 本次不部署、不打包、不声称具体生产 VPS 已修复。生产落地前须从部署源取得带 Git commit 的源码并核对同一函数，再将补丁迁入真实上游工作树。

### 本地验收实绩（2026-10-14）

- 独立文档审计：PASS。
- 独立代码审计：PASS；确认异常流不会被补成成功终态，未发现共享转换器或其他路径改动。
- `go test ./internal/service -run NativeAnthropic -count=1`：PASS。
- `go test ./internal/service -count=1`：PASS（150.485s；本机测试需允许 loopback socket）。
- `go test ./internal/pkg/apicompat -count=1`：PASS。
- 最终 handler SHA-256：`D4BD04525CC58C8FE0A32E6433B8214085ACA875DDBE2BA41D6F57BE13372904`。
- 验收仅覆盖此本地源码快照，不代表已部署到 VPS，也不能单独证明它就是历史复读事故的唯一成因。

## 7. 参考

- [OpenAI Responses streaming events](https://developers.openai.com/api/reference/resources/responses/streaming-events)：区分 `response.completed`、`response.incomplete` 与 `response.failed`。
- [Anthropic Messages streaming](https://platform.claude.com/docs/en/build-with-claude/streaming)：正常生命周期含 `message_stop`；流可发送 `error` event。
- [Sub2API issue #3084](https://github.com/Wei-Shaw/sub2api/issues/3084)：Responses 流缺终态与 Codex 重连/重复行为的公开排障讨论。
- [Sub2API issue #2245](https://github.com/Wei-Shaw/sub2api/issues/2245)：对 EOF/终态处理的相关讨论。
- [Sub2API issue #7274](https://github.com/Wei-Shaw/sub2api/issues/7274)：EOF 后错误终态/合成终态相关讨论。

公开 issue 是旁证与成熟排障经验，不视作已合并的规范补丁；实现仍以官方协议和当前源码为准。



