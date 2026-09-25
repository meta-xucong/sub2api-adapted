package apicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// These are protocol fixtures, not live provider calls. They pin the common
// Responses contract at the boundary where GLM/DeepSeek use Chat Completions
// and Claude uses Anthropic Messages.
func TestCodexThirdPartyResponsesRequestContract(t *testing.T) {
	const input = `[
      {"type":"message","role":"user","content":[{"type":"input_text","text":"请检查东京的天气 ☔"}]},
      {"type":"function_call","id":"item_exec","call_id":"call_exec","name":"unified_exec","arguments":"{\"command\":\"Get-ChildItem\"}"},
      {"type":"function_call_output","item_reference":"item_exec","call_id":"call_exec","is_error":true,"output":{"error":"工具暂时不可用"}}
    ]`

	for _, model := range []string{"glm-5.2", "deepseek-reasoner", "claude-sonnet-4-6"} {
		t.Run(model, func(t *testing.T) {
			req := &ResponsesRequest{
				Model:              model,
				Instructions:       "Skills instructions: 保留当前工作区规则。",
				Input:              json.RawMessage(input),
				PreviousResponseID: "resp_previous_123",
				ParallelToolCalls:  boolPtr(true),
				Tools: []ResponsesTool{{
					Type:       "function",
					Name:       "unified_exec",
					Parameters: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`),
				}},
			}

			chat, err := ResponsesToChatCompletionsRequest(req)
			require.NoError(t, err)
			require.Equal(t, model, chat.Model)
			require.Equal(t, "system", chat.Messages[0].Role)
			require.Contains(t, string(chat.Messages[0].Content), "Skills instructions")
			require.Equal(t, []string{"system", "user", "assistant", "tool"}, chatMessageRoles(chat.Messages))
			require.Equal(t, "call_exec", chat.Messages[2].ToolCalls[0].ID)
			require.Equal(t, "unified_exec", chat.Messages[2].ToolCalls[0].Function.Name)
			require.Equal(t, "call_exec", chat.Messages[3].ToolCallID)
			require.Contains(t, string(chat.Messages[3].Content), "工具暂时不可用")
			require.Len(t, chat.Tools, 1)
			require.NotNil(t, chat.ParallelToolCalls)

			anthropic, err := ResponsesToAnthropicRequest(req)
			require.NoError(t, err)
			require.Contains(t, string(anthropic.System), "Skills instructions")
			require.Len(t, anthropic.Messages, 3)

			var toolUse []AnthropicContentBlock
			require.NoError(t, json.Unmarshal(anthropic.Messages[1].Content, &toolUse))
			require.Len(t, toolUse, 1)
			require.Equal(t, "tool_use", toolUse[0].Type)
			require.Equal(t, "call_exec", toolUse[0].ID)

			var toolResult []AnthropicContentBlock
			require.NoError(t, json.Unmarshal(anthropic.Messages[2].Content, &toolResult))
			require.Len(t, toolResult, 1)
			require.Equal(t, "tool_result", toolResult[0].Type)
			require.Equal(t, "call_exec", toolResult[0].ToolUseID)
			require.True(t, toolResult[0].IsError, "Anthropic tool_result must retain Responses is_error")
			require.Contains(t, string(toolResult[0].Content), "工具暂时不可用")
		})
	}
}

func TestResponsesInputItemRetainsItemReferenceAndToolError(t *testing.T) {
	var item ResponsesInputItem
	require.NoError(t, json.Unmarshal([]byte(`{"type":"function_call_output","item_reference":"item_exec","call_id":"call_exec","is_error":true,"output":"failed"}`), &item))
	require.Equal(t, "item_exec", item.ItemReference)
	require.Equal(t, "call_exec", item.CallID)
	require.True(t, item.IsError)
	require.Equal(t, "failed", item.Output)

	encoded, err := json.Marshal(item)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"function_call_output","item_reference":"item_exec","call_id":"call_exec","output":"failed","is_error":true}`, string(encoded))
}

func TestResponsesToChatCompletionsRequest_ReplaysPortableCompactionSummary(t *testing.T) {
	req := &ResponsesRequest{
		Model: "glm-5.3",
		Input: json.RawMessage(`[{"type":"compaction","id":"cmp_1","encrypted_content":"provider-opaque","summary":[{"type":"summary_text","text":"保留用户目标、工具结果和未完成工作"}]},{"type":"message","role":"user","content":"继续"}]`),
	}

	chat, err := ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.Len(t, chat.Messages, 2)
	require.Equal(t, "user", chat.Messages[0].Role)
	var summary string
	require.NoError(t, json.Unmarshal(chat.Messages[0].Content, &summary))
	require.Contains(t, summary, "<conversation_summary>")
	require.Contains(t, summary, "保留用户目标、工具结果和未完成工作")
	var next string
	require.NoError(t, json.Unmarshal(chat.Messages[1].Content, &next))
	require.Equal(t, "继续", next)
}

func TestResponsesToChatCompletionsRequest_RejectsOpaqueOnlyCompaction(t *testing.T) {
	_, err := ResponsesToChatCompletionsRequest(&ResponsesRequest{
		Model: "deepseek-v4-flash",
		Input: json.RawMessage(`[{"type":"compaction","encrypted_content":"provider-opaque"}]`),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no portable summary")
}

func TestResponsesToAnthropicRequest_ReplaysPortableCompactionSummary(t *testing.T) {
	anthropic, err := ResponsesToAnthropicRequest(&ResponsesRequest{
		Model: "claude-sonnet-4-6",
		Input: json.RawMessage(`[{"type":"compaction","encrypted_content":"provider-opaque","summary":[{"type":"summary_text","text":"保留 Claude 会话摘要"}]}]`),
	})
	require.NoError(t, err)
	require.Len(t, anthropic.Messages, 1)
	require.Equal(t, "user", anthropic.Messages[0].Role)
	require.Contains(t, string(anthropic.Messages[0].Content), "保留 Claude 会话摘要")
}

func TestAnthropicToolResultConvertsBackWithErrorFlag(t *testing.T) {
	items, err := anthropicUserToResponses(json.RawMessage(`[{"type":"tool_result","tool_use_id":"call_exec","is_error":true,"content":"command failed"}]`))
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "function_call_output", items[0].Type)
	require.Equal(t, "call_exec", items[0].CallID)
	require.True(t, items[0].IsError)
	require.Equal(t, "command failed", items[0].Output)
}

func TestCodexThirdPartyResponsesOutputPreservesResponseAndCallIDs(t *testing.T) {
	resp := ChatCompletionsResponse{
		ID:    "chatcmpl_glm_parallel",
		Model: "glm-5.2",
		Choices: []ChatChoice{{
			FinishReason: "tool_calls",
			Message: ChatMessage{ToolCalls: []ChatToolCall{
				{ID: "call_a", Type: "function", Function: ChatFunctionCall{Name: "unified_exec", Arguments: `{"command":"pwd"}`}},
				{ID: "call_b", Type: "function", Function: ChatFunctionCall{Name: "lookup", Arguments: `{"q":"上海"}`}},
			}},
		}},
	}

	out := ChatCompletionsResponseToResponses(&resp, resp.Model, nil, nil, false, nil)
	require.Equal(t, "chatcmpl_glm_parallel", out.ID)
	require.Equal(t, "completed", out.Status)
	require.Len(t, out.Output, 2)
	for i, callID := range []string{"call_a", "call_b"} {
		require.Equal(t, "function_call", out.Output[i].Type)
		require.NotEmpty(t, out.Output[i].ID, "Responses item_id must be generated")
		require.Equal(t, callID, out.Output[i].CallID)
	}
}

func TestCodexThirdPartyResponsesStreamContract(t *testing.T) {
	state := NewChatCompletionsToResponsesStreamState("deepseek-reasoner")
	chunks := []ChatCompletionsChunk{
		{ID: "chatcmpl_stream", Model: "deepseek-reasoner", Choices: []ChatChunkChoice{{Delta: ChatDelta{Role: "assistant"}}}},
		{ID: "chatcmpl_stream", Choices: []ChatChunkChoice{
			{Delta: ChatDelta{ToolCalls: []ChatToolCall{
				{Index: intPtr(0), ID: "call_exec", Type: "function", Function: ChatFunctionCall{Name: "unified_exec", Arguments: `{"command":"Get-`}},
			}}},
		}},
		{ID: "chatcmpl_stream", Choices: []ChatChunkChoice{
			{Delta: ChatDelta{ToolCalls: []ChatToolCall{
				{Index: intPtr(0), Function: ChatFunctionCall{Arguments: `ChildItem"}`}},
			}}},
		}},
		{ID: "chatcmpl_stream", Choices: []ChatChunkChoice{
			{Delta: ChatDelta{ToolCalls: []ChatToolCall{
				{Index: intPtr(1), ID: "call_lookup", Type: "function", Function: ChatFunctionCall{Name: "lookup", Arguments: `{"q":"深圳"}`}},
			}}},
		}},
		{ID: "chatcmpl_stream", Choices: []ChatChunkChoice{{FinishReason: stringPtr("tool_calls")}}},
	}

	var events []ResponsesStreamEvent
	for i := range chunks {
		events = append(events, ChatCompletionsChunkToResponsesEvents(&chunks[i], state)...)
	}
	events = append(events, FinalizeChatCompletionsResponsesStream(state)...)
	require.Nil(t, FinalizeChatCompletionsResponsesStream(state), "retry/finalize must not duplicate a completed response")

	var types []string
	seenSeq := map[int]bool{}
	for _, event := range events {
		types = append(types, event.Type)
		require.False(t, seenSeq[event.SequenceNumber], "duplicate sequence_number %d", event.SequenceNumber)
		seenSeq[event.SequenceNumber] = true
	}
	require.Equal(t, "response.created", types[0])
	require.Equal(t, "response.in_progress", types[1])
	require.Contains(t, types, "response.output_item.added")
	require.Contains(t, types, "response.function_call_arguments.delta")
	require.Contains(t, types, "response.function_call_arguments.done")
	require.Equal(t, "response.completed", types[len(types)-1])

	var completed *ResponsesStreamEvent
	for i := range events {
		if events[i].Type == "response.completed" {
			completed = &events[i]
		}
	}
	require.NotNil(t, completed)
	require.Equal(t, "chatcmpl_stream", completed.Response.ID)
	require.Len(t, completed.Response.Output, 2)
	require.Equal(t, []string{"call_exec", "call_lookup"}, []string{completed.Response.Output[0].CallID, completed.Response.Output[1].CallID})
	require.Equal(t, `{"command":"Get-ChildItem"}`, completed.Response.Output[0].Arguments)
	require.Contains(t, completed.Response.Output[1].Arguments, "深圳")
	require.False(t, strings.Contains(strings.Join(types, ","), "agent.completed"), "response.completed is not whole-agent completion")
}

func TestCodexThirdPartyAnthropicStreamStartsWithResponsesLifecycle(t *testing.T) {
	state := NewAnthropicEventToResponsesState()
	events := AnthropicEventToResponsesEvents(&AnthropicStreamEvent{
		Type: "message_start",
		Message: &AnthropicResponse{
			ID:    "msg_claude_1",
			Model: "claude-sonnet-4-6",
		},
	}, state)
	require.Len(t, events, 2)
	require.Equal(t, "response.created", events[0].Type)
	require.Equal(t, "response.in_progress", events[1].Type)
	require.Equal(t, "msg_claude_1", events[1].Response.ID)
	require.Equal(t, 0, events[0].SequenceNumber)
	require.Equal(t, 1, events[1].SequenceNumber)
}

func TestCodexThirdPartyAnthropicToolStreamContract(t *testing.T) {
	state := NewAnthropicEventToResponsesState()
	stream := []AnthropicStreamEvent{
		{
			Type: "message_start",
			Message: &AnthropicResponse{
				ID:    "msg_claude_tool",
				Model: "claude-sonnet-4-6",
			},
		},
		{
			Type: "content_block_start",
			ContentBlock: &AnthropicContentBlock{
				Type: "tool_use",
				ID:   "toolu_exec",
				Name: "unified_exec",
			},
		},
		{
			Type: "content_block_delta",
			Delta: &AnthropicDelta{
				Type:        "input_json_delta",
				PartialJSON: `{"command":"Get-ChildItem"}`,
			},
		},
		{Type: "content_block_stop"},
		{
			Type:  "message_delta",
			Delta: &AnthropicDelta{StopReason: "tool_use"},
		},
		{Type: "message_stop"},
	}

	var events []ResponsesStreamEvent
	for i := range stream {
		events = append(events, AnthropicEventToResponsesEvents(&stream[i], state)...)
	}

	gotTypes := make([]string, 0, len(events))
	for _, event := range events {
		gotTypes = append(gotTypes, event.Type)
	}
	require.Equal(t, []string{
		"response.created",
		"response.in_progress",
		"response.output_item.added",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.output_item.done",
		"response.completed",
	}, gotTypes)

	for i, event := range events {
		require.Equal(t, i, event.SequenceNumber, "Responses sequence_number must be contiguous")
	}
	require.Equal(t, "msg_claude_tool", events[len(events)-1].Response.ID)
	require.Equal(t, "toolu_exec", events[2].Item.CallID)
	require.Equal(t, "toolu_exec", events[3].CallID)
	require.Equal(t, "toolu_exec", events[4].CallID)
	require.Nil(t, FinalizeAnthropicResponsesStream(state), "finalization retry must not duplicate response.completed")
}
