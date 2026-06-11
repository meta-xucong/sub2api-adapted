$ErrorActionPreference = 'Stop'

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..')
$expectedBranch = 'custom/main'
$currentBranch = git -C $repoRoot branch --show-current

if ($currentBranch -ne $expectedBranch) {
    throw "Run this script on $expectedBranch. Current branch: $currentBranch"
}

Write-Host "Checking Veyra overlay files..."

$requiredPaths = @(
    'backend/internal/veyra',
    'extensions/veyra/portal',
    'backend/internal/server/router.go',
    'backend/internal/server/http.go',
    'backend/internal/config/config.go',
    'backend/cmd/server/wire_gen.go',
    'frontend/src/views/auth/LoginView.vue',
    'docs/CUSTOM_PATCHES.md'
)

foreach ($path in $requiredPaths) {
    $fullPath = Join-Path $repoRoot $path
    if (-not (Test-Path $fullPath)) {
        throw "Missing required Veyra overlay path: $path"
    }
}

Write-Host "Syncing editable portal assets into Go embed directory..."
& (Join-Path $repoRoot 'extensions/veyra/portal/build.ps1')

Write-Host "Running focused Veyra tests..."
git -C $repoRoot diff --check
Push-Location (Join-Path $repoRoot 'backend')
try {
    go test ./internal/veyra ./internal/config ./internal/server ./cmd/server
} finally {
    Pop-Location
}

Write-Host "Veyra overlay is present and testable on custom/main."
