# 实施计划：修已有 SmartRouter，不叠加第二套图片熔断

状态：2026-10-19 先行设计，已按用户“全部线路不可用时尝试之前失败线路”的澄清修订。以下“必须”是当前验收要求；早先全冷却硬拒绝提案已被替代，历史见第8节。最终实现与测试以 [ACCEPTANCE.md](ACCEPTANCE.md) 的候选身份和证据为准。

## 1. 问题与证据边界

基线：`6db55037080fbd994a738d3601efcf4a4d782730`。本轮只读检查确认：

1. `openai_image_poll_timeout_failover.go` 在独立配置开关下识别真实 HTTP 400 中的结构化超时代码，产生专用 failover reason；这不是一套完整的跨请求健康准入机制。
2. `smart_router_scheduler.go` 的重排依赖 advanced scheduler；空排序、lane ID 冲突及部分 fallback 会回到 native 候选。`core/router.go` 的 GPT-image 冷却过滤在全部 lane 都冷却时保留最后兜底，这是用户有意保留的可用性策略。本轮要修复的是健康备用可用时仍绕过状态，以及最后兜底缺乏统一准入/并发约束，不能把全部冷却兜底本身当成应删除的缺陷。sticky、fallback、slot 等待后的重新校验必须单独检查。
3. 健康键已经按 lane、capability、精确模型隔离，但专用 failover 可沿旧 transient 路径触发账号级调度阻断；“健康键隔离”不等于“共享文本调度不受影响”。
4. 图片校准接口没有接收目标 model，执行端使用固定 `gpt-image-2`；不能用该探测的成功恢复另一模型的失败状态。

约 140 秒超时是需要解释的现象，本轮尚未取得独立的生产请求/账号/健康事件关联证据。耗时本身不能证明触发了哪个 timeout，也不能证明已正确切号。同一 base URL 可能承载多个账号；URL 相同不能作为“仍使用同一账号”的判据。验证须关联请求 ID、account ID、lane、capability、模型、尝试序号、失败分类和健康状态。

## 2. 单一权威状态

以现有 SmartRouter HealthTracker/ledger 为唯一图片恢复状态源，复用 `HealthKey(lane, capability, exact model)`、恢复阶段、冷却时间、失败计数和恢复优先级。不新增平行图片熔断 map、独立 cooldown 配置、独立轮询任务或 300 秒专属惩罚。

- 有效 lane 身份必须稳定且能区分账号。当前实现决定：默认 `account:<id>` 保持稳定；自定义图片 lane ID 加账号限定为 `account:<id>/image/<custom>`，超长自定义值经 hash 约束到 ledger 的 255 字节限制内。文本等其他能力不改 lane 身份。重复自定义名称不再合并不同账号的图片健康，普通 native fallback 不能隐式变成最后兜底授权。
- 生成与编辑分别记账；`gpt-image-2`、`gpt-image-2.5` 及其他受支持图片模型不能相互清除失败或共享冷却。健康键采用本次选择使用的精确 routing model，观察和转发前校验使用同一份上下文，避免请求别名/映射模型不一致。模型归一化必须与现有 exact-model key 一致，不把模型家族、通配符或 OAuth 内部文本主控模型当成图片成功证据。
- 本轮受管 `/images` 和实际 Responses→Images bridge 的图片瞬时失败不得借该图片路径调用账号级 `BlockAccountScheduling` / `SetTempUnschedulable` 来影响同账号文本、embedding、视频或另一图片能力。既有独立认证/余额等保护不因此取消；下述 generic native Responses 例外不能被省略。
- 不修改管理员设置的基础价格/优先级；恢复优先级是现有健康状态的临时投影。
- 观察、排序、最终准入、恢复和持久化必须读写同一键；不能靠“失败时写了日志”替代下一请求的准入约束。

### HTTP 传输边界

完整图片错误分类与 HTTP 副作用隔离覆盖 `/images` 和真正派发到 Images 的 Responses→Images bridge。若解析为图片 bridge 的请求选中显式 native-mode 账号，仍在等待 slot 后执行 scoped admission，并只做一次 scoped image result 观察；实际转发保留 generic native Responses `Forward`，不设置 ImagesEndpoint 标记，也保留其原有全局 HTTP/auth 副作用。普通或混合 Responses 协议不在本轮扩大改写范围。因此“有图片 scoped 观察”不能等同于“所有底层账号级 HTTP 副作用均被移除”。

## 3. 严格识别仍保留，独立功能撤销

移除 `gateway.image_poll_timeout_failover_enabled` 的活动配置、默认值、示例和独立启用语义。严格识别的上游超时进入现有有界 native 单请求 failover；跨请求健康记录/准入/恢复由现有 SmartRouter 开关及 lane 准入控制。SmartRouter 关闭时保留有界 native 单请求切换，不假称仍有跨请求健康保护；该超时代码也不得退回账号级惩罚。旧键不能悄悄重新启用另一套机制。关闭或账号未纳入时的行为必须明确测试。

