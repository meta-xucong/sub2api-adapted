# 文档来源审计与独立审查记录

```yaml
DOCUMENT_REV: 1.9
ANALYSIS_BASE: 3130d42a256797396f3b21b31e1dc9d83bcdff47
SOURCE_CONTENT_REVIEW: PASS_REV_1_9_CONTENT_ONLY
SOURCE_FIDELITY_ROUTE: ROUTE_UNVERIFIED
INDEPENDENT_DESIGN_AUDIT: PASS_REV_1_9_CONTENT_ONLY
LOCAL_ARTIFACT_CHECKS: PASS_REV_1_9
CODE_IMPLEMENTATION: LOCAL_IMPLEMENTED
LOCAL_TEST_STATUS: PASS_DEFAULT_BACKEND_SUITE
INDEPENDENT_CODE_AUDIT: PASS_FINAL_DIFF_CONTENT_ONLY_RUNTIME_UNVERIFIED
SOURCE_FIDELITY_REVIEW: PARTIAL_OVERLAY_DIGEST_ROUTE_UNVERIFIED
LIVE_VERIFICATION: NOT_RUN
DOCUMENT_ACCEPTANCE: CONTENT_PASS_ROUTE_UNVERIFIED
```

## 1. 必须复核的结论

1. 当前 source 是否缺少每日调度，而非仅凭 VPS 历史日志推断。
2. 旧 `88745b2` 自动刷新实现和旧方案的限制是否如文档所述，尤其是 non-empty mapping 新模型不上架、错过 04:00 不补跑。
3. 当前 compact 默认是否在首个 compact 上游请求就改写 model；所有部署模板是否都包含隐式 `gpt-5.5`。
4. `2852f2f` 与 `489968f` 的版本先后及行为差异；区分“历史设计存在”和“历史线上实测成功”。
5. DeepSeek official model alias lifecycle 的原始资料是否足以支持 registry 候选；第三方 Yetoken raw route 必须仍单列为 live pending。
6. 规范名方案不会使用通用日期/preview/exp 剥离，不扩大 account/group access；public listing 与 route 使用同一规则。
7. 上游 v0.2.11 文件是否已吸收部分功能。因分析工作树当前不是 v0.2.11，实施前必须重做 source-fidelity diff。

## 2. 文档静态检查

已实际执行（PowerShell read-only checks，退出码 `0`）：

- `ConvertFrom-Json` 解析 schema v2 fixture；21 个 model cases、8 个 source profiles、6 个 surface cases、15 个 lifecycle cases、15 个 resource-limit cases（6 组 exact/plus-one 边界）、7 个 manual-policy cases、3 个 fencing cases、5 个 policy opt-in concurrency cases、3 个 billing cases、7 个 compact request cases、24 个 compact fallback cases。
- Markdown 包内相对链接缺失 `0`；文档/fixture 尾随空白 `0`；旧状态名与已修复的 OAuth 集合断言等冲突文本 `0`；HTTP 200 SSE retry 正例 `0`。
- **历史快照（rev1.9 文档审计时）**：当时工作树只有新增 `backend/dev-docs/model-catalog-compact-repair/`。当前实现阶段已新增后端代码、测试和迁移，故该快照不代表本轮最终工作树。由于文档包文件未跟踪时 Git 的普通 `git diff --check` 不覆盖它们，文档另用逐文件 whitespace checker。
- 官方 Sub2API v0.2.11 release 页面、OpenAI、Anthropic、DeepSeek、Alibaba 官方资料已在线复核；DeepSeek `GET /models` current schema/example 与 Pricing 中 model ID/version 分列也通过官方文档索引复核。关键 URL 记录在来源清单中。此检查不证明每个 provider account 的 reseller 行为。
- **历史快照（rev1.9 文档静态校验阶段）**：PowerShell 静态校验已通过，fixture JSON 可解析、`gpt-5.6` canonical alias 正例和 revision-writer-first `409 stale_preview` 正例存在；当时 Markdown 包内相对链接缺失为 0，文档/fixture 尾随空白为 0，且当时唯一变更为文档包目录。后续实现阶段新增了本报告所述后端代码、测试及迁移；此历史状态不代表当前工作树。
- `SHA256SUMS.txt` 覆盖除清单自身外的全部交付文件；文件冻结后逐个用 SHA-256 生成并可独立复算。
- 文档最终 SHA256 清单须在独立复审结束、rev1.9 内容冻结后生成；其 digest 不代替 Git commit/signature。

