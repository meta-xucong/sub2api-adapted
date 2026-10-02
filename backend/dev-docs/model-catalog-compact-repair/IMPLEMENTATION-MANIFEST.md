# 实施文件清单与移植顺序

## 1. 固定基线与源映射

- 历史分析定位基线：`3130d42a256797396f3b21b31e1dc9d83bcdff47`。
- 实施基线：`dcfc55bb64e510d59b5ffb1e37f85fcff4ac740e`；已包含官方 v0.2.11 release `96f4c11` 及官方 main `d6adebd`。
- 本轮已在此实施基线上形成工作树改动。下表是实际变更/测试责任映射，不再是“开工前待执行”清单。独立 Source Fidelity sidecar 仍需绑定最终冻结版本并给出 PASS；官方已存在的等价实现必须继续复用，不得复制平行实现。

源分类：`FORK_REQUIRED` = 目标基线缺少此语义或存在经测试复现的差异；`CONFIG_ONLY` = 仅默认值/部署模板变化；`UPSTREAM_ABSORBED` = 直接保留并复用官方等价路径；`DROP` = 不应移植/重复实现。

## 2. 预期变更映射

| 路径（按 3130d42 定位） | 预期变化 | 测试/审计点 |
|---|---|---|
| `backend/internal/service/upstream_models.go` | 抽出可复用的 ID-only source fetch；availability refresh 使用 `FetchUpstreamSupportedModels` 等价的 fetch + source completeness guard，不调用 `models.dev` 作为 availability source。现有 `SyncUpstreamModelCatalog` 的 Codex capability enrichment 保持独立；如果一条管理员同步请求同时刷新两种数据，必须复用同一次 fetched body，不能双 GET。 | 401/403/404/405/429/5xx、timeout、超大/非法/空响应；Models.dev 数据不能新增/移除 live IDs。 |
| `backend/internal/service/upstream_model_availability.go`（新增/恢复前需对比旧版） | Snapshot schema、source profile、canonical/input-alias/billing/raw binding、manual/follow policy resolver；不能把刷新快照当作权限。 | mapping 保护、stale/expired、cache generation；OpenAI official alias、DeepSeek retired aliases、dated official IDs 正反例。 |
| `backend/internal/service/upstream_model_refresh_service.go`（新增/恢复前需对比旧版） | 唯一 04:00 Asia/Shanghai refresh coordinator、due-only startup catch-up、共享 leader/per-account due lock、有界并发/timeout、日志与统计。 | 两实例只一轮、错过 04:00 补跑、进程取消、account/source policy。 |
| `backend/internal/service/leader_lock.go`, `backend/internal/repository/leader_lock_cache.go`, `backend/internal/repository/leader_lock_cache_test.go` | 扩展为可续租、owner-checked 的 leader/per-account lease；lock lease 不等同于 fencing；续租丢失必须取消当前 account fetch。 | 两 VPS 共用同一 Redis namespace；持锁人失联/续租失败后不可继续提交。 |
| `backend/migrations/242_upstream_model_refresh_fencing.sql`, `backend/migrations/243_upstream_model_refresh_admin_state.sql`, `backend/internal/repository/upstream_model_refresh_fence_repo.go` 与集成测试 | 已实现 per-account `latest_issued_token` / `last_applied_token` fencing、refresh run 与 admin policy preview/audit 持久化；snapshot transaction 原子更新 `accounts.extra` 和 applied token。迁移编号已核对当前分支后续序号。 | 集成用例覆盖旧 token、latest-issued token、事务回滚；本轮增加隔离 Postgres 的 policy apply single-use 与 stale-preview 整批无写入测试。完整 migration rollback 演练仍待部署前验证。 |
| `backend/internal/service/smart_router_calibration_service.go` | 将现有 `refreshOpenAIModelCatalog` 中直接 `SyncUpstreamModelCatalog` 的副作用改为调用共享 coordinator 的 `RefreshIfDue`/读取 last-good snapshot；不得保留第二套独立全量抓取。Smart Router calibration 仍独立按 enabled 开关运行。 | coordinator 与 calibration 同时触发时每账号只 fetch 一次；Smart Router off 时通用每日 refresh 仍能运行；calibration 不绕过 account policy。 |
| `backend/internal/service/upstream_model_sources.go`（新增）及 fixtures | 该文件实现各可信 source 的 endpoint、response schema、pagination 与目录 fetch；source profile 判定和 fingerprint 在 `upstream_model_availability.go`，不是另有 `upstream_model_source_profiles.go`。OpenAI Platform API-key 与 ChatGPT Codex OAuth manifest 分开；OAuth manifest schema 未证明完整前 manual-only。未验证 GLM/MiniMax/Kimi/HY4/YeToken 初始 `manual_only`。 | 不同 endpoint/schema 不共用 parser；分页截断不得提交；错误端点不清目录；reseller 不继承直营模型事实。 |
| `backend/internal/handler/admin/account_handler.go`, `backend/internal/server/routes/admin.go`, admin DTO/tests | 增加只读 status API、契约 §6 的 preview/opt-in 两步 admin API 与审计；单账号 POST sync 调用 shared `RefreshAccountNow`，与计划任务争用同一 per-account lease/fencing token，复用同一 fetched payload。不得由 mapping 为空自动 opt-in。 | R1.9a/R1.9b 权限、hash/版本防 TOCTOU、稳定顺序锁+锁后重读+持锁至 commit 的整批原子性与并发交错；CAS 为附加条件更新，SERIALIZABLE 仅可附加、不得替代锁协议；事务失败不能产生成功 audit/outbox 或 refresh enqueue；status Redis 不可用时标 unknown；无 secret/raw body；manual policy refresh 不改变准入。 |
| `backend/internal/service/gateway_service.go` | `/v1/models` availability aggregation 与请求候选使用共用 parser；动态模式禁止 synthetic static fallback。 | 目录与实际调度完全一致。 |
| `backend/internal/handler/gateway_handler.go` | `Models`/composite listing 保留权限与 cache 语义，不拼入未证明可用的 static IDs。 | 普通/composite/allowlist。 |
| `backend/internal/service/openai_codex_models_service.go` + `backend/internal/handler/openai_codex_models_handler.go` | 若 Codex manifest 另有目录路径，接入同一 canonicalization/ACL semantics；按各自 source profile、account entitlement、visibility 与 client capability 生成集合。 | 对同一请求身份分别验证各 listing 中每个可见 ID 均有合法 route；不要求 OAuth manifest 与 API-key `/v1/models` 全集相同。证明不同 entitlement 可产出不同集合，且 parser 不跨 profile 复用。 |
| `backend/internal/service/upstream_model_availability.go` | 精确的 provider/source-specific input alias rules；公开目录规范名与输入兼容 alias 分开。Lifecycle cutoff 数据与账号 availability 分开判定；不做通用日期/后缀剥除。 | `gpt-5.6` input → `gpt-5.6-sol` but hidden from listing; DeepSeek retired names → `deepseek-flash`; official `deepseek-v4-pro` stays that API ID while `DeepSeek-V4-Pro-0813` is version metadata; `gpt-6` no inferred target. |
| `backend/internal/service/unified_gateway.go`, `unified_gateway_runtime.go` | `DROP`：本轮不修改这两个独立 route/catalog/runtime 文件，不由上游目录刷新自动创建/删除统一网关路由或定价。 | Unified Gateway 的 routes/prices 由管理员显式维护；本轮验收不把 `/unified/v1/models` 与账户上游自动发现混为一个目录。 |
| `backend/internal/pkg/openai/model_filter.go` | 将 admin 可选模型、默认路由模型和内部探测名分开过滤；known OpenAI dated snapshots 不进入正式列表。 | 所有静态 fallback/manifest 确认过滤，custom source 保留精确自定义名。 |
| `backend/internal/config/config.go` | refresh 参数以及 `openai_compact_model` 默认空；校验 bounded config。 | absent/empty/override 配置。 |
| `backend/internal/service/openai_compact_fallback.go`, `openai_gateway_forward.go`, `openai_gateway_passthrough.go`, `openai_gateway_responses_chat_fallback.go` | 让 compact default-empty；分别明确 general/native compact、passthrough compact、raw-chat summary、native-v2 trigger 的契约。compact-specific config、fallback signal 和 model-switch retry 限于 `/responses/compact`；native v2 所有错误类别均不发 signal、不 retry、不换模型。Message-only classifier 使用固定短语锚定 grammar，不能保留任意子串命中；SSE fallback 保留真实 outer HTTP status，HTTP 200 的 `response.failed` 不触发 fallback。 | 上游收到的请求体 model；各路径分别测试；explicit compact 的真实 HTTP status/code/message 与 SSE 事件正反 fixture；native-v2 所有 error class stream/nonstream 负测。 |
| `backend/internal/service/openai_gateway_forward.go` 与相关 compact/billing regression tests | 不改计费算法；只验证 compact model rewrite 不改变既有 request-based billing model/usage 口径。 | 不能把“用户扣费符合 Sub2API tariff”声称为“上游供应商成本已验证”。 |
| `deploy/.env.example`, `deploy/config.example.yaml`, `deploy/docker-compose*.yml`（四份） | 清除 `gpt-5.5` 的隐式 compact fallback 默认，避免容器覆盖空 Go default。 | ripgrep 必须无默认残留；显式运维值仍可用。 |
| `backend/cmd/server/wire.go`, `wire_gen.go`; `backend/internal/service/wire.go` | 注入刷新服务并由 server lifecycle Start/Stop；仅在 generator 要求时更新生成文件。 | wire tests、正常 shutdown。 |
| `backend/internal/service/*_test.go`, `backend/internal/handler/*_test.go`, `backend/internal/config/config_test.go` | 新增契约、状态机、协议回归。 | 详见验收矩阵。 |

