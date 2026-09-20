# aiself 本地快照与 Claude 统一网关探测（2026-09-19）

## 1. 目的与边界

本次只读取 aiself VPS 数据，未修改 VPS、未创建远端 API Key、未发布统一网关配置。目标是：

1. 在本地保留可恢复的完整 PostgreSQL 数据副本；
2. 用本地副本核对刚新增的 Claude 分组和账号；
3. 验证 Claude 能否纳入现有 `/unified/v1` OpenAI-shaped 统一入口；
4. 保持旧 `/v1` 路由和既有计费链路不受影响。

## 2. 本地完整副本

快照目录（不进入 Git）：

`E:\AI\_vps_snapshots\aiself-20260919`

已保存并校验：

- `sub2api-pg.dump.gz`：远端 `pg_dump -Fc` 压缩归档；
- `sub2api-pg.dump`：解压后的 PostgreSQL custom archive，文件头为 `PGDMP`；
- 压缩归档 SHA-256：`3A089CB2832BFF6AFCEE27C60FFD624A902E952BCC985AE269181C55F30749F6`；
- 解压归档 SHA-256：`7958B0550D72F32F69886FAF9D5D772BD2B7EBCE0A3EBDA1E215D2D34E0D1ABC`。

本地使用隔离的 PostgreSQL 18.4 便携运行时恢复，连接地址为 `127.0.0.1:55432`，数据目录仍在上述快照目录下；没有安装 Windows 服务。`pg_restore --exit-on-error` 成功完成。

恢复后的逻辑数据核验：

- 原始业务库：12 个 group（其中 9 个未删除）、120 个 account、49 个未删除 API Key；
- 本地恢复后数据库逻辑大小约 481 MB。它小于远端物理库大小是正常的：custom dump 恢复会重建索引并消除 PostgreSQL 表/索引膨胀，不代表数据缺失；
- 在本地副本上额外应用候选代码的统一网关迁移 `235`–`238`，用于后续本地运行测试；这些迁移没有回写 VPS。

## 3. aiself 新增 Claude 数据

本地复刻与 VPS 只读探针一致：

| 项目 | 结果 |
|---|---|
| 分组 | `id=14`, `name=claude` |
| 分组平台 | `anthropic` |
| 分组状态 | `active` |
| 分组账号数 | 6 |
| API Key 数 | 0 |
| Claude 账号 | `123`–`128` |
| 账号类型 | `apikey` |
| 账号状态 | 6 个均 `active` 且 `schedulable=true` |
| 模型映射并集 | 22 个 Claude 模型 |

模型并集包括 `claude-opus-4-6/4-7/4-8/5`、`claude-sonnet-4-6/5`、`claude-haiku-4-5`、`claude-fable-5` 以及历史版本名等。账号凭证、`api_key`、`base_url` 和其他敏感字段没有写入仓库中的测试 fixture。

当前重要发现：Claude 分组本身还没有下游 API Key，因此“数据已存在”不等于“已进入统一 API”。统一网关还需要管理员在其指定的统一访问分组中发布模型目标和账号绑定；当前契约只允许一个指定统一访问分组，不会偷偷跨多个分组自动取数。最小操作可以是把 Claude 账号纳入现有统一访问分组，或将 `UnifiedGatewayAccessGroupID` 指向专门承载统一入口的分组后重新发布配置。

## 4. 协议兼容结论

可以兼容，但不是把 OpenAI JSON 原样发送给 Anthropic。统一入口采用 OpenAI-shaped 请求：

```text
下游 OpenAI Chat Completions / Responses
        │
        ▼
/unified/v1 统一网关选中 Claude 账号、模型映射和价格快照
        │
        ▼
既有 GatewayService.ForwardAsChatCompletions / ForwardAsResponses
        │  OpenAI → Anthropic Messages
        ▼
上游 {base_url}/v1/messages?beta=true + x-api-key
        │
        ▼
Anthropic SSE → OpenAI JSON/SSE，回到统一结算
```

本次候选代码已补齐的最小边界：

- 新增 `anthropic-api-key` 统一 provider identity；
- `PlatformAnthropic + AccountTypeAPIKey` 自动出现在候选模型中；
- 能力矩阵只放行 `chat_completions` 和 `responses`，明确拒绝图片、视频等端点；
- 复用已经存在的 Anthropic 转换层，不另写第二套协议转换器；
- 路由绑定的 `public_model → upstream_model` 会覆盖账号旧映射，即使两者同名也不会让账号级旧映射抢回路由选择；
- 流式上游仍由共享转换器解析；统一运行时当前按既有设计通过 recorder 收集完整响应后结算/回传，因此是协议正确的 SSE/JSON 结果，不是逐块实时透传；
- 按次计费路由统一按 1 次预留、结算和扣费，即使上游返回 token usage，也不会误按 token 数收费；
- Claude 适配结果受统一响应体上限约束，超限直接失败并释放预留；
- 失败、无有效用量、协议不匹配均不捕获费用。

## 5. 本地验证证据

已通过：

- 统一网关能力矩阵测试，包含 generic Anthropic API Key 的 Chat/Responses、媒体拒绝和 provider alias；
- 模型候选识别测试，确认 `PlatformAnthropic + APIKey` 输出 `anthropic-api-key`；
- Yetoken/Claude 混合模型 fixture 测试，确认 OpenAI、国产模型、图片模型与 Claude 模型可出现在同一模型目录，失效账号中的旧模型不出现在可用列表；
- Chat Completions 真实转换形态测试：验证 `/v1/messages`、`x-api-key`、模型映射、Anthropic SSE 转 OpenAI 响应和用量；
- Responses 真实转换形态测试：验证同一 Claude 账号走 Responses 入口仍转换到 Anthropic Messages 并返回统一格式；
- 按次计费测试：即使模拟上游返回 12 个 token-like units，最终 `MeasuredUnits=1`、扣费只按 1 次；
- 旧 `/v1` 没有接入统一执行器，代码触点仍与 `/unified/v1` 隔离。

## 6. 尚未做的事情

本次没有做以下动作：

- 没有把 Claude 分组自动写入 VPS 的统一路由目录；
- 没有在 group 14 上创建 API Key；
- 没有把真实 Claude 凭证复制到测试代码；
- 没有新增公开 `/unified/v1/messages`。如果以后要让 Anthropic SDK 直接使用 Anthropic Messages 公共协议，应另立契约并单独审计，不能混入当前 OpenAI-shaped 最小实现。

下一步若要在本地做完整 HTTP 验收，应只在本地复刻库创建临时统一访问 API Key、为 group 14 发布 Claude 目标/绑定，并用本地假上游或隔离测试 endpoint 验证；验收后清理临时数据，再决定是否把相同配置发布到 VPS。

## 7. 最终审计结论

独立只读复审已完成：`MUST_FIX: 无`，`AUDIT_RESULT: 通过`。复审重点包括 Claude provider identity、单一指定分组门禁、同名模型映射覆盖、OpenAI↔Anthropic Chat/Responses 适配、同步/异步按次计费归一为 1，以及旧 `/v1` 隔离。

剩余限制是环境性而非代码阻断：本机便携 Go 工具链未启用 cgo，且没有 gcc/clang，因此本轮没有执行 `go test -race`；Claude 适配仍是 recorder 收集后回传，尚未实现逐块实时 SSE；VPS 尚未写入任何新配置或 API Key。
