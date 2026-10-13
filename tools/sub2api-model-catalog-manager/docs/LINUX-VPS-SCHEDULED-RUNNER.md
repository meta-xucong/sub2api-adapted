# Linux VPS scheduled runner

The manager runs as a separate systemd service and remains an external Sub2API Admin API client. It does not share Sub2API's database, Docker socket, or application files.

## Scope and permissions

The approved workflow calls the native Admin API to run `sync-upstream` and update upstream model metadata, then applies only the configured, approved `model_mapping` rules. These API writes are explicitly within the manager's purpose. The manager does not write to the database directly and does not change users, providers, groups, API keys, prices, multipliers, or routing configuration. Do not grant its service user database, Docker, or Sub2API host-file access.

The current all-account configuration explicitly enables `accept_all_credential_response_memory_exposure`. Account credential responses can therefore briefly exist in manager process memory during inspection. Continue extracting only approved fields; never put credentials in reports or logs. Do not enable this setting for a new environment without owner approval.

## Files and account

The supplied unit files expect:

- manager binary at `/opt/sub2api-model-catalog-manager/bin/model-catalog-manager`;
- runtime config and reviewed rules in `/etc/sub2api-model-catalog-manager/`;
- private state, reports, mapping backups, and official lifecycle cache in `/var/lib/sub2api-model-catalog-manager/`;
- a dedicated `sub2api-model-catalog-manager` system user and group;
- an encrypted credential at `/etc/credstore.encrypted/sub2api-model-catalog-manager-admin-api-key`.

The runtime config must use `credential.systemd_credential_name: "admin-api-key"`, Linux paths, the explicitly approved `account_scope: "all"`, the manual-only gate, and an `approved_rules_sha256` that matches the reviewed rules file.

## Credential handling

Preserve an existing `/var/lib/systemd/credential.secret`; run `systemd-creds setup` only if it is absent. Before installing files, test host-key encrypt/decrypt with a dummy value that contains no real credential.

The Admin API key is read from the current Windows user's DPAPI file and decrypted in memory. The deployment process builds the JSON credential object and Base64 representation in memory, substitutes them into a script string, then calls `connect.ps1 -RemoteScript $remoteScript` so SSH sends it through stdin. The current wrapper path uses `ProcessStartInfo`; it does not persist the rendered script. Do not switch to `-RemoteScriptPath`, which stages a script file. On the VPS the JSON Base64 is piped directly to `systemd-creds encrypt --with-key=host --name=admin-api-key`; plaintext is not written to a persistent file or passed as a command argument or environment value. Do not print the rendered script or put any credential in logs. The SSH wrapper separately creates short-lived, ACL-restricted SSH private-key files; those are not the Sub2API Admin API key.

If the SSH wrapper implementation changes, re-audit its data flow before transmitting credentials. Use SSH host-key `accept-new` with a dedicated `known_hosts` file for the previously owner-approved TOFU first connection, then reject changed fingerprints.

`LoadCredentialEncrypted=` injects the encrypted credential into a private runtime credential directory. The manager reads it through `CREDENTIALS_DIRECTORY` and validates the JSON object.

## Install and verify

1. Build the manager for the VPS architecture (`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ...`) and verify the binary checksum.
2. Preflight systemd/systemd-creds support, exact destination paths, the service user, unit files, credential destination, and timer calendar. Refuse all pre-existing manager destinations rather than overwrite them. Validate the exact installed unit files with `systemd-analyze verify` inside the installation transaction; on failure, roll back only exact paths created by that installation. Preserve the system host credential key even if the sidecar install rolls back.
3. Install the binary, config, rules, and existing ownership state into the paths above. Keep reports and mapping backups private; do not copy unrelated logs or historical data. Leave the service stopped and timer disabled.
4. Verify `systemd-analyze verify` and `systemd-analyze calendar '*-*-* 04:10:00 Asia/Shanghai'`.
5. Run the service manually once after the native 04:00 refresh gate is satisfied. Inspect exit status, private report counts, exact approved mapping changes, and conflicts. If state is unchanged, expect `applied=0, no_op=18`; stop on any unexplained mapping change. The manual-only workflow may update upstream metadata and approved mappings; it does not generate content.
6. Compare Sub2API container IDs, images, health and compose configuration before and after; they must remain unchanged.
7. Cut over without overlapping schedulers: disable Windows task `\Sub2API-Upstream-Model-Refresh` and verify `Disabled`; only then enable/start the VPS timer and verify its next 04:10 Asia/Shanghai firing. If VPS timer activation fails, disable and verify the VPS timer before restoring the Windows task.

The timer uses `Persistent=true`; it can catch up after downtime but cannot bypass the manager's manual-only refresh gate or approved-rule hash check. A manual oneshot does not prove that a future scheduled firing worked. Record the first natural timer execution separately.

## Rollback

Run `systemctl disable --now sub2api-model-catalog-manager.timer` and verify it is disabled before re-enabling the Windows task. Keep the manager's private state and mapping backups for review. Do not delete `/var/lib/systemd/credential.secret`, Sub2API data, or alter the Sub2API service during sidecar rollback.
