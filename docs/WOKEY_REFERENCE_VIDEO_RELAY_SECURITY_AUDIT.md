# Wokey Reference Video Relay Security Audit

## Trust Boundary

The client controls image URLs. Sub2API becomes an outbound HTTP client and
must treat every URL as untrusted, including the known relay hostname.

## Required Controls

| Control | Owner | Status |
| --- | --- | --- |
| HTTPS scheme and valid host | downloader | required |
| DNS/IP SSRF blocking | downloader + HTTP transport | required |
| Redirect revalidation | downloader | required |
| Response-header timeout | HTTP client | 10 seconds |
| Total fetch timeout | request preparation context | 30 seconds |
| Per-image byte limit | downloader | 8 MiB |
| MIME sniffing | downloader | PNG/JPEG/WebP/GIF |
| R2V count limit | public validation + builder | 7 |
| I2V count limit | builder | 4 |
| No URL forwarding to Wokey | multipart builder | required |
| No upstream call after local fetch failure | service flow | required |

## Duplicate Validation Decision

The old flow validated R2V URLs in `normalizeWokeyVideoForwardBody` and then
validated them again in `downloadWokeyVideoReferenceImage`. The first check was
not the actual network gate and could reject a public relay when the service's
DNS view differed from the downloader transport's view. The first check is
removed. The downloader retains all SSRF controls and revalidates redirects.

This is not a private-host bypass: removing the duplicate check does not allow
the downloader to fetch private, loopback, link-local, multicast, or
unspecified addresses.

## Residual Risks

- DNS can change between validation and connection; the configured HTTP client
  must keep resolved-IP validation enabled.
- Large numbers of references can amplify outbound work; count, byte, timeout,
  and per-host connection limits remain mandatory.
- Relay tokens appear in URLs. Do not log full URLs or request bodies.

## Audit Evidence

The implementation tests use an injectable downloader, so relay-shaped URLs can
be tested without network access or paid upstream calls. Production tests must
also verify that no full token-bearing URL is written to logs.
