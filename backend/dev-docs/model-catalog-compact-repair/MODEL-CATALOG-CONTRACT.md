# 上游模型目录、规范名和路由契约

## 1. 模型身份：展示、输入、计费和上游路由分离

每条目录记录分开表达，禁止用一个字符串同时代表“上游发现”“下游名字”“路由目标”：

| 字段 | 含义 | 是否下发客户端 |
|---|---|---|
| `raw_upstream_id` | 上游模型目录返回的精确 ID，用于真实请求和 provider-specific 映射。 | 否 |
| `canonical_public_id` | 唯一正式、稳定、公开 ID。仅精确正式 ID或经审核的 provider-specific alias rule 可产生。 | 是 |
| `availability` | 该账号最近一次权威刷新对 raw ID 的新鲜度与可用状态。 | 否 |
| `lifecycle` | 厂商明示的 active/legacy/deprecated/retired 状态及准确截止时刻、证据版本；与账号是否可访问分开保存。OpenAI 的 shutdown date 使用本系统状态 `scheduled_shutdown`，不伪称厂商生命周期状态。 | 否 |

实现时还必须明确区分以下身份，不能把“客户端只显示正式名”误实现成“拒绝官方兼容别名”：

- `canonical_public_id`：列表和 manifest 唯一展示名。
- `accepted_input_alias`：明确有一手依据、允许客户端提交的兼容别名；不默认出现在目录中。
- `billing_model_id`：沿现有计费映射计算价格的 ID。alias 归一前后必须检查价格/计费身份不发生意外变化。
- `raw_upstream_id`：该 source/account 实际请求的模型 ID。只有精确 catalog target 或当前有效、source-scoped 的兼容 alias route 才可使用。

因此 `GET /v1/models` 中可不列 `gpt-5.6`，但若官方兼容契约确认它是 `gpt-5.6-sol` 的 alias，输入 `gpt-5.6` 应先规范化到 `gpt-5.6-sol`，并仅在账号/组有该 canonical route 时继续；绝不能拒绝为“未知模型”，也不能猜到 Terra/Luna。

快照还需要 `provider/source_family`、`source_profile_id`、`schema_version`、`last_attempt_at`、`last_success_at`、`next_due_at`、`status`、`raw_digest`、`normalizer_version` 和脱敏 `last_error`。模型 lifecycle 独立存 `lifecycle_state`（`active` / `legacy` / `deprecated` / `scheduled_shutdown` / `retired` / `unknown`）、`lifecycle_cutoff_date`（`YYYY-MM-DD` 或 null）、`lifecycle_source` 与 `lifecycle_evidence_revision`。`legacy` 表示不再更新、未来可能弃用，不等于不可调用；`deprecated` 表示仍可用但不再推荐且有退休日期；`scheduled_shutdown` 是本系统对 OpenAI `shutdown_date` 的确定性映射；`retired` 表示该 ID 不再作为独立模型提供，默认不得直接路由。单独的 `accepted_input_alias` 记录需保存 `alias_state`（`accepted_compatibility` / `withdrawn`）、canonical target、source profile、`route_mode`（`canonical_target` / `raw_compat_alias`）、证据版本/复核日期；只有仍有效且来源明确的兼容 alias 可在 standalone model retired 时继续接受输入。`raw_compat_alias` 还必须有该账号完整 snapshot 精确包含 raw ID、该 source 的当前 alias-route contract 已验证；证据过期/撤销就拒绝旧 alias，不得将 retired model 当 canonical route。生命周期不得从 account catalog 的缺席推断；不确定是否实际退役时保持 `unknown`，另设 review-required 状态而不扩展 lifecycle enum。不得保存 authorization、API key、上游响应全文或可还原凭证的 URL。

`confirmed_absent` 是与 model lifecycle 分开的 availability 负向证据：仅在权威完整目录成功后，对尚在显式 account mapping 中但无法按当前 canonical resolver 路由的 target 记录 `canonical_id → raw_digest`。其有效范围绑定 snapshot 的 `source_identity_fingerprint`；超过 48 小时后仍用于拒绝同一身份下的显式 target，避免已确认缺席在 stale 过期时被误当作可用。身份或 normalizer 变化后必须重新探测；新的成功完整目录重建该集合，模型重新出现即可解除负向事实。它不表示厂商生命周期退役，不删除管理员 mapping，也不影响其他 source identity。

快照还需持久化 `source_identity_fingerprint`：绑定精确 source origin、账号认证身份的单向摘要和可安全取得的 entitlement 标识。切换地域/endpoint/凭证/权限后旧快照不可复用；原始凭证仅参与单向散列，不保存在快照、日志或 API。`normalizer_version` 与受审 alias registry 版本绑定；版本变化后旧 `canonical/raw` 绑定必须失效并触发刷新，不得继续沿用。

### 1.1 数据与分页硬上限

