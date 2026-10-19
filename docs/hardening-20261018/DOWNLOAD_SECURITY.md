# Download security: provenance, scope and verification

## Classification and official-first comparison

This is a narrow, public-evidence-backed gap repair, not a claim that the patch
was ported unchanged from upstream. The fork-only KIE/Wokey call-site changes
opt into the repaired downloader. No provider transport redesign is included.

Compared on 2026-10-17 against official Wei-Shaw/sub2api
[`b0a02a3304a0a78cdd003ddda1dd23ba02c03084`](https://github.com/Wei-Shaw/sub2api/commit/b0a02a3304a0a78cdd003ddda1dd23ba02c03084),
dated 2026-10-16. The following official Git blob hashes exactly match the fork
baseline `2bedb713b6b0b9cf968468decae36732f390574a`:

| Baseline file under `backend/internal/` | Matching official blob |
| --- | --- |
| `pkg/httpclient/pool.go` | `22d3c65feb9d65b2d954bbcde6d36a6309626c11` |
| `service/image_storage.go` | `b160846b471660f28fbf1b6d1fd3bbdf6ed676b2` |
| `repository/http_upstream.go` | `ff19b03d8320d6ff321faae3fa37c50572c2c62a` |
| `service/http_upstream_profile.go` | `92020abb357c5822bd2271eebe30baf6ff97aabc` |
| `service/openai_images_b64_backfill.go` | `3210c0fcbe717b77f69c3e7b6473a13cf1243fa1` |

Existing official changes were inspected before retaining a local repair:

- [`25e1632`](https://github.com/Wei-Shaw/sub2api/commit/25e16326281583360a06cb31448f8ca0a51a4875)
  added request-stage DNS validation to the shared HTTP pool. It is already in
  the baseline. The validator resolves a hostname separately from the standard
  transport's subsequent connection lookup; a successful precheck does not bind
  the socket destination.
- [`c227863`](https://github.com/Wei-Shaw/sub2api/commit/c227863d51468bee3a48f999515bd233bc2d41cf)
  explicitly addressed untrusted image-result URLs, adding the public-host-only
  marker, initial/redirect validation and byte-based MIME checks. It is already
  in the baseline. The marker still routes through a transport which resolves
  again after validation; the proxy can also resolve independently.
- [`d6467f6`](https://github.com/Wei-Shaw/sub2api/commit/d6467f6eb029c0547f7b0d7b51768179de631103)
  bounded data-URL decoding in image-result offload. It is already in the
  baseline. The separate `b64_json` decode remains unbounded there, the default
  downloader has no public-destination guard, and non-image bytes can fall back
  to `image/png` or a supplied image MIME label.

No newer repair for these gaps is present in the compared official files.
The three changed KIE/Wokey service files are absent from that official tree
(GitHub contents API returned 404); their strict download opt-ins are fork work.

## Matching public evidence and existing official primitive

- Official [image-download SSRF issue #6169](https://github.com/Wei-Shaw/sub2api/issues/6169)
  reports the same untrusted-image-URL/internal-network threat. It identifies
  `downloadOpenAIImageBytes`, not `ImageResultUploader`, so it is contextual
  evidence rather than a claim that upstream reported every function here.
- Official [`20a4e41`](https://github.com/Wei-Shaw/sub2api/commit/20a4e41872a5207a31511c80ab649df9b5fec298)
  introduced socket-level rebinding protection for channel monitoring. Current
  [`channel_monitor_ssrf.go`](https://github.com/Wei-Shaw/sub2api/blob/b0a02a3304a0a78cdd003ddda1dd23ba02c03084/backend/internal/service/channel_monitor_ssrf.go)
  uses one lookup followed by numeric-IP dialing, and blocks `100.64.0.0/10`.
  Its blob `8d93f6004593f3cb32e691c4831f1881ef5977e3` also matches the fork.
- [OWASP's SSRF guidance](https://cheatsheetseries.owasp.org/cheatsheets/Server_Side_Request_Forgery_Prevention_Cheat_Sheet.html)
  explains why checking DNS then allowing a second unchecked lookup is
  insufficient, and requires preserving the original Host/SNI and TLS hostname
  verification when binding validated addresses.
- [Alibaba's metadata documentation](https://www.alibabacloud.com/help/en/ecs/user-guide/view-instance-metadata/)
  identifies `100.100.100.200` and explicitly discusses image-download SSRF.
  Go's global-unicast/private predicates alone do not reject this shared-space
  address. The [IANA IPv4](https://www.iana.org/assignments/iana-ipv4-special-registry/iana-ipv4-special-registry.xhtml)
  and [IPv6](https://www.iana.org/assignments/iana-ipv6-special-registry/iana-ipv6-special-registry.xhtml)
  special-purpose registries support the conservative address policy.
- [OWASP file-upload guidance](https://cheatsheetseries.owasp.org/cheatsheets/File_Upload_Cheat_Sheet.html)
  supports bounded content and checking bytes rather than trusting declared MIME.
  These principles also apply to the uploader's untrusted remote/base64 inputs.

The shared-pool adaptation follows the official monitor's lookup-once,
numeric-dial algorithm. Calling its service-private helper from `pkg/httpclient`
would create an import cycle, because `service` already imports that package.
Extracting and migrating the monitor would broaden scope and change policy:
the official helper skips unsafe DNS answers and may use a public answer,
whereas these untrusted downloaders deliberately reject the entire mixed
answer set before any dial. Additional special-purpose/translation address
restrictions are intentional. The monitor is unchanged.

## Implemented behavior and compatibility limits

- Direct `ValidateResolvedIP` clients now validate each fresh connection and
  dial only the checked numeric addresses. Existing connections can be reused;
  a new connection resolves and validates again. URL/Host remain unchanged, so
  signed URLs, TLS SNI and certificate hostname verification remain intact.
  DNS and all dial attempts share a five-second cap (or the caller's earlier
  deadline); each candidate receives a share of the remaining time, preserving
  fallback if an earlier address is blackholed.
- `RequirePinnedDestination` implies public-destination enforcement. Explicit
  HTTP/HTTPS/SOCKS proxies and `AllowPrivateHosts` conflicts fail closed. The
  three KIE/Wokey download/probe call sites set this option; provider API proxy
  calls are unchanged.
- Both repository `Do` and `DoWithTLS` dispatch only public-host-marked requests
  to the strict shared downloader. Initial URLs and redirects must be HTTP(S),
  credential-free and public. The redirects-disabled marker is preserved.
  The path reuses existing cancellation/decompression handling and default
  connection limits. It does not invoke provider TLS fingerprinting or Grok
  provider fallbacks. Current callers are OpenAI b64 backfill and the fork's
  KIE signed-video download.
- Marked repository downloads explicitly reject account proxies. The default
  image uploader explicitly rejects an effective environment HTTP(S) proxy,
  evaluating `NO_PROXY` for each request/redirect. It never silently switches a
  proxy-configured request to direct access. Thus proxy-only deployments may
  lose these download features until a transport can securely enforce the
  destination through the proxy. Errors do not expose proxy credentials.
- Trusted, non-strict proxy callers keep their existing DNS prechecks and
  pooling. Those prechecks do **not** constrain the proxy's DNS result. Unmarked
  repository provider transports are unchanged. This patch is not an
  all-transports SSRF or rebinding fix.
- Strict policy rejects loopback/private/link-local/shared/reserved addresses,
  IPv4-mapped unsafe addresses, standard NAT64 prefixes, 6to4 and Teredo, and
  conservatively allows native IPv6 only within `2000::/3` excluding the listed
  special-purpose blocks. Some globally reachable special-purpose anycasts
  are intentionally rejected too. Standard NAT64-only connectivity is therefore
  unsupported. Application checks cannot identify arbitrary network-specific
  NAT64 prefixes or replace an egress firewall on unusually translated/routed
  networks.
- The default image-result uploader is protected; an explicitly supplied HTTP
  client is a trusted injection whose caller owns URL/redirect policy. Production
  settings supply `nil` and use the protected default.
- Remote bodies, data URLs and `b64_json` use the configured decoded-byte cap
  (32 MiB by default). Accepted bytes must have consistent PNG/JPEG/GIF/WebP
  sniffing and `DecodeConfig` results, positive dimensions, side at most 16,384
  and area at most 16,777,216 pixels. The geometry limits are local conservative
  policy, not an upstream standard. No full pixel allocation is performed.
  Header/geometry checks do not validate every compressed pixel stream and do
  not establish a global concurrent-memory bound.

## Verification

Completed focused runs, using Go 1.27.2, `GOTOOLCHAIN=local`, `GOMAXPROCS=1`,
`GOGC=20`, `GOMEMLIMIT=1GiB`, and `-p 1`:

- Entire `internal/pkg/httpclient` package: PASS, 1.012s. Covers connection
  binding, changed DNS on the next connection, unsafe literal/mixed answers,
  CGNAT metadata, IPv4-mapped addresses and IPv6 translation ranges, cancellation,
  TLS SNI/Host/certificate verification, blackholed-address fallback within the
  shared deadline, strict proxy refusal (including malformed credential-bearing
  proxy URLs) and legacy proxy construction.
- Isolated `image_storage.go` / `image_storage_test.go` slice: PASS, 0.006s.
  Covers URL/redirect policy, effective proxy/NO_PROXY, all three byte caps,
  MIME spoofing/non-images, and geometry rejection without pixel allocation.
- `gofmt` and scoped `git diff --check`: PASS.

All test traffic is mocked or loopback; no live provider or paid calls.
The isolated uploader run omits only the two ImageTaskService integration
tests. Repository marker tests and full service integration remain for the
coordinated backend run; these focused results are not a full-suite claim.
The final test matrix records later coordinated results.

Focused logs: `httpclient-security-tests.log` and
`image-storage-security-tests.log` in the audit delivery bundle.
