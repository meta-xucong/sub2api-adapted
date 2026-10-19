# PR13 独立复审与最小修复计划

审计起点：`ddee35cd7e4110b60fc76c7d91a87528d250675e`。2026-10-19，隔离工作树。本文先于产品代码修改建立。PR 保持 Draft；不合并、不部署、不接生产或收费 Provider。

## 已确认问题与修复顺序

1. **P2，票据逻辑过期与消费不在同一原子边界。** `backend/internal/veyra/ticket_redis.go` 的调用方时间/ExpiresAt 校验先于 Redis `TryConsume`；后者仅校验物理 PTTL。构造较长存储 TTL 或延迟到达消费命令，即可在逻辑过期后领取。最小修复：新增有截止时间及精确 payload 绑定的 Redis 原子消费方法，用 Redis TIME 在同一脚本中判定截止时间和写入单次标记；保留已有 OAuth TryConsume 的契约。补边界、TTL 不一致、并发、错误 intent 和 Redis 故障测试。
2. **P2，兑换时没有重新核对账号状态。** JWT 在签发时验证账号，兑换时只认票据。最小修复：使用已有 SessionUserReader 在兑换时拒绝已删除、停用或无法查询的账号，不改变票据兑换请求/响应字段。票据未绑定原 JWT 版本或 session ID，不能宣称会话撤销立即撤回已签发票据；单独披露。
3. **验收记录过期。** SmartRouter ACCEPTANCE.md 仍待冻结。重新冻结本轮代码候选并执行相关测试，逐项记录命令、退出码、跳过、源码身份，不用旧 image-timeout 方案或旧 CI 代替。保留旧失败记录的历史身份。

## 并行复核与决策边界

- LastResort：仅图片；健康及正常有界等待优先，正常选项耗尽后对允许的 Cooling 使用现有有界 lease。枚举原因；不放宽 ModelUnavailable、原生账号资格、失败账号排除和预算，不新增独立切号开关。发现具体代码缺陷后先追加复现与最小方案。
- SSRF/代理：URL 图片下载在环境代理生效时拒绝、部分下载路径拒绝账号代理，存在条件性兼容影响。当前没有目标部署代理/格式证据，不悄悄绕过代理或降低 DNS/IP 保护。评估安全可实现的最小适配，否则列为部署阻塞，并明确直接连接和 NO_PROXY 的管理员决策边界。
- 内部 token：现有共享 token 是跨用户的可信服务权限，不具备消费端之间的按用户隔离。不能把随机票据或登录签发检查当作该 token 的最小权限证明。不擅自改协议；记录可信服务要求和未闭环的消费端授权门禁。
- 财务：复核余额及成功回执原子性、精度/指纹和重放冲突；成功回执不被通用清理移除。生产/回滚版本、容量留存及 source/reference 对账需求没有运行证据时标为证据不足。
- 跨项目 intent、消费者会话身份派生、部署网络与真实费用不在本轮离线测试中伪造通过。切换重试可能造成上游重复执行/费用，本地扣费幂等不等于上游 exactly-once。

## 本轮最终验收

最终代码冻结后执行相关 unit/race/integration、前端检查、构建及安全检查；独立复审最终 diff 与接口契约。输出五项标准的 PASS/FAIL/INSUFFICIENT_EVIDENCE、源码位置、完整变更清单与精确测试收据。任何未验证生产/消费端条件继续保留，不以“无漏洞/可直接部署”结案。

## 新增确认：异步图片转存的合法 4K 尺寸兼容性（修改前记录）

