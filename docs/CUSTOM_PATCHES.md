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

## Upstream Update And Replay Workflow

The Veyra layer is intentionally kept as a small overlay on top of upstream
sub2api. When upstream is updated:

1. Fetch and merge upstream into `custom/main`.
2. Resolve conflicts without moving Veyra code into unrelated upstream modules.
3. Keep the editable portal source in `extensions/veyra/portal`.
4. Rebuild the embedded runtime copy with:

```powershell
./scripts/reapply-veyra-overlay.ps1
```

The script verifies the required Veyra overlay paths, syncs portal assets into
`backend/internal/veyra/portal_dist`, and runs focused Go tests for the Veyra,
config, server, and cmd/server packages.

Do not deploy directly from upstream/main. Production should deploy only from
`custom/main` after the overlay replay and tests pass.

### `custom: add Veyra portal extension`

Adds the first stage of the Veyra Extension layer. The portal assets live under
`extensions/veyra/portal`, while the Go-embedded runtime copy lives under
`backend/internal/veyra/portal_dist`.

The only core hook for this stage is a Veyra portal middleware registered before
the embedded upstream frontend middleware. It is disabled by default and only
serves:

- `/` when `veyra.enabled=true` and `veyra.portal_enabled=true`
- `/_veyra/*` portal assets

It deliberately passes through `/api/*`, `/v1/*`, `/v1beta/*`, gateway routes,
setup routes, and all other upstream routes. Keep this patch as the isolated
home-page entry point for the Veyra Agent product shell.

The portal frontend uses the existing sub2api browser login state:

- reads `localStorage.auth_token` and `localStorage.auth_user`;
- sends `Authorization: Bearer <auth_token>` to `POST /api/veyra/login-ticket`
  when launching Alchemy;
- redirects unauthenticated clicks to `/login?redirect=/_veyra/return?...`;
- serves `/_veyra/return` through the same portal bundle so login can return to
  the product shell and continue the user's original target.

The only upstream frontend hook for this login-return flow is in
`frontend/src/views/auth/LoginView.vue`: after a successful normal or 2FA login,
redirects beginning with `/_veyra/return` use `window.location.assign(...)` so
the browser reloads the server-served Veyra portal instead of staying inside the
Vue SPA router. Regular sub2api login redirects still use the original
`router.push(...)` path.

### `custom: add Veyra session and login-ticket extension`

Adds an isolated Veyra session adapter and login-ticket API under
`backend/internal/veyra`. This layer reuses sub2api's existing JWT validation
and authenticated user context instead of creating a parallel account system.

The only core hook for the API layer is a pair of route registration calls in
`backend/internal/server/router.go`. Both registrations share the same ticket
store and debit ledger. Routes are registered only when `veyra.enabled=true`.

The versioned routes are kept for sub2api-side compatibility:

- `GET /api/v1/veyra/portal/config`
  - public portal runtime config
  - returns `alchemy_base_url`, sourced from `veyra.alchemy_base_url`
- `POST /api/v1/veyra/login-ticket`
  - protected by the existing sub2api JWT middleware
  - issues a short-lived one-time ticket for an intent such as `home`,
    `sub2api`, or `alchemy`
- `POST /api/v1/veyra/internal/login-ticket/exchange`
  - protected by `X-Veyra-Internal-Token`
  - consumes the ticket exactly once and returns the sub2api user id plus intent
- `GET /api/v1/veyra/internal/users/:user_id/account`
  - protected by `X-Veyra-Internal-Token`
  - returns a minimal account projection for external products: user id, email,
    role, balance, status, and concurrency
- `POST /api/v1/veyra/internal/billing/debit`
  - protected by `X-Veyra-Internal-Token`
  - checks the user's current balance before mutating it
  - rejects insufficient balance with HTTP 402
  - requires `idempotency_key` so external retries do not double-charge

Alchemy should call the non-versioned Veyra extension aliases:

- `GET /api/veyra/portal/config`
- `POST /api/veyra/internal/login-ticket/exchange`
- `GET /api/veyra/internal/users/:user_id/account`
- `POST /api/veyra/internal/billing/debit`

This keeps the Alchemy V2 backend from depending on `/api/v1/*` or `/v1/*`
paths while preserving the sub2api-side route compatibility layer.

