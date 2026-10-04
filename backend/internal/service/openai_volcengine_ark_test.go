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

func arkTestAccount() *Account {
	return &Account{
		ID:       7,
		Name:     "synthetic-ark",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "synthetic-key",
			"base_url": "https://ark.example/api/coding/v3",
		},
		Extra: map[string]any{"provider": " Volcengine_Ark "},
	}
}

func TestParseOpenAIImagesRequestAllowsArkModelButCapabilityIsAPIKeyOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"doubao-seedream-4-0-test","prompt":"draw"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	require.Equal(t, OpenAIImagesCapabilityAPIKey, parsed.RequiredCapabilityForModel(parsed.Model))
	require.Equal(t, OpenAIImagesCapabilityAPIKey, parsed.RequiredCapability)
	require.Error(t, validateCompatibleImagesModel(parsed.Model), "Seedream parsing must not widen generic model validation")
	require.Error(t, validateOpenAIImagesModelForAccount(&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, parsed.Model))
	require.Error(t, validateOpenAIImagesModelForAccount(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"provider": "volcengine_ark"}}, parsed.Model))
	require.NoError(t, validateOpenAIImagesModelForAccount(&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "gemini-2.5-flash-image"), "existing compatible image models remain accepted")
}

func TestArkImagesTransformsAreAccountScoped(t *testing.T) {
	parsed := &OpenAIImagesRequest{Endpoint: openAIImagesGenerationsEndpoint, Model: "doubao-seedream-4-0-test", Prompt: "draw", Quality: "high"}
	body := []byte(`{"model":"doubao-seedream-4-0-test","prompt":"draw","quality":"high"}`)
	ordinary := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://ordinary.example/v1"}}

	require.Error(t, validateOpenAIImagesModelForAccount(ordinary, parsed.Model))
	adaptedBody, adaptedType, adaptedEndpoint, adapted, err := adaptVolcengineArkImagesToGeneration(ordinary, parsed)
	require.NoError(t, err)
	require.False(t, adapted)
	require.Empty(t, adaptedBody)
	require.Empty(t, adaptedType)
	require.Empty(t, adaptedEndpoint)
	cleaned, contentType, err := sanitizeVolcengineArkImagesRequest(ordinary, body, "application/json", parsed)
	require.NoError(t, err)
	require.Equal(t, body, cleaned)
	require.Equal(t, "application/json", contentType)
	require.Empty(t, volcengineArkImagesBaseURL(ordinary))
}

func TestArkImagesEditAdaptsToGenerationWithReferenceImage(t *testing.T) {
	account := arkTestAccount()
	png := []byte("\x89PNG\r\n\x1a\nimage")
	parsed := &OpenAIImagesRequest{
		Endpoint:       openAIImagesEditsEndpoint,
		Model:          "doubao-seedream-4-0-test",
		Prompt:         "edit this",
		N:              2,
		Size:           "2K",
		ResponseFormat: "b64_json",
		Uploads:        []OpenAIImagesUpload{{FileName: "reference.png", ContentType: "image/png", Data: png}},
	}

	body, contentType, endpoint, adapted, err := adaptVolcengineArkImagesToGeneration(account, parsed)
	require.NoError(t, err)
	require.True(t, adapted)
	require.Equal(t, "application/json", contentType)
	require.Equal(t, openAIImagesGenerationsEndpoint, endpoint)
	require.Equal(t, parsed.Model, gjson.GetBytes(body, "model").String())
	require.Equal(t, parsed.Prompt, gjson.GetBytes(body, "prompt").String())
	require.Equal(t, int64(2), gjson.GetBytes(body, "n").Int())
	require.Equal(t, "2K", gjson.GetBytes(body, "size").String())
	require.True(t, strings.HasPrefix(gjson.GetBytes(body, "image.0").String(), "data:image/png;base64,"))

	account.Extra["openai_images_base_url"] = "https://ark-images.example/api/v3"
	require.Equal(t, "https://ark-images.example/api/v3", volcengineArkImagesBaseURL(account))
}

