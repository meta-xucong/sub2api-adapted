# Sub2API 官方 v0.2.10 适配层升级方案

状态：`PLAN_REVISED_AFTER_INDEPENDENT_AUDIT`

日期：2026-09-30（Asia/Shanghai）

## 1. 目标与不可变边界

本次目标是把当前自有适配层从旧分支安全迁移到官方 Sub2API `v0.2.10`，保留仍然有业务价值且未被官方吸收的能力，同时删除或停止重放已由官方实现的旧补丁。

唯一代码范围是 Sub2API 仓库及其后端/前端测试。不会修改本地 Codex 安装器、CCSwitch、官方 Codex 二进制或 U 盘项目。未完成本地全量测试、受控真实测试和独立审计前，不部署 aiself、404token，也不改生产数据库。

## 2. 基线和证据

| 项目 | 固定值 |
|---|---|
| 当前已提交快照 | `13430dd5209728a4a1b5523f92ed4de267d1c745` |
| GitHub 留档分支 | `backup/pre-v0210-migration-20260930` |
| 官方升级基线 | tag `v0.2.10`，commit `2f3fed2fd` |
| 官方主线复核 | `upstream/main`，当前 `a60a29549`，版本同步为 `0.2.10` |
| 当前源码版本 | `0.1.173` |
| 当前工作树未跟踪内容 | `backend/internal/pkg/apicompat/historical_model_contract_matrix_test.go`；保留但不纳入备份和迁移提交，除非后续单独审查后明确收录 |
| 已知脏实验树 | `_worktrees/sub2api-v0210-overlay-port`；禁止清理、复用或从其中直接复制整批文件 |

官方 tag 是本次可复现基线。GitHub Releases 页面在当前抓取结果中将 `0.2.9` 显示为 Latest，但仓库已存在 `v0.2.10` tag 且 `upstream/main` 已同步到 `0.2.10`；因此本次不使用浮动 `latest`，只使用不可变 tag/commit，并在最终报告中同时记录该发布元数据差异。

## 3. 迁移原则

1. 先从官方 tag 建立干净分支，再按能力批次移植；禁止把旧自有分支整体 merge 或用旧文件覆盖官方文件。
2. 每个批次先做“官方已有/部分已有/仍缺失/应废弃”四态判断，再决定是否移植。
3. 对共享核心文件只移植最小语义差异，优先保留官方接口、错误分类、数据库迁移和测试结构。
4. 新增自有功能必须有独立开关、provider/account discriminator 或明确的默认行为，不改变官方默认路径。
5. 每个批次都要有静态检查、定向测试和回归记录；一批失败时停止后续批次，不用放宽断言或跳过测试掩盖问题。
6. 所有真实测试只使用隔离测试账号、最小请求和可追踪的测试标识；不把生产客户流量当测试证据。

## 4. 功能盘点与移植顺序

### 批次 0：官方基线和工程护栏

建立 `upgrade/v0210-overlay-migration-20260930`，固定 `v0.2.10` commit，记录 `go version`、Node/pnpm、Docker/Compose 版本和基线测试结果。先执行 `go test ./...`、前端 lint/build（若仓库脚本可用）、`git diff --check`。

不得把当前旧分支的测试文件、生成物或生产配置复制进基线。

### 批次 1：Responses/Chat/Anthropic 协议适配

候选范围：`backend/internal/pkg/apicompat/`、Responses handler/service、compact 入口及其对应测试。

先逐项核对官方是否已吸收：Responses item ID、工具参数终态、Anthropic 工具名称重写、DeepSeek reasoning 占位、流式终态和 usage 转发、`previous_response_id`/工具结果续接、幂等保护。官方已有实现不重放旧补丁；只有官方缺失的第三方模型兼容、ID 终态重协调、Claude signed thinking、结构化失败终态和 compact 契约才保留为独立最小改动。官方基线已有 `backend/internal/pkg/apicompat/` 的基础桥接、`backend/internal/service/openai_responses_item_id.go` 和 `openai_gateway_response_handling.go` 的基础 compact 处理，必须以源码/测试对照确认后再移植。

