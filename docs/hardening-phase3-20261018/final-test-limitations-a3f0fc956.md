# 最终测试限制：a3f0fc956

候选：a3f0fc956e9a9a43660cd44bd3b3992e61cde144。只读审计时间：2026-10-18 11:23 UTC。来源：a3f0fc956/unit.summary.json、unit.log、unit.command、unit.exit，lint.log/exit。未改源、未运行测试或编译。

## 已确认结果与计数口径

- lint exit0，日志0 issues；仍含明示的窄SA1019兼容例外，不能解释成所有弃用均移除。
- unit exit0：12,138顶层测试通过、11,650子项通过、18个显式测试跳过；失败0。顶层和子项是嵌套统计，勿称23,788个互不重叠独立场景。
- 60个package pass；51个package skip逐个核对均为 `[no test files]`，不是51组环境阻塞测试。
- 命令另用 `-skip` 排除3项AF_UNIX host-service测试，它们未执行，不包含在18个显式skip中。
- 命令为 `go test -json -tags=unit -count=1 -timeout=20m -skip '^(TestOfferPluginHostServices_BrokerRoundtrip|TestOfferPluginHostServices_UnimplementedIsGraceful|TestOfferPluginHostServices_NilHostServicesNoop)$' ./...`。因此可以说该明确范围unit门禁通过，不能说无排除的原始全测试零跳过。

## 18个显式测试跳过：原始日志原因

- `internal/handler:TestDingTalkOAuthStart_Disabled`
  - auth_dingtalk_oauth_test.go:21: helper newTestAuthHandlerWithDingTalk added in Task 1.10; sentinel only
- `internal/pkg/tlsfingerprint:TestDialerBasicConnection`
  - dialer_test.go:160: 跳过网络测试（需要设置 TLSFINGERPRINT_NETWORK_TESTS=1）
- `internal/pkg/tlsfingerprint:TestJA3Fingerprint`
  - dialer_test.go:160: 跳过网络测试（需要设置 TLSFINGERPRINT_NETWORK_TESTS=1）
- `internal/pkg/tlsfingerprint:TestAllProfiles`
  - dialer_test.go:160: 跳过网络测试（需要设置 TLSFINGERPRINT_NETWORK_TESTS=1）
- `internal/securityaudit:TestPromptAuditConfigCASSecretRoundTripInvalidationAndTTL`
  - prompt_config_integration_test.go:141: PROMPT_AUDIT_TEST_REDIS_ADDR is not set
- `internal/securityaudit:TestRedisPayloadStoreRoundTripTTLNamespaceAndDelete`
  - prompt_payload_store_integration_test.go:17: PROMPT_AUDIT_TEST_REDIS_ADDR is not set
- `internal/securityaudit:TestPromptRuntimeAggregatesConfigWorkersQueueRedisEndpointsAndGuardMetrics`
  - prompt_payload_store_integration_test.go:43: PROMPT_AUDIT_TEST_REDIS_ADDR is not set
- `internal/securityaudit:TestPromptAuditMigrationSchemaAndLeakageGate`
  - prompt_repository_integration_test.go:105: PROMPT_AUDIT_TEST_POSTGRES_DSN is not set
- `internal/securityaudit:TestPromptAuditDatabasePersistsFullPromptOnEventsOnly`
  - prompt_repository_integration_test.go:155: PROMPT_AUDIT_TEST_POSTGRES_DSN is not set
- `internal/securityaudit:TestPromptAuditRepositoryAdmissionClaimFencingAndEventTransaction`
  - prompt_repository_integration_test.go:202: PROMPT_AUDIT_TEST_POSTGRES_DSN is not set
- `internal/securityaudit:TestPromptAuditRepositoryForeignKeysFiltersAndStableIdentitySnapshots`
  - prompt_repository_integration_test.go:299: PROMPT_AUDIT_TEST_POSTGRES_DSN is not set
- `internal/securityaudit:TestPromptAuditRepositoryHighWaterAndSafeDeletion`
  - prompt_repository_integration_test.go:347: PROMPT_AUDIT_TEST_POSTGRES_DSN is not set
- `internal/securityaudit:TestPromptAuditServiceConfirmationKeepsPostPreviewEventsAndConcurrentDeletesAreSafe`
  - prompt_repository_integration_test.go:399: PROMPT_AUDIT_TEST_POSTGRES_DSN is not set
- `internal/service:TestAuthPendingIdentityService_UpsertAdoptionDecision_ClearsLegacyNullSessionReference`
  - auth_pending_identity_service_test.go:365: legacy NULL pending_auth_session_id rows only exist in production PostgreSQL history; sqlite unit schema rejects NULL
