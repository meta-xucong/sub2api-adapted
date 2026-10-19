# 上传前云端代码验收记录

独立复核更新：2026-10-18 14:41 UTC。本记录依据独立复核结果整理；生成本文档不改变受测业务代码。下列结果来自重新解析原始日志、计算哈希、读取Git对象及实际二进制构建元数据。本轮原生PostgreSQL已完成；计数、源绑定、清理收据和归档内部hash亦已独立核对。

## 1. 当前结论与停止点

本轮新识别的代码缺陷已形成冻结候选，最终unit、integration-tag、lint、定向race、core全包race、embedded构建/版本检查及选定真实PostgreSQL均通过各自明确范围。保留实际跳过、命令排除与运行环境限制，不能写成全库零缺口或可直接上线。

- 原始基线：`2bedb713b6b0b9cf968468decae36732f390574a`。
- 上一交付提交：`1e54bf4f64b000695cc0efed4eebf47aa61e10f1`，其受测代码为a3f0fc956。本轮执行结果替代旧代码候选结果，旧结果只作历史。
- 本轮冻结受测提交：`ef5187a2f252302c80ea461b0df2060a0104b937`。
- 受测tree：`a8bda37336115cd103cff86412beb993bbca177a`。
- 相对1e54改变15份文件，含本轮修复/测试、CI资源设置及当前停止点计划。相对ff27dd9c的frontend没有任何文件差异。
- **当前明确停在“云端开发及验收材料完成、尚未上传GitHub”。不push、不创建PR、不合并、不操作用户本地电脑、不部署VPS。** 此停止点优先于旧文档中的推送许可；即使以后扫描获得许可，也不自动触发发布，仍须用户新的发布指令。
- 本稿并非GitHub CI通过、生产迁移批准或真实消费授权。最终交付包会按文档提交HEAD另行导入核验；1e54旧包不能覆盖ef5187代码。

## 2. 本轮新增修复及反例证据

### 非流式Images用量与错误响应

- OAuth Responses上游实际仍是SSE；完整已解析的正数用量曾在终止错误的非流式适配中丢失。直接Codex图片完整HTTP200 JSON遇到语义错误也会在用量读取前退出。现在保留已观察到的可信完整响应计量，优先沿用原错误/超时分类，failover仍返回nil，不新增跨尝试聚合或价格。
- 直接图片非流式typed不可重试错误曾产生HTTP200空body。现在使用既有错误响应函数；可重试分支不提前提交响应。若既有whitespace keepalive已经提交200，仍保留200并返回JSON错误，这是原协议边界。
- 75个新增子案例覆盖两条路径、三类计价、正/零/无用量、cache字段、成功/切号、无效JSON、局部读取失败、HTTP错误和keepalive。真实handler/service/pricing链路止于合成usage/扣费命令，不能称真实金额验收。
- 冻结两文件旧源overlay负控：42个子项按预期失败、33个控制通过。修复专项：4个顶层/177个子项通过（含75新非流式+102原流式子项，以及订阅ctx/异步上下文顶层控制），无失败/skip。它是受控两文件反例，不是完整历史树测试；最终整树门禁另见下一节。
- 独立复算旧源日志SHA256：`b70068fa42ad8239b883ff00a1f05f2d14fbdbd7d484094c97be7c8a4f054440`；修复专项：`d8201b5ca16ab5a8d3939f03ea1a294b2ed9fd1cc57c0383cc557da9cdbc23c3`。三个归属文件当前hash与专项保存值和ef5187完全相同。

### 公共内置voice大小写兼容

- 公共目录对ASCII大小写进行匹配，修复Eve/EVE、Ara等合法公共别名被误拒。custom voice仍按精确ID归属和账号固定；精确已知拒绝先于目录，canonical ID已有任意custom映射时不按公共别名放行，缓存不缓存权限，歧义目录/SQL错误拒绝。
- 用1e54原版单文件overlay运行`TestGrokBuiltinVoiceCaseInsensitiveContract`：2个子项（目录大小2和257）及父测试失败；没有把父/子失败相加称3个独立反例。原overlay字节与1e54 Git对象一致。日志SHA256：`1777a50b1fdd5e1a3bcbb2226eff2fb7ca71690bf4a55296cafa682de8a05fb1`。
- 修复后的冷/热/超缓存目录、TTS/Realtime请求、跨用户/墓碑/组拒绝、缓存后新映射、非ASCII边界与custom精确ID测试已进入最终unit及定向race。没有调用真实voice Provider。

