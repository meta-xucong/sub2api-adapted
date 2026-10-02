# GPT Responses Compact 契约

## 1. 当前行为与问题

**代码事实（`3130d42`）**：

- `gateway.openai_compact_model` 默认是 `gpt-5.5`。
- 同一个值也出现在 `.env.example`、`config.example.yaml` 与四个 Compose 文件的环境变量 fallback；只改 Go config default 不足以改变部署默认。
- `/v1/responses/compact` 有多个 transport：非 passthrough 的 raw-Chat summary 分支不读 compact override；native/general 与 passthrough compact 分支首发会调用 `resolveOpenAICompactFallbackModel`，只要其得到非空值就改写首次上游请求。
- native compaction v2 在首次请求时通常保留原始 model，但当前 retry helper 会在 `compaction_trigger` 的 `context_length_exceeded` 后使用账号 compact mapping 或 global compact model 重试；已有测试明确期待这一行为。故“v2 完全不受 compact config 影响”不是当前代码事实。
- 匹配的账号 `compact_model_mapping` 优先于全局值。目标设计把 compact-specific mapping/global override 和所有 model-switch retry 限定到明确的 `/v1/responses/compact` endpoint。native v2 不因 context/window、model-not-found、unsupported、message-only availability、空 failed shell 或其他任何失败分类切换模型重试；这是行为收紧/兼容变更，必须替换现有期待 v2 改模型的测试并在发布说明中标注。
- 真实历史日志里的 model rewrite 后出现 502/503，触发前错误证据不完整；这支持移除默认强制改写的风险判断，但不是上游根因的单独证明。

## 2. 目标路由优先级与实际分支

当前不是一个统一 compact upstream path。实施者不得把“compact model mapping”套到每一种路径。

| 请求/账号条件 | 当前实现分支 | 目标模型解析契约 |
|---|---|---|
| `/v1/responses/compact`，第三方 OpenAI API-key，非 passthrough、非 WSv2、账号未显式选择 native compact | 提前进入 `forwardResponsesCompactViaRawChatCompletions`，生成无工具的 summary Chat Completions 请求 | 使用普通 `resolveOpenAIForwardModel` + `normalizeOpenAIModelForUpstream`；保留客户端 canonical model 和正常账号模型 mapping。**不读取** compact-only mapping 或 `gateway.openai_compact_model`。不能因为它是 compact URL 就暗改到 `gpt-5.5`。 |
| `/v1/responses/compact` native/general 非 passthrough | `resolveOpenAIForwardMappedModels` 后调用 `resolveOpenAICompactFallbackModel` | 精确账号 mapping > 显式 global override > empty 时保留常规模型解析结果。 |
| `/v1/responses/compact` passthrough | `forwardOpenAIPassthrough` 在发送前调用同一 compact resolver | 使用 mapping > global > passthrough priority；保留原始 Responses body/headers 约束，必须独立测试。 |
| `/v1/responses` 普通响应 | 普通 Responses 路径 | 不读取 compact-only mapping/global override；不做 compact model retry。 |
| `/v1/responses` + native compaction v2 trigger | 首发使用普通模型解析；当前失败 retry 可被 trigger 激活 | **目标**：model、header、trigger 保持不变；任何失败均原样 fail-closed；不读取 compact-only mapping/global override，不发 fallback signal，不 retry 换模型。 |

对每条分支，日志/trace 保留 `requested_model`、`billing_model`、`upstream_model`、`rewrite_reason` 和 `compact_transport`（`native` / `passthrough` / `raw_chat_summary` / `native_v2`），从上游收到的 body 断言模型 ID。不得把这些不同 compact path 的通过结果合并成一个笼统“compact tested”。

```text
legacy /responses/compact
  ├─ raw-chat summary branch → regular account/model resolver (no compact override)
  ├─ native/general or passthrough branch
       ├─ exact account compact_model_mapping
       ├─ else explicit gateway.openai_compact_model
       └─ else empty config → preserve resolved client canonical model
native /responses compaction v2
  └─ no compact-specific rewrite, no fallback signal, no retry/model switch for any error class
```

