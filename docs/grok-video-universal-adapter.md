# Grok 视频分组通用适配方案

## 目标与范围

本方案只作用于 Grok 视频请求（`/v1/videos/generations`、视频状态和内容查询）。
不会改变 Grok 文本、Grok 图片或其他平台的路由，也不改变 Smart Router 的排序、冷却和切换策略。

统一入口仍然是 OpenAI-compatible 视频接口；账号级 transport profile 决定向具体上游投影请求的方式。
因此同一个 `grok-video` 分组可以同时放入 Wokey、Subrouter、原生 KIE 以及后续新增线路。

## 现有线路的自动选择

`Account.GrokVideoTransport()` 是唯一的 transport 解析入口，按以下顺序工作：

1. 读取账号凭据中的 `grok_video_transport` 显式值；
2. `wokey`、`wokey_multipart`、`multipart` 选择 Wokey multipart profile；
3. `kie`、`kie_jobs`、`native_kie` 选择 KIE jobs profile；
4. `openai_compat`、`subrouter`、`xai` 或空值选择 OpenAI-compatible profile；
5. 未知值安全回退到 OpenAI-compatible，不会因为新值把请求误发到原生 KIE 端点；
6. 没有显式值时，精确官方主机 `api.wokey.ai` 和 `api.kie.ai` 自动识别，其余主机默认 OpenAI-compatible。

这使历史账号无需修改即可保持原行为：Subrouter 账号继续调用 `/v1/videos/generations`，由 Subrouter 内部转换到 KIE；原生 KIE 账号调用 `createTask/recordInfo`；Wokey 账号使用 `/v1/videos` 的 multipart 约定。

审计过的 `【自用】Subrouter-Grok视频（KIE）` 就属于第一类：Sub2API 看到的是普通 OpenAI-compatible relay，而不是 KIE native account。该账号历史成功记录的创建、状态和内容请求均是标准 `/v1/videos/...`，所以不能把这些成功记录当作“Sub2API 直接调用 KIE createTask”的证据。

## 统一输入层与线路投影

所有视频参考图先进入同一个输入标准化阶段：

- 解析 `image`、`images`、`reference_images` 和 multipart 上传；
- 只允许公网 HTTPS（显式支持的 data URL 除外）；
- 下载时执行 SSRF、重定向、MIME、真实文件头、大小和超时校验；
- 保留原始顺序并限制最多 7 张参考图；
- 不把签名 URL、token 或上游响应原文写入客户端错误或普通日志。

标准化完成后再按 profile 投影：

| Profile | 创建接口 | 参考图投影 |
| --- | --- | --- |
| `openai_compat` | `/v1/videos/generations` | 按聚合器支持的标准 JSON/URL 传递 |
| `wokey_multipart` | `/v1/videos` | 服务端下载后写入 multipart `image[]`，参考图使用 `multimodal_reference` |
| `kie_jobs` | `/api/v1/jobs/createTask` | JSON `input.image_urls[]`；Aiself relay 图默认先由 Sub2API 下载并上传到 KIE 临时文件服务，再提交 KIE 返回的 `downloadUrl` |

KIE 的 JSON schema 与 Wokey multipart 不是同一个协议，不能把 Wokey 的 `image[]` 直接发送到 KIE。KIE 官方创建任务接口使用 `input.image_urls`。[KIE Grok Imagine Video 1.5 Preview](https://docs.kie.ai/market/grok-imagine/1-5-preview)

如果 KIE 不能访问 Aiself relay，KIE profile 会在创建任务前将 `video.aiself.vip` 下的标准化图片由 Sub2API 服务端下载，再通过 KIE 的 multipart 文件流接口上传，最后把返回的临时 `downloadUrl` 放入 `image_urls`。这样 KIE 不再需要访问 Aiself relay。KIE 官方提供 multipart 文件流上传接口，上传文件为临时对象，不会创建视频任务。[KIE File Stream Upload](https://docs.kie.ai/file-upload-api/upload-file-stream)

默认只转换 `video.aiself.vip`，普通公网图片仍原样传递，避免无意义的二次上传。账号凭据可用以下可选项扩展范围：

- `kie_reference_upload_mode=always`：原生 KIE 的所有参考图都先上传；
- `kie_reference_upload_mode=off`：关闭转换，保留旧的公网 URL 行为；
- `kie_reference_upload_hosts=host-a,host-b`：在默认 relay 之外增加精确主机名。

上传阶段沿用公网 HTTPS、DNS/SSRF、重定向、MIME 和 20 MB 单图校验；下载或上传失败会在 `createTask` 前失败，因此不会产生 KIE provider task 或视频计费。上传只增加首提交的网络往返，不增加视频任务次数。

### KIE 临时文件 MIME 保真

KIE 文件流上传不能只依赖文件名后缀。上传 multipart 的 `file` part 必须显式携带已探测到的 `image/png`、`image/jpeg` 或 `image/webp`；否则文件服务可能先返回 `downloadUrl`，但后续 `createTask` 会以 `File type not supported` 拒绝该临时文件。

实现约束：

- 文件名扩展名按真实 MIME 规范化，避免 URL 后缀与图片内容不一致；
- 上传响应若返回 `data.mimeType`，必须是支持的图片类型且与本地探测结果一致；
- MIME 不匹配时在 `createTask` 前失败，不将其伪装成上游任务失败；
- 单元测试必须检查 multipart 文件 part 的 `Content-Type`，并覆盖 KIE 返回不支持 MIME 的场景；
- `input.image_urls[]` 仍只放 KIE 返回的临时 `downloadUrl`，不把本地文件、Data URL 或 Aiself relay URL 直接交给 KIE。

该补丁只改变原生 KIE relay 图片上传的 multipart 元数据和前置校验，不改变 Wokey、Subrouter 或无参考图请求。

## 新增线路的扩展规则

新增线路先判断它属于哪一种协议：

- 如果兼容 `/v1/videos/generations`，无需新增代码，保持默认 `openai_compat`；
- 如果是已知 host 但字段不同，在 transport resolver 中增加一个精确 host matcher 和对应 projection；
- 如果是非标准创建/状态协议，新增一个独立 profile，并通过 `grok_video_transport=<profile>` 显式启用；
- 不允许用模糊域名后缀匹配自动切换到特殊协议，避免把 bearer token 发往仿冒域名。

每个新 profile 必须同时提供创建、状态、内容 URL 构造、输入投影、响应归一化和单元测试。未知 profile 始终回退标准协议，保证旧账号不会被新适配器影响。

## 失败边界与计费

输入校验或图片上传失败时，在上游创建任务前返回 400，不产生 provider task ID，也不触发视频计费或线路健康惩罚。创建任务成功后才进入现有状态轮询和延迟计费流程。

服务端下载/上传会增加少量首提交延迟和网络流量；不会额外创建视频任务。KIE 临时文件按其服务策略自动清理，适合异步任务，但不能替代有效的源图片地址。

## 验收清单

- Wokey 多图仍使用 multipart，且只影响视频生成；
- Subrouter 仍使用标准 `/v1/videos/generations`，不被误判为原生 KIE；
- 官方 KIE host 或显式 `kie_jobs` 使用原生 KIE URL；
- 无图请求不触发图片下载；
- 无效/HTML/非公网/超限图片在付费提交前失败；
- KIE 上传的图片 part 保留正确 MIME，且 KIE 返回的 MIME 不匹配时在创建任务前失败；
- 未来未知 transport 不会误走 KIE；
- 现有 service、handler 和 Grok 视频生命周期测试全部通过。