- 每页响应体受现有 `gateway.models_list_read_max_bytes` 限制（当前默认 8 MiB）；一次完整账号 refresh 所有页累计响应体最多 8 MiB；最多 100 页、最多 2,048 个 distinct model IDs、单 ID UTF-8 长度最多 256 bytes。
- 持久化的单账号 availability snapshot JSON 不超过 2 MiB；snapshot 存精确 raw IDs、必要的官方日期型 lifecycle 字段、运行元数据，以及仅针对显式 mapping target 的 `confirmed_absent` canonical ID→raw digest 负向记录。canonical/alias 正向 route binding 从版本化 registry/目录派生，避免重复序列化整份路由记录。confirmed-absent 记录受 source identity、normalizer 与总字节上限约束。超过任一限制整次刷新判为 `incomplete/over_limit`，保留 last-good，不截断、不提交部分目录。任何扩大上限须先有实际账号目录尺寸证据、VPS 内存/数据库行尺寸评估与边界测试。
- 每个 source adapter 必须使用其官方/供应商文档规定的分页参数与终止条件；若官方 page size 小于硬上限，采用官方值。没有分页证据不得自行猜测参数。页游标重复、ID 重复异常、总数变化、到达本地 page/byte cap 但远端仍报告 `has_more` 都视作抓取不完整。
- 上述 100 页/2,048 IDs/256 bytes/2 MiB 是本系统资源保护上限，不是厂商承诺；若合法供应商目录超限，须单独做存储/资源设计后再提高，不能静默放宽或接受部分结果。

## 2. 目录来源与账号策略

### 2.1 Account policy

每个账号使用以下目录策略；不得仅由“是否有 mapping”隐式地在后台改权限：

- `manual`：目录刷新本身不自动增加公开候选或启用动态 route。可信官方 OpenAI Platform API-key 账号的空 mapping 不构成授权；已有显式 mapping 只按 fresh/stale/manual-unverified 规则处理。`manual_only`/未识别 OpenAI-compatible 来源及 OAuth 保留旧版请求兼容行为（例如无 mapping 时按原规则透传/筛选），但不会被 catalog snapshot 描述为已验证，也不从静态模型表合成 `/v1/models`。
- `follow_upstream`：使用权威成功目录中的安全 canonical IDs 自动提供、路由新增模型；显式 mapping 对同一 public ID 的目标优先。此项是权限开关。

`manual_only` 是 source profile 能力标签，`unsupported` 是一次 refresh 状态；它们不是第三种 account policy，也不得被 API 写成 policy。两者都不能 opt-in 为 `follow_upstream`。

安全默认：所有新建、既有和缺少该字段的账号均为 `manual`；绝不根据 `model_mapping` 是否为空自动授予 `follow_upstream`。这样部署与回滚不会静默扩大可调用模型集。若用户希望新模型随日常刷新自动进入目标用户组，管理员先查看 dry-run 预览，再一次性显式 opt-in 指定 source/account（可以按已审核的一组 account IDs 批量授权）；此后每天自动刷新，不需每日操作。迁移不得改写任何既有 `credentials.model_mapping`、group allowlist 或 unified route/pricing。本字段变化要清除共享认证 projection 与目录缓存。`manual` 且没有任何管理员配置的 mapping/允许模型时，该账号不贡献动态公开模型；不得回填 static defaults 冒充 account availability。上线 Phase A 应先比较此行为导致的列表变化，再由管理员明确执行 Phase B 授权。

`manual` 只禁止上游新模型自动扩展管理员目录，不代表忽略可验证的下线事实。以下矩阵定义可信官方 source 的 mapping 可见/路由语义；列表和请求解析必须使用相同结果。最后两行记录为兼容保留的 legacy exception：

| Source/快照状态 | 显式管理员 mapping target | `manual` 行为 | 状态标记 |
|---|---|---|---|
| 权威、完整、fresh snapshot 包含精确 raw target | 存在 | 列出并允许路由；不自动增加其它 ID | `verified` |
| 权威、完整、fresh snapshot 的 canonical route set 不含 target（上游未返回，或 source-specific normalizer 将其隔离） | 存在 | 立即隐藏并拒绝该账号 route；mapping 保留，不能把 stale/删除误改成配置删除 | `confirmed_absent` |
| 有 last-good，且处于 48h stale grace | 存在且上次完整 snapshot 包含 | 临时列出/路由该 last-good target，明确为 stale；不接受本轮不完整结果的正/负判断 | `stale` |
| 从未有完整 snapshot、超过 stale grace、`manual_only` source 或 endpoint `unsupported` | 存在且符合已审核精确 alias/canonical rule | 为兼容显式管理员意图，可列出/尝试路由；不宣称可用，也不产生自动发现 | `manual_unverified` |
| 任意状态 | 无显式 mapping | 不列出、不路由；空 mapping 不触发 follow | `not_configured` |
| 任意状态 | 明确 retired 且没有仍有效的兼容 alias，或兼容 alias 已 withdrawn | 隐藏并拒绝旧 ID；即使有 mapping 也不能越过 lifecycle/alias gate | `retired_or_alias_withdrawn` |
| 未识别/`manual_only` OpenAI-compatible source，缺失或不可信 snapshot | 无 mapping 且旧版路径允许透传 | 保留上游既有请求语义；不因 refresh 自动收录；公开 group list 不从 static defaults 补模型 | `legacy_unverified_route` |
| Codex OAuth / 特定 legacy platform rule | 无 mapping | 按原 OAuth/provider 模型筛选规则处理；manifest 不得从无来源的 static defaults 冒充该账号实时可用目录 | `legacy_source_rule` |