### 2.1 当前工作树的实际源分类（不是预期文件清单）

- `FORK_REQUIRED — runtime`: `backend/internal/config/config.go` (refresh configuration fields and bounded validation only), `backend/internal/service/upstream_models.go`, `upstream_model_availability.go`, `upstream_model_sources.go`, `upstream_model_refresh_service.go`, `upstream_model_refresh_admin.go`, `account.go`, `account_service.go`, `admin_account.go`, `gateway_service.go`, `group_model_allowlist.go`, `openai_codex_models_service.go`, `openai_compact_fallback.go`, `openai_gateway_forward.go`, `openai_gateway_passthrough.go`, `openai_gateway_response_handling.go`, `openai_models_list.go`, `smart_router_calibration_service.go`, `leader_lock.go`, `wire.go`; `backend/internal/handler/admin/account_handler.go`, `gateway_handler.go`, `openai_codex_models_handler.go`, `wire.go`; `backend/internal/repository/account_repo.go`, `group_repo.go`, `leader_lock_cache.go`, `wire.go`, `upstream_model_refresh_fence_repo.go`; `backend/internal/server/routes/admin.go`; `backend/cmd/server/wire.go`, `wire_gen.go`; migrations `242_upstream_model_refresh_fencing.sql` and `243_upstream_model_refresh_admin_state.sql`. These implement only the three stated backend scopes and their persistence/DI boundaries.
- `CONFIG_ONLY`: the default-only compact change in `backend/internal/config/config.go`, plus `deploy/.env.example`, `deploy/config.example.yaml`, and the four `deploy/docker-compose*.yml` files. They remove the implicit GPT compact model rewrite and expose bounded refresh defaults; they do not add a provider, public route, or permission.
- `FORK_REQUIRED — tests/support`: changed and added `*_test.go` files under `backend/internal/{config,handler,pkg/openai,repository,service}` plus `backend/cmd/server/wire_gen_test.go`; `backend/internal/service/account_test_service.go` is a test fake. These are regression coverage/support, not production features.
- `UPSTREAM_ABSORBED`: standard Responses/Chat handlers, upstream protocol primitives, and existing account/group authorization remain the base implementation; this patch reuses them and does not create a parallel protocol stack.
- `DROP / explicit non-goals`: no frontend, Codex installer, CCSwitch, model catalog in the Codex client, unified-gateway route/pricing, billing algorithm, or unverified GLM/MiniMax/Kimi/HY4/YeToken catalog adapter was changed. Unified routes and pricing remain manually configured; this refresh does not manufacture routes or grant group access.

