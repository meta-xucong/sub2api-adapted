# 内部统一 API 最小化方案独立审计

> `TASK_ID=UNIFIED-MINIMAL-20260919`  
> `AUDIT_OWNER=Goodall / 01a0b868-ef94-7cd2-877b-1cd4fbfd9176`  
> `CONTRACT_REV=v1.3.1-route-guard`  
> `FINAL_AUDIT_RESULT=符合（仅文档范围）`  
> `IMPLEMENTATION_STATUS=NOT_ACCEPTED / NEXT_IMPLEMENTATION_NOT_STARTED`  
> `PRODUCTION_GATE_DISABLED`

本审计只审查“内部使用最小版本”的开发范围、已有能力复用、必要缺口、旧链路边界和过度开发控制。它不把候选代码现状当成已上线，也不替代 PostgreSQL、真实 Provider、账务恢复、Grok 生产链路或旧入口回归证据。

## 1. 审计对象和固定版本

### 1.1 文档对象

| 文件 | SHA256 |
|---|---|
| `docs/unified-gateway-aiself/00-task-record.md` | `6BF6771B3635828FC417C99A32C07FA67ABCCAFE29ED99F906C1894239817006` |
| `docs/unified-gateway-aiself/README.md` | `391766BAB727BE030F81A9828ECA415BDE976379BC598C2AB8BAB5DDE925CCB3` |
| `docs/unified-gateway-aiself/17-minimal-unified-group-development-plan.md` | `B11A94400B09BA48A51CB762A25A91CF9C53FFB8D61A3745A62160FBC6E4EB4F` |

### 1.2 相关候选代码只读基线

候选目录 `E:/AI/sub2api/upgrade-worktree/merged-dryrun` 没有可用 Git 提交，因此使用受控文件清单和 SHA256 固定审计对象：

| 文件 | SHA256 |
|---|---|
| `backend/internal/service/unified_gateway.go` | `E0DC9190A50388735A41BAE325BC9151AB8443DE876D58DDA6E21B17DF344A88` |
| `backend/internal/service/unified_gateway_admin.go` | `20E5BDB8D4B7D4152983BBCDC05FDFC849EA0DA5A4337768DAD15B4592347B4C` |
| `backend/internal/service/unified_gateway_runtime.go` | `A8BB81688EBCA3C82EB580CC350B296852F357D7B795131729874EB720F9BD71` |
| `backend/internal/handler/unified_gateway_runtime.go` | `00A367A32EC24FEEA89E4788DD3CB89094539FE80CE73534A0590C523FB478A5` |
| `backend/internal/server/routes/unified_gateway.go` | `57FA12FB5F49A85A998AE1CB448EE49E1A9758A7094655364CA8EC3EA7C8C289` |
| `frontend/src/views/admin/UnifiedGatewayView.vue` | `8F2198D8BFF24EF24A261B39B98E549B304417955363E41F62FF3DACBDF2F4C4` |
| `backend/migrations/235_unified_gateway.sql` | `090075A9347AA8183D1C05576047C8753EB5762E59732B1DBD4BDA362DD7BF3A` |
| `backend/migrations/236_unified_gateway_admin.sql` | `0D6FAB7163A14A4ED18F258705CAF9C6AC834ED9C132288963EA0FA7AA6CC73C` |
| `backend/migrations/237_unified_gateway_runtime.sql` | `ECAA45A13B6AAE31E0F08B1954A959BC52553F84B966F6DBE1B71CD23C4183D2` |
| `backend/migrations/238_unified_gateway_recovery.sql` | `1017215FC047BB132A6E09705F65B476FDBAAA15D587DEF3AFDE69420933A3AA` |

这些哈希只证明审计对象的文件内容，不证明代码已通过生产验证。

## 2. 审计方法

独立审计角色 `Goodall` 在主控写入文档后只读检查：

- 用户最新边界：内部使用、一个统一 Group、管理员统一分配、用户只使用 API key；
- 第 17 章与现有第 00、15、16 章之间的状态和范围一致性；
- 候选 runtime、管理面、路由、价格和账号绑定代码是否支持文档中的“已完成/缺口”判断；
- 旧 `/v1`、旧 Group、旧 key、旧计费是否有结构隔离；
- 是否引入 Provider Node、用户调度、公开报价或其他未授权功能；
- 文档能否作为下一阶段 M1–M5 的最小实施依据。

## 3. 初次审计结果

初次审计结论：`不符合`。

发现的问题：

