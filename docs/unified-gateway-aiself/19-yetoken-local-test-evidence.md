# Yetoken 本地统一模型验证证据

日期：2026-09-19  
工作区：`upgrade-worktree/merged-dryrun`  
阶段：本地实现完成，未部署、未推送、统一 runtime 生产 gate 保持关闭。

## 1. 数据来源与安全边界

主控通过 aiself VPS 的只读探测确认存在 YeToken 账号和模型映射。仓库只保留脱敏元数据夹具：

`backend/internal/service/testdata/yetoken_accounts_sanitized.json`

夹具仅包含账号 ID、脱敏名称、平台、类型、状态、`schedulable` 和模型名；不包含 `credentials`、API key、access token、refresh token、密码、`base_url` 或 `custom_base_url`。测试会先把原始 JSON 转成小写文本，并断言上述敏感字段不存在。

当前夹具覆盖：

- 可调度 GPT 文本/推理模型；
- `gpt-image-2` 图像模型；
- DeepSeek、GLM、Kimi、MiniMax、Qwen、HY 等国模候选；
- 一个 `error + unschedulable` 账号，包含已退役 GPT 模型，用于验证实时资格过滤。

真实上游凭据没有拉入本地，也没有写入仓库、日志或测试输出；本轮只验证统一目录、资格筛选和计费前置契约，不执行无授权的真实付费请求。

## 2. 本地实现证据

- 统一入口仅接受配置的单一 `UnifiedGatewayAccessGroupID`；组未配置时管理面和 runtime 均 fail-closed。
- superadmin 也必须满足账号属于 designated group，不能绕过账号归属校验。
- 模型候选从 designated group 账号的 `model_mapping` 派生，保留 provider、endpoint、账号状态和可调度性信息；不自动发布路线。
- `inactive`、`unschedulable`、provider/endpoint capability 不匹配的候选不会进入运行时模型目录。
- 计费字段继续要求显式价格/倍率；新建 lane 不再填充隐式默认价格或倍率。普通按量与 image 按次复用原版计费模式，失败路径释放预占，不产生成功扣费。
- 统一路由保持隔离，没有重写既有 `/v1` 路由。
- 管理前端提供候选模型只读预览、刷新入口和显式价格编辑字段。

## 3. 验证命令与结果

以下命令均在本地候选工作区执行并通过：

```text
go test ./... -count=1
go test ./internal/service ./internal/handler/admin ./internal/handler ./internal/server/routes -run 'UnifiedGateway|UnifiedErrorEnvelope' -count=1
go test -tags unit ./internal/config -run TestConfigKeysAreEnvReachable -count=1
pnpm typecheck
pnpm lint:check
pnpm vitest run src/views/admin/__tests__/UnifiedGatewayView.spec.ts src/api/__tests__/admin.unifiedGateway.spec.ts
```

统一网关前端定向测试：2 个文件、14 个测试通过。后端全仓 `go test ./... -count=1` 通过；浏览器数据过期提示仅为既有警告，不影响结果。

## 4. 独立审计结论

第一次独立审计发现两项真实问题：契约版本标识未升级、Yetoken 夹具和测试含 endpoint 字段。主控已修复并重新测试。第二次独立只读复核结论为 `有条件通过`，`MUST_FIX: 无`；条件是测试证据由主控执行，且本轮没有执行 PostgreSQL fresh/repeat/conflict、真实 provider staging 或生产部署验证。

因此本文件只证明“本地代码和安全元数据夹具验证完成”，不构成生产启用或 VPS 部署授权。
