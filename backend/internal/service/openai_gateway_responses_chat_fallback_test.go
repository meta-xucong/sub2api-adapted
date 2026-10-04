//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type responsesCompatInjectedReadFailure struct{}

func (responsesCompatInjectedReadFailure) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type responsesCompatFailAfterWriter struct {
	gin.ResponseWriter
	successfulWrites int
	failAfter        int
	failed           bool
}

func (w *responsesCompatFailAfterWriter) Write(p []byte) (int, error) {
	if w.successfulWrites >= w.failAfter {
		w.failed = true
		return 0, io.ErrClosedPipe
	}
	w.successfulWrites++
	return w.ResponseWriter.Write(p)
}

func (w *responsesCompatFailAfterWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func assertResponsesFailedTerminal(t *testing.T, body, expectedCode string) {
	t.Helper()
	var responseID string
	sequence := int64(0)
	sawFailed := false
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		require.Equal(t, sequence, gjson.Get(payload, "sequence_number").Int())
		sequence++
		eventResponseID := gjson.Get(payload, "response.id").String()
		if eventResponseID != "" {
			if responseID == "" {
				responseID = eventResponseID
			} else {
				require.Equal(t, responseID, eventResponseID)
			}
		}
		switch gjson.Get(payload, "type").String() {
		case "response.created":
		case "response.completed":
			t.Fatal("incomplete upstream stream was reported as completed")
		case "response.failed":
			sawFailed = true
			require.NotEmpty(t, responseID)
			require.Equal(t, "failed", gjson.Get(payload, "response.status").String())
			require.Equal(t, expectedCode, gjson.Get(payload, "response.error.code").String())
		}
	}
	require.True(t, sawFailed, "stream must end with response.failed")
}

func TestResponsesChatFallbackFailsTruncatedOrUnterminatedUpstreamStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	chunk := "data: {\"id\":\"chatcmpl_partial\",\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n"
	for _, tc := range []struct {
		name string
		body io.Reader
	}{
		{name: "read_error", body: io.MultiReader(strings.NewReader(chunk), responsesCompatInjectedReadFailure{})},
		{name: "missing_done", body: strings.NewReader(chunk)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(tc.body)}
			svc := &OpenAIGatewayService{}

			_, err := svc.streamChatCompletionsAsResponses(c, resp, "test-model", nil, nil, false, nil, "test-model", "test-model", nil, nil, time.Now())
			require.Error(t, err)
			assertResponsesFailedTerminal(t, rec.Body.String(), "upstream_stream_error")
			_, marked := GetOpsStreamError(c)
			require.True(t, marked, "started stream failure must be recorded for operational error reporting")
		})
	}
}

func TestResponsesChatFallbackFailureBeforeFirstEventUsesJSONError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(responsesCompatInjectedReadFailure{})}

	_, err := (&OpenAIGatewayService{}).streamChatCompletionsAsResponses(c, resp, "test-model", nil, nil, false, nil, "test-model", "test-model", nil, nil, time.Now())
	require.Error(t, err)
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	require.Equal(t, "upstream_stream_error", gjson.Get(rec.Body.String(), "error.code").String())
	require.NotContains(t, rec.Body.String(), "event: response.")
	_, marked := GetOpsStreamError(c)
	require.False(t, marked, "failure before stream creation follows the existing JSON error path")
}