- 默认值改为空字符串，且 config example、`.env.example` 和所有 Compose fallback 同步为空。验证最终容器环境与解析后 config；只改模板或 Go 默认不算闭环。
- 全局 override 是运维显式选择，不再默认指向一个特定 GPT 版本。对于只支持特定 compact 模型的 provider，优先设置该账号的 `compact_model_mapping`，限定影响范围。
- `model` 首选 canonical public ID；官方 `gpt-5.6` input alias 可归一到 `gpt-5.6-sol`（列表仍不单列 alias）；`gpt-6` 没有已确认 target 时拒绝，不能靠 compact 路由猜成 Astra。
- 请求/usage 同时保留 `requested_model`、`billing_model`、`upstream_model` 和 `rewrite_reason`。默认无 rewrite；显式 mapping/override 需审计可见。
- Billing compatibility: canonicalize an accepted input alias before allowlist and tariff lookup. Preserve the existing request-based billing model across an explicit compact-only upstream rewrite/retry; do not price by hidden raw alias or silently substitute the compact target's public tariff. Usage must record requested/canonical billing ID separately from actual upstream model and rewrite reason. This protects the current Sub2API charging contract; it is **not** a claim that the user's charge equals the provider's upstream cost. Do not alter Unified Gateway's explicitly configured billing lane in this task.

## 3. 协议隔离

| 输入/路径 | compact 模型改写 | 输出/副作用约束 |
|---|---|---|
| `POST /v1/responses/compact`（legacy endpoint） | 依上节 transport-specific 优先级。默认不因 compact-only setting 改写，但普通 model mapping 仍适用。 | 仍须满足 compact 输出 item 契约；错误不能包装成成功；不能在已向客户端写响应后 retry。 |
| `POST /v1/responses` 普通响应 | 不读取 `openai_compact_model`，不读 legacy `compact_model_mapping`。 | 正常 Responses 请求/事件语义保持不变。 |
| `/v1/responses` + native compaction v2 触发 | 首发保留所选 canonical model；目标不再在 context error 后使用 legacy compact setting 换模型 retry。 | v2 header/触发内容不丢；不得误转 legacy `/compact`；旧行为移除需测试和 release note。 |
| Chat/Anthropic/其他 provider | 不使用 OpenAI GPT compact config。 | 按其自身协议适配，不因共享配置被改写。 |

OpenAI 官方/Sub2API 的 compact endpoint 对较旧 compact 模型的兼容动机仍有价值；本项目接受“显式指定兼容模型”，不接受对所有异构上游默认把 GPT 模型降成同一个 ID。

## 4. Retry 规则（保持最小改动）

- 本任务不新增 retry，不扩大模型错误分类，不增加账号切换次数。
- 只对显式 `/v1/responses/compact` 保留单次、响应尚未写出前的 same-account model fallback。非流式请求必须有真实 transport HTTP status 400/404；流式请求若外层 HTTP status 为 200 且收到 `response.failed`，事件不能伪造/合成 HTTP 400，**不得触发 fallback**（即便事件内含 model error code）；只有上游在 SSE 开始前实际返回 HTTP 400/404，才适用同一分类器。先检查真实 transport status，再接受结构化 code/type `model_not_found`、`model_not_available`、`unsupported_model`、`invalid_model`；message-only 兼容只保留固定短语锚定形式（case-insensitive、trim 后整句匹配，或明确 grammar 中 `{model}` 占位），不能用 `strings.Contains` 任意子串命中。空 failed shell 仅在真实 HTTP 400/404 响应中、error message/code/type 全缺失且上游 message 为空时才符合窄规则。不得用泛化的 `not supported`、`model` 等词匹配。执行前还要求目标不同、同一 account、同一 request、尚未 retry。必须有命中/不命中的正反 fixture，含带前后文的相似句。raw-chat summary 分支不走 compact-specific retry；native-v2 trigger 完全不产生 fallback signal、不做任何模型切换重试。
- **context-window/上下文过长错误一律不触发显式 compact fallback**，即使真实 transport status 是 400/404 或错误字符串包含“model”字样。它是当前模型/输入的容量或请求问题，不是模型 ID 不存在；换模型会改变语义、能力和费用。对 429、5xx、502/503、timeout、网络断开、quota、policy、安全拒绝、无效参数或普通 business error 也不得改模型重试。native v2 对这些错误以及 model availability 错误同样全部不 retry。
- 分类器必须先验证真实 transport status，再检查错误类型；当前 `isOpenAICompactModelFailure` 在 status 检查前对 context-window 错误返回 true，属于应修复的条件顺序/语义缺陷。streaming `response.failed` 的调用点必须保留 transport status provenance，不能把 HTTP 200 SSE body 包装成 synthetic 400 后调用 fallback；且必须由精确 `/v1/responses/compact` 路径守卫，不得因 helper 共用让 native v2 绕过 endpoint 限制。若未来某个明确 provider contract 证明其 context 错误实际上表示 model incompatibility，须另加 source-scoped rule 与正反 fixture，不得恢复通用 shortcut。
- 默认空值且无账号 mapping 时，fallback 无目标，返回原结构化错误；禁止隐式改用某个 static model。
- 如管理员设置显式全局 override，首发已使用 override 时不得把同一模型重复 retry；所有 retry 须在请求 trace 中保留同一 request id、retry reason 和 from/to model。
- 成功的 HTTP 200 仍需校验 compact response 至少有一个合约要求的 compaction item；普通文本、缺失 item、被截断 SSE 不得记为 compact 成功。

