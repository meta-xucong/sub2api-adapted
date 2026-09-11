# 统一 API 管理面开发规范、公共契约与验收矩阵

> 状态：`CONTRACT_REV=v1.3.1-route-guard / UI_PROFILE=ui-20260911-r4 / DOCS_AUDITED_PASS_WITH_CONDITIONS / CODE_IMPLEMENTED_LOCAL / FINAL_AUDIT_PASS_WITH_CONDITIONS / PRODUCTION_GATE_DISABLED`
>
> 本文是本轮前端和管理面开发的唯一实施依据。它定义“如何方便地配置同一个公共模型对应的多个上游 provider、账号和独立价格”，不代表统一运行时已经启用。

## 1. 目标、边界和现状

用户需要在 Sub2api 管理后台完成：

1. 创建一个统一入口，例如 `gpt-5.5`、DeepSeek、豆包或 Grok 视频模型。
2. 在同一个入口下配置 Plus、Pro、生图、视频、火山/Ark 等互不混淆的 Billing Lane。
3. 每个 Lane 绑定自己的 provider、上游模型、endpoint、账号和 Pricing Profile；实际选择哪条 Lane，就使用哪条 Lane 的价格。
4. 支持 `probe_preferred`、`manual_only`、`probe_only`；没有自动探测能力的 provider 可以用人工倍率、人工单位价或白名单 provider-specific 规则。
5. 前端能在发布前验证账号资格、计量单位、价格来源和失败不收费规则，并预览服务器计算结果。

本阶段的硬边界：

- 只修改本地候选树 `upgrade-worktree/merged-dryrun`，不部署、不推送 GitHub、不写线上数据库。
- 保留官方前端基线 `upgrade-worktree/baseline/frontend`，候选前端是其原样复制后的工作树。
- 统一运行时 `config.gateway.unified_gateway_runtime_enabled=false`；不注册普通 `/v1/*` 统一运行时路由。
- 现有 group、channel price、Composite Route、账号调度、OpenAI/DeepSeek/Kimi/豆包/Grok/图片/视频旧路径和旧账本不读取新管理表。
- 本阶段可以实现管理员配置、草稿、校验、预览和本地 SQL/HTTP 测试；真实 provider adapter、余额原子结算、Grok 异步生产回路和线上灰度仍是后续门槛。

当前候选后端已经有隔离的 route/account/catalog、价格解析器、快照状态机、SQL raw repository，并已接入受 gate 保护的 admin management plane；前端已接入独立 DTO/API、聚合编辑器、价格导入、预览、版本和生命周期操作。本文件不把候选管理面误称为已上线运行时：真实 provider adapter、余额原子结算、Grok 异步生产回路和线上迁移仍未放行。

截至 2026-09-11 的候选树证据：`UnifiedGatewayHandler`、`UnifiedGatewayAdminService`、SQL admin repository/atomic repository、`/api/v1/admin/unified-gateway/*` 路由、`UnifiedGatewayView.vue` 和侧栏 gate 均已落地到 `upgrade-worktree/merged-dryrun`；迁移 `236_unified_gateway_admin.sql` 仍只存在候选树，未在任何线上数据库执行。代码变更不能被解释为 runtime 已开启。

## 2. 资源模型：聚合草稿与原子发布

### 2.1 公共模型不是 Route Target

V1 冻结为以下资源闭包：

```text
ModelConfig
  └─ BillingLane
       ├─ PricingProfileVersion
       └─ RouteTarget
            └─ AccountBinding

实际请求/attempt ──> immutable RoutePriceSnapshot
```

概念边界：

- **ModelConfig**：下游看到的公共入口，唯一表达 `access_group + public_model + endpoint`。它有生命周期、revision 和启用状态。
- **BillingLane**：同一公共入口下的一套账号/上游/计费候选，例如 `chatgpt-plus`、`chatgpt-pro`、`image-2`、`volcengine-ark`。Lane 是一等资源，不再只是 `route_target.billing_lane_id` 的自由文本。
- **PricingProfileVersion**：Lane 的不可变价格配置版本，包含来源组、探测/手动规则、计量单位、币种、舍入、成功触发条件和版本摘要。
- **RouteTarget**：Lane 的具体 provider/upstream model/endpoint 候选和优先级。
- **AccountBinding**：RouteTarget 绑定的现有账号。账号凭据仍由现有账号系统管理，统一管理面只接收 account ID 并读取脱敏状态。
- **RoutePriceSnapshot**：实际 attempt 发送上游前冻结的路由、账号和价格事实；完成/失败只更新其状态，不重新选路或重新取价。

V1 只支持 `exact public_model` 和 `fixed_priority` 选择策略。V1 不把 `pool_id`、自由文本 `pool_rule` 暴露给管理 UI；账号池如果需要表达，先展开为多个显式 AccountBinding。候选运行时原型中已有的 `pool_id/pool_rule` 保留兼容读取，但新管理 API 不写入它们。

### 2.2 聚合提交而不是逐条启用

前端编辑的是一个完整的 ModelConfig 草稿，包含全部 Lane、Profile、RouteTarget 和 Binding。保存草稿可以是不完整的；发布必须在服务端一次性完成：

