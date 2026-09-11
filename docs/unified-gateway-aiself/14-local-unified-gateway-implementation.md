# 统一 API 本地实现与管理面验收（Revision 5）

状态：`IMPLEMENTATION_REV5 / LOCAL_ADMIN_PLANE_TESTING / FINAL_AUDIT_PASS_WITH_CONDITIONS / PRODUCTION_GATE_DISABLED`  
适用环境：aiself 候选代码树 `upgrade-worktree/merged-dryrun/backend`  
本文件记录本轮“把剩余前置闭环落成代码，并在本地创建统一 API 模拟测试”的结果。

## 1. 本轮交付边界

本轮已经落地一个与旧网关隔离的统一网关内核。它不是对旧 `/v1` 路由的替换，也没有注册到线上生产 Router；这样可以在验证价格、路由和结算语义时保持旧链路零干扰。

已实现：

- 统一 route target：公开模型、provider、上游模型、endpoint、Billing Lane、计量模式和倍率来源；
- 显式 route-account binding：实际账号由绑定对象选出，不能由公共模型名直接推导；
- lane → pool → account 规则合并，复用已有 `ResolveUnifiedRoutePrice` 的 probe、手动倍率、手动单价、非 token basis 和防重复计价约束；
- 发送上游前持久化/冻结 request-scoped price snapshot；快照同时保存实际 route selection、账号和价格摘要；
- `QUOTED → RESERVED → CAPTURED` 与失败 `RELEASED` 状态机；
- 成功才 capture，失败/未交付/缺 usage 释放预占并保持用户费用为 0；
- 以 API key、用户、访问组、request id、attempt id 共同构成幂等作用域，防止不同用户复用客户端 Idempotency-Key；
- 统一 `/v1/models` 只列出能解析出有效账号和价格的模型；
- 本地标准 HTTP 模拟器，覆盖 `/v1/models`、`/v1/chat/completions`、`/v1/images/generations` 和 `/v1/videos` 的入口分派；
- PostgreSQL raw-SQL 路由目录和价格快照仓储，以及 `235_unified_gateway.sql` 隔离表迁移；
- SQL 仓储和内存状态机单测，确保 JSON 快照、owner scope、唯一冲突和状态更新有证据。
- 隔离 admin management plane：aggregate draft、existing-config draft、server validate/preview、explicit pricing import、publish/disable/restore、revision/snapshot 查询和 step-up protected probe；
- 236 号管理迁移的 schema readiness、旧唯一约束定义删除、部分唯一索引 conflict inference 和 draft/config 双 revision CAS；
- 前端 `UnifiedGatewayView.vue`、API/DTO、metadata fail-closed、Lane/Target/Binding preview selector、账号资格提示和 lifecycle 操作。

未启用/未宣称：

- 没有修改 `SetupRouter`、`RegisterGatewayRoutes`、旧 `GatewayService`、`OpenAIGatewayService` 或旧 `usage_logs` 计费路径；
- 没有接入真实 API key 中间件、真实 provider HTTP adapter、Grok 视频创建—轮询—回调任务状态机；
- admin API 仍只服务候选管理面，未接入生产 `/v1` unified runtime、真实 provider adapter、余额结算或 Grok 异步生产状态机；
- 没有向 aiself/404token 写入数据库、配置、容器或 GitHub；
- 迁移文件虽已加入候选代码树，但尚未在任何线上数据库执行；统一入口 feature gate 仍关闭。

## 2. 代码组成

| 文件 | 用途 |
| --- | --- |
| `internal/service/unified_route_pricing.go` | 纯价格解析、probe freshness、manual fallback、charge 公式 |
| `internal/service/unified_gateway.go` | route/account 目录、快照状态机、内存账本、HTTP 模拟器 |
| `internal/service/unified_gateway_test.go` | 本地统一 API 创建、同名模型切换 Plus/Pro、按次 provider、失败不收费、HTTP 回归 |
| `internal/repository/unified_gateway_route_catalog_repo.go` | 从统一 route/account 表读取候选并按 priority 解析 |
| `internal/repository/unified_gateway_snapshot_repo.go` | PostgreSQL 快照 Create/Get/Finalize，JSON 内容不可变 |
| `internal/repository/unified_gateway_*_test.go` | SQL mock 证据：owner scope、JSON round-trip、状态更新和数据库错误传播 |
| `migrations/235_unified_gateway.sql` | 独立 route target、account binding、price snapshot 表和索引 |
| `internal/service/unified_gateway_admin.go` | 管理 DTO、校验、decimal preview、draft/publish/disable/restore、pricing import、probe scope |
| `internal/repository/unified_gateway_admin_repo.go` | 管理表 SQL、schema readiness、materialize、revision/snapshot 读写 |
| `internal/repository/unified_gateway_admin_atomic.go` | 事务内 advisory-lock 幂等、draft/config CAS、probe/import 原子写入 |
| `internal/handler/admin/unified_gateway.go` | 严格 JSON、标准 envelope、管理 API handler |
| `migrations/236_unified_gateway_admin.sql` | 管理域表、namespaced route 字段、RESTRICT 外键和 partial unique indexes |
| `frontend/src/views/admin/UnifiedGatewayView.vue` | 聚合管理 UI、价格导入、预览、生命周期和 step-up |