func TestResponsesChatFallbackClientDisconnectDoesNotAppendFailureTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Writer = &responsesCompatFailAfterWriter{ResponseWriter: c.Writer, failAfter: 4}
	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_disconnect","model":"test-model","choices":[{"index":0,"delta":{"content":"partial"}}]}`,
		``,
		`data: {"id":"chatcmpl_disconnect","model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(upstreamBody))}

	result, err := (&OpenAIGatewayService{}).streamChatCompletionsAsResponses(c, resp, "test-model", nil, nil, false, nil, "test-model", "test-model", nil, nil, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.responsesCompatResponse, "the adapter should drain the upstream stream after the client disconnects")
	require.True(t, c.Writer.(*responsesCompatFailAfterWriter).failed)
	require.NotContains(t, rec.Body.String(), "event: response.failed\n")
	require.NotContains(t, rec.Body.String(), "event: response.completed\n")
}

func TestForwardResponses_ForceChatCompletionsRoutesNonStreamingToChatCompletions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","input":"hello","stream":false,"service_tier":"priority"}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_resp_chat_json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"chatcmpl_json","object":"chat.completion","model":"gpt-5.4","service_tier":"default","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,"prompt_tokens_details":{"cached_tokens":1}}}`,
		)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	SetActualOpenAIUpstreamEndpoint(c, "/v1/responses")

	result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "http://upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "/v1/chat/completions", GetActualOpenAIUpstreamEndpoint(c))
	require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(upstream.lastReq.Context()))
	require.Equal(t, "hello", gjson.GetBytes(upstream.lastBody, "messages.0.content").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
	require.Equal(t, "response", gjson.Get(rec.Body.String(), "object").String())
	require.Equal(t, "ok", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, 1, result.Usage.CacheReadInputTokens)
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, "priority", *result.ServiceTier)
	require.Equal(t, "default", result.UpstreamResponseServiceTier)
	require.False(t, result.Stream)
}

// This is the P3-2 differential fixture: a Chat-only /v1/responses account must
// be able to continue a tool turn by expanding the preceding request/output
// history before adapting the new function_call_output to Chat Completions.
func TestForwardResponses_ChatFallbackReplaysPreviousResponseHistory(t *testing.T) {
	gin.SetMode(gin.TestMode)

	firstBody := []byte(`{"model":"glm-5","instructions":"保留 Skills instructions","input":[{"type":"message","role":"user","content":"执行 café 检查"}],"tools":[{"type":"function","name":"unified_exec","parameters":{"type":"object","properties":{}}}],"stream":false}`)
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
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	account := rawChatCompletionsTestAccount()
	account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: false}

	firstRecorder := httptest.NewRecorder()
	firstContext, _ := gin.CreateTestContext(firstRecorder)
	firstContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(firstBody))
	firstContext.Request.Header.Set("Content-Type", "application/json")
	firstResult, err := svc.Forward(context.Background(), firstContext, account, firstBody)
	require.NoError(t, err)
	require.NotNil(t, firstResult)
	require.Equal(t, "chatcmpl_first", gjson.Get(firstRecorder.Body.String(), "id").String())
	require.Contains(t, firstRecorder.Body.String(), "function_call")

	secondRecorder := httptest.NewRecorder()
	secondContext, _ := gin.CreateTestContext(secondRecorder)
	secondContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(secondBody))
	secondContext.Request.Header.Set("Content-Type", "application/json")
	secondResult, err := svc.Forward(context.Background(), secondContext, account, secondBody)
	require.NoError(t, err)
	require.NotNil(t, secondResult)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, "http://upstream.example/v1/chat/completions", upstream.requests[1].URL.String())

	replayed := upstream.bodies[1]
	require.Equal(t, "glm-5", gjson.GetBytes(replayed, "model").String())
	require.Equalf(t, int64(4), gjson.GetBytes(replayed, "messages.#").Int(), "second Chat request body: %s", replayed)
	require.Equal(t, "system", gjson.GetBytes(replayed, "messages.0.role").String())
	require.Equal(t, "保留 Skills instructions", gjson.GetBytes(replayed, "messages.0.content").String())
	require.Equal(t, "user", gjson.GetBytes(replayed, "messages.1.role").String())
	require.Contains(t, gjson.GetBytes(replayed, "messages.1.content").String(), "café")
	require.Equal(t, "assistant", gjson.GetBytes(replayed, "messages.2.role").String())
	require.Equal(t, "call_exec_1", gjson.GetBytes(replayed, "messages.2.tool_calls.0.id").String())
	require.Equal(t, "unified_exec", gjson.GetBytes(replayed, "messages.2.tool_calls.0.function.name").String())
	require.Equal(t, "tool", gjson.GetBytes(replayed, "messages.3.role").String())
	require.Equal(t, "call_exec_1", gjson.GetBytes(replayed, "messages.3.tool_call_id").String())
	require.Equal(t, "已完成", gjson.GetBytes(replayed, "messages.3.content").String())
	require.Equal(t, "function", gjson.GetBytes(replayed, "tools.0.type").String())
}

