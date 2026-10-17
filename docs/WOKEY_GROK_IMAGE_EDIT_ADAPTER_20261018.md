# Wokey Grok 图生图薄适配模块开发与验收文档

**状态：**实施前冻结版 v1
**目标仓库：**`D:/AI/AGENT_U盘/.tmp-sub2api-build-37a5d260`
**基线提交：**`37a5d260abbcddb3e40749cfcd1a158fa82c0330`
**参考证据日期：**2026-10-18
**范围：**仅适配 Wokey 上游的 Grok `/v1/images/edits` 图生图请求。

## 1. 目标与硬约束

### 1.1 目标

Sub2API 现有 Grok 通用图像编辑层把 multipart 上传序列化为 xAI 官方 JSON 格式。Wokey 的图像编辑接口要求图片文件字段，导致经网关转发时出现 `image[0] must be base64 or a base64 data URL`。增加一个独立、可拔除的 Wokey 专用适配器，将受支持的 Grok 图像编辑输入转为 Wokey 的 multipart 文件请求。

### 1.2 硬约束

1. 适配器只作用于 `PlatformGrok` 账户、目标上游为规范化的 `https://api.wokey.ai`、媒体端点为 `GrokMediaEndpointImagesEdits` 的请求。
2. xAI 官方端点、其他兼容中转、其他 Wokey 端点和其他平台的请求必须走当前实现，字节/Content-Type 语义不变。
3. 不修改通用 xAI JSON 序列化函数的行为，不改路由、账户选择、模型映射、限流、计费、响应解析、视频状态/下载流程。
4. 不修改现有 `grok_wokey_video.go`、`grok_video_transport.go`、媒体 URL 构造、数据库、配置项、前端或部署文件。适配器不增加管理开关；规范化精确 Wokey 主机即为唯一自动启用条件。
5. 不发真实上游请求，不接触真实凭据，不提交、推送或部署。验收使用离线请求体单测和本地 HTTP mock。
6. 保留工作区里在本任务前已存在的所有改动和未跟踪文件。

### 1.3 非目标

- 不改变 Wokey 文生图 `/images/generations`。
- 不改变 Wokey 文生视频、图生视频或参考图视频；它们继续由当前视频代码处理。
- 不将 Wokey 的 `duration_seconds`、`ratio`、`video_resolution` 协议扩展至图片接口。
- 不改变 xAI 官方图生图 JSON 契约。
- 首版不支持 Wokey 文档未明确承诺的 mask、多图、外链图片下载、异步图像任务或额外文件字段；请求遇到不支持的图像形态时在本地返回明确、可定位的 4xx 错误，不向上游发送猜测格式。

## 2. D/I/A 冻结

- **D0：**端点、主机、上游格式差异与回归要求均有官方文档及已有直连成功/网关失败证据；不需要引入设计分支。
- **I1：**需要在单一媒体转发管线增加一个账号/端点分派点、一个序列化 helper 和有边界的测试；不改现有通用 helper。
- **A2：**上游协议、认证请求路由和计费前向链路属于 provider/public-contract 边界；严格验证请求只能被精确 Wokey 主机接收，且不能回归其他上游。
- **源码/协议保真：**本任务的行为基准为 Wokey 与 xAI 官方协议文档，而不是另一个实现仓库。独立 Source Fidelity 复核应在代码冻结后逐项核对文档字段、适配范围、差异矩阵及测试证据；普通代码审计另行执行。
- **协作路由：**主会话请求的实现/审计角色不等于已观测运行路由。若子任务运行元数据不可见，标记 `ROUTE_UNVERIFIED`，不声称模型路由已验证。

## 3. 冻结的协议证据

### 3.1 Wokey

- 官方图像文档：<https://wokey.ai/docs/images>
- 截止 2026-10-18 的已读取内容：图像编辑 endpoint 为 `/v1/images/edits`；文档写明 “Image edits use the image file field”。图像生成 endpoint `/v1/images/generations` 使用 JSON 示例。
- 本任务开始前的直接调用记录：向 Wokey `/v1/images/edits` 发送 multipart 图片文件字段的 Grok 图生图请求成功；经 404token 的同类请求因 JSON `image` 格式被拒绝。此项是既有会话证据，本轮不重复收费调用。

