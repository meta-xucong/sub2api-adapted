# Smart Router 图片链路与上游端点健壮性修正规格

**状态：审计完成，待实现；本文件本轮只读审计，不包含 VPS 改动。**

## 1. 目标与不可破坏项

本规格解决两类问题：

1. OpenAI 兼容账号把完整 API 端点误填进 `base_url` 后，生成、编辑、Responses 或 compact 端点被错误拼接。
2. Smart Router 每日校准把不应参与图片测试的账号纳入探测，造成无效请求、错误健康记录和不必要的上游消耗。
3. Smart Router 也作用于普通聊天/Responses 链路；如果只恢复图片线路，普通线路被软降权后可能长期得不到恢复机会。
4. 新增或修改线路后，Smart Router 应自动发现、验证并纳入调度，不要求为每条新线路修改代码或增加硬编码规则。

以下既有行为必须保持不变：

- `responses_compact` 仍是独立能力，不与普通 `responses` 或图片能力共享健康状态、失败次数和切线预算。
- compact 仍要求真实的压缩结果，不能用 HTTP 200、usage 或普通文本替代 `encrypted_content`。
- compact 的独立 failover、健康账本和上海时间 04:00 校准继续生效。
- 图片生成与图生图仍是两个能力 lane；流云等生成/编辑行为不同的上游仍需分别校准。
- 线路失败只做软降权/临时退避，不把账号永久删除、不把 `schedulable` 改为永久关闭、不修改用户手工计费优先级。
- 上海时间 04:00 的校准是“恢复探测”，不是只检查当前高优先级健康线路：必须覆盖 Smart Router 之前软降权/临时退避的线路。
- 恢复探测成功后恢复该线路的原始有效优先级；失败则继续保留降权并延后下一次探测，不能因为暂时失败而永久关闭。
- 普通聊天/Responses、图片生成、图生图和 compact 必须使用独立的能力状态；某个能力恢复成功不能替另一个能力恢复优先级。
- 现有 `/v1`、自定义版本路径、代理、TLS、模型映射和账号级请求头行为不能被端点规范化改坏。
- 新线路接入必须通过通用的账号/分组/模型映射和能力声明完成，不依赖 URL 白名单或供应商名称判断。

## 2. 已确认的线上证据

### 2.1 账号 100 的 Base URL 配置错误

aiself 数据库中：

| 字段 | 结果 |
|---|---|
| 账号 | `【自用】nbility.dev-生图` |
| account_id | `100` |
| 状态 | active / schedulable |
| 模型映射 | `gpt-image-2 -> gpt-image-2` |
| 当前 base_url | `https://api.nbility.dev/v1/images/generations` |
| 应填 base_url | `https://api.nbility.dev/v1` |

当前日志完全复现：

```text
POST /v1/images/edits
upstream: 404 Invalid URL (POST /v1/images/generations/v1/images/edits)
```

同一账号的 04:00 校准记录同时显示：

- 文生图：HTTP 200，约 34.8 秒；
- 图生图：HTTP 404，约 175 毫秒；
- 生产图生图请求也连续返回同一个 404。

因此这是一个确定的配置/端点组合问题，不是 aiself 宕机，也不是 Smart Router 的临时抖动。

### 2.2 为什么旧测试没有暴露

当前 `base_url` 恰好包含 generations 端点，所以文生图会得到一个看似正确的：

```text
https://api.nbility.dev/v1/images/generations
```

但图生图流程会在该字符串后继续拼接 `/v1/images/edits`，于是形成错误路径。直连完整 generations URL、只测文生图、或测试其他账号，都不会触发这个组合。

### 2.3 04:00 校准实际测试了不应测试的账号

aiself 最近一次校准运行：

```text
scheduled_for: 2026-07-13 04:00:00 +08
summary: probes=28
```

结果中出现了普通聊天/代码账号、手工 `schedulable=false` 的账号和没有明确图片模型映射的账号，例如 account `6`、`10`、`21`、`24`、`83`、`84`、`85`、`98`、`99`、`103`。

这些探测产生了 403、404、模型不存在、资源不存在以及错误的图片 MIME 等结果。它们会：

- 消耗上游额度或触发供应商风控；
- 污染图片健康账本；
- 使每日校准耗时增加；
- 把“账号不具备图片能力”误记成“图片线路不稳定”。

