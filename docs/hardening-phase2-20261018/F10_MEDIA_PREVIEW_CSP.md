# F10：管理端音视频预览 CSP 的最小默认策略

基线：`e5b9bfbe91c4939befae1278d57141c6fbbbe6a7`。本变更只修改代码默认值和回归检查，不修改部署文件、运行中的安全配置或管理员自定义策略。

## 来源库存与选择

`frontend/src/components/admin/account/AccountTestModal.vue` 将 SSE 的 `audio_url`、`video_url` 直接绑定到原生 `<audio>`、`<video>`。它不通过 `fetch` 转换媒体，不调用 `URL.createObjectURL`，也不构造媒体 `blob:` URL。

|路径|实际来源|默认策略处理|
|---|---|---|
|`AccountTestService.testGrokTTS`|后端将上游音频二进制编码为 `data:<mime>;base64,...`|增加 `data:`|
|`AccountTestService.emitGrokVideoResult` 下载 `/content` 的分支|后端编码的 `data:<mime>;base64,...` 视频|增加 `data:`|
|同函数的状态 URL 直出分支|上游报告的 `http:`、`https:`、`data:`；失败回退还能原样发送报告 URL|仅接受已有同源权限、`data:`、下述确证官方 HTTPS 主机；不据此信任任意上游 URL|
|官方 xAI 视频 URL|`https://vidgen.x.ai/...`，与既有 `grokMediaSignedVideoContentURL` 的精确主机约束一致|增加精确 `https://vidgen.x.ai`|
|同源媒体|保留此前 `default-src 'self'` 给予的 HTTP(S) 同源权限|显式保留 `'self'`|
|`blob:`|这些生产路径没有生成 blob URL|不增加 `blob:` scheme-source；不改变浏览器对 `'self'` 的既有解释|

新默认指令：

```text
media-src 'self' data: https://vidgen.x.ai
```

只新增这一条 `media-src`。`script-src`、`frame-src`、`connect-src`、`frame-ancestors` 以及每请求 nonce 逻辑均不改动。媒体域名不进入脚本或 iframe 白名单；`data:` 媒体不会授权 data 脚本。

来源依据：

