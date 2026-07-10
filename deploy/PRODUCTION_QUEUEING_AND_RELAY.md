# Production Queueing And Relay Notes

This note captures production hardening patterns that are easy to lose during upgrades:

1. Group permission changes must invalidate API-key authentication snapshots.
2. Single-account or low-account deployments should prefer waiting/queueing over fast 429 failures.
3. Multi-relay outbound proxy setups should expose container-reachable failover endpoints instead of binding accounts directly to host-only SOCKS ports.

## 1. Image Permission Cache Invalidation

Current upstream Sub2API invalidates API-key auth snapshots by group when group or channel settings change. Keep that upstream behavior intact when resolving future upgrade conflicts.

Relevant upstream paths include:

- `backend/internal/service/api_key_service.go`
- `backend/internal/service/admin_group.go`
- `backend/internal/service/group_service.go`
- `backend/internal/service/channel_service.go`

The former adapted request-time `GetByKeyFresh` fallback is intentionally retired. It added a database read on stale/denied requests, covered only selected handlers, and duplicated the upstream invalidation contract. If a permission flip appears stale, diagnose the invalidation path instead of restoring that fallback.

## 2. Prefer Queueing Before Returning 429

When only one OpenAI account is usable, a low account concurrency limit can produce avoidable internal
`429 Concurrency limit exceeded` errors. In that situation, longer account-level wait queues are often
better than immediate failure.

Recommended production overrides:

```env
OPENAI_ADVANCED_SCHEDULER_ENABLED=true
GATEWAY_SCHEDULING_STICKY_SESSION_MAX_WAITING=20
GATEWAY_SCHEDULING_STICKY_SESSION_WAIT_TIMEOUT=300s
GATEWAY_SCHEDULING_FALLBACK_WAIT_TIMEOUT=300s
GATEWAY_SCHEDULING_FALLBACK_MAX_WAITING=300
```

What these do:

- `OPENAI_ADVANCED_SCHEDULER_ENABLED=true`
  - Enables the OpenAI scheduler path so overloaded accounts are more likely to return a wait plan instead of failing fast.
- `GATEWAY_SCHEDULING_STICKY_SESSION_MAX_WAITING`
  - Maximum queue depth for requests that should stay on the same sticky account.
- `GATEWAY_SCHEDULING_STICKY_SESSION_WAIT_TIMEOUT`
  - How long a sticky request may wait for an account slot.
- `GATEWAY_SCHEDULING_FALLBACK_WAIT_TIMEOUT`
  - How long non-sticky fallback selection may wait.
- `GATEWAY_SCHEDULING_FALLBACK_MAX_WAITING`
  - Maximum fallback queue depth.

Notes:

- These settings do not change account concurrency by themselves.
- They only make the gateway more willing to wait for a free slot before returning `429`.
- `OpenAI` and `Anthropic/Kimi` account pools are isolated; tune them independently.

## 3. Container-Reachable Multi-Relay Failover

Avoid binding accounts directly to host-only SOCKS listeners such as `127.0.0.1:20081` when the gateway
runs in Docker. A container cannot reliably consume those listeners through a different host-side address
unless they are explicitly re-exposed.

Recommended pattern:

1. Keep per-relay SSH dynamic forwarders on the host:

```text
127.0.0.1:20081 -> JP relay 1
127.0.0.1:20082 -> JP relay 2
127.0.0.1:20083 -> JP relay 3
```

2. Re-expose container-reachable HAProxy frontends:

```text
172.18.0.1:21081 -> primary JP1, backup JP2, backup JP3
172.18.0.1:21082 -> primary JP2, backup JP3, backup JP1
172.18.0.1:21083 -> primary JP3, backup JP1, backup JP2
```

3. Bind accounts one-to-one to the HAProxy frontends, not to the raw SSH SOCKS listeners.

This gives:

- Stable primary egress IP per account
- Automatic failover when a relay goes down
- No need to rotate the same OAuth account across multiple IPs during normal operation

## 4. Recommended Operational Shape

For a small OpenAI account pool:

- Keep per-account concurrency conservative, for example `2` to `3`
- Enable waiting/queueing before failure
- Assign each account a dedicated primary relay with automatic backups
- Avoid random per-request IP rotation for the same OAuth account

For image generation:

- Keep the dedicated image concurrency queue enabled if image requests are heavy
- Revisit image-specific queue limits separately from text-account concurrency
