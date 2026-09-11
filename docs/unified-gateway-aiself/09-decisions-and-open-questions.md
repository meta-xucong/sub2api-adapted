# 推荐决策与待用户确认事项

本文件把会影响实现边界的决定集中列出。推荐值用于减少后续歧义；用户验收前仍不执行代码或配置变更。

## 1. 推荐冻结的设计决定

| ID | 决定 | 推荐值 | 原因 |
| --- | --- | --- | --- |
| D-01 | 统一入口隔离方式 | 独立 hostname 优先；不能用 hostname 时采用明确的 `/unified/v1` path | 最大限度保护旧 `/v1`，也方便独立 key 和回滚 |
| D-02 | 新入口身份 | 新 composite group + 新 API key/测试身份 | 防止旧 key 看到新目录或落入新账务 |
| D-03 | 同名模型多 provider | 允许多候选，但按实际 resolved route/account/profile 动态计费；usage 必须可见 provider | 满足“调用哪条链路扣哪条链路” |
| D-04 | 无价格 route | fail closed | 禁止统一默认价造成错账 |
| D-05 | 无能力/无 schedulable 账号 | 不进入模型目录和候选池 | 目录只展示可调用模型 |
| D-06 | 账号 `schedulable=false` | 保持排除，除非另有明确变更请求和独立审批 | 避免绕过现有运维意图 |
| D-07 | Grok 视频结算 | 只有成功完成且有可交付 URL 时结算；创建时锁定 snapshot；失败/取消/超时/过期用户费用为 0 | 严格遵循成功收费、失败不收费 |
| D-08 | provider 失败重试 | 只在新统一池显式候选内重试；禁止跨旧 group | 零干扰和账务可解释 |
| D-09 | 历史下架模型 | 不因历史残留自动进入统一目录；以当前能力快照为准 | 避免暴露不可用模型 |
| D-10 | 旧计费兼容 | 只在明确隔离的 legacy route 使用 group/channel fallback；新动态 route 必须 profile 完整 | 兼容旧链路且不污染新契约 |
| D-11 | 结算事实载体 | 每次转发前持久化 durable `route_price_snapshot_id`，usage/charge/media 全部引用它 | 防止进程重启、改价、换账号造成错账 |
| D-12 | 扣款与用量一致性 | 同事务或 durable outbox；禁止“扣款成功、usage 缺失”作为正常结果 | 保证可恢复对账 |
| D-13 | 媒体状态机 | `CREATING/PENDING/SETTLING/SETTLED`，并明确 FAILED/CANCELLED/EXPIRED/ORPHANED | 覆盖异步和进程故障 |
| D-14 | 目录闭包 | 只展示至少一个可执行、有价格的 route target；mapping 并集不直接作为目录 | 防止看得到但调不通 |
| D-15 | 子分组建模 | Plus/Pro/生图/KIE/直连等使用 Billing Lane，不为每个 lane 创建对外 API group | 一个统一链接保持不变，价格和账号池仍可分离 |
| D-16 | 倍率语义 | lane 用户倍率、客户倍率、provider 成本倍率分别存储；统一 group 动态倍率固定 1.0 | 防止重复乘倍率或把内部成本当用户售价 |
| D-17 | 订阅型账号 | 无逐请求 provider 账单时使用 amortized_subscription/fixed_request | 避免伪造 token 成本 |
| D-18 | 预占结算 | 长文本/图片/视频可 QUOTED→RESERVED→CAPTURED/RELEASED | 防止异步任务中余额不足或重复扣款 |
| D-19 | V1 价格来源 | 每个 Billing Lane 绑定 `pricing_source_group_id`，优先复用现有分组的模型价格、普通倍率、图片/视频独立倍率 | 减少重复配置，不改变旧 group |
| D-20 | 账号倍率语义 | `accounts.rate_multiplier` 只用于账号/上游成本统计；用户收费必须读取 source group/lane 规则 | 避免误以为账号倍率会自动影响用户扣款 |
| D-21 | 上游倍率来源模式 | 每个 lane 显式选择 `probe_preferred`、`manual_only` 或 `probe_only` | 让火山引擎/Ark 等非 ChatGPT 链路不依赖不存在的 probe |
| D-22 | 自动探测失败 | `probe_preferred` 按 account > pool > lane 使用已审核手动兜底；没有兜底则发送前 fail closed | 不把 unsupported/failed/stale 默认为 1.0 或免费 |
| D-23 | 非 token 计费 | 按次、图片、视频和 provider-specific 链路必须有手动单位价格或独立公式 | 防止把 ChatGPT token 倍率套错到其他 provider |
| D-24 | 手动规则版本 | 手动倍率、单位价格、fallback reason 和负责人进入独立 profile version；自动 probe 不覆盖手动兜底 | 支持审计、切换和历史 snapshot 不回算 |

