# Veyra Video OS 门户入口恢复记录（2026-10-11）

## 目标与基线

本次只恢复 AISelf Veyra 门户中的 Video OS 入口和既有身份/usage 桥接，不重写 Video OS，不新增计费账本，也不修改 Provider 配置。

- 基线：候选 Sub2API 提交 `c121ab03b5b2b72ca314bbb90cd4b778527cb0cd`。
- 功能参考：历史补丁 `c56179836c08058ef6cd9ea68a66f0d5809b40cf`（Video OS portal bridge）。
- 集成原则：保留基线的 `Veyra.Enabled && Veyra.PortalEnabled` 门禁、Portal Origin/现有身份安全边界和 Alchemy V2 跳转；只移植恢复 Video OS 必需的差异。

## 恢复的用户路径

1. 桌面和移动门户展示 Video OS 卡片；入口仍通过本站 `/_veyra/return?target=video` 路由。
2. 已登录时，前端向 Sub2API 请求带 `intent=video` 的短时、单次 ticket；未登录时回到本站登录页并保留 return target。
3. 浏览器通过隐藏 POST 表单把 ticket 发送到固定 HTTPS `https://video.aiself.vip/auth/veyra/callback`，不把 ticket 放入 URL。
4. Video OS 现有 identity adapter 的 exchange 请求只包含 `{ticket}`。Sub2API 从票据本身返回原始 intent；不把缺失 intent 错当作 Alchemy。
5. Exchange 只允许消费 `alchemy` 或 `video` ticket。若请求显式给出 intent，则存储层先校验；不允许 intent 的票据在消费前被拒绝。Redis 仍使用现有单次 claim。

## 身份与计费边界

- Video OS 登录仍由现有 Veyra ticket 机制完成，ticket TTL、Redis 存储和单次消费模型沿用现有设计。
- 生产入口继续受 `veyra.enabled` 与 `veyra.portal_enabled` 双开关保护；内部 exchange/usage/debit API 继续受内部 token 保护。
- Video OS usage endpoint 只读查询现有 `usage_logs`，按 user ID 和 request ID 精确匹配并返回已结算 `model`、`actual_cost`。不写新账、不触发扣款；service charge 仍由既有 billing debit/idempotency 路径负责，不能从 usage 查询重复扣费。
- 门户跳转 URL 固定 allowlist 为各自的 AISelf HTTPS 域名，不允许任意外部重定向。

## 代码/测试覆盖

- 新增桌面/移动 Video OS 卡片与受控路由；保留 Alchemy 同源 return URL 与 `/go/v2` 桌面跳转。
- Portal config 暴露固定校验后的 Video OS URL；配置缺失或不可信时前端拒绝启动跳转。
- 新增 `{ticket}` 视频 exchange 回归测试，以及错误显式 intent 不消费票据、错误应用类型不被消费的测试。
- 新增 Redis `ConsumeAllowed` 负例：不允许的 intent 返回拒绝，随后合法 allowed intent 仍可兑换同一票据。
- 新增 usage 查询过滤、无效/未结算事实拒绝测试；该实现复用既有 `usage_logs`。

## 验收边界与生产切换门

代码测试通过不代表线上页面或桥接已经恢复。Sub2API 将 Portal 静态资源嵌入服务二进制/镜像，必须构建并发布包含本次文件的镜像后，入口才会在站点出现。生产切换前还需核实镜像 SHA、配置双开关、Portal 的 POST callback、同一用户登录、Redis ticket 一次性、usage request ID 映射和已结算费用展示；任何未知费用、票据或账单问题都不得靠重复调用 Provider 验证。

本记录创建时：没有合并、没有部署、没有调用真实 Provider、没有变更生产配置/数据库。独立本地测试结果以本提交交接记录为准；真实浏览器/生产桥接和实际计费仍未验收。
