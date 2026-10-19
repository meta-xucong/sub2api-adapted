# 验收矩阵

下表是用户澄清后“健康优先、显式有界最后兜底”的待验证要求，**不是已运行测试清单**。旧全冷却硬拒绝提案已撤回。实际测试名称、候选 SHA/tree、命令、退出码、日志及 skip 须写入 [ACCEPTANCE.md](ACCEPTANCE.md)。旧测试、仅源码中存在测试、package pass 或模拟耗时数字均不能代替本候选证据。

| ID | 场景 | 必要断言 |
| --- | --- | --- |
| CFG-1 | 删除独立配置 | struct/default/example/活动调用无独立 flag；旧 YAML/env true/false 的真实解析行为明确；不意外打开 SmartRouter |
| CFG-2 | SmartRouter on/off；advanced on/off；账号纳入/排除 | 激活矩阵明确；统一保护不依赖 advanced；关闭时有界native单请求切换，但无SmartRouter跨请求状态且不退回账号级超时惩罚 |
| CFG-3 | advanced关闭而SmartRouter图片启用，随后文本/视频请求 | 复用已有default scheduler的图片路径有效；全局设置不变；非图片调度入口不被切换 |
| CLS-1 | 真实 HTTP400 + 三个允许 code 字段 + trim | 精确匹配分类为图片上游超时；不走同账号重试；保留状态与安全摘要 |
| CLS-2 | 普通400、HTTP200 SSE、其他状态、大小写/前后缀、message 内嵌 JSON、非法 JSON、object/array/null/数字 code | 不命中特例；普通错误与旧通用策略语义保持 |
| CLS-3 | passthrough、账号策略、OAuth 主控模型 gate | 管理员终止决策不被恢复分类覆盖；账号类型适用范围保持 |
| SCP-1 | 同账号 generation/edit；两种图片模型；文本/compact/embedding/video | 在SCP-4限定传输路径只影响失败scope，无受管图片瞬时失败导致账号级阻断；不跨能力/模型恢复 |
| SCP-2 | 相同 URL 不同账号；不同 URL；重复自定义 lane ID | 正确区分 account/lane；不因单lane失败自动隔离整host；不声称备用必跨host；重复ID不串用状态或 fail-open 绕过阻断 |
| SCP-3 | 请求模型别名、通道mapping、OAuth图片内部文本主控模型 | 选择、观察、最终准入用同一精确routing model；技术性内部模型不误作图片健康键 |
| SCP-4 | `/images`、实际Responses→Images bridge、选中显式native-mode、普通/mixed Responses | 前两者验证scoped HTTP副作用；native-mode验证postqueue准入与一次图片观察，同时记录保留generic HTTP/auth全局副作用；不把所有含图片请求写成完整隔离 |
| SEL-1 | 请求1失败A；请求2有健康B；正常候选与advanced关闭 | 第二请求优先符合原授权/模型/能力限制的B；B可取得slot或有界WaitPlan时不抢先重试Cooling A |
| SEL-2 | sticky A、weighted sticky、overflow、订阅偏好、空 core order | 普通选择不暗中恢复Cooling；正常健康替代优先于恢复探测，只有LR阶段可以提前探测Cooling |
| SEL-3 | 选中后等待slot期间A失败；等待结束到转发前检查 | 普通尝试拒绝已知变坏的A，释放资源并重新选择；若后续确需LR，必须显式标记/重新准入，不能隐式放行或忙循环 |
| LR-1 | 全Cooling、单Cooling账号；generation/edit及两模型 | 正常阶段无结果后可选一个符合资格的Cooling线路，core只在显式ImageLastResort且无合格非冷却lane时给出Cooling；不能仅以全冷却为由503 |
| LR-2 | 健康账号暂忙但有bounded WaitPlan；健康native未受管账号；健康overflow | 返回正常已取得或bounded-wait结果，不能提前进入LR；全正常容量及等待均不可用才允许LR |
| LR-3 | Cooling与RecoveryModelUnavailable；auth/disabled/group/model/quota/profit/concurrency | 仅Cooling deadline可被显式LR探测，ModelUnavailable deadline不放宽；所有原生资格仍生效，否决者零上游调用 |
| LR-4 | acquire/cancel/release、成功、失败 | acquire/release不抹cooldown/stage/计数；取消保持状态；真实成功warming，真实失败沿现有阶梯，无独立计数/配置/timer |
| LR-5 | 同键并发LR、普通恢复与LR竞争、过期/替代token | 共享同一lease/generation；一个未过期进程内lease，旧release/迟到成功不覆盖；披露非分布式与在途超lease边界 |
| LR-6 | 同请求已经失败的账号、切换上限、request/attempt及slot/DB探查预算、已有输出 | normal/native-basic/LR共用预算和failedIDs，不通过二次扫描重置；不重试已有真实输出；503/耗尽限于无合格候选/租约阻断等真实条件 |
| LR-7 | 多种failure class形成Cooling；普通文本/mixed Responses；客户端参数 | 不把LR误称timeout-only；原生auth等仍过滤；LR仅内部显式图片范围，无公开绕过开关 |
| REC-1 | 第1、第2、第3次连续瞬时失败；时间边界 | 默认30s、600s、下一Shanghai 04:00；30s结束只到ProbeDue；默认3次生产成功到Normal；Warming5/25不作为字面流量比例断言 |
| REC-2 | 普通冷却未到期、到期、多请求half-open；显式LR对照 | 普通路径未到期不放行，LR按专属条件有界放行；到期scope探测仍有界，其他scope互不阻塞；不声称跨进程互斥 |
| REC-3 | half-open成功/失败/取消/超时/未转发/早退 | 正确warming或重新冷却；占位释放或有界过期；无永久占位和并发放量 |
| REC-4 | 旧探测迟到、lease过期/替代、普通旧请求在新失败后成功 | token/健康代次拒绝迟到恢复；当前未释放探测的自身超时仍计失败；旧release不清除新lease；计量保持 |
| REC-5 | request/attempt限时启停、600s默认lease、core24h封顶 | lease截止计算准确且释放幂等；不把占位过期等同终止在途HTTP；校准零token不宣称与生产共同fence |
| CAL-1 | gpt-image-2.5失败，gpt-image-2成功；generation/edit分离 | 精确模型进入实际JSON/multipart payload和结果健康键；不同模型/能力成功不能恢复失败键 |
| CAL-2 | 故障模型已有其他模型的新鲜证据；重启后的故障scope | 计划仍包含对应精确模型；去重准确；保留校准开关、总预算、锁、取消与无真实Provider测试 |
| PST-1 | 内存restore、真实ledger写入后新tracker/子进程恢复 | 持久化cooldown/consecutive failures/stage/已分配priority/key保持；private升级窗口不持久化且重启可能延后+30；不把memory测试称真实PG |
| PST-2 | 并发观察、乱序sink、旧事件、恢复后再次观察 | 保留现有持久化单调/投影保护、安全摘要和超时；不宣称分布式exactly-once |
| PST-3 | 默认/重复custom/超长lane ID；raw legacy与qualified行共存 | 默认身份稳定；custom按account限定；owner已知才映射；新qualified优先；不改SQL、不虚构被覆盖owner状态 |
| HND-1 | API-key、OAuth direct Images、OAuth Responses × generation/edit × stream/nonstream | 真实handler/service边界测试，记录实际覆盖组合；不得用路径单测冒充完整笛卡尔积 |
| HND-2 | 切换成功/耗尽/上限；caller取消/总deadline/绝对deadline先到；已有输出/partial image | 不超预算、不在取消后发新请求、不丢既有输出；失败账号排除有效 |
| HND-3 | postqueue内部预算到期与caller取消 | 内部deadline返回既有对外502错误并记录ops504，不返回空200、不发新Provider请求、不惩罚健康、不泄漏资源；caller取消保持既有安静退出 |
| BILL-1 | token/image/per-request计价；余额/订阅；失败可信usage | 原本地结算语义不变、无重复提交；slot/reservation释放；明确mock不能证明Provider账单 |
| REG-1 | 非图片、旧429、compact、token refresh、voices、视频等受影响包 | 原测试实际重跑；修复只改变指定图片健康准入，不回滚前序修复 |

## 执行安排

仓库入口为 `backend/Makefile` 的 `test-unit`、`test-integration`、`test`，其中 `test` 还执行 lint。最终执行者应记录实际工具链、资源约束和命令，不以此计划中的命令说明冒充已执行。

1. 先运行新增分类/状态/选路/校准专项及现有取消、输出、计量回归。
2. 冻结组合代码后执行后端 unit、integration-tag、lint、相关 race 和构建/版本检查。存在Docker、数据库或工具链阻塞时记录具体未运行范围。
3. 持久化至少区分内存快照、repository mock、真实PostgreSQL以及新进程恢复；缺任一证据就如实标记，不用“integration通过”合并描述。
4. 前端若未改，可说明不相关或在字节一致前提下历史复用；不称本轮重跑。代码在验证后再变必须重跑受影响门禁。
5. 独立复核最终差异和原始日志，记录测试是否实际run、pass、fail或skip；生产/付费Provider、浏览器、远端CI各自独立标识。

旧候选1中两个全冷却断言失败反映当时硬拒绝提案与用户既有兜底语义冲突，不能称为已证实的旧产品缺陷；候选2因策略修订中断，不是handler测试失败。两次均不作为本矩阵LR场景的通过证据。
