[CmdletBinding()]
param(
    [string]$Branch = "custom/main",
    [string]$UpstreamRemote = "upstream",
    [string]$UpstreamBranch = "main",
    [switch]$SkipChecks
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Invoke-Step {
    param(
        [Parameter(Mandatory)]
        [string]$Name,
        [Parameter(Mandatory)]
        [scriptblock]$Script
    )

    Write-Host "==> $Name"
    & $Script
}

$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $repoRoot

if (-not (git rev-parse --is-inside-work-tree 2>$null)) {
    throw "Not inside a git repository."
}

$status = git status --porcelain
if ($status) {
    throw "Working tree is not clean. Commit or stash changes before updating."
}

Invoke-Step "Fetch upstream" {
    git fetch $UpstreamRemote $UpstreamBranch --prune
}

Invoke-Step "Checkout $Branch" {
    git switch $Branch
}

Invoke-Step "Rebase custom patches onto $UpstreamRemote/$UpstreamBranch" {
    git rebase "$UpstreamRemote/$UpstreamBranch"
}

if (-not $SkipChecks) {
    $frontendDir = Join-Path $repoRoot "frontend"
    if (Test-Path (Join-Path $frontendDir "package.json")) {
        Invoke-Step "Frontend install" {
            Push-Location $frontendDir
            try {
                pnpm install --frozen-lockfile
            }
            finally {
                Pop-Location
            }
        }

        Invoke-Step "Frontend targeted tests" {
            Push-Location $frontendDir
            try {
                pnpm test:run src/views/admin/__tests__/groupsMessagesDispatch.spec.ts src/composables/__tests__/useModelWhitelist.spec.ts
            }
            finally {
                Pop-Location
            }
        }

        Invoke-Step "Frontend typecheck" {
            Push-Location $frontendDir
            try {
                pnpm typecheck
            }
            finally {
                Pop-Location
            }
        }
    }

    if (Get-Command go -ErrorAction SilentlyContinue) {
        Invoke-Step "Go targeted tests" {
            Push-Location (Join-Path $repoRoot "backend")
            try {
                go test ./internal/service ./internal/handler
            }
            finally {
                Pop-Location
            }
        }
    }
    else {
        Write-Warning "Go executable was not found; skipped backend Go tests."
    }
}

Write-Host "Update complete. Review with: git log --oneline $UpstreamRemote/$UpstreamBranch..$Branch"
