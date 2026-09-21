# aiself 统一上游网关：设计、实现与验收包

状态：`LOCAL_RUNTIME_IMPLEMENTED / DURABLE_RECOVERY_WIRED / CAPABILITY_MATRIX_FAIL_CLOSED / FRONTEND_IMPLEMENTED / LOCAL_AUDIT_PASS_WITH_STAGING_GATES / PRODUCTION_GATE_DISABLED`  
基线日期：2026-09-18  
目标：在不干扰现有链路的前提下，为 aiself 准备一个统一入口，使多个 provider 的可用模型可由同一 API 暴露，同时按实际选中的 provider、账号、模型和计费规则结算。

本包的设计阶段和本轮候选实现都没有修改线上系统；候选代码树已加入隔离的统一网关内核、受 gate 保护的 admin 管理面、provider 适配和本地测试。统一运行时只注册在 `/unified/v1`，不替换生产 `/v1`；没有执行线上迁移、真实付费调用或修改现有链路。实现边界和证据见第 14、15、16 章。

## 阅读顺序

1. [00-task-record.md](./00-task-record.md)：范围、硬约束、角色和放行门槛。
2. [01-development-spec.md](./01-development-spec.md)：目标架构、请求流、模型目录、协议适配和实施分层。
3. [02-aiself-observed-baseline-20260910.md](./02-aiself-observed-baseline-20260910.md)：aiself 当前真实结构与证据边界。
4. [03-billing-contract.md](./03-billing-contract.md)：路由级计费、价格快照、Grok 视频结算和幂等契约。
5. [04-preflight-checklist.md](./04-preflight-checklist.md)：进入编码前必须完成的清单。
6. [05-isolation-rollout-and-rollback.md](./05-isolation-rollout-and-rollback.md)：零干扰发布、灰度与回滚方案。
7. [06-test-and-acceptance-matrix.md](./06-test-and-acceptance-matrix.md)：测试、证据和验收矩阵。
8. [07-implementation-preflight-artifacts.md](./07-implementation-preflight-artifacts.md)：开发前要生成/归档的材料目录。
9. [08-independent-audit-report.md](./08-independent-audit-report.md)：历史独立审计记录、修复项与条件性结论。
10. [09-decisions-and-open-questions.md](./09-decisions-and-open-questions.md)：推荐决策、风险和需要用户确认的事项。
11. [10-read-only-probe-and-evidence-plan.md](./10-read-only-probe-and-evidence-plan.md)：后续实施前可重复的只读探测与证据规则。
12. [11-integrity-manifest.md](./11-integrity-manifest.md)：设计包文件完整性清单。
13. [12-final-executable-plan.md](./12-final-executable-plan.md)：将 Plus/Pro/生图等计费子组纳入统一 API 的最终可执行方案。
14. [13-upstream-rate-and-manual-fallback.md](./13-upstream-rate-and-manual-fallback.md)：自动探测、手动倍率/单位价格、非 ChatGPT provider 与兜底规则。
15. [14-local-unified-gateway-implementation.md](./14-local-unified-gateway-implementation.md)：本地统一 API 创建、快照状态机、SQL 仓储和模拟验收结果。
16. [15-frontend-unified-gateway-development.md](./15-frontend-unified-gateway-development.md)：统一 API 管理前端、管理 API 契约、倍率表单、权限开关、测试和编码前独立审计结论。
17. [16-runtime-implementation-and-final-audit.md](./16-runtime-implementation-and-final-audit.md)：当前候选运行时、provider 适配矩阵、结算闭环、测试证据和最终生产门禁。
18. [17-minimal-unified-group-development-plan.md](./17-minimal-unified-group-development-plan.md)：面向内部使用的最小化统一 Group/API key、模型目录、固定优先级和线路计费方案；本章优先于早期完整平台路线。
19. [18-minimal-unified-group-audit.md](./18-minimal-unified-group-audit.md)：对本次最小化范围、现有能力复用、必要缺口、禁止过度开发和验收门槛的独立审计结论。
20. [20-aiself-local-snapshot-and-claude-probe-20260919.md](./20-aiself-local-snapshot-and-claude-probe-20260919.md)：aiself 最新完整本地快照、Claude 分组探测、OpenAI↔Anthropic 适配结论和本地验证证据。
21. [21-local-unified-api-key-and-live-probe-20260919.md](./21-local-unified-api-key-and-live-probe-20260919.md)：本地统一测试用户/API Key、全量模型路由物化、Provider 代表链路探测、价格与隔离审计记录。
22. [22-p0-commercial-readiness-development-plan.md](./22-p0-commercial-readiness-development-plan.md)：只做前三条 P0 的商业化收口开发方案，冻结模型目录、管理员极简配置、计费可解释性和非干扰边界。
23. [23-p0-commercial-readiness-audit-checklist.md](./23-p0-commercial-readiness-audit-checklist.md)：P0 独立审计清单、端到端矩阵、非干扰回归和最终放行模板。

