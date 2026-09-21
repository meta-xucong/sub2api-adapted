# 统一 API P0 商业化收口开发方案

> `TASK_ID=P0-UNIFIED-GATEWAY-COMMERCIAL-20260921`
> `CONTRACT_REV=P0-COMMERCIAL-20260921-v1.1`
> `STATUS=CODE_IMPLEMENTED / LOCAL_TESTS_PASS / STAGING_AUDIT_PENDING / PRODUCTION_GATE_DISABLED`
> `COMPLEXITY_GATE=ESCALATE_REQUIRED`
> `CONTEXT_MODE=ARMED`

本文件是当前阶段的唯一 P0 实施边界。它承接第 17、18、19、20、21 章的已有实现与验收证据，只处理“内部统一 API 进入受控商业使用前必须补齐的最小闭环”。本轮代码已经按 v1.1 契约落地，最终独立审计和真实环境证据仍需单独确认。

本轮不修改数据库 schema、Docker、VPS、GitHub 或线上配置；只修改统一网关管理页、统一网关 preview 返回字段、对应类型/i18n、测试和本 P0 文档。代码实施与审计必须以本文件和 [23-p0-commercial-readiness-audit-checklist.md](./23-p0-commercial-readiness-audit-checklist.md) 为冻结依据。

## 1. 用户原始指令与目标

用户原始指令：

> 只做 P0，P1 暂时不考虑。你设计一个开发升级方案，把你的思路落实成开发文档

目标：

1. 保留现有统一 `/unified/v1`、多 provider、固定优先级、线路级计费和失败不收费能力。
2. 让管理员用一个简单页面维护“模型—线路—价格—测试—发布”，不要求理解内部 route/target/binding 等数据库概念。
3. 让统一模型目录只暴露当前真正可调度、能力匹配、价格完整的模型。
4. 本轮只在不干扰旧 `/v1`、旧 Group、旧 API key、旧账号调度和旧账务的前提下完成前三项收口；Key 安全、健康运营、备份回滚和商业授权不在本轮开发范围。

## 2. 当前基线

### 2.1 代码与运行基线

| 项目 | 基线 |
|---|---|
| 工作树 | `E:/AI/sub2api/.release-merge-20260920-r2` |
| 分支 | `codex/deploy-unified-ui-20260921` |
| 基线提交 | `fb5aa6d6ec08992afa8c6ec47a9b1796337b40b3` |
| 统一入口 | `/unified/v1`，独立 runtime gate |
| 线上实例 | `aiself.vip`、`404token.xyz` 已有运行中统一入口；本文件不授权再次部署 |
| 当前 gate | 本地代码/配置以现有部署事实为准；任何新版本必须先保持可关闭、可回滚 |

最近验收已证明的事实：

- Aiself 统一模型目录最近一次真实探测约 69 个模型；404token 约 27 个模型。
- GPT、Claude、DeepSeek 等不同来源可以通过统一入口调用。
- 404token 上 GPT-5.5、GPT-6 Astra、DeepSeek、Claude 代表链路产生了不同的 route/provider/account 账单记录。
- Aiself 已有“上游失败释放预占、成功才 capture”的真实账单证据。
- 前端最近一次全量测试、typecheck 和 production build 已通过；后端统一网关定向测试已通过。

本轮实施后的本地证据：

- 前端统一网关定向测试：9/9；前端全量测试：257 个测试文件、1883 个测试；`pnpm typecheck` 和 `pnpm build` 均通过。
- 后端统一网关 preview 计价定向测试、`internal/service`、`internal/handler/admin`、`internal/server/routes` 均通过。
- `git diff --check` 无错误；改动仅限统一网关管理页、preview 展示契约、对应测试/i18n/types 和本 P0 文档；未改旧 `/v1`、旧账务主流程、数据库 schema、Docker、VPS 或 GitHub。
- 真实 provider、旧入口回归、PostgreSQL/部署环境和线上账单证据仍未在本轮执行，因此不能据此宣称 P0 商业放行。
- 404token 低资源 VPS 未完成完整 Go 全量/race 证据；这必须在本地 CI 或隔离 staging 补证，不能在生产机上硬跑。