func TestForwardResponses_ChatFallbackCapturesStreamForContinuation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	firstBody := []byte(`{"model":"glm-5","instructions":"keep these instructions","input":"first turn","stream":true}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_chat_stream_first"}},
			Body: io.NopCloser(strings.NewReader(strings.Join([]string{
				`data: {"id":"chatcmpl_stream_first","object":"chat.completion.chunk","model":"glm-5","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
				"",
				`data: {"id":"chatcmpl_stream_first","object":"chat.completion.chunk","model":"glm-5","choices":[{"index":0,"delta":{"content":"stream answer"},"finish_reason":null}]}`,
				"",
				`data: {"id":"chatcmpl_stream_first","object":"chat.completion.chunk","model":"glm-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				"",
				`data: [DONE]`,
				"",
			}, "\n"))),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_chat_stream_second"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl_stream_second","object":"chat.completion","model":"glm-5","choices":[{"index":0,"message":{"role":"assistant","content":"second answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}`)),
		},
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	account := forceChatResponsesFallbackAccount()

	firstRecorder := httptest.NewRecorder()
	firstContext, _ := gin.CreateTestContext(firstRecorder)
	firstContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(firstBody))
	firstContext.Request.Header.Set("Content-Type", "application/json")
	firstResult, err := svc.Forward(context.Background(), firstContext, account, firstBody)
	require.NoError(t, err)
	require.NotNil(t, firstResult)
	require.NotNil(t, firstResult.responsesCompatResponse)
	require.Equal(t, "chatcmpl_stream_first", firstResult.responsesCompatResponse.ID)
	require.Contains(t, firstRecorder.Body.String(), "event: response.completed")

	secondBody := []byte(`{"model":"glm-5","previous_response_id":"chatcmpl_stream_first","input":"second turn","stream":false}`)
	secondRecorder := httptest.NewRecorder()
	secondContext, _ := gin.CreateTestContext(secondRecorder)
	secondContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(secondBody))
	secondContext.Request.Header.Set("Content-Type", "application/json")
	secondResult, err := svc.Forward(context.Background(), secondContext, account, secondBody)
	require.NoError(t, err)
	require.NotNil(t, secondResult)
	require.Len(t, upstream.bodies, 2)

	replayed := upstream.bodies[1]
	require.Equal(t, int64(4), gjson.GetBytes(replayed, "messages.#").Int(), "second Chat request body: %s", replayed)
	require.Equal(t, "keep these instructions", gjson.GetBytes(replayed, "messages.0.content").String())
	require.Equal(t, "first turn", gjson.GetBytes(replayed, "messages.1.content").String())
	require.Equal(t, "assistant", gjson.GetBytes(replayed, "messages.2.role").String())
	require.Equal(t, "stream answer", gjson.GetBytes(replayed, "messages.2.content").String())
	require.Equal(t, "second turn", gjson.GetBytes(replayed, "messages.3.content").String())
}

