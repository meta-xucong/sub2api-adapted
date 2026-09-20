# 内部统一 API 最小化开发方案

> `TASK_ID=UNIFIED-MINIMAL-20260919`  
> `CONTRACT_REV=v1.3.1-route-guard`  
> `STATUS=DOCS_SCOPE_FROZEN / EXISTING_CANDIDATE_PRESENT / NEXT_IMPLEMENTATION_NOT_STARTED / PRODUCTION_GATE_DISABLED`  
> `COMPLEXITY_GATE=ESCALATE_REQUIRED`  
> `CONTEXT_MODE=ARMED`

本文是当前阶段的唯一实施边界。它把前面面向“完整 Wokey/KIE 类平台”的设计收敛为内部使用的最小版本：管理员维护一个统一分组、模型、上游线路、账号优先级和价格；用户只使用该分组下发的 API key。本文不授权部署、线上迁移、真实付费调用、GitHub 推送或修改旧 `/v1` 链路。

如果本文与早期完整设计中的未来能力冲突，以本文的最小范围为准；早期文档保留作历史依据和已完成实现的说明。

状态解释：前面候选树中已经存在统一 runtime、管理面和若干 provider/账务基础；本文件描述的 M1–M5 是下一阶段的最小收口和生产前补证工作，尚未在本阶段实现。`NEXT_IMPLEMENTATION_NOT_STARTED` 只指 M1–M5，不代表候选树不存在历史实现。

## 1. 用户目标与冻结决策

### 1.1 目标

在不干扰现有 Sub2api 链路的前提下，提供一个内部统一入口：

```text
管理员配置一个 Unified Access Group
    ↓
管理员把当前已验证可用的模型和上游线路发布到该 Group
    ↓
用户只获得该 Group 下的 API key
    ↓
用户按公共模型名调用
    ↓
系统按管理员优先级自动选择 provider/account
    ↓
按实际命中的线路和价格 profile 结算
```

### 1.2 冻结的业务规则

1. 只创建一个面向内部统一入口的 Unified Access Group；不为 Plus、Pro、DeepSeek、豆包、Grok 等分别创建用户可见的 API group。
2. 用户不能选择 provider、账号、Billing Lane、路由优先级或价格规则。
3. 管理员统一维护模型、上游模型、协议 endpoint、账号绑定、优先级和价格。
4. 统一模型目录只包含“当前存在可调度账号、能力匹配且有有效价格”的已发布模型。
5. 同一个公共模型可以有多个内部 lane；实际选中的 lane/account/profile 决定本次价格。
6. 文本模型继续使用现有同步/流式 OpenAI 兼容路径；图片和视频只复用当前已经存在且经过能力矩阵确认的路径。
7. Grok 或其他异步媒体任务创建成功后固定原始账号和价格快照，不能在轮询时重新随机选路。
8. 成功完成或成功交付才扣用户费用；失败、取消、超时、过期、空结果和未成功交付均不扣用户费用。
9. 统一入口失败时只能在统一入口已发布的候选线路中重试，禁止回落旧 `/v1`、旧 Group 或旧 API key 的路由。
10. 价格、能力和账号状态不足时 fail closed，不使用默认倍率、默认价格或免费放行。

### 1.3 “所有模型”的安全定义

“包含当前 Sub2api 支持的所有模型”定义为：

> 当前来源配置中被确认可调用，至少有一个 enabled 且 schedulable 的账号/渠道绑定，endpoint 与能力匹配，并且存在有效价格 profile 的模型。

历史模型、已下架模型、只有 `model_mapping` 记录但没有可调度账号的模型、没有价格的模型、只被旧入口看到但未被统一入口发布的模型，不进入统一目录。

## 2. 现有能力盘点

