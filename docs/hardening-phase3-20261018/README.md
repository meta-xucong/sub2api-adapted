> Current cumulative handoff and validation limits: [Cloud integration acceptance](../cloud-integration-20261019.md). The checkpoint-specific status and evidence below are retained as historical records.

# Sub2API 云端开发验收包

本轮已识别代码阻断已修复并完成约定的云端验证，当前停在上传 GitHub 之前，等待用户验收。未 push、创建 PR、合并、部署或调用真实 Provider。

受测代码：`ef5187a2f252302c80ea461b0df2060a0104b937`。后续提交仅整理本目录文档；交付 HEAD 见包内 HANDOFF 和 Git bundle，不把文档提交虚称为重新运行过全部测试。

## 先看这些材料

1. [本轮验收记录](PREUPLOAD_ACCEPTANCE.md)：最新结果、反例、跳过和残余边界。
2. [机器可读证据](PREUPLOAD_VALIDATION_MANIFEST.json)：代码身份、命令、环境、退出码及日志hash。
3. [本地隔离验收步骤](LOCAL_ACCEPTANCE.md)：保护原工作区，使用离线bundle建立独立worktree；本轮没有执行用户电脑上的测试。
4. [最终复核开发计划](PREUPLOAD_REVIEW_PLAN.md)：非流式Images、内置voice大小写、单tracker健康投影顺序与CI资源边界。
5. [健康顺序独立复核](HEALTH_ORDERING_REVIEW.md)、[非流式计费复核](NONSTREAM_BILLING_REVIEW.md)。
6. [独立并行分支排除说明](EXCLUDED_PARALLEL_BRANCH.md)：9c638df7图片轮询超时切号功能未合入，后续不能未经核对覆盖或直接混用。

unit、integration-tag、lint、定向race、core全包race、embedded构建/版本及选定真实PostgreSQL均通过各自范围。测试组有重叠；Docker/Redis/IPC/浏览器/真实Provider等未测项仍清楚列明，不将代码验收写成生产上线批准。

## 历史材料

[FINAL_EXECUTION_RECEIPT.md](FINAL_EXECUTION_RECEIPT.md)与VALIDATION_MANIFEST.json对应先前a3候选，仅保留历史；最新以本页指向的PREUPLOAD文件为准。阶段一、二和早期阶段三结果也不能冒充本次新代码运行。

在线依赖扫描与正式GitHub CI属于后续发布门禁，本轮不执行、不绕过。此停止点不是宣称所有潜在漏洞已被穷尽。
