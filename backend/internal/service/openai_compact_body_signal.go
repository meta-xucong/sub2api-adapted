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
// current Codex clients. The newer form is gated by Codex turn metadata and an
// encrypted compaction item so ordinary Responses calls remain unaffected.
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

// HasCompactionTriggerInInput detects an input item with
// type="compaction_trigger". The handler combines this body signal with the
// request path, stream flag, and Codex beta feature header to distinguish the
// native remote compaction v2 wire from the legacy /responses/compact bridge.
func HasCompactionTriggerInInput(body []byte) bool {
	return DetectOpenAICompactBodySignal(body).Kind == "input.compaction_trigger"
}
