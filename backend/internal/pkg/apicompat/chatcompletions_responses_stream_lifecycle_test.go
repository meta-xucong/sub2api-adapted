package apicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func collectStreamEvents(t *testing.T, chunks []string) []ResponsesStreamEvent {
	t.Helper()
	state := NewChatCompletionsToResponsesStreamState("deepseek-v4-pro")
	var events []ResponsesStreamEvent
	for _, payload := range chunks {
		var chunk ChatCompletionsChunk
		require.NoError(t, json.Unmarshal([]byte(payload), &chunk))
		events = append(events, ChatCompletionsChunkToResponsesEvents(&chunk, state)...)
	}
	events = append(events, FinalizeChatCompletionsResponsesStream(state)...)
	return events
}

// TestStream_ReasoningOpensItemBeforeDelta guards the bug where a strict client
// (Codex) drops reasoning deltas that reference an item not yet opened.
func TestStream_ReasoningOpensItemBeforeDelta(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":null,"reasoning_content":""}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning_content":"think"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"hello"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
	})

	open := map[int]string{} // output_index -> item type
	for _, e := range events {
		switch e.Type {
		case "response.output_item.added":
			require.NotNil(t, e.Item)
			open[e.OutputIndex] = e.Item.Type
		case "response.reasoning_summary_text.delta":
			require.Equalf(t, "reasoning", open[e.OutputIndex], "reasoning delta before its item was opened")
		case "response.output_text.delta":
			require.Equalf(t, "message", open[e.OutputIndex], "text delta before its item was opened")
		}
	}
}

func TestStream_ReasoningOnlySynthesizesVisibleText(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":null,"reasoning_content":""}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning_content":"thinking before final"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
	})

	open := map[int]string{}
	var sawTextDelta, sawTextDone, sawMessageDone bool
	for _, e := range events {
		switch e.Type {
		case "response.output_item.added":
			require.NotNil(t, e.Item)
			open[e.OutputIndex] = e.Item.Type
		case "response.output_text.delta":
			sawTextDelta = true
			require.Equalf(t, "message", open[e.OutputIndex], "fallback text delta before its item was opened")
			require.Equal(t, "thinking before final", e.Delta)
		case "response.output_text.done":
			sawTextDone = true
			require.Equal(t, "thinking before final", e.Text)
		case "response.output_item.done":
			if e.Item != nil && e.Item.Type == "message" {
				sawMessageDone = true
				require.Equal(t, "thinking before final", e.Item.Content[0].Text)
			}
		case "response.completed":
			require.NotNil(t, e.Response)
			require.Equal(t, "incomplete", e.Response.Status)
			require.NotNil(t, e.Response.IncompleteDetails)
			require.Equal(t, "max_output_tokens", e.Response.IncompleteDetails.Reason)
			require.Len(t, e.Response.Output, 2)
			require.Equal(t, "reasoning", e.Response.Output[0].Type)
			require.Equal(t, "message", e.Response.Output[1].Type)
			require.Equal(t, "thinking before final", e.Response.Output[1].Content[0].Text)
		}
	}
	require.True(t, sawTextDelta, "reasoning-only stream must produce visible text delta")
	require.True(t, sawTextDone, "reasoning-only stream must close visible text part")
	require.True(t, sawMessageDone, "reasoning-only stream must close synthesized message item")
}

func TestStream_ReasoningOnlyBlankDoesNotSynthesizeVisibleText(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"reasoning_content":"   "}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	})

	for _, e := range events {
		require.NotEqual(t, "response.output_text.delta", e.Type)
		if e.Type == "response.completed" {
			require.NotNil(t, e.Response)
			require.Len(t, e.Response.Output, 2)
			require.Equal(t, "reasoning", e.Response.Output[0].Type)
			require.Equal(t, "message", e.Response.Output[1].Type)
			require.Equal(t, "", e.Response.Output[1].Content[0].Text)
		}
	}
}

