//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestMaterializeKIEJobsVideoRelayImages(t *testing.T) {
	previousDownloader := kieJobsVideoReferenceImageDownloader
	t.Cleanup(func() { kieJobsVideoReferenceImageDownloader = previousDownloader })
	kieJobsVideoReferenceImageDownloader = func(context.Context, string) (kieJobsVideoReferenceImage, error) {
		return kieJobsVideoReferenceImage{
			Data:        []byte("\x89PNG\r\n\x1a\nreference"),
			ContentType: "image/png",
			FileName:    "relay-reference.png",
		}, nil
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"success":true,"data":{"downloadUrl":"https://tempfile.redpandaai.co/images/sub2api/grok-video/reference.png"}}`)),
	}}
	account := &Account{ID: 117, Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-key"}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	body := []byte(`{"model":"grok-imagine-video-1-5-preview","input":{"image_urls":["https://video.aiself.vip/provider-input/token-a","https://cdn.example.com/reference.png"]}}`)

	materialized, err := svc.materializeKIEJobsVideoImageURLs(context.Background(), account, "test-key", body)
	require.NoError(t, err)
	require.Equal(t, "https://tempfile.redpandaai.co/images/sub2api/grok-video/reference.png", gjson.GetBytes(materialized, "input.image_urls.0").String())
	require.Equal(t, "https://cdn.example.com/reference.png", gjson.GetBytes(materialized, "input.image_urls.1").String())
	require.Equal(t, "https://kieai.redpandaai.co/api/file-stream-upload", upstream.lastReq.URL.String())
	require.Contains(t, upstream.lastReq.Header.Get("Content-Type"), "multipart/form-data")
	require.Contains(t, string(upstream.lastBody), "relay-reference.png")
}

func TestMaterializeKIEJobsVideoRelayUploadFailureStopsBeforeCreate(t *testing.T) {
	previousDownloader := kieJobsVideoReferenceImageDownloader
	t.Cleanup(func() { kieJobsVideoReferenceImageDownloader = previousDownloader })
	kieJobsVideoReferenceImageDownloader = func(context.Context, string) (kieJobsVideoReferenceImage, error) {
		return kieJobsVideoReferenceImage{Data: []byte("image"), ContentType: "image/png", FileName: "reference.png"}, nil
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadGateway,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"code":502,"msg":"upload unavailable"}`)),
	}}
	account := &Account{ID: 117, Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-key"}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	_, err := svc.materializeKIEJobsVideoImageURLs(context.Background(), account, "test-key", []byte(`{"input":{"image_urls":["https://video.aiself.vip/provider-input/token-a"]}}`))
	require.Error(t, err)
	require.Len(t, upstream.requests, 1)
}

func TestForwardGrokMediaKIEJobsUploadsRelayBeforeCreate(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")
	gin.SetMode(gin.TestMode)
	previousProber := kieJobsVideoImageURLProber
	previousDownloader := kieJobsVideoReferenceImageDownloader
	t.Cleanup(func() {
		kieJobsVideoImageURLProber = previousProber
		kieJobsVideoReferenceImageDownloader = previousDownloader
	})
	kieJobsVideoImageURLProber = func(context.Context, string) (kieJobsVideoImageURLProbe, error) {
		return kieJobsVideoImageURLProbe{statusCode: 200, contentType: "image/png", contentLength: 16}, nil
	}
	kieJobsVideoReferenceImageDownloader = func(context.Context, string) (kieJobsVideoReferenceImage, error) {
		return kieJobsVideoReferenceImage{Data: []byte("\x89PNG\r\n\x1a\nreference"), ContentType: "image/png", FileName: "reference.png"}, nil
	}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"success":true,"data":{"downloadUrl":"https://tempfile.redpandaai.co/images/sub2api/grok-video/reference.png"}}`))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"code":200,"msg":"success","data":{"taskId":"task-kie-uploaded"}}`))},
	}}
	account := &Account{ID: 117, Platform: PlatformGrok, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{
		"api_key": "test-key", "base_url": "https://api.kie.ai", "grok_video_transport": "kie_jobs",
		"model_mapping": map[string]any{"grok-imagine-video-1.5": "grok-imagine-video-1-5-preview"},
	}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok-imagine-video-1.5","prompt":"waves","image":{"url":"https://video.aiself.vip/provider-input/token-a"},"duration":8}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	svc := &OpenAIGatewayService{httpUpstream: upstream}

	result, err := svc.ForwardGrokMedia(context.Background(), c, account, GrokMediaEndpointVideosGenerations, "", body, "application/json")
	require.NoError(t, err)
	require.Equal(t, "task-kie-uploaded", result.ResponseID)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, kieJobsVideoFileUploadURL, upstream.requests[0].URL.String())
	require.Equal(t, "https://api.kie.ai/api/v1/jobs/createTask", upstream.requests[1].URL.String())
	require.Equal(t, "https://tempfile.redpandaai.co/images/sub2api/grok-video/reference.png", gjson.GetBytes(upstream.bodies[1], "input.image_urls.0").String())
}
