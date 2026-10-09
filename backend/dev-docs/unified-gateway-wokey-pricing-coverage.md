# Wokey 全模型线路计价覆盖方案

状态：`PUBLIC_CATALOG_MATCH_PASS_CODE_AUDIT_PASS_LIVE_UI_PENDING_MEDIA_E2E_PARTIAL`

本状态和下文第 1–13 节记录既有手工维护线路价卡的设计/实现及其验收范围；不表示 Wokey 自动目录同步已配置或经过运行时验证。第 14 节记录独立的自动同步附加设计及 2026-10-09 本地代码验收；当前没有写入自动维护参数。

日期：2026-10-08

## 1. 目标和边界

第 1–13 节记录的目标是让管理员通过统一网关线路计价页面，手工为 Wokey 公开目录模型维护用户侧费率，并看清每条价卡是否能命中。费率以当时确认的 Wokey 公开 USD 价格为输入，按暂定汇率 `1 USD = 6.9 星` 换算；本地约定 `1 星 = 1 元人民币`。具体保存到哪条 Wokey 账号线路，仍由 Sub2API 原生 scheduler 先选账号；线路价卡只按实际命中账号和模型匹配。第 14 节的自动同步设计是独立后续增量，不继承下文“手工录入”的旧实现范围。

本方案不增加 Wokey 专属路由、不改模型名/协议/请求/响应、不改原生 scheduler、账号成本或 `total_cost`，不做自动定时拉价、自动汇率更新、利润算法或独立账本。只调整统一网关下游 `actual_cost` 的线路计价匹配和现有管理页。基础设计仍以 `SUB2API-V0213-UNIFIED-GATEWAY-ROUTE-BILLING-DESIGN.md`、`SUB2API-V0213-UNIFIED-GATEWAY-BASE-PRICE-OVERRIDE-DESIGN.md` 为准；本文件只补充 Wokey 当前目录确实无法用现有表单完整表达的部分。

## 2. 基线与已知状态

- 仓库：`D:\AI\SSH\sub2api-official-v0.2.13-20261003`，当前 HEAD `5153f8a8f09949baf54f79667993eb2d52db4ab0`；官方 v0.2.13 源码提交 `3040209f205472038c1ba745a1bedd2edd9053b1` 是其祖先。现有未跟踪文件 `backend/dev-docs/account-delete-scheduler-outbox-atomicity.md` 必须保留。
- 当前运行容器 `sub2api-v0213-404token-app-currenthead-100db` 在 `127.0.0.1:18081` 健康。当前数据库 `sub2api_phase5b_currenthead` 已有 Wokey 图片账号 #27（OpenAI API Key）和视频账号 #28（Grok API Key），均在 `unified-api-internal` 组且账号状态 active、可调度；账号 Base URL 当前为 `https://api.wokey.ai/v1`。组内优先级为 1。#27 的文本映射包含 GPT Luna/GLM-5.3，但不含 `gpt-image-2.5`；#28 不含 `grok-imagine-video-1.5`。当前统一网关线路计价配置没有 #27/#28 的任何条目。
- 隔离容器 `sub2api-wokey-route-pricing-e2e-20261008` 仍连着旧克隆库：其中 #27 disabled、#28 active；克隆库测试 key 的用户已软删除，因而不能用于 E2E（请求返回 `INVALID_API_KEY`）。本次使用当前 18081 的既有测试 key 做下游请求；没有修改任何账号、分组、API key 或费率配置。
- 设计与证据的历史差异：开始本 Wokey 覆盖记录时，基础设计文档与当时 `AGENTS.md` 哈希不一致，且标题状态仍是待修正；检查了实际文件与第 10–12 节证据后，未将旧哈希当作当前版本。之后已按第 11–12 节的实现/运行证据更正基础设计状态，当前哈希记录在 `AGENTS.md`；本历史记录仍保留当时观察，不据此改变实际测试范围或结论。
- 当前公开 `GET https://api.wokey.ai/v1/models/pricing` 返回 48 个 `available=true` 条目：44 个 token 模型、2 个图片模型、2 个视频模型。44 个文本条目带 `pricing_mode=dynamic_discount`；4 个媒体条目没有 `pricing_mode` 字段。公开费率记录是本次查询快照，不等同于具体账号的实际扣费账单。Wokey 文档说明视频按每秒价格乘时长、成功后结算；图像价格按张。

## 3. 最小兼容改动

### 3.1 文本模型基价

- 43 个当前无上下文阶梯价的模型继续使用 token 基价卡：input、output、cache read，以及 Wokey 明确公布的 cache-write SKU。Wokey 当前有 24 个模型只公布 input/output/cache-read，7 个 GPT 模型公布 5 项，13 个 Claude 模型公布 6 项；其中 `gpt-5.5` 属于前一类。基价卡写入 Wokey 当前 `price_usd × 6.9`，单位星/百万 token。input、output、cache read 为必填；cache-write、5m、1h 可留空表示“此卡未覆盖该 meter”。实际 usage 使用任何未覆盖 meter 时，整张卡不适用并完整回退到下一层计价。对明确已覆盖的费率字段，数值 `0` 表示免费；但为了向后兼容，`cache_write_1h=0` 保留为历史“不覆盖 1h 类别”哨兵，UI 必须继续显示并说明这一例外，不能把它解释为免费 1h。若将来需要支持 1h 免费价，必须另增明确的字段状态后再做迁移；本期不支持该配置。不得把未公布费率猜成免费或 input 价。用户侧覆盖范围严格限定在这些公开 SKU 实际覆盖的请求。
- 对 `claude-haiku-5-5` 增加一个最小可选长上下文价档：Wokey 模型页称 `prompts over 100K`，当前价格 API 的 `prompt_tiers[0]` 返回 `above_prompt_tokens=100000` 及六种 meter 的长上下文费率。先通过实际响应 usage 和 Wokey request record 确认门槛计量。若定义为完整 prompt context，则本地计费 token 总量为 `InputTokens + CacheReadTokens + CacheCreationTokens`；该和只用于选档，不改变六种 meter 的分别计价。若不能从实际记录验证缓存 token 是否计入阈值，则该长上下文档不能用于有缓存的请求；即使本地 usage 总量跨过阈值，也按未覆盖回退到原生价格，不能声称已对齐。
- 价档结构和请求决策共用同一不可变配置 revision。预留阶段不得用 `BodyBytes / 4` 的近似值决定最终价档；须将两档中较高的费率纳入现有保守预留候选。成功结算时只依据实际 usage 选择 `>100000` 档。100000/100001 边界测试覆盖无缓存和缓存 tokens 使总 prompt 跨线的情况；若本地 `LongContextBillingApplied` 标记为 true，显式 Wokey 长上下文卡仍须可用，不能误被本地原生模型阶梯价拦截。
- 如果 Wokey 对长上下文阈值的实际计量仍不能由官方字段或同一请求账单证据确定，则不能启用/验收该模型长上下文卡；超阈值模型请求保持现有原生回退并标为未完全对齐，不能用近似倍率伪装匹配。
- 不支持 Wokey 公开费率未列出的动态条件（例如未来新增的时段、服务级别或计费 meter）。如果同一请求记录表明实际账单价格与公开快照不同，则先以同一请求的 Wokey 实际 cost 复核；不静默声称已对齐。

### 3.2 图片模型

- Wokey 当前 `GET /v1/images/models` 列出 `gpt-image-2.5` 与 `grok-imagine-image-2.0`，两者均公布 `/v1/images/generations` 和 `/v1/images/edits` endpoint，公开价均为 `$0.01/张`；按 6.9 换算为 `0.069 星/张`。GPT Image 覆盖 1K/2K/4K；Grok Image 覆盖 1K/2K，所有规格价格相同。Wokey 静态 API 文档的图片段落仍只写 GPT Image，和当前图片模型 API 的两模型目录存在差异；本方案按当前模型 API 记录目录与价格，但两模型仍需通过本地账号实测才算路由可用。
- 为固定每张单价增加明确的 `flat_per_image` 规格模式，和原有精确 size/quality 模式互斥。该模式按组、账号、模型精确匹配，但忽略 size/quality；只允许用于上游价格明确不随这两项变化的模型/账号价卡。前端必须显式标注“按张固定价，规格无关”，不能以空值或通配符隐式实现；精确规格项和规格无关项不得同时保存为同一账号/模型价卡。
- flat per-image 只覆盖本地已有 OpenAI Images API Key/OAuth 实际结算路径。请求的任何字段都不得因计价而改写/补造。若某模型在 Wokey 文档或实际账号上无法通过该路径调用，则标记为路由不可用，不因能录入价格而称为已匹配。

### 3.3 视频模型

- 两个模型各有 480p/720p/1080p 费率，时长 1–15 秒。逐个模型/分辨率手工维护 90 条固定规格价卡易错。
- 在现有管理页增加“按秒费率录入”小表单：管理员录入模型、分辨率和星/秒单价；前端按现有后端配置格式展开为 1–15 秒固定任务价 `单价/秒 × 秒数`，保存时仍通过现有 revision/CAS API 写配置。计算不得提前舍入；提交值、预览值与回读值一致，按最多 9 位小数保存。无需新增后端费率类型或改变结算逻辑。
- 两模型公开费率（USD/秒）当前为：`grok-imagine-video-1.5` 480p `.0056`、720p `.0098`、1080p `.0175`；`grok-imagine-video-1.5-lite` 480p `.0036`、720p `.0054`、1080p `.0252`。对应星/秒为前述数字各乘 `6.9`。前端显示换算结果与展开后 1s/5s/15s 样例，保存数据使用现有星/任务格式。

## 4. 前端操作与账号约束

- 所有费率只能经统一网关线路计价页的人工编辑/保存操作写入；不允许用 SQL、直接数据库更新或手写后台 API 绕过前端完成配置。Token 卡 UI 对无公开 SKU 的 cache-write 项提供显式“不覆盖”状态；仅 input/output/cache-read 为必填费率，任何实际用量落入未覆盖项时必须整笔回退。
- 目标组和账号选择继续沿用现有规则：账号必须已在统一组内且原生调度可用。实现前仅进程启动时加载配置；本次已修改保存 API，使成功保存后单实例立即热发布：对保存和管理页回读使用同一把配置互斥锁，保存锁覆盖读取当前 revision、校验、CAS 持久化和原子发布快照的完整区间；只有 CAS 成功后才发布同一已校验不可变快照，CAS 冲突/失败绝不发布。竞争保存按 revision 顺序串行，旧 revision 不能覆盖新快照；GET 返回的 saved/active revision 必须是同一一致性观察。已开始请求继续使用捕获的旧 revision，后续请求使用新 revision；异步视频继续使用创建时已保存的快照。单实例本地进程内保存立即生效，页面显示 saved revision = active revision，不再需要管理员/助手重启容器。若多副本部署，各实例一致性不在本方案范围内，不能宣称跨实例自动同步。
- 用户今后只需在一个页面添加/编辑 Wokey 模型价格并保存，即可作用于当前单实例，不必让助手直接写后台或重启服务。Wokey 账号创建、启用和加入统一组仍通过 Sub2API 原生账号/分组页面完成。
- 逐模型测试先确认真实账号 provider profile 和请求协议。Wokey 文档声称同一 key/base URL 兼容 OpenAI 与 Anthropic，但不可仅由目录 `available=true` 推定本地某一种 account type 可调度所有模型。若 Claude 需要 Anthropic 消息路径或另一 Sub2API provider account，价卡必须按实际命中的 account ID 分开记录。
- 页面不得隐藏回退：只有完整匹配到线路价卡时才覆盖用户侧 `actual_cost`；不匹配时按现有线路倍率/原生价格优先级回退，并让管理员可辨认价卡没有命中。

## 5. 验收方式

### 代码和 UI

