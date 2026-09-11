# aiself 只读观察基线

观察日期：2026-09-10（Asia/Shanghai）  
目标：`aiself` 线上 sub2api 实例  
方式：SSH 只读探测、HTTPS 只读接口探测、数据库只读查询、候选源码静态阅读。  
敏感信息处理：本文不记录 API key、SSH 口令、解密口令、cookie 或 credential JSON。

## 1. 结论摘要

- `[OBSERVED]` 健康入口 `https://aiself.vip/health` 返回 200；setup 状态已完成；首页返回 Veyra Agent 前端。
- `[OBSERVED]` 现有 `/v1/models` 受 API key/group 作用域控制；使用已授权测试 key 探测到 `gpt-5.5`、`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna`、`gpt-image-2`。这不是全局模型清单，不能据此推断所有账号均支持这些模型。
- `[OBSERVED]` 现有 group 已覆盖多个 provider/transport，但 `composite_model_routes` 为空，`channels` 及其价格相关表当前为空；统一入口不能只依赖现有 composite route 或 channel 配置。
- `[OBSERVED]` group 级模型目录、账号 `model_mapping`、schedulable 状态和平台 adapter 共同影响实际可用性。
- `[OBSERVED]` Grok 视频已有按分辨率/时长的 group 价格和异步 pending/claim/dedup 基础；历史日志的 `account_stats_cost` 为空，说明当前用户收费与账号成本统计并非同一字段。
- `[INFERRED]` 要实现“调用哪个 provider 就按哪个 provider 计费”，必须把 route/account/pricing snapshot 放进同一结算上下文；只新增 public alias 或只扩充 group 模型列表都不够。

## 2. 运行时结构

### 2.1 应用与依赖

探测时看到的运行形态：

| 组件 | 观察结果 |
| --- | --- |
| 应用容器 | `sub2api`，健康状态正常；运行镜像标签与历史升级文档不同，实施前必须重新取证 |
| PostgreSQL | `sub2api-postgres`，健康状态正常 |
| Redis | 健康状态正常 |
| Compose CLI | 远端可用 `docker-compose`；`docker compose` 命令未作为实施前提 |
| 数据持久化 | 应用、数据库和 Redis 均由现有部署管理；本轮未改变挂载或配置 |

运行镜像和 compose 版本存在环境漂移风险。未来实施前必须再次记录 image digest、容器环境摘要、迁移版本和工作树/发布 commit，不能直接沿用历史文档中的标签。

### 2.2 公开接口观察

| 接口 | 结果 | 说明 |
| --- | --- | --- |
| `GET /health` | 200 | 返回健康状态 |
| `GET /setup/status` | 200 | setup 已完成，不需要初始化 |
| `GET /` | 200 | Veyra Agent 前端可返回 |
| 未认证 `GET /v1/models` | 401 | 认证边界有效 |
| 已授权 `GET /v1/models` | 200 | 返回 key/group 作用域模型目录 |
| 已授权最小文本冒烟 | 200 | 之前已完成，见第 6 节；不是本阶段新功能验收 |

## 3. 当前 group 与 provider 形态

以下是按逻辑用途整理的观察摘要，不代表统一入口已经存在：

| group | 平台/transport | 观察到的模型或特征 | 统一入口处理建议 |
| --- | --- | --- | --- |
| 2 | OpenAI | `gpt-5.5`、`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna`、`gpt-image-2` | 先作为独立 candidate 源核验，不直接复用其 group 价格 |
| 3 | OpenAI | `kimi-for-coding` | 需区分 Kimi provider identity 与 OpenAI transport |
| 4 | Anthropic | Kimi 原生/兼容形态 | 必须核验消息、工具和 usage 转换 |
| 5 | Gemini | `gemini-2.5-flash`、`gemini-2.5-flash-lite` | 以 Gemini adapter 能力和价格 profile 为准 |
| 6 | OpenAI | Ark/Doubao/DeepSeek/GLM/Kimi/MiniMax 家族模型 | 不能因为 transport 为 OpenAI 就套用 OpenAI 价格 |
| 10 | Anthropic | Ark Claude 及相关账号 | 需要区分账号真实 provider 与兼容协议 |
| 11 | Grok | 视频独立计价，按分辨率/时长 | 作为独立 media task route，创建时锁定账号与价格 |