func TestForwardResponses_NativeContinuationUnavailableReplaysMatchingHistoryThroughChat(t *testing.T) {
	gin.SetMode(gin.TestMode)

	firstBody := []byte(`{"model":"gpt-5.4","instructions":"keep Skills instructions","input":[{"type":"message","role":"user","content":"check café"}],"tools":[{"type":"function","name":"unified_exec","parameters":{"type":"object","properties":{}}}],"stream":false}`)
	secondBody := []byte(`{"model":"gpt-5.4","previous_response_id":"resp_native_first","input":[{"type":"function_call_output","call_id":"call_exec_native","output":"finished"}],"stream":false}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_native_first"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_native_first","object":"response","model":"gpt-5.4","status":"completed","output":[{"id":"fc_item_native","type":"function_call","status":"completed","call_id":"call_exec_native","name":"unified_exec","arguments":"{\"command\":\"Get-Date\"}"}],"usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13}}`)),
		},
		{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_native_continuation_rejected"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"previous_response_id is not available for this user","type":"invalid_request_error"}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_native_chat_fallback"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl_native_fallback","object":"chat.completion","model":"gpt-5.4","choices":[{"index":0,"message":{"role":"assistant","content":"finished"},"finish_reason":"stop"}],"usage":{"prompt_tokens":14,"completion_tokens":2,"total_tokens":16}}`)),
		},
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	account := rawChatCompletionsTestAccount()
	account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: true}

	firstRecorder := httptest.NewRecorder()
	firstContext, _ := gin.CreateTestContext(firstRecorder)
	firstContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(firstBody))
	firstContext.Request.Header.Set("Content-Type", "application/json")
	firstResult, err := svc.Forward(context.Background(), firstContext, account, firstBody)
	require.NoError(t, err)
	require.NotNil(t, firstResult)
	require.Equal(t, "http://upstream.example/v1/responses", upstream.requests[0].URL.String())

	secondRecorder := httptest.NewRecorder()
	secondContext, _ := gin.CreateTestContext(secondRecorder)
	secondContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(secondBody))
	secondContext.Request.Header.Set("Content-Type", "application/json")
	secondResult, err := svc.Forward(context.Background(), secondContext, account, secondBody)
	require.NoError(t, err)
	require.NotNil(t, secondResult)
	require.Len(t, upstream.requests, 3)
	require.Equal(t, "http://upstream.example/v1/responses", upstream.requests[1].URL.String())
	require.Equal(t, "http://upstream.example/v1/chat/completions", upstream.requests[2].URL.String())

	replayed := upstream.bodies[2]
	require.Equal(t, "gpt-5.4", gjson.GetBytes(replayed, "model").String())
	require.Equal(t, int64(4), gjson.GetBytes(replayed, "messages.#").Int(), "fallback request body: %s", replayed)
	require.Equal(t, "system", gjson.GetBytes(replayed, "messages.0.role").String())
	require.Equal(t, "keep Skills instructions", gjson.GetBytes(replayed, "messages.0.content").String())
	require.Contains(t, gjson.GetBytes(replayed, "messages.1.content").String(), "café")
	require.Equal(t, "call_exec_native", gjson.GetBytes(replayed, "messages.2.tool_calls.0.id").String())
	require.Equal(t, "unified_exec", gjson.GetBytes(replayed, "messages.2.tool_calls.0.function.name").String())
	require.Equal(t, "tool", gjson.GetBytes(replayed, "messages.3.role").String())
	require.Equal(t, "call_exec_native", gjson.GetBytes(replayed, "messages.3.tool_call_id").String())
	require.Equal(t, "finished", gjson.GetBytes(replayed, "messages.3.content").String())
}

