# Codex Streaming Chain Tuning

This note captures the practical tuning that improved a chained Sub2API setup where:

- downstream public entry: `aiself.vip`
- upstream relay: `404token.xyz`
- active Codex chat chain: downstream account `22` -> upstream group `4` (`7646881-plus/pro/fallback`)

It is intentionally operational. The goal is to preserve the replay steps after future upstream syncs or VPS rebuilds.

## What Actually Helped

### 1. Fix the real downstream ingress path

Codex traffic in this deployment was entering the downstream gateway through `/responses`, not `/v1/responses`.

The old nginx snippet only matched:

- `/v1/messages`
- `/v1/responses`
- `/v1/chat/completions`

That meant the live Codex path fell back to the generic `location /` proxy rules and missed the dedicated streaming behavior.

Recommended dedicated location pattern:

```nginx
location ~ ^/(v1/)?(messages|responses|chat/completions)(/.*)?$ {
    limit_req zone=sub2api_gateway_api burst=120 nodelay;

    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;
    proxy_send_timeout 600s;
    proxy_read_timeout 600s;
    proxy_buffering off;
    proxy_request_buffering off;
    gzip off;
    add_header X-Accel-Buffering no always;
}
```

Also keep this in the nginx `http` block:

```nginx
underscores_in_headers on;
```

Without it, sticky/codex headers that contain underscores may be silently dropped.

### 2. Keep downstream stream-timeout policy conservative

On `aiself.vip`, the live row had been tuned too aggressively:

- `threshold_count = 1`
- `threshold_window_minutes = 5`
- `temp_unsched_minutes = 3`

For a chained upstream, that is too eager to mark the only active route temporarily unschedulable after one bad stream.

The safer operational baseline is the adapted repo default:

```json
{
  "enabled": true,
  "action": "temp_unsched",
  "temp_unsched_minutes": 5,
  "threshold_count": 3,
  "threshold_window_minutes": 10
}
```

This still cools down a truly bad route, but it avoids false positives from one long-tail stream.

### 3. Enable account-level OpenAI Responses WSv2 where the chain actually runs

Applied examples:

- downstream: accounts `22`, `23`
- upstream: accounts `4`, `5`, `6`

Account extra fields:

```json
{
  "openai_apikey_responses_websockets_v2_mode": "ctx_pool",
  "openai_apikey_responses_websockets_v2_enabled": true
}
```

`ctx_pool` is the conservative choice for API-key accounts in this topology.

### 4. Add guard rails when the upstream lanes are the same source

For the Philippines `7646881-plus / pro / fallback` group, all three lanes point at the
same upstream vendor and fail in correlated ways under Codex-style load.

The practical symptoms were:

- one lane starts returning `429`
- the other lanes soon follow with `502`
- the downstream edge finally reports `503` because every candidate was exhausted

The source-side protection that helped was intentionally simple:

- lower per-account concurrency from `10` to `2 / 2 / 1`
- add account-level `502` temp-unsched rules in `accounts.credentials`
- increase default `429` fallback cooldown from `5s` to `60s`

Recommended upstream values for this same-source trio:

```text
7646881-plus      concurrency = 2
7646881-pro       concurrency = 2
7646881-fallback  concurrency = 1
```

Recommended `credentials` additions on each upstream lane:

```json
{
  "temp_unschedulable_enabled": true,
  "temp_unschedulable_rules": [
    {
      "error_code": 502,
      "keywords": [
        "temporarily unavailable",
        "request failed",
        "openai_error",
        "concurrency limit exceeded"
      ],
      "duration_minutes": 3,
      "description": "codex upstream 502 cooldown"
    }
  ]
}
```

Recommended global setting:

```json
{
  "enabled": true,
  "cooldown_seconds": 60
}
```

This does not make a weak source strong. What it does is reduce self-inflicted pileups:

- `plus` no longer absorbs the whole burst first
- `429` stops being retried almost immediately
- repeated `502` lanes step out for a short cooldown instead of poisoning every request

## What Was Already Fine Upstream

The Philippines upstream nginx was already much closer to the desired shape:

- `underscores_in_headers on;`
- `proxy_buffering off;`
- `proxy_request_buffering off;`
- long read/send timeouts

That is why the first useful wins came from fixing the downstream edge instead of reworking the upstream proxy.

## Verification Pattern

### Downstream checks

1. Confirm the downstream usage rows for the user key:

```sql
select
  id,
  request_id,
  account_id,
  model,
  first_token_ms,
  duration_ms,
  inbound_endpoint,
  upstream_endpoint,
  created_at
from usage_logs
where api_key_id = 19
order by id desc
limit 8;
```

Expected shape after the tuning:

- `account_id = 22`
- `inbound_endpoint = /responses`
- `upstream_endpoint = /v1/responses`
- `first_token_ms` roughly low single-digit seconds
- `duration_ms` roughly around 10 to 12 seconds for light synthetic probes

### Upstream checks

```sql
select
  id,
  account_id,
  model,
  first_token_ms,
  duration_ms,
  inbound_endpoint,
  upstream_endpoint,
  created_at
from usage_logs
where api_key_id = 3
order by id desc
limit 8;
```

Expected shape:

- `account_id = 4` under normal healthy conditions
- `5` and `6` stay as failover lanes, not round-robin primaries

## Important Residual Risk

This run also reproduced an important long-tail behavior:

- the gateway may produce a fast first byte and even finish server-side in about 10 seconds;
- but a client can still spend 60 to 120 seconds in "thinking" if the upstream stream only emits:
  - `response.created`
  - `response.output_item.added` with `type=reasoning`
  - encrypted reasoning content
  - no early `response.output_text.delta`

That is not the same problem as nginx buffering.

In other words:

- proxy tuning improves transport stability;
- timeout tuning prevents bad self-disable decisions;
- but neither can force the upstream model to emit visible text earlier when it chooses long hidden reasoning first.

When diagnosing Codex "stuck thinking", always separate:

1. server-side first byte / first token
2. client-visible first `response.output_text.delta`

They are not the same metric.

## Replay Assets

Use these companion files when replaying the tuning:

- `deploy/nginx/codex-streaming-location.conf.example`
- `deploy/sql/openai_responses_codex_tuning.example.sql`

Apply them only after taking VPS-local file backups and SQL row snapshots.
