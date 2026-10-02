# 验收矩阵与可复现测试计划

所有测试结果必须绑定 commit SHA、命令、退出码、环境、skip 名单；线上请求要有时间、部署 image digest、group/account/provider source、请求 ID 和脱敏证据。不能把静态 code inspection、Mock 上游或 HTTP 200 等同于真实 provider 可用。

## 1. 自动化测试层次

| 层 | 最低覆盖 | 必须保存的证据 |
|---|---|---|
| Unit | 正式名规则、alias 唯一性、quarantine、status transition、 compact model resolve/rewrite | 测试输出、fixture revision |
| Service integration | fake upstream + fake repos/cache，刷新→snapshot→models listing→实际 route 同值 | 目录 before/after、route target、权限结果 |
| HTTP integration | `/v1/models`、Codex manifest、`/v1/responses/compact` 请求体和结构化错误 | 脱敏请求/响应与 upstream received body |
| Redis/Postgres integration | 两个独立进程争 leader、刷新快照持久化、进程重启、共享缓存失效 | 两进程 logs、DB snapshot hash、调用计数 |
| Full backend | `go test ./...`、vet、格式及 `git diff --check` | 退出码与所有 conditional skip |
| Live gated | staging/明确批准的生产小流量，实际上游列表与 compact 最小请求 | group/account/source、model/raw target、request IDs、usage/cost、停止条件 |

## 2. R1—每日刷新与可用性

