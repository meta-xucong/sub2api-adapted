package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGatewayServiceForwardCompactUnknownCustomEndpointUsesChatFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"minimax-m3","input":[{"type":"compaction_trigger"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-compact-chat"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl_compact","object":"chat.completion","model":"minimax-m3","choices":[{"index":0,"message":{"role":"assistant","content":"<summary>portable summary</summary>"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`)),
	}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg)}
	account := &Account{
		ID: 42, Name: "third-party-openai-compatible", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Concurrency: 1, Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://yetoken.example/v1"},
		Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}, Status: StatusActive, Schedulable: true,
	}

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://yetoken.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "minimax-m3", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "compaction", gjson.Get(rec.Body.String(), "output.0.type").String())
	require.Equal(t, "portable summary", gjson.Get(rec.Body.String(), "output.0.summary.0.text").String())
	require.True(t, strings.HasPrefix(gjson.Get(rec.Body.String(), "output.0.encrypted_content").String(), responsesCompatCompactEnvelopePrefix))
	require.Equal(t, []string{"rid-compact-chat"}, result.UpstreamHeaders["x-request-id"])

	continuation := &apicompat.ResponsesRequest{
		PreviousResponseID: result.ResponseID,
		Input:              json.RawMessage(`[{"type":"message","role":"user","content":"next"}]`),
	}
	require.NoError(t, svc.prepareOpenAIResponsesCompatContinuation(continuation))
	continuationInput := string(continuation.Input)
	require.NotContains(t, continuationInput, "compaction_trigger")
	require.Contains(t, continuationInput, "portable summary")
	require.Contains(t, continuationInput, "next")
}

func TestOpenAIGatewayServiceForwardCompactChatFallbackPreservesStreamingWire(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"minimax-m3","stream":true,"input":[{"type":"compaction_trigger"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	MarkOpenAICompactClientStream(c)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl_compact_stream","object":"chat.completion","model":"minimax-m3","choices":[{"index":0,"message":{"role":"assistant","content":"stream summary"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)),
	}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg)}
	account := &Account{
		ID: 43, Name: "third-party-stream-compact", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Concurrency: 1, Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://yetoken.example/v1"},
		Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}, Status: StatusActive, Schedulable: true,
	}

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), "response.output_item.done")
	require.Contains(t, rec.Body.String(), "response.completed")
	require.Contains(t, rec.Body.String(), "stream summary")
}
