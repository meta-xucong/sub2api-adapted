//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPrepareKIEJobsVideoCreateBody(t *testing.T) {
	info := ParseGrokMediaRequest("application/json", []byte(`{
  "model":"grok-imagine-video-1.5",
  "prompt":"A slow camera push-in",
  "aspect_ratio":"9:16",
  "resolution":"720p",
  "duration":8
}`))

	body, contentType, err := prepareKIEJobsVideoCreateBody(info, "grok-imagine-video-1-5-preview")
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.JSONEq(t, `{
  "model":"grok-imagine-video-1-5-preview",
  "input":{
    "prompt":"A slow camera push-in",
    "aspect_ratio":"9:16",
    "resolution":"720p",
    "duration":8
  }
}`, string(body))
}

func TestPrepareKIEJobsVideoCreateBodyRejectsUnsafeInputs(t *testing.T) {
	t.Run("empty prompt", func(t *testing.T) {
		info := ParseGrokMediaRequest("application/json", []byte(`{"model":"grok-imagine-video-1.5"}`))
		_, _, err := prepareKIEJobsVideoCreateBody(info, "grok-imagine-video-1-5-preview")
		require.EqualError(t, err, "KIE video generation requires a non-empty prompt")
	})

	t.Run("data URL image", func(t *testing.T) {
		info := ParseGrokMediaRequest("application/json", []byte(`{
  "model":"grok-imagine-video-1.5",
  "prompt":"Motion",
  "image":{"url":"data:image/png;base64,AA=="}
}`))
		_, _, err := prepareKIEJobsVideoCreateBody(info, "grok-imagine-video-1-5-preview")
		require.EqualError(t, err, "KIE reference image 0 must be a public HTTPS URL")
	})

	t.Run("seven references", func(t *testing.T) {
		info := ParseGrokMediaRequest("application/json", []byte(`{
  "model":"grok-imagine-video-1.5",
  "prompt":"Motion",
  "reference_images":[
    {"url":"https://example.com/a.png"},
    {"url":"https://example.com/b.png"},
    {"url":"https://example.com/c.png"},
    {"url":"https://example.com/d.png"},
    {"url":"https://example.com/e.png"},
    {"url":"https://example.com/f.png"},
    {"url":"https://example.com/g.png"}
  ]
}`))
		body, _, err := prepareKIEJobsVideoCreateBody(info, "grok-imagine-video-1-5-preview")
		require.NoError(t, err)
		imageURLs := gjson.GetBytes(body, "input.image_urls").Array()
		require.Len(t, imageURLs, 7)
		require.Equal(t, "https://example.com/a.png", imageURLs[0].String())
		require.Equal(t, "https://example.com/g.png", imageURLs[6].String())
	})

	t.Run("reference request follows native KIE input schema", func(t *testing.T) {
		info := ParseGrokMediaRequest("application/json", []byte(`{
  "model":"grok-imagine-video-1.5",
  "prompt":"Animate the supplied image",
  "reference_images":[{"url":"https://example.com/reference.png"}],
  "aspect_ratio":"16:9",
  "resolution":"480p",
  "duration":8
}`))
		body, _, err := prepareKIEJobsVideoCreateBody(info, "grok-imagine-video-1-5-preview")
		require.NoError(t, err)
		require.Equal(t, "https://example.com/reference.png", gjson.GetBytes(body, "input.image_urls.0").String())
		require.False(t, gjson.GetBytes(body, "input.mode").Exists(), "native KIE schema does not define input.mode")
	})

	t.Run("eight references", func(t *testing.T) {
		info := ParseGrokMediaRequest("application/json", []byte(`{
  "model":"grok-imagine-video-1.5",
  "prompt":"Motion",
  "reference_images":[
    {"url":"https://example.com/a.png"},
    {"url":"https://example.com/b.png"},
    {"url":"https://example.com/c.png"},
    {"url":"https://example.com/d.png"},
    {"url":"https://example.com/e.png"},
    {"url":"https://example.com/f.png"},
    {"url":"https://example.com/g.png"},
    {"url":"https://example.com/h.png"}
  ]
}`))
		_, _, err := prepareKIEJobsVideoCreateBody(info, "grok-imagine-video-1-5-preview")
		require.EqualError(t, err, "KIE Grok video accepts at most 7 reference images")
	})
}

func TestNormalizeKIEJobsVideoResponses(t *testing.T) {
	create, err := normalizeKIEJobsVideoCreateResponse([]byte(`{
  "code":200,"msg":"success","data":{"taskId":"task_kie_123"}
}`), "grok-imagine-video-1.5")
	require.NoError(t, err)
	require.JSONEq(t, `{
  "id":"task_kie_123","request_id":"task_kie_123","status":"pending","model":"grok-imagine-video-1.5"
}`, string(create))

	status, err := normalizeKIEJobsVideoStatusResponse([]byte(`{
  "code":200,"msg":"success","data":{
    "taskId":"task_kie_123",
    "model":"grok-imagine-video-1-5-preview",
    "state":"success",
    "progress":100,
    "resultJson":"{\"resultUrls\":[\"https://cdn.example.com/video.mp4\"]}"
  }
}`), "task_kie_123")
	require.NoError(t, err)
	require.Equal(t, "done", gjson.GetBytes(status, "status").String())
	require.Equal(t, "https://cdn.example.com/video.mp4", gjson.GetBytes(status, "video.url").String())
	require.Equal(t, int64(100), gjson.GetBytes(status, "progress").Int())

	failed, err := normalizeKIEJobsVideoStatusResponse([]byte(`{
  "data":{"taskId":"task_kie_123","state":"fail","failMsg":"provider rejected input"}
}`), "task_kie_123")
	require.NoError(t, err)
	require.Equal(t, "failed", gjson.GetBytes(failed, "status").String())
	require.Equal(t, "provider rejected input", gjson.GetBytes(failed, "error.message").String())

	_, err = normalizeKIEJobsVideoStatusResponse([]byte(`{"code":404,"msg":"task not found"}`), "task_kie_123")
	require.EqualError(t, err, "KIE task status failed: task not found")
}

func TestKIEJobsAccountEndpointSupport(t *testing.T) {
	account := &Account{Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"grok_video_transport": "kie_jobs",
	}}
	require.True(t, account.SupportsGrokMediaEndpoint(GrokMediaEndpointVideosGenerations))
	require.True(t, account.SupportsGrokMediaEndpoint(GrokMediaEndpointVideoStatus))
	require.False(t, account.SupportsGrokMediaEndpoint(GrokMediaEndpointVideosEdits))
}
