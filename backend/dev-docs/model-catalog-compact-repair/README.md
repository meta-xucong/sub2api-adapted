# Model Catalog 与 GPT Compact 修复方案

```yaml
TASK_ID: MODEL-CATALOG-COMPACT-REPAIR-002
DOCUMENT_REV: 1.9
ANALYSIS_BASE: 3130d42a256797396f3b21b31e1dc9d83bcdff47
IMPLEMENTATION_BASE: dcfc55bb64e510d59b5ffb1e37f85fcff4ac740e
UPSTREAM_RELEASE_REFERENCE: sub2api v0.2.11 / 96f4c115c9749078f90cbf210a01d39baf3f53b6 (latest release checked 2026-10-02)
UPSTREAM_MAIN_CHECKED: 5106065716e494204fc0e8db16f68f6e9d576be0 (not merged; scoped overlap review recorded in SOURCE-AND-HISTORY.md)
SCOPE: backend catalog refresh, canonical public model IDs, Responses compact routing
IMPLEMENTATION_STATUS: LOCAL_IMPLEMENTATION_COMPLETE
LOCAL_TEST_STATUS: PASS_DEFAULT_BACKEND_SUITE
DOCUMENT_AUDIT: DESIGN_CONTENT_PASS_ROUTE_UNVERIFIED
CODE_AUDIT: PASS_FINAL_DIFF_CONTENT_ONLY_RUNTIME_UNVERIFIED
SOURCE_FIDELITY_REVIEW: PARTIAL_OVERLAY_DIGEST_ROUTE_UNVERIFIED
DEPLOYMENT_STATUS: NOT_DEPLOYED
```

## 1. 目标

修复并可验证地闭环三个彼此相关、但不能混为一谈的问题：

1. 每日刷新任务不能只留下历史日志；成功取得的上游目录必须实际驱动 `/v1/models`、Codex manifest 和请求路由。
2. 下游只公开规范模型 ID。已知内部探测名、旧别名、日期快照和歧义名不得伪装成正式模型；有证据的一对一别名可以内部映射到正式 ID，但不能泛化地删日期或后缀。
3. `/v1/responses/compact` 默认保留客户端选择的 GPT 模型。只有管理员显式配置账号 compact 映射或全局 compact override 时才改写；普通 `/v1/responses` 与 native compaction v2 不受此配置影响。native v2 对任何错误均不切换模型重试。

本方案只覆盖 Sub2API 后端与必要的后端测试、部署默认值。实现基于本地合并基线 `dcfc55b`（包含 v0.2.11 和 2026-10-01 官方 main `d6adebd`），不是当前未发布 main `5106065` 的完整合并版。当前 main 的 scoped overlap review 见来源记录；只有本地门禁和独立代码复核完成后才考虑推送/部署。

## 2. 冻结的设计决定

| ID | 决定 | 约束与理由 |
|---|---|---|
| D1 | 刷新快照是“上游观察事实”，mapping 与 group policy 是准入事实 | 两者分层；刷新不得悄悄扩大账号或用户组权限。 |
| D2 | 新模型自动加入必须由账号级 `follow_upstream` 显式开启 | 缺字段、旧数据和新建账号统一为 `manual`；显式可信来源上的空映射不自动获得 snapshot 路由。为兼容既有上游行为，未识别 OpenAI-compatible 来源与 OAuth 保留旧请求语义，但不会因后台刷新新增全局候选；自动增删仍须预览/授权 opt-in。group allowlist 仍是最终准入。 |
| D3 | group model allowlist 永远是最终授权门 | 自动目录不能绕过 allowlist。若 allowlist 开启，要用现有精确项或经过审查的通配规则一次性授权未来模型；不得把“刷新模型”隐式变成“给所有用户放权”。 |
| D4 | 目录展示 ID 与接受的输入 alias 分开治理；归一化只使用精确 ID 或版本化、可审计的 provider/source-specific alias registry | 不做通用日期剥离、`-preview`/`-exp` 删除或任意后缀猜测。官方确认的输入 alias 可不出现在模型列表，但必须规范到其唯一正式 target。 |
| D5 | `codex-auto-*` 是内部名称，不是公开目录模型 | 所有 public list、manifest、admin picker、自动发现和 follow-policy 快照都必须隐藏；不要仅因隐藏目录而改变既有明确配置/内部调用的路由语义。它不作为新的自动授权来源，账号与 group policy 仍控制请求准入。 |
| D6 | GPT compact 默认配置为空，compact-only override、fallback signal 与 model-switch retry 限定到明确 `/v1/responses/compact` | 复用旧适配中“透传所选模型”的设计。保留该 endpoint 的显式 `compact_model_mapping` 和全局 override；native compaction v2 对所有错误类别均不换模型 retry；这是行为收紧的 breaking change，需专项回归和 release note。 |
| D7 | 每日 04:00（Asia/Shanghai，UTC+8）刷新，并对漏过的 due 刷新做有界补跑 | Smart Router 与管理员即时同步共用 coordinator/per-account lock；每个账号的持久 fence 在取得刷新权时递增，快照提交要求 token 仍等于该账号最新已签发 token，不能只与最后已提交版本比较。必须把现有 calibration 的同步副作用收敛到该 coordinator，不能双抓；资源有界。 |
| D8 | 动态目录与显式 mapping 共用 canonical/source/availability resolver | `follow_upstream` 与有可信快照的 mapping 必须 list/route 一致；公开目录不拼接 static defaults。为避免破坏旧行为，manual-only/未识别来源的无映射请求可能仍按旧规则透传，但不能被描述成权威目录验证通过；此兼容例外须在验收矩阵单列。 |
| D9 | source adapter 采用 evidence tier；未验证的 provider/reseller 保持 `manual_only` | 不能仅凭 OpenAI-compatible chat API 假设 `/models` 目录完整、权威或带分页/生命周期信息。 |

