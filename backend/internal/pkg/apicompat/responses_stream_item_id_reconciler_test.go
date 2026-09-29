package apicompat

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestResponsesStreamItemIDReconciler_ReusesStreamIDInCompletedOutput(t *testing.T) {
	r := NewResponsesStreamItemIDReconciler()
	r.Observe([]byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_stream","type":"function_call","call_id":"call_glm"}}`))
	r.Observe([]byte(`{"type":"response.output_item.done","output_index":0,"item":{"id":"fc_stream","type":"function_call","call_id":"call_glm"}}`))

	completed := []byte(`{"type":"response.completed","response":{"output":[{"id":"fc_rebuilt","type":"function_call","call_id":"call_glm"}]}}`)
	updated, changed := r.ReconcileTerminalEvent(completed)
	if !changed {
		t.Fatal("expected terminal output ID to be reconciled")
	}
	if got := gjson.GetBytes(updated, "response.output.0.id").String(); got != "fc_stream" {
		t.Fatalf("response.output[0].id = %q, want fc_stream", got)
	}
}

func TestResponsesStreamItemIDReconciler_UsesCallIDForSparseTerminalPosition(t *testing.T) {
	r := NewResponsesStreamItemIDReconciler()
	r.Observe([]byte(`{"type":"response.output_item.added","output_index":7,"item":{"id":"fc_sparse","type":"function_call","call_id":"call_sparse"}}`))

	completed := []byte(`{"type":"response.completed","response":{"output":[{"type":"message","id":"msg_1"},{"type":"function_call","id":"fc_rebuilt","call_id":"call_sparse"}]}}`)
	updated, changed := r.ReconcileTerminalEvent(completed)
	if !changed {
		t.Fatal("expected sparse terminal output ID to be reconciled by call_id")
	}
	if got := gjson.GetBytes(updated, "response.output.1.id").String(); got != "fc_sparse" {
		t.Fatalf("response.output[1].id = %q, want fc_sparse", got)
	}
	if got := gjson.GetBytes(updated, "response.output.0.id").String(); got != "msg_1" {
		t.Fatalf("unobserved message ID changed unexpectedly: %q", got)
	}
}

func TestResponsesStreamItemIDReconciler_DoesNotInventOrCrossTypeIDs(t *testing.T) {
	r := NewResponsesStreamItemIDReconciler()
	r.Observe([]byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_stream","type":"function_call","call_id":"call_glm"}}`))
	unchanged := []byte(`{"type":"response.completed","response":{"output":[{"type":"message","id":"msg_rebuilt","call_id":"call_glm"},{"type":"function_call","id":"fc_stream","call_id":"other"}]}}`)
	updated, changed := r.ReconcileTerminalEvent(unchanged)
	if changed || string(updated) != string(unchanged) {
		t.Fatalf("unexpected ID reconciliation: changed=%v payload=%s", changed, updated)
	}
}
