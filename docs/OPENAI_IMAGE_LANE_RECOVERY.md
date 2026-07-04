# OpenAI Image Lane Recovery

This note documents the production pattern we now use for transient OpenAI image
lanes such as `aicodexvip` image routing behind chained Sub2API deployments.

## Problem Pattern

Some image providers flap in a way that looks permanent to the generic OpenAI
403 handler:

- `/v1/images/generations` or `/v1/images/edits` returns outward `502`
  or `503`
- the real upstream payload contains a `403 forbidden` style error body
- repeated hits can push the lane into a permanently disabled account state
  even though a manual retry succeeds a few minutes later

That behavior is too harsh for cheap-primary / expensive-fallback image routing.

## Patch Behavior

The custom patch layer changes image-only recovery in three ways:

1. OpenAI image `403` no longer escalates directly to whole-account disable.
   It sets a model-scoped cooldown on `openai:image_generation` for 10 minutes,
   which lets the scheduler fail over to the backup lane immediately.
2. Scheduled tests can now target image edits with a synthetic model selector:
   `gpt-image-2#edits`
3. When a scheduled edit probe succeeds and `auto_recover=true`, existing
   runtime cooldown / temp-unschedulable state is cleared automatically.

The edit probe is intentionally stricter than plain generations because the
problem lanes often pass text-to-image while still flaking on image-to-image.

## Recommended Rollout

1. Back up the account row and any existing scheduled test plans.
2. Keep cheap same-source lanes on conservative account concurrency.
3. Apply `temp_unschedulable_rules` on the account so raw provider flaps
   (`403` / `502` / `503`) enter a short cooldown instead of hammering the
   provider repeatedly.
4. Add a scheduled test plan with:
   - `model_id = gpt-image-2#edits`
   - `cron_expression = */5 * * * *`
   - `auto_recover = true`
5. Leave the backup lane enabled with a lower priority so requests fail over
   during the cooldown window.

## Verification

After rollout, verify all of the following:

- image failures move the primary lane into temporary cooldown, not `error`
- backup image lane is selected during the cooldown window
- scheduled edit probe succeeds once the provider recovers
- the primary lane becomes schedulable again without manual re-enable
- recent logs show both generation and edit probes returning success

## Related Artifacts

- patch inventory: `docs/CUSTOM_PATCHES.md`
- SQL replay template: `deploy/sql/openai_image_lane_recovery.example.sql`
