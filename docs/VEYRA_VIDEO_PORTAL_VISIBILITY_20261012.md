# Video OS 门户入口可见性修复（2026-10-12）

## 发现

Video OS 卡片已存在于桌面和移动页面源码，但桌面入口网格固定为两列，第三张卡片落在第二行；移动端入口列表也在其他应用之后。首屏可只显示 API Gateway 与 Alchemy Media Agent，造成入口被删除的观感。

线上 Nginx 还对 `/_veyra/*.js`、`*.css` 增加 `Cache-Control: public, max-age=600`。这些资源使用稳定文件名而非内容哈希，因此浏览器可能在发布后继续复用旧布局脚本/样式。

## 修复

- 页面设计严格恢复到最早包含 Video OS 的历史版本 `c56179836c08058ef6cd9ea68a66f0d5809b40cf`：保留原来的介绍区、三张应用卡片顺序和两列桌面网格，Video OS 作为第三张卡片；不新增置顶 CTA，也不改造为三列布局。
- Video OS 卡片继续使用既有 `data-route="video"` 与 ticket 登录桥接。
- 后端对所有 `/_veyra/` 静态资源设置 `Cache-Control: no-cache`。
- 线上 Nginx 不再覆盖门户 JS/CSS 的缓存策略；由后端 `no-cache` 响应负责重新验证。不得对未指纹化的 `/_veyra/*.js|css` 使用固定 max-age。

## 验证

- `go test ./internal/veyra`：通过（包含临时 Redis 集成测试）。
- `git diff --check`：通过。
- 发布后需验证桌面与移动页面保持原版三卡片顺序与布局，并保留 Video OS 卡片；`/_veyra/app.js`、`/_veyra/styles.css`、`/_veyra/mobile.html` 均返回 `Cache-Control: no-cache`；`/api/veyra/portal/config` 返回经校验的 `https://video.aiself.vip`。

## 边界

此修复只改变门户显示/缓存，不触碰登录票据业务、计费账本、Provider、数据库 schema 或视频服务。部署只更新 Sub2API 候选门户服务和对应 Nginx 缓存头；保留旧容器与镜像以便回滚。