| ID | 输入/故障 | 期望结果 | 阻断条件 |
|---|---|---|---|
| R1.1 | 时间冻结于上海 03:59、04:00、04:01 | 仅到计划 tick 执行；日志时区/日期正确。 | 重复或漏跑。 |
| R1.2 | 04:00 时服务停止，05:00 启动 | due-only startup catch-up 对过期账号触发有界一轮。 | 每个实例各自请求；重启风暴。 |
| R1.3 | 两实例同时 tick/start | 共享 leader lease 定期续租；每批和每账号使用统一协调；持久 per-account fencing state 与原子 snapshot 条件提交。两 VPS 连接同一 Redis lock namespace 和 Postgres fence state。 | 双倍请求、续租丢失仍继续、失租旧 worker 覆盖新快照、fence state 不持久或锁错误被当成功。 |
| R1.3a | Smart Router calibration 04:00 与 daily refresh 同时触发；或 calibration 后台独立运行 | 两者通过同一 refresh coordinator/per-account lease；每个 due account/source 一次真实目录 fetch；校准可以消费 last-good snapshot。 | 保留两套 cron 后每个上游账号被双重抓取；Smart Router 的 enabled=false 意外禁掉通用 refresh。 |
| R1.3b | 管理员即时 `sync-upstream` 与计划 refresh 同时触发同一账号 | 二者竞争同一 per-account lease/fencing token；最多一个上游 fetch；若手动操作赢得锁，scheduler 消费该新鲜结果或跳过，不能并行抓取。 | 双重上游请求、手动 sync 绕过 lock，或重复写覆盖。 |
| R1.3c | owner 暂停超过 lease TTL，另一实例获得新 lease/fence 并提交，旧 owner 随后恢复 | 旧 owner context 被取消；即便迟到写入也因旧 fence 原子拒绝。 | stale owner 覆盖新 snapshot。 |
| R1.3d | owner A 持 token n 暂停；owner B 获 lease 并先持久签发 n+1，但尚未 fetch/提交；A 随后尝试提交 | 账号 fence row 的 `latest_issued_token=n+1` 已在 B fetch 前提交，A 的 token n 不等于 latest-issued，必须拒绝；即使 `last_applied_token<n` 也不得接受 A。 | 仅以 `incoming_token > last_applied_token` 判定而误接收 A；A 趁 B 尚未写入先发布 stale snapshot。 |
| R1.4 | 目录 `{A}` 成功变 `{B}` | `follow_upstream` account 的 list/manifest 取消 A、增加 B；route A 拒绝、B 到 raw B。manual account 不自动增加 B。 | 列表和路由不同集合。 |
| R1.4a | `manual` account 得到完整权威目录，mapped target 从 canonical route set `{A}` 中消失（源目录缺席或 normalizer quarantine）；对照 manual_only/unsupported source | 有可信 fresh negative fact 时即使 `manual` 也隐藏/拒绝不可解析的 mapped target；无权威目录时只保留管理员配置并标 `manual_unverified`，不可声称 live-available。 | policy `manual` 被误解为忽略已确认的 route absence，或 unsupported 被错误标记实时可用。 |
| R1.4b | `confirmed_absent` target 经 48h stale grace；另分别改变 source identity、normalizer，并验证新完整目录重新包含该 target | 同一身份/normalizer 下即使 positive snapshot 过期，显式 target 仍拒绝；身份或 normalizer 改变使旧负向证据失效并触发刷新；新目录重新包含模型时负向事实清除。该事实写入 availability map，不伪装成 vendor lifecycle。 | 缺席仅存在于 usable snapshot 而过期后复活；旧账号负向事实串到新凭证；新上线模型无法恢复。 |
| R1.4c | 读取旧版 schema-1 snapshot，Lifecycle 含 `withdrawn/complete_catalog_negative` 且正向快照已过期 | 读取时只在内存迁移到 `confirmed_absent`；显式 route 仍拒绝，原存储值不被读操作改写。 | 旧部署写下的负向记录因升级后状态枚举变化而重新放行。 |
| R1.5 | timeout/401/429/500/invalid JSON/over-limit | last-good snapshot 不变，状态 stale；错误脱敏。 | 出错目录覆盖可信快照。 |
| R1.6 | stale 超过 48h | follow account 隐藏/拒绝自动发现模型；manual account 只保留明确管理员 mapping，列/路由状态明确为 `manual_unverified`，且 lifecycle/alias withdrawal 与新鲜权威 negative 仍是硬拒绝。 | 过期 follow 模型继续自动可调；或 manual admin mapping 被误标 fresh/official-verified。 |
| R1.6a | manual policy × fresh-positive / fresh-negative / stale-within-grace / expired / never-fetched / unsupported/manual_only | 严格匹配契约 §2.1 状态表：fresh positive 可验证；fresh complete negative 立即隐藏/拒绝但保留配置；stale grace 使用 last-good 并标 stale；expired/never/unsupported 仅显式 mapping 以 `manual_unverified` 列/路由；无 mapping 不列；retired/withdrawn 永远拒绝。 | listing 与 request resolver 状态不同；manual 被当 follow；unsupported 被冒称可用；confirmed negative 被 stale config 覆盖。 |
| R1.7 | 空数组/只有 quarantine ID | 视为刷新失败，除 provider policy 明确允许空目录；不清空上次目录。 | 模型目录意外全空。 |
| R1.8 | 404/405 不支持目录 API | `unsupported`，同身份/normalizer 下不做每日无意义请求；显式 admin force refresh 仍可探测；显式 mapping 按契约标 `manual_unverified`，无 mapping 不列出；绝不声称实时 available。身份/normalizer 变化允许自动重探。 | 404 被误当权威空目录，重复日探测无效端点，或静态默认冒充实时 availability。 |
| R1.8a | 分页正常结束、重复 cursor；单页/累计 body 恰好 8 MiB 与 8 MiB+1；100 页与第 101 页；2,048 IDs 与 2,049；UTF-8 ID 256 bytes 与 257; snapshot JSON 2 MiB 与 2 MiB+1 | 恰好达到每项上限且响应完整时允许原子提交；超 1、循环 cursor、或 `has_more=true` 到达页数上限时保留 last-good，标 `incomplete/over_limit`。 | 边界内误拒、超限接受、截断目录提交成功或造成模型下线。 |
| R1.9 | 新账号、既有账号、policy 字段缺失；有/无 mapping | 所有情况默认 `manual`，与 mapping 是否为空无关；只有显式 opt-in 的 `follow_upstream` 账号自动增删模型。mapping 内容/hash不被刷新修改。 | 老账号无意自动扩大访问、回滚时策略改变。 |
| R1.9a | 非 admin 调 policy API；admin preview→opt-in；preview 前后账号/profile/snapshot/group ACL/alias registry revision 改变；100/101 accounts；非法/重复 IDs；选中集存在一项不 eligible | 普通 API key 返回拒绝且无状态变化；preview 不发 upstream 请求并返回精确影响；opt-in 仅对同一未过期 hash 的完整集合原子执行；过期/版本变化返回 `409 stale_preview`；>100、重复、非法、含 ineligible account 整批拒绝；写入 admin audit 且不改 mapping/group/Unified route/billing。 | 跳过显式授权、TOCTOU、部分成功、越权或密钥/URL 泄漏。 |
| R1.9b | 两种锁顺序交错：revision writer 先锁住相关 revision 行并修改提交，opt-in 随后取得锁；以及 opt-in 先锁住全部行、revision writer 等待 | 前者 opt-in 锁后重读到版本变化，整批回滚返回 `409 stale_preview`；后者明确定义为 opt-in 先线性化提交，revision writer 只能随后提交。两者都不得存在锁外“最后校验→policy 写入”窗口；死锁重试必须重新取得全部锁并重新校验原 preview。 | 仅用普通读后写接受过期授权；声称 `SERIALIZABLE` 本身必然返回 409；部分账号写入；事务失败仍留下成功审计/任务；复用旧读集。 |
| R1.10 | group allowlist 开/关、exact/wildcard、input alias 与 account follow mode | alias 先 canonicalize 再检查 allowlist；未获授权的 canonical ID 既不列出也不能请求；未来模型只被已审查 wildcard 命名空间准入。 | alias 绕过 allowlist、目录任务绕过组权限或扩大到未批准 provider。 |
| R1.11 | cache/shared cache 多实例 | 新 snapshot generation 在所有实例及时失效；不等待不可接受的旧 TTL。 | 任一实例还列旧集合/路由旧 owner。 |
| R1.12 | 两 VPS，同 provider source fingerprint 且同 entitlement | canonical set digest 一致；不相同则输出可归因的账号权限/refresh 诊断，不从一台复制另一台结果。不同套餐不强制一致。 | 同源同权限差异被静默忽略或用错误来源覆盖。 |

