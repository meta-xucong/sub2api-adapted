# 图片轮询超时切号整合：上传前验收记录

更新：2026-10-18 17:52 UTC。**最终unit、integration-tag、lint、定向race、embedded build、补充core全包race、二进制--version及本轮选定15项原生PostgreSQL均已在明确范围通过。最终文档交付提交、离线包身份与核验结果以验收包清单为准。** 原始失败、环境跳过和业务边界均保留；文档整理未修改受测运行时代码与测试。

## 1. 候选身份、范围与停止点

- 云端交付基线：`4a137bbf05bb192e488c79ab9954027e05c9bc50`；其此前受测业务代码为 `ef5187a2f252302c80ea461b0df2060a0104b937`。
- 本次吸收的功能来源：`9c638df774c22252ed1b7d25a9dcf9e930fdfe8c`。它是适配整合来源，不据此虚称它已成为当前 Git 祖先。
- 首个整合提交：`86337bda5ea7c88ff9eec65f15de68e7572ced14`，tree `9551a57a91e9ce883aea0b02497ad2d619e5f2cc`。
- 最终受测代码候选：`528820ea5e8f1694fc2a54ef3d3b1045869ff212`，tree `b31518278953eff35cdc154882cc1d57ce7417f0`。
- `528820ea` 对 `86337bda` 只修改两个测试文件：`backend/internal/service/grok_wokey_video_decode_bounds_test.go`、`backend/internal/service/token_refresh_pool_health_test.go`。运行时代码、配置与其他测试相同；测试修正及证据见第4节。
- 最终文档交付 SHA、离线 bundle/证据包身份与导入核验：见最终验收包根目录 DELIVERY_IDENTITY.json 与独立导入收据。不得把后续文档 SHA 写成已重新运行所有测试的代码 SHA。
- 明确停在“云端开发、验证及离线交付供验收，尚未上传 GitHub”。不 push、不创建 PR、不合并、不发布、不部署 VPS、不操作用户本地电脑、不调用真实付费 Provider。本文不批准生产启用或回退。

## 2. 实际整合与保留边界

只读独立复核确认：相对云端基线，六处生产/config 比较均与来源功能增删行或完整新helper字节匹配，未发现必须修正的整合生产缺陷。新选项 `gateway.image_poll_timeout_failover_enabled` 默认 false，独立于原 `failover_on_400`。

启用时，仅真实 HTTP400、有效 JSON 中 `error.code`/`response.error.code`/`code` 的精确大小写敏感、trim 后 `image_poll_timeout` 触发新分支。管理员 passthrough、账号错误码策略、OAuth 主控模型gate先执行。其他早已存在的独立重试逻辑不因该flag开关而消失。该分支返回 nil result 与专用 `UpstreamFailoverError`，保留状态400，不同账号切换，不走same-account retry。

适配了云端带context的图片/文本 Smart Router API，保留取消和绝对总预算保护、真实输出后停止切号、partial-image结算、流式/非流式已观察用量规则、单请求reservation、每次account slot释放和单tracker健康持久化顺序。此次没有改定价、添加跨尝试费用聚合、远端任务取消、幂等协议或schema。

风险仍然存在：

- Provider轮询超时不等于生成停止或没有计费。一次本地结算不证明一次上游任务/费用，重试可能形成重复图片和Provider费用。
- 复用既有预算：加载默认10次切换，至多11个不同账号尝试，受账号耗尽/180秒单次和600秒总预算提前限制；构造器未取得正数配置时兜底3次。不是“仅重试一次”；两个时间配置为0可禁用相应限时。
- 图片健康分类隔离不等于调度隔离。transient cooldown仍按账号ID同时阻断runtime和持久调度状态，共享凭据的文本请求可能受影响。生成30秒、编辑12秒为加载默认基值，还受既有错误率倍率和上限约束。

## 3. 新专项与矩阵证据

### 源码绑定和计数口径

实施专项原命令：

```sh
go test -tags=unit -json -count=1 ./internal/config ./internal/service ./internal/handler -run 'Test(OpenAIImage|SmartRouter|OpenAIGatewayHandlerImages|ImagesPollTimeoutIntegration|ImagesFailedStreamObservedUsageBilling|ImagesNonStreamingObservedUsageBilling|ImagesSchedulerRequestBoundary)'
```

