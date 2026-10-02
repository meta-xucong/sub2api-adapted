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

先只读取得运行容器的 Compose 工作目录和配置文件列表，不能假定目录是 `/opt/sub2api`，也不能根据目录名猜正在使用的项目：

```bash
docker inspect --format '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' sub2api
docker inspect --format '{{index .Config.Labels "com.docker.compose.project.config_files"}}' sub2api
```

验证目录与文件均存在后，使用标签列出的**相同 Compose 文件及顺序**、相同 project name，并检测主机实际支持的 `docker compose` 或 `docker-compose`。本次只读预检发现两台 VPS 的目录和 Compose CLI 不同，且应用二进制不在宿主机 bind mount 中；不能安全地原位覆盖 `/app/sub2api` 作为热更新。仍按本节只替换单个应用容器，不触碰数据库、Redis 或代理。

部署前检查、部署和发布后验收脚本必须使用 `set -euo pipefail`，并用下面的 helper 从运行容器取得绝对工作目录、project name、配置文件（含顺序），确认 CLI/文件存在；标签缺失就停止，禁止猜目录或回退到默认 Compose 文件。回滚脚本不依赖可能已丢失的运行容器，使用发布前另行记录的上下文，见下节：

```bash
resolve_compose() {
  APP_DIR=$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' sub2api)
  PROJECT=$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project"}}' sub2api)
  CONFIG_FILES=$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project.config_files"}}' sub2api)
  test -n "$APP_DIR" && test -d "$APP_DIR"
  test -n "$PROJECT" && test -n "$CONFIG_FILES"
  cd "$APP_DIR"
  if docker compose version >/dev/null 2>&1; then COMPOSE=(docker compose)
  elif command -v docker-compose >/dev/null 2>&1; then COMPOSE=(docker-compose)
  else echo 'Compose CLI unavailable' >&2; return 1
  fi
  COMPOSE_ARGS=(-p "$PROJECT")
  IFS=',' read -r -a FILES <<< "$CONFIG_FILES"
  for file in "${FILES[@]}"; do
    [[ "$file" = /* ]] || file="$APP_DIR/$file"
    test -f "$file"
    COMPOSE_ARGS+=(-f "$file")
  done
}
```

Before building, record `DEPLOY_REF=$(docker inspect --format '{{.Config.Image}}' sub2api)` on both hosts; stop if they differ. Build locally with the immutable commit tag **and** current `DEPLOY_REF`, then save both tags in one tar. Pass the full source commit into the binary metadata. This lets Compose keep its existing image reference while the new image is loaded; preserve the prior image ID under a separate rollback tag before loading:

```bash
COMMIT=$(git rev-parse HEAD)
TAG=sub2api-adapted:<commit-short-sha>
docker build -f deploy/Dockerfile --build-arg COMMIT="$COMMIT" -t "$TAG" -t "$DEPLOY_REF" .
docker image inspect "$TAG" --format '{{.Id}} {{.Size}}'
docker save "$TAG" "$DEPLOY_REF" -o "sub2api-<commit-short-sha>.tar"
sha256sum "sub2api-<commit-short-sha>.tar"
```

On each host, ensure available disk exceeds image size plus a safety buffer. Then use one remote script:

```bash
set -euo pipefail
resolve_compose
DEPLOY_REF=$(docker inspect --format '{{.Config.Image}}' sub2api)
TAG=sub2api-adapted:<commit-short-sha>
DOCKER_ROOT=$(docker info --format '{{.DockerRootDir}}')
df -h "$APP_DIR" "$DOCKER_ROOT"
df -i "$APP_DIR" "$DOCKER_ROOT"
free -h
docker system df
"${COMPOSE[@]}" "${COMPOSE_ARGS[@]}" config --images
OLD_IMAGE_ID=$(docker inspect --format '{{.Image}}' sub2api)
ROLLBACK_REF=sub2api-adapted:rollback-<commit-short-sha>
docker image tag "$OLD_IMAGE_ID" "$ROLLBACK_REF"
echo '<tar-sha256>  /tmp/sub2api-<commit-short-sha>.tar' | sha256sum -c -
docker load -i /tmp/sub2api-<commit-short-sha>.tar
docker image inspect "$TAG" --format '{{.Id}}'
docker image inspect "$DEPLOY_REF" --format '{{.Id}}'
"${COMPOSE[@]}" "${COMPOSE_ARGS[@]}" up -d --no-deps --force-recreate sub2api
```

不得重启 PostgreSQL、Redis、反向代理或其它业务容器。应用容器健康后再删除 `/tmp` 中精确匹配本次发布的 tar；保留上一版镜像作为回滚对象。

## 发布后验收

使用同一 helper 检查服务，禁止重启依赖容器：

```bash
set -euo pipefail
DEPLOY_REF=$(docker inspect --format '{{.Config.Image}}' sub2api)
resolve_compose
"${COMPOSE[@]}" "${COMPOSE_ARGS[@]}" ps sub2api
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
APP_DIR='<recorded-absolute-compose-working-dir>'
PROJECT='<recorded-compose-project-name>'
DEPLOY_REF='<recorded-pre-release-image-reference>'
ROLLBACK_REF=sub2api-adapted:rollback-<commit-short-sha>
COMPOSE_FILES=('<recorded-compose-file-1>' '<recorded-compose-file-2-if-any>')
cd "$APP_DIR"
if docker compose version >/dev/null 2>&1; then COMPOSE=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then COMPOSE=(docker-compose)
else echo 'Compose CLI unavailable' >&2; exit 1
fi
COMPOSE_ARGS=(-p "$PROJECT")
for file in "${COMPOSE_FILES[@]}"; do test -f "$file"; COMPOSE_ARGS+=(-f "$file"); done
docker image tag "$ROLLBACK_REF" "$DEPLOY_REF"
"${COMPOSE[@]}" "${COMPOSE_ARGS[@]}" up -d --no-deps --force-recreate sub2api
"${COMPOSE[@]}" "${COMPOSE_ARGS[@]}" ps sub2api
```

`ROLLBACK_REF` 和 `DEPLOY_REF` 必须是本次发布前记录的值。重新执行健康检查后才能结束回滚。不要删除数据库、Redis 数据卷，不要改生产配置或清空 Docker 全局缓存。

## SSH 约定

Windows 侧复杂命令统一通过仓库的 `hosts/*/connect.ps1 -RemoteScriptPath <script>` 传递。404token 继续使用已验证的菲律宾跳板机双跳脚本，不使用临时 ProxyJump 或嵌套引号的长 `-RemoteCommand`。脚本执行结束后立即删除本地临时脚本和 VPS 上本次 tar，保留校验摘要。

## 适用边界

该流程只解决低资源、可回滚的应用发布，不替代数据库迁移和生产数据变更审批。涉及 migration、配置写入、账号路由或计费规则时，必须先单独备份并做只读核对；不能借发布流程顺便修改生产数据库。