| 能力 | 当前候选状态 | 本阶段处理 |
|---|---|---|
| 独立 `/unified/v1` 入口和 feature gate | 已有候选实现，生产 gate 关闭 | 直接复用；不改旧 `/v1` |
| ModelConfig/Lane/Target/Binding 结构 | 已有候选实现 | 继续作为内部配置结构，不向用户暴露复杂语义 |
| 同名公共模型对应多个 provider/账号 | 已有候选路由和价格快照设计 | 直接复用；默认固定优先级 |
| OpenAI、DeepSeek、Kimi、Ark/豆包、Gemini、Antigravity、Grok 适配基础 | 已有能力矩阵和适配代码候选 | 只纳入已有能力矩阵确认的 endpoint；不新增猜测适配 |
| Grok 图片/视频异步基础 | 已有创建、pending、poll/content 和结算状态基础 | 只做真实 staging 证据；不扩展通用媒体平台 |
| route-level price profile/snapshot | 已有候选实现和失败释放语义 | 继续复用；生产接线前必须补持久化和账务证据 |
| 管理后台统一网关页面 | 已有较完整的聚合编辑器 | 收敛为管理员视角；隐藏用户自选、复杂策略和未来字段 |
| Unified Access Group 与现有用户 API key 的正式接线 | 候选文档有约定，尚无生产证据 | 本阶段必补 |
| 统一模型目录的可执行闭包 | 候选运行时有目录约束，尚无生产目录发布证据 | 本阶段必补 |
| 从当前 Sub2api 模型来源生成候选集合 | 尚无最小化的管理员刷新/预览闭环 | 本阶段必补；不做外部全自动发现 |
| 旧链路回归、PostgreSQL 迁移、真实 provider/结算/Grok 证据 | 尚未完成 | 本阶段放行门槛；没有证据就保持 gate 关闭 |

### 2.1 独立审计发现的实现门禁

以下不是新增产品功能，而是本阶段必须补齐的安全和正确性约束：

1. **唯一 Unified Group 必须由服务端强制**：管理 API 和运行时都必须拒绝非指定 Unified Group；不能只依赖前端选择或调用者传入的任意有效 `GroupID`。
2. **账号归属校验不受管理员等级绕过**：超级管理员和受限管理员都必须验证 binding account 属于所选 access group/允许的账号范围。
3. **价格表单必须 fail closed**：新建 lane 不得预填可被解释为真实价格的 `1.000000` 上游倍率、`1.200000` 用户倍率或其他默认售价；未导入/审核的 profile 必须为空或明确 blocked，服务端不得发布。
4. **目录必须与实时调度资格一致**：`/unified/v1/models` 不能只检查静态绑定和价格；发布、目录读取或发送前至少要重新确认存在 schedulable/资格通过的账号，不能展示“看得到但当前无法调用”的模型。
5. **候选树没有 Git 提交时使用文件证据**：实施前后用受控文件清单、SHA256、差异清单和命令结果固定版本，不能把历史候选目录表述为不可变提交。

已有实现和限制的详细证据见 [16-runtime-implementation-and-final-audit.md](./16-runtime-implementation-and-final-audit.md) 与 [15-frontend-unified-gateway-development.md](./15-frontend-unified-gateway-development.md)。

## 3. 最小目标架构

本阶段只保留五个必要层：

```text
现有用户/API key/Group 体系
            ↓
一个 Unified Access Group
            ↓
已发布公共模型目录
            ↓
固定优先级的内部线路选择
            ↓
已有 provider adapter + route price snapshot + 账务结算
```

### 3.1 Access Group 与 API key

- 复用 Sub2api 既有 Group、用户和 API key 体系。
- 只增加一个统一入口 Group 的配置/接线，不新增用户级 Provider 偏好表。
- API key 只决定是否进入 Unified Access Group；不携带 provider、account 或价格选择参数。
- 管理后台可以创建、禁用和轮换统一入口 key，但本阶段不改变普通用户的现有 key 行为。

### 3.2 统一模型目录

统一目录的来源不是所有历史表的简单并集，而是已发布配置的可执行闭包：

```text
published ModelConfig
  → enabled RouteTarget
  → enabled AccountBinding
  → account schedulable/资格通过
  → endpoint/capability 匹配
  → 有效 Pricing Profile
  → 进入 /unified/v1/models
```

