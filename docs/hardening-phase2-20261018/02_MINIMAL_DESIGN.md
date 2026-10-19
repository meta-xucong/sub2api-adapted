# 最小实现方案与不变量

本文只定义下一阶段开发，不表示实现已完成。每项独立差异、独立回归、独立归属说明。

## F01：Wokey歧义JSON

### 位置与顺序

现有Grok handler在选择具体账号前审核，故不能简单要求在审核前调用依赖账号的Wokey谓词。最小方案：

1. 在prepareGrokMediaForwardBodyForAccount已确认isWokeyGrokImageEdit的JSON分支，任何prepareGrokMediaForwardBody、normalize、sjson改写前，校验原始JSON。
2. 最终prepareWokeyGrokImageEditBody入口复用同一校验器，防止内部直接调用绕过。
3. 拒绝后绝不发Provider POST。可能已做过一次审核，这只是现有流程下的额外成本，不是绕过审核。
4. 若产品要求在审核前就拒绝，需另议整个Grok images-edits入口收紧；本补丁不提前更改全网关契约或重排账号选择/审核。

### 校验规则

使用标准JSON token读取器只读检查，不把body全量marshal成新格式。根必须为对象，仅一个JSON文档；按每个对象独立记录解码后的键名，拒绝任意层级重复键。prompt与pr\u006fmpt同名；大小写保持敏感，不做Unicode归一化。不同数组元素的同名键合法。覆盖嵌套image.url/image.image_url.url及images元素。

UseNumber可避免数值token不必要转浮点；不要全局迁移JSON库。对象键集合随输入大小增长，受现有请求体上限约束，不宣称O(1)；释放已结束对象的键集合，避免额外保留整树。优先迭代栈，避免另引入不受控递归。深度沿用标准解析器/既有有效输入限制，不随意增加新的全站产品限额。

错误复用GrokImageEditInputValidationError，400，无原始body/提示词/密钥回显。非Wokey、非图片编辑、合法未知标量、图片格式与既有字段策略保持原样。所有拒绝都要求无Provider调用、无生成计费，且已获并发名额会正常释放。

## F02：失败结果的可计费资格

修复范围先锁定“上游失败，未输出模型语义内容，未观测usage”这一个确定不变量；不修改成功响应价格、不调整历史账单。

这里的“无usage/零计量”按数值而非字段存在性判定：缺失usage、空usage对象、所有受支持有效计量值均为0，均属零计量。只有已有计量规则认可的正数token/cache/image等计量才构成非零证据；不凭空估算，不把异常或负数仅因字段存在当作收费依据。本补丁不重做全局usage校验。

|情况|处理|
|---|---|
|失败，无模型内容，无usage|不生成可计费partial result；错误类型与不重试语义保留|
|失败，存在可信非零usage|保留原计量与幂等结算，不因HTTP失败清零|
|已有模型内容，但usage为零|本轮保留现有兼容行为，不能声称官方统一保证其按次收费正确；如要改变，另行决策|
|failover继续其他账号|保持既有result=nil/跨尝试策略，不新增一次失败一次成功双扣|
|成功|不因本补丁降低或改变现有价格|

### 实现约束

- CC→Messages/Responses错误分支在生成错误响应之前判断模型内容/usage证据。错误JSON、SSE error、仅header、心跳、response.created/message_start元数据不能自行构成“已交付模型内容”。
- 使用已有scan/state结果和小型局部布尔状态；若需复用，只建纯资格辅助函数，不新增持久billable字段或外部协议。
- 确保内容、工具调用、推理片段的定义在两种转换器中一致；真实写失败不能被当完整交付。取消后的既有计量drain策略不变。
- 原样CC已覆盖streamErr，以及无交付无计量的截断/failover分支（当前547–563行）；保留这些已通过的行为。取消/预算到期（531–536）、已终态后读取错误（540–541）、其他scanErr（570–572）和转换工具参数校验错误分支均列入测试矩阵，不能写成“raw所有失败已覆盖”。未经复现的分支先做探针，证实后沿同一不变量最小修复。
- 不把HTTP 502统一当免费，也不把非nil结果统一当应收费；必须测试扣款命令。

## F03/F04：SSE帧与词法

F03先独立修候选回归：raw观测记录是否实际出现data字段，空块重置状态；无data的event:error不终止后续流，显式data空值的error仍按错误处理。复用现有boundedGatewaySSEFrames语义或共享最小字段状态，不更改所有reader。

