# OpenAI Remote Compact 流内失败与 Smart Router 临时冷却

## 1. 任务记录

### 用户要求（原文）

> 你针对这个情况，设计一个完整的优化修复方案,并落开发文档，审计、根据文档落代码，审计代码，本地测试。全部通过后传GitHub和应用到aiself和404token

### 目标与非目标

目标是修复 Sub2API 对 OpenAI Responses remote compact 流内失败的可观测性和线路恢复：

1. 上游已经以 HTTP 200 建立 SSE、随后发送 `response.failed` 时，remote compact 不能再记录为成功；
2. `Too many pending requests` 等明确并发拥塞信号必须保留 429 语义，交给现有 Smart Router 记录；
3. compact 并发拥塞只造成 lane 级短暂冷却和临时优先级惩罚，冷却后自动恢复，不永久禁用账号；
4. 保留既有首个语义输出前 failover、请求内排除账号和恢复探测机制。

明确不做：

- 不修改公共 HTTP API、JSON/SSE schema、数据库 schema 或账号人工 priority；
- 不修改请求总超时、流空闲超时、最大切线次数、同账号重试次数或调度排序算法；
- 不改变普通 Responses、Chat、图片、Grok 和 WebSocket 路由；
- 不新增依赖、后台任务、重连接接管或重复计费逻辑；
- 不执行付费模型调用作为本地验收条件。

## 2. 基线与复杂度门禁

- 仓库：`sub2api`
- 基线提交：`20a905caa` (`custom/main`)
- 基线工作区已有改动（不属于本任务）：
  `frontend/src/router/index.ts`、`frontend/src/views/auth/LoginView.vue`、
  `backend/sub2api-linux`、`tools/codex_yetoken_readonly.sh`。
- `CONTRACT_REV`: `compact-stream-failure-v1`
- `COMPLEXITY_GATE`: `ESCALATE_REQUIRED`（跨 service、handler、Smart Router health 和两台 VPS，且涉及流式错误语义）。
- 唯一写入范围：本文档、`backend/internal/service/openai_gateway_response_handling.go`、
  `backend/internal/service/openai_gateway_passthrough.go`、
  `backend/internal/service/smart_router_adapter.go`、
  `backend/internal/smartrouter/core/classifier.go`、
  `backend/internal/smartrouter/core/health.go`，以及对应定向测试文件。
- 执行者写入完成并固定版本后，独立审计者只读复核；审计通过后才允许提交、推送和 VPS 热更新。

## 3. 根因证据

当前 `Responses` handler 会在请求结束时根据 `c.Writer.Status()` 记录
`codex.remote_compact.succeeded`。body-signal compact 的 keepalive 会先把 wire 状态固定为
200；服务层在 `response.failed` 已经写出语义事件后返回普通 error，但没有设置
`OpsStreamError`，因此结尾日志只能看到 200，错误看板和 compact outcome 会出现“成功”。

另外，`openAIStreamFailureStatus` 只把显式 `rate_limit` 代码提升到 429。部分聚合上游把
`Too many pending requests` 放在 message 中，或者以 `server_error` 代码返回；这种错误在
流内结束后可能丢失并发拥塞语义，Smart Router 只能看到 `stream_interrupted` 或未知错误。

## 4. 最小修复设计

### 4.1 失败事件的观测闭环

在非 passthrough 和 passthrough 的 Responses 流处理器中，仅当 `response.failed` 不再走
“首个语义输出前可安全 failover”分支（即已向客户端提交语义失败事件，或该失败本身不可
failover）时，且请求属于 remote/in-band compact，设置一次 `OpsStreamError`：

- `IntendedStatus` 使用流内错误的语义状态码；
- `Code` 保留上游错误码（若存在）；
- 429、5xx 等上游失败使用 `CountTowardsSLA=true`；确定性的 4xx 使用普通
  `OpsStreamError`（保留上游 `code`，但 `CountTowardsSLA=false`）；