### 3.2 xAI

- 官方图像编辑文档：<https://docs.x.ai/developers/model-capabilities/images/editing>
- 截止 2026-10-18 的已读取内容：`/v1/images/edits` 使用 `application/json`；图片使用 `image.url` 的公开 URL 或 base64 Data URL，也可用 `file_id`；文档明确说明使用 multipart 的 OpenAI `images.edit()` 不受支持。

### 3.3 当前请求链路

1. `backend/internal/service/grok_media.go::forwardGrokMedia` 构造目标 URL。
2. 当前通用 `prepareGrokMediaForwardBody` 对编辑端点将 multipart 文件转换为 xAI JSON Data URL；JSON 输入则保留为 JSON 并规范化图片字段。
3. 之后 `normalizeGrokMediaForwardBodyForAccount`、计费模型提取、账户模型映射和 `sanitizeGrokMediaForwardBody` 继续运行。
4. `backend/internal/service/grok_wokey_video.go` 已有独立 Wokey 视频传输逻辑，但该模块属于视频端点，不是图片编辑适配器。

## 4. 代码清单与允许写入范围

### 4.1 允许新增/修改

| 文件 | 变更 | 边界 |
|---|---|---|
| `docs/WOKEY_GROK_IMAGE_EDIT_ADAPTER_20261018.md` | 本开发文档及最终验收记录 | 只描述本适配 |
| `backend/internal/service/grok_wokey_image_edit.go` | 新建隔离的 Wokey 图像编辑 serializer 与精确条件判断 | 不承载通用 xAI 行为，不复用/修改视频行为 |
| `backend/internal/service/grok_media.go` | 在所有现有 JSON 解析、账户模型映射、sanitize 完成后、创建上游 body reader 之前加一个窄分派；仅精确 Wokey edits 将通用请求体准备/输入解析错误映射为专用 client validation error | 适配只接收最终已规范化 JSON；其他请求完全走旧路径 |
| `backend/internal/service/grok_wokey_image_edit_test.go` | 新建适配器和精确匹配测试；覆盖 PNG/JPEG/WebP、边界尺寸、像素上限，以及 Wokey multipart 空文件的 typed 本地拒绝 | 只新增本模块测试 |
| `backend/internal/service/openai_gateway_grok_test.go` | 仅在需要时补充端到端 mock 路由断言 | 不重写既有测试 |
| `backend/internal/handler/grok_media.go` | 将新的 Wokey 图像编辑输入校验错误映射为 HTTP 400，并在账号调度失败上报之前结束该请求 | 只扩展现有输入校验早退分支；不得改动选择、重试、计费或其他 provider 行为 |
| `backend/internal/handler/grok_media_test.go` | 验证 Wokey 图像输入校验错误映射为 400，其他错误仍交给现有上游错误分支 | 只增加窄映射测试 |

### 4.2 明确禁止修改

`grok_wokey_video.go`、`grok_video_transport.go`、`grok_upstream_url.go`、其他 provider 适配、公共 JSON helper、账户模型、schema、数据库、前端、配置默认值、模型目录、计费算法及生产环境。

当前工作区中，`grok_wokey_video.go` 有任务开始前未提交改动（其视频参考图 Data URL 校验/解码相关变更）。这些改动归属基线之外的既有工作，必须原样保留；新图像 adapter 不能借机格式化、重命名、挪动或依赖它们。

## 5. 实现规格

### 5.1 适配分派

在 `forwardGrokMedia` 完成通用处理之后、构造上游 body reader 之前挂接唯一适配器：

```text
if endpoint == images_edits AND account is Grok AND
   xai.IsWokeyAPIBaseURL(account.GetGrokMediaBaseURL()):
    prepareWokeyGrokImageEditBody(final normalized JSON body)
    Content-Type = newly generated multipart/form-data boundary
else:
    body and Content-Type remain exactly as produced by current pipeline
```

必须用已有 `xai.IsWokeyAPIBaseURL` 精确规范化判断，不得 `contains`、后缀匹配或模型名猜测。不得使用账户的 `grok_video_transport` 显式覆盖值作为图片适配开关，以免视频传输设置影响图片。

