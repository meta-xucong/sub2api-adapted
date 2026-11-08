# 标准路径的专用 Key 接缝（不是普通收费渠道）

## 目的与边界

`gateway.native_harness.standard_api` 默认 `false`。显式开启后，只有既有
`allowed_user_id` + `allowed_api_key_id` 对应的真实认证 Key 使用以下映射：

| 请求 | 实际路径 |
| --- | --- |
| `POST /v1/chat/completions` | 同一 Native Harness handler、冻结路由、Bridge 账本 |
| `GET /v1/models` | 同一 Native Harness 配置目录（不是现场模型可用性探测） |

客户端可用 `base_url=https://<Sub2API>/v1` 和这个专用 Key。仍须明确传入稳定
`Idempotency-Key`，不能在重试时生成新键。其余 Key 与默认关闭的安装保持原逻辑。
这是专用可信测试 Key 的标准 HTTP 路径，不是账号管理 UI 的普通 provider 类型。
现有 `/v1/native-harness/...` 路径继续有效，两种路径共享操作身份与冻结路由。

按 **Key** 而非实时模型名称决定分流：即使原模型被移除、native 路由/新准入关闭或结果待恢复，
请求也不会落入普通供应商选择、重试、切号或计费。新准入和旧结果恢复仍由现有
native 服务区分。认证仍检查 Key、用户、IP、组；模型白名单在 native handler
新准入处检查，禁止外部身份头伪造归属。

## 最小配置

在已正确部署、默认关准入的三仓配置上显式增加：

```yaml
gateway:
  native_harness:
    standard_api: true
    standard_wait_seconds: 180
    # enabled、accepting_new、allowed_user_id、allowed_api_key_id、routes
    # 仍使用完整部署生成器输出，不能只复制这个片段替换整份配置。
```

`standard_api` 不会自动启用 `enabled` 或 `accepting_new`。开启时为该专用 Key
保留上述两个路径，包括服务关闭的情况；不要把该 Key 同时当作普通模型 Key。
**排空与配置变更：**停止新请求请使用 `accepting_new=false`，保留 `standard_api=true`
和原专用 Key/owner 身份，直到所有已受理/unknown 任务及结果恢复结束。将
`standard_api` 关掉或换掉 allowlisted Key 会撤销标准路径保留，旧请求可能进入普通
路由；不可在恢复期间这样做，不可将此 Key 重用于普通 provider。必须撤销时，
先禁用/撤销该 Key，使旧请求认证失败；不要把关闭路径开关当作排空。

此开关不改变 `/chat/completions`（无 v1）、`/responses`、`/messages`、Codex
专属接口或其他模型路由。它们不是 native 接缝入口，不代表提供相同能力。

## 有界等待同一任务

标准路径在一次请求内最多等待 `standard_wait_seconds`（默认180秒，允许0–300，
0为不追加观察）。首次 Invoke 使用当前新准入权限；仅当返回明确的
`TASK_WAIT_TIMEOUT` 后，才用 **canExecute=false** 恢复同一冻结操作。后续调用
不能创建缺失的任务/路由、不能切模型/Key，也不允许普通provider fallback。
其他错误（未知receipt、审批、未知执行、终态失败等）立即返回，不循环重派。

总deadline和客户端断连都只停止观察，不表示取消了原生任务；收到
`NATIVE_OBSERVATION_TIMEOUT` 仍须按原正文/Key/Idempotency-Key恢复。不把超时
变成安全重试或新幂等键。反向代理、Sub2API服务器及客户端超时须覆盖所配置等待
时间与网络余量；否则仍会提前断连，不能因此重复生成。此段说明非流式等待；实时事件模式见下。

## 实际协议限制

- `workbuddy_local_trial` 原生执行仍只接受单条 user 纯文本、`n=1`。
  标准路径 `stream:true` 现输出真实 **native-events-v1 named SSE**，不再是 buffered-final。
  `text_snapshot` 是权威替换（可缩短/更正），`native_status`/`task_status` 如实反映任务；
  只有 Bridge 确认终态并验证结果后才有完整 `completion` 或 `task_error`。
  客户端必须认识此原生事件协议，不能当作 OpenAI token delta。未知 usage 不造假。
  每个持久事件有 `id: seq`；以相同 Key、正文、Idempotency-Key 及 `Last-Event-ID`
  恢复，后续只读。无 id 的 `observation_error` 仅表示本次观察中断，不能换键重生成。
  `observation_end` 表示已到终态游标；从最终 id 恢复不重复发送 completion。
  `: observing` 只是保活，不伪称原生产出。普通 EOF 不等于完成。
  模式切换仍共享规范摘要与唯一任务；每次连接首次调用之后及带 Last-Event-ID 的恢复均禁止准入。
  丢失游标时也应携带 Last-Event-ID:0；不带此头的重新请求会重新检查准入，
  仍依赖原幂等键与 Bridge 唯一任务约束，不可把它当作只读恢复。
  stream预算须1–300秒，单次写≤5秒且受总deadline限制；不支持有界写的transport拒绝。
  `stream_options` 尚不支持。专用 native-harness 路径仍拒绝 `stream:true`。
- tools、tool_choice、functions、tool-role、图片、Responses 等均未通过此接缝实现。
- 厂商已执行的工具事件不会被伪造成下游待执行 `tool_calls`；审批等待通过状态事件呈现，不代为批准。
- 非流式只返回已验证完成的 HTTP 200；上游待完成/非完成响应或超时会保守返回错误，
  不伪造成完成响应。错误不证明任务没开始：客户端须以原 Key、原正文、
  原 `Idempotency-Key` 恢复同一任务。普通 SDK 不可换键自动重生。
- usage 未知不造零；非收费。超时、断网、节点忙或未知执行结果均不能安全重生成。
- 目录配置不等于模型实时可用，更不等于真实 WorkBuddy 账号、模型调用已验收。

## 完整普通 provider 尚需哪些工作

不能将 Facade 直接注册为普通 OpenAI 账号来绕过上述边界。完整普通 provider
需先把账本操作标识与未知/已受理状态纳入统一调度，保证任何失败/超时不会触发
跨账号再执行；需要经批准的使用量/收费合同、多租户隔离、原生工具权限模型，
以及按真实 Harness 能力实现的流式与多轮/工具调用协议。配置 UI、模型目录、
费用/配额及协议测试亦需同步。仅换 URL、套一层 SSE 或拼接对话不能证明这些语义。

本次测试只覆盖标准路径选择、真实认证边界、普通路由不被误触发及现有 native
ledger 契约；不执行用户登录、真实推理、部署或商业收费。
