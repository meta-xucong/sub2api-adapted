# 回归与验收矩阵

本文件是实施后的要求，不是测试通过记录。既有外部失败探针须整理进正式测试；保持反例在旧候选上失败，在修复提交上通过。新增用例名称由实施提交登记，不伪造已经存在的命令。

## 隔离原则

mock Provider、合成API key/图片、隔离账本/数据库。优先已有Transport注入与httptest；不访问真实生成接口、不写现有余额。环境不支持AF_UNIX/Docker等必须记未验证；禁止把skip变pass或为让CI通过删测试。

## 入口/价格矩阵

F02覆盖raw Chat Completions、CC→Messages、CC→Responses，stream与涉及的buffered路径；按次和按token都测。每例断言HTTP/SSE终态、Provider调用数、usage行数、扣款命令数量与金额、错误类型、重复入账防护。

|场景|必要断言|
|---|---|
|第一帧损坏、无内容无usage|错误，不扣费、不重试产生第二次POST|
|失败无内容，usage缺失/空对象/显式所有字段为0|三入口×按次/按token均不得凭字段存在生成扣款|
|无data error块后正常输出|正常继续，最终usage正确|
|显式空data error|仍错误，不被当注释忽略|
|仅heartbeat/created/message_start后出错|不能把元数据当模型交付；收费资格按无内容/usage判断|
|有内容后错误、无usage|保留当前兼容策略，记录明确预期，不把它冒称官方统一规则|
|usage先到后出错/取消|观测字段不丢，实际金额正确且只入账一次|
|未出内容前取消/总预算、scanErr|先复现各分支，再依无内容无usage不变量处理|
|工具JSON校验失败|已输出与未输出分开；无错误体误计费|
|成功+usage、cache/image细项|保持既有金额、原模型映射/快照|
|可failover错误后成功|既有去重与换号规则不变，无双扣|

真实余额对账只有在单独批准的测试账号/小额预算中进行；mock金额0.25不代表生产币种或价格。

## F01 Wokey矩阵

- JSON重复prompt、model、image、images、nested url，原顺序/倒序、转义同名键。
- 根非对象、两份JSON、深嵌套、截断、体积上限；不用新全局限制替代既有限额。
- 同层重复拒绝；不同对象/数组元素同名合法；大小写不同与Unicode等价但不同码点不被擅自合并。
- Wokey校验在首次规范化前与最终序列化入口都覆盖；直接helper调用也拒绝。
- handler真实审核投影→实际出站multipart；拒绝时Provider=0，生成扣款=0，名额最终释放。允许审核先发生，但不允许未审核差异出站。
- 合法JSON/图片文件multipart、模型映射、未知合法标量保持；非Wokey/非edits对照不改变。
- 既有multipart images拒绝及同名multipart字段用例必须保持。

## F03/F04/F05流与终态

- LF、CRLF、CR，分隔符跨Read边界；仅初始BOM剥离，流中BOM不任意删除。
- 多行data、末尾空data、无冒号data、无data、注释、单个前导空格、未知字段、显式error、坏JSON。
- 行/帧上限边界，带取消的半帧阻塞，EOF伴读取错误；已有EOF兼容和DONE提前识别不回退。
- raw原支持输入的字节回归，转换器语义一致；没有无理由新增goroutine/无界buffer。
- finish_reason stop/length/content_filter/tool_calls/未知，buffered/stream；incomplete理由与事件一致，错误后无成功终态。
- 已有反向转换、多个观察choice、重复finish、非法index、最终usage帧全部回归；不暗改必须收齐n的策略。

## F06计数与计费

用真实小PNG而非任意字符串作正例；字段中partial_image作为普通文字、URL名称仍算完整图。部分事件、空结果、非字符串结果不算；同ID跨事件去重，多个ID计数正确。大小/尺寸计费信息不丢。必须穿过native Responses与Images桥到计费命令，覆盖按图、显式token、混合输出，不仅测counter。

## F07取消评分

Smart Router开/关、高级scheduler开/关四组合；请求取消、总预算过期、绝对deadline已过但Err尚nil、独立attempt超时、真实5xx、成功；保留校准路径对照。

对新账号可断言无新统计项；对已有账号必须种入统计，比较before/after EWMA、TTFT及可观测健康状态，不能只看账户计数。确认跳过评分不会丢usage、泄露slot或多发POST。错误原因与取消先后竞态使用可控时钟/阻塞点，不靠10毫秒sleep。

## F08/F09/F10

F08：合成secret在所有错误/日志中不可见；坏URL与有效HTTP/HTTPS/SOCKS代理的协议/失败关闭行为保持。无网络时用注入Transport。
F09：调用实际Search断言Bearer/body；首尾空白与内嵌非法字符；指定A不会误测B；没候选/全部失败/超时/401/429/成功分类；不能触发本站刷新token或登出。
F10：真实浏览器检查音视频data/同源/受控远端可播放、非法来源被拒；原script/frame限制与自定义CSP保持，不能仅grep字符串。

## 分层命令与收据

先运行各修改包聚焦用例，再按仓库Makefile/CI运行完整后端unit、integration、race关键包，前端typecheck/lint/test/build与安全扫描。实际命令、工具版本、提交SHA、退出码、通过/失败/skip/命令排除分别记入验收收据；不得将重叠组相加。

云端现有Go1.27.2/Node20.20.2仅为已验证工具链记录，实施前按仓库锁文件和CI核验，禁止使用系统同名非Go程序。AF_UNIX/Docker不支持的组须在支持环境补齐，不能修改生产测试规则掩盖。

阶段通过条件：已确认反例全部转绿，控制组仍通过；新增lint/依赖风险为零；既有lint107/full-dev high只能标为遗留门禁，未经明确处置不能写整库CI全绿或生产就绪。

## F11 与最终证据补充

补测统一 API 与旧回退真实入口、父取消/过期、独立 attempt 超时、正常成功、过期结果不修改 token version/凭据、父过期不惩罚账号，以及已提交成功后的清理。保留原先失败用例；helper 级旧规则重建与真实完整入口红例分开记录。具体数量与失败、排除、跳过范围见 06。

embed 测试必须使用当前真实前端构建的 logo.svg，校验 MIME 和完整资产字节，同时保留非指纹/指纹缓存断言。仅修改测试夹具不等于修改静态资源服务逻辑。
