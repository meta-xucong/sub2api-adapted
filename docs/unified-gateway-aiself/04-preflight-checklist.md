# 开发前置清单

状态值：`DONE` 已有证据；`IN_PROGRESS` 正在形成材料；`PENDING` 进入开发前必须完成；`BLOCKED` 没有外部确认不得推进。当前清单只记录准备情况，不执行任何线上写操作。

## 1. 总清单

| ID | 检查项 | 当前状态 | 必须证据 | 责任 |
| --- | --- | --- | --- | --- |
| P-01 | 任务范围、硬约束和禁止动作冻结 | DONE | `00-task-record.md` | 主控/用户 |
| P-02 | aiself 健康、setup、认证边界基线 | DONE | `02` 第 2 节 | 主控 |
| P-03 | 当前应用 image digest、迁移版本、配置摘要重新取证 | PENDING | 只读运行快照 | 实施前主控 |
| P-04 | 候选源码 commit 和工作树状态冻结 | DONE | commit + status 记录 | 主控 |
| P-05 | 现有 group/account/model mapping/schedulable 清单导出 | IN_PROGRESS | 脱敏 inventory | 运营/主控 |
| P-06 | provider 能力逐模型验证 | PENDING | capability inventory + 时间戳 | provider owner |
| P-07 | 协议转换损失与 endpoint 支持矩阵 | PENDING | adapter capability matrix | 开发 |
| P-08 | route candidate 与账号池关系冻结 | PENDING | route catalog + account eligibility | 运营/开发 |
| P-09 | 每条 route 的价格来源和负责人 | PENDING | signed/approved pricing profiles | 财务/运营 |
| P-10 | token/image/video 计量单位和 charge trigger | PENDING | billing profile | 财务/开发 |
| P-10A | Plus/Pro/生图等 Billing Lane 目录 | PENDING | lane catalog + account membership | 运营/开发 |
| P-10B | lane/user/provider 三类倍率语义分离 | PENDING | multiplier policy + no-double-count test | 财务/开发 |
| P-10C | 订阅摊销、按次、图片、视频 cost model | PENDING | approved billing profiles | 财务/运营 |
| P-10D | QUOTED/RESERVED/CAPTURED/RELEASED 预占规则 | PENDING | reservation state matrix | 财务/开发 |
| P-10E | 每个 lane 的 `pricing_source_group_id` 映射 | PENDING | lane-to-source-group matrix | 运营/开发 |
| P-10F | 验证 account.rate_multiplier 不参与用户扣费 | PENDING | dual-cost billing test | 开发/QA |
| P-10G | 每条 lane 的 `upstream_rate_mode` 冻结 | PENDING | upstream-rate-policy-matrix | 运营/开发 |
| P-10H | 不支持自动探测的账号具备手动倍率/单位价格 | PENDING | manual rate policy + approval | 运营/财务 |
| P-10I | probe unsupported/failed/stale 的手动兜底优先级 | PENDING | rate-source precedence test | 开发/QA |
| P-10J | token、按次、图片、视频、provider-specific 计量边界 | PENDING | rate-basis matrix + profile | 财务/开发 |
| P-10K | 自动探测不会覆盖手动兜底且不会回写用户售价 | PENDING | audit/version test | 开发/QA |
| P-11 | 同名 public model 的多 provider 策略 | PENDING | 用户确认的决策记录 | 用户/产品 |
| P-12 | `schedulable=false` 账号排除规则 | DONE（规则） | 设计契约；实施需验证 | 主控 |
| P-13 | Grok media snapshot/claim/dedup 字段映射 | PENDING | media state diagram + fixture | 开发 |
| P-13A | durable `route_price_snapshot_id` 在转发前落库/可恢复 | PENDING | snapshot schema + crash recovery test | 开发 |
| P-13B | charge、usage、settlement 同事务或 durable outbox | PENDING | fault injection + reconciliation report | 开发 |
| P-13C | `CREATING/PENDING/SETTLING/SETTLED` 及失败终态冻结 | PENDING | state machine + transition matrix | 开发 |
| P-13D | status/content 无 model 时按 task binding 找回原账号 | PENDING | media endpoint trace | 开发 |
| P-14 | 新 group/key/feature gate 的隔离方式 | PENDING | rollout manifest | 运维/主控 |
| P-15 | 不改旧入口的反向代理/路由证明 | PENDING | config diff + endpoint regression | 运维 |
| P-16 | 数据库迁移向前/回滚演练 | PENDING | 副本演练日志 | 开发/运维 |
| P-17 | 旧 key/旧模型/旧计费无回归基线 | PENDING | regression report | QA |
| P-18 | 新 key 文本 canary 预算与限额 | PENDING | canary budget approval | 用户/运维 |
| P-19 | Grok 视频测试预算与停止条件 | PENDING | media budget approval | 用户/运维 |
| P-20 | 监控、告警、审计查询和敏感信息脱敏 | PENDING | observability checklist | 运维/安全 |
| P-21 | 回滚演练和恢复时间目标 | PENDING | rollback drill evidence | 运维 |
| P-21A | 目录闭包：展示模型均有可执行、有价格 target | PENDING | catalog closure report | 开发/QA |
| P-21B | detector/cache/fallback 不会跨 group 进入统一链路 | PENDING | route isolation test | QA |
| P-21C | 同名模型跨 lane 动态收费或 qualified alias 策略 | PENDING | model/price exposure decision | 产品/用户 |
| P-22 | 独立审计结论 | DONE（结果 BLOCKED） | `08-independent-audit-report.md` | 审计角色 |
| P-23 | 用户验收记录 | BLOCKED | 用户确认 | 用户 |

