package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesCompatibilityHistoryAppliesToCustomResponsesAPIKeys(t *testing.T) {
	tests := []struct {
		name    string
		account *Account
		want    bool
	}{
		{
			name: "custom endpoint with explicit responses support is stateless-compatible",
			account: &Account{
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": "https://gateway.example/v1"},
				Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: true},
			},
			want: true,
		},
		{
			name: "custom endpoint explicitly marked chat-only is not stateless responses",
			account: &Account{
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": "https://gateway.example/v1"},
				Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: false},
			},
		},
		{
			name: "official OpenAI stateful responses is excluded",
			account: &Account{
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": "https://api.openai.com/v1"},
				Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: true},
			},
		},
		{
			name: "websocket v2 stateful route is excluded",
			account: &Account{
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": "https://gateway.example/v1"},
				Extra: map[string]any{
					openai_compat.ExtraKeyResponsesSupported:        true,
					"openai_apikey_responses_websockets_v2_enabled": true,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, accountUsesStatelessOpenAIResponsesHistory(tt.account))
		})
	}
}

func TestOpenAIGatewayPublicResponsesNormalizesIDAndRemembersHistory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &OpenAIGatewayService{}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	upstreamBody := `{"id":"chatcmpl_upstream","object":"response","status":"completed","model":"glm-5.2","output":[{"id":"fc_1","type":"function_call","call_id":"call_1","name":"echo_tool","arguments":"{}"}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}`
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}
	input := []byte(`[ {"type":"message","role":"user","content":"call echo_tool"} ]`)
	result, err := service.handleNonStreamingResponse(context.Background(), response, ctx, &Account{ID: 4}, "glm-5.2", "glm-5.2", input)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, strings.HasPrefix(result.responseID, "resp_"), result.responseID)
	require.Contains(t, recorder.Body.String(), result.responseID)

	continuation := &apicompat.ResponsesRequest{
		PreviousResponseID: result.responseID,
		Input:              []byte(`[{"type":"function_call_output","call_id":"call_1","output":"done"}]`),
	}
	require.NoError(t, service.prepareOpenAIResponsesCompatContinuation(continuation))
	require.Empty(t, continuation.PreviousResponseID)
	require.Contains(t, string(continuation.Input), "call_1")
	require.Contains(t, string(continuation.Input), "function_call")
}
