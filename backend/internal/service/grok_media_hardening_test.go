//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func hardeningGrokAccount() *Account {
	return &Account{ID: 162, Platform: PlatformGrok, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "mock-only-key", "base_url": "https://api.wokey.ai/v1"}}
}

func TestGrokResolutionBillingUsesExplicitResolution(t *testing.T) {
	for _, tc := range []struct{ body, tier string }{
		{`{"resolution":"1k"}`, ImageBillingSize1K},
		{`{"resolution":"2k"}`, ImageBillingSize2K},
		{`{"resolution":"1k","size":"2048x2048"}`, ImageBillingSize1K},
		{`{"size":"1024x1024"}`, ImageBillingSize1K},
		{`{}`, ImageBillingSize2K},
	} {
		info := ParseGrokMediaRequest("application/json", []byte(tc.body))
		usage := grokMediaUsageFromResponse(GrokMediaEndpointImagesGenerations, info, []byte(`{"data":[{"url":"https://images.example/result.png"}]}`))
		result := &OpenAIForwardResult{ImageCount: usage.ImageCount, ImageSize: usage.ImageSize, ImageInputSize: usage.ImageInputSize, ImageOutputSizes: usage.ImageOutputSizes}
		ApplyOpenAIImageBillingResolution(result)
		require.Equal(t, tc.tier, result.ImageSize, tc.body)
	}
	for _, tc := range []struct{ resolution, outputSize, tier string }{
		{"1k", "2048x2048", ImageBillingSize2K},
		{"2k", "1024x1024", ImageBillingSize1K},
	} {
		info := ParseGrokMediaRequest("application/json", []byte(`{"resolution":"`+tc.resolution+`"}`))
		usage := grokMediaUsageFromResponse(GrokMediaEndpointImagesGenerations, info, []byte(`{"data":[{"url":"https://images.example/result.png","size":"`+tc.outputSize+`"}]}`))
		result := &OpenAIForwardResult{ImageCount: usage.ImageCount, ImageSize: usage.ImageSize, ImageInputSize: usage.ImageInputSize, ImageOutputSizes: usage.ImageOutputSizes}
		ApplyOpenAIImageBillingResolution(result)
		require.Equal(t, tc.tier, result.ImageSize, "actual output size wins over %s", tc.resolution)
	}
}

func hardeningGrokMultipart(t *testing.T, fields map[string]string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for name, value := range fields {
		require.NoError(t, w.WriteField(name, value))
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="image"; filename="ref.png"`)
	h.Set("Content-Type", "image/png")
	part, err := w.CreatePart(h)
	require.NoError(t, err)
	_, err = part.Write(testWokeyPNG)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return body.Bytes(), w.FormDataContentType()
}

func TestGrokMultipartKeepsOptionsAndRejectsReferences(t *testing.T) {
	for _, reference := range []bool{false, true} {
		fields := map[string]string{"model": "grok-imagine-image-quality", "prompt": "edit", "response_format": "b64_json", "quality": "high", "n": "1"}
		if reference {
			fields["reference_images"] = `["https://images.example/reference.png"]`
		}
		body, contentType := hardeningGrokMultipart(t, fields)
		c, _ := newOpenAIImagesTestContext(t, body)
		upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"url":"https://images.example/result.png"}]}`))}}
		svc := &OpenAIGatewayService{httpUpstream: upstream}
		_, err := svc.ForwardGrokMedia(context.Background(), c, hardeningGrokAccount(), GrokMediaEndpointImagesEdits, "", body, contentType)
		if reference {
			var inputErr *GrokImageEditInputValidationError
			require.ErrorAs(t, err, &inputErr)
			require.Contains(t, err.Error(), "reference_images")
			require.Nil(t, upstream.lastReq)
		} else {
			require.NoError(t, err)
			forwarded, _ := readWokeyImageEditMultipart(t, upstream.lastReq.Header.Get("Content-Type"), upstream.lastBody)
			require.Equal(t, "b64_json", forwarded["response_format"])
			require.Equal(t, "high", forwarded["quality"])
			require.Equal(t, "1", forwarded["n"])
		}
	}
}

func TestGrokMultipartPreservationIsWokeyOnly(t *testing.T) {
	body, contentType := hardeningGrokMultipart(t, map[string]string{"model": "grok-imagine-image-quality", "prompt": "edit", "response_format": "b64_json", "quality": "high"})
	expected, expectedType, err := prepareGrokMediaForwardBody(GrokMediaEndpointImagesEdits, body, contentType)
	require.NoError(t, err)
	account := hardeningGrokAccount()
	account.Credentials["base_url"] = "https://api.x.ai/v1"
	actual, actualType, err := prepareGrokMediaForwardBodyForAccount(account, GrokMediaEndpointImagesEdits, body, contentType)
	require.NoError(t, err)
	require.Equal(t, expectedType, actualType)
	require.JSONEq(t, string(expected), string(actual))
}

