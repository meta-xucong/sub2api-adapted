# API Endpoint URL Contract

## Scope

This document defines how Sub2API presents and generates client-facing URLs. It
does not change the persisted `api_base_url` setting, gateway routes, or
provider/upstream URLs.

Baseline for this change: `custom/main` at `19308bad1`.

## Problem

`api_base_url` is stored as the site origin and is also used to build management
callbacks. The public OpenAI-compatible model gateway, however, is rooted at
`/v1`. Treating the same value as both a site origin and a client base URL makes
the API Keys page and some generated client files show or copy an incomplete
host-only URL.

## Contract

The persisted setting keeps its existing meaning:

- `api_base_url`: site origin/base used by the application and callback hints.
- Management API and OAuth callbacks: `<origin>/api/v1/...`.

Client-facing protocol bases are resolved per client rather than by appending a
suffix everywhere:

| Client/protocol | Base URL shown or generated | Example request path |
| --- | --- | --- |
| OpenAI, Codex, Grok Build | `<origin>/v1` | `/v1/responses`, `/v1/images/generations` |
| Gemini | `<origin>` or the existing client-specific `/v1beta` contract | `/v1beta/models/...` |
| Claude Code | `<origin>` | `/v1/messages` |
| Antigravity Claude | `<origin>/antigravity` | `/antigravity/v1/messages` |
| Management API/callbacks | `<origin>/api/v1` | `/api/v1/auth/...` |

## OpenAI-compatible normalization

The shared resolver follows the official Grok/CC Switch behavior of keeping one
`/v1` suffix, while being conservative with explicit paths:

- `https://example.com` → `https://example.com/v1`
- `https://example.com/` → `https://example.com/v1`
- `https://example.com/v1` → unchanged
- `https://example.com/v1/` → `https://example.com/v1`
- `https://example.com/api/v1` → unchanged
- `https://example.com/custom` → unchanged; explicit custom paths are never
  guessed or rewritten

Only the known default OpenAI-compatible endpoint is normalized automatically.
Custom endpoint entries remain exactly as configured and should include their
protocol path explicitly (for example, `https://api2.example.com/v1`).

## Implementation plan

1. Add the shared resolver to `frontend/src/utils/url.ts`.
2. Reuse it in the Grok CC Switch import path, matching the official one-suffix
   behavior.
3. Normalize the default endpoint displayed/copied on the API Keys page.
4. Use the normalized value for OpenAI/Codex/Grok Build files only; preserve
   Claude, Gemini, and Antigravity client-specific base semantics.
5. Clarify admin settings, user endpoint hints, and README documentation.
6. Add unit, component, generated-config, callback-regression, and duplicate
   suffix tests.

## Non-goals and safety boundaries

- Do not rewrite the stored `api_base_url`.
- Do not append `/v1` to arbitrary custom endpoints.
- Do not change `/api/v1` management routes or OAuth callback construction.
- Do not change provider account `base_url` values or gateway routing.
- Do not add a database migration or a new public API field in this patch.

## Acceptance criteria

- A bare configured origin is displayed and copied as an OpenAI-compatible
  endpoint ending in exactly one `/v1`.
- Existing `/v1`, `/api/v1`, and explicit custom paths are not duplicated or
  rewritten.
- OpenAI/Codex/Grok Build generated configurations use the complete `/v1` base.
- Claude, Gemini, and Antigravity generated configurations retain their current
  protocol-specific paths.
- Callback suggestions remain under `/api/v1`.
- Frontend typecheck, lint, focused tests, and production build pass.
