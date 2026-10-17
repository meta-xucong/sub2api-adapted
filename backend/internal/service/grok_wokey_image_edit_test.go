//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var testWokeyPNG = func() []byte {
	var out bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&out, img); err != nil {
		panic(err)
	}
	return out.Bytes()
}()

// Test fixture from golang.org/x/image@v0.41.0/testdata/gopher-doc.1bpp.lossless.webp (BSD-3-Clause).
const testWokeyWebPBase64 = "UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA=="

func TestIsWokeyGrokImageEditIsExactAndEndpointScoped(t *testing.T) {
	tests := []struct {
		name     string
		account  *Account
		endpoint GrokMediaEndpoint
		want     bool
	}{
		{
			name: "canonical Wokey base URL",
			account: &Account{Platform: PlatformGrok, Credentials: map[string]any{
				"base_url": "https://api.wokey.ai/v1",
			}},
			endpoint: GrokMediaEndpointImagesEdits,
			want:     true,
		},
		{
			name: "canonical Wokey host without version path",
			account: &Account{Platform: PlatformGrok, Credentials: map[string]any{
				"base_url": "https://api.wokey.ai",
			}},
			endpoint: GrokMediaEndpointImagesEdits,
			want:     true,
		},
		{
			name: "xAI official account",
			account: &Account{Platform: PlatformGrok, Credentials: map[string]any{
				"base_url": "https://api.x.ai/v1",
			}},
			endpoint: GrokMediaEndpointImagesEdits,
		},
		{
			name: "Wokey lookalike host",
			account: &Account{Platform: PlatformGrok, Credentials: map[string]any{
				"base_url": "https://api.wokey.ai.evil.example/v1",
			}},
			endpoint: GrokMediaEndpointImagesEdits,
		},
		{
			name: "Wokey image generation is not adapted",
			account: &Account{Platform: PlatformGrok, Credentials: map[string]any{
				"base_url": "https://api.wokey.ai/v1",
			}},
			endpoint: GrokMediaEndpointImagesGenerations,
		},
		{
			name: "Wokey video generation is not adapted here",
			account: &Account{Platform: PlatformGrok, Credentials: map[string]any{
				"base_url": "https://api.wokey.ai/v1",
			}},
			endpoint: GrokMediaEndpointVideosGenerations,
		},
		{
			name: "non Grok account",
			account: &Account{Platform: PlatformOpenAI, Credentials: map[string]any{
				"base_url": "https://api.wokey.ai/v1",
			}},
			endpoint: GrokMediaEndpointImagesEdits,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isWokeyGrokImageEdit(tt.account, tt.endpoint))
		})
	}
}

func TestPrepareWokeyGrokImageEditBodyBuildsMultipartFile(t *testing.T) {
	imageData := append([]byte(nil), testWokeyPNG...)
	body := []byte(fmt.Sprintf(`{"model":"mapped-grok-edit","prompt":"keep the cup","size":"auto","n":1,"image":{"url":"data:image/png;base64,%s","type":"image_url"}}`, base64.StdEncoding.EncodeToString(imageData)))

	out, contentType, err := prepareWokeyGrokImageEditBody(body)
	require.NoError(t, err)
	mediaType, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	require.NotEmpty(t, params["boundary"])

	fields, uploaded := readWokeyImageEditMultipart(t, contentType, out)
	require.Equal(t, "mapped-grok-edit", fields["model"])
	require.Equal(t, "keep the cup", fields["prompt"])
	require.Equal(t, "auto", fields["size"])
	require.Equal(t, "1", fields["n"])
	require.Equal(t, "image", uploaded.FormName)
	require.Equal(t, "reference.png", uploaded.FileName)
	require.Equal(t, "image/png", uploaded.ContentType)
	require.Equal(t, imageData, uploaded.Data)
}

