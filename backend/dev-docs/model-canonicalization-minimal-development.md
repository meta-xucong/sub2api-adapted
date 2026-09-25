# Sub2API 模型正式名归一化：开发、审计与验收记录

## 目标

在不改 Codex 安装器、CCSwitch、`model_catalog.rs`、官方二进制、数据库结构或前端的前提下，让 `/v1/models`、composite 模型列表和 Codex manifest 只暴露可路由的正式模型 ID。安全处理日期、`preview`、`exp` 别名；不能证明唯一归属的候选不猜测、不展示。

基线：`cecb23bb2824f7a8e245675c14d62cc3ebb18ca4`

## 规则

- 已有正式基名时，日期/preview/exp 变体归并到基名。
- 没有正式基名时，只有一个日期候选才生成基名；多个日期候选冲突则全部隐藏。
- preview/exp 没有明确基名时不猜测。
- GPT 裸父模型若存在多个语义子模型（例如 `luna/sol/terra`）则隐藏；语义子模型保留。
- 通配符和空映射不公开；原始映射仍用于上游转发和计费字段。
- 正式名请求可通过同族安全别名解析回原始账号映射。
- composite 同一正式模型被多个平台声明时，按组优先级、再按账号优先级选择；平级跨平台仍 fail closed。

## 修改文件

- `backend/internal/service/model_id_normalization.go`
- `backend/internal/service/model_id_normalization_test.go`
- `backend/internal/service/account.go`
- `backend/internal/service/gateway_service.go`
- `backend/internal/service/openai_codex_models_service.go`

## 模拟测试与审计

```text
go test -count=1 -timeout=300s ./internal/service ./internal/handler ./internal/pkg/openai -> 0
go test -count=1 -timeout=300s ./internal/pkg/apicompat ./internal/server/routes -> 0
go vet ./internal/service ./internal/handler ./internal/pkg/openai -> 0
gofmt -l changed Go files -> empty
git diff --check -> 0
```

覆盖：唯一日期别名、冲突日期别名、GPT 父模型隐藏、OpenAI 既有过滤、账号映射回译、composite 日期别名、composite 多平台优先级、service 列表和 Codex manifest 边界。

仓库包仍有一个本轮无关问题：`TestAliyunCaptchaVerifier_TransportError` 失败，本轮未修改验证码实现或测试，因此没有宣称 `go test ./...` 全仓通过。

## 线上实测

部署版本：`model-canon-20260925-r3`

二进制 SHA256：

`a94ee89ecf7597023fdf6b2350c342ec31311ab21770b15fa1c7ae007d05cd3c`

两台 `unified-api-internal` 真实 `/v1/models` 与 Responses 验证：

| 主机 | 模型数 | 日期后缀 | `gpt-5.6` 父 ID | GLM / DeepSeek / Claude / GPT |
|---|---:|---:|---|---|
| aiself | 53 | 0 | absent | 4/4 HTTP 200，completed |
| 404token | 22 | 0 | absent | 4/4 HTTP 200，completed |

实测模型：`glm-5.2`、`deepseek-v4-flash`、`claude-fable-5`、`gpt-5.6-sol`。两台均健康，未改数据库；部署备份已保留在远端 `model-canon-a94ee89ecf75-*` 目录。

## 验收结论

```yaml
IMPLEMENTATION_VERSION: model-canon-20260925-r3
AUDIT_STATUS: MAIN_SESSION_READ_ONLY_AUDIT_COMPLETE
INDEPENDENT_AUDIT: UNAVAILABLE_IN_CURRENT_SESSION
SIMULATION_STATUS: PASS
LIVE_STATUS: PASS_AISELF_AND_404TOKEN
ACCEPTANCE: ACCEPTED_FOR_MODEL_CANONICALIZATION_SCOPE
```

这是模型正式名归一化和对应 composite 路由增强的验收，不等同于对所有未来上游模型实时可用性的永久保证；上游下线、余额、限流仍由现有账号健康和映射状态决定。