模板位于 [templates](./templates/)：能力清单、Billing Lane 目录、路由目录、价格 profile、上游倍率策略矩阵、回滚清单、冒烟报告和 secret 检查清单。模板不含任何真实凭据。

> 当前阶段优先级：第 22、23 章是“只做 P0-A/B/C”的下一阶段唯一实施与审计依据；P0-D/E/F（Key 安全运营、健康告警、备份回滚/商业安全）明确暂缓；第 17、18 章是内部统一 API 的上一阶段范围和审计依据；第 01–16 章中的完整 Wokey/KIE 平台能力属于历史设计或未来路线，除非第 22 章明确保留，否则不得在本阶段实现。

## 证据标记

- `[OBSERVED]`：本轮对 aiself 或候选源码直接观察到的事实。
- `[INFERRED]`：由源码和运行数据推导出的结论，开发前仍需用专项测试确认。
- `[PROPOSED]`：本包推荐的未来设计，不代表已经部署。
- `[TO-VERIFY]`：进入实现前必须补齐的证据。
- `[ACCEPTANCE]`：需要用户确认或验收的门槛。

## 核心安全结论

统一入口必须使用独立的 composite group、独立 API key/测试身份和 feature gate。旧 group、旧 key、旧账号池、旧模型目录和旧计费路径不得被统一入口隐式接管；新入口也不得失败回退到旧 group。未知价格、未知能力或没有可调度账号时应 fail closed，而不是用一个“默认统一价”继续放行。

统一入口的用户计费硬规则是：只有请求成功完成或成功交付时才按实际选中的 lane/provider/account/profile 收费；失败、取消、超时、过期、无有效结果或未成功交付均不扣用户费。预占必须释放；provider 已产生的内部成本另行记入成本账，不得转成用户 charge。该规则不改变现有旧入口的既有计费语义。

倍率来源也必须按链路独立处理：支持兼容 billing probe 的链路可以自动探测；不支持的火山引擎/Ark 等链路必须填写版本化手动倍率、单位价格或 provider-specific 规则。探测失败且没有手动兜底时不得发送上游；`accounts.rate_multiplier` 仍是内部账号成本口径，不是统一入口的直接用户售价。

## 当前放行状态

- 结构探测：已完成，见第 02 章。
- 方案设计：Revision 4 已完成；在 Revision 3 基础上冻结 admin CRUD、现有 config 草稿编辑、价格导入、双 revision CAS、部分索引约束和前端 step-up/生命周期操作。
- 源码实现：候选树已完成隔离的本地 runtime、provider adapter、能力矩阵、durable recovery worker 接入、结算状态机和前端管理面；主控审计与独立只读复核已通过代码级门禁。剩余条件包括 PostgreSQL 真实迁移验证、真实 provider/runtime/settlement/Grok 证据、race、灰度与回滚证据。
- 数据库/线上变更：未发生。
- GitHub 发布：本阶段不执行。
- 本轮已把隔离 runtime、durable snapshot/ledger/recovery 代码、OpenAI/CN/Ark/Grok/Gemini/Antigravity 适配和能力校验接入候选 server graph；真实 provider、PostgreSQL 事务和 Grok 生产恢复仍未放行，统一入口保持关闭。
- 本轮 v1.1 前端全量 257 个测试文件/1883 个测试、typecheck、production build，以及后端统一网关 service/handler/routes 回归均已通过；本地代码审计通过，真实 provider、staging/生产证据仍是放行条件。