模型列表只是当前 group 配置的观察结果，不是 provider 官方能力证明。`gpt-6-astra`、历史废弃模型和新 provider 模型必须在后续能力探测中逐项核验，不能凭名称写入统一目录。

## 4. 账号资格观察

- `[OBSERVED]` OpenAI group 存在多个 active/schedulable 账号，也存在带错误状态的账号；统一池必须按实时 schedulable/health 过滤。
- `[OBSERVED]` Kimi、Gemini、Ark、Ark Claude 等 group 至少各有可调度账号。
- `[OBSERVED]` Grok group 有三个逻辑来源账号：其中两个 `schedulable=true`，一个 `schedulable=false`；该禁用账号不能因为新功能而被隐式重新启用。
- `[OBSERVED]` Grok 账号存在不同 `rate_multiplier`，这进一步证明“账号统计成本/倍率”和“用户收费价格”需要分开建模。
- `[TO-VERIFY]` 每个账号的凭据有效期、provider 侧剩余配额、并发上限、真实模型能力和来源方价格尚未在本阶段全面主动验证；后续能力探测必须使用最小风险、可追踪预算。

## 5. 数据库结构观察

公开表/字段中已观察到以下能力：

| 表/领域 | 已观察到的作用 | 对统一网关的限制 |
| --- | --- | --- |
| `accounts` | provider、credentials/extra、并发、priority、status、schedulable、rate multiplier 等 | 账号是调度实体，但现有 route 未必绑定具体账号 |
| `account_groups` | 账号与 group 关联及优先级 | 可作为候选池基础，但要增加 route 作用域 |
| `groups` | platform、model routing、models list、默认映射、图片/视频价格等 | group 是访问域，不宜独自承担多 provider 动态价格 |
| `composite_model_routes` | public model、match type、target platform、upstream model、endpoint、priority、enabled、notes | 缺少 account binding 与 pricing profile；同 endpoint/public model 的唯一性也会限制多候选表达 |
| `usage_logs` | 请求/上游模型、账号、group、token、媒体、endpoint、倍率、billing mode、request id 等 | 足以作为审计主表基础，但需要关联 route/pricing snapshot |
| `billing_usage_entries` | 使用记录到用户余额/订阅变化的幂等结算入口 | 需要把同一 snapshot/idempotency 规则贯穿媒体任务 |
| `channel_*` | token/per request/image/video/interval 等价格结构和账号统计价格结构 | 当前 aiself 相关 channel 计价表为空，且 channel 作用域不足以代表 route/account 池 |

本阶段只读查询确认：`channels`、`channel_model_pricing`、`channel_account_stats_model_pricing`、`channel_groups`、`channel_pricing_intervals`、`composite_model_routes` 当前均为 0 行；这是当时的运行快照，不应假设未来仍不变。

## 6. 已发生的最小文本证据

在本任务之前，按用户授权使用已有测试 key 对 aiself 发起一次最小 `gpt-5.5` 文本请求：

- 入站：`/v1/chat/completions`。
- 结果：HTTP 200，正常返回简短文本，finish reason 为 stop。
- 上游：日志记录为 `/v1/responses`。
- usage log：`requested_model=gpt-5.5`、`upstream_model=gpt-5.5`，group 为 OpenAI group，记录了 token 用量。
- 本次记录的 `total_cost/actual_cost` 约为 `0.00482` 美元；这是既有链路的真实小额消耗，不是统一网关实现的测试结果。

该证据只证明现有文本链路在当时可用，不能证明 DeepSeek、Kimi、Doubao、Grok 或统一计费已经可用。后续新功能测试必须使用独立 key 和单独预算。

