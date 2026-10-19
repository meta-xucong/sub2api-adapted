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