原始资料：`phase3-evidence/image-timeout-integration/scoped-final-receipt.json`、`scoped-final.jsonl`、before/after manifest。专项exit0，838个通过leaf，无失败/skip，其中438个为新handler矩阵leaf。独立复核重新计算得到相同计数，并核对4,490份已列源码与同一manifest，0不符；之后的86337提交仅额外收录三份计划文档。此专项属于提交前但源码已绑定的运行，不是528820最终聚合的替代品。

最终528820 unit.log及定向race.log均已重新执行并通过同一438矩阵。分布为：disabled、success、exhausted、switch_limit、cancelled、total_deadline、deadline_before_timer、already_written、ordinary_400、admin_passthrough、account_policy各36；partial_artifact18；main_model_gate24。438是838专项的子集，同时包含于本轮unit；不得把这些计数相加成独立场景。

矩阵覆盖API-key、OAuth direct Images、OAuth Responses三条上游路径，generation/edit、stream/nonstream及token/image/per-request三种计价。实际handler→service→pricing/usage/billing-command链路使用合成transport/repository；单请求保留一份reservation，失败首尝试尚无结算，成功仅结算一次7输入/3输出而非错误body中的99/33，耗尽零本地结算；检查account/user/global-image slots与reservation释放及无panic被恢复。

### 原 TEST_MATRIX.md 对照（精确证据范围）

下表“已验证”指本轮528820 unit对应命名测试通过，并由源码复核确认含相应断言；不代表每个最初计划的笛卡尔积、真实数据库或Provider都已执行。M表示 `internal/handler:TestImagesPollTimeoutIntegration`，其下列scenario名即原始日志子例末段。完整命名保留在unit JSONL。

