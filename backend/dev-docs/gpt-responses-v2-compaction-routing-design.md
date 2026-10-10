# GPT Responses V2 压缩路由修正方案

状态：`DESIGN_AUDIT_PASS; ROUTE_ID_5_APPLIED; ROUTE_PREVIEWS_PASS; REFRESH_AND_MANIFEST_UNIT_TESTS_PASS; LIVE_COMPACT_E2E_PENDING`

日期：2026-10-10  
目标仓库：`D:\AI\SSH\sub2api-official-v0.2.13-20261003`  
官方基线：Sub2API v0.2.13，`3040209f205472038c1ba745a1bedd2edd9053b1`  
核验时 HEAD：`0036e1248cd4b1a10acf5982100a755b6be5e43c`

本文最初只定义单个 `gpt-5.6-sol` 问题。负责人于 2026-10-10 后续明确要求 `gpt-6-luna`、`gpt-6-sol`、`gpt-6-astra` 以及未来刷新发现的 GPT 模型都能自动适配；该新范围取代本文 §0、§3.1、§6 中“只允许精确 Sol 路由、不允许 GPT 前缀路由”的历史决定。新范围仍不授权改 compact 协议、账号/模型映射机制、费率、GitHub/VPS/部署。只对本地统一组 #16 添加 `gpt-` + Responses 前缀路由。

## 0. 本轮执行范围决议与只读预检

历史执行范围：最初“全面修复 GPT 模型问题”仅按**关闭当前记录中有证据的 GPT compact 缺口**解释。对应 YeToken #15 × `gpt-5.6-sol` × native Responses V2；`gpt-5.6-luna` 是已有成功正向对照。Sol 的 legacy `/responses/compact` 直连曾返回 503，按上游 legacy 入口失败单独记录，不通过本地 fallback 改写。负责人后来新增的 GPT-6 与未来 GPT 家族自动路由要求由 §9 承接。

2026-10-10 最初单模型实施前的只读快照核验了 18081 对应的实际数据库 `sub2api_phase5b_currenthead` 与统一组 #16。那时路由表有 `gpt-5.6-luna`（exact/any → OpenAI）、`grok-4.5`（exact/any → Grok）和 `glm-5.3`（exact/any → OpenAI），尚无 `gpt-5.6-sol` 路由。统一组账号目录显示 Sol 同时由 OpenAI 和 Grok 平台账号声明，包含 YeToken #15；管理员路由预览对 `gpt-5.6-sol` + Responses 返回“未匹配 — model is exposed by multiple provider platforms”。随后最初方案新增 exact route id 4（见 §8）；这段是历史快照，不代表新增 GPT namespace route 前的状态。运行容器为 `sub2api-v0213-404token-app-media-model-refresh-20261009`，镜像 digest `sha256:b617510c0f47cc33eb48a4dff0bd7ed4cf1d89b45ee666a83ef0328882dd7312`；镜像没有可验证的源码 commit 标签。故当前 HEAD 定向 Go 测试用于验证源码路径，真实请求只证明该运行实例加此配置的行为，二者不合并成“当前 HEAD 镜像实测”。

设计审计对最初已知 Sol 切片为 PASS；此前“所有 GPT 模型全部验收”证据不足。负责人新要求把自动路由扩展到 GPT 命名空间后，本文件下方 §9 定义最新决策与验收；此前的限制仅作为历史记录，不再控制本轮范围。源码/历史对照审计为 PASS：当前解析器先处理精确 Composite route，再判断账号归属歧义；旧版 priority arbitration 与 legacy Chat fallback 都不移植。

以下段落记录原始精确 Sol 配置，现由 §9 的 GPT 命名空间扩展取代：当时授权新增 `gpt-5.6-sol` exact/Responses → OpenAI；该行仍保留为更具体配置。当前新操作是在其上新增 `gpt-` prefix/Responses → OpenAI。两类 route 均只选择平台，不固定账号；`responses` endpoint 类别会覆盖根 `/responses`、当前 `/responses/*`、`/alpha/search`、`/realtime/calls` 和 `/live`，不覆盖 `/chat/completions`、`/messages`、Images 等类别。

