# 统一上游网关（aiself）前置任务记录

## 1. 原始请求与当前阶段

用户原始请求：

> 根据你分析出来的思路，出一个开发文档，并把相对应的开发前的其他前置文件，清单配齐。注意唯一边界：不要干扰到现有其他链路的正常运行。做完后审计严谨性，确认万事俱备后，交付我验收。

补充说明：

> 你先充分探测，搞清楚现在的结构后出方案。但是这次先不动代码，只详细分析。

原前置阶段已经完成设计与验收包。本轮用户已明确授权“先审计方案，再落代码、独立审计和测试”，因此任务进入受控实现阶段；统一入口仍保持未启用，除本轮明确的隔离代码与测试外，不向线上/VPS写入。

## 2. 范围

### 2.1 本阶段包含

- 对 aiself 线上运行结构进行只读探测：健康状态、API 面、Docker 运行形态、数据库表与字段、分组、账号资格、模型目录、复合路由和计费现状。
- 结合当前 sub2api 源码候选版本，核对复合模型路由、账号调度、OpenAI 兼容入口、Grok 视频异步计费和现有计费表的能力边界。
- 设计一个隔离的统一上游网关：同一个入口可以暴露多个 provider 的可用模型，但请求必须落到确定的 provider、上游模型、账号和计费价格快照。
- 设计 OpenAI 兼容文本链路、非 OpenAI provider 的协议适配边界，以及 Grok 视频异步链路的创建—完成—结算闭环。
- 设计按“实际选中的上游链路”计费的账务契约、幂等、失败和回滚规则。
- 形成开发文档、前置清单、数据/接口模板、测试与验收矩阵、隔离发布和回滚方案。
- 完成一次独立角色的只读审计，以及主控的最终一致性和安全检查。

### 2.2 明确不包含

- 不修改任何源代码、迁移文件、配置文件、Docker Compose、镜像或容器。
- 不向 aiself 或其他 VPS 写数据库，不创建/修改用户、API key、group、account、route 或 pricing profile。
- 不重启、升级、切换或删除任何线上服务。
- 不改变当前既有 API key、既有 group、既有账号调度、既有模型列表或既有计费行为。
- 不进行 GitHub push、发布新镜像或部署统一网关。
- 不为了验证新能力发起付费图片、视频或大 token 测试。
- 不在文档中记录 API key、SSH 口令、解密口令、完整凭据、cookie 或原始 credential JSON。

## 3. 硬约束（Hard Constraints）

| 编号 | 约束 | 验收方式 |
| --- | --- | --- |
| C1 | 现有链路零干扰：现有 group/account/key/route/pricing 的运行语义不变 | 实施前后既有回归矩阵、配置 diff、旧 key 冒烟对比 |
| C2 | 统一入口必须隔离：使用独立 feature gate、独立 composite group、独立 key/测试域名或路径 | 旧 key 不可自动进入新路由；新路由不可 fallback 到旧 group |
| C3 | 真实选中的 provider、upstream model、account、endpoint 和价格必须可审计 | usage log/settlement 必须保存路由与价格版本快照 |
| C4 | Grok 视频必须在创建任务时锁定账号与价格，完成回调/轮询时只结算一次 | pending billing、claim/dedup、失败/超时测试 |
| C5 | 不以“统一价格”覆盖多 provider | 账务契约要求 route/account/profile 级计价，未知价格 fail closed |
| C6 | 本阶段无线上写操作 | 探测命令清单、审计日志和变更记录中无写入动作 |
| C7 | 任何后续实施必须先有快照、备份、灰度、监控和回滚证据 | rollout checklist 全部达到放行状态 |
| C8 | 文档验收通过前不进入代码实现；本轮用户消息已作为实现授权记录 | 只允许进入受控代码阶段，不等于允许部署或启用线上统一入口 |
| C9 | 没有自动 probe 的 provider 必须使用显式、版本化、可审计的手动倍率/单位价格；不得默认 1.0、免费或沿用其他 provider | upstream-rate-policy-matrix、manual fallback 测试、fail-closed 证据 |

## 4. 工作流与角色

