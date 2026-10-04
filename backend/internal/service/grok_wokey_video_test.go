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

func TestBuildWokeyVideoMultipartBodyPreservesJSONWithoutImages(t *testing.T) {
	body := []byte(`{"model":"grok-imagine-video-1.5","prompt":"Move"}`)
	out, contentType, err := buildWokeyVideoMultipartBody(body, nil)
	require.NoError(t, err)
	require.Equal(t, body, out)
	require.Equal(t, "application/json", contentType)
}
