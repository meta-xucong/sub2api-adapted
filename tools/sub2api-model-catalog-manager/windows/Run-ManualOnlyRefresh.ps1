[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$ManagerRoot,

    [Parameter(Mandatory = $true)]
    [string]$ConfigurationPath,

    [Parameter(Mandatory = $true)]
    [string]$ProtectedApiKeyPath
)

$ErrorActionPreference = 'Stop'

$managerRoot = [IO.Path]::GetFullPath($ManagerRoot)
if (-not [IO.Path]::IsPathRooted($ConfigurationPath)) {
    $ConfigurationPath = Join-Path $managerRoot $ConfigurationPath
}
$configuration = [IO.Path]::GetFullPath($ConfigurationPath)
$protectedKeyPath = [IO.Path]::GetFullPath($ProtectedApiKeyPath)
$executable = [IO.Path]::GetFullPath((Join-Path $managerRoot 'bin\model-catalog-manager.exe'))
$logDirectory = Join-Path $managerRoot 'logs'

foreach ($path in @($executable, $configuration, $protectedKeyPath)) {
    if ($path.Contains('"')) {
        throw 'A configured path contains an unsupported quote character.'
    }
}
if (-not (Test-Path -LiteralPath $executable -PathType Leaf)) {
    throw 'The model catalog manager executable is unavailable.'
}
if (-not (Test-Path -LiteralPath $configuration -PathType Leaf)) {
    throw 'The manager configuration is unavailable.'
}
if (-not (Test-Path -LiteralPath $protectedKeyPath -PathType Leaf)) {
    throw 'The user-protected admin API key file is unavailable.'
}

New-Item -ItemType Directory -Path $logDirectory -Force | Out-Null
$logPath = Join-Path $logDirectory ('manual-only-{0}.log' -f (Get-Date -Format 'yyyyMMdd'))
$utf8NoBom = [System.Text.UTF8Encoding]::new($false)
$exitCode = 1

function Write-RunLogLine {
    param([string]$Text)
    [IO.File]::AppendAllText($logPath, $Text + "`r`n", $utf8NoBom)
}

function Write-RunLogStream {
    param([string]$StreamName, [string]$Content)
    foreach ($line in ($Content -split "`r?`n")) {
        if ($line.Length -gt 0) {
            Write-RunLogLine ("[{0}] {1}" -f $StreamName, $line)
        }
    }
}

Write-RunLogLine "[$(Get-Date -Format o)] starting manual-only model refresh"
$process = New-Object System.Diagnostics.Process
$started = $false
$secureKey = $null
$plainKey = $null
$keyPointer = [IntPtr]::Zero
$startInfo = $null
try {
    $protectedKey = [IO.File]::ReadAllText($protectedKeyPath).Trim()
    if (-not $protectedKey) {
        throw 'The user-protected admin API key file is empty.'
    }
    $secureKey = ConvertTo-SecureString -String $protectedKey -ErrorAction Stop
    $keyPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secureKey)
    $plainKey = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($keyPointer)
    if ([string]::IsNullOrWhiteSpace($plainKey) -or $plainKey.Length -gt 16384 -or $plainKey -match '\s') {
        throw 'The decrypted admin API key has an invalid shape.'
    }

    $startInfo = New-Object System.Diagnostics.ProcessStartInfo
    $startInfo.FileName = $executable
    $startInfo.Arguments = 'run --manual-only --config "{0}"' -f $configuration
    $startInfo.WorkingDirectory = $managerRoot
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $startInfo.StandardOutputEncoding = $utf8NoBom
    $startInfo.StandardErrorEncoding = $utf8NoBom
    $startInfo.EnvironmentVariables['CODEX_404TOKEN_ADMIN_API_KEY'] = $plainKey
    $process.StartInfo = $startInfo
    if (-not $process.Start()) {
        throw 'The model catalog manager process did not start.'
    }
    $started = $true
    [void]$startInfo.EnvironmentVariables.Remove('CODEX_404TOKEN_ADMIN_API_KEY')
    [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($keyPointer)
    $keyPointer = [IntPtr]::Zero
    $secureKey.Dispose()
    $secureKey = $null

    $stdoutTask = $process.StandardOutput.ReadToEndAsync()
    $stderrTask = $process.StandardError.ReadToEndAsync()
    $process.WaitForExit()
    $stdout = $stdoutTask.GetAwaiter().GetResult()
    $stderr = $stderrTask.GetAwaiter().GetResult()

    if ($plainKey) {
        $escapedKey = [regex]::Escape($plainKey)
        $stdout = [regex]::Replace($stdout, $escapedKey, '[REDACTED]')
        $stderr = [regex]::Replace($stderr, $escapedKey, '[REDACTED]')
    }
    Write-RunLogStream 'stdout' $stdout
    Write-RunLogStream 'stderr' $stderr
    $exitCode = $process.ExitCode
} catch {
    Write-RunLogLine ("[wrapper-error] {0}" -f $_.Exception.Message)
    if ($started) {
        try { $process.WaitForExit() } catch { }
    }
} finally {
    if ($startInfo) {
        [void]$startInfo.EnvironmentVariables.Remove('CODEX_404TOKEN_ADMIN_API_KEY')
    }
    if ($keyPointer -ne [IntPtr]::Zero) {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($keyPointer)
    }
    if ($secureKey) {
        $secureKey.Dispose()
    }
    $plainKey = $null
    $protectedKey = $null
    $process.Dispose()
}

Write-RunLogLine "[$(Get-Date -Format o)] finished with exit code $exitCode"
exit $exitCode
