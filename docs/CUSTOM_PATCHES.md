# Custom Patch Layer

This repository tracks the upstream project plus production patches that must
survive upstream updates.

## Remotes

- `upstream`: `https://github.com/Wei-Shaw/sub2api.git`
- `origin`: private adapted repository

## Branches

- `upstream/main`: official upstream code
- `custom/main`: official upstream code plus local production patches

## Current Custom Patches

### `custom: cap Kimi gateway max tokens`

For Kimi gateway requests, force `max_tokens` to `1600` on both the standard
gateway path and the Anthropic API key passthrough path.

Keep this patch until upstream provides an equivalent Kimi-specific hard limit
or the Kimi upstream no longer needs the cap.

### `custom: detach Kimi passthrough upstream timeout`

Anthropic API-key passthrough requests, including the production Kimi account,
use a detached upstream context for non-streaming `/v1/messages` and
`/v1/messages/count_tokens` calls. This prevents short client-side cancellations
from immediately cancelling the Kimi upstream request.

The non-streaming upstream budget is configurable through
`GATEWAY_ANTHROPIC_APIKEY_UPSTREAM_TIMEOUT_SECONDS` and defaults to `60`.
Streaming requests continue to use the existing stream idle-timeout logic.

Production Kimi accounts must also have `accounts.extra.anthropic_passthrough`
set to `true`; otherwise they use the normal Anthropic API-key forwarding path
and do not receive this detached upstream timeout behavior.

Keep this patch until upstream provides an equivalent Anthropic API-key
passthrough upstream timeout/cancellation policy.

### `custom: add Kimi fallback billing`

Adds fallback token pricing for Kimi/Moonshot models when LiteLLM pricing does
not contain the production model alias. The production `kimi-for-coding` alias is
charged with the current Kimi K2.6 default rate:

- input/cache miss: `$0.95 / 1M tokens`
- cache hit: `$0.16 / 1M tokens`
- output: `$4.00 / 1M tokens`

Moonshot V1 aliases fall back to `$2.00 / 1M input tokens` and `$5.00 / 1M
output tokens`.

Keep this patch until upstream includes equivalent Kimi/Moonshot pricing aliases
or production switches to channel-level custom pricing for these models.

### `custom: add ops alert request thresholds`

Adds `min_request_count` and `min_error_count` filters to ops alert evaluation.
This prevents low-sample false positives in production status alerts.

Keep this patch until upstream supports equivalent alert threshold filters.

### `custom: resolve skipped ops alert events`

When an alert rule is skipped because request/error sample thresholds are no
longer met, resolve any existing active event for that rule. Without this,
low-traffic recovery windows can leave old `firing` events stuck even though the
current success/error rate is healthy.

Keep this patch together with the request-threshold alert filters.

### `custom: prefer latest OpenAI Codex models`

Routes default OpenAI Messages Dispatch models away from retired/unsupported
Codex targets and maps legacy Codex aliases to current supported targets:

- Opus default: `gpt-5.5`
- Sonnet default: `gpt-5.4`
- Haiku default: `gpt-5.4-mini`
- Legacy Codex aliases: `gpt-5.4` or `gpt-5.4-mini`

Keep this patch until upstream removes unsupported `gpt-5.3` defaults and legacy
Codex alias routing.

### `custom: filter Gemini native model list`

Gemini native `/v1beta/models`, `/v1beta/models/{model}`, and
`/v1beta/models/{model}:generateContent|streamGenerateContent` now honor a
group's custom `models_list_config` when it is enabled. This keeps free-tier or
internal Gemini groups from advertising or accepting upstream Pro/image models
after an upstream model sync.

Keep this patch until upstream applies custom group model-list filtering to
Gemini native routes.

## Update Workflow

Run:

```powershell
.\scripts\update-upstream.ps1
```

The script fetches upstream, rebases `custom/main` onto `upstream/main`, and runs
available checks. If Git reports conflicts, resolve only the custom patch logic
that still applies, then run:

```powershell
git add <resolved-files>
git rebase --continue
```

After verification, push:

```powershell
git push origin custom/main
```

Never deploy raw `upstream/main` to production.
