# Sub2API 官方 0.2.10 叠加层迁移与验收方案

状态：`IN_PROGRESS_INDEPENDENT_AUDIT_PENDING`

日期：2026-09-30（Asia/Shanghai）

## 1. 目标和边界

本次把自有 Sub2API 适配层迁移到官方 `0.2.10` 基线，逐项判断官方是否已经吸收旧改造，只保留官方缺失且有业务证据的增量。目标是兼容 Responses/Chat/Anthropic、统一网关模型目录、Smart Router 健康隔离、图片桥接、失败闭环和运维安全；不修改 Codex 安装器、CCSwitch、`model_catalog.rs`、官方 Codex 二进制或 U 盘项目。

在本地全量测试、独立只读审计和受控真实矩阵完成前，不部署 aiself、404token，不修改生产数据库。

## 2. 固定版本和回滚点

| 项目 | 固定值 |
|---|---|
| 迁移分支 | `upgrade/v0210-overlay-migration-20260930` |
| 当前 HEAD | `f3372897d4b476aaef9213639d887f6955d24f78` |
| 当前 HEAD 已推送 | `origin/upgrade/v0210-overlay-migration-20260930` |
| 官方来源 | `upstream/main@a60a29549f488a854966aaec9541abbe006cac22` |
| 官方版本 | `0.2.10` |
| 本地适配标签 | `v0.2.10@2f3fed2fd`；仅作适配仓库可复现标签，不代替官方来源证据 |
| 旧版本留档 | `backup/pre-v0210-migration-20260930` → `13430dd5209728a4a1b5523f92ed4de267d1c745` |
| 当前工作树 | 已提交、已推送；未部署 |

官方主线已合入迁移分支历史；通过 `git merge-base --is-ancestor a60a29549 HEAD` 验证。迁移不是把旧分支整体覆盖到官方，而是以官方代码为主、按批次叠加自有差异。

## 3. 官方已吸收、不得重复移植的内容

已对官方源码和测试做过定位的基础能力包括：Responses/Chat/Anthropic 基础桥接、Responses 输入 item ID 清洗、工具参数终态、基础 compact HTTP/SSE、基础图片上传/错误处理、DeepSeek reasoning、Fast `service_tier`、Codex manifest 和通用 Grok media。对应官方路径主要是：

- `backend/internal/pkg/apicompat/`
- `backend/internal/service/openai_responses_item_id.go`
- `backend/internal/service/openai_gateway_response_handling.go`
- `backend/internal/service/openai_compact_*`
- `backend/internal/service/openai_images.go`
- `backend/internal/service/openai_fast_service_tier.go`

自有层只保留官方没有覆盖的 Responses 终态 item ID 重协调、结构化失败闭环、幂等/续接、Responses image bridge、模型正式名过滤、Smart Router 以及 operator 测试护栏。每一项仍需以源码差异和测试证据验收。

## 4. 已迁移和已验证的批次

### 4.1 Responses/Chat/Anthropic 协议增量

已保留/迁移：

- 流式工具 item 的真实 `item.id` 终态重协调，避免 `output_item.added/done` 与 `response.completed` 不一致；
- Chat/Anthropic fallback 的不完整流 fail-closed，并发送结构化失败终态；
- Images/Responses 相关 ID、工具和终态事件测试；
- 原生 Responses 生命周期 normalizer：缺失 `response.in_progress` 时补齐，并维持连续序号；
- 旧 T0 的请求幂等、Redis 跨实例历史续接、工具结果一致性校验，仍以最终代码和独立审计为准。

主要提交：`45c17b42b`、`23279d81f`、`612735d53`、`2f10670cd`、`0a0ad153f`。

### 4.2 模型目录和自动刷新

已保留：正式模型 ID、日期快照/旧别名/bare alias 过滤、下线模型不从 stale-only 刷新中重新出现、手工映射不被刷新覆盖。模型目录刷新默认按 24 小时有效期并在 Asia/Shanghai 04:00 调度；刷新失败保留上一份完整有效目录。