1. 校验所有关系、账号资格和价格规则。
2. 创建不可变 Pricing Profile Version。
3. 在一个数据库事务中写入 ModelConfig/Lane/revision，并把可执行候选物化到隔离的 `unified_route_targets`、`unified_route_account_bindings`。
4. 只有整个聚合通过时才产生 `published` revision；任何一部分失败都不能出现半启用配置。

禁用只影响新的 attempt；历史 revision、RoutePriceSnapshot、审计记录和结算状态不删除。恢复旧版本也是创建新的 revision，不回写旧行。

### 2.3 最小公共 DTO

API ID 使用不透明字符串；服务端可以在内部映射到现有 BIGINT ID，但不能把数据库自增规律作为前端契约。金额、单价和倍率在 HTTP DTO 中使用规范十进制字符串，不能使用 JSON float；当前账本币种仅接受 `USD`，不做隐式汇率转换。

```json
{
  "id": "mc_01J...",
  "access_group_id": "ag_7",
  "public_model": "gpt-5.5",
  "endpoint": "chat_completions",
  "lifecycle": "draft",
  "readiness": "blocked",
  "revision": 3,
  "lanes": [
    {
      "id": "lane_plus",
      "code": "chatgpt-plus",
      "name": "ChatGPT Plus",
      "selection_strategy": "fixed_priority",
      "pricing_source_group_id": "group_12",
      "pricing_source_revision": "sha256:...",
      "profile": {
        "id": "profile_plus_v4",
        "version": "4",
        "pricing_model": "amortized_subscription",
        "billing_mode": "token",
        "rate_mode": "probe_preferred",
        "rate_basis": "token",
        "currency": "USD",
        "base_price_semantics": "provider_base",
        "provider_base_unit_price": "0.000010000000",
        "manual_base_unit_price": null,
        "manual_upstream_multiplier": "1.000000",
        "user_markup_multiplier": "1.200000",
        "final_user_unit_price": null,
        "fixed_fee": "0",
        "minimum_charge": "0",
        "rounding_mode": "half_up",
        "precision": 8,
        "fallback_reason": null,
        "manual_pricing_rules": null,
        "charge_trigger": "success_delivery",
        "failure_charge": "zero"
      },
      "targets": [
        {
          "id": "target_plus_openai",
          "provider_identity": "openai-chatgpt",
          "upstream_model": "gpt-5.5",
          "endpoint": "chat_completions",
          "priority": 10,
          "bindings": [
            {
              "id": "binding_301",
              "account_id": "acct_301",
              "display_name": "Plus account ••••301",
              "schedulable": true,
              "eligibility": "eligible",
              "probe": {
                "status": "ok",
                "basis": "token",
                "resolved_rate_multiplier": "1.000000",
                "snapshot_ref": "probe-20260911-301",
                "received_at": "2026-09-11T00:00:00Z",
                "fresh_until": "2026-09-12T00:00:00Z"
              },
              "priority": 10,
              "enabled": false,
              "revision": 1
            }
          ]
        }
      ]
    }
  ]
}
```

`lifecycle`、`readiness`、`blockers[]`、`warnings[]`、`runtime_effective` 必须拆开返回，不能用一个状态混合“禁用”“探测过期”“运行时关闭”和“没有价格”。

## 3. 页面和操作流程

### 3.1 页面入口

新增管理员页面 `/admin/unified-gateway`，专用 API 模块和组件目录。不要把新语义塞入旧 `GroupsView.vue` Composite Route 弹窗。

页面打开先调用 `GET /admin/unified-gateway/meta`。菜单只有在 `admin_ui_enabled=true` 且用户有 read capability 时显示；直链仍由后端拦截。

页面区域：

1. **ModelConfig 列表**：公共模型、endpoint、Lane 数量、ready/blockers、当前 published revision、runtime_effective。
2. **聚合编辑器**：入口 → Lane → Profile → Target → Binding 的树形/分步表单；草稿状态清晰可见。
3. **账号资格面板**：provider、脱敏账号、schedulable、能力和排除原因；不显示凭据。
4. **计费面板**：探测倍率、人工 fallback、基础单价、下游加价、图片/视频计量和失败不收费规则。
5. **校验/预览面板**：blockers、warnings、服务器公式分解、preview digest 和发布前确认。
6. **Revision/快照面板**：已发布版本、来源组摘要、规则来源、实际快照状态和操作审计。

### 3.2 新建/编辑/发布

1. 选择 access group、public model 和 endpoint。
2. 新增一个或多个 Billing Lane，指定 code/name/选择策略。
3. 为每个 Lane 创建 Pricing Profile；可以先保存草稿，再通过 `pricing-import/preview` 与 `pricing-import/apply` 显式导入某个旧分组价格，页面展示来源组、来源摘要和导入 digest。
4. 填写 provider、上游模型、上游 endpoint、priority，并绑定现有账号；上游 endpoint 可以与公共 endpoint 不同，但必须属于服务端支持的枚举。
5. 选择 `rate_mode` 和 `rate_basis`，填写必要的价格字段。
6. 点击“服务端校验”；前端只做体验校验，不自行判定 ready。
7. 点击“价格预览”；请求必须明确选择 Lane/Target/Binding 和计量输入。
8. 已发布配置可以打开同一 config 的编辑草稿；草稿更新不改 published document，只有发布事务成功后才创建新 revision。页面也提供禁用、版本记录和恢复入口。
9. 校验通过后点击“发布 revision”；发布、禁用、恢复、probe 使用同一个幂等 key 在 step-up 后重试。
10. 发布成功不会打开 runtime gate；页面明确显示 `runtime_effective=false`。