F04补CR、LF、CRLF以及仅流首BOM：在受影响的有界scanner/观测路径实现。无冒号的data字段也表示显式空data；仅一个冒号后空格按规范移除，不任意TrimSpace整个payload。分隔符跨Read边界、EOF、最大行字节计数必须精确；单帧上限和取消/drain保持。

raw的观测与原字节转发要分离：本阶段不授权随意重排data行、空格、注释、event字段；如当前转发已规范化换行，应记录现状，不能虚称旧版逐字节保真。为原先支持的LF/CRLF输入增加字节回归，新增CR/BOM按确定契约测试。

保留已记录的EOF末帧兼容、standalone DONE处理。不能以符合浏览器规范为由无声取消上游兼容。错误或缺终态不得生成成功终态；有数据的缺终态不得因BOM走非SSE豁免。

## F05：content_filter终态

只补CC→Responses流式/非流式：finish_reason=content_filter → incomplete及incomplete_details.reason=content_filter；不再生成response.completed。length仍max_output_tokens，stop/tool_calls保留现状。反向Responses→CC已有content_filter处理，做回归而非重写；不改CC→Anthropic其他映射。

多choice或错误/过滤组合优先级必须沿既有选择/终态政策审查，不新增强制等待全部n的行为。分清“不完整”与传输错误，不把内容过滤改成自动换账号重试。

## F06：图片产物计数

删除整个item.Raw包含partial_image就排除的策略。只对明确的结构化部分事件/type/status做过滤；已完成、有效非空字符串产物按现有规则计数。不得借此恢复请求n兜底、把非字符串强制转产物、重复计算同一ID。

同一ID在部分事件、output_item.done、最终output数组重复出现仍只算一次；多张实际完成图仍计多张；prompt或URL含普通partial_image文本不影响。

检验native Responses与Images桥消费结果、BillingModel和实际扣款，而非只测Count。混合文本/图片请求仍按其明确价格方案，不把ImageCount=0自动推论整请求免费。

## F07：fork总预算与高级scheduler

在Images持有requestCtx的结果上报边界复用OpenAIImageRequestContextErr。请求级取消或总预算终止不把最后一个账号记为失败；没有请求级终止的独立attempt超时继续按既有规则记录/换号。

不要在全局scheduler.ReportResult过滤所有DeadlineExceeded；不要影响Embeddings、Compact或校准流程，不新增第三套健康状态。区分EWMA、Smart Router和熔断器，分别断言。若Provider已返回可信独立故障而取消随后发生，保留既有可证明因果，不用单一时间字符串重分类；具体竞态以确定性ctx/mock测试定义。

成功、usage结算与错误响应不得因跳过评分被跳过。总预算开始时机与排队上下文不在本补丁改造。

## F08/F09：官方旧代理错误与Tavily

F08先做：解析失败不要格式化原URL或整个*url.Error；只返回稳定安全分类。websearch使用同一安全解析器，成功代理协议/DNS行为必须保持；不得直接按新helper默认将socks5语义改变而不审查。测试坏端口、坏转义、userinfo、query中的合成secret、IPv6、成功带凭据代理及各层日志；Redacted只隐藏userinfo密码，不是所有秘密的通用过滤器。不能只把%w改为%v。

F09按当前官方契约发Authorization Bearer，body不再含api_key。保存与既存配置运行时加载均处理首尾粘贴空白，不无声重写所有已存配置，错误不回显key；不另造凭据源。Transport级测试实际Search请求，不只检查手造结构体。

测试目标建议显式provider_type（当前配置禁止重复类型时足够）；兼容旧不传字段请求的原有语义，但UI卡片测试必须指定目标。若现有唯一性约束变化，先确认稳定ID而不是随意取第一项。成功必须显示实际被测provider。

错误对管理员返回脱敏的本地typed错误；不要把上游401透传成本站401。保留可排障分类，不回显上游原始正文/代理凭据。无候选、全部失败、部分候选成功分开；本补丁不改生产provider择优和配额策略。

## F10：媒体预览CSP

先库存实际预览URL方案和来源。默认策略增加最小media-src；data:为已知需要，blob:/可信https来源按代码和浏览器用例确认，不能无依据扩展到所有协议。可优先让受控预览保持同源，若要重构媒体代理需另立项，不能偷偷扩大本修复。

自定义CSP不得被无声放宽；提供迁移说明/提示，由管理员在部署时确认。部署安全策略变更须另行授权。保留script/frame/connect规则及既有nonce行为。
