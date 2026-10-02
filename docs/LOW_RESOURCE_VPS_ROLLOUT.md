# Sub2API 小硬盘 VPS 发布流程

本流程用于 AISelf、404token 这类空间和内存有限的 VPS。原则是：构建和压缩在开发机完成，VPS 只做校验、加载和替换单个应用容器；不在 VPS 上拉源码、编译、`docker build` 或执行无边界清理。

## 发布前门禁

1. 在本地固定 Git commit，工作树必须干净。
2. 本地完成定向回归、协议夹具和至少一次完整后端回归；环境相关失败必须有基线对照记录。
3. 用 commit 生成不可变镜像标签，例如 `sub2api-adapted:c28ea7c`，并记录镜像 ID、应用二进制 SHA256。
4. 两台 VPS 使用同一个镜像 tar 和同一个 SHA256。不要在服务器分别构建镜像，也不要用 `latest` 覆盖正在运行的版本。
5. 发布前只读检查服务器的可用空间、inode、内存、Docker 磁盘占用和当前容器状态。空间不足时只删除本次传输产生的明确临时文件；禁止 `docker system prune -a`。

## 本地构建与校验

先完成下节两台主机的只读预检，确认相同的当前 Compose 镜像引用并设置 `DEPLOY_REF`。部署 tar 必须包含不可变 commit tag 和 Compose 当前引用这两个 tag，不能使用单 tag 示例。

```bash
TAG=sub2api-adapted:<commit-short-sha>
COMMIT=$(git rev-parse HEAD)
docker build -f deploy/Dockerfile --build-arg COMMIT="$COMMIT" -t "$TAG" -t "$DEPLOY_REF" .
docker image inspect "$TAG" "$DEPLOY_REF" --format '{{.RepoTags}} {{.Id}} {{.Size}}'
docker save "$TAG" "$DEPLOY_REF" -o "sub2api-<commit-short-sha>.tar"
sha256sum "sub2api-<commit-short-sha>.tar"
```

构建机应保留 tar、SHA256、commit SHA 和测试记录。tar 传输完成后再删除，不要把大 tar 留在 VPS 的数据盘。

## VPS 原子替换

先只读取得运行容器的 Compose 工作目录和配置文件列表，不能假定目录是 `/opt/sub2api`，也不能根据目录名猜正在使用的项目。以下示例用 `sub2api` 作为容器名和 service；实际执行前从 `docker ps` 和 `config --services` 核实两者，若不同则统一替换脚本中的 `APP_CONTAINER` / `APP_SERVICE`，不得直接照抄：

```bash
docker inspect --format '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' sub2api
docker inspect --format '{{index .Config.Labels "com.docker.compose.project.config_files"}}' sub2api
```

验证目录与文件均存在后，使用标签列出的**相同 Compose 文件及顺序**、相同 project name，并检测主机实际支持的 `docker compose` 或 `docker-compose`。本次只读预检发现两台 VPS 的目录和 Compose CLI 不同，且应用二进制不在宿主机 bind mount 中；不能安全地原位覆盖 `/app/sub2api` 作为热更新。仍按本节只替换单个应用容器，不触碰数据库、Redis 或代理。

部署前检查、部署和发布后验收脚本必须使用 `set -euo pipefail`，并用下面的 helper 从运行容器取得绝对工作目录、project name、配置文件（含顺序），确认 CLI/文件存在；标签缺失就停止，禁止猜目录或回退到默认 Compose 文件。回滚脚本不依赖可能已丢失的运行容器，使用发布前另行记录的上下文，见下节：

