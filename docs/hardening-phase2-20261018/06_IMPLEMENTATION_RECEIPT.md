# 第二阶段实施与验证记录

## 结论与身份

本轮 F01–F11 已实现并通过对应范围的独立源码复审。生产代码完成完整单元、integration 标签套件、关键 handler 竞态检测、选定真实 PostgreSQL 以及嵌入构建验证。**发布状态仍为 HOLD**：存在未完成的环境/授权门禁及全仓 lint 遗留诊断，不可写成“整个系统没有缺口”。

- 用户原始基线：`2bedb713b6b0b9cf968468decae36732f390574a`。
- 本阶段基线：`e5b9bfbe91c4939befae1278d57141c6fbbbe6a7`。
- 最终受测生产代码：`09305f4ff35d3cd25f503ef2813dbcd977874add`。
- 后续测试清理：`57a5c6c78c34e895286f8db1325efbedc7728e7a`，只改两个 `_test.go` 文件；生产代码与 093 完全一致。
- 文档收尾提交由交付清单记录；没有新增生产代码变更。
- 全程云端隔离工作，未操作 VPS、未调用真实生成 Provider、未修改生产余额、未推送/合并/部署。

## 修复范围

|编号|实现|保留边界|
|---|---|---|
|F01|Wokey 原始 JSON 规范化前及最终 multipart 前拒绝重复键等歧义输入|仅此适配分支；不全局换 JSON 库，审核可能已发生|
|F02|失败的 raw CC、CC→Messages/Responses 流无语义交付且无正数计量时，不生成计费结果；断开信号仍传到账号健康保护|正数计量、成功和既有“有输出无 usage”收费策略保持；raw buffered 非标准 HTTP200 兼容不重写|
|F03|无 data 的事件不提前结束 raw 流或污染 silent-refusal 判断|显式空 data 错误仍按错误处理|
|F04|有界 SSE 支持 CR/LF/CRLF、流首 BOM、无冒号字段和单空格规则|保留 EOF/DONE、字段载荷、既有宽松事件名兼容与转发行为|
|F05|content_filter 在 CC→Responses 流式/非流式映射为 incomplete|不改重试、价格或其他 choice 规则|
|F06|结构化识别 partial 事件，避免 prompt/URL/ID 普通文本导致完成图漏计|保留产物校验、去重、尺寸和原定价，不按请求 n 推断产物|
|F07|Images 总预算/取消不误罚最后账号的高级 scheduler 评分|独立 attempt 超时和 Provider 故障保持原规则|
|F08|代理错误稳定脱敏；websearch 统一安全解析|保留 SOCKS scheme；全空白代理不静默变为直连|
|F09|Tavily 官方 Bearer、key 规范化、指定 provider 测试及安全错误分类|不改变生产择优/配额；上游 401 不注销本站登录；旧无目标调用兼容|
|F10|默认 CSP 最小新增 media-src 的 self/data/vidgen.x.ai|自定义策略不自动放宽；未知媒体主机仍需单独配置；浏览器未验收|
|F11|统一 OAuth API 和旧回退补绝对期限/父取消检查点，避免过晚结果准入及错误处罚|不改锁/CAS/重试协议；保留已持久化成功后的清理；不提供原子提交截止保证|

依据、归属与不变量见 01、02、07。F11 的继承来源为官方 PR #4221；对旧版本的实际执行与 helper 级重建证据有明确区分。

## 验证矩阵

所有数量按套件分别报告，**不可相加**。顶层测试与其子测试也不能相加视为独立测试。