| 项目 | 值 |
| --- | --- |
| 工作流 | `multi-agent-dev` 自适应四角色工作流 |
| CONTRACT_REV | `v1.3.1-route-guard` |
| COMPLEXITY_GATE | `ESCALATE_REQUIRED` |
| 触发原因 | 跨 handler/service/DB schema/异步媒体/账务/生产隔离，且存在公共模型到多 provider 的路由与动态计费耦合 |
| CONTEXT_MODE | `ARMED` |
| 主控 | 当前 Codex 任务：负责探测、文档编写、整合和最终自检 |
| 思考角色 | 独立只读架构复核：挑战 provider 路由、账号选择、价格快照和 Grok 生命周期设计 |
| 执行角色 | 受控 worker：只写 `upgrade-worktree/merged-dryrun/backend` 的冻结文件范围 |
| 审计角色 | 独立只读审计最终文档，检查硬约束、证据链、缺口和误操作风险 |
| WRITE_OWNER | `MAIN_RECOVERY_AFTER_WORKER_SHUTDOWN`；执行代理未产出文件后由主控接管冻结范围 |
| SOURCE_WRITER | 主控恢复执行；执行代理 `01a08b87-5c96-7092-97e0-35931f5fbe9b` 已停止且未写入 |
| AUDIT_OWNER | 独立审计角色；主控只负责记录结果，不替代其意见 |

思考角色已返回（agent `01a08932-2c01-7dc3-ae47-56dd34f12089`）。其独立结论为：`BLOCK`，在持久化不可变 `route_price_snapshot_id`、模型目录闭包、未知价格 fail-closed、账务/用量一致性、Grok 持久状态机和三重隔离契约冻结前，不得进入开发。该结论已吸收进本包第 01、03、04、05、06、09 章。

## 5. 已确认基线

- 目标环境：aiself；公开健康入口为 `https://aiself.vip/health`，探测时返回健康状态。
- 当前 sub2api 候选源码基线：`e7118bc669e2013dbbb2fdf6ab698b5c0d33c488`。
- 候选源码工作树已有用户历史生成的四个未跟踪 tar 归档；本任务不触碰、不删除、不覆盖它们。
- 远端运行形态为 Docker Compose 兼容环境；探测时应用、PostgreSQL 和 Redis 均处于可用状态，实际镜像标签与历史文档中的标签存在漂移，必须在实施前重新取证。
- 当前 `/v1/models` 是 API key/group 作用域目录，不是全局 provider 能力目录；同一实例不同 key 可能看到不同模型。
- 当前 aiself 已存在多个独立 provider group，包括 OpenAI 兼容、Kimi、Gemini、Volcengine/Ark、Anthropic 兼容和 Grok 视频等；但当前复合路由表为空，channel 计价表为空。
- 当前 group 级模型列表、账号 `model_mapping`、账号 schedulable 状态和现有路由解析共同决定可见模型与可调度能力。
- 当前账务已经具备 group/user multiplier、usage log、billing usage entry、视频按分辨率/时长计价和 Grok pending/claim/dedup 基础，但还没有将“复合路由—实际账号—价格 profile”作为一个不可变结算快照。
- `[OBSERVED/REVIEWED]` 源码复核进一步确认：现有解析上下文和账务路径还不能证明 route/account/profile 已被同一个持久化快照绑定；现有价格缺失路径存在写入零成本/告警后继续的风险，不能直接作为统一网关的 fail-closed 方案。
- 在本任务之前已按用户授权使用现有测试 key 对 `gpt-5.5` 发起过一次最小文本冒烟：返回成功；该次记录显示 `/v1/chat/completions` 入站映射到 `/v1/responses`，实际计费约 `0.00482` 美元。该记录仅作为现状证据，不作为新统一网关验收结果；后续文档不重复记录密钥。

## 6. 关键设计结论（待用户验收）

