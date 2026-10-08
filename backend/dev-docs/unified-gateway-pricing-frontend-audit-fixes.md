# Unified Gateway 线路定价页：前端审计问题修复设计

状态：`IMPLEMENTED; A2_STATIC_CODE_AUDIT_PASS; FRONTEND_TESTS_PASS; BROWSER_E2E_UNVERIFIED`

## 1. 目标

修复本地 Unified Gateway 线路定价页审计中确认的错误提示、说明文案和大列表操作问题，并让重复线路在发送保存请求前被指出。继续使用现有管理页、现有线路定价 API 和现有服务端校验。

本设计只调整管理员前端与对应测试、翻译文案。它不改变费率或计费结果。

## 2. 基线与已审阅依据

- 仓库：`D:\AI\SSH\sub2api-official-v0.2.13-20261003`
- HEAD：`41fa907421851c17859f88345ccee1e2ff7c22f8`
- 官方 v0.2.13 锚点：`3040209f205472038c1ba745a1bedd2edd9053b1`，是当前 HEAD 的祖先；不换基线。
- 基价卡控制设计文件：`D:\AI\SSH\SUB2API-V0213-UNIFIED-GATEWAY-BASE-PRICE-OVERRIDE-DESIGN.md`。`AGENTS.md` 记录的 SHA256 为 `DE9E4E344974C0C2ACB19E0787108BDB3A295CB50B0E8F4A13B87C1EDAEA98CF`；本次实际文件 SHA256 为 `7CD72B484D9F7002588C39E71B21D4E315267B0D2E3D335F0F015A1FC4A05391`。已检查当前文件：第 3 节确认 1 小时缓存哨兵值触发时整卡不适用，回退至“Sub2API 原生价格 × 线路倍率”；第 11、12 节记录了线路倍率应用及运行验证。首行状态仍写着 `LINE_MULTIPLIER_COMPOSITION_FIX_PENDING`，与第 11、12 节的后续状态冲突。此不一致作为当前控制文档的历史/状态问题记录；本次实现只依赖上述当前正文中一致的计价语义，不改写仓库外的 `AGENTS.md`、蓝图或基价卡控制设计。
- Phase 6 文档哈希与 `AGENTS.md` 记录一致；本次继续遵守账单页只读边界。
- 本设计依据上一轮前端审计已确认的问题及当前 Vue 组件、API 类型、服务端重复键规范化和组件测试。工作树现有用户修改作为基线保留，不覆盖、不清理。

## 3. 分类与权限

- **D0**：修复目标由既有审计发现直接确定，没有费率语义或产品范围选择待定。
- **I1**：修改一个 Vue 页面、其组件测试和中英文 locale，沿用现有数据类型及 API。
- **A2**：页面编辑线路费率配置，错误提示可能导致管理员误判保存/生效状态；需要独立审计。但本任务不改变金额计算、服务端合同、权限或持久化。
- **唯一写入者**：主会话；在代码冻结后交给独立只读审计者审查。
- **当前路由证据**：`ROUTE_UNVERIFIED`；不影响本地前端行为审计。

## 4. 问题与修复

### 4.1 首次加载失败被误报为没有 Composite 组

现有模板用 `!state?.groups.length` 区分无组状态。首次 GET 失败时 `state` 仍为 `null`，因此错误和“没有 Composite 组”同时出现。

修复：把加载中、首次加载失败、已成功加载但没有 Composite 组、配置已加载分别表示。首次加载失败状态提供“重试”按钮，重试继续调用当前 GET API；不清除或改写服务端配置。成功加载且 groups 为空时才显示无组提示。

### 4.2 1 小时缓存哨兵文案不精确

修复中英文说明：`cache_write_1h_per_million = 0` 表示该卡不支持 1 小时缓存写入，不表示免费；当本次 usage 存在 1 小时缓存写入 token 时，整张卡不用于该请求，并完整回退到 **Sub2API 原生用户价格 × 已配置线路倍率**（线路倍率未配置时按原生价格）。不改变后端处理。

### 4.3 大型配置列表缺少定位方式

修复：增加页面内搜索，按账号名称、账号 ID、模型名和计价类型过滤可见行；显示匹配数/总数，并提供清空搜索。搜索只控制显示，不删除、重排或修改 `form.entries`，保存仍提交完整原配置。无匹配时显示独立空结果提示；零配置时仍显示原有空配置提示。

不引入分页、远端搜索、额外 API、持久化搜索状态或复杂筛选器。

