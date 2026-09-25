package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCloneResponsesCompatRequest_CopiesRawInputBeforeProtocolAdaptation(t *testing.T) {
	original := apicompat.ResponsesRequest{
		Input:      json.RawMessage(`[{"type":"message","role":"user","content":"保留历史"}]`),
		ToolChoice: json.RawMessage(`"auto"`),
		Include:    []string{"reasoning.encrypted_content"},
	}
	clone := cloneResponsesCompatRequest(original)
	clone.Input = append(clone.Input[:0], []byte(`"adapted"`)...)
	clone.ToolChoice = append(clone.ToolChoice[:0], []byte(`"required"`)...)
	clone.Include[0] = "mutated"

	require.JSONEq(t, `[{"type":"message","role":"user","content":"保留历史"}]`, string(original.Input))
	require.JSONEq(t, `"auto"`, string(original.ToolChoice))
	require.Equal(t, "reasoning.encrypted_content", original.Include[0])
}

func TestResponsesCompatSession_ReplaysOutputAndDeduplicatesToolResult(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ctx, _ := gin.CreateTestContext(nil)
	first := &apicompat.ResponsesRequest{
		Model:        "glm-5",
		Instructions: "保留 Skills instructions",
		Input:        json.RawMessage(`[{"type":"message","role":"user","content":"执行检查"}]`),
	}
	response := &apicompat.ResponsesResponse{
		ID: "resp_compat_001",
		Output: []apicompat.ResponsesOutput{
			{Type: "reasoning", Summary: []apicompat.ResponsesSummary{{Type: "summary_text", Text: "先检查"}}},
			{Type: "function_call", ID: "item_call_1", CallID: "call_exec_1", Name: "unified_exec", Arguments: `{"command":"Get-Date"}`},
		},
	}

	state, err := buildResponsesCompatSessionState(first, response)
	require.NoError(t, err)
	var local sync.Map
	require.NoError(t, saveResponsesCompatSession(context.Background(), ctx, nil, &local, state, time.Hour))

	next := &apicompat.ResponsesRequest{
		Model:              "glm-5",
		PreviousResponseID: "resp_compat_001",
		Input:              json.RawMessage(`[{"type":"function_call_output","call_id":"call_exec_1","output":"2026-09-24"}]`),
	}
	require.NoError(t, func() error {
		_, err := prepareResponsesCompatContinuation(context.Background(), ctx, nil, &local, next)
		return err
	}())
	require.Empty(t, next.PreviousResponseID)
	require.Equal(t, "保留 Skills instructions", next.Instructions)
	require.Equal(t, int64(4), gjson.GetBytes(next.Input, "#").Int())
	require.Equal(t, "reasoning", gjson.GetBytes(next.Input, "1.type").String())
	require.Equal(t, "function_call", gjson.GetBytes(next.Input, "2.type").String())
	require.Equal(t, "function_call_output", gjson.GetBytes(next.Input, "3.type").String())

	// A client retry with the same call_id must not append a second tool result.
	retry := &apicompat.ResponsesRequest{
		PreviousResponseID: "resp_compat_001",
		Input:              json.RawMessage(`[{"type":"function_call_output","call_id":"call_exec_1","output":"2026-09-24"},{"type":"function_call_output","call_id":"call_exec_1","output":"2026-09-24"}]`),
	}
	require.NoError(t, func() error {
		_, err := prepareResponsesCompatContinuation(context.Background(), ctx, nil, &local, retry)
		return err
	}())
	require.Equal(t, int64(4), gjson.GetBytes(retry.Input, "#").Int())
	require.Equal(t, "function_call_output", gjson.GetBytes(retry.Input, "3.type").String())
}

func TestResponsesCompatSession_UnknownPreviousResponseIsStructuredAtAdapterBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	req := &apicompat.ResponsesRequest{
		PreviousResponseID: "resp_missing",
		Input:              json.RawMessage(`"继续"`),
	}
	var local sync.Map
	_, err := prepareResponsesCompatContinuation(context.Background(), ctx, nil, &local, req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not available")
}