这不是 compact 调度问题，但必须修正校准候选集合。

### 2.4 浏览器/客户端还存在一条独立的错误预检路径

日志曾出现：

```text
OPTIONS /v1/images/generations/v1/images/edits -> 403
```

同时正式 V3 请求进入 aiself 的路径是正确的 `/v1/images/edits`。因此这条 OPTIONS 错误属于调用端的 Base URL/endpoint 拼接或 CORS 预检问题，不应通过 Smart Router 切线解决。修复账号 100 后，客户端仍需完成一次路径契约验收。

### 2.5 当前图片错误分类本身是合理的

账号 100 的 404 被记录为：

```text
capability=image_edit
failure_class=client_error
action=no_penalty
```

这符合预期：确定性的错误 URL 不应被当成瞬时 5xx 去重试和切线。修正方向是阻止错误配置进入运行态，而不是把所有 404 改成可重试。

### 2.6 校准图生图探针存在 MIME 假阴性风险

当前校准 fixture 使用 `multipart.Writer.CreateFormFile`。该方法生成的文件 part 默认可能是 `application/octet-stream`。部分聚合上游会保留该 MIME 并拒绝转换后的图片输入，线上结果表现为：

```text
Expected a base64-encoded data URL with an image MIME type,
but got unsupported MIME type 'application/octet-stream'
```

本地转换函数对需要转换的请求有 `http.DetectContentType`，但原始 API-key multipart 转发并不一定会经过该转换。因此校准探针应显式发送 `Content-Type: image/png`，否则可能把“探针格式不规范”误判为“上游不支持图生图”。

## 3. 源码定位

### D1：账号 Base URL 没有语义校验，完整 endpoint 会进入运行态

相关位置：


- `backend/internal/service/account.go:1226`，`GetOpenAIBaseURL` 原样返回 API-key 账号的 `credentials.base_url`；
- `backend/internal/service/openai_gateway_request_body.go:39`，`validateUpstreamBaseURL` 只做 URL 格式/安全校验；
- `backend/internal/service/openai_endpoint_url.go:8`，`buildOpenAIEndpointURL` 只识别版本后缀或完整目标后缀，不会把 `/v1/images/generations` 归一化为 `/v1`；
- `backend/internal/service/openai_images.go:773`，图片端点由 base URL 和请求 endpoint 组合；
- `backend/internal/service/openai_images.go:824`，`buildOpenAIImagesURL` 调用通用拼接器；
- `backend/internal/service/account_test_service.go` 的 API-key 测试和 compact 测试也复用同一 base URL 语义。

### D2：图片校准候选集合过宽

相关位置：

- `backend/internal/service/smart_router_calibration_service.go:187`，`imageCalibrationLanes` 从 `ListActive` 结果生成图片 lane；
- 当前筛选只检查 `IsOpenAI`、`SupportsOpenAIImageCapability` 和模型字符串格式；
- `backend/internal/service/account.go:1425`，`SupportsOpenAIImageCapability` 对 OpenAI OAuth/API-key 基本按账号类型返回 true；
- `backend/internal/service/smart_router_calibration_service.go:202`，没有显式能力元数据时直接补上 `image_generation` 和 `image_edit`；
- 当前校准筛选没有强制检查 `Schedulable`，也没有要求实际图片模型映射或显式图片能力声明。

### D3：旧账号兼容策略导致“无映射”语义过宽

相关位置：

- `backend/internal/service/account.go:752`，API-key 账号没有 `model_mapping` 时按兼容策略允许模型；
- `backend/internal/service/smart_router_adapter.go:651`，Smart Router 从 `model_mapping` 生成模型模式；
- `backend/internal/smartrouter/core/router.go:222` 附近，空模型模式/空能力集合保留向后兼容的默认允许行为。

这套默认开放行为不能直接全局改成默认拒绝，否则会破坏旧的兼容聚合账号和 compact。应新增图片专用候选判定，而不是修改普通模型或 compact 的默认语义。

### D4：客户端预检路径不属于后端调度层

证据是正式 POST 与错误 OPTIONS 的路径不同。后端 `/v1/images/edits` 入口在：

- `backend/internal/handler/endpoint.go:23-24`；
- `backend/internal/handler/openai_images.go` 的图片入口和 failover 主循环。

