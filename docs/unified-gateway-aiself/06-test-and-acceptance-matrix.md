# 测试与验收矩阵

当前状态：`IMPLEMENTATION_REV2 / LOCAL_SIMULATION_PASS / PRODUCTION_GATE_DISABLED`。核心计价、隔离 route/account、owner-scoped durable snapshot contract、预占/结算/释放和本地 HTTP 模拟已通过；真实 provider、生产认证/余额适配、Grok 异步链路和线上灰度仍保持 `PENDING`，不因本地 fake upstream 通过而提前放行。

本轮已覆盖的本地夹具：probe 成功、stale/unsupported 手动兜底、probe-only fail-closed、manual-only 按次、basis 隔离、account > pool > lane、同名 gpt-5.5 Plus/Pro 切换、owner-scoped idempotency、final user price 防重复计价、失败零收费、版本化 digest、模型目录过滤、HTTP models/chat 以及 SQL route catalog/snapshot repository。证据位于 `internal/service/unified_route_pricing_test.go`、`internal/service/unified_gateway_test.go` 和 `internal/repository/unified_gateway_*_test.go`，运行命令为 `go test ./... -count=1` 和 `go vet ./internal/service ./internal/pkg/ctxkey`（Docker Go 1.27）。

## 1. 证据等级

- `B0`：静态设计/源码证据，不代表线上可用。
- `B1`：只读线上探测，无付费副作用。
- `B2`：独立测试 key 的最小文本请求。
- `B3`：受预算控制的图片/视频真实请求。
- `B4`：用户验收和对账签字。

## 2. 功能矩阵