1. Token 卡缺失的 cache-write SKU 不得默认为零：任一实际使用的未覆盖计量项应使整张线路卡回退。分别测试六项已填、非覆盖项零 usage、非覆盖项正 usage、明确零费率，以及旧配置 `cache_write_1h=0` 哨兵仍按“不覆盖”解释；验证 UI 对该 1h 哨兵提示一致。只有将来增加单独状态字段后才允许显式免费 1h，本期不测/不支持。
2. 为 Haiku 阈值增加 100000/100001 选择、余额预留、最终结算、异步快照和配置 revision 测试；使用真实 input/cache usage 总数，且 body-size 估算不能决定最终费档；覆盖 `LongContextBillingApplied=true`。若 Wokey 缓存阈值仍无法证实，缓存 usage 跨阈值必须回退原生价格。
3. 图片规格无关价卡应在 size 缺失/auto/具体规格、quality 缺失/auto/显式值下按张匹配；与精确规格价卡冲突时拒绝配置；请求/响应字节保持不变。
4. 视频前端每个模型/分辨率生成 1–15 秒 15 个固定项；单位换算与边界时长计算精确；原有逐任务价格编辑不回归。
5. 通过浏览器管理页面完成一次 GET→人工修改→PUT→刷新页面回读；确认配置走现有 CAS API、活动 revision 立即更新、没有 SQL 配置路径。保存同时验证并发保存按 revision 串行、CAS 冲突不发布、saved 与 active 读取一致、在途普通请求和异步视频仍使用已捕获的旧 revision。
6. 后端单测、受影响包测试，前端 Vitest、类型检查、生产构建及独立 A2 代码/前端审计通过。

### 本地真实计价验证

1. 先确认测试账号/协议 profile 能通过本地 Sub2API 原生路径调用每一个型号；44 个 token 模型逐个发送一次极小输入/输出请求；两种图像模型各发送一次最低规格/单张请求；两个视频模型各发送一次最低分辨率/最短时长请求。所有调用仅在确认账号与目标组后执行，不自动重试。
2. 对每个成功请求，关联 Sub2API usage 与 Wokey `/v1/requests` 的唯一记录，核对模型、token/图片数/视频时长和费用。文本简单请求验证基础 input/output 卡；缓存费率只在真实产生对应 cache usage 的记录上判定，不能据普通文本请求宣称缓存计价已实测。
3. Haiku 长上下文边界须使用超过 100000 prompt tokens 的请求才可实测。预先说明成本，并另行确认 100K 门槛是否包含缓存 prompt；不把普通短请求或模拟测试写成该边界的线上证据。
4. Wokey 公共价格与实际 request cost 不一致时，以该具体请求的上游 cost 作为实际观察值，查清折扣/计费字段之后才调整卡价。快照配置不被说成永久实时同步。
5. 实际模型调用可能产生费用；上面仅包含每模型一次、最小规格、不重试的验证，不包括压力/矩阵测试。所有上游请求都是本地测试，不触及 VPS 或 GitHub。

## 6. 明确不做

- 不实现自动拉价、定时价目同步、后台隐式配置、FX 服务或自动利润率。
- 不调整模型映射、白名单、API key、统一组倍率、Wokey 账号 key、上游请求内容或协议适配。
- 不把 Wokey 公共目录的 `available=true` 等同于本地线路已成功，也不把价卡保存成功等同于计价已实测。
- 不修改原生 `total_cost`、账号成本、历史账单；不进入 Phase 6 的自造账本，不提交、推送、部署或操作 VPS。

## 7. 2026-10-08 独立设计审计记录

- 两位独立只读 A2 审计者分别返回 PASS；审计记录在当前会话中，未另存文件。通过范围包括 CAS 与并发一致性要求、`cache_write_1h=0` 历史哨兵、Haiku 缓存用量时保守回退，以及仅承诺单实例热更新。
- 这只是设计审计，不是实现或运行验收。当前运行库没有可用于目标路径验收的 Wokey 账号；本次实现不得发送真实 Wokey 请求，实际账号/路由与上游账单对账仍是后续验收条件。

## 8. 2026-10-08 实现与聚焦验证记录

- 实现覆盖可选 cache-write meter、Haiku 长上下文卡及保守缓存回退、规格无关图片价、视频每秒录入展开，以及 CAS 成功后的同版本原子快照发布。缓存写入 aggregate 多于 5m/1h 明细时，仅对剩余量应用通用 cache-write 价；没有通用价则整卡回退。负 token meter 会使自定义 token 卡不匹配，保留现有原生回退。
- 聚焦 Go 测试：`go test ./internal/service -run 'TestUnifiedGateway|TestBuildUnifiedGatewayRoutePricing|TestPrepareUnifiedGatewayRoutePricing|TestApplyUnifiedGateway|TestApplyUnifiedGatewayTokenBasePrice|TestOpenAIRecordUsageAppliesRoute|TestOpenAILongContextBillingMarker|TestUpdateUnifiedGatewayRoutePricing|TestConcurrentRoutePricing|TestRoutePricingDecisionRetains' -count=1 -v -timeout 90s`，exit 0。完整受影响包：`go test ./internal/service -count=1 -timeout 5m`，exit 0（约 142 秒）。
- 前端：`pnpm exec vitest run src/views/admin/__tests__/UnifiedGatewayPricingView.spec.ts`，26/26 通过；`pnpm exec vue-tsc --noEmit` exit 0；`pnpm run build` exit 0，包含 3 项 i18n 完整性测试。构建有既有 Browserslist、动态导入及大 chunk 提示。pnpm 自动改写的 lockfile 仅是本次工具运行副作用，已恢复；未留下 lockfile 改动。
- 两名独立只读 A2 审计者对相同冻结 Go 源码/测试 SHA256 分别复审通过：实现 `1D9917FB7602D3FC207718C983F48844DB5E24774D1B9DDC2324F22ED2ABBD93`，测试 `FA1EF396F16E150AB756C3C23ACAC8E9F2488C968E86308FB6B3C33DDDF5EDDE`。审计覆盖负 meter、缓存余量、Haiku 阈值/缓存回退、native `TotalCost` 不变，以及前端 DTO/GET/PUT/CAS/热更新路径。
- 前端可逐条新增、编辑和保存所有费率类型；模型 ID 是自由文本，没有目录下拉、自动校验或批量导入，因此须照快照逐字填写；页面隐藏于简易管理菜单，需使用完整管理菜单；没有 USD 汇率输入框，须先在页面外按当前汇率换算成星币。保存后仅当前单实例热更新，不做多副本广播。
- 本节是实现完成时的状态快照：当时没有执行真实 Wokey 生成调用或浏览器到运行实例的端到端保存。之后当前库新增/恢复了 Wokey 账号，并在 2026-10-08 做了小样本实测；以第 11 节为后续运行状态。此前没有因此变更代码或替换容器。

## 9. 2026-10-08 Wokey 公共目录核对