## 3. 审查结果

第一轮只读源审（rev 1.2）返回 `HOLD`，并指出四项 P1：OAuth/API-key listing 不应强制相同集合；compact HTTP status 与 HTTP 200 SSE 事件混淆；API-key source profile 未绑定真实 origin；Anthropic lifecycle 缺少 `legacy`。另指出 OpenAI shutdown 状态命名和 message substring matcher/fixture 覆盖问题。上述均已在 rev 1.5 中订正并加入对应夹具。

第二轮只读源审（rev 1.5）返回 `HOLD`，rev 1.6 补了 Anthropic `active` 正例与六组资源 exact/plus-one 边界；第三轮只读内容审查对 rev 1.6 给出 `PASS`，确认这些修正以及 OAuth/API-key 集合差异、compact 真实 HTTP 状态、source origin、OpenAI shutdown date、DeepSeek ID/版本边界均正确。审查者为独立子会话 `01a0f90d-e734-7693-b0fa-241381adfd9d`，但本工作台未提供 `thread/settings/updated` 运行时模型/effort 事件，也未按 `source_fidelity` 专用 runtime profile 发起，因此仅记内容审查 PASS，`SOURCE_FIDELITY_ROUTE=ROUTE_UNVERIFIED`，不是正式绑定的 `SourceFidelityReceipt`。该审查未运行项目代码、未做完整 v0.2.11 overlay diff，也未验证历史刷新实现的全部细节。

rev 1.6 的独立设计内容审计返回 `HOLD`，无 P0、五项 P1；rev 1.7 后续独立设计审计又指出 fencing latest-issued race、manual freshness 状态歧义、DeepSeek retired-model 与兼容 alias 语义冲突，以及 `gpt-5.6` fixture 未闭合 canonical account target（P2）。rev 1.8 已作针对性修正：每账号先持久签发 latest token、snapshot transaction 要求 token 与 latest-issued 相等，并加入新 token 已签但新 snapshot 未提交用例；明确 manual fresh-positive/negative/stale/expired/never/unsupported 行为状态表和 fixture；将 standalone model lifecycle 与 source-scoped alias acceptance 分离并定义 alias withdrawal；增加 `gpt-5.6` input alias 路由到账号目录 canonical target 的 fixture。rev 1.8 独立设计复审确认上述四项均修复，但指出 policy opt-in 在“最后版本校验后、批量写入前”仍有 TOCTOU 并发窗口（P1）。rev 1.9 初稿以 serializable transaction 作为锁/CAS 的等价替代，复审指出它不能保证特定的 `409 stale_preview` 结果。rev 1.9 已移除 serializable-only 选项，改为稳定顺序锁定所有目标账号和 hash 覆盖的可变 revision 行，锁后重读并持锁至提交；所有 revision 写路径使用同一锁协议。验收矩阵和夹具现分别覆盖 revision writer 先提交时 stale/整批无写入，以及 opt-in 先取得锁时写入方等待的合法线性化顺序。该轮复审仍待完成。