硬门槛：普通文本、单/多/并行工具、工具错误回传、五轮续接、断线回放、重复 `call_id`、流式事件序号和非流式响应必须分别有测试；`response.completed` 不得被误判为整个 Agent 任务完成。

### 批次 2：Smart Router、模型能力和 04:00 恢复

候选范围：`backend/internal/smartrouter/`、`smart_router_adapter.go`、账号调度器、健康账本、校准服务、compact/image lane 相关 SQL migration 与测试。

保留通用 exact-model、capability 分 lane、共享上游 source-group 隔离、失败冷却、04:00（Asia/Shanghai）校准、图片和 compact 独立健康状态、429 backoff。官方已经提供的 scheduler、health、stream failover 逻辑不复制；所有自有字段要做 schema/启动兼容验证，避免在 v0.2.10 新接口上重建旧类型。

硬门槛：不同模型失败互不污染、重启后账本可恢复、04:00 只探测真实支持的模型、compact 不污染普通 responses 健康、单个上游 source group 不被重复轰炸。

### 批次 3：模型目录和自动刷新

候选范围：模型同步、OpenAI model filter、管理员候选模型、定时刷新任务及测试。

保留“上游模型自动刷新”和正式模型归一化：默认 24 小时、Asia/Shanghai 每日 04:00；只向下游展示正式稳定名，带日期、旧别名、下线模型和不干净的 bare alias 进入过滤/映射，不删除管理员明确手工映射。刷新失败保留上一份有效目录并留下可审计状态。

硬门槛：同步不泄漏账号密钥、不生成错误 `gpt-5.6` 这类非正式展示名、模型列表和实际路由映射一致；离线测试覆盖新增、下线、日期模型、重复 provider、刷新失败和时区边界。

### 批次 4：图片与第三方 provider 适配

候选范围：Responses image bridge、AIAI、Volcengine/Ark、Wokey/KIE、图片超时/故障转移和相关 tests。

每个 provider 必须由明确的 URL/账号/profile 判定触发，不能凭模型名称或 prompt 猜测。保留图片 lane 的 failover、超时预算、响应为空/文本错误的非成功分类、usage 去重；已被官方吸收的上传限制、基础图片错误分类不重放。

硬门槛：文本、图片、编辑、异步视频状态不能串账；同一请求只计费一次；图片失败不伪装成功，不绕过能力检查。

### 批次 5：管理员、统一网关和运营安全适配

候选范围：统一网关管理入口、默认开关、模型选择器、operator test key guard、Veyra/门户和 SQL 示例/文档。

保留统一网关的配置和审计能力，但不得改变官方认证、权限和支付默认路径。运营探针只能使用管理员拥有的测试 key，不能让普通客户 key 承担探测流量。

硬门槛：管理面登录、统一网关默认开启、普通用户权限隔离、计费流水和报表口径不回退；前端构建产物与后端 API 版本一致。

## 5. 每批次工作流

对每个候选功能执行：

1. `git diff --no-index`/`git log -S/-G`/官方源码定位，写明四态判断和冲突原因。
2. 在官方基线分支只修改明确文件，优先 cherry-pick 可独立且无冲突的自有提交；共享文件改用手工最小移植。
3. 运行该批次的最小单测、协议夹具、race/静态检查；记录命令、退出码、提交和文件 SHA256。
4. 独立审计员只读复核：范围、默认行为、错误路径、测试证据和未验证项。
5. 审计 PASS 后才进入下一批；FAIL 必须修复并重测，不能带缺陷推进。

## 6. 最终验收矩阵

### 本地

- `go test -count=1 -timeout=900s ./...`
- 所有新增/修改 Go 文件 `gofmt -l` 为空，`git diff --check`
- apicompat、service、handler、routes、repository、smart router 定向回归
- Redis 两独立进程：幂等占位、跨实例历史续接、首响应丢失回放
- 前端 lint/build 与后端嵌入/静态资源检查
- 至少 GLM、DeepSeek、Claude 的协议夹具；并对 GLM 5.3、HY4/Preview、Kimi、MiniMax、Qwen 分别记录能力状态，不把“模型在列表中”当作可用