func TestStream_ReasoningThenContentDoesNotDuplicateFallbackText(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"reasoning_content":"private plan"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"final answer"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	})

	var textDeltas []string
	for _, e := range events {
		switch e.Type {
		case "response.output_text.delta":
			textDeltas = append(textDeltas, e.Delta)
		case "response.completed":
			require.NotNil(t, e.Response)
			require.Len(t, e.Response.Output, 2)
			require.Equal(t, "private plan", e.Response.Output[0].Summary[0].Text)
			require.Equal(t, "final answer", e.Response.Output[1].Content[0].Text)
		}
	}
	require.Equal(t, []string{"final answer"}, textDeltas)
}

func TestStream_ReasoningThenToolCallDoesNotSynthesizeVisibleText(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"reasoning_content":"call a tool"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"exec","arguments":"{}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	})

	for _, e := range events {
		require.NotEqual(t, "response.output_text.delta", e.Type)
		if e.Type == "response.completed" {
			require.NotNil(t, e.Response)
			require.Len(t, e.Response.Output, 2)
			require.Equal(t, "reasoning", e.Response.Output[0].Type)
			require.Equal(t, "function_call", e.Response.Output[1].Type)
		}
	}
}

// TestStream_ToolCallLifecycleComplete guards that a tool call is fully closed
// (function_call_arguments.done + output_item.done with full arguments), which
// codex needs to execute the call.
func TestStream_ToolCallLifecycleComplete(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"plan"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"exec","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"cmd\":\"ls\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
	})

	var sawAdded, sawArgsDone, sawItemDone bool
	for _, e := range events {
		switch e.Type {
		case "response.output_item.added":
			if e.Item != nil && e.Item.Type == "function_call" {
				sawAdded = true
			}
		case "response.function_call_arguments.done":
			sawArgsDone = true
			require.Equal(t, `{"cmd":"ls"}`, e.Arguments)
		case "response.output_item.done":
			if e.Item != nil && e.Item.Type == "function_call" {
				sawItemDone = true
				require.Equal(t, `{"cmd":"ls"}`, e.Item.Arguments)
				require.Equal(t, "call_a", e.Item.CallID)
			}
		}
	}
	require.True(t, sawAdded, "function_call output_item.added missing")
	require.True(t, sawArgsDone, "function_call_arguments.done missing")
	require.True(t, sawItemDone, "function_call output_item.done missing")
}

