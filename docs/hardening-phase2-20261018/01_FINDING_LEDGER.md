# 二次复核登记与来源

所有行号以e5b9bfbe为准，后续以函数名和新提交复算。独立失败探针证明的是当前候选，不自动证明历史版本在相同输入下产生相同结果。

|ID|级别|归属/证据|核心位置|处置|
|---|---|---|---|---|
|F01|P1|fork旧Wokey，adapter与2bed相同且官方无文件；审核首prompt/出站末prompt mock实测|grok_media.go:155；grok_wokey_image_edit.go:205–229|Wokey限定歧义JSON拒绝|
|F02|P1|官方旧非nil错误结果机制；当前Messages/Responses实测502且零token扣0.25；历史未同输入重跑|openai_gateway_*_chat_fallback.go；handler/openai_gateway_handler.go|限定无模型输出/无usage错误不生成计费结果|
|F03|P2|本轮候选回归；无data error块使raw流提前断开|openai_gateway_chat_completions_raw.go:434、461|raw观测同步data字段存在性|
|F04|P2|官方/基线词法限制；CR被误读、BOM隐藏坏首帧|gateway_bounded_sse.go；openai_gateway_response_handling.go|受影响reader补词法，字节转发另验|
|F05|P2|官方/基线终态遗漏；content_filter流/非流被完成|chatcompletions_responses_bridge.go:1375、1845|双向状态矩阵补全|
|F06|P2|官方/基线全JSON子串误判；真实PNG+revised_prompt包含partial_image计数0|image_output_accounting.go:284|结构化判断partial|
|F07|P2|fork总预算+既有高级scheduler交互；启用scheduler后超时记失败，Smart Router已有保护|handler/openai_images.go:426；openai_account_scheduler.go:2647|Images调用点限定保护第二套评分|
|F08|P2|官方/基线；精确源码副本复现合成密码进入解析错误|pkg/proxyurl/parse.go:45；websearch/manager.go:418|安全错误分类和统一解析|
|F09|P2|官方旧契约；源码与Tavily文档不符，未实测所有套餐认证失败|websearch/tavily.go；manager.go:354|官方Bearer+定向测试诊断|
|F10|P2|官方旧默认CSP；源码+规范确认，浏览器尚未跑|config.go:35；AccountTestModal.vue:262/276|最小媒体源策略+浏览器验收|

## 可用依据与强度

1. Gin官方要求异步任务不读取原始Context： https://gin-gonic.com/en/docs/middleware/goroutines-inside-a-middleware/ 。上一轮快照修复复核成立，本阶段不重新设计。
2. 官方维护者已合并#5154： https://github.com/Wei-Shaw/sub2api/pull/5154 。明确已观测token保留、零usage不产生partial result、failover不双扣。这不授权统一改造所有“有输出无usage”的收费政策。
3. 官方已合并#3215： https://github.com/Wei-Shaw/sub2api/pull/3215 。真实产物计数而非请求数；不等于计数器每个边界已完善。
4. SSE规范： https://html.spec.whatwg.org/multipage/server-sent-events.html#event-stream-interpretation 。有data/无data、BOM和CR/LF规则有明确依据。候选既有EOF兼容策略不在本阶段无声改成严格浏览器行为。
5. Go JSON安全说明： https://pkg.go.dev/encoding/json/v2#hdr-Security_Considerations 。解析器差异支持拒绝歧义输入，不支持全仓库贸然迁移json/v2。
6. OpenAI响应schema： https://github.com/openai/openai-python/blob/main/src/openai/types/responses/response.py ， https://raw.githubusercontent.com/openai/openai-python/main/src/openai/types/responses/response_output_item.py 。支持content_filter不完整语义以及completed/result/revised_prompt组合。
7. #7201公开问题与Tavily当前接口： https://github.com/Wei-Shaw/sub2api/issues/7201 ， https://docs.tavily.com/documentation/api-reference/endpoint/search 。原body认证不是本轮真实证明对所有key都无效。
8. #7070与CSP规范： https://github.com/Wei-Shaw/sub2api/issues/7070 ， https://www.w3.org/TR/CSP3/#directive-media-src 。不应为了修播放而关闭CSP。
9. 原始文件URL错误语义： https://pkg.go.dev/net/url#Error 。%v不是脱敏。

## 证据保留与排除项

- 失败探针使用外部overlay/源码副本；工作树未被测试夹具污染。
- F01仅service真实构造+审核投影，未模拟整个审核Provider部署；新实施必须补handler级链路。
- F02实际执行真实计费服务到mock扣款命令；0.25是测试价格。
- F07实测高级scheduler记账；并未证明熔断/封号/cooldown触发。
- F06计数及账单模型选择路径成立，未实测真实金额。
- F08仅合成口令，未读取生产日志。
- 没有将无生产调用者的旧下载helper宣布为可达SSRF；没有凭native Responses未标ImageRequest推断混合响应必然虚假收费。
- #7791/#7862/#6252未合并提案不等同官方已发布修复。

## 实施中新增的 F11

完整运行又定位到官方 PR #4221 继承的两处 OAuth 刷新截止检查遗漏，级别 P2。统一 API 与旧版回退都可能在绝对期限已到、Err 尚未发布时放行结果。最小修复、确切反例强度和持久化边界见 07；没有新增调度或数据库协议。另有两项测试隔离修正、SVG 品牌资源夹具修正和一项测试声明 lint 修正，均不改变对应生产功能。最终实测覆盖见 06；本文件上文的证据限度描述保留为设计阶段历史记录。