主要提交：`5bd7026ff`、`b5b625173`、`c1a2eb579`、`389b0500b`、`28d17f2c1`。

### 4.3 Smart Router 和健康隔离

已迁移：

- capability lane：Chat、Responses、Compact、图片生成分别记录；
- exact formal model key：`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna` 不互相污染；
- 并发 429 作为压力/限流，不直接降权 provider 健康；
- compact 持续失败使用独立隔离窗口；
- SQL health ledger、重启恢复和 04:00 校准调度；
- 图片真实请求单独记入 image lane，不污染文字/Responses lane；
- 默认 Smart Router 安全关闭，开启后才接入生产路由。

主要提交：`014b69f61`、`d3158818e`、`f2fb1dd99`、`0121027e8`、`f3372897d`。

每日校准当前只执行 Chat/Responses/Compact。图片生成会产生真实成本，服务适配器没有安全的零成本图片探针，因此图片健康只由真实图片请求记录；代码用 `textCalibrationProbes` 明确过滤图片探针，并有反例测试，不能把“没有自动图片探针”误报为图片能力已自动校准。

### 4.4 图片 Responses bridge 和安全护栏

已迁移：Responses native image generation 到 `/v1/images/*` 的显式 bridge，保留既有图片 APIKey/OAuth/SetupToken 路径；默认关闭，需已有账号能力和配置触发。图片失败、空响应和流终态不包装成成功。

### 4.5 运营安全

已迁移 operator/test guard：默认关闭，仅接受专用管理员测试 key；客户端伪造 `X-Forwarded-For`/`X-Real-IP` 不改变 TCP 来源判断。生产普通用户 key 不承担探测流量。

## 5. 尚未闭环的自有适配层

以下项目不能因为主干编译通过而标记完成：

| 项目 | 当前结论 | 放行条件 |
|---|---|---|
| Wokey/KIE 自定义媒体/视频 relay | 官方没有对应 profile/jobs/input/relay；现有旧提交相互依赖，尚未安全移植 | 独立 worker 完成依赖链分析；若移植，必须形成独立提交、负向测试、provider/profile 判定和专项审计 |
| Veyra portal/账务 overlay | 属于自有业务层，不是官方 Sub2API 协议核心；当前未移植 | 单独定义范围、SQL/权限/账务迁移和回滚方案，不与本次核心升级混入 |
| Smart Router SQL 真正重启恢复 | 代码和迁移已在；还需临时 PostgreSQL 上跑官方+自有 migration，写入、重启、恢复和重复 migration 证据 | 隔离数据库测试通过 |
| Compact 完整 HTTP 入口和真实账号矩阵 | 适配器/服务已有定向测试，完整 handler + 真实统一网关仍待测 | 完整 HTTP 夹具、真实 GLM/DeepSeek/Claude 及权限组证据 |
| Wokey/KIE/Grok、Kimi、MiniMax、Qwen、HY4 实际矩阵 | 不能把 `/v1/models` 出现当作可用；各 provider 能力仍需真实请求分级记录 | 文本、流式、工具、续接、错误和计费逐模型完成 |
| 独立最终审计 | 之前审计曾对 image lane、Wokey/KIE、Veyra、数据库证据提出 FAIL；修复后需按新 HEAD 重审 | 审计员只读 PASS，明确剩余范围 |

## 6. 具体执行方案

### 阶段 A：完成当前本地代码闭环

1. 等待 Wokey/KIE 独立 worker 返回；只审阅其提交，不直接复制工作树。
2. 对 Wokey/KIE 采用“完整依赖链才移植”的原则；无法形成闭环就保持不移植，并把它作为独立 provider 批次阻断，不用半成品冒充支持。
3. 核对 compact、Responses lifecycle、image bridge 和模型过滤的共享文件差异，补齐缺失的 HTTP 入口测试。
4. 在临时 PostgreSQL/Redis 上执行所有 migration，验证幂等、旧数据读取、重启恢复和 rollback 点。
5. 生成迁移文件清单、SHA256、测试日志和审计包。