| ID | 场景 | 预期结果 | 必需证据 | 当前状态 |
| --- | --- | --- | --- | --- |
| T-01 | 旧 key 请求旧 `/v1/models` | 与基线一致，新模型不泄漏 | before/after JSON diff | PENDING |
| T-02 | 新 key 请求统一 `/models` | 只返回 enabled、可调度、有价格的候选 | catalog snapshot | PENDING |
| T-03 | 未认证统一入口 | 401/既定认证错误，不泄露目录 | HTTP + log | PENDING |
| T-04 | OpenAI-compatible 非流式 | 正确 provider、model、usage、charge | B2 request + usage/billing | PENDING |
| T-05 | OpenAI-compatible stream | stream 完整、usage 只结算一次 | B2 stream + settlement | PENDING |
| T-06 | 工具调用/结构化输出 | 能力不满足时拒绝或按 adapter 规则转换 | request/response fixture | PENDING |
| T-07 | DeepSeek route | 不套用 OpenAI group price | route + profile + charge | PENDING |
| T-08 | Kimi OpenAI/Anthropic 形态 | transport 与 provider identity 分离 | adapter trace | PENDING |
| T-09 | Doubao/Ark route | provider/来源/模型和价格正确 | route + profile + charge | PENDING |
| T-10 | Gemini route | 原生协议或明确转换，usage 可解释 | adapter + billing | PENDING |
| T-11 | 同名模型多 provider | 选中哪个就按哪个 snapshot 计费 | 两组独立请求 | PENDING |
| T-12 | 没有有效价格 | 请求前 fail closed，不扣费 | error + no charge | PENDING |
| T-13 | 首选账号故障、候选账号成功 | 重试在新 group 候选池内，账务不串 | 两次 attempt + one policy result | PENDING |
| T-14 | 所有候选不可用 | 明确错误，不掉入旧 group | resolver trace | PENDING |
| T-15 | Grok 创建成功完成成功 | 使用创建 snapshot，结算一次 | B3 media + pending/claim | PENDING |
| T-16 | Grok 重复轮询/回调 | 只产生一次 settlement | duplicate fixture | PENDING |
| T-17 | Grok 创建成功完成失败 | 用户费用为 0、预占释放；provider 内部成本单独记录 | media state + billing | PENDING |
| T-18 | Grok 创建后价格变更 | 仍使用旧 snapshot | versioned profile test | PENDING |
| T-19 | Grok `schedulable=false` 账号 | 永不被新池隐式选中 | candidate dump + trace | PENDING |
| T-20 | 视频结果 URL 缺失/失效 | 不误判成功、不重复扣费 | media error trace | PENDING |
| T-21 | status/content 请求不带 model | 仍按持久 task binding 找回原账号，不重新随机选路 | endpoint trace | PENDING |
| T-22 | snapshot 落库失败但上游已接受 | 进入可恢复异常态，不重复创建未知任务 | fault injection + reconciliation | PENDING |
| T-23 | Redis pending 丢失/进程重启 | durable binding 可恢复，结算不重复 | restart/recovery fixture | PENDING |
| T-24 | 同一 `gpt-5.5` 命中 Plus lane | 使用 Plus profile/multiplier，snapshot 中 lane 明确 | B2 + billing snapshot | PENDING |
| T-25 | 同一 `gpt-5.5` 命中 Pro lane | 使用 Pro profile/multiplier，不沿用 Plus 价格 | B2 + billing snapshot | PENDING |
| T-26 | Plus 失败、Pro 成功 | 最终费用按 Pro snapshot；两次 attempt 均可审计 | failover fixture | PENDING |
| T-27 | `gpt-image-2` 生图 | 使用 image/resolution profile，不进入 token 计费 | B3 image + billing | PENDING |
| T-28 | 订阅账号无 provider token 账单 | 使用 approved amortized/fixed-request profile，不伪造 token 价 | profile review + charge | PENDING |
| T-29 | lane 倍率修改 | 新请求使用新 profile，历史 snapshot 不回算 | versioned profile test | PENDING |
| T-30 | 同一账号重复加入同模型多个 lane | 配置校验阻止歧义，或必须有显式 scope | catalog validation | PENDING |
| T-31 | lane 引用 Plus 来源分组 | 使用来源分组普通模型价格和 `rate_multiplier`，不使用 unified group 默认倍率 | source-group billing fixture | PENDING |
| T-32 | lane 引用 Pro 来源分组 | 同名模型切换到 Pro 时使用 Pro 来源分组倍率 | source-group billing fixture | PENDING |
| T-33 | lane 引用生图来源分组 | 使用来源分组 image price/independent multiplier，按次计费 | image source-group fixture | PENDING |
| T-34 | 修改 `account.rate_multiplier` | 只影响 provider/account cost，不改变用户 charge | dual-ledger test | PENDING |
| T-35 | 任意文本/图片/视频失败 | 用户费用为 0；预占释放；provider 内部成本可单独记录 | failure matrix + ledger diff | PENDING |
| T-36 | probe 成功且未过期 | snapshot 使用 `probe_declared`，记录 probe 时间和倍率 | probe fixture + billing snapshot | PENDING |
| T-37 | probe 返回 unsupported | `probe_preferred` 使用已审核的手动兜底；无兜底则发送前拒绝 | unsupported fixture + no-charge trace | PENDING |
| T-38 | probe 返回 failed 或 stale | 不默认使用 1.0、不沿用其他 provider；按 fallback/拒绝规则处理 | failure/stale fixture | PENDING |
| T-39 | 非 ChatGPT/火山引擎/Ark `manual_only` | 按手动倍率、单位价格或 provider-specific 规则计费 | manual profile + charge | PENDING |
| T-40 | 同一 lane 账号手动倍率不同 | 选中实际账号后使用 account override，不使用 pool 错价 | account override fixture | PENDING |
| T-41 | token probe 遇到 image/video/per-request | 无匹配手动单位价格时 fail closed，不套 token 倍率 | basis mismatch fixture | PENDING |
| T-42 | 自动探测恢复 | 新请求切回 probe；手动兜底值保留；历史 snapshot 不变 | source transition test | PENDING |
| T-43 | 基础价格为最终销售价 | 不再叠加 upstream/user multiplier，费用可复算 | no-double-count fixture | PENDING |

## 3. 计费与对账矩阵