|验证|实际结果|范围限定|
|---|---|---|
|前端完整 Vitest|345 文件，2,666 测试通过|运行早于 1487 提交创建；源码/锁文件字节一直未变，按相同字节复用|
|前端 lint / typecheck / production build|均 exit 0|不等于真实浏览器/Provider 通过|
|093 完整 unit|12,097 顶层通过，18 顶层跳过；另有 11,485 子测试通过；exit 0|另明确排除 3 个 AF_UNIX broker 测试，不包含在 18 项跳过里|
|093 完整 integration 标签命令|6,778 顶层通过，20 顶层及 2 子测试跳过；另有 6,147 子测试通过；exit 0|另有 3 个命令排除；repository TestMain 因无 Docker 整包不执行，不能算数据库整包通过|
|093 关键 handler race|3 顶层 / 136 子场景通过，零失败、跳过或 race 诊断|覆盖 F01/F02/F07；不是全仓库 race|
|093 原生 PostgreSQL|8 顶层 / 9 子测试通过，零失败/跳过；含两项真实新进程恢复断言|PostgreSQL 18.1 Debian；仅 bootstrap overlay，原断言/帮助方法不改；不是 Docker/Alpine/Redis 验收|
|093 嵌入二进制构建与 --version|exit 0 / exit 0|版本为 0.2.12-audit、提交 093；仅版本 smoke，不是配置完整应用启动|
|57 测试清理后 embed 整包|14 顶层 / 79 子测试通过，零失败/跳过|真实 SVG 资产完整字节、MIME、缓存断言均保留|
|57 测试声明清理定向|2 顶层 / 19 子测试通过，零失败/跳过|Context 变量静态类型及测试行为不变|
|最终本地 golangci-lint|exit 1，107 项：errcheck 32、ineffassign 4、staticcheck 61、unused 10|与保留的 1487 候选报告按文件+诊断比较，新增 0、移除 0；不是重新运行原始 main 的结果，也不能算 lint 通过。093 新增的测试声明诊断已消除|
|辅助检查|4 个 shell 检查、release matrix 10 项、Python 审计解析器 30 项通过；语法/diff 检查通过|分项结果不并入业务测试总数|
|Apple container / Compose 辅助检查|未通过|Linux 不支持该 macOS stat 用法；缺 Docker CLI|
|真实浏览器|未执行用例|Chromium IPC/socket 环境受限；语法、jsdom、middleware 不能代替播放/CSP 验收|
|npm / Go 在线漏洞审计|无有效当前结果，待明确授权|不把历史报告或客户端协议检查写成零漏洞|

源码与日志独立复核确认完整套件无重复终态记录。所有 4,445 个受测源文件在 093 实测期间保持一致；后续只改测试与文档。最终 PostgreSQL 的 41 个受控子进程均已清理。Redis 下载返回 HTTP403/Cloudflare1010，未绕过限制。

## 失败历史与收据说明

- 1487 完整 unit 的三项失败保留：统一 API 截止检查、心跳用例与分配次数用例。后两项针对调度/进程级计数假设改为隔离验证，具体原始干扰未追踪。修复后保留原截止测试；后两项不改变计时值/分配阈值，合成负控仍失败。
- da6 完整 unit 通过后，integration 又发现旧回退截止遗漏；修于 093，原失败用例与邻近控制重新通过。
- 093 embed 额外检查发现两个过期 /logo.png 夹具；fork 品牌已改为 logo.svg。57 只修夹具并增加真实字节校验，没有放宽静态服务行为。
- 093 新增的一项 QF1011 仅为测试变量声明，57 按实际返回类型简化；不以关闭检查器消除诊断。
- PG 两次 bootstrap 失败、早期夹具错误、中断编译、race 中断、lint 资源重试均单独保留，不计通过。外部运行脚本曾被运行中修改，造成早期日志覆盖；那些结果未计数，后续冻结脚本并独立去重，详见 harness-incident.json。

## 证据与制品

交付包 evidence/ 中保留命令、退出码、起止时间、原始日志、摘要和哈希：

- backend/checkpoint-1487/ 与 checkpoint-da6e73ca/：历史终态记录。
- backend/final-09305f4f/：最终生产代码完整 unit/integration、构建、版本及原始 lint/embed 结果。
- backend/final-test-cleanup-57a5c6c7/：测试清理后的最终 lint。
- f02-f04/race-09305f4f/、native-postgres-final-09305f4/：关键 race 和最终真实 PostgreSQL。
- oauth-deadline/、outer-refresh-deadline/、embed-fixture/：分项红绿、测试清理和审查。
- core-code-audit.md、security-code-audit.md、补充代码审查与独立统计收据：按各自文件指纹限定结论。
- 前端构建的 180 个文件及二进制另有构建清单；最终包不包含发行镜像、依赖缓存或数据库目录。

原生 PG 证据包含精确 overlay 与来源校验，不是自包含服务器安装包。测试既没有证明真实 Provider 生成成功，也没有完成 VPS 容量、真实计费或完整浏览器验收。后续步骤见 08。
