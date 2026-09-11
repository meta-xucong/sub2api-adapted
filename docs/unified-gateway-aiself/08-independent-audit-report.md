# 独立审计报告

状态：`COMPLETED / FINAL_AUDIT_PASS_WITH_CONDITIONS / HISTORICAL_REV1_BLOCK_RECORDED / PRODUCTION_GATE_DISABLED`  
独立审计角色：`Kepler`，agent `01a0894a-c75e-7da0-943c-2a1e9de2f3fe`  
工作流：`MAD_ROUTE_V1 / ESCALATE_REQUIRED / VERSION_FROZEN / v1.3.1-route-guard`  
审计方式：只读检查本目录文档/模板，并对照候选源码与 aiself 观察基线；未修改文件、代码、数据库、远程服务，也未发起付费请求。

## 1. 审计结论

`AUDIT_RESULT=BLOCKED`

设计覆盖已经达到“可以交用户验收”的文档完整度，但不能把当前材料称为“可以进入开发/上线”的绿灯。阻断原因主要是：本阶段按用户要求不写代码、不写数据库、不做真实 Grok B3 测试，因此 durable snapshot、账务原子恢复、迁移/回滚演练和旧链路回归仍只有契约与模板，没有运行证据；同时 URL、同名模型策略、Grok 来源/价格负责人、失败收费语义和测试预算需要用户冻结。

这不是现有链路故障，也不是要求本阶段改变线上配置；它是进入下一阶段前必须保留的安全门槛。

## 2. Findings

| Finding ID | 严重度 | 文件/章节 | 发现与证据 | 是否阻止进入开发 | 处置 |
| --- | --- | --- | --- | --- | --- |
| AUD-001 | P1 | `00`、`04`、本文件 | 审计已完成，但用户验收仍为 `BLOCKED`；设计包尚未取得用户对 D/Q 决策的确认。 | 是 | 保持 `P-23 BLOCKED`，由用户验收后进入下一阶段。 |
| AUD-002 | P1 | `01`、`09` | 同名模型多 provider、正式 URL、Grok 来源/价格负责人、失败收费规则、测试预算仍是提案或待确认项。 | 是 | 用户确认 D-01～D-14、Q-01～Q-06，记录责任人、版本和验收标准。 |
| AUD-003 | P1 | `01`、`05` | 三重隔离和禁止旧 group fallback 已声明；若使用“回到原实现”措辞可能造成误读。 | 是 | 已修正文案：统一入口缺任一 gate 必须直接拒绝；只有原入口请求才走旧实现。 |
| AUD-004 | P1 | `01`、`03`、`04`、`06` | `route_price_snapshot_id`、发送前落库、崩溃恢复、outbox、重启恢复仍是契约/测试项，没有执行证据。 | 是 | 实现阶段完成 schema/outbox、发送前落库失败、进程重启、Redis pending 丢失和恢复对账测试。 |
| AUD-005 | P1 | `03`、`06` | 用户 charge、usage/audit、provider/account cost 已概念分离，但原子恢复边界需要覆盖三类事实，而不只是 charge/usage。 | 是 | 已将三类事实统一纳入事务或 durable outbox 的恢复边界；实现阶段分别验证 idempotency、状态和补偿。 |
| AUD-006 | P1 | `02`、`04`、`05`、`06` | Grok 创建、无 model 的 status/content、轮询、callback、失败、重启、重复回调、改价、账号粘性均尚未真实执行。 | 是 | 在独立预算和独立 key 下完成 B3 测试并归档 task binding、snapshot、状态机、去重和对账证据。 |
| AUD-007 | P1 | `04`、`05`、`07` | runtime snapshot、能力验证、route/account catalog、价格批准、旧链路回归、迁移 forward/down、回滚演练、预算和 observability 仍为 pending/模板。 | 是 | 进入编码前补齐脱敏快照、基线、演练、预算批准和 secret scan 报告。 |
| AUD-008 | P2 | `03`、`templates/smoke-test-report.md` | 原契约要求 durable `route_price_snapshot_id`，冒烟模板此前只有 digest，不能证明发送前落库或 settlement 关联。 | 是（至少阻止验收） | 已补充 snapshot ID、持久化时点、settlement 关联和 provider/account cost recovery 字段。 |