## 3. R2—正式名、别名和下线模型

使用 `fixtures/model-catalog-contract.json`：

- 已注册精确正式 ID：list 和 route 一致。
- 有证据且唯一的日期/experimental alias：只公开 canonical ID，route 到 raw ID；raw ID 不泄漏到 client/manifest。
- 多日期候选、两个语义变体、别名多个 target、未知 preview/exp、无 registry 依据：quarantine 且不能通过猜测请求。
- 日期/实验性 ID 按 source policy 分开验收：已知 OpenAI dated snapshots 不自动公开/不映射到 stable ID；DeepSeek、Anthropic、Alibaba 与自定义 provider 的正式日期 ID 不做通用剥除；命中精确受审 alias 时仅按该 provider 规则 canonicalize。OpenAI stable alias、dated suppression、非 OpenAI dated exact preservation 均须有独立正反用例。
- `gpt-5.6`：目录不单列 alias；输入接受并规范到官方 alias target `gpt-5.6-sol`，不得拒绝或归到 Terra/Luna；测试覆盖 list 与 direct request 的差异。
- `gpt-5.6-sol/terra/luna` 与 `gpt-6-astra/sol/luna/6.1-sol` 作为各自精确官方 IDs 分开路由，不能互相替换；`gpt-6` bare ID 无已核实 alias 证据时隐藏并拒绝，不能推为 Astra。静态 table 只对照，不代表某个 reseller account 当前可用。
- `codex-auto-*`: 不在 public default、`/v1/models`、manifest、admin normal picker 或自动发现 snapshot；目录过滤不得全局破坏已有显式/内部路由，实际准入仍由账号配置与 group policy 控制。
- DeepSeek `deepseek-v4-flash-vision-exp`: canonical name 归并只在具体上游 adapter registry 生效；模拟 upstream 断言正式 public ID映射到 raw ID；live YeToken acceptance remains separate.
- DeepSeek 的 retired standalone model lifecycle 与 `accepted_input_alias` 状态分开：官方旧名仍在兼容期时输入 alias → `deepseek-flash`；官方账号按当前目录 canonical target 路由，不假设 `/models` 返回 retired raw ID。Reseller 只有在其精确账号 snapshot 含该 raw ID且 source-specific alias route 经验证时才可内部路由 raw alias；一旦官方/该 source 撤销兼容，旧 alias 输入立刻拒绝，canonical ID 仍只在该账号目录实际提供时可调。
- DeepSeek `deepseek-v4-pro` API ID 必须保持精确；`DeepSeek-V4-Pro-0813` 只作为 model-version metadata，不得改写 public ID、upstream route ID 或 billing model ID。
- Official OpenAI `ft:` account-specific fine-tuned IDs are not auto-published from an official catalog; they may be made available only via explicit admin mapping/alias and continue to route to the exact raw ID. Do not delete or transform that provider-owned ID.
- A retired standalone model ID is never routed as a canonical model. A still-accepted compatibility input alias is a separate, source-scoped contract: it may route to an available canonical target, or to a raw alias ID only when that exact account snapshot contains it and a current alias-route probe is verified; withdrawal rejects the old input alias. A different fresh account may still contribute the canonical model through normal group-union rules.
- Anthropic lifecycle registry 覆盖 `active`、`legacy`（仍可调用）、`deprecated`（仍可调用且有退休日期）、`retired`（禁止该 source route）；partner-operated platform 使用自身 lifecycle，不把 Anthropic API 状态外推。
- Source profile 契约：OpenAI/DeepSeek 官方、Alibaba Model Studio、Anthropic 使用各自文档中的 pagination/region/lifecycle 语义；未有完整可信端点契约的 GLM/MiniMax/Kimi/HY4/YeToken 初始 `manual_only`，绝不伪造 `/models` adapter。
- 带日期 ID：按 provider-specific policy 验收。OpenAI 已知 snapshots 用正式 OpenAI filter 隐藏且不改路由；DeepSeek/Anthropic/Alibaba/custom-source 日期 ID 按各自精确 ID 规则保留；只有明确注册的 source-specific alias 才可归并。通用 strip-date 以不同 model family 和日期正式 ID 反例拒绝。
- 已 retired model 在成功 authoritative refresh 中缺席：该账号立即停止列出/路由；若别的账号仍支持，同 group union 可继续列出但 scheduler 只能选有 fresh route 的账号。
- unknown future ID policy test: DeepSeek/Anthropic/Alibaba 等 profile 中通过 schema/完整性校验的未知精确 ID（包括日期/preview/exp 形式）按该 source policy 保留，除非命中 exact lifecycle/alias/internal-ID 规则；OpenAI 专属 snapshot/deprecation filter 只作用于 OpenAI source。未验证 reseller/source 不可按名称猜测，必须 manual-only/quarantine 并提供审查告警。

