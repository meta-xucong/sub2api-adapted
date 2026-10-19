# Narrow resource repair and retained IP policy

## Scope and provenance

Reviewed baseline `ff27dd9c0` and the existing phase-one/two evidence. This is not
an aggregate memory-governance project or an IP-default migration. No provider,
VPS, credential, voice, or billing action is part of this repair.

`grok_wokey_video.go` is fork-specific; the prior official comparison and exact
scope are recorded in `../hardening-20261018/DOWNLOAD_SECURITY.md`. The recorded
comparison is official `b0a02a3304a0a78cdd003ddda1dd23ba02c03084`; that object is
not present in this checkout, so this pass does not claim a fresh upstream diff.

## Wokey video Data URL allocation defect

Reachability: `handler/grok_media.go` reads an authenticated media request;
`service/grok_media.go` calls `prepareWokeyVideoImageMultipartBody` only for
Wokey video generation; its reference downloader routes Data URLs to
`decodeWokeyVideoDataURL`. The existing per-image decoded limit is **8 MiB**,
with 4 ordinary or 7 multimodal references. The gateway default request limit
is 256 MiB, so the existing request-body limit does not prevent this defect.

Previously the decoder allocated from the complete base64 string, or unescaped
the complete URL and converted it to bytes, **before** rejecting images larger
than 8 MiB. Base64 CR/LF wrapping also inflated allocation for a tiny valid image.

The repair enforces the existing bound before allocation:

- Count base64 characters excluding the CR/LF ignored by Go's decoder; reject
  counts above the encoded representation of 8 MiB.
- Keep the already-bounded ordinary base64 decoding path. For line-wrapped input,
  stream through a decoded 8 MiB + 1 reader instead of allocating by raw length.
- Count the URL-unescaped size first. Keep `url.PathUnescape` as the validator;
  malformed escapes still fail. Existing plus-sign and binary-byte semantics stay.
- Preserve the existing exact-8-MiB acceptance, MIME behavior, image counts,
  provider transport and downstream error classification.

No new product size limit, semaphore, busy response or global resource policy
was introduced. Input/request-body storage, JSON copies, multipart serialization,
concurrent requests, and stream accumulation remain independent memory costs.

### Validation evidence

`grok_wokey_video_decode_bounds_test.go` covers valid and malformed base64/URL
encodings, CR/LF compatibility, exact 8 MiB and +1 for plain/wrapped base64 and
plain/escaped URL data, plus preallocated-input allocation checks.

An isolated Go probe copied the exact decoder and its two helpers from baseline
and the patched worktree, avoiding a full service rebuild while other work was
in progress. It used synthetic data only and performed no network calls.

- Baseline oversized 16 MiB decoded inputs allocated 16,786,112 bytes (base64),
  16,777,888 bytes (plain) and 33,555,104 bytes (escaped) before rejection.
- Patched oversized rejection allocated 608 bytes for each representation.
- A one-byte valid image with 16 MiB of CR/LF allocated 12,591,216 bytes before;
  the patched decoder allocated 2,744 bytes.
- Baseline failed the four intended allocation checks. Patched compatibility,
  exact-cap and allocation cases passed. These are isolated function receipts,
  **not** full service, deployment, or aggregate RSS acceptance.
- The full service regression suite remains a separate coordinated release gate.

Adjacent bounded review: Wokey image-edit Data URLs already have encoded-byte
and pixel checks; KIE reference-image upload/probe paths use validated HTTPS URLs
and bounded body reads rather than this decoder. This is a finite adjacent review,
not a claim that all decoders in the repository are covered.

## Forwarded-IP boundary retained

The default compatibility setting and migration that may change an old stored
`false` to `true` when no trusted-proxy policy was configured are unchanged.
This is a documented compatibility/deployment policy, not a defect declared
closed by this patch. Refer to the existing official-first provenance and release
instructions before choosing a deployment setting.

The explicitly configured secure path remains available: request settings are
snapshotted in `SessionBindingContext`; `GetClientIP` and `GetSecurityClientIP`
use Gin's trusted-proxy chain when that snapshot disables forwarded-header trust.
API-key ACLs and session binding use that security resolver. Read failure or
malformed configured header settings retain the existing fail-closed behavior.

Fresh `go test -tags unit -count=1 ./internal/pkg/ip` passed. The existing tests
cover forged forwarding headers, custom-header precedence, disabled-mode trusted
peer behavior, and request-snapshot precedence. This does not validate a live
proxy topology or persisted database settings. Deployment acceptance must inspect
the effective persisted mode, exact trusted peers and origin access restrictions;
YAML alone is insufficient proof.

## Explicit residuals

No fixed global memory cap or zero-vulnerability claim is justified. Existing
large SSE line limits, accumulated content/tool output, concurrency and request
body copies require separate capacity policy and real workload tests. Shared
custom-voice ownership, cross-attempt billing policy, real Provider behavior,
Docker/Redis/IPC integration and VPS capacity remain outside this narrow result.