以上是最初单模型阶段的历史路由快照与范围。GPT 家族扩展前的路由快照见 §9.4：route id 1–4 均保持不变，本轮只增加 route id 5。若本轮 GPT namespace 路由需要回滚，只删除本轮新增的 route id 5；保留先前已有的 exact Sol route id 4 和其他路由，也不恢复或清理其他历史/未跟踪工作区内容。

## 1. 结论

GPT-5.6-Luna 的受控本地测试证明：在请求进入正确的 OpenAI Responses 原生 handler、使用同一个上游账号完成压缩和续接时，现有 native V2 路径可以工作。该做法与 OpenAI/Codex 的协议语义一致：原生 V2 是流式 `POST /responses` 请求，由压缩触发信号启动；客户端收到 `compaction` 状态后，在下一次 Responses 请求中继续携带该状态。

历史单模型方案复用了 Composite 精确路由，将发生平台归属冲突的具体模型和 endpoint 路由到 `openai` 并保留模型 ID。后续 GPT 家族扩展由 §9 的 `gpt-` namespace 前缀路由覆盖；解析仍由原生 Composite resolver 先匹配更具体的 exact route，再处理 prefix route。两种规则都只选择 handler 平台，之后由 Sub2API 原生调度器选账号，不锁定特定账号。

这不是把所有 compact 问题判定为 Composite 路由故障。YeToken GPT-5.6-Sol 的历史本地请求没有保存完整请求体，运行镜像也没有可验证的源码 SHA；那次 `native_compaction_v2=false` 不能单独证明当前代码缺陷。route id 4 保存前的管理员预览曾复现 Sol 因多平台声明而未匹配，这构成当时添加 exact route 的独立理由；保存 id 4 后的当前预览不能再描述为未匹配。本轮会记录运行镜像摘要与路由快照；验收会明确区分当前运行实例行为和当前 HEAD 源码测试结果，不声称镜像与 HEAD 完全一致。

## 2. 证据与边界

### 2.1 已确认的本地与上游结果

- YeToken GPT-5.6-Sol：上游直连 native V2 返回有效 compaction item，并在同账号续接成功。此前本地统一网关请求返回 HTTP 200，但只得到普通 message，usage 标记 `native_compaction_v2=false`。该本地请求体未保存，旧运行容器没有可验证的源码 commit 标签，具体是请求形态、运行镜像还是 handler 选择尚不能定因。
- Wokey GPT-5.6-Luna：上游直连 native V2 及续接成功。2026-10-10 本地受控运行实例以稳定 `prompt_cache_key` 发起压缩和续接，两次均选中 Wokey #27，返回有效 `compaction` item，后续三条事实全部回忆正确；该运行实例没有可验证的源码 SHA。更早一次续接曾跨账号，但受控重测通过；不能据此声称所有客户端形态都有账号亲和保证。
- YeToken 国模普通 Responses 可用，不代表其 legacy `/responses/compact` 或 native V2 可用。三种压缩路径必须分开验收。

完整历史与请求结果见 `D:\AI\SSH\SUB2API-V0213-COMPACT-TEST-RUN-20261010.md` §4、§7、§8、§10；Luna 路由配置证据见 `backend/dev-docs/unified-gateway-wokey-pricing-coverage.md` §15.1；兼容性分类见 `D:\AI\SSH\SUB2API-V0213-COMPACT-COMPATIBILITY-RESEARCH-REPORT.md` §3、§6。

### 2.2 当前源码路径

当前 Composite 路由解析在 `backend/internal/service/composite_route_resolver.go`：

1. 先匹配显式 Composite route；
2. 再解析账号模型归属；若同一模型被多个平台声明，当前行为是返回 ambiguous；
3. 只有账号归属未命中时，才调用内置 `DetectModelPlatform`。

`backend/internal/server/routes/gateway.go` 的 `/responses` HTTP 路由随后根据已解析的平台选择 `OpenAIGateway.Responses` 或通用 `Gateway.Responses`。通用 Responses handler 虽可晚些时候检测 `gpt-*`，但那发生在 handler 已选定之后；它无法把请求重新派发到 OpenAI handler。通用路径会执行 OpenAI Responses 到 Anthropic Messages 的兼容转换。因此，在确有跨平台归属歧义且没有显式 route 时，静态代码存在“可识别 GPT 模型仍进入错误 handler”的风险。

