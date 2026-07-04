# Sub2API Smart Router 开发文档

## 目标

Sub2API Smart Router 是一个模块化、可插拔、可复用的智能调度补丁。它的目标不是服务某一个供应商，而是能随时接到任意一个 Sub2API 部署上，让多条上游线路在成本、健康度、并发、失败恢复之间取得稳定平衡。

它必须满足这些产品目标：

- 便宜线路优先，但不能被 100% 高频打爆。
- 多条线路要从一开始均摊一部分真实流量，而不是只在故障时冷启动。
- 抖动线路临时冷却，恢复后自动探测并灰度回流，不能永久关闭。
- 同源线路要成组限流，避免 plus、pro、fallback 实际来自同一源头时被连续打穿。
- 单个请求要限制尝试次数，不能因为有 5 条以上备选线路就把一次失败放大成多次上游压力。
- 能兼容单层 Sub2API、上下游双层 Sub2API，也能发展成独立旁路网关。
- 默认关闭或无策略时，不能改变现有 Sub2API 行为。

## 非目标

Smart Router 第一阶段不做这些事：

- 不替代 Sub2API 的账号、分组、模型映射、计费、API key 鉴权体系。
- 不强制改造所有平台，MVP 先接 OpenAI-compatible 路由，再逐步扩展 Gemini、Anthropic 等。
- 不把供应商名称写死进代码，例如 aicodexvip、aiai、404token 都只能出现在示例策略里。
- 不把所有失败都包装成成功；真实无可用线路时仍应返回清晰错误。
- 不默认把生产配置写死在源码里。

## 总体架构

Smart Router 分成核心引擎和 Sub2API 适配层。

```text
client
  -> Sub2API handler
    -> Sub2API account/group/model filtering
      -> Smart Router adapter
        -> Smart Router core
          -> lane selection / cooldown / source-group guard / scoring
        -> Sub2API concurrency acquire
      -> upstream request
      -> Smart Router result report
```

核心原则：

- `smart_router/core` 不直接依赖 Ent、Gin、Sub2API `Account` 结构或数据库实现。
- `smart_router/sub2api` 负责把 Sub2API 账号、分组、模型映射、临时不可调度、并发槽位转换为通用 `LaneSnapshot`。
- 旁路网关模式复用同一个 core，只换 adapter。

推荐目录：

```text
backend/internal/smartrouter/
  core/
    router.go
    policy.go
    scoring.go
    classifier.go
    cooldown.go
    recovery.go
    source_group.go
    metrics.go
  sub2api/
    account_adapter.go
    policy_loader.go
    runtime_store.go
    probe_adapter.go
    scheduler_hook.go
  testkit/
    fixtures.go
    fake_runtime_store.go
    deterministic_rng.go
```

## 核心接口

核心引擎只认通用请求和线路。

```go
type Capability string

const (
    CapabilityChat            Capability = "chat"
    CapabilityResponses       Capability = "responses"
    CapabilityImageGeneration Capability = "image_generation"
    CapabilityImageEdit       Capability = "image_edit"
    CapabilityEmbedding       Capability = "embedding"
)

type RouteRequest struct {
    RequestID        string
    GroupID          string
    Model            string
    Capability       Capability
    StickyLaneID     string
    PreviousResponse string
    ExcludedLaneIDs  map[string]struct{}
    AttemptNumber    int
}

type LaneSnapshot struct {
    LaneID              string
    AccountID           int64
    Name                string
    SourceGroup         string
    Capabilities        map[Capability]bool
    ModelPatterns       []string
    Priority            int
    CostMultiplier      float64
    BaseWeight          float64
    MaxConcurrency      int
    CurrentConcurrency  int
    CurrentWaiting      int
    HealthScore         float64
    ErrorRateEWMA       float64
    LatencyEWMAms       float64
    CooldownUntilUnix   int64
    RecoveryStage       string
    Metadata            map[string]string
}

type RouteDecision struct {
    SelectedLaneID string
    AccountID      int64
    Reason         string
    CandidateCount int
    SourceGroup    string
    AttemptBudget  int
}
```

结果回报也必须抽象化：

```go
type RouteResult struct {
    LaneID         string
    AccountID      int64
    SourceGroup    string
    Capability     Capability
    Model          string
    Success        bool
    StatusCode     int
    ErrorClass     FailureClass
    FirstTokenMs   *int
    TotalLatencyMs int64
    ErrorSummary   string
}
```

