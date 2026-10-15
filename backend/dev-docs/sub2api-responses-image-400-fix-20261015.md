# Sub2API Responses 图片上游 400 修复与验收

日期：2026-10-15
目标仓库：`meta-xucong/sub2api-adapted`
冻结基线：`9ed538faeec311a85712839c1771efb36189963e`（VPS 运行二进制与 GitHub 提交一致）
本地工作树：`D:\AI\SSH\sub2api`
部署镜像：`sha256:25dd5c4f338b21fbe7e84dc58adba1fb76eac690e4925ba6a7fb6b6500f4ac68`

## 1. 目标和症状

统一网关向图片上游发送非流式 OpenAI Responses 请求时，上游返回结构化 HTTP 400，例如：

```json
{"error":{"type":"invalid_request_error","code":"invalid_image_input","param":"input[0].content[1].image_url","message":"The image request is invalid. Check the input and try again."}}
```

该响应应作为确定性请求错误返回给调用方。将其改写为通用 HTTP 502 会掩盖 `code`、`param` 和实际错误说明，并可能让下游把同一请求重复提交，增加上游调用量。

## 2. 已核对的版本状态

- VPS 容器二进制自报：`Sub2API 0.2.13-anthropic-terminal`，提交 `9ed538faeec311a85712839c1771efb36189963e`。
- GitHub 同一提交的提交主题为 `fix(responses): require Anthropic message_stop before success`，提交日期为 2026-10-14。
- GitHub `backend/cmd/server/VERSION` 文件在该提交仍写 `0.2.12`；构建二进制标签另报 `0.2.13-anthropic-terminal`。本次以提交 SHA 识别源码，不以 VERSION 文件单独判断。
- 被替换的旧本地工作树已完整保留在 `D:\AI\SSH\sub2api-pre-vps-sync-20261015`，不属于本次实现范围。

## 3. 代码路径与根因判断

在冻结基线中，原生 Responses 上游 HTTP 错误进入：

1. `backend/internal/service/openai_gateway_forward.go`：先运行 `shouldFailoverOpenAIUpstreamResponse`；普通 `invalid_image_input` 400 不应触发账号 failover。
2. `backend/internal/service/openai_gateway_upstream_errors.go`：转入 `handleErrorResponse`。自定义错误透传规则和账号错误处理保持原有优先级；确定性 400 最终通过 `writeOpenAIUpstreamClientError` 写出真实 400 和可安全透传的 OpenAI 错误字段。
3. `backend/internal/service/openai_upstream_client_error.go`：集中构造确定性客户端错误响应。

本冻结基线的生产代码已包含确定性 400 分支，且已有普通 400、错误字段、failover 和 5xx 维持通用 502 的测试。本次先增加针对 MCP 实际图片请求形态及 `invalid_image_input` 的精确回归；除非该测试揭示生产缺口，不修改生产逻辑。

## 4. 设计约束

- 对 `invalid_image_input` 这类非流式 Responses 请求错误返回 HTTP 400，并保留上游 `error.type`、`code`、`param`、`message`。
- 此类确定性错误不能作为重试型上游故障改写为 502，也不能触发账号 failover。
- 必须保留现有优先级：显式错误透传规则、账号自定义错误码策略及 transient/capacity 处理不得因本回归被绕过。
- 可重试容量型 400、认证/账单/权限错误、404、429、5xx 继续按当前策略处理；本次不重定义它们的状态映射或冷却行为。
- 测试只构造内存 HTTP 响应，不连接 Provider、404token、Sub2API VPS，也不使用真实 API Key。

## 5. 开发清单

### 必须修改

| 文件 | 变更 | 验收点 |
|---|---|---|
| `backend/internal/service/openai_upstream_client_error_test.go` | 新增 `TestHandleErrorResponse_ResponsesImageGenerationInvalidImageInputReturns400`，模拟 `gpt-image-2.5`、`tools[].type=image_generation`、参考图输入和上游结构化 400 | 断言非 failover、下游状态为 400、`type/code/param/message` 保留、响应无通用 `Upstream request failed` |

### 只读审查，不改动