1. 统一入口采用独立 composite group/feature gate，不修改现有 group 的默认模型目录，不接管既有 key。
2. 统一模型目录分为“公开模型名”和“真实路由候选”；公开模型名不能单独决定计费，必须解析到 provider、upstream model、endpoint、account 和价格 profile。
3. 路由选择与计费选择必须使用同一份 resolved route context；不能先按 provider 选路、再按 group 统一收费。
4. 建议新增 route-account eligibility 与 route pricing profile 概念；现有 channel/group 价格可作为兼容来源，但不能作为多账号、多 provider 动态路由的唯一价格源。
5. Grok 视频在创建阶段锁定 route/account/pricing snapshot，完成阶段沿用快照并通过现有 claim/dedup 语义只结算一次；不能在完成阶段重新随机选账号或重新取价。
6. 统一入口第一阶段优先覆盖 OpenAI-compatible 文本；图片/视频保留异步任务抽象，Grok 视频需要独立小流量验收，不与文本回归绑定。
7. 不纳入新统一池的账号必须保持原状态。例如当前 `schedulable=false` 的 Grok 账号不能因为统一入口而被隐式启用。
8. 统一入口必须有 durable `route_price_snapshot_id`；只放在内存 context、Redis pending 或 usage log 的推导字段都不足以作为结算事实。
9. 目录必须是“可执行且有价格的 route target 闭包”；不能把账号 mapping 并集直接当成可调用目录。
10. Plus、Pro、生图、KIE/直连等差异正式建模为 Billing Lane；统一 Access Group 不再承担单一统一价格。
11. lane 用户倍率、客户倍率和 provider 成本倍率分离；订阅型账号使用摊销/按次 cost model，图片和视频使用独立计量单位。
12. V1 优先复用现有 ChatGPT 子组的原生 `groups.rate_multiplier`、模型价格、图片/视频独立倍率；通过 `pricing_source_group_id` 传递到统一 route snapshot。
13. 现有 `accounts.rate_multiplier` 不作为用户收费倍率；它只进入账号/上游成本统计，除非后续明确做语义迁移。
14. 用户收费契约固定为“成功完成或成功交付才收费，失败、取消、超时、过期、无有效结果或未成功交付均不收费”；provider 已产生的内部成本与用户 charge 分账记录。
15. 每个 lane/model/endpoint 必须声明 `probe_preferred`、`manual_only` 或 `probe_only`；火山引擎/Ark 等非 ChatGPT 计费链路默认使用 `manual_only`。
16. `probe_preferred` 的 fallback 顺序为实际账号 override、账号池 override、lane 默认手动规则；probe unsupported、failed 或 stale 且没有手动规则时，在发送上游前 fail closed。
17. 手动倍率、手动单位价格和 provider-specific 计价公式不写入旧账号倍率语义；自动 probe 不覆盖手动兜底，实际采用来源和版本必须进入 route price snapshot。

## 7. 交付与放行标准

本任务只有在以下条件全部满足后，才算“前置材料齐备，可交用户验收”：

- [x] 设计文档明确当前实现可复用部分、缺口和新增边界。
- [x] aiself 观察基线有来源、时间、限制和未探测项说明。
- [x] 计费契约能覆盖文本、图片、Grok 视频、失败、重试、超时和重复回调。
- [x] 非干扰方案不依赖“先改旧配置再恢复”的高风险操作。
- [x] 实施前清单、数据模板、路由矩阵、价格矩阵、回滚清单和测试矩阵齐备。
- [x] 所有待决定项有明确推荐值、风险和验收者。
- [x] 独立审计角色已返回结论；发现项已写回并完成代码修复、定向测试和最终独立复审。
- [x] 文档安全扫描未发现 secret、私钥、口令或完整 API key。

## 8. 本轮方案审计与实现边界

主控审计结论：`PASS_WITH_CONDITIONS`。设计中的计费语义、倍率来源优先级、手动兜底和旧链路隔离没有发现方向性冲突；但当前候选源码仍缺少可证明的 durable `route_price_snapshot_id`、统一目录闭包、完整 route-account binding、Grok 创建前快照和三类账务原子恢复。因此本轮不得宣称完整统一入口已经可用。

本轮只实现并测试以下隔离能力：

- 路由级上游倍率策略解析：`probe_preferred`、`manual_only`、`probe_only`；
- probe freshness/basis 校验、账号/账号池/lane 手动兜底优先级、无有效价格时 fail closed；
- provider base price、上游倍率、下游加价、固定费、按次/图片/视频单位和 `final_user_price` 防重复计价的纯函数与测试；
- 可供后续统一入口接入的版本化 route-price snapshot 数据结构/摘要，不改变旧 group/account 计费语义；
- 受 gate 保护的 admin 管理面：聚合草稿、现有 config 编辑草稿、服务端校验/预览、显式 legacy pricing import、原子发布/禁用/恢复、版本/快照查询和 binding probe；
- 前端聚合编辑器、账号资格提示、明确 Lane/Target/Binding 预览、失败零收费试算、生命周期操作和现有 step-up 重试；
- 不启用任何真实 unified lane，不改线上配置，不把新策略接入旧入口的默认路径。