`follow_upstream` 必须有 authoritative complete snapshot；在 stale grace 内可使用上次完整快照，过期后隐藏/拒绝自动发现模型。`manual_only/unsupported` 不允许切换成 `follow_upstream`。上述 manual-unverified 只承认管理员配置，不等于刷新成功、上游可用或模型已从官方目录确认；管理员状态 API 必须明确区分。

### 2.2 Group policy 和安全

用户组的模型 allowlist/request guard 是最终准入规则：

```text
published/requestable(group) =
  union(account verified-or-explicit-manual route candidates)
  ∩ group model-list visibility policy
  ∩ group model request allowlist (when enabled)
```

- 不得因 refresh 自动重写/关闭 allowlist。
- `manual_unverified` 是管理员显式配置候选，不得在状态 API/审计中描述为 live-available；客户端目录与请求 resolver 仍必须一致地包含或排除它。
- 若某个组需要随上游自动新增正式模型，其来源账号需启用 `follow_upstream`；若组 allowlist 已开启，需通过现有 exact entries 或经过范围审查的 wildcard（`GroupModelAllowlist` 已支持 `*`）一次性准入未来模型。启用前记录受影响 provider/account 数和允许的命名空间。若无显式账号策略/白名单匹配，刷新只更新 source-of-truth snapshot，不扩大组权限。
- 被 allowlist 拒绝的模型既不得出现在客户端目录，也不得通过手工构造请求绕过。
- composite group 对每个来源 account 分别解析 availability/configuration status；相同 canonical ID 的 route target 只能由现有 ownership/scheduling 规则选出，不因字符串相同而混淆不同 provider。

## 3. 刷新状态机

默认参数沿用用户确认的运行要求和旧实现有据的低压力值：

```yaml
gateway:
  upstream_model_refresh:
    enabled: true
    schedule: "0 4 * * *"
    timezone: "Asia/Shanghai"  # UTC+8
    request_timeout: 30s
    total_budget: 30m
    stale_grace: 48h
    max_concurrency: 4
    startup_catch_up: true  # 仅 due accounts，有界批次
```

唯一调度键为 `gateway.upstream_model_refresh`（实施时与目标官方版本配置结构对照；若已有等价项则复用）。每天 04:00 运行一次；此外，启动时只对已 due 的账号做有界 catch-up，以避免进程在 04:00 离线导致整天漏刷新。所有计划刷新、Smart Router 校准和管理员即时 sync 共用同一 per-account distributed lease，避免并发双抓。共享 coordinator 有集群 leader lease（原子获取、定期续租、owner-checked release）；租约丢失即取消该 owner 尚未完成的请求。每账号持久 fencing 状态需区分 `latest_issued_token` 与 `last_applied_token`：获得该账号刷新权后，先在 Postgres 原子递增并提交 `latest_issued_token`，然后才发起上游 fetch；snapshot commit 必须在同一 DB transaction 中要求请求 token **等于当前 `latest_issued_token`** 且大于 `last_applied_token`，再合并写入 snapshot 并更新 `last_applied_token`。因此新 owner 仅取得锁/签发 token、尚未抓取或提交时，也立即使旧 owner 的迟到写入失效；只比较“小于最近已提交 token”不够。两个 VPS 必须使用相同的 Redis lock namespace 与 Postgres fencing state。锁不可用、续租失败或 fencing 状态不可读时 fail closed，不发或不提交 refresh。批次设置总时限、并发上限、账号级 timeout 和可取消 context；lease TTL/heartbeat 必须覆盖这些界限。

| 结果 | 更新快照 | Dynamic 列表/路由 |
|---|---|---|
| 2xx、schema 合法、目录通过供应商特定完整性检查 | 原子替换成功快照；成功空目录只有供应商明确说明空列表合法时才接受 | 立即用新快照；模型缺席即确认从该账号下线 |
| timeout、连接错误、401/403、429、5xx、超体积、非法 JSON/结构 | 只记录 attempt/error，不覆盖 last-good | 在 48h stale grace 内继续用上次快照并标 stale；过期后隐藏且拒绝路由 |
| 404/405 或适配器声明不支持目录接口 | 标 unsupported；不伪装成一次“空成功” | `follow_upstream` 不得自动生效；显式管理员 mapping 可按 `manual_unverified` 兼容保留，静态 fallback 不得冒充 account availability |
| empty / 全部归一结果进入 quarantine | 除供应商规则明确授权空集外视为不可信失败 | 保留 last-good，告警；不能一次空响应清空生产目录 |

默认 48h 宽限期不代表过期目录可无限继续调用。一次刷新失败不能使此前模型瞬间消失；多次失败跨过宽限后，dynamic 模式 fail closed。

## 4. 解析算法与一致性

对 `follow_upstream` 与可信显式 mapping，请求 `model` 按如下顺序解析。`legacy_unverified_route` 例外保持官方上游的旧行为，不能被误报成 catalog-verified：