## Sub2API 适配原则

MVP 优先复用已有字段，减少迁移成本：

| Smart Router 字段 | Sub2API 来源 |
| --- | --- |
| `AccountID` | account id |
| `Name` | account name |
| `Priority` | account priority |
| `MaxConcurrency` | account concurrency |
| `CostMultiplier` | account rate multiplier，缺省 1 |
| `BaseWeight` | account load factor 或 `extra.smart_router.base_weight` |
| `ModelPatterns` | account model_mapping |
| `CooldownUntilUnix` | temp unschedulable / model rate limit / Smart Router runtime state |
| `Capabilities` | OpenAI endpoint capability、image capability、策略配置 |
| `SourceGroup` | `extra.smart_router.source_group`，缺省为 account id |

建议在账号 `extra` 中增加可选块，不要求所有用户迁移数据库：

```json
{
  "smart_router": {
    "enabled": true,
    "lane_id": "aicodexvip-image",
    "source_group": "aicodexvip",
    "capabilities": ["image_generation", "image_edit"],
    "base_weight": 1.2,
    "cost_multiplier": 0.45,
    "max_concurrency": 1,
    "source_group_max_concurrency": 2,
    "attempt_budget": 2,
    "recovery_profile": "cheap_flappy_image"
  }
}
```

没有 `extra.smart_router` 时：

- Smart Router 如果关闭，完全走现有 scheduler。
- Smart Router 如果开启，则从现有 account 字段生成默认 lane。
- `source_group` 默认使用 `account:<id>`，避免无配置账号被错误合并。

## 配置入口

全局配置建议挂在 `gateway.smart_router`：

```yaml
gateway:
  smart_router:
    enabled: false
    mode: sub2api_core
    selection:
      top_k: 5
      max_attempts_image: 2
      max_attempts_chat: 3
      keep_fallback_lanes_warm: true
    scoring:
      priority_weight: 0.8
      cost_weight: 1.0
      health_weight: 1.2
      load_weight: 1.0
      queue_weight: 0.6
      latency_weight: 0.4
    cooldown:
      default_transient_seconds: 12
      default_forbidden_seconds: 600
      default_rate_limit_seconds: 60
    recovery:
      probe_enabled: true
      first_weight_ratio: 0.05
      second_weight_ratio: 0.25
      success_to_full: 3
      quiet_window_seconds: 900
```

也可以增加 DB setting，例如 `smart_router_policy_json`，让商业用户不改容器配置就能灰度策略。优先级建议：

```text
account extra override
-> DB setting policy
-> config.yaml policy
-> built-in safe defaults
```

## 调度流程

每次请求按以下顺序执行：

1. Sub2API 先完成原有用户鉴权、分组、模型映射和基础账号过滤。
2. adapter 把候选账号转换成 `LaneSnapshot`。
3. core 按 model、capability、active/schedulable、cooldown、并发、source group 过滤。
4. core 计算线路分数。
5. core 从 top-K 候选中加权选择，而不是硬选第一名。
6. adapter 获取 Sub2API 账号并发槽位。
7. handler 转发请求。
8. 请求成功或失败后回报 Smart Router。
9. Smart Router 更新健康分、冷却、恢复阶段和 source group 状态。

推荐分数：

```text
score =
  base_weight
  * health_score
  * cost_factor
  * availability_factor
  * recovery_factor
  / load_factor
  / queue_factor
```

其中：

- `cost_factor` 让便宜线路更容易被选中，但必须设置上下限，避免低价线路垄断。
- `health_score` 来自成功率、错误率 EWMA、近期冷却次数。
- `recovery_factor` 用于恢复后的灰度回流。
- `load_factor` 来自当前并发和等待队列。

## Source Group 规则

同源线路是 Smart Router 的关键商业能力。

规则：

- 同一个供应商、同一个上游 Sub2API、同一批账号池出来的 plus/pro/fallback 应归为同一个 `source_group`。
- 单个 source group 可以设置总并发，例如 `source_group_max_concurrency=2`。
- 一个请求中某 source group 出现源头级失败后，默认不继续尝试同组其它 lane。
- source group 失败率升高时，可以对整个组降权或冷却。

这能避免这种错误模式：

```text
7646881-plus failed
-> 7646881-pro failed
-> 7646881-fallback failed
```