| 计划ID | 实际测试与证据 | 结论/限定 |
| --- | --- | --- |
| C01–C02 | config `TestOpenAIImageFailoverSettingsDefaultsAndValidation`、`TestOpenAIImagePollTimeoutFailoverConfigOptIn`；service `TestOpenAIImagePollTimeoutFailoverClassifierIsOptInAndExact`；M disabled/success | 默认关闭、nil/零配置关闭、显式开启、三路径code与trim、独立400策略、状态/typed reason和native切号已验证 |
| C03–C04 | `TestOpenAIImagePollTimeoutFailoverClassifierIsOptInAndExact` 的case/prefix/invalid JSON/object/array/quoted whole response/ordinary400/code-in-message/plain-text；M ordinary_400 | 已验证列明反例；最初计划列的null、数字、空body等不是各有独立新子例，不能称全部输入类型穷举 |
| C05 | classifier HTTP200/其他HTTP状态；M partial_artifact；`TestImagesNonStreamingObservedUsageBilling`、`TestImagesFailedStreamObservedUsageBilling` | 新分支保持真实HTTP400边界，HTTP200语义/已输出计量沿旧路径；未穷举所有200事件组合 |
| C06–C08 | `TestOpenAIImagePollTimeoutFailoverPreservesExistingGates`；M admin_passthrough/account_policy/main_model_gate | 已验证优先顺序。自定义HTTP错误码策略本来仅对API-key生效；OAuth同fixture继续三账号耗尽，不能误写成OAuth策略强制终止 |
| C09 | `TestOpenAIImagePollTimeoutFailoverReasonScopesCooldownAndSmartRouter`、`TestSmartRouterHealthEventsDoNotPersistUpstreamErrorBodies`、handler `TestOpenAIGatewayHandlerImages_ImagePollTimeout400UsesNativeFailover` | 新固定原因、已存在安全健康摘要及ops路径通过；可选上游诊断日志的既有行为未改变 |
| P01–P04 | M disabled/success/exhausted/switch_limit；native failover测试 | 路径和排除集/切换上限已验证；默认10与构造器3来自源码，不声称用真实11账号重试演练。新矩阵edit使用JSON；`TestCodexDirectImagesMultipartEdit`、`TestOpenAIGatewayServiceParseOpenAIImagesRequest_MultipartEdit`等通用multipart回归通过，未做poll-timeout×全部multipart组合 |
| P05–P06 | M success/exhausted及伪造99/33 usage、reservation/slot断言 | 三计价、余额资金路径的一次/零本地结算已验证。`TestOpenAIGatewayServiceRecordUsage_SubscriptionBillingSetsSubscriptionFields`、`TestReserveInflightBalance_SubscriptionPreservesRouteDecision`等通用订阅回归通过，但没有完整poll-timeout×订阅专属矩阵；本地结果不代表Provider账单 |
| P07 | `TestImagesFailedStreamObservedUsageBilling`、`TestImagesNonStreamingObservedUsageBilling` | 正/零/无、cache字段、终止失败/切号与partial-read等既有计量回归重新通过；不把所有失败视作免费 |
| P08 | `TestOpenAIGatewayServiceForwardImages_OAuth429CarriesSameAccountRetryWindow`、`TestOpenAIImagesHandlerSetsRetryAfterOnlyForImageEdits`及M普通错误/gate | 已有429与编辑Retry-After边界重新通过 |
| B01–B03 | M cancelled/total_deadline/deadline_before_timer；`TestImagesSchedulerRequestBoundary`、`TestOpenAIGatewayHandlerImages_TotalTimeoutStopsFurtherFailover`、`TestOpenAIGatewayHandlerImages_ElapsedDeadlineBeforeTimerDeliveryStopsScheduling` | 取消/已到绝对deadline后不继续调度的选定路径通过；不是进入前/所有等待槽时机的穷举 |
| B04 | `TestOpenAIImageRequestAndAttemptTimeoutClassification`、`TestForwardOpenAIImagesAttemptTimeoutFailoverForAPIKeyAndOAuth`、`TestOpenAIImageAttemptTimeoutDoesNotRetryExpiredRequestBeforeTimerDelivery` | attempt/total/caller分类、默认/0禁用、timer滞后断言通过 |
| B05–B06 | M already_written/partial_artifact；上述流式/nonstream计量回归 | 已写真实输出不再切号；已交付图不因随后HTTP200 SSE错误丢弃或重新生成；partial_artifact新子例只覆盖stream=true |
| B07 | `TestOpenAIImagesJSONKeepalive_DoesNotBlockFailoverDetection`、`TestOpenAIImagesJSONKeepalive_HeartbeatBeforeForwardStillFailsOver`、`TestOpenAIImagesJSONKeepalive_KeepsOAuthNonStreamResponseValid`及nonstream metered_keepalive | 已有whitespace心跳与真实输出分离、已提交HTTP状态/JSON边界通过；不是全部poll-timeout×心跳时间点组合 |
| B08 | M每子例释放断言；`TestInflightReservation_HeldUntilBillingTaskDone`、`TestInflightReservation_RenewedWhileHandlerActive`、`TestOpenAIGatewayServiceRecordUsage_BillingUsesDetachedContext`、`TestOpenAIGatewayHandlerSubmitMandatoryUsageRecordTask_DroppedTaskSyncFallback` | 通用异步usage、独立context、资源生命周期重新通过；定向race已通过其明确选择器，不能把余额路径扩称订阅全组合，也不把本行每个通用回归都说成被race选择 |
| H01–H02 | `TestSmartRouterClassifiesImagePollTimeoutAsTimeoutOnlyForImages`、`TestSmartRouterReportsPersistDistinctCapabilityAndModel`、`TestSmartRouterImageRequestBudgetDoesNotPenalize`、`TestSmartRouterTextDisconnectDoesNotPenalize` | 图片timeout/文本不特判、带context取消/截止保护、能力键回归通过；生成/edit共享分类分支另经源码复核 |
| H03–H05 | `TestOpenAIImagePollTimeoutFailoverReasonScopesCooldownAndSmartRouter`、`TestTempUnscheduleOpenAIImageGenerationTransientError`、`TestTempUnscheduleOpenAIImageEditTransientErrorAndErrorRateBackoff`、`TestOpenAIImageTransientCooldownBackoffThresholdsAndCap`、`TestOpenAIImageEditTransientRetryAfterSeconds`、`TestOpenAIImageEditCapacityRetryDelayRequiresAllEligibleCandidatesInCooldown`、`TestRuntimeBlockHonorsClearedPersistedCooldown`、`TestRuntimeBlockKeepsActivePersistedCooldown` | 新reason的生成/edit账号级block、刷新后持久状态对共享文本的影响与既有倍率/上限/0/恢复规则通过；并非新reason×repo失败/所有调度时序全组合。旧/空持久快照既有fail-open未改变 |
| H06–H07 | `TestSmartRouterPersistenceSinkOrdersSnapshotsAndReleasesOnFailure`、core `TestHealthPersistenceKeepsNewerProjectionAndAllEvents`、`TestHealthPersistenceWaitUsesCallerBudget`、`TestHealthPersistenceAttemptFenceSurvivesWriteFailureAndPanic`、`TestHealthPersistenceBlockedSinkDoesNotBlockOtherKeyOrSnapshot`、`TestHealthPersistenceRestorePreservesTokenAndSequence`、`TestHealthPersistenceTrackersAndLegacyEventsStayIndependent`、`TestHealthPersistenceRejectsExpiredAbsoluteDeadlineBeforeErr` | 单tracker顺序/失败/Restore/多key回归unit通过，新原因沿原sink由源码复核；不是专门将新reason与全部乱序时机组合。本轮15项选定原生PG已通过，含健康晚到事件双向子例，见第7节 |
| G01–G04 | 独立源码审查、最终unit/integration-tag/lint | 明确范围全部通过；integration缺Docker导致repository提前退出仍保留，不说成全数据库套件已跑 |
| G05–G06 | 最终定向race、embedded build、go version -m；补充core全包race及--version | 全部已通过，命令/时间/hash见第5节；不是全库race、完整应用启动或部署验证 |
| G07 | 新候选选定15项原生PG回归 | PASS，15顶层/22子项、0失败/skip；最终528820源码绑定与清理完整，只替换数据库启动bootstrap；不是完整Docker/Redis/多版本验收 |
| G08 | 最终文档提交和离线包/独立导入 | 由最终验收包根目录 DELIVERY_IDENTITY.json 和独立导入收据记录，本文不循环嵌入自己的提交号 |

