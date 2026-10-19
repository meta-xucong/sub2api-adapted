package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChatCompletionsResponsesFinishReasonMatrix(t *testing.T) {
	for _, tc := range []struct {
		finish, status, incompleteReason string
	}{
		{"stop", "completed", ""},
		{"length", "incomplete", "max_output_tokens"},
		{"content_filter", "incomplete", "content_filter"},
		{"tool_calls", "completed", ""},
		{"unknown", "completed", ""},
		{"", "completed", ""},
	} {
		for _, text := range []string{"", "partial text"} {
			t.Run(tc.finish+"/"+text, func(t *testing.T) {
				content, err := json.Marshal(text)
				require.NoError(t, err)
				resp := &ChatCompletionsResponse{
					ID: "chat_finish", Model: "gpt-5.4", Created: 123,
					Choices: []ChatChoice{{Message: ChatMessage{Role: "assistant", Content: content}, FinishReason: tc.finish}},
					Usage:   &ChatUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
				}
				buffered := ChatCompletionsResponseToResponses(resp, "gpt-5.4", nil, nil, false, nil)
				assertResponsesFinishReason(t, buffered, tc.status, tc.incompleteReason)
				require.Equal(t, 10, buffered.Usage.InputTokens)
				require.Equal(t, 5, buffered.Usage.OutputTokens)
				require.Equal(t, text, buffered.Output[0].Content[0].Text)

				state := NewChatCompletionsToResponsesStreamState("gpt-5.4")
				var chunk ChatCompletionsChunk
				payload, err := json.Marshal(map[string]any{"id": "chat_finish", "choices": []any{
					map[string]any{"index": 0, "delta": map[string]any{"content": text}, "finish_reason": tc.finish},
				}})
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(payload, &chunk))
				events := ChatCompletionsChunkToResponsesEvents(&chunk, state)
				// Repeated finish metadata and a later usage-only frame must not
				// erase the terminal reason or require another choice to finish.
				chunk = ChatCompletionsChunk{}
				require.NoError(t, json.Unmarshal([]byte(`{"choices":[{"index":0,"delta":{},"finish_reason":`+string(mustFinishJSON(t, tc.finish))+`}]}`), &chunk))
				events = append(events, ChatCompletionsChunkToResponsesEvents(&chunk, state)...)
				chunk = ChatCompletionsChunk{Usage: resp.Usage}
				events = append(events, ChatCompletionsChunkToResponsesEvents(&chunk, state)...)
				events = append(events, FinalizeChatCompletionsResponsesStream(state)...)
				terminalCount := 0
				for _, event := range events {
					if event.Type != "response.completed" && event.Type != "response.incomplete" {
						continue
					}
					terminalCount++
					require.Equal(t, "response."+tc.status, event.Type)
					assertResponsesFinishReason(t, event.Response, tc.status, tc.incompleteReason)
					require.Equal(t, text, state.Text.String())
					require.Equal(t, 10, event.Response.Usage.InputTokens)
					require.Equal(t, 5, event.Response.Usage.OutputTokens)
				}
				require.Equal(t, 1, terminalCount)
				require.Empty(t, FinalizeChatCompletionsResponsesStream(state))
			})
		}
	}
}

func TestChatCompletionsResponsesFinishReasonPreservesChoicePolicy(t *testing.T) {
	for _, finishes := range [][]string{{"content_filter", "stop"}, {"stop", "content_filter"}} {
		t.Run(finishes[0]+"/"+finishes[1], func(t *testing.T) {
			// Buffered conversion has always selected the first choice, even
			// when its index is unusual. Streaming keeps the last observed finish.
			resp := &ChatCompletionsResponse{Choices: []ChatChoice{
				{Index: -1, FinishReason: finishes[0]},
				{Index: 7, FinishReason: finishes[1]},
			}}
			out := ChatCompletionsResponseToResponses(resp, "gpt-5.4", nil, nil, false, nil)
			wantStatus, wantReason := "completed", ""
			if finishes[0] == "content_filter" {
				wantStatus, wantReason = "incomplete", "content_filter"
			}
			assertResponsesFinishReason(t, out, wantStatus, wantReason)
			state := NewChatCompletionsToResponsesStreamState("gpt-5.4")
			var chunk ChatCompletionsChunk
			payload := `{"choices":[{"index":-1,"delta":{},"finish_reason":` + string(mustFinishJSON(t, finishes[0])) + `},{"index":7,"delta":{},"finish_reason":` + string(mustFinishJSON(t, finishes[1])) + `}]}`
			require.NoError(t, json.Unmarshal([]byte(payload), &chunk))
			ChatCompletionsChunkToResponsesEvents(&chunk, state)
			require.Equal(t, finishes[1], state.FinishReason)
			events := FinalizeChatCompletionsResponsesStream(state)
			wantStatus, wantReason = "completed", ""
			if finishes[1] == "content_filter" {
				wantStatus, wantReason = "incomplete", "content_filter"
			}
			assertResponsesFinishReason(t, events[len(events)-1].Response, wantStatus, wantReason)
		})
	}
}

func TestResponsesContentFilterRoundTrip(t *testing.T) {
	for _, text := range []string{"", "partial text"} {
		t.Run(text, func(t *testing.T) {
			response := &ResponsesResponse{
				ID: "resp_filter", Status: "incomplete", IncompleteDetails: &ResponsesIncompleteDetails{Reason: "content_filter"},
				Output: []ResponsesOutput{{Type: "message", Role: "assistant", Content: []ResponsesContentPart{{Type: "output_text", Text: text}}}},
				Usage:  &ResponsesUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
			}
			chat := ResponsesToChatCompletions(response, "gpt-5.4")
			require.Equal(t, "content_filter", chat.Choices[0].FinishReason)
			assertResponsesFinishReason(t, ChatCompletionsResponseToResponses(chat, "gpt-5.4", nil, nil, false, nil), "incomplete", "content_filter")
			state := NewResponsesEventToChatState()
			chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.incomplete", Response: response}, state)
			finishes := 0
			for _, chunk := range chunks {
				for _, choice := range chunk.Choices {
					if choice.FinishReason != nil {
						require.Equal(t, "content_filter", *choice.FinishReason)
						finishes++
					}
				}
			}
			require.Equal(t, 1, finishes)
		})
	}
}

func assertResponsesFinishReason(t *testing.T, response *ResponsesResponse, status, reason string) {
	t.Helper()
	require.NotNil(t, response)
	require.Equal(t, status, response.Status)
	if reason == "" {
		require.Nil(t, response.IncompleteDetails)
		return
	}
	require.NotNil(t, response.IncompleteDetails)
	require.Equal(t, reason, response.IncompleteDetails.Reason)
}

func mustFinishJSON(t *testing.T, value string) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}