如果三条线路底层来自同一源头，这不是高可用，而是三倍打击。

## 失败分类

失败分类必须独立成模块，方便不同客户按供应商调整。

| FailureClass | 例子 | 默认动作 |
| --- | --- | --- |
| `client_error` | 400、请求参数错误 | 不惩罚 lane |
| `capability_error` | 模型不支持、image tool 不存在 | capability 冷却 |
| `auth_forbidden` | 确定性 401、账号失效 | 强冷却或交给原有账号状态逻辑 |
| `transient_forbidden` | 图像 403 抖动 | capability 冷却 |
| `rate_limited` | 429 | 冷却 + 降并发 |
| `upstream_5xx` | 500、502、503 | 短冷却 |
| `timeout` | 408、504、首 token 超时 | 降健康分，可短冷却 |
| `cancelled` | 客户端取消 | 默认不惩罚供应商 |

现有 `gateway.image_edit_transient_cooldown_seconds` 补丁可以映射为：

```text
capability = image_edit
status_codes = 403,408,500,502,503,504
action = short_cooldown
default_duration = 12s
scope = lane/account
```

后续要把它泛化成策略，而不是继续新增大量专用 helper。

## 冷却与恢复

冷却必须临时、可解释、可恢复：

```text
fail
-> classify
-> cooldown lane/capability/source_group
-> skip in real traffic
-> probe
-> recovery ramp
-> normal weight
```

推荐恢复阶段：

| Stage | 行为 |
| --- | --- |
| `cooling` | 真实请求跳过 |
| `probe_due` | 冷却到期，等待探针 |
| `warming_5` | 探针成功，恢复目标权重的 5% |
| `warming_25` | 连续成功，恢复目标权重的 25% |
| `normal` | 恢复目标权重 |

探针优先复用现有 scheduled tests：

- `gpt-image-2` 文生图探针。
- `gpt-image-2#edits` 图生图探针。
- chat/responses 简短流式探针。

## 请求尝试预算

尝试预算是减少高频多次调度的第二道保护。

默认建议：

- image generation：最多 2 条 lane。
- image edit：最多 2 条 lane。
- chat/responses：最多 3 条 lane。
- 同一个 source group：默认一次请求最多尝试 1 条 lane。

如果所有候选都在冷却或预算用尽，返回明确的 `No available compatible accounts`，并带上安全摘要日志，不输出 token、key、cookie 或账号密文。

## 与现有 Sub2API scheduler 的兼容性

现有 OpenAI scheduler 已经有这些能力：

- sticky previous response。
- sticky session。
- top-K 加权选择。
- error rate / TTFT EWMA。
- account concurrency。
- temp unschedulable。
- image capability 与 endpoint capability。

Smart Router 不应该重写这些能力，而应该做成更通用的增强层：

- sticky previous response 优先级最高，不能被 Smart Router 随意打散。
- sticky session 可以在健康恶化或并发满时逃逸，但要保留现有 sticky preserve 逻辑。
- load-balanced 层接入 Smart Router 的 lane scoring 和 source group guard。
- temp unschedulable 继续作为账号级持久冷却底座。
- model rate limit 继续作为模型或能力级冷却底座。
- image edit 短冷却先保留，后续迁移到通用 failure policy。

## 双层 Sub2API 兼容性

如果部署结构是：

```text
client
  -> downstream Sub2API
    -> upstream Sub2API
      -> provider lanes
```

两层都可以装 Smart Router，且互不冲突：

- 下游把上游 Sub2API endpoint 当作 lane，负责用户体验、快速跳过已知坏链路、避免重复撞 503。
- 上游把真实供应商账号当作 lane，负责供应商级限流、冷却、探针和恢复。
- 两层 runtime state 独立，不共享数据库。
- 下游不需要知道上游内部账号 id，上游也不需要知道下游用户 id。

## 旁路网关模式

商业化时可以提供 sidecar gateway：

```text
client
  -> Smart Router Gateway
    -> Sub2API A
    -> Sub2API B
    -> Sub2API C
```

sidecar 优点：

- 用户不用改 Sub2API 源码。
- 可以接多个已有 Sub2API。
- 容易作为独立商业产品安装。

sidecar 限制：

- 看不到 Sub2API 内部账号级健康，只能 endpoint 级调度。
- 不能直接复用 Sub2API 的 account concurrency，需要自己维护 endpoint 并发。
- 精细能力探针依赖外部 API key 和 endpoint 配置。