这里不应添加“收到错误 OPTIONS 就切上游”的逻辑；应在客户端/Alchemy V3 的 URL 组装处保证 Base URL 只出现一次 `/v1`，并加端到端路径断言。

### D5：部分异步图片适配器也直接使用原始 Base URL

例如 `backend/internal/service/openai_images.go:828` 附近的 AIAI 任务轮询直接在账号 Base URL 后追加任务路径。完整 endpoint 被误填时，同样可能生成错误轮询 URL。因此 Base URL 规范化必须是共享组件，不能只修 `/v1/images/edits`。

### D6：健康与弹性等待维度目前没有包含模型/尺寸/适配器

这是当前不是主因、但在新增线路和尺寸专用线路后必须处理的 P2 风险：

- `backend/internal/smartrouter/core/health.go:72` 的健康键按 `lane + capability + model family` 保存；
- `backend/internal/smartrouter/core/adaptive.go:67` 附近的超时 profile 按 `lane + capability` 保存；
- `backend/internal/service/smart_router_calibration_service.go:274` 将图片校准统一记录为 `model_family=gpt-image`。

如果同一 lane 以后同时承载不同模型、1K/2K/4K 或不同上游适配器，一个尺寸/模型的慢或失败样本可能影响另一个请求。当前主要使用 `gpt-image-2`，所以这不是账号 100 这次 404 的根因；但 YeToken 超分等尺寸专用线路已经使这个边界变得真实。

后续应增加可选的 `model_family + size_tier + adapter_profile` 维度，或至少让图片尺寸专用 lane 拥有独立延迟/健康 profile。该改动必须只扩展图片键，不能把 compact 的键迁移到图片维度。

## 4. 修正设计

### 4.1 新增统一 Base URL 规范化组件

新增一个只负责 OpenAI-compatible 账号的纯函数模块，例如：

```text
backend/internal/service/openai_base_url.go
```

职责：

1. 校验 scheme/host/allowlist，沿用现有安全校验；
2. 去掉末尾 `/`；
3. 识别并拒绝或安全剥离以下“完整 endpoint”后缀：
   - `/v1/images/generations`
   - `/v1/images/edits`
   - `/v1/responses`
   - `/v1/responses/compact`
   - `/v1/chat/completions`
   - `/v1/embeddings`
   - `/v1/models`
4. 保留供应商自定义版本根路径，例如 `/api/v1`、`/api/coding/v3`、`/v4`；
5. 对管理员保存请求返回明确错误，提示“请填 API 根地址”，而不是让错误账号进入 active；
6. 对历史脏数据在请求时做防御性归一化或快速失败，不能静默拼出双 endpoint。

推荐策略：

- 管理员保存时：检测到完整 endpoint，直接返回可读的 400，并显示规范化建议；
- 运行时：再次调用同一纯函数。历史脏数据可进入“兼容归一化”路径，但必须记录一次 `invalid_base_url_endpoint_suffix`，避免悄悄掩盖配置问题；
- 绝不把 `/v1/images/generations` 当成普通 base URL 继续拼接。

必须覆盖的 URL 结果：

| 输入 | 生成 | 编辑 | Responses | compact |
|---|---|---|---|---|
| `https://host` | `https://host/v1/images/generations` | `https://host/v1/images/edits` | `https://host/v1/responses` | `https://host/v1/responses/compact` |
| `https://host/v1` | 同上 | 同上 | 同上 | 同上 |
| `https://host/v1/images/generations` | 保持可诊断，不产生重复路径 | 必须得到 `/v1/images/edits` 或保存时拒绝 | 不得产生 images 路径 | 不得产生 images 路径 |
| `https://host/api/coding/v3` | 保留 `/api/coding/v3`，按该供应商的既有 endpoint 规则拼接 | 不改变现有 Ark 适配 | 不改变现有 Responses 规则 | 不改变 compact 规则 |

### 4.2 新增图片专用候选判定，不改普通能力默认值

新增类似 `IsOpenAIImageRouteCandidate(account, requestedModel, capability)` 的纯判定，顺序如下：

1. 账号必须 active 且 `Schedulable=true`；
2. 账号不能处于运行时临时退避；
3. 必须命中请求模型映射，或有显式 `extra.smart_router.capabilities` 图片声明；
4. 明确声明只有 `image_generation` 的账号不能进入 `image_edit`；
5. 有 `image_size_tiers` 的账号只在匹配尺寸时进入；
6. 通过供应商适配器注册的特殊账号可以提供显式能力声明，但不能靠账号名称猜测。

