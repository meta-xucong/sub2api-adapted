# Custom voice isolation design

Before this change, the baseline gateway passed account-wide custom voice management through a shared-account scheduler. This exposed another user's voice metadata, reference audio and mutation operations. Baseline TTS and realtime also accepted account-scoped voice IDs without ownership checks.

## Boundary

The administrator account-identity contract and legacy-resource/rollback procedure are defined in [LOCAL_ACCEPTANCE.md](LOCAL_ACCEPTANCE.md). An account ID must continue to represent the same upstream resource namespace; replacing its principal or origin requires a new account and explicit migration assessment. Ordinary token refresh does not constitute principal replacement.

A durable SQL mapping records voice ID, authenticated user ID, upstream account ID and the provider profile. User and account ownership are immutable. External ID collisions fail closed rather than overwriting ownership; no assumption of globally unique provider IDs is required. Deletion retains a tombstone. Unknown legacy IDs are never automatically adopted, even by guessing them. Admin adoption is intentionally outside this change.

New creation persists the mapping before returning the provider response. A persistence failure returns a gateway error without exposing the ID; the upstream object may be orphaned and requires operator reconciliation. Creation is not retried across accounts after it reaches the provider. List responses use SQL keyset pagination (limit 1–1000, one extra row) over the caller's durable mappings, joined to current account/group assignments; no provider-wide inventory or unbounded per-row lookup occurs. Item reads, patches, audio and deletes pin the recorded account and recheck scheduler/group eligibility. No resource request can fail over to another account. Persistence uses a bounded five-second context detached from client cancellation. Successful deletion tombstones the mapping; if that write fails, the gateway reports failure and retries may treat upstream 404 as deletion success for the already-authorized mapping.

TTS checks custom ownership before account selection and pins owned voices. Known foreign, tombstoned or group-denied IDs never fall back to built-in classification, even when a provider catalogue lists a colliding ID. Only truly unmapped voice identifiers are permitted if the selected provider's public built-in catalogue includes them. The documented default without a voice identifier is retained without a catalogue lookup. The catalogue is cached for five minutes per account/credential/URL, with at most 128 entries of 256 IDs (128 bytes per ID); larger catalogues are checked without caching. Metadata is limited to 8 KiB on persistence and 16 KiB after PostgreSQL JSONB canonicalization; no audio is stored in ownership rows. The catalogue response is bounded to 1 MiB; failures fail closed only for explicitly selected, unverified voices. HTTP JSON and realtime events reject ambiguous duplicate voice-bearing fields. Realtime validates each client voice setting against the authenticated owner and already-selected account; it cannot migrate an established socket. An invalid voice event produces a sanitized protocol error and is not forwarded, while the session continues so already-observed audio remains eligible for the existing billing flow. Initial requests forward no caller voice query, and provider default semantics remain intact.

SQL changes are additive. Existing unmapped custom voices become inaccessible through tenant gateway routes; operators must reconcile them explicitly. Built-in and default voices and STT remain available. No real provider calls or production data are used in automated verification.

## Evidence

- Existing paths: `internal/handler/grok_audio.go`, `internal/service/grok_audio.go`.
- Official voice reference: https://docs.x.ai/developers/rest-api-reference/inference/voice
- Custom voice interoperability: https://docs.x.ai/developers/model-capabilities/audio/custom-voices

## Verification targets

Tenant denial without ID enumeration, immutable ownership/collision rejection, group removal, account pinning, list filtering, create persistence failure, deletion retry/tombstones, TTS and realtime voice checks including malformed/duplicate JSON, and concrete production repository interface wiring. These tests are local fake-provider/database tests, not live-provider acceptance.

## Identifier compatibility

Custom IDs must contain 1–128 ASCII letters, digits, dashes or underscores. This preserves official eight-character IDs and common UUID/relay aliases. Creation previously accepted malformed IDs such as `..` that resource paths could not use; this was an unusable-mapping defect, not a demonstrated gateway traversal. Provider profiles must expose exactly one canonical `voice_id` string in the root object. Duplicate/case-variant identity keys are rejected before persistence or returning the profile. Unrelated metadata fields remain unchanged.

Realtime validation uses a fresh bounded per-event context and does not retain pooled Gin state or the expired handshake credential budget. Audio observation retains the original valid, authorized client-frame policy before transport write; rejected malformed/unauthorized frames do not enable billing. Existing write-error/close billing rules are unchanged.

## Final pre-upload compatibility closure

Public built-in catalogue IDs use ASCII case-insensitive comparison according to the official xAI TTS contract. Exact custom ownership checks still come first. A matching canonical catalogue ID with any persisted custom mapping cannot be used as a public alias; ambiguous case variants are rejected. Catalogue cache hits recheck current canonical ownership rather than caching authorization. Custom IDs remain exact. Tests and official source links are recorded in PREUPLOAD_REVIEW_PLAN.md and PREUPLOAD_ACCEPTANCE.md.