func TestResponsesCompatSession_OutputItemsKeepCallAndItemIdentity(t *testing.T) {
	items, err := responsesCompatOutputItems([]apicompat.ResponsesOutput{
		{Type: "function_call", ID: "item_1", CallID: "call_1", Name: "工具", Arguments: `{"x":1}`},
		{Type: "message", ID: "msg_1", Role: "assistant", Content: []apicompat.ResponsesContentPart{{Type: "output_text", Text: "完成"}}},
	})
	require.NoError(t, err)
	require.Equal(t, "item_1", gjson.GetBytes(items[0], "id").String())
	require.Equal(t, "call_1", gjson.GetBytes(items[0], "call_id").String())
	require.Equal(t, "msg_1", gjson.GetBytes(items[1], "id").String())
}

func TestResponsesCompatHTTPContinuation_ReplaysFullHistoryToChatUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	firstBody := []byte(`{"model":"glm-5","instructions":"保留 Skills instructions","input":[{"type":"message","role":"user","content":"执行检查"}],"tools":[{"type":"function","name":"unified_exec","parameters":{"type":"object","properties":{}}}],"stream":false}`)
	secondBody := []byte(`{"model":"glm-5","previous_response_id":"chatcmpl_first","input":[{"type":"function_call_output","call_id":"call_exec_1","output":"已完成"}],"stream":false}`)

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_first"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl_first","object":"chat.completion","model":"glm-5","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_exec_1","type":"function","function":{"name":"unified_exec","arguments":"{\"command\":\"Get-Date\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_second"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl_second","object":"chat.completion","model":"glm-5","choices":[{"index":0,"message":{"role":"assistant","content":"已完成"},"finish_reason":"stop"}],"usage":{"prompt_tokens":14,"completion_tokens":2,"total_tokens":16}}`)),
		},
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false, AllowInsecureHTTP: true}}},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:       101,
		Name:     "glm-chat-only",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "http://upstream.example",
		},
		Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false},
	}

	firstRecorder := httptest.NewRecorder()
	firstContext, _ := gin.CreateTestContext(firstRecorder)
	firstContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(firstBody))
	firstContext.Request.Header.Set("Content-Type", "application/json")
	firstResult, err := svc.forwardResponsesViaRawChatCompletions(context.Background(), firstContext, account, firstBody)
	require.NoError(t, err)
	require.NotNil(t, firstResult)
	require.Equal(t, "chatcmpl_first", firstResult.ResponseID)
	require.Contains(t, firstRecorder.Body.String(), "function_call")

	secondRecorder := httptest.NewRecorder()
	secondContext, _ := gin.CreateTestContext(secondRecorder)
	secondContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(secondBody))
	secondContext.Request.Header.Set("Content-Type", "application/json")
	secondResult, err := svc.forwardResponsesViaRawChatCompletions(context.Background(), secondContext, account, secondBody)
	require.NoError(t, err)
	require.NotNil(t, secondResult)
	require.Equal(t, "chatcmpl_second", secondResult.ResponseID)
	require.Len(t, upstream.bodies, 2)

	replayed := upstream.bodies[1]
	require.Equal(t, "glm-5", gjson.GetBytes(replayed, "model").String())
	require.Equal(t, int64(4), gjson.GetBytes(replayed, "messages.#").Int())
	require.Equal(t, "system", gjson.GetBytes(replayed, "messages.0.role").String())
	require.Equal(t, "保留 Skills instructions", gjson.GetBytes(replayed, "messages.0.content").String())
	require.Equal(t, "user", gjson.GetBytes(replayed, "messages.1.role").String())
	require.Equal(t, "assistant", gjson.GetBytes(replayed, "messages.2.role").String())
	require.Equal(t, "call_exec_1", gjson.GetBytes(replayed, "messages.2.tool_calls.0.id").String())
	require.Equal(t, "unified_exec", gjson.GetBytes(replayed, "messages.2.tool_calls.0.function.name").String())
	require.Equal(t, "tool", gjson.GetBytes(replayed, "messages.3.role").String())
	require.Equal(t, "call_exec_1", gjson.GetBytes(replayed, "messages.3.tool_call_id").String())
	require.Equal(t, "已完成", gjson.GetBytes(replayed, "messages.3.content").String())
	require.Equal(t, "function", gjson.GetBytes(replayed, "tools.0.type").String())
}