原始请求必须先完整经过现有 generic prepare、account normalization、计费/请求信息提取、账户模型映射和 sanitize。**禁止在这些步骤之前把 body 转成 multipart**：后续一些处理只处理 JSON body，提前切换会导致账户模型映射和图片参数清理被静默跳过。Wokey serializer 是上游发送前的最后一项 body 变换，不得再进入任何通用 JSON 改写。

### 5.2 输入及输出

首版适配器接收的是已经走完通用解析的 JSON，且只接受一个源图像，支持：

1. 客户端 JSON 中 `image` 或唯一 `images[0]` 的 `url`/字符串为 `data:image/{jpeg,png,webp};base64,...`，可带官方 JSON `type` 字段。
2. 客户端若以 multipart 上传，沿用现有通用解析器转换为 JSON Data URL；最后适配器再解码该 Data URL 并生成 Wokey 文件字段。不得为此改动通用 parser。

规则：

- 输出 multipart boundary 必须由 Go `mime/multipart.Writer` 生成，并以 writer 的 `FormDataContentType()` 设置请求头；不能复用入站 boundary。
- 输出文件名采用安全固定名；Content-Type 必须由实际字节签名确定/验证，支持 JPEG/PNG/WebP；不信任请求自报 MIME 与实际文件字节不匹配的输入。
- `model`、`prompt` 必须作为文本表单字段传递。`model` 使用本地管线完成账户模型映射后的上游模型值。现有通用流程会先做 Grok alias normalization，再创建 `requestInfo`，所以适配不得尝试恢复/改写原始 client model；保持当前 alias normalization、requestInfo 和计费 identity 原样，只让账户映射后的最终模型进入 multipart。`prompt` 保持规范化后 JSON 中的原字符串。为了不丢掉现有请求中的兼容参数，其余顶层 JSON 字段只接受 string/number/bool/null：string 使用解码后的原字符串，number 使用 JSON 原始数字 token，bool 使用小写 `true`/`false`，null 不写入 form；字段名和值作为文本 part 传递。`image`、`images`、`mask` 除外。未知对象/数组字段本地拒绝，不猜测其 Wokey 表示法。Wokey 文档当前没有完整列出 image-edit 表单 schema；只有文件字段、model、prompt 是文档或已有调用证据确认的契约，不声称其他字段有 Wokey 专属语义。
- JSON 图片须限制为恰好一张；无图、多图、外部 URL、mask、非 base64 Data URL、无效 MIME、格式不支持、空/损坏/过大文件须明确拒绝。PNG/JPEG/WebP 使用 Go 解码器执行完整结构校验；宽或高超过 8192、总像素超过 16 Mi 像素的输入在解码前拒绝，避免畸形/超大图像造成资源放大。该适配器的用户输入校验错误由 handler 映射为 HTTP 400，不记录为上游账号故障。不得在本模块中自行下载任意用户 URL，避免新增 SSRF/安全边界。
- 上传字节上限复用 `openAIImageMaxUploadPartSize`（20 MiB）。当前通用 multipart parser 使用 `LimitReader`，可能把超限 part 截断到上限而不返回专门的超限错误；因此 Wokey serializer 对解码后长度 `>= 20 MiB` 的图像 fail closed，避免把截断文件继续发出。
- 最终 Wokey 请求只带一个名为 `image` 的文件字段；不能把 mask/多图无意丢弃。`image` 与 `images` 同时出现、多个图像引用或多文件输入均拒绝，避免解析顺序改变语义。
- 失败在网关本地返回，不产生上游请求，不进入上游重试。

### 5.3 模型与计费语义

适配只改变线上 body 编码和 Content-Type。适配前必须从入站请求提取计费/审查需要的 Grok model、prompt、图像尺寸和图片内容；现有请求信息解析和计费流程保持有效。适配不得改变既有 requestInfo/billing identity 或账户模型映射；Wokey multipart 的 `model` 字段使用最终映射后的上游模型值。

Wokey serializer 读取已完成通用规范化的最终 JSON；源图像字节从唯一 `image.url` Data URL 解码并校验。不得从 moderation body、日志文本或 MIME 声明反向猜测图片字节。不要调整通用读取顺序。

## 6. 测试与验收清单

### 6.1 单元测试

