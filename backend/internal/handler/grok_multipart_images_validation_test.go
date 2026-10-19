//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWokeyMultipartImagesRejectedBeforeUpstreamWith400(t *testing.T) {
	var imageBytes bytes.Buffer
	require.NoError(t, png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	images, err := json.Marshal([]string{"data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes.Bytes())})
	require.NoError(t, err)
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	require.NoError(t, w.WriteField("model", "grok-imagine-image-quality"))
	require.NoError(t, w.WriteField("prompt", "edit"))
	require.NoError(t, w.WriteField("images", string(images)))
	require.NoError(t, w.Close())
	parsed := service.ParseGrokMediaRequest(w.FormDataContentType(), body.Bytes())
	require.False(t, parsed.HasInputImage())
	require.JSONEq(t, `{"prompt":"edit"}`, string(parsed.ModerationBody()))

	upstream := &compatPartialUsageUpstream{}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	gateway := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg,
		nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
	account := &service.Account{ID: 1, Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "local-test", "base_url": "https://api.wokey.ai/v1"}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", w.FormDataContentType())
	writerSize := c.Writer.Size()
	result, err := gateway.ForwardGrokMedia(context.Background(), c, account, service.GrokMediaEndpointImagesEdits, "", body.Bytes(), w.FormDataContentType())
	require.Error(t, err)
	require.Nil(t, result)
	require.Zero(t, upstream.calls, "no image missing from the audit may reach a provider")
	h := &OpenAIGatewayHandler{}
	require.True(t, h.writeGrokMediaInputValidationError(c, err, writerSize))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "invalid_request_error")
	require.Contains(t, recorder.Body.String(), "use one image file or a JSON request")
}
