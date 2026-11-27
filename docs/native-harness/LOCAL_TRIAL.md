# 单用户 WorkBuddy local-trial route

只扩展现有 native-harness 专用非计费路径，不改变普通渠道路由/重试/计费。

在既有 `gateway.native_harness` 的单 `allowed_user_id` / `allowed_api_key_id` 白名单配置下，route 可显式设置 `execution_mode: workbuddy_local_trial`。该 route 必须使用 `workbuddy-*` model_alias 与非 `fake-` native_model。缺省/`synthetic` 仍只接受 synthetic-/fake-；其他 mode 拒绝。

Route 的 execution_mode 以 JSON 字段随 PostgreSQL operation 冻结，无须新表/迁移。已有 synthetic 行缺字段读回仍为空并按原模式验证。当前 route 改动或删除后，旧操作仍按原 origin/source/ledger/binding/mode查询，且 `X-Native-Admission-Allowed=false`，不重新派发。

新 local-trial 仅单条 user 文本、非流式、n=1。`POST /v1/native-harness/chat/completions` 仍需 Idempotency-Key；不是普通 `/v1/chat/completions`，外部内部身份头不透传。成功响应 `X-Native-Billing=disabled-local-trial`、usage未知省略，仍完全不进入余额预留/结算/账号failover。

真实模型标识需由本机原生回读确认；workbuddy-前缀本身不是身份验证。服务器配置不代表用户已登录或可计费商业转供。

## 隔离数据库验证

`TestSQLStorePostgres` 与 `TestSQLStorePostgresLocalTrial` 仅在显式 `NATIVE_HARNESS_TEST_DSN` 时运行；操作者必须提供已应用 migration 248 的可丢弃 PostgreSQL。各自100并发冻结、重开连接、跨key隔离、竞争者不能覆盖摘要/route。

native-harness-api 的 `integration/run_sub2api_chain.py --disposable-postgres` 可用同一显式DSN验证真实SQLStore和三仓HTTP链。默认不加该flag主动移除DSN。API Key查找仍fixture，执行仍synthetic；不能称完整生产数据层/真实推理验收。

## 无推理的 Sub2API 配置预检

`GET /v1/native-harness/models` 使用相同真实 API Key 认证、单用户/单 Key 白名单、IP/用户/Key/group限制。无需 Idempotency-Key，不接受客户端内部身份头。

```json
{"object":"list","data":[{"id":"workbuddy-example","object":"model","owned_by":"native-harness"}],"native_harness":{"accepting_new":true,"live_readiness_checked":false}}
```

只列有效route别名且经过group模型allowlist过滤，不返回origin、service凭据引用、原生model ID、账户信息或profile。`accepting_new` 仅是服务配置及当前Key执行资格的合取；不是Bridge在线或模型/账号真实可用的证明。关闭新准入、Key过期/配额耗尽时仍可认证读取，但列表为空且该值false。停用Key/用户、IP或group限制仍拒绝。

此GET不读写operation store，不调用凭据resolver、不连接Facade/Bridge/原生程序，无推理或计费；成功设置no-store及`X-Native-Billing: disabled-read-only`，不更新Key last-used。正常鉴权缓存/拒绝保护及HTTP访问日志仍可能发生，不能称整个系统零副作用。其他HTTP方法/路径不取得这个免费预检例外。调用者仍应分别检查Bridge能力、配置一致性和本机账号/模型状态。
