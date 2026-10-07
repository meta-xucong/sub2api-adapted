# Claude Code 原生 Anthropic 路径：已验收基线

状态：**本地测试路径通过（限定本记录列明的容器、账号和模型）**
验证日期：2026-10-06

## 冻结结论

Claude Code 使用 Anthropic Messages 原生路径时，当前 Sub2API 本地容器能够完成普通消息、流式工具往返、Claude Code `/compact` 和压缩后的上下文续接。本轮没有修改代码；没有理由仅因 OpenAI Responses/compact 的失败而改造这条 Anthropic 路径。

后续将此记录作为该路径的回归基线。除非相同路径出现可复现缺陷，或用户提出明确的新需求，不增加跨协议转换、Claude 专属补丁或额外“兼容”规则。遇到失败时，先核对运行版本、Base URL、有效 key、分组/账号、模型以及入站/上游路径；不同路径或认证配置不匹配，不能直接算作本基线回归。

## 固定的实测环境

- 源码提交：`f9c19a3b2e88ba7ae01bd32799cc1d1438e63851`。
- 测试容器：`sub2api-v0213-404token-app-tested-f9c19a3`，镜像 `sub2api:local-f9c19a3`，测试时健康运行；镜像 digest `sha256:9b14edf6918a3f1e8d00d5ee6f2d71284aa67e2bbb7a74cb2205dcf2d117f94d`。
- Claude Code：`2.1.282`，隔离配置目录和临时工作目录；未修改用户全局 Claude 配置或已有会话。
- 模型：`claude-sonnet-4-6`。
- 本地测试组/上游账号：`TMP-YETOKEN-CLAUDE-20261006` / `kiro-claude-特惠-yetoken`。使用已存在的专用测试 key；本文不记录 key 值。
- 当前工作树有若干 OpenAI/Responses 相关未提交改动，但本轮原生 Anthropic handler 文件干净；这些未提交文件未包含在该镜像中，也不是本轮通过依据。

## 实测项目与结果

1. 专用 key 的本地认证和模型列表检查成功，组内可用模型包含 `claude-sonnet-4-6`。
2. Claude Code 普通消息返回预期文本。
3. Claude Code 发起只读终端工具调用，命令成功执行；工具往返后会话继续工作。
4. Claude Code `/compact` 显示压缩完成。
5. 压缩后追问仍正确回忆终端工具输出 `TOOL_NATIVE_ANTHROPIC_OK` 和模型 ID `claude-sonnet-4-6`。
6. Sub2API 用量明细将这些测试请求记录为入站 `/v1/messages`、上游 `/v1/messages`，流式调用使用同一模型 ID；命中上述组和上游账号。没有走 `/v1/chat/completions` 或 `/v1/responses` 转换路径。
7. 测试 key 的累计本地计量从约 `$0.0046` 增至 `$0.0629`（增量约 `$0.0583`）。测试组、测试 key 均保留；未部署、未重启容器。

## 必须区分的两种“压缩”

- 本次通过的是 **Claude Code 客户端的 `/compact` 工作流**。实际请求通过 Anthropic Messages `/v1/messages` 完成；这是用户在 Claude Code 中使用 `/compact` 的路径。
- 本次**没有**测试 OpenAI `POST /v1/responses/compact`，也没有测试 Anthropic 服务端 `context_management`/原生 compaction API。它们是不同协议/功能，失败不能反推上述 Claude Code 工作流失败。

本次确认了客户端报告压缩成功及压缩后上下文续接；没有采集压缩前后的精确 context-window 百分比，因此不对具体释放 token 数量作量化结论。本次也不代表所有 Claude 模型、账号、分组或 VPS 部署都已逐一验收。

## 重测触发与归因规则

若将来 Claude Code 原生路径出错，应先在不改全局配置的隔离会话中，用有效 key 对当前待验镜像做最小复现，并保留请求时间、模型、分组、状态码及入站/上游端点。只有确认请求确实走 `/v1/messages` 后，才检查 Anthropic adapter；如果问题来自死端口、无效 key、不同账号/模型、上游拒绝或 Responses compact，应分别按其所属层排查，不把它记为本路径的代码缺陷。

本轮排障还观察到：当时已有的全局 Claude 配置指向 `http://127.0.0.1:15721`，该端口没有监听；其配置凭据对本轮本地服务 `18081` 返回 401。成功验证使用的是隔离配置和专用测试 key。这证明当时存在配置/认证路径不一致，但不足以单独解释此前每一次失败；后续归因仍需依据失败请求实际使用的 URL、key 和服务日志。

此处的“冻结”表示**已通过行为基线和变更归因边界**，不是禁止修复未来真实回归，也不是宣称全模型/全环境永久无故障。
