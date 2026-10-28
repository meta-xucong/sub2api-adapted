# Codex 原生压缩到 Anthropic 原生压缩桥接设计

状态：`LOCAL_IMPLEMENTATION_COMPLETE; SOURCE_AUDIT_PASS; ONLINE_E2E_PENDING`

日期：2026-10-28
目标仓库：`D:\AI\SSH\sub2api`
设计时分支：`codex/cloud-integration-pr12`
设计时 HEAD：`3073f8dd318f9b6562e81736ce13c0f31650ef81`

本文冻结最小实现方向和当前本地实现记录，不授权调用线上上游、提交、推送或部署。

## 1. 目标

让 Codex 经 Sub2API 的 Responses→Anthropic 兼容路径调用 Claude Sonnet 5.5 时，Codex 发起的原生 remote compaction v2 请求能够调用 Anthropic 原生按需压缩，并能在后续 Claude 请求中续接。

普通 Claude 对话保持现有处理路径。

## 2. 最小实现结论

复用现有 Responses→Anthropic 转换链，增加一个只处理原生压缩往返的小适配器：

1. 识别现有 Codex remote compaction v2 请求信号。
2. 将该次请求转换为 Anthropic Messages 原生压缩请求。
3. 将 Anthropic 返回的 compaction block 映射成 Codex 要求的单个 Responses `compaction` 输出项。
4. 将后续请求带回的不透明状态还原成原 Anthropic compaction block。

不另选 GPT 摘要，不编写自定义摘要 prompt，不改普通 Responses→Anthropic 转换语义。

## 3. 协议映射

### 3.1 请求识别

复用当前代码已有的 native remote compaction v2 识别条件，不新增模型名、上下文长度或提示词猜测。当前识别入口位于 `backend/internal/handler/openai_gateway_handler.go` 的 `isOpenAIRemoteCompactionV2Request`，调用位置在 `OpenAIGateway` Responses handler。

压缩请求只在进入 Anthropic 平台的 Responses 兼容路径时由适配器接管；其他请求原样沿用原有路径。

### 3.2 Codex 请求转 Anthropic 请求

- 将 `compaction_trigger` 作为 Codex 控制项消费，不转换成普通 user/assistant 消息。
- 其余 Responses 对话历史沿用当前 `ResponsesToAnthropicRequest` 转换。
- Anthropic 请求增加 `compaction: {"type":"summarize"}`。
- 该次压缩请求携带 `anthropic-beta: compact-2026-09-04`。
- 请求仍使用当前选中的 Claude 上游账号及模型；不改路由和账号调度。

### 3.3 Anthropic 结果转 Codex 结果

- 从 Anthropic 压缩响应中保留完整 compaction block，包括 `type`、`content` 和 `signature`。
- 将完整 block 序列化为 JSON，再放入带固定版本前缀的 Base64URL 字符串，作为 Responses 输出项的 `encrypted_content`。
- 向 Codex 返回恰好一个 `type: "compaction"` 输出项，并正常结束 Responses 流。

固定封装前缀建议：`anthropic-compaction-v1:`。该字段是本适配器的**不透明状态封装**；Base64URL 只用于无损编码，不代表加密。

### 3.4 后续请求续接

- 当后续 Responses 历史包含上述前缀的 compaction 项时，解码其中的 Anthropic block。
- 将 block 作为独立 `assistant` 消息的首个 compaction block；丢弃该状态项之前已被压缩的历史，只转换其后的对话项。
- 该请求携带同一个 `anthropic-beta: compact-2026-09-04` header。
- 其余对话继续由现有转换器处理。

不得改写或重建 block 内的 `content`、`signature`，也不得把封装后的 `encrypted_content` 原样发送给 Anthropic。

## 4. 现有代码边界

- 请求兼容转换：`backend/internal/pkg/apicompat/responses_to_anthropic_request.go`
- Anthropic/Responses 类型：`backend/internal/pkg/apicompat/types.go`
- Anthropic Responses 转发及流处理：`backend/internal/service/gateway_forward_as_responses.go`
- Responses 兼容入口：`backend/internal/handler/gateway_handler_responses.go` 与现有 OpenAI Responses handler 的平台分发处

当前代码已有对带可读 `summary` 的 `compaction` / `compaction_summary` 输入进行兼容转换的逻辑；它会转成普通摘要消息。对只有 opaque state 的 compaction item，当前测试要求报错。该既有 portable-summary 行为不属于本设计范围，不替换、不扩展。本功能只处理带 `anthropic-compaction-v1:` 前缀的桥接状态。

具体接线点以实现时重新核对的调用链为准；不得因此改动 Composite 路由、账号选择、计费或普通 Claude 请求。

## 5. 明确不做

- 不做 GPT/其他模型摘要桥或自动模型回退。
- 不做 Claude 能力探测、模型可用性校验、Beta 预探测或配置开关。
- 不新增重试、fallback、账号粘性、持久化状态或后台任务。
- 不改变普通 Responses→Anthropic 输入/输出转换。
- 不改账号调度、模型路由、计费、前端、数据库或部署流程。
- 不将不支持 Anthropic compaction beta 的上游自动判成账号故障；按现有上游错误路径返回错误。

## 6. 最小验收

