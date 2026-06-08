# Runtime Configs

This file records production runtime settings that live in the database or
provider consoles rather than in source code. Do not store secrets here.

## Volcengine Ark Free Lab

- Date configured: 2026-06-07
- Production group: `volcengine-ark-free-lab`
- Group id: `6`
- Platform in sub2api: `openai`
- Account type: `apikey`
- Base URL: `https://ark.cn-beijing.volces.com/api/v3`
- Downstream test API key name: `volcengine-ark-free-lab-test`
- Scheduling scope: isolated test group, not merged into the main OpenAI group
- Image generation: disabled
- OpenAI Chat Completions path: force Chat Completions compatibility
- Anthropic Messages path: enabled for Claude Code fallback through the OpenAI
  `/v1/messages` bridge; this path forwards to Ark Responses with a
  provider-specific sanitizer
- RPM limit: `10`

### Model Exposure Policy

Do not expose every model returned by Ark `/api/v3/models`. The endpoint can
list models that the current API key cannot invoke through Chat Completions.
Expose only models that are both listed upstream and verified with the current
key through the intended API path.

### Current Whitelist

These models are exposed through group `models_list_config` and account
`credentials.model_mapping` as 1:1 mappings:

- `doubao-seed-2-0-lite-260215`
- `doubao-seed-2-0-lite-260428`
- `doubao-seed-1-6-lite-251015`
- `doubao-lite-128k-240428`
- `doubao-lite-32k-240428`
- `doubao-lite-4k-240328`
- `deepseek-v3-2-251201`
- `deepseek-v4-flash-260425`
- `deepseek-v4-pro-260425`
- `glm-4-7-251222`

### Verification

On 2026-06-07, the Ark upstream model list returned HTTP 200 with 119 model
ids. The following representative calls succeeded through sub2api
`/v1/chat/completions` using the isolated downstream test key:

- `doubao-seed-2-0-lite-260215`
- `deepseek-v3-2-251201`
- `deepseek-v4-flash-260425`
- `deepseek-v4-pro-260425`
- `glm-4-7-251222`

The sub2api `/v1/models` response for the test key returned the 10 whitelisted
models after refreshing the scheduler/model-list cache for group `6`.

On 2026-06-08, Claude Code compatibility was enabled for group `6`:

- `allow_messages_dispatch=true`
- `messages_dispatch_model_config` maps the 10 whitelisted Ark model ids to
  themselves
- Claude-family fallback aliases map to Ark models:
  - Opus -> `deepseek-v4-pro-260425`
  - Sonnet -> `deepseek-v4-flash-260425`
  - Haiku -> `deepseek-v3-2-251201`

Ark Responses accepts the bridge request only after stripping OpenAI-specific
request fields that Ark rejects:

- `reasoning.summary`
- `text.verbosity`

This sanitizer is source-controlled in
`backend/internal/service/openai_volcengine_ark.go` and is scoped to OpenAI
API-key accounts with `extra.provider=volcengine_ark`.

### Models Not Exposed Yet

These model families appeared in the upstream model list but returned upstream
`404` authorization or missing-endpoint errors with the current API key during
small Chat Completions tests, so they are intentionally not exposed:

- Kimi: `kimi-k2-250905`, `kimi-k2-thinking-251104`
- Qwen: `qwen3-32b-20250429`
- Mistral: `mistral-7b-instruct-v0.2`

Re-test these before adding them to `models_list_config` or `model_mapping`.

### Pricing Caveat

The Ark/Doubao/DeepSeek/GLM models currently trigger
`openai_usage.pricing_missing_record_zero_cost` in production logs. They are
usable, but usage accounting can be zero-cost until model pricing is added or
mapped to a custom pricing rule.

Do not make this group broadly available until pricing behavior is explicitly
accepted or configured.