## 4. 管理 API 公共契约

### 4.1 路径和错误 envelope

实际 HTTP 前缀是 `/api/v1`；前端 `apiClient` 的 base URL 已经是 `/api/v1`，所以前端模块传入的 path 不含 `/api/v1`：

```text
实际 HTTP：/api/v1/admin/unified-gateway/*
前端 apiClient：/admin/unified-gateway/*
页面路由：/admin/unified-gateway
```

必须复用现有 response envelope，不新增 `{error:{...}}` 特例：

```json
{
  "code": 422,
  "message": "rate basis does not match billing mode",
  "reason": "UNIFIED_GATEWAY_RATE_BASIS_MISMATCH",
  "metadata": {
    "field": "profile.rate_basis",
    "expected": "image",
    "received": "token"
  }
}
```

成功响应为 `{code:0,message:"success",data:...}`，前端 interceptor 会自动解包 `data`。`reason` 是稳定机器码，`metadata` 只能放脱敏字符串值。不得返回 SQL、凭据、原始 provider 响应体或完整账号信息。

### 4.2 元数据与选项

```http
GET /api/v1/admin/unified-gateway/meta
GET /api/v1/admin/unified-gateway/options?page=1&page_size=50
```

`meta` 在管理员认证后始终可调用，即使 UI gate 关闭也返回 fail-closed 状态：

```json
{
  "contract_rev": "v1.3.1-route-guard",
  "server_time": "2026-09-11T00:00:00Z",
  "admin_ui_enabled": false,
  "runtime_enabled": false,
  "migration_ready": false,
  "schema_version": "unified_gateway_admin_v1",
  "capabilities": {"read": true, "draft": false, "publish": false, "probe": false},
  "supported_endpoints": ["chat_completions", "responses", "images_generations", "videos"],
  "supported_billing_modes": ["token", "per_request", "image", "video"],
  "supported_rate_bases": ["token", "per_request", "image", "video", "provider_specific"],
  "supported_rate_modes": ["probe_preferred", "manual_only", "probe_only"],
  "supported_pricing_models": ["provider_metered", "amortized_subscription", "fixed_request", "image", "video"]
}
```

开关来源和失败策略：

| 字段 | 服务端来源 | 默认值 | 失败策略 |
|---|---|---:|---|
| `admin_ui_enabled` | `config.gateway.unified_gateway_admin_ui_enabled` | `false` | 缺失/读取失败按 false |
| `runtime_enabled` | `config.gateway.unified_gateway_runtime_enabled` | `false` | 缺失/读取失败按 false，管理 API 不得修改 |
| `migration_ready` | 精确检查表、列、约束、索引和 schema version | `false` | DB 错误按 false |

`migration_ready=false` 时只能返回 `meta` 和明确的 gate 错误；不承诺依赖数据库的“只读配置查看”或服务端草稿预览。页面显示阻塞原因，不用本地伪造数据冒充服务端配置。

`options` 分页返回 access group、显式可导入的 pricing source group、脱敏账号、账号能力和服务端 eligibility；前端不得从旧 `/groups`、`accounts` 列表拼出可发布资格。

### 4.3 聚合配置、草稿、校验和发布

```http
GET    /api/v1/admin/unified-gateway/configs?page=1&page_size=20&status=published
GET    /api/v1/admin/unified-gateway/configs/{config_id}
POST   /api/v1/admin/unified-gateway/configs/{config_id}/draft

POST   /api/v1/admin/unified-gateway/drafts
GET    /api/v1/admin/unified-gateway/drafts/{draft_id}
PUT    /api/v1/admin/unified-gateway/drafts/{draft_id}
POST   /api/v1/admin/unified-gateway/drafts/{draft_id}/validate
POST   /api/v1/admin/unified-gateway/drafts/{draft_id}/preview
POST   /api/v1/admin/unified-gateway/drafts/{draft_id}/publish

POST   /api/v1/admin/unified-gateway/configs/{config_id}/disable
GET    /api/v1/admin/unified-gateway/configs/{config_id}/revisions
POST   /api/v1/admin/unified-gateway/configs/{config_id}/revisions/{revision}/restore

POST   /api/v1/admin/unified-gateway/bindings/{binding_id}/probe
```

草稿写入和发布：

- `POST/PUT` 只保存聚合草稿，不改变任何运行时候选。
- `POST /configs/{id}/draft` 打开已有 config 的唯一编辑草稿；如果已有草稿则幂等返回该草稿，不复制出第二个 public config。
- `PUT`、validate、publish 使用不透明 `revision`/`If-Match`，冲突返回 `409`；validate 的 If-Match 绑定 draft revision，publish 同时锁定 draft revision 和 config revision。
- 关键写操作带 `Idempotency-Key`；同 key 不同请求摘要返回 `UNIFIED_GATEWAY_IDEMPOTENCY_CONFLICT`。
- validate 返回 HTTP 200 的 `{valid, blockers, warnings, field_errors, validation_token, server_revision}`；业务不就绪不是协议错误。
- publish 必须携带最新 validation token；服务端在事务内重新校验，消除 validate→publish TOCTOU，然后创建新的不可变 published revision。
- `disable` 是软禁用，必须记录 actor、原因、旧/新 revision；不物理删除。