## 4. 首次聚合失败与测试稳定性修正

保留86337原unit失败：2026-10-18 15:55:21–16:09:00 UTC，exit1；12,160顶层/12,206子项通过，2顶层失败及其内嵌1子项失败，18顶层skip。父/子失败不能相加称3个独立缺陷。失败日志SHA256：`eadc3a3fac41047064d6c71e210275fe8a3009e177f30ae1efcfd39eaf73099b`。运行器停止后续integration/lint/race/build，没有把它们记为通过。

1. `TestDecodeWokeyVideoDataURLRejectsOversizeBeforeLargeAllocation/base64` 的process-wide TotalAlloc差值为1,316,664，超出原严格 `<1,048,576`。独立子进程隔离移除无关服务测试遗留分配噪音，仍运行同一测试二进制/真实decoder及原语义断言，原 `<1 MiB` 阈值未放宽；失败、超时、启动失败会传播。child使用GOMAXPROCS2/GOMEMLIMIT2GiB，测量仍是process-wide，不能解释为完整服务内存上限。
   - 实际positive命令 `go test -json -tags=unit -count=10 -timeout=5m -run '^TestDecodeWokeyVideoDataURL' ./internal/service`：exit0，210个通过leaf执行，无fail/skip；oversize测量608–640字节，wrapped2,744–2,776字节。
   - 外部overlay恢复不安全全量分配，原四个leaf均按预期失败，exit1，分别16,786,112 / 16,777,920 / 33,555,104 / 12,591,232字节。它证明真实断言执行且能抓住回归，不是产品测试失败或zero-test成功。
