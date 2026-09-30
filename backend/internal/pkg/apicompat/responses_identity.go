package apicompat

import "strings"

// normalizeResponsesResponseID keeps the public Responses identity in the
// Responses namespace. Chat Completions and Anthropic upstreams commonly use
// chatcmpl_* / msg_* (or provider-specific UUIDs); exposing those values as a
// Responses response.id makes previous_response_id validation reject an
// otherwise valid bridge response and can also make item ownership ambiguous.
func normalizeResponsesResponseID(id string) string {
	if strings.HasPrefix(strings.TrimSpace(id), "resp_") {
		return strings.TrimSpace(id)
	}
	return generateResponsesID()
}

// NormalizeResponsesResponseID applies the public Responses ID contract to a
// response that is about to be emitted on /v1/responses.  The generic
// Anthropic-to-Responses value is also consumed by Chat Completions bridges,
// where preserving the provider's request ID is useful for tracing; callers
// must therefore opt in at the public Responses boundary.
func NormalizeResponsesResponseID(resp *ResponsesResponse) {
	if resp == nil {
		return
	}
	resp.ID = normalizeResponsesResponseID(resp.ID)
}

// NewResponsesID returns a response-scoped identifier for adapters that must
// normalize a raw upstream Responses event without first decoding the full
// response object.  The returned value is safe to expose as response.id and
// previous_response_id.
func NewResponsesID() string {
	return generateResponsesID()
}
