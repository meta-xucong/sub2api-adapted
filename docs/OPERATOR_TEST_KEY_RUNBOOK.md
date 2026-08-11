# Operator Test Key Runbook

Production smoke tests, scheduled health checks, and SSH maintenance must not
use a customer API key. A successful request can create usage records and
customer-visible audit entries even when no balance is debited.

## Required Setup

Create dedicated API keys under an administrator-owned user. Use separate keys
when the test needs a distinct routing lane, for example:

- chat and Responses;
- Compact;
- image generation or image editing;
- video;
- optional Fast tier.

Bind every test key to the smallest appropriate group. Keys and groups must be
named as operational resources, never after a customer. Keep keys out of source
control, scripts, CI output, shell history, and support messages.

Enable `gateway.operator_test_guard` on production hosts once the dedicated
administrator keys exist. The guard rejects trusted-local operator requests
that use a non-administrator or unrecognized test key without changing public
client traffic.

## Before Every Smoke Test

1. Verify the test key is active and owned by the administrator.
2. Verify its assigned group is active.
3. Verify that group has at least one active account advertising the exact test
   model and endpoint capability.
4. Verify the target account has not been manually `force_off` for Compact or
   intentionally disabled for the requested image/video capability.

Do not treat an immediate gateway `503` as a release regression before these
four checks. It can simply mean the test key targets a group whose accounts are
all disabled, exhausted, or model-incompatible.

## Assertions

Use protocol assertions, not status code alone:

- Responses SSE: HTTP success and terminal `response.completed`.
- Compact: a non-empty compaction output item or encrypted content.
- Image: at least one returned image asset in the requested response shape.
- Video: task creation plus a completed asset/status check.
- Fast: a completed Responses result using the explicit Fast/priority request
  mode when that capability is enabled.

Record only the test key id or an internal alias, endpoint, model, duration,
HTTP status, terminal-protocol result, and sanitized upstream error category.
Never record prompts, raw stream bodies, image inputs, generated assets, API
keys, cookies, or provider credentials in a general-purpose repository.

## Failure Handling

- A deterministic model or parameter error should be corrected in the account
  mapping or test request before retrying.
- A 429, timeout, 5xx, or stream interruption should be classified as lane
  health evidence and allowed to use the Smart Router recovery policy.
- Compare a controlled request against the rollback release only when deciding
  whether an upgrade caused a regression. Equal failures indicate upstream or
  configuration health, not the new image.
- Keep the administrator key's group binding stable during normal monitoring.
  For an isolated alternate-lane test, use a separate temporary key and remove
  or disable it after the investigation.