2. `TestTokenRefreshService_SaturatedProviderPreservesConcurrencyAndActualQPSStartSpacing` 原wall-clock记录被主机调度压缩至2.356899ms，未达到当时5ms断言。测试改在 `testing/synctest.Test` 内构造同一真实rate gate、semaphore和8个合成refresh尝试，保持两并发饱和及实际Refresh入口时间观测，阈值加强到配置的完整50ms。
   - positive `go test -C backend -run '^TestTokenRefreshService_SaturatedProviderPreservesConcurrencyAndActualQPSStartSpacing$' -count=100 ./internal/service` exit0。100是实际记录命令的重复次数，普通文本日志不能虚称100条独立JSON终态；运行后只补两行OS/网络限制说明，执行逻辑相同。
   - 关闭rate gate与把rate admission错误前移至semaphore之前的两个外部overlay，各count10、exit1、10/10预期失败，分别抓到0/1及2/3入口间隔0秒。
   - 此证据证明受控虚拟调度下的admission和并发顺序，不保证真实网络发送在任意OS停顿下严格相隔50ms。

独立复核确认两份测试修正没有削弱安全断言，4,493份冻结文件hash与528820全部匹配，production对86337字节未变。测试稳定性正负控不替代新SHA的最终unit/race；本轮最终unit和包含这两个测试的定向race均已重新通过。

## 5. 最终528820门禁状态与源绑定

五个主门禁run.json状态为`passed-with-disclosed-scope`，2026-10-18 16:41:58开始、17:40:57结束。runner顺序执行，未在阶段间改源。统一GOMAXPROCS2、GOFLAGS=-p=1、GOMEMLIMIT3GiB；isolated HOME、GOPROXY/GOSUMDB off，不继承真实Provider凭据/live opt-in/外部DSN。Go软内存目标不是RSS硬限制。

| Gate | UTC开始–结束 | 状态/计数 | 原始日志SHA256 |
| --- | --- | --- | --- |
| unit | 16:41:58–16:52:36 | PASS，exit0；12,162顶层/12,207子项通过；18顶层skip；60包pass、51包no-test-files | `8d5b9650b60e3eff8bc42bcfa2fbf4d8cf9925b8fc3ee986e4d00c548232bfd5` |
| integration-tag | 16:52:39–17:11:33 | PASS，exit0；6,821顶层/6,216子项通过；20顶层+2子项skip；55包pass、57包no-test-files | `34610acc1b7d68a31d372fe6faf3fb80c354b89dc942fe30ef44bd1af9831075` |
| lint | 17:11:36–17:26:20 | PASS，exit0；0 issues；原规则/路径与资源限制保留 | `e92606b0bf483111dff0a120c315ea165821348f31365020e2468a0059095c47` |
| 定向race | 17:26:22–17:38:15 | PASS，exit0；70顶层/649子项通过，0skip；4个包的明确选择器，不是全库race | `543925e1f78cc8ff9dc74e412b221dd1d22690c15183713838fe16eefb09035e` |
| embedded build / build-info | 17:38:15–17:40:56 | PASS，build及go version -m均exit0；Linux amd64、CGO=0、embed、trimpath | build `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`；build-info `9698c62531a002d4c8e45fe93e109cb92fea4151a5f1b6a443c9337047255af4` |
| 补充core全包race | 17:41:08–17:41:12 | PASS，exit0；69顶层/14子项，0skip；仅internal/smartrouter/core全包 | `891497f722e018f4cb4417eb31aa6c85cb7f4e86e7a1f32067321bcbf4cb7e7c` |
| 补充--version | 输出时间17:41:57 UTC；无独立起止收据 | exit0；0.2.12-cloud-integration、完整528820提交、built 16:41:58Z | `06d17aaabd899c86732cd3deaac9c113f913e2573c4cb1af67acbee725a39e64` |

五个主门禁各自source-start/source-end均为528820/b3151827、工作区空，最终runner源收据亦一致；embedded assets起止清单保留在run.json。unit/integration/race和补充core-race原始JSONL均已独立复算，计数一致、0坏JSON、0重复终态。补充core-race单独保存命令、起止时间、退出码、起止HEAD/tree和空worktree-end；未单独保存完整environment/起点worktree状态，不能说它拥有与主五门禁完全相同的所有收据字段。

unit完整命令：

```sh
go test -json -count=1 -timeout=20m -tags=unit -skip '^(TestOfferPluginHostServices_BrokerRoundtrip|TestOfferPluginHostServices_UnimplementedIsGraceful|TestOfferPluginHostServices_NilHostServicesNoop)$' ./...
```