## 3. 正向核对结果

以下项目已在设计上覆盖，但仍需未来运行证据，不能标成线上 PASS：

- 新 group、new key、feature gate 三重隔离，以及禁止旧 group fallback。
- “可执行且有价格的 route target 闭包”模型目录原则。
- 未知价格在上游调用前 fail closed。
- 用户 charge、provider/account cost、usage/audit 的语义分离。
- durable `route_price_snapshot_id`、Grok task binding、状态机和重复结算防线。
- 未执行测试明确标记为 `PENDING`/未实施；未发现把新功能测试虚报为 PASS。
- 模板、交叉链接、代码块配对、YAML 语法和文档 secret scan 已通过主控自检。

## 4. 用户验收前最小清单

1. 用户确认统一 URL、同名模型策略、Grok 来源/价格负责人、失败收费规则和测试预算。
2. 补齐 runtime snapshot、能力清单、route catalog、版本化 pricing profile。
3. 明确并验证 durable snapshot、三类账务事实原子恢复、重启恢复和幂等。
4. 完成旧链路回归、migration forward/down 演练、rollback drill 和资源隔离验证。
5. 完成 Grok 创建/查询/无 model status/content/轮询/callback/失败/重启/重复回调/改价/账号粘性测试。
6. 完成 secret scan、脱敏检查、预算停止条件和所有证据归档。

## 5. 审计边界

本报告的 `BLOCKED` 不允许被解释成“线上现有链路不可用”。它只表示新统一入口尚未获得实现和灰度放行所需证据；在用户验收和后续实施前，现有入口保持不变。

## 6. 修订后复核

复核角色：同一独立审计角色；方式仍为只读。  
结果：`REAUDIT_RESULT=PASS_WITH_REMEDIATION`

| Finding | 状态 | 复核结论 |
| --- | --- | --- |
| AUD-003 | CLOSED | `01`、`05` 已明确：统一入口缺任一 gate 直接拒绝；只有原入口请求走旧实现；禁止旧 fallback。 |
| AUD-005 | CLOSED（契约层） | `03` 已明确 user charge、usage/audit、provider/account cost 共用事务或 durable outbox 的恢复边界，并分别保留语义、幂等和补偿。 |
| AUD-008 | CLOSED | 冒烟模板已包含 `route_price_snapshot_id`、发送前持久化时点、settlement 关联和 provider/account cost recovery。 |
| AUD-009 | CLOSED | `01` 和 `06` 已同步要求三类账务事实及 settlement 的原子恢复与补偿验证。 |

此前 AUD-001、AUD-002、AUD-004、AUD-006、AUD-007 继续保持未关闭：它们要求用户决策或后续实现/灰度证据，不属于文档修订即可安全消除的项目。因而总体放行状态仍为 `BLOCKED_FOR_IMPLEMENTATION`，但本次文档修订已通过复核。

## 7. Revision 2 范围变更

后续新增的 [12-final-executable-plan.md](./12-final-executable-plan.md) 将 Plus/Pro/生图等 Billing Lane、订阅摊销、lane/user/provider 三类倍率、预占状态和动态价格展示正式化。这些内容在本报告第一次独立审计之后加入，因此不应借用 Revision 1 的审计结论直接放行。Revision 2 的主控自检已覆盖文档一致性，但进入代码前仍需要一次针对 Billing Lane/倍率/预占的独立审计。

Revision 2 的进一步澄清是：V1 优先复用现有 `pricing_source_group_id` 的原生分组价格和用户倍率；`accounts.rate_multiplier` 仅作为账号/上游成本统计，不自动改变用户扣费。该映射关系也必须纳入重新审计和 T-31～T-34 验收。

本轮用户已进一步冻结失败收费语义：成功完成或成功交付才产生用户费用；失败、取消、超时、过期、无有效结果或未成功交付均为用户费用 0。该决定已写入 `03`、`06`、`09`、`12` 和计费模板，但仍需在实现阶段以真实失败路径和账务对账证据验证，不能仅凭文档宣称通过。

## 8. Revision 3 范围变更

