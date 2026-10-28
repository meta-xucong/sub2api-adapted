package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type compactionRequestRecorder struct {
	request *http.Request
	body    []byte
	stream  string
}

func (r *compactionRequestRecorder) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	r.request = req
	r.body, _ = io.ReadAll(req.Body)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Request-Id": []string{"upstream-compaction"}},
		Body:       io.NopCloser(strings.NewReader(r.stream)),
	}, nil
}

func (r *compactionRequestRecorder) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return r.Do(req, proxyURL, accountID, concurrency)
}

func TestOpenAIGatewayNativeAnthropicCompactionRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &compactionRequestRecorder{stream: strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"msg_compact","usage":{"input_tokens":0,"output_tokens":0}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"compaction","content":"summary","signature":"signed-state"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"compaction"},"usage":{"input_tokens":0,"output_tokens":0,"iterations":[{"type":"compaction","input_tokens":144,"output_tokens":276}]}}`,
		`data: {"type":"message_stop"}`,
	}, "\n\n")}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:       17,
		Platform: PlatformZhipu,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_protocol": APIProtocolAnthropic,
			"api_key":      "test-key",
			"base_url":     "https://upstream.example",
		},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("anthropic-beta", "existing-beta")
	body := []byte(`{"model":"claude-sonnet-5-5","stream":true,"input":[{"type":"message","role":"user","content":"compact this"},{"type":"compaction_trigger"}]}`)

	result, err := svc.forwardResponsesViaNativeAnthropic(t.Context(), c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "claude-sonnet-5-5", result.Model)
	require.Equal(t, "claude-sonnet-5-5", result.BillingModel)
	require.Equal(t, "claude-sonnet-5-5", result.UpstreamModel)
	require.Equal(t, "/v1/messages", result.UpstreamEndpoint)
	require.True(t, result.Stream)
	require.Equal(t, 144, result.Usage.InputTokens)
	require.Equal(t, 276, result.Usage.OutputTokens)
	require.Equal(t, "https://upstream.example/v1/messages", upstream.request.URL.String())
	require.Equal(t, "existing-beta, compact-2026-09-04", getHeaderRaw(upstream.request.Header, "anthropic-beta"))
	betaHeaderCount := 0
	for key := range upstream.request.Header {
		if strings.EqualFold(key, "anthropic-beta") {
			betaHeaderCount++
		}
	}
	require.Equal(t, 1, betaHeaderCount)
	require.Equal(t, `{"type":"summarize"}`, gjson.GetBytes(upstream.body, "compaction").Raw)
	require.Equal(t, "user", gjson.GetBytes(upstream.body, "messages.0.role").String())
	require.NotContains(t, string(upstream.body), "compaction_trigger")

	var output []apicompat.ResponsesStreamEvent
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event apicompat.ResponsesStreamEvent
		require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
		output = append(output, event)
	}
	require.NotEmpty(t, output)
	require.Equal(t, "response.completed", output[len(output)-1].Type)
	require.Len(t, output[len(output)-1].Response.Output, 1)
	require.Equal(t, "compaction", output[len(output)-1].Response.Output[0].Type)
}