上述事实证明核心网关已经具备受控使用基础，但不等于所有模型、所有媒体任务和所有商业运营能力都已经放行。

### 2.2 现有能力复用矩阵

| 能力 | 处理决策 | 说明 |
|---|---|---|
| 统一 runtime、route catalog、capability matrix | 直接复用 | 不另写第二套路由器或 Provider Registry |
| OpenAI-compatible、Anthropic、CN/Ark、Gemini、Grok 适配 | 直接复用已确认的 adapter | 未被能力矩阵确认的 endpoint 继续拒绝 |
| 固定优先级和失败重试 | 直接复用 | P0 不新增 weighted/cheapest/capacity-aware 策略 |
| route price snapshot、reserve/capture/release | 直接复用 | P0 只补可见性和发布前校验，不改变账务核心语义 |
| 统一模型候选读取 | 复用并收口 | 候选可以刷新，发布必须由管理员确认 |
| 现有 API key、Group、用户额度、禁用/过期机制 | 优先复用 | 不新建用户级 Provider 或额度体系 |
| `UnifiedGatewayView` 聚合编辑器 | 保留并简化默认视图 | 高级 lane/target/binding 继续隐藏 |
| channel monitor、smart-router health/cooldown、审计日志 | 保持现状 | 本轮不新增健康/告警功能 |
| Docker 镜像、数据库备份、回滚目录 | 保持现状 | 本轮不新增备份/回滚功能 |

## 3. P0 范围冻结

### 3.1 本阶段必须完成

#### P0-A：模型目录治理

- 统一目录只返回已发布且可执行的模型。
- 每个模型至少有一个 `enabled + schedulable + capability matched + valid pricing` 的候选。
- 已下架、无账号、无价格、协议不匹配、能力未知的模型不能进入公开目录。
- 管理员可以刷新候选、查看阻断原因、隐藏/恢复模型、批量测试和发布。
- 历史账单、历史 snapshot、历史 usage 不删除、不回算。

#### P0-B：管理员极简配置

- 默认页面只展示统一入口状态、模型目录、线路状态、价格摘要和发布操作。
- 管理员完成一个模型配置的主流程不超过四步：

  `选择模型 → 选择/确认线路 → 设置价格 → 测试并发布`

- route、target、binding、profile、revision 等术语只在高级详情中出现。
- 不允许用户选择 provider、账号、线路、倍率或价格。
- 统一 API key 的用户侧流程仍然是选择统一 Group、创建 key、复制 endpoint；不新增用户路由设置页。

#### P0-C：计费可解释与发布前阻断

- 管理员可以在发布前看到真实线路、计量单位、倍率来源、用户价预览和失败收费规则。
- 同一公共模型的不同线路使用各自的 price profile；命中哪条线路就冻结哪条线路的 snapshot。
- 价格缺失、倍率缺失、basis 不匹配、来源版本无效时禁止发布和发送。
- 成功才 capture；失败、取消、超时、空结果、异步任务最终失败均 release，用户费用为零。
- 前端不显示伪造的默认价格；未配置值必须显示“未配置/不可发布”。

### 3.2 暂缓的后三条线

以下能力本轮明确不开发、不重构、不新增验收门槛：

- **统一 API key 安全闭环**：不新增 key 生命周期、额度、限速、并发、使用量和撤销页面；继续使用现有机制，若发现既有缺陷另立任务。
- **健康状态和故障运营**：不新增健康仪表盘、告警、通知平台或新的熔断系统；继续复用当前已有的 schedulable、cooldown、重试和日志行为。
- **备份、回滚和商业安全**：不新增迁移演练、镜像发布流程、商业授权登记、数据保留策略或回滚平台；本轮只禁止破坏现有流程。