用户新增要求：没有自动 probe 的账号必须允许按链路手动设定倍率或单位价格，尤其是火山引擎/Ark 等非 ChatGPT 计费链路。主控已将该要求写入 `00`、`01`、`02`、`03`、`04`、`05`、`06`、`07`、`09`、`10`、`12`、`13` 及相关模板：

- 每个 lane/model/endpoint 必须选择 `probe_preferred`、`manual_only` 或 `probe_only`；
- `probe_preferred` 在 unsupported、failed 或 stale 时按 account > pool > lane 使用人工兜底；无兜底则发送前 fail closed；
- 非 token provider 必须填写手动单位价格或 provider-specific 公式，不能把 ChatGPT token 倍率当成通用规则；
- `accounts.rate_multiplier` 仍只用于内部账号/上游成本，不自动改变统一入口的用户扣费；
- snapshot 必须记录倍率来源、freshness、手动兜底状态、基础价格语义和最终价格，避免重复计算。

这些新增内容尚未由独立审计角色重新审计，也没有真实 lane、火山引擎或其他非 ChatGPT provider 的运行证据。因此总体状态继续保持 `BLOCKED_FOR_IMPLEMENTATION`；在 Revision 3 独立复核和手动价格责任人确认前，不得进入代码、迁移或部署。

## 9. 前端管理面文档审计（2026-09-11）

本节记录本阶段针对第 15 章的独立复审，不覆盖生产运行时放行。

| 项目 | 结果 |
|---|---|
| 审计 Agent | `Mill` / `01a08e31-483d-7ee0-96f8-d060f9cbafb5` |
| 审计范围 | 第 13、15 章，候选前后端类型/迁移/路由/wiring 基线 |
| 修改权限 | 只读；未编辑代码、数据库或远端服务 |
| 最终结论 | `PASS_WITH_CONDITIONS` |
| 文档门 | 已通过，允许进入编码 |
| 生产门 | 仍关闭；`runtime_enabled=false` |

审计期间先发现并修正了 `provider_specific` 被误写为 billing mode、`rate_mode/rate_source` 混用、`updated_at` 与 version 冲突、DTO 不完整、旧 group 价格导入边界不清和快照列表缺乏分页/脱敏等问题。最终版本已明确：

- `provider_specific` 是 `rate_basis`；当前 billing mode 仍使用 token/per_request/image/video；
- rule 配置使用 `rate_mode`，实际采用来源使用 `rate_source`；
- 更新只提交 `version`，`updated_at` 仅服务端返回；
- provider-specific 链路在新增 rule/snapshot/migration 字段完成前保持禁用；
- 旧 group/channel price 只能显式导入，运行时不做隐式 fallback；
- dry-run 预览不冒充持久化 snapshot，快照列表分页且不返回 response body 或凭据。

实现阶段条件仍为：完成 admin handler/service/repository/wiring、前端 DTO/页面/gate、字段迁移、CAS/409、软禁用与 RESTRICT、权限和 access group 隔离、脱敏、显式导入、成功/失败计费边界测试，并以新的独立代码审计作为最终放行依据。

## 10. Revision 3 文档复审（2026-09-11）

Revision 3 吸收了独立思考 Agent `Hume`（`01a08e2b-3688-7841-9300-e97dcb377f70`）提出的结构性意见：ModelConfig 与 RouteTarget 分离、Billing Lane/Pricing Profile 一等资源、实际 `/api/v1` 前缀、现有 `{code,message,data}` envelope、decimal 金额、真实 endpoint 枚举和聚合原子发布。

随后独立审计 Agent `Epicurus`（`01a08e44-41fa-71c3-96a4-d81dbd0070bb`）只读复审第 15 章与候选前后端，最终结论：`PASS_WITH_CONDITIONS`，允许进入编码。

本次复审确认编码前契约已补齐：

- `Idempotency-Key` 长度、canonical JSON SHA-256、同 key 重放/冲突、`If-Match` revision 和现有 `useStepUp` 重试协议；
- config/draft/lane/target/binding/snapshot 的服务端归属解析和跨 access-group 拒绝链；
- `pricing_schema_id`、MeasureVector、token/request/image/video/flat-unit adapter、多 attempt snapshot、失败 release、唯一成功 capture 和 Grok pending→最终结算；
- 明确候选树当前没有统一管理页面/API/handler/route/aggregate 实现，文档接口均为待实现项。

