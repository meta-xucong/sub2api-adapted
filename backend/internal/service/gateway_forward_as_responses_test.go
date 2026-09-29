//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAdaptResponsesClientToolsForAnthropic_FlattensNamespace(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model":"claude-fable-5",
		"input":[{"type":"function_call","call_id":"call_1","namespace":"codex_app","name":"read_thread","arguments":"{}"}],
		"tools":[{"type":"namespace","name":"codex_app","tools":[{"type":"function","name":"read_thread","description":"Read a task","parameters":{"type":"object","properties":{}}}]}]
	}`)

	adapted, mapping, err := adaptResponsesClientToolsForAnthropic(body)
	require.NoError(t, err)
	require.Equal(t, apicompat.ResponsesNamespaceName{Namespace: "codex_app", Name: "read_thread"}, mapping.NamespaceTools["codex_app__read_thread"])

	var request map[string]any
	require.NoError(t, json.Unmarshal(adapted, &request))
	tools := request["tools"].([]any)
	require.Len(t, tools, 1)
	tool := tools[0].(map[string]any)
	require.Equal(t, "function", tool["type"])
	require.Equal(t, "codex_app__read_thread", tool["name"])

	input := request["input"].([]any)
	call := input[0].(map[string]any)
	require.Equal(t, "codex_app__read_thread", call["name"])
	require.NotContains(t, call, "namespace")
}

func TestAdaptResponsesClientToolsForAnthropic_LiftsAdditionalTools(t *testing.T) {
	body := []byte(`{
		"model":"claude-fable-5",
		"input":[
			{"type":"additional_tools","tools":[
				{"type":"custom","name":"exec","description":"Run a command"},
				{"type":"namespace","name":"codex_app","tools":[
					{"type":"function","name":"read_thread","parameters":{"type":"object"}}
				]}
			]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"inspect"}]}
		]
	}`)

	adapted, mapping, err := adaptResponsesClientToolsForAnthropic(body)
	require.NoError(t, err)
	require.True(t, mapping.CustomTools["exec"])
	require.Equal(t, apicompat.ResponsesNamespaceName{Namespace: "codex_app", Name: "read_thread"}, mapping.NamespaceTools["codex_app__read_thread"])

	var request map[string]any
	require.NoError(t, json.Unmarshal(adapted, &request))
	tools := request["tools"].([]any)
	require.Len(t, tools, 2)
	require.Equal(t, "function", tools[0].(map[string]any)["type"])
	require.Equal(t, "codex_app__read_thread", tools[1].(map[string]any)["name"])

	input := request["input"].([]any)
	require.Len(t, input, 1)
	require.Equal(t, "message", input[0].(map[string]any)["type"])
}

func namespaceToolAnthropicStream() string {
	return strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_namespace","type":"message","role":"assistant","content":[],"model":"claude-fable-5","stop_reason":"","usage":{"input_tokens":10}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_namespace","name":"codex_app__read_thread","input":{"thread_id":"123"}}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
}

func namespaceToolMapping() apicompat.ResponsesClientToolMapping {
	return apicompat.ResponsesClientToolMapping{NamespaceTools: map[string]apicompat.ResponsesNamespaceName{
		"codex_app__read_thread": {Namespace: "codex_app", Name: "read_thread"},
	}}
}

func TestHandleResponsesBufferedStreamingResponse_RestoresNamespaceTool(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(namespaceToolAnthropicStream()))}

	svc := &GatewayService{}
	_, err := svc.handleResponsesBufferedStreamingResponse(resp, c, "claude-fable-5", "claude-fable-5", nil, time.Now(), namespaceToolMapping())
	require.NoError(t, err)
	require.Contains(t, rec.Body.String(), `"type":"function_call"`)
	require.Contains(t, rec.Body.String(), `"name":"read_thread"`)
	require.Contains(t, rec.Body.String(), `"namespace":"codex_app"`)
	require.NotContains(t, rec.Body.String(), `"name":"codex_app__read_thread"`)
}

func TestHandleResponsesBufferedStreamingResponse_DoesNotPrefixToolArgumentsWithPlaceholder(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	stream := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_tool","type":"message","role":"assistant","content":[],"model":"claude-fable-5","stop_reason":"","usage":{"input_tokens":1}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"codex_usb_echo","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"token\":\"abc\"}"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":1}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(stream))}

	svc := &GatewayService{}
	_, err := svc.handleResponsesBufferedStreamingResponse(resp, c, "claude-fable-5", "claude-fable-5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
	require.NoError(t, err)

	var got apicompat.ResponsesResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Output, 1)
	require.Equal(t, "function_call", got.Output[0].Type)
	require.JSONEq(t, `{"token":"abc"}`, got.Output[0].Arguments)
}

func TestHandleResponsesStreamingResponse_RestoresNamespaceTool(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(namespaceToolAnthropicStream()))}

	svc := &GatewayService{}
	_, err := svc.handleResponsesStreamingResponse(resp, c, "claude-fable-5", "claude-fable-5", nil, time.Now(), namespaceToolMapping())
	require.NoError(t, err)
	require.Contains(t, rec.Body.String(), `response.output_item.added`)
	require.Contains(t, rec.Body.String(), `"name":"read_thread"`)
	require.Contains(t, rec.Body.String(), `"namespace":"codex_app"`)
	require.NotContains(t, rec.Body.String(), `"name":"codex_app__read_thread"`)
}

func TestForwardAsResponses_ClaudeThinkingSignatureSurvivesToolContinuation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	firstStream := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_claude_first","type":"message","role":"assistant","content":[],"model":"claude-fable-5","stop_reason":null,"usage":{"input_tokens":10}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"inspect"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"claude-signature-part-1"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"-part-2"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_exec_1","name":"unified_exec","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"Get-Date\"}"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":1}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":6}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	secondStream := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_claude_second","type":"message","role":"assistant","content":[],"model":"claude-fable-5","stop_reason":null,"usage":{"input_tokens":30}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"done"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(firstStream))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(secondStream))},
	}}
	sharedCache := &sharedResponsesCompatCache{states: make(map[string][]byte)}
	firstService := &GatewayService{
		cfg:                 &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false, AllowInsecureHTTP: true}}},
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
		cache:               sharedCache,
	}
	secondService := &GatewayService{
		cfg:                 firstService.cfg,
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
		cache:               sharedCache,
	}
	account := &Account{
		ID:       901,
		Name:     "claude-local-compat-fixture",
		Platform: PlatformAnthropic,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "http://upstream.example",
		},
	}

	firstBody := []byte(`{"model":"claude-fable-5","stream":true,"reasoning":{"effort":"high"},"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"inspect"}]}],"tools":[{"type":"function","name":"unified_exec","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}]}`)
	firstRecorder := httptest.NewRecorder()
	firstContext, _ := gin.CreateTestContext(firstRecorder)
	firstContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(firstBody))
	firstContext.Request.Header.Set("Content-Type", "application/json")
	firstResult, err := firstService.ForwardAsResponses(context.Background(), firstContext, account, firstBody, nil)
	require.NoError(t, err)
	require.NotNil(t, firstResult)
	require.NotNil(t, firstResult.responsesCompatResponse)
	firstResponseID := firstResult.responsesCompatResponse.ID
	require.True(t, strings.HasPrefix(firstResponseID, "resp_"))

	var reasoningAddedID, reasoningDoneID, completedReasoningSignature string
	var sequence []int
	for _, line := range strings.Split(firstRecorder.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") || strings.TrimSpace(strings.TrimPrefix(line, "data: ")) == "[DONE]" {
			continue
		}
		var event apicompat.ResponsesStreamEvent
		require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
		sequence = append(sequence, event.SequenceNumber)
		switch event.Type {
		case "response.output_item.added":
			if event.Item != nil && event.Item.Type == "reasoning" {
				reasoningAddedID = event.Item.ID
			}
		case "response.output_item.done":
			if event.Item != nil && event.Item.Type == "reasoning" {
				reasoningDoneID = event.Item.ID
				completedReasoningSignature = event.Item.EncryptedContent
			}
		case "response.completed":
			require.Len(t, event.Response.Output, 2)
			require.Equal(t, "reasoning", event.Response.Output[0].Type)
			require.Equal(t, "claude-signature-part-1-part-2", event.Response.Output[0].EncryptedContent)
			require.Equal(t, reasoningDoneID, event.Response.Output[0].ID)
		}
	}
	require.NotEmpty(t, reasoningAddedID)
	require.Equal(t, reasoningAddedID, reasoningDoneID)
	require.Equal(t, "claude-signature-part-1-part-2", completedReasoningSignature)
	for index, got := range sequence {
		require.Equal(t, index, got, "Responses sequence_number must be continuous")
	}

	secondBody := []byte(`{"model":"claude-fable-5","stream":false,"previous_response_id":"` + firstResponseID + `","input":[{"type":"function_call_output","call_id":"toolu_exec_1","output":"permission denied","is_error":true}]}`)
	secondRecorder := httptest.NewRecorder()
	secondContext, _ := gin.CreateTestContext(secondRecorder)
	secondContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(secondBody))
	secondContext.Request.Header.Set("Content-Type", "application/json")
	secondResult, err := secondService.ForwardAsResponses(context.Background(), secondContext, account, secondBody, nil)
	require.NoError(t, err)
	require.NotNil(t, secondResult)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, "user", gjson.GetBytes(upstream.bodies[1], "messages.0.role").String())
	require.Equal(t, "assistant", gjson.GetBytes(upstream.bodies[1], "messages.1.role").String())
	require.Equal(t, "thinking", gjson.GetBytes(upstream.bodies[1], "messages.1.content.0.type").String())
	require.Equal(t, "claude-signature-part-1-part-2", gjson.GetBytes(upstream.bodies[1], "messages.1.content.0.signature").String())
	require.Equal(t, "tool_use", gjson.GetBytes(upstream.bodies[1], "messages.1.content.1.type").String())
	require.Equal(t, "toolu_exec_1", gjson.GetBytes(upstream.bodies[1], "messages.1.content.1.id").String())
	require.Equal(t, "user", gjson.GetBytes(upstream.bodies[1], "messages.2.role").String())
	require.Equal(t, "tool_result", gjson.GetBytes(upstream.bodies[1], "messages.2.content.0.type").String())
	require.Equal(t, "toolu_exec_1", gjson.GetBytes(upstream.bodies[1], "messages.2.content.0.tool_use_id").String())
	require.True(t, gjson.GetBytes(upstream.bodies[1], "messages.2.content.0.is_error").Bool())
}

func TestExtractResponsesReasoningEffortFromBody(t *testing.T) {
	t.Parallel()

	got := ExtractResponsesReasoningEffortFromBody([]byte(`{"model":"claude-sonnet-4.5","reasoning":{"effort":"HIGH"}}`))
	require.NotNil(t, got)
	require.Equal(t, "high", *got)

	maxGot := ExtractResponsesReasoningEffortFromBody([]byte(`{"model":"deepseek-v4-pro","reasoning":{"effort":"max"}}`))
	require.NotNil(t, maxGot)
	require.Equal(t, "xhigh", *maxGot)

	require.Nil(t, ExtractResponsesReasoningEffortFromBody([]byte(`{"model":"claude-sonnet-4.5"}`)))
}

func TestHandleResponsesBufferedStreamingResponse_PreservesMessageStartCacheUsage(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_buffered"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":"","usage":{"input_tokens":12,"cache_read_input_tokens":9,"cache_creation_input_tokens":3}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleResponsesBufferedStreamingResponse(resp, c, "claude-sonnet-4.5", "claude-sonnet-4.5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Equal(t, 9, result.Usage.CacheReadInputTokens)
	require.Equal(t, 3, result.Usage.CacheCreationInputTokens)
	require.Contains(t, rec.Body.String(), `"cached_tokens":9`)
}

func TestHandleResponsesBufferedStreamingResponse_PreservesThinkingSignature(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_buffered_signature"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_signature","type":"message","role":"assistant","content":[],"model":"claude-fable-5","stop_reason":null,"usage":{"input_tokens":4}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"inspect"}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-buffered"}}`,
			``,
			`event: content_block_stop`,
			`data: {"type":"content_block_stop","index":0}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleResponsesBufferedStreamingResponse(resp, c, "claude-fable-5", "claude-fable-5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "sig-buffered", gjson.Get(rec.Body.String(), "output.0.encrypted_content").String())
}

func TestHandleResponsesBufferedStreamingResponse_FailsClosedWithoutMessageStop(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_buffered_missing_stop"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_missing_stop_buffered","type":"message","role":"assistant","content":[],"model":"claude-fable-5","stop_reason":null,"usage":{"input_tokens":1}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_partial_buffered","name":"unified_exec","input":{}}}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleResponsesBufferedStreamingResponse(resp, c, "claude-fable-5", "claude-fable-5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "message_stop")
	require.Contains(t, rec.Body.String(), `"code":"upstream_stream_incomplete"`)
	require.NotContains(t, rec.Body.String(), `"object":"response"`)
}

// sharedResponsesCompatCache models the shared-cache contract between two
// GatewayService instances. The repository package separately verifies the
// same contract against Redis; keeping this fixture in the service package
// lets the test exercise the complete Claude continuation path without an
// import cycle.
type sharedResponsesCompatCache struct {
	mu     sync.Mutex
	states map[string][]byte
}

func (c *sharedResponsesCompatCache) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, ErrStickySessionNotFound
}

func (*sharedResponsesCompatCache) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}

func (*sharedResponsesCompatCache) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

func (*sharedResponsesCompatCache) DeleteSessionAccountID(context.Context, int64, string) error {
	return nil
}

func (*sharedResponsesCompatCache) SetGrokVideoPendingBilling(context.Context, string, []byte, time.Duration) error {
	return nil
}

func (*sharedResponsesCompatCache) GetGrokVideoPendingBilling(context.Context, string) ([]byte, error) {
	return nil, nil
}

func (*sharedResponsesCompatCache) ClaimGrokVideoBilled(context.Context, string, time.Duration) (bool, error) {
	return false, nil
}

func (*sharedResponsesCompatCache) ReleaseGrokVideoBilled(context.Context, string) error {
	return nil
}

func (c *sharedResponsesCompatCache) GetResponsesCompatState(_ context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	payload := c.states[key]
	if payload == nil {
		return nil, nil
	}
	return append([]byte(nil), payload...), nil
}

func (c *sharedResponsesCompatCache) SetResponsesCompatState(_ context.Context, key string, payload []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.states[key] = append([]byte(nil), payload...)
	return nil
}

func (c *sharedResponsesCompatCache) DeleteResponsesCompatState(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.states, key)
	return nil
}

func TestHandleResponsesStreamingResponse_PreservesMessageStartCacheUsage(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":"","usage":{"input_tokens":20,"cache_read_input_tokens":11,"cache_creation_input_tokens":4}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":8}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleResponsesStreamingResponse(resp, c, "claude-sonnet-4.5", "claude-sonnet-4.5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 20, result.Usage.InputTokens)
	require.Equal(t, 8, result.Usage.OutputTokens)
	require.Equal(t, 11, result.Usage.CacheReadInputTokens)
	require.Equal(t, 4, result.Usage.CacheCreationInputTokens)
	require.Contains(t, rec.Body.String(), `response.completed`)
}

func TestParseAnthropicSSEField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		line      string
		field     string
		wantValue string
		wantOK    bool
	}{
		{
			name:      "standard format with space",
			line:      "event: message_start",
			field:     "event",
			wantValue: "message_start",
			wantOK:    true,
		},
		{
			name:      "compact format without space",
			line:      "event:message_start",
			field:     "event",
			wantValue: "message_start",
			wantOK:    true,
		},
		{
			name:      "data field with space",
			line:      "data: {\"type\":\"message_start\"}",
			field:     "data",
			wantValue: "{\"type\":\"message_start\"}",
			wantOK:    true,
		},
		{
			name:      "data field without space",
			line:      "data:{\"type\":\"message_start\"}",
			field:     "data",
			wantValue: "{\"type\":\"message_start\"}",
			wantOK:    true,
		},
		{
			name:      "field with multiple spaces after colon",
			line:      "event:  message_delta",
			field:     "event",
			wantValue: "message_delta",
			wantOK:    true,
		},
		{
			name:      "wrong field name",
			line:      "event: message_start",
			field:     "data",
			wantValue: "",
			wantOK:    false,
		},
		{
			name:      "empty line",
			line:      "",
			field:     "event",
			wantValue: "",
			wantOK:    false,
		},
		{
			name:      "line without colon",
			line:      "invalid line",
			field:     "event",
			wantValue: "",
			wantOK:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotValue, gotOK := parseAnthropicSSEField(tt.line, tt.field)
			require.Equal(t, tt.wantOK, gotOK, "parseAnthropicSSEField() ok")
			require.Equal(t, tt.wantValue, gotValue, "parseAnthropicSSEField() value")
		})
	}
}

func TestHandleResponsesBufferedStreamingResponse_CompactSSEFormat(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	// Simulate compact SSE format without spaces after colons (e.g. Kimi API)
	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_compact"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event:message_start`,
			`data:{"type":"message_start","message":{"id":"msg_compact","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":"","usage":{"input_tokens":10}}}`,
			``,
			`event:content_block_start`,
			`data:{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"OK"}}`,
			``,
			`event:message_delta`,
			`data:{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
			``,
			`event:message_stop`,
			`data:{"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleResponsesBufferedStreamingResponse(resp, c, "claude-sonnet-4.5", "claude-sonnet-4.5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 10, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
}