## 4. R3—Gateway surface agreement

对每一组测试账号计算集合 `C`，比较：

```text
GET /v1/models
GET Codex models manifest (若此 group/client 使用)
composite models result
is-model-supported / account candidate set
实际 dispatch 可到达的 canonical IDs
```

对每个 listing surface，所有可见 ID 必须至少有一个满足该 surface 的 source profile、account entitlement、ACL、freshness 和 route binding 的账号；所有可路由 ID 必须属于对应 group 的公开/allowlist 契约。不同 credentials/source profile 的 OAuth manifest 与 API-key `/v1/models` 集合允许不同，不能跨 profile 比较并强制相等；应证明它们共享 canonicalization/authorization semantics、不会复用错误 parser，且各自可见 ID 均可正确 route。按账户失效、快照过期、账号禁用、alias 歧义、同名多 provider、cache stale 各跑一次。无任何可获准路由 ID 时 `/v1/models`/manifest 返回合法空集合，不得回填静态 `DefaultModels`。

另独立测试 `GET /unified/v1/models`：结果必须完全来自显式 unified route/billing lane 配置；upstream refresh 不得创建新 unified model 或 price。显式 route 绑定到已失效账号能力时须按 runtime capability contract 排除或返回明确状态，不可声称 refresh 已把它变成可售 route。

