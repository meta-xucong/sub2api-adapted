package apicompat

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ResponsesStreamItemIDReconciler keeps output item IDs stable across a
// Responses SSE stream. Some OpenAI-compatible upstreams use the correct ID
// in output_item.added/done but rebuild response.completed.output with a fresh
// ID. Clients correlate those lifecycle events, so retain the first identity.
type ResponsesStreamItemIDReconciler struct {
	byCallID      map[string]responsesStreamItemID
	byOutputIndex map[int]responsesStreamItemID
}

type responsesStreamItemID struct {
	id       string
	typeName string
}

// NewResponsesStreamItemIDReconciler creates a per-response reconciler.
func NewResponsesStreamItemIDReconciler() *ResponsesStreamItemIDReconciler {
	return &ResponsesStreamItemIDReconciler{
		byCallID:      make(map[string]responsesStreamItemID),
		byOutputIndex: make(map[int]responsesStreamItemID),
	}
}

// Observe records IDs from output_item.added/done. Invalid or unrelated
// provider extensions are ignored to preserve the tolerant passthrough path.
func (r *ResponsesStreamItemIDReconciler) Observe(payload []byte, eventTypeHint ...string) {
	if r == nil || len(bytes.TrimSpace(payload)) == 0 || !gjson.ValidBytes(payload) {
		return
	}
	eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
	if eventType == "" && len(eventTypeHint) > 0 {
		eventType = strings.TrimSpace(eventTypeHint[0])
	}
	if eventType != "response.output_item.added" && eventType != "response.output_item.done" {
		return
	}
	item := gjson.GetBytes(payload, "item")
	if !item.Exists() || !item.IsObject() {
		return
	}
	itemID := strings.TrimSpace(item.Get("id").String())
	if itemID == "" {
		return
	}
	entry := responsesStreamItemID{id: itemID, typeName: strings.TrimSpace(item.Get("type").String())}
	callID := strings.TrimSpace(item.Get("call_id").String())
	if callID != "" {
		if _, exists := r.byCallID[callID]; !exists {
			r.byCallID[callID] = entry
		}
	}
	if outputIndex := gjson.GetBytes(payload, "output_index"); outputIndex.Exists() && outputIndex.Type == gjson.Number {
		index := int(outputIndex.Int())
		if _, exists := r.byOutputIndex[index]; !exists {
			r.byOutputIndex[index] = entry
		}
	}
}

// ReconcileTerminalEvent replaces response.output item IDs with IDs observed
// earlier in the same response. call_id is preferred because terminal arrays
// may reorder sparse output indexes; array position is the fallback.
func (r *ResponsesStreamItemIDReconciler) ReconcileTerminalEvent(payload []byte, eventTypeHint ...string) ([]byte, bool) {
	if r == nil || len(bytes.TrimSpace(payload)) == 0 || !gjson.ValidBytes(payload) {
		return payload, false
	}
	eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
	if eventType == "" && len(eventTypeHint) > 0 {
		eventType = strings.TrimSpace(eventTypeHint[0])
	}
	switch eventType {
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
	default:
		return payload, false
	}
	output := gjson.GetBytes(payload, "response.output")
	if !output.Exists() || !output.IsArray() {
		return payload, false
	}

	updated := payload
	changed := false
	for index, item := range output.Array() {
		if !item.IsObject() {
			continue
		}
		itemType := strings.TrimSpace(item.Get("type").String())
		callID := strings.TrimSpace(item.Get("call_id").String())
		entry, found := r.byCallID[callID]
		if !found {
			entry, found = r.byOutputIndex[index]
		}
		if !found || entry.id == "" || !responsesStreamItemTypesMatch(entry.typeName, itemType) || strings.TrimSpace(item.Get("id").String()) == entry.id {
			continue
		}
		next, err := sjson.SetBytes(updated, "response.output."+strconv.Itoa(index)+".id", entry.id)
		if err != nil {
			continue
		}
		updated = next
		changed = true
	}
	return updated, changed
}

func responsesStreamItemTypesMatch(observed, terminal string) bool {
	observed = strings.TrimSpace(observed)
	terminal = strings.TrimSpace(terminal)
	return observed == "" || terminal == "" || observed == terminal
}