rev1.7 source-content review 是**范围有限的内容审查，不是完整 source-fidelity PASS**：OpenAI API-key/OAuth 和 origin、`ft:` 设计边界、Anthropic lifecycle、billing fixture 的“项目设计而非官方事实”、fencing “新增设计”措辞通过；DeepSeek 原始目录成员与 alias 事实和 compact baseline/history 在审查者当时留有核对缺口。本轮主审已用官方 DeepSeek List Models/Pricing/Updates 页面和固定 baseline 源码/本地 Git 历史补核：官方 List Models 的 schema/example 给出 `deepseek-flash` / `deepseek-v4-pro`，legacy Flash 名仍接受并按 Flash 服务/计费但不据此宣称它们在官方 `/models` 列表；review 对应修改已写入 rev1.8。Reseller 账号/旧 raw route、current vendor runtime cost、历史线上“曾通过”仍是外部/实测门禁。仍未做完整 v0.2.11 overlay diff；实施前必须执行。

由于审查子会话的 runtime provenance 不可读取，且来源审查未走专用 `source_fidelity` runtime profile，即使后续内容结论通过，本包也不能声称符合 V2 正式审计路由的 `ACCEPTED`；必须分别报告内容审查结果与 `ROUTE_UNVERIFIED` 限制。

**rev1.9 独立设计审计补记：**前文“该轮复审仍待完成”是当时状态；随后子会话 `01a0f930-f906-7363-823c-6c4a5455d4eb` 已对 rev1.9 完成聚焦复审并给出 `PASS`。其确认锁顺序、整批条件更新和 deadlock/serialization retry fixture 一致；未发现 P0/P1。该结论只审文档语义与夹具，不证明数据库实际遵守锁协议。

来源/历史内容复核子会话 `01a0f90d-e734-7693-b0fa-241381adfd9d` 对当前 rev1.9 修订后的 source facts 给出 `CONTENT PASS`：官方 v0.2.11 release/commit、compact default、OpenAI 模型 alias、DeepSeek API ID/legacy 区分正确；历史 commit 的非祖先关系已注明为时间先后而非因果；三个官方 issue 的版本/拓扑边界已写清。该审查不代表对 v0.2.11 全树和适配分支完成 overlay diff，也不验证线上账号或运行时路由。

## 4. 剩余门禁（不属于文档审计通过）

- 最后新增旧快照 read-time migration 后，最终代码全仓测试、定向回归、限定范围的 Postgres 集成测试均已复跑通过；独立代码内容复核针对该最终实现未发现 P0/P1/P2。由于工具面未提供可验证的 V2 runtime route provenance，该复核不构成正式绑定 route receipt。
- 尚未对真实 GLM、DeepSeek、Claude、GPT provider 发请求；未验证 YeToken 等第三方上游 alias 的当前接收行为。
- 未部署或修改生产配置；aiself/404token 的 scheduler/runtime config、真实 `/v1/models` 和 compact 行为仍未验证。
- 官方 v0.2.11 与适配分支的逐文件 source-fidelity diff 仍未完成，不能宣称所有历史适配均已被正确吸收/移植。
- `git ls-remote --tags https://github.com/Wei-Shaw/sub2api.git 'refs/tags/v0.2.11*'` 已只读复核 annotated tag `881a722349105e21aaa38e11de90dd05046ebe4a` peeled 到 release commit `96f4c115c9749078f90cbf210a01d39baf3f53b6`；该 tag ref 未在本地镜像检出，不影响这次 release 来源关联。上一轮临时工作树 content digest 缺少可复算命令，最终会以固定 Git commit 绑定审计版本；V2 专用 runtime route/provenance 仍不可验证。官方 v0.2.11 到适配分支的完整逐文件 overlay diff 仍未完成。因此 source-content 事实审查与正式 SourceFidelityReceipt/最终 fixed-version 路由审计必须分开报告。
- `go test -tags=integration` 本轮只运行了名字匹配 `TestUpstreamModel` 的 5 项隔离 Postgres 用例，不代表整个 build-tagged 集成套件都通过。

## 5. 本地实现与证据（2026-10-02）

### 独立审计问题闭环

本轮按只读审计的三个 P1 代码发现补齐了最小修复：

