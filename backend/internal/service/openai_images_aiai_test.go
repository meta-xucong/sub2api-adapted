package service

import (
	"bytes"
	"context"
	"errors"
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

func TestAdaptAIAIImagesEditToGenerationIsProviderAndAccountScoped(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://aiai.ac/api/v1",
		},
	}
	parsed := &OpenAIImagesRequest{
		Endpoint:       openAIImagesEditsEndpoint,
		Model:          "gpt-image-2",
		Prompt:         "preserve the reference",
		ResponseFormat: "b64_json",
		Uploads: []OpenAIImagesUpload{{
			FileName:    "reference.png",
			ContentType: "application/octet-stream",
			Data:        []byte("\x89PNG\r\n\x1a\nimage"),
		}},
	}

	body, contentType, endpoint, adapted, async, err := adaptAIAIImagesEditToGeneration(account, parsed)
	require.NoError(t, err)
	require.True(t, adapted)
	require.True(t, async)
	require.Equal(t, "application/json", contentType)
	require.Equal(t, openAIImagesGenerationsEndpoint, endpoint)
	require.True(t, gjson.GetBytes(body, "async").Bool())
	require.True(t, strings.HasPrefix(gjson.GetBytes(body, "image.0").String(), "data:image/png;base64,"))

	otherProvider := *account
	otherProvider.Credentials = map[string]any{"base_url": "https://images.example/v1"}
	_, _, _, adapted, async, err = adaptAIAIImagesEditToGeneration(&otherProvider, parsed)
	require.NoError(t, err)
	require.False(t, adapted)
	require.False(t, async)

	otherAccountType := *account
	otherAccountType.Type = AccountTypeOAuth
	_, _, _, adapted, async, err = adaptAIAIImagesEditToGeneration(&otherAccountType, parsed)
	require.NoError(t, err)
	require.False(t, adapted)
	require.False(t, async)
}

func TestAIAIImageBaseURLAllowlist(t *testing.T) {
	require.True(t, isAIAIImageBaseURL("https://aiai.ac/api/v1"))
	require.True(t, isAIAIImageBaseURL("https://api.llmtoken.shop/v1"))
	require.False(t, isAIAIImageBaseURL("https://notllmtoken.shop/v1"))
	require.False(t, isAIAIImageBaseURL("https://example.com/v1"))
}

func TestPollAIAIImagesAsyncResponse(t *testing.T) {
	upstream := &httpUpstreamRecorder{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"task_status":"succeed","data":[{"b64_json":"aW1hZ2U="}]}`)),
	}}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          42,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"base_url": "https://aiai.ac/api/v1"},
	}
	submit := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"task_id":"task-123"}`)),
	}

	resp, err := svc.pollAIAIImagesAsyncResponse(context.Background(), context.Background(), nil, account, submit, "test-token")
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://aiai.ac/api/v1/images/task-123", upstream.requests[0].URL.String())
	require.Equal(t, "Bearer test-token", upstream.requests[0].Header.Get("Authorization"))
}

func TestPollAIAIImagesAsyncResponseFailsOverOnPollError(t *testing.T) {
	upstream := &httpUpstreamRecorder{responses: []*http.Response{{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"busy"}}`)),
	}}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"base_url": "https://aiai.ac/api/v1"}}
	submit := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"task_id":"task-503"}`))}

	resp, err := svc.pollAIAIImagesAsyncResponse(context.Background(), context.Background(), nil, account, submit, "test-token")
	require.Nil(t, resp)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.StatusCode)
}

func TestForwardAIAIImagesEditSubmitsAndPollsOnlyForAIAIAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	makeContext := func(path string) *gin.Context {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(`{}`)))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("api_key", &APIKey{ID: 42})
		return c
	}
	responses := func() []*http.Response {
		return []*http.Response{
			{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"task_id":"task-123"}`))},
			{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"task_status":"succeed","data":[{"b64_json":"aW1hZ2U="}]}`))},
		}
	}
	body := []byte(`{"model":"gpt-image-2","prompt":"edit"}`)
	parsed := &OpenAIImagesRequest{
		Endpoint:    openAIImagesEditsEndpoint,
		ContentType: "application/json",
		Model:       "gpt-image-2",
		Prompt:      "edit",
		N:           1,
		Uploads:     []OpenAIImagesUpload{{FileName: "reference.png", ContentType: "image/png", Data: []byte("image")}},
	}
	newService := func(recorder *httpUpstreamRecorder) *OpenAIGatewayService {
		return &OpenAIGatewayService{
			cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
			httpUpstream: recorder,
		}
	}

	aiaiUpstream := &httpUpstreamRecorder{responses: responses()}
	aiaiAccount := &Account{ID: 51, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "aiai-key", "base_url": "https://aiai.ac/api/v1"}}
	result, err := newService(aiaiUpstream).ForwardImages(context.Background(), makeContext("/v1/images/edits"), aiaiAccount, body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, aiaiUpstream.requests, 2)
	require.Equal(t, http.MethodPost, aiaiUpstream.requests[0].Method)
	require.Equal(t, "https://aiai.ac/api/v1/images/generations", aiaiUpstream.requests[0].URL.String())
	require.True(t, gjson.GetBytes(aiaiUpstream.bodies[0], "async").Bool())
	require.Equal(t, http.MethodGet, aiaiUpstream.requests[1].Method)
	require.Equal(t, "https://aiai.ac/api/v1/images/task-123", aiaiUpstream.requests[1].URL.String())

	ordinaryUpstream := &httpUpstreamRecorder{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"created":1710000000,"data":[{"b64_json":"aW1hZ2U="}]}`)),
	}}}
	ordinaryAccount := &Account{ID: 52, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "ordinary-key", "base_url": "https://ordinary.example/v1"}}
	_, err = newService(ordinaryUpstream).ForwardImages(context.Background(), makeContext("/v1/images/edits"), ordinaryAccount, body, parsed, "")
	require.NoError(t, err)
	require.Len(t, ordinaryUpstream.requests, 1, "ordinary accounts must not start AIAI task polling")
	require.Equal(t, "https://ordinary.example/v1/images/edits", ordinaryUpstream.requests[0].URL.String())
	require.False(t, gjson.GetBytes(ordinaryUpstream.bodies[0], "async").Bool())
}