### 4.4 重复线路只能等到服务端拒绝才发现

修复：保存前按服务端 `buildUnifiedGatewayRoutePricingSnapshot` 的线路键规则检测重复项，首个重复时阻止 PUT 并显示专门的中英文错误。键规则为目标组、账号 ID、去首尾空格的模型名、计价种类；图片继续加入规范化图片档位和小写质量，视频继续加入规范化分辨率和时长。视频分辨率别名与服务端一致：`480`/`sd`/`480p` → `480p`，`720`/`hd`/`720p` → `720p`，`1080`/`1080p`/`full_hd`/`full-hd`/`fhd` → `1080p`。token 项不按倍率或基价卡拆键；同一账号/模型/kind 只能出现一个 token 项。

保留服务端校验作为最终权威；不改服务端验证或 API 错误结构。

### 4.5 视频时长错误只能得到通用保存失败

现有前端只检查时长是正整数；服务端只接受 1–15 秒。比如 16 秒会先发 PUT，再落入通用保存错误。

修复：前端与服务端保持相同范围，校验视频时长为 1 至 15 的整数；超范围时显示专门的中英文提示，不发 PUT。服务端校验继续保留为最终权威，后端常量和规则不改。

### 4.6 搜索期间新增线路会被当前过滤条件隐藏

修复：添加线路时清空当前搜索词，让新建空白行立即可见；不改变已有表单数据。

### 4.7 API 客户端调用契约缺少直接测试

价格页组件测试 mock 了整个管理 API 模块，因此无法证明真正的 API 客户端仍使用配置读取/保存约定的 URL、HTTP 方法，并把 revision 与完整线路列表作为保存载荷传递。

修复：为现有 API 客户端添加轻量单测，断言 GET/PUT 路径和方法、完整保存载荷中的 `target_group_id`、`expected_revision` 与 `entries`，以及返回 data 的解析。测试使用 mock `apiClient`，不连接服务器、不改变 API 或生产逻辑；它补齐请求组装证据，不冒充真实服务端集成/E2E。

## 5. 文件边界

允许修改：

- `backend/dev-docs/unified-gateway-pricing-frontend-audit-fixes.md`
- `frontend/src/views/admin/UnifiedGatewayPricingView.vue`
- `frontend/src/views/admin/__tests__/UnifiedGatewayPricingView.spec.ts`
- `frontend/src/api/__tests__/admin.unifiedGatewayRoutePricing.spec.ts`
- `frontend/src/i18n/locales/zh/admin/unifiedGateway.ts`
- `frontend/src/i18n/locales/en/admin/unifiedGateway.ts`

不得修改：后端计价/配置/API、费率字段、图片/视频计价规则、数据库和运行配置、统一网关账单看板、路由/scheduler、普通用户页面、原生 Sub2API 计费、其它已有用户修改。不得重置、清理、覆盖或批量暂存当前工作树的其它变更。

## 6. 验收标准

1. GET 失败时只显示加载失败和可用重试，不显示“没有 Composite 组”；重试成功后正常显示配置。
2. 成功加载且组列表为空时只显示无组提示；加载中状态独立。
3. 搜索可匹配账号名、账号 ID、模型和类型；清空后恢复所有行；搜索过滤后保存仍提交完整列表。
4. token、图片、视频三类的重复键按服务端规范被前端拦截；服务端 API 不被调用。视频的所有已支持分辨率别名折叠到同一重复键；合法不同账号或不同规格可保存。
5. 视频时长仅允许 1–15 秒整数；0、16、小数等值不发送 PUT，显示专门的范围提示。
6. 搜索过滤后保存仍提交完整列表；筛选后的删除操作只删除匹配行的原始条目；搜索时添加新线路会清空搜索并显示新增行。
7. 中文/英文缓存哨兵文案描述原生价格乘线路倍率的完整回退。
8. 定向 Vitest、locale key 完整性、`vue-tsc --noEmit`、前端构建通过；按用户要求执行适当的前端回归测试。
9. API 客户端单测确认 GET/PUT 路径和方法、完整保存载荷中的 target group ID/revision/entries 与 response data 解析；不把 mock 单测称为服务端集成验证。
10. 独立 A2 只读代码审计通过，确认只改本文件边界、搜索不影响保存数据、费率算法和 Phase 6 只读行为未变。
11. 页面视觉/E2E 检查如环境可用则附证据；若浏览器自动化仍不可用，明确记录为未验证，不用组件测试替代。

## 7. 不在本设计中的工作

