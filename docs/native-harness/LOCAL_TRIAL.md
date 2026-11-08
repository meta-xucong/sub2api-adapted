# 单用户 WorkBuddy local-trial route

只扩展现有 native-harness 专用非计费路径，不改变普通渠道路由/重试/计费。

在既有 `gateway.native_harness` 的单 `allowed_user_id` / `allowed_api_key_id` 白名单配置下，route 可显式设置 `execution_mode: workbuddy_local_trial`。该 route 必须使用 `workbuddy-*` model_alias 与非 `fake-` native_model。缺省/`synthetic` 仍只接受 synthetic-/fake-；其他 mode 拒绝。

Route 的 execution_mode 以 JSON 字段随 PostgreSQL operation 冻结，无须新表/迁移。已有 synthetic 行缺字段读回仍为空并按原模式验证。当前 route 改动或删除后，旧操作仍按原 origin/source/ledger/binding/mode查询，且 `X-Native-Admission-Allowed=false`，不重新派发。

新 local-trial 仅单条 user 文本、非流式、n=1。`POST /v1/native-harness/chat/completions` 仍需 Idempotency-Key；不是普通 `/v1/chat/completions`，外部内部身份头不透传。成功响应 `X-Native-Billing=disabled-local-trial`、usage未知省略，仍完全不进入余额预留/结算/账号failover。

真实模型标识需由本机原生回读确认；workbuddy-前缀本身不是身份验证。服务器配置不代表用户已登录或可计费商业转供。

## 隔离数据库验证

`TestSQLStorePostgres` 与 `TestSQLStorePostgresLocalTrial` 仅在显式 `NATIVE_HARNESS_TEST_DSN` 时运行；操作者必须提供已应用 migration 248 的可丢弃 PostgreSQL。各自100并发冻结、重开连接、跨key隔离、竞争者不能覆盖摘要/route。

native-harness-api 的 `integration/run_sub2api_chain.py --disposable-postgres` 可用同一显式DSN验证真实SQLStore和三仓HTTP链。默认不加该flag主动移除DSN。API Key查找仍fixture，执行仍synthetic；不能称完整生产数据层/真实推理验收。
