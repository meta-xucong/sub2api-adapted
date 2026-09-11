# 开发前材料目录与归档规则

本文件定义下一阶段真正开始写代码前需要准备的材料。材料应存放在受控目录，脱敏后才可进入仓库或发送给审计者。当前只提供模板，不生成线上真实配置。

## 1. 材料清单

| 文件/材料 | 作用 | 最低内容 | 当前状态 |
| --- | --- | --- | --- |
| `capability-inventory.yaml` | provider/模型能力快照 | provider、source、account ref、model、endpoint、能力、验证时间 | 模板已备，真实值待验证 |
| `route-catalog.md` | public model 到候选 route/account 的目录 | route、账号资格、优先级、adapter、价格 profile | 模板已备，真实值待冻结 |
| `billing-lane-catalog.md` | Plus/Pro/生图/来源渠道等计费子组目录 | lane、账号池、`pricing_source_group_id`、能力、selection policy、倍率、cost model | 已纳入 Revision 2/3，模板已备 |
| `billing-profile.md` | 价格和结算规则 | 计量单位、价格、版本、trigger、失败语义、倍率来源 | 模板已备，价格负责人待确认 |
| `upstream-rate-policy.md` | 自动探测与手动倍率/单位价格规则 | probe 状态、`upstream_rate_mode`、手动兜底、非 token 计价、证据和负责人 | 本 Revision 3 新增，模板已备 |
| `upstream-rate-policy-matrix.md` | 所有 lane/account 的倍率来源总表 | 每条链路的 probe/manual 模式、basis、fallback、当前生效来源和有效版本 | 实施前生成 |
| `rollback-manifest.md` | 变更对象边界 | 新增对象、禁用顺序、旧对象保护、验证点 | 模板已备，实施前填充 |
| `smoke-test-report.md` | 冒烟测试证据 | request id、route snapshot、结果、usage、charge、预算 | 模板已备，开发阶段填充 |
| `secret-handling-checklist.md` | 敏感信息检查 | key/口令/credential/URL/log 脱敏规则 | 模板已备 |
| runtime snapshot | 线上运行基线 | image digest、migration、config hash、container、proxy | 待实施前只读生成 |
| old-route regression | 旧链路回归 | old key/models/text/media/billing 对照 | 待实施前生成 |
| DB migration rehearsal | schema 变更演练 | backup/forward/down/compatibility | 待实施前生成 |
| audit record | 独立审计意见 | findings、severity、evidence、closure | 本包审计中 |

## 2. 命名与版本

- 文件名包含目标环境、日期和 revision；禁止用 `latest` 作为唯一标识。
- provider 能力、价格 profile、route catalog 和 runtime snapshot 各自有版本；价格版本不能随代码 commit 隐式变化。
- 真实凭据放在受控 secret manager/本地安全存储，材料只保存引用 ID、hash 或末四位（若确有审计需要）。
- 任何以 API key、SSH、JWT、cookie、credential、Authorization 开头的字段，默认视为敏感，除非明确脱敏。

## 3. 生成顺序

1. 先保存 runtime snapshot 和旧链路回归基线。
2. 再生成 provider capability inventory。
3. 由运营确认 route candidate/account eligibility。
4. 对支持 probe 的链路记录探测 schema、freshness 和来源；对不支持的链路先填写 upstream-rate policy 和人工价格证据。
5. 由财务/运营确认 billing profiles、手动兜底规则和版本。
6. 开发基于这些材料生成 migration/接口实现草案。
7. 独立审计检查材料之间是否一致。
8. 用户验收后才可在副本环境实施。

## 4. 一致性校验

- route catalog 中每个 enabled route 必须在 capability inventory 有对应能力记录。
- lane catalog 中每个 enabled lane 必须有明确账号池、provider/source、selection policy、倍率语义和 cost model。
- 每个 enabled lane 必须有 `upstream_rate_mode`、`upstream_rate_basis` 和有效的 probe 或手动规则；`manual_only` 不得缺手动单位价格/倍率。
- probe 失效时使用的手动兜底不得被自动同步覆盖；实际采用来源必须能在 snapshot 中复原。
- token probe 不得覆盖 image/video/per-request/provider-specific 规则，除非有明确的批准例外。
- 每个 enabled route 必须在 billing profile 有覆盖且版本有效。
- billing profile 的 upstream model/endpoint 必须与 route catalog 完全匹配。
- rollback manifest 只能包含新建/新启用对象；出现旧 group/key/account 时必须停止并解释。
- smoke report 的 route/account/profile 必须能在对应快照中找到。
- 所有文档、模板和归档文件执行 secret scan 后才能提交。
