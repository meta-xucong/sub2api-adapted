# 历史验收记录（非当前候选结论）

当前 PR13 的新一轮审计、修复与验证入口为 [2026-10-19 复验说明](../pr13-revalidation-20261019/README.md)。下面待完成/旧候选栏位保留为历史记录，不用于替代新提交的实际执行收据。最终 SHA 和新执行结果以 PR #13 的固定提交回执为准。

建立时间：2026-10-19。计划文档先于实现完成建立；当前按用户澄清保留全冷却有界最后兜底，之前硬拒绝提案及其验证记录不删除，但不作为修订候选的通过结论。

## 候选身份

- 起点代码：`6db55037080fbd994a738d3601efcf4a4d782730`。
- 起点 tree：`54c35e03e5d5b1156bbcb1e8755b4f36eb0e7341`。
- 最终组合候选 SHA/tree：**待冻结**。
- 最终受测源码清单、工具链、执行命令、退出码、日志hash及交付包：**待记录**。
- 不用工作区 HEAD 冒充未提交改动的完整受测身份；若运行在dirty tree，必须附准确源码/差异manifest。后续纯文档提交与受测代码提交分别记载。

## 当前状态

| 项目 | 状态 | 证据/限制 |
| --- | --- | --- |
| 先行计划、兼容说明、测试矩阵 | 已建立 | 本目录，仅设计与验收要求 |
| 基线源码阅读 | 已进行 | core health/router、scheduler/runtime、图片超时helper、配置/校准及旧验收材料；不是动态验证 |
| 独立开关删除、统一SmartRouter实现 | 实现决定已同步，组合待验 | 当前源码与边界见PLAN/COMPATIBILITY；未宣布完整代码验收通过 |
| 早先独立core state/race | 旧提案范围通过，保留历史 | 见下节；新LastResort源码已变化，不能复用成当前通过 |
| focused candidate1 | 未通过，历史保留 | config/core通过，service两个全冷却语义断言失败；非旧产品缺陷证明 |
| focused candidate2 | 因用户策略澄清中断 | config/core/service已通过；handler未完成，控制退出130不是测试失败，也不是整体通过 |
| 当前LastResort分类、选路、half-open调用方、精确模型校准专项 | 修订组合待重新冻结/执行 | 不把先前部分通过扩称新策略通过 |
| 计量、取消/输出、资源释放、非图片回归 | 待执行/绑定 | 历史通过不能代替本轮 |
| 后端unit/integration/lint/race/build | 待执行/绑定 | 必须逐项记录失败、skip和环境阻塞 |
| 真实PostgreSQL及新进程恢复 | 待执行/绑定 | 内存restore与mock不代替此项 |
| 独立复核与离线交付 | 待完成 | 未发布交付完成结论 |
| 真实Provider、真实费用、生产请求复现 | 未执行 | 约140秒描述不是本轮独立实测；需要另行授权 |
| GitHub push/PR、远端CI、发布、生产部署 | 未执行 | 不在本轮自动执行范围 |

## 早先独立核心验证（旧提案，已被策略修订替代）

执行证据位于 `/tmp/smartrouter-image-core-validation/`，最终交付时须将这些临时文件纳入可持久交付的证据包。工作目录是仓库 `backend/`，环境先加载 `/workspace/scratch/79ebac196883/sub2api-reaudit-tools/env.sh`。文档在03:06策略修订前核对了原始日志、前后源码清单及当时核心文件；没有另行启动编译或测试。当前新增LastResort实现不受以下结果覆盖。

- 核心默认 build tags 包测试：`go test -p=1 ./internal/smartrouter/core`，结果 `ok`，包耗时 0.021 秒。`core-test.log` SHA256：`2f007b128cfa585e090cf24f106e3fb465b82548bf2284f40572fabd2f54879d`。这不包括 integration-tag/PostgreSQL验证。
- 核心定向 race 初次命令：`go test -race -p=1 ./internal/smartrouter/core -run 'TestImageAdmission|TestOrder_ImageResilience' -count=20`，exit 0；日志为 `core-race-test.log`。随后 JSON 记录命令：`go test -race -p=1 -json ./internal/smartrouter/core -run 'TestImageAdmission|TestOrder_ImageResilience' -count=20`，exit 0。
- JSON 记录 `core-race-test.jsonl` 时间为 2026-10-19 02:30:30.087737368–02:30:31.406658607 UTC，包通过。复算得到 17 个不同 test/subtest 名称各通过 20 次，共 340 条 pass 终止事件，fail/skip 均为 0；包含父测试事件，不能写成 340 个独立业务场景。340 仅对应这次 JSON 记录，不与初次 race 结果相加充作新增覆盖。
- JSONL SHA256：`98b89d49659eb539514d08ef4cecab9132df03685436454d342fb782e0d22f18`。`core-race-summary.json` SHA256：`103b8fe9cc952360c553efe61344d23eb3352fce07588e9a489140e306bb82f3`。
- `source-hashes.before.txt`、`source-hashes.after.txt`、`source-hashes.race-after.txt`、`source-hashes.race-json-after.txt` 完全一致，SHA256 均为 `c97026e4dbc063c0c820ffe2b68f326fbd4f99cf1b35ead76208ebd178bb7404`。18 份核心源码/测试文件与当时文档复核的工作区一致；不是整个dirty工作区或修订后候选的身份。
- 定向范围包含顺序失败阶梯、并发进程内lease、迟到成功/替代探测、lease deadline自身失败、warming、restore、全冷却排除、FIFO half-open、healthy-first TopK和文本不变。handler/service配置、native-mode传输边界、真实ledger和完整矩阵仍待组合门禁。
- 三次测试命令与前后源码hash比较的执行者退出码均为0；后续核心文件再变时上述结果不自动覆盖新源码。