管理员可以执行“刷新候选模型”得到只读预览：

1. 读取现有 Sub2api 模型/账号/渠道的当前可用信息；
2. 合并同名公共模型；
3. 标出没有线路、没有价格或能力不匹配的阻断项；
4. 管理员确认后写入统一草稿；
5. 服务端校验通过后一次性发布。

该刷新不主动扫描未知外部 Provider，不自动发起付费 probe，不自动发布，不自动启用历史模型。

### 3.3 线路选择

第一版只使用 `fixed_priority`：

1. 按公共模型和 endpoint 找到已发布候选；
2. 过滤能力不匹配、无有效价格、不可调度或熔断账号；
3. 按管理员 priority 选择 lane/target；
4. 在 target 内选择健康、可调度的绑定账号；
5. 生成 route price snapshot 后才发送上游。

不开发 weighted、cheapest、capacity-aware、用户偏好、客户级路由或预测型调度。现有代码中如果保留这些字段，只作为兼容读取或未来扩展，不在本阶段暴露为可配置行为。

### 3.4 计费

保留已有线路级计费模型：

```text
实际命中 route/account/lane/profile
        ↓
冻结 route_price_snapshot
        ↓
Reserve（需要预占时）
        ↓
成功 Capture / 失败 Release
```

每条线路可以使用：

- 现有分组价格来源；
- 手动倍率；
- 手动单位价格；
- 按 token、request、image、video 或 provider-specific 规则。

本阶段不新增公开报价接口，不给用户显示或选择不同价格。管理端只需要显示价格来源、当前有效值和实际结算记录。

## 4. 必要开发项

### M1：统一 Group/API key 接线

目标：让管理员可以真正创建一个统一分组并让用户 API key 进入该分组。

必要内容：

- 复用现有 Group、User、API key 和权限校验；
- 确定统一 Group 的唯一标识和启用状态；
- 在服务端配置/数据库约束中把该标识固定为唯一 Unified Group；管理 API、runtime、模型目录和 key 发行路径都拒绝其他 Group；
- 对所有管理员等级执行 access-group 与 binding-account 归属校验，不允许超级管理员路径绕过；
- 统一 handler 按该 Group 做 scope 校验；
- `/unified/v1/models` 只返回该 Group 已发布目录；
- 旧 key 不自动获得统一目录，统一 key 不自动进入旧路由。

禁止内容：

- 新增用户 Provider 选择表；
- 新增客户级价格系统；
- 将统一 Group 直接替换现有默认 Group；
- 修改旧 `/v1` 的认证和计费分支。

### M2：模型候选刷新与统一目录发布

目标：管理员不需要逐个手工猜模型，但仍然保留最终发布权。

必要内容：

- 复用现有模型可用性/账号资格读取逻辑；
- 生成候选公共模型列表；
- 对每个候选显示 provider、上游模型、endpoint、账号资格、价格阻断项；
- 候选和目录发布都必须重新检查账号 schedulable/资格状态；不能仅依赖历史 binding 状态；
- 支持全选/排除/编辑后生成统一草稿；
- 发布时只物化有可执行闭包的模型；
- 下架模型从新目录移除或标记不可用，但不删除历史快照和历史账务。

禁止内容：

- 建立完整外部 Provider Registry；
- 自动探测所有互联网模型；
- 自动写入或覆盖旧 Group 的模型列表；
- 自动发布探测结果；
- 用模型名并集绕过账号、能力和价格检查。

### M3：默认优先级运行时接线

目标：用户只提供模型名，系统自动选择管理员指定的线路。

必要内容：

- 使用现有统一 route catalog 和 capability matrix；
- 使用固定优先级和现有健康/schedulable 检查；
- 失败只在统一候选中重试；
- 发送前写入 route/account/profile snapshot；
- 同步请求成功结算，失败释放；
- 异步任务固定创建时的 snapshot 和账号。

禁止内容：