- [xAI 官方视频生成说明](https://docs.x.ai/developers/model-capabilities/video/generation)给出的完成响应使用 `video.url=https://vidgen.x.ai/.../video.mp4`，并说明返回的链接是临时链接。核对日期：2026-10-18。
- [CSP Level 3 的 media-src 规则](https://www.w3.org/TR/CSP3/#directive-media-src)专门限制音频、视频和字幕资源；图片或 connect 指令不能代替这条权限。
- 仓库代码：`backend/internal/service/account_test_service.go` 的 `testGrokTTS` / `emitGrokVideoResult`；`backend/internal/service/grok_media.go` 的 `grokMediaSignedVideoContentURL`。

## 有意保留的边界与迁移

没有加入 `https:`、`http:`、`*`、`https://*.x.ai` 或 `blob:`。中转账号可能返回自己的 CDN 地址，而账号测试当前允许直接回传这些 URL；这不构成默认信任全部 HTTPS 站点的理由。因此未知中转/CDN 视频在默认策略下仍可能被浏览器阻止，不能宣称所有提供商的预览已兼容。上游的测试成功也不等于浏览器已播放。

需要这种中转预览时，可选择：

1. 管理员核实真实媒体主机、HTTPS 和重定向目标后，在自定义策略中精确列出该主机。不要复制示例域名到生产，也不要为一次预览加入全协议白名单。
2. 另立项设计受鉴权、大小和超时限制保护的同源媒体代理，复用现有 SSRF/凭证隔离规则；本补丁不引入代理或下载回退重构。

显式自定义 CSP 保持原来的 `media-src`。若原策略没有这条指令，继续使用其原有 `default-src` 回退；不会自动补充 `data:` 或 xAI 主机。现有中间件为验证码/支付等组件补充其他指令的行为未由 F10 更改。使用旧配置中显式填写的 CSP 字符串的部署不会自动获得新媒体权限。管理员应先审查实际响应的 CSP，再决定是否把上述精确媒体源合并进自己的策略。若反向代理另加 CSP，两份策略会同时约束请求，也需独立核实；此处不修改任一部署配置。

## 回归与执行方法

### 后端响应头

```sh
cd backend
go test ./internal/server/middleware -run 'Test(SecurityHeaders|EnhanceCSPPolicy)' -count=1
```

新增 `security_headers_media_test.go` 覆盖空值/空白/显式默认策略的精确媒体源、自定义 `none`/`self`/指定主机和缺失 media 指令的回退保留，以及 nonce、脚本/iframe/connect 指令。它验证实际中间件输出头，但不代替浏览器 CSP 执行验证。

### 真实组件路径

```sh
pnpm --dir frontend exec vitest run src/components/admin/account/__tests__/AccountTestModal.spec.ts
```

新增参数化用例从模拟 SSE 驱动真实组件，检查 data 音频、data 视频、官方 HTTPS 视频、同源 URL 的控件和 MIME，重试后旧媒体清空。jsdom 不实现媒体播放或 CSP，因此这些用例不算浏览器验收。

### 真实浏览器

`tools/check_media_preview_csp.cjs` 使用已有 Playwright 和 Chromium，无需运行应用、调用上游、账号或真实凭证。它读取实际 `DefaultCSPPolicy` 常量，替换 nonce 后作为 HTTP 响应头；以请求拦截提供静音 WAV 和 0.5 秒黑色视频 MP4，所有请求均本地处理。

```sh
# 使用已安装的 Playwright；若它不在 Node 默认解析路径，给出模块绝对路径。
PLAYWRIGHT_MODULE=/path/to/node_modules/playwright \
CHROMIUM_EXECUTABLE=/path/to/chromium \
node tools/check_media_preview_csp.cjs
```

该脚本检查真实 `playing` 事件和 CSP 拒绝：data 音视频、同源音视频、精确官方主机；未知 HTTPS、伪装后缀、未批准子域名/端口、跨源 HTTP 被拒且不发出请求；自定义策略及缺失 media 的回退仍生效；有效 nonce 脚本执行，未批准的内联/data 脚本和媒体主机 iframe 被拒。官方主机测试的媒体内容由本地拦截提供，不代表验证了实时签名 URL、上游响应或生产部署。

当前环境执行结果（2026-10-18）：`node --check` 通过；Chromium 在启动时因 `process_singleton_posix.cc:297: socket() failed: Operation not permitted (1)` 退出，浏览器用例均未运行。没有通过跳过 CSP、伪造结果或调整系统安全权限绕过限制。F10 的真实浏览器门禁仍待在允许 Chromium IPC 的授权环境运行，并补充实际部署响应头和真实上游预览验收。

交接校验：

- 与基线逐字节比较：除新增精确 `media-src` 外，已有默认策略指令全部不变。
- `DefaultCSPPolicy` 字符串的 UTF-8 值（不含换行），SHA-256：`8911435ae7ebfe36ae753b4cdadafbba4daf899bec76cb92ea4acdcc93e0a89c`。
- 浏览器脚本 SHA-256：`6678eafe5fe64711824d3d3444c768208be23557d8077bc3520d1a50c6a69304`。
- MP4 夹具 1,653 字节，SHA-256：`4526ddce1f5d407d86719a820b8502d7b8e12d6c57d60cda5261749ab9fdd186`；`ffprobe` 确认 H.264、16×16、0.5 秒。
- 此处记录的是 F10 初次交接时状态，当时 Go/前端组合测试尚待执行。现已完成的固定版本结果见 [06_IMPLEMENTATION_RECEIPT.md](06_IMPLEMENTATION_RECEIPT.md)；真实浏览器用例仍未运行，不能以组合测试替代。
