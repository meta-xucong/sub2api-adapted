package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGatewayService_ForwardAsResponses_AnthropicCompactUsesPortableSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"claude-sonnet-4-6","input":[{"type":"compaction_trigger"},{"type":"message","role":"user","content":"整理这段会话"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_claude_compact"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"msg_claude_compact","type":"message","role":"assistant","content":[{"type":"text","text":"Claude 会话摘要"}],"model":"claude-sonnet-4-6","stop_reason":"end_turn","usage":{"input_tokens":7,"output_tokens":3}}`)),
	}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	svc := &GatewayService{cfg: cfg, httpUpstream: upstream}
	account := &Account{
		ID:       203,
		Name:     "claude-api-key",
		Platform: PlatformAnthropic,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "http://upstream.example",
		},
	}

	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, strings.HasPrefix(gjson.Get(recorder.Body.String(), "id").String(), "resp_"))
	require.Equal(t, "compaction", gjson.Get(recorder.Body.String(), "output.0.type").String())
	require.Equal(t, "Claude 会话摘要", gjson.Get(recorder.Body.String(), "output.0.summary.0.text").String())
	require.NotEmpty(t, gjson.Get(recorder.Body.String(), "output.0.encrypted_content").String())
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "http://upstream.example/v1/messages?beta=true", upstream.lastReq.URL.String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.Contains(t, string(upstream.lastBody), "Summarize the conversation")
}
