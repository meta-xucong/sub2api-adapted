$ErrorActionPreference = 'Stop'

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = Resolve-Path (Join-Path $scriptDir '..\..\..')
$target = Join-Path $repoRoot 'backend\internal\veyra\portal_dist'

New-Item -ItemType Directory -Force -Path $target | Out-Null
Get-ChildItem -Path $target -Force | Remove-Item -Recurse -Force

$excluded = @('build.ps1')
Get-ChildItem -Path $scriptDir -Force |
  Where-Object { $excluded -notcontains $_.Name } |
  Copy-Item -Destination $target -Recurse -Force

Write-Host "Veyra portal assets synced to $target"