注意：不能简单把 `SupportsOpenAIImageCapability` 改成“没有声明就 false”。那会改变历史 OAuth/API-key 账号和 compact 的兼容行为。图片校准和图片调度应使用新的图片专用 predicate；普通 `IsModelSupported`、Responses 和 compact 路径保持现状。

### 4.3 修正 04:00 校准：恢复探测而非普通健康检查

`imageCalibrationLanes` 应改为两步：先从账本构造恢复候选，再执行有界探测。

候选范围：

- 包含正常线路，也包含 Smart Router 之前因失败被软降权、临时退避或排到 30/31/32 等低优先级的线路；
- 必须是 active、未删除、未被管理员明确关闭的逻辑账号；
- 不把历史 `schedulable=false` 直接当成永久排除条件：如果该状态是旧版本因失败写入的临时退避，应由恢复探测专门解除；
- 明确的人工禁用、余额耗尽、认证失效或账号已删除，仍然跳过，不由 04:00 自动打开；
- 经过图片专用候选判定，并保留显式 `image_generation` / `image_edit` 声明；
- 不为普通聊天/代码账号自动补图片能力。

每条恢复候选的处理：

1. 先读取图片账本，判断该线路是只需测文生图，还是文生图与图生图都要测；对已知两种能力表现不同的线路必须分别测试。
2. 04:00 探测绕过“当前有效优先级排序”，但仍遵守单线路并发上限和 source group 总并发上限，不能让恢复任务打爆同一上游。
3. 文生图或图生图成功：清除 Router 产生的临时退避/失败计数，恢复到该线路保存的原始有效优先级，并写入 `recovered_at`、探测类型和结果。
4. 仍然失败：保留原始优先级快照不变，继续使用降权后的有效优先级，更新 `next_probe_at` 和失败计数；不永久关闭账号。
5. 只记录 `skipped_reason` 的人工禁用、认证失效、余额耗尽、无图片映射、模型不匹配和尺寸不匹配，避免把“没有测试”误写成“线路失败”。

因此，“当前可调度”只能作为普通请求的筛选条件，不能作为 04:00 恢复探测的唯一条件。

### 4.4 修正图生图校准 fixture

在 `smartRouterCalibrationRequest` 中不要使用默认 `CreateFormFile` 的 MIME。应显式创建：

```text
Content-Disposition: form-data; name="image"; filename="smart-router-probe.png"
Content-Type: image/png
```

并保留有效的最小 PNG。测试必须断言：

- multipart 文件 part 的 MIME 是 `image/png`；
- 生成数据 URL 时 MIME 仍是 `image/png`；
- 探针请求不把 `application/octet-stream` 当成真实上游能力失败；
- 对明确拒绝编辑的上游，结果进入 capability-specific failure，而不是普通聊天失败。

### 4.5 增加普通链路恢复校准

Smart Router 的软降权不仅适用于图片。普通聊天/Responses 请求也会经过线路评分、健康记录、失败退避和有限切线，因此 04:00 应增加独立的普通链路恢复探测。

探测范围：

- 只探测账本中曾被 Smart Router 降权、临时退避或连续失败的普通链路；正常线路由真实业务流量持续提供健康信号，不必每天额外消耗一次探针。
- 按实际使用的协议分别探测 `responses` 和 `chat/completions`；不能用一个端点的成功替代另一个端点的成功。
- 按实际模型映射选择最小可用模型请求；模型不支持、无映射或账号明确不提供该协议的线路跳过并记录原因。
- 探针必须是无副作用、低 token、短输出的请求，不能触发工具调用、外部写入或用户可见任务。
- compact 仍然使用独立的 `responses_compact` 探针和独立恢复逻辑；普通 Responses 成功不能恢复 compact，compact 成功也不能恢复普通 Responses。

恢复规则：

1. 普通链路探测成功，只恢复对应的普通能力 lane；同一账号的图片或 compact 降权状态保持不变。
2. 普通链路探测失败，继续保留该 lane 的动态降权，更新 `next_probe_at`，不永久关闭账号。
3. 429 按 `Retry-After` 和退避窗口处理；502/503/504、连接断开和超时视为暂时失败；确定性的 400/错误端点配置进入配置错误状态，不把无效重试当成健康探测。
4. 普通链路恢复也要遵守账号并发、source group 并发和全局 04:00 校准预算，避免校准本身制造新的限流。