### 单tracker健康快照持久化顺序

- 原fork的同key观测在内存中有序，但解锁后的sink可逆序写库，旧失败覆盖新恢复快照。标准库隔离探针20/20重现同秒逆序；该探针的PASS表示成功复现缺陷，不是旧实现正确，也不是真PG执行。
- 现于既有healthState附内部token/序号；生产sink的原3秒context同时覆盖每key gate等待与DB调用，不跨I/O持tracker总锁，不新增永久第二map、schema、队列或goroutine。gate接受即推进“已尝试”高水位，避免失败/commit不确定后迟到旧快照反写。
- 旧事件仍尝试进入日志，仅对独立成功/失败时间戳作GREATEST更新，不更新health/cooldown/priority、last_failure_unix或updated_at，不新建缺失状态行。正常UPSERT也保持两个证据时间戳单调。Restore保留token，私有控制字段无法由JSON注入。
- 新core、真实service sink和repository SQLmock断言已进入最终自动门禁；新增双向真实PG测试本轮也已实际通过，两个子例均有原始JSONL终态。
- 明确保留：只保证同一tracker/key的投影尝试顺序。多个tracker/进程仍独立维护内存，未建立分布式一致累计；数据库错误、超时与commit不确定仍是best-effort。三秒是协作式预算，不是任意driver的硬耗时上限；不保证每条事件必达、exactly-once或原子内存/数据库同步。

### CI可执行资源设置

CI lint固定golangci-lint v2.15.0；设置GOMEMLIMIT=3GiB、GOMAXPROCS=2、GOFLAGS=-p=1、concurrency1和45分钟预算，保留全部规则/路径。当前云端相同资源参数lint通过；Go软内存目标不是进程RSS硬限制，也不能直接宣称GitHub runner已通过。

## 3. 最终执行结果与源绑定

五个主门禁unit/integration/lint/race/build各自保存的source-start和source-end均精确等于ef5187及上述tree，所有worktree-end为空。执行脚本在每组开始前检查HEAD和tracked/index无修改；独立复核时当前工作树也干净。没有补写不存在的历史证据。

| Gate | UTC开始–结束 | 结果 | 限定 |
| --- | --- | --- | --- |
| unit | 13:32:25–13:47:45 | exit0；12155顶层/11750子项通过；18顶层skip | 另3项AF_UNIX命令排除；60包pass/51包无测试文件 |
| integration标签 | 13:47:45–14:06:31 | exit0；6819顶层/6216子项通过；20顶层+2子项skip | 另3项命令排除；55包pass/57包无测试文件；repository整包未执行 |
| lint | 14:06:31–14:17:24 | exit0；0 issues | 保留已解释的9处逐行staticcheck兼容抑制 |
| 定向race | 14:17:24–14:28:30 | exit0；39顶层/235子项通过；无skip | service/handler/core指定选择器，非全库race |
| Linux amd64 embedded build | 14:28:31–14:32:17 | exit0 | embed、trimpath、CGO=0；构建非启动 |
| core全包race | 14:33:22–14:33:28 | exit0；69顶层/14子项通过；无skip | 仅internal/smartrouter/core全包，不是全库race |
| version | 日志14:33:21 | exit0 | 显示0.2.12-cloud-review、完整ef5187提交与构建时间 |

计数由原JSONL独立重建，全部已有summary逐字段相同，无坏JSON行和重复终态记录。不同套件互相重叠，顶层与子项也嵌套，不能相加称独立场景。

core-race有原始命令/起止时间/日志/退出码，但未单独保存source-start/end/environment；其冻结上下文与当前clean状态可核对，不能声称该组有与主五门禁完全相同的双端源收据。version绑定实际构建二进制，未另存独立起止时间。

主门禁资源记录：unit/integration/race/build为GOMEMLIMIT=4GiB，lint为3GiB；均GOMAXPROCS=2、GOFLAGS=-p=1。runner启动时unset真实Provider凭据/opt-in变量。完整命令和环境在`phase3-evidence/ef5187a2f/`。

### 日志SHA256