- **P2：固定 16,777,216 像素上限拒绝受支持的非方形 Gemini 4K 图片。** 起点 `image_storage.go:30,314`；`openai_images.go:511–520` 已接受 Gemini-compatible image 模型，异步接口复用同一 Images 请求及响应，`image_task.go:195–199` 在转存拒绝后将已生成任务标记为 failed/502。Google 官方尺寸表列出 3392×5056、5504×3072 等 4K 输出，分别为 17,149,952 和 16,908,288 像素；3.1 Flash 1:8 的 1536×12288 为已核实最大值 18,874,368。证据来源：[Google 官方 image generation resolution 表](https://ai.google.dev/gemini-api/docs/generate-content/image-generation?hl=en)，2026-10-19 重新读取。原生 Gemini 透传不经过本 uploader，不能把这一缺陷扩大为所有 Gemini 调用故障。
- **最小方案：** 仅把 uploader 像素上限从 `16*1024*1024` 调到有限的 `24*1024*1024`，保留 16,384 单边上限、默认 32 MiB 解码/下载字节限制、图片格式/头部校验和全部 DNS/IP/代理拒绝。继续仅 `DecodeConfig`，不增加全像素解码。补全部官方 4K 比例尺寸及 24 Mi 像素恰好/超限、单边恰好/超限测试；用带重算 CRC 的轻量 PNG IHDR fixture 验证几何策略，不分配巨幅像素。fixture 只证明头部几何接受性，不冒称完整压缩流解码。
- **代理条件性发布门禁保留：** 有效 `HTTP(S)_PROXY` 会使 URL-backed 异步转存拒绝；非空账号代理会使 Images URL→b64 回填跳过以及 KIE signed-video 内容下载失败。没有目标生产环境的有效代理、NO_PROXY、出口路由和真实上游响应证据，当前只能判定 `INSUFFICIENT_EVIDENCE`，不能断言生产已故障或可部署。直连/NO_PROXY 必须由管理员明确选择并验证连通性，不能暗中绕过代理；需要代理的环境须另行实现并验证“本地校验后对数值 IP 建立代理隧道，同时保留原 Host/SNI”的窄路径，或验证代理端同等目的地址约束。本轮不引入该实现或设置，不以域名预检查后让代理再次解析代替 SSRF 保护。
- **冻结后验证：** uploader 全编码路径、PNG/JPEG/GIF/WebP、字节与几何边界、初始 URL/每次重定向/环境代理与 NO_PROXY；共享 HTTP pool 的公有 IP 绑定、混合 DNS 拒绝及代理 fail-closed；Images backfill/KIE content 与未标记文本/流式/provider 路径的区别。无真实 Provider/生产验证。

## 新增确认：图片 sticky 选号未共享探查预算（修改前记录）

- **P2：普通/weighted sticky 绕过现有图片 slot/DB 探查预算。** 起点 `openai_account_scheduler.go:547,579,1365,1400` 直接 recheck/acquire；共享预算直到 `selectByLoadBalance:1481–1486` 才启用。健康 sticky 抢槽失败并触发默认 escape 后，仍可追加完整 64 次 LB 抢槽；weighted fallback 也能在 LB 用尽后追加第 65 次。现有 `smart_router_image_recovery_test.go:396–418` 只直接调用已耗尽预算的 LB，不覆盖公开图片选号入口及 sticky 路径。
- **最小方案：** 在公开图片选号入口建立共享预算时启用已有 64 次限制；普通及 weighted sticky 的 DB recheck/slot acquire 读取同一预算，耗尽时停止追加探查。仅 SmartRouter 已启用且明确 `RequiredImageCapability` 的路径应用；不改变文本/视频、关闭 SmartRouter 时的 sticky 行为、原生资格、健康优先或 LastResort 状态/租约策略。非图片 helper 的 nil 预算表示原有无限制调用。
- **冻结后验证：** 新增公开 `SelectAccountWithSchedulerForImageOperation` 回归：65 个健康候选、全部 slot 拒绝，分别覆盖普通 sticky escape 和 weighted fallback，断言总 slot acquire 不超过 64 且正常 bounded WaitPlan 保留；补共享 DB recheck 耗尽的 sticky 防绕过及非图片对照。运行既有 SmartRouter image/core、sticky scheduler、LastResort、取消/输出/计量专项，所有收据绑定最终组合 SHA，不复用旧独立 flag 测试。此处尚未执行新测试。

## 新增验证：图片持久化与新进程恢复边界（测试修改前记录）

- 仅新增 integration 测试文件，复用现有 PostgreSQL harness、生产 ledger repository 及 service 构造/惰性恢复路径，不修改产品代码或 schema。
- 持久化 generation/edit 的精确模型、账号限定自定义 lane、Cooling/ModelUnavailable/健康对照；由独立 Go 测试进程验证普通准入、LastResort 的 Cooling 限定、同进程有界 lease 与释放不清除冷却，及新观察继续累加既有状态。
- 从数据库加载的快照另验证冷却截止前与截止时的准入边界。证明范围仅为持久化健康状态和进程内恢复准入，不宣称跨进程 lease 或分布式互斥。冻结后统一运行真实 PostgreSQL 专项并保留命令、退出码和 skip 证据。

## 新增确认：完整 race 门禁暴露既有测试夹具竞态（修改前记录）

- 冻结候选 `f0f9758a2312d83794e891320bbf50fcbd9802b7` 的新跑 `race-related/stdout.log` 中有 5 份独立 race 报告，归并为两个测试代码原因。19 个顶层测试失败均记录 race detector failure；6 个测试直接出现在冲突调用栈中，其余 13 个并发测试受 race 归因传播影响，未见额外断言失败。完整原始失败收据保留在 `pr13-revalidation-evidence/20261019T123026Z-f0f9758a2312/race-related/`（工作树外），不覆盖或改写。
- **原因一：** `backend/internal/service/usage_cleanup_service_test.go:68` 的 `dashboardRepoStub.recomputeCalls++` 与 `:583,611` 的异步 `require.Eventually` 裸读竞态。最小修复仅把该测试夹具计数改为 `atomic.Int64`，写入用 `Add(1)`、两处读取用 `Load()`，保留恰好一次的断言和原有超时。
- **原因二：** `backend/internal/service/openai_compat_model_test.go:1316,374,477` 在 `t.Parallel()` 之后调用 `gin.SetMode`，与另一并行用例的 `gin.CreateTestContext`（`:1520`）读 Gin 全局模式，以及彼此对两个全局变量的写入竞态。同文件共 27 处相邻的 `t.Parallel(); gin.SetMode(gin.TestMode)`；最小修复统一把模式写入移到 `t.Parallel()` 前的顺序 setup 阶段。保留全部 `t.Parallel()`、子用例、请求行为和断言；其余 14 个非并行模式设置不改。不引入全局串行化、race suppression 或测试排除。
- 两个文件在修复前均与 `origin/main` 的 `2bedb713b6b0b9cf968468decae36732f390574a` 完全相同（Git blobs 分别 `17f21bef36957d7a9ebf89c9e4042cb60e6cbf08`、`7bd000b4b890d5fd1d5f1ff808d8297b3e9d4bbb`）。属于既有测试数据同步缺陷；不据此豁免本轮失败，也不更改业务实现。
- 修改范围仅上述两个 `_test.go` 文件与本记录。先独立复审 diff，冻结新组合 SHA，再新跑相关专项和完整 race 门禁；所有旧失败仍为失败，不以筛选、跳过或旧成功收据代替新验证。

### 同一 race 选集中的同类静态补漏（修改前记录）

- 为避免仅修本次调度碰巧暴露的写入点，检查同一 service 包后确认三个由原 race 正则选中的 unit 文件也有相同的 setup 顺序问题：`gateway_forward_as_responses_test.go` 7 对、`gateway_forward_as_chat_completions_test.go` 5 对、`account_test_service_gemini_test.go:42–43` 1 对。这是静态同模式确认，不伪称原始日志已报告这三个文件的竞态。
- 授权范围补充为以上三个 `_test.go` 文件中的局部顺序调整，仍保留所有并行调用、断言和用例。Gemini 用例还在 `newTestContext()` 内调用同一个全局模式写入（既有 helper 定义于 `account_test_service_openai_test.go:57`），因此把该用例现有 context setup 一起移到 `t.Parallel()` 之前，不改 helper 或其他文件。不修改原 race 选集外的测试，也不以扩大串行执行掩盖冲突。
- **未在本轮扩展修复的完整套件测试卫生问题：** `openai_fast_service_tier_test.go:784–812` 的 `TestResolvedOpenAIUpstreamServiceTier` 在并行父测试的三个子用例内调用 `gin.SetMode`，静态上仍属相同风险，但该测试名不匹配本轮原 race 正则。按范围约束保留原样；本轮结果不得描述为全原生套件 race-clean，完整 suite 的该项仍待另行处理与验证。

## 新增确认：首输出超时与 keepalive 测试边界重合（修改前记录）

- 冻结候选 `a517cced76fca8260917b349c943b777fc90af64` 的新 race 门禁仅剩 `TestOpenAINativeFirstOutputFailoverKeepsAttemptHeadersPrivateAfterKeepaliveCommit` 在 `openai_first_output_timeout_test.go:720` 失败：2 秒首输出超时按预期触发，但响应体仍为空，尚未提交测试假定存在的 keepalive。原始失败收据保留于工作树外 `pr13-revalidation-evidence/20261019T134803Z-a517cced76fc/race-related/`。
- 原因是测试设置的边界重合：生产 keepalive ticker 在 `openai_gateway_response_handling.go:233` 创建，而 downstream idle 起点在 `:268` 稍后创建；首个 1 秒 tick 可在 `:1040` 因尚不足 1 秒而跳过。首输出 deadline 在 `:244` 绑定更早的请求起点，2 秒超时可以先于第二次 tick 返回。这份日志没有显示产品头部泄漏或错误重试；它显示夹具把尚未成立的“已经提交 keepalive”当作前提。
- **限定最小修复：** 只修改该 `_test.go` 文件及本计划。post-commit 头部隐私用例的测试专用首输出 deadline 调为 4 秒，keepalive 仍为 1 秒；保持上游 pipe 阻塞直至真实产品超时关闭，不替换成 EOF，不改产品超时或时间函数。新增仅观察的 recorder：调用真实 `Flush` 后且实际缓冲区已有稳定 `:\n\n` 字节时发出 channel 信号，不生成字节。断言真实 Flush、504/first_output_timeout、`SafeToFailoverAfterWrite` 为真、实际语义输出为假，保留第一次前导数据不泄漏、第二账号成功及 wire/live 响应头隐私检查。
- 相邻 `TestOpenAINativeFirstOutputTimeoutDisabledKeepsPreamblePrivateAcrossKeepalive` 原本已关闭首输出超时并用 2100ms sleep 后 EOF。仅把该 sleep 改为等待上述真实 Flush 信号后再 EOF；仍验证真实 keepalive 与前导数据私有，不改变其 EOF 失败语义。两例共用现有 10 秒观察/清理边界，明确 watchdog 不制造成功或触发被测超时。
- 既有 1 秒响应头 deadline、未完成语义事件/大前导事件 deadline、超时关闭 reader 与输出后 timer disarm 测试均不修改。严格限于本轮流式输出/切换审计的两个已识别夹具；不扩展原生套件卫生清理。独立复审后冻结新 SHA，再新跑相关完整验证；旧失败不覆盖、不豁免。

## 范围收回决定（用户要求避免过度开发）

2026-10-19 14:05 UTC：用户明确只看指定审计范围。前述五个原生测试文件的计数器/Gin 初始化卫生修复从本 PR 撤回，恢复到 `f0f9758a` 中的原始内容；不回退该文件此前已有的业务测试。撤回通过新增提交保存历史，不改写旧失败证据。它们没有生产行为修改，但不应作为本轮顺带治理项目。

已观察的两类基线测试竞态和19个受影响测试仍记录为 FAIL，不能称全仓 race 通过。最终定向 race 必须按本轮实际修改/自定义路径列明测试清单，不再使用泛化 Usage/Stream/Grok 等词扫入无关原生测试。不得排除目标范围内失败来制造通过。完整原生普通 unit/integration 继续由最终提交 CI 实际运行。

保留的生产修改限四项：票据原子过期、兑换当前账号状态、图片 sticky 共用预算、合法4K尺寸边界。图片 PostgreSQL 恢复及对应队列/身份/输出/计量验证属于用户指定验收项。当前 SSE 测试准备条件修正仅对应本 PR 已修改的 first-output/流式重试行为，不改变生产超时、协议或断言标准。后续不再治理范围外原生测试。

## 最终定向验证范围（不再扩展）

- 核心：SmartRouter core、Veyra、Redis session 的本轮实际代码与兼容测试。
- Service：`smart_router_image_recovery`、`smart_router_scheduler`、`smart_router_calibration_service`、`smart_router_persistence_order`、`smart_router_runtime`；`openai_image_poll_timeout_failover/compatibility`；`image_storage`、`image_storage_settings`、`image_task`；`openai_first_output_timeout` 测试文件中的命名用例。
- Handler：`openai_images_smartrouter_recovery`、`openai_images_poll_timeout_integration`、`openai_gateway_first_output_timeout` 中的命名用例。
- Repository unit：Veyra debit、幂等回执、SmartRouter health repo/order；隔离 PostgreSQL 仅3项既有健康状态持久化/迁移/顺序、6项桥接扣费和1项图片新进程恢复。语音库、上游模型管理等其他专项不纳入此次定向执行。
- SSRF/代理：共享 httpclient、proxyurl、urlvalidator；前端 lint/typecheck/关键用例由最终提交 CI 运行，云端只保留必要的新鲜前端/嵌入式后端构建。
- 从上述文件明确提取测试名并核对终止事件，不以泛词扩张范围。最终回归必须包括15项新测试和2项 SSE 测试；已撤回的19项原生夹具失败作为范围外基线问题记录，不声称已经解决。
- 完整原生 ordinary unit/integration 和安全检查仍运行现有 CI，不改动原生工作流来隐藏失败。定向 race 的通过不代表全仓 race 通过。后续范围外发现只记录，需用户明确批准才扩展实现。