## 3. 本地模拟的实际流程

测试夹具在内存中创建一个访问组 `42` 和本地 key `local-key`，余额账本与真实数据库隔离。随后创建：

1. `gpt-5.5 → chatgpt-plus → openai-plus → account 101`，使用新鲜 probe 倍率；
2. 同名 `gpt-5.5 → chatgpt-pro → openai-pro → account 202`，切换后使用独立手动倍率；
3. `doubao/ark` 的 `manual_only + provider_specific/per-request` 规则；
4. 一个 fake upstream executor，用于模拟成功交付、provider 失败和 usage 返回。

每次调用先解析 route/account 和价格，再写 snapshot，之后才允许 fake upstream 执行。成功请求 capture 实际 measured units；失败请求 release 预占。重复请求从已完成 snapshot 返回，不再次调用 upstream 或扣费；不同用户即使使用同一个客户端 request id，也会因 owner scope 不同而重新建立自己的 snapshot。

## 4. 验收命令

当前环境没有宿主机 Go PATH，且 Docker daemon 不可用；本轮使用隔离目录中的 Go 1.27 runtime 和独立缓存执行本地验证：

```text
gofmt -w internal/service/unified_gateway.go internal/service/unified_gateway_test.go internal/repository/unified_gateway_*.go
go test ./internal/service ./internal/repository -run UnifiedGateway -count=1
go test ./... -count=1
go vet ./internal/service ./internal/repository ./internal/handler/admin ./internal/server/routes
```

本轮已通过：

- unified service 状态机和 HTTP 模拟测试；
- unified route catalog / snapshot repository SQL mock 测试；
- 管理面 service/repository/handler 定向测试；
- schema readiness predicate/index helper 测试；
- 后端定向 `go vet`；
- 旧 runner 对比：旧 billing、handler、usage schema/migration 文件未被修改。

全仓 `go test ./... -count=1` 已实际执行，但候选与官方基线均受既有内容审核 runtime snapshot 异步等待和插件包 Windows rename 文件锁测试影响而非零退出；该结果不被记录为本轮全仓 PASS，也没有将基线问题改写为统一网关回归。

管理面新增验收命令：

```text
go test ./internal/service ./internal/repository ./internal/handler/admin ./internal/server/routes -run 'UnifiedGateway|BindUnifiedJSON|UnifiedErrorEnvelope' -count=1
go test -race ./internal/service ./internal/repository ./internal/handler/admin ./internal/server/routes -run 'UnifiedGateway|BindUnifiedJSON|UnifiedErrorEnvelope' -count=1
go vet ./...
pnpm typecheck
pnpm lint:check
pnpm vitest run src/api/__tests__/admin.unifiedGateway.spec.ts
pnpm test:run
pnpm build
```

前端 typecheck/lint/全量测试/build 和后端定向测试/vet 的最终结果已归档；PostgreSQL forward/repeat/conflict migration 实证、race 测试和真实 runtime/provider/settlement/Grok 验证仍未具备，因此本文件不把候选代码测试写成生产放行。

## 5. 放行判定

本轮判定为：`本地管理面闭环通过 / 全量证据受基线与环境限制 / 生产 gate 保持关闭`。

理由是候选树已把配置管理和本地核心逻辑变成可重复测试的代码，但真实 provider adapter、生产 API key 认证接入、统一 runtime catalog、Grok 异步任务持久化和 PostgreSQL/现有用户余额的生产原子适配仍必须单独完成并审计。任何线上启用前都必须先补齐这些证据，不能把本地 fake upstream 或 admin API 通过结果当成 aiself 可用性结论。

特别说明：当前 `UnifiedGatewayChargeLedger` 与 `UnifiedGatewayPriceSnapshotStore` 是两个可替换端口；已实现“能确认 snapshot 仍未 captured 时退款并写入 `settlement_failed`”的安全恢复分支，但如果数据库返回的是提交状态不明的错误，仍必须由生产适配器提供同事务提交或 outbox/reconciliation 恢复，不能直接把两个独立写入当成原子账务。本地故障注入已覆盖可确认未提交分支，不证明 PostgreSQL/现有余额的最终原子恢复。