`compositeRouteEndpointForPath` 把路径中含 `/responses` 的请求（包括根 `/responses`、`/responses/compact`、`/responses/input_tokens` 及当前挂载的其他 `/responses/*` 子路径），以及 `/alpha/search`、`/realtime/calls`、`/live`，都归入 `CompositeRouteEndpointResponses`。现有 Composite endpoint 配置不能只匹配根 `/responses`。同一模型的 exact `responses` route 会影响以上共享 endpoint 类别的目标平台选择；它不会影响 `/chat/completions`、`/messages` 等其他 endpoint 类别。native V2 与 legacy compact 的请求协议仍是不同的。

这个代码级风险与历史 Sol 观察相符，但由于原请求体、运行镜像版本和实际 Composite decision 未留存，仍是**待验证的因果假设**，不是已完成的运行时根因证明。

### 2.3 旧版修复为什么不直接移植

历史适配提交 `213ea460e11de58ade2daba33e93fc09345b75e2` 位于旧版分支，不是当前 HEAD 的祖先。其相关内容分成两类：

- 账号模型跨平台重名时，按账号组优先级和账号优先级选出一个平台。该规则改变所有 Composite 重名模型的隐式选线，不局限于 GPT 或 Responses；它会把当前 fail-closed 冲突处理改成静默选线。
- 已经进入 OpenAI handler 的旧 `/responses/compact` 请求，在账号不支持 native compact 时降级到普通 Chat Completions 摘要。它不负责将根路径 `/responses` 的 native V2 请求重新分派到 OpenAI handler，还会改变上游请求协议和压缩语义。

这两类变化均不是当前首选。第一类范围过宽，第二类解决的是 legacy endpoint 兼容，不是 native V2 handler 选择。

## 3. 选定方案：配置优先，限定到模型与 Responses endpoint 类别

### 3.1 配置动作

先只读核对统一组现有 Composite routes。若目标模型尚无等价规则，则在本地当前统一组 #16 为已确认的平台归属冲突模型添加一条 exact route：

| 字段 | 目标值 |
|---|---|
| public model | 当前待验模型原名，例如 `gpt-5.6-sol` |
| match | exact |
| endpoint | `CompositeRouteEndpointResponses` |
| target platform | `openai` |
| upstream model | 与 public model 相同，不做重命名 |

历史决定：首轮只处理 `gpt-5.6-sol`，不建覆盖所有模型的 `gpt-` prefix route。2026-10-10 §9 的负责人更新明确替换该限制，当前改为建立 GPT namespace prefix route。

该 endpoint 类别不等于只匹配根 `/responses`：同模型的 `/responses/compact`、`/responses/input_tokens` 和当前挂载的其他 `/responses/*` 子路径，以及 `/alpha/search`、`/realtime/calls`、`/live`，也会命中该 route。本轮把 OpenAI 目标视为该具体 GPT 模型跨这些 Responses 类 endpoint 的平台归属；保存前运行本地 route-decision 检查确认实际挂载路径与分类，保存后再以目标模型的 native V2 压缩和续接作为行为验收。配置模型不能表达“只路由 native V2 根路径”，因此本次不声称该规则只作用于 compact。

精确 Composite route 只指定目标平台，不指定账号。测试必须从请求/usage 日志中记录实际选中的 OpenAI 账号；若不是 YeToken #15，就只报告实际被测账号，不得把“目标平台是 OpenAI”误报为“目标账号一定是 YeToken #15”。本轮不改变账号映射或调度优先级来强行选择账号。

### 3.2 协议处理

原生 V2 请求继续走 `/v1/responses`：保留 `stream=true`、Codex compaction trigger、V2 beta 声明和上游 Responses 语义。已有 OpenAI handler 的 native V2 识别、转发和结果处理按当前实现运行。

不把 native V2 请求改写为 `/responses/compact`，不降级到 Chat Completions，不合成或伪造 compaction item，不改变 model ID、账号映射、SSE 生命周期、错误状态或计费。该 route 会让同模型的 legacy `/responses/compact` 也选择 OpenAI 平台，但请求仍按 legacy compact 协议运行；共享 endpoint 类别中的其他 `/responses/*` 子路径及 `/alpha/search`、`/realtime/calls`、`/live` 也会选择 OpenAI 平台。入站与上游是否逐字节相同不是验收条件；验收核对的是协议路径和必要语义没有被错误 handler 丢弃。