只验证该协议闭环：

1. 带有 native compaction v2 信号的 Codex 请求转换为 Anthropic 原生压缩请求，`compaction_trigger` 不进入普通消息历史。
2. Anthropic compaction block 被封装为一个 Responses `compaction` 输出项。
3. Codex 回传该项后，网关恢复出的 Anthropic block 与原 block 完全一致，并位于 Anthropic 历史首位。
4. 不带压缩信号的普通 Claude Responses 请求保持现有转换结果。

线上 Sonnet 5.5 压缩往返属于另行授权的真实调用验收；本设计本身不发起线上请求。

## 7. 官方协议依据与限制

- Codex remote compaction v2 在响应流中要求恰好一个 `compaction` 输出项；Codex 客户端将该项用于构建后续压缩历史：<https://github.com/openai/codex/blob/main/codex-rs/core/src/compact_remote_v2.rs>
- OpenAI Responses 文档说明 compaction item 的 `encrypted_content` 是不透明状态，并要求在后续请求中携带：<https://developers.openai.com/api/docs/guides/compaction>
- Anthropic 按需压缩要求使用 `compaction: {"type":"summarize"}` 与 `compact-2026-09-04` beta；返回 block 带 `content` 和 `signature`，后续请求必须原样携带。官方兼容列表包括 Claude Sonnet 5.5：<https://platform.claude.com/docs/en/build-with-claude/compaction-on-demand>

官方文档未定义 Anthropic 签名 block 与 OpenAI `encrypted_content` 的跨协议封装格式。因此本设计采用网关私有、版本化、可往返的状态封装；Codex 客户端实际 round-trip 是实现验收条件。404token 等中转上游是否透传 Anthropic beta 参数也尚未由公开资料证明。

## 8. 本地实现记录

### 8.1 实现边界

- 压缩触发只由流式 Responses 输入中的 `compaction_trigger` 识别；触发项不进入 Anthropic 消息。
- 带本适配器版本前缀的 `compaction.encrypted_content` 被还原为 assistant 消息中的 Anthropic compaction block；其他 opaque 状态仍按原有行为处理。
- 仅压缩请求和携带本适配器状态的续接请求确保包含精确的 `compact-2026-09-04` beta token。
- OAuth 伪装会为 thinking 请求补充 `context_management`；仅在首次压缩请求中移除此字段，因为 Anthropic 不允许它与 `compaction` 参数同发。携带签名 block 的后续普通请求仍保留原有 OAuth context editing。
- 压缩响应流保留上游 `content_block_start.content_block` 原始 JSON，正常终态返回一个 Responses `compaction` 项；没有完整 compaction 终态时返回 Responses failure，不返回空成功。
- 桥接通过显式 opt-in 转换入口，仅接入 Anthropic Responses 转发路径；共享转换器的其他调用方（包括 Antigravity）保持原行为。
- Codex OpenAI Gateway 的原生 Anthropic 分支是实际 `/v1/responses` 调度入口之一，已接入同一压缩请求转换及流解析器。
- 普通 Claude Responses 请求继续使用原处理路径；没有修改模型路由、账号调度、计费、前端或数据库。

### 8.2 本地源码与测试

实现位于本文件记录的目标仓库及分支/HEAD。变更文件为：

- `backend/internal/pkg/apicompat/types.go`
- `backend/internal/pkg/apicompat/responses_anthropic_compaction.go`
- `backend/internal/pkg/apicompat/responses_to_anthropic_request.go`
- `backend/internal/pkg/apicompat/responses_to_anthropic_compaction_test.go`
- `backend/internal/pkg/apicompat/responses_stream_event_wire.go`
- `backend/internal/service/gateway_forward_as_responses.go`
- `backend/internal/service/gateway_forward_as_responses_compaction.go`
- `backend/internal/service/gateway_forward_as_responses_compaction_test.go`
- `backend/internal/service/openai_gateway_responses_anthropic_native.go`
- `backend/internal/service/openai_gateway_responses_anthropic_compaction_test.go`

定向压缩请求/状态往返测试、OpenAI Gateway 原生 Anthropic 转发测试、完整 `internal/pkg/apicompat` 测试、完整 `internal/service` 包测试、`go vet ./internal/pkg/apicompat ./internal/service` 均通过。完整 `go test ./... -count=1` 仅在无关环境测试失败：`internal/repository` 的 3 个 pg_dump 测试要求 Windows 环境没有的 `sh`，`internal/veyra` 的 portal 嵌入文件测试因源码 CRLF 与嵌入 LF 字节不同而失败。其余包通过；这些失败不在本次变更路径内。

Anthropic 文档把压缩调用计费 token 放在 `usage.iterations`，顶层输入/输出 token 可为零。2026-10-28 用户授权追加最小计费映射：仅专用 compaction 处理路径将 `type=compaction` 的 iteration 输入/输出 token 映射到既有内部用量字段，不改变客户端响应和普通请求计费。映射的单元测试及受影响服务测试需在本轮重新通过后才算本地验收；线上真实计费仍未验证。

当前只完成本地源码实现和独立审计。没有向 Anthropic/404token 发真实请求，故中转上游是否透传 compaction 参数、beta header 和签名块仍未做线上验证；没有提交、推送或部署。
