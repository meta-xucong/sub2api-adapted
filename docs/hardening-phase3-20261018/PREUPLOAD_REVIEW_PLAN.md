# 上传前最终复核与最小修复计划

## 本轮停止点

2026-10-18 用户要求再次复核，在“云端开发完成，仅差上传 GitHub”时交验收。本轮不执行 push、创建 PR、合并、本地电脑操作或 VPS 部署；此前的公开推送授权保留为历史，不据此跳过本轮用户验收。在线 npm/Go 扫描仍没有新的确认，不执行，也不关闭工作流。

起点 `1e54bf4f64b000695cc0efed4eebf47aa61e10f1`（其代码受测于 `a3f0fc956`）。旧交接包只证明该旧候选，不覆盖本轮新增修改。最终代码冻结后补对应验证、文档与交接包。

## 新发现和限定修复

1. **P2：下游非流式生图失败的用量丢失。** OAuth Responses 实际仍读取上游 SSE，已解析正数用量后遇到不可重试的内容策略拒绝会返回空用量；forward 又返回 nil result。直接 Codex 图片完整 HTTP200 JSON 的类似语义错误也会在解析用量前退出。同样输入在流式分支已经保留用量。仅修可信完整响应的实际用量传播，先沿用现有错误分类，failover 保持 nil；无用量不造收费，图片零产物不造图片数。不改 HTTP>=400、局部读取失败、新定价或跨尝试聚合。
   - 直接图片非流式 typed nonretry 错误还可能形成 HTTP200 空 body；使用既有错误响应函数，与 Responses 路径一致。重试前不得提前写响应，已发 keepalive 状态遵循既有边界。
   - 用真实 handler + 计费服务到合成扣费命令验证三类计价、正/零/无用量、成功/切号、无效JSON、部分读失败、HTTP错误，保留旧源 overlay 反例。
2. **P2：内置语音 ID 大小写兼容。** xAI 官方 TTS 文档说明 eve/Eve/EVE 等价，Realtime 官方样例使用 Ara。此前公共目录精确比较误拒合法名称。仅公共目录做 ASCII 折叠；custom ID 的精确归属与账号固定不变；精确已知拒绝先于目录。大小写匹配的 canonical ID 若已有任何持久化 custom 映射则不作为公共别名放行；歧义目录和读取失败拒绝，目录缓存不缓存权限。
   - 覆盖冷/热/超缓存目录、TTS/Realtime、foreign/deleted/group-denied、缓存后新映射、SQL错误、非ASCII折叠与custom精确ID。
3. **P2：单 tracker 的健康状态持久化乱序。** 同 key 的内存观测先后有序，但解锁后同步 sink 可逆序完成，使较旧失败快照覆盖较新恢复状态；该问题继承自 fork 基线，非本轮首次引入。标准库隔离探针同秒复现20/20。
   - 在既有 healthState 上附内部 token 和事件序号，沿用既有状态生命周期；生产 sink 的现有3秒 context 同时覆盖 per-key gate 等待和 DB 调用，不持全局状态锁跨 I/O，不新建后台队列/goroutine或永久第二注册表。
   - gate 接受新序号即推进“已尝试投影”高水位，避免 commit 错误结果不确定时旧快照反写。旧事件仍入日志，只不覆盖物化状态；超时/DB失败保持既有 best-effort 失败语义。不能据此宣称所有事件必达或严格持久化。
   - Restore 复用同 key token，defer 安全释放，私有投影控制字段不可由 JSON 输入。验证同秒/逆时钟、延迟旧事件、取消/超时/异常释放、不同key/读取不阻塞、真实PG事件日志与投影。
   - 多 tracker/进程仍各自维护内存并最后提交快照；这不是分布式一致健康累计协议，不新增 schema 或全局租约。
4. **CI 可执行性：** lint 使用已验证的 Go软内存目标3GiB、GOMAXPROCS2、GOFLAGS=-p=1、concurrency1与45分钟预算，保留全部规则和路径；不宣称软限制等于 RSS 硬上限，也不宣称 GitHub 已执行。此前本地大内存运行137和后续低内存通过证据均保留。

## 依据

- xAI TTS： https://docs.x.ai/developers/model-capabilities/audio/text-to-speech （Built-in voices）
- xAI Voice REST： https://docs.x.ai/developers/rest-api-reference/inference/voice#text-to-speech-list-voices
- xAI Realtime 示例： https://docs.x.ai/cookbook/examples/voice-agent-phone
- 图片失败用量沿用此前已核对的官方 Sub2API #5154 原则；原来源和语义见 IMAGES_FAILED_USAGE.md，非自行设定收费策略。

## 验证和验收

先保留可复现反例，再跑修复专项及独立源码复核；冻结候选后重跑受影响的最终 unit、integration、lint、定向race和构建，并用隔离原生 PostgreSQL 验证新增持久化分支。环境跳过仍如实列明，不能将缺 Docker 的 TestMain 提前退出计作数据库执行通过。新源码包离线导入到仅有原始base的独立仓库后比较tree与fsck。

最终报告明确：已修代码缺陷、实际通过项、环境/业务验收待做项、候选SHA及只含文档的后续SHA。用户验收前停在云端，不将附件中的源码提交误写成已在GitHub。
