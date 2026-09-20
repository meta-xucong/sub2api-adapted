# 统一 API 运行时实现与最终审计基线（Revision 8）

状态：`LOCAL_RUNTIME_IMPLEMENTED / DURABLE_RECOVERY_WIRED / CAPABILITY_MATRIX_FAIL_CLOSED / FRONTEND_IMPLEMENTED / LOCAL_AUDIT_PASS_WITH_STAGING_GATES / PRODUCTION_GATE_DISABLED`

本章是前面设计文档与候选代码之间的当前映射。它描述的是 `upgrade-worktree/merged-dryrun` 本地候选树，不是 aiself 或 404token 的已部署状态。统一运行时配置仍必须保持 `false`；本章没有授权线上迁移、真实付费调用、GitHub 推送或合并。

## 1. 已落地的隔离边界

- 新运行时只挂在 `/unified/v1` 命名空间，包含 `models`、`chat/completions`、`responses`、`images/generations`、`images/edits`、`videos` 以及视频状态/内容读取路径。
- 旧 `/v1/*`、旧 `GatewayService`、旧 `OpenAIGatewayService`、旧 `usage_logs` 计费和既有账号调度没有被统一路由改写。
- `UnifiedGatewayRuntimeEnabled` 是独立的 fail-closed gate；管理端不能直接打开它。gate 关闭时统一 handler 返回 `UNIFIED_GATEWAY_DISABLED`，旧入口继续按原逻辑工作。
- 统一目录查询要求目标、绑定、revision、endpoint 和 enabled 状态一致；绑定可以覆盖目标 endpoint，避免同一账号被错误地投递到另一个协议端点。

主要入口文件：

| 组件 | 候选文件 |
| --- | --- |
| 运行时状态机 | `backend/internal/service/unified_gateway.go`、`unified_gateway_runtime.go` |
| 上游执行与协议适配 | `backend/internal/service/unified_gateway_upstream.go`、`unified_gateway_grok_adapter.go` |
| 隔离路由 | `backend/internal/server/routes/unified_gateway.go`、`gateway.go` |
| 管理 API/物化 | `backend/internal/service/unified_gateway_admin.go`、`backend/internal/repository/unified_gateway_admin_repo.go` |
| 管理前端 | `frontend/src/views/admin/UnifiedGatewayView.vue`、`frontend/src/types/unifiedGateway.ts` |
| 账本/快照/恢复迁移 | `backend/migrations/235_unified_gateway.sql`、`236_unified_gateway_admin.sql`、`237_unified_gateway_runtime.sql`、`238_unified_gateway_recovery.sql` |
| 恢复与能力门禁 | `backend/internal/service/unified_gateway_reconciler.go`、`unified_gateway_recovery_runtime.go`、`unified_gateway_capabilities.go` |

## 2. 请求、调度和结算闭环

1. handler 从 JSON 或 multipart 请求提取 `model`，生成 owner-scoped `request_id/attempt_id`，并计算预估单位；图片生成/编辑按请求至少预估一次，视频按请求预估一次。
2. runtime 从统一目录取得候选，按 priority 排序；每次候选尝试前重新读取账号状态，通过 `IsSchedulable()` 排除临时禁用、过载、过期或不可用账号。失败只在候选边界内切换，不回落旧 `/v1`。
3. `UnifiedGateway.Execute` 先解析并冻结 `UnifiedGatewayPriceSnapshot`，再 `Reserve` 预占；快照保存 target、binding、账号、provider、上游模型、endpoint 和完整价格摘要。
4. 上游成功后按实际 usage/image count 计算最终价并 `Capture`；上游错误、超时、取消、空结果、usage 缺失或适配不支持都 `Release`，用户费用为零。
5. 异步 provider 先进入 `PENDING`，只保留预占；轮询仍 pending 时更新最新任务 ID/响应，成功才 capture，失败释放。重复提交、重复轮询和重复结算以 owner + request + attempt 幂等。
6. 已捕获的快照回放固定的原始响应字节；SSE/二进制不再强制 JSON 化，JSONB 列与 `response_body_bytes` 并存以兼容旧读取工具。
7. 账本和快照不是同一数据库事务，因此新增 durable recovery task、租约领取、重试和幂等修复：capture 已提交而快照未更新时补齐 captured；reserved/pending 超过恢复宽限期时 release 并保持零用户收费。领取后的 attempt 版本会约束 complete/retry，过期 worker 不能重开已被新 worker 接管的任务。恢复循环仅在统一 runtime gate 开启时启动，退出先停 worker 再关数据库。

