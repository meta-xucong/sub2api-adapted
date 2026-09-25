# Sub2API 上游可用模型自动刷新

## 0. 任务记录

```yaml
TASK_ID: MODEL-AUTO-REFRESH-001
TASK_NAME: upstream-model-auto-refresh
CONTRACT_REV: v1.3.1-route-guard
COMPLEXITY_GATE: ESCALATE_REQUIRED
BASE_COMMIT: c4d48cf753c62f166d848c06c1fc25a23430316d
BRANCH: codex/t0-backend-completion-20260925
UNCOMMITTED_BASELINE: frontend/pnpm-lock.yaml
UNIQUE_WRITER: main-session
AUDIT_OWNER: main-session-read-only-audit; independent-audit-pending
CONTEXT_MODE: ARMED
WRITER_STATUS: LOCAL_IMPLEMENTATION_COMPLETE_SELF_AUDIT_COMPLETE
IMPLEMENTATION_STATUS: COMPLETE_LOCAL_TESTED
```

本任务与 T0 第三方 Responses 兼容层分离。T0 的固定版本、协议适配、计费和断线恢复不在本任务范围内。

## 1. 用户目标与原始要求

用户原始要求：

> 支持自动刷新可用模型。否则我过几天就得重新跑一次，很麻烦。你设计一下。

随后确认：

> 这个和 T0 无关，我之前不是让你开发自动刷新可用模型吗？

本任务的结果必须让 Sub2API 定期从可探测的上游获取模型目录，并让下游公开目录只暴露经过正式名归一化且当前被上游确认可用的模型；上游暂时失败时不能因为一次网络错误清空生产模型目录。

## 2. 当前实现审计结论

审计基线为 `c4d48cf753c62f166d848c06c1fc25a23430316d`。当前观察到：

1. `backend/internal/handler/admin/account_handler.go:2838` 提供 `POST /api/v1/admin/accounts/:id/models/sync-upstream`，另有 preview 接口；这是人工触发路径。
2. `backend/internal/service/upstream_models.go:193-320` 已有上游模型列表抓取、元数据补全和 `accounts.extra` 快照持久化能力，但没有后台调度调用者。
3. `backend/internal/service/gateway_service.go:1383-1465` 的 `GetAvailableModels` 从账户 `credentials.model_mapping` 汇总模型，并使用短缓存；它没有请求上游 `/models`。
4. `backend/internal/config/config.go:1134` 的 `models_list_cache_ttl_seconds` 默认值为 15 秒。它只是本地列表缓存 TTL，不是上游目录刷新。
5. `ScheduledTestRunnerService` 定时运行账号连通性测试，不调用 `SyncUpstreamModelCatalog`。
6. `model_id_normalization.go` 已实现日期、preview、exp、语义后缀和歧义收敛，但归一化输入仍主要来自配置映射，不能证明上游当前可用。
7. 当前工作树唯一已有未提交改动是用户原有的 `frontend/pnpm-lock.yaml`；本任务不得覆盖、清理或纳入它。

结论：`AUTOMATIC_UPSTREAM_MODEL_REFRESH=NOT_IMPLEMENTED`；已有的是人工同步、缓存刷新和模型名归一化。

## 3. 范围冻结

### 3.1 本次要做

- 新增后台上游模型目录刷新任务。
- 复用现有 `fetchUpstreamModelList`/`SyncUpstreamModelCatalog` 的供应商适配，不为每个供应商另造轮询逻辑。
- 将成功的上游目录以 `accounts.extra` JSONB 快照保存，不新增数据库表或迁移。
- 对成功目录执行现有正式名归一化，并保存“公开正式名 → 实际上游 ID”的安全映射。
- 让 `/v1/models`、Codex manifest、模型候选和实际路由使用同一份可用性判断。
- 上游成功后立即移除已确认下线的模型；临时网络、认证、限流或服务端失败保留最后成功目录一段宽限期。
- 多实例部署只允许一个实例执行本轮刷新，并限制并发和超时。
- 保留人工同步接口，但人工和后台必须调用同一核心刷新函数。
- 增加单元、集成、跨进程锁、失败回退和真实脱敏线上验证。

### 3.2 明确不做

- 不修改 Codex 安装器、CCSwitch、`model_catalog.rs` 或官方 Codex 二进制。
- 不修改前端页面；如需显示状态，先通过现有管理接口/日志提供后端字段，不在本任务新增前端功能。
- 不直接覆盖用户手工维护的 `credentials.model_mapping`。
- 不把上游返回的任意日期、preview、exp 或未知语义变体猜测成正式名。
- 不把 `/v1/models` 的 10–30 秒本地缓存 TTL 当成上游刷新机制。
- 不改变计费规则、钱包流水、Responses/Chat/Anthropic 协议语义。
- 不为不支持模型列表接口的供应商伪造“已刷新”状态；此类账号继续使用现有静态映射/默认目录，并标记 `unsupported`。