func TestStream_CompletedOutputReusesStreamItemIDAcrossChatModels(t *testing.T) {
	for _, model := range []string{"glm-5.2", "deepseek-v4-pro", "gpt-5.6-sol"} {
		t.Run(model, func(t *testing.T) {
			state := NewChatCompletionsToResponsesStreamState(model)
			var events []ResponsesStreamEvent
			for _, payload := range []string{
				// GLM-style chunks can carry id, name, and arguments together.
				`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_glm","type":"function","function":{"name":"exec","arguments":"{\"cmd\":\"Get-Date\"}"}}]}}]}`,
				// A later argument chunk must not replace the established call ID.
				`{"id":"chatcmpl_later","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_changed","function":{"arguments":""}}]}}]}`,
				`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			} {
				var chunk ChatCompletionsChunk
				require.NoError(t, json.Unmarshal([]byte(payload), &chunk))
				events = append(events, ChatCompletionsChunkToResponsesEvents(&chunk, state)...)
			}
			events = append(events, FinalizeChatCompletionsResponsesStream(state)...)

			var addedID, doneID, completedID, createdResponseID, completedResponseID string
			for _, event := range events {
				switch event.Type {
				case "response.created":
					if event.Response != nil {
						createdResponseID = event.Response.ID
					}
				case "response.output_item.added":
					if event.Item != nil && event.Item.Type == "function_call" {
						addedID = event.Item.ID
					}
				case "response.output_item.done":
					if event.Item != nil && event.Item.Type == "function_call" {
						doneID = event.Item.ID
					}
				case "response.completed":
					require.NotNil(t, event.Response)
					completedResponseID = event.Response.ID
					for _, item := range event.Response.Output {
						if item.Type == "function_call" && item.CallID == "call_glm" {
							completedID = item.ID
						}
					}
				}
			}

			require.NotEmpty(t, addedID)
			require.Equal(t, addedID, doneID)
			require.Equal(t, addedID, completedID)
			require.NotEmpty(t, createdResponseID)
			require.Equal(t, createdResponseID, completedResponseID)
		})
	}
}

func TestStream_CompletedOutputReusesStreamItemIDForSparseToolIndex(t *testing.T) {
	state := NewChatCompletionsToResponsesStreamState("glm-5.2")
	var events []ResponsesStreamEvent
	for _, payload := range []string{
		// Some Chat Completions providers use a non-zero/sparse tool-call index.
		// The bridge must key terminal output by the actual upstream index, not
		// by the number of calls currently stored in the state map.
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":7,"id":"call_sparse","type":"function","function":{"name":"exec","arguments":"{\"cmd\":\"Get-Date\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	} {
		var chunk ChatCompletionsChunk
		require.NoError(t, json.Unmarshal([]byte(payload), &chunk))
		events = append(events, ChatCompletionsChunkToResponsesEvents(&chunk, state)...)
	}
	events = append(events, FinalizeChatCompletionsResponsesStream(state)...)

	var addedID, doneID, completedID string
	for _, event := range events {
		switch event.Type {
		case "response.output_item.added":
			if event.Item != nil && event.Item.Type == "function_call" {
				addedID = event.Item.ID
			}
		case "response.output_item.done":
			if event.Item != nil && event.Item.Type == "function_call" {
				doneID = event.Item.ID
			}
		case "response.completed":
			require.NotNil(t, event.Response)
			for _, item := range event.Response.Output {
				if item.Type == "function_call" && item.CallID == "call_sparse" {
					completedID = item.ID
				}
			}
		}
	}

	require.NotEmpty(t, addedID)
	require.Equal(t, addedID, doneID)
	require.Equal(t, addedID, completedID)
}

// TestStream_ToolCallArgumentsInFirstChunkNotDoubled guards the GLM/Zhipu shape
// where a single tool_call delta chunk carries id+name+arguments together.
// Earlier code copied the whole tool_call (including arguments) into state and
// then accumulated the same chunk's arguments again, producing a doubled,
// invalid JSON like {"cmd":"ls"}{"cmd":"ls"} that breaks Codex tool parsing
// ("trailing characters").
func TestStream_ToolCallArgumentsInFirstChunkNotDoubled(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"exec","arguments":"{\"cmd\":\"ls\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	})

	var argsDelta strings.Builder
	var sawArgsDone, sawItemDone bool
	for _, e := range events {
		switch e.Type {
		case "response.function_call_arguments.delta":
			_, _ = argsDelta.WriteString(e.Delta)
		case "response.function_call_arguments.done":
			sawArgsDone = true
			require.Equal(t, `{"cmd":"ls"}`, e.Arguments)
		case "response.output_item.done":
			if e.Item != nil && e.Item.Type == "function_call" {
				sawItemDone = true
				require.Equal(t, `{"cmd":"ls"}`, e.Item.Arguments)
			}
		}
	}
	require.True(t, sawArgsDone, "function_call_arguments.done missing")
	require.True(t, sawItemDone, "function_call output_item.done missing")
	// Accumulated deltas must equal the final arguments exactly (no duplication).
	require.Equal(t, `{"cmd":"ls"}`, argsDelta.String())
}

// TestStream_SSEWireComplete drives the full stream through SSE encoding and
// asserts the function_call events carry complete fields on the wire.
func TestStream_SSEWireComplete(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"plan"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"exec","arguments":"{}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	})

	var addedLine string
	for _, e := range events {
		sse, err := ResponsesEventToSSE(e)
		require.NoError(t, err)
		if e.Type == "response.output_item.added" && e.Item != nil && e.Item.Type == "function_call" {
			addedLine = sse
		}
	}
	require.NotEmpty(t, addedLine)
	// The function_call added event must carry arguments:"" on the wire.
	require.True(t, strings.Contains(addedLine, `"arguments":""`), "added line missing arguments: %s", addedLine)
	require.Contains(t, addedLine, `"call_id":"call_a"`)
}
