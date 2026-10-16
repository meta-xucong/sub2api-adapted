# Sub2API 模型目录整理器

这是一个独立的 Go 命令行工具，用于检查 Sub2API 账号目录、发现上游模型、对照官方公开目录，并生成待审阅的模型整理结果。它运行在 Sub2API 进程之外，不修改 Sub2API 源码。

## 当前范围

- 支持 `inspect`、`adopt`、`discover`、`apply`、`run`、`official-review` 和 `inventory-summary`；参数可通过各命令的 `--help` 查看。
- 预览和审核报告写入本机目录。映射变更须经显式审核流程。
- 官方目录核验只为人工审核提供证据；厂商官方目录不能证明某个代理上游是否仍支持某模型。
- 当前管理 API 客户端固定连接 `https://404token.xyz`。发布包不含管理员 API key、实际账号配置、运行报告或本地测试数据。

## 构建与验证

需要 Go 1.23 或更新版本。

```powershell
go test ./... -count=1
go build -o model-catalog-manager.exe ./cmd/model-catalog-manager
```

## 使用前

1. 复制 `config-all-accounts.example.json` 到本机配置文件，并核对全部安全开关。示例中的 `runtime_compatibility` 为空；运行 `discover`、`apply` 或 `run` 前，以及运行 `account_scope=all` 的 `inspect` 前，必须根据目标实例实际版本和 API 契约证据填写 `build_id`、`api_contract_id`、`evidence` 与 `reviewed_at`，不要猜测。
2. 将管理员 API key 存入当前操作系统的凭据存储；不要写进配置、命令行参数、报告或 Git。
3. 先运行只读检查并审阅输出。不要直接对真实账号执行批量更改。
4. 账号检查报告可能包含账号 ID、模型名和映射信息，应留在本地并按敏感运维数据保管。

`official-review` 可选使用对应官方 API 的独立环境变量：`ANTHROPIC_API_KEY`、`OPENAI_API_KEY`、`DEEPSEEK_API_KEY`、`DASHSCOPE_API_KEY`、`XAI_API_KEY`。这些只用于只读官方目录核验，不要写入配置文件或日志。

命令与安全步骤见 [操作说明](docs/OPERATOR-GUIDE.md) 与 [目录摘要说明](docs/INVENTORY-SUMMARY.md)。