- 新写一套路由器替代现有统一 runtime；
- 为内部版本引入多种调度策略；
- 让失败请求回落旧 `/v1`；
- 在异步轮询阶段重新选账号或重新取价。

### M4：管理员简化页面

目标：管理员能在一个页面完成统一入口维护，普通用户无需新增配置页面。

保留/展示：

- Unified Group 状态；
- 模型候选刷新和发布预览；
- 每个公共模型的上游线路；
- 账号、endpoint、优先级和健康状态；
- 线路倍率/单位价格/计量方式；
- 校验、发布、禁用、回滚和审计记录。

价格输入规则：新建 lane 的倍率和单位价格默认为空/未配置状态；只有显式导入或填写并通过服务端校验的 profile 才能发布。前端不得用 `1.0`、`1.2` 或其他数值作为“方便填写”的隐式价格。

隐藏或禁用：

- 用户自选 Provider；
- 用户自选账号；
- weighted/cheapest/capacity-aware 选项；
- 公开 quote/pricing 配置；
- 尚未实现的 Provider Node、外部节点和通用任务模板。

普通用户侧只保留既有 API key 创建/查看能力，不增加新设置流。

### M5：必要验证与放行

必须补齐：

- PostgreSQL 迁移 fresh/repeat/upgrade/conflict 证据；
- 统一 Group/API key scope 证据；
- `/unified/v1/models` 可执行闭包证据，包含实时 schedulable/资格校验；
- 至少一个文本模型的真实 provider staging 调用；
- 至少一个同名模型多 lane 的不同价格验证；
- 图片按次/图片计量验证（若该 lane 纳入）；
- Grok 视频成功、失败、超时、重复轮询/回调验证（若该 lane 纳入）；
- 旧 `/v1`、旧 key、旧 Group 回归；
- 统一 gate 关闭时零影响和即时回滚。

还必须验证：非 Unified Group 被服务端拒绝、超级管理员不能越过账号归属检查、未配置价格的 lane 无法发布，以及新建前端表单不会生成默认有效价格。

## 5. 明确不开发的功能

以下功能不属于本阶段，也不应因为参考 Wokey/KIE 而加入：

1. Wokey 风格 Provider Node、远程节点注册、WebSocket 出口和多 VPS 控制平面。
2. KIE 风格的全品类模型市场、公开模型详情页、用户自定义参数模板和公开积分系统。
3. 用户选择 provider/account/lane 或用户级价格。
4. cheapest、weighted、capacity-aware 等复杂路由策略。
5. 自动发现所有外部 Provider 和自动启用未知模型。
6. 通用音频/音乐/视频任务平台；只验证现有已实现的媒体 adapter。
7. 公开 `/pricing`、`/quote` 和动态价格范围接口。
8. 重写旧 Group、旧 Composite Route、旧 GatewayService 或旧计费体系。
9. 与统一目标无关的依赖升级、代码重构、目录重命名或 UI 重做。

