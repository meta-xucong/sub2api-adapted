# Sub2API Model Catalog Manager

This standalone Go CLI audits account model catalogs, compares them with locally maintained rules, and produces reviewable plans. It runs outside the Sub2API server and does not modify Sub2API source code.

## Safety and target

- This build is pinned to the HTTPS origin `https://404token.xyz`; it refuses other destinations.
- The admin API key is read from the operating system credential store (or the documented environment-variable fallback). Do not put keys in config files, command arguments, reports, or Git.
- Account inspection can receive credential fields in the API response. The manager projects only approved account fields into its reports, but the response is present in process memory while being handled. Use only on a trusted machine.
- Reports contain account IDs, upstream URLs, model names, and mappings. Keep runtime data local and private.
- `apply` and `run` can change account model mappings when their explicit preflight, compatibility, approval, and rule-hash gates pass. Start with `inspect`, `discover`, and `merge-review`; review their reports before considering any write command.
- The example config is deliberately an unusable, fail-closed template: replace the placeholder account ID, explicitly review the memory-exposure setting, and supply reviewed runtime compatibility evidence before using write-capable commands.
- Official lifecycle sources are fetched anonymously. They are evidence for human review, not proof that an upstream proxy currently serves or rejects a model. `--offline-official-lifecycle` uses the local cache and does not disable the manager's admin API request.

## Build and verify

Requires Go 1.23 or newer.

```powershell
go test ./... -count=1
go vet ./...
go build -o .test-temp\model-catalog-manager.exe ./cmd/model-catalog-manager
```

## First read-only run

1. Copy `config.example.json` to `config.private.json` and `model-rules.example.json` to `model-rules.json` in a private local directory.
2. Enter the real account ID in the config and review each safety field. Keep `apply_mode` as `preview`, leave native metadata sync disabled, and do not set an approved rules hash until the rules have been independently reviewed.
3. Store the admin API key in Windows Credential Manager under the configured target. Never commit a real config or rules file.
4. Run `inspect`, then `discover`, then `merge-review`. Treat the reports as private operational data.

Example:

```powershell
go run ./cmd/model-catalog-manager inspect --config .\config.private.json
go run ./cmd/model-catalog-manager discover --config .\config.private.json
go run ./cmd/model-catalog-manager merge-review --config .\config.private.json --inspect <inspect-report.json> --discover <discover-report.json> --out-dir .\review-private
```

The executable's `--help` output is authoritative for current command flags. There is no model-vendor API key or OEM key requirement.

## Runtime files

Keep real configs, model rules, state, reports, backups, logs, local binaries, and temporary test output outside version control. `.gitignore` blocks the standard runtime names; review `git status` before committing.
