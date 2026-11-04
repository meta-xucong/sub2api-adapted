# Wokey Claude #37 价卡和实际账单结算修正

用户授权最小修复、审计测试、GitHub 和两端二进制热更新；不重做镜像。

基线：404token `96cc1c671`，aiself `91494541e`。当前适配工作区 `D:\AI\SSH\sub2api`；这是当前计费缺口修复，不重新执行旧 v0.2.13 适配阶段。

## 改动

- `isWokeyAPIKeyAccount` 新增 Anthropic 分支，使用已有 GetBaseURL。现有响应头识别、精确账单查询和两条 RecordUsage 自动复用。
- 不新增网络查询机制、重试、fallback、校验、路由或协议逻辑。原先查询失败保留卡价并告警的行为仍在，不能声称这种情况已取得实际账单。
- 生产将 #37 加入既有 Wokey 同步账号名单 `[27,28]`，FX 保持 6.9。自动卡不设额外线路倍率，统一组倍率保持 1.25，所以用户扣费为实际 USD × 6.9 × 1.25，按既有八位精度结算。
- 同步器沿既有行为为选中账号保存目录价卡；#37 的模型映射仍只有原有 12 个 Claude。未映射模型的价卡不等于新增模型路由，不另加过滤规则。
- 不更改 #27/#28 的选线和模型映射，不处理此次审计发现的其他旧模型/未映射模型。

## 测试与发布

- 直接验证 Anthropic Wokey 可以进入原账单客户端，自动取得 x-wokey-request-id；其他 Anthropic 上游保持不匹配。
- Gateway 与 OpenAI RecordUsage（普通 Responses 和压缩标记）使用精确上游账单结算，验证用户余额/API-key 配额与 ActualCost 一致、原始 usage/TotalCost/账号成本保持不变。
- 复跑相关 Wokey、YeToken、线路价卡、结算、压缩回归及独立代码审计。
- 源码同步 GitHub；两端分别基于原有提交构建，复用二进制替换和短暂重启流程，保留旧二进制回滚。不是无中断滚动更新。
- 生产确认 #37 的 12 个模型都有当前 Wokey 卡；使用专用测试 Key 合成请求，通过实际 #37 的响应头 ID 与 Wokey 账单精确核对用户扣费和余额。不重放真实用户请求。

准确性仍受已有账单查询时限、可见性和原有计价资格限制；本轮不新增延迟补扣或历史调整。

## 本地结果

- 生产差异仅新增 Anthropic 分支两行；现有两个测试中的旧拒绝预期同步改为非 Wokey Anthropic 负例，并新增直接结算测试。
- 独立代码与范围审计通过。组 #16 只读确认：倍率 1.25，高峰加价关闭，长上下文加价关闭，effort 映射为空。
- `go test ./internal/service -run 'Test.*(Wokey|YeToken|UnifiedGatewayRoute|RecordUsage|UsageBilling|UpstreamRequestID|Compaction)' -count=1 -timeout 180s` 通过（4.370s）。这是相关回归，不是全仓测试。
- 线上版本、同步结果及真实账单验收在发布记录中另记。
