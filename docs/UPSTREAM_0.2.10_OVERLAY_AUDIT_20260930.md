# Sub2API v0.2.10 overlay audit record

审计类型：只读计划/范围审计，不是代码验收

日期：2026-09-30

## 结论

```yaml
AUDIT_STATUS: BATCH_REAUDIT_IN_PROGRESS
CODE_MIGRATION: IN_PROGRESS
RELEASE_STATUS: NOT_ACCEPTED
```

首轮独立审计结论为 `FAIL`。失败项已经回写到
`UPSTREAM_0.2.10_MIGRATION_PLAN_20260930.md`：补齐 8 批覆盖、官方证据路径、
数据库/权限/回滚门槛、Fast/auto-review/Kimi/OpenCode/relay/Veyra 处置，以及
缺失的 0.1.173 文档桥接。该记录只允许在重新复核这些修订后变为计划通过；
它不表示任何迁移代码已经通过。

## 已核实事实

| 事实 | 证据 | 判定 |
|---|---|---|
| 当前代码快照（备份对象） | `13430dd5209728a4a1b5523f92ed4de267d1c745` | CODE_FACT |
| 方案文档提交 | `654a154f66b07a95ad2b6fde89edd8dcf3183baf` | DOC_FACT；不属于旧代码备份 |
| 当前快照 GitHub 留档 | `backup/pre-v0210-migration-20260930` | OPERATION_FACT |
| 官方本次基线 | `upstream/main` → `a60a29549`，版本为 `0.2.10` | OFFICIAL_SOURCE_FACT |
| 本地标签对照 | `v0.2.10` → `2f3fed2fd`，仅少 `VERSION` 同步提交；来自适配仓库 | OFFICIAL_SOURCE_FACT / REPOSITORY_FACT |
| 当前自有树与官方差异过大 | 约 1939 个文件，不能整体 merge | CODE_FACT |
| 旧实验树不干净 | `D:\AI\SSH\_worktrees\sub2api-v0210-overlay-port` / branch `upgrade/v0210-overlay-port-20260929` 有大量未提交/未跟踪项 | OPERATION_FACT |
| 当前工作树未跟踪测试文件 | `backend/internal/pkg/apicompat/historical_model_contract_matrix_test.go` | CODE_FACT；不纳入留档 |
| 官方 tag 与 main 差异 | `git diff --name-status v0.2.10 upstream/main` 仅为 `backend/cmd/server/VERSION` | OFFICIAL_SOURCE_FACT |
| 官方基线全仓测试 | `go.cmd test -count=1 -timeout=900s ./...` → exit `1`；核心包通过，repository 有 Aliyun 与 Windows `sh` 环境失败 | EXPERIMENT_FACT；迁移回归对照 |

## 官方已覆盖的能力（迁移时跳过旧实现）

- Responses 基础生命周期、工具参数 `function_call_arguments.done`、终态文本恢复和序号零。
- Responses 输入 item ID 基础清洗。
- Compact HTTP/SSE 入口、失败终态和 Chat fallback。
- DeepSeek reasoning、Anthropic 通用 bridge、基础图片路由/错误处理、Fast `service_tier`、Codex manifest、通用 Grok media 的基础能力。

以官方 `v0.2.10` 源码和对应 tests 为准，不能以旧提交标题单独判定已吸收。

### 可复核的官方提交证据

以下命令已在干净官方基线工作树执行，提交均可由 `git show` 复核：

| 官方提交 | 结果 | 迁移含义 |
|---|---|---|
| `0952ce341` | `fix(apicompat): include streamed arguments in function_call_arguments.done` | 旧的工具参数 `.done` 补丁不重复移植 |
| `a5d8db244` | `fix(responses): strip oversized input item IDs` | 输入 item ID 基础清洗不重复移植 |
| `97bdde313` | `fix(apicompat): keep tool arguments sent on content_block_start` | Anthropic 工具输入基础修复不重复移植 |
| `cf3577a3c`、`d6d9f6ea4`、`26b5e8745` | Responses 兼容、未完成流终态、content_part/full output | 只核对自有终态重协调和 fail-closed 差异 |
| `f06bf181d` | `service_tier` across Responses/Chat/WS | 只保留第三方 URL 策略差异 |
| `8490a8186` | Claude Sonnet 5.5 | 官方模型目录能力不重复移植 |

已执行的结构检查：