`upstream_model_availability.go` is the actual reviewed alias registry and lifecycle/resolver implementation; no separate `openai_model_mapping.go` registry was added. The exact changed-path inventory remains reproducible with `git status --short` against the implementation base.

## 3. 数据持久化

账号目录 snapshot 和 `upstream_model_policy` 放在 `accounts.extra` JSONB，仍须做 key-level merge，不能覆盖整个 object。另以独立持久 fencing 表保存每账号的最高已签发 token 与最近已应用 token；签发新 token 与 snapshot 条件写都必须在 Postgres 持久化并可串行化校验，且 snapshot、`last_applied_token`、`accounts.extra` 更新在同一事务。Preview/opt-in 必须按稳定顺序锁定目标账号及所有被摘要的可变 revision 行（snapshot/profile/alias registry/group ACL），锁后重读和校验并持有到事务结束；所有修改这些 revision 的写路径必须遵循同一锁协议、同事务推进 revision。revision writer 先提交则 opt-in 必须返回 stale；opt-in 先持锁则 writer 等待到 opt-in commit 后再提交。仅 `SERIALIZABLE` 不代替这些锁。带版本条件的 policy 更新受影响行数不等于完整选中数时立即 rollback；持久 admin audit/outbox 与 policy 更新同事务，异步 enqueue 仅在 commit 后幂等消费。所有影响这些 revision 的写路径必须递增 revision。新增 migration 必须纳入 source diff、编号复核、up/down/rollback 测试；回滚程序版本时不得删除或倒退已经使用的 fencing 状态。写前先确认 v0.2.11 的 schema/update semantics。快照有 schema version、数量/长度/总字节限制；若模型目录大到 JSONB 不安全，停止并单独发起 migration 设计，不得隐式扩表。

