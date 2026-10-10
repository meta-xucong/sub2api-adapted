package service

import (
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBuildWokeyVideoMultipartBodyUsesImagePartsAndScalarFields(t *testing.T) {
	imageBytes := []byte("\x89PNG\r\n\x1a\nreference")
	body := []byte(`{"model":"grok-imagine-video-1.5","prompt":"Move","resolution":"720p","image":{"url":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(imageBytes) + `"}}`)
	info := ParseGrokMediaRequest("application/json", body)
	normalized, contentType, err := normalizeWokeyVideoForwardBody(body, "application/json", info)
	require.NoError(t, err)
	require.Equal(t, "720p", gjson.GetBytes(normalized, "video_resolution").String())
	require.False(t, gjson.GetBytes(normalized, "resolution").Exists())
	require.Equal(t, "image_to_video", gjson.GetBytes(normalized, "mode").String())
	require.Equal(t, "16:9", gjson.GetBytes(normalized, "ratio").String())

	multipartBody, multipartType, err := buildWokeyVideoMultipartBody(normalized, []wokeyVideoReferenceImage{{
		Data: imageBytes, ContentType: "image/png", FileName: "reference.png",
	}})
	require.NoError(t, err)
	require.NotEqual(t, contentType, multipartType)
	mediaType, params, err := mime.ParseMediaType(multipartType)
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	reader := multipart.NewReader(strings.NewReader(string(multipartBody)), params["boundary"])
	fields := map[string]string{}
	var uploaded []byte
	for {
		part, nextErr := reader.NextPart()
		if nextErr == io.EOF {
			break
		}
		require.NoError(t, nextErr)
		data, readErr := io.ReadAll(part)
		require.NoError(t, readErr)
		if part.FormName() == "image[]" {
			require.Equal(t, "reference.png", part.FileName())
			require.Equal(t, "image/png", part.Header.Get("Content-Type"))
			uploaded = data
			continue
		}
		fields[part.FormName()] = string(data)
	}
	require.Equal(t, imageBytes, uploaded)
	require.Equal(t, "grok-imagine-video-1.5", fields["model"])
	require.Equal(t, "Move", fields["prompt"])
	require.Equal(t, "720p", fields["video_resolution"])
	require.Equal(t, "image_to_video", fields["mode"])
	require.Equal(t, "16:9", fields["ratio"])
	require.NotContains(t, fields, "image")
	require.NotContains(t, fields, "resolution")
}

func TestWokeyVideoBillingResolutionSurvivesForwardNormalization(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "original resolution",
			body: `{"model":"grok-imagine-video-1.5","resolution":"720p"}`,
			want: "720p",
		},
		{
			name: "native video resolution",
			body: `{"model":"grok-imagine-video-1.5","video_resolution":"1080p"}`,
			want: "1080p",
		},
		{
			name: "native field takes precedence",
			body: `{"model":"grok-imagine-video-1.5","resolution":"720p","video_resolution":"1080p"}`,
			want: "1080p",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := []byte(tt.body)
			info := ParseGrokMediaRequest("application/json", original)
			normalized, _, err := normalizeWokeyVideoForwardBody(original, "application/json", info)
			require.NoError(t, err)
			require.Equal(t, tt.want, gjson.GetBytes(normalized, "video_resolution").String())
			require.False(t, gjson.GetBytes(normalized, "resolution").Exists())
			require.Equal(t, tt.want, wokeyVideoBillingResolution(original, ParseGrokMediaRequest("application/json", normalized).Resolution))
		})
	}
}

func TestBuildWokeyVideoMultipartBodyPreservesJSONWithoutImages(t *testing.T) {
	body := []byte(`{"model":"grok-imagine-video-1.5","prompt":"Move"}`)
	out, contentType, err := buildWokeyVideoMultipartBody(body, nil)
	require.NoError(t, err)
	require.Equal(t, body, out)
	require.Equal(t, "application/json", contentType)
}

func TestNormalizeWokeyVideoStatusForBillingUsesContentURLWithoutMutatingResponse(t *testing.T) {
	statusBody := []byte(`{"status":"completed","request_id":"wokey-task-1","model":"grok-imagine-video-1.5","content_url":"https://api.wokey.ai/v1/videos/wokey-task-1/content"}`)
	originalBody := append([]byte(nil), statusBody...)
	pending := &GrokVideoPendingBilling{
		Model:                "grok-imagine-video-1.5",
		BillingModel:         "grok-imagine-video-1.5",
		VideoResolution:      "720p",
		VideoDurationSeconds: 5,
	}

	billingBody := normalizeWokeyVideoStatusForBilling(statusBody)

	require.NotSame(t, &statusBody[0], &billingBody[0])
	require.Equal(t, originalBody, statusBody, "billing normalization must not mutate the downstream response body")
	require.Equal(t, "completed", gjson.GetBytes(statusBody, "status").String())
	require.False(t, gjson.GetBytes(statusBody, "video.url").Exists())
	require.Equal(t, "done", gjson.GetBytes(billingBody, "status").String())
	require.Equal(t, "https://api.wokey.ai/v1/videos/wokey-task-1/content", gjson.GetBytes(billingBody, "video.url").String())
	require.True(t, IsGrokVideoStatusBillable(billingBody))
	usageMeta := grokMediaUsageFromResponse(GrokMediaEndpointVideoStatus, GrokMediaRequestInfo{}, billingBody)
	require.Equal(t, 1, usageMeta.VideoCount)
	require.Equal(t, "grok-imagine-video-1.5", usageMeta.BillingModel)

	usage := ExtractGrokVideoBillingFromStatusBody(billingBody, pending, "wokey-task-1")
	require.NotNil(t, usage)
	require.Equal(t, 1, usage.VideoCount)
	require.Equal(t, "grok-imagine-video-1.5", usage.BillingModel)
	require.Equal(t, "720p", usage.VideoResolution)
	require.Equal(t, 5, usage.VideoDurationSeconds)
}

func TestNormalizeWokeyVideoStatusForBillingKeepsExistingGuards(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		wantNormalized bool
	}{
		{
			name:           "video_url remains supported",
			body:           `{"status":"completed","video_url":"https://api.wokey.ai/video.mp4"}`,
			wantNormalized: true,
		},
		{
			name: "non-completed status is not billable",
			body: `{"status":"processing","content_url":"https://api.wokey.ai/video.mp4"}`,
		},
		{
			name: "completed status still requires a URL",
			body: `{"status":"completed"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := []byte(tt.body)
			got := normalizeWokeyVideoStatusForBilling(original)
			if tt.wantNormalized {
				require.Equal(t, "done", gjson.GetBytes(got, "status").String())
				require.Equal(t, "https://api.wokey.ai/video.mp4", gjson.GetBytes(got, "video.url").String())
				require.True(t, IsGrokVideoStatusBillable(got))
				return
			}
			require.Equal(t, original, got)
		})
	}
}