这些内容不是“已完成”，而是 `OUT_OF_SCOPE`。它们不能被写入本轮开发任务、验收结论或商业承诺中。

### 3.3 明确不做的 P1/未来能力

本阶段禁止顺手加入以下内容：

- 跨 VPS 统一控制平面、跨站点 HA、负载均衡和多地域容灾；
- 用户自选 provider、账号、lane、价格或调度策略；
- Provider Node、供应商入驻、收益分成和提现；
- 公开模型市场、供应量交易、动态报价爬虫和自动采购；
- weighted、cheapest、capacity-aware、预测型或用户偏好调度；
- 支付、充值、退款、优惠券和客户级账单体系；
- 重新设计全部 Sub2api 管理后台；
- 为了“更通用”而新增第二套模型注册中心或第二套账务系统。

如果某个媒体能力无法在前三条线的目录、前端和计费证据中完成端到端验证，则只能从统一公开目录隐藏或标记为内部 Beta，不能因为已有 adapter 就宣称可商用。

## 4. 目标管理员体验

### 4.1 默认页面结构

`/admin/unified-gateway` 默认只保留四个区域；不把数据库结构直接暴露给普通管理员：

1. **统一 API 状态卡**

   显示：统一入口地址、当前统一分组、已发布模型数、可用模型数、需要处理数、最近检查时间、运行开关状态。分组由服务端固定；页面只读显示，不让管理员在主流程中选择其他分组。

2. **模型目录表**

   默认列：

   `模型 | 类型/Provider | 可用线路 | 备用线路 | 计费方式 | 状态 | 操作`

   只提供一个模型名搜索框和“配置此模型”操作。provider、端点、候选数量和阻断原因在行内/折叠详情中显示；本轮不新增批量测试、批量隐藏或批量发布，避免把单模型计费确认误做成批量操作。

3. **配置与价格摘要**

   点击模型后自动带入第一条资格通过的线路，并把其余资格通过的线路作为备用线路。每条线路都显示一组简化价格字段，管理员无需打开高级设置即可分别填写价格；页面显示首选/备用、价格来源、计量单位、倍率和用户价预览。模型名、端点、账号和 provider 在主流程中只读，默认不显示内部 ID。

4. **API 使用卡**

   显示统一 base URL、支持的 endpoint 和模型列表入口提示。Key 生命周期、限额和健康运营不在本轮新增页面中。

高级设置折叠在“高级线路设置”中，只保留确实需要的 priority、provider model、endpoint、binding、来源分组导入和 retry 信息。普通管理员不需要打开它。

### 4.2 配置向导

#### 第一步：选择模型

- 从候选目录选择一个模型，显示 Provider、endpoint、能力和当前状态。
- 主流程不提供公共模型名自由输入；只允许从候选目录选择，避免误拼出不可执行配置。
- 同名模型存在多条线路时显示“可选择首选和备用”。

#### 第二步：确认线路

- 系统默认选择第一条资格通过的线路。
- 其他资格通过的线路作为备用线路。
- 资格失败的线路显示具体原因，不允许误选发布。
- 同一账号、同一模型、同一 endpoint 的重复 lane 给出阻断或明确合并提示。

#### 第三步：设置价格

普通管理员只看到两种业务语言；来源分组导入保留在高级设置：

- `上游基础价 × 上游倍率 × 管理员加价`；
- `手动按 token/次/图片/视频设置价格`。

来源分组导入仍可在高级设置中使用现有服务端导入/预览接口，不在主流程复制一套价格逻辑。

高级字段 `rate_mode`、`rate_basis`、`pricing_schema_id`、`base_price_semantics`、`rounding_mode` 只在展开详情后显示。

页面必须展示一个服务器计算的示例：