1. 候选管理面可选择多个 active Group，运行时只按传入 key 的 GroupID 进入，未形成“唯一 Unified Group + 专用 key”的服务端强隔离。
2. 超级管理员路径的 binding account 归属校验不充分。
3. 前端新建 lane 存在默认倍率/价格值，可能绕过未知价格 fail-closed 语义。
4. 模型候选刷新、管理员确认和可执行目录闭包没有被明确冻结为必需门禁。
5. 文档同时出现“候选实现已完成”和“实现尚未开始”的表述，容易把历史候选实现误读为本阶段 M1–M5 已完成。

初次结论没有要求增加产品功能，而是要求把已经存在的安全/正确性缺口写成明确的实施门禁。

## 4. 文档修正记录

主控只修改了文档，没有修改候选代码：

- 第 17 章状态改为 `EXISTING_CANDIDATE_PRESENT / NEXT_IMPLEMENTATION_NOT_STARTED`，区分历史候选实现和下一阶段收口工作；
- 增加服务端唯一 Unified Group 强制、所有管理员等级的账号归属校验；
- 增加前端价格初始为空、不得预填 `1.0/1.2` 等隐式有效售价的门禁；
- 增加目录发布/读取/发送前的 schedulable/eligibility 校验要求；
- 增加无 Git 提交时的文件清单、SHA256 和差异证据规则；
- 在 M1、M2、M4、M5 和验收矩阵中分别绑定上述要求；
- 在 `00-task-record.md` 和 `README.md` 中明确第 17、18 章优先于早期完整平台路线。

## 5. 复审结果

复审结论：

```text
AUDIT_RESULT: 符合（仅就文档范围；代码尚未达到实施完成）
MUST_FIX: 无新增文档硬阻塞
```

复审确认：

- 最小目标已经收敛为一个 Unified Access Group，不再要求用户自选 provider/account/lane；
- 已完成候选能力与 M1–M5 必要缺口被明确区分；
- 服务端 Group 强隔离、管理员账号归属、价格 fail-closed 和实时可调度目录均已列为硬门禁；
- 旧 `/v1`、旧 Group、旧 key 和旧计费不得被统一入口接管或 fallback；
- Provider Node、公开 quote/pricing、复杂调度、用户级价格和完整 KIE 平台均已明确排除；
- 验收矩阵覆盖正常、失败、异步、重复回调、旧入口和发布冲突场景；
- 文档没有要求新建用户 Provider 选择模型、外部供应注册中心或通用任务平台。

## 6. 仍未完成的实现和证据

文档复审通过不等于代码实现通过。进入 M1–M5 前，必须真实修复并验证：

| 项目 | 当前结论 | 进入实现后的要求 |
|---|---|---|
| 唯一 Unified Group | 候选代码尚未强制 | 服务端和 runtime 拒绝其他 Group；专用 key 双向隔离 |
| 管理员账号归属 | superadmin 路径证据不足 | 所有管理员等级统一执行归属检查 |
| 前端默认价格 | 候选页面仍有预填风险 | 初始为空，未导入/审核 profile 不可发布 |
| `/models` 目录闭包 | 静态价格检查不能替代实时资格 | 目录和发送前确认 schedulable/eligibility |
| PostgreSQL | 未完成 staging fresh/repeat/upgrade/conflict/rollback | 迁移和约束在隔离数据库实证 |
| 真实 Provider/账务 | 未完成 | 至少文本、同名多 lane、失败零收费和恢复证据 |
| Grok 视频 | 候选有基础，生产恢复未证实 | 若纳入首批，完成 pending/成功/失败/重复回调闭环 |
| 旧入口回归 | 结构隔离已有，证据不完整 | 旧 key、旧 Group、旧 `/v1` 和旧计费对照测试 |
| 版本固定 | 本审计使用文件 SHA256 | 后续代码变更重新生成清单和哈希，旧证据失效 |

在这些证据完成前，`PRODUCTION_GATE_DISABLED` 不得改变。

## 7. 最终放行结论

本次交付可以进入下一阶段代码实施，但只允许按照第 17 章的 M1–M5 执行；本次没有授权或完成代码、数据库、线上配置、真实付费调用、GitHub 推送和部署。

最终状态：

```text
文档：AUDITED_PASS_WITH_CONDITIONS
代码：NEXT_IMPLEMENTATION_NOT_STARTED
候选历史实现：EXISTING_CANDIDATE_PRESENT
生产：PRODUCTION_GATE_DISABLED
```