### 阶段 B：本地验证

按以下顺序执行并记录退出码：

```powershell
go.cmd test -count=1 -timeout=900s ./internal/smartrouter/core
go.cmd test -count=1 -timeout=900s ./internal/pkg/apicompat ./internal/service ./internal/handler ./internal/server/routes ./internal/repository ./internal/config ./migrations
go.cmd test -count=1 -timeout=900s ./...
git diff --check
git merge-base --is-ancestor a60a29549 HEAD
```

官方基线已知存在环境相关失败：Windows PATH 缺少 `sh` 的 `backup_pg_dumper` 测试，以及 `TestAliyunCaptchaVerifier_TransportError` 的基线不稳定。迁移后必须保留基线对照，不能把这些失败静默算绿；能在 Linux/容器补跑则补跑，否则在报告中单独列为环境门禁。

### 阶段 C：受控真实测试

仅使用授权的 `unified-api-internal` 组和隔离测试账号；每个请求带独立测试标识，保存脱敏的 URL、状态、Content-Type、事件序列、request/response/call/item ID、usage 和账务流水。至少覆盖：

- GLM、DeepSeek、Claude：非流式、流式、工具、工具结果续接、错误工具结果、五轮对话、compact（账号支持时）；
- GLM 5.3、HY4/Preview、Kimi、MiniMax、Qwen：逐项记录支持/不支持/上游错误/网关错误；
- 断线恢复必须复用同一 `Idempotency-Key`，并核对工具 `call_id` 不重复执行、usage 不重复计费；
- `/v1/models` 只展示正式 ID，实际请求映射必须与展示一致。

失败必须分类为上游线路、网关转换、账号权限、客户端解析、超时或账务记录问题；不得用增加超时、自动切换协议或盲目重试掩盖协议错误。

### 阶段 D：最终发布

只有“本地全量通过或已有基线失败被独立复现并隔离、数据库恢复通过、独立审计 PASS、真实矩阵完成、usage/钱包流水对账一致、工作树干净”全部满足后，才允许创建发布提交并部署两台 VPS。部署前必须备份配置/数据库、使用唯一镜像 tag、健康检查、协议 smoke、回滚演练；本方案当前不包含部署动作。

## 7. 当前证据

已通过：

- `go.cmd test -count=1 -timeout=300s ./internal/smartrouter/core`：退出码 0；
- `go.cmd test -count=1 -timeout=300s ./internal/service -run TestSmartRouterCalibrationSkipsUnimplementedImageProbes`：退出码 0；
- Smart Router service、config、migrations、handler 编译/定向测试：此前退出码 0；
- `git merge-base --is-ancestor a60a29549 HEAD`：退出码 0；
- 最新代码提交已推送 GitHub 分支。

未通过/未完成：

- 迁移后全仓测试尚未在最终 HEAD 上形成完整日志；
- 独立最终审计尚未完成；
- Wokey/KIE worker 结果未纳入主分支；
- 临时 PostgreSQL/Redis 的最终重启恢复证据尚未生成；
- aiself、404token 尚未部署和真实矩阵尚未执行。

## 8. 验收状态

```yaml
WRITER_STATUS: MIGRATION_IMPLEMENTATION_PUSHED_LOCAL_VERIFICATION_IN_PROGRESS
OFFICIAL_BASELINE: upstream/main@a60a29549
CURRENT_VERSION: f3372897d4b476aaef9213639d887f6955d24f78
BACKUP_VERSION: 13430dd5209728a4a1b5523f92ed4de267d1c745
INDEPENDENT_AUDIT: PENDING_AFTER_LATEST_FIXES
FULL_LOCAL_TEST: PENDING_ON_FINAL_HEAD
LIVE_TEST: NOT_STARTED
DEPLOYMENT_STATUS: NOT_DEPLOYED
RELEASE_STATUS: NOT_ACCEPTED
```
