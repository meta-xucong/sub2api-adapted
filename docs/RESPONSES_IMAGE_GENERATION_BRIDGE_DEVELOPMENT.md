# Responses Image Generation Bridge Development

## 1. Objective

Add a server-side compatibility bridge for clients such as Codex that send:

```text
POST /v1/responses
tools[].type = image_generation
```

when the selected upstream only accepts:

```text
POST /v1/images/generations
```

Downstream clients keep the existing Sub2API URL, API key, and request format.
Existing `/v1/images/generations` and `/v1/images/edits` callers must remain
unchanged.

This is a protocol adaptation patch. It is not a replacement for Smart Router.

## 2. Decision: placement

The bridge belongs in the OpenAI gateway adaptation layer, between request
classification and upstream forwarding.

Smart Router remains responsible for:

- selecting image lanes by configured priority, capability, cost, and health;
- applying concurrency and same-source protections;
- recording transient failures and temporary soft demotion;
- running recovery probes and the daily 04:00 Asia/Shanghai calibration.

The bridge is responsible for:

- recognizing a Responses image-generation request;
- deciding whether the selected lane is native Responses or Images-API-only;
- translating the request and response protocol;
- preserving streaming, errors, usage, and idempotency semantics.

Do not put JSON translation, SSE conversion, or image payload parsing in
`backend/internal/smartrouter/core`. The router core must stay protocol-neutral.

## 3. Compatibility contract

The bridge is activated only when all conditions are true:

1. The inbound endpoint is `/v1/responses`.
2. The request contains an `image_generation` tool or an equivalent explicit
   image-generation tool choice.
3. The selected account/lane declares `responses_image_mode: images_api`.
4. The request group permits image generation.

All other requests keep the existing path:

- ordinary Responses requests;
- Responses compact requests;
- native Responses image lanes;
- `/v1/images/generations`;
- `/v1/images/edits`;
- chat completions and other API protocols.

If the capability declaration is missing, preserve the current native Responses
behavior. Unknown capability must never silently activate the bridge.

## 4. Capability model

Each OpenAI image lane needs an explicit protocol capability independent from
image capability:

```text
image_generation       -- can generate an image
image_edit              -- can edit an input image
responses_image_native  -- accepts Responses image_generation directly
images_api_generation   -- accepts /v1/images/generations
images_api_edit         -- accepts /v1/images/edits
```

Examples:

```text
native OAuth account:
  image_generation + image_edit + responses_image_native

third-party image URL:
  image_generation + images_api_generation

specialist edit URL:
  image_edit + images_api_edit
```

The bridge must not infer protocol support only from the model name. A lane can
advertise `gpt-image-2` and still support only one of these protocols.

## 5. Request flow

```text
POST /v1/responses
        |
        v
classify explicit image_generation intent
        |
        +-- no image tool --> existing Responses path
        |
        +-- native Responses lane --> existing Responses path
        |
        +-- Images-API-only lane --> bridge
                                      |
                                      +--> Smart Router selects image lane
                                      +--> POST /v1/images/generations
                                      +--> normalize image result
                                      +--> build Responses image output
                                      +--> return to Codex
```

The bridge must reuse the existing image forwarding service or a shared service
interface. It must not make a second public API request through the same
`/v1/responses` route, which could recurse into the bridge.

## 6. Request translation

The first implementation supports only fields with a stable OpenAI Images
mapping:

```text
Responses tool/request       Images request
---------------------------  -------------------------
tool.model                   model
request input text           prompt
tool.size                    size
tool.quality                 quality
tool.output_format           output_format
tool.background              background
tool.output_compression      output_compression
tool.n                       n
```

The Responses top-level `request.model` is the text-orchestration model (for
example `gpt-5.5`) and must never override the Images API model. The bridge
uses `tool.model` when it names a supported image model, otherwise it uses
the configured image default (`gpt-image-2`). After translation, normal
account-level image model mapping and image Smart Router capability checks
remain authoritative. A channel mapping calculated for the top-level text
model must not be passed into the Images forwarding call.

The bridge must not use natural-language prompt inspection to decide whether a
request is text-to-image or image-to-image. Reference-image requests require
an explicit supported image input and must be routed to an edit-capable lane.

Unsupported fields must produce a structured, fast 4xx response or be omitted
only when the documented Images API semantics allow omission. They must not
cause an unbounded upstream wait.

