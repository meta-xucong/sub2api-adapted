package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAdaptAIAIImagesEditToGeneration(t *testing.T) {
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

	resp, err := svc.pollAIAIImagesAsyncResponse(context.Background(), account, submit, "test-token")

	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://aiai.ac/api/v1/images/task-123", upstream.requests[0].URL.String())
	require.Equal(t, "Bearer test-token", upstream.requests[0].Header.Get("Authorization"))
}

func TestAIAIImageBaseURLAllowlist(t *testing.T) {
	require.True(t, isAIAIImageBaseURL("https://aiai.ac/api/v1"))
	require.True(t, isAIAIImageBaseURL("https://api.llmtoken.shop/v1"))
	require.False(t, isAIAIImageBaseURL("https://example.com/v1"))
}

func TestPollAIAIImagesAsyncResponseFailsOverOnPoll5xx(t *testing.T) {
	upstream := &httpUpstreamRecorder{responses: []*http.Response{{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"busy"}}`)),
	}}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"base_url": "https://aiai.ac/api/v1"}}
	submit := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"task_id":"task-503"}`))}

	resp, err := svc.pollAIAIImagesAsyncResponse(context.Background(), account, submit, "test-token")

	require.Nil(t, resp)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.StatusCode)
}