func TestForwardResponses_NativeStreamingResponseCapturesDoneItemForContinuation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	firstBody := []byte(`{"model":"gpt-5.4","instructions":"keep stream instructions","input":"run a command","tools":[{"type":"function","name":"exec","parameters":{"type":"object","properties":{}}}],"stream":true}`)
	secondBody := []byte(`{"model":"gpt-5.4","previous_response_id":"resp_native_stream","input":[{"type":"function_call_output","call_id":"call_stream","output":"done"}],"stream":false}`)
	streamBody := strings.Join([]string{
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"item_stream","type":"function_call","status":"completed","call_id":"call_stream","name":"exec","arguments":"{\"cmd\":\"Get-Date\"}"}}`,
		"",
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_native_stream","object":"response","model":"gpt-5.4","status":"completed","output":[],"usage":{"input_tokens":8,"output_tokens":2,"total_tokens":10}}}`,
		"",
	}, "\n")
	clientStreamBody := strings.Replace(streamBody, `"output":[]`, `"output":[{"id":"item_stream","type":"function_call","status":"completed","call_id":"call_stream","name":"exec","arguments":"{\"cmd\":\"Get-Date\"}"}]`, 1)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamBody))},
		{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"previous_response_id is not available for this user"}}`))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"chatcmpl_stream_fallback","object":"chat.completion","model":"gpt-5.4","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11}}`))},
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream, toolCorrector: NewCodexToolCorrector()}
	account := rawChatCompletionsTestAccount()
	account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: true}

	firstRecorder := httptest.NewRecorder()
	firstContext, _ := gin.CreateTestContext(firstRecorder)
	firstContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(firstBody))
	firstContext.Request.Header.Set("Content-Type", "application/json")
	firstResult, err := svc.Forward(context.Background(), firstContext, account, firstBody)
	require.NoError(t, err)
	require.NotNil(t, firstResult)
	require.NotNil(t, firstResult.responsesCompatResponse)
	require.Equal(t, "call_stream", firstResult.responsesCompatResponse.Output[0].CallID)
	require.Equal(t, clientStreamBody, firstRecorder.Body.String(), "session capture must not add mutations beyond the existing terminal-output normalization")

	secondRecorder := httptest.NewRecorder()
	secondContext, _ := gin.CreateTestContext(secondRecorder)
	secondContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(secondBody))
	secondContext.Request.Header.Set("Content-Type", "application/json")
	_, err = svc.Forward(context.Background(), secondContext, account, secondBody)
	require.NoError(t, err)
	require.Len(t, upstream.requests, 3)
	replayed := upstream.bodies[2]
	require.Equal(t, "keep stream instructions", gjson.GetBytes(replayed, "messages.0.content").String())
	require.Equal(t, "call_stream", gjson.GetBytes(replayed, "messages.2.tool_calls.0.id").String())
	require.Equal(t, "tool", gjson.GetBytes(replayed, "messages.3.role").String())
	require.Equal(t, "done", gjson.GetBytes(replayed, "messages.3.content").String())
}

func TestOpenAIStreamingResponseCapturesAccumulatorOutputForContinuation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), toolCorrector: NewCodexToolCorrector()}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	body := strings.Join([]string{
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"从流式增量重建的回答"}`,
		"",
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_accumulator","object":"response","model":"gpt-5.4","status":"completed","output":[],"usage":{"input_tokens":8,"output_tokens":2,"total_tokens":10}}}`,
		"",
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	result, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1}, time.Now(), "gpt-5.4", "gpt-5.4")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.responsesCompatResponse)
	require.Equal(t, "resp_accumulator", result.responsesCompatResponse.ID)
	require.Len(t, result.responsesCompatResponse.Output, 1)
	require.Equal(t, "message", result.responsesCompatResponse.Output[0].Type)
	require.Len(t, result.responsesCompatResponse.Output[0].Content, 1)
	require.Equal(t, "从流式增量重建的回答", result.responsesCompatResponse.Output[0].Content[0].Text)
	clientStreamBody := strings.Replace(body,
		`"output":[]`,
		`"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"从流式增量重建的回答"}]}]`,
		1,
	)
	require.Equal(t, clientStreamBody, recorder.Body.String(), "session capture must not mutate the downstream SSE beyond the existing terminal-output reconstruction")
}

