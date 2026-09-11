# 隔离发布、灰度与回滚方案

## 1. 不可协商的隔离原则

1. 统一网关使用全新的 composite group，不修改现有 group 的 `models_list_config`、`model_routing`、默认模型、价格或账号关联。
2. 使用独立 API key/测试身份；不得把旧 key 改绑到新 group，也不得把新 key 复用为旧链路的生产 key。
3. 使用独立 feature gate，默认关闭；关闭时旧入口的路由、目录、调度和账务代码路径与基线一致。
4. 新 resolver 的候选池只来源于显式启用的 route/account binding；失败时在新池内返回可解释错误，不得跨 group fallback。
5. 新 endpoint 首先使用独立 hostname；如果最终选 path，必须验证反向代理、前端、健康检查和旧 `/v1` 路径互不影响。
6. 未经用户验收，不将统一入口设置为现有默认域名或旧 key 的默认模型入口。
7. 上游倍率 probe/manual policy 只由统一入口的新 lane/profile 读取；不得通过修改旧账号倍率、旧 group 价格或旧 key 绑定来实现手动计费。

统一链路必须通过三重门槛才可执行：全局 feature gate 开启、请求 key 显式绑定统一 composite group、candidate/route 显式属于该 group。统一入口请求缺任一门槛必须直接拒绝；只有原入口请求才继续走旧实现。任何 detector fallback、catalog cache、账号不可用降级或旧 group fallback 都不得绕过其中任一门槛。

## 2. 推荐 rollout 阶段

### Stage 0：文档验收

- 用户确认 public model 策略、route/account 绑定、计费 profile、Grok charge trigger 和 endpoint 隔离方式。
- 用户确认 Plus/Pro/生图等 Billing Lane、每个 lane 的账号池、倍率、cost model 和动态价格展示方式。
- 归档当前基线：旧 key 的 models、旧入口成功/失败样例、数据库 schema/migration 版本、容器 digest、配置摘要。
- 确认新功能停止条件和最大测试预算。

### Stage 1：本地/副本演练

- 在数据库副本或独立测试环境执行 migration dry-run。
- 只导入脱敏 lane/route/profile fixture；统一 group 的全局倍率固定为 1.0，验证 lane/user/provider 倍率不会重复计算。
- 使用一个 `manual_only` 的非 ChatGPT fixture 和一个 `probe_preferred` fixture，验证手动兜底、probe freshness、来源切换和 fail closed；不触碰旧 `accounts.rate_multiplier`。
- 演练订阅摊销、按次、图片、视频四种 cost model，以及预占释放。
- 验证目录、resolver、usage/billing snapshot 和 rollback，不连接真实付费上游。

### Stage 2：aiself 只读能力目录

- 继续使用现有线上数据只读生成 capability inventory。
- 不创建线上 route、不启用新 key、不发送图片/视频。
- 比较 group/model_mapping/schedulable 与 provider 能力，标出不适用或下架模型。

### Stage 3：独立 key 文本 canary

- 仅开放少量已确认价格和能力的文本模型。
- 先用同名 `gpt-5.5` 的 Plus/Pro 两个 lane 验证动态收费，再加入其他 provider。
- 每次测试保留 request id、route/account/pricing snapshot、usage log 和余额变化前后对账。
- 先验证单 provider，再验证同名模型多候选和候选故障切换。

### Stage 4：Grok 视频受控 canary

- 使用明确允许的 schedulable Grok route；保持 `schedulable=false` 账号排除。
- 单独预算、单独测试模型、单独停止条件。
- 先验证 KIE、Subrouter、直连来源作为不同 lane 时，分辨率/时长价格和来源倍率不会互相覆盖。
- 覆盖创建成功/完成成功、重复轮询、重复回调、超时、失败、结果 URL 缺失、价格变更后完成等情形。

### Stage 5：扩大候选与用户验收

- 增加 DeepSeek、Kimi、Doubao/Ark、Gemini 等已完成能力和价格验证的候选。
- 对账无差异、旧链路无回归、告警稳定后，提交用户验收。
- 只有用户明确确认，才讨论统一域名正式化；旧入口继续保留。

## 3. 变更前快照

实施前必须保存以下只读证据：

- 应用镜像 digest、Git commit、迁移版本、容器状态、compose/config 摘要。
- 所有既有 group、account、account_group、API key scope、model mapping、schedulable 状态的脱敏清单。
- 旧入口各类 key 的 `/v1/models` 快照和最小文本回归结果。
- 近 30 天按 group/model/request type 的 usage 与 charge 汇总。
- Grok pending/claimed media 数量、状态和未结算项。
- 数据库 schema checksum、Redis key namespace 检查结果和反向代理配置快照。

快照中不得包含明文凭据或完整 API key。

## 4. 回滚触发条件

满足任意条件即停止扩大并回滚新功能：

- 旧 key 的模型列表、响应、错误码、延迟或收费与基线不一致。
- 新入口出现 route/account/pricing snapshot 缺失或无法解释。
- 用户扣款与 usage log、provider 成本或预期 profile 不一致。
- manual-only 链路没有手动计价证据、probe fallback 来源不可追溯或出现 token 倍率污染非 token 计量。
- Grok 出现重复扣款、任务跨账号、重复 provider task 或 pending 泄漏。
- 统一入口请求落入旧 group 或旧 key 可见新模型。
- Redis/数据库出现未脱敏凭据、跨租户数据或不可回收任务。
- 上游错误率、限流、并发或资源占用影响现有入口。

## 5. 回滚步骤

1. 关闭统一 feature gate，停止新 key 的入口流量。
2. 禁用新 composite group、route candidates、pricing profiles 和反向代理入口；不修改旧 group/旧 key。
3. 禁用新 lane 的 rate policy 与 manual profile；保留 snapshot、审计和证据，不回滚或清空旧账号倍率。
4. 停止新入口的媒体轮询/回调消费，但保留已创建任务的只读状态和结算处理，防止遗留任务重复扣款。
5. 对未结算媒体任务按 snapshot 逐项核对：继续安全结算、按规则取消或人工挂起，禁止重新随机选路。
6. 恢复旧入口配置/路由快照；验证旧 key 的 models、文本、图片/视频（如原有支持）和余额无回归。
7. 保存新入口失败日志、usage、billing entry 和 snapshot，供审计；不删除证据。
8. 只有在根因和修复方案完成评审后，才允许再次启用新入口。

## 6. 回滚安全要求

- 禁止使用 `git reset --hard`、覆盖式还原数据库或删除用户账务作为回滚手段。
- 新表/新字段必须向后兼容旧版本；migration down/forward 演练在副本完成后才可线上执行。
- 回滚脚本只允许命中新建对象和新 namespace；对象范围必须有 manifest，不使用宽泛 glob。
- 任何会影响旧入口的配置差异必须由用户显式确认，并从“零干扰”验收中单独豁免；默认不允许。