### 3.3 为什么暂不改代码

已有显式 Composite route 正是当前路由解析链最靠前的配置入口。Luna 测试前已有精确模型路由指向 OpenAI；测试结果与该路由使请求进入 OpenAI handler 相符，并在同一账号完成 native V2 压缩和续接。运行日志没有保存本次请求的 Composite decision/handler 名称，且实例没有可验证源码 SHA，因此只作为配置/行为先例，不作为当前 HEAD 的构建证明。Phase 5B 和蓝图均要求先用原生配置满足需求，只有证据证明配置不足才修改原生接线。

若 exact route 无法使请求进入 OpenAI handler，或在可复现请求下 resolver/dispatcher 仍选择错误平台，再补充最小代码 TaskSpec；当前文档不预先批准该代码变更。

## 4. 分阶段实施与验收

### 阶段 A：只读预检

1. 核对目标 group、现有 Composite route、目标模型 allowlist、相关账号的平台/模型映射和可调度状态；只输出非敏感摘要。
2. 核对实际运行镜像或二进制对应的源码 commit；若没有可验证标记，记录镜像 digest 和“源码 commit 未知”，并将运行验收限定为该实例行为。记录请求路径、stream、trigger、beta header 和模型名。禁止输出 API key 或凭据。
3. 确认是否有明确 exact route，以及是否存在跨平台同名 ownership ambiguity。
4. 保存本次测试的完整脱敏请求结构/哈希、响应和 `native_compaction_v2` 记录，使结果可复现。

### 阶段 B：本地无付费测试

使用假上游/集成测试证明：

- explicit exact route 优先于账号 ownership ambiguity；target 是 `openai`，模型名不变；
- 同一 route decision 对所有当前挂载的 `/responses/*` 子路径（含 `/responses/compact`、`/responses/input_tokens`）、`/alpha/search`、`/realtime/calls`、`/live` 使用同一 Responses endpoint 类别；只验证该模型的本地假 route decision，不产生真实费用；
- HTTP route 进入 `OpenAIGateway.Responses`，不会先进入通用 `Gateway.Responses`；
- 对符合 native V2 条件的请求，OpenAI handler 将其识别为 native V2，仍走根 `/responses` 与流式处理；
- native V2 根 `/responses` 使用根路径与原生流式处理；`/responses/compact` 仍保留 legacy 请求格式、资格和转发语义，但该 exact model route 会显式把它的平台目标设为 OpenAI；`/chat/completions`、`/messages` 等其他 endpoint 类别不受这条 route 影响；
- 未知且跨平台重名的自定义 alias 仍 fail-closed；显式 route 仍能明确解除该具体冲突。

### 阶段 C：本地真实链路验收

复用已知能返回 compaction item 的目标上游模型，并确保记录真实选中账号：

1. 发送一次最小 native V2 压缩请求；检查 HTTP/SSE 终态含有效 `type=compaction` item，usage 标记 native V2，模型、endpoint、账号和上游 request ID 可对照。
2. 将完整压缩输出原样用于后续 `/v1/responses` 请求并追加一个事实问题；尽可能保持同一 `prompt_cache_key`，检查续接成功及事实回忆。
3. 先复用已有的同模型、同账号、同协议上游直连证据作为对照；只有模型/账号/协议无法一一配对或上游状态发生变化时，才补发一次直接请求。分开记录本地 handler、账号选择和上游响应；不能把 HTTP 200 普通 message 计作 compact 成功。
4. 不重试、不扩成全模型矩阵。失败按入口 handler 错误、模型/账号调度、上游协议不支持、输出项丢失、续接账号切换分类。

### 阶段 D：代码变更放行条件

只有阶段 A/B 显示显式 route 仍不能选择正确 handler，且有可复现的本地代码证据时，才另开最小实现任务。实现前须明确：变更函数、影响的 endpoint/provider、 fail-closed 回归边界、定向测试和独立 Source Fidelity/General Audit。不得直接复用旧版 priority arbitration 或 legacy Chat fallback。