## 4. 推荐行为契约

### 4.1 数据来源与权威性

每个账号独立保存最后一次成功的上游目录。只有满足以下全部条件才替换成功快照：

- HTTP 响应成功；
- 响应是合法的受限大小 JSON；
- 模型 ID 非空、去重、无通配符；
- 目录不是意外的空列表；
- 归一化后至少有一个可安全公开的模型，或供应商明确声明该账号确实没有模型。

一次失败、空响应、解析失败、超限、401/403、429、5xx 或网络超时不得覆盖最后成功目录。

### 4.2 刷新状态

写入现有 `accounts.extra` 的新键 `upstream_model_refresh`。建议结构：

```json
{
  "schema_version": 1,
  "status": "fresh",
  "source": "upstream_models_endpoint",
  "last_attempt_at": "2026-09-26T00:00:00Z",
  "last_success_at": "2026-09-26T00:00:00Z",
  "next_refresh_at": "2026-09-26T20:00:00Z",
  "raw_models": ["deepseek-v4-flash-0731"],
  "public_models": ["deepseek-v4-flash"],
  "public_to_upstream": {"deepseek-v4-flash": "deepseek-v4-flash-0731"},
  "raw_digest": "sha256:...",
  "last_error": null
}
```

约束：

- 不保存凭证、响应原文或完整错误 body。
- `raw_models`、`public_models` 和映射按稳定顺序保存并设置数量/字节上限。
- `last_error` 只保存分类、HTTP 状态和脱敏短消息。
- `status` 至少包括 `fresh`、`stale`、`expired`、`unsupported`、`error`。
- 快照写入用现有 `UpdateExtra` 的 JSONB 合并语义，不能读改写整个账号，避免覆盖并发写入的其它 extra 字段。

### 4.3 新鲜度与失败回退

推荐默认值：

```yaml
enabled: true
schedule: "04:00"
timezone: "Asia/Shanghai"
refresh_interval: 24h
request_timeout: 30s
stale_grace: 48h
max_concurrency: 4
```

- 任务使用固定的 `0 4 * * *` 调度（Asia/Shanghai，全年 UTC+8），不是从进程启动时间计算的 24 小时定时器；并发上限用于控制凌晨批量刷新对上游的压力。
- `fresh`：最近一次 04:00 批次成功；公开和路由均使用本次快照。
- `stale`：刷新失败但最后成功时间未超过 `stale_grace`；继续使用最后成功目录，同时记录告警和失败计数。
- `expired`：超过宽限期仍未成功；对支持目录探测的账号 fail closed，不再公开仅由旧快照证明的模型，也不允许其路由这些模型。
- `unsupported`：供应商明确不支持模型列表接口；不应用自动下线过滤，保持现有手工映射/默认行为，并避免每个 tick 重复请求。
- 首次刷新失败且没有成功快照：不清空已有手工配置；保持现有静态行为并标记 `error`，直到首次成功快照产生。

失败不改变下一次固定执行时间；下次仍在下一个 04:00 批次重试。48 小时宽限允许一次凌晨批次失败而不立即误隐藏模型。

### 4.4 正式名归一化和路由

- 原始上游 ID 只用于转发、计费和审计；下游公开只使用 `NormalizePublicModelIDs` 产生的正式名。
- 日期/preview/exp 只有在现有安全规则能够唯一归属时归并；多个候选冲突时隐藏，不猜测。
- 语义子模型同时存在时，不公开模糊父模型；`gpt-5.6` 与 `gpt-5.6-luna/sol/terra` 的冲突继续按现有 fail-closed 规则处理。
- 构建 `public_to_upstream` 时，若一个正式名对应多个不同原始 ID，只有存在明确精确基名或唯一安全目标时才保留；否则不公开该正式名。
- 有手工 `model_mapping` 的账号：保留手工映射内容，只用成功上游快照过滤其目标是否仍可用；不自动改写凭证。
- 无手工 mapping 且供应商支持目录探测的账号：使用成功快照生成公开模型，并将正式名映射回原始上游 ID。
- passthrough 账号仍由上游决定模型语义，但 `/v1/models` 只公开成功快照确认的正式名；请求路由必须使用同一可用性判断，不能出现“列表没有但请求能走”或“列表有但请求必然走到已下线模型”。
- composite 继续使用现有组/账号优先级；候选集先经过账号快照过滤，再进行平台归一化和所有权选择。

