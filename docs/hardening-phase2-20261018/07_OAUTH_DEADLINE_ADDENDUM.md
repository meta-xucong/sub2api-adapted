# F11：OAuth 刷新截止边界补充设计与验证

## 为什么纳入本轮

完整测试在两个入口发现了同一类真实边界遗漏：取消计时器可能尚未发布 Context.Err，但绝对截止时间已经过去。仅检查 Err 会放行过晚返回的刷新结果。相关旧实现和测试来自官方 [PR #4221](https://github.com/Wei-Shaw/sub2api/pull/4221)；本补丁遵循其“取消和超时避免过期凭据写入”的既有目标，不重做刷新架构。

[Go Context 官方契约](https://pkg.go.dev/context#Context)分别定义 Deadline、Done 和 Err；绝对期限检查用来补充当前检查点的计时器通知窗口，不构成跨数据库提交的原子截止栅栏。

## 两条责任路径

1. OAuthRefreshAPI.RefreshIfNeeded：平台 Refresh 返回后，原来仅检查 ctx.Err，然后进入既有凭据持久化流程。
2. TokenRefreshService.refreshWithRetryWithRateGate：旧版 nil-executor 回退直接调用 refresher，绕过统一 API。原先的 attemptTimedOut 同样只检查 Err；父上下文取消时也可能因 attemptTimedOut 为 false 而继续尝试保存。

旧完整单元失败证明统一 API 的过晚结果到达 CAS 分支；原夹具的 nil/空 map 差异使 mock 拒绝 CAS，不能写成已经证明真实生产写入。新增带旧凭据的反例补充了零持久化调用断言。旧完整集成失败则定位到真实旧回退入口。所有验证使用隔离夹具，没有刷新生产凭据。

## 最小修复

- oauthRefreshAttemptContextErr：先返回已经发布的 ctx.Err；否则在 Deadline 已到时返回 context.DeadlineExceeded。
- 统一 API 在原来的 Refresh 返回后检查点调用该辅助函数。
- tokenRefreshAttemptContextState：父上下文错误优先；仅当父上下文仍有效且独立 attempt 已超时，才产生 attempt timeout 分类。
- 旧回退在修改 _token_version 和调用持久化方法之前检查上下文状态。
- 外层刷新在原有四个父上下文检查点使用绝对期限检查，避免把父任务取消/过期算成账号或 Provider 故障。
- 保留既有锁、CAS、重试次数、错误类型、Provider 终态优先规则，以及“已持久化成功后才在清理中跨过 attempt 期限”的成功处理。

## 明确保留的边界

这些都是进入下一阶段之前的检查点。期限仍可能在检查之后、数据库提交之前到达；数据库上下文和 CAS 继续承担原有保障。不宣称可以撤销已在途上游请求、回滚已完成写入，或提供跨上游和数据库的原子事务。

统一 API 的 lock-held/already-refreshed 等 shortCircuit 返回顺序保持不变，因此不宣称所有返回分支都改成“父上下文绝对优先”。

## 可复现验证

- 精确旧统一 API 源码 overlay + 相同新夹具：Grok/Gemini 均在预期 DeadlineExceeded 却返回 nil 的断言失败。
- 最新统一 API 定向及邻近回归：70 个顶层测试、26 个子测试通过。
- da6e73 完整 integration 标签运行保留了旧回退入口的实际失败日志。
- 旧回退判定规则的轻量重建只作为 helper 级反例；不能冒充原完整源码重放。该重建在三个未发布 Err 的截止场景失败，提取的修复后 helper 七种情形通过。
- 最新外层刷新定向及邻近回归：104 个顶层测试、47 个子测试通过。覆盖 legacy/unified 两条真实入口、正常成功、启动前过期、刷新中取消、父期限过期、独立 attempt 超时、账号处罚/熔断分类和已提交后的清理。
- 原先失败的两个测试均保留，未通过加长时间阈值或删除断言规避。

完整最终结果与跳过范围见 06_IMPLEMENTATION_RECEIPT.md；定向套件与完整套件有重叠，不相加。

## 测试稳定性补充

另两项首次失败暴露了固定等待与进程级分配计数的测试假设。原失败对应的具体调度轨迹和后台分配来源未追踪，不能断言已定位唯一干扰原因。针对这些假设，心跳测试采用 [Go synctest](https://pkg.go.dev/testing/synctest) 保留原计时值和断言；分配次数测试保留 AllocsPerRun(100) 和小于 200 的阈值，在独立测试子进程中运行，隔离其他服务测试的后台分配噪声。禁止心跳以及每个工具增加一次保留分配的两个合成负控，仍会使原断言失败；后者验证的是线性分配敏感性，没有重放旧的完整重写算法。