## 5. R4—GPT compact

| ID | 请求/响应 | 期望 |
|---|---|---|
| R4.1 | 配置未提供，全局默认启动 | resolved config 为空；无 Compose/env 隐式 gpt5.5。 |
| R4.2 | `/v1/responses/compact` native/general path，model=`gpt-5.6-sol`，无账号 mapping/global override | fake upstream 收到 `gpt-5.6-sol`。另对 Terra/Luna 同测。 |
| R4.3 | native/general path account `compact_model_mapping` 匹配 | upstream 收到映射目标；仅 legacy compact 生效，日志写明 account mapping。 |
| R4.4 | native/general path account mapping 不匹配、显式 global override=`gpt-5.5` | global override 生效且可从 trace 判断；移除配置恢复透传。 |
| R4.4a | 第三方 API-key 走 raw Chat summary compact branch，compact mapping 和 global override 都设置 | 上游走 Chat Completions；仅普通模型 mapping 生效，compact-specific mapping/global override 均不应用；返回仍是完整 compact Responses item。 |
| R4.4b | `/responses/compact` passthrough account，分别配置 account compact mapping/global override | 上游首发 model 按 mapping > global > empty/pass-through 优先级；原始 Responses body/header 契约保留。 |
| R4.5 | 普通 `/v1/responses` 与 native v2 compaction 首发 | 请求 model/header/body 未被 legacy compact config 改写。 |
| R4.5a | native v2 stream/nonstream：`context_length_exceeded`、`model_not_found`、`unsupported_model`、窄 message-only availability、无 error 的 failed shell、其他业务错误 | 全部不产生 compact fallback signal、不 retry、不换模型；保留协议终态/错误，compact account/global config 均不可见。更新所有既有 v2 model-switch 预期。 |
| R4.6 | GPT 5.5 old config/settings | 只有显式 override/mapping 时行为与旧配置一致，验证兼容升级路径。 |
| R4.7 | 对 `/v1/responses/compact` 覆盖 status/code/message：400/404 structured model error；当前窄 message-only availability 短语；空 failed shell；context-window；invalid request；“model output is not supported”；429/5xx（body 含 context/window/model）；timeout/quota/policy | 仅 400/404 + 精确 structured model error、已列窄 message-only 规则或严格空 failed shell 最多同账号 fallback 一次；context-window 与其他错误不 fallback。native v2 对所有上述类别均不 retry。 |
| R4.7a | 区分真实 HTTP response 与 SSE event：非流式 HTTP 400/404；外层 HTTP 200 内 `response.failed`（含 model error code、无 code、空 shell）；message phrase 锚定正例/包含相似短语的负例 | 只有真实 transport HTTP 400/404 才允许 compact model fallback；HTTP 200 SSE event 不合成 400、不触发 fallback。matcher 不可用任意子串匹配；上下文句 `request says model not found is not the failure` 不得误触发。 |
| R4.8 | 上游先发任何下游 event 后出错 | 不重试；不伪造 success terminal。 |
| R4.9 | compact 返回 compaction item / 普通 text / missing item / truncated SSE / `status=failed|in_progress` 或 compaction item 非 completed（含部分 encrypted payload） | 若 response/item 带 status，只接受 completed；只有完整合法 compact result 才成功；其它有结构化错误且不作为成功 usage。 |
| R4.10 | `gpt-5.6` alias → `gpt-5.6-sol`；测试价 input=`0.000003/token`、output=`0.000015/token`，usage 1000/500 tokens、rate multiplier 1.5；compact 映射上游到 `gpt-5.5` | canonical billing ID=`gpt-5.6-sol`；base `TotalCost=0.0105`，user charge/`ActualCost=0.01575`；usage 记录 actual upstream=`gpt-5.5` 与 rewrite reason。alias/raw ID/compact target 不暗改用户价格。另断言 Unified Gateway 显式 billing lane/price 不被 refresh 改动；真实 provider cost 另行核算，不伪称本地价格等于上游成本。 | 把 client charge model、upstream request model 和供应商成本混成一个字段；计费变化无法解释。 |