因此，04:00 应实现为“按能力恢复的统一校准框架”，而不是单独的图片校准任务：

```text
responses/chat  -> 普通对话恢复探测
images/generations -> 文生图恢复探测
images/edits -> 图生图恢复探测
responses/compact -> 独立压缩恢复探测
```

四类探测共享任务编排、账本和软降权原则，但不共享健康状态、失败计数、恢复结果或切线预算。

### 4.6 compact 的降权与恢复

compact 也属于 Smart Router 管理范围，但它必须使用独立的 `responses_compact` lane：

- compact 的 502/503/504、连接断开、上游超时和可确认的临时 429，可以触发 compact lane 自己的软降权；不能修改同账号普通 Responses、图片或图生图的优先级。
- 软降权后仍保留该账号作为低优先级 compact 兜底，不把账号永久关闭，也不把 compact 账号从数据库删除。
- 04:00 恢复探测必须真正调用 `/responses/compact`，并验证返回了可解析且有效的 `encrypted_content`；HTTP 200、普通文本或只有 usage 都不算成功。
- compact 校准探针本身必须显式标记为 HTTP 入站请求；不能继承账号的 Responses WebSocket 开关。否则 WS-enabled API-key 账号会把校准请求错误发成 WebSocket，并将 404/1011 握手失败误记为 compact 不可用。
- compact 探测成功，只恢复 `responses_compact` lane 的原始有效优先级；失败则继续保持降权，等待下一次恢复窗口。
- `force_off`、人工禁用、删除、认证失效和确定性的端点/参数配置错误不由成功探针自动打开；需要配置修正或人工确认。
- 客户端主动 `context canceled` 不应触发 compact 降权，也不能因此重放压缩请求。

当前运行态的临时 quarantine 可以作为过渡，但正式实现应统一记录 `base_priority`、`effective_priority`、`penalty_reason`、`next_probe_at` 和 `capability=responses_compact`，避免把临时退避误当永久不可用。

### 4.7 失败分类与调度边界

保持以下分类：

| 失败 | 处理 |
|---|---|
| 双 endpoint / invalid URL | 配置错误，快速失败并告警；不把 404 全局改成重试 |
| 400 参数/MIME/模型不支持 | 当前 lane 不适配；只有结构化、可确认是 lane 兼容性错误时才跳过 |
| 403/408/429/5xx/transport timeout | 图片能力范围内临时失败，软降权、短退避、按剩余预算切线 |
| client context canceled | 视为客户端取消，不惩罚、不重复生成 |
| compact `encrypted_content` 缺失 | 只影响 `responses_compact` lane，不影响普通 responses 或图片 |

### 4.8 尺寸/模型维度的后续加固

第一阶段先修正端点和候选集合，不改变现有账本 schema。第二阶段再为图片 profile 增加可选维度：

- `model_family`：至少区分 `gpt-image-2` 与后续新模型；
- `size_tier`：区分默认尺寸、1K、2K、4K；
- `adapter_profile`：区分原生 Images、Responses image tool、AIAI/Ark 等转换路径。

旧记录在缺少新维度时归入 `default`，不影响恢复；compact 继续使用自己的 `responses_compact` 键。

### 4.9 新线路自动接入

新账号或现有账号发生 base URL、模型映射、能力声明、分组或启用状态变化时，Smart Router 应自动触发一次 enrollment：

- 先校验 URL、映射和能力元数据，再进入 `pending_verification`；
- 按声明能力自动选择探针，不需要为新供应商写代码；
- 生图线路默认测 `image_generation`，账本或声明表明文生图/图生图可能不同则同时测 `image_edit`；
- 普通 Responses、`chat/completions` 和 compact 各自独立验证；
- 验证成功后自动清理旧的临时降权，恢复保存的原始价格优先级；
- 暂时失败则进入 `degraded`，分配下一个 `30/31/32...` 恢复槽位，仍保留低优先级兜底；
- 人工禁用、删除、认证失效、余额耗尽和确定性配置错误只记录原因，不自动打开。

推荐使用“账号变更事件 + 定时 reconciliation”双保险：事件保证新线路快速生效，定时扫描负责修复事件丢失、旧版本导入和手工数据库变更。新线路的自动接入不能改变 compact 或其他能力已有的健康状态。