1. `internal/service/upstream_model_sources.go`：Alibaba Model Studio 分页固定首屏 total，跨页变化、重复/空 ID、超过 total 时拒绝结果；只在唯一 ID 数严格等于 total 时返回完整目录，避免不完整混合快照覆盖 last-good。
2. `internal/service/group_model_allowlist.go`：通用 allowlist 不再把 OpenAI 专属 retired alias 全局 canonicalize；仍保留平台中立的 Gemini 前缀、Claude thinking 和 OpenAI reasoning-suffix 既有规则。`gpt-5.6` 这类 account/source 专属解析留在 account resolver。
3. `internal/service/openai_gateway_passthrough.go`：仅 compact passthrough 收集同一 SSE 流的 completed output items，并在 compact terminal 校验前调用已有 `normalizeResponsesStreamingTerminalOutput`；不会把行为扩散到 Chat Completions 或普通 Responses。
4. 独立复核又发现 terminal `response.output` 非空、但遗漏一个已在 `response.output_item.done` 完成的 compaction item 时，通用 normalizer 会保留 terminal array，compact validator 因而误报失败。`internal/service/openai_gateway_response_handling.go` 现只在显式 compact 流中按 output index 补回该 completed item；native/passthrough 两条 handler 均有非空 terminal 回归测试。普通 Responses 不改变。
5. 固定版本代码复核补出的负向可用性边界已闭环：完整可信目录成功后，无法解析的显式 account mapping target 会写入独立 `confirmed_absent` availability evidence（不是 ModelLifecycle）；该记录绑定 source identity、normalizer 和 raw digest，快照正向数据过期后仍拒绝同一身份下的 target；身份/normalizer 改变不继承旧负向事实，新成功目录会自然清除旧记录。兼容读取已写入旧格式 `withdrawn/complete_catalog_negative` 的快照时会在内存中迁移到 `confirmed_absent`，不在读取时改写数据库。通配/非法 target 不进入负向快照。
6. 模型目录端点返回 HTTP 404/405 时现在分类为 `unsupported`，把 source identity、normalizer 和状态码写入 snapshot；同一身份/normalizer 下每日 scheduler 不再重复探测，并在 run status 中归入 unsupported。管理员强制刷新仍可重试；身份或 normalizer 改变后自动恢复探测。普通 401/429/5xx 仍按失败退避，不会变成 unsupported。
7. GPT compact validator 在发现可用 encrypted compaction item 前，要求若顶层 response `status` 存在则为 `completed`；若 item `status` 存在也必须为 `completed`。因此 HTTP 200 中的 failed/in-progress 外壳和部分 encrypted payload 不会被误报成功；兼容旧响应中省略 status 的行为保持不变。

最终独立代码复核（只读，最终确认旧 snapshot compatibility patch 后）无剩余 P0/P1/P2：确认旧格式负向状态只迁移解码副本、原始 `accounts.extra` 不变；同一 identity/normalizer 的 route/list guard 生效；完整成功快照重建后新出现模型清除旧负向。独立审计者未运行测试；回归由实现者单独执行。该内容审计不替代 V2 runtime route provenance 或生产环境验证。

另在 `internal/repository/upstream_model_refresh_fence_repo_test.go` 增加隔离 Postgres 用例，验证 policy preview apply 成功后只消费一次；stale account revision 使整批 apply 失败、两账号 policy 均不变、无 audit 记录、preview 未消费。现有 fence integration 覆盖 latest-issued token 与事务回滚。

### 可复现测试记录