## 7. Response translation

For non-streaming requests, convert a successful Images response into a
Responses response containing an `image_generation_call` output item. Preserve
the image result as the format expected by the Codex client, including the
image bytes or a supported URL according to the configured response policy.

For streaming requests, emit valid Responses SSE lifecycle events, including:

- response creation;
- image-generation call/output item events;
- completed image result;
- response completion;
- structured error events.

Do not expose upstream provider IDs, credentials, signed URLs, or raw upstream
error bodies. Large base64 payloads must obey the existing request/response
limits and output accounting rules.

## 8. Failover and accounting

The bridge is one logical request. It must use the existing Smart Router image
attempt budget and total wall-clock budget.

Rules:

- no duplicate top-level client request;
- no retry after a valid image result has been received;
- transient upstream failures are reported to the image lane health ledger;
- deterministic parameter errors and content-policy refusals are not blindly
  retried on every lane;
- client cancellation is not recorded as an upstream failure;
- idempotency metadata must be preserved so a failover does not create an
  uncontrolled duplicate charge;
- a failed translation must not be recorded as an upstream lane failure.

The bridge must preserve the distinction between:

```text
translation_error       local adapter bug or unsupported request
upstream_transient      408/429/5xx/transport failure
upstream_capability     upstream does not support the requested image shape
content_rejected        upstream policy/content refusal
client_cancelled        downstream closed the request
```

## 9. Configuration and rollout

The feature is opt-in and disabled by default:

```yaml
gateway:
  responses_image_bridge:
    enabled: false
    apply_to_protocol: images_api_only
    max_request_bytes: 16777216
    preserve_streaming: true
```

The effective enablement order is:

1. the global bridge switch must be enabled;
2. the selected account must explicitly declare `responses_image_mode: images_api`;
3. missing or unknown account metadata keeps the existing native path.

Group-level rollout can be done by applying the account metadata only to the
accounts in that group; no new group schema is required.

Recommended aiself rollout:

1. keep all existing account URLs, keys, groups, and priorities;
2. mark only known Images-API-only lanes with `responses_image_mode: images_api`;
3. enable the bridge for one test API key or test group;
4. validate ordinary chat, compact, direct Images API, Codex text, and Codex
   image generation;
5. expand to the production ChatGPT group only after the compatibility matrix
   passes;
6. keep a runtime disable switch for immediate rollback.

No downstream URL or API key change is required.

## 10. Tests and acceptance criteria

Unit tests must cover:

- exact image-tool detection;
- ordinary Responses pass-through;
- compact pass-through;
- native Responses lane bypass;
- Images-API-only lane selection;
- text-to-image request mapping;
- reference-image/edit rejection or mapping;
- unsupported parameter handling;
- non-streaming response wrapping;
- streaming SSE conversion;
- upstream 4xx/429/5xx/transport errors;
- client cancellation;
- idempotency and no duplicate top-level requests;
- large base64 response limits;
- disabled bridge behavior.

Staging acceptance must prove that these existing flows are unchanged:

```text
direct /v1/images/generations      success and same response shape
direct /v1/images/edits            success and same response shape
ordinary /v1/responses             same upstream path and behavior
/v1/responses/compact              same upstream path and behavior
Codex text                         no image translation
Codex image_generation             translated only for Images-API-only lanes
```

Every test should report only status, model, account/lane ID, timing, and
sanitized error summary. Never log API keys, cookies, credentials, full
prompts, or raw images.

## 11. Rollback

Rollback order:

1. disable the account/group bridge override;
2. if needed, disable the global bridge switch;
3. leave existing `/v1/images/*` routes and Smart Router state untouched;
4. remove the adapter code only during a later maintenance release.

The bridge must fail closed for its own translation errors and return a clear
4xx/5xx without changing account priority, `schedulable`, or existing image
lane configuration.

## 12. Relationship to official Sub2API

Official Sub2API documents and implements:

- synchronous and asynchronous `/v1/images/generations`;
- `/v1/images/edits`;
- a `codex_image_generation_bridge_enabled` option that injects or normalizes
  the Responses image tool;
- native Responses image handling for upstreams that support that protocol.

This patch adds the missing compatibility direction:

```text
Responses image_generation -> Images API
```

It should be maintained as a gateway adapter overlay, while Smart Router keeps
ownership of image lane selection and health recovery.