- `internal/service:TestContentModerationTypeSafeLive`
  - content_moderation_typesafe_live_test.go:17: requires explicit TYPESAFE_LIVE_TEST=1 and a new TYPESAFE_API_KEY
- `internal/service:TestEstimateOpenAIInputTokens_CompareWithOpenAIAPI`
  - openai_gateway_count_tokens_test.go:314: OPENAI_API_KEY not set
- `internal/service:TestPluginRuntimeIntegration`
  - plugin_runtime_integration_test.go:29: 未提供 SUB2API_TEST_PLUGIN_PACKAGE，跳过本地插件进程集成测试
- `internal/service:TestResponsesCompatSessionExternalRedisSeparateProcesses`
  - responses_compat_session_test.go:576: requires an explicitly supplied isolated loopback Redis

分类：DingTalk占位sentinel 1；TLS外网opt-in 3；Redis依赖4（prompt audit3+跨进程Responses1）；prompt audit PostgreSQL DSN6；历史NULL/sqlite不兼容1；真实Provider凭据/opt-in2；插件包未提供1。网络/真实Provider测试需要相应授权和隔离凭据，不应为追求零skip自动打开。

## 三项命令排除

- TestOfferPluginHostServices_BrokerRoundtrip
- TestOfferPluginHostServices_UnimplementedIsGraceful
- TestOfferPluginHostServices_NilHostServicesNoop

这三项按既有AF_UNIX环境限制明确排除；本次unit日志不能证明它们通过，也不能把命令排除当作代码内显式skip。交接应继续列为本地受支持IPC环境验收事项。

## 与选定native PostgreSQL证据的关系

先前447f188的14顶层/20子项native PostgreSQL证据是独立、bootstrap-overlay、有限范围验证，不能抵销本次prompt audit DSN缺失的6项，也不能替代未执行Redis/插件/IPC/live-provider测试。若最终候选源指纹一致而复用，必须记录精确源绑定，不能将历史执行改写成a3f0fc956重跑。

## Integration状态

已在integration.exit产生后（11:31 UTC）读取完整日志，exit0。6,804顶层/6,195子项通过；20顶层/2子项显式跳过；55 package pass、57 package skip。57个package skip均经核对为[no test files]。

unit.log SHA256：4fe667a4e7cccaa8ad17c75a208c0bd8452c2cf8e56a4786e67bb78517f3085e。

## Integration关键包级限制

`internal/repository`虽输出package pass，但其TestMain明确记录“docker is not available; skipping integration tests (start Docker to enable)”并提前退出，本轮该包没有实际运行测试。55个package pass不能解释成55个包所有测试已执行。这一包级提前退出不进入20+2显式测试skip计数。独立native PG有限范围证据须单列，不能掩盖该原始Docker bootstrap未验证事实。

Integration继续使用同一3项AF_UNIX -skip排除，命令见integration.command。下列显式skip逐项来自终局JSONL；没有重试受限外网。

- `internal/handler:TestDingTalkOAuthStart_Disabled`
  - auth_dingtalk_oauth_test.go:21: helper newTestAuthHandlerWithDingTalk added in Task 1.10; sentinel only
- `internal/middleware:TestRateLimiterSetsTTLAndDoesNotRefresh`
  - rate_limiter_integration_test.go:28: Docker 未启用，跳过依赖 testcontainers 的集成测试
- `internal/middleware:TestRateLimiterFixesMissingTTL`
  - rate_limiter_integration_test.go:60: Docker 未启用，跳过依赖 testcontainers 的集成测试
- `internal/pkg/tlsfingerprint:TestDialerAgainstCaptureServer`
  - dialer_capture_test.go:48: 跳过外部 TLS 指纹 capture 测试：未设置 TLSFINGERPRINT_CAPTURE_URL
- `internal/pkg/tlsfingerprint:TestJA3Fingerprint`
  - dialer_integration_test.go:76: skipping test: external service unavailable: Get "https://tls.peet.ws/api/all": dial tcp: lookup tls.peet.ws on 168.63.129.16:53: dial udp 168.63.129.16:53: connect: network is unreachable
- `internal/pkg/tlsfingerprint:TestAllProfiles/default_node_v24`
  - dialer_integration_test.go:145: skipping test: external service unavailable: Get "https://tls.peet.ws/api/all": dial tcp: lookup tls.peet.ws on 168.63.129.16:53: dial udp 168.63.129.16:53: connect: network is unreachable
