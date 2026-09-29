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

func TestResponsesStreamItemIDReconciler_UsesSSEEventTypeHint(t *testing.T) {
	r := NewResponsesStreamItemIDReconciler()
	r.Observe([]byte(`{"output_index":0,"item":{"id":"fc_hint","type":"function_call","call_id":"call_hint"}}`), "response.output_item.added")

	completed := []byte(`{"response":{"output":[{"type":"function_call","id":"fc_rebuilt","call_id":"call_hint"}]}}`)
	updated, changed := r.ReconcileTerminalEvent(completed, "response.completed")
	if !changed {
		t.Fatal("expected event type hint to enable reconciliation")
	}
	if got := gjson.GetBytes(updated, "response.output.0.id").String(); got != "fc_hint" {
		t.Fatalf("response.output[0].id = %q, want fc_hint", got)
	}
}

func TestResponsesStreamItemIDReconciler_ReconcilesFailedTerminalEvent(t *testing.T) {
	r := NewResponsesStreamItemIDReconciler()
	r.Observe([]byte(`{"type":"response.output_item.done","output_index":0,"item":{"id":"fc_failed","type":"function_call","call_id":"call_failed"}}`))

	failed := []byte(`{"type":"response.failed","response":{"output":[{"type":"function_call","id":"fc_rebuilt","call_id":"call_failed"}]}}`)
	updated, changed := r.ReconcileTerminalEvent(failed)
	if !changed {
		t.Fatal("expected failed terminal output ID to be reconciled")
	}
	if got := gjson.GetBytes(updated, "response.output.0.id").String(); got != "fc_failed" {
		t.Fatalf("response.output[0].id = %q, want fc_failed", got)
	}
}