## 3. provider 覆盖矩阵

| provider/账号形态 | 当前候选适配 | 计费与失败语义 |
| --- | --- | --- |
| OpenAI API key / OpenAI-compatible API key | `/chat/completions`、`/responses`、普通图片；按账号 base URL、model mapping 和 endpoint 能力发送 | 读取真实 usage；没有正 usage 且不是按次规则时 fail closed |
| OpenAI OAuth / setup-token Plus/Pro | 复用已审计 `OpenAIGatewayService` 的 ChatGPT/Codex、Responses 和图片路径，统一账本在外层接管 | 不复用旧用户计费；只把成功的 provider 结果交给统一 capture |
| DeepSeek / Kimi / 智谱 | OpenAI-compatible 路径；原生 Responses、Chat Completions 和 provider-specific reasoning 变换按账号协议处理 | 可用手动 profile；不得把 CN provider 的 Anthropic base URL 当作 OpenAI URL |
| CN provider 原生 Anthropic 协议 | 复用现有 OpenAI↔Anthropic 转换入口；图片/视频在本适配器中明确拒绝 | 失败释放，避免把协议不支持误算成成功 |
| 火山 Ark / 豆包 | Responses、图片/编辑请求执行确定性字段过滤、模型恢复、图片 generation 变换和 Ark base URL 选择 | 建议 `manual_only + provider_specific` 或显式单位价 |
| Grok 图片/视频 | 图片/视频走独立 Grok media adapter；视频创建→pending→poll→content，不把 Grok 当普通 OpenAI 图片 | 只有视频完成/内容成功交付才 capture；失败/超时不扣用户费 |
| Gemini / AI Studio | 复用 `GeminiMessagesCompatService`，当前统一 OpenAI 入口支持 Chat Completions；Responses/图片未在该适配器中猜测 | 未覆盖 endpoint 必须在目录中禁用，不得自动改走旧路由 |
| Antigravity | OAuth 复用原生兼容服务支持 Chat/Responses；API key 复用 Gemini 兼容 Chat 路径 | token/project/model whitelist 失败均释放；未覆盖协议 fail closed |

管理发布和运行时共享 `unified_gateway_capabilities.go` 的 fail-closed 能力矩阵。未知 provider、账号平台/类型不匹配、binding endpoint 不支持时，validate/publish 会产生阻断；已有异常物化数据在 runtime 入口也不能靠默认适配器静默放行。能力矩阵包含 OpenAI API key/OAuth、OpenAI-compatible、CN 原生 Anthropic、Ark、Grok media、Gemini 和 Antigravity，并为 provider-specific endpoint 保留显式拒绝规则。

自动“上游声明倍率”探测仍不是统一运行时的隐式网络行为：运行时 `Meta`/`ProbeBinding` 当前保持能力保守，探测不成功时只能使用前端配置的手动倍率、手动单位价或 provider-specific 规则。没有有效 probe 或手动兜底的 lane 不应发布为可执行 lane。

## 4. 价格模型与同名模型

同一 `public_model + endpoint` 可以有多个 lane；Plus、Pro、生图、Ark、Grok 视频各自拥有 profile 和 binding。选中哪条绑定，就冻结哪条绑定的价格，不使用统一公共模型价。

支持的价格输入包括：

- `provider_base_unit_price × upstream_multiplier × user_markup_multiplier`；
- 手动基础单位价；
- per-request/per-image/per-video 按次价格；
- provider-specific 手动规则、最低收费和舍入；
- probe 优先但手动兜底。

