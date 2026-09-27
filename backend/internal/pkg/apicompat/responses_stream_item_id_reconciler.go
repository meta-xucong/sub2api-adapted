package apicompat

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ResponsesStreamItemIDReconciler keeps output item IDs stable across a
// Responses SSE stream. Some OpenAI-compatible upstreams reuse the correct
// item ID in response.output_item.added/done but rebuild response.completed's
// response.output items with a different ID. Codex correlates those lifecycle
// events, so the gateway must retain the first streamed identity.
//
// The reconciler is deliberately observation-only until a terminal event is
// received. It never invents an ID and only rewrites an output item when that
// item was observed earlier in the same response.
type ResponsesStreamItemIDReconciler struct {
	byCallID      map[string]responsesStreamItemID
	byOutputIndex map[int]responsesStreamItemID
}

type responsesStreamItemID struct {
	id       string
	typeName string
}

// NewResponsesStreamItemIDReconciler creates an empty per-response ID
// reconciler. A new instance must be used for every Responses response.
func NewResponsesStreamItemIDReconciler() *ResponsesStreamItemIDReconciler {
	return &ResponsesStreamItemIDReconciler{
		byCallID:      make(map[string]responsesStreamItemID),
		byOutputIndex: make(map[int]responsesStreamItemID),
	}
}

// Observe records the ID from response.output_item.added/done. Invalid or
// unrelated payloads are ignored so the normal stream path remains tolerant of
// provider-specific event extensions.
func (r *ResponsesStreamItemIDReconciler) Observe(payload []byte) {
	if r == nil || len(bytes.TrimSpace(payload)) == 0 || !gjson.ValidBytes(payload) {
		return
	}
	eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
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
	itemType := strings.TrimSpace(item.Get("type").String())
	entry := responsesStreamItemID{id: itemID, typeName: itemType}

	callID := strings.TrimSpace(item.Get("call_id").String())
	if callID != "" {
		// The first observed ID is the identity exposed by output_item.added.
		// Keep it if a provider changes the ID again in output_item.done.
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

// ReconcileTerminalEvent replaces response.output item IDs with the IDs
// observed in the stream. call_id is preferred because some providers emit
// sparse/non-zero output indexes; array position is used as the fallback for
// items without a call_id.
func (r *ResponsesStreamItemIDReconciler) ReconcileTerminalEvent(payload []byte) ([]byte, bool) {
	if r == nil || len(bytes.TrimSpace(payload)) == 0 || !gjson.ValidBytes(payload) {
		return payload, false
	}
	eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
	switch eventType {
	case "response.completed", "response.done", "response.incomplete", "response.cancelled", "response.canceled":
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
		if !found || entry.id == "" || !responsesStreamItemTypesMatch(entry.typeName, itemType) {
			continue
		}
		if strings.TrimSpace(item.Get("id").String()) == entry.id {
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
