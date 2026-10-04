//go:build unit

package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIImagesHandlerSetsRetryAfterOnlyForImageEdits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.Gateway.ImageEditTransientCooldownSeconds = 7
	gw := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h := &OpenAIGatewayHandler{gatewayService: gw}

	editRecorder := httptest.NewRecorder()
	editContext, _ := gin.CreateTestContext(editRecorder)
	h.setImageEditTransientRetryAfter(editContext, &service.OpenAIImagesRequest{Endpoint: "/v1/images/edits"})
	require.Equal(t, "8", editRecorder.Header().Get("Retry-After"))

	generationRecorder := httptest.NewRecorder()
	generationContext, _ := gin.CreateTestContext(generationRecorder)
	h.setImageEditTransientRetryAfter(generationContext, &service.OpenAIImagesRequest{Endpoint: "/v1/images/generations"})
	require.Empty(t, generationRecorder.Header().Get("Retry-After"))
}