## 5. 验收矩阵

| 验收项 | 通过条件 | 不足以判通过的证据 |
|---|---|---|
| 路由决策 | exact route 命中，目标平台 `openai`，模型名原样保留 | 仅看页面配置或仅看请求 HTTP 200 |
| handler | 访问/测试记录证明进入 `OpenAIGateway.Responses` | generic handler 后期能识别 `gpt-*` |
| native V2 | 原生 V2 请求条件有效，usage/日志标记 native V2，未变成 legacy compact/Chat | `native_compaction_v2=false` 或普通 message |
| 压缩结果 | Responses 输出含有效 `type=compaction` item | 仅 HTTP 2xx 或过程事件 |
| 续接 | 完整压缩状态进入后续 `/responses` 请求，成功继续并回忆测试事实 | 只验证首次 compact response |
| 账号归属 | 本地和上游证据都指向实际目标账号/请求 ID | 只证明 route target 为 OpenAI |
| 回归边界 | unknown duplicate alias 继续 fail-closed；共享 `responses` endpoint 类别中的所有当前挂载路径目标明确；其他 endpoint 类别不受影响 | 只验证目标模型成功；声称 route 仅作用于根 `/responses` |

## 6. 明确排除

- 不将历史 priority arbitration 移植到当前 Composite resolver。
- 不把 native V2 请求降级到 legacy `/responses/compact`、Chat Completions 或 Anthropic Messages。
- 不新增 compact fallback、账户能力探测、账号亲和、重试策略、model alias 或新调度器。
- 不修改 Smart Router、账号模型映射、费率、用户余额、统一网关计费、其他 provider 路径。
- 本节为最初单模型任务的历史范围，已由 §9 取代。当前执行范围是本地统一组 #16 的 `gpt-` Responses 前缀 route 和定向测试；不改生产路由解析代码、刷新器、schema 或协议，也不授权替换运行镜像、GitHub 操作或 VPS 部署。

## 7. 依据与控制文档

- 当前官方适配蓝图：`D:\AI\SSH\SUB2API-V0213-ADAPTATION-BLUEPRINT.md`，已核对 Phase 3 和 5B 相关范围。
- Phase 3 Responses/T0 方案：`D:\AI\SSH\SUB2API-V0213-PHASE3-RESPONSES-T0-DEVELOPMENT.md`，明确不重实现官方 native compaction V2/legacy compact，不新增 compact 策略。
- Phase 5B 合同及最小优化方案：`D:\AI\SSH\SUB2API-V0213-PHASE5B-UNIFIED-LLM-API.md`、`D:\AI\SSH\SUB2API-V0213-PHASE5B-CODE-OPTIMIZATION.md`，配置优先、复用原生 handler 和 Composite 路由。
- 运行测试与互联网资料：`D:\AI\SSH\SUB2API-V0213-COMPACT-TEST-RUN-20261010.md`、`D:\AI\SSH\SUB2API-V0213-COMPACT-COMPATIBILITY-RESEARCH-REPORT.md`。
- OpenAI Compaction Guide：<https://developers.openai.com/api/docs/guides/compaction>
- OpenAI Compact API Reference：<https://developers.openai.com/api/reference/resources/responses/methods/compact>
- Historical adapted commit (reference only): <https://github.com/meta-xucong/sub2api-adapted/commit/213ea460e11de58ade2daba33e93fc09345b75e2>

## 8. 2026-10-10 本轮执行记录

独立设计复审对本文件最终版本为 PASS，限于 `gpt-5.6-sol` 已证实的路由缺口；明确不代表所有 GPT 模型已验收。独立复审指出的精确影响范围是：该模型的 Responses endpoint 类别会固定选择 OpenAI，包含 `/responses*`、`/alpha/search`、`/realtime/calls`、`/live`，并排除这些路径上的 Grok 候选；其他模型以及 Chat/Messages/Images endpoint 不变。