func TestOpenAIStreamingResponseWithoutTerminalDoesNotCaptureContinuationState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), toolCorrector: NewCodexToolCorrector()}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"尚未完成的回答"}`,
			"",
		}, "\n"))),
	}
	result, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1}, time.Now(), "gpt-5.4", "gpt-5.4")
	require.ErrorContains(t, err, "missing terminal event")
	require.NotNil(t, result)
	require.Nil(t, result.responsesCompatResponse, "an incomplete stream must not create resumable session state")
	require.Contains(t, recorder.Body.String(), "尚未完成的回答")
	require.NotContains(t, recorder.Body.String(), "event: response.completed")
}

func TestResponsesCompatNativeHTTPSessionScopeIsNarrow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	account := rawChatCompletionsTestAccount()
	account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: true}
	require.True(t, shouldUseResponsesCompatSessionForNativeHTTP(ctx, account, OpenAIUpstreamTransportHTTPSSE))
	require.False(t, shouldUseResponsesCompatSessionForNativeHTTP(ctx, account, OpenAIUpstreamTransportResponsesWebsocketV2))
	ctx.Request.URL.Path = "/v1/responses/compact"
	require.False(t, shouldUseResponsesCompatSessionForNativeHTTP(ctx, account, OpenAIUpstreamTransportHTTPSSE))
	ctx.Request.URL.Path = "/v1/responses"
	MarkOpenAINativeCompactionV2(ctx)
	require.False(t, shouldUseResponsesCompatSessionForNativeHTTP(ctx, account, OpenAIUpstreamTransportHTTPSSE),
		"native Responses compaction v2 must stay on the official Responses path")
	ctx.Set(openAINativeCompactionV2Key, false)
	account.Platform = PlatformDeepseek
	require.False(t, shouldUseResponsesCompatSessionForNativeHTTP(ctx, account, OpenAIUpstreamTransportHTTPSSE))
	account.Platform = PlatformOpenAI
	account.Type = AccountTypeOAuth
	require.False(t, shouldUseResponsesCompatSessionForNativeHTTP(ctx, account, OpenAIUpstreamTransportHTTPSSE))
}

func TestForwardResponses_NativeContinuationFallbackRequiresMatchingHistoryAndKnownError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name          string
		seedHistory   bool
		nativeV2      bool
		upstreamError string
	}{
		{
			name:          "recognized error without local history",
			upstreamError: `{"error":{"message":"previous_response_id is not available for this user"}}`,
		},
		{
			name:          "unrelated error with local history",
			seedHistory:   true,
			upstreamError: `{"error":{"message":"invalid model parameter"}}`,
		},
		{
			name:          "native compaction v2 does not fallback with matching history",
			seedHistory:   true,
			nativeV2:      true,
			upstreamError: `{"error":{"message":"previous_response_id is not available for this user"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(tc.upstreamError)),
			}}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			account := rawChatCompletionsTestAccount()
			account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: true}
			body := []byte(`{"model":"gpt-5.4","previous_response_id":"resp_seeded","input":"continue","stream":false}`)
			if tc.nativeV2 {
				body = []byte(`{"model":"gpt-5.4","previous_response_id":"resp_seeded","input":[{"type":"compaction_trigger"}],"stream":true}`)
			}
			rec := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(rec)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			ctx.Request.Header.Set("Content-Type", "application/json")
			if tc.nativeV2 {
				MarkOpenAINativeCompactionV2(ctx)
			}
			if tc.seedHistory {
				seedReq := &apicompat.ResponsesRequest{Model: "gpt-5.4", Input: []byte(`"prior"`)}
				seedResp := &apicompat.ResponsesResponse{ID: "resp_seeded", Output: []apicompat.ResponsesOutput{{Type: "message", Role: "assistant", Content: []apicompat.ResponsesContentPart{{Type: "output_text", Text: "prior answer"}}}}}
				state, err := buildResponsesCompatSessionState(seedReq, seedResp)
				require.NoError(t, err)
				require.NoError(t, saveResponsesCompatSession(context.Background(), ctx, nil, &svc.responsesCompatSessions, state, time.Hour))
			}

			_, err := svc.Forward(context.Background(), ctx, account, body)
			require.Error(t, err)
			require.Len(t, upstream.requests, 1, "this condition must not issue a Chat Completions request")
			require.Equal(t, "http://upstream.example/v1/responses", upstream.requests[0].URL.String())
		})
	}
}

// Scenario: 第三方无推理模型不收到兼容档位。
func TestForwardResponses_ForceChatCompletionsOmitsNoneReasoningEffort(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"company-coding-model","input":"hello","reasoning":{"effort":"none"},"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"chatcmpl_none","object":"chat.completion","model":"company-coding-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "company-coding-model", gjson.GetBytes(upstream.lastBody, "model").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "reasoning_effort").Exists())
	require.Nil(t, result.ReasoningEffort)
}

