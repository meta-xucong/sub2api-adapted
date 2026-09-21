# 统一 API P0 商业化收口审计清单

> `TASK_ID=P0-UNIFIED-GATEWAY-COMMERCIAL-20260921`
> `CONTRACT_REV=P0-COMMERCIAL-20260921-v1.1`
> `STATUS=LOCAL_CODE_AUDIT_PASS_WITH_STAGING_GATES / PRODUCTION_GATE_DISABLED`
> `BASELINE=fb5aa6d6ec08992afa8c6ec47a9b1796337b40b3`

本文件是 [22-p0-commercial-readiness-development-plan.md](./22-p0-commercial-readiness-development-plan.md) 的独立审计清单。当前代码已按 v1.1 契约实施并完成本地自动化验证，但不代表 P0 已经通过；独立审计员仍必须针对固定工作树、真实 provider、账单和旧入口逐项填写证据，不能直接复制执行者结论。

## 1. 审计结论规则

- `PASS`：有绑定到最终版本的可复现证据。
- `FAIL`：发现违反需求、边界或安全契约。
- `INSUFFICIENT`：尚未验证、证据版本不匹配或环境不足。
- 任一 P0-A/B/C Blocker 为 `FAIL` 或 `INSUFFICIENT`，整体不得标记 `P0_ABC_ACCEPTED`。
- 只完成本地 fake upstream 不得替代真实 provider、账单和权限证据。
- 代码、迁移、配置、镜像或文档在审计后有变化，受影响项目必须重新审计。

## 2. 角色与版本记录

| 字段 | 实际值 |
|---|---|
| 主控/执行者 | 待实施时填写 |
| 独立审计者 | 待实施时填写；必须与执行者分离 |
| 审计版本/commit | 待填写 |
| 工作树状态 | 待填写 |
| 运行环境 | 本地 / staging / aiself / 404token（按项目实际填写） |
| 数据库 schema | 待填写 |
| 镜像 digest | 待填写 |
| 统一 Group 标识 | 脱敏填写，不写 secret |
| 审计开始/结束时间 | 待填写 |

## 3. P0 Blocker 清单

### 3.1 模型目录与生命周期（P0-A）

| ID | 检查项 | 必需证据 | 状态 |
|---|---|---|---|
| CAT-01 | `/unified/v1/models` 只返回 ready 且有实际候选的模型 | API 响应 + catalog snapshot + 版本 | `INSUFFICIENT` |
| CAT-02 | 无账号、不可调度、能力不匹配或无价格模型无法发布 | validate/publish blocker 测试 | `INSUFFICIENT` |
| CAT-03 | stale/deleted/unschedulable account 不进入候选 | 账号状态夹具 + runtime trace | `INSUFFICIENT` |
| CAT-04 | 已下架模型可以隐藏，历史 usage/snapshot 不被删除 | lifecycle/SQL/账单对照 | `INSUFFICIENT` |
| CAT-05 | 同名模型多线路显示 provider、endpoint、候选数和备用线路 | 管理页截图或 UI 测试 + API fixture | `INSUFFICIENT` |
| CAT-06 | 发布前重新检查资格，不能只依赖旧 binding 缓存 | stale state injection + publish test | `INSUFFICIENT` |
| CAT-07 | 5.4mini 等已知下架模型不出现在可用目录 | 实际模型目录 diff | `INSUFFICIENT` |
| CAT-08 | 模型 catalog 不是历史模型/账号 mapping 的简单并集 | 源数据、过滤器和结果对照 | `INSUFFICIENT` |

### 3.2 管理员体验与权限（P0-B）

| ID | 检查项 | 必需证据 | 状态 |
|---|---|---|---|
| UX-01 | 默认页面包含状态卡、可搜索模型表、配置/价格摘要和 API 使用卡 | UI 截图或组件测试 | `INSUFFICIENT` |
| UX-02 | 主流程只保留“选模型→填价格→试算→发布”；保存/校验自动完成 | Playwright/Vitest 流程证据 | `INSUFFICIENT` |
| UX-03 | 主流程只读显示统一分组、公共模型、端点和首选线路；target/binding/profile/revision 等高级字段默认隐藏 | DOM/UI 测试 | `INSUFFICIENT` |
| UX-04 | 服务端 preview 是价格唯一计算来源，前端不复制公式 | API contract + source review | `INSUFFICIENT` |
| UX-05 | blocker/warning 使用业务可读文案，并可展开技术原因 | UI fixture | `INSUFFICIENT` |
| UX-06 | publish/disable/restore 继续使用 step-up、CAS、幂等和审计 | handler/service tests | `INSUFFICIENT` |
| UX-07 | 主流程不提供自由选择 group/account/binding；管理员不能通过前端或直接 API 选择越权 group/account/binding | IDOR/权限测试 | `INSUFFICIENT` |
| UX-08 | 用户页面只需要统一 Group + API key，不出现 Provider 选择 | user/admin UI regression | `INSUFFICIENT` |
| UX-09 | 未配置价格/倍率保持空值并阻止发布，不显示 `1.0`/`1.2` 等伪造默认值 | UI fixture + source review | `INSUFFICIENT` |