```text
输入：1000 token / 生成：500 token
命中线路：Claude Pro / account-xx
计量方式：token
上游价格来源：manual v3
用户加价：1.20x
预计用户费用：$x.xxxxxxxx
失败费用：$0
```

示例不得由前端自行复制计价逻辑；前端只展示服务端 preview 结果。

#### 第四步：测试并发布

- “保存草稿”和“服务端校验”不作为普通管理员必须理解的独立步骤：点击“试算价格”或“发布”时由前端自动保存并调用服务端校验。
- 先由服务端重新检查当前模型和各线路的调度资格；高级设置仍可调用既有 capability probe。
- “试算价格”只调用服务端 preview，不向真实上游发送业务请求；页面明确展示命中线路、计量单位、价格来源、用户单价和失败 `$0`。真实 provider response、usage 和账单对账继续由独立 E2E/回归矩阵验收，不伪装成前端 dry-run 结果。
- 只有没有 P0 blocker 时才能发布。
- 发布成功后显示 revision/digest 和发布时间；回滚按钮不在本轮新增。
- 发布不会自动打开旧入口，也不会修改旧 Group。

### 4.3 状态语言

| 状态 | 管理员看到的文案 | 运行行为 |
|---|---|---|
| `ready` | 可用 | 可进入统一模型目录 |
| `blocked` | 暂不可发布 | 不进公开目录，不发送上游 |
| `disabled` | 已隐藏 | 不进公开目录，保留历史数据 |
| `unknown` | 等待检查 | 不允许当作可用发布 |

## 5. 运行与计费契约

### 5.1 可执行目录闭包

公开模型必须满足以下全部条件：

```text
published config
  → enabled route target
  → enabled account binding
  → account active + schedulable
  → provider/endpoint/model capability matched
  → valid price profile
```

任何一步失败，模型只能进入管理员问题列表，不能出现在 `/unified/v1/models`。

### 5.2 计价来源优先级

```text
明确的 account override
    > pool/source override
    > lane 手动规则
    > 已审核的 probe 结果
    > 无价格：fail closed
```

实际顺序必须以当前已有 pricing resolver 的契约为准；本文件不授权重写已有价格解析器。无论来源是哪一种，最终都必须写进本次请求的不可变 price snapshot。

### 5.3 调度与计费必须同源

禁止以下错误流程：

```text
先选 Provider A
再按统一 Group 价格计费
```

必须是：

```text
选中 route/account/profile
    → 冻结 snapshot
    → 发送上游
    → 成功 capture / 失败 release
```

重试时每个 attempt 都要可审计；失败 attempt 用户费用为零，只有成功交付的 attempt 产生用户费用。

### 5.4 协议边界

- OpenAI-compatible 文本、Responses、Chat Completions 继续使用统一入口。
- Anthropic 原生线路必须显示该模型支持的请求形式，不假设所有模型都能被同一 JSON 体调用。
- 图片按 image profile/按次规则计费，不套 token 倍率。
- 视频必须固定创建时的 route/account/profile；创建成功不代表用户已经成功交付，只有最终结果有效并交付才 capture。
- Grok 视频若未完成创建—等待—轮询—结果读取—失败释放的真实端到端验收，必须隐藏或标记 Beta。

## 6. 最小实现拆分

### M0：实施前冻结与基线

只读完成：

- 固定当前 commit、工作树、镜像、数据库 schema、旧 `/v1` 模型目录和账单摘要；
- 生成脱敏 catalog、route 和 price 快照；
- 确认指定 Unified Group 唯一标识；
- 固定现有发布流程的只读信息，不新增迁移/回滚流程；
- 运行 secret scan 和旧链路 smoke baseline。

交付物：基线记录、脱敏快照、旧链路对照报告。

### M1：模型目录与发布阻断

允许修改范围：