写接口的并发/幂等协议冻结如下：

- `Idempotency-Key` 必须是 1–128 个可打印 ASCII 字符；draft create/update、pricing import、publish、restore、disable、probe 都必须携带。
- 服务端对请求 body 做 canonical JSON（UTF-8、对象 key 按字典序、无空白、数字按 DTO 字符串处理）后计算 `SHA-256`，保存 `request_digest`。同一 actor、resource、operation 和 key 搭配相同 digest 时重放原 HTTP 状态和响应摘要；digest 不同返回 `409 UNIFIED_GATEWAY_IDEMPOTENCY_CONFLICT`，不得执行第二次。
- 更新/发布/恢复/禁用必须带 `If-Match: "<revision>"`；创建 draft 不需要。服务端同时校验 path resource 当前 revision 和 body 中的 revision，任一不一致返回 `409 UNIFIED_GATEWAY_VERSION_CONFLICT`。
- 409 响应只返回当前 revision、resource kind/id 和重新加载提示，不返回另一管理员未提交的草稿内容。
- publish/restore/enable/probe 使用现有 `stepUpAuth` 中间件和 `useStepUp` 重试约定：首次返回 `403` + `reason=STEP_UP_REQUIRED` 时前端完成 `/user/totp/step-up`，然后用同一 `Idempotency-Key`、同一 digest 重试一次；TOTP 未启用、管理员 API key 禁止或 step-up 不可用分别透传现有 `STEP_UP_TOTP_NOT_ENABLED`、`STEP_UP_ADMIN_API_KEY_FORBIDDEN`、`STEP_UP_UNAVAILABLE`，不得自定义 header 绕过会话授权。
- 服务端只在 handler 进入 service 前读取 `Idempotency-Key`，不能从前端提交的 `actor_id`、`access_group_id` 或任何 hidden field 信任权限。

### 4.4 价格导入、预览和快照

旧 `groups.rate_multiplier`、模型价格、图片/视频价格只能作为显式动作：

```http
POST /api/v1/admin/unified-gateway/drafts/{draft_id}/pricing-import/preview
POST /api/v1/admin/unified-gateway/drafts/{draft_id}/pricing-import/apply
```

请求至少包含 `lane_id` 与 `source_group_id`；apply 还必须带 `Idempotency-Key`、`If-Match: "<draft_revision>"` 和 preview 返回的 `import_digest`。前端先展示 source revision/profile digest，再由管理员点击确认应用；服务端重新计算 digest，不匹配时拒绝写入。当前实现从服务端 active group reader 读取旧分组的倍率/图片/视频价格字段，生成新的独立 Profile、`source_revision` 和 `import_digest`，并将导入写回当前 draft；运行时不回读旧 group/channel price。flat image/video rule 只有在来源分组存在兼容单位价时才允许导入，否则阻塞并要求人工填写。

```http
POST /api/v1/admin/unified-gateway/drafts/{draft_id}/preview
GET  /api/v1/admin/unified-gateway/snapshots?page=1&page_size=50
    &access_group_id=ag_7&public_model=gpt-5.5&status=captured
    &from=2026-09-01T00:00:00Z&to=2026-09-12T00:00:00Z
```

preview 请求必须明确 `lane_id`、`target_id`、`binding_id` 和计量输入。响应返回：

```json
{
  "valid": true,
  "preview_digest": "sha256:...",
  "quote_id": "quote_01J...",
  "persisted": false,
  "selection_status": "ready",
  "billing_mode": "token",
  "rate_mode": "probe_preferred",
  "resolved_rate_source": "probe_declared",
  "probe_status": "ok",
  "upstream_declared_rate": "1.200000",
  "manual_upstream_multiplier": null,
  "user_markup_multiplier": "1.200000",
  "effective_multiplier": "1.200000",
  "billable_units": {"input_tokens": "1000", "output_tokens": "500"},
  "estimated_charge": "0.00123000",
  "currency": "USD",
  "rounding_mode": "half_up",
  "precision": 8,
  "fallback_reason": null,
  "policy_version": "4",
  "profile_id": "profile_plus_v4",
  "probe_snapshot_ref": "probe-20260911-301",
  "charge_trigger": "success_delivery",
  "failure_charge": "zero"
}
```

preview 是 dry-run：不冻结余额、不产生可结算 `price_snapshot_id`。真正运行时只有在发送上游前才创建不可变 RoutePriceSnapshot，并将 ID 关联到后续 usage/settlement。快照列表返回 `{items,total,page,page_size,pages}`，只返回脱敏路由/账号、状态、倍率来源、版本、单位、用户费用、时间和 digest；不返回 `response_body`、原始 provider 响应或凭据。

## 5. 计费和倍率契约

### 5.1 配置字段和精度

Pricing Profile 必须包括：