integration实际同一命令仅改 `-tags=integration`。lint实际命令为 `golangci-lint run --timeout=45m --concurrency=1 --max-issues-per-linter=0 --max-same-issues=0 ./...`。race/build完整实际命令保存在各自 `.command`/run.json；race选择config/service/handler/core中的图片failover/timeout、失败计量、HealthPersistence/HealthTracker/SmartRouterPersistence和两个修正后的测试，438新矩阵亦实际进入。补充core全包race实际命令为 `go test -json -race -tags=unit -count=1 ./internal/smartrouter/core`。

实际二进制166,870,050字节，独立重算SHA256 `969892512e9d9d84a2dddf3e631017d371ef1df14fe3cb202743a1c015610afb`，与run.json一致。go version -m记录Go1.27.2、linux/amd64、CGO_ENABLED=0、embed、trimpath、vcs.revision=528820及vcs.modified=false。--version输出和ldflags对应，内嵌built时间是runner开始16:41:58Z，不是17:38:15实际编译开始。--version只核对版本路径，不是完整服务启动/健康验证。

## 6. 跳过、排除和缺环境

最终unit的18个显式skip与上轮集合相同：DingTalk sentinel1、TLS网络opt-in3、prompt-audit/外部Redis4、prompt-audit PostgreSQL6、legacy NULL/sqlite差异1、真实Provider2、插件包1。具体名称和输出原因在unit.summary.json的explicit_test_skips；所有51包级skip均是无测试文件。

三项AF_UNIX命令排除与上轮保持：`TestOfferPluginHostServices_BrokerRoundtrip`、`TestOfferPluginHostServices_UnimplementedIsGraceful`、`TestOfferPluginHostServices_NilHostServicesNoop`。没有run事件，也不包含在18个显式skip内。

integration原始收据已核对：20顶层skip为DingTalk1、Docker限流3、TLS capture缺配置1、外网TLS不可达1、parent-only probe2、prompt-audit Redis3/PG6、真实Provider2、插件1；另2个TLS profile子项因外网不可达跳过。57包级skip均为无测试文件。`internal/repository`输出package pass但因缺Docker在TestMain提前退出，原始日志0条test run；这不等于数据库整包执行通过。单列原生PG结果只覆盖选定测试，也不抵销该事实。

## 7. 历史复用与未证明事项

本次对4a137bbf比较，frontend、backend/internal/repository、backend/internal/smartrouter/core没有文件差异。frontend345文件/2,666 Vitest及lint/typecheck/build保留为历史执行，**本轮未重跑前端**。旧原生PG15顶层/22子项仍只作历史；下述原生PG是最终528820本轮重新运行，不能误标为历史复用。与旧证据分开，本轮core全包race已实际补跑并通过，见第5节。

原计划G05额外core全包race和G07选定原生PG均已有本轮新证据。源码一致不自动产生新执行证明；以下新PG结论来自实际528820收据，也不证明真实Provider费用或生产DB上的完整端到端行为。

### 本轮原生PostgreSQL终态