func TestPrepareWokeyGrokImageEditBodyAcceptsSingleImagesEntryAndScalars(t *testing.T) {
	imageData := append([]byte(nil), testWokeyPNG...)
	body := []byte(fmt.Sprintf(`{"model":"grok-edit","prompt":"edit","enabled":true,"count":2,"nullable":null,"images":[{"url":"data:image/png;base64,%s"}]}`, base64.StdEncoding.EncodeToString(imageData)))

	out, contentType, err := prepareWokeyGrokImageEditBody(body)
	require.NoError(t, err)
	fields, uploaded := readWokeyImageEditMultipart(t, contentType, out)
	require.Equal(t, "true", fields["enabled"])
	require.Equal(t, "2", fields["count"])
	require.NotContains(t, fields, "nullable")
	require.Equal(t, imageData, uploaded.Data)
}

func TestPrepareWokeyGrokImageEditBodyRejectsUnsupportedInputs(t *testing.T) {
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(testWokeyPNG)
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "missing image", body: `{"model":"grok-edit","prompt":"edit"}`, want: "requires one image"},
		{name: "external image URL", body: `{"model":"grok-edit","prompt":"edit","image":{"url":"https://images.example/image.png"}}`, want: "remote image URLs are not supported"},
		{name: "image and images conflict", body: fmt.Sprintf(`{"model":"grok-edit","prompt":"edit","image":"%s","images":["%s"]}`, dataURL, dataURL), want: "cannot both be set"},
		{name: "multiple image array", body: fmt.Sprintf(`{"model":"grok-edit","prompt":"edit","images":["%s","%s"]}`, dataURL, dataURL), want: "exactly one image"},
		{name: "mask", body: fmt.Sprintf(`{"model":"grok-edit","prompt":"edit","image":"%s","mask":"%s"}`, dataURL, dataURL), want: "does not support mask"},
		{name: "reference images", body: fmt.Sprintf(`{"model":"grok-edit","prompt":"edit","reference_images":["%s"]}`, dataURL), want: "does not support reference_images"},
		{name: "non-base64 data URL", body: `{"model":"grok-edit","prompt":"edit","image":"data:image/png,abc"}`, want: "requires a base64 Data URL"},
		{name: "bad base64", body: `{"model":"grok-edit","prompt":"edit","image":"data:image/png;base64,%%"}`, want: "invalid Wokey Grok image edit base64"},
		{name: "MIME mismatch", body: fmt.Sprintf(`{"model":"grok-edit","prompt":"edit","image":"data:image/jpeg;base64,%s"}`, base64.StdEncoding.EncodeToString(testWokeyPNG)), want: "MIME type does not match"},
		{name: "truncated image structure", body: fmt.Sprintf(`{"model":"grok-edit","prompt":"edit","image":"data:image/png;base64,%s"}`, base64.StdEncoding.EncodeToString(testWokeyPNG[:10])), want: "invalid Wokey Grok image edit image structure"},
		{name: "unsupported MIME", body: `{"model":"grok-edit","prompt":"edit","image":"data:image/gif;base64,R0lGODlh"}`, want: "unsupported Wokey Grok image edit image MIME"},
		{name: "nested scalar field", body: fmt.Sprintf(`{"model":"grok-edit","prompt":"edit","quality":{"value":"high"},"image":"%s"}`, dataURL), want: `field "quality" must be a scalar`},
		{name: "missing model", body: fmt.Sprintf(`{"prompt":"edit","image":"%s"}`, dataURL), want: "requires model"},
		{name: "missing prompt", body: fmt.Sprintf(`{"model":"grok-edit","image":"%s"}`, dataURL), want: "requires prompt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, _, err := prepareWokeyGrokImageEditBody([]byte(tt.body))
			require.ErrorContains(t, err, tt.want)
			require.Nil(t, out)
		})
	}
}

func TestPrepareWokeyGrokImageEditBodyRejectsTruncatedMultipartParserLimit(t *testing.T) {
	truncated := make([]byte, openAIImageMaxUploadPartSize)
	copy(truncated, testWokeyPNG)
	body := []byte(fmt.Sprintf(`{"model":"grok-edit","prompt":"edit","image":"data:image/png;base64,%s"}`, base64.StdEncoding.EncodeToString(truncated)))

	out, _, err := prepareWokeyGrokImageEditBody(body)
	require.ErrorContains(t, err, "must be smaller than 20 MiB")
	require.Nil(t, out)
}