### 3.3 计费与账务（P0-C）

| ID | 检查项 | 必需证据 | 状态 |
|---|---|---|---|
| BILL-01 | 命中 route/account/profile 后冻结不可变 price snapshot | snapshot row/JSON + request ID | `INSUFFICIENT` |
| BILL-02 | 同名模型不同线路使用不同价格，不能沿用统一 Group 价 | 两条 route 的成功请求和账单对账 | `INSUFFICIENT` |
| BILL-03 | 失败、取消、超时、空结果、异步最终失败用户费用为 0 | failure matrix + ledger diff | `INSUFFICIENT` |
| BILL-04 | retry 的失败 attempt 不收费，最终成功 attempt 只结算一次 | multi-attempt snapshot + idempotency | `INSUFFICIENT` |
| BILL-05 | token/image/video/per-request/provider-specific 计量互不串用 | 单位隔离测试 | `INSUFFICIENT` |
| BILL-06 | 无价格、过期 probe、basis mismatch 在发送前 fail closed | no-charge error test | `INSUFFICIENT` |
| BILL-07 | 手动倍率/单位价格版本变化不回算历史账务 | profile version fixture | `INSUFFICIENT` |
| BILL-08 | 管理员可以复算预估价和实际价，且展示来源 | preview/ledger audit screen | `INSUFFICIENT` |
| BILL-09 | provider/account 内部成本和用户 charge 分离 | dual-ledger fields/report | `INSUFFICIENT` |
| BILL-10 | snapshot/ledger 不包含原始 key、credential 或完整响应 | DB/log redaction scan | `INSUFFICIENT` |

### 3.4 暂缓项记录（不作为本轮 Blocker）

| 范围 | 本轮决策 | 状态 |
|---|---|---|
| P0-D：统一 API key 生命周期、限速、并发、额度和 key 级报表 | 不开发、不重构；继续使用现有机制 | `OUT_OF_SCOPE` |
| P0-E：健康仪表盘、告警、通知平台和新的熔断系统 | 不开发、不重构；继续使用现有运行逻辑 | `OUT_OF_SCOPE` |
| P0-F：备份、迁移演练、镜像回滚、商业授权登记和数据保留策略 | 不开发、不重构；不作为本轮放行条件 | `OUT_OF_SCOPE` |

`OUT_OF_SCOPE` 不是 `PASS`，也不是“已完成”。它表示本轮不得把这些内容加入代码、验收结论或商业承诺。

## 4. 代表性端到端矩阵

以下只测试实际准备发布的 provider；未纳入目录的 provider 不因没有测试而被视为通过。

| ID | provider/能力 | 成功证据 | 失败证据 | 状态 |
|---|---|---|---|---|
| E2E-01 | OpenAI-compatible 文本 | response + usage + route snapshot + charge | failure release | `INSUFFICIENT` |
| E2E-02 | Anthropic/Claude | adapter response + provider identity + charge | protocol mismatch no-charge | `INSUFFICIENT` |
| E2E-03 | DeepSeek/Kimi/国模 | provider/model mapping + charge | upstream rejection no-charge | `INSUFFICIENT` |
| E2E-04 | Ark/豆包（若发布） | provider-specific transform + manual pricing | unsupported field no-charge | `INSUFFICIENT` |
| E2E-05 | Image（若发布） | image unit/price + delivered result | failed generation release | `INSUFFICIENT` |
| E2E-06 | Grok video（若发布） | create→pending→poll→content→capture | timeout/final failure release | `INSUFFICIENT` |
| E2E-07 | 首选失败、备用成功 | two attempts, one final charge | failed attempt zero charge | `INSUFFICIENT` |
| E2E-08 | duplicate callback/poll | one settlement | no duplicate charge | `INSUFFICIENT` |

## 5. 非干扰回归矩阵

| ID | 检查 | 通过标准 | 状态 |
|---|---|---|---|
| REG-01 | 旧 `/v1/models` | 与基线一致，无统一目录泄漏 | `INSUFFICIENT` |
| REG-02 | 旧 `/v1/chat/completions`/Responses | 认证、响应、usage 和 charge 无非预期变化 | `INSUFFICIENT` |
| REG-03 | 旧 Group/账号/key | 配置、scope、schedulable 和额度无非预期变化 | `INSUFFICIENT` |
| REG-04 | 登录、首页、管理其他页面 | 无 404/前端路由/权限回归 | `INSUFFICIENT` |
| REG-05 | unified gate off | 新入口关闭，旧入口仍可用 | `INSUFFICIENT` |
| REG-06 | 资源隔离 | 新失败不会耗尽旧连接池/并发/Redis namespace | `INSUFFICIENT` |

## 6. 代码范围审计

实施后必须检查：