func TestForwardResponses_PassthroughFlagWithUnsupportedResponsesUsesAccountMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
		path := path
		t.Run(path, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.4-channel","input":"hello","stream":false}`)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(
					`{"id":"chatcmpl_mapping","object":"chat.completion","model":"gpt-5.4-account","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
				)),
			}}
			svc := &OpenAIGatewayService{
				cfg:          rawChatCompletionsTestConfig(),
				httpUpstream: upstream,
			}
			account := rawChatCompletionsTestAccount()
			account.Credentials["model_mapping"] = map[string]any{
				"gpt-5.4-channel": "gpt-5.4-account",
			}
			account.Credentials["compact_model_mapping"] = map[string]any{
				"gpt-5.4-account": "gpt-5.4-compact",
			}
			account.Extra = map[string]any{
				"openai_passthrough":                     true,
				openai_compat.ExtraKeyResponsesSupported: false,
			}

			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, "http://upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
			require.Equal(t, "gpt-5.4-account", gjson.GetBytes(upstream.lastBody, "model").String())
		})
	}
}

func TestForwardResponses_ForceChatCompletionsRoutesStreamingToChatCompletions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"he"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"llo"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_resp_chat_stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "http://upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream_options.include_usage").Bool())
	require.Contains(t, rec.Body.String(), "event: response.output_text.delta")
	require.Contains(t, rec.Body.String(), `"delta":"he"`)
	require.Contains(t, rec.Body.String(), "event: response.completed")
	require.Contains(t, rec.Body.String(), `"input_tokens":4`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
	require.Equal(t, 4, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.True(t, result.Stream)
	require.NotNil(t, result.FirstTokenMs)
}

func TestForwardResponses_ChatFallbackRejectsInvalidToolArgumentsAtOutputLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"deepseek-v4-flash","input":"run the command","stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_length_tool","object":"chat.completion.chunk","model":"deepseek-v4-flash","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_length","type":"function","function":{"name":"exec_command","arguments":"{\"cmd\":\"ssh root@HOST"}}]},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_length_tool","object":"chat.completion.chunk","model":"deepseek-v4-flash","choices":[{"index":0,"delta":{},"finish_reason":"length"}],"usage":{"prompt_tokens":4,"completion_tokens":6492,"total_tokens":6496}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_length_tool"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
	require.ErrorContains(t, err, "invalid JSON")
	require.NotNil(t, result)
	require.Equal(t, 4, result.Usage.InputTokens)
	require.Equal(t, 6492, result.Usage.OutputTokens)
	require.NotContains(t, rec.Body.String(), "response.function_call_arguments.done")
	require.NotContains(t, rec.Body.String(), "response.output_item.done")
	require.NotContains(t, rec.Body.String(), "data: [DONE]")
	assertResponsesFailedTerminal(t, rec.Body.String(), "invalid_tool_arguments")
}

func TestForwardResponses_DeepSeekReasoningOnlyStreamProducesVisibleText(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"deepseek-reasoner","input":"hello","stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"role":"assistant","content":null,"reasoning_content":""},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"reasoning_content":"visible fallback"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_deepseek_reasoning_responses_stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Contains(t, rec.Body.String(), "event: response.output_text.delta")
	require.Contains(t, rec.Body.String(), `"delta":"visible fallback"`)
	require.Contains(t, rec.Body.String(), `"status":"incomplete"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

func TestForwardResponses_AutoSupportedAccountStillUsesResponsesEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","input":"hello","stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_resp_native"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_native","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}],"status":"completed"}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}`,
		)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := rawChatCompletionsTestAccount()
	account.Extra = map[string]any{
		openai_compat.ExtraKeyResponsesMode:      string(openai_compat.ResponsesSupportModeAuto),
		openai_compat.ExtraKeyResponsesSupported: true,
	}

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "http://upstream.example/v1/responses", upstream.lastReq.URL.String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "messages").Exists())
	require.Equal(t, "ok", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
}