- `pricing_model`：`provider_metered | amortized_subscription | fixed_request | image | video`。
- `billing_mode`：`token | per_request | image | video`。
- `rate_mode`：`probe_preferred | manual_only | probe_only`。
- `rate_basis`：`token | per_request | image | video | provider_specific`。`provider_specific` 是计量基础，不是当前独立 billing mode。
- `base_price_semantics`：`provider_base | final_user_price`。
- `provider_base_unit_price`、`manual_base_unit_price`、`manual_upstream_multiplier`、`user_markup_multiplier`、`final_user_unit_price`、`fixed_fee`、`minimum_charge`，均为十进制字符串或 null。
- `currency=USD`、`rounding_mode=half_up`、`precision`、`charge_trigger=success_delivery`、`failure_charge=zero`。
- `fallback_reason`：`manual_only` 或实际使用人工 fallback 时必填。
- `manual_pricing_rules`：只允许服务端白名单结构，禁止脚本/SQL/任意表达式；v1 只允许 `formula_id=flat_unit_price`。
- `pricing_source_group_id`、`pricing_source_revision`：显式导入时保存，运行时不隐式读取。

金额公式由服务端使用 decimal 处理，前端只展示服务端返回的分解项：

```text
provider_unit_price = provider_base_price × effective_upstream_multiplier
user_unit_price = provider_unit_price × user_markup_multiplier
user_charge = round(user_unit_price × measured_units + fixed_fee)
```

如果 `base_price_semantics=final_user_price`，必须只填写 `final_user_unit_price`，禁止再填写或叠加任何倍率。无有效基础价格、单位、币种或规则版本时 fail closed。

### 5.2 探测与人工兜底

- `probe_preferred`：新鲜且 basis 匹配的 probe 优先；unsupported/failed/stale 时按 account → lane → source profile 查找人工规则；没有有效规则则 blocked。
- `manual_only`：不等待 probe，直接使用人工规则；probe 状态不参与 readiness。
- `probe_only`：没有新鲜兼容 probe 立即 blocked。
- 输出中的 `resolved_rate_source` 只能是 `probe_declared`、`manual_fallback`、`manual_only`；它与配置字段 `rate_mode` 分离。
- `accounts.rate_multiplier` 仍是内部成本/配额语义，不是统一下游售价。

### 5.3 计量与失败

- 普通文本按 token 或 provider 真实计量；图片按原版 image 模式的图片单位；视频按最终成功任务的 adapter 计量。
- 火山/Ark、豆包、Kimi 等非 ChatGPT 链路使用 `manual_only + provider_specific` 或经验证的具体 basis；没有 adapter 能把维度转换为单位时保持 disabled。
- 成功完成或成功交付才计费；上游失败、切换失败、取消、超时、过期、无有效结果和视频最终失败均为用户费用 0。
- 同一请求重试/重复回调必须复用同一 snapshot 和幂等状态；不得重新随机选账号或重新取价。

计量 adapter 和多 attempt 规则：

- 每个发布的 Profile 指定 `pricing_schema_id`。V1 白名单为 `token_v1`、`request_v1`、`image_delivery_v1`、`video_delivery_v1`、`flat_unit_price_v1`；未知 schema 不能 publish。
- adapter 接收已脱敏的请求/上游响应/任务最终状态，返回规范 `MeasureVector`（十进制字符串）和 `delivery_status`，不能直接返回用户金额。`token_v1` 至少区分 input/output/cache；`request_v1` 返回成功交付请求数；`image_delivery_v1` 返回已交付图片数；`video_delivery_v1` 只在最终任务成功时返回交付时长/任务单位；`flat_unit_price_v1` 只允许明确声明 `unit=request|image|video_task`，不接受任意表达式。
- 一个逻辑请求可以有多个 `attempt_id`。每次实际选中的账号/provider 都有自己的 attempt snapshot：失败 attempt 只能 `released`/`failed` 且用户费用为 0；成功交付的唯一 attempt 才能 `captured`。逻辑请求最多一个用户收费快照，重复回调按 `(request_id, attempt_id, delivery_event_id)` 去重。
- Grok/视频提交阶段只绑定账号、Lane、Profile 和 pending snapshot，不收费；轮询/回调使用同一 binding 和 snapshot。最终成功交付才由 `video_delivery_v1` 产出单位并 capture；失败、取消、超时、过期和无法确认交付均 release 且用户费用为 0。
- provider 已产生的内部成本可以另记 provider/account cost，但不能通过失败 attempt 转成用户 charge；settlement 状态不明时 fail closed，并进入 reconciliation，而不是重新扣费。

## 6. Gate、权限、状态和安全

### 6.1 Gate

- `admin_ui_enabled`、`runtime_enabled` 默认 false，缺失/读取失败均 false。
- admin gate 关闭时只有认证后的 `meta` 可返回；其它管理接口 fail closed。
- migration readiness 必须检查精确 schema version、列、约束和索引，不是只判断表存在。
- runtime gate 只由配置/发布流程控制，管理 API 不得修改；关闭时不注册普通 `/v1` 统一路由。
- 未来运行时必须同时满足 runtime gate、API key/access-group 资格和最新 published enabled revision；任一失败直接拒绝，不 fallback 旧 group/Composite Route。

### 6.2 权限