func TestArkImagesSanitizationAndMaskFailover(t *testing.T) {
	account := arkTestAccount()
	parsed := &OpenAIImagesRequest{Endpoint: openAIImagesGenerationsEndpoint, Model: "seedream-3-0-test"}
	body := []byte(`{"model":"seedream-3-0-test","prompt":"draw","quality":"high","background":"transparent","output_format":"png"}`)

	cleaned, contentType, err := sanitizeVolcengineArkImagesRequest(account, body, "application/json", parsed)
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.Equal(t, "draw", gjson.GetBytes(cleaned, "prompt").String())
	require.Equal(t, "b64_json", gjson.GetBytes(cleaned, "response_format").String())
	require.False(t, gjson.GetBytes(cleaned, "quality").Exists())
	require.False(t, gjson.GetBytes(cleaned, "background").Exists())
	require.False(t, gjson.GetBytes(cleaned, "output_format").Exists())

	parsed.HasMask = true
	_, _, _, adapted, err := adaptVolcengineArkImagesToGeneration(account, parsed)
	require.False(t, adapted)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadRequest, failoverErr.StatusCode)
}

func TestForwardImagesArkUsesProfileBaseURLAndPreservesOrdinaryPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("api_key", &APIKey{ID: 42})

	svc := &OpenAIGatewayService{
		cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"created":1710000000,"data":[{"b64_json":"aGVsbG8="}]}`)),
		}},
	}
	body := []byte(`{"model":"doubao-seedream-4-0-test","prompt":"draw","quality":"high","background":"transparent"}`)
	parsed := &OpenAIImagesRequest{Endpoint: openAIImagesGenerationsEndpoint, ContentType: "application/json", Model: "doubao-seedream-4-0-test", Prompt: "draw", N: 1}

	result, err := svc.ForwardImages(context.Background(), c, arkTestAccount(), body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	upstream := svc.httpUpstream.(*httpUpstreamRecorder)
	require.Equal(t, "https://ark.cn-beijing.volces.com/api/v3/images/generations", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer synthetic-key", upstream.lastReq.Header.Get("Authorization"))
	require.False(t, gjson.GetBytes(upstream.lastBody, "quality").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "background").Exists())

	ordinary := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "normal-key", "base_url": "https://ordinary.example/v1"}}
	_, err = svc.ForwardImages(context.Background(), c, ordinary, body, parsed, "")
	require.Error(t, err)
	require.Len(t, upstream.requests, 1, "ordinary accounts must not receive Seedream provider requests")
}

func TestForwardImagesArkEditUsesGenerationImageField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("api_key", &APIKey{ID: 42})

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"created":1710000001,"data":[{"b64_json":"ZG91YmFvMQ=="}]}`)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		httpUpstream: upstream,
	}
	account := arkTestAccount()
	body := []byte(`{"model":"doubao-seedream-4-0-test","prompt":"edit this"}`)
	parsed := &OpenAIImagesRequest{
		Endpoint:       openAIImagesEditsEndpoint,
		ContentType:    "application/json",
		Model:          "doubao-seedream-4-0-test",
		Prompt:         "edit this",
		N:              1,
		InputImageURLs: []string{"https://images.example/reference.png"},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, "https://ark.cn-beijing.volces.com/api/v3/images/generations", upstream.lastReq.URL.String())
	require.Equal(t, "application/json", upstream.lastReq.Header.Get("Content-Type"))
	require.Equal(t, parsed.Model, gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "https://images.example/reference.png", gjson.GetBytes(upstream.lastBody, "image.0").String())
}

func TestApplyVolcengineArkMessagesToResponsesIsAccountScoped(t *testing.T) {
	request := func() *apicompat.ResponsesRequest {
		return &apicompat.ResponsesRequest{
			Model:     "doubao-seed-2.0-lite",
			Reasoning: &apicompat.ResponsesReasoning{Effort: "medium", Summary: "auto"},
			Text:      &apicompat.ResponsesText{Verbosity: "medium"},
		}
	}
	ark := arkTestAccount()
	ark.Extra["openai_multimodal_model"] = "doubao-multimodal-endpoint"
	arkReq := request()
	require.True(t, applyVolcengineArkMessagesToResponses(ark, arkReq))
	require.Equal(t, "doubao-multimodal-endpoint", arkReq.Model)
	require.Nil(t, arkReq.Reasoning)
	require.Nil(t, arkReq.Text)

	ordinaryReq := request()
	ordinary := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"provider": "other"}}
	require.False(t, applyVolcengineArkMessagesToResponses(ordinary, ordinaryReq))
	require.Equal(t, "doubao-seed-2.0-lite", ordinaryReq.Model)
	require.Equal(t, "auto", ordinaryReq.Reasoning.Summary)
	require.Equal(t, "medium", ordinaryReq.Text.Verbosity)

	ark.Extra = map[string]any{"provider": "volcengine_ark"}
	fallbackReq := request()
	require.True(t, applyVolcengineArkMessagesToResponses(ark, fallbackReq))
	require.Equal(t, "doubao-seed-2-0-lite-260428", fallbackReq.Model)
}