1. 如果输入精确命中 canonical ID，则保持该 ID；如果命中 `accepted_input_alias` registry，则规范化到唯一 canonical target；未注册 alias/retired model 返回结构化 `model_not_found`。官方 documented aliases 与 retired aliases是否接受必须按 registry 显式写明，不能套一个“所有 alias 都拒绝”的全局默认。
2. 组权限检查针对归一后的 canonical ID；输入 alias 不得绕过 allowlist。
3. 找到 group 允许且策略可调度的候选账号：`follow_upstream` 只能来自 fresh snapshot 或 stale grace 内 last-good；`manual` 可由显式 mapping 在 fresh positive / stale-within-grace / manual-unverified 状态参与，fresh authoritative negative 与明确退役/撤销 alias 永远拒绝。
4. 对有权威 fresh/last-good 证据的账号，mapping raw target 必须精确存在于相应完整 `raw_models`；在无权威目录或过期/unsupported 的 `manual_unverified` 状态，只允许使用管理员显式 mapping 的 exact raw target，不得自动改写或把 quarantine ID 当作 route。
5. `follow_upstream` 账号可使用 snapshot 的 `canonical_public_id → raw_upstream_id` 映射；manual 账号不能凭刷新快照临时扩大 mapping。
6. composite 在通过上述规则后再执行现有 provider ownership、priority/sticky/health 选择。
7. usage 的请求模型保留 canonical public ID，实际 provider 模型单独记录 raw ID；计费价格选择沿既有正式 public/billing mapping，不得把 raw experimental alias 当新公开价目 ID。输入 alias 必须先 canonicalize，再做 allowlist 和公开模型资费 lookup。

计费契约（保持当前 Sub2API user-charge 语义，不重做 upstream-cost accounting）：计费 ID 是规范化后的客户端请求模型所对应的既有 `billing_model_id`；隐藏 raw alias、账号 raw upstream ID 与 compact rewrite target 不能暗中改变用户价格。显式 compact fallback 只变更实际 upstream model，usage 必须同时保留 canonical requested/billing model、actual upstream model 和 rewrite reason；当前 forward retry 保留首次确定的 billing model，这一行为需要在新版本基线复核。Unified Gateway 的 price/billing lane 继续使用显式 route 配置，目录刷新不得创建或重写它。实际供应商成本若由其他账务数据源计算，必须以实际 upstream model/usage 单独核账；本任务不声称改变或验证供应商成本价。

`/v1/models`、Codex manifest、OpenAI dedicated manifest、composite listing、`IsModelSupported`、调度候选和实际 forward 对动态目录及显式 mapping 必须共用同一 canonicalization、来源与 availability 规则；但各 surface 可因 source profile、entitlement、visibility/client filter、legacy compatibility 与 group ACL 而不同，**不得强制 OAuth manifest 与 API-key `/v1/models` 返回相同全集**。`follow_upstream`/trusted snapshot 中展示的 ID 必须可解析到同一账号 raw route。公开 handler 不可再合并 static `DefaultModels` 并声称实时可用；静态表只可作为管理 UI/协议元数据。无可信或显式候选时返回合法空集合。legacy manual-only 无 mapping 仍可能接受旧版请求，但不得据此伪造公开模型目录或显示为已验证可用。

### 4.1 独立的 Unified Gateway catalog

当前自适配仓库还存在独立的 `GET /unified/v1/models`，其 `UnifiedGateway.ListModels` 从管理员显式配置的 route/target/billing lane 生成结果，并校验 capability 与定价。它不是 legacy `/v1/models` 的别名，也不是 account refresh snapshot 的直接投影：

- upstream refresh **不得自动创建** unified route、公开 alias、价格、billing lane 或权限。
- 每个 unified public model 必须保留已有显式 route/pricing 配置；其底层账号能力可引用同一 fresh snapshot 做过滤，但缺少显式配置时不能因发现 raw ID 就自动售卖/开放。
- 测试分别断言 `/v1/models` 与 `/unified/v1/models`；前者动态账户 catalog 可随策略变化，后者只在显式 route 配置变化时变化。
- 如未来需要自动生成 Unified Gateway routes，必须另开设计，解决定价、安全和 route identity 后再做，不能混入本任务。

## 5. 规范名规则

### 5.1 通用规则

