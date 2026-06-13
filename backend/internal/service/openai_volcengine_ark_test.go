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
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSanitizeVolcengineArkResponsesRequest(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			"provider": "volcengine_ark",
		},
	}
	req := &apicompat.ResponsesRequest{
		Reasoning: &apicompat.ResponsesReasoning{
			Effort:  "medium",
			Summary: "auto",
		},
		Text: &apicompat.ResponsesText{Verbosity: "medium"},
	}

	changed := sanitizeVolcengineArkResponsesRequest(account, req)

	require.True(t, changed)
	require.Equal(t, "medium", req.Reasoning.Effort)
	require.Empty(t, req.Reasoning.Summary)
	require.Nil(t, req.Text)
}

func TestSanitizeVolcengineArkResponsesRequestLeavesOtherProvidersUntouched(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			"provider": "other",
		},
	}
	req := &apicompat.ResponsesRequest{
		Reasoning: &apicompat.ResponsesReasoning{
			Effort:  "medium",
			Summary: "auto",
		},
		Text: &apicompat.ResponsesText{Verbosity: "medium"},
	}

	changed := sanitizeVolcengineArkResponsesRequest(account, req)

	require.False(t, changed)
	require.Equal(t, "auto", req.Reasoning.Summary)
	require.NotNil(t, req.Text)
}

func TestVolcengineArkImageModelValidation(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			"provider": "volcengine_ark",
		},
	}

	require.NoError(t, validateOpenAIImagesModelForAccount(account, "doubao-seedream-4-0-250828"))
	require.NoError(t, validateOpenAIImagesModelForAccount(account, "seedream-3-0-t2i-250415"))
	require.Error(t, validateOpenAIImagesModelForAccount(&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "doubao-seedream-4-0-250828"))
}

func TestParseOpenAIImagesRequestAllowsVolcengineArkImageModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"doubao-seedream-4-0-250828","prompt":"draw"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, body)

	require.NoError(t, err)
	require.Equal(t, "doubao-seedream-4-0-250828", parsed.Model)
}

func TestSanitizeVolcengineArkImagesRequest(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			"provider": "volcengine_ark",
		},
	}
	parsed := &OpenAIImagesRequest{
		Endpoint: openAIImagesGenerationsEndpoint,
		Model:    "doubao-seedream-4-0-250828",
	}
	body := []byte(`{"model":"doubao-seedream-4-0-250828","prompt":"draw","quality":"high","background":"transparent","output_format":"png","response_format":"b64_json"}`)

	got, contentType, err := sanitizeVolcengineArkImagesRequest(account, body, "application/json", parsed)
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.Equal(t, "doubao-seedream-4-0-250828", gjson.GetBytes(got, "model").String())
	require.Equal(t, "draw", gjson.GetBytes(got, "prompt").String())
	require.Equal(t, "b64_json", gjson.GetBytes(got, "response_format").String())
	require.False(t, gjson.GetBytes(got, "quality").Exists())
	require.False(t, gjson.GetBytes(got, "background").Exists())
	require.False(t, gjson.GetBytes(got, "output_format").Exists())
}

func TestForwardImagesVolcengineArkUsesImagesBaseURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"doubao-seedream-4-0-250828","prompt":"draw","quality":"high","background":"transparent","response_format":"b64_json"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set("api_key", &APIKey{ID: 42})

	svc := &OpenAIGatewayService{cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}}}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"created":1710000000,"data":[{"b64_json":"aGVsbG8="}]}`)),
		},
	}
	svc.httpUpstream = upstream

	account := &Account{
		ID:          1,
		Name:        "volcengine-ark",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "ark-test", "base_url": "https://ark.cn-beijing.volces.com/api/coding/v3"},
		Extra:       map[string]any{"provider": "volcengine_ark"},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "https://ark.cn-beijing.volces.com/api/v3/images/generations", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer ark-test", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "doubao-seedream-4-0-250828", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "b64_json", gjson.GetBytes(upstream.lastBody, "response_format").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "quality").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "background").Exists())
}
