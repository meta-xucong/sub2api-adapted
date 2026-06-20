[CmdletBinding()]
param(
    [string]$HostName = "45.113.1.228",
    [int]$Port = 27793,
    [string]$User = "root",
    [string]$RemoteDir = "/opt/sub2api/src",
    [string]$Branch = "custom/main",
    [string]$ComposeDir = "/opt/sub2api/deploy",
    [string]$Remote = "origin",
    [string]$Service = "sub2api",
    [string]$HealthURL = "http://127.0.0.1:8080/health",
    [string]$PortalBaseURL = "http://127.0.0.1:8080",
    [string]$EncryptedKeyPath,
    [string]$EncryptedSshScript,
    [string]$Passphrase,
    [switch]$SkipBuild
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function ConvertTo-RemoteLiteral {
    param([string]$Value)

    return $Value.Replace("\", "\\").Replace('"', '\"')
}

$remoteTemplate = @'
set -euo pipefail

SRC_DIR="__REMOTE_DIR__"
BRANCH="__BRANCH__"
COMPOSE_DIR="__COMPOSE_DIR__"
REMOTE="__REMOTE__"
SERVICE="__SERVICE__"
HEALTH_URL="__HEALTH_URL__"
PORTAL_BASE_URL="__PORTAL_BASE_URL__"
SKIP_BUILD="__SKIP_BUILD__"

compose_cmd() {
  if docker compose version >/dev/null 2>&1; then
    docker compose "$@"
  else
    docker-compose "$@"
  fi
}

wait_service_health() {
  for i in $(seq 1 60); do
    status=$(docker inspect "$SERVICE" --format '{{.State.Status}}/{{if .State.Health}}{{.State.Health.Status}}{{else}}no-health{{end}}' 2>/dev/null || true)
    echo "health[$i]=$status"
    if echo "$status" | grep -q 'running/healthy'; then
      curl -fsS "$HEALTH_URL" >/dev/null
      return 0
    fi
    sleep 2
  done
  docker logs --tail 160 "$SERVICE" || true
  return 1
}

verify_veyra_portal() {
  tmp_app="/tmp/veyra-portal-app-$$.js"
  tmp_home="/tmp/veyra-portal-home-$$.html"
  curl -fsS "$PORTAL_BASE_URL/" -o "$tmp_home"
  grep -F 'Veyra Agent' "$tmp_home" >/dev/null
  curl -fsS "$PORTAL_BASE_URL/_veyra/app.js" -o "$tmp_app"
  grep -F 'home: "/_veyra/return?target=home"' "$tmp_app" >/dev/null
  grep -F 'state.authenticated ? "/" : loginUrl(routeTargets.home)' "$tmp_app" >/dev/null
  grep -F 'target === "home"' "$tmp_app" >/dev/null
  if grep -F 'state.authenticated ? "/dashboard" : loginUrl(routeTargets.home)' "$tmp_app" >/dev/null; then
    echo "old dashboard login-return behavior is still present" >&2
    return 1
  fi
  rm -f "$tmp_app" "$tmp_home"
}

ensure_veyra_active_config() {
  data_mount=$(docker inspect "$SERVICE" --format '{{range .Mounts}}{{if eq .Destination "/app/data"}}{{.Type}}:{{if eq .Type "volume"}}{{.Name}}{{else}}{{.Source}}{{end}}{{end}}{{end}}' 2>/dev/null || true)
  if [ -z "$data_mount" ]; then
    echo "No active /app/data mount found; skipping Veyra active-config sync."
    return 0
  fi

  case "$data_mount" in
    volume:*)
      volume_name="${data_mount#volume:}"
      data_dir=$(docker volume inspect "$volume_name" --format '{{.Mountpoint}}')
      ;;
    bind:*)
      data_dir="${data_mount#bind:}"
      ;;
    *)
      echo "Unsupported /app/data mount descriptor: $data_mount" >&2
      return 1
      ;;
  esac

  active_config="$data_dir/config.yaml"
  source_config="$COMPOSE_DIR/data/config.yaml"
  if [ ! -f "$active_config" ]; then
    echo "Active config missing: $active_config" >&2
    return 1
  fi
  if grep -q '^veyra:' "$active_config"; then
    echo "Active config already has Veyra block."
    return 0
  fi
  if [ ! -f "$source_config" ] || ! grep -q '^veyra:' "$source_config"; then
    echo "Active config lacks Veyra block and no source Veyra block exists at $source_config" >&2
    return 1
  fi

  backup="$active_config.before-veyra-$(date +%Y%m%d-%H%M%S)"
  cp "$active_config" "$backup"
  awk 'BEGIN{show=0} /^veyra:/ {show=1} show{print} show && /^[^[:space:]].*:/ && $0 !~ /^veyra:/ {exit}' "$source_config" >> "$active_config"
  echo "Appended Veyra config to active /app/data config. Backup: $backup"
}

