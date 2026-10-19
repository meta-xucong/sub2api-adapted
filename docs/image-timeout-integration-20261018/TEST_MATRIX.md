# 组合候选测试与证据矩阵

以下保留初始验收要求。本轮执行结果见文末“本轮执行状态”和 ACCEPTANCE.md；测试代码存在、旧候选通过或包级 PASS 不等于所有组合均已验证。

## 1. 新功能和最小行为边界

| ID | 场景 | 必须断言 |
| --- | --- | --- |
| C01 | 配置未设置、显式 false、显式 true；nil/零配置服务 | 默认关闭；只有明确启用才进入新分类；原 `failover_on_400` 独立有效，关闭新 flag 不承诺关闭其他已有重试 |
| C02 | 实际 HTTP400，有效 JSON 的 `error.code`、`response.error.code`、`code` 各为精确码；首尾空白 | flag on 命中，状态保持400，reason=`openai_image_poll_timeout`，result=nil，`RetryableOnSameAccount=false`；flag off 保持原路径 |
| C03 | 大小写变化、前后额外字符、普通400、其他code、code缺失/null/数字/对象、无效JSON、空body | 新分类不命中；若既有独立重试逻辑命中须单独解释，不误报为该 flag 引发 |
| C04 | code只出现在message字符串、嵌套JSON字符串、任意请求字段、自然语言/模型文本 | 不从 message/文本反解或做子串猜测，不触发新原因 |
| C05 | HTTP200 JSON语义错误、HTTP200 SSE错误/模型文本、其他真实HTTP状态中的相同code | 不进入新的HTTP400分类；既有SSE/语义错误和用量传播行为保持不变 |
| C06 | 管理员 passthrough 命中，body同时带精确码 | 既有管理员响应优先，新分支不重试、不改变映射状态/消息 |
| C07 | 账号策略不处理400，body同时带精确码 | 账号策略优先，保持原隐藏上游内部信息的终止路径 |
| C08 | OAuth主控模型错误与精确码同时出现 | 主控gate优先，不因新分支误冷却整个图片池；保留原终止分类 |
| C09 | 新reason写入ops/cooldown/health | 状态/原因可审计；固定、安全摘要，不把上游message或私有请求内容带入新增持久摘要 |

C02–C08 的分类器断言不能替代完整 service/handler 组合路径。每条 gate 用能同时满足特殊分类的输入证明优先级。

## 2. 路径、切号与计量

公共维度：`/v1/images/generations`、`/v1/images/edits`（包括适用 multipart）；上游 API-key、OAuth。每条受支持组合须有 handler → service → 合成上游的路径证据。若某组合不是产品支持路径，记录实际不适用原因，不能默默跳过。

| ID | 场景 | 必须断言 |
| --- | --- | --- |
| P01 | 上述四类组合，flag off，首账号返回真实400精确码 | 新flag不额外切号；响应与基线一致，slots/预留资源释放；避免用全局400开关掩盖测试意图 |
| P02 | 上述四类组合，flag on，首账号超时、次账号成功 | 第一次result=nil；切到不同账号且携带failed-account排除集；成功图片与响应保留，后续不再尝试 |
| P03 | 上述四类组合，flag on，所有可用账号返回精确超时 | 有界耗尽，最终错误符合已有协议；无图片成功记录，无请求挂死，所有资源释放 |
| P04 | 配置切换上限为小值，账号池大于上限；另测账号池先耗尽 | 不超过首次尝试+允许切换数；不重复已失败账号；精确码不走same-account retry；默认10和构造器兜底3的来源有配置/构造测试 |
| P05 | 超时→成功，使用计数usage repository、计费/扣费命令及并发资源桩 | 对每种实际支持的既有计价/余额或订阅路径，失败尝试不独立settle；最终成功按原逻辑一次，金额/字段与成功输入一致；不跨尝试聚合 |
| P06 | 全部超时耗尽，含错误body伪造usage/image_count | 不从该HTTP错误body制造结果、用量或扣费；本地usage/结算计数为0；slots/预留资源无泄漏 |
| P07 | 终止不可重试的非流式/流式失败确有可信正数、零或缺失用量 | 上轮实际观察用量计量规则不倒退；保留原错误，正/零/无usage和cache字段断言；不能因新分支变成“全部失败免费” |
| P08 | 既有不同HTTP错误、429限流、主控拒绝、图像编辑Retry-After控制 | 原错误优先级、限流/同账号重试规则及编辑专属响应头保持原语义 |

计价模式和资金路径按现有代码实际支持集合枚举，至少复用上一轮三类计价及余额/订阅相关回归。测试报告列出实际子例，不能把未覆盖组合写成通用保证。以上本地计量断言不证明真实 Provider 没有重复费用。