func TestResponsesCompatNativeResponsesFallback_ReplaysWhenUpstreamRejectsPreviousResponseID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_native_first"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_native_first","object":"response","created_at":1,"model":"glm-5.2","status":"completed","output":[{"type":"function_call","id":"fc_item_1","call_id":"call_exec_1","name":"unified_exec","arguments":"{\"command\":\"Get-Date\"}","status":"completed"}],"usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13}}`)),
		},
		{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"previous_response_id is not available for this user"}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_chat_replay"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl_replay","object":"chat.completion","model":"glm-5.2","choices":[{"index":0,"message":{"role":"assistant","content":"工具结果已处理"},"finish_reason":"stop"}],"usage":{"prompt_tokens":18,"completion_tokens":4,"total_tokens":22}}`)),
		},
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false, AllowInsecureHTTP: true}}},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:       701,
		Name:     "native-responses-without-stateful-continuation",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "http://upstream.example",
		},
		Extra: map[string]any{
			openai_compat.ExtraKeyResponsesMode:      string(openai_compat.ResponsesSupportModeAuto),
			openai_compat.ExtraKeyResponsesSupported: true,
		},
	}

	firstBody := []byte(`{"model":"glm-5.2","instructions":"保留 Skills instructions","input":[{"type":"message","role":"user","content":"执行检查"}],"tools":[{"type":"function","name":"unified_exec","parameters":{"type":"object","properties":{}}}],"stream":false}`)
	firstRecorder := httptest.NewRecorder()
	firstContext, _ := gin.CreateTestContext(firstRecorder)
	firstContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(firstBody))
	firstContext.Request.Header.Set("Content-Type", "application/json")
	firstResult, err := svc.Forward(context.Background(), firstContext, account, firstBody)
	require.NoError(t, err)
	require.NotNil(t, firstResult)
	require.NotNil(t, firstResult.responsesCompatResponse)
	require.Equal(t, "resp_native_first", firstResult.responsesCompatResponse.ID)

	secondBody := []byte(`{"model":"glm-5.2","previous_response_id":"resp_native_first","input":[{"type":"function_call_output","call_id":"call_exec_1","output":"执行成功"}],"stream":false}`)
	secondRecorder := httptest.NewRecorder()
	secondContext, _ := gin.CreateTestContext(secondRecorder)
	secondContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(secondBody))
	secondContext.Request.Header.Set("Content-Type", "application/json")
	secondResult, err := svc.Forward(context.Background(), secondContext, account, secondBody)
	require.NoError(t, err)
	require.NotNil(t, secondResult)
	require.Equal(t, "chatcmpl_replay", secondResult.ResponseID)
	require.Len(t, upstream.requests, 3)
	require.Equal(t, "/v1/responses", upstream.requests[0].URL.Path)
	require.Equal(t, "/v1/responses", upstream.requests[1].URL.Path)
	require.Equal(t, "/v1/chat/completions", upstream.requests[2].URL.Path)

	replayed := upstream.bodies[2]
	require.Equal(t, int64(4), gjson.GetBytes(replayed, "messages.#").Int())
	require.Equal(t, "system", gjson.GetBytes(replayed, "messages.0.role").String())
	require.Equal(t, "保留 Skills instructions", gjson.GetBytes(replayed, "messages.0.content").String())
	require.Equal(t, "user", gjson.GetBytes(replayed, "messages.1.role").String())
	require.Equal(t, "call_exec_1", gjson.GetBytes(replayed, "messages.2.tool_calls.0.id").String())
	require.Equal(t, "unified_exec", gjson.GetBytes(replayed, "messages.2.tool_calls.0.function.name").String())
	require.Equal(t, "tool", gjson.GetBytes(replayed, "messages.3.role").String())
	require.Equal(t, "call_exec_1", gjson.GetBytes(replayed, "messages.3.tool_call_id").String())
	require.Equal(t, "执行成功", gjson.GetBytes(replayed, "messages.3.content").String())
	require.Equal(t, "function", gjson.GetBytes(replayed, "tools.0.type").String())
}

func TestResponsesCompatNativeResponsesStreamingCapturesTerminalOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stream := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_native_stream","object":"response","created_at":1,"model":"glm-5.2","status":"in_progress","output":[]}}`,
		"",
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_stream_1","call_id":"call_stream_1","name":"unified_exec","arguments":"{\"command\":\"Get-Date\"}","status":"completed"}}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_native_stream","object":"response","created_at":1,"model":"glm-5.2","status":"completed","output":[{"type":"function_call","id":"fc_stream_1","call_id":"call_stream_1","name":"unified_exec","arguments":"{\"command\":\"Get-Date\"}","status":"completed"}],"usage":{"input_tokens":7,"output_tokens":2,"total_tokens":9}}}`,
		"",
	}, "\n")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"glm-5.2","stream":true}`))
	account := &Account{ID: 702, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Name: "native-stream"}
	svc := &OpenAIGatewayService{}
	result, err := svc.handleStreamingResponseWithReasoning(
		context.Background(),
		&http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(stream)),
		},
		c,
		account,
		time.Now(),
		"glm-5.2",
		"glm-5.2",
		"",
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "resp_native_stream", result.responseID)
	require.NotNil(t, result.responsesCompatResponse)
	require.Len(t, result.responsesCompatResponse.Output, 1)
	require.Equal(t, "function_call", result.responsesCompatResponse.Output[0].Type)
	require.Equal(t, "call_stream_1", result.responsesCompatResponse.Output[0].CallID)
}

