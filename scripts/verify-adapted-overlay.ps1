param(
    [switch]$SkipFrontend
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$backend = Join-Path $repoRoot 'backend'
$frontend = Join-Path $repoRoot 'frontend'
$goRoot = & go env GOROOT
$gofmt = Join-Path $goRoot 'bin\gofmt.exe'

Push-Location $backend
try {
    $changedGo = & git -C $repoRoot diff --name-only --diff-filter=ACMR upstream/main -- '*.go'
    if ($changedGo) {
        $backendGo = $changedGo | Where-Object { $_ -like 'backend/*.go' } | ForEach-Object { $_.Substring(8) }
        if ($backendGo) {
            $formatDiff = & $gofmt -d @backendGo
            if ($formatDiff) {
                $formatDiff | Write-Host
                throw 'gofmt check failed'
            }
        }
    }
    & go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go test suite failed' }
    & go test -tags unit ./internal/service ./internal/handler
    if ($LASTEXITCODE -ne 0) { throw 'Tagged unit tests failed' }
} finally {
    Pop-Location
}

if (-not $SkipFrontend) {
    Push-Location $frontend
    try {
        & pnpm install --frozen-lockfile
        if ($LASTEXITCODE -ne 0) { throw 'pnpm install failed' }
        & pnpm build
        if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }
    } finally {
        Pop-Location
    }
}

& git -C $repoRoot diff --check
if ($LASTEXITCODE -ne 0) { throw 'git diff --check failed' }

Write-Host 'Adapted overlay verification passed.'