实现阶段条件：补充契约测试；统一 publish（产生 enabled revision）与 disable/restore 的 endpoint 语义；实现上述迁移、管理面、前端、权限、脱敏和回归证据；保持 runtime gate 关闭，旧 Composite/channel/group multiplier/旧账本零干扰。

## 11. Revision 5 最终代码复审（2026-09-11）

本节记录本轮代码落地后的两次独立只读终审。主控仅根据回执修复明确问题，没有让审计角色修改文件。

### 11.1 后端终审

| 项目 | 结果 |
|---|---|
| 审计 Agent | `Linnaeus` / `01a08e96-befd-7433-b501-1f1aafbac8d9` |
| 范围 | admin service/repository、236 迁移、wiring、权限 scope、幂等 replay、schema readiness |
| 最终结论 | `PASS_WITH_CONDITIONS` |
| 代码级阻塞 | 无 |
| 未完成生产条件 | PostgreSQL fresh/repeat/conflict migration、race、真实 runtime/provider/settlement/Grok |

审计期间关闭的高风险项：

- `CreateDraftFromConfig` 幂等重放在返回前重新校验当前 draft/access-group scope；
- `ProbeBinding` 在返回旧幂等结果前重新校验当前 scope 内的 binding 归属；
- `AllowedGroups` 非空时限制 config/draft/revision/snapshot/options/pricing source/probe/binding，并校验绑定账号属于所选 access group；空列表保留显式超级管理员 unrestricted 语义；
- `CheckSchema` 通过 `pg_index.indisunique`、`pg_get_indexdef`、`pg_get_expr` 校验索引唯一性、列顺序和精确谓词，同时校验旧唯一约束已移除及 FK `ON DELETE RESTRICT`；
- 新增权限收窄后旧 idempotency key 必须拒绝的 service 回归测试和 schema normalization 测试。

### 11.2 前端终审

| 项目 | 结果 |
|---|---|
| 审计 Agent | `Newton` / `01a08e96-bfc6-7220-b298-9561e0c649fd` |
| 范围 | UnifiedGatewayView、API/DTO、locale、router/sidebar gate、step-up、pricing import、契约测试 |
| 最终结论 | `PASS_WITH_CONDITIONS` |
| 代码级阻塞 | 无 |
| 未完成生产条件 | 页面级自动化覆盖仍可增强；后端/生产运行时条件由后端终审与生产 gate 承担 |

审计确认 pricing import 已形成预览→详情→确认应用流程，展示计费模式、倍率来源、schema、上下游倍率、最终价格、source revision 和 digest；publish/disable/restore/probe 在 step-up 重试中复用同一幂等 key；metadata、权限、migration gate 均 fail closed。

### 11.3 测试证据与限制

- 前端 `pnpm typecheck`、`pnpm lint:check`、admin API 定向测试（1 文件、6 tests）、`pnpm test:run`（255 files、1863 tests）和 `pnpm build` 均通过。
- 后端统一网关定向测试覆盖 service/repository/handler/routes，且 `go vet` 通过；新增 scope replay/schema helper 测试通过。
- 后端全量 `go test ./... -count=1` 已执行，但候选与官方基线均出现既有内容审核 runtime snapshot 异步等待和插件包 Windows rename 文件锁失败；该结果不被宣称为全量 PASS，也不归因于统一网关改动。
- race 测试因当前环境缺少 C 编译器无法执行；Docker daemon 为不可用状态，未伪造 PostgreSQL integration/migration 通过证据。

## 12. 最终审计结论

`FINAL_AUDIT_RESULT=PASS_WITH_CONDITIONS`。本轮候选管理面和本地模拟能力没有遗留已识别的代码级安全/一致性阻塞，现有旧链路保持隔离；但 `runtime_enabled=false` 必须继续保持，直到 PostgreSQL migration fresh/repeat/conflict、真实 provider adapter、durable settlement、Grok 异步恢复、race/容器化验证和灰度回滚证据完成。该结论不代表 aiself 或 404token 已部署或统一入口已可用。