## focused尝试与用户策略修订历史

证据根目录：`/workspace/scratch/79ebac196883/phase6-evidence/`。以下计数按各summary的leaf口径，各次测试互相重叠，不相加作独立覆盖。

| 尝试 | 已完成范围 | 停止原因与结论 |
| --- | --- | --- |
| focused-candidate-1，02:48:34–02:53:13 UTC | config 22 pass；core 93 pass；service 1539 pass / 2 fail；对应exit 0/0/1 | service两个旧全冷却兜底断言与当时新硬拒绝实现冲突，后续handler未运行。这不是旧产品兜底有缺陷的证明；用户明确保留该意图 |
| focused-candidate-2，02:57:31–03:04:33 UTC | config 22 pass；core 93 pass；service 1581 pass；对应exit均0 | 用户澄清策略后主动停止，状态interrupted-superseded-policy；runner控制exit130；handler未完成且没有pass/fail结论，不算整体通过 |

candidate2的 `INTERRUPTED_SUPERSEDED_POLICY.json` 记录停止时源码仍匹配该次冻结快照，SHA256为 `b43e2c287d887abf50de887ab85e7b2774c881e8e9f017b0e3513af01a40bd14`；`run.json` SHA256为 `92e003f002dcaeb5e86dc906b013073a0c22275c130fa758c61bf3e285baf56d`。config/core/service日志hash分别见其summary；handler没有完整收据，不能把控制中断解释为测试失败或成功。

用户澄清后，当前验收要求是健康可用/有界等待优先，正常容量耗尽后允许显式、图片限定、共用原健康状态和有界lease的Cooling最后兜底；模型硬阻断与原生账号资格不放宽。之前“全冷却即硬拒绝”结论撤回。当前源码须重新冻结并重跑相关门禁，不把这两次旧尝试或phase5记录作为修订后通过依据。

## 必须满足后才能宣布代码验收通过

1. 活动配置和代码已撤掉独立图片超时flag/旁路惩罚，且没有新300秒机制；保留严格上游错误识别及管理员优先级。
2. 同一 lane+capability+exact model 的状态贯穿观察、选择、转发前准入、half-open和校准；正常健康容量/有界WaitPlan优先，全部不可用才允许显式有界Cooling最后兜底。不得直接把全冷却判503，也不得放宽模型硬阻断、原生账号资格、failedIDs或预算。
3. `/images` 与实际 Responses→Images bridge 的受管瞬时失败不新增账号级文本/视频惩罚；生成、编辑、模型、账号身份不串用。显式native-mode/mixed Responses保留generic HTTP/auth副作用，必须记录并验证该边界。
4. 已实际证明默认恢复阶梯、bounded half-open释放/过期、精确模型请求及重启恢复边界，披露内存、持久化失败、多实例、校准零token以及非持久升级窗口限制。旧lane只按已知owner内存映射，不能恢复历史被覆盖owner。
5. 既有取消、deadline、真实输出、计量、并发和安全摘要保护通过相应回归；所有未覆盖或环境阻塞明确列出。
6. 结果绑定最终组合源码，独立核查差异、测试日志和交付身份。只给出已验证范围，不宣布“所有场景无问题”或“可直接上线”。

## 最终收据待补栏目

执行者逐项追加：实际测试名/矩阵ID、候选身份、开始/结束UTC、命令、退出码、通过/失败/skip数量、日志路径与hash、环境阻塞及复验情况。没有日志的口头“通过”不得替代执行证据；变更实现边界时同步更新本目录其他文件。phase5及更早源码上的执行记录不得作为本轮新增修复后的组合通过依据。

旧方案验收可在 `../image-timeout-integration-20261018/ACCEPTANCE.md` 查阅，但其独立flag语义和旧候选测试结果仅作历史参考。