本轮 runtime 仍明确不完成且必须继续保持 gate 关闭的内容：

- durable snapshot 数据库写入及 usage/billing/media 全链路关联；
- 统一 `/v1/models` 可执行闭包、route-account runtime candidate pool 和真实余额结算；
- Grok 视频创建前快照、异步 task binding 和恢复性结算；
- 真实 aiself/404token 价格配置、灰度、部署和 GitHub push。

若执行或独立审计发现上述边界被突破，必须停止并回退到文档/测试阶段，不得以“测试通过”替代缺失的持久化证据。

## 9. 当前状态

`IMPLEMENTATION_REV5 / ADMIN_PLANE_LOCAL_TESTING / FINAL_AUDIT_PASS_WITH_CONDITIONS / PRODUCTION_GATE_DISABLED`

Revision 3 的自动 probe、手动倍率/单位价格、非 token provider、probe freshness、手动 fallback 优先级和不重复计价规则已冻结为本轮实现契约。独立思考代理在前一轮文档阶段曾对完整开发提出 BLOCK；本轮已将该结论转化为上述“核心策略先实现、完整入口继续禁用”的条件放行。代码完成后必须重新计算版本、运行测试并由独立审计角色只读复核最终版本；代码若继续改变，必须重新冻结和复审。

前端/后端管理面初次独立审计发现了 fail-closed、If-Match、step-up、生命周期、迁移部分索引、draft/publish TOCTOU、probe scope/freshness 和幂等重放 scope 等问题；主控已按回执修复并补充定向回归测试。最终前端审计和后端审计均为 `PASS_WITH_CONDITIONS`，未保留代码级安全/一致性阻塞；PostgreSQL fresh/repeat/conflict 实证仍是生产门槛。已通过的管理面测试不等于生产运行时通过；任何证据缺失都保持不部署、不启用、不宣称统一网关完成。

## 10. 本轮实现更新（Revision 4）

用户随后要求“把剩余问题全部解决，然后本地模拟新建一个统一 API，测试”。本轮在不注册生产路由的前提下补齐了可执行的隔离闭环：

- `internal/service/unified_gateway.go`：显式 route/account catalog、owner-scoped idempotency、价格快照状态机、预占/成功 capture/失败 release、内存账本和 loopback HTTP 模拟器；
- `internal/repository/unified_gateway_route_catalog_repo.go`：统一表的显式 route/account 读取，不复用会 fallback 的旧 CompositeRouteResolver；
- `internal/repository/unified_gateway_snapshot_repo.go`：同步 PostgreSQL JSON 快照仓储；
- `migrations/235_unified_gateway.sql`：新增域专用表，包含 API key/user/group scope、路由绑定和不可变快照；
- `internal/service/unified_gateway_test.go` 与 `internal/repository/unified_gateway_*_test.go`：本地 API、同名模型不同 lane、手动 provider、失败零扣费、重复请求和 SQL 仓储证据。

本轮状态为：`IMPLEMENTATION_REV2 / LOCAL_SIMULATION_PASS / PRODUCTION_GATE_DISABLED`。统一模拟器未挂到 `SetupRouter`，没有线上迁移、VPS 写入、真实 provider 请求或 GitHub push。生产放行仍需真实 provider adapter、现有认证/余额账务适配、Grok 异步任务持久化、管理端 CRUD 和一次独立的 PostgreSQL 集成验收；这些不是本地 fake upstream 通过就可以推断的事项。

## 11. 前端管理面开发阶段（2026-09-11）

用户要求继续下一步开发，并保持“先写开发文档、独立审计、再落代码、测试、全量审计”的顺序。本阶段新增 [15-frontend-unified-gateway-development.md](./15-frontend-unified-gateway-development.md)，冻结统一 API 管理前端、管理 API、独立计费字段、显式旧分组价格导入、权限/gate、版本冲突、软禁用、快照分页脱敏和验收矩阵。