## 7. Grok 视频历史计费观察

历史 usage log 中观察到：

- 存在不同 Grok 视频 upstream model 名称和不同 endpoint 形态。
- 480p/720p、不同 duration 的费用随 group 视频价格计算；历史记录中用户费用存在与分辨率、时长相关的结果。
- 某些记录绑定到具体 Grok 账号，但 `account_stats_cost` 为空；不能把该字段当作当前用户真实收费的唯一来源。
- 现有系统已有 pending billing、Redis TTL、claim/release 和账号粘性基础，这是后续设计应扩展而不是绕过的关键能力。

## 8. 上游倍率与手动规则基线

候选源码复核得到以下边界，属于 `[REVIEWED]`，不是对 aiself 每一个账号都已完成的线上探测：

- 上游 billing probe 访问兼容的 `/v1/sub2api/billing`，保存 `group_rate_multiplier`、`user_rate_multiplier`、`resolved_rate_multiplier`、`effective_rate_multiplier`、状态和 freshness 信息。
- 自动同步路径将声明的 `resolved_rate_multiplier` 写回 `accounts.rate_multiplier`；该字段的源码注释明确它只影响账号维度成本/配额口径，不影响用户/API Key 扣费。
- 账号 rate sync 开启时，原生管理路径拒绝手工修改该账号倍率；因此“自动探测优先、失败后手工兜底”不能只靠原生账号字段实现，必须增加统一入口专用的 lane/profile 规则。
- `unsupported`、`failed` 或 stale probe 不应被解释成倍率 `1.0`，也不应自动沿用其他 provider 的价格。
- 火山引擎/Ark 等非 ChatGPT 计费链路是否支持兼容 probe，当前没有逐账号完成证据；在正式能力证据出现前，统一入口应按 `manual_only` 处理，并要求手动倍率、单位价格或 provider-specific 公式。

这些结论只影响后续新统一入口；既有账号成本统计和旧 group 计费语义不在本次设计中迁移。真实启用前仍需为每条 lane 生成 `upstream-rate-policy-matrix`，由价格负责人确认手动规则和有效版本。

## 9. 候选源码证据索引

以下路径来自候选源码基线 `e7118bc669e2013dbbb2fdf6ab698b5c0d33c488`：

| 主题 | 重点文件 |
| --- | --- |
| `/v1/models` 和复合模型聚合 | `backend/internal/handler/gateway_handler.go` |
| 可用模型与账号调度 | `backend/internal/service/gateway_service.go` |
| route 解析 | `backend/internal/service/composite_route_resolver.go`、`composite_platform.go` |
| composite route migration | `backend/migrations/172_composite_model_routes.sql`、`227_composite_routes_add_cn_providers.sql` |
| admin route CRUD/preview | `backend/internal/handler/admin/group_handler.go` |
| usage/billing 核心 | `backend/internal/service/gateway_usage_billing.go`、`billing_service.go` |
| OpenAI/Grok 媒体 usage | `backend/internal/service/openai_gateway_usage.go`、`grok_media.go` |
| channel 价格 | `backend/internal/service/channel.go` |
| 账号与 model mapping | `backend/internal/service/account.go` |

## 10. 已知限制与待补证据

1. 本轮没有对所有 provider 发起探测，也没有主动测试图片/视频，因此模型能力表仍需后续专项取证。
2. 本轮没有写入或创建新 route/profile，故没有真实的统一 resolver/账务运行证据。
3. 本轮本地未执行 Go 测试：当前工作机 PATH 中没有可用 `go` 命令；不能把未执行的测试写成通过。
4. 线上镜像标签和历史文档存在漂移；实施前必须重新获取 digest、迁移版本和配置摘要。
5. 上游 provider 的价格、免费额度、账号成本和来源方规则必须由价格负责人确认并形成版本化 profile，不能从历史 usage log 反推全部价格。