### 4.5 人工同步

现有 `sync-upstream` 和 preview 接口改为调用同一个 `RefreshAccountModelCatalog` 核心函数，区别只在于调用来源、是否强制刷新和返回详细结果。人工同步成功后立即失效相关模型列表缓存；失败不覆盖最后成功快照。

## 5. 实现方案

### 5.1 建议修改文件

实现阶段只允许修改 Sub2API 后端及其测试，目标文件如下；执行前若调用链证明不需要某文件，不应为了凑清单修改它。

```text
backend/internal/config/config.go
backend/internal/service/upstream_models.go
backend/internal/service/upstream_model_refresh_service.go       # 新增
backend/internal/service/gateway_service.go
backend/internal/service/account.go
backend/internal/service/openai_codex_models_service.go
backend/internal/handler/gateway_handler.go
backend/internal/service/wire.go
backend/cmd/server/wire_gen.go                                  # 仅在生成/注入确有需要时
backend/internal/service/upstream_model_refresh_service_test.go   # 新增
backend/internal/service/upstream_models_test.go
backend/internal/service/openai_codex_models_service_test.go
backend/internal/service/model_id_normalization_test.go           # 仅在归一化回归需要时
```

不预先批准新增数据库 migration；`extra` JSONB 是本任务的最小持久化边界。

### 5.2 后台任务

新增 `UpstreamModelRefreshService`，提供：

- `Start`/`Stop`：按 `0 4 * * *`（Asia/Shanghai，UTC+8）每天执行一轮；
- `RunOnce`：测试和运维可直接触发一轮；
- `RefreshAccountModelCatalog`：人工和后台共享的单账号核心逻辑；
- 现有 `LeaderLockCache`/`tryAcquireSingletonLeaderLock`：集群单实例执行；
- 有界并发、单账号超时、稳定排序、上下文取消和可观测统计。

后台任务只扫描 active、未删除且具备可探测凭证的账号；不因临时限流状态把账号从目录永久移除。账号凭证、base URL 或平台变化时，应让旧快照立即进入 `error`/due 状态，下一次成功请求重新建立权威目录。

### 5.3 公开目录和实际路由共用判断

不能只修改 `GET /v1/models`。实现必须抽出可测试的账号级解析结果，例如：

```text
ResolveAvailableModel(account, requested/public model) ->
  {PublicID, UpstreamID, Available, Reason, SnapshotStatus}
```

`GetAvailableModels`、composite ownership、Codex manifest 和请求调度均调用同一解析逻辑。`IsModelSupported`/`ResolveMappedModel` 只能在兼容层保留现有语义的前提下接入快照，不得产生第二套日期归一化规则。

### 5.4 缓存失效

- 后台成功刷新、手工成功同步、账号 mapping/凭证更新均失效该账号所属 group/platform 的模型列表缓存。
- 失败只更新快照状态，不把短缓存误当作新鲜目录；必要时保留当前缓存直到 TTL 到期。
- composite ownership 缓存也必须按受影响 group/model 失效，避免目录已移除而所有权仍命中旧值。

## 6. 配置与安全边界

实现采用 `GatewayConfig` 下的扁平配置键，避免新增嵌套配置解析兼容问题；所有值有上下限并支持关闭：

```yaml
gateway:
  upstream_model_refresh_enabled: true
  upstream_model_refresh_request_timeout_seconds: 30
  upstream_model_refresh_stale_grace_hours: 48
  upstream_model_refresh_max_concurrency: 4
```

调度时间是代码契约而不是可配置项：`0 4 * * *`、`Asia/Shanghai`（UTC+8），等价于每天 24 小时刷新一次。

必须复用现有上游 base URL 校验、代理、TLS 指纹、token provider 和响应体 8 MiB 上限。不得把用户提交的任意 URL、凭证、响应 body 写入日志或快照。刷新失败日志只包含 account ID、platform、分类、HTTP 状态、模型数量和脱敏错误。

## 7. 验收矩阵

### R1：成功发现与归一化

- 上游返回正式 ID：公开同名，路由同名。
- 上游只返回唯一日期/preview/exp：公开安全正式名，路由回原始 ID。
- 上游返回多个日期候选或多个语义子模型造成歧义：冲突名不公开、不猜测。
- 上游返回错误脏 ID、通配符、空 ID：不进入公开目录。

### R2：下线与失败保护

- 成功刷新从 A 列表变为 B 列表：A 中消失且不在 B 的模型立即从 `/v1/models`、manifest、composite 和路由中消失。
- 一次超时/429/5xx/无效 JSON：继续使用最后成功目录，状态 `stale`。
- 超过宽限期仍失败：状态 `expired`，支持探测的账号不公开旧快照模型。
- 空响应不能把全部模型误删；必须按供应商安全规则处理并有测试证明。

