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

### `custom: prefer latest OpenAI Codex models`

Routes default OpenAI Messages Dispatch models away from retired/unsupported
Codex targets and maps legacy Codex aliases to current supported targets:

- Opus default: `gpt-5.5`
- Sonnet default: `gpt-5.4`
- Haiku default: `gpt-5.4-mini`
- Legacy Codex aliases: `gpt-5.4` or `gpt-5.4-mini`

Keep this patch until upstream removes unsupported `gpt-5.3` defaults and legacy
Codex alias routing.

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
