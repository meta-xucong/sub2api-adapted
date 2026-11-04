# Responses 图片线路计费修复

## 已确认的缺口

HTTP Responses 入口此前只传入 token 类型的余额预留估算。统一网关因而只保存文本计价快照；即使返回值正确记录图片模型和图片数，图片结算也无法取得图片线路价卡，最终沿用原生图片价格。

404token 的两个已对账样本走 Wokey 图片线路：上游实际费用均为 USD 0.01，图片卡为 0.069 星/张，组倍率为 1.25。原生 2K 基价 0.07 导致用户实扣 0.0875；按线路卡应扣 0.08625。相同线路的标准 Images 请求已有正确的 0.08625 记录。错误不在上游报价同步，而在 Responses 入口未建立图片计费快照。

## 最小修复

- Responses 保留原 token 估算，入口明确图片模型或原生 `image_generation` 工具时，额外建立图片估算和线路卡快照。
- 复用原线路计价规划及预留许可；两种计价快照均存在时预留较高估算。
- 只有实际图片数大于零时使用图片快照；auto 返回纯文本保留文本计价。
- 原生 Responses 与 passthrough 将请求中已有的 quality 传到结算 metadata，使既有尺寸＋质量卡可以命中。
- 继续按实际交付图片数收费，只改变用户 ActualCost，原生 TotalCost、账户成本及原有结算方式不变。

这扩展了原线路计费设计中仅标准 Images 的图片接入范围，依据为本次用户明确要求。没有新增价格规则、请求校验、重试、路由、正文转换或历史账单修改。

## 验证与边界

聚焦测试 `go test ./internal/service -run 'TestResponsesImage|TestImageOutputAccounting' -count=1` 通过。覆盖文本/图片双快照、只配一类卡、auto 文本、未授权预留、其他账号，以及现有 9 条转发路径的固定单价与尺寸＋质量卡。完整 Forward→RecordUsage 隔离测试验证 0.069×1.25=0.08625 同时进入用量记录和原子余额扣款指令，原生 TotalCost 不变。

独立代码审计通过；独立范围审计未发现越界。测试使用隔离 HTTP 和仓储，不冒充线上图片生成或生产余额扣款验证。

直接相关回归 `go test ./internal/service -run 'TestResponsesImage|TestImageOutputAccounting|UnifiedGatewayRoutePricing|WokeyActualBilling|Inflight|RecordUsage' -count=1` 通过（exit 0）；完整 handler、server/routes 包通过。完整 service 包有一个 Grok 模型别名断言失败（exit 1）：期望 `gpt-5.6`，实际 `grok-4.6`。在独立、未修改的 `8b44d4c10` 工作树重跑完整 service 包，得到同一唯一失败（exit 1）；该测试单跑通过。没有修复范围外的既有测试状态问题，也不宣称全量测试全绿。

本次覆盖 HTTP Responses 的入口图片模型及原生图片工具；服务端后来才隐式注入图片工具的 Codex 路径没有本轮线上漏扣证据，未扩大处理。模型别名继续遵守既有精确计费模型匹配。未命中适用线路卡仍保留原生计价。

## 发布原则

404token 已确认运行基线为 `8b44d4c107006f736fd114e073be8c6975d828ac`；aiself 为 `3a6d72a16de05c66d16ad2e061f66a5efa115841`。两端分别应用同一补丁，保留各自前端和配置，不重建镜像、不迁移数据库。单实例二进制替换仍需短暂重启，不承诺无中断。发布结果和版本另记验收记录。