func TestGrokWokeyMultipartRejectsImagesMissingFromModeration(t *testing.T) {
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(testWokeyPNG)
	images, err := json.Marshal([]string{dataURL})
	require.NoError(t, err)
	for _, includeImageFile := range []bool{false, true} {
		t.Run(map[bool]string{false: "images_only", true: "images_with_file"}[includeImageFile], func(t *testing.T) {
			fields := map[string]string{"model": "grok-imagine-image-quality", "prompt": "edit", "images": string(images)}
			var body []byte
			var contentType string
			if includeImageFile {
				body, contentType = hardeningGrokMultipart(t, fields)
			} else {
				var raw bytes.Buffer
				w := multipart.NewWriter(&raw)
				for name, value := range fields {
					require.NoError(t, w.WriteField(name, value))
				}
				require.NoError(t, w.Close())
				body, contentType = raw.Bytes(), w.FormDataContentType()
			}
			parsed := ParseGrokMediaRequest(contentType, body)
			if !includeImageFile {
				require.False(t, parsed.HasInputImage())
				require.JSONEq(t, `{"prompt":"edit"}`, string(parsed.ModerationBody()), "structured multipart images are absent from the existing request audit")
			}
			c, _ := newOpenAIImagesTestContext(t, body)
			upstream := &httpUpstreamRecorder{}
			svc := &OpenAIGatewayService{httpUpstream: upstream}
			result, err := svc.ForwardGrokMedia(context.Background(), c, hardeningGrokAccount(), GrokMediaEndpointImagesEdits, "", body, contentType)
			var validation *GrokImageEditInputValidationError
			require.ErrorAs(t, err, &validation)
			require.Contains(t, validation.Message, "multipart does not support images")
			require.Nil(t, result)
			require.Nil(t, upstream.lastReq, "an image omitted from moderation must never reach the upstream")
		})
	}
}

func TestGrokWokeyJSONImagesRemainAuditedAndSupported(t *testing.T) {
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(testWokeyPNG)
	body, err := json.Marshal(map[string]any{"model": "grok-imagine-image-quality", "prompt": "edit", "images": []string{dataURL}})
	require.NoError(t, err)
	parsed := ParseGrokMediaRequest("application/json", body)
	require.Equal(t, []string{dataURL}, parsed.InputImageURLs)
	require.Contains(t, string(parsed.ModerationBody()), dataURL)
	c, _ := newOpenAIImagesTestContext(t, body)
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"url":"https://images.example/result.png"}]}`))}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	_, err = svc.ForwardGrokMedia(context.Background(), c, hardeningGrokAccount(), GrokMediaEndpointImagesEdits, "", body, "application/json")
	require.NoError(t, err)
	_, uploaded := readWokeyImageEditMultipart(t, upstream.lastReq.Header.Get("Content-Type"), upstream.lastBody)
	require.NotNil(t, uploaded)
	require.Equal(t, testWokeyPNG, uploaded.Data)
}

func TestGrokMultipartRejectsAmbiguousWokeyFieldsBeforeUpstream(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields [][2]string
		file   string
		broken bool
	}{
		{name: "duplicate option", fields: [][2]string{{"quality", "high"}, {"quality", "low"}}},
		{name: "invalid n", fields: [][2]string{{"n", "oops"}}},
		{name: "zero n", fields: [][2]string{{"n", "0"}}},
		{name: "negative n", fields: [][2]string{{"n", "-1"}}},
		{name: "unsupported file", file: "reference_file"},
		{name: "broken multipart", broken: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			w := multipart.NewWriter(&body)
			for _, field := range tc.fields {
				require.NoError(t, w.WriteField(field[0], field[1]))
			}
			if tc.file != "" {
				part, err := w.CreateFormFile(tc.file, "ref.png")
				require.NoError(t, err)
				_, err = part.Write(testWokeyPNG)
				require.NoError(t, err)
			}
			require.NoError(t, w.Close())
			raw := body.Bytes()
			if tc.broken {
				raw = []byte("invalid multipart body")
			}
			c, _ := newOpenAIImagesTestContext(t, raw)
			upstream := &httpUpstreamRecorder{}
			svc := &OpenAIGatewayService{httpUpstream: upstream}
			_, err := svc.ForwardGrokMedia(context.Background(), c, hardeningGrokAccount(), GrokMediaEndpointImagesEdits, "", raw, w.FormDataContentType())
			var validation *GrokImageEditInputValidationError
			require.ErrorAs(t, err, &validation)
			require.Nil(t, upstream.lastReq)
		})
	}
}