func TestResponsesCompatHTTPContinuation_FiveRoundsDoesNotDuplicateToolCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_round_1"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"chat_round_1","object":"chat.completion","model":"glm-5.3","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_exec_5","type":"function","function":{"name":"unified_exec","arguments":"{\"command\":\"Get-Date\"}"}}]},"finish_reason":"tool_calls"}]}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_round_2"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"chat_round_2","object":"chat.completion","model":"glm-5.3","choices":[{"index":0,"message":{"role":"assistant","content":"工具结果已处理"},"finish_reason":"stop"}]}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_round_3"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"chat_round_3","object":"chat.completion","model":"glm-5.3","choices":[{"index":0,"message":{"role":"assistant","content":"第三轮完成"},"finish_reason":"stop"}]}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_round_4"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"chat_round_4","object":"chat.completion","model":"glm-5.3","choices":[{"index":0,"message":{"role":"assistant","content":"第四轮完成"},"finish_reason":"stop"}]}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_round_5"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"chat_round_5","object":"chat.completion","model":"glm-5.3","choices":[{"index":0,"message":{"role":"assistant","content":"第五轮完成"},"finish_reason":"stop"}]}`)),
		},
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false, AllowInsecureHTTP: true}}},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:       103,
		Name:     "glm-chat-only-five-rounds",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "http://upstream.example",
		},
		Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false},
	}

	requests := []string{
		`{"model":"glm-5.3","input":[{"type":"message","role":"user","content":"开始"}],"stream":false}`,
		`{"model":"glm-5.3","input":[{"type":"function_call_output","call_id":"call_exec_5","output":"执行成功"}],"stream":false}`,
		`{"model":"glm-5.3","input":[{"type":"message","role":"user","content":"继续第三轮"}],"stream":false}`,
		`{"model":"glm-5.3","input":[{"type":"message","role":"user","content":"继续第四轮"}],"stream":false}`,
		`{"model":"glm-5.3","input":[{"type":"message","role":"user","content":"继续第五轮"}],"stream":false}`,
	}
	previousID := ""
	responseIDs := make([]string, 0, len(requests))
	for index, requestBody := range requests {
		if previousID != "" {
			requestBody = strings.Replace(requestBody, `{"model":`, fmt.Sprintf(`{"previous_response_id":%q,"model":`, previousID), 1)
		}
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(requestBody))
		ctx.Request.Header.Set("Content-Type", "application/json")
		result, err := svc.Forward(context.Background(), ctx, account, []byte(requestBody))
		require.NoError(t, err, "round %d", index+1)
		require.NotNil(t, result, "round %d", index+1)
		require.NotEmpty(t, result.ResponseID, "round %d", index+1)
		responseIDs = append(responseIDs, result.ResponseID)
		previousID = result.ResponseID
	}

	require.Len(t, responseIDs, 5)
	require.Len(t, upstream.bodies, 5)
	require.Len(t, upstream.requests, 5)
	require.Equal(t, int64(3), gjson.GetBytes(upstream.bodies[1], "messages.#").Int())
	require.Equal(t, "assistant", gjson.GetBytes(upstream.bodies[1], "messages.1.role").String())
	require.Equal(t, "call_exec_5", gjson.GetBytes(upstream.bodies[1], "messages.1.tool_calls.0.id").String())
	require.Equal(t, "tool", gjson.GetBytes(upstream.bodies[1], "messages.2.role").String())
	require.Equal(t, "call_exec_5", gjson.GetBytes(upstream.bodies[1], "messages.2.tool_call_id").String())
	require.Equal(t, int64(5), gjson.GetBytes(upstream.bodies[2], "messages.#").Int())
	require.Equal(t, int64(7), gjson.GetBytes(upstream.bodies[3], "messages.#").Int())
	require.Equal(t, int64(9), gjson.GetBytes(upstream.bodies[4], "messages.#").Int())

	toolResultCount := 0
	for _, body := range upstream.bodies {
		messages := gjson.GetBytes(body, "messages").Array()
		for _, message := range messages {
			if message.Get("role").String() == "tool" && message.Get("tool_call_id").String() == "call_exec_5" {
				toolResultCount++
			}
		}
	}
	require.Equal(t, 4, toolResultCount, "the same tool result is replayed once per later round, never duplicated within a round")
}

func TestResponsesCompatCompactHTTPRoundTripReplaysPortableSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_compact"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl_compact","object":"chat.completion","model":"glm-5.3","choices":[{"index":0,"message":{"role":"assistant","content":"历史摘要"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_followup"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl_followup","object":"chat.completion","model":"glm-5.3","choices":[{"index":0,"message":{"role":"assistant","content":"继续完成"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":2,"total_tokens":14}}`)),
		},
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false, AllowInsecureHTTP: true}}},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:       102,
		Name:     "glm-chat-only-compact",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "http://upstream.example",
		},
		Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false},
	}

	firstBody := []byte(`{"model":"glm-5.3","input":[{"type":"compaction_trigger"},{"type":"message","role":"user","content":"原始对话"}]}`)
	firstRecorder := httptest.NewRecorder()
	firstContext, _ := gin.CreateTestContext(firstRecorder)
	firstContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(firstBody))
	firstContext.Request.Header.Set("Content-Type", "application/json")
	firstResult, err := svc.Forward(context.Background(), firstContext, account, firstBody)
	require.NoError(t, err)
	require.NotNil(t, firstResult)
	require.True(t, strings.HasPrefix(firstResult.ResponseID, "resp_"))
	require.Equal(t, "compaction", gjson.Get(firstRecorder.Body.String(), "output.0.type").String())

	secondBody := []byte(fmt.Sprintf(`{"model":"glm-5.3","previous_response_id":%q,"input":[{"type":"message","role":"user","content":"继续"}]}`, firstResult.ResponseID))
	secondRecorder := httptest.NewRecorder()
	secondContext, _ := gin.CreateTestContext(secondRecorder)
	secondContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(secondBody))
	secondContext.Request.Header.Set("Content-Type", "application/json")
	secondResult, err := svc.Forward(context.Background(), secondContext, account, secondBody)
	require.NoError(t, err)
	require.NotNil(t, secondResult)
	require.Len(t, upstream.bodies, 2)
	require.Contains(t, gjson.GetBytes(upstream.bodies[1], "messages.1.content").String(), "历史摘要")
	require.Equal(t, "继续", gjson.GetBytes(upstream.bodies[1], "messages.2.content").String())
}

