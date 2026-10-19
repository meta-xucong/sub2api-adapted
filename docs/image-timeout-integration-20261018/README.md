# Sub2API 图片轮询超时整合验收包

已将 `9c638df7` 的图片轮询超时切号功能最小化整合到上一云端交付基线 `4a137bbf`，开关仍默认关闭。最终受测代码为 `528820ea5e8f1694fc2a54ef3d3b1045869ff212`；最终文档交付提交与离线包身份见交接清单。

本轮unit、integration-tag、lint、定向race、core全包race、embedded构建/版本检查，以及选定15项真实PostgreSQL验证均通过各自范围。438个新增handler矩阵子例在unit和定向race中实际通过。缺Docker导致integration中的repository整包未执行、其他跳过及真实环境边界均单列，不能写成全库零缺口或可直接上线。

1. [验收记录](ACCEPTANCE.md)：最终结果、初次失败与测试修正、真实PG证据、跳过和风险。
2. [验证清单](VALIDATION_MANIFEST.json)：受测代码身份、命令、退出码及原始日志/二进制hash。
3. [测试矩阵](TEST_MATRIX.md)：实际测试名称与已验证范围；余额/JSON-edit矩阵不扩称订阅/multipart完整交叉覆盖。
4. [整合与回退计划](PLAN.md)：默认关闭、启用前条件和配置优先的回退说明。

本地一次结算不证明Provider仅生成或收费一次；轮询超时重试仍有重复图片/上游费用风险。既有cooldown按账号生效，共享凭据的文本请求可能受影响。前端结果为源码未变的历史复用，真实Provider、浏览器和在线依赖审计未执行。

当前明确停在用户验收和GitHub上传之间：未push、创建PR、合并、发布镜像、操作用户电脑或部署VPS。代码验收不会自动打开开关。
