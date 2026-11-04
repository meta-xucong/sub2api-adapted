# YeToken Sonnet/GPT 文本实际账单计费修正

## 目标与证据

用户要求修正 YeToken Sonnet 缓存计价和 GPT 网页搜索附加费差额。只改变这些请求的用户侧 ActualCost，不改模型请求/响应、调度、原生 TotalCost、账号成本或历史余额。

基线：109611def17174732c89c2780bb65438ef6e722c，工作分支 fix/yetoken-text-actual-billing-20261105。

历史证据见 `D:\AI\SSH\TEXT-LINES-BILLING-AUDIT-20261105.md`。210 笔唯一 Sonnet 候选中 170 笔有超舍入差额；38 笔本地无 5m 明细而上游账单有。两笔 GPT 差额精确对应上游网页搜索费。

2026-11-05 的小型合成 Sonnet 请求确认：HTTP 响应头 `x-oneapi-request-id` 与消费账单 `request_id` 精确一致；响应 usage 仅有聚合缓存写入 605，账单有 5m 写入 605。因此不能通过伪造明细或单改一个倍率完整解决。

YeToken `/api/log/token` 会忽略 request_id 查询参数，返回最近 1,000 条记录；完整读取约 1MB 曾耗时 55 秒。新请求账单按新到旧排列，流式读取精确目标后即结束的实测为 1.052 秒、857 字节压缩流量。该接口只读，quota_per_unit=500000，显示换算系数为 1。

## 最小实施

1. 新增独立 YeToken 账单读取器，不重构现有 Wokey 实现。固定请求 `https://yetoken.vip/api/log/token`，复用账号已有 API key，单次 HTTP 查询总时限 3 秒。使用 JSON 流式解码，取得同 request_id、type=2 的 quota 后立即关闭响应。无新增重试、分页、缓存或后台补扣任务。
2. 在 GatewayService、OpenAIGatewayService 已有线路计价之后、原生原子扣款之前接入。只处理已识别 YeToken API-key、原有有效精确 group/account/model 文本价卡、既有 token 计价资格，且模型为 gpt-* 或 claude-sonnet-*。不扩展 Kimi、Opus、Wokey 或媒体。
3. 复用账号 `extra.upstream_request_id_header`，启用目标账号的 `x-oneapi-request-id`，不修改全局响应头/协议规则，不以时间、模型或 Token 猜配扣费。
4. 应收金额为 `quota / 500000 × 本次组/用户倍率`，按既有余额精度量化。quota 已含线路采购折扣，不再乘线路倍率或汇率。只覆盖 ActualCost 和既有线路计价标记。
5. 查询失败或账单尚不可见时，不改动原有计算结果，并记录脱敏错误码；这是本方案的准确性限制，不声称这些请求已经按实际账单核准。不新增静默零价、猜配或历史调账。

## 直接测试与发布验收

- 读取器：精确 ID/type 匹配、quota 金额、只读固定端点、找到目标后不等完整列表、缺失/不可读账单保留原有计算结果。
- 两个 RecordUsage 接入：历史 Sonnet/GPT quota 样本、不同用户倍率、余额与 API-key quota 同金额、TotalCost/usage 不变；非 YeToken、非目标模型、未命中价卡不受影响。
- 复跑相关既有线路计价与 Wokey 实际账单回归；独立代码和范围审计。
- 启用目标账号关联头后，使用小型合成请求核对本地上游 ID、消费账单、ActualCost/余额扣费。真实调用失败不算通过；不重放用户请求。
- 发布沿现有流程，源码提交与 GitHub 同步；沿已有二进制更新方式部署，不重做镜像。分别记录 404token/aiself 版本及适用范围。

## 实施与本地验收

- 新增 `unified_gateway_yetoken_actual_billing.go`；两个服务的 constructor 各增加一个读取器，两个既有 RecordUsage 结算接缝各调用一次。原 Wokey 实现未修改。
- 新增两份针对性测试；`go test ./internal/service -run 'Test.*YeToken' -count=1 -timeout 120s` 通过，执行时间 3.409s。
- gzip 测试提供不结束的历史列表，证明命中精确记录后即关闭；超时测试确认只结束查询子上下文，不取消结算父上下文。
- Sonnet/GPT 的 RecordUsage 测试确认 ActualCost、余额和 API-key 配额一致，TotalCost、账号配额成本及原始 usage 不变。
- 独立源码审计通过，范围审计无越界。Responses 网页搜索没有设置独立 `/alpha/search` 专用的 WebSearchCalls 字段；因此这些历史差额请求沿既有资格门控可进入新结算，不扩大资格规则。

状态：本地实施及专项测试通过，相关回归与线上验收结果另行记录。