- unit.log：`09dee2c45690dfed7c9fbe07c4be5f965d38fb0d0b782513577d84e1b94c5562`
- integration.log：`600161c2410be6d3a4bed365ce194993ba7c0f9ee509acf66619e84437a3469a`
- lint.log：`e92606b0bf483111dff0a120c315ea165821348f31365020e2468a0059095c47`
- race.log：`6278bb67ebc77060494f1d4c90459093d2aa434c68dd6c0f54eb72c97e3a6b20`
- build.log：`e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`
- core-race.log：`485c1ca27d3351852cb0dbaa37a745ce745edfd4735756b241b67f8f859dc397`
- version.log：`9eaf093be9c187365a15f8f301c034571294bbc97dd0a5b5e50eb53bc18af43e`

## 4. 跳过、排除与缺失环境

显式skip集合与a3完全相同，未用新增skip规避本轮修复。unit51、integration57包级skip逐个都是`[no test files]`；不能混成环境测试通过或失败。

integration的`internal/repository`尽管输出package pass，日志明确因缺Docker在TestMain提前退出，独立计数为0条test run。所有数据库断言必须依赖单列的真实PG收据，不能把55个package pass解释为所有包全部执行。

命令另排除以下三项，unit/integration均未产生run事件，且不包含于显式skip计数：
- TestOfferPluginHostServices_BrokerRoundtrip
- TestOfferPluginHostServices_UnimplementedIsGraceful
- TestOfferPluginHostServices_NilHostServicesNoop

unit18项的原因类别：DingTalk sentinel1，TLS网络opt-in3，Redis依赖4，prompt-audit PostgreSQL DSN6，历史NULL/sqlite差异1，真实Provider2，插件包1。integration20顶层的原因类别：DingTalk1、Docker限流3、TLS capture配置1、外网TLS不可达1、parent-only子进程probe2、prompt-audit Redis3/PG6、真实Provider2、插件包1；另两个TLS profile子项因外网不可达跳过。原生PG选定验证不抵销这些prompt-audit/Redis/插件/IPC/外网缺测。

### unit显式跳过清单

- `internal/handler:TestDingTalkOAuthStart_Disabled`
- `internal/pkg/tlsfingerprint:TestDialerBasicConnection`
- `internal/pkg/tlsfingerprint:TestJA3Fingerprint`
- `internal/pkg/tlsfingerprint:TestAllProfiles`
- `internal/securityaudit:TestPromptAuditConfigCASSecretRoundTripInvalidationAndTTL`
- `internal/securityaudit:TestRedisPayloadStoreRoundTripTTLNamespaceAndDelete`
- `internal/securityaudit:TestPromptRuntimeAggregatesConfigWorkersQueueRedisEndpointsAndGuardMetrics`
- `internal/securityaudit:TestPromptAuditMigrationSchemaAndLeakageGate`
- `internal/securityaudit:TestPromptAuditDatabasePersistsFullPromptOnEventsOnly`
- `internal/securityaudit:TestPromptAuditRepositoryAdmissionClaimFencingAndEventTransaction`
- `internal/securityaudit:TestPromptAuditRepositoryForeignKeysFiltersAndStableIdentitySnapshots`
- `internal/securityaudit:TestPromptAuditRepositoryHighWaterAndSafeDeletion`
- `internal/securityaudit:TestPromptAuditServiceConfirmationKeepsPostPreviewEventsAndConcurrentDeletesAreSafe`
- `internal/service:TestAuthPendingIdentityService_UpsertAdoptionDecision_ClearsLegacyNullSessionReference`
- `internal/service:TestContentModerationTypeSafeLive`
- `internal/service:TestEstimateOpenAIInputTokens_CompareWithOpenAIAPI`
- `internal/service:TestPluginRuntimeIntegration`
- `internal/service:TestResponsesCompatSessionExternalRedisSeparateProcesses`

### integration显式跳过清单