保留窄分类边界：真实上游 HTTP 400、有效 JSON、受支持字段 `error.code` / `response.error.code` / `code` 的字符串 trim 后精确等于 `image_poll_timeout`。大小写变体、前后缀、普通文字、message 内嵌 JSON、非法/截断 JSON、非字符串 code、HTTP 200 SSE/model text 不得命中这条特例。分类属于 Images 错误处理，不扩散到文本/视频。

管理员 passthrough、账号错误码策略、OAuth 主控模型 gate 的既有优先顺序保留；不能通过新分类覆盖其终止决定。已有 `failover_on_400` 等独立通用策略不因删除旧图片开关而被重定义。

## 4. 从故障到下一请求的完整路径

1. 图片尝试结束时，区分可重试上游故障、请求自身取消/总预算耗尽、请求错误/内容拒绝及已有真实输出；只有相应上游故障更新该 scoped health。
2. 同一请求使用已有失败账号排除集和切换预算，不在同账号循环重试该超时；真实输出后不重发生成。可重试失败的 nil result 与计量规则沿用既有实现。
3. 正常选择阶段在 advanced/非 advanced、sticky、普通候选、native fallback、空 core 排序和等待 slot 后均检查 scoped health。已知 Cooling 不能作为普通健康候选被悄悄塞回；恢复中sticky也不能越过健康候选或绕过探测选择。
4. 先尝试所有满足原生资格的正常候选，包括受管和原生可用的未受管候选、健康overflow及订阅偏好路径。只要能取得健康账号槽位或返回既有有界 `WaitPlan`，就使用该结果，不因为健康账号暂忙而抢先重试冷却线路。
5. 只有正常容量/有界等待都未给出可用结果，才进入显式、仅图片的最后兜底阶段。`AccountSelectionResult.SmartRouterImageLastResort` 标记本次选出的单账号，handler将标记传给 `AcquireSmartRouterImageAttempt`；`RouteRequest.ImageLastResort` 与 `ImageLastResortAdmission` / `AcquireImageLastResortAdmission` 复用同一 HealthTracker、键和 token/lease，允许 `RecoveryCooling` 在原deadline前尝试。资格判定和取得lease不清除原cooldown、失败计数或stage，待真实结果再更新；这不是新的公开配置、计数器或计时策略。
6. 最后兜底仍遵守账号启用、认证、group/model、余额/配额、profit、并发等原生过滤、每请求failed account IDs、切换次数和总/尝试预算。`RecoveryModelUnavailable` 在deadline前继续硬阻断；Cooling可能来自多种失败类，当前快照没有持久化最后failure class，故不能声称兜底仅对timeout开放，也不能假造timeout专属判断。
7. 取得slot后、实际转发前按所选普通/最后兜底路径再次准入。准入拒绝的账号加入本请求既有failed account IDs并释放资源，不在同一请求中通过LR重新引入；如果这是唯一账号或所有剩余账号均被排除，本请求可以返回503。后续新请求拥有新的排除集，可在正常选项耗尽后显式LR尝试仍在Cooling的账号。取消或未转发只释放租约，保留健康状态；成功进入既有warming，失败沿现有阶梯升级。该检查不能撤销已发送的并发请求，也不宣称分布式事务原子性。
8. 全冷却本身不是直接返回503的理由；有符合原生条件且可取得lease的Cooling线路时允许有界兜底。没有任何合格候选、租约占用、硬模型阻断或预算已耗尽时仍可按现有协议返回不可用/耗尽，不保证兜底成功。

当前适配决定：advanced 关闭但 SmartRouter 图片恢复启用时使用已有 default scheduler 执行受管图片准入；不翻转全局 advanced 设置，也不改变非图片请求的调度入口。健康overflow/订阅偏好与recovering sticky顺序均须专项回归。纯图片bridge关闭或mixed Responses保持原生排序，不能因请求含图片工具而扩大图片最后兜底范围。

正常、native-basic与最后阶段共用已有slot/数据库探查预算和排除集，不通过再次扫描刷新预算。健康容量判断是本进程顺序选择期间的观察，不是对所有副本/所有上游容量的原子全局认知；选择期间容量可能改变。每次最终准入仍复查当前所选scope。

备用账号不保证属于不同 host/source group。相同URL/source group只提供相关性或既有多样性线索，本轮不据单账号失败自动隔离整台host；也不能承诺同host其他凭据一定健康。验收以确实选到哪个account/lane及其状态为准。

## 5. 恢复与校准

复用已有加载默认恢复节奏：第一次瞬时失败 30 秒；第二次连续失败 600 秒；达到第三次连续失败阈值后到下一次 Asia/Shanghai 04:00。实际配置覆盖及不同失败类的既有规则仍须核查；这不是任意故障都固定套用三步的承诺。当天恰好 04:00 视为下一日边界，不能恢复到过去时间。

