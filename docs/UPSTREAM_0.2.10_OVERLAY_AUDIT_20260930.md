# Sub2API v0.2.10 overlay audit record

审计类型：只读计划/范围审计，不是代码验收

日期：2026-09-30

## 结论

```yaml
AUDIT_STATUS: CONDITIONAL_PASS_FOR_PLAN_ONLY
CODE_MIGRATION: NOT_STARTED
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
| 当前代码快照 | `13430dd5209728a4a1b5523f92ed4de267d1c745` | CODE_FACT |
| 当前快照 GitHub 留档 | `backup/pre-v0210-migration-20260930` | OPERATION_FACT |
| 官方 tag | `v0.2.10` → `2f3fed2fd` | OFFICIAL_SOURCE_FACT |
| 官方 main | `a60a29549`，仅比 tag 多版本同步提交 | OFFICIAL_SOURCE_FACT |
| 当前自有树与官方差异过大 | 约 1939 个文件，不能整体 merge | CODE_FACT |
| 旧实验树不干净 | `sub2api-v0210-overlay-port-20260929` 有大量未提交/未跟踪项 | OPERATION_FACT |
| 当前工作树未跟踪测试文件 | `backend/internal/pkg/apicompat/historical_model_contract_matrix_test.go` | CODE_FACT；不纳入留档 |

## 官方已覆盖的能力（迁移时跳过旧实现）

- Responses 基础生命周期、工具参数 `function_call_arguments.done`、终态文本恢复和序号零。
- Responses 输入 item ID 基础清洗。
- Compact HTTP/SSE 入口、失败终态和 Chat fallback。
- DeepSeek reasoning、Anthropic 通用 bridge、基础图片路由/错误处理、Fast `service_tier`、Codex manifest、通用 Grok media 的基础能力。

以官方 `v0.2.10` 源码和对应 tests 为准，不能以旧提交标题单独判定已吸收。

## 仍需核对/移植的自有差异

- Smart Router core、exact-model/capability/source-group、429 backoff、持久健康账本、04:00 校准。
- 图片超时/空结果 failover 与 Responses image-generation → Images bridge。
- Responses item ID 终态重协调、Claude signed thinking、未完成流 fail-closed、T0 幂等/跨实例续接的官方缺口。
- 模型日期/下线 alias 过滤、手工 mapping 保留、第三方 `codex-auto-review` 到 `gpt-5.5` 的保守兼容。
- 第三方 service tier 剥离、operator test key guard。
- Wokey/KIE profile 和 reference relay。
- Veyra portal/账务 bridge；Kimi/OpenCode 只移植官方没有的业务差异。

## 阻断项

1. 尚无干净 v0.2.10 叠加后的代码提交。
2. 尚无迁移后定向或全仓测试退出码。
3. 尚无数据库前向/回滚、权限负向、账务不变性证据。
4. 尚无真实 GLM、DeepSeek、Claude、Qwen、MiniMax、Kimi、HY4 矩阵证据。
5. 尚无针对新迁移树的独立代码审计。

## 放行条件

只有在每个迁移批次都有“变更文件、官方对照、测试命令/退出码、独立审计结论”，
且最终满足全仓本地回归、临时数据库迁移、受控真实模型/账务矩阵、工作树干净和
可恢复回滚后，才能将 `AUDIT_STATUS` 改为 `PASS`，并讨论发布/部署。
