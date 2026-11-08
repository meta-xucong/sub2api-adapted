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
  标准路径 `stream:true` 缺省为 **openai-append-v1**：普通 `data:` SSE、
  `chat.completion.chunk` / `choices.delta`，可追加快照仅发送真实新增后缀。
  只有已验证最终结果与已发内容一致时才发送结束帧和 `[DONE]`。
  缩短、改写或最终结果不一致时返回 `NATIVE_STREAM_REWRITE_REQUIRED` 错误并关闭，
  不追加另一份“修正答案”、不伪成功、不重新执行。客户端仍可能留着已显示的临时文本；
  错误后不能把它当最终正确答案，须原键 `stream:false` 获取权威最终结果。
- 要正确展示原生快照更正及任务状态，显式发送
  `X-Native-Stream-Protocol: native-events-v1`，输出命名事件：
  `text_snapshot` 替换全部文本（可缩短/更正），`native_status` / `task_status`
  如实反映任务；只有 Bridge 确认终态并验证结果后才有完整 `completion` 或 `task_error`。
  普通 OpenAI SDK 无法自动展示这些事件，不应在普通 SDK 上选择此模式。
- 标准流的原生状态仅为 SSE 注释，不混入 assistant 文本。普通 SDK 通常忽略注释，
  所以不能声称它能显示审批/unknown等任务状态。未知 usage 不造假；stream 的
  `created` 是本次 HTTP 流创建时间，不是原生推理起始时间。`delta.refusal` 仅在
  没有已发冲突文本且终态拒答已验证时发送，客户端的拒答显示能力各异。
- 标准流恢复必须从头，以原 Key/正文/Idempotency-Key 及 `Last-Event-ID:0`
  只读重放；正游标会在准入前返回 `STANDARD_STREAM_CURSOR_UNSUPPORTED`。
  原生流每个持久事件有 `id: seq`，可用正 `Last-Event-ID` 从已确认事件后恢复。
  不带该头的新HTTP请求会重新检查准入，仍由原操作键与Bridge唯一任务去重，
  不能把它称为只读恢复。每次连接首次调用之后与带此头的恢复均禁止准入。
  模式切换共享同一规范摘要，不产生第二个任务，不能自动换Key/模型/操作键。
- 原生流的 `observation_error` 不带 id，仅表示本次观察中断；`observation_end`
  表示终态游标到达，从最终 id 恢复不重复发送 completion。标准流错误采用
  `data: {"error": ...}` 并关闭，无成功终止帧。两种流中普通 EOF 都不等于成功。
  `: observing` 只是连接保活，不伪称原生产出。
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

## 普通 Python SDK 示例（仍需本渠道幂等头）

```python
import os
from openai import OpenAI

client = OpenAI(base_url=os.environ["SUB2_BASE_URL"],
                api_key=os.environ["SUB2_API_KEY"], max_retries=0, timeout=200)
headers = {"Idempotency-Key": "a-stable-key-for-this-one-operation"}
request = {"model": "workbuddy-your-configured-alias",
           "messages": [{"role": "user", "content": "你的请求"}]}
completed = False
for chunk in client.chat.completions.create(**request, stream=True, extra_headers=headers):
    if chunk.choices:
        part = chunk.choices[0]
        if part.delta.content:
            print(part.delta.content, end="", flush=True)  # 完成前均为临时文本
        if part.finish_reason == "stop":
            completed = True
if not completed:
    raise RuntimeError("流未确认完成，请按原操作身份恢复")
# 改写/观察错误由 SDK 抛出。需要恢复权威最终结果时显式使用同一个 request/headers：
# final = client.chat.completions.create(**request, stream=False, extra_headers=headers)
```

这证明受限文本流可被实际 SDK 消费，不表示无需幂等头、不受限完整 API、
工具调用兼容或普通客户端能够自动撤销已显示的文本。每个新业务请求用新的操作键，
但同一任务的恢复始终保留原键。不得自动把出错请求当作新业务请求重生。