因此 MVP 应先做 core patch mode，再把 core 引擎抽出来给 sidecar 复用。

## 数据存储

第一阶段尽量不加表：

- 静态策略：config.yaml、DB setting、account extra。
- 短期 runtime：内存 + Redis/cache。
- 持久冷却：复用 temp unschedulable 与 model rate limit。
- 操作审计：复用 usage/error log，增加 Smart Router 字段。

后续商业版可选加表：

```text
smart_router_lane_state
smart_router_source_group_state
smart_router_policy_revision
smart_router_probe_result
```

但加表不是 MVP 前置条件。

## 日志与指标

日志必须可诊断，但不能泄露密钥。

建议新增事件：

- `smart_router.select`
- `smart_router.skip.cooldown`
- `smart_router.skip.source_group`
- `smart_router.fail.classified`
- `smart_router.cooldown.set`
- `smart_router.recovery.probe_due`
- `smart_router.recovery.ramp`
- `smart_router.result`

安全字段：

- request id
- group id
- account id
- lane id
- source group
- model
- capability
- status code
- failure class
- latency
- short error summary

禁止输出：

- token
- key
- password
- cookie
- refresh token
- raw credential JSON
- 完整上游响应体中的敏感字段

## MVP 开发阶段

### Phase 1: Policy 与 core 引擎

- 增加 core 数据结构。
- 增加 policy parser 和默认策略。
- 增加 failure classifier。
- 增加 scoring 与 weighted top-K。
- 增加 source group filter。
- 增加 attempt budget。

验收：

- 无 Sub2API 依赖的 core 单测通过。
- 同一输入使用 deterministic RNG 可复现。

### Phase 2: Sub2API adapter

- 从 `Account` 生成 `LaneSnapshot`。
- 读取 `extra.smart_router`。
- 接入现有 concurrency、temp unschedulable、model rate limit。
- 在 OpenAI load-balance 层接入 Smart Router。
- 默认关闭时行为不变。

验收：

- 现有 scheduler 测试不回退。
- 无策略账号仍按原 scheduler 行为可用。
- 开启策略后 source group 与 cost/health 生效。

### Phase 3: Image transient cooldown 泛化

- 把现有 image edit 短冷却迁移为通用 failure policy。
- 保留旧配置项作为兼容 alias。
- 支持 `image_generation`、`image_edit` 分别冷却。
- 支持短冷却和长熔断同时存在。

验收：

- 旧 `gateway.image_edit_transient_cooldown_seconds` 测试继续通过。
- 新 policy 测试覆盖 403、429、500、504。

### Phase 4: Probe 与恢复回流

- 复用 scheduled tests 跑探针。
- 支持 `gpt-image-2#edits`。
- 探针成功后进入 5%/25%/100% 回流。
- 真实请求成功也能推进恢复阶段。

验收：

- 冷却中真实请求跳过。
- 探针成功后只恢复小权重。
- 连续成功后恢复目标权重。

### Phase 5: UI 与运维文档

- 后台展示 lane、source group、cooldown、recovery stage。
- 支持账号 extra 的 Smart Router 配置编辑或 JSON 导入。
- 增加示例策略。
- 增加部署、回滚、审计文档。

验收：

- 非技术用户能把第 5 条线路接入策略。
- 回滚关闭 `gateway.smart_router.enabled` 后恢复原行为。

## 示例策略

### 低价图片优先但防打爆

```json
{
  "lanes": [
    {
      "match": {"account_name": "aicodexvip"},
      "source_group": "aicodexvip",
      "capabilities": ["image_generation", "image_edit"],
      "cost_multiplier": 0.45,
      "base_weight": 1.4,
      "max_concurrency": 1,
      "source_group_max_concurrency": 1,
      "recovery_profile": "cheap_flappy_image"
    },
    {
      "match": {"account_name": "aiai"},
      "source_group": "aiai",
      "capabilities": ["image_generation", "image_edit"],
      "cost_multiplier": 1.0,
      "base_weight": 1.0,
      "max_concurrency": 3,
      "source_group_max_concurrency": 3,
      "recovery_profile": "stable_fallback"
    }
  ]
}
```

### 三条同源 chat 线路