## 5. 变更边界

候选代码变化限于：

1. 将 `config.go` 默认改为空；同步 deploy 示例/Compose 的 fallback。
2. 增加 config tests 和 forward-path test，证明默认传入 GPT 5.6 Sol/Terra/Luna 时上游收到相同 ID。
3. 保留已有账号映射和协议 guard；只在审计发现测试缺口时增加窄测试，不重写 compact bridge。
4. 如 source-fidelity audit 发现官方 v0.2.11 改变 compact semantics，按 official contract 复核，但 adapter default-empty 的明确产品决定仍需要独立配置覆盖而非静默删除。

明确不做：改 Codex/CCSwitch、改客户端压缩算法、增加跨账号盲目 retry、自动把所有 GPT compact 改成 `gpt-5.5`、扩大模型 capability false 的硬排除、改变账单倍率。

## 6. 必测断言

- 配置 absent、empty、明确设置三种情况分别验证；部署模板没有残留 `:-gpt-5.5`。
- legacy compact：三个 formal GPT ID 默认原样上游；账号 mapping 优先；无 mapping 时显式全局 override 生效；非 compact endpoint 不改写。
- 第三方 API-key raw-chat summary compact：请求经 `forwardResponsesCompactViaRawChatCompletions`；普通模型 mapping 生效，compact-only mapping/global override 不生效；fake Chat upstream 收到已解析的 canonical/raw target；返回仍符合 Responses compaction item 契约。
- native/general compact 与 raw-chat summary 使用各自 resolver 的断言必须独立，避免仅测一个分支误报全覆盖。
- native v2 header、trigger、model 均保持原样；stream/nonstream 的 context-window、model_not_found、unsupported、窄 message-only availability、空 failed shell、其他业务错误全部 fail-closed 原错，不发 fallback signal、不 retry、不改变模型。更新所有当前期待 native-v2 改到 gpt5.4/其他 compact mapping 的测试。
- 仅显式 `/v1/responses/compact` 的真实 transport HTTP 400/404 明确 model unavailable 可按锚定规则最多 fallback 一次；结构化错误和固定 message grammar 正例可触发，诸如“model output is not supported”、上下文中提到“model not found”的无关句子、普通 invalid request、429/5xx（包括错误体含 context-window/model 字样）、timeout/quota 不触发；HTTP 200 `response.failed` 无论 body code 均不触发；context-window 永不触发；响应已开始后不重试。
- account switch 与 global override 不会双重改写；requested/billing/upstream IDs 可在 usage/ops trace 区分。
- compact stream/nonstream 输出满足同一输出 item contract；非流式不能靠 200 伪通过。

## 7. 官方语义参考

- [Sub2API v0.2.11 config example](https://github.com/Wei-Shaw/sub2api/blob/v0.2.11/deploy/config.example.yaml) 仍默认 gpt-5.5；实现需记录本项目有意覆盖该官方默认。
- [Sub2API issue #3777](https://github.com/Wei-Shaw/sub2api/issues/3777)：compact 请求体信号和 SSE 终态问题背景。
- [Sub2API issue #5586](https://github.com/Wei-Shaw/sub2api/issues/5586)：native compaction v2 header 问题背景。
- [Sub2API issue #1957](https://github.com/Wei-Shaw/sub2api/issues/1957)：GPT-5.5 continuation 问题背景。

这些资料用于定义要回归的边界；issue 不是对本地代码已修复或线上可用的证明。
