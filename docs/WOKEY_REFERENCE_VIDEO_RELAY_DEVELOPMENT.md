# Wokey Reference-to-Video Relay Development Design

## Problem

Video OS sends `reference_images[].url` through the fixed HTTPS relay
`https://video.aiself.vip/provider-input/<token>`. Wokey rejects that relay
hostname when the URL is forwarded directly, even though the image is valid.
The gateway must fetch the image server-side and send Wokey's multipart
contract: repeated `image[]` file parts and `mode=multimodal_reference`.

## Scope

This adapter applies only to a Grok account whose base URL is the supported
Wokey endpoint and only to video creation. It covers both:

- R2V: `reference_images[]` -> ordered multipart `image[]`, mode
  `multimodal_reference`.
- I2V: `image`, `images`, or legacy `image.image_url`/`image_url` -> multipart
  `image[]`, mode `image_to_video`.

No other Grok, image, Responses, compact, or generic proxy path changes.

## Request Flow

1. Parse and normalize the public JSON shape.
2. For Wokey R2V, set/validate `mode=multimodal_reference` and normalize video
   fields (`resolution` -> `video_resolution`, default ratio).
3. Fetch each URL in input order on the server.
4. Apply the downloader's single outbound security gate.
5. Build multipart fields and ordered `image[]` parts. Do not forward the
   original URL fields.
6. POST the multipart body to Wokey `/v1/videos`.

The URL is intentionally not validated twice. The downloader is the only
component that makes the outbound request and therefore owns the final SSRF
decision. This avoids a DNS/host decision made earlier becoming inconsistent
with the request that is actually sent.

## Security Invariants

- HTTPS only for remote URLs; the Wokey R2V route continues to reject Data
  URLs to preserve its existing public contract.
- DNS resolution must not produce loopback, private, link-local, multicast, or
  unspecified addresses.
- Redirect targets are revalidated with the same rules.
- 30 second overall image preparation timeout; 10 second response-header
  timeout.
- Maximum 8 MiB per image and bounded body reads (`limit+1`).
- Only PNG, JPEG, WebP, and GIF are forwarded; MIME is detected from bytes,
  not trusted solely from the response header.
- Maximum seven R2V references and four I2V images.
- Multipart filenames are sanitized to a basename and safe ASCII characters.
- Failed downloads happen before the upstream POST, so they cannot create a
  billable Wokey task or trigger account failover.

## Compatibility Contract

The adapter preserves scalar request fields and drops only URL-bearing image
fields replaced by multipart data. The public response remains the existing
Wokey task response; status and content polling are unchanged.

## Test Requirements

- R2V with two relay-shaped HTTPS URLs produces two ordered `image[]` parts.
- I2V with `image.url` and `image.image_url` produces one `image[]` part.
- A relay-shaped host is not rejected by a duplicate preflight check when the
  injected downloader succeeds.
- Downloader rejects non-HTTPS, private IP, redirects to private IP, bad MIME,
  empty bodies, oversized bodies, and timeout/error responses.
- Conflicting R2V mode is rejected before any download.
- Download failure produces no upstream request.