func TestResponsesCompatSession_DeduplicatesCompactionItemOnRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	first := &apicompat.ResponsesRequest{
		Model: "glm-5.3",
		Input: json.RawMessage(`[{"type":"message","role":"user","content":"原始"}]`),
	}
	response := &apicompat.ResponsesResponse{
		ID: "resp_compaction_retry",
		Output: []apicompat.ResponsesOutput{{
			ID:               "cmp_retry",
			Type:             "compaction",
			Status:           "completed",
			EncryptedContent: responsesCompatCompactEnvelopePrefix + "opaque",
			Summary:          []apicompat.ResponsesSummary{{Type: "summary_text", Text: "摘要"}},
		}},
	}
	state, err := buildResponsesCompatSessionState(first, response)
	require.NoError(t, err)
	var local sync.Map
	require.NoError(t, saveResponsesCompatSession(context.Background(), ctx, nil, &local, state, time.Hour))

	retry := &apicompat.ResponsesRequest{
		PreviousResponseID: "resp_compaction_retry",
		Input:              json.RawMessage(`[{"type":"compaction","id":"cmp_retry","summary":[{"type":"summary_text","text":"摘要"}]},{"type":"message","role":"user","content":"继续"}]`),
	}
	require.NoError(t, func() error {
		_, err := prepareResponsesCompatContinuation(context.Background(), ctx, nil, &local, retry)
		return err
	}())
	require.Equal(t, int64(3), gjson.GetBytes(retry.Input, "#").Int())
	require.Equal(t, "message", gjson.GetBytes(retry.Input, "0.type").String())
	require.Equal(t, "compaction", gjson.GetBytes(retry.Input, "1.type").String())
	require.Equal(t, "继续", gjson.GetBytes(retry.Input, "2.content").String())
}