profile 使用十进制字符串传输，运行时解析为有限非负数；最低收费只在成功 capture 时生效。预占可以使用估算价，但最终扣款永远以成功结果和冻结 profile 为准。

## 5. 前端操作能力

管理页现在可以在一个聚合草稿中：

- 创建公共模型和多个 Billing Lane；
- 为每个 lane 选择 provider、上游模型、endpoint、账号、优先级和启用状态；
- 为每个 lane 填写 probe/manual/flat/minimum 价格规则；
- 为单个 binding 覆盖 endpoint，或显式继承 target endpoint；
- 查看账号资格、价格预览、revision/snapshot，并执行 validate、publish、disable、restore 等受保护生命周期操作。
- 查看 `admin_ui_enabled`、`migration_ready`、`runtime_enabled`、`runtime_effective` 和 read/write/probe 能力；当 Probe 能力未开放时，前端只显示手动确认/能力检查提示，不再发起必然返回 unsupported 的请求。

前端不会把统一 runtime gate 当成普通 lane 开关；发布成功也不代表生产流量已经启用。

## 6. 当前证据与必须补齐的生产门禁

当前本地证据：

- 统一 service、上游 executor、handler、route catalog、snapshot/admin repository、recovery reconciler 和 runtime wiring 的定向测试已通过；终态 `released/settlement_failed` 的异步轮询和内容读取也已 fail closed；
- backend 关键包编译检查已通过；repository、handler、routes、cmd/server 全量回归已通过；最终 service 全量回归 `go test ./internal/service -count=1` 已通过（186.489s）；
- 前端全量为 256 个测试文件、1870 个测试通过，typecheck 和 production build 均通过；build 仅有既有分包体积、浏览器数据和 Node deprecation 提示；
- 全仓编译检查 `go test ./... -run '^$' -count=1` 已通过；`go test -race` 仍受当前 Windows 环境缺少 gcc/cgo 工具链限制，不能伪造为通过；
- 没有执行真实 provider 付费请求，没有向 aiself/404token 数据库写入，没有打开 gate。

### 6.1 主控审计与独立只读复核

当前代码级审计结论为 `PASS_WITH_STAGING_GATES`：

- 独立只读复核确认 recovery runtime 的 gate + migration readiness 前置、退出清理和生产 wiring 正确；
- 独立只读复核确认 `PublishDraft` 的 If-Match、validation token、scope、能力校验、幂等和 CAS 顺序正确；
- 独立只读复核确认生产 runtime 要求 account reader，管理端与运行端共享 fail-closed capability matrix，未知 provider/不支持 endpoint 不放行；
- 独立只读复核确认 `/unified/v1` 与旧 `/v1` 路由隔离；
- 初始复核发现的“非标准构造器可能缺少 account reader”已通过 runtime readiness 硬门禁关闭；恢复 worker 在迁移未就绪时也不会启动；
- 审计只覆盖候选源码与本地测试，不等价于 PostgreSQL、真实 provider、付费链路、进程崩溃恢复或生产灰度证据。

生产放行前仍必须由独立环境完成：

1. PostgreSQL 235→236→237→238 在 staging 的 fresh/repeat/upgrade/rollback 验证，包含 recovery task lease、`response_body_bytes`、status constraint、partial unique index 和 owner scope；
2. 真实 ledger/recovery worker 在并发重复 capture/release、余额不足、进程重启和数据库超时下的原子证据；
3. 每个已发布 lane 至少一次 provider-specific staging 冒烟：OpenAI Plus/Pro、DeepSeek/Kimi/智谱、Ark、Gemini/Antigravity、Grok image/video；
4. SSE、图片编辑 multipart、视频 pending/poll/content、失败不收费和响应回放的端到端测试；
5. `go test -race`、全仓测试、迁移检查、secret scan、灰度/回滚演练；
6. 明确的 lane 探测/手动倍率审计记录，以及 gate 打开后的即时关闭开关。

在上述证据完成前，`PRODUCTION_GATE_DISABLED` 是唯一允许状态。本章不把“代码已经注册到候选 server graph”解释为“已经上线”。