- 精确选择：规范 Wokey Base URL（含规范 `/v1` 形式）+ Grok + images_edits 命中；xAI 官方 URL、其他 relay、look-alike `api.wokey.ai.evil.example`、非 HTTPS、其他 endpoint 不命中。
- JSON Data URL → multipart：验证字段 `image` 是文件、文件字节完整、Content-Type 与文件一致、model/prompt/标量字段保留、boundary 与请求头一致。
- 入站 multipart 经原通用解析器转为 JSON Data URL 后再生成 Wokey multipart：验证模型 alias、账户模型映射、图片几何清理先于序列化执行；输出中映射后的模型和规范化参数不丢失。
- 无效输入矩阵：0/2 张图片、非 Data URL、未声明/不支持 MIME、MIME 与 magic bytes 不匹配、错误 base64、结构截断/损坏、超尺寸或超像素上限、JSON 空文件、multipart 空文件、达到20 MiB、mask、外部 URL、缺 model/prompt（按当前请求验证规则）。每项均无上游网络调用，并由 handler 映射为 400，不报告账号调度失败。
- Wokey 不相关链路字节等价：images_generations、videos_generations、status/content 对请求 body/content type 继续走原处理。
- xAI 原生编辑回归：同样的 JSON 图片引用仍为 application/json 且保持现有字段规范化；multipart 仍由旧 `prepareGrokMediaForwardBody` 生成官方 JSON Data URL。

### 6.2 HTTP mock 集成

使用本地 httptest server 或可注入 transport 验证：

1. Wokey Grok 图生图命中时收到 multipart，解析到 model/prompt/image 原始字节正确。
2. Wokey 文生图仍收到原有 JSON。
3. Wokey 文生视频和图生视频仍由当前视频 serializer 产生原有路径/模式/字段；预先存在的 `grok_wokey_video.go` dirty diff 不被覆盖。
4. xAI 官方图生图仍收到 JSON，不得收到 multipart。
5. 非 Wokey relay 的图生图仍收到原有 JSON。
6. 精确 host mismatch / 端点 mismatch 时新适配器未被调用。
7. 原始 multipart 文件字节数大于 20 MiB 时，既有解析器虽然将 part 截断至 20 MiB，Wokey serializer 必须通过“拒绝长度 `>= 20 MiB`”检测触顶截断并返回本地错误；mock server 调用次数为 0。

所有测试禁止外网访问，禁止真实 Wokey API key 和生产服务。

### 6.3 命令验收

在 `backend` 目录运行：

```powershell
go test -tags unit ./internal/service -run 'WokeyGrokImageEdit|GrokImageEdit|ForwardGrokMedia' -count=1
go test -tags unit ./internal/service -count=1
go test -tags unit ./...
```

如全量 backend 测试受已知环境依赖影响，准确记录失败测试、错误及是否涉及本改动；不得把未执行或失败描述成通过。`gofmt` 只允许作用于本次新增/修改文件，不触碰既有脏文件。

### 6.4 代码审计与 Source Fidelity

- 文档先由独立只读审计者检查完整性、范围和源协议准确性；PASS 后才实施。
- 实现冻结后，独立 Source Fidelity 复核官方协议映射和所有负向边界；再由另一名独立 Audit 审核全 diff、共享流程风险、错误处理和测试证据。
- FAIL 时只按审计报告做最小修复，冻结新 diff 后重跑相关测试并重新审计。
- 缺少独立子任务身份/终态或运行时路由信息时，独立性或路由按工具实际证据标注；不得虚报。

## 7. 实施顺序

1. 保存并核对 dirty baseline；确认本任务允许文件没有与既有工作重叠（`grok_media.go` 有本次挂接点，先阅读其当前 diff/状态）。
2. 独立审计本文档并修订至 PASS；文档变更后冻结版本号/hash。
3. 新建适配 helper 和测试，随后在单一 `grok_media.go` 调用点加入精确分派。
4. 每个小阶段检查 diff，确认未触碰视频、生成、路由、计费或配置代码。
5. 执行 6.1–6.3；用外部网络禁用/httptest 证据确认没有真实上游调用。
6. 冻结实现 commit-tree/diff hash；分别执行 Source Fidelity 和普通代码 Audit。
7. 仅在测试和两项独立复核均通过后报告代码验收结论。当前任务不包含 Git commit、GitHub 同步、生产部署或真实计费生成。