## 6. R5—刷新可观察与管理员控制

- 每次计划 tick/catch-up 写 global run ID 与开始/结束时间、结果分类和计数；per-account 状态独立可读。
- `GET /api/v1/admin/model-catalog-refresh/status` 在成功、partial failure、disabled、无 leader、Redis unavailable 时返回可区分状态；缺失记录不能显示成 success。
- 响应只含 account/source IDs、freshness、counts、error kind、digest prefix；测试断言不包含 API key、Authorization、credential extra、完整 URL/query、上游原始错误 body。
- 已有 `POST /api/v1/admin/accounts/:id/models/sync-upstream` 使用同一 adapter/schema/completeness/normalizer 与原子提交路径；manual policy 的即时刷新不会自动扩大组权限。
- 无管理员权限时 status endpoint 服从现有 admin auth/ACL；错误不能泄露目标 account 是否存在。

## 7. R6—定向命令与停止条件

依实施时代码路径调整命令，但至少执行等价覆盖：

```powershell
go test -count=1 -timeout=300s ./internal/pkg/openai ./internal/config
go test -count=1 -timeout=300s ./internal/service -run 'UpstreamModel|Compact|ModelMapping'
go test -count=1 -timeout=300s ./internal/handler ./internal/handler/admin ./cmd/server
go test -count=1 -timeout=600s ./...
go vet ./internal/service ./internal/handler ./internal/repository
git diff --check
```

报告每条命令真实 exit code；任何 skip 列出名字/原因。不得把链接器内存不足报成代码测试通过。Windows 可按验证环境限制 `GOMAXPROCS`/`-p 1` 重跑并记录精确参数；不可偷偷把整个全仓测试换成定向测试。

**遇到即停止发布**：alias route 无唯一目标、模型可见但 ACL 未批准、路由列表不一致、上游 transient error 清空目录、GPT 首次请求仍暗改成 gpt5.5、普通 Responses/v2 被 compact setting 改写、真实请求发生不可解释的 model/account switch 或计费模型错配。

## 8. 最小实测矩阵

本地全过后另行申请/执行线上测试：

1. 两台 VPS 固定同一 image digest 和配置摘要；先只读比对账号来源/目标用户组。
2. 通过后台只读状态确认 scheduler build、最近 run ID、成功/失败/unsupported 数和目录 generation；不要以系统 cron/旧日志替代。
3. 选择一个已批准测试账号、一个组、每个 provider 各一个低成本文本请求；先比对上游 model list，不发送模型探测到所有生产账号。
4. 运行 compact 请求时只使用该账号原本支持且额度可承受的模型，核对上游实际 `model`、response compact item、usage 和钱包 delta。
5. 每个 write-like request 一次；除非有稳定幂等键并经用户批准，不重放可能扣费的流量。
6. 保留脱敏日志与失败证据；任何 502/503/429 归因需原始 status/error code/request ID，而不是只写 “upstream error”。

线上未执行或尚未观察到的矩阵项必须标 `NOT_VERIFIED`，不能被本地模拟结果覆盖。