```json
{
  "lanes": [
    {"match": {"account_name": "7646881-plus"}, "source_group": "7646881", "cost_multiplier": 0.6, "max_concurrency": 1},
    {"match": {"account_name": "7646881-pro"}, "source_group": "7646881", "cost_multiplier": 0.9, "max_concurrency": 1},
    {"match": {"account_name": "7646881-fallback"}, "source_group": "7646881", "cost_multiplier": 1.2, "max_concurrency": 1}
  ],
  "source_groups": {
    "7646881": {
      "max_concurrency": 1,
      "same_request_max_attempts": 1,
      "rate_limit_cooldown_seconds": 300
    }
  }
}
```

## 测试计划

必须新增这些测试：

- core policy 默认值和非法值校验。
- capability 匹配。
- model pattern 匹配。
- cost multiplier 不允许把单一 lane 永久垄断。
- source group 总并发过滤。
- 同请求 source group 失败后跳过同组其它 lane。
- 403 image edit 触发短冷却。
- 429 触发 rate limit 冷却。
- 500/502/503 触发 transient cooldown。
- 504 降低 health score。
- cancelled 不惩罚 lane。
- cooling 阶段真实请求跳过。
- probe 成功后进入 warming_5。
- 连续成功后进入 normal。
- Smart Router disabled 时现有 scheduler 结果不变。
- 无 `extra.smart_router` 的旧账号可继续调度。
- 双层 Sub2API 场景：下游只看到上游 endpoint 失败，不依赖上游内部 account id。

建议命令：

```powershell
D:\AI\.tools\go\bin\go.exe test -tags unit ./internal/smartrouter/... -count=1
D:\AI\.tools\go\bin\go.exe test -tags unit ./internal/service -run 'Scheduler|TempUnsched|Image' -count=1
D:\AI\.tools\go\bin\go.exe test -tags unit ./internal/handler -run 'Images|Responses|ChatCompletions' -count=1
git diff --check
```

## 兼容性审计清单

开发完成前必须逐项确认：

- Smart Router 默认关闭，不改变现有生产行为。
- 开启但无策略时，旧账号仍可用。
- 账号 `active=false` 或 `schedulable=false` 仍不可被调度。
- 现有 `temp_unschedulable_until` 生效。
- 现有 `rate_limit_reset_at` 生效。
- 现有 model mapping 生效。
- 现有 group 绑定生效。
- 现有 sticky previous response 不被破坏。
- 现有 sticky session 不被无故覆盖到昂贵 fallback。
- image generation 和 image edit 可以分别冷却。
- 403/429/5xx 不会永久关闭账号，除非原有确定性账号状态逻辑明确要求。
- 同源线路不会在同一请求里被连续打穿。
- 单请求尝试次数有上限。
- 日志不输出密钥、token、cookie、账号密文。
- 两层 Sub2API 同时安装时 runtime state 独立。
- sidecar 模式不要求修改用户已有 Sub2API 数据库。
- 关闭 `gateway.smart_router.enabled` 可以回滚到旧 scheduler。

## 用户意图覆盖审计

| 用户目标 | 文档落点 |
| --- | --- |
| 减少高频多次调度 | 请求尝试预算、source group guard、冷却 |
| 请求均摊到各线路 | weighted top-K、cost/health/load scoring |
| 未来至少 5 条线路 | lane 抽象、policy 示例、非硬编码供应商 |
| 便宜线路优先 | cost multiplier 与 base weight |
| 便宜线路不稳定时自动跳过 | failure classifier、cooldown |
| 稳定后自动回来 | probe、recovery ramp |
| 可接任意 Sub2API | core/adapter 分离、默认无侵入、sidecar 模式 |
| 可商业化 | 模块边界、配置入口、UI/运维阶段、示例策略 |
| 兼容上下游双层 Sub2API | 双层兼容性章节 |
| 融合现有图生图短冷却补丁 | Phase 3 与 failure policy 映射 |

## 推荐落地顺序

先不要直接做 sidecar。最稳的开发路径是：

1. 在当前 custom Sub2API 中完成 core patch mode。
2. 用 404token 和 aiself.vip 两层真实链路验证。
3. 把 core 包与 Sub2API adapter 边界打稳。
4. 抽出 sidecar gateway，只复用 core。
5. 最后做商业化安装脚本和 UI。

这样既能解决当前生产问题，又不会把商业版设计绑死在你自己的结构上。