- read：管理员。
- draft/validate/preview：统一网关配置管理员。
- publish/restore/probe：高级管理员 + 强制 step-up；V1 不另设独立 `enable`，发布即产生 enabled published revision，恢复通过 restore 创建新 revision。
- emergency disable：高级管理员，可 break-glass 但必须填写原因并审计。
- 所有查询、更新、probe、snapshot 都按 access group 在服务端约束，不能用前端隐藏或仅返回 404 代替 IDOR 防护。
- publish、restore、disable、probe 记录 actor、原因、前后 revision、digest 和 request ID。

候选实现已接入现有 `User.AllowedGroups` 的 group-scope reader，并由 admin middleware 将认证 actor ID 写入 service context。`AllowedGroups` 非空时，config/draft/revision/snapshot/options/pricing source/probe/binding 均按服务端 access-group 范围过滤；账号 binding 还必须属于所选 access group，缺少 group-account reader 时 fail closed。`AllowedGroups` 为空保留既有超级管理员的显式 unrestricted 语义。所有幂等 replay 也会重新检查当前 scope，权限收窄后旧 key 不能返回越权响应。该实现只完成候选管理面，`runtime_enabled=false` 和生产放行门仍保持关闭。

路径资源的归属解析必须由服务端沿关系链完成，不接受客户端传入的 owner 覆盖：

| path 参数 | 服务端解析链 | 跨组或不存在时 |
|---|---|---|
| `config_id` | `ModelConfig.access_group_id` | 对外 404，对内审计 `RESOURCE_SCOPE_DENIED` |
| `draft_id` | `Draft.model_config_id → ModelConfig.access_group_id` | 对外 404；禁止用 draft body 的 group 作为归属 |
| `lane_id` | `BillingLane.model_config_id → ModelConfig.access_group_id` | 对外 404；不能仅按 lane code 查 |
| `target_id` | `RouteTarget.lane_id → BillingLane → ModelConfig` | 对外 404；不能只按 target ID 更新；上游 endpoint 可与公共 endpoint 不同，但必须在支持枚举内 |
| `binding_id` | `AccountBinding.target_id → Target → Lane → ModelConfig` | 对外 404；不能只按 account ID 更新 |
| `snapshot_id` | `RoutePriceSnapshot.access_group_id` 且校验 API key/user scope | 对外 404；列表查询与 path scope 取交集 |

普通管理员可以读取的 access group 范围由现有 admin 权限服务决定；统一 service 必须显式调用该授权结果。任何 body/query 中的 `access_group_id` 仅用于筛选，不能扩大调用者范围。

### 6.3 状态

服务端分别返回：

- `lifecycle`：`draft | published | disabled | archived`。
- `readiness`：`ready | blocked`。
- `blockers[]`：无账号、无价格、basis/能力不匹配、schema 未就绪等。
- `warnings[]`：probe 过期但有人工 fallback 等。
- `runtime_effective`：结合全局 runtime gate 后的真实效果。

真值规则：`probe_only + stale/missing probe=blocked`；`probe_preferred + stale probe + valid fallback=ready` 且 warning；`manual_only` 不因 probe 状态阻塞；`runtime_off` 不改变 config lifecycle。

### 6.4 复用边界

| 既有组件 | 可复用 | 禁止复用 |
|---|---|---|
| Composite Route | 列表/抽屉视觉、provider 标签、优先级交互 | DTO、resolver、prefix/fallback/所有权语义、硬删除 |
| GroupRateMultipliers | 对话框壳、搜索分页、dirty state | 旧倍率数据源、客户端最终计价、批量覆盖旧 group |
| UpstreamBillingRateCell | badge、tooltip、刷新交互 | 浏览器 freshness/effective 计算、默认倍率、失败探测当可用 |
| 旧 channel pricing | 显式导入预览和来源摘要 | 运行时隐式读取或覆盖统一 Profile |

## 7. 后端实施契约和迁移

必须新增隔离管理面：

```text
internal/handler/admin/unified_gateway.go
internal/handler/admin/unified_gateway_test.go
internal/service/unified_gateway_admin.go
internal/service/unified_gateway_admin_test.go
internal/repository/unified_gateway_admin_repo.go
internal/repository/unified_gateway_admin_atomic.go
internal/server/routes/admin.go
internal/handler/handler.go
internal/handler/wire.go
internal/service/wire.go
internal/repository/wire.go
```

服务分层：handler 解码/权限/错误映射；service 做聚合校验、decimal 转换、access group、profile 版本和发布事务；repository 做参数化 SQL、schema readiness、CAS、脱敏查询。新管理面不得调用旧 CompositeRouteResolver、旧计费仓储或旧账本。

候选迁移需要新增统一管理域表（建议迁移 `236_unified_gateway_admin.sql`）：

- `unified_gateway_model_configs`：ModelConfig 生命周期、access group、public model、endpoint、revision。
- `unified_gateway_billing_lanes`：Lane 一等资源、选择策略、来源组/版本、current profile、revision。
- `unified_gateway_pricing_profiles`：不可变 Profile JSON/字段、digest、审批人、版本和币种。
- `unified_gateway_drafts`：聚合草稿和 actor/版本。
- `unified_gateway_config_revisions`：已发布聚合快照、revision、原因和审计摘要。

发布事务将完整聚合物化到现有隔离 route/binding 表；旧表增加 `version`/来源字段和约束。binding 外键的级联删除必须改为 `ON DELETE RESTRICT` 或等价保护。普通管理 API 不提供物理 DELETE，只提供软禁用和新 revision 恢复。

