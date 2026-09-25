package service

import (
	"context"
	"encoding/json"
	"errors"
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

// Synthetic data only: these tests never call a model or a production cache.
type t0AuditStateCache struct {
	stubGatewayCache
	values  map[string][]byte
	readErr error
}

func (c *t0AuditStateCache) GetResponsesCompatState(_ context.Context, key string) ([]byte, error) {
	return append([]byte(nil), c.values[key]...), c.readErr
}
func (c *t0AuditStateCache) SetResponsesCompatState(_ context.Context, key string, value []byte, _ time.Duration) error {
	if c.values == nil {
		c.values = make(map[string][]byte)
	}
	c.values[key] = append([]byte(nil), value...)
	return nil
}
func (c *t0AuditStateCache) DeleteResponsesCompatState(_ context.Context, key string) error {
	delete(c.values, key)
	return nil
}

func t0AuditContext(path, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Writer.Header().Set("X-Request-Id", "t0-synthetic-request")
	return c, recorder
}

func t0AuditCompact(t *testing.T, provider, body string, marked bool) (*httptest.ResponseRecorder, *httpUpstreamRecorder, error) {
	t.Helper()
	c, recorder := t0AuditContext("/v1/responses/compact", body)
	if marked {
		MarkOpenAICompactClientStream(c)
	}
	cfg := &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{AllowInsecureHTTP: true}}}
	payload := `{"id":"chat_test","model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"summary"},"finish_reason":"stop"}]}`
	platform := PlatformOpenAI
	if provider == "anthropic" {
		platform = PlatformAnthropic
		payload = `{"id":"msg_test","type":"message","role":"assistant","model":"test-model","content":[{"type":"text","text":"summary"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(payload))}}
	account := &Account{ID: 9001, Platform: platform, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-key", "base_url": "http://upstream.example"}}
	var err error
	if provider == "anthropic" {
		svc := &GatewayService{cfg: cfg, httpUpstream: upstream}
		_, err = svc.forwardResponsesCompactViaAnthropicMessages(context.Background(), c, account, []byte(body))
	} else {
		svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
		_, err = svc.forwardResponsesCompactViaRawChatCompletions(context.Background(), c, account, []byte(body), "")
	}
	return recorder, upstream, err
}

func TestT0Audit_CompactExplicitStreamRejectedBeforeUpstream(t *testing.T) {
	for _, provider := range []string{"chat", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			rec, upstream, err := t0AuditCompact(t, provider, `{"model":"test-model","input":"hello","stream":true}`, false)
			require.Error(t, err)
			require.Equal(t, 400, rec.Code)
			require.Equal(t, "invalid_request_error", gjson.Get(rec.Body.String(), "error.type").String())
			require.Equal(t, "unsupported_parameter", gjson.Get(rec.Body.String(), "error.code").String())
			require.Equal(t, "stream", gjson.Get(rec.Body.String(), "error.param").String())
			require.Nil(t, upstream.lastReq, "unsupported mode must not incur an upstream request")
		})
	}
}

func TestT0Audit_CompactBodySignalPreservesSSE(t *testing.T) {
	for _, provider := range []string{"chat", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			rec, upstream, err := t0AuditCompact(t, provider, `{"model":"test-model","input":"hello"}`, true)
			require.NoError(t, err)
			require.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")
			require.Contains(t, rec.Body.String(), "event: response.output_item.done")
			require.Contains(t, rec.Body.String(), "event: response.completed")
			require.NotNil(t, upstream.lastReq)
			require.False(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
		})
	}
}

func TestT0Audit_ContinuationCacheFailureIsSanitized(t *testing.T) {
	body := `{"model":"test-model","previous_response_id":"resp_test","input":"continue"}`
	for _, route := range []string{"chat", "anthropic", "chat_compact", "anthropic_compact"} {
		t.Run(route, func(t *testing.T) {
			c, rec := t0AuditContext("/v1/responses", body)
			cache := &t0AuditStateCache{readErr: errors.New("dial redis://user:secret@private-cache:6379 failed")}
			chat := &OpenAIGatewayService{cache: cache}
			anthropic := &GatewayService{cache: cache}
			var err error
			switch route {
			case "chat":
				_, err = chat.forwardResponsesViaRawChatCompletions(context.Background(), c, nil, []byte(body))
			case "anthropic":
				_, err = anthropic.ForwardAsResponses(context.Background(), c, nil, []byte(body), nil)
			case "chat_compact":
				_, err = chat.forwardResponsesCompactViaRawChatCompletions(context.Background(), c, nil, []byte(body), "")
			case "anthropic_compact":
				_, err = anthropic.forwardResponsesCompactViaAnthropicMessages(context.Background(), c, nil, []byte(body))
			}
			require.Error(t, err)
			require.Equal(t, 503, rec.Code)
			require.Equal(t, "server_error", gjson.Get(rec.Body.String(), "error.type").String())
			require.Equal(t, "compat_session_unavailable", gjson.Get(rec.Body.String(), "error.code").String())
			require.NotContains(t, rec.Body.String(), "secret")
			require.NotContains(t, rec.Body.String(), "private-cache")
			require.Equal(t, "t0-synthetic-request", rec.Header().Get("X-Request-Id"))
		})
	}
}

func TestT0Audit_CacheResponseIDMustMatchLookup(t *testing.T) {
	c, _ := t0AuditContext("/v1/responses", "")
	cache := &t0AuditStateCache{values: map[string][]byte{responsesCompatSessionCacheKey(c, "resp_expected"): []byte(`{"response_id":"resp_other","history_input":[{"type":"message","role":"user","content":"foreign"}]}`)}}
	var local sync.Map
	state, err := loadResponsesCompatSession(context.Background(), c, cache, &local, "resp_expected")
	require.Error(t, err)
	require.Nil(t, state)
	_, cached := local.Load(responsesCompatSessionLocalKey(c, "resp_expected"))
	require.False(t, cached)
}

func TestT0Audit_ConflictingToolOutputRejectedWithoutMutation(t *testing.T) {
	c, _ := t0AuditContext("/v1/responses", "")
	state := responsesCompatSessionState{ResponseID: "resp_tool", HistoryInput: []json.RawMessage{
		json.RawMessage(`{"type":"function_call","call_id":"call_exec","name":"unified_exec","arguments":"{}"}`),
	}}
	var local sync.Map
	require.NoError(t, saveResponsesCompatSession(context.Background(), c, nil, &local, state, time.Hour))
	for _, input := range []string{
		`[{"type":"function_call_output","call_id":"call_exec","output":"ok"},{"type":"function_call_output","call_id":"call_exec","output":"failed"}]`,
		`[{"type":"function_call_output","call_id":"call_exec","output":"same","is_error":false},{"type":"function_call_output","call_id":"call_exec","output":"same","is_error":true}]`,
		`[{"type":"function_call_output","call_id":"call_unknown","output":"ok"}]`,
	} {
		req := &apicompat.ResponsesRequest{PreviousResponseID: "resp_tool", Input: json.RawMessage(input)}
		_, err := prepareResponsesCompatContinuation(context.Background(), c, nil, &local, req)
		require.Error(t, err)
		require.Equal(t, "resp_tool", req.PreviousResponseID)
		require.JSONEq(t, input, string(req.Input))
	}
}