- 不改价格计算、上游对账、任何倍率或价格卡数据。
- 不修复基价设计控制文件、`AGENTS.md` 哈希或蓝图状态。该现有文档状态冲突已在第 2 节记录，避免把本次前端维护扩成计费设计版本整理。
- 不部署、替换容器、改数据库、连接上游、提交或发布 VPS。
- GitHub 发布只在代码和测试审计通过后进行，并且必须先根据 Git diff 建立精确的发布文件清单；不把无关脏改动顺手混入。

## 8. 实施记录

2026-10-08：独立 A2 设计审计首轮指出视频分辨率别名必须按服务端档位归一，已补充别名映射和测试要求；复审 `PASS`，审阅文档 SHA256 `0E938C49CF77A9C473DE9D1C77C257D219A131E8B6459DE83495B132E7F5AB9E`。后续 A2 代码审计又发现视频时长范围和搜索时新增条目两项 UX 缺口，已扩充设计；该修订由独立 A2 复审 `PASS`，审阅 SHA256 `F4DA6C4437EA3FFA1A738AB4D447FE57B633CFB0323A334AC7ACF51AE6E83613`。

首轮获批修订已完成实现并通过静态 A2 代码审计：视频时长范围、搜索时新增条目和过滤删除索引均已处理；不改价格 API、后端计费、费率字段或 Phase 6 看板。补充 API 客户端契约测试的文档修订由独立 A2 设计复审通过，新增测试已实现并通过全量前端验证；最终代码审计也已通过，详见本节末尾记录。浏览器 E2E 仍未验证。

2026-10-08 实施与验证：

- 代码修改范围仅为本设计第 5 节中的价格页、组件测试、API 客户端测试和中英文 locale；本设计文档同时记录本轮实施、验证与审计结果。
- 定向 Vitest：`vitest run src/api/__tests__/admin.unifiedGatewayRoutePricing.spec.ts src/views/admin/__tests__/UnifiedGatewayPricingView.spec.ts src/views/admin/__tests__/UnifiedGatewayView.spec.ts src/components/layout/__tests__/AppSidebar.spec.ts src/i18n/__tests__/localeKeyCompleteness.spec.ts`，5 个文件、43 项通过。
- 全量前端 Vitest：`vitest run`，340 个文件、2629 项通过。
- `vue-tsc --noEmit` 通过。
- 定向 ESLint：对本设计第 5 节五个前端文件执行 `eslint`，通过。
- `vite build` 通过。输出已有警告：旧版 caniuse-lite、Vue app store 静态/动态混用，以及大 chunk；未见构建失败。
- 浏览器视觉/管理员真实 API E2E 未验证；组件测试和构建不作为这类运行态证据。
- 首轮独立 A2 静态代码审计对四个前端文件和本设计文档为 `PASS`，审阅文档 SHA256 `8FDDC86FAB6D6BDB0B1EDDD33B692B631F60882EA1EA3DEEAF09D124167FD0ED`。源码 SHA256：Vue 页面 `0CEEAAF40280255C38C32B8575E2AD598D69EC7C91A11D1E77B82BEE6245B40C`；组件测试 `9D583BBB51BE918BB91A4CAAC25F8676459BE1E58023CF133C39B133BFAD850C`；中文 locale `6BAA91FD08B78C5C102660787825EDD4F9C11C3E66DE658569ADB210675E3CCA`；英文 locale `53DBD9EFCC29DA133E7B77397BE4C26EE9E101DFB4CAA73BE242648C0DE961DC`。
- API 契约测试的设计修订由独立 A2 复审 `PASS`，审阅文档 SHA256 `0F890C9357E3F5BD3B6856795FE64F81C295FF06663A81F8C9D9DE0E33F35C40`；随后按本设计范围完成了新增测试与修订文档的最终代码审计，结果见下条。
- 审计范围是静态代码与文档；浏览器视觉和管理员真实 API E2E 仍为 `UNVERIFIED`，不将组件/API mock 单测或构建表述为运行态验收。
- 2026-10-08 最终独立 A2 静态代码审计：`PASS`。审阅文档 SHA256 `ABBC8778679132BB6BE9EC70DEA97F1B5D3732FF5EBAB13F8F0F913146875466`，并逐项核验 API 请求组装及前端交互边界；未发现计费或 Phase 6 越界。审计确认 API 测试是 mock `apiClient` 的单测，不代表真实服务端集成；浏览器与真实 API E2E 仍 `UNVERIFIED`，route provenance 为 `ROUTE_UNVERIFIED`。