30秒到期只进入 `ProbeDue`，不是直接恢复Normal；`ProbeDue`、`Warming5`、`Warming25` 继续受同键租约约束，默认累计三次生产成功才到Normal。阶段名5/25不保证真实流量比例恰为5%/25%。最后兜底可在Cooling到期前取得同一套有界探测租约，不先把状态改成健康；成功沿既有warming，失败沿现有冷却/升级，取消保持原状态。

核心普通路径使用 `ImageAdmission` / `AcquireImageAdmission`；显式最后兜底使用对应LastResort方法，两者共享probe token、generation和租约。正常恢复或提前Cooling兜底都只允许同键一个未过期的进程内租约，release幂等。lease 截止取已有 attempt/request deadline 的有效较早边界；操作员禁用 transport timeout 时，以已有默认图片请求预算 600 秒限制占位，不因此重新启用 HTTP timeout。core 24 小时上限只是异常 deadline 的安全封顶，不是正常默认租约，更不是新增 cooldown。

每次实际图片尝试取得健康 generation，探测还取得 token。被释放/替代探测、过期探测成功及健康代次落后的普通成功不能清掉新失败；仍匹配且未释放的当前探测失败，即使已过 lease 时间，仍记录其自身 timeout，直到被替代。这限制的是单进程同键的有效占位；不能召回已经发出的请求，timeout 被禁用时请求可能活过 lease。不是多实例互斥或物理在途请求严格唯一的保证。最终组合还需覆盖所有调用方传递和释放。

校准必须把被恢复 scope 的精确 model 一直传到实际请求 payload，并把结果记录回同一 lane/capability/model。默认 `gpt-image-2` 探测只能证明对应模型；不得恢复 `gpt-image-2.5`。同能力其他模型已有新鲜成功，也不能跳过故障模型的必要校准。既有校准开关、每日调度、锁及时间/成本预算保留；本轮测试使用 fake transport，不启动真实收费探测。

校准保留既有零 token/generation 观察语义，不宣称与生产 half-open 同一并发屏障；现有校准锁/预算不等于生产请求与校准之间的全局互斥。校准的图片账号 lookup 与其他能力分别处理，不能因新图片 lane ID 丢失目标账号。

## 6. 持久化与重启

复用现有 ledger，核对冷却、连续失败、恢复阶段/优先级及精确 scope 在 restore 后一致。保留已有事件排序、旧事件不覆盖新快照、安全摘要和有界 persistence context。临时 half-open 租约与持久健康状态的生命周期分别说明，重启不能留下永久占位。

旧自定义图片 lane 行只按其持久化的已知 `AccountID` 在内存 restore 时映射到新账号限定身份；同键已有新 qualified 行时优先新行，不执行 SQL 批量重写。历史相撞而已经被覆盖的其他账号状态没有可恢复证据，不能重建或假称全量迁移成功。

已有 private `recoveryFailuresSinceStep` 窗口计数不在 `HealthSnapshot`/数据库中，本轮不增加 schema 或重写其语义。连续失败计数与该窗口的 reset/累加规则不同，不能用取模等方式反推。已持久化 cooldown、consecutive failures、stage 和已分配的 RecoveryPriority 可以恢复，但重启可能延后首次进入或下一次 +30 优先级升级；不能称“所有恢复计数完整恢复”。

没有 ledger、读取/写入失败时只能声明内存/best-effort 能力；恢复失败不可被报告为重启保护已通过。当前 ledger restore 是启动时恢复，进程内有序写入不提供多个副本间的即时健康广播或一致准入。half-open token/租约刻意不持久化，不能宣称重启保留原探测锁。历史错误写入的账号级临时阻断不得自动批量删除，因为它可能来自其他真实故障。

## 7. 实施顺序与停止点

1. 先建立本计划、兼容说明、测试矩阵与 pending 验收记录，保留历史材料。
2. 核心补齐 scoped admission / bounded recovery；适配层移除独立开关和限定图片传输范围的账号级旁路，接通受管图片选路及精确模型校准，保留上述 generic Responses 边界。
3. 编写真实入口和状态转换测试，覆盖 [TEST_MATRIX.md](TEST_MATRIX.md)，冻结候选后执行专项、聚合、race 及持久化验证。文档中的“必须”只有绑定本候选执行证据后才能改为“通过”。
4. 独立复核删除范围、未旁路状态、非图片回归和证据；再形成验收包。上传 GitHub、PR、发布、真实 Provider 和生产配置变更均另行授权，不随代码验收执行。

## 8. 已替代提案与策略修订记录

本目录初稿把“没有健康备用时拒绝全部Cooling线路”作为目标，并把全冷却立即不可用写入矩阵。这超出了用户要修复“有健康线却仍反复选坏线”的目标。用户随后明确：全部线路不可用时，应保留尝试之前失败线路的最后机会。当前第4–5节以“健康优先、正常容量耗尽后有界最后兜底”替代早先硬拒绝，不擅自改变30/600/下一04:00数值。

早先独立core通过与focused candidate1/2执行记录保留为历史。candidate2在config/core/service已结束后因这次策略修订中断handler，不能标记整个候选通过，也不能作为新LastResort策略的通过证据。新组合须重新冻结源码并验证。
