package service

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesCompatHistoryExpandsPreviousResponse(t *testing.T) {
	svc := &OpenAIGatewayService{}
	firstInput := json.RawMessage(`[{"role":"user","content":"run the tool"}]`)
	first := &apicompat.ResponsesResponse{
		ID: "resp_first",
		Output: []apicompat.ResponsesOutput{{
			Type:      "function_call",
			ID:        "fc_item",
			CallID:    "call_1",
			Name:      "exec",
			Arguments: `{"cmd":"pwd"}`,
		}},
	}
	svc.rememberOpenAIResponsesCompatHistory(firstInput, first)

	req := &apicompat.ResponsesRequest{
		PreviousResponseID: "resp_first",
		Input:              json.RawMessage(`[{"type":"function_call_output","call_id":"call_1","output":"ok"}]`),
	}
	require.NoError(t, svc.prepareOpenAIResponsesCompatContinuation(req))
	require.Empty(t, req.PreviousResponseID)
	var items []map[string]any
	require.NoError(t, json.Unmarshal(req.Input, &items))
	require.Len(t, items, 3)
	require.Equal(t, "function_call", items[1]["type"])
	require.Equal(t, "function_call_output", items[2]["type"])
}

func TestOpenAIResponsesCompatHistoryFailsClosedWhenMissing(t *testing.T) {
	svc := &OpenAIGatewayService{}
	req := &apicompat.ResponsesRequest{PreviousResponseID: "resp_missing", Input: json.RawMessage(`"next"`)}
	require.ErrorIs(t, svc.prepareOpenAIResponsesCompatContinuation(req), errOpenAIResponsesCompatHistoryUnavailable)
}