已在本地 group #16 保存 route id `4`：public model `gpt-5.6-sol`、exact、endpoint `responses`、target `openai`、upstream `gpt-5.6-sol`、enabled。表单中 upstream 留空，由当前服务端将 exact route 的空上游模型规范化为公开模型名。UI 保存后预览显示“已匹配，目标平台 OpenAI，上游模型 gpt-5.6-sol”；数据库确认原有三条路由未改。保存前后均核对账号 #15/#16/#27/#28 的 active/schedulable 状态；无账号凭据读取或修改。

运行库身份曾有一项审计疑问：静态 `app_data/config.yaml` 显示默认数据库名 `sub2api`，该库有 0 条 Composite route。进一步用活动 app 容器网络 IP `172.22.0.4` 对照 PostgreSQL `pg_stat_activity`，发现该 IP 的活动连接实际落在 `sub2api_phase5b_currenthead`；route id 4 与 UI 路由预览均存在于该活动连接数据库。连接数随运行变化，故不以某一时点的连接数量作验收条件。此次配置保存位置与服务实际连接一致；静态配置文件的数据库默认值不代表当前有效连接。该疑问由活动连接证据解决。

当前 HEAD `0036e1248cd4b1a10acf5982100a755b6be5e43c` 的以下定向测试均通过（exit 0）：

- `go test ./internal/service -run TestCompositeRouteResolver -count=1`
- `go test ./internal/service -run TestOpenAIGatewayService_SelectAccountWithScheduler_NativeCompaction -count=1`
- `go test ./internal/service -run TestOpenAIGatewayServiceForwardOAuthRemoteCompactV2PreservesResponsesWire -count=1`
- `go test ./internal/service -run TestOpenAIGatewayServiceForwardAPIKeyRemoteCompactV2PreservesResponsesWire -count=1`
- `go test ./internal/server/routes -run TestGatewayRoutesCompositeOpenAIOnlyEndpointsRequireOpenAITarget -count=1`
- `go test ./internal/handler -run TestNormalizeOpenAIResponsesCompactRequest_RemoteV2 -count=1`

真实 native V2 compact 与 continuation 尚未发送。用于受控本地请求的安全测试 key 为现有 active 测试 key #54；执行工具自动拦截了从数据库读取其保存值并作为本地 HTTP Authorization 凭据使用的命令。没有披露或输出 key，也没有发出任何 API 请求、产生费用或新增 usage row。运行实例 digest 与源码身份见 §0：源码 commit 未知，所以即使后续在此实例完成 live E2E，也只归类为该运行实例的配置/行为验收；当前 HEAD 的判断以源码定向测试为依据。

因此 §8 记录的是当时完成的单模型路由应用和源码测试；native V2 端到端结果仍为 **未测**。不能据此写成 compact 已修复或 GPT 全面通过。若回滚当时的 exact Sol 路由，应只删除 route id `4`；若回滚 §9 本轮新增的 GPT namespace 扩展，则只删除 route id `5`，不删除另一条。要完成运行时闭环，需要通过安全方式执行一次 compact 和原样续接，且不把 key 发到聊天中。

### 文档版本差异说明

负责人在 2026-10-10 本轮最后提供的控制指令取代之前版本；该指令列出的蓝图 SHA256 为 `1635154DFB8C36440E19B6C154198993A720A6B78B7BE3A5E8918F200C75A32B`。当前蓝图文件实测 SHA256 为 `F02DE1A3FBFD86892FB2BB8790881FE1C54CFF02BD06C62148BAAC5503142E51`；磁盘 `D:\AI\SSH\AGENTS.md` 实测 SHA256 为 `FCF5C7E658FD86F2301A3A32BD81D5708CDFE0EDDF8DD65D72450DEB04499278`。已查看当前蓝图对应段落：Phase 3 确认官方 native Responses/compact 覆盖部分不重复实现，Phase 5B 允许优先使用原生 Composite 配置；这些现行文字支持本轮复用原生 Composite resolver 增加精确 route 和有明确 GPT namespace 边界的 prefix route，不改协议代码。此记录不替换用户提供的新控制指令，也没有修改蓝图或源码基线。
## 9. 2026-10-10 GPT 家族及模型刷新自动路由扩展

负责人新增目标：让 `gpt-6-luna`、`gpt-6-sol`、`gpt-6-astra` 以及未来由模型刷新加入的 GPT 模型，在统一网关 Responses/compact 路径上自动进入 OpenAI 原生 handler，并保留请求模型 ID。继续复用 Sub2API 原生 compact 和 scheduler；不改请求协议、模型映射机制、刷新器、计费或其他 provider。