| 命令 | 退出码/结果 |
|---|---|
| `go.cmd test -count=1 -timeout=180s ./internal/service -run TestFetchUpstreamAvailabilityModelsAlibabaRejectsPageTotalChanges` | 0 |
| `go.cmd test -count=1 -timeout=180s ./internal/service -run TestGroupModelAllowlistDoesNotApplyOpenAIInputAliasesGlobally` | 0 |
| `go.cmd test -count=1 -timeout=180s ./internal/service -run TestCompactStreamingHandlersPreserveDoneItemWhenTerminalOutputIsEmpty` | 0 |
| `go.cmd test -count=1 -timeout=180s ./internal/service -run TestCompleteCatalogNegativeSurvivesExpiryButIsScopedToSourceIdentity` | 最终独立 availability evidence 实现退出码 0（2.642s）；含实际 `ResolveMappedModel`、expired snapshot、credential/normalizer 失效、新目录恢复 |
| `go.cmd test -count=1 -timeout=180s ./internal/service -run TestAvailabilitySnapshotMigratesLegacyConfirmedAbsentLifecycleOnRead` | 退出码 0（2.728s）；旧 schema snapshot 过期后仍拒绝 route，读转换不写回 `accounts.extra` |
| `go.cmd test -count=1 -timeout=180s ./internal/service -run TestUnsupportedCatalogEndpoint` | 最新复跑退出码 0；identity/normalizer 限定自动重试，force 路径保持可用 |
| `go.cmd test -count=1 -timeout=180s ./internal/service -run TestFetchUpstreamAvailabilityModelsClassifiesMissingCatalogEndpointsAsUnsupported` | 最新复跑退出码 0；404/405 分类 |
| `go.cmd test -count=1 -timeout=180s ./internal/service -run TestValidateOpenAICompactResponseRequiresUsableCompactionItem` | 最新复跑退出码 0；失败/进行中顶层及 item 状态负例 |
| `go.cmd test -count=1 -timeout=180s ./internal/service -run TestCompactStreamingHandlers` | 0；native 与 passthrough 流式 compact fixtures |
| `go.cmd test -count=1 -timeout=360s ./internal/service` | 先前完整包复跑 0；137.647s；最终代码也由下方全仓命令覆盖（136.767s） |
| `go.cmd test -count=1 -timeout=300s ./internal/handler` | 0；41.071s |
| `go.cmd test -tags=integration -count=1 -timeout=240s ./internal/repository -run TestUpstreamModel` | 最终代码退出码 0（12.277s）；隔离 Postgres，覆盖 policy preview 与 refresh fence 相关 5 项集成用例 |
| `$env:PATH='C:\Program Files\Git\usr\bin;'+$env:PATH; go.cmd test -count=1 -timeout=600s ./...` | 最终代码全仓复跑退出码 0（service 136.767s，handler 42.283s，repository 12.863s）；Git Bash 路径仅临时加至测试进程 PATH |
| `go.cmd vet ./internal/service ./internal/handler ./internal/repository` | 0 |
| `git diff --check`；变更 Go 文件 `gofmt -l` | 0；格式检查无输出。diff-check 有 `.env.example` LF→CRLF 提示 |

全仓首次未设置 Git Bash 路径，3 个 `PgDumper` 用例因找不到 `sh.exe` 失败；检测到机器已有 `C:\Program Files\Git\usr\bin\sh.exe` 后，仅对测试进程临时扩展 PATH，三个用例和完整全仓重跑通过。未修改测试或生产源码来绕过环境问题。

以上是本地模拟/隔离数据库证据，不包含真实供应商请求或 VPS 部署。迁移 up/down 回滚演练、真实上游 refresh/compact 探测及两 VPS 同源目录核验尚未执行。

### 官方主线版本边界（2026-10-02）

官方 latest release 页面仍指向 v0.2.11 (`96f4c11`)；当前官方 main 为 `5106065716e494204fc0e8db16f68f6e9d576be0`。相较本实施基线记录的 `d6adebd`，新增 main diff 共 139 个文件；与本轮共同触及的 7 个账号/模型列表文件新增的是 TypeSafe/System One 专用能力和 composite listing 隔离。定向 diff 未发现 upstream catalog fetch/refresh 或 GPT compact 路径变更。由于这些 main 增量属于未发布且与本任务无关的功能，本轮不合并它们，也不把当前交付称为官方 main 最新版；这是有意冻结 stable release 衍生基线的范围决策，不是完整 `d6adebd..5106065` 全树 source-fidelity 证明。
