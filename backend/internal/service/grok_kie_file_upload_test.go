//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
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
		Body:       io.NopCloser(strings.NewReader(`{"success":true,"data":{"downloadUrl":"https://tempfile.redpandaai.co/images/sub2api/grok-video/reference.png","mimeType":"image/png"}}`)),
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
	mediaType, params, err := mime.ParseMediaType(upstream.lastReq.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	part, err := multipart.NewReader(bytes.NewReader(upstream.lastBody), params["boundary"]).NextPart()
	require.NoError(t, err)
	require.Equal(t, "image/png", part.Header.Get("Content-Type"))
	require.Equal(t, "relay-reference.png", part.FileName())
}

func TestUploadKIEJobsReferenceImageRejectsUnsupportedReturnedMIME(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"success":true,"data":{"downloadUrl":"https://tempfile.redpandaai.co/images/sub2api/grok-video/reference.png","mimeType":"application/octet-stream"}}`)),
	}}
	account := &Account{ID: 117, Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-key"}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	_, err := svc.uploadKIEJobsReferenceImage(context.Background(), account, "test-key", kieJobsVideoReferenceImage{
		Data: []byte("png-data"), ContentType: "image/png", FileName: "reference.png",
	}, "https://video.aiself.vip/provider-input/token-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported MIME type")
}

func TestMaterializeKIEJobsVideoRelayImagesPreservesFourReferences(t *testing.T) {
	previousDownloader := kieJobsVideoReferenceImageDownloader
	t.Cleanup(func() { kieJobsVideoReferenceImageDownloader = previousDownloader })
	kieJobsVideoReferenceImageDownloader = func(context.Context, string) (kieJobsVideoReferenceImage, error) {
		return kieJobsVideoReferenceImage{Data: []byte("png-data"), ContentType: "image/png", FileName: "reference.png"}, nil
	}
	responses := make([]*http.Response, 0, 4)
	for index := 0; index < 4; index++ {
		responses = append(responses, &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(fmt.Sprintf(
				`{"success":true,"data":{"downloadUrl":"https://tempfile.redpandaai.co/images/sub2api/grok-video/reference-%d.png","mimeType":"image/png"}}`, index))),
		})
	}
	upstream := &httpUpstreamRecorder{responses: responses}
	account := &Account{ID: 117, Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-key"}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	body := []byte(`{"input":{"image_urls":["https://video.aiself.vip/provider-input/token-0","https://video.aiself.vip/provider-input/token-1","https://video.aiself.vip/provider-input/token-2","https://video.aiself.vip/provider-input/token-3"]}}`)

	materialized, err := svc.materializeKIEJobsVideoImageURLs(context.Background(), account, "test-key", body)
	require.NoError(t, err)
	imageURLs := gjson.GetBytes(materialized, "input.image_urls").Array()
	require.Len(t, imageURLs, 4)
	for index, imageURL := range imageURLs {
		require.Equal(t, fmt.Sprintf("https://tempfile.redpandaai.co/images/sub2api/grok-video/reference-%d.png", index), imageURL.String())
	}
	require.Len(t, upstream.requests, 4)
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
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"success":true,"data":{"downloadUrl":"https://tempfile.redpandaai.co/images/sub2api/grok-video/reference.png","mimeType":"image/png"}}`))},
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