## 2. provider/模型能力取证清单

每个 candidate 需要独立填写，不允许用模型名称推断能力：

- [ ] provider identity、source channel、账号和 route。
- [ ] 文本非流式、流式、工具调用、视觉输入、结构化输出。
- [ ] 上游 endpoint、HTTP method、认证方式和错误码映射。
- [ ] 最大输入/输出、上下文、超时、并发和限流。
- [ ] usage 返回字段及 token 估算策略。
- [ ] 图片参数、分辨率和计费单位。
- [ ] 视频创建、状态查询、结果获取、回调、超时和取消语义。
- [ ] provider 下架/模型不可用时的目录撤销条件。
- [ ] 最近验证时间、验证请求 ID、验证预算和证据保存位置。

## 3. 账务前置清单

- [ ] 每条 route 至少有一个有效、版本化 pricing profile。
- [ ] profile 覆盖实际 upstream model、endpoint、计量单位和 charge trigger。
- [ ] 每条 lane 明确 `probe_preferred`、`manual_only` 或 `probe_only`，并有新鲜 probe 或审核过的手动规则。
- [ ] 火山引擎/Ark 等非 ChatGPT 计费链路填写手动倍率、手动单位价格或 provider-specific 公式；不能套用 ChatGPT token 倍率。
- [ ] probe 的 `unsupported`、`failed`、stale 和无配置状态不会被默认为 `1.0` 或免费。
- [ ] `manual_fallback_used`、倍率来源、探测 freshness 和最终单位价格会写入不可变 snapshot。
- [ ] 用户 charge 与 account/provider cost 的字段和报表分离。
- [ ] 重试、部分输出、上游已接受但未完成、取消、超时的处理策略已经冻结。
- [ ] 文本和媒体的 settlement idempotency key 定义完成。
- [ ] 价格缺失时 fail closed 的错误码和用户可见信息完成。
- [ ] 价格变更不回写历史 snapshot 的证明完成。
- [ ] 统一入口的余额/配额不足不会改变旧 group 的计费行为。

## 4. 安全与运维前置清单

- [ ] 文档、日志、快照和测试报告均无完整 API key、SSH 私钥、口令、cookie、credential JSON。
- [ ] 新 key 与旧 key 不共享误配置的 group。
- [ ] 新 feature gate 默认关闭，关闭后旧入口仍可用。
- [ ] route/profile 管理操作有审计、最小权限和变更人。
- [ ] Redis pending record 不存明文上游凭据。
- [ ] provider task id、video URL、错误体按敏感级别脱敏。
- [ ] 线上探测默认只读；付费测试必须有单独预算和停止条件。
- [ ] 回滚不删除旧数据、不重置用户余额、不重建现有 key。

## 5. 放行规则

P-03、P-06、P-08、P-09、P-10、P-10G、P-10H、P-10I、P-10J、P-10K、P-14、P-15、P-16、P-17、P-18、P-19、P-20、P-21、P-23 未满足时，不得把统一入口标记为生产可用。任何一项只有口头确认而没有证据，仍保持 `PENDING` 或 `BLOCKED`。