- 每个上游 adapter 必须有 `catalog_trust_profile`：`primary_official_catalog`（模型厂商官方目录，精确 ID 默认可信）、`verified_reseller_catalog`（已审计的代理目录，精确 ID 是否 canonical 由其 provider policy 决定）或 `manual_only`。不能把“HTTP 200 + 有 id 字段”自动当作官方目录。
- `primary_official_catalog` 中未命中 source-specific alias/deprecation/internal-ID registry 且符合该 provider ID policy 的精确 ID 可作为候选正式名；只有账号显式启用了 `follow_upstream` 才能自动进入公开列表/路由。日期/preview/exp 等命名不做通用剥离：有确切来源规则的 alias 才 canonicalize；其余按 provider policy 原样保留或 quarantine。OpenAI 的已知 dated snapshot IDs 按 `IsAutoDiscoveredModelID` 策略不自动公开，也不改路由到其 stable alias；stable ID 仍按上游目录中的精确 ID 处理。`ft:` 等 OpenAI 账号私有 fine-tuned IDs 默认不自动公开，只允许管理员显式 mapping/公开别名后提供；原始 ID 仍可内部诊断。`verified_reseller_catalog` 只能按已验证 source policy 自动收录。来源 profile 不明或异常 ID 无法判别时 quarantine 并告警，不下发。
- 精确、可信来源给出的正式 ID 原样保留（仅做空白、重复、大小写比较等无损处理，不做大小写改写）。
- alias rule 必须带 provider/source family、精确 raw pattern/ID、唯一 canonical target、生效来源 URL/版本、有效期/状态、测试夹具和 `normalizer_version`。
- 只有 source-specific 官方规则明确声明 dated snapshot 是某个 stable public model 的可替代别名，且不会把请求重定向到另一种模型语义时，才可公开 canonical 并把 raw snapshot ID 留在内部 route target；若语义仍是 pin 到特定 snapshot，则不得悄悄改成 stable model。
- 日期名本身若被该 provider 明确作为正式 ID 发布，应按该 provider 的 ID policy 精确保留；不能因为形状含日期就通用删除。当前 OpenAI 已知快照过滤是明确的 OpenAI 专属 policy，不适用于 Anthropic、Model Studio 或自定义 OpenAI-compatible source。
- `preview`、`exp`、`latest`、`codex`、`vision` 等语义后缀不得通用剥除。对已验证的权威官方目录，精确列出的此类 ID 按原名保留，除非该来源明确标为 alias/deprecated/retired/internal；对未验证 reseller 或来源不明目录不从名称猜正式性，保持 manual-only/quarantine。
- 若正式与 alias raw IDs 同时可见，精确正式 raw ID 优先；两个候选都只能映射到同一 canonical ID且无唯一选路证据时，canonical 可显示但不得给该账号创建不确定 route；如果显示意味着不可路由则整体不显示。
- provider policy 必须明确未知 ID 的 identity-map 规则。当前 DeepSeek、Anthropic 与 Alibaba trusted profiles 对未命中特定规则的精确 ID 保留；OpenAI official profile 额外应用既有 OpenAI lifecycle/deprecated/dated-snapshot filter。Custom OpenAI-compatible IDs 按 source 原名保留。不得将 OpenAI filter 外推到其他 provider，也不得因新 ID 带日期就自动改写；报告/测试需分别覆盖 OpenAI dated suppression、non-OpenAI dated exact preservation、custom-source exact preservation，以及显式 source-scoped alias。

### 5.1.1 Source profile registry（实施前不得扩大自动发现）

Source profile 必须按**实际连接账号所用 endpoint 和供应商/代理**定义，而不是只看平台枚举或 OpenAI-compatible 协议。至少存 profile ID、normalized origin fingerprint、认证类型、request method/path、分页、query/filter/region、最大页/总字节、官方/代理一手依据、entitlement 语义、model ID/lifecycle 映射、失败分类和契约夹具。Source origin 必须使用 HTTPS 且不得含 userinfo/query；模型目录请求不得跟随到不同 origin 的 redirect，发生跨 origin redirect 即拒绝并保留 last-good，避免把 credential/source trust 转移给第三方。来源或分页不明时必须 `manual_only`。

