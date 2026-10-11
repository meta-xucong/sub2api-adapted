# Video OS 门户入口可见性修复（2026-10-12）

## 发现

Video OS 卡片已存在于桌面和移动页面源码，但桌面入口网格固定为两列，第三张卡片落在第二行；移动端入口列表也在其他应用之后。首屏可只显示 API Gateway 与 Alchemy Media Agent，造成入口被删除的观感。

线上 Nginx 还对 `/_veyra/*.js`、`*.css` 增加 `Cache-Control: public, max-age=600`。这些资源使用稳定文件名而非内容哈希，因此浏览器可能在发布后继续复用旧布局脚本/样式。

## 修复

- 桌面版与移动版在应用卡片之前增加“打开 Video OS”直达按钮，仍复用 `data-route="video"` 的既有登录与 ticket 桥接，不新增身份或扣费路径。
- 桌面宽屏改为三列，761–1000px 使用两列，窄屏维持单列。
- 后端对所有 `/_veyra/` 静态资源设置 `Cache-Control: no-cache`。
- 线上 Nginx 不再覆盖门户 JS/CSS 的缓存策略；由后端 `no-cache` 响应负责重新验证。不得对未指纹化的 `/_veyra/*.js|css` 使用固定 max-age。

## 验证

- `go test ./internal/veyra`：通过（包含临时 Redis 集成测试）。
- `git diff --check`：通过。
- 发布后需验证公网桌面与移动页面均提供首屏直达按钮和 Video OS 卡片；`/_veyra/app.js`、`/_veyra/styles.css`、`/_veyra/mobile.html` 均返回 `Cache-Control: no-cache`；`/api/veyra/portal/config` 返回经校验的 `https://video.aiself.vip`。

## 边界

此修复只改变门户显示/缓存，不触碰登录票据业务、计费账本、Provider、数据库 schema 或视频服务。部署只更新 Sub2API 候选门户服务和对应 Nginx 缓存头；保留旧容器与镜像以便回滚。