The extension is disabled by default. Production must set
`veyra.internal_token` before enabling Alchemy ticket exchange.
The current local verification layer uses an in-process Veyra debit ledger; for
multi-instance production deployments, replace that ledger with a DB or Redis
implementation while keeping the HTTP contract unchanged.

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

### `custom: show actual OpenAI model in Messages dispatch responses`

OpenAI `/v1/messages` compatibility dispatch is used by Codex/Claude-code style
clients that submit Anthropic model names while the real upstream is an OpenAI
Responses model. The Anthropic-format response now reports the actual mapped
OpenAI model, for example `gpt-5.4-mini`, instead of echoing the requested
Anthropic model such as `claude-3-5-haiku-20241022`.

Usage logs still retain `requested_model`, `upstream_model`, and
`model_mapping_chain`, so diagnostics can distinguish the client-facing request
from the real upstream model. Keep this patch until upstream exposes the mapped
model in OpenAI Messages dispatch responses or provides an equivalent display
policy.

### `custom: filter Gemini native model list`

Gemini native `/v1beta/models`, `/v1beta/models/{model}`, and
`/v1beta/models/{model}:generateContent|streamGenerateContent` now honor a
group's custom `models_list_config` when it is enabled. This keeps free-tier or
internal Gemini groups from advertising or accepting upstream Pro/image models
after an upstream model sync.

Keep this patch until upstream applies custom group model-list filtering to
Gemini native routes.

### `custom: classify OpenAI image rate limits as 429`

OpenAI Responses-backed image generation can return SSE error payloads with
`rate_limit_exceeded` even when the HTTP response itself is otherwise consumed
through the image compatibility layer. Those errors are now returned to clients
as `429 Too Many Requests` instead of the generic `502 Bad Gateway`, with a
best-effort `Retry-After` header when the upstream message includes a retry
delay.

Keep this patch until upstream classifies OpenAI image rate-limit payloads as
429 in the image compatibility layer.

### `custom: route OpenAI Responses image intent by image model`

Codex can submit image-generation work through `/v1/responses` using a normal
Responses text model, for example `gpt-5.4`, plus an `image_generation` tool.
Account selection now uses the effective image model, usually `gpt-image-2`, for
these image-intent requests while leaving the forwarded Responses payload
unchanged.

This lets account-level `model_mapping` act as an image capability whitelist:
accounts that only support coding/text models are skipped automatically for
image tasks, and later accounts with `gpt-image-2` support receive the request.

Keep this patch until upstream routes Responses image-generation intents through
image-model-aware account selection.

### `custom: store OpenAI Codex 5h usage as used percent`

OpenAI Codex quota response headers are normalized into account extra fields
for admin display and scheduler decisions. The canonical
`codex_5h_used_percent` field must store the upstream used percentage directly,
clamped to `0..100`; it must not invert the value as `100 - raw`.

This prevents accounts with fresh 5-hour quota, for example an upstream
`x-codex-primary-used-percent: 0` and `x-codex-primary-window-minutes: 300`,
from being misread as `100%` used and incorrectly treated as exhausted.

Keep this patch until upstream has an equivalent Codex quota normalization rule
and regression coverage for a `0%` 5-hour used snapshot.

### `custom: fast-fail OpenAI OAuth account-state errors`

OpenAI OAuth accounts can briefly keep being scheduled after ChatGPT/Codex login
state has been revoked or kicked out, because the durable account status update
may lag behind the first upstream error. Clear account-state errors now
immediately add the account to the OpenAI runtime scheduling block and return a
failover signal for the same request:

- `401 token_invalidated` / `401 token_revoked`
- `401 Unauthorized`-style OAuth failures
- `400 ... not supported when using Codex with a ChatGPT account`

The `gpt-5.4 ... not supported` Codex error is intentionally treated as an
account-state problem, not as a durable `gpt-5.4` model ban, because this
production failure mode has been observed when a ChatGPT account was kicked out
even though the account normally supports the model.

OpenAI image tool capability errors such as `Tool choice 'image_generation' not
found in 'tools' parameter` are instead recorded as a model-level cooldown for
the requested image model, so future image requests avoid that account while
text models can still use it if they remain healthy.

Keep this patch until upstream immediately failovers on deterministic OpenAI
OAuth account-state errors and separates image-tool capability cooldowns from
whole-account disablement.

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