```text
git diff --name-status v0.2.10 upstream/main
=> M backend/cmd/server/VERSION
git cat-file -e v0.2.10:<official file>
=> official bridge/item-id/compact files PRESENT; custom smartrouter/image bridge/operator guard ABSENT
git grep -l "function_call_arguments.done" v0.2.10 -- backend/internal/pkg/apicompat | Measure-Object
=> 19 files
```

这已经补足了“官方吸收项”的第一层证据；仍需在每个实际移植批次中附定向测试退出码，不能把本表当成代码验收。

## 已开始迁移的批次证据

| 批次 | 提交 | 结果 | 测试证据 |
|---|---|---|---|
| Smart Router core | `014b69f61` | 已移植；尚未接入 service/config/repository | `go.cmd test -count=1 -timeout=120s ./internal/smartrouter/core` → exit 0；worker 的 `-tags unit` → exit 0 |
| 模型目录过滤/手工 mapping | `5bd7026ff`, `b5b625173`, `c1a2eb579` | 已移植；日期/过期 ID 过滤、正式 ID 保留、空结果拒绝、手工 mapping 不再被自动发现规则误伤 | `go.cmd test ... ./internal/pkg/openai` → exit 0；`./internal/service -run TestFetchUpstream` → exit 0；`-tags unit ...TestAdminService_CompositeModelsListCandidatesIncludeConcreteAccountMappings` → exit 0 |
| 模型目录第二轮收紧 | `389b0500b` | 自定义 provider 的日期样式 ID 不再误删；bare `gpt-5.6`/`gpt-6` 不进入自动目录或管理员候选；管理员 OpenAI 候选改用 curated formal IDs；standalone mapping 有覆盖 | `go.cmd test -count=1 -timeout=180s ./internal/pkg/openai` → exit 0；`go.cmd test ... ./internal/service -run TestFetchUpstream` → exit 0；`-tags unit ...TestAdminService_CompositeModelsListCandidatesIncludeConcreteAccountMappings` → exit 0 |
| Responses 终态 item ID | `45c17b42b`, `23279d81f` | Chat->Responses 首 ID/工具 call ID 保持稳定，并将同一 SSE 响应中 output_item ID 重协调到 terminal `response.output`；已接入 `openai_gateway_response_handling.go` | `go.cmd test -count=1 -timeout=300s ./internal/pkg/apicompat ./internal/service -run TestResponsesStreamItemIDReconciler` → exit 0；独立复审尚未完成 |
| Smart Router core | `014b69f61` | 仅移植了独立 core；独立审计判定 FAIL：尚无 service/API 接线，compact 默认预算和限流边界存在缺口 | `go test ./internal/smartrouter/core` → exit 0；不能作为功能通过 |
| 官方版本同步 | `3ecc366a3` | 已纳入官方 `upstream/main` 的 VERSION 同步 | `git show --stat 3ecc366a3` |

上述只证明对应批次的定向测试；不等于整树、数据库、实盘或发布验收。

## 仍需核对/移植的自有差异

- Smart Router core、exact-model/capability/source-group、429 backoff、持久健康账本、04:00 校准。
- 图片超时/空结果 failover 与 Responses image-generation → Images bridge。
- Responses item ID 终态重协调、Claude signed thinking、未完成流 fail-closed、T0 幂等/跨实例续接的官方缺口。
- 模型日期/下线 alias 过滤、手工 mapping 保留、第三方 `codex-auto-review` 到 `gpt-5.5` 的保守兼容。
- 第三方 service tier 剥离、operator test key guard。
- Wokey/KIE profile 和 reference relay。
- Veyra portal/账务 bridge；Kimi/OpenCode 只移植官方没有的业务差异。

## 阻断项

1. 尚无完整迁移完成后的干净发布提交。
2. 尚无迁移后全仓测试退出码；当前只有已列批次定向测试。
3. 尚无数据库前向/回滚、权限负向、账务不变性证据。
4. 尚无真实 GLM、DeepSeek、Claude、Qwen、MiniMax、Kimi、HY4 矩阵证据。
5. 已启动针对模型目录和 Smart Router core 的独立代码审计；结果尚未全部回写，不能视为 PASS。
6. 官方吸收项目前只有源码存在性和测试文件证据，尚未逐项生成 `git show`/定向测试的独立证据包；因此本记录仍是计划级条件结论，不是迁移放行。

## 放行条件

只有在每个迁移批次都有“变更文件、官方对照、测试命令/退出码、独立审计结论”，
且最终满足全仓本地回归、临时数据库迁移、受控真实模型/账务矩阵、工作树干净和
可恢复回滚后，才能将 `AUDIT_STATUS` 改为 `PASS`，并讨论发布/部署。