- `backend/internal/service/unified_gateway*.go`；
- `backend/internal/repository/unified_gateway*.go`；
- 现有 unified admin handler/route；
- 必要时的统一迁移文件，但只能增加 P0 所需字段/索引；
- 对应 service/repository/handler 测试。

实现要点：

- 统一 `ready/degraded/blocked/disabled/unknown` 状态；
- 发布前和发送前都执行资格/能力/价格闭包检查；
- 下架不删除历史账务；
- stale/deleted account binding 不得生成候选；
- public models 只返回 ready/degraded 中实际有可用候选的模型；
- 失败时返回明确 blocker code，不使用默认价格或默认账号。

### M2：管理员极简前端

允许修改范围：

- `frontend/src/views/admin/UnifiedGatewayView.vue`；
- `frontend/src/api/admin/unifiedGateway.ts`；
- `frontend/src/types/unifiedGateway.ts`；
- `frontend/src/i18n/locales/{zh,en}/admin/unifiedGateway.ts`；
- 对应 Vitest/API contract 测试。

实现要点：

- 保留当前聚合编辑器的 API 契约和高级能力；
- 默认页面改成状态卡、可搜索目录、配置/价格摘要、API 使用卡；
- 主流程只保留“选模型 → 填价格 → 试算 → 发布”；保存和校验由试算/发布自动完成；
- 统一分组、公共模型、端点和首选线路在主流程只读显示，避免普通管理员误选内部对象；
- 复杂字段默认折叠；
- 服务端返回 preview，前端不重算价格；
- 未填写的价格、倍率和来源版本保持空值，不显示 `1.0`、`1.2` 或其他伪造默认价格；
- blocker/warning 以中文业务语言显示，并可展开查看内部原因；
- 发布/禁用仍走现有 step-up、revision、idempotency 和 audit 机制；
- 不新建用户 Provider 选择页面，不新增 key/限额页面。

### M3：计费预览与发布阻断

只在现有 unified pricing/snapshot 能力不足时补最小适配：

- 发布前返回服务端计算的 route/account/profile/price preview；
- 无价格、无有效倍率、basis 不匹配、来源版本无效时返回 blocker；
- 保证成功 capture、失败 release、重试不重复扣费；
- 保证 preview、snapshot 和实际 charge 使用同一 resolved route；
- 不新建第二套余额账本、不新建客户级计费、不新建用户 Provider 偏好。

### M4：前三条线代表性端到端验收

按已有真实授权测试账号，对每个实际发布的 provider 类型至少做一个最小请求：

- OpenAI-compatible/GPT；
- Claude/Anthropic；
- DeepSeek 或其他国模；
- Ark/豆包（若纳入目录）；
- 图片（若纳入目录）；
- Grok 视频（只有完整异步闭环通过才可纳入目录）。

每个成功请求必须有 route/account/profile/snapshot/charge 证据；每个失败请求必须有 release/no-charge 证据。

## 7. 非干扰与权限边界

### 7.1 旧链路保护

P0 实施不得修改以下旧行为：

- 旧 `/v1/*` 路由、认证和响应格式；
- 旧 Group 的模型目录、账号池和价格；
- 旧 API key 的 scope；
- 旧 `usage_logs`、旧 billing 和旧 provider 调度；
- 旧登录、首页、管理后台其他页面；
- 现有 Grok/图片/视频专用入口。

任何修改旧 gateway 核心文件的需求必须单独建立契约，不得以 P0 顺手重构处理。

### 7.2 管理权限

- 读取、草稿、验证、发布、禁用和快照操作分别检查权限；key 生命周期操作不在本轮改动。
- 管理员只能操作自己 scope 内的统一 Group、模型、线路和账号绑定。
- 超级管理员也不能绕过 account/group scope 和 capability 校验。
- 所有本轮写操作保留 actor、reason、revision、digest 和 idempotency 证据。

## 8. 测试与验收门槛

