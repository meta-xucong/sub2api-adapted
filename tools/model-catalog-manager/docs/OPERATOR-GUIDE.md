# 操作说明

## 管理范围

此工具可通过 Sub2API 管理 API 检查账号、读取上游模型目录并生成候选模型整理方案。官方目录核验仅作参考；厂商官方退役信息不能单独证明代理上游已停服。

当前版本的管理 API 地址固定为 `https://404token.xyz`。如需连接其他实例，必须先对代码中的目标地址进行单独审查和发布；不要仅修改配置后假设目标地址已改变。

## 配置与兼容性

- 将管理员 API key 存在操作系统凭据管理器中。Windows 默认凭据项名为 `Sub2API 404token Admin API Key`；Linux 使用 systemd credential `admin-api-key`。
- 配置文件中的 `account_scope`、内存暴露确认和同步写入开关决定检查范围及行为。使用前逐项确认。
- 示例的 `target.runtime_compatibility` 字段为空。运行 `discover`、`apply` 或 `run` 前，以及运行 `account_scope=all` 的 `inspect` 前，必须根据目标实例的实际构建版本、API 契约和可复核证据填写 `build_id`、`api_contract_id`、`evidence` 与 `reviewed_at`。
- `official-review` 可选使用只读官方目录凭据：`ANTHROPIC_API_KEY`、`OPENAI_API_KEY`、`DEEPSEEK_API_KEY`、`DASHSCOPE_API_KEY`、`XAI_API_KEY`。按需要设置环境变量；不要把这些 key 写入配置或 Git。

## 凭据与报告

- 管理员 API key 不放入 JSON 配置或命令行。
- `adopt` 只在管理器本地登记经选择的映射 ownership，不调用 Sub2API 写接口，也不改变账号映射。
- 先执行 inspect/discover 并人工审阅报告，再决定是否执行后续批准步骤。
- 报告可能含真实账号 ID、上游模型名和映射，应视为敏感运维资料；不要提交到 GitHub、工单或公开聊天。

## 命令发现

构建后可查询每个命令的参数：

```powershell
.\model-catalog-manager.exe inspect --help
.\model-catalog-manager.exe adopt --help
.\model-catalog-manager.exe discover --help
.\model-catalog-manager.exe official-review --help
.\model-catalog-manager.exe inventory-summary --help
.\model-catalog-manager.exe apply --help
.\model-catalog-manager.exe run --help
```

只读检查示例：

```powershell
.\model-catalog-manager.exe inspect --config .\config.json
```

请先使用只读路径，并核对每份报告的完整性和目标账号范围。只有确认规则、账号范围与映射变化后，才进入显式批准的更新流程。