- 只读请求 Wokey 公共 `GET /v1/models/pricing` 返回 48 个 `available=true` 模型：44 text、2 image、2 video，共 206 个基础价格 SKU（185 token meter、5 image tier、16 video mode/resolution variants），另有 1 个 Haiku 长上下文 `prompt_tiers` 档。参考文件 `D:\AI\SSH\SUB2API-V0213-WOKEY-PRICING-SNAPSHOT-20261008.csv` 有 53 个费率录入行：44 个文本基础卡、1 个 Haiku 长上下文附加卡、2 个图片模型卡、6 个视频模型/分辨率费率。基础 206 个 SKU 与 CSV 公开 USD 价格逐项匹配，差异数为 0；Haiku 长上下文卡另外按 `prompt_tiers` 校对；金额按暂定 `USD × 6.9 = 星` 换算。
- 两种图片模型的当前所有规格均为 `$0.01/image`，即 `0.069 星/image`；两个视频模型在相同分辨率下各模式单价相同，按 `USD/second × 6.9` 录入并生成 1–15 秒固定价格。当前比对证明的是公开目录价格映射一致，不证明某 Wokey 账号的实际 API 扣费已经相同。
- `claude-haiku-5-5` 的 `prompts over 100K` 费档由 [Wokey 模型价目页](https://wokey.ai/models) 与当前价格 API 的 `prompt_tiers[0]` 给出：input `$0.10/M`、output `$0.50/M`、cache read `$0.01/M`、generic cache write `$0.20/M`、5m `$0.125/M`、1h `$0.20/M`，均已按 6.9 换算进 CSV。当前缓存 token 是否计入 100K 阈值仍缺少同一请求账单证据，所以代码对跨阈值且带缓存的请求整卡回退原生价；表中已公开的缓存费率准确记录，但本地不会对该未证实的缓存长上下文场景宣称已匹配。
- Wokey 文档说明当前模型/价格目录可查询，并且 `/v1/requests` 返回 request id、model、tokens 和 cost；也说明不同 cache path/upstream route 可能有不同计费。因此还需用真实同一请求的 Sub2API 账单和 Wokey request record 对照，才能把“公开价目匹配”升级为“实际扣费准确”。详见 [Wokey API 文档](https://wokey.ai/docs)。

## 10. 2026-10-08 隔离运行尝试

- 为在不替换 `127.0.0.1:18081` 的情况下验证本次代码，已从历史 Wokey 测试库复制出隔离数据库 `sub2api_wokey_route_pricing_e2e_20261008`，并启动 `sub2api-wokey-route-pricing-e2e-20261008`，地址 `http://127.0.0.1:18082`，镜像 `sub2api:local-linefactor-20261008`，使用独立 Redis DB 14 和单独 `/app/data` 目录。18081 与源测试数据库未改动；隔离副本中的 Wokey 图片账号 #27 仍是 disabled、视频账号 #28 为 active。当前没有在隔离实例保存价卡，也没有发真实 Wokey 生成请求。
- 已确认管理页面路由为 `/admin/unified-gateway/pricing`，前端能够逐条编辑并一次保存 token、图片、视频价卡；视频按秒助手可为一个模型/分辨率生成 1–15 秒记录。页面没有通用 CSV 批量导入、模型目录下拉或自动汇率输入，模型 ID 需照快照逐字填写，USD 价格需先在页面外乘以 6.9。代码级前端测试与 DTO/CAS 源码审计通过，但这不等于运行中的真实浏览器保存验收。
- 本轮尝试启动本地浏览器自动化时，Cua 与 Node REPL 工具均返回 kernel assets 路径不存在；收到用户授权后再次尝试启动独立 Chrome 调试会话，仍被执行环境策略拒绝，未返回更细原因。因此不能完成管理员页面点击、保存后刷新回读，或用真实 Wokey 请求关联 `/v1/requests` 与本地 usage；已授权的请求额度没有使用。测试环境已留在 18082 供后续继续操作。
- 目前通过的是：公开目录 48 个模型、206 个基础 SKU、Haiku 长上下文额外费档到 53 行 CSV 的价格/规格映射，差异为 0；代码与组件测试、类型检查、构建和独立 A2 审计通过。尚未通过的是：全量价卡在隔离前端持久化、逐模型实际路由可用性、上游真实 cost 与本地下游 `actual_cost` 对账，以及 Haiku 缓存 usage 是否进入 100K 门槛。故当前只能说“公开价目映射准确、实现具备录入能力”，不能说“48 个模型已在实例中配置并且实际扣费已验证准确”。

## 11. 2026-10-08 代表性实测复核

### 11.1 配置快照

- 测试目标是用户侧真实扣费价格，不涉及 `account.rate_multiplier`（它是账号成本统计口径，不改变用户 `actual_cost`）。当前统一组文本倍率为 `1.0`，图片/视频独立倍率均为 `1.0`；Wokey 账号 #27/#28 的统一网关线路价卡数为 **0**。
- 统一组图片默认价仍为 1K=`0.02`、2K=`0.07`、4K=`0.07` 星/张；视频价卡为空，原生 `grok-imagine-video-1.5` 回退价为 480p=`0.08`、720p=`0.14`、1080p=`0.25` 星/秒。上述值不等于 Wokey 全部规格的人民币折算价。
- 当前公开价格 API 的 Wokey 文本费率是动态价，页面说明以每个请求处理时的价格结算；所以本节只证明测试时点，不承诺长期费率不变。注意静态 GLM-5.3 页面仍展示旧快照 `$0.56/$1.76`，而本轮实时价格 API 与同日账单都为 `$0.28/$0.88`；遇到冲突以请求时价 API 和该请求账单为准。参考：[Wokey 实时价格 API](https://api.wokey.ai/v1/models/pricing)、[Wokey GPT-5.6 Luna 目录](https://wokey.ai/models/gpt-5.6-luna)、[Wokey GLM-5.3 目录](https://wokey.ai/models/glm-5.3)、[Wokey API 文档](https://wokey.ai/docs)。

### 11.2 结果

| 样本 | 实测路径与证据 | 结论 |
|---|---|---|
| GPT-5.6 Luna 文本 | Wokey 直连 `/v1/chat/completions` 成功，usage 为输入 21 / 输出 5；Wokey request `20261008115904nfdjk` 为 succeeded，`actualAmount=$0.000006`。当前价格 API 为 `$0.12/$0.72` 每百万输入/输出 token；按未舍入公式为 `$0.00000612`，账单按 meter 舍入后显示 `$0.000006`。统一网关请求选中账号 #27，两次本地访问都返回 404：`Route POST:/v1/v1/messages not found`；Wokey 没有对应失败计费记录，本地无成功 usage。 | Wokey 公开费率和账单一致（微小请求的账单金额按 USD 小数位舍入）。统一网关真实计费尚未验收；现有上游协议/路径配置未能成功调用，不能把 404 归因于价格倍率。 |
| GLM-5.3 文本 | Wokey 直连成功，usage 输入 17 / 输出 8；request `202610081159320i1ra` 为 succeeded，`actualAmount=$0.000012`。当前价格 API 为 `$0.28/$0.88` 每百万输入/输出 token；未舍入公式为 `$0.0000118`，账单按 meter 舍入后为 `$0.000012`。统一网关尝试返回 503 `No available accounts`；Wokey 同时有失败 request `202610081159202udff`、金额为零，本地没有成功 usage 记录。 | Wokey 公开费率和其请求记录一致。统一网关调用/扣费未验收。 |
| GPT Image 2（本地 YeToken） | 统一网关 `gpt-image-2` 成功，Sub2API usage row #429 记录账号 #18、1 张 1K、`actual_cost=0.02` 星；命中 YeToken #18 的既有 1K=`0.02` 价卡。 | 这证明 YeToken 图片路径的当前本地收费是 0.02 星/张；它不是 Wokey 链路，不能代表 Wokey 的 GPT Image 2.5。 |
| GPT Image 2.5（Wokey） | Wokey 直连 `/v1/images/generations` 成功生成 1 张；request `202610081201489gtjz` 为 succeeded，`actualAmount=$0.010000`。请求 1024×1024，实际输出档识别为 2K，但 Wokey 账单仍为 `$0.01/张`。本地统一网关模型 `gpt-image-2.5` 返回 404 `Model ... is not supported by any configured account in this group`；本地没有 Wokey usage。 | Wokey 实际按张价格与公开目录一致。当前本地没有暴露/映射这个准确模型 ID，不能验收本地 Wokey 计费。按汇率 6.9，Wokey 用户侧目标价为 `0.069` 星/张，各分辨率相同。 |
| Grok Imagine Video 1.5（Wokey） | Wokey request `20261007115740ry0al` 与 `202610070940175vz0w` 均为 succeeded：各 5 秒、720p、16:9，账单各 `$0.049`。当前公开价 `$0.0098/秒 × 5 = $0.049`。统一网关发起 1 秒 480p 请求返回 503 `No eligible Grok media accounts`；本地无 Wokey usage。 | Wokey 实际视频账单与公开每秒价一致。当前统一网关没有可调度的视频模型路径，不能验收本地 Wokey 计费。 |

### 11.3 价格与倍率判断

- Wokey GPT-5.6 Luna 当前实际美元单价按 `USD × 6.9 = 星`，无利润时线路倍率设 `1.0`，线路基价卡应填输入 `0.828`、输出 `4.968`、cache read `0.0828`、cache write `1.035` 星/百万 token（5m cache write 也是 `1.035`；未公布的 1h 不录入）。
- Wokey GLM-5.3 当前线路基价卡应填输入 `1.932`、输出 `6.072`、cache read `0.3588` 星/百万 token；Wokey 未公布独立 cache-write SKU，不应猜成零。线路倍率设 `1.0`。
- Wokey `gpt-image-2.5` 应按 1K/2K/4K 分别录入 `0.069` 星/张，图片倍率 `1.0`。统一组现有 1K=`0.02` 与 Wokey 不一致，2K/4K=`0.07` 与 `0.069` 接近但不等于 Wokey 精确价。
- Wokey `grok-imagine-video-1.5` 按 `USD/秒 × 6.9` 的目标星/秒价为 480p=`0.03864`、720p=`0.06762`、1080p=`0.12075`。当前 Sub2API 原生默认是 `0.08/0.14/0.25` 星/秒；在相同用量下分别约为 Wokey 目标价的 `2.07×`。线路视频价卡应按实际成功任务规格保存，视频倍率 `1.0`。
- 仅作费率基准比较：Sub2API 原生 GPT-5.6 Luna 兜底为输入/输出 `0.2/1.2` 星/百万 token；GLM-5.3 为 `1.4/4.4`。若未设置 Wokey 价卡且请求成功，这些兜底价分别低于 Wokey 汇率折算价；但本次统一网关的两条 Wokey 文本调用均未成功，所以这些只是配置推导，不是本地下游扣费实测。

### 11.4 验收边界与未完成项

- 本轮真实 Wokey 成功请求为 GPT Luna、GLM-5.3、Image 2.5；Grok 视频实际账单证据来自先前同一 Wokey key 的两条成功请求。Wokey 账单记录与当前公开费率相符。
- 本轮通过统一网关成功的媒体请求只有 YeToken `gpt-image-2`，收费 `0.02` 星/张。Wokey GPT Luna 的两次统一网关访问均失败 404；Wokey GLM-5.3 请求失败 503；Wokey Image 2.5 在本地模型解析阶段失败 404；Wokey Grok 视频在可调度账号筛选阶段失败 503。失败请求没有成功的 Wokey 计费记录。故“Wokey 公开价准确”已验证，但“统一网关实际扣费与 Wokey 对齐”**未通过/尚未验证**。
- 本轮没有改代码、账号、模型映射、分组、路由计价配置或数据库；没有替换运行镜像，也没有接触 GitHub/VPS。只有测试生成请求可能产生的上游用量：Wokey GPT/GLM/Image 直连账单约 `$0.010018`，另有 YeToken `gpt-image-2` 本地请求扣 `0.02` 星；失败请求 Wokey 账单为零。
- 后续应先通过 Sub2API 管理前端修正 Wokey OpenAI 账号的协议/URL 路径、补齐 `gpt-image-2.5` 与 `grok-imagine-video-1.5` 的可调度模型映射，再通过管理前端为 #27/#28 写入上述线路价卡；保存后才能用同一 API key 做本地成功请求并逐条关联 Wokey request record。此前隔离副本 key 属于已删除测试用户，需重建有效测试 key 后才能用于隔离 E2E。不得把本节的 Wokey 直连结果写成本地 gateway E2E 成功。

## 12. 2026-10-08 Wokey 媒体链路配置修正与复测

本节更新第 2、10、11 节中“账号未配置/本地媒体路由未验收”的历史状态。配置和测试限于本机 `127.0.0.1:18081`；没有修改源码、GitHub 或 VPS，也没有变更现有用户 key、分组或模型白名单。

### 12.1 已应用配置

- Wokey 图片账号 #27 与视频账号 #28 的 Base URL 均设为 `https://api.wokey.ai`，使用 Wokey 文档建议的根地址，避免客户端重复拼接 `/v1`。
- #27 的原生模型映射包含 `gpt-image-2`、`gpt-image-2.5`；#28 包含 `grok-imagine-video-1.5`。`unified-api-internal` 的原生模型路由分别指向 #27、#28。账号仍为 active、schedulable。
- 统一网关线路价格保存修订为 4，并由运行中实例加载：#27 的两个 GPT Image 模型按 1K/2K/4K 各 `0.069` 星/张；#28 的 Grok Video 1.5 在 480p、5 秒规格为 `0.1932` 星/任务。配置通过本地管理 API 保存；浏览器 UI 点击/回读本轮仍未实测，不能把它记作前端操作验收。

### 12.2 实际调用结果

| 测试 | 本地响应与路由证据 | Wokey 账单 | 结论 |
|---|---|---|---|
| Grok Imagine Video 1.5，5 秒、480p、16:9 | 统一 key 创建任务返回 202，轮询完成；本地 usage row #430 记录账号 #28、1 个视频、5 秒、480p，用户 `actual_cost=0.1932` 星、原生 `total_cost=0.4` | Wokey request `20261008132205abvyi` 为 succeeded，实际扣 `$0.028`；`0.028 × 6.9 = 0.1932` | **通过。** Wokey 视频请求经统一网关成功，用户侧固定费率与同一请求上游扣款精确相等；`total_cost` 维持原生统计口径。 |
| GPT Image 2.5，1024×1024、medium，三次实际到达上游 | 三次请求均被本地路由到账号 #27 并到达 Wokey；上游 HTTP 502。错误响应原文为 `Our servers are currently overloaded. Please try again later.` | 请求 `20261008132041l9b8p` 与 `20261008132715jyf0z、202610081342354hhlf` 均为 failed，`server_is_overloaded`，`actualAmount=0` | 路由、模型名和上游 endpoint 已匹配；生成未通过，原因是 Wokey 持续返回过载，失败请求没有上游收费，也没有本地成功 usage。 |
| 图像请求在 30 秒冷却期后重试 | 两次同进程重试分别返回 503 `No available compatible accounts`；日志显示 #27 未进入候选池，其余 14 个 OpenAI 账号都因不拥有该模型被过滤。数据库中 #27 仍 active/schedulable、模型映射存在，且 `temp_unschedulable_until` 已早于当前时间。第二次重启后等待超过 300 秒，同一进程的后续请求重新选中 #27 并到达 Wokey | 两次本地 503 未到达 Wokey、无 usage；超过 300 秒后 Wokey 记录再次出现零费用 overload | 发现临时停调到期后，同一进程的候选快照暂时未恢复 #27；重启会立即重建快照。当前容器没有设置覆盖项，源码默认全量快照重建间隔为 300 秒；这轮同进程重试发生在 300 秒周期内并收到 503；超过该周期后，同一进程重新选中账号，验证了周期性快照重建可恢复调度。本轮没有关闭冷却或修改调度代码。 |

### 12.3 结论与边界

- **视频：**Wokey 经统一网关成功生成并完成实际扣费对照，模型映射、路由、异步状态查询和 6.9 汇率后的用户扣费均通过。
- **图像：**已证明统一网关能够把原模型名和请求送到 Wokey 正确 endpoint；这三次 Wokey 上游均报告过载且计费为零。因此当前不能宣称 Wokey 图像生成已通过，也不能把这三次错误归因于 Sub2API 请求格式。Wokey 曾有另一条直连成功且扣 `$0.01` 的历史账单，但它不是本地统一网关成功 usage，不能替代本地验收。
- **调度恢复：**短暂的 Wokey 图像过载会触发原生 30 秒临时停调；同进程在 300 秒全量快照重建周期内重试时返回无可用账号，超过该周期后，同进程调用重新选中 #27 并到达 Wokey。最后一次测试冷却到期后，我重启了 18081；当前实例健康，#27 active/schedulable 且冷却已过期。此现象表现为周期性调度快照延迟，本轮没有关闭冷却或修改原生调度代码。
- **本轮代码变更：**无。未运行代码测试；执行的是本机 API E2E 与 Wokey 账单核对。历史工作区已有改动均保留。

## 13. 2026-10-08 Wokey 文本模型复测

测试对象是当前运行的本机 `http://127.0.0.1:18081`，使用现有专用 API key #816；没有改代码、账号映射、模型路由、分组、价格配置、GitHub 或 VPS。测试请求产生本地用量记录 #431、#432。

### 13.1 当前模型与价格配置

- Wokey 当前 `GET /v1/models` 返回 44 个文本模型；本地统一网关 `GET /v1/models` 返回 74 个模型，其中包含这 44 个 ID。Wokey #27 映射了 43 个文本模型，#28 也映射了同样的 43 个文本模型；两者都缺 `claude-haiku-5-5`。该模型在统一网关列表中出现，来自其他账号，不表示 Wokey 链路已配置。
- 线路计价设置仍为 revision 4；Wokey #27/#28 有 7 个媒体条目，但 **0 个文本 token 价格卡**。Wokey 文本请求因此没有按 Wokey 单模型费率换算为星，仍使用本地原生价格回退。Wokey 官方说明其价格按处理请求时的实时目录费率结算，价格可能动态变化；目录显示可用不等于逐模型生成已经通过。[API 文档](https://wokey.ai/docs) [实时价格 API](https://api.wokey.ai/v1/models/pricing)

### 13.2 统一网关实际请求

| 模型 | 本地结果 | 同一请求的 Wokey 账单 | 结论 |
|---|---|---|---|
| `gpt-5.6-luna` | HTTP 200，账号 #27，usage #431；本地记录输入 0、输出 5、`actual_cost=0.000006` 星。网关请求先经历多个账号 403/502，再由 #27 成功。 | Wokey request `20261008135858m73kl` succeeded，输入 21、输出 5，扣 `$0.000006`；请求时费率输入/输出 `$0.12/$0.72` 每百万 token。按 6.9 换算，账单目标约 `0.0000414` 星。 | **生成通过，计价不通过。** 本地漏记 21 个输入 token；小金额的美元账单舍入导致原始数字偶然相同，不能算对齐。 |
| `grok-4.5` | HTTP 200，账号 #28，usage #432；本地记录输入 0、cache read 0、输出 219，`actual_cost=0.001314` 星。请求指定 `max_tokens=8`，但实际输出 219 token。 | Wokey request `20261008140409w3wdk` succeeded，输入 1,935（其中 cache read 384）、输出 219，扣 `$0.000629`。账单费率为未缓存输入 `$0.28/M`、cache read `$0.028/M`、输出 `$0.84/M`。×6.9 后目标为 `0.0043401` 星。 | **生成通过，计价不通过。** 本地金额只有上游人民币折算目标的约 30.3%；usage 还漏记全部输入与缓存读取。输出上限也没有在端到端结果中生效，需另查是适配层还是上游行为。 |
| `glm-5.3` | HTTP 503，耗时约 176ms，无本地 usage。网关日志将请求归到 `zhipu` provider，并返回 `No available accounts`；没有请求到 Wokey。 | 本次没有 Wokey 账单。此前 Wokey 直连成功记录 `202610081159320i1ra` 只证明上游直连可用，不替代这次统一网关测试。 | **本地路由不通过；不能据此判定 Wokey 上游失败。** 当前运行实例没有把这条 OpenAI 兼容的 Wokey 模型映射送到 #27/#28。 |

### 13.3 验收结论与影响

- 当前文本模型验收为 **不通过**：目录列出 44 个可用文本模型，统一网关列表也包含 44 个 ID；本轮只做了 3 个模型的本地生成测试，其中 2 个通过生成、1 个在本地无可用账号。其余 41 个没有逐项做真实生成，不能标记为已验收。
- 价格准确性为 **不通过**：Wokey 文本卡数量为 0；两个成功样本的本地 usage 均未记录输入 token，Grok 样本还漏记 cache read。没有完整 usage 与 Wokey 费率卡，不能把现有金额称为上游真实价对齐。
- 本轮两条成功请求本地合计记录 `0.001320` 星；对应 Wokey 账单合计 `$0.000635`，按 6.9 折算为 `0.0043815` 星。GLM-5.3 503 没有产生上游请求或费用。
- GPT-Luna 请求在原生 failover 中遇到多个 403/502。测试结束后的只读检查显示账号 #8、#11、#13、#20 仍是 active/schedulable，但处于临时不可调度状态，预计于 `2026-10-08 14:08:38–14:08:43 UTC` 自动到期；本轮没有手工清除冷却。当前 18081 健康检查返回 200，Wokey #27/#28 仍 active。
- 不再对其余文本模型发同类本地请求，直到路由和 usage 计费缺口处理好：当前已验证的差异是系统性配置/计量缺口，重复发送无法让计价通过，还会重复触发原生 failover。下一次验收需先确保跨 provider Wokey 模型能落到正确账号，并使本地 usage 含输入与缓存 token；随后为 Wokey 每模型建立按请求时价维护的完整 token 价卡，再做覆盖测试。

## 14. 2026-10-08 Wokey 公开价格目录同步适配层设计

本节首段记录 2026-10-08 的设计授权与当时状态；当前实现验收见下方 2026-10-09 补记。适配层从公开 GET /v1/models/pricing 取价，USD 乘可配置静态 FX（默认 6.9 星/USD）写入现有线路价格卡。不改线路倍率；无利润原价下发需管理员在现有页面把 Wokey 线路倍率和统一组/用户倍率设为 1.0。这里只同步公开价目，不定时查询 /v1/requests 实际扣费；之前各节“尚未自动导入价目”和“文本 usage 漏记 input/cache token”的证据仍是对应历史时点的事实。

**2026-10-09 实施核验补记：**实现已进入本地工作树，实施任务书与最终代码增量的独立 A2 审计均通过。Wokey service 与线路价卡测试、管理 handler 测试、server 编译检查、前端目标测试 32/32、typecheck 和 production build 均通过；`git diff --check` 通过。测试使用 fixture/fake fetcher，没有调用真实 Wokey 价格接口执行写入，也没有配置运行中的账号/FX/价卡或发起生成请求。因此这只证明适配代码和本地界面通过定向验证，不证明线上目录同步正常、账号模型可用或本地费用与 Wokey 最终实扣对齐。该能力仍默认关闭，后续需管理员在页面显式启用并另行验收。

设计文件：[Wokey 公开价格目录同步适配层开发设计](D:/AI/SSH/SUB2API-V0213-WOKEY-PRICE-SYNC-ADAPTER-DESIGN.md)。实施文件：[Wokey 公开价格目录同步适配层实施任务书](D:/AI/SSH/SUB2API-V0213-WOKEY-PRICE-SYNC-IMPLEMENTATION-PLAN.md)。第一期仅同步现有卡能精确表达的静态 token、Haiku 长上下文、同价图片 tier 和同价视频 variants；DeepSeek time_of_day 峰谷价、变价视频 mode/quality 与不完整 usage 不宣称覆盖或实扣对齐。缺失或过期项保留最后成功价并显示待复核，不自动删卡、不拒绝请求、不改路由。实施任务书审计、代码审计和本地代码/前端测试已完成；后续若要实际启用，仍须通过前端显式填写/确认汇率与账号并另行验收代表模型。

### 14.1 公开目录接口结构探测（2026-10-08）

对 Wokey 官方文档所列的三个只读接口发起无认证 GET：`/v1/models/pricing`、`/v1/images/models`、`/v1/videos/models`。三者均返回 HTTP 成功，响应根字段为 `object` 和 `data`；本次快照分别返回 48 个定价目录模型、2 个图片模型、2 个视频模型。数量只代表本次快照，不可硬编码。定价模型含 `id`、`currency`、`pricing_mode`、`pricing_skus`；图片目录含支持参数和按张分档；视频目录含 generation modes、resolution、duration 范围及按秒 SKU。三接口当前均可不带 Sub2API 中的上游 key 读取。字段类型实测：公共价格目录中的 `pricing_skus[].price_usd` 为十进制字符串（例如 `"2.390000"`）、`quantity` 为整数；图片和视频规格端点的 `price_usd_per_image`、`price_usd_per_second` 为 JSON number。设计要求按各端点类型做严格解析并统一转十进制，避免浮点误差或字段类型导致整批误失败。

截至本次快照，`gpt-image-2.5` 图片目录列出 1K/2K/4K 三档且公开价相同；`grok-imagine-video-1.5` 有 10 个 mode/quality/resolution SKU，视频价格随分辨率而不同，需要先按每个分辨率核对所有 mode/quality 后，才能折叠到现有“分辨率＋时长”价卡。上述是有日期的价目观察，不是参数配置，也不代表未来价格不变。官方接口定义见 [Wokey API 文档](https://wokey.ai/docs)。

公开定价接口目前未返回 total count 或 pagination 完整性标记。因此合法响应中缺少既有 SKU 时，无法仅凭本接口判断其已下架还是服务端列表变化；设计要求保留 last-good、标记待复核，不自动删除、拒绝请求或切换路由。

### 14.2 适用范围与线路隔离

Wokey adapter 配置默认关闭、账号范围默认为空；未启用或无校验通过账号时不发 HTTP 请求，也不启动刷新周期。只处理管理员明确选中且 Base URL 精确为 `https://api.wokey.ai` 的现有账号价卡。管理卡的模型 ID 来自公开价格目录；这只新增下游费率卡，不证明该账号 key 有该模型权限，也不创建/改变映射、白名单或路由。价卡按目标统一组、账号 ID、模型 ID 和规格精确键控。原生 Sub2API provider、其他非 Wokey provider、未选中的 Wokey 账号、同名模型的其他账号、人工价卡和其他分组均不读取其上游目录、不改写其计价字段，继续采用原有手工线路价或 Sub2API 原生价格。定时触发器只刷新 Wokey 的公开价格，不影响任何请求的路由、账号选择、协议或结算路径。关闭同步只停止后续拉价，已发布价卡保持原状并沿用现有计价。

未来若接入其他“一个 key 聚合多个模型”的上游，只复用“公开目录价格映射到账号级线路卡”的设计原则；必须另做该 provider 的 API/计价审查、专属解析器和开关、字段/账号隔离测试与独立审计，不自动复用 Wokey endpoint、汇率或 SKU 规则。隔离不变量见[适配层开发设计](D:/AI/SSH/SUB2API-V0213-WOKEY-PRICE-SYNC-ADAPTER-DESIGN.md)，精确实现与测试门见[实施任务书](D:/AI/SSH/SUB2API-V0213-WOKEY-PRICE-SYNC-IMPLEMENTATION-PLAN.md)。

## 15. 2026-10-09 本地运行时同步与扣费复核

在修复图片目录 `sizes` 数组解析后，活动容器 `sub2api-v0213-404token-app-currenthead-100db` 运行镜像 `sub2api:local-wokey-sync-20261009-fixed`，`/health` 返回 200。保留原本地数据库挂载。Wokey 同步配置在统一网关价卡中保存为：启用、账号 #27/#28、FX `6.9` 星/USD、间隔 `60` 分钟。镜像启动后的目录同步成功：262 张受管价卡、0 张人工冲突、3 个不支持的时段价 SKU、0 个未返回 SKU；最后成功时间 `2026-10-08T17:42:48Z`。`gpt-5.6-luna` 文本卡为输入 `0.828`、输出 `4.968` 星/百万 token；`gpt-image-2.5` 为 `0.069` 星/张；Grok Video `720p` 为 `0.06762` 星/秒。上述卡片均来自 Wokey 当前公开价格目录乘以 6.9。

| 统一网关请求 | Wokey request record | 本地 Sub2API usage | 对照 |
|---|---|---|---|
| `gpt-5.6-luna`，账号 #27 | `20261008175155mw3dm`，成功；21 输入、5 输出，账单显示 `$0.000006` | usage #433，成功；输入记为 0、输出 5；`actual_cost=0.00002484` 星 | **计价不通过。** 按 Wokey 账单显示金额乘 6.9 是 `0.0000414` 星；本地少 `0.00001656` 星。按价卡和 Wokey 返回的 21/5 token 计算则为 `0.000042228` 星。同步价卡正确，但 Sub2API 这次响应/用量记录遗漏了 21 个输入 token，造成用户少扣。 |
| `gpt-image-2.5`，1024×1024，账号 #27 | `20261008175321k52bp`，成功；图像账单 `$0.010000` | usage #434，成功；1 张、1K；`actual_cost=0.069` 星 | **通过。** `$0.01 × 6.9 = 0.069` 星。 |
| `grok-imagine-video-1.5`，5 秒、720p，账号 #28 | `2026100817543018ptl`，成功；视频账单 `$0.049000` | usage #435，成功；5 秒、720p；`actual_cost=0.3381` 星 | **通过。** `$0.049 × 6.9 = 0.3381` 星。 |

三个本地请求合计 `actual_cost=0.40712484` 星；用户 #444 余额从 `10.00000000` 降到 `9.59287516`，差额与三条本地 `actual_cost` 精确相等。用户面板代码以 `actual_cost` 展示本次扣费/实际消费，以用户 `balance` 展示余额；因此面板与本地扣款记录一致。文本请求的本地输入 token 缺失使本地扣费未与 Wokey 实扣对齐，故不能把“面板与本地余额一致”表述成“所有上游实际费用均已对齐”。`total_cost` 仍按原生标准成本口径记录，本轮不改其逻辑。

本次验证证明同步已开启、启动时公开目录读取和价卡写入成功，以及图片/视频按次价格准确；文本价格卡正确但输入 token 计量链路不准确。配置中的 60 分钟间隔已保存，但本轮观察窗口没有经过完整的第二个 60 分钟周期，未把定时器第二次触发记作运行时 PASS。浏览器自动化本轮因 CUA kernel 初始化失败不可用，前端验收核对了源码绑定（用户余额取 `user.balance`，实际费用取 `actual_cost`），没有截图级视觉验收。没有改 VPS、GitHub、账号密钥、模型映射或代码；真实请求仅上述 3 次。

Wokey 请求记录接口为官方文档列出的 `GET /v1/requests?page=1`，它返回当前 API key 的模型、token、状态与实际金额；本次账单对照来自该接口，不是价格卡反推。[Wokey API 文档](https://wokey.ai/docs)


### 15.1 2026-10-09 GPT-5.6-Luna 用量与计费复测

2026-10-08 的 usage #433 是历史失败样本：请求由 Wokey 成功处理，但当时输入 token 没有进入本地 usage，故不能作为当前验收结论。复核发现当时请求未命中 Composite 显式路由：组内 OpenAI 账号与 Grok 账号 #28 的 `model_mapping` 都声明了 `gpt-5.6-luna`，解析器按既有契约将跨平台歧义 fail-closed，导致请求落入通用 Anthropic handler。独立代码审计不建议把所有可识别前缀都改成自动覆盖账号映射；本次采用已有配置能力，以精确 Composite 路由解决这个模型的归属冲突。

主本地组 #16 新增精确规则 `gpt-5.6-luna -> openai`（endpoint `any`，上游模型名不改）；未把模型固定到某个账号，OpenAI 账号仍由 Sub2API 原生调度。新镜像 `sub2api:local-gpt-luna-usage-retest-20261009` 已替换本地 `127.0.0.1:18081` 实例并通过健康检查；旧容器以 `...-rollback-20261009` 名称停止保留。隔离测试端口 18084 使用克隆库和独立 Redis DB 14。VPS/GitHub 未触及。

主实例一次最小 Chat Completions 请求由账号 #27 `Wokey-Image-OpenAI` 成功处理：HTTP 200，响应 usage 为 23 输入/5 输出；本地 usage #436 同样记录 23/5，端点均为 `/v1/chat/completions`，`actual_cost=0.000043884` 星。Wokey `GET /v1/requests?page=1` 对应记录 `20261008195958cwcrz` 为成功、23/5、实际扣款 `$0.000006`，`usageSource=upstream`。同步卡的输入/输出价为 `$0.12/$0.72` 每百万 token，即 `0.828/4.968` 星；按精确 SKU 价和 FX 6.9 计算，本地费用 `(23×0.828 + 5×4.968)/1,000,000 = 0.000043884` 星。

Wokey 的该小额账单按费用项舍入到 `$0.000001`：输入 `$0.000003`、输出 `$0.000003`；显示金额换算为 `0.0000414` 星，与本地未舍入的 SKU 计算相差 `0.000002484` 星（本地高 6%）。这是微小账单的上游美元舍入差，不是 token 遗漏。为降低舍入对比影响，克隆库同镜像、同 Wokey 账号再做一笔 1577 输入/5 输出样本：本地 usage #445 `actual_cost=0.001330596` 星；Wokey 记录 `2026100819560951ccc` 成功记录 1577/5、`actualAmount=$0.000193`，按 6.9 为 `0.0013317` 星，差 `0.000001104` 星（约 0.083%），来自 Wokey 按费用项保留 6 位美元小数。两笔记录均使用同一公开 SKU 价格，较大样本与上游显示金额在其舍入精度内接近。

**结论：**此次用量漏记已在新镜像和主实例复测通过；本地响应、Sub2API usage 与 Wokey 上游账单的 token 数一致。用户侧 `actual_cost` 按 Wokey 价卡与 6.9 汇率计算；`total_cost` 仍按 Sub2API 原生价格记录，两者职责不同且未改原生成本逻辑。主组的精确线路规则已补齐；本次没有修改通用歧义处理代码。先前短账单的美元舍入差不应被误读为倍率错误。

### 15.2 2026-10-09 Wokey 价格同步稳定性与 Grok 用量复核

本轮没有改同步器生产逻辑。只为 `unified_gateway_wokey_price_sync_test.go` 增加并发刷新后管理员关闭保护、CAS 冲突后重读最新管理员汇率/手工卡并验证持久 revision、HTTP 非 2xx/重定向/超大响应/超时/TLS 失败、重复模型/SKU/币种/负数与非有限价格拒绝、图片/视频目录错价拒绝、视频 1 秒/15 秒边界、空/无效账号范围零请求零定时器，以及 interval 更新和 last-good 保留测试。`go test ./internal/service -run 'Wokey' -count=1 -timeout 5m` 与 `go test ./internal/handler/admin -run 'Wokey' -count=1 -timeout 2m` 均通过；`git diff --check` 通过。独立 A2 审计确认未发现同步器生产逻辑越界或明确缺陷，但仍判 `INSUFFICIENT_EVIDENCE`：其结论发生在上述后续测试加入之前，需对当前树再复核；`-race` 受 `CGO_ENABLED=0` 限制未跑。

**真实周期 tick 补记：**主容器 `sub2api-v0213-404token-app-currenthead-100db` 未重启，第二个 60 分钟周期刷新于 `2026-10-08T20:59:41.250019128Z` 成功，状态 revision 从 12 增至 13；`last_attempt_at=last_success_at`，managed card count 仍为 262、错误码为空、目录 SHA256 与启动同步相同，健康状态仍为 running。此证据补齐“真实周期触发”这一项；不证明未来每次刷新都成功，也不证明实际 usage/账单正确。该运行状态仍只应用 Wokey 显式账号/价卡范围。

| 统一网关请求 | Wokey request record | 本地 Sub2API usage | 对照 |
|---|---|---|---|
| `grok-4.5`，账号 #28，`/v1/responses`，usage #442 | 对应记录：输入 501（含 cache-read 384，普通输入 117）、输出 28，金额 `$0.000067` | 输入/cache-read 均为 0、输出 28；`actual_cost=0.000162288` 星 | **计价不通过。**按 Wokey 账单金额乘 6.9 为 `0.0004623` 星；本地少 `0.000300012` 星。按公开 SKU 未舍入费率计算为 `0.0004625208` 星。 |
| `grok-4.5`，账号 #28，入站 `/v1/chat/completions`、上游 `/v1/responses`，usage #443 | 对应记录：输入 501（cache-read 384、普通输入 117）、输出 31，金额 `$0.000070` | 输入/cache-read 均为 0、输出 31；`actual_cost=0.000179676` 星 | **计价不通过。**上游账单金额乘 6.9 为 `0.000483` 星；本地少 `0.000303324` 星。 |
| `grok-4.6`，账号 #28，入站/上游 `/v1/chat/completions` 与 `/v1/responses` | 本轮无唯一配对账单 | usage #441 成功；输入/cache-read 为 0、输出 387 | **只能确认生成成功，不能确认计价准确。** |

Sub2API 向下游返回的成功响应 usage 与本地 #442–#444 记录显示输入/cache-read 为 0；`handleNonStreamingResponse` 对非流式 Responses JSON 先从收到的响应体提取 usage，再将同一响应体写给下游，未发现本轮相关改动把正数输入强制清零。Wokey 自己的 `/v1/requests` 账单则显示正数输入和缓存读取。此前另行直连 Wokey 的相似请求可返回正数输入，但并非同一请求/请求体，不能据此证明差异来源。当前没有捕获到可与 Wokey 账单逐请求配对的上游原始 HTTP 响应体，故**根因仍未定**：现有证据不足以认定是 Sub2API parser 缺陷，也不足以认定 Wokey 响应字段缺陷；不得用 prompt 长度估算替代真实 usage。价卡的同步正确与本地文本扣费准确必须分别判定。

已有不同模态证据仍有效但范围有限：GPT-5.6-Luna 账号 #27 的 #436 文本样本 token 对齐；`gpt-image-2.5` #434 与 Grok 视频 #435 的按次扣费与 Wokey 账单对齐。它们不覆盖 Grok 文本的输入/cache 计量缺口，也不能证明所有模型/规格均已验收。本轮未再发生成请求，未修改账号、模型路由、费率设置或运行时参数；主容器未重启。第二次小时刷新已按上段补记。

**当前结论：**价格同步器有通过的定向代码测试、启动同步和第二次小时 tick 运行证据；完整实现计划矩阵仍待独立复核，Grok 文本费用准确性未闭环。Phase 0–7 原有的本地验收记录不因此被重置，但不能把 Wokey 适配层或整份蓝图称作无条件完整闭环。

### 15.3 2026-10-09 续审：规格边界补齐与多线路实测

**适配代码/测试补齐：**本轮只在 Wokey 图片价卡生成处增加具体 `sizes[]` 校验：每个声明尺寸必须能由现有图片规格分类器识别，且必须与目录 `tier` 一致；未知尺寸或尺寸/档位冲突时不生成 flat-per-image 卡。其余同步器价格转换、请求转发和 native billing 均未改。`unified_gateway_wokey_price_sync_test.go` 新增/补齐：JSON number 高精度价格从解析到价卡、缺失可选 cache-write SKU 不造零价、未知具体图片尺寸、尺寸与 tier 不一致、成功转手工且保留全部价格字段并清除来源元数据。此前 A2 指出的这些测试证据缺口已按实施任务书第 8 节补齐，待当前树独立复审。

定向命令结果：`go test ./internal/service -run Wokey -count=1 -timeout 5m` PASS；`go test ./internal/service -run UnifiedGatewayRoutePricing -count=1 -timeout 5m` PASS；`go test ./internal/handler/admin -run Wokey -count=1 -timeout 2m` PASS；`git diff --check` PASS。`-race` 因 Windows 环境 `CGO_ENABLED=0` 不可用，仍未运行。本轮没有为这个图片目录保护重建/替换 18081 镜像；当前运行公开目录只含已识别规格，所以价卡和请求行为未受该新拒绝边界影响。

**当前运行配置只读核对：**18081 健康检查 HTTP 200；Wokey 同步仍启用，revision 13，FX 6.9、60 分钟，受管卡 262，`last_success_at=2026-10-08T20:59:41.250019128Z`，目录 hash 为 `bcc34c91d584e1fd0bc67e95c56b85d59907d4966d4d048d84e18c9488d9d602`。本轮没有改账号状态、优先级、模型路由、API key、分组、汇率或运行中价卡。额外尝试的 `gpt-5.6-luna` 和 `gpt-5.6-sol` 请求虽然 HTTP 200，但原生调度实际分别选到 YeToken 账号 #20 和流云 AI 账号 #15；它们不是 Wokey 样本，不计入 Wokey 线路验收。该结果说明在统一总组直接测试某个同名模型不能证明命中了 Wokey。

**Grok 4.5 同模型、相同有效缓存身份对照：**统一 key #816 的 `/v1/responses` 请求由 Wokey Grok 账号 #28 成功处理，Wokey 记录 `2026-10-08 21:16:12` 为输入 498（含 cache-read 384）、输出 19、实际扣款 `$0.000059`；本地 usage #448 为输入 0、cache-read 0、输出 19，`actual_cost=0.000110124` 星。上游实扣折为 `0.0004071` 星，本地少 `0.000296976` 星。随后直连 Wokey、使用相同模型/提示、由源码计算出的相同 tenant-scoped `prompt_cache_key` 与 `X-Grok-Conv-Id`，Wokey 响应和账单均记输入 507/cache-read 384/输出 19，扣 `$0.000061`。两次分别请求且账单输入数相差 9，不能视为同一请求对照；它仅表明缓存身份本身不足以解释网关响应的零输入。

**根因仍未确认，未贸然改请求/计费代码。**这笔网关生成响应与本地用量都报告输入/cache 为 0，而 Wokey账单为正数；直连另一次请求返回正数。当前仍没有保存到与请求 #448 精确配对的上游原始 HTTP 响应，也没有一份可证明逐字段相同的出站请求。因此还不能判断是 Sub2API 对 Wokey 响应的处理，还是 Wokey 对网关请求返回不同 usage；不以 prompt 长度估算，也不把真实账单核对功能塞入 Wokey 公开价格同步模块。若继续修复，下一步必须在隔离诊断镜像捕获脱敏的请求方法/path、关键字段与请求头存在性，以及原始 JSON/SSE usage 字段，再根据证据决定是否需修代码；不得记录 key 或提示词正文。

**阶段性判定：**Wokey 公开价同步适配层的规格/精度、手工卡保留、并发 CAS、失败 last-good、HTTP 防护、启动同步及真实第二次小时 tick 均已有通过证据；当前树的最终 A2 复审待完成。实际 Wokey 账单准确性仍有 Grok 文本欠扣缺口，因此不能宣称 Wokey 全部线路都已精确对账，也不能称整份蓝图所有附加项均闭环。Phase 0–7 可按各自记录与排除项交付本地验收；VPS/生产、外部集成与未完成的真实账单校准附加章仍不计 PASS。

### 15.4 2026-10-09 新版实例 Grok 账单复测与终审

在 18081 当前本地实例用现有测试 key #816，对 Wokey Grok 账号 #28 发出一次最小非流式 `/v1/responses` 请求。只记录响应 usage 和账单用量，不记录 key 或提示词。下游返回 HTTP 200，模型 `grok-4.5`，usage 为 input=0、output=52、total=52；本地 usage #450 记 account #28、input/cache-read=0、output=52、`actual_cost=0.000301392` 星，`upstream_request_id` 为空。

Wokey `/v1/requests?page=1` 紧邻记录为 `202610082126052c6sz`：模型 `grok-4.5`，21:26:05.254Z 创建，成功，input=500、cache-read=384、output=52，`usageSource=upstream`，实际扣 `$0.000087`。它与本地 #450 在模型、输出 token 数和时间上高度吻合；因接口未提供共同 request ID 且本地 `upstream_request_id` 为空，这属于高置信度配对，不是严格 ID 配对。按 6.9 折算，上游金额为 `0.0006003` 星，本地少 `0.000298908` 星。该请求再现了文本 usage 欠记。

**诊断结论：**本地非流式 Responses handler 读取响应 JSON 中的 usage 来计价；没有发现它将正数输入 token 强制归零的逻辑。客户端收到的 JSON usage 也是 input=0。现有证据倾向于 Wokey 响应 usage 与 Wokey `/v1/requests` 账单记录不一致，但本轮未捕获经过 TLS 解密的原始上游 HTTP 响应体，故不能据此断言上游责任或修改 parser。不要用提示词长度估算补账；准确计费需另行建设蓝图附加章中的真实消费校准/查询机制，当前明确延期。

**Wokey 价格同步适配层终审：**当前树的 Wokey service、route-pricing、Admin handler 定向测试，前端 32 项测试、typecheck、build、Wire diff 和 `git diff --check` 均通过；独立 A2 审计判 §8/§9 代码与测试 PASS。运行实例同步仍启用，revision 13、FX 6.9、60 分钟、262 张受管卡；第二次真实周期 tick 成功。代表性实际线路证据包括 GPT-5.6-Luna token 样本 #436、GPT Image 2.5 图像 #434、Grok 视频 #435；token/图片/视频场景均分别按各自证据核对。Grok 文本 #442–#450 显示输入/cache 用量差异，不能用前述其他模型的通过结果覆盖。

最终范围结论：Phase 0–7 按阶段记录和 caveat 可交本地验收；Wokey 公开价同步功能运行和当前代码审计通过；Wokey Grok 文本账单准确性仍 FAIL；真实消费校准附加章按蓝图仍延期，因此包含附加章的整份蓝图不能称为全闭环。未改账号、路由、分组、费率、镜像或代码；本轮只发上述一次生成请求并更新此记录。未提交、推送、部署 VPS 或 GitHub。

### 15.5 2026-10-09 精确 ID 实扣证据与欠扣确认（实施前）

为消除 §15.4 的时间/模型推测，本轮在本地测试账号 #28 临时配置既有 `upstream_request_id_header=x-wokey-request-id` 字段并重启 18081 重新加载账号；只发一条最小 `grok-4.5` 请求。usage #453 保存上游 ID `202610090054176ktq3`，且 Wokey `GET /v1/requests?page=1` 返回完全相同 ID：成功、`usageSource=upstream`、input=501（cache-read=384）、output=13、`actualAmount=$0.000054`。这证明本地 Wokey 路由能精确获得 Wokey 账单 ID；之前空 ID 是测试账号未启用上游 ID 捕获。

同一请求的本地响应 JSON usage 和 usage #453 均为 input/cache-read=0、output=13；本地 `actual_cost=0.000075348` 星，`total_cost=0.000078`。按实际 Wokey 实扣与 FX 6.9，应为 `$0.000054×6.9=0.0003726` 星，本地少扣 `0.000297252` 星。命中的 Wokey 价卡 revision 17 来源 `wokey_catalog`，FX=6.9，输入/输出/cache-read 价格分别为 1.932/5.796/0.1932 星/百万，线路倍率缺省 1。

**判定：**问题不是上游价格目录不同步，而是客户端/本地 usage 不含 Wokey 实际计费的输入与缓存 token；使用不完整 usage 计算用户扣款造成欠扣。此证据仍不能断言缺口由 Sub2API parser 引入，因为向下游返回的原始形态 usage 已是 0，未捕获上游原始 HTTP body。实施决定采用精确 request ID 查 Wokey 实际 `actualAmount`，不估算 token、不启发式配对、不仅改 parser。实施前测试账号 #28 的 Extra 仍临时设为上述响应头名；待自动 Wokey 专项捕获代码部署后恢复该字段原值。

本节样本证明精确 ID 查询与 Wokey 实扣差额，不是修复后的 E2E。尚未纠正历史 usage #450–#453 或余额；只在新代码镜像上创建新样本验收。用户面板、实际余额、key quota 与修复后 `ActualCost` 是否一致尚未验证。

### 15.6 2026-10-09 通用 Composite 结算路径修复与新镜像验收

**根因与修复：**此前 Wokey 实扣 helper 只挂在 `OpenAIGatewayService.RecordUsage`。统一组 #16 的 `/v1/responses` 实际经 `GatewayHandler.Responses → GatewayService.RecordUsage → recordUsageCore` 结算，所以 helper 根本未执行；独立 matcher 单测通过不能证明真实统一网关路径生效。本次将 Wokey 专项覆盖移到通用 `GatewayService.recordUsageCore`：保留通用价卡原有 `Eligible` 判断，在 route-card 计算后、usage log 创建和原生 `applyUsageBilling` 前，仅对获准的 `wokey_catalog` token 卡、精确 group/account/model、Wokey API-key host 和上游 ID 查账；成功时仅写 `CostBreakdown.ActualCost` 并复用原生结算。没有更改路由、请求/响应、usage tokens、`TotalCost`、账号成本或非 Wokey 行为；OpenAI 专用的错误挂载和临时诊断已删除。

**代码验收：**定向 `go test ./internal/service -run WokeyActualBilling -count=1 -timeout 5m` PASS；完整 `go test ./internal/service -count=1 -timeout 8m` PASS（140.422s）；`git diff --check` PASS；本地 Docker 镜像 `sub2api:local-wokey-actual-billing-final2-20261009` 构建 PASS。独立 A2 代码审计 PASS，确认 helper 只修改用户 `ActualCost`，余额、订阅/API key quota 继续沿原生 settlement command，账号成本仍只使用未变更的 `TotalCost`。新容器 `sub2api-v0213-404token-app-wokey-actual-billing-final2-20261009` 在 18081 返回 HTTP 200；旧诊断容器停止并以 `...diag3-rollback-20261009` 保留。只替换本机测试实例，没有触碰 18082、VPS、GitHub、提交或推送。

**真实成功请求精确对账：**API key #816 / user #1 / unified group #16、Wokey 账号 #28，仅发一笔已有成功证据的 `grok-4.5` 最小非流式文本请求。响应 HTTP 200，usage input=0、output=21。新 usage `client:2d14f226-02ee-4e5e-9458-f3658b47c2f0` 保存上游 request ID `20261009024234y5f20`；Wokey `/v1/requests?page=1` 以同 ID 唯一命中 succeeded、`usageSource=upstream`、`actualAmount=$0.000061`。该模型当前价卡 source=`wokey_catalog`、`source_fx=6.9`、线路倍率缺省 1，统一组倍率=1；故上游实扣折算 `0.000061×6.9=0.00042090` 星。本地同一行 `actual_cost=0.0004209000` 星、`total_cost=0.0001260000`，21 个输出 token 未改，完全符合公式且保留原生 `TotalCost`。本次请求前余额由前序已记录余额 `98.91628382` 扣除 #459 的原生量化费用 `0.00015070` 得 `98.91613312`；请求后数据库余额为 `98.91571222`，本次差额 `0.00042090` 星，与 Wokey 实扣换算及 usage `ActualCost` 一致。历史行/余额未回写。当前测试 key 的 quota=0（无限额），故运行时没有 API-key quota 增量样本；有限 quota 同额路径由针对性 service 单测覆盖。

**用户展示核验及边界：**用户用量页复用 `UsageTable`，把本行 `actual_cost` 作为实扣值并以 `⭐️` 显示；表格显示 6 位小数（本例 `⭐️0.000421`），详情 tooltip 显示 8 位（`⭐️0.00042090`）。用户资料余额来源为同一数据库余额，既有 `formatPoints` 对大于 0.01 的余额显示两位小数，因此数据库 `98.91571222` 在资料卡上显示 `⭐️98.92`；这是展示舍入，不是结算差额。用户 dashboard 的近期记录另按四位小数显示（本例 `⭐️0.0004`）。Windows Computer Use 初始化两次均以“failed to write kernel assets / path not found”失败，按技能规则停止继续 UI 自动化，因此未取得现场页面截图；本次依据真实数据库记录、前端字段映射与格式化代码核验页面数据口径，不能声称已视觉验收截图。

**失败请求单独记录：**同一镜像上的 `gpt-5.6-luna` 探测请求返回 HTTP 502，数据库没有对应的成功 usage 行，未形成可配对 Wokey 账单，未用于计费验收，也未重试或据此归因到 Sub2API/Wokey。该结果不影响随后 `grok-4.5` 的精确实扣验收。

**判定：**Wokey 精确 request-ID token 实扣已在本地 Composite 实际结算路径通过一笔端到端样本验证；用户扣款、数据库余额变化和 usage 行一致，价卡回退错误已修复。面板字段映射已核验，视觉页面未现场验收，Key quota 真实运行样本未测（该 key 不设 quota）。本结果只关闭本地 Wokey 请求级实扣切片，不代表所有上游/所有模型的真实账单校准已完成；Wokey 媒体、历史补偿和其他 provider 继续按蓝图分别记录，不据此宣称整份真实消费附加章或全蓝图闭环。

### 15.7 2026-10-09 Grok 路由与专用结算入口补修

**模型范围结论：**目前的证据不支持“Wokey 文本模型全部对不上”。GPT-5.6-Luna 的主实例样本 #436 记录 23 输入/5 输出，与同 ID Wokey 记录相同；较大的克隆样本 #445 记录 1577/5，也与 Wokey usage 一致。该大样本本地价卡计算 `0.001330596` 星，Wokey `$0.000193 × 6.9 = 0.0013317` 星，差 `0.000001104` 星（约 0.083%），符合 Wokey 将美元金额按费用项保留 6 位小数造成的量级。小样本 23/5 的 Wokey `$0.000006` 换算后与未舍入 SKU 价差距比例较大，也源于金额很小时的微美元舍入，不是输入 usage 漏失。其余 Wokey 文本模型没有逐个取得同一请求 ID 的本地/上游配对证据，仍标为未核实。

反复出现的大额差异集中于 Grok-4.5。之前 #442–#450 等成功用量行中，本地 input/cache-read 多为 0，而 Wokey 同 ID 或高置信配对记录报告正数；例如 #453 本地按 13 输出 token 计 `0.000075348` 星，Wokey `$0.000054 × 6.9 = 0.0003726` 星。不是 Wokey 全部文本模型同类失配。

**根因：**账号 #27（OpenAI）与 #28（Grok）的映射都曾声明 `grok-4.5`，没有 Composite 精确公共路由时会产生跨平台归属歧义并落入 Responses 兼容路径；该路径表现为兼容响应 ID（`msg_…`）且接收到的 usage 缺少 Grok 输入/cache 字段。按项目边界没有改原生路由器或 parser。本地已有的 `composite_model_routes` 记录 #2 将公共模型 `grok-4.5` 指向平台 `grok`、保留上游模型名、endpoint=`any`；它只选择 provider 平台，不锁定账号，账号选择仍由 Sub2API 原生调度。显式路由后的 Grok 原生响应 ID 为 `70624f15-e066-9c9d-9314-9cc4824eb2b6`，usage 为总输入 508（普通输入 124、cache-read 384）和输出 20，与同 ID Wokey usage 一致。

**计费入口缺口及最小修复：**上一节的成功结算样本覆盖 `GatewayService.recordUsageCore`。显式 Grok 路由实际进入 `OpenAIGatewayService.RecordUsage`；该入口此前没有调用精确 Wokey amount lookup。诊断请求 #465 / 上游 ID `2026100903073570v4x` 的远程费用为 `$0.000062`，原本地价卡费用 `0.0004296768` 星与 FX 换算值 `0.0004278` 星差 `0.0000018768` 星（约 0.44%）。现已在 `openai_gateway_usage.go` 的原生结算前挂接同一个 `applyUnifiedGatewayWokeyActualBillingCost`，与通用入口共用精确 ID matcher、价卡/账号/模型 guard、FX 和倍率计算。没有新造算法，也没有修改响应 usage、请求路由、协议、TotalCost、账号成本或非 Wokey provider。临时取证日志已清除。

**代码与镜像验收：**`go test ./internal/service -run 'WokeyActualBilling' -count=1 -timeout 5m` PASS；`go test ./internal/service -count=1 -timeout 8m` PASS（141.445s）；`git diff --check` PASS；Docker image `sub2api:local-wokey-grok-billing-final-20261009` 构建 PASS。当前 18081 运行该镜像且 `/health` 返回 HTTP 200；此前的 diagnostic4 容器已停止并改名为 rollback。18082、VPS、GitHub、提交和推送均未触碰。

**修复后精确实扣对照：**在 18081 通过测试 key #816、统一组 #16、账号 #28 仅发一笔最小非流式 `grok-4.5` 请求。HTTP 200，响应 usage input=508/output=23；本地新 usage #466 的上游 ID 为 `202610090321564iiip`。用同一 Wokey API key 读取 `GET /v1/requests?page=1`，以该 ID 唯一命中 `status=succeeded`、`usageSource=upstream`、`actualAmount=$0.000065`，上游 input=508/cache-read=384/output=23。价卡 FX=6.9、线路倍率=1、统一组倍率=1，故应扣 `$0.000065×6.9=0.00044850` 星；本地 usage `actual_cost=0.00044850`，用户数据库余额减少 `0.00044850` 星，二者均与远端实扣精确一致。当地记录将输入拆为普通 124 + cache-read 384，总数 508；输出 23，与上游一致。原生 `total_cost=0.00050120` 保持不变。

**最终判定：**本次实测确认存在的大额 usage/扣费缺口是 Grok-4.5 的映射歧义及 Wokey 精确查账未接入其实际 OpenAI 专用结算入口所致；两点现分别由既有精确 Composite 路由配置和共享 Wokey helper 补齐，且新请求的 Wokey 实扣、Sub2API `ActualCost` 与用户余额变化一致。GPT-5.6-Luna 的已配对样本未发现输入漏记，微小金额差异可由 Wokey 美元舍入解释。其他 Wokey 文本模型仍需各自的同 ID 样本才能做模型级结论；本次没有宣称全部模型已逐个验收，也没有修改历史 usage/余额。

### 15.8 2026-10-09 分时模型价卡需求与基线

**范围：**此前 Wokey 同步器将 time-of-day 文本模型标成 `unsupported_time_of_day`，因为既有单费率卡不能表达峰/非峰价格。本增量只允许三个精确模型 ID（`deepseek-flash`、`deepseek-v4-flash`、`deepseek-v4-pro`）生成单行双档 token 卡；其他 Wokey 分时模型仍 unsupported，未来扩展须单独审计并修改 allowlist。不扩展非 Wokey provider，不修改 Wokey 实扣查询路径或其他既有结算行为。方案、UTC 语义、reserve 与边界见 [Wokey 分时文本价卡设计](D:/AI/SSH/SUB2API-V0213-WOKEY-TIME-OF-DAY-PRICE-DESIGN.md)，精确范围与测试见其实施任务书。

**公开目录实时报文核验：**2026-10-09 UTC 通过不带 API key 的 `GET https://api.wokey.ai/v1/models/pricing` 读取了 `deepseek-flash`、`deepseek-v4-flash`、`deepseek-v4-pro`。三者均为 `available=true`、币种 USD、`pricing_mode=dynamic_discount`，另含 `time_of_day.current_tier`、`peak_windows_utc`、`tiers.peak` 与 `tiers.off_peak`。本次抓取时三模型 `current_tier=peak`，峰值窗口均为 `[01:00,04:00)` 和 `[06:00,10:00)` UTC。各档 input/output/cache-read USD/M：

| 模型 | peak | off-peak |
|---|---|---|
| deepseek-flash | 0.224 / 0.896 / 0.004480 | 0.112 / 0.448 / 0.002240 |
| deepseek-v4-flash | 0.140 / 0.560 / 0.002800 | 0.112 / 0.448 / 0.002240 |
| deepseek-v4-pro | 0.660 / 1.980 / 0.022000 | 0.528 / 1.584 / 0.017600 |

**风险核验：**三模型目录都报告 `off_peak_multiplier=0.5`，但 v4-flash 与 v4-pro 的上述 off-peak/peak meter 价格比为 0.8，不能以该字段推价。实施必须存并使用两个 tiers 的逐 meter 原值；顶层 `pricing_skus` 只校验当前 tier，不得作为全天费率。原价按配置 FX 换星/M。请求时间采用 Sub2API 已捕获 `PricingAt` 的 UTC 时间，reserve 取两档估算的最大值；相同 Wokey request ID 的已结算实扣仍优先。

独立只读 A2 文档审计于 2026-10-09 PASS：审计确认分时 schema 白名单、三模型 opt-in、PricingAt 缺失退路、exact-bill 独立优先级、双档 FX 原子重算、last-good 保护及其他 provider 隔离均有明确约束。设计 SHA256 `202E7D4F4D83AD97F6020973AC13CADA78E34CE67A992251883B212706DC9E2F`；TaskSpec SHA256 `FD53DF285BA5BDCC2957E35444538505C433D3261A692DD58AAE0EFE7F5A7157`。截至文档审计完成，尚未开始代码修改、构建、前端验收、配置同步或真实生成调用。本节的公开目录快照只作为探测证据，不代表卡价已落库，也不表示上述模型已能通过当前中转。后续唯一执行结果继续写在本文件的新增条目中。

### 15.9 2026-10-09 分时价卡实现、隔离同步与三模型实扣验收

**实现与代码审计：**按分时开发设计与任务书完成后端双档卡解析/校验、FX 重算、UTC 请求时段选择、双档 reserve 上界和既有 Wokey 同 ID 实扣优先；前端同步显示 Peak/Off-peak 星价、UTC 窗口、FX/来源和请求开始时选档说明，并禁止动态卡降格为丢失双档的手工卡。目标仅为精确 ID `deepseek-flash`、`deepseek-v4-flash`、`deepseek-v4-pro`，不扩展其他 Wokey SKU 或 provider。

第一轮独立代码 A2 指出四类 fail-open/输入校验缺口：目标模型缺少/null `time_of_day` 可能退成静态当前价、窗口起始小时缺失被当作 0、meter/价格约束校验不完整、SKU constraints 未严格处理。修正后第二轮独立 A2 复审 PASS；相应缺失/null、缺窗口字段、未知/已知 constraints 以及价格字段测试已补。前端独立实现测试 PASS：2 个聚焦文件共 34 项、i18n 3 项、typecheck、production build、相关 ESLint、`git diff --check` 均 exit 0。后端聚焦测试 `go test ./internal/service -run 'UnifiedGatewayRoutePricing|WokeyPrice|WokeyActualBilling' -count=1 -timeout 5m` PASS；服务测试包此前完整运行亦 PASS。Go race 未运行（本机 `CGO_ENABLED=0`）。完整 `go test ./...` 不属于本任务验收范围。

本地 Docker 镜像 `sub2api:local-wokey-tod-pricing-20261009` 构建 PASS，镜像 manifest digest `ba0eac0ac746df3f98b8ffa299f1f8fc9b7510a2db86db9e1afbe8c32da85f0a`。直接在原 dirty worktree 构建时，pnpm 因已有的 `frontend/pnpm-lock.yaml` 与未改 `frontend/package.json` 不匹配而停止；未覆盖该既有锁文件，改在临时构建上下文中只替换为与 package.json 对应的 HEAD 锁文件后构建成功。隔离容器运行上述新镜像，健康检查正常。原 18081、18082、主数据库、VPS、GitHub 均未修改。

**通过前端同源保存/同步接口准备隔离测试配置：**从当前本地运行库克隆数据库到 `sub2api_wokey_tod_20261009`，在克隆内将统一组 #16 限制为仅 Wokey 账号 #27，并用管理员页面相同 API 保存账号 #27 的可编辑价卡与同步设置（只选 #27、FX 6.9、60 分钟）；没有直接 SQL 创建价卡。第一次原样提交其他账号的价卡被后台正确拒绝，因为它们不属于隔离组；移除那些克隆环境中不相关的价卡后保存成功。手动 sync 请求首次遇到启动同步正在运行的 409；随后读取到该同步已成功完成。最终设置 revision 36、目标组 #16、同步账号仅 #27、FX 6.9、周期 60 分钟，目录时间 `2026-10-09T07:13:09Z`，受管卡 265、unsupported 0。

同步生成三张单行双档 Wokey 卡，USD 目录源值乘 FX 6.9 后写为星/百万 token：

| 模型 | peak 输入/输出/cache-read | off-peak 输入/输出/cache-read | UTC peak 窗口 |
|---|---|---|---|
| `deepseek-flash` | 1.5456 / 6.1824 / 0.030912 | 0.7728 / 3.0912 / 0.015456 | [01:00,04:00)、[06:00,10:00) |
| `deepseek-v4-flash` | 0.966 / 3.864 / 0.01932 | 0.7728 / 3.0912 / 0.015456 | [01:00,04:00)、[06:00,10:00) |
| `deepseek-v4-pro` | 4.554 / 13.662 / 0.1518 | 3.6432 / 10.9296 / 0.12144 | [01:00,04:00)、[06:00,10:00) |

**真实请求与实扣对照：**在 UTC 07:14 左右，对测试 key #816、用户 #1、组 #16 各发一次最小非流式 `/v1/chat/completions` 请求，三个请求均由隔离组唯一账号 #27 处理并返回 HTTP 200。Wokey `/v1/requests?page=1` 以每条 usage 保存的上游 request ID 唯一命中 `succeeded`、`usageSource=upstream` 的记录。ID 仅部分遮蔽如下：

| 模型 | 本地 usage | 上游 request ID（遮蔽） | 本地 / Wokey 输入、cache-read、输出 | Wokey 实扣 USD × 6.9 | 本地 `ActualCost` / 原生 `TotalCost` | 余额变化 |
|---|---:|---|---|---:|---:|---:|
| `deepseek-flash` | #470 | `20261009…oeq4` | 34 / 0 / 8 | $0.000015 → ⭐0.00010350 | ⭐0.00010350 / ⭐0.00001980 | -⭐0.00010350 |
| `deepseek-v4-flash` | #471 | `20261009…u2vl` | 34 / 0 / 8 | $0.000009 → ⭐0.00006210 | ⭐0.00006210 / ⭐0.00001980 | -⭐0.00006210 |
| `deepseek-v4-pro` | #472 | `20261009…xx43n` | 87 / 0 / 8 | $0.000073 → ⭐0.00050370 | ⭐0.00050370 / ⭐0.00003570 | -⭐0.00050370 |

测试前余额 `⭐98.91007994`，三次请求后余额 `⭐98.90941064`，合计减少 `⭐0.00066930`，恰等于三条 `ActualCost` 之和。每条本地 token usage 与同 ID Wokey usage 完全相同；三次本地用户实扣均等于上游账单 USD × 6.9，体现的是 Wokey exact-bill 优先路径，而不是将该三笔误当成静态卡公式的验收。原生 `TotalCost` 保持原生价格并明显不同，符合“只改用户侧 ActualCost”的既定边界。

**时段与限制：**三次实请求均位于 Wokey UTC 峰时段；峰档的卡价、当前时段与 exact-bill 结算路径有真实运行证据。离峰实际请求未发，离峰选档、半开区间、UTC 转换、两个档位的公式与最大 reserve 由聚焦单测覆盖。测试 key quota 为 0（无限额），因此没有有限 API-key quota 的现场增量；其路径由 service 单测覆盖。未用截图声称完成 UI 视觉验收；前端构建/交互单测通过，并通过对应管理员 GET/PUT/sync 接口验证持久化数据与三张卡。真实容器使用隔离端口 18083 与克隆 DB；验收后依任务书清理克隆容器/DB/应用数据。最终 A2 代码审计 PASS；本次不提交、不推送、不触碰 VPS 或主实例。

**判定：**三个当前明确纳入白名单的 Wokey 分时文本模型已完成本地价卡同步、前后端验收、独立代码审计和三条峰时同 ID 实扣对账；离峰档通过确定性测试但没有现场生请求。Wokey 其他分时 SKU、其他文本模型逐项账单、离峰现场账单、有限 key quota、生产/VPS 仍不在本次 PASS 范围。

**清理结果补记：**隔离容器 `sub2api-wokey-tod-e2e-20261009` 与克隆数据库 `sub2api_wokey_tod_20261009` 已删除，构建镜像保留。自动策略拒绝对两个精确的 Temp 目录递归删除，返回 `blocked by policy`；因此 `C:\Users\T14S\AppData\Local\Temp\sub2api-wokey-tod-data-aab28dc3f3b4470899365594154f5b34` 与 `C:\Users\T14S\AppData\Local\Temp\sub2api-wokey-tod-build-b35b15f7f14e446d97ce11a6b5ce342b` 仍暂留，未尝试绕过策略。主实例和 18082 实例未清理或更改。

### 15.10 2026-10-09 GLM-5.3 与 GPT Image 2.5 直连/网关故障归因

根据历史未通过记录选取两个有代表性的失败候选（不做 44 个模型的全量生成扫描）。测试使用 Wokey 账号 #27 的现存 API key，但 key 未输出；未更改账号、路由、模型映射、价卡或其他运行配置。

| 模型 | Wokey 直连 | Sub2API 证据 | 归因 |
|---|---|---|---|
| `glm-5.3` | `POST https://api.wokey.ai/v1/chat/completions` 返回 HTTP 200，Wokey usage 为 input=16/output=8；请求 ID `20261009…p2w2` 状态 succeeded、`usageSource=upstream`、实扣 `$0.000012`。公开目录仍标为 available。 | 随后对 18081 统一 key #816 发一次相同模型的最小请求，HTTP 503、无本地 usage。容器日志明确 provider=`zhipu`；当前统一组内无 Zhipu account。Wokey #27 是 platform=`openai`、active/schedulable，账号映射含 `glm-5.3`，但当前组没有此模型的 `glm-5.3 → openai` Composite 路由。 | **Sub2API 本地路由归属问题。**请求在选择上游账号前失败，没有发给 Wokey；不能归因为上游模型不通或费率设置。最小修复方向是使用已存在的精确 Composite 路由能力把公共模型指向 OpenAI provider，再用同一请求对照；本轮未应用配置或代码修改。 |
| `gpt-image-2.5` | `POST https://api.wokey.ai/v1/images/generations` 一次请求返回 HTTP 502。Wokey `/v1/requests?page=1` 唯一可见记录 `20261009…kxo4` 为 failed、`errorCode=server_is_overloaded`、错误 `Our servers are currently overloaded. Please try again later.`、`actualAmount=0`；公开目录此时仍标记 available。 | 历史 §12.2 已记录三次本地请求确实到达 Wokey 并收到同样的 HTTP 502 overload；冷却期内的额外两次 503 是本地候选快照暂时未恢复，并非 Wokey 调用。 | **当前生成失败是 Wokey 上游过载。**已观测到的 Sub2API 503 属于上游 502 后原生冷却/调度快照阶段的后续表现，不是请求协议格式拒绝。本轮不重试图像。 |

**结论和范围：**这两条已知失败模型并非同一根因：GLM-5.3 是 Sub2API 将模型前缀路由到没有可用账号的 `zhipu` provider；GPT Image 2.5 直连与历史转发均得到 Wokey overload。本次只对这两个有历史失败证据的模型做小范围核验，不代表 Wokey 其余模型全量可用性扫描；GPT-5.6-Luna、Grok 文本/视频已有其他后续成功记录。没有修改或重启 18081，没有重试，没有 VPS/GitHub/提交/推送。

### 15.11 2026-10-09 GLM-5.3 网关复测与路由归因

**复测范围：**只复测上一节中“直连成功、网关失败”的 `glm-5.3`，不扩大到 44 个模型。Wokey 直连证据仍为 HTTP 200、input=16/output=8、上游实际扣款 `$0.000012`。在添加路由前，统一网关测试 key #816 对同模型连续两次得到 HTTP 503，均没有本地 usage；日志 provider 为 `zhipu`、没有 `account_id`，请求在账号调度前结束。

**根因证据：**运行库 `unified-api-internal`（group #16）里，YeToken 国模账号 #25 和 Wokey OpenAI 账号 #27 的 `model_mapping` 都声明了 `glm-5.3`；Wokey Grok 账号 #28 的同一映射也声明了该模型。由于声明跨越 `openai` 与 `grok` 两个平台，Composite 自动归属不能安全选定目标平台。路由预览对未显式配置的 `glm-5.3-flash` 返回“model is exposed by multiple provider platforms”；这与 GLM-5.3 的账号映射结构相同。无显式路由时，模型名前缀探测回退到 `zhipu`，而该组没有可调度的 Zhipu 账号。证据指向**模型归属歧义导致的确定性路由配置缺口**，不是 Wokey 瞬时拒绝，也没有证据表明请求体或协议适配有缺陷。

**最小修复及验证：**通过现有 Composite 路由管理页面，在 group #16 新增精确路由 `glm-5.3 → openai`，上游模型仍为 `glm-5.3`，端点为“任意”；不改请求/响应内容，不改账号映射、优先级或分组，也不改代码。保存后统一网关请求 HTTP 200，模型仍为 `glm-5.3`，返回 `OK.`；本地 usage #470 记录 input=23/output=36。访问日志显示实际调度到 YeToken 国模账号 #25（`openai`），不是 Wokey #27。因此已证明**统一网关经 OpenAI 兼容线路调用 GLM-5.3 可用**，但这次不是 Wokey 专线实测，不能据此声称 Wokey #27 的转发与对应扣费已复验。当前精确路由保留在本地统一网关配置中；后续若需锁定 Wokey #27，应另行使用已受支持的账号级模型路由并单独验收，不能通过调高全局账号优先级代替。

**代码处置：**没有修改 Sub2API 核心代码。现有 Composite 归属逻辑在多个 provider 声明同一模型时拒绝猜测目标平台；显式路由是已有的管理配置能力，且此次实际解除 503。故本轮定性为“持久配置歧义，非瞬时上游故障；现有路由配置可修复”，不提交代码补丁。

**GPT Image 2.5：**本轮不再发图像请求。当前 Wokey 直连返回 502，Wokey 对应请求记录为 `server_is_overloaded`、实扣为 0；历史 Sub2API 图像请求曾到达 Wokey 并收到相同 502。当前证据仍归因于 Wokey 过载，不能把它算作 Sub2API 代码缺陷，也不能算模型永久不可用。

**边界：**本轮修改仅是统一网关一条精确运行时路由和本记录；未改账号、模型映射、价卡、倍率、API key、组成员、代码、镜像、GitHub 或 VPS。只验证了以上模型与请求形态，不代表所有 Wokey 模型通过。

### 15.12 2026-10-09 Wokey GLM-5.3 指定账号路由与精确账单验收

**范围纠正：**§15.11 的首个修复请求实际由 YeToken #25 处理，不能作为 Wokey 链路证据。本节将 Wokey 与 YeToken 视为两个独立上游，只核验 Wokey OpenAI API-key 账号 #27。

**指定路由：**本地 18081 运行库中，统一组 #16 已有 `glm-5.3 → openai` Composite 平台路由；Wokey #27 在该组内、状态 active/schedulable，且账号映射包含精确模型 `glm-5.3`。统一组已有 `model_routing_enabled=true`，原规则包括 gpt-image-2、gpt-image-2.5 和 grok-imagine-video-1.5。本次只追加账号级规则 `model_routing["glm-5.3"]=[27]`，其他规则及其他账号配置保持不变；清除测试 API key #816 的认证缓存并广播本地失效通知。未改源代码、模型映射、价卡、倍率、API key、组成员、镜像、VPS 或 GitHub。

此字段沿用 Composite 目标解析为 OpenAI 时的原生账号路由优先池。Wokey #27 可调度时，该请求优先只在指定账号池内选择；若指定账号不可用，调度器仍保留原生普通选择回退行为。本次两笔请求均实际命中 Wokey #27。

**真实网关请求及逐请求账单：**

| Wokey 请求 ID | 本地 usage | 结果 | Wokey 同 ID 账单 | 用户实扣核对 |
|---|---:|---|---|---|
| `20261009075440aau8k` | #471；账号 #27；input/output=17/8 | HTTP 200；因 max_tokens=8 在 reasoning 中止，未作为可见文本生成验收 | `succeeded`、`usageSource=upstream`、input/output=17/8、`actualAmount=$0.000012` | FX 6.9 后 ⭐0.0000828；本地 `ActualCost=0.0000828`；余额从 ⭐98.90960314 降至 ⭐98.90952034，差额完全一致 |
| `20261009075551y6ov7` | 最新 Wokey usage；账号 #27；input/output=19/14 | HTTP 200；`finish_reason=stop`，正文 `OK` | `succeeded`、`usageSource=upstream`、modelId=`glm-5.3`、input/output=19/14、cache-read=0、`actualAmount=$0.000018` | FX 6.9 后 ⭐0.0001242；本地 `ActualCost=0.0001242`；余额从 ⭐98.90952034 降至 ⭐98.90939614，差额完全一致 |

第二笔的统一组倍率和账号倍率均为 1。Wokey 价卡来源为 `wokey_catalog`、FX=6.9，input/output 价分别为 ⭐1.932/⭐6.072 每百万 token；Wokey 实扣按同一 request ID 覆盖用户 `ActualCost`。价卡公式与极小请求的上游 `$0.000001` 计费粒度会有舍入差，因此这里以精确同 ID 实扣作为核对值；原生 `TotalCost=0.0000882` 保持不变。

**结论：**Wokey #27 的 GLM-5.3 已通过统一网关实际生成可见文本；本地调度账号、上游模型、完整 token usage、同 ID Wokey 实扣、Sub2API 用户 `ActualCost` 和余额变化均得到配对证据。此前把 YeToken #25 的请求当作 Wokey 验收是错误的，本节更正该归属。此结论仅覆盖 Wokey #27 的 `glm-5.3` Chat Completions 窄路径，不代表 Wokey 其他模型已逐项验证。管理员 Composite 分组编辑页目前隐藏账号级 ModelRouting 控件；本次沿用已存在的后端字段保存该精确规则。