func forceChatResponsesFallbackAccount() *Account {
	account := rawChatCompletionsTestAccount()
	account.Extra = map[string]any{
		openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions),
	}
	return account
}

// reasoningRecordingCache 记录 reasoning 缓存写入、并按需响应回查。
type reasoningRecordingCache struct {
	stubGatewayCache
	mu      sync.Mutex
	sets    map[string]string
	getResp map[string]string
}

func (c *reasoningRecordingCache) SetReasoningContent(_ context.Context, itemID string, content string, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sets == nil {
		c.sets = make(map[string]string)
	}
	c.sets[itemID] = content
	return nil
}

func (c *reasoningRecordingCache) GetReasoningContent(_ context.Context, itemID string) (string, error) {
	if v, ok := c.getResp[itemID]; ok {
		return v, nil
	}
	return "", ErrReasoningContentNotFound
}

func (c *reasoningRecordingCache) snapshotSets() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]string, len(c.sets))
	for k, v := range c.sets {
		out[k] = v
	}
	return out
}

// 流式响应里的 reasoning_content 应按 reasoning item id 写入缓存，供后续轮次
// 客户端不回传明文 summary 时回注（DeepSeek thinking mode 400 修复的写入侧）。
func TestForwardResponses_ChatFallbackCachesStreamedReasoning(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"deepseek-reasoner","input":"hello","stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_rc","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_rc","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"reasoning_content":"think "},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_rc","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"reasoning_content":"first"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_rc","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":"stop"}]}`,
		"",
		`data: {"id":"chatcmpl_rc","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_reasoning_cache_stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	cache := &reasoningRecordingCache{}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
		cache:        cache,
	}

	result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)

	sets := cache.snapshotSets()
	require.Len(t, sets, 1, "应恰好缓存一个 reasoning item")
	for itemID, content := range sets {
		require.NotEmpty(t, itemID)
		require.Equal(t, "think first", content)
	}
}

// 请求侧：encrypted-only reasoning item（无明文 summary）经缓存回查补回
// reasoning_content；带明文 summary 的 item 顺手回写缓存（自愈）。
func TestForwardResponses_ChatFallbackRestoresReasoningFromCache(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{
		"model":"deepseek-reasoner",
		"stream":false,
		"input":[
			{"type":"reasoning","id":"item_plain","summary":[{"type":"summary_text","text":"plain thinking"}]},
			{"type":"function_call","call_id":"call_0","name":"get_value","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_0","output":"ok"},
			{"type":"reasoning","id":"item_enc1","summary":[],"encrypted_content":"opaque"},
			{"type":"function_call","call_id":"call_1","name":"get_value","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_1","output":"ok"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"go on"}]}
		]
	}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_reasoning_cache_restore"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"chatcmpl_restore","object":"chat.completion","model":"deepseek-reasoner","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
		)),
	}}
	cache := &reasoningRecordingCache{
		getResp: map[string]string{"item_enc1": "cached thinking"},
	}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
		cache:        cache,
	}

	result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)

	// 明文 summary 的 assistant 工具调用消息：reasoning_content 来自 summary 本身。
	require.Equal(t, "plain thinking", gjson.GetBytes(upstream.lastBody, "messages.0.reasoning_content").String())
	require.Equal(t, "call_0", gjson.GetBytes(upstream.lastBody, "messages.0.tool_calls.0.id").String())
	require.Equal(t, "tool", gjson.GetBytes(upstream.lastBody, "messages.1.role").String())
	// encrypted-only 的 assistant 工具调用消息：reasoning_content 来自缓存回查。
	require.Equal(t, "cached thinking", gjson.GetBytes(upstream.lastBody, "messages.2.reasoning_content").String())
	require.Equal(t, "call_1", gjson.GetBytes(upstream.lastBody, "messages.2.tool_calls.0.id").String())
	require.Equal(t, "tool", gjson.GetBytes(upstream.lastBody, "messages.3.role").String())

	// 明文 summary 的 item 被回写进缓存（自愈）。
	require.Equal(t, "plain thinking", cache.snapshotSets()["item_plain"])
}
