> Current cumulative handoff and validation limits: [Cloud integration acceptance](../cloud-integration-20261019.md). The checkpoint-specific status and evidence below are retained as historical records.

# SmartRouter 图片恢复统一修复

计划建立于 2026-10-19，基线 `6db55037080fbd994a738d3601efcf4a4d782730`，tree `54c35e03e5d5b1156bbcb1e8755b4f36eb0e7341`。本目录先于本轮代码完成建立；**当前是实施与验收要求，不是通过报告**。

目标：撤掉独立 `image_poll_timeout` 切号开关与旁路惩罚，把严格识别的上游图片超时纳入已有 SmartRouter 的 lane + capability + exact model 健康状态，并让受管图片请求的选路入口遵守该状态。HTTP 副作用隔离覆盖 `/images` 与实际 Responses→Images bridge；generic native-mode/mixed Responses 保留原有 HTTP/auth 行为，不能概括为所有含图片请求都已完全隔离。

2026-10-19 用户澄清后，当前策略是：健康且可用的正常路径优先；正常容量和有界等待均不可用时，允许图片专属最后兜底尝试之前失败的 Cooling 线路，复用同一 SmartRouter 状态和有界探测租约。**全冷却不等于直接503**。早先“全部冷却即硬拒绝”提案已撤回，过程与中断测试保留在验收记录中；新策略尚待组合验证。

实际修复重点是sticky/等待后准入遗漏、健康键身份与模型一致性、恢复探测控制，以及健康overflow/订阅排序；保留用户有意设置的全池最后兜底。

- [实施计划](PLAN.md)：已读源码中的问题、统一状态与恢复边界。
- [兼容性与操作边界](COMPATIBILITY.md)：旧配置、文本/视频、计量、身份和回退要求。
- [测试矩阵](TEST_MATRIX.md)：必须验证的场景与证据要求，全部待执行或待绑定证据。
- [验收记录](ACCEPTANCE.md)：旧提案独立验证、被策略修订中断的组合尝试、当前待验状态及未执行范围。

`../image-timeout-integration-20261018/` 是旧独立开关方案的历史验收记录，保留原文；其通过结果不能替代本轮候选的验证。本轮没有新增 300 秒图片专属恢复机制，也不把约 140 秒的描述当成独立生产复现证据。

工作止于云端代码与验收材料。真实 Provider 请求、GitHub push/PR、发布及生产部署均不在本目录的授权或通过声明内。
