# Windows scheduled runner

`windows/Run-ManualOnlyRefresh.ps1` is a PowerShell 5.1 runner for a scheduled
`model-catalog-manager run --manual-only` invocation. It is platform glue; the
manager's account selection, approval gates, and API behavior remain in the Go
program.

## Why the runner uses .NET Process

Windows PowerShell 5.1 can treat native-process stderr captured through
`2>&1` as an error record. With `$ErrorActionPreference = 'Stop'`, that can
interrupt the wrapper before it records the child process's actual result.
The runner starts the executable with `System.Diagnostics.Process`, redirects
stdout and stderr separately, begins reading both streams concurrently, and
returns the child's exit code. Wrapper failures return a non-zero exit code.

## Credentials

- Save the API key as a current-user DPAPI-protected SecureString value. Do not
  place the plaintext key in a scheduled-task argument, config file, or log.
- Run the task under the same Windows user whose DPAPI protected the file, and
  restrict the file ACL to that user, SYSTEM, and local Administrators.
- The runner decrypts the value in memory and passes it to the manager only as
  `CODEX_404TOKEN_ADMIN_API_KEY` in the child process environment. The key is
  not added to the process command line. If the child echoes the exact key to
  stdout or stderr, the runner redacts it before writing the daily log.
- Keep the private config, key file, reports, logs, state, and backups outside
  the source checkout.
- Restrict write access to the manager executable and its `bin` directory to
  the scheduled-task user and trusted administrators. A process that can
  replace the executable could read the API key from its own environment.

## Invocation

Pass the manager directory, the private config path, and the protected key-file
path as ordinary filesystem paths. The config must set
`require_manual_only_mode=true`; the runner always supplies `--manual-only`.
The example config and rules in this repository are templates, not production
configuration.

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\windows\Run-ManualOnlyRefresh.ps1 `
  -ManagerRoot 'C:\path\to\manager' `
  -ConfigurationPath 'C:\private\config.json' `
  -ProtectedApiKeyPath 'C:\private\admin-api-key.dpapi'
```

Configure the scheduler to prevent overlapping instances and set a finite task
execution limit. Verify the task's exit result and the UTF-8 daily log after a
manual trigger before relying on unattended runs.