func TestResponsesCompatAnthropicStream_DrainsAfterClientDisconnect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Writer = &openAICompatFailingWriter{ResponseWriter: c.Writer, failAfter: 0}

	stream := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_disconnect","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6","stop_reason":"","usage":{"input_tokens":5}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_exec","name":"unified_exec","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"Get-Date\"}"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	svc := &GatewayService{}
	result, err := svc.handleResponsesStreamingResponse(
		&http.Response{
			Header: http.Header{"x-request-id": []string{"rid_disconnect"}},
			Body:   io.NopCloser(strings.NewReader(stream)),
		},
		c,
		"claude-sonnet-4-6",
		"claude-sonnet-4-6",
		nil,
		time.Now(),
		apicompat.ResponsesClientToolMapping{},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.NotNil(t, result.responsesCompatResponse)
	require.NotEqual(t, "msg_disconnect", result.responsesCompatResponse.ID)
	require.True(t, strings.HasPrefix(result.responsesCompatResponse.ID, "resp_"))
	require.Len(t, result.responsesCompatResponse.Output, 1)
	require.Equal(t, "function_call", result.responsesCompatResponse.Output[0].Type)
	require.Equal(t, "toolu_exec", result.responsesCompatResponse.Output[0].CallID)
}

func TestNormalizeResponsesCompatResponseID(t *testing.T) {
	respID := normalizeResponsesCompatResponseID("resp_existing")
	require.Equal(t, "resp_existing", respID)

	for _, upstreamID := range []string{"msg_claude_1", "message_claude_2", "item_claude_3"} {
		compatID := normalizeResponsesCompatResponseID(upstreamID)
		require.NotEqual(t, upstreamID, compatID)
		require.True(t, strings.HasPrefix(compatID, "resp_"))
	}

	state := apicompat.NewAnthropicEventToResponsesState()
	events := []apicompat.ResponsesStreamEvent{{Response: &apicompat.ResponsesResponse{}}}
	normalizeResponsesCompatStreamEvents(events, state)
	require.True(t, strings.HasPrefix(state.ResponseID, "resp_"))
	require.Equal(t, state.ResponseID, events[0].Response.ID)
}

