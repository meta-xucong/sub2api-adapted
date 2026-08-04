# GPT-5.6 Model Compatibility Overlay

This overlay keeps GPT-5.6 model selection safe across OpenAI-compatible
providers whose aliases are not identical. It is intended for any Sub2API
deployment and does not depend on group names, account ids, URLs, or provider
credentials.

## Model contract

The public model names have different meanings and must not be collapsed into
one broad `gpt-5.6` bucket:

| Requested model | Canonical target |
| --- | --- |
| `gpt-5.6` | `gpt-5.6-sol` |
| `gpt-5.6-sol` | `gpt-5.6-sol` |
| `gpt-5.6-terra` | `gpt-5.6-terra` |
| `gpt-5.6-luna` | `gpt-5.6-luna` |

The bare alias is a compatibility alias for the Sol tier. It is not a reason
to rewrite an explicit Terra or Luna request. A provider may expose Sol and
Terra while returning `model_not_found` for Luna, or the reverse; that is an
upstream capability difference, not evidence that all GPT-5.6 models should be
remapped together.

## Runtime behavior

The model normalizer in `backend/internal/service/openai_model_alias.go`:

1. removes an optional provider namespace such as `openai/`;
2. normalizes spelling variants and known Codex suffixes;
3. maps only the bare GPT-5.6 alias to `gpt-5.6-sol`;
4. preserves explicit `gpt-5.6-sol`, `gpt-5.6-terra`, and `gpt-5.6-luna`.

The same exact requested model is used for Smart Router health and compact
health keys. A failure on Terra must not quarantine Luna, Sol, or ordinary
GPT-5.5 traffic.

## Account mapping policy

For an individual account, add the bare alias only when the account already
has a verified explicit Sol mapping:

```json
{
  "gpt-5.6": "gpt-5.6-sol",
  "gpt-5.6-sol": "gpt-5.6-sol",
  "gpt-5.6-terra": "gpt-5.6-terra",
  "gpt-5.6-luna": "gpt-5.6-luna"
}
```

The last two entries are optional and must reflect that account's own upstream
tests. Never copy a mapping from another VPS merely because the account name
looks similar. Preserve the administrator's group membership, billing tier,
priority, concurrency, schedulable state, credentials, and image-only lanes.

Do not overwrite an existing `gpt-5.6` mapping automatically. An existing
manual mapping is operator intent and must be reviewed separately. The
replayable SQL template in `deploy/sql/gpt56_chat_model_compatibility.example.sql`
only fills an absent bare alias for accounts that already advertise explicit
Sol support.

## Capability and health are separate

The following outcomes must not be merged:

- `model_not_found`: model/account compatibility evidence for that exact model;
- `401` or invalid token: authentication or expired credential;
- balance/quota errors: account funding or provider policy;
- `4xx` parameter errors: request or upstream adapter compatibility;
- `5xx`, timeout, EOF, and interrupted SSE: transient lane health.

Smart Router can demote or cool a failing exact lane, but it must not rewrite
the user's requested model or permanently disable the whole account because one
GPT-5.6 variant failed.

## Safe deployment sequence

1. Back up `accounts.credentials` for the affected accounts.
2. Apply only model-mapping changes inside a transaction.
3. Insert `scheduler_outbox.account_changed` for changed accounts and a
   `full_rebuild` event when the deployment uses a stale scheduler snapshot.
4. Confirm `/v1/models` and the account test selector expose only the models
   configured by the group and account mapping.
5. Test `gpt-5.6`, `gpt-5.6-sol`, `gpt-5.6-terra`, and `gpt-5.6-luna` through the
   deployment's dedicated operator test key. Record only HTTP status, selected
   model, account id, and a short sanitized error summary.

The update is database-hot and does not require rebuilding or replacing the
container image. Keep the previous mapping backup until all four probes and a
normal Responses request succeed.

## What this overlay does not do

This overlay does not:

- enable GPT-5.6 in a billing group;
- move an account between groups;
- copy credentials or health state between deployments;
- force Luna or Terra onto providers that have not passed their own tests;
- add GPT-5.6 to image-only accounts;
- change ordinary priority or concurrency settings.

The operator remains responsible for deciding which billing groups advertise
the explicit models. The router then preserves that policy while adapting the
bare compatibility alias safely.
