[CmdletBinding()]
param(
    [string]$HostName = "45.113.1.228",
    [int]$Port = 27793,
    [string]$User = "root",
    [string]$RemoteDir = "/opt/sub2api",
    [string]$Branch = "custom/main",
    [string]$ComposeDir = "/opt/sub2api/deploy",
    [string]$Remote = "origin"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$remoteCommand = @"
set -e
cd '$RemoteDir'
git fetch '$Remote' '$Branch' --prune
git switch '$Branch' || git switch -c '$Branch' --track '$Remote/$Branch'
git reset --hard '$Remote/$Branch'
cd '$ComposeDir'
if docker compose version >/dev/null 2>&1; then
  docker compose up -d --build
else
  docker-compose up -d --build
fi
"@

ssh -p $Port "$User@$HostName" $remoteCommand