func TestResponsesCompatSession_ResolvesItemReferenceToCallID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	first := &apicompat.ResponsesRequest{
		Model: "glm-5",
		Input: json.RawMessage(`[{"type":"message","role":"user","content":"执行检查"}]`),
	}
	response := &apicompat.ResponsesResponse{
		ID: "resp_item_reference",
		Output: []apicompat.ResponsesOutput{{
			Type:      "function_call",
			ID:        "item_exec",
			CallID:    "call_exec",
			Name:      "unified_exec",
			Arguments: `{"command":"Get-Date"}`,
		}},
	}
	state, err := buildResponsesCompatSessionState(first, response)
	require.NoError(t, err)
	var local sync.Map
	require.NoError(t, saveResponsesCompatSession(context.Background(), ctx, nil, &local, state, time.Hour))

	next := &apicompat.ResponsesRequest{
		PreviousResponseID: "resp_item_reference",
		Input:              json.RawMessage(`[{"type":"function_call_output","item_reference":"item_exec","output":"完成"}]`),
	}
	require.NoError(t, func() error {
		_, err := prepareResponsesCompatContinuation(context.Background(), ctx, nil, &local, next)
		return err
	}())
	require.Equal(t, "call_exec", gjson.GetBytes(next.Input, "2.call_id").String())
	require.Equal(t, "item_exec", gjson.GetBytes(next.Input, "2.item_reference").String())
}

func TestResponsesCompatSession_ResolvesItemIDAndToolUseIDToCallID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	first := &apicompat.ResponsesRequest{
		Model: "claude-sonnet-4-6",
		Input: json.RawMessage(`[{"type":"message","role":"user","content":"执行检查"}]`),
	}
	response := &apicompat.ResponsesResponse{
		ID: "resp_item_aliases",
		Output: []apicompat.ResponsesOutput{{
			Type:      "function_call",
			ID:        "item_exec",
			CallID:    "toolu_exec",
			Name:      "unified_exec",
			Arguments: `{"command":"Get-Date"}`,
		}},
	}
	state, err := buildResponsesCompatSessionState(first, response)
	require.NoError(t, err)
	var local sync.Map
	require.NoError(t, saveResponsesCompatSession(context.Background(), ctx, nil, &local, state, time.Hour))

	for _, field := range []string{"item_id", "tool_use_id"} {
		next := &apicompat.ResponsesRequest{
			PreviousResponseID: "resp_item_aliases",
			Input:              json.RawMessage(fmt.Sprintf(`[{"type":"function_call_output","%s":"%s","output":"完成"}]`, field, map[string]string{"item_id": "item_exec", "tool_use_id": "toolu_exec"}[field])),
		}
		require.NoError(t, func() error {
			_, err := prepareResponsesCompatContinuation(context.Background(), ctx, nil, &local, next)
			return err
		}())
		require.Equal(t, "toolu_exec", gjson.GetBytes(next.Input, "2.call_id").String())
	}
}