### R3：账号和供应商边界

- 手工 `model_mapping` 的 key/value 不被后台改写。
- 无 mapping 的 API key/兼容账号能通过成功快照公开并路由新模型。
- 不支持 `/models` 的账号保持现有手工/默认行为并标记 `unsupported`。
- GLM、DeepSeek、Claude 和 OpenAI 兼容账号至少各有成功、失败、日期归一化夹具。
- OAuth/token provider 复用现有刷新和代理，不重复保存敏感凭证。

### R4：调度和多实例

- 两个服务实例同时到期时，上游只收到一份刷新请求。
- Redis/leader lock 暂时不可用时有明确 fail-safe 行为，不产生无限并发刷新。
- 单个上游慢请求不会阻塞整轮；超时后其它账号继续。
- 重启后快照和状态可恢复，启动不会无界并发打满上游。

### R5：接口、缓存和账务不回归

- `/v1/models`、Responses、Chat Completions、Anthropic 路由使用同一可用性结果。
- 模型列表变更后缓存及时失效；无变更不产生不必要写入。
- 计费的 requested/upstream model 仍保存原始实际路由 ID；正式公开名不改变成本口径。
- T0 全部定向测试和既有全仓测试保持通过。

## 8. 测试计划

### 本地模拟

```powershell
go.cmd test -count=1 -timeout=300s ./internal/service -run 'UpstreamModel|ModelID|Gateway.*Models'
go.cmd test -count=1 -timeout=300s ./internal/handler/admin ./internal/service ./internal/repository
go.cmd test -count=1 -timeout=420s ./...
go.cmd vet ./internal/service ./internal/handler ./internal/repository
git diff --check
```

覆盖 fake upstream 的 200/empty/invalid/404/405/401/429/500/timeout、快照读写、失败宽限、过期隐藏、别名冲突、并发锁、缓存失效和计费字段。

### 隔离 Redis/Postgres

- 两个独立进程同时执行 `RunOnce`，验证只有一个 leader 实际请求上游。
- 进程重启后读取同一 `extra` 快照，验证不丢目录。
- Redis 锁不可用时验证不会把错误当作成功目录，也不会无限重试。

### 真实线上

固定构建版本后，在 aiself 与 404token 的 `unified-api-internal` 上：

1. 记录刷新前 `/v1/models` 和数据库快照摘要；
2. 手工触发一次同步，确认成功目录、正式名和路由；
3. 等待/触发后台刷新，确认无需重复人工操作；
4. 对至少 GLM、DeepSeek、Claude 做目录与简单请求验证；
5. 核对下线/脏名称不展示、usage 原始模型与成本口径不变；
6. 注入网络失败或使用隔离账号验证最后成功快照和宽限期行为；
7. 保存脱敏事件、版本 SHA256、退出码和未验证项。

禁止用线上真实生产账号制造“下线模型”来做破坏性测试；该场景使用隔离测试账号或本地 fake upstream。

## 9. 发布、回滚和审计门禁

### 发布顺序

1. 文档审计通过后，执行者只修改冻结文件。
2. 先以 `enabled=false` 构建并通过本地/隔离测试。
3. 在 aiself/404token 部署相同 SHA256，确认健康和旧行为不变。
4. 开启自动刷新配置，手工触发/等待一轮，完成真实矩阵。
5. 固定最终提交、二进制 SHA256、配置摘要和证据包。

### 回滚

- 首选关闭 `upstream_model_refresh.enabled`，恢复原有 mapping/default 公开逻辑；快照留存但不参与路由。
- 若代码异常，恢复部署前二进制；不得删除账号 extra 或清理用户 mapping。
- 回滚后确认 `/v1/models`、简单请求、usage 和管理员模型列表恢复。

### 独立审计清单

- 审计员必须针对固定提交逐项检查 R1–R5，不接受“手动接口通过”替代后台调度证据。
- 检查实际差异是否越过后端范围、是否新增 schema/migration、是否覆盖用户 mapping、是否把失败当成功。
- 重新运行关键失败和边界测试；代码在审计期间冻结。
- 只有固定版本、完整证据和独立 `AUDIT_OWNER` 结论均满足，才可标记 `ACCEPTED`。

## 10. 已知开放问题与默认决定

