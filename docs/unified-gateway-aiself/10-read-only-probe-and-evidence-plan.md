# 只读探测与证据复现计划

本文件把后续实施前的探测分成可重复的只读步骤。它不是部署脚本，也不应直接复制成带有真实凭据的命令。任何需要写数据库、重启容器、修改配置、发送付费媒体请求的动作都不属于本文件。

## 1. HTTP 只读探测

| 类别 | 目标 | 证据 |
| --- | --- | --- |
| 健康 | `/health` | status/body/time |
| 初始化 | `/setup/status` | setup state |
| 前端 | `/`、登录回跳相关静态资源 | status/content-type |
| 认证边界 | 无 key 的 `/v1/models` | 401/错误体脱敏 |
| 目录 | 独立测试 key 的 `/v1/models` | model ids、响应时间、request ref |
| 文本 | 仅在预算批准后使用最小 token | request/route/usage/billing report |
| 媒体 | 仅在 B3 批准后 | 独立 media report，不与文本冒烟合并 |

每次探测记录 UTC/Asia-Shanghai 时间、目标环境、HTTP status、响应摘要和请求引用；不保存 Authorization header。

## 2. 运行时只读探测

- 容器名称、状态、健康检查、image digest、启动时间。
- compose/config 版本摘要、环境变量名清单（不导出值）、迁移版本。
- 应用日志最近错误类别和 request id（脱敏）。
- PostgreSQL 只读：表存在性、列名/类型、行数、状态分布、索引/约束摘要。
- Redis 只读：统一入口未来使用的 namespace 是否冲突；不读取凭据值，不删除 key。
- 反向代理只读：现有 host/path 到 upstream 的映射和缓存/SPA fallback 规则。

## 3. 数据库只读查询类别

后续执行时使用只读事务或只读连接，查询结果先脱敏再归档：

1. `groups`：id、name 的逻辑标签、platform、status、model list 摘要、routing 开关。
2. `accounts`：id、provider/type、status、schedulable、priority、rate multiplier；禁止导出 credentials/extra 原值。
3. `account_groups`：group/账号关系、priority。
4. `composite_model_routes`：route、public/upstream model、endpoint、priority、enabled。
5. `usage_logs`：近 30 天按 group/model/request type/provider/endpoint 的聚合，不导出 prompt、key 或完整响应。
6. `billing_usage_entries`：按 usage log/用户/状态聚合，核对幂等。
7. `channel_*`：行数、价格字段覆盖率、有效期和账号统计规则。
8. Grok pending/claimed 记录：数量、状态、TTL、settlement disposition；不输出带签名的 URL。

9. 统一入口设计域：Billing Lane、lane/account membership、route target、pricing profile、倍率组件和有效版本；检查同一账号在同一模型/endpoint 下是否出现多个 enabled lane。
10. 上游倍率来源：对支持的账号只读记录 `/v1/sub2api/billing` 的 status、schema、`resolved/effective` 值、`received_at` 和 `fresh_until`；对 unsupported/failed/stale 账号核对是否存在已审核的手动倍率或手动单位价格。
11. 非 ChatGPT provider 计费规则：核对火山引擎/Ark 等 lane 的 `manual_only` 配置、计量单位、人工价格证据、负责人和有效版本；不得从 ChatGPT group 默认价格推断。

## 4. 源码只读核对

- `/v1/models` 目录聚合与 key/group scope。
- composite route match/priority/endpoint/target platform。
- account model mapping、schedulable 和选择器。
- provider adapter 的输入、输出和 usage 归一化。
- usage billing 的用户 charge 与 account cost 分离。
- upstream rate source 的 probe/manual/fallback 解析、freshness 和 snapshot 记录。
- 非 token provider 的手动单位价格、按次/图片/视频/provider-specific 计量规则。
- Grok media pending、account binding、claim/release、TTL 和成功判断。
- migration 的唯一索引、外键、默认值和 down/forward 兼容性。

源码观察必须记录 commit；不能把工作树的未提交修改当成发布基线。

## 5. 禁止的探测动作

- 不能用 `UPDATE`、`DELETE`、`INSERT`、`ALTER`、`TRUNCATE` 或管理 API 写配置。
- 不能重启/停止/拉取/替换/升级容器。
- 不能修改 compose、反代、DNS、证书、模型目录或 key 绑定。
- 没有预算和用户验收前，不能发起真实图片/视频任务。
- 不能为了“验证是否能用”重新尝试已知下架模型并造成费用。
- 不能把 `/v1/sub2api/billing` 的 unsupported/failed 响应改写成倍率 1.0，也不能为探测而向非兼容 provider 发起猜测性付费请求。

## 6. 证据完整性

每个事实必须附：采集时间、环境、来源（HTTP/DB/source）、脱敏方式和限制。若查询失败，记录为 `UNKNOWN`，不能用历史结果填补为 `PASS`。

对手动价格事实还必须附：计量单位、基础价格语义（provider base/final user price）、倍率或单位价格、负责人、审批引用、有效期和复核时间。没有这些字段的手动规则只能标记为 `PENDING`，不能作为 enabled lane 的价格证据。