| Profile | 一手目录资料 | 初始策略 | 自动发现前置条件 |
|---|---|---|---|
| `openai-platform-api-key` | [OpenAI Platform List Models API](https://developers.openai.com/api/reference/resources/models/methods/list) | 管理员显式启用后可 `follow_upstream` | 仅当账号 credentials 显式保存的 base URL origin 精确为 `https://api.openai.com`、路径契约为 Platform `/v1/models` 且认证为 OpenAI API key 时，使用此官方 profile 与 `data[].id` parser；不能用 `PlatformOpenAI` 的运行时默认 URL 推断官方来源。自定义 base URL、兼容网关和 reseller 使用独立 profile，未经验证保持 `manual_only`。保留有效 `shutdown_date`；不套给 ChatGPT OAuth。 |
| `openai-chatgpt-codex-manifest` | 当前代码 `buildOpenAIOAuthUpstreamModelsRequest` 请求 `https://chatgpt.com/backend-api/codex/models`；[OpenAI SIWC Models and Inference](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference) 描述另一种 OAuth `/v1/models` `models[]/visibility/slug` 形态 | `manual_only`，完成 schema/分页/完整性验证后再启用 | 两个 OAuth 目录不是同一 endpoint/response contract；不得复用 API-key `data[].id` parser 或把 SIWC 文档直接当成 Codex manifest 契约。明确验证 ChatGPT manifest auth、客户端/版本过滤、分页与生命周期后才允许自动发现。 |
| `deepseek-official` | [DeepSeek List Models](https://api-docs.deepseek.com/api/list-models/)、[Models & Pricing](https://api-docs.deepseek.com/quick_start/pricing/) | 管理员显式启用后可 `follow_upstream` | 仅精确官方 origin `https://api.deepseek.com` 与官方 API credentials 使用此 profile；完整读取官方当前目录；legacy alias 单独存输入别名与 raw ID；自定义 base URL/第三方转发需独立 profile/live验证。 |
| `aliyun-model-studio` | [List Models API](https://help.aliyun.com/en/model-studio/list-models) | 管理员显式启用后可 `follow_upstream` | 按账户 region/workspace endpoint 和权限读取所有页；保留官方返回的日期版本；模型 provider 与 inference provider 均需记录。 |
| `anthropic-official` | [Models API](https://platform.claude.com/docs/en/api/models/list)、[Deprecations](https://platform.claude.com/docs/en/about-claude/model-deprecations) | 管理员显式启用后可 `follow_upstream`；生命周期 registry 独立 | 仅精确官方 origin `https://api.anthropic.com` 与 Anthropic API credentials 使用此 profile。Models API 负责账号可用目录并完整 cursor 分页；不能假设 list response 是生命周期权威。Active/Legacy/Deprecated/Retired 由独立、版本化 deprecation registry 给出；Deprecated 仍可请求，Legacy 也不等于已下线，只有 Retired 才禁止 Anthropic-operated route。Partner-operated platform 的状态/日期可能不同，必须 source-scoped；未确认状态不得按缺席推断。不要剥除正式日期 ID。 |
| `glm-direct`, `minimax-direct`, `moonshot-direct`, `hy4-direct`, `yetoken-reseller` | 本方案未确认足以写入自动拉取器的 endpoint/分页/权限契约 | `manual_only` | 逐个确认正式文档或代理书面 API 契约、当前 account plan、返回是否完整、分页终止与生命周期含义，并以脱敏线上只读目录验证。 |

OpenAI-compatible `/v1/models` 只有在 account source 经验证确实实现了完整模型列表语义后才可读取。协议兼容不等于 endpoint 一定存在、完整或权威；404、405、截断页、region/filter 不明都不是“空模型目录”。

注意 source 与 model vendor 是两个维度：Alibaba Model Studio 官方 `List Models` 文档列有 provider/inference-provider filter，包括 Zhipu、Moonshot、MiniMax 等。若实际账户 endpoint 是 Model Studio，应使用 `aliyun-model-studio` profile 并保留其 region/workspace/entitlement；这不证明对应 vendor 的直营账号也有相同目录，更不证明 YeToken 等 reseller 与 Model Studio 同源/同套餐。`HY4-Preview` 若未确认具体 source/vendor，必须维持 quarantine/manual，不按名字猜供应商。

### 5.2 已知治理条目

| 上游名/静态名 | canonical public ID | 处理 | 证据/待办 |
|---|---|---|---|
| `gpt-5.6` | `gpt-5.6-sol` | 官方 input alias：目录不单独展示；请求输入可接受并规范到 Sol；严禁将其解释成“任一 GPT-5.6 variant”。路由前按账号能力校验 canonical target。 | [OpenAI GPT-5.6 Sol model page](https://developers.openai.com/api/docs/models/gpt-5.6-sol) 明确记载 alias `gpt-5.6` routes to GPT-5.6 Sol。 |
| `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna` | 各自精确 ID | 分别公开/路由；不可互相替换。 | OpenAI [models catalog](https://platform.openai.com/docs/models) 显示 Sol/Terra/Luna 的独立 ID 与能力。具体第三方账号是否支持仍按 account source 验证。 |
| `gpt-6-astra`, `gpt-6-sol`, `gpt-6-luna`, `gpt-6.1-sol` | 各自精确 ID | 均是独立官方模型 ID；分别按账号目录/授权路由，不互相替换。 | OpenAI 官方 [model catalog](https://developers.openai.com/api/docs/models)、[GPT-6 guide](https://developers.openai.com/api/docs/guides/latest-model) 与 [GPT-6 Astra page](https://developers.openai.com/api/docs/models/gpt-6-astra)。catalog freshness 和账号 entitlement 仍不能由静态表替代。 |
| bare `gpt-6` | 未知；不自动推到 `gpt-6-astra` | 不公开、不路由、不接受为 alias，除非未来找到官方或精确 reseller 一手契约。 | 当前静态名/界面格式不构成 bare alias 依据；不要将真实 canonical `gpt-6-astra` 错删。 |
| OpenAI `shutdown_date` | 同一 exact model ID | 官方字段格式为 `YYYY-MM-DD` date，不是时间戳。内部状态名为 `scheduled_shutdown`，不称作厂商 `deprecated`。保存原日期；在该 UTC 日结束前仍按 account availability/ACL 判定，次日 `00:00:00Z` 起隐藏并拒绝，以避免在厂商未声明时区时过早下线。缺失/null/非法值是 `unknown`，不是 retired。请求时按 UTC 日实时校验，不等次日 04:00 scheduler。 | [OpenAI List Models](https://developers.openai.com/api/reference/resources/models/methods/list) 明确 `shutdown_date` format=date，并示例 `2026-10-23`。UTC end-of-date 是本系统保守确定性策略，不是 OpenAI 声明的时区；若官方补充时区/精确 instant，实施需按新一手契约更新。 |
| `codex-auto-*` | 无公开 canonical ID | 从 `/v1/models`、manifest、admin picker、自动发现与 follow-policy snapshot 隐藏；不因目录隐藏而全局拒绝既有显式/内部路由。它不能因上游目录出现而自动变成公开模型或扩大账号/用户组授权。 | `constants.go` 中 probe 常量可继续复用；每个公开目录投影必须剔除该前缀，路由仍按账号及 group policy 决定。 |
| `deepseek-v4-flash`, `deepseek-v4-flash-vision-exp` | `deepseek-flash` | 官方 `/models` 当前 schema/example 使用 canonical ID `deepseek-flash`；上述 legacy input names 虽仍暂时接受，但不是当前目录中独立正式模型。客户端旧名只在账号目录中存在 canonical target 时归一并路由到该 exact target。若 reseller 的完整账号快照实际包含旧 raw ID，是否将它作为该账号内部 route target 必须由该 reseller/account 的独立受审测试决定，不得从官方 API 推断。 | [DeepSeek List Models](https://api-docs.deepseek.com/api/list-models/)、[Models & Pricing](https://api-docs.deepseek.com/quick_start/pricing/) 与 [Updates](https://api-docs.deepseek.com/updates/)；官方请求兼容与目录成员资格分开；vendor 事实不外推到 YeToken/其他 reseller。 |
| `deepseek-v4-pro` | `deepseek-v4-pro` | 官方当前 API model ID，精确保留。`DeepSeek-V4-Pro-0813` 是官方显示的 model-version metadata，不是 API ID；不得把版本字符串变成 `deepseek-v4-pro-0813`，也不得只凭 version 字段把 API ID 改写为 `deepseek-flash`。 | [DeepSeek List Models](https://api-docs.deepseek.com/api/list-models/)、[Models & Pricing](https://api-docs.deepseek.com/quick_start/pricing/)。官方 Updates 与 2026-09-10 News 对 2026-09-14 后 V4 Pro routing/billing 说明存在差异，故本条只锁定 API ID，不断言底层部署/计价；需以当前账号实时目录、响应/usage 与最新官方公告确认。 |
| `deepseek-v4-pro-0813` | 暂无全局 canonical | 未找到 DeepSeek 官方 API model ID 证据；若实际独立 provider 的 authoritative list 精确返回此 ID，先按 source-specific exact ID quarantine/review，不自动 alias 到 `deepseek-v4-pro`。 | `DeepSeek-V4-Pro-0813` 仅在官方 pricing table 的 MODEL VERSION 栏出现；API `id` 仍是 `deepseek-v4-pro`。不得把第三方通用目录页当作此具体 ID 的证据。 |
| 任意 `*-YYYYMMDD` / `*-preview` / `*-exp` | 无通用目标 | 权威官方目录精确列出的 ID 按正式 ID 原样保留，除非来源另有明确 alias/lifecycle 标注；非权威或未验证 reseller source 的此类 ID quarantine，等待 source-specific review。绝不通用剥除日期/后缀。 | `fixtures/model-catalog-contract.json` 给出 trusted official exact-ID 与 generic/unverified-source quarantine 正反夹具。 |

### 5.3 刷新更新 alias registry 的控制

自动刷新只更新 availability snapshot，不自动把观察到的新 alias 写进 normalization registry。正式名归一化 registry 是代码/受审计数据：新增/更改 alias 规则必须带厂商一手来源或经批准的 provider contract、唯一性证明、价格/计费影响检查和反例测试。否则只在该账号 quarantine。这样既可自动发现可信目录里的新正式 ID，也不会让一次上游脏目录自动改写所有下游模型名称。

### 5.4 生命周期治理与下线判定

- Account availability（此账号当前能否列出/调用）与 vendor lifecycle（厂商是否已计划弃用/退役）是两个正交维度；二者任一不满足时不列出/不路由，但不得互相代填证据。
- OpenAI Platform catalog 的合法 `shutdown_date` 是 date-only，不含时区/时刻。为避免过早下线，内部状态为 `scheduled_shutdown`，并按 UTC 该日结束作为 cutoff：该日之内仍须满足 account availability 与 ACL，次日 `00:00:00Z` 起隐藏/拒绝；请求 admission 要实时检查，不依赖每日刷新时钟。缺失、null、非法格式记为 `unknown`，不得自行推测退役。这个 UTC 日期边界是明示的本地决策，官方未声明 timezone；若后续文档明确需更新。
- Anthropic Models API 的分页结果作为 availability；弃用时间从独立、版本控制的 Anthropic deprecations source registry 引入。若没有机器可读且可认证的生命周期接口，registry 更新是受审阅的资料更新，不伪装成 daily availability refresh 自动得出。
- 对 DeepSeek 等官方文档之间若出现仍提供 API、底层实际路由和计价说明不一致，canonical API ID 保持原值，相关 runtime route/billing observation 维持 `unknown` 并另记 `review_required=true`，停止自动更改 route/价格；不要把此运行态不确定误记为 `model_lifecycle_state=retired`。Reseller compatibility alias acceptance 必须单独有自己的 source profile evidence。
- authoritative availability fetch 缺席只表示该 account 当前不再列出该 ID；只有 lifecycle authority 给出 retired 才可写 lifecycle retired。刷新失败/过期按 stale policy 处理，不能把失败当下线。

## 6. Cache 与可观测性

- 一次成功目录变化必须失效 account 所属 group/platform 的 models cache、Codex manifests、composite ownership cache；跨实例部署的缓存失效不得只依赖进程内 TTL。
- 缓存 key 至少包含 group、platform、policy revision/catalog generation；账号目录 generation 变化可触发共享失效或短 TTL 重验。
- 成功但无变化可只更新 attempt/last-success 元数据；不得无意义重写大对象。
- 快照记 provider/source family、脱敏 origin/source fingerprint、adapter version、账号权限/plan 标识（若可安全取得）。两台 VPS 的同源且同 entitlement account 可以比较 canonical digest；差异触发诊断，不把 A 站点目录复制到 B，也不要求不同 plan 的账号目录一致。
- 结构化日志只记录 account ID、provider、run ID、结果分类、raw/canonical/quarantine 数量、耗时和 digest 前缀；不记录 key、授权 header、完整 URL/query 或 body。
- 管理可观察字段必须能区分 `fresh`、`stale`、`expired`、`unsupported`、`error`，以及最后成功时间/本轮错误分类；不得只打印“refresh started”。

为让管理员能直接确认“今天是否跑过、哪些账号刷新成功、模型为何没变化”，新增只读 admin API：

```http
GET /api/v1/admin/model-catalog-refresh/status?account_id=<optional>&page=<optional>
```

为安全启用每日自动收录，再新增两步显式授权 API（以下均为**本方案的新接口，不是 Sub2API 既有官方接口**；须沿用现有 admin session/RBAC/audit middleware，不新增独立认证机制）：

```http
POST /api/v1/admin/model-catalog-refresh/policy-preview
Content-Type: application/json
{"account_ids":[101,102]}

POST /api/v1/admin/model-catalog-refresh/policy-opt-in
Content-Type: application/json
{"preview_id":"opaque-short-lived-id","plan_hash":"sha256:...","confirm_account_ids":[101,102]}
```

- Preview 只读、不发上游请求；限定 1–100 个去重精确 account IDs，不接受 wildcard。返回每账号当前 policy、source profile、adapter readiness、snapshot status/age、将增加/保留/隐藏的 canonical ID、mapping 冲突、受影响 group IDs/allowlist 是否匹配，以及不能 opt-in 的明确原因。`manual_only/unsupported`、profile origin/credential 不匹配、alias 未审、目录不完整的账号必须 `eligible=false`。
- Preview 返回随机 `preview_id`（有效 10 分钟）与 `plan_hash`。服务端短暂保存或可验签表达规范化账号集合、每账号更新版本、snapshot generation、source profile revision、alias registry revision、allowlist digest 与到期时间；hash 无法解码/签名校验错误一律拒绝。不得把 credentials、完整 URL、raw body 放入 preview/token。
- Opt-in 必须提交 preview 原选中的完整 `confirm_account_ids` 与匹配的 `preview_id/plan_hash`。服务端必须按稳定顺序锁定所有目标账号及 hash 覆盖的可变 revision 行（account、snapshot generation、source profile、alias registry、group/allowlist），锁定后重新读取并校验；从校验起持锁到整批 commit。所有会改变这些 revision 的写路径必须先取得同一行锁并在同事务推进 revision。若 revision writer 先获得锁并提交，opt-in 随后必须观察到版本变化，整批回滚并返回 `409 stale_preview`；若 opt-in 先获得全部锁，revision writer 必须等待，opt-in 可按锁序先提交，之后 writer 再生效。不得允许“最终校验后、写入前”出现未锁定的并发修改。仅 `SERIALIZABLE` 隔离而没有这些锁/版本约束不满足本契约；普通事务中先读版本再 update 也不合格。对目标账号执行的带旧版本条件更新行数必须等于所选账号数，否则回滚整批；禁止部分 opt-in。任一账号不可 opt-in 时整批拒绝；已是 `follow_upstream` 的账号允许幂等成功，不改配置。
- Opt-in 仅写 account policy 白名单字段为 `follow_upstream`，不改 `credentials.model_mapping`、group allowlist、Unified routes/pricing；在同一事务写入持久 admin audit/outbox event（actor、selected account IDs、plan hash、结果），严禁记录凭证。消费 audit/outbox 或 refresh queue 仅在 commit 后进行且具幂等键；事务失败不得留下授权、成功审计或 refresh job。API 成功后只排队一次 due refresh/复用 shared coordinator，不在 HTTP request 中同步扫全账号。
- Preview/Opt-in 都要求管理员权限并具审计；普通 API key 用户无权调用。若目标 Sub2API admin API 中间件无法提供此授权与审计语义，先复用其对应机制，不允许以公开或无鉴权 endpoint 代替。

响应分为 scheduler summary 和 account rows。summary 最少有 `enabled`、`schedule`、`timezone`、`leader_active`、`last_run_id`、`last_scheduled_at`、`last_started_at`、`last_finished_at`、`last_run_status`、`eligible/succeeded/failed/unsupported/skipped` counts。account row 最少有 `account_id`、`source_profile`、`catalog_policy`、`status`、`last_attempt_at`、`last_success_at`、`next_due_at`、`raw_count`、`canonical_count`、`quarantine_count`、`last_error_kind`、`digest_prefix`。error message 使用固定脱敏摘要；不得返回 key、完整 endpoint/query、上游原文 body 或 credential extra。

account catalog snapshot/freshness 保存在账号 snapshot；最近全局 run summary 存共享 Redis 有界 TTL（30 天）并写结构化日志。Redis 状态读不到时 API 明确返回 `status_source=unavailable` / unknown，不把缺失当成功。现有 `POST /api/v1/admin/accounts/:id/models/sync-upstream` 继续作为管理员单账号 on-demand refresh，必须复用相同 source adapter/校验/快照提交规则，并且 `manual` 策略下不能因此自动开放新模型。

## 7. 兼容与迁移

- 已有手工 mapping 和 group allowlist 原样保留。dynamic follow 是显式迁移，不由一次版本升级自动打开。
- 已展示 bare GPT/旧实验 alias 的客户端会失去这些**非 canonical** ID。按用户“下游只识别正式名”的目标，此为明确 breaking change：实现发布说明，先审计当前 `/v1/models` 与 access logs 命中数；不提供静默 fallback 到另一 GPT variant。
- 在目标来源账号尚未设为 follow-upstream、或现有 group allowlist 不匹配前，不能对用户承诺该组能自动展示新增模型。部署 runbook 必须记录一次性策略切换和预期授权范围。
