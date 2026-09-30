package apicompat

import (
	"strings"
	"testing"
)

func TestChatCompletionsResponseToResponsesNormalizesUpstreamID(t *testing.T) {
	response := ChatCompletionsResponseToResponses(&ChatCompletionsResponse{ID: "chatcmpl_upstream"}, "glm-5.2", nil, nil, false, nil)
	if !strings.HasPrefix(response.ID, "resp_") {
		t.Fatalf("response id = %q, want resp_*", response.ID)
	}
}

func TestChatCompletionsStreamKeepsResponsesID(t *testing.T) {
	state := NewChatCompletionsToResponsesStreamState("glm-5.2")
	ChatCompletionsChunkToResponsesEvents(&ChatCompletionsChunk{ID: "chatcmpl_upstream"}, state)
	if !strings.HasPrefix(state.ResponseID, "resp_") {
		t.Fatalf("stream response id = %q, want resp_*", state.ResponseID)
	}
	ChatCompletionsChunkToResponsesEvents(&ChatCompletionsChunk{ID: "chatcmpl_other"}, state)
	if !strings.HasPrefix(state.ResponseID, "resp_") {
		t.Fatalf("stream response id changed to %q", state.ResponseID)
	}
}

func TestAnthropicResponsesNormalizeMessageID(t *testing.T) {
	response := AnthropicToResponsesResponse(&AnthropicResponse{ID: "msg_upstream", Model: "claude-fable-5"})
	NormalizeResponsesResponseID(response)
	if !strings.HasPrefix(response.ID, "resp_") {
		t.Fatalf("response id = %q, want resp_*", response.ID)
	}
}

func TestAnthropicStreamLifecycleAndIdentity(t *testing.T) {
	state := NewAnthropicEventToResponsesState()
	events := AnthropicEventToResponsesEvents(&AnthropicStreamEvent{
		Type:    "message_start",
		Message: &AnthropicResponse{ID: "msg_upstream", Model: "claude-fable-5"},
	}, state)
	if len(events) != 2 || events[0].Type != "response.created" || events[1].Type != "response.in_progress" {
		t.Fatalf("message_start events = %#v, want created then in_progress", events)
	}
	if !strings.HasPrefix(state.ResponseID, "resp_") {
		t.Fatalf("stream response id = %q, want resp_*", state.ResponseID)
	}
	if events[0].Response.ID != events[1].Response.ID {
		t.Fatalf("lifecycle IDs differ: %q vs %q", events[0].Response.ID, events[1].Response.ID)
	}
}