- `internal/pkg/tlsfingerprint:TestAllProfiles/linux_x64_node_v22171`
  - dialer_integration_test.go:145: skipping test: external service unavailable: Get "https://tls.peet.ws/api/all": dial tcp: lookup tls.peet.ws on 168.63.129.16:53: dial udp 168.63.129.16:53: connect: network is unreachable
- `internal/repository/upstreamrefreshprobe:TestRefreshPersistenceChildProcess`
  - refresh_persistence_child_integration_test.go:27: this probe is run by the parent repository integration test
- `internal/securityaudit:TestPromptAuditConfigCASSecretRoundTripInvalidationAndTTL`
  - prompt_config_integration_test.go:141: PROMPT_AUDIT_TEST_REDIS_ADDR is not set
- `internal/securityaudit:TestRedisPayloadStoreRoundTripTTLNamespaceAndDelete`
  - prompt_payload_store_integration_test.go:17: PROMPT_AUDIT_TEST_REDIS_ADDR is not set
- `internal/securityaudit:TestPromptRuntimeAggregatesConfigWorkersQueueRedisEndpointsAndGuardMetrics`
  - prompt_payload_store_integration_test.go:43: PROMPT_AUDIT_TEST_REDIS_ADDR is not set
- `internal/securityaudit:TestPromptAuditMigrationSchemaAndLeakageGate`
  - prompt_repository_integration_test.go:105: PROMPT_AUDIT_TEST_POSTGRES_DSN is not set
- `internal/securityaudit:TestPromptAuditDatabasePersistsFullPromptOnEventsOnly`
  - prompt_repository_integration_test.go:155: PROMPT_AUDIT_TEST_POSTGRES_DSN is not set
- `internal/securityaudit:TestPromptAuditRepositoryAdmissionClaimFencingAndEventTransaction`
  - prompt_repository_integration_test.go:202: PROMPT_AUDIT_TEST_POSTGRES_DSN is not set
- `internal/securityaudit:TestPromptAuditRepositoryForeignKeysFiltersAndStableIdentitySnapshots`
  - prompt_repository_integration_test.go:299: PROMPT_AUDIT_TEST_POSTGRES_DSN is not set
- `internal/securityaudit:TestPromptAuditRepositoryHighWaterAndSafeDeletion`
  - prompt_repository_integration_test.go:347: PROMPT_AUDIT_TEST_POSTGRES_DSN is not set
- `internal/securityaudit:TestPromptAuditServiceConfirmationKeepsPostPreviewEventsAndConcurrentDeletesAreSafe`
  - prompt_repository_integration_test.go:399: PROMPT_AUDIT_TEST_POSTGRES_DSN is not set
- `internal/server/routes:TestAuthRegisterRateLimitThresholdHitReturns429`
  - auth_rate_limit_integration_test.go:25: Docker 未启用，跳过认证限流集成测试
- `internal/service:TestContentModerationTypeSafeLive`
  - content_moderation_typesafe_live_test.go:17: requires explicit TYPESAFE_LIVE_TEST=1 and a new TYPESAFE_API_KEY
- `internal/service:TestEstimateOpenAIInputTokens_CompareWithOpenAIAPI`
  - openai_gateway_count_tokens_test.go:314: OPENAI_API_KEY not set
- `internal/service:TestPluginRuntimeIntegration`
  - plugin_runtime_integration_test.go:29: 未提供 SUB2API_TEST_PLUGIN_PACKAGE，跳过本地插件进程集成测试
- `internal/smartrouter/core:TestSmartRouterHealthRestoreThroughPostgresLedgerInChildProcess`
  - smart_router_postgres_restore_integration_test.go:23: this probe is run by the parent repository integration test

Integration分类（顶层20）：DingTalk sentinel1、Docker依赖限流3、TLS capture配置缺失1、外部TLS网络不可达1、需父测试启动的child probe2、prompt audit Redis3/PG6、真实Provider2、插件包1。另2子项为TLS profiles外网不可达。TestAllProfiles顶层通过不代表其中两个外网子项已执行通过。

外网TLS测试在integration标签下会尝试tls.peet.ws；本次因DNS/网络不可达主动skip。这是未验证，不是TLS兼容通过，也不是本审计追加的外网请求。真实Provider/Redis/PG/插件测试需对应隔离资源、配置和授权后验收。

推荐交接表述：“明确排除3项AF_UNIX后的unit和integration-tag命令均exit0；unit18项、integration20顶层+2子项显式skip，integration repository TestMain因缺Docker未执行该包测试。选定native PostgreSQL测试另有独立证据；未把环境缺测记成通过。”

integration.log SHA256：1d0f64d9380f1d67223ee9b9715aa7d97c84db313773138a4c78e6995654094b。