- `internal/handler:TestDingTalkOAuthStart_Disabled`
- `internal/middleware:TestRateLimiterSetsTTLAndDoesNotRefresh`
- `internal/middleware:TestRateLimiterFixesMissingTTL`
- `internal/pkg/tlsfingerprint:TestDialerAgainstCaptureServer`
- `internal/pkg/tlsfingerprint:TestJA3Fingerprint`
- `internal/pkg/tlsfingerprint:TestAllProfiles/default_node_v24`
- `internal/pkg/tlsfingerprint:TestAllProfiles/linux_x64_node_v22171`
- `internal/repository/upstreamrefreshprobe:TestRefreshPersistenceChildProcess`
- `internal/securityaudit:TestPromptAuditConfigCASSecretRoundTripInvalidationAndTTL`
- `internal/securityaudit:TestRedisPayloadStoreRoundTripTTLNamespaceAndDelete`
- `internal/securityaudit:TestPromptRuntimeAggregatesConfigWorkersQueueRedisEndpointsAndGuardMetrics`
- `internal/securityaudit:TestPromptAuditMigrationSchemaAndLeakageGate`
- `internal/securityaudit:TestPromptAuditDatabasePersistsFullPromptOnEventsOnly`
- `internal/securityaudit:TestPromptAuditRepositoryAdmissionClaimFencingAndEventTransaction`
- `internal/securityaudit:TestPromptAuditRepositoryForeignKeysFiltersAndStableIdentitySnapshots`
- `internal/securityaudit:TestPromptAuditRepositoryHighWaterAndSafeDeletion`
- `internal/securityaudit:TestPromptAuditServiceConfirmationKeepsPostPreviewEventsAndConcurrentDeletesAreSafe`
- `internal/server/routes:TestAuthRegisterRateLimitThresholdHitReturns429`
- `internal/service:TestContentModerationTypeSafeLive`
- `internal/service:TestEstimateOpenAIInputTokens_CompareWithOpenAIAPI`
- `internal/service:TestPluginRuntimeIntegration`
- `internal/smartrouter/core:TestSmartRouterHealthRestoreThroughPostgresLedgerInChildProcess`

原始逐项输出保存在独立机器核验JSON的skip_reason_outputs，故保留精确原因而未从名称推测。

## 5. 构建与版本身份

实际云端二进制SHA256：`f815c5b08e1eb1d1519bee987ac020054037387ac6c2c8b6f2301d757aa5d4bf`，独立重算与binary.sha256一致。

`--version`日志：0.2.12-cloud-review；commit ef5187a2f252302c80ea461b0df2060a0104b937；built 2026-10-18T14:28:30Z。与build.command注入的ldflags一致。独立只读`go version -m`重读实际二进制确认Go1.27.2、linux/amd64、CGO_ENABLED=0、embed、trimpath、vcs.revision=ef5187、vcs.modified=false。与原build-info除首行绝对/相对显示路径外全部相同。

这是云端验证构建，非发行镜像、完整应用启动或部署健康检查。最终文档提交若另有SHA，应区分“交付提交”与本受测/构建代码提交，不伪称重新构建。

## 6. 原生PostgreSQL本轮终态

状态：**PASS，仅限明确选定的15项真实PostgreSQL测试**，exit0。它是ef5187本轮实际重新执行，不是沿用447旧证据。

- 实际commit：ef5187a2f252302c80ea461b0df2060a0104b937；起止tree均a8bda37336115cd103cff86412beb993bbca177a；起止工作区空。
- UTC开始–结束：2026-10-18 14:33:42–14:40:02；测试package elapsed 165.747秒，完整运行含准备/编译/清理，不把这两个耗时混同。
- 独立重新解析原始JSONL：**15顶层/22子项通过，0失败、0跳过，无重复终态**；选定名称与raw receipt逐项相同。其中voice6/11、既有PG回归8/9、新health乱序1/2。
- 真实数据库：PostgreSQL18.1 Debian18.1-1.pgdg13+2，临时loopback实例；启动时确认data目录、版本、127.0.0.1、端口50751、用户与mmap身份后才建库/迁移。
- 冻结4482文件、repository358文件在运行前后保持一致。独立检查归档中的完整4482份SHA256清单：文件集合与ef5187 Git tree相同，每份hash与相应Git对象字节相同，0不符。
- 新增`TestSmartRouterHealthPostgresLateEventPreservesNewerProjection`双向子例真实通过，覆盖两条事件历史、最新health快照、独立成功/失败时间戳、旧事件不改last_failure_unix/updated_at及后续回拨时钟不降证据时间戳。
- 原有跨进程恢复/持久化断言仍执行。清理收据记录62个自有进程，剩余相同身份进程0，complete=true；此为本轮运行器记录，独立复核没有另行启动数据库。
- 使用bootstrap-only overlay替换Docker启动；选定测试和断言保持当前候选原样。不是完整原始Docker/Testcontainers套件、Alpine、Redis或PG14–16兼容验收，不能抵销原integration中的repository TestMain缺Docker未执行事实。

