> Historical candidate record for 2c756fbe. Fresh re-audit repairs and results supersede its verification status: see REAUDIT_20261018.md and REAUDIT_RESULTS.json. Original receipts below are retained, not relabeled as new runs.

# 云端修复交接与本地独立验收

## 范围和结论

本轮以 fork 基线 `2bedb713b6b0b9cf968468decae36732f390574a` 为起点，对照官方 `b0a02a3304a0a78cdd003ddda1dd23ba02c03084`。采用官方已有实现、公开缺陷/安全公告支持的最小适配，以及自定义 Wokey、Smart Router 路径的针对性修复。没有调用本地 Codex、操作 VPS、访问生产凭据，或执行真实 Provider 生成及生产账户/账单变更。历史测试夹具意外触发的外网请求被沙箱阻断，相关夹具随后修复；未重试被拒绝的外网访问或绕过策略，记录见 PROGRESS.md。

**这是可交叉审查的候选补丁，尚不满足合并或生产部署条件。** 最终完整 lint 仍以退出码 1 报告 107 条基线归因记录（106 个主诊断和 1 个关联位置）；归因是静态对比，未重跑基线 lint。前端完整开发依赖树仍有 1 个高危告警，完整高危审计门禁未通过。Docker/数据库路径尚未验证，受环境限制的跳过不能计为通过。公开发布仍待批准。

## 已修内容

1. 图片实际产物计数与零产物计费保护；显式 token 定价仍使用已观测 token，不修改历史账单。保留官方既有空输出重试策略。
2. 自定义图片总时限的竞态修复：绝对截止时间已过但取消信号尚未发布时，不再误判为可跨账号重试的单次超时；独立单次超时且总预算仍有效时维持重试。
3. Grok 显式分辨率与实际输出尺寸的计费优先级；Wokey multipart 编辑参数和大小错误兼容。
4. SSE 截断、错误终态、取消与部分用量记账；保留已经输出时不重投的边界。Smart Router 不因客户取消扣减上游健康分。
5. 图片下载目的地址校验与连接绑定、重定向校验、严格代理边界，以及图片字节和解码尺寸限制。
6. 头像在像素分配前校验尺寸；保留现有小头像与压缩行为。
7. Vue、SheetJS、Go 图片/压缩依赖等公告修复；pnpm audit 检查器在网络错误或无效报告时失败关闭；升级兼容 Go 的 lint 工具。

## 不能混称为已经解决

- 官方源自的共享账户自定义 voice 隔离没有引入新协议或数据库迁移，风险保留。
- 官方 forwarded-IP 兼容默认值没有改动；上线须核实已有开关的实际持久化值和可信代理。
- 没有重写官方图片重试、长媒体超时、HTTP/2 传输层。
- SSE 单行配置和累计输出仍可能有较大内存成本；本轮不是整个服务的固定内存上限。
- 严格图片下载拒绝无法保证目的地址的代理；必须验收实际部署代理、签名 URL 和图片尺寸，不能假定无兼容影响。
- 真实 Provider、账单对账、Docker/数据库集成和 VPS 容量尚未证明。

## 本地核验步骤

1. 保留当前工作树和未提交文件，在独立 worktree 导入候选提交；核对基线、候选提交和附带 SHA256 清单。不要覆盖 main 或清理用户文件。Windows 开启 core.autocrlf 时，源码身份应核对 Git 提交及 blob；不要将 CRLF 工作树字节直接与云端 LF 快照哈希比较。下载的 bundle/zip 则按原始文件字节核对 SHA256。
2. 先读 PLAN、UPSTREAM_PROVENANCE、RELEASE 与本报告，再独立检查源代码。不要把本报告里的测试数当成本机已执行。
3. 使用 Go 1.27.2、Node 20.20.2、pnpm 9.15.9；执行 frozen install、完整前端 lint/typecheck/test/build、后端 unit/integration/build/vulnerability/lint。
4. 云端只在测试进程中清空 HTTP_PROXY/http_proxy，以免官方 Aliyun SDK 的本地假端点被环境代理截走。本机应按真实测试网络判断，不修改生产网络配置。
5. 云端临时排除了三个 Unix socket broker 测试。具备支持条件的 CI/本地环境必须补跑；原测试和 CI 没有加入跳过规则。数据库/Docker 跳过也必须单独补测。
6. 对共享 asset、图片空结果、token 定价、流式中断/取消/重复记账、代理下载以及已有合法大图做隔离验收。真实付费请求和生产切换另行批准。
7. 全量 lint 的既有问题见 LINT_BASELINE_ATTRIBUTION；不要关闭检查来获得绿灯。对官方传输层的后续修复应优先追踪上游；自定义代码缺口可逐项修复并单独回归。

## 发布边界

候选分支保持独立。公开 GitHub 发布须获得确认，未获确认时只交付离线补丁/提交材料。没有合并或部署。最终测试结果见 ACCEPTANCE.md；失败、跳过和历史中断记录见 PROGRESS.md。
