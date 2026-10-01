# Sub2API 小硬盘 VPS 发布流程

本流程用于 AISelf、404token 这类空间和内存有限的 VPS。原则是：构建和压缩在开发机完成，VPS 只做校验、加载和替换单个应用容器；不在 VPS 上拉源码、编译、`docker build` 或执行无边界清理。

## 发布前门禁

1. 在本地固定 Git commit，工作树必须干净。
2. 本地完成定向回归、协议夹具和至少一次完整后端回归；环境相关失败必须有基线对照记录。
3. 用 commit 生成不可变镜像标签，例如 `sub2api-adapted:c28ea7c`，并记录镜像 ID、应用二进制 SHA256。
4. 两台 VPS 使用同一个镜像 tar 和同一个 SHA256。不要在服务器分别构建镜像，也不要用 `latest` 覆盖正在运行的版本。
5. 发布前只读检查服务器的可用空间、inode、内存、Docker 磁盘占用和当前容器状态。空间不足时只删除本次传输产生的明确临时文件；禁止 `docker system prune -a`。

## 本地构建与校验

```bash
TAG=sub2api-adapted:<commit-short-sha>
docker build -f deploy/Dockerfile -t "$TAG" .
docker image inspect "$TAG" --format '{{.Id}}'
docker save "$TAG" -o "sub2api-<commit-short-sha>.tar"
sha256sum "sub2api-<commit-short-sha>.tar"
```

构建机应保留 tar、SHA256、commit SHA 和测试记录。tar 传输完成后再删除，不要把大 tar 留在 VPS 的数据盘。

## VPS 原子替换

在 VPS 上执行的远程脚本必须使用 `set -euo pipefail`，并按以下顺序完成：

```bash
set -euo pipefail
cd /opt/sub2api
test -f docker-compose.yml
test -f .env
df -h /opt /var/lib/docker
df -i /opt /var/lib/docker
free -h
docker system df
docker load -i /tmp/sub2api-<commit-short-sha>.tar
docker image inspect sub2api-adapted:<commit-short-sha> --format '{{.Id}}'
docker compose config >/tmp/sub2api-compose-<commit-short-sha>.yaml
docker compose up -d --no-deps sub2api
```

不得重启 PostgreSQL、Redis、反向代理或其它业务容器。应用容器健康后再删除 `/tmp` 中精确匹配本次发布的 tar；保留上一版镜像作为回滚对象。

## 发布后验收

```bash
docker compose ps sub2api
docker inspect --format '{{.State.Health.Status}}' sub2api
docker exec sub2api sha256sum /app/sub2api
curl --fail --max-time 15 https://<host>/health
```

同时记录：

- 运行镜像标签和镜像 ID；
- `/app/sub2api` SHA256；
- `/v1/models` 是否可读；
- 一个普通非流式请求、一个流式请求、一个工具调用和一个 compact 请求的状态码及 request ID；
- 发布前后容器重启次数、空间和内存。

两台服务器必须以“镜像 tar SHA256 + 二进制 SHA256 + commit SHA”三项一致为准。镜像 ID 因本地 Docker 层重建而不同，不单独作为版本不一致证据。

## 失败回滚

只在健康检查或最小协议探测失败时回滚到已知健康的上一版镜像：

```bash
set -euo pipefail
cd /opt/sub2api
docker compose up -d --no-deps sub2api
docker compose ps sub2api
```

实际回滚前必须把 compose 使用的镜像标签恢复为上一版，并重新执行健康检查。不要删除数据库、Redis 数据卷，不要改生产配置或清空 Docker 全局缓存。

## SSH 约定

Windows 侧复杂命令统一通过仓库的 `hosts/*/connect.ps1 -RemoteScriptPath <script>` 传递。404token 继续使用已验证的菲律宾跳板机双跳脚本，不使用临时 ProxyJump 或嵌套引号的长 `-RemoteCommand`。脚本执行结束后立即删除本地临时脚本和 VPS 上本次 tar，保留校验摘要。

## 适用边界

该流程只解决低资源、可回滚的应用发布，不替代数据库迁移和生产数据变更审批。涉及 migration、配置写入、账号路由或计费规则时，必须先单独备份并做只读核对；不能借发布流程顺便修改生产数据库。