基线前端 `upgrade-worktree/baseline/frontend` 已原样复制到候选 `upgrade-worktree/merged-dryrun/frontend`；官方基线未修改。候选前端已加入统一 API 页面、DTO/API、侧栏 gate、step-up、价格导入、预览和生命周期操作。

旧版前端契约的独立只读审计由 `Mill`（agent `01a08e31-483d-7ee0-96f8-d060f9cbafb5`）完成，结论为 `PASS_WITH_CONDITIONS`。随后 `Hume`（agent `01a08e2b-3688-7841-9300-e97dcb377f70`）提出了聚合资源、实际 `/api/v1` 前缀、现有 response envelope、金额精度和 endpoint 枚举等结构性修正。主控据此将第 15 章升级为 Revision 3，先不落代码。

Revision 3 由 `Epicurus`（agent `01a08e44-41fa-71c3-96a4-d81dbd0070bb`）独立只读复审，结论为 `PASS_WITH_CONDITIONS`，文档门已通过，允许进入编码。审计确认第 15 章已冻结：

- `rate_mode` 与 `rate_source` 分离；
- `currency`、`rounding_mode`、`fallback_reason`、`manual_pricing_rules`、来源版本、`probe_status` 的 rule/snapshot/migration 持久化；
- `version` 条件更新与 409、软禁用和 `ON DELETE RESTRICT`；
- 显式 group 价格导入，禁止运行时隐式读取旧 group/channel pricing；
- 管理员权限、access group 隔离、敏感字段/响应体脱敏、快照分页筛选；
- `runtime_enabled=false` 和旧链路零干扰测试证据。

同时冻结：聚合草稿→服务端 validate→原子 publish revision；`Idempotency-Key` + canonical JSON SHA-256 + `If-Match`/revision + 现有 `useStepUp` 重试；path resource 沿 config/lane/target/binding/snapshot 链路做 access-group 归属校验；provider-specific pricing schema、MeasureVector、多 attempt snapshot release/capture 和 Grok pending→最终成功结算。

阶段状态：`FRONTEND_BACKEND_IMPLEMENTED_LOCAL / FINAL_AUDIT_PASS_WITH_CONDITIONS / PRODUCTION_GATE_DISABLED`。最终独立前端审计 Agent `Newton` 与后端审计 Agent `Linnaeus` 均返回 `PASS_WITH_CONDITIONS`。候选管理 API 仍不等于生产统一运行时：没有注册 `/v1` 统一入口、没有执行 236 线上迁移、没有接入真实 provider/余额/Grok 生产结算。

## 12. 最终实现、测试与审计证据（2026-09-11）

- 后端候选：`upgrade-worktree/merged-dryrun/backend`；前端候选：`upgrade-worktree/merged-dryrun/frontend`；官方基线目录未修改；线上/VPS/GitHub 均未写入。
- 后端定向 `go test ./internal/service ./internal/repository ./internal/handler/admin ./internal/server/routes -run 'UnifiedGateway|BindUnifiedJSON|UnifiedErrorEnvelope' -count=1`：通过。
- 后端 `go vet ./internal/service ./internal/repository ./internal/handler/admin ./internal/server/routes`：通过。
- 后端新增权限收窄重放测试、schema predicate/index helper 测试：通过。
- 后端全量 `go test ./... -count=1`：候选与官方基线均受同一组既有环境/时序问题影响而非零退出（内容审核 runtime snapshot 1 秒异步等待、插件包 Windows rename 文件锁）；统一网关定向测试仍通过，故不将其标为本次回归。
- race 测试：当前环境没有可用 C 编译器，`-race` 无法执行；Docker daemon 也不可用，未伪造 PostgreSQL 集成通过证据。
- 前端 `pnpm typecheck`、`pnpm lint:check`、定向 admin API 测试（1 文件/6 tests）、`pnpm test:run`（255 文件/1863 tests）和 `pnpm build`：均通过；构建仅有既有 chunk/Browserslist 警告。
- 后端独立终审：`Linnaeus`，`PASS_WITH_CONDITIONS`；前端独立终审：`Newton`，`PASS_WITH_CONDITIONS`。条件仅为 PostgreSQL migration fresh/repeat/conflict、真实 runtime/provider/settlement/Grok 恢复性验证和 race 环境证据。