新增字段至少包括 `currency`、`rounding_mode`、`fallback_reason`、`manual_pricing_rules`、`pricing_source_group_id`、`pricing_source_revision`、`probe_status` 和不可变 profile digest。缺字段时 provider-specific 管理 Lane 必须保持 disabled。

管理更新使用不透明 revision/`If-Match` 条件；版本冲突为 `409 UNIFIED_GATEWAY_VERSION_CONFLICT`。所有创建、发布、恢复、禁用、probe 使用幂等键和请求摘要。

## 8. 前端实现文件清单

```text
frontend/src/types/unifiedGateway.ts
frontend/src/api/admin/unifiedGateway.ts
frontend/src/api/__tests__/admin.unifiedGateway.spec.ts
frontend/src/views/admin/UnifiedGatewayView.vue
frontend/src/router/index.ts
frontend/src/components/layout/AppSidebar.vue
frontend/src/i18n/locales/{zh,en}/
backend/migrations/236_unified_gateway_admin.sql
```

当前 UI 采用一个隔离的聚合页面承载 Lane/Profile/Target/Binding/Validation/Preview/Revision 操作，后续可以按组件拆分但不是本阶段契约前提。前端 API 模块只传 `/admin/unified-gateway/*`，使用现有 `apiClient`，沿用现有 envelope/interceptor。所有新 DTO 采用 string money/IDs、nullable price、RFC3339 时间；不得在组件中复制服务端 ready、freshness、最终费用或 lane selection 算法。`useStepUp` 与 `TotpStepUpDialog` 复用现有认证流程，不自定义绕过 header。

## 9. 测试、审计和验收矩阵

> 本节保留开发阶段冻结的验收矩阵；未勾选项表示尚未满足生产放行条件，不否定第 11、12 节已完成的候选管理面实现和本地证据。最终状态以文档顶部、Revision 5 记录及第 12 节为准。

### 9.1 契约固定

- [ ] `v1.3.1-route-guard`、Go DTO、TypeScript DTO、枚举和错误 fixture hash 一致。
- [ ] HTTP 前缀 `/api/v1`、前端 path `/admin`、response envelope 和 endpoint 枚举一致。
- [ ] ModelConfig/Lane/Profile/Target/Binding/Snapshot 关系不混淆；公共模型不等于 route target。
- [ ] money/multiplier 不使用 JSON float；decimal 黄金向量覆盖 0、极小值、大值、precision 和 rounding。

### 9.2 后端管理 API

- [ ] admin gate、runtime gate、精确 schema readiness fail closed。
- [ ] access group IDOR、管理员权限、step-up、audit actor/reason/digest。
- [ ] 草稿不影响 runtime；validate 返回 blockers/warnings/field errors；publish 事务无半配置。
- [ ] revision/If-Match/幂等键/请求 fingerprint 冲突正确。
- [ ] probe/manual/provider-specific 规则和 profile/snapshot 字段完整。
- [ ] 旧 group/channel pricing 只能显式导入；运行时不隐式读取。
- [ ] soft disable、restore new revision、RESTRICT 防删和历史 snapshot 保留。
- [ ] snapshot 分页、筛选、脱敏；不返回 response body、凭据或原始 provider 响应。

### 9.3 前端

- [ ] gate loading/error 默认隐藏菜单，直链由后端拦截。
- [ ] 聚合编辑器可创建同一 public model 的 Plus/Pro/image/video/Ark 多 Lane。
- [ ] 表单字段按 Profile/Target/Binding 分组，手动规则、探测规则、过期 fallback 和 blockers/warnings 可区分。
- [ ] 预览显示服务器金额和分解，不在前端重复计价；dry-run 不显示为 snapshot ID。
- [ ] 409 不覆盖未提交内容；未知字段、无价格、basis 不匹配、无账号均阻止 publish。
- [ ] 不把账号凭据写入 DOM、日志、toast、网络回显或快照列表。
- [ ] 既有 Groups、Channels、Accounts 和旧侧边栏页面回归不变。

### 9.4 本地集成与回归

- [ ] 本地创建 `gpt-5.5` ModelConfig，添加 Plus/Pro/图片三条 Lane，独立 Profile 和账号。
- [ ] 本地创建 DeepSeek/豆包/火山 `manual_only + provider_specific` Lane，不能伪造 token probe。
- [ ] 同一公共模型切换 Lane 时 preview digest、rate source、单位和金额不串价。
- [ ] 成功 attempt 产生 snapshot；失败/取消/超时/视频最终失败用户费用为 0。
- [x] 前端 `pnpm typecheck`、`pnpm lint:check`、`pnpm test:run`、`pnpm build`；后端统一网关定向 `go test` 与定向 `go vet`。
- [~] 后端全量 `go test ./... -count=1` 已执行但受官方基线同样存在的内容审核异步时序、插件包 Windows rename 文件锁影响；race 受环境缺少 C 编译器阻断。
- [ ] 静态比较旧八个 billing/request 关键文件，确认无非必要修改；runtime gate 仍为 false。
- [x] 新的独立前端/后端审计 Agent 复核最终代码、迁移、路由注册、测试输出和敏感信息边界；两者均为 `PASS_WITH_CONDITIONS`。

## 10. 实施顺序和停止条件