### 9.1 当前自动链路核验

- 本地 18081 的 `unified-api-internal` 组当前模型白名单关闭，故没有组级白名单阻止新模型；本扩展不改变白名单。
- 已保存路由含 `gpt-5.6-luna` exact/any → OpenAI，以及 `gpt-5.6-sol` exact/Responses → OpenAI；本次新增 GPT namespace route 前，三款 GPT-6 均没有覆盖规则。
- 管理页只读预览对 `gpt-6-luna`、`gpt-6-sol`、`gpt-6-astra` 的 Responses 请求均返回“未匹配 — model is exposed by multiple provider platforms”。所以它们不能依靠内置 `gpt-` detector 解决账号归属歧义。
- 刷新与路由是两个独立职责：原生 04:00 refresh 对支持的 source profile 获取模型目录并持久化 availability snapshot；Windows `Sub2API-Upstream-Model-Refresh` 04:10 sidecar 只处理 native run 标成 `manual_only` 的账号，并在成功 live catalog 后更新这些账号的 `model_mapping`。二者都不创建/删除 Composite route。数据库中 2026-10-10 04:00 native run 状态为 completed、eligible=0、succeeded=0、failed=0、skipped=18；04:10 sidecar 计划任务为 Ready、LastTaskResult=0，实际处理 18 个账号，其中 #15 unchanged、#27/#28 保存 32 个模型、0 failed。`-SelfTest` 通过，覆盖模型增删、别名/wildcard 保留、空/非 live/失败快照不保存。不要把这描述成“所有刷新账号都会写 model_mapping”。
- 只读数据库核对显示：统一组 #16 内 active 的 OpenAI 账号映射中已包含 `gpt-6-luna`、`gpt-6-sol`、`gpt-6-astra`；部分账号还列有 `gpt-6.1-sol` 与 `gpt-6-astra-fast`。该结果证明当前账号目录提供了调度候选，不等于已向上游实际生成验证。

### 9.2 冻结的最小解决方案

在统一组增加一条 `public_model=gpt-`、`match_type=prefix`、`endpoint=responses`、`target_platform=openai`、`upstream_model` 留空的 Composite 路由。前缀路由留空上游模型时，resolver 会对每个请求沿用完整原模型名。路由解析本来就是 exact 优先于 prefix，因此现有更具体的 GPT exact routes 继续生效；此规则覆盖没有更具体规则的 GPT 模型，包括 GPT-6 当前型号和之后仍使用 `gpt-` 前缀的目录项。

这是常驻的命名空间路由规则，不让每日刷新器逐条创建/删除 route 行。刷新器如何记录新目录取决于账号来源：原生支持的 profile 更新 availability snapshot；Windows sidecar 只对 native run 标为 `manual_only` 的账号在成功 live catalog 后更新 `model_mapping`。统一组不启用白名单；prefix route 在下一次请求时自动选择 OpenAI handler。这样将模型发现、账号可用性、Composite platform dispatch 保持为现有职责，不给刷新器增加管理路由的副作用。

自动出现模型列表和自动路由是两个条件链，不能仅凭 prefix route 推断完成。上游刷新必须成功发现具体模型；该 OpenAI 账号必须处于可调度状态，且新 ID 通过其 availability snapshot/`model_mapping` 资格；Composite `/v1/models` 与 Codex `/models` manifest 才可能列出该模型。实际请求还要求至少一个合格的 OpenAI 调度候选。未来 GPT ID 若只是被前缀规则命中、但不在任何可用账号模型目录里，不能因此被新增到模型清单，也不能变成可调用。

该规则只覆盖 Composite 的 Responses endpoint 类别：当前 `/responses*`、`/alpha/search`、`/realtime/calls`、`/live`。它不覆盖 `/chat/completions`、Messages 或 Images endpoint；本轮缺口是 Responses native compact，不据此宣称其他 endpoint 已覆盖。它也不证明任何上游一定列出或能生成新型号：只有刷新器取得成功的非空 live catalog、对应账号的有效 availability snapshot 或 `model_mapping` 资格、且原生 scheduler 存在有效 OpenAI-compatible 候选，才可实际调用。若未来模型 ID 不再以 `gpt-` 开头，需单独添加/调整命名空间规则。