| ID | 验收点 | 通过标准 |
| --- | --- | --- |
| B-01 | route/account/profile 关联完整 | 每个成功 usage 都可回溯三者 |
| B-02 | 用户 charge 与 account cost 分离 | 报表和字段不混淆 |
| B-03 | token 输入/输出/缓存 | 按实际 provider profile 和 usage 计量 |
| B-04 | per-request/image/video | 单位、分辨率、时长、触发条件可解释 |
| B-05 | 重试 | attempt 记录完整；失败尝试用户费用为 0，只有最终成功交付尝试收费 |
| B-06 | 幂等 | 相同 settlement key 不产生第二笔扣款 |
| B-07 | profile 版本 | 历史账务不因新价回算 |
| B-08 | 价格缺失 | fail closed，不使用统一默认价 |
| B-09 | 余额不足 | 新入口拒绝且不影响旧 key |
| B-10 | 近 30 天对账 | 旧数据无被重算/改写 |
| B-11 | durable snapshot | route/account/profile/unit price 可从 snapshot 复原 |
| B-12 | 三类账务事实原子性 | user charge、usage/audit、provider/account cost、settlement 同边界恢复；不出现部分落库，失败可由 outbox 对账恢复 |
| B-13 | 倍率不重复计算 | source group/lane/user/provider 三类倍率组件和最终单价可复算，unified group 倍率不隐式叠加 |
| B-14 | 预占结算 | QUOTED/RESERVED/CAPTURED/RELEASED 状态和余额变化可对账 |
| B-15 | 动态价格展示 | `/models` 不伪造固定价；quote/usage/后台能说明实际 lane/profile |
| B-16 | 倍率来源可追溯 | 每笔成功 charge 能区分 probe、manual fallback、manual only |
| B-17 | 手动规则版本 | 手动倍率/单位价格变更只影响新 snapshot，历史不回算 |
| B-18 | 非 token 单位 | 按次、图片、视频、provider-specific 计量不依赖 ChatGPT token 倍率 |
| B-19 | fallback 优先级 | account override > pool override > lane default，且 probe freshness 规则可复现 |

## 4. 非干扰矩阵

| ID | 检查 | 通过标准 |
| --- | --- | --- |
| I-01 | 旧 API key | 认证、模型、响应和计费与 baseline 一致 |
| I-02 | 旧 group | 配置、账号、路由和价格无 diff |
| I-03 | 旧账号 | schedulable/concurrency/priority 无意外改变 |
| I-04 | 旧 endpoint | `/v1`、health、setup、前端登录无回归 |
| I-05 | 资源隔离 | 新流量不会耗尽旧链路连接池/并发/Redis namespace |
| I-06 | 失败路径 | 新入口失败不会跨 group fallback |
| I-07 | 回滚 | 关闭新 gate 后旧流量恢复，新增对象可单独禁用 |
| I-08 | 三重隔离 | gate、统一 group、显式 candidate 任一缺失都不能进入新链路 |

## 5. 安全与数据矩阵

| ID | 检查 | 通过标准 |
| --- | --- | --- |
| S-01 | secret scan | 文档、日志、快照无完整 key/口令/credential |
| S-02 | 跨租户访问 | 新 key 不能看到别的 group/private model |
| S-03 | 管理权限 | route/profile CRUD 有授权和审计 |
| S-04 | media URL | 日志脱敏、访问范围和 TTL 明确 |
| S-05 | migration | forward/rollback 副本演练通过 |
| S-06 | 审计字段 | route/account/profile/version 缺失会报警 |

## 6. 放行门槛

必须达到：所有 T-01、T-02、T-04、T-11、T-12、T-14、T-15、T-16、T-17、T-24、T-25、T-26、T-27、T-31、T-32、T-33、T-34、T-35、T-36、T-37、T-38、T-39、T-40、T-41、T-42、T-43、B-01、B-06、B-08、B-11、B-12、B-13、B-14、B-16、B-17、B-18、B-19、I-01、I-02、I-04、I-06、S-01 通过；其余项目有明确风险接受人和后续期限。未达到 B3 前，不得宣称 Grok 视频统一链路可用。
