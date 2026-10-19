# Realtime custom-voice routing boundary

The gateway opens the provider WebSocket before accepting the first client
`session.update` event. At that time the custom voice has not been supplied, so
the ordinary scheduler selects an account without custom-voice ownership affinity.

The isolation repair validates voice selections against the connection's already
selected account. An owned voice on that account is permitted; an owned voice on
a different account is rejected with a sanitized error event, and the session
continues. The gateway does not migrate the socket or expose another account's
voice. This is a deliberate safety boundary, **not proof that custom-voice
realtime requests succeed for every multi-account group**.

The existing HTTP TTS flow can inspect the body before scheduling and pins the
owning account. Realtime does not gain that routing capability from this patch.
Operators can make selection deterministic with an appropriately authorized group
containing the single owning Grok account, subject to ordinary account eligibility.
Changing groups/accounts is a separate administrative action. No new query hint,
voice protocol, deferred-handshake behavior, or automatic configuration change is
introduced here.

Coverage: the actual local WebSocket relay test accepts a same-account owned
voice after rejecting a foreign owner's voice and preserves observed audio.
`TestGrokCustomVoiceRealtimeAccountAffinityBoundary` explicitly checks
`session.update` for a same-owner voice on the selected account versus another
account. That additional test must be run on the combined candidate before it is
counted as passing coverage. No live-provider test is implied.
