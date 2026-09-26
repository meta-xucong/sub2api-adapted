# OpenAI Images `upstream_text_reply` failover

## Purpose

Some OpenAI-compatible image lanes return HTTP 400 with the explicit error code
`upstream_text_reply` instead of image data. This identifies an incompatibility
in the selected lane, not a malformed client prompt. The normal gateway
classifier correctly treats most HTTP 400 responses as client errors, so this
image-only case needs a narrow exception to try another account.

## Behavior

- Only image generation/edit forwarding uses the exception; generic Responses,
  chat, embeddings, and other gateway routes retain their existing classifier.
- The special case requires HTTP 400 and the exact marker in `error.code`,
  `response.error.code`, or top-level `code` (case-insensitive, trimmed).
- Valid JSON is not scanned as free text, preventing prompt/message echoes from
  triggering failover. Plain-text upstream errors retain a case-insensitive
  marker fallback.
- The marker exception switches accounts but disables same-account pool retry
  for both API-key and OAuth image paths.
- Ordinary 400s, 404s, `image_poll_timeout`, and non-marker errors keep existing
  behavior. A marker on HTTP 5xx follows the generic 5xx policy.

## Implementation surface

- `backend/internal/service/openai_gateway_upstream_errors.go`
- `backend/internal/service/openai_image_generation_transient_cooldown.go`
- `backend/internal/service/openai_images.go`
- `backend/internal/service/openai_images_responses.go`
- Image classifier, account retry, and handler failover tests.

The shared marker predicate is also used by image transient-cooldown detection,
so cooldown and account switching agree on what constitutes this failure.

## Verification

The candidate was based on the currently deployed source revision
`95122c08037053f3947424004272ebc992b1a3d5` and was verified with:

- `go build ./...`
- `go test -count=1 -tags unit ./internal/handler`
- `go test -count=1 -tags unit ./internal/service -run TestOpenAIGatewayServiceForwardImages`
- `go test -count=1 -tags unit ./internal/service -run TestShouldFailoverOpenAIImagesResponse`
- `gofmt -d` on changed Go files and `git diff --check`

The full `internal/service` unit suite currently has unrelated Kimi/DeepSeek
adaptive-protocol tests that panic on this target branch; those tests do not
exercise the image forwarding paths changed here. The image-specific service
tests, full handler unit suite, and production build pass.

No live/paid image generation was performed from this candidate. For rollback,
revert only the image failover commit; do not reset or clean the deployment
directory or its persistent data.