func TestValidateWokeyGrokImageEditImageDecodesPNGJPEGAAndWebP(t *testing.T) {
	var jpegBuffer bytes.Buffer
	jpegImage := image.NewRGBA(image.Rect(0, 0, 2, 2))
	jpegImage.Set(0, 0, color.RGBA{G: 255, A: 255})
	require.NoError(t, jpeg.Encode(&jpegBuffer, jpegImage, &jpeg.Options{Quality: 80}))
	webpData, err := base64.StdEncoding.DecodeString(testWokeyWebPBase64)
	require.NoError(t, err)

	for _, tt := range []struct {
		name        string
		mime        string
		data        []byte
		wantContent string
	}{
		{name: "png", mime: "image/png", data: testWokeyPNG, wantContent: "image/png"},
		{name: "jpeg", mime: "image/jpeg", data: jpegBuffer.Bytes(), wantContent: "image/jpeg"},
		{name: "webp", mime: "image/webp", data: webpData, wantContent: "image/webp"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dataURL := "data:" + tt.mime + ";base64," + base64.StdEncoding.EncodeToString(tt.data)
			decoded, contentType, err := decodeWokeyGrokImageEditDataURL(dataURL)
			require.NoError(t, err)
			require.Equal(t, tt.wantContent, contentType)
			require.Equal(t, tt.data, decoded)
		})
	}
}

func TestValidateWokeyGrokImageEditImageRejectsDimensionAndPixelLimits(t *testing.T) {
	tests := []struct {
		name   string
		width  uint32
		height uint32
	}{
		{name: "edge dimension", width: wokeyGrokImageEditMaxDimension + 1, height: 1},
		{name: "total pixels", width: 4097, height: 4096},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := pngHeaderWithDimensions(tt.width, tt.height)
			err := validateWokeyGrokImageEditImage(data)
			require.ErrorContains(t, err, "dimensions exceed the supported limit")
		})
	}
}

func TestValidateWokeyGrokImageEditImageRejectsTruncatedFormats(t *testing.T) {
	var jpegBuffer bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpegBuffer, image.NewRGBA(image.Rect(0, 0, 2, 2)), &jpeg.Options{Quality: 80}))
	webpData, err := base64.StdEncoding.DecodeString(testWokeyWebPBase64)
	require.NoError(t, err)
	tests := []struct {
		name string
		data []byte
	}{
		{name: "png", data: testWokeyPNG[:10]},
		{name: "jpeg", data: jpegBuffer.Bytes()[:5]},
		{name: "webp", data: webpData[:12]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWokeyGrokImageEditImage(tt.data)
			require.Error(t, err)
		})
	}
}

func pngHeaderWithDimensions(width, height uint32) []byte {
	data := append([]byte(nil), testWokeyPNG[:33]...)
	binary.BigEndian.PutUint32(data[16:20], width)
	binary.BigEndian.PutUint32(data[20:24], height)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	return data
}