| 文件 | 复用位置 |
|---|---|
| `backend/internal/service/openai_gateway_forward.go` | Responses 上游错误分类和 `handleErrorResponse` 调用 |
| `backend/internal/service/openai_gateway_upstream_errors.go` | failover 判定、错误规则优先级与确定性 400 响应分支 |
| `backend/internal/service/openai_upstream_client_error.go` | 错误字段清理和 JSON 响应构造 |

禁止为本次问题重写通用错误映射、跨协议转换、重试/冷却策略、账单/路由、MCP 或 VPS 配置。

## 6. 验收测试

从 `D:\AI\SSH\sub2api\backend` 执行：

```powershell
go test -tags=unit ./internal/service -run '^TestHandleErrorResponse_ResponsesImageGenerationInvalidImageInputReturns400$' -count=1
go test -tags=unit ./internal/service -run '^(TestHandleErrorResponse.*|TestIsOpenAIDeterministicClientError|TestWriteOpenAIUpstreamClientError.*|TestShouldFailoverOpenAIUpstreamResponseContextWindow502|TestOpenAIImageFailoverClassifierIsImageOnlyAndNarrow)$' -count=1
go test -tags=unit ./internal/service -count=1
```

第一条为新回归，第二条检查相邻映射及 failover，第三条为 service 包完整回归。若第三条因无关测试失败，必须记录失败用例并单独复现；不得将其写成通过。不得调用真实上游来证明 HTTP 映射。

## 7. 审计和完成条件

1. 文档先由独立只读审计检查范围、代码路径、优先级和验收命令；审计通过后冻结。
2. 仅在冻结后的文档约束内增加精确测试；如测试证明生产代码存在真实缺口，先更新文档并重新审计，再改生产代码。
3. 代码完成后冻结提交/差异与文件哈希，由独立只读审计确认无生产策略漂移、无超范围修改、测试断言有效。
4. 报告新回归、相邻测试和完整包测试的真实结果。通过只代表该本地源码快照，不代表 VPS 已重新部署或已做真实 Provider 测试。

## 8. 当前轮实际结果

- 文档独立审计：PASS（开发文档边界和路径审计通过；修正验收命令后复审通过）。
- `gofmt -d backend/internal/service/openai_upstream_client_error_test.go`：通过；新增末尾换行后无格式差异。
- 精确图片 400 回归：PASS，`TestHandleErrorResponse_ResponsesImageGenerationInvalidImageInputReturns400`。
- 相邻错误映射与图片 failover 测试组：PASS。
- 主控先前完整 `internal/service` 包测试收据：PASS，206.516 秒；随后独立复审者重新运行同一命令，47.781 秒后 FAIL。因此不能把完整 service 包标为稳定通过，先前的 PASS 仅是一次单独运行结果，未被本次复测复现。
- 独立复测失败项：`TestFetchOpenAIAccountModelsOAuthPopulatesPickerFields`（本地 loopback socket 被系统拒绝）；`TestContentModerationTypeSafeAllCategoriesAndImages`（`typesafe transport unavailable`）；`TestContentModerationTypeSafeFailureOpenAndKeyIsolation`（断言不符）；`TestContentModerationTypeSafeTestDoesNotSwitchActiveEngine`（nil pointer panic）。这些失败位于不同测试路径；当前没有证据表明它们由新增图片错误映射测试造成。需要单独诊断完整包失败，不能归为本次修复已通过。
- 独立代码审计：PASS，仅限测试文件末尾新增的 34 行 test-only 差异；审计确认固定基线为 `9ed538faeec311a85712839c1771efb36189963e`，未发现生产代码差异或测试断言问题。审计未独立复跑完整 service 包成功。
- 生产代码未改；本地只新增上述图片回归测试和本文件。未调用 Provider、未部署 VPS。
- 测试文件 SHA-256：`C2FF4498EDDE4460F18C786081C1C3C931B0369757AC60C9A6B78E4E370F333A`。`git diff --check` 通过；相对固定基线的代码差异只有该测试文件新增 34 行。文档当前 SHA-256 待本轮文档审计后记录。
