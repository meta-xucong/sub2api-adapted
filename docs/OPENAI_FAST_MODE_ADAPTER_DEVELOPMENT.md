# OpenAI Fast Mode Adapter

## Goal

Codex can request Fast mode through OpenAI-compatible metadata, but most third-party upstream URLs do not accept `service_tier=fast` or the legacy alias `service_tier=priority` directly. Passing that field through can produce upstream 403 errors such as `service_tier priority only allowed for approved OpenAI models`.

This adapter makes Fast mode a gateway-local routing intent:

- `/v1/models` advertises Fast capability for OpenAI chat models so Codex can expose the mode.
- Inbound `service_tier=fast` and `service_tier=priority` are normalized to the same intent.
- Existing OpenAI Fast Policy still decides whether the field is passed, filtered, blocked, or forced for the selected account.
- Smart Router uses the preserved intent to prefer lower-latency, healthier, less loaded chat/responses lanes.
- Image, embedding, and compact routes stay isolated; the fast hint does not change their scoring.

## Official API Semantics

OpenAI's Priority Processing guide defines the request field as `service_tier: "fast"`; the older `priority` name remains a compatible alias for supported models. That is an upstream API feature, not a guarantee that arbitrary OpenAI-compatible providers accept the same field.

## Design

The request body remains the source of truth for client intent. Before account selection, handlers call `OpenAIFastIntentFromBody`; if it sees a known fast tier it stores `OpenAIFastIntent` in context.

The account scheduler copies this into `OpenAIAccountScheduleRequest.SmartRouterPreferLowLatency`, then into `smartrouter.RouteRequest.PreferLowLatency`.

The core router applies the hint only to `chat` and `responses` capabilities:

- lower cost weight
- higher latency, queue, load, health, and recovery weights
- unchanged model/capability/priority-layer filtering

The OpenAI Fast Policy continues to protect third-party URLs from unsupported upstream fields. For third-party API-key accounts the default remains `filter` for `priority`, so the upstream request does not fail just because Codex selected Fast.

## Compatibility

Existing callers that do not send `service_tier` are unchanged. Existing callers that directly use `/v1/images/generations`, `/v1/images/edits`, `/v1/responses/compact`, or embeddings are unchanged. Model list clients that ignore unknown fields continue to work because the new fields are additive.

## Deployment Notes

This is part of the Smart Router patch family and should be preserved when updating from upstream Sub2API. A hot update requires replacing the backend binary or equivalent container artifact and restarting the app process/container; no database migration is required.