cd "$SRC_DIR"
git fetch "$REMOTE" "$BRANCH" --prune
git switch "$BRANCH" >/dev/null 2>&1 || git switch -c "$BRANCH" --track "$REMOTE/$BRANCH"
git reset --hard "$REMOTE/$BRANCH"

commit=$(git rev-parse --short HEAD)
image_tag="sub2api-adapted:custom-main-$commit"
rollback_tag=""
if docker ps -a --format '{{.Names}}' | grep -qx "$SERVICE"; then
  old_image_id=$(docker inspect "$SERVICE" --format '{{.Image}}')
  rollback_tag="sub2api-adapted:rollback-before-$commit-$(date +%Y%m%d-%H%M%S)"
  docker tag "$old_image_id" "$rollback_tag"
  echo "Rollback image tag: $rollback_tag"
fi

rollback_on_failure() {
  code=$?
  if [ "$code" -ne 0 ] && [ -n "${rollback_tag:-}" ]; then
    echo "Deployment failed; restoring $rollback_tag to sub2api-adapted:custom-main" >&2
    set +e
    docker tag "$rollback_tag" sub2api-adapted:custom-main
    cd "$COMPOSE_DIR"
    compose_cmd up -d --no-deps --force-recreate "$SERVICE"
    wait_service_health
  fi
  exit "$code"
}
trap rollback_on_failure ERR INT TERM

if [ "$SKIP_BUILD" != "true" ]; then
  DOCKER_BUILDKIT=1 docker build \
    -t "$image_tag" \
    --build-arg GOPROXY=https://goproxy.cn,direct \
    --build-arg GOSUMDB=sum.golang.google.cn \
    --build-arg COMMIT="$commit" \
    --build-arg DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -f Dockerfile \
    .
else
  docker image inspect "$image_tag" >/dev/null
fi

ensure_veyra_active_config

docker tag "$image_tag" sub2api-adapted:custom-main
cd "$COMPOSE_DIR"
compose_cmd up -d --no-deps --force-recreate "$SERVICE"
wait_service_health
verify_veyra_portal

printf '%s\n' "$commit" > "$SRC_DIR/.deployed-commit"
trap - ERR INT TERM

echo "DEPLOY_OK commit=$commit image=$image_tag"
docker inspect "$SERVICE" --format 'SERVICE_IMAGE={{.Config.Image}} SERVICE_IMAGE_ID={{.Image}}'
docker ps --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}'
df -h /
'@

$replacements = @{
    "__REMOTE_DIR__"      = ConvertTo-RemoteLiteral $RemoteDir
    "__BRANCH__"          = ConvertTo-RemoteLiteral $Branch
    "__COMPOSE_DIR__"     = ConvertTo-RemoteLiteral $ComposeDir
    "__REMOTE__"          = ConvertTo-RemoteLiteral $Remote
    "__SERVICE__"         = ConvertTo-RemoteLiteral $Service
    "__HEALTH_URL__"      = ConvertTo-RemoteLiteral $HealthURL
    "__PORTAL_BASE_URL__" = ConvertTo-RemoteLiteral $PortalBaseURL
    "__SKIP_BUILD__"      = if ($SkipBuild) { "true" } else { "false" }
}

$remoteCommand = $remoteTemplate
foreach ($entry in $replacements.GetEnumerator()) {
    $remoteCommand = $remoteCommand.Replace($entry.Key, $entry.Value)
}

if ($EncryptedKeyPath) {
    if (-not $EncryptedSshScript) {
        $envScript = $env:VEYRA_ENCRYPTED_SSH_SCRIPT
        if ($envScript) {
            $EncryptedSshScript = $envScript
        }
    }
    if (-not $EncryptedSshScript) {
        throw "EncryptedSshScript is required when EncryptedKeyPath is provided."
    }

    $invokeArgs = @{
        EncryptedKeyPath = $EncryptedKeyPath
        HostName         = $HostName
        Port             = $Port
        User             = $User
        ExtraSshArgs     = @(
            "-o", "StrictHostKeyChecking=no",
            "-o", "UserKnownHostsFile=/dev/null",
            "-o", "ServerAliveInterval=30",
            "-o", "ServerAliveCountMax=20"
        )
        RemoteCommand    = $remoteCommand
    }
    if ($Passphrase) {
        $invokeArgs.Passphrase = $Passphrase
    }

    & $EncryptedSshScript @invokeArgs
} else {
    ssh `
        -o StrictHostKeyChecking=no `
        -o UserKnownHostsFile=/dev/null `
        -o ServerAliveInterval=30 `
        -o ServerAliveCountMax=20 `
        -p $Port `
        "$User@$HostName" `
        $remoteCommand
}