## 3. 用户可验收的结果

- 最新一次可信上游目录成功后，新正式模型在已启用 follow 的账号上自动出现；还必须通过组现有 allowlist 才对该组下游可见/可调。已从权威目录确认下线的模型从该账号公开候选和路由中消失。
- 网络/认证/限流/解析失败不会清空最后成功目录；超过 stale grace 后，dynamic 模式 fail closed。
- 手工 mapping、用户组 allowlist 和 composite 权限不被后台刷新改写或绕过。
- 下游看到正式 canonical ID；上游原始 ID只留作内部路由、计费和审计字段。
- 默认 compact 请求的 `model` 与客户端所选模型一致；显式 mapping/override 才改写，并有日志/trace 明确说明改写来源。
- 普通 Responses、native compaction v2、非 OpenAI provider compact 路径均不因 GPT compact 设置改变。

## 4. 文档包

- [SOURCE-AND-HISTORY.md](SOURCE-AND-HISTORY.md)：当前代码、官方 Sub2API 与历史适配的证据边界。
- [MODEL-CATALOG-CONTRACT.md](MODEL-CATALOG-CONTRACT.md)：刷新、规范名、授权、过期和请求解析契约。
- [`fixtures/model-catalog-contract.json`](fixtures/model-catalog-contract.json)：固定 alias 和目录状态契约夹具。
- Source profile 表将 OpenAI Platform API-key 与 ChatGPT Codex OAuth manifest 分开，并要求官方 profile 与精确 origin/认证匹配；不同 profile 的模型全集可不同。DeepSeek 当前 API ID 与模型版本标签分开；Anthropic lifecycle 区分 active/legacy/deprecated/retired；GLM、MiniMax、Kimi、HY4、YeToken 在来源契约未经验证前标为 manual-only。
- 管理员可通过只读 `GET /api/v1/admin/model-catalog-refresh/status` 核对计划调度与各账号最后一次刷新，不把旧日志或列表 HTTP 200 当成刷新成功。
- [GPT-COMPACT-CONTRACT.md](GPT-COMPACT-CONTRACT.md)：compact 默认、映射优先级、协议隔离与负向测试。
- [IMPLEMENTATION-MANIFEST.md](IMPLEMENTATION-MANIFEST.md)：候选文件/符号、写入边界和移植顺序。
- [ACCEPTANCE-MATRIX.md](ACCEPTANCE-MATRIX.md)：可复现的静态、单元、集成、跨实例和受控实测验收矩阵。
- [ROLLOUT-AND-ROLLBACK.md](ROLLOUT-AND-ROLLBACK.md)：实现完成后的低磁盘发布步骤与回滚。
- [AUDIT-REPORT.md](AUDIT-REPORT.md)：文档来源审计、rev1.7 HOLD 修复与独立审查结果。
- [SHA256SUMS.txt](SHA256SUMS.txt)：对其余交付文件逐文件校验的 SHA-256 清单；清单文件本身不在清单内。

## 5. 放行规则