## 3. 取消、预算和输出边界

| ID | 场景 | 必须断言 |
| --- | --- | --- |
| B01 | 请求进入时已取消/已过截止；选择前、等待槽时取消 | 不发新上游请求，不占住并发/预留资源 |
| B02 | 首次尝试返回精确超时，同时下游取消 | 不再切换/请求第二账号；取消不新增Provider健康惩罚；清理完成 |
| B03 | 总预算在首尝试后耗尽；截止时间已过但context timer尚未投递 | 保留云端显式截止检查，不调度下一账号；请求总预算不被每次重试重置 |
| B04 | 独立attempt限时与总请求限时交错；0禁用行为 | 区分attempt timeout、新poll-timeout reason及用户/总预算结束；0语义与现有配置一致，无无限测试或真实等待 |
| B05 | 返回精确超时前已有真实下游输出/partial output | 不继续切号或覆盖已提交内容；使用实际handler写入边界断言，不仅测试分类器 |
| B06 | 已取得图片但随后上游报错/断开 | 保留成功/部分成功产物，按既有成功结果一次结算，不能丢图后重试 |
| B07 | 非流式JSON whitespace keepalive先写入；另测真实内容先写入 | 特殊keepalive计数和原HTTP200已提交边界保持；不能简单把任意写入都等同可重试或不可重试 |
| B08 | 请求结束与异步usage提交竞态、订阅context、slot释放 | 已授权的成功结算沿原独立计量上下文完成；不因调用方取消重复/丢失结算，无泄漏；定向race覆盖 |

## 4. cooldown、能力隔离和健康顺序

| ID | 场景 | 必须断言 |
| --- | --- | --- |
| H01 | image generation、image edit上报新reason；text上报相同reason/body | 仅图片特判timeout；不修改文本分类逻辑；各能力/模型健康键独立 |
| H02 | context取消/总预算结束后报告图片和文本结果 | 使用云端带context签名，保留取消/截止保护；不得用删参数或绕过检查使测试通过 |
| H03 | 生成/编辑超时触发cooldown；0禁用、倍率/上限 | 复用相应原基值、倍率、上限与禁用规则；不改永久账号状态/优先级 |
| H04 | 同一账号同时服务图片和文本；另一个未受影响账号 | 明确断言账号级runtime block与持久TempUnschedulable对共享文本选择的实际影响；另账号继续可选。不能把健康能力隔离误当调度完全隔离 |
| H05 | cooldown到期、repo失败的memory-only行为、请求排除集 | 既有到期恢复/失败语义保持，repo失败不使本请求重复选回已失败账号；固定安全原因 |
| H06 | 同tracker/key旧失败与新恢复乱序，含新图片timeout分类 | 新分类走既有有序投影；旧事件不覆盖新health/cooldown/priority；保留独立时间戳单调与原sink预算 |
| H07 | 多key、Restore、取消等待、超时、repo错误与真实选定PG回归 | 现有单tracker保证不退化；如执行PG须列实际测试终态和源绑定；不宣称跨进程一致累计/事件必达 |

## 5. 最终候选门禁与记录格式

下列保留初始门禁要求，实际结果见文末执行状态。运行器应复用上一轮经核对的命令/资源设置并保留精确选择器；禁止只写“go test通过”而省略排除项。

| Gate | 范围和证据要求 |
| --- | --- |
| G01 源码复核/编译 | 配置、handler、service、Smart Router及新增测试能在云端签名下编译；逐项核对最小差异与保留的云端修复 |
| G02 新专项与历史回归 | 对应C/P/B/H项目；明确真实handler/service与纯helper/mock边界；引用真实测试名称、子例和原始日志 |
| G03 unit、integration-tag | 最终冻结组合源码重新运行；分别列run/pass/fail/skip和命令排除，repository TestMain缺Docker提前退出不能算数据库测试执行 |
| G04 lint | 使用云端已固定golangci-lint版本与现有规则；记录版本、资源参数和原始输出，不能挪用远端旧CI失败或旧候选通过 |
| G05 race | 受影响的handler/service及Smart Router定向race，core全包race；明确不是全库race |
| G06 构建/版本 | Linux amd64 embedded构建与真实二进制版本元数据；记录代码SHA、tree、Go版本、flags、binary SHA256；构建通过不代表完整启动/部署 |
| G07 有关真实PG | 选定健康持久化相关回归使用隔离临时实例；保留实际run/终态、bootstrap overlay边界、DB版本、源hash、清理收据；不把历史PG结果当新执行 |
| G08 离线交付 | 源码/文档身份分开，bundle从所需base导入后HEAD/tree一致，fsck/路径/hash/附件核验；不push或创建PR |

