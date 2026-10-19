# 兼容性与操作边界

状态：按用户澄清修订后的要求与待验证项，未声明最终实现通过。全冷却硬拒绝旧提案已撤回；当前保留显式图片最后兜底。

## 配置与历史材料

- 删除活动 `gateway.image_poll_timeout_failover_enabled` 配置及示例，不新增替代图片专属 flag 或 300 秒冷却。统一恢复服从现有 `gateway.smart_router.enabled` 及账号准入；其默认仍为 false。
- 当前配置实现及专项测试按Viper容忍旧YAML/env键处理：遗留true/false不改变SmartRouter或通用FailoverOn400。部署时应清理旧键，不依赖parser报错；最终候选仍须重验该兼容行为。
- 不把旧键 `true` 自动迁移为启用整个 SmartRouter；两者作用范围不同。管理员需检查真实部署值，普通使用者不应自行调 cooldown 来绕过坏线。
- 计划中的行为变化必须明确：严格上游超时分类使用已有有界 native 单请求切换，不再受旧独立flag控制；SmartRouter关闭时也可发生这种切换，但不记录/执行SmartRouter跨请求健康恢复。关闭路径不能把该超时变回账号级惩罚。
- 历史 `docs/image-timeout-integration-20261018/` 保留，不改写其当时验收；新方案由本目录说明，旧“默认关闭独立flag”与账号级 cooldown 描述不得当成本轮运行契约。

## 业务兼容

| 范围 | 必须保持或明确改变 |
| --- | --- |
| 文本、compact、embedding、视频 | `/images` 与实际 Images bridge 的受管瞬时失败不新增全账号惩罚；这些能力原路由、限流、认证/余额保护和计量不改，不据此承诺 generic Responses 全局副作用被删除 |
| 显式native-mode / mixed Responses | generic `Forward` 保留原全局HTTP/auth副作用；已解析的图片bridge选中native-mode时有postqueue准入和一次scoped观察，但不加ImagesEndpoint标记；混合Responses不扩大改写 |
| 图片生成/编辑 | 分别记录和准入；一种能力恢复不清除另一种能力失败 |
| 图片模型与映射 | 以选择时精确routing model为统一键，别名/通道映射上下文贯穿观察和最终准入；其他模型成功不恢复故障模型 |
| 相同 URL / source group | source group 可用于现有排序或多样性策略，但不是账号身份；同 URL 的不同账号不得误合并健康或用 URL 证明未切号 |
| 同host相关故障 | 不从单lane失败自动升级为整host隔离；备用account可能共享host，不保证跨host切换或同host其他账号健康 |
| advanced关闭而SmartRouter图片启用 | 使用已有default scheduler执行受管图片准入，不改全局开关或非图片入口；恢复中sticky进入排序，健康overflow保持；须专项回归 |
| 全冷却/正常容量耗尽 | 健康可取得槽位或有界WaitPlan优先；都无可用结果才启用内部ImageLastResort，在同一SR状态和lease下尝试Cooling，不直接以全冷却为由503 |
| 最后兜底的硬边界 | 原生auth/disabled/group/model/quota/profit/concurrency及failed IDs/请求预算不放宽；RecoveryModelUnavailable在deadline前仍阻断 |
| Cooling失败类 | 快照无durable last failure class；允许的最后兜底不是timeout-only，不新增平行分类状态或数据库迁移 |
| 选路预算与观察 | normal/native-basic/最后阶段共用slot/DB探查预算与排除集；健康容量是顺序局部观察，不保证跨副本原子全局容量判断 |
| 共享历史统计 | 受管图片失败不再向共享文本统计追加惩罚，但尚无图片观察的新scope仍可使用已有共享先验；不宣称所有历史统计完全独立 |
| 自定义重复 lane ID | 图片custom ID加account限定，过长时hash至255字节以内；默认account:<id>稳定，其他能力身份不改 |
| 旧lane状态 | 根据durable AccountID在内存映射，已有qualified行优先，不改写SQL；历史被碰撞覆盖的其他owner状态不可还原 |
| 管理员错误策略 | passthrough、账号错误码策略、OAuth 模型 gate 先于特殊上游超时分类，具体适用账号类型沿旧规则 |
| 时间与输出 | 单次尝试和请求总预算、已到绝对 deadline 检查、用户取消、真实输出后禁止切号保持；不等 timer 投递才停止 |
| 并发与资源 | account/user/global-image slots、单请求 reservation 和 half-open lease 都须有界释放，不能在早退中泄漏 |
| 计量 | 可切换 nil-result 尝试不新增本地结算；后续成功沿已有计价；终止失败的可信已观察 usage 和 partial-image 不丢弃；余额/订阅均回归 |
| 响应协议 | 不伪造成功、不把 HTTP200 文本当成 HTTP400；postqueue内部deadline沿既有对外502/ops504映射，修正空200而不新增对外504契约；caller取消保持安静 |
| 健康日志 | 只持久化安全分类/固定摘要，不把任意 upstream body、私有 prompt、凭据或图片写入健康事件 |

## 不能由此次修复保证的结果

- 上游轮询超时不证明生成已终止。重试仍可能产生重复图片或 Provider 费用；一次本地 usage/结算不证明上游 exactly-once。
- 没有健康备用时仍尝试具备原生资格的Cooling最后兜底，不能保证成功；若全部候选失去资格、模型硬阻断或lease占用，返回不可用仍合理。兜底只持有同键一个未过期进程内lease，不能全量放开。
- Cooling最后兜底不清除原cooldown/stage/失败计数；真实成功才warming，失败沿原阶梯，取消只释放占位。30秒到期也只是ProbeDue，默认三次生产成功到Normal，Warming5/25并非固定百分比流量承诺。
- 约 140 秒的耗时不构成独立的生产复现或账单证据；必须用请求与 account/lane 关联分析，不根据 URL 猜账号。
- 已在转发中的并发请求不能由新健康状态召回。迟到结果受token/代次保护；当前未释放探测自己的失败可在lease到期后记录，过期成功不恢复。half-open租约不持久化且仅单进程；ledger启动时恢复和进程内有序写入不等于跨副本一致准入或即时广播。
- lease使用现有attempt/request预算；transport timeout禁用时仍以既有600秒默认预算限定占位，并不重启HTTP timeout。24小时core封顶是异常输入保护；在途请求可活过lease，不能宣称物理并发全局唯一。校准零token/generation保留既有语义，尚未与生产探测共同fence。
- `recoveryFailuresSinceStep` 私有升级窗口不持久化，且不能从consecutive failure计数推导。重启保留已写入冷却/阶段/优先级，但可能延后下一个+30升级窗口；本轮没有新增数据库迁移解决此既有限制。
- 旧账号级阻断可能在升级后保留到期；不能只因来源可能是图片错误就删除所有 temp-unschedulable 状态。

## 验证与回退

先在云端 fake-provider/数据库测试下确认配置和上述边界。真实 Provider 验证必须另行批准目标、数据、凭据使用和费用上限，不能以本计划自动发起。

当前LastResort是既有SmartRouter的内部最后选择阶段，无新增公开flag、计数器或计时策略；不能把客户端参数当成绕过准入的授权。旧硬拒绝提案和candidate2中断记录只保留历史用途。

本轮不提供旧图片专属flag作为回退按钮。若未来需要回退，先由管理员明确选择恢复上一受验包，或关闭整个 SmartRouter 并接受其全部范围的影响；不能把后者描述为仅关闭图片超时恢复。核查实际配置加载/重启方式，不假定热加载。回退不会撤销已发上游请求、费用和历史健康事件，不删除账单证据或擅自清空健康表。生产回退同样未执行。