```bash
resolve_compose() {
  APP_CONTAINER=sub2api # set to the verified container name
  APP_SERVICE=sub2api   # set to the verified Compose service
  APP_DIR=$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' "$APP_CONTAINER")
  PROJECT=$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project"}}' "$APP_CONTAINER")
  CONFIG_FILES=$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project.config_files"}}' "$APP_CONTAINER")
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

发布前在两台主机分别只读记录并保存到本地发布记录：容器名、Compose service、working_dir、project name、按原顺序排列的完整 Compose 文件路径、Compose CLI、`DEPLOY_REF`、`OLD_IMAGE_ID`、`ROLLBACK_REF`、health、restart count、DockerRootDir、磁盘可用空间和 inode。确认两台 `DEPLOY_REF` 与目标 service 一致；否则停止并分析差异。Compose 标签用于记录配置路径，`config --services` 用于确认真实 service。Build locally with the immutable commit tag **and** current `DEPLOY_REF`, then save both tags in one tar. Pass the full source commit into the binary metadata. This lets Compose keep its existing image reference while the new image is loaded; preserve the prior image ID under a separate rollback tag before loading:

```bash
COMMIT=$(git rev-parse HEAD)
TAG=sub2api-adapted:<commit-short-sha>
docker build -f deploy/Dockerfile --build-arg COMMIT="$COMMIT" -t "$TAG" -t "$DEPLOY_REF" .
docker image inspect "$TAG" --format '{{.Id}} {{.Size}}'
docker save "$TAG" "$DEPLOY_REF" -o "sub2api-<commit-short-sha>.tar"
sha256sum "sub2api-<commit-short-sha>.tar"
```

On each host, check space **before transferring the tar**. Using the `resolve_compose` helper above, verify the container and service name, then check every filesystem that will hold the tar or Docker layers. Available space must exceed the tar/image size plus a safety buffer; otherwise stop before upload. Run this first, before SCP:

```bash
set -euo pipefail
resolve_compose
SERVICES=$("${COMPOSE[@]}" "${COMPOSE_ARGS[@]}" config --services)
printf '%s\n' "$SERVICES" | grep -Fxq "$APP_SERVICE"
DOCKER_ROOT=$(docker info --format '{{.DockerRootDir}}')
df -h "$APP_DIR" "$DOCKER_ROOT" /tmp
df -i "$APP_DIR" "$DOCKER_ROOT" /tmp
free -h
docker system df
```

After transfer, repeat the read-only checks during deployment as follows:

```bash
set -euo pipefail
resolve_compose
DEPLOY_REF=$(docker inspect --format '{{.Config.Image}}' "$APP_CONTAINER")
TAG=sub2api-adapted:<commit-short-sha>
DOCKER_ROOT=$(docker info --format '{{.DockerRootDir}}')
df -h "$APP_DIR" "$DOCKER_ROOT" /tmp
df -i "$APP_DIR" "$DOCKER_ROOT" /tmp
free -h
docker system df
SERVICES=$("${COMPOSE[@]}" "${COMPOSE_ARGS[@]}" config --services)
printf '%s\n' "$SERVICES" | grep -Fxq "$APP_SERVICE"
"${COMPOSE[@]}" "${COMPOSE_ARGS[@]}" config --images
OLD_IMAGE_ID=$(docker inspect --format '{{.Image}}' "$APP_CONTAINER")
ROLLBACK_REF=sub2api-adapted:rollback-<commit-short-sha>
docker image tag "$OLD_IMAGE_ID" "$ROLLBACK_REF"
echo '<tar-sha256>  /tmp/sub2api-<commit-short-sha>.tar' | sha256sum -c -
docker load -i /tmp/sub2api-<commit-short-sha>.tar
docker image inspect "$TAG" --format '{{.Id}}'
docker image inspect "$DEPLOY_REF" --format '{{.Id}}'
"${COMPOSE[@]}" "${COMPOSE_ARGS[@]}" up -d --no-deps --force-recreate "$APP_SERVICE"
```

不得重启 PostgreSQL、Redis、反向代理或其它业务容器。本仓库应用在正常启动时会先运行内嵌 SQL migrations（`backend/internal/repository/ent.go`）；本次 242/243 迁移只新增带 `IF NOT EXISTS` 的表和索引，不执行既有业务行更新/删除。部署前确认数据库备份策略与连接正常；发布后从应用启动日志和 `schema_migrations` 确认迁移成功，不要另行手工执行同一迁移。若 `/tmp` 与 `APP_DIR`、`DOCKER_ROOT` 不在同一文件系统，分别检查承载 tar、解包层和 Docker 数据的文件系统；每处都需留出 tar/镜像大小及安全余量。应用容器健康检查及最小协议检查均通过后，才删除 `/tmp` 中精确匹配本次发布的 tar；保留上一版镜像作为回滚对象。

## 发布后验收

使用同一 helper 检查服务，禁止重启依赖容器。健康检查和最小协议检查通过、且回滚镜像标签确认存在后，再删除 VPS 临时 tar；本地 SSH 脚本可在 SSH 调用结束后立即删除：

```bash
set -euo pipefail
resolve_compose
DEPLOY_REF=$(docker inspect --format '{{.Config.Image}}' "$APP_CONTAINER")
SERVICES=$("${COMPOSE[@]}" "${COMPOSE_ARGS[@]}" config --services)
printf '%s\n' "$SERVICES" | grep -Fxq "$APP_SERVICE"
"${COMPOSE[@]}" "${COMPOSE_ARGS[@]}" ps "$APP_SERVICE"
docker inspect --format '{{.State.Health.Status}}' "$APP_CONTAINER"
docker exec "$APP_CONTAINER" sha256sum /app/sub2api
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
APP_SERVICE='<recorded-compose-service>'
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
"${COMPOSE[@]}" "${COMPOSE_ARGS[@]}" up -d --no-deps --force-recreate "$APP_SERVICE"
"${COMPOSE[@]}" "${COMPOSE_ARGS[@]}" ps "$APP_SERVICE"
```

`ROLLBACK_REF` 和 `DEPLOY_REF` 必须是本次发布前记录的值。重新执行健康检查后才能结束回滚。不要删除数据库、Redis 数据卷，不要改生产配置或清空 Docker 全局缓存。

## SSH 约定

Windows 侧复杂命令统一通过仓库的 `hosts/*/connect.ps1 -RemoteScriptPath <script>` 传递。404token 继续使用已验证的菲律宾跳板机双跳脚本，不使用临时 ProxyJump 或嵌套引号的长 `-RemoteCommand`。SSH 调用结束后立即删除本地临时脚本。VPS 临时 tar 仅在健康及最小协议检查通过后精确删除；失败时先保留供诊断，确认无需再用后清除。保留校验摘要。

## 适用边界

该流程只解决低资源、可回滚的应用发布，不替代数据库迁移和生产数据变更审批。涉及 migration、配置写入、账号路由或计费规则时，必须先单独备份并做只读核对；不能借发布流程顺便修改生产数据库。