### 受控真实测试

只允许在本地/测试环境用 `unified-api-internal` 测试组和指定测试账号，逐模型执行：普通文本、流式、工具调用、工具结果续接、错误工具结果、五轮对话、compact（如账号支持）、断线重试和 `/v1/models`。保存脱敏请求/响应/事件、HTTP 状态、request id、response id、call/item id、usage 与流水对账。

任何真实测试失败都必须先分类为上游能力、网关转换、客户端解析、超时或账号配置；不以增加超时、自动改协议或盲目重试代替修复。

### 发布门槛

只有同时满足“全仓本地通过、独立审计 PASS、受控真实矩阵完成、账单对账一致、工作树干净、提交清单和回滚点完整”后，才可建议 GitHub 主发布或 VPS 部署。部署是后续显式动作，本方案不自动执行。

## 7. 回滚和交付

- 回滚点：`backup/pre-v0210-migration-20260930`，对应当前旧版本精确提交。
- 每个迁移批次一个提交；不 squash 掉官方基线和自有适配的来源证据。
- 最终交付必须包含：升级文档、官方基线 commit、移植提交清单、删除/跳过的旧补丁清单、测试命令/退出码、真实证据包 SHA256、独立审计结论、`WRITER_STATUS`、`AUDIT_STATUS`、`RELEASE_STATUS`。
- 任何生产部署前先做配置/数据库备份、镜像唯一 tag、健康检查和可回滚演练；本轮未授权 VPS 写入。

## 8. 独立审计修订与完整覆盖矩阵

初次审计结论为 `FAIL`，以下内容是必须执行的修订，不是可选说明。执行表拆成八批，覆盖矩阵优先于前文的五类概览。

| 批次 | 自有能力/候选文件 | 官方 v0.2.10 证据 | 决定 |
|---|---|---|---|
| 1 | Smart Router core、exact-model、capability lane、source-group、429 backoff | 官方基线没有 `backend/internal/smartrouter/core/` 及对应健康账本 | 移植；先 core，后接线 |
| 2 | Smart Router 持久化、04:00 校准、图片/compact 独立健康、`174/175_smart_router_*.sql` | 官方没有自有 ledger/recovery migration；官方 scheduler 只作为接口基线 | 移植并做 schema 前向/回滚验证 |
| 3 | 图片超时预算、空结果 failover、Responses image-generation → Images bridge、Volcengine/AIAI | 官方已有基础图片路由、URL→b64 和通用错误处理（`openai_images.go`、图片测试）；官方没有 `openai_responses_image_bridge.go` | 只移植自有差异，不覆盖官方图片核心 |
| 4 | Responses/Anthropic/Chat 差异：item ID 终态重协调、Claude signed thinking、不完整流 fail-closed、T0 幂等/续接 | 官方已有基础 item ID 清洗、工具参数 `.done`、compact HTTP/SSE、基础 bridge；证据为 `service/openai_responses_item_id.go`、`apicompat/*stream*test.go`、`openai_gateway_response_handling.go` | 对共享文件做窄合并，仅保留官方缺失增量 |
| 5 | 模型目录过滤、日期/下线模型归一化、GPT-5.6 精确别名、Codex manifest、`codex-auto-review` 映射 | 官方 `openai_codex_models_handler.go`/测试已覆盖目录展示和识别；自有 `model_filter.go` 与 SQL 模板才是差异 | 保留过滤/手工映射规则；删除旧 GPT-5.6 优先策略 |
| 6 | Fast mode、第三方 URL `service_tier` 策略、operator test key guard、部署/relay runbook | 官方已有 service-tier 基础透传和 fast 基础策略；无 `operator_test_guard.go` | 只移植第三方剥离策略、guard 和运维文档 |
| 7 | Wokey/KIE video transport、Grok reference relay | 官方已有通用 Grok media；无 Wokey/KIE profile、relay 安全边界 | 以 provider/profile 判定窄合并，保留负向测试 |
| 8 | Veyra portal、登录桥、持久化扣款/账务 overlay、Kimi/OpenCode/部署 SQL | 官方没有 Veyra bridge；官方已有 Kimi/OpenCode 基础平台和迁移 | Veyra 作为独立自有业务批次；Kimi/OpenCode 逐文件判断，不复制官方已具备部分；所有 SQL 编号先查冲突 |