具体 ID、预期证据和审计记录见 [23-p0-commercial-readiness-audit-checklist.md](./23-p0-commercial-readiness-audit-checklist.md)。本阶段最低必须通过：

1. 目录只显示可执行、有价格模型；无价格/无账号/能力不匹配模型无法发布。
2. 同名模型切换不同线路时，preview、snapshot 和 charge 不串价。
3. 成功 capture、失败 release、重试不重复扣费，异步最终失败不收费。
4. 管理员可以用四步主流程完成模型配置；高级字段默认隐藏；前端不自行计算最终价。
5. old `/v1`、旧 Group、旧 key、旧账单完成 before/after 对照，且没有被本轮配置改写。
6. 实际纳入公开目录的每类 provider 至少有代表性成功和失败请求证据。
7. secret scan、日志脱敏、权限/IDOR、审计字段检查通过；这些是通用安全底线，不代表本轮开发了 Key/运营/回滚功能。
8. 所有 P0-A/B/C blocker 清零；任何未验证项必须从公开目录隐藏或标记为 Beta。

## 9. 发布顺序与停止条件

### 9.1 发布顺序

```text
文档冻结
  → 本地测试/静态检查
  → 隔离 PostgreSQL 与 provider staging
  → 只读 catalog 校验
  → 统一 Group 小范围 canary
  → 扩大内部用户范围
```

P0 通过不等于直接全量公开。至少要经过小范围已有统一 key canary，并保留旧入口独立可用；本轮不新增 key 生命周期、健康告警或回滚平台。

### 9.2 任一条件出现立即停止

- 旧 `/v1` 的模型、认证、费用或响应出现非预期变化；
- unified 请求回落旧 Group/旧入口；
- 发布后出现无价格、默认价、错误倍率或失败扣费；
- `/unified/v1/models` 出现没有可调度候选的模型；
- stale/deleted/unschedulable account 被选中；
- Grok/图片异步任务无法恢复、重复结算或错误判定成功；
- 日志、错误、前端网络响应出现完整 secret 或原始 provider credential。

## 10. 完成定义

本阶段只有同时满足以下条件才可标记 `P0_ABC_ACCEPTED`：

- P0-A、P0-B、P0-C 的必要实现和复用项完成；
- 23 号审计清单中所有 Blocker 为 `PASS`；
- 本地、隔离 staging、至少一个真实授权 provider 代表链路均有证据；
- 旧 `/v1` 和旧账务完成非干扰对照；
- 管理员可以不接触内部 ID 完成四步配置；
- 价格、路线和失败不收费都能从 snapshot/ledger 复算；
- 没有任何未披露的 P0-A/B/C blocker；
- P0-D/E/F 明确记录为 `OUT_OF_SCOPE`，不能被写成已完成。

如果只是代码测试通过、真实 provider/账务/非干扰证据不足，状态只能是 `P0_ABC_IMPLEMENTED_WITH_GATES`，不能称为前三条线已完成。

## 11. 参考资料

- [Wokey API gateway](https://wokey.ai/api)：统一 Key、OpenAI-compatible 接口、路由/计费/限额/供应状态的产品参考。
- [Wokey Models and Pricing](https://wokey.ai/models)：模型目录、能力、厂商和价格展示参考。
- [KIE Getting Started](https://docs.kie.ai/)：模型市场、Playground、Key、限流、日志、异步任务和保留策略参考。
- [KIE Market](https://docs.kie.ai/market/quickstart)：图片/视频/音频/聊天模型的统一市场和模型专属参数参考。
- [17-minimal-unified-group-development-plan.md](./17-minimal-unified-group-development-plan.md)：内部统一 Group 最小范围。
- [18-minimal-unified-group-audit.md](./18-minimal-unified-group-audit.md)：上一阶段独立审计结论。
- [21-local-unified-api-key-and-live-probe-20260919.md](./21-local-unified-api-key-and-live-probe-20260919.md)：真实测试 key、模型路由和账单证据。