Wokey 只作为统一网关和“控制层/上游执行层分离”的参考；KIE 只作为模型目录和异步媒体任务的参考，不复制其完整商业平台范围。[Wokey API](https://wokey.ai/api) [KIE 官方文档](https://docs.kie.ai/)

## 6. 允许修改与禁止修改边界

### 6.1 允许修改

仅限统一网关新增/已有模块：

- `backend/internal/service/unified_gateway*.go`；
- `backend/internal/repository/unified_gateway*.go`；
- `backend/internal/server/routes/unified_gateway.go` 及统一路由注册的最小新增段；
- 统一网关相关 migration；只有现有候选 schema 无法表达 M1/M2 时才新增字段/表；
- `frontend/src/views/admin/UnifiedGatewayView.vue`、统一网关 API/types/i18n/test；
- 本文及对应测试夹具。

如果必须调用旧 Group/API key/模型能力读取服务，只允许新增只读调用或明确的适配接口，不改变旧服务的业务语义。

### 6.2 禁止修改

- 旧 `/v1/*` handler 和旧 GatewayService 的计费/调度语义；
- 旧 Group、旧 API key、旧 Composite Route、旧 channel price 的默认行为；
- 现有 provider adapter 的非必要重构；
- 生产 VPS、线上数据库、容器、DNS、GitHub 和付费上游；
- 任何用户自定义 Provider 或价格逻辑。

### 6.3 唯一写入边界

本阶段文档由主控写入；后续代码实施必须先重新冻结唯一代码写入者。独立审计只读，不修改代码、测试、契约或审计标准。

## 7. 实施顺序与停止条件

### Phase 0：文档和审计（当前阶段）

- 固定本文和审计报告；
- 逐项核对现有实现；
- 不改代码、不改数据库、不启用 gate。

### Phase 1：本地最小接线

- M1、M2、M3 的本地候选实现；
- 复用现有统一 runtime 和管理面；
- 添加正常、失败、目录闭包、计费和旧路由隔离测试；
- 不接真实生产 Group/API key。

### Phase 2：Staging 证据

- 在隔离数据库执行 migration；
- 创建临时统一 Group/key；
- 只纳入少量已验证文本线路；
- 补齐真实 provider、结算和旧链路回归证据；
- 仍不自动纳入所有历史模型。

### Phase 3：内部 canary

- 只打开 Unified Group 的独立 gate；
- 先纳入文本模型，再按单独清单纳入图片/Grok 视频；
- 每条线路可单独 disable；
- 出现任何旧链路回归、错账、模型目录越界或异步任务无法恢复，立即关闭 unified gate。

### 必须停止的情况

- 需要修改旧 `/v1` 语义才能实现；
- 需要新增用户 Provider 选择或客户价格模型；
- 需要自动启用未经确认的模型；
- 没有 durable snapshot 仍要求上线；
- 价格来源不明却要求继续放行；
- 统一路由失败需要回落旧 Group；
- 迁移、真实 provider 或旧链路回归证据缺失。

## 8. 最小验收矩阵

| ID | 验收场景 | 预期 |
|---|---|---|
| R1 | 普通用户 key 调用 Unified Group | 只看到统一目录，不能选择 provider/account |
| R2 | 统一目录刷新 | 只进入有可执行账号、能力和价格的模型 |
| R3 | 历史下架模型 | 不出现在新目录；历史快照不被删除 |
| R4 | 同一公共模型有 Plus/Pro 两条 lane | 按管理员 priority 自动选路，用户无选择参数 |
| R5 | 同一模型两条 lane 价格不同 | 按实际命中 lane 的 snapshot 收费 |
| R6 | 上游失败后重试 | 只在统一候选内切换；失败 attempt 用户费用为 0 |
| R7 | 图片请求 | 仅在已发布 image lane 使用 image/按次规则，不套 token 价格 |
| R8 | Grok 视频成功 | 创建时锁账号/价格，成功交付只结算一次 |
| R9 | Grok 视频失败/超时/重复回调 | 用户不扣费，预占释放，重复回调幂等 |
| R10 | 无价格/无能力/无可调度账号 | 发送上游前 fail closed |
| R11 | 统一 gate 关闭 | Unified API 被拒绝，旧 `/v1` 行为不变 |
| R12 | 旧 key/旧 Group | 不看到统一目录，不进入统一路由 |
| R13 | 统一路由失败 | 不回落旧 Group 或旧 `/v1` |
| R14 | 管理员禁用一条 lane | 新请求不再选择；历史 snapshot/任务可继续按原状态收敛 |
| R15 | 发布版本冲突 | 服务端拒绝过期 revision，不产生半发布配置 |

## 9. 当前结论

当前候选代码已经覆盖最小版本的大部分核心运行时能力，缺口集中在“正式 Unified Group/API key 接线、模型目录发布闭包、生产/ staging 证据和简化管理入口”，不是重新设计调度器或重写 Provider 适配器。

本阶段完成文档和独立审计后，下一次代码任务只允许围绕 M1–M5 展开。任何 Wokey Provider Node、KIE 完整任务平台、用户自定义路由或复杂调度能力都应登记为未来路线，不得混入本次实现。
