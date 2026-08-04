# Smart Router Exact-Model Recovery

Smart Router applies one temporary recovery rule to every model and capability.
The health key is:

```text
lane_id + capability + exact requested model
```

Examples:

```text
account:5 + responses + gpt-5.6-luna
account:5 + responses + gpt-5.6-terra
account:5 + image_generation + gpt-image-2
account:5 + embedding + text-embedding-3-large
```

A failure in one key does not suppress another model on the same account.
This is especially important for providers that temporarily have no channel
for one model tier while other tiers remain available.

## Failure behavior

- First transient upstream failure: short cooldown and temporary soft penalty.
- Repeated failures: bounded longer cooldown and further soft demotion.
- Sustained failures: cooldown until the next 04:00 Asia/Shanghai calibration.
- Calibration success: clears only that exact model/capability state and restores
  the configured account priority.
- Manual disable, deleted accounts, and credentials are not automatically
  reopened by calibration.
- Client cancellation, content rejection, and request-specific payload errors
  do not penalize the upstream model lane.

The temporary penalty is runtime health state. It never overwrites account
priority, group priority, model pricing, model mappings, credentials, or the
user-selected model.

## Daily recovery probes

Healthy lanes may use a representative daily probe. A lane/model that is in
cooldown or has a recovery priority receives a probe for its exact recorded
model before it can return to its normal priority. Therefore a failed
`gpt-5.6-luna` lane is tested with `gpt-5.6-luna`, not with `gpt-5.5` or
`gpt-5.6-sol`.

This keeps the policy generic for chat, Responses, compact, image generation,
image edit, and embedding lanes without requiring provider-specific model
rules. Embeddings use a real `/v1/embeddings` probe; they are not mistaken for
an image or chat probe.

## Legacy state compatibility

Older versions stored broad keys such as `gpt-5` or `gpt-image` in the same
database column. Those rows remain readable for audit history, but are not
used to create a recovery request or aggregate daily capability evidence,
because neither value is an exact user model. New events and recovery rows use
only the exact requested model.

## Compatibility

The change is in-memory and ledger-compatible. The existing database column
`model_family` stores the exact model key for backward schema compatibility;
no credential or account table migration is required. Existing broader health
rows naturally stop affecting a different exact model after the next normal
state refresh, while new events and recovery probes use exact keys.
