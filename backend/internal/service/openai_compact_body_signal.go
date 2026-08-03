package service

import "github.com/tidwall/gjson"

// OpenAICompactBodySignal describes a compact request detected on the regular
// /responses endpoint. Kind is intentionally a stable, low-cardinality value
// for request logs and diagnostics.
type OpenAICompactBodySignal struct {
	Detected bool
	Kind     string
}

// DetectOpenAICompactBodySignal detects both compact request shapes emitted by
// current Codex clients:
//
//   - the older explicit input item: type=compaction_trigger;
//   - the newer pre-sampling shape: Codex turn metadata declares
//     request_kind=compaction and input contains a non-empty compaction item.
//
// The second form is deliberately gated by Codex's semantic metadata and a
// valid encrypted compaction item. This prevents ordinary gpt-5.6 Responses
// calls, which also use additional_tools, from being routed to compact.
func DetectOpenAICompactBodySignal(body []byte) OpenAICompactBodySignal {
	if len(body) == 0 {
		return OpenAICompactBodySignal{}
	}

	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return OpenAICompactBodySignal{}
	}

	hasTrigger := false
	hasEncryptedCompaction := false
	input.ForEach(func(_, item gjson.Result) bool {
		switch item.Get("type").String() {
		case "compaction_trigger":
			hasTrigger = true
		case "compaction", "compaction_summary":
			if item.Get("encrypted_content").Type == gjson.String && item.Get("encrypted_content").String() != "" {
				hasEncryptedCompaction = true
			}
		}
		return !(hasTrigger && hasEncryptedCompaction)
	})
	if hasTrigger {
		return OpenAICompactBodySignal{Detected: true, Kind: "input.compaction_trigger"}
	}

	turnMetadata := gjson.GetBytes(body, "client_metadata").Get("x-codex-turn-metadata").String()
	if turnMetadata == "" || gjson.Get(turnMetadata, "request_kind").String() != "compaction" || !hasEncryptedCompaction {
		return OpenAICompactBodySignal{}
	}
	return OpenAICompactBodySignal{Detected: true, Kind: "codex.request_kind_compaction"}
}

// HasCompactionTriggerInInput detects the Codex remote compact v2 body signal:
// an input item with type "compaction_trigger". When the client sends this
// inside a normal POST /v1/responses (instead of POST /v1/responses/compact),
// the request must still be treated as a compact request — otherwise the
// upstream path, model mapping, and body normalization are all wrong, causing
// Codex to receive a non-compact response and fail with:
//
//	"remote compaction v2 expected exactly one compaction output item, got 0"
//
// The gateway handler promotes such requests by rewriting the URL path to the
// compact form before stream parsing, compact body normalization, and
// compact-capable account scheduling, so both inbound forms share one code path.
func HasCompactionTriggerInInput(body []byte) bool {
	return DetectOpenAICompactBodySignal(body).Kind == "input.compaction_trigger"
}
