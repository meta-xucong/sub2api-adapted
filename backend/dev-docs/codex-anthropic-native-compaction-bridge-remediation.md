# Codex Anthropic 压缩桥接审计修复

状态：`LOCAL_FIX_APPLIED; TEST_PASS; ONLINE_E2E_PENDING`

目标仓库：`D:\AI\SSH\sub2api`
基线：`codex/cloud-integration-pr12-20261019` @ `3073f8dd318f9b6562e81736ce13c0f31650ef81`

## 目标与边界

只修复本地审计发现的三处桥接问题，使 Codex 原生 `/responses` 压缩经过 Anthropic 上游时能正确转换，同时让普通共享转换调用保持原行为。

不改账号选择、路由、重试、前端、数据库、其他协议路径或部署；不发线上请求。

### 2026-10-28 范围追加：压缩调用用量计费

用户授权补齐 Anthropic compaction 的计费用量映射。Anthropic 将压缩调用用量放在 `usage.iterations`，而顶层 token 用量可为零；仅在专用压缩 SSE 处理器中读取 `type=compaction` 的 input/output token，写入既有内部用量字段供既有计费路径计算。普通请求、响应事件格式及其他 iteration 类型不变。

## 修复项

1. **共享转换器越界处理桥接状态**：`ResponsesToAnthropicRequest` 保持桥接关闭。只有 Anthropic Responses 转发入口在压缩触发或携带本桥接状态时，显式调用 opt-in 转换器。Antigravity 等共享转换器调用方继续走原逻辑。
2. **Codex OpenAI Gateway 入口未接线**：在 `forwardResponsesViaNativeAnthropic` 中识别初始压缩和桥接状态续接；初始压缩使用专用 compaction SSE 转换器，续接仍使用现有普通流转换器。
3. **Beta 请求头大小写变体造成重复 header**：合并请求中的所有大小写形式的 `anthropic-beta` 值，确保存在 `compact-2026-09-04`，再统一写回一个 header 项。

## 改动与验收

- 转换器采用默认关闭、显式开启的入口；其他调用点不改。
- 两个 Anthropic Responses 转发入口复用同一压缩流转换器。
- 回归测试覆盖共享转换器不启用桥接、OpenAI Gateway 上游压缩请求与单个 completed 输出项、beta 值合并且只保留一个 header 项。
- 普通对话和 continuation 继续使用既有流处理代码。
- 定向和受影响包测试通过，且独立只读审计通过后，本地修复验收完成。

## 本地执行记录

本次修复范围内的 `go test ./internal/pkg/apicompat -count=1`、`go test ./internal/service -run 'TestOpenAIGatewayNativeAnthropicCompactionRequest|TestHandleResponsesAnthropicCompactionStream' -count=1`、`go test ./internal/service -count=1`、`go vet ./internal/pkg/apicompat ./internal/service` 和 `git diff --check` 均通过。
本次改动仅限以上实现、对应回归测试及本文件。线上上游透传仍未验证；未提交、推送或部署。