- 允许改动只在 unified service/repository/handler、统一前端页面/API/types/i18n、必要迁移、对应测试和本 P0 文档。
- `backend/internal/service/gateway_*.go`、旧 usage/billing 主流程、旧 `/v1` 路由如有改动，必须逐文件解释；无关改动直接 `FAIL`。
- 没有新增 Provider Registry、用户 provider preference、第二账本、第二模型目录或 P0-D/E/F 功能的数据表；后三条保持 `OUT_OF_SCOPE`。
- 没有以默认 `1.0`/`1.2`/统一价格代替未配置价格。
- 没有把测试 key、SSH 口令、JWT secret、数据库密码、provider token 或完整 API 响应写入仓库。
- 没有因为真实 provider 不可用而把失败断言改成跳过；只能将该 provider 从公开目录移除或标记 Beta。

## 7. 本轮本地实施与代码审计证据

以下是执行者可复现的本地证据，不替代真实 provider、staging、旧入口和账单独立验收：

| 范围 | 命令/检查 | 实际结果 | 结论 |
|---|---|---|---|
| 前端统一网关 | `pnpm vitest run src/views/admin/__tests__/UnifiedGatewayView.spec.ts` | 9/9 passed；覆盖状态卡、API 使用卡、只读主流程、首选/备用线路和逐线路空价格 | `PASS` |
| 前端类型 | `pnpm typecheck` | `vue-tsc --noEmit` 通过 | `PASS` |
| 前端全量 | `pnpm test:run` | 257 files / 1883 tests passed | `PASS` |
| 前端构建 | `pnpm build` | Vite production build 通过；仅有既有 chunk-size/dynamic-import/Browserslist warnings | `PASS` |
| 后端计价 preview | `go test ./internal/service -run 'TestUnifiedGatewayAdmin(ValidationPreviewAndFailureDoesNotCharge|ManualFlatRuleAndDeliveryState|ProfileIdentityChangesWithPrice)'` | 通过；preview 返回 provider、上游模型、计量单位、用户单价和失败 `$0` | `PASS` |
| 后端相关回归 | `go test ./internal/service ./internal/handler/admin ./internal/server/routes` | 通过 | `PASS` |
| 范围/格式 | `git diff --check`、改动文件清单 | 无 diff 错误；无旧 `/v1`、数据库 schema、Docker、VPS/GitHub 改动 | `PASS` |

主控代码审计结论：

- 默认界面现在是“状态/模型搜索 → 自动生成首选+备用线路 → 每条线路填价格 → 服务端试算 → 发布”；route/target/binding/profile/revision 只在高级区出现。
- 每条候选线路在基础区有独立的 token、按次、图片或视频价格输入；未确认的基础价、倍率、下游倍率和最终价保持空值，服务端继续 fail closed。
- 前端不复制计价公式；价格、线路、计量单位、来源版本和失败 `$0` 均来自服务端 preview。preview 是 dry-run，不冒充真实上游请求。
- 后端新增 preview 字段只返回 provider identity、上游模型、脱敏账号显示名和价格解释字段，不返回 credential、原始 key 或完整上游响应。
- 没有新增 P0-D/E/F、用户自选 provider、第二账本、第二模型目录或旧 `/v1` 改动。

仍未通过的证据边界：

- `P0-A/B/C` 表格中的真实 provider、真实账单、旧入口非干扰、PostgreSQL/部署环境和 independent reviewer 证据仍为 `INSUFFICIENT`；本地 fake/单元测试不能替代它们。
- 因此当前结论是“本地代码与测试通过、保留 staging/线上门禁”，不允许据此直接部署或宣称商业 P0 已放行。

## 8. 证据登记表

| Requirement ID | 文件/接口/命令 | 实际结果 | 版本/时间 | 审计结论 |
|---|---|---|---|---|
| 待填写 | 待填写 | 待填写 | 待填写 | `INSUFFICIENT` |

## 9. 最终放行结论模板

```text
P0_SCOPE=ABC_ONLY
P0_CONCLUSION=PASS / PASS_WITH_CONDITIONS / FAIL
P0_BLOCKERS_REMAINING=<count>
P0_EVIDENCE_VERSION=<commit/image/schema digest>
OLD_V1_REGRESSION=PASS / FAIL / INSUFFICIENT
BILLING_SUCCESS_ONLY=PASS / FAIL / INSUFFICIENT
MODEL_CATALOG_CLOSED=PASS / FAIL / INSUFFICIENT
MEDIA_STATUS=READY / BETA / HIDDEN / INSUFFICIENT
AUDIT_OWNER=<independent reviewer>
```

只有 `P0_CONCLUSION=PASS`、所有 P0-A/B/C Blocker 为 `PASS`，且 `OLD_V1_REGRESSION=PASS`、`BILLING_SUCCESS_ONLY=PASS`、`MODEL_CATALOG_CLOSED=PASS`，才允许进入小范围已有统一 key canary。P0-D/E/F 必须保持 `OUT_OF_SCOPE`，不能被伪装成已完成。