## 8. 完成定义

只有同时满足下列条件才报告“已完成”：

- Wokey 的 Grok 图生图按文件字段 multipart 发送且请求 mock 解析通过；
- Wokey 文生图、文生视频、图生视频及其他视频端点没有被新逻辑改变；
- xAI 和其他 relay 的图生图仍保持原生 JSON 行为；
- 无效或不支持的输入在本地 fail-closed，且不发上游请求；
- 项目测试通过并有命令记录；
- 文档审计、Source Fidelity、独立代码审计分别通过，范围和基线外修改均完整保留。

## 9. 本轮执行记录

### 9.1 文档审计

- 独立只读文档审计：PASS（2026-10-18）。审计提出并已修订 serializer 执行顺序、Wokey 字段契约、20 MiB parser 截断检测、账户映射后的 model 语义及既有计费身份保持。
- 初版文档 hash（实施前）：`231E81C439CBD597941923E1E027C109453E23DD29503C8844AE326AFE127F04`。该值仅标识实施前版本，不代表最终规格。
- 最终规格包含本节 4.1 新增的 handler 错误映射及测试范围；为避免文档内自引用哈希，最终文档 SHA256 在本轮验收记录中单独提供，并作为代码审计冻结版本。

### 9.2 实现状态

- 新增 `backend/internal/service/grok_wokey_image_edit.go` 独立 serializer/精确匹配器。
- `backend/internal/service/grok_media.go` 仅在既有 JSON 规范化、计费请求信息解析、账户 model 映射和 sanitize 后、创建上游 body reader 前调用该适配器。
- 新增 `backend/internal/service/grok_wokey_image_edit_test.go`，包含精确 host/endpoint 匹配、Data URL multipart、无效输入、完整且有界的 PNG/JPEG/WebP 解码、尺寸/像素上限、20 MiB 截断防护、Wokey 图生图端到端 mock、超限/损坏/空 multipart 图像零上游调用、Wokey 文生图 JSON 保持测试。
- `backend/internal/handler/grok_media.go` 仅增加 Wokey 图生图输入校验错误到 HTTP 400 的早退映射；该分支在上报账号调度结果前结束，不影响原视频校验和上游错误处理。
- `backend/internal/handler/grok_media_test.go` 新增输入校验返回 400、其他错误仍交给现有上游错误分支的回归。
- 未修改 `grok_wokey_video.go` 或 `grok_video_transport.go`；它们在本任务前已有的工作区变更原样保留。

### 9.3 当前测试结果

- 通过：`go test -tags unit ./internal/service ./internal/handler -run 'Wokey|GrokMediaInputValidation' -count=1`（Wokey 路由及 handler 输入校验定向测试）。
- 通过：`go test -tags unit ./internal/service ./internal/handler -count=1`（完整受影响包测试；service 208.712 秒，handler 44.261 秒）。
- 未通过：`go test -tags unit ./...`。`internal/service`、handler 及其他多数包通过；`internal/repository` 有既有/环境相关失败：`TestAliyunCaptchaVerifier_TransportError` 的 transport 错误断言失败；3 个 `TestPgDumper...` 因 Windows 环境无 `sh` executable 失败。上述文件/包不在本次 diff 中；不能将全仓库测试标为通过。
- 未调用 Wokey、xAI 或生产网关；mock 上游请求不出本机。
- 最终规格的独立普通代码审计：PASS；Source Fidelity：PASS。两项均绑定于最终代码及文档哈希，确认 Wokey-only 分派、输入错误处理和图像解码边界符合本方案。

### 9.4 独立代码审计与 Source Fidelity

- 独立代码审计：PASS。确认空 multipart 文件和适配器输入错误在本地返回 400、不会发送上游请求或上报账号调度失败；PNG/JPEG/WebP 的完整解码、尺寸与像素限制有回归覆盖；xAI 和其他平台/端点保持原路径。
- Source Fidelity：PASS。官方文档支持 Wokey `images/edits` 使用图片文件字段、xAI 原生格式使用 JSON；审计确认专属路由和代码顺序符合协议差异。
- 两项审计均未调用 provider 或生产环境。模型可用性仍以当前 Wokey 账号授权为准，本轮未做真实生成。
