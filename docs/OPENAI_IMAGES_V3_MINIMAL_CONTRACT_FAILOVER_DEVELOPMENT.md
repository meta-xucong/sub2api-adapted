# V3 OpenAI Images 最小化契约与故障转移补丁

## 1. 目标

在不重写 Alchemy V3、不改变 Smart Router 全局调度、不新增数据库字段的前提下，修复图片链路的两个边界问题：

1. 保证发往上游的 `gpt-image-2` 请求符合目标线路声明的 OpenAI Images 契约；
2. 恢复已验证的图片专用 `upstream_text_reply` 跨账号切换，避免单个账号把本次生图任务直接终止。

本补丁只作用于 `/v1/images/generations`、`/v1/images/edits` 的 OpenAI 图片转发路径。

## 2. 已确认的根因边界

- 历史修改曾把图片专用错误判断并入通用 upstream 错误处理，导致 `HTTP 400 + error.code=upstream_text_reply` 被当作普通客户端 400，不再切换账号。
- 这会把“上游返回文本而非图片”的线路错误暴露给 V3，表现为无像素、重试同一账号或最终 400/502。
- 不能把普通提示词中出现的 `template`、`reference`、`edit` 等词误判为上游结构化错误。
- 不能通过全局删除字段来“修复”所有兼容线路；不同 provider 对 OpenAI 兼容字段的容忍度不同。

## 3. 最小修改方案

### 3.1 请求契约边界

在现有图片转发器进入 provider 请求构造前增加一个**按 transport profile 的轻量清洗层**：

- `strict_openai_images`（仅明确声明遵循官方 GPT Image 契约的线路）：
  - `generations` 保留 `model`、`prompt`、`n`、`size`、`quality`、`background`、`moderation` 等支持字段；
  - 不主动发送 GPT Image 不需要的 `response_format`；
  - `edits` 使用 multipart 重复字段 `image[]`，并保留 `prompt` 与受支持的图像参数；
  - 参考图仅从已验证的本地/relay 文件生成 multipart 部件，不把内部 `file_id` 原样跨账号转发。
- `openai_compatible`（未声明 strict 的现有线路）：
  - 保持当前兼容行为，不全局删除 `response_format` 或其他字段；
  - 继续沿用现有 generations JSON、edits multipart/`image_url` 适配。

这一步只做字段边界，不改变模型选择、账号排序、重试次数、计费或响应格式。严格清洗只作用于 API Key 直连 Images endpoint；OAuth 现有 Responses image tool 保持原有桥接行为，避免跨协议改动。

### 3.2 图片专用 failover

保留并固定以下判定顺序：

1. 先检查结构化 JSON 的 `error.code`、`response.error.code`、顶层 `code` 是否**精确等于** `upstream_text_reply`；
2. 仅当 HTTP 状态为 400 时，将其视为图片线路不可用并允许切换到下一个账号；
3. 非 JSON 响应仅在整个 body 明确等于/包含该专用标记时兼容识别；普通上游文本、提示词回显和 HTML 不得触发；
4. 同一账号不得重复尝试；API Key 与 OAuth 两条图片路径使用同一规则；
5. `image_poll_timeout`、404、普通 400、鉴权失败及其他确定性客户端错误保持原策略，不扩大切换范围。

### 3.3 不在本补丁处理的内容

- 不把所有 4xx/5xx 都改成换线；
- 不改变 Smart Router 的长期权重、冷却、永久禁用逻辑；
- 不把 `generations` 自动改成 `edits`，也不把无图请求强行补图；
- 不把 HTTP 200 但 `data=[]` 的语义扩展成全局失败（除非另有独立证据和测试）；
- 不修改 Alchemy、V3 job 状态机、数据库 schema、管理端或前端。

## 4. 预计改动文件

优先限制在现有实现：

- `backend/internal/service/openai_images.go`
- `backend/internal/service/openai_images_responses.go`
- `backend/internal/service/openai_gateway_upstream_errors.go`
- `backend/internal/service/openai_responses_image_bridge.go`
- `backend/internal/service/openai_image_generation_transient_cooldown.go`（仅复用已有冷却判定，除非测试证明无需修改）

不得触碰计费、账号表、通用 Smart Router 评分和非图片 provider。

## 5. 测试设计

### 单元/契约测试

覆盖以下最小矩阵：

| 场景 | 预期 |
|---|---|
| strict generations + `gpt-image-2` | 不发送 `response_format`，其余字段保持 |
| compat generations | 保持现有兼容字段 |
| strict edits + 多张图 | 发送重复 `image[]` multipart，顺序稳定 |
| `images[].image_url` | 可解析并转成受控 multipart |
| `images[].file_id` 跨账号 | fail-closed，不盲转发 |
| HTTP 400 + `error.code=upstream_text_reply` | 切换账号 |
| HTTP 400 + 普通文本提示词回显 | 不切换 |
| HTTP 200 + 普通文本 | 不因本补丁扩大为全局 failover |
| 同一账号 API Key/OAuth | 不重复重试 |

### 回归测试

```text
go test -count=1 -tags unit ./internal/service
go test -count=1 -tags unit ./internal/handler
go build ./...
gofmt -w <modified-go-files>
git diff --check
```

真实验证只允许使用一次低成本、可审计的图片请求；必须记录请求路径、实际账号、HTTP 状态、是否获得像素，不记录 API Key。

## 6. 发布与回滚

1. 本地先完成契约/故障转移测试和构建；
2. 只提交本补丁涉及的代码与本开发文档；
3. 先在 404token 灰度验证，再更新 AISelf；两台实例均确认唯一运行容器和健康检查；
4. 保留部署前镜像/commit，可一键回滚；
5. 线上验证失败时仅回滚本补丁，不清理现有账号、路由或数据。

## 7. 验收标准

- 原有 generations、edits 和 OAuth 图片路径测试全部通过；
- `upstream_text_reply` 能跨账号切换且同账号不重复；
- 普通 400、404、轮询超时行为不变；
- strict provider 收到官方契约字段，compat provider 行为不被破坏；
- 404token、AISelf、本地代码版本和 GitHub 提交可追溯；
- 本文档之外没有隐含的调度策略或生产配置变更。