func TestForwardWokeyGrokImageEditUsesMultipartAfterNativeJSONProcessing(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")
	gin.SetMode(gin.TestMode)

	imageData := append([]byte(nil), testWokeyPNG...)
	body := []byte(fmt.Sprintf(`{"model":"grok-imagine","prompt":"keep the cup","resolution":"2k","aspect_ratio":"1:1","image":{"url":"data:image/png;base64,%s","type":"image_url"}}`, base64.StdEncoding.EncodeToString(imageData)))
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &Account{
		ID:          162,
		Name:        "wokey-grok-image-edit",
		Platform:    PlatformGrok,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":       "mock-only-key",
			"base_url":      "https://api.wokey.ai/v1",
			"model_mapping": map[string]any{"grok-imagine-image-quality": "wokey-mapped-grok-edit"},
		},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"aW1hZ2U="}]}`)),
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}

	result, err := svc.ForwardGrokMedia(context.Background(), c, account, GrokMediaEndpointImagesEdits, "", body, "application/json")
	require.NoError(t, err)
	require.Equal(t, "https://api.wokey.ai/v1/images/edits", upstream.lastReq.URL.String())
	fields, uploaded := readWokeyImageEditMultipart(t, upstream.lastReq.Header.Get("Content-Type"), upstream.lastBody)
	require.Equal(t, "wokey-mapped-grok-edit", fields["model"])
	require.Equal(t, "keep the cup", fields["prompt"])
	require.Equal(t, "2k", fields["resolution"])
	require.Equal(t, "1:1", fields["aspect_ratio"])
	require.Equal(t, imageData, uploaded.Data)
	require.Equal(t, "grok-imagine-image-quality", result.BillingModel)
	require.Equal(t, "wokey-mapped-grok-edit", result.UpstreamModel)
}

func TestForwardWokeyOversizeImageEditDoesNotCallUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	truncated := make([]byte, openAIImageMaxUploadPartSize+1)
	copy(truncated, testWokeyPNG)
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	require.NoError(t, writer.WriteField("model", "grok-edit"))
	require.NoError(t, writer.WriteField("prompt", "edit"))
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", `form-data; name="image"; filename="large.png"`)
	partHeader.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(partHeader)
	require.NoError(t, err)
	_, err = part.Write(truncated)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	body := buffer.Bytes()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	account := &Account{
		ID:          163,
		Name:        "wokey-grok-image-edit",
		Platform:    PlatformGrok,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "mock-only-key", "base_url": "https://api.wokey.ai/v1"},
	}
	upstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{httpUpstream: upstream}

	result, err := svc.ForwardGrokMedia(context.Background(), c, account, GrokMediaEndpointImagesEdits, "", body, writer.FormDataContentType())
	require.Nil(t, result)
	require.ErrorContains(t, err, "must be smaller than 20 MiB")
	require.Empty(t, upstream.requests)
}

func TestForwardWokeyEmptyMultipartImageIsTypedClientError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "grok-edit"))
	require.NoError(t, writer.WriteField("prompt", "edit"))
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="image"; filename="empty.png"`)
	header.Set("Content-Type", "image/png")
	_, err := writer.CreatePart(header)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	account := &Account{
		ID:          165,
		Name:        "wokey-grok-image-edit",
		Platform:    PlatformGrok,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "mock-only-key", "base_url": "https://api.wokey.ai/v1"},
	}
	upstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{httpUpstream: upstream}

	result, err := svc.ForwardGrokMedia(context.Background(), c, account, GrokMediaEndpointImagesEdits, "", body.Bytes(), writer.FormDataContentType())
	require.Nil(t, result)
	var inputErr *GrokImageEditInputValidationError
	require.ErrorAs(t, err, &inputErr)
	require.Empty(t, upstream.requests)
}

func TestForwardWokeyImageGenerationRemainsJSON(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"grok-imagine-image-2.0","prompt":"a cup"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	account := &Account{
		ID:          164,
		Name:        "wokey-grok-image-generation",
		Platform:    PlatformGrok,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "mock-only-key", "base_url": "https://api.wokey.ai/v1"},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"aW1hZ2U="}]}`)),
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}

	_, err := svc.ForwardGrokMedia(context.Background(), c, account, GrokMediaEndpointImagesGenerations, "", body, "application/json")
	require.NoError(t, err)
	require.Equal(t, "application/json", upstream.lastReq.Header.Get("Content-Type"))
	require.Equal(t, string(body), string(upstream.lastBody))
}

type parsedWokeyImageEditFile struct {
	FormName    string
	FileName    string
	ContentType string
	Data        []byte
}

func readWokeyImageEditMultipart(t *testing.T, contentType string, body []byte) (map[string]string, *parsedWokeyImageEditFile) {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	require.NotEmpty(t, params["boundary"])
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	fields := make(map[string]string)
	var uploaded *parsedWokeyImageEditFile
	for {
		part, nextErr := reader.NextPart()
		if nextErr == io.EOF {
			break
		}
		require.NoError(t, nextErr)
		data, readErr := io.ReadAll(part)
		require.NoError(t, readErr)
		if part.FileName() != "" {
			require.Nil(t, uploaded)
			uploaded = &parsedWokeyImageEditFile{
				FormName:    part.FormName(),
				FileName:    part.FileName(),
				ContentType: part.Header.Get("Content-Type"),
				Data:        data,
			}
		} else {
			fields[part.FormName()] = string(data)
		}
		_ = part.Close()
	}
	return fields, uploaded
}