1. 先完成第 236 号隔离管理域迁移和 schema readiness 检查，不在线执行。
2. 实现公共 DTO、decimal 校验、admin repository/service/handler/wiring 和 aggregate draft/publish 测试。
3. 实现前端 types/API/editor/preview/revision 页面、router/sidebar gate 和双语文案。
4. 先定向测试，再前端全量检查和后端全量测试；失败路径、并发、IDOR、幂等、脱敏和旧链路回归均必须有证据。
5. 新的独立审计 Agent 只读复核；主 Agent 只修复明确发现并重新测试/审计。

下列任一项发生都必须停止，不能宣称完成：runtime gate 意外开启；旧链路文件出现无关变更；配置可半发布；服务端使用隐式旧价格；预览污染可结算 snapshot；失败路径可能扣费；金额精度/舍入无证据；真实数据库/线上探测缺少证据。

最终交付只表示“候选树管理面和本地模拟测试通过”。生产启用仍需迁移备份、线上只读探测、灰度、余额账务适配、Grok 异步验收、回滚演练和 runtime gate 审批。

## 11. Revision 4 实现记录与最终前置门

本轮已在 `upgrade-worktree/merged-dryrun` 落地候选管理面，且保持 `config.gateway.unified_gateway_runtime_enabled=false`：

- 后端：236 号隔离迁移、精确 schema readiness、aggregate draft/create-from-existing-config/update/validate/preview/publish/disable/restore、pricing import preview/apply、revision/snapshot/probe 管理 API、标准 response envelope、严格 JSON 解码、SQL atomic idempotency 和 draft/config 双 revision CAS。
- 前端：metadata fail-closed 菜单与直链页面、聚合 Lane/Profile/Target/Binding 编辑、每 Lane 手动倍率/最终单价/flat rule、显式价格导入、明确 Lane/Target/Binding 预览选择、success/failed 试算、账号资格提示、版本/禁用/恢复、`useStepUp` 同幂等 key 重试、双语文案和 API 契约测试。
- 安全修复：旧 route target 自动约束名使用定义解析删除；admin 部分唯一索引在 `ON CONFLICT` 中显式带谓词；schema readiness 检查索引列顺序、唯一性、精确谓词、旧约束残留和 `ON DELETE RESTRICT` FK；probe 只接受属于统一配置文档及当前管理员 scope 的 binding，缺失/非法/过期 `FreshUntil` 一律 stale；幂等 replay 重新执行当前 scope 检查。

候选代码验收已保留以下证据；尚未具备的生产级证据仍列为 gate：

1. 后端统一网关定向 `go test`、定向 `go vet`、权限收窄重放测试和 schema predicate/index helper 测试；全量测试已执行但保留官方基线失败说明；
2. 前端 `pnpm typecheck`、`pnpm lint:check`、`pnpm test:run`（255/1863）、`pnpm build` 和 admin API 契约测试；
3. 临时 PostgreSQL forward migration/重复 migration/发布物化验证，特别是部分唯一索引 conflict inference：尚未执行，生产 gate 保持关闭；
4. 修复后独立前端审计 `Newton` 和后端审计 `Linnaeus` 均为 `PASS_WITH_CONDITIONS`；真实 provider adapter、余额原子结算或 Grok 异步恢复缺口继续列为生产 gate。

## 11. 编码前独立审计状态

上一轮独立审计 Agent `Mill`（`01a08e31-483d-7ee0-96f8-d060f9cbafb5`）对旧版文档的结论为 `PASS_WITH_CONDITIONS`。随后延迟返回的独立思考 Agent `Hume`（`01a08e2b-3688-7841-9300-e97dcb377f70`）提出了聚合资源、实际 API 前缀、错误 envelope、金额精度和 endpoint 枚举等结构性问题；Revision 3 已吸收这些意见。

Revision 3 最终只读复审 Agent：`Epicurus`（`01a08e44-41fa-71c3-96a4-d81dbd0070bb`）。复审结论：`PASS_WITH_CONDITIONS`，允许进入编码。实现条件是：补充契约测试，统一 enable 与现有 disable/restore endpoint 命名，runtime gate 保持关闭，旧 Composite/channel/group multiplier 与旧账本零干扰。

## 12. 最终前端/后端复审记录（2026-09-11）

| 角色 | Agent | 结论 | 关键确认 |
|---|---|---|---|
| 前端终审 | `Newton` / `01a08e96-bfc6-7220-b298-9561e0c649fd` | `PASS_WITH_CONDITIONS` | preview→details→apply、digest/If-Match/idempotency、step-up 同 key、metadata fail-closed、生命周期和 snapshots 契约一致；无代码级 P0/P1 阻塞。 |
| 后端终审 | `Linnaeus` / `01a08e96-befd-7433-b501-1f1aafbac8d9` | `PASS_WITH_CONDITIONS` | scope/replay、probe binding scope、索引列/唯一性/谓词、旧约束移除和 RESTRICT FK 已满足 fail-closed 门禁；无代码级安全/一致性阻塞。 |

终审条件：PostgreSQL migration fresh/repeat/conflict、race（当前环境缺 C 编译器）、真实 provider/runtime/settlement/Grok 恢复和线上灰度仍未验证；这些条件不影响现有旧链路，但在统一入口启用前必须完成。