风险审查边界：任何自定义 alias 若也以 `gpt-` 开头，在 Responses endpoint 上也会被显式送入 OpenAI handler；这与现有内置 `DetectModelPlatform` 的 `gpt-` 分类一致。本次操作前须检查没有更具体 route 把当前三种 GPT-6 型号重定向到其他平台；新 prefix 不应覆盖 exact 路由。不要把它改成 `any` endpoint。

### 9.3 当前实施与验收边界

本任务仅在本地 group #16 新增上述一条 prefix route，并在 `backend/internal/service/composite_route_resolver_test.go` 增加 focused resolver regression test；不改生产路由解析代码、自动刷新代码、数据库 schema 或前端。变更前保留现有 dirty worktree 内容。

### 9.4 本轮实施和验收结果

本地统一组 #16 已保存 route id `5`：`public_model=gpt-`、prefix、Responses、target OpenAI、`upstream_model` 空、enabled。数据库只读核对确认原有 route id 1–4 不变。该常驻规则让刷新到账号目录的新 `gpt-*` ID 无需逐个增 route；resolver 对空前缀上游模型使用具体请求模型 ID。

管理页 route preview 结果：

- `gpt-6-luna`、`gpt-6-sol`、`gpt-6-astra`、模拟未来名 `gpt-7-luna` 和 `gpt-next-preview-2030`：均命中 OpenAI，upstream model 与请求模型名相同。
- `claude-sonnet-4-6`：仍由原账号归属解析到 Anthropic；`glm-5.3`：仍命中原 exact OpenAI route。
- `gpt-6-astra` + Chat Completions：不匹配新 Responses prefix route（当前 ownership 仍未匹配）；这是预期边界。

独立只读设计/测试审计最终为 PASS。审计确认 exact/any 规则优先于 prefix/Responses，模型名保持原样，非 GPT 重名模型仍 fail-closed。源码定向测试（均 exit 0）：

- `go test ./internal/service -run TestCompositeRouteResolverGPTNamespacePrefixRoutesCurrentAndFutureResponsesModels -count=1`
- `go test ./internal/service -run TestSyncUpstreamModelCatalogReplacesSnapshotWhenUpstreamModelsChange -count=1`
- `go test ./internal/service -run TestFollowPolicyUsesSnapshotWhileManualPolicyPreservesRouting -count=1`
- `go test ./internal/service -run TestOpenAIConfiguredCodexModelIDsUsesRefreshSnapshotOnlyWhenManaged -count=1`

以上分别验证：具体及模拟未来 GPT ID 的路由、刷新目录更换、原生 follow snapshot 与 manual mapping 的不同准入方式、Codex 模型 ID 使用刷新 snapshot 的通用逻辑。Sidecar `-SelfTest` 与当天 04:00/04:10 运行状态如 §9.1。阅读服务实现还确认 Composite `/v1/models` 汇总各平台可用模型，Codex `/models` manifest 使用同一 Responses route helper 选择 provider。

仍未完成的运行时验收：没有使用 API key 请求 Composite `/v1/models` 或 Codex `/models`，没有发送真实 native compact + continuation 请求。因此不能声称刷新发现→用户模型列表→Codex manifest→上游请求的 HTTP E2E 已全链路通过，也不能声称这三款模型的 live compact 成功。本轮没有读取存储 API key，也没有生成上游费用。若要补齐该项，需通过本机安全提供的测试方式运行一个列表请求和一轮最小 compact/续接；不要把 key 发到聊天里。

本轮只改这份文档和 resolver 测试文件，并在本地管理界面新增 route id 5；没有改生产代码或刷新器、用户/账号映射、费率、数据库 schema、模型协议、镜像、GitHub 或 VPS。既有 dirty worktree 其他文件未改。

当前固定源码 HEAD 仍为 `0036e1248cd4b1a10acf5982100a755b6be5e43c`；原有 dirty source changes 和 untracked files 均不属于此次写入范围。没有 GitHub、VPS、镜像或发布操作授权。