证据身份：
- 归档：`phase3-evidence/native-postgres-ef5187a-evidence.tar.gz`，361819字节；SHA256 `acbc697f091dcdf67a254b9c3a114410c53d11f1b5ff2fbc14a1876346cd8416`。
- summary SHA256：`26e582e0a0c91a84ed61ecb1745ea50c7a10c0d792b3bd8a3a74614b9e80e8fb`。
- 原始执行收据SHA256：`f17758bf13c9878da59708cd048c49c9691b73e5266ec568d3144db5ad10df43`。
- tests.jsonl SHA256：`6b70539b1b4f387e6329505097b7674fac741d07c813edb1718edc7d642e766e`。
- 归档42个普通文件（含SHA256SUMS），55个tar条目含目录；41条内部SHA256SUMS全部核对一致，无不安全路径/链接。归档raw receipt与本轮运行目录原件完全一致，不含数据库data目录、服务器二进制、下载包或构建缓存。

## 7. 前端历史复用及验收界限

独立Git比较ff27dd9c至ef5187的frontend为空，包含源码、package与锁文件；没有本轮前端代码变化。复用阶段二已有收据：345文件/2666 Vitest通过，lint/typecheck/production build均exit0。它们是历史执行，未在本轮重跑，不虚构开始结束时间。

真实浏览器媒体播放/CSP允许与拒绝路径仍未验。jsdom、middleware断言、语法检查和production build不能代替浏览器执行。在线npm/Go漏洞数据仍无本轮有效扫描结果，待明确授权；源码字节一致不会使浏览器或漏洞扫描自动变PASS。

## 8. 并行9c638分支明确排除

独立只读对照记录于`parallel-image-branch-scope.md`：远端`codex/image-poll-timeout-failover-20261018`的`9c638df774c22252ed1b7d25a9dcf9e930fdfe8c`属于另一条开发线，不在ef5187本轮验收内；本轮未fetch/cherry-pick/merge它。当前冻结源码也不存在其`image_poll_timeout_failover_enabled`配置或`openai_image_poll_timeout`特殊原因。

其功能是默认关闭的HTTP400 exact image_poll_timeout重试开关，不能说已并入或通过本轮测试。两线共享部分文件，未来整合需另立候选并测试错误路由/失败计量/请求预算/health交互。远端新增Smart Router测试缺少本线API现要求的context参数，直接拷贝会不编译；同文件修改本身不等于已证实文本merge冲突。

14:12UTC只读核对到的远端CI失败属于9c638、旧golangci-lint v2.13.2无法读取Go1.27.2 export-data版本，不属于ef5187的执行结果。该旧观察也不能当作远端CI最新终态。当前云端lint v2.15.0已实际通过，GitHub上本候选未运行。另有14:39:28UTC的只读远端main收据，仍指向2bedb713b6b0b9cf968468decae36732f390574a；这不是发布本候选的证明。

## 9. 当前仍未证明的内容

- 在线npm/Go安全扫描、GitHub候选分支/PR/CI、镜像发布。
- 无排除的原始全integration、Docker启动、隔离Redis、三个AF_UNIX项目、prompt-audit缺失资源、插件与外网TLS。
- 完整应用启动/健康、真实浏览器CSP、PG14–16/Alpine兼容。
- 真实Provider生成/消费与账单对账、生产代理/IP拓扑、容量与峰值RSS、生产迁移/回滚、旧voice归属对账或VPS部署。
- 多tracker分布式一致持久化与全局容量政策；本次局部修复不涵盖这些产品设计。

旧voice未知ID不自动认领；provider成功/SQL失败仍可能孤儿并需人工对账。账号ID仍须代表同一provider主体/origin。Realtime不迁移既有socket；跨账号voice拒绝仍可能影响多账号组可用性。回退旧应用可能重新打开共享voice通路，保留表/墓碑并单独评估接口隔离。

## 10. 交付前剩余收尾

1. 将当前停止点、新结果、旧历史边界与并行分支排除合并到正式文档，冻结文档提交；不改已受测运行时代码。
2. 针对最终交付HEAD重新制作离线bundle/证据包，验证所需base、解包路径、导入HEAD/tree、文档链接、hash与附件身份；不沿用旧1e54包身份。
3. 交给用户验收并停在上传GitHub之前。后续本地/真实环境按独立worktree、空PG/Redis、合成配置进行，绝不自动连接生产、重启VPS或发起付费Provider请求。

推荐结论：“本轮已识别代码缺陷已补齐，冻结候选明确范围自动门禁和15项真实PG验证通过；跳过和真实环境边界完整保留。最终离线交付核验完成后，交付云端候选供验收，尚未上传GitHub。”