### 8.1 官方已吸收与不得重复移植的增量

- Responses 基础流式生命周期、工具参数 `.done`、终态文本恢复、`sequence_number=0`：官方 `backend/internal/pkg/apicompat/anthropic_to_responses_response.go`、`chatcompletions_responses_bridge.go` 及其 stream/lifecycle tests；自有旧补丁只取未覆盖的终态 ID 重协调、signed thinking 和 fail-closed。
- Responses 输入 item ID 基础清洗：官方 `backend/internal/service/openai_responses_item_id.go` 及测试；不复制旧清洗实现。
- Compact HTTP/SSE、失败终态和 Chat fallback：官方 `backend/internal/handler/openai_gateway_response_handling.go` 及 compact tests；只复核自有 strict contract、Smart Router capability 和幂等差异。
- DeepSeek reasoning、Anthropic 通用桥接、基础图片错误/上传限制、Fast `service_tier`、Codex manifest 和通用 Grok media：官方基线已存在，按 8.1 的差异列表做窄合并。

### 8.2 证据和覆盖缺口修复

- 在第一批代码提交前生成 `docs/UPSTREAM_0.2.10_OVERLAY_AUDIT_20260930.md`，逐条列出“自有提交/文件 → 批次 → 官方文件/测试 → 保留/废弃/范围外”，每条必须带 `git show` 或测试命令证据；没有证据的项标记 `INSUFFICIENT_EVIDENCE`，不得默认为保留。
- 记录官方基线 `go version`、Node/pnpm、依赖 lockfile SHA256、`go test`/前端测试真实退出码；文档中的“待执行”不算通过。
- 数据库门槛增加：在临时 PostgreSQL/Redis 上从当前 schema 执行官方与自有 migration，验证重复启动幂等、旧数据可读、回滚点可恢复；发现 migration 编号冲突（例如旧的 221 文件与官方 221）时改用新的自有编号并更新 runner，不覆盖官方 migration。
- 权限矩阵至少覆盖匿名、普通用户、管理员、operator test key、Veyra portal 五类主体；每个受保护端点有允许/拒绝测试、状态码、审计日志和“不产生客户 usage/扣费”的断言。
- 回滚演练必须包含：停止条件、旧镜像/配置恢复、数据库备份恢复或前向兼容证明、健康检查、协议 smoke、usage/余额不变性核对；不能只依赖 Git 回退。

### 8.3 文档一致性

当前本地维护文档 `docs/CUSTOM_PATCHES.md` 引用了不存在的 `docs/UPSTREAM_0.1.173_AUDIT.md`。本方案不把该旧引用当作证据；迁移提交必须附带一个明确标记 `SUPERSEDED_NOT_RELEASE_EVIDENCE` 的桥接说明，或同步修正引用到本方案，避免后续审计误读旧文档。

## 9. 当前状态

```yaml
PLAN_STATUS: REVISED_AFTER_AUDIT_FAIL_REMEDIATION_PENDING
CURRENT_SNAPSHOT_PUSHED: true
OFFICIAL_BASELINE: v0.2.10@2f3fed2fd
OVERLAY_MIGRATION: NOT_STARTED
FULL_LOCAL_TEST: NOT_RUN_ON_MIGRATED_TREE
LIVE_TEST: NOT_RUN_ON_MIGRATED_TREE
VPS_DEPLOY: NOT_AUTHORIZED_IN_THIS_PHASE
INDEPENDENT_AUDIT: INITIAL_FAIL_REMEDIATIONS_WRITTEN_REAUDIT_REQUIRED
```
