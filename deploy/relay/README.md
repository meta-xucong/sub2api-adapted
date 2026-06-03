# JP Relay Deployment Assets

This folder stores the host-level relay topology used by the production Sub2API deployment.

Why this exists:

- The application-level account-to-proxy mapping lives in the database and usually survives normal app upgrades.
- The host-level relay runtime does **not** live in the application database:
  - SSH dynamic forward tunnels
  - HAProxy TCP frontends/backends
  - systemd units that keep tunnels alive
- If the VPS is reprovisioned or the host-level networking layer is rebuilt, these files are the source of truth needed to restore the relay topology.

## Topology

Host-side SOCKS tunnels:

```text
127.0.0.1:20081 -> JP relay 1
127.0.0.1:20082 -> JP relay 2
127.0.0.1:20083 -> JP relay 3
```

Container-reachable HAProxy frontends:

```text
172.18.0.1:21080 -> generic failover (JP1 primary, JP2 backup, JP3 backup)
172.18.0.1:21081 -> OpenAI account lane 1 (JP1 primary, JP2 backup, JP3 backup)
172.18.0.1:21082 -> OpenAI account lane 2 (JP2 primary, JP3 backup, JP1 backup)
172.18.0.1:21083 -> OpenAI account lane 3 (JP3 primary, JP1 backup, JP2 backup)
```

Recommended account binding pattern:

```text
OpenAI account A -> proxy lane 21081
OpenAI account B -> proxy lane 21082
OpenAI account C -> proxy lane 21083
```

This preserves a stable primary egress IP per account while keeping automatic failover.

## Files

- `haproxy-jp-relays.cfg`
  - HAProxy config for container-reachable failover frontends.
- `jp-relay-1-tunnel.service`
- `jp-relay-2-tunnel.service`
- `jp-relay-3-tunnel.service`
  - systemd services that keep the host-side SSH dynamic forwarders alive.

## Restore Checklist

1. Copy the systemd unit files into `/etc/systemd/system/`.
2. Ensure the private keys exist on the host under `/root/.ssh/`.
3. `systemctl daemon-reload`
4. `systemctl enable --now jp-relay-1-tunnel.service jp-relay-2-tunnel.service jp-relay-3-tunnel.service`
5. Copy `haproxy-jp-relays.cfg` into `/etc/haproxy/haproxy.cfg`
6. `haproxy -c -f /etc/haproxy/haproxy.cfg`
7. `systemctl restart haproxy`
8. Verify:
   - `curl --socks5-hostname 172.18.0.1:21081 https://api.ipify.org`
   - `curl --socks5-hostname 172.18.0.1:21082 https://api.ipify.org`
   - `curl --socks5-hostname 172.18.0.1:21083 https://api.ipify.org`

## Important Boundary

These files restore the host-side relay runtime only.
The application still needs account-to-proxy bindings in the database.

Those bindings are typically preserved during app updates because they live in Postgres, but for a fresh restore you should also reapply the intended account/proxy assignments through the admin UI or SQL migration/ops steps.