```yaml
DQ-001:
  question: "刷新成功后是否物理改写 credentials.model_mapping？"
  decision: "否；只写 extra 快照并在公开/路由层过滤，避免覆盖用户配置。"
DQ-002:
  question: "上游临时失败是否立即隐藏模型？"
  decision: "否；保留最后成功目录至 stale_grace，之后对支持探测的账号 fail closed。"
DQ-003:
  question: "不支持模型列表接口的供应商如何处理？"
  decision: "标记 unsupported，保留现有静态行为，不伪造自动可用性。"
DQ-004:
  question: "默认刷新频率？"
  decision: "每天 04:00（Asia/Shanghai，UTC+8）执行；等价默认周期 24 小时；请求 30 秒、宽限 48 小时、并发 4。"
DQ-005:
  question: "是否在本任务新增数据库表？"
  decision: "否，复用 accounts.extra JSONB。"
```

## 11. 当前审计结论

```yaml
SOURCE_AUDIT: COMPLETE
DESIGN_STATUS: COMPLETE
IMPLEMENTATION: COMPLETE_LOCAL_TESTED
SIMULATION: PASSED_LOCAL_FAKE_UPSTREAM
LIVE_TEST: NOT_RUN_FOR_NEW_FEATURE
INDEPENDENT_AUDIT: PENDING
NEXT_OWNER: independent-auditor-or-release-tester
```

## 12. 本轮实际落地与本地证据

本轮已在 `BASE_COMMIT` 工作树落地以下后端能力：

- 新增 `UpstreamModelRefreshService`，默认每天 04:00（Asia/Shanghai/UTC+8）执行一次；单账号请求默认 30 秒、失败宽限 48 小时、并发上限 4。
- 使用现有 `LeaderLockCache`/Postgres advisory lock 机制做多实例单批次选主；只探测 active 且非 shadow/linked 的账号，避免重复探测同一凭证。
- 将成功的原始模型列表、正式公开 ID、正式 ID 到上游 ID 的映射、摘要和状态写入 `accounts.extra.upstream_model_refresh`；不改写 `credentials.model_mapping`。
- 上游 404/405 的不支持目录接口标记 `unsupported` 并保留旧静态行为；超时、429、5xx、非法响应和其它失败在宽限期内保留最后成功目录，过期后对可探测账号 fail closed。
- `/v1/models`、composite ownership、实际模型支持判断、Gateway Codex manifest 和 OpenAI 专用 Codex manifest 均接入同一份账号快照；快照有效时隐藏日期/preview/语义脏 ID、歧义父模型和已下线模型。
- 手工 `sync-upstream` 继续复用既有 `SyncUpstreamModelCatalog` 核心；成功后失效账号所属 group/platform 的模型列表及 composite ownership 缓存。

本地实际命令和结果：

```text
go.cmd test -count=1 -timeout=360s ./internal/service
EXIT_CODE=0

go.cmd test -count=1 -timeout=360s ./internal/handler ./internal/handler/admin ./cmd/server ./internal/config
EXIT_CODE=0

GOMAXPROCS=2; go.cmd test -p 1 -count=1 -timeout=600s ./...
EXIT_CODE=0

go.cmd test -count=1 -timeout=420s ./...
EXIT_CODE=1
CAUSE=Windows linker/runtime VirtualAlloc failed with paging-file/system-resource exhaustion; no Go assertion or compile diagnostic. The same source passed with the bounded command above after the host was overloaded by an unrelated project.

go.cmd test -count=1 -timeout=180s ./internal/handler -run TestGatewayCodexModels_DoesNotFallbackWhenRefreshSnapshotExpired
EXIT_CODE=0

go.cmd vet ./internal/service ./internal/handler ./internal/repository
EXIT_CODE=0

git diff --check
EXIT_CODE=0

gofmt -l <all changed Go files>
OUTPUT=empty
```

模拟覆盖：每日 04:00 时间计算、OpenAI `gpt-5.6` 父模型与 `luna/sol/terra` 正式名收敛、日期模型隐藏/归一化、账号路由、已下线模型过滤、快照过期、失败 stale/expired、上游 404/405 unsupported、真实 HTTP fake upstream 的 `RunOnce`、缓存失效、Codex manifest 过滤和启动 wiring。

尚未完成的验收门禁：

- 没有部署或推送本轮工作树；aiself/404token 的真实 04:00 任务和生产 Redis/数据库锁尚未验证。
- 没有用真实 GLM、DeepSeek、Claude 凭据跑线上目录和生成矩阵。
- 当前是主执行者的源码审计，不是另一位开发者的独立只读审计；因此不能把本轮状态写成 `ACCEPTED`。
- 用户原有的 `frontend/pnpm-lock.yaml` 未触碰、未纳入本任务。

本文件记录的是本轮实际实现和证据，不等同于线上发布批准。
