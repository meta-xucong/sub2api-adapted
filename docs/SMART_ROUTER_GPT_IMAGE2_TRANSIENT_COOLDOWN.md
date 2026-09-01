# gpt-image-2 临时冷却与粘性绕过开发文档

## 1. 目的与范围

本变更只针对 `gpt-image-2` 的 Smart Router 调度，处理 Yetoken 等上游的间歇性 502、504、连接断开和超时。目标是减少新请求继续命中故障 lane，同时保留账号在上游恢复后的自动回流能力。

不修改：

- `generations` / `edits` 的协议选择；
- 请求超时、重试次数或重试顺序；
- 其他图片模型、文本模型和视频链路；
- 账号的持久化优先级、启用状态或人工开关；
- 额外的健康探测或付费调用。

## 2. 现状与问题

Smart Router 已按 `(lane, capability, model)` 保存健康状态，并有 `cooling`、`probe_due`、`warming` 和恢复槽位。问题在于：

1. 普通会话粘性路径可能在 Smart Router 之前直接返回固定账号；
2. 冷却 lane 在存在健康同能力候选时仍保留为候选，导致新请求有机会再次命中它。

这会让已进入冷却的 `gpt-image-2 + generations` 继续承接新任务，放大上游故障窗口。

## 3. 最小改动方案

### 3.1 调度前过滤

在 `internal/smartrouter/core.Order` 中，仅当请求模型为 `gpt-image-2` 且 capability 为 `image_generation` 或 `image_edit` 时：

- 如果存在至少一个非冷却 lane，暂时跳过 `RecoveryCooling` 且 `CooldownUntilUnix` 未到期的 lane；
- 如果所有候选都在冷却，保留全部 lane，继续使用现有最低权重兜底逻辑。

被跳过的 lane 只写入内存计划原因 `gpt_image2_health_cooldown`，不修改数据库账号状态。

### 3.2 会话粘性绕过

在普通 session sticky 返回账号前，检查该账号对应的 `gpt-image-2 + capability` 健康状态：

- lane 正在 cooling：本次跳过粘性账号，进入现有 Smart Router/load-balance 选择；
- lane 未冷却：保持原有粘性行为；
- 原粘性绑定不删除，账号恢复后仍可自动复用。

该检查只影响 `gpt-image-2` 图片请求，不改变文本和其他模型的粘性策略。

## 4. 状态与失败处理

继续复用现有 HealthTracker：

- 400、invalid image、客户端取消：`no_penalty`；
- 502、504、EOF、连接断开、超时：进入已有临时冷却和恢复槽位；
- 冷却到期：由现有 `probe_due` / `warming` 机制恢复；
- 成功：按现有 warming 规则逐步恢复优先级；
- 不存在永久自动禁用。

本补丁不新增重试、不缩短超时、不发送健康探测，因此不会主动增加上游调用或计费。

## 5. 代码改动

- `backend/internal/smartrouter/core/router.go`
  - 增加 `gpt-image-2` 冷却 lane 的有健康候选过滤；
  - 全池冷却时保留最后兜底。
- `backend/internal/smartrouter/core/router_test.go`
  - 验证 gpt-image-2 有健康候选时跳过冷却 lane；
  - 验证全池冷却时仍保留候选；
  - 验证其他图片模型行为不变。
- `backend/internal/service/openai_account_scheduler.go`
  - 普通 session sticky 命中冷却的 gpt-image-2 lane 时，仅绕过本次粘性选择。
- `backend/internal/service/smart_router_scheduler_test.go`
  - 验证冷却的 gpt-image-2 粘性账号会切到健康账号，且原绑定不删除。

## 6. 验收标准

1. account A 的 `gpt-image-2 generations` 进入 cooling 后，新请求优先选择健康的 generation 账号；
2. account A 的 `gpt-image-2 edits` 不受 generation lane 冷却影响；
3. 所有候选冷却时仍可按原逻辑尝试，不提前返回失败；
4. cooldown 到期并成功后，账号按 warming 自动回流；
5. 单次偶发失败不会永久关闭账号；
6. 不产生额外探测调用，不改变超时和重试上限；
7. 非 `gpt-image-2` 路由测试保持原行为。