- 继续保留现有对客 `response.failed` 内容和错误类型，不追加第二个终止事件。

首个语义输出前可切线的失败不提前设置该标记，避免“第一条线路失败、第二条线路成功”被
最终 compact outcome 误记为失败。若所有线路都失败，handler 现有错误兜底会设置最终标记。

### 4.2 并发拥塞语义

将明确的并发/排队信号（包括 `Too many pending requests`）在任何 HTTP 状态缺失的流内
错误中识别为 `FailureConcurrencyLimited`，优先级高于“流已中断”判断；因此普通 Responses
仍保持现有不惩罚策略，compact 则进入 4.3 的专用短冷却。

### 4.3 compact 的短冷却与临时降权

仅对 `CapabilityResponsesCompact + FailureConcurrencyLimited` 使用已有 transient cooldown
参数：

- `ConsecutiveFailures` 按现有计数递增，冷却使用已有指数退避（默认约 30 秒、60 秒……并受
  `MaxCooldown` 限制），不使用“冷却到下一次 04:00 校准”的严格 compact quarantine；
- `HealthPenalty` 增加 1，作为内存中的临时有效优先级惩罚；不写回账号配置；
- `RecoveryStage=Cooling`，不分配永久/长时恢复槽；冷却后进入既有 probe/warming 流程；
- 成功会沿用既有恢复计数，连续成功后逐步清除临时惩罚；只有池中全部 lane 都冷却时才允许
  作为最后候选，保持可用性。

其他 compact 失败（不支持、协议错误、5xx、鉴权）继续使用现有严格 compact quarantine，
避免把真正不具备 compact 能力的线路反复打爆。

## 5. 不变量与验收

### 不变量

- 线路仍按 `(lane, capability, exact model)` 隔离；普通 Responses 不受 compact 健康状态影响；
- Smart Router 不永久关闭账号、不修改人工 priority、不改变已有最大尝试/超时配置；
- 已提交语义输出后不切换账号，不拼接两条 SSE；
- 首个语义输出前的可恢复失败仍走现有 failover；成功备用线路最终 outcome 必须为 succeeded；
- 客户端收到最多一个合法 `response.failed` 终止事件；错误日志不含完整 prompt、凭据或密钥。

### 必测场景

R1. compact SSE 在 HTTP 200 后发送 `response.failed`（含 `Too many pending requests`），服务
设置 `OpsStreamError`，日志 outcome 为 failed，且保留 429/并发错误码。

R2. 同一错误在首个语义输出前仍返回 `UpstreamFailoverError`，不设置最终失败标记；备用账号成功
时 outcome 为 succeeded。

R3. `ClassifyFailureDetails` 在 status=0、message 仅含 `Too many pending requests` 时返回
`FailureConcurrencyLimited`，不被 `FailureStreamInterrupted` 抢先匹配。

R4. compact 并发失败只得到短暂 cooldown 和 +1 临时 penalty；普通 Responses 同账号状态不变；
冷却到期后 lane 可再次选择。

R5. 既有 compact 5xx/能力错误仍保持严格 quarantine；普通 429、图片和其他模型回归不变。

R6. 定向 Go 测试、`git diff --check`、构建通过；不得产生付费上游调用。

R7. 确定性 4xx（例如 `context_length_exceeded`）保留错误码且不计入 SLA；错误消息中的
敏感查询参数在 `OpsStreamError` 中已清洗并截断。

## 6. 版本、审计与部署门禁

代码与测试完成后先固定提交/补丁哈希，独立审计逐条检查 C1（范围）、C2（SSE 语义）、C3
（Smart Router 临时冷却）、C4（回归测试）和 C5（无敏感信息/无生产副作用）。审计结论必须为
“符合”才能推送。

推送后分别在 aiself 与 404token 进行备份、二进制热更新和健康检查；不重建镜像、不改数据库、
不清理现有运行数据。任一 VPS 热更新失败则停止该 VPS 的后续变更并回报，不把本地绿灯当作
生产通过。