func TestHandleResponsesStreamingResponse_CompactSSEFormat(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	// Simulate compact SSE format without spaces after colons (e.g. Kimi API)
	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_compact_stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event:message_start`,
			`data:{"type":"message_start","message":{"id":"msg_compact_stream","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":"","usage":{"input_tokens":15}}}`,
			``,
			`event:content_block_start`,
			`data:{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"OK"}}`,
			``,
			`event:message_delta`,
			`data:{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":6}}`,
			``,
			`event:message_stop`,
			`data:{"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleResponsesStreamingResponse(resp, c, "claude-sonnet-4.5", "claude-sonnet-4.5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 15, result.Usage.InputTokens)
	require.Equal(t, 6, result.Usage.OutputTokens)
	require.Contains(t, rec.Body.String(), `response.completed`)
}

func TestHandleResponsesStreamingResponse_FailsClosedWithoutMessageStop(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_anthropic_missing_stop"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_missing_stop","type":"message","role":"assistant","content":[],"model":"claude-fable-5","stop_reason":"","usage":{"input_tokens":1}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_partial","name":"unified_exec","input":{}}}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleResponsesStreamingResponse(resp, c, "claude-fable-5", "claude-fable-5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
	require.Error(t, err)
	require.NotNil(t, result)
	require.Contains(t, err.Error(), "message_stop")
	require.Contains(t, rec.Body.String(), "response.failed")
	require.Contains(t, rec.Body.String(), `"code":"upstream_stream_incomplete"`)
	require.NotContains(t, rec.Body.String(), "response.completed")
}