## 5. compact 保护清单

本次修正必须增加回归测试，而不是依赖人工观察：

1. `https://host/v1` 仍生成 `/v1/responses/compact`；
2. compact 仍强制 JSON `Accept`，不被图片 multipart 逻辑污染；
3. compact 的失败只写 `responses_compact` 健康键；
4. 图片 404/403/5xx 不改变同账号普通 responses/compact 的健康键；
5. compact failover 默认按候选数量动态尝试，每条候选最多一次；设置正数
   `max_attempts_compact` 时仍作为显式上限；
6. 04:00 图片候选修正不会删除或跳过明确允许 compact 的账号；
7. 真实 compact 响应必须包含非空 `encrypted_content` 才算成功。

## 6. 测试矩阵

### 单元测试

- Base URL 根地址、`/v1`、`/api/v1`、`/api/coding/v3`；
- 完整 generations、edits、responses、compact、chat、embeddings endpoint 的拒绝/归一化；
- account 100 类型 URL 不再生成双 endpoint；
- AIAI 轮询 URL 使用规范化 base；
- 图片候选 predicate 对 active/schedulable、model mapping、capability、edit、size tier 的组合；
- 普通聊天账号、无图片映射账号、图片显式声明账号的校准筛选；
- 04:00 校准包含 Router 临时降权的账号，但排除人工永久关闭、删除、认证失效和余额耗尽账号；
- 恢复成功会清除 Router 临时降权并恢复原始优先级，恢复失败不会永久关闭；
- 04:00 恢复探测绕过有效优先级排序，但遵守线路和 source group 并发上限；
- 新发生的降权线路按同一 group + capability 内的 `30、31、32...` 顺序分配恢复优先级；
- 恢复成功释放该恢复槽位，恢复失败继续保留原恢复优先级；
- 普通 Responses 与 chat/completions 被降权后能够独立恢复；普通聊天成功不会恢复图片或 compact；
- 04:00 校准使用低 token、无副作用的普通链路探针，不触发工具调用或外部写入；
- fixture 文件 part 显式为 `image/png`；
- 404 配置错误不触发 failover，不写图片健康惩罚；
- 502/503/timeout 仍触发图片软降权和切线。

### compact 回归

至少运行现有 compact 测试集，并新增：

- Base URL 规范化后的 `/responses/compact` URL；
- compact 独立 lane 健康键；
- compact 失败不改变 image/chat 健康键；
- 真实 `encrypted_content` 判定保持不变。

### aiself 只读验收后再改配置

1. 先备份 account 100 的整行（凭据字段脱敏备份）和运行配置；
2. 只把 account 100 Base URL 改为 `https://api.nbility.dev/v1`；
3. 先测一次 `/v1/images/generations`；
4. 再测一次 `/v1/images/edits`；
5. 验证上游收到的路径分别是 `/v1/images/generations` 和 `/v1/images/edits`；
6. 检查账本只新增对应 lane/capability 记录；
7. 验证 compact 和普通 responses 的日志、健康状态、模型映射没有变化。

## 7. 实施顺序与回滚

1. 先实现纯函数 URL 规范化和测试；
2. 再实现图片候选 predicate 和校准候选收窄；
3. 再修正 PNG fixture；
4. 跑图片、Smart Router、compact 定向测试和全量 Go 测试；
5. 先在本地或隔离实例验证请求路径；
6. 线上先做数据库备份和只读快照，再配置修正；
7. 配置修正优先使用后台/SQL 热更新，不重新拉镜像；代码修正才进入后续镜像升级流程；
8. 保留旧镜像/旧配置，可按账号行和配置备份回滚。

## 8. 本轮结论

当前最直接的生产失败由 account 100 的错误 `base_url` 造成；同时 Smart Router 校准器存在确定的候选筛选缺陷，导致 04:00 探测了不应探测的账号。校准 fixture 的默认 MIME 还会给部分聚合上游制造图生图假阴性。

本规格的修正不要求把所有 404 改成 failover，也不要求改变 compact 的协议判定。正确做法是：先阻止错误端点进入运行态，再用按能力拆分的恢复校准统一处理普通聊天、图片生成、图生图和 compact；每个 lane 都只做软降权、保留低优先级兜底，并用独立回归证明 compact 行为未变。