文档审计通过只表示设计足以进入实现，不表示生产刷新已发生或 compact 已线上通过。实现代码与本地测试已完成；独立代码内容复核针对当前最终补丁未发现 P0/P1/P2，但没有可验证的 V2 runtime route receipt。完整 v0.2.11 与适配分支逐文件 overlay/source-fidelity 对照也未完成，因此不得宣称所有历史适配均已核实吸收或移植。部署验收还必须绑定实际 commit/image digest、成功 refresh run、真实 `/v1/models` 与 compact 请求证据。

## 6. 当前实现验证状态

本地实现包含三项审计修复：Aliyun 分页总量跨页变化/重复 ID 时拒绝提交；分组 allowlist 不再全局套用 OpenAI 专属 `gpt-5.6` 归一化；compact 透传 SSE 用已有 terminal normalizer 恢复 `response.output_item.done` 中的 compaction item。管理预览应用增加真实 Postgres 集成测试，验证成功只应用一次及 stale revision 不发生部分应用。代码范围、测试和证据边界记录在 [AUDIT-REPORT.md](AUDIT-REPORT.md)。

本轮方向复核又闭合三项直接相关边界：可信完整目录确认 mapping target 缺席后，将其记入独立 `confirmed_absent` availability evidence（不伪装成模型 lifecycle），并限制到当前 source identity/normalizer；来源端点 404/405 缓存为 unsupported，停止同身份下每日无效重试但保留管理员强制探测；GPT compact 响应/compaction item 若带状态必须 completed，拒绝 HTTP 200 的 failed/in-progress 部分结果。未增加 provider、公开路由、计费逻辑或前端功能。

读取旧 snapshot 若含此前试验过的 `withdrawn/complete_catalog_negative` lifecycle 标记，会在内存中迁移到 `confirmed_absent`，保留拒绝语义但不在读取时写数据库；相应过期快照反例已单测覆盖。

已执行并通过：

| 检查 | 结果 |
|---|---|
| `go.cmd test -count=1 -timeout=360s ./internal/service` | 退出码 0（137.647s；包含 compact terminal item 遗漏回归） |
| `go.cmd test -count=1 -timeout=300s ./internal/handler` | 退出码 0（41.071s） |
| `go.cmd test -count=1 -timeout=180s ./internal/service -run TestCompleteCatalogNegativeSurvivesExpiryButIsScopedToSourceIdentity` | 退出码 0（2.642s）；最终独立 availability map 版本 |
| `go.cmd test -count=1 -timeout=180s ./internal/service -run TestAvailabilitySnapshotMigratesLegacyConfirmedAbsentLifecycleOnRead` | 退出码 0（2.728s）；旧 snapshot 过期后仍拒绝 route，读取不写回旧值 |
| `go.cmd test -tags=integration -count=1 -timeout=240s ./internal/repository -run TestUpstreamModel` | 最终代码退出码 0（12.277s）；隔离 Postgres，覆盖 policy preview 与 fence 相关 5 项集成用例 |
| `$env:PATH='C:\Program Files\Git\usr\bin;'+$env:PATH; go.cmd test -count=1 -timeout=600s ./...` | 最终代码复跑退出码 0；全仓默认测试（service 136.767s，handler 42.283s，repository 12.863s） |
| `go.cmd vet ./internal/service ./internal/handler ./internal/repository` | 退出码 0 |
| `git diff --check`、所有变更 Go 文件 `gofmt -l` | 退出码 0；格式检查无输出。diff-check 有 `.env.example` 的 LF/CRLF 转换提示 |

本轮最终代码复跑 `go.cmd test -count=1 -timeout=600s ./...` 退出码 **0**；运行测试时临时将已安装的 `C:\Program Files\Git\usr\bin` 加入进程 PATH，以满足既有 `PgDumper` 用例所需 `sh.exe`，未改项目代码。另复跑 `go vet ./internal/service ./internal/handler ./internal/repository` 退出码 **0**，并对最终代码执行 `TestUpstreamModel*` 的 5 项隔离 Postgres 集成用例，退出码 **0**。独立代码内容复核无 P0/P1/P2；但 V2 runtime route receipt 未验证，且完整 source-fidelity overlay、真实供应商/生产账号验证和 VPS 部署仍未完成，不得把本地绿灯表述为线上验收。

正式验收状态只能在所有阻断项有可复现证据后设置为 `ACCEPTED`；当前部署状态仍为 `NOT_DEPLOYED`。