每组收据最少包含：候选完整commit/tree、UTC起止时间、源码和工作区起止状态、完整命令、关键环境/工具版本、退出码、原始日志SHA256、通过/失败/跳过的分层计数、实际未执行项及原因。不同套件重复/父子case不得相加冒称独立场景。源码在门禁间变化时明确重跑范围和失效证据。

## 6. 不属于本次代码验收的自动通过项

真实Provider生成/编辑与费用对账、真实用户数据、生产账号池共享文本容量、生产代理/IP拓扑、生产迁移/回退、完整启动、浏览器CSP、外网依赖扫描、GitHub CI/镜像发布都没有因本矩阵存在而被执行或证明。后续真实Provider验收应另列授权、预算、实际输入/输出与Provider账单证据；任何mock结果都不能替代它。

## 2026-10-18 本轮执行状态

本节补充原计划，不删除其设计要求。最终受测代码为 `528820ea5e8f1694fc2a54ef3d3b1045869ff212`，tree `b31518278953eff35cdc154882cc1d57ce7417f0`。实际测试名称、命令、覆盖限制及源收据见[验收记录第3、5、7节](ACCEPTANCE.md)，机器身份与日志hash见[验证清单](VALIDATION_MANIFEST.json)。原计划不再统一标为“待执行”，但也不把未执行的输入组合或业务验证改成通过。

| 原矩阵ID | 本轮闭合状态 | 直接证据指向 |
| --- | --- | --- |
| C01–C09 | 选定分类/gate/安全摘要正反例通过 | config opt-in/defaults；`TestOpenAIImagePollTimeoutFailoverClassifierIsOptInAndExact`、`TestOpenAIImagePollTimeoutFailoverPreservesExistingGates`、reason/cooldown及Smart Router测试；完整名称见验收记录第3节 |
| P01–P08 | 438个handler矩阵与既有计量回归通过 | `TestImagesPollTimeoutIntegration`：API-key/OAuth direct/OAuth Responses、generation/edit、stream/nonstream、三计价及13类scenario；新矩阵为余额+JSON-edit，不是订阅×multipart的完整新交叉矩阵 |
| B01–B08 | 选定取消、绝对deadline、输出/partial artifact、keepalive和资源生命周期回归通过 | 同一438矩阵及`TestImagesSchedulerRequestBoundary`、attempt/request timeout、observed-usage、keepalive与inflight reservation命名回归；unit/race各自选择范围有区别 |
| H01–H05 | 选定图片/文本分类、context保护与账号级cooldown边界通过 | `TestSmartRouterClassifiesImagePollTimeoutAsTimeoutOnlyForImages`、`TestOpenAIImagePollTimeoutFailoverReasonScopesCooldownAndSmartRouter`及既有runtime-block/backoff回归；不声称cooldown能力隔离 |
| H06–H07 | 单tracker顺序与选定真实PG回归通过 | `TestHealthPersistence*`、`TestSmartRouterPersistenceSinkOrdersSnapshotsAndReleasesOnFailure`；本轮PG `TestSmartRouterHealthPostgresLateEventPreservesNewerProjection`双向子例及fresh-process restore；不承诺分布式exactly-once |
| G01–G04 | 已完成各自范围 | 独立源码复核；unit 12,162顶层/12,207子项通过、18skip；integration 6,821/6,216通过、20顶层+2子项skip；lint0 issues。repository因缺Docker的0条test-run单列 |
| G05 | 已完成各自范围 | 定向race70顶层/649子项、core全包race69/14均通过且0skip；不是全库race；两个测试稳定性修正亦进入定向race |
| G06 | 已完成构建和版本检查 | embedded build、go version -m及--version均exit0，绑定528820；不是完整应用启动 |
| G07 | 本轮新执行已完成 | 隔离PostgreSQL18.1、15顶层/22子项通过、0fail/skip；只换bootstrap，源码冻结及56进程/0残留清理收据；非完整Docker/Redis/多版本PG验收 |
| G08 | 待最终交付收据 | 最终文档提交、bundle/证据包、独立导入HEAD/tree/hash和附件核验由交付清单关闭；未上传GitHub |

计数相互重叠：438是838专项leaf的子集，也重复包含于unit与定向race。顶层与子项嵌套，不能相加成独立场景。初次86337聚合失败保留为失败历史；528820仅用两项测试隔离/虚拟时间修正后重跑最终门禁，未改变运行时代码或放宽分配断言。

真实Provider生成/账单、浏览器/CSP、在线依赖审计和生产启用/回退均未执行。具体非全组合/非穷举限制以验收记录第3节逐ID说明为准，不再追加无边界的测试范围，也不把这些限制隐去。