- 2026-10-18 **17:43:56–17:50:16 UTC**，test exit0。原始JSONL独立复算：**15顶层/22子项通过、0失败、0skip、0坏JSON、0重复终态**；package elapsed 170.748秒，不能与含准备/编译/清理的总运行时间混同。
- 起止commit均为`528820ea5e8f1694fc2a54ef3d3b1045869ff212`，tree均为`b31518278953eff35cdc154882cc1d57ce7417f0`，起止worktree为空；4,493份冻结源码及358份repository文件保持不变，收据`final_commit_binding_verified=true`。
- PostgreSQL **18.1 Debian18.1-1.pgdg13+2**，隔离临时loopback实例；启动前identity_query核对data路径、180001、127.0.0.1、端口55061及服务进程身份后执行。未连接生产数据库。
- 使用`-tags=integration -overlay <本轮postgres-overlay.json> -count=1 -p=1 -timeout=15m -json ./internal/repository/ -run <15项精确名称的锚定表达式>`；完整实际argv在`postgres-validation-receipt.json:test_command`，选定集合与`selected_test_actions`逐项相同。bootstrap-only overlay替代Docker数据库启动，`helpers_unchanged=true`，15项测试及断言保持最终候选原样。不是原始完整Docker/Testcontainers套件、Redis、Alpine或PG14–16兼容验收。
- 6项voice覆盖隔离/分页/成员关系、跨实例不可变归属、并发collision、并发delete/tombstone、owner account删除后墓碑、迁移约束；9项其他回归覆盖上游刷新持久化/回滚、single-use预览与成员变化fence、软删除账号组绑定及health持久化/迁移/旧投影顺序。
- `TestSmartRouterHealthPostgresLateEventPreservesNewerProjection/first_success=false`与`/first_success=true`均实际通过；`TestSmartRouterHealthPostgresPersistenceAndFreshProcessRestore`亦实际通过，不从unit/mock推断数据库结果。
- 清理收据记录56个自有子进程，剩余相同身份进程0，`complete=true`。这是本轮运行器证据，文档整理未另行启动数据库。

原始证据目录：`sub2api-reaudit-native-final-528820e-20261018/`。收据SHA256：`4c47de026fb27cef4f444c58b0e45fe4c71c1f8d304df710906a1a3159731dfc`；`postgres-tests.jsonl`：`1283f771366cc4bebdb23399332c793097fe2568b4393e3985d9aefbc36a4203`；`postgres-validation.log`：`b51ab181c629e4f3431dfbfba361430c524dd5c72de5d9470f3d77586adec4f1`。小型证据归档位于`phase3-evidence/native-postgres-528820e-evidence.tar.gz`，362,442字节，SHA256 `76cb8b20b158e1c1339875bb3a0f8eb351d46ca4bf45578d1926f205828a4e8d`；外部manifest `phase3-evidence/native-postgres-528820e-manifest.json` SHA256 `0f4ed42b810cecdcc61ed4c0489a8452887c159d44a9a550766231f34a39e544`；包内summary SHA256 `915bd565038d4bc8e8e0767d98fa11ccb28b2498b43142fb7ea2c3046a9d07fe`。以上归档/manifest/summary hash均重新计算匹配；归档55条目、42个普通文件（含SHA256SUMS），内部41条checksum全部匹配，无不安全路径或链接。不含数据库data、服务器二进制或编译缓存。

本轮没有执行：真实浏览器/CSP、真实Provider生成编辑与费用对账、在线npm/Go依赖安全扫描、GitHub CI、镜像发布、完整应用启动、生产代理/IP拓扑、真实容量峰值、生产迁移或回退、VPS部署。Docker/Redis/IPC/外网/TLS等已有缺测按最终收据保留；没有宣称所有潜在缺陷已经穷尽。

## 8. 启用、回退与交付前剩余事项

代码验收不打开默认关闭的功能。未来启用仍需确认重复生成/重复Provider费用风险、实际切号/时间预算和共享文本账号容量，并单独授权真实凭据、请求数据、花费上限及上线范围。

回退首先设 `gateway.image_poll_timeout_failover_enabled: false`，按实际配置加载方式验证生效；不假定热加载，也不擅自改变其他重试策略。禁用不能取消已发出的Provider请求、费用或已有cooldown。若需要代码回退，另行授权维护窗口并核验上一云端4a137bbf交付包身份，不回到丢失云端修复的早期远端父提交；不删除usage/账单或健康事件。本轮没有执行这些生产步骤。

交付身份与后续：

1. 五主门禁、补充core-race/version和15项原生PG均已形成通过终态，原始收据保留各自源身份。86337首次失败和测试修正的正反例证据也保留，不计作最终通过次数。
2. 最终文档交付提交只更新本目录，不改变受测528820运行时代码、测试或CI。准确交付SHA/tree、bundle哈希和实际离线导入结果见验收包根目录的DELIVERY_IDENTITY.json及独立导入收据；本文不循环嵌入自己的提交号。
3. 本轮止于云端开发和验证，交用户验收。上传GitHub、正式CI、真实Provider、浏览器和部署仍是后续步骤；本文件不授予这些操作权限。