## 2. 需要用户验收的事项

### Q-01：统一入口的正式 URL 形态

推荐：先使用独立 hostname 做 canary，正式化前再决定是否保留；若没有 DNS 条件，则使用 `/unified/v1`。验收时需要确认是否接受独立入口在一段时间内与现有 `/v1` 并存。

### Q-02：同名模型的价格可变性

推荐：同一 public model 选到不同 provider 时按实际 route 价格收费；对外 usage/审计中展示 provider。若业务要求客户端看到始终固定价格，则必须拆分 provider-qualified alias，牺牲透明的自动选路。

### Q-03：Grok/KIE/Subrouter/直连的来源和价格负责人

需要为每一条来源确认：provider identity、source channel、上游成本、用户价格、失败收费规则和价格版本。不能只给一个 “Grok 价格”。

### Q-04：异步任务失败时是否存在 provider 已接受成本

provider 在 `accepted/created` 阶段产生的不可退成本，只进入 provider/account cost 和人工对账；无论该成本是否存在，用户费用仍固定为 0。统一入口不允许以 `charge_trigger=accepted` 向用户扣款。

### Q-05：第一批正式候选

推荐先纳入已完成能力/价格验证的少量文本 route，再单独纳入 Grok 视频；不要一开始把所有历史模型和所有账号自动加入。

### Q-06：测试预算与停止条件

需要在进入 B2/B3 测试前确认每个 provider 的最大请求数、最大 token、最大视频秒数和自动停止阈值。当前本包不申请新预算，也不执行这些请求。

### Q-07：同名模型的动态价格展示

推荐统一入口的 `/v1/models` 只展示模型可用性，并标记动态计价；由 `/v1/pricing`、quote 接口或 usage/后台展示实际 lane/profile。若必须保证固定价格，则使用 `gpt-5.5-plus`、`gpt-5.5-pro` 等 qualified alias。

### Q-08：Plus/Pro 的成本基础

需要确认每个订阅型 lane 使用月度摊销、按次成本还是人工核定单位成本，并指定价格负责人。没有成本基础的 lane 不能启用。

### Q-09：来源分组映射

需要为每个统一入口 lane 指定现有价格来源分组，例如 Plus 文本、Pro 文本、生图。映射只读取价格和倍率，不把统一请求切换到旧分组，也不改变旧分组 API key 的行为。

### Q-10：非 ChatGPT provider 的人工价格规则

需要为火山引擎/Ark、Kimi、DeepSeek、Gemini、自定义中转等不能稳定返回兼容 billing schema 的链路，确认以下内容：

- 使用手动倍率还是直接使用手动单位价格；
- 计量单位是 token、请求、图片、视频秒数、字符还是 provider-specific usage；
- 手动规则由谁审批、何时复核、何时失效；
- probe 恢复后是否自动切回 probe（推荐是）；
- 没有有效手动规则时是否禁用该 lane（推荐是）。

推荐默认冻结为：`manual_only` + 明确的手动基础单价/倍率 + 版本化审核；不能用 ChatGPT 的 token 价格作为隐式兜底。

## 3. 当前不应被误读为已完成的事项

- `gpt-6-astra` 是否被某个具体上游账号支持：未在本阶段全面核验。
- DeepSeek、Kimi、Doubao/Ark、Gemini 的统一入口真实调用：未在本阶段实施。
- Grok 视频新统一 route 的真实创建/完成/重复回调测试：未在本阶段实施。
- route-level pricing profile schema/migration：只完成设计，未写 migration。
- probe/manual rate policy、手动单位价格和非 token 计量：已完成设计，尚未为真实 lane 填写并审核具体值。
- 新 endpoint、new group、new key、feature gate：均未创建。