`extra.upstream_model_policy` 由现有 Admin Account API 以白名单枚举字段（`manual`/`follow_upstream`）读写，缺省一律 `manual`；不得接受任意用户直接写账户 extra。现有账户批量 opt-in 必须提供预览与 account IDs 确认，不自动以空 mapping 判 follow。Group allowlist 沿用既有 `GroupModelAllowlist{Enabled, Models}` 与 `*` 规则，不新增绕过白名单的 group 字段。管理员若要自动接收未来模型，使用已有 wildcard 并审查授权范围。若 v0.2.11 改变 admin DTO/extra persistence/cache projection，更新本清单并重新 D/A 审计；默认不修改 frontend。

## 4. 实施与放行顺序

本轮已完成实施，当前状态以 README 与 AUDIT-REPORT 为准。最终发布顺序：

1. 冻结最终 worktree diff/hash；完成 source mapping 与独立 Source Fidelity 只读复核。
2. 跑定向、受影响 packages、后端全仓测试及文档/静态校验；保存退出码和版本绑定证据。
3. 对冻结版本做独立只读代码审计；FAIL/证据不足先修复并重跑受影响检查，再冻结新版本复审。
4. 所有本地门禁通过后，提交并推送 GitHub；在两台 VPS 先评估热更新可行性。若不能以安全、可回滚方式替换运行二进制，才用低磁盘单阶段构建/镜像发布。
5. 部署后核对两端 commit/image digest、健康状态、运行时配置、模型目录与 GPT compact 受控验收；部署证据不得与本地测试证据混称。

## 5. 禁止写入

- 不改本地 Codex 安装器、CCSwitch、`model_catalog.rs`、官方二进制。
- 不改生产 DB、账号 mapping、用户余额、钱包流水。
- GitHub 推送/VPS 部署仅在用户本轮明确授权且全部本地门禁和独立审计通过后执行；未通过或强制审计路由不可验证时停止发布，不重启服务规避失败。
- 不触碰 primary dirty checkout；实现使用经过确认的独立 worktree。
- 不顺手重构计费、协议 bridge、provider credentials、前端 UI。

## 6. 审计问题修复记录

只读代码审计指出的三项分页、allowlist source scoping、compact passthrough terminal-output 缺陷已修复并分别由回归测试覆盖；policy preview 的 PostgreSQL 持久化边界增加集成测试。当前冻结前的状态与完整命令见 [README.md](README.md) 和 [AUDIT-REPORT.md](AUDIT-REPORT.md)。固定版本独立代码审计及 source-fidelity 对照完成前，不得提交发布状态为 ACCEPTED。
