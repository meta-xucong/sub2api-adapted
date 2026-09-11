# aiself 统一上游网关：开发前设计与验收包

状态：`BACKEND_ADMIN_PLANE_IMPLEMENTED / FRONTEND_ADMIN_PLANE_IMPLEMENTED / FINAL_AUDIT_PASS_WITH_CONDITIONS / PRODUCTION_GATE_DISABLED`  
基线日期：2026-09-10  
目标：在不干扰现有链路的前提下，为 aiself 准备一个统一入口，使多个 provider 的可用模型可由同一 API 暴露，同时按实际选中的 provider、账号、模型和计费规则结算。

本包的设计阶段和本轮候选实现都没有修改线上系统；候选代码树已加入隔离的统一网关内核、受 gate 保护的 admin 管理面和本地测试，但没有注册生产 `/v1` 统一运行时、执行线上迁移或修改现有链路。实现边界和证据见第 14、15 章。

## 阅读顺序

1. [00-task-record.md](./00-task-record.md)：范围、硬约束、角色和放行门槛。
2. [01-development-spec.md](./01-development-spec.md)：目标架构、请求流、模型目录、协议适配和实施分层。
3. [02-aiself-observed-baseline-20260910.md](./02-aiself-observed-baseline-20260910.md)：aiself 当前真实结构与证据边界。
4. [03-billing-contract.md](./03-billing-contract.md)：路由级计费、价格快照、Grok 视频结算和幂等契约。
5. [04-preflight-checklist.md](./04-preflight-checklist.md)：进入编码前必须完成的清单。
6. [05-isolation-rollout-and-rollback.md](./05-isolation-rollout-and-rollback.md)：零干扰发布、灰度与回滚方案。
7. [06-test-and-acceptance-matrix.md](./06-test-and-acceptance-matrix.md)：测试、证据和验收矩阵。
8. [07-implementation-preflight-artifacts.md](./07-implementation-preflight-artifacts.md)：开发前要生成/归档的材料目录。
9. [08-independent-audit-report.md](./08-independent-audit-report.md)：独立审计记录，待审计完成后填写结论。
10. [09-decisions-and-open-questions.md](./09-decisions-and-open-questions.md)：推荐决策、风险和需要用户确认的事项。
11. [10-read-only-probe-and-evidence-plan.md](./10-read-only-probe-and-evidence-plan.md)：后续实施前可重复的只读探测与证据规则。
12. [11-integrity-manifest.md](./11-integrity-manifest.md)：设计包文件完整性清单。
13. [12-final-executable-plan.md](./12-final-executable-plan.md)：将 Plus/Pro/生图等计费子组纳入统一 API 的最终可执行方案。
14. [13-upstream-rate-and-manual-fallback.md](./13-upstream-rate-and-manual-fallback.md)：自动探测、手动倍率/单位价格、非 ChatGPT provider 与兜底规则。
15. [14-local-unified-gateway-implementation.md](./14-local-unified-gateway-implementation.md)：本地统一 API 创建、快照状态机、SQL 仓储和模拟验收结果。
16. [15-frontend-unified-gateway-development.md](./15-frontend-unified-gateway-development.md)：统一 API 管理前端、管理 API 契约、倍率表单、权限开关、测试和编码前独立审计结论。

模板位于 [templates](./templates/)：能力清单、Billing Lane 目录、路由目录、价格 profile、上游倍率策略矩阵、回滚清单、冒烟报告和 secret 检查清单。模板不含任何真实凭据。

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
- 源码实现：候选树已完成本地实现；后端独立审计与前端独立审计均为 `PASS_WITH_CONDITIONS`。条件包括 PostgreSQL 真实迁移验证、环境/基线全量测试限制、真实 provider/runtime/settlement/Grok 恢复、race、灰度与回滚证据。
- 数据库/线上变更：未发生。
- GitHub 发布：本阶段不执行。
- 本轮只落地并测试隔离的管理面/配置面和本地模拟能力；真实 provider adapter、durable runtime settlement、统一目录运行时、Grok 异步生产绑定和真实 lane 仍未放行，统一入口保持关闭。
- 前端/后端开发文档：初审发现项已修复并完成独立复审；候选管理面定向测试、前端全量测试和构建已通过，production gate 仍保持关闭。
