package handler

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponsesImageBridgeDispatchAndAccountSelection(t *testing.T) {
	h := &OpenAIGatewayHandler{cfg: &config.Config{Gateway: config.GatewayConfig{
		ResponsesImageBridge: config.ResponsesImageBridgeConfig{
			Enabled:         true,
			ApplyToProtocol: "images_api_only",
		},
	}}}
	bridgeBody := []byte(`{"model":"gpt-5.6","input":"draw a lighthouse","tools":[{"type":"image_generation","model":"gpt-image-2"}]}`)
	ordinaryImageBody := []byte(`{"model":"gpt-image-2","input":"draw a lighthouse"}`)

	require.True(t, h.responsesImageBridgeEnabled(bridgeBody))
	require.False(t, h.responsesImageBridgeEnabled(ordinaryImageBody), "model name alone must not select the bridge")

	_, parsed, err := service.BuildOpenAIResponsesImageBridgeRequest(bridgeBody)
	require.NoError(t, err)
	require.Equal(t, "gpt-image-2", responsesImageBridgeSelectionModel(true, parsed), "active bridge requests select using the translated Images model")
	require.Empty(t, responsesImageBridgeSelectionModel(false, parsed), "disabled bridge requests use the native Responses selector")

	autoBridgeAccount := &service.Account{
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://images.example/v1", "model_mapping": map[string]any{"gpt-image-2": "image-model"}},
	}
	nativeAccount := &service.Account{
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Credentials: autoBridgeAccount.Credentials,
		Extra:       map[string]any{"responses_image_mode": "native"},
	}
	require.True(t, responsesImageBridgeDispatches(true, autoBridgeAccount), "eligible third-party APIKey account dispatches through Images API")
	require.False(t, responsesImageBridgeDispatches(true, nativeAccount), "explicit native account keeps the official Responses forwarder")
	require.False(t, responsesImageBridgeDispatches(false, autoBridgeAccount), "bridge dispatch remains opt-in")
}

func TestResponsesImageBridgeUsesImageRequestTimeout(t *testing.T) {
	imageConfig := &config.Config{}
	imageConfig.Gateway.ImageRequestTimeoutSeconds = 2
	imageService := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, imageConfig, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h := &OpenAIGatewayHandler{gatewayService: imageService}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	_, hasDeadline := c.Request.Context().Deadline()
	require.False(t, hasDeadline)

	cancel := h.applyResponsesImageBridgeRequestTimeout(c)
	defer cancel()
	deadline, ok := c.Request.Context().Deadline()
	require.True(t, ok, "Responses image bridge must share the Images request-wide timeout")
	require.InDelta(t, 2*time.Second, time.Until(deadline), float64(250*time.Millisecond))
}

func TestResponsesImageBridgeDoesNotAffectOrdinaryImagesOrNativeResponses(t *testing.T) {
	h := &OpenAIGatewayHandler{cfg: &config.Config{Gateway: config.GatewayConfig{
		ResponsesImageBridge: config.ResponsesImageBridgeConfig{Enabled: true, ApplyToProtocol: "images_api_only"},
	}}}
	// This is the Images endpoint's ordinary request shape; it does not enter the
	// Responses handler bridge, which requires a Responses image_generation tool.
	require.False(t, h.responsesImageBridgeEnabled([]byte(`{"model":"gpt-image-2","prompt":"draw"}`)))
	require.False(t, h.responsesImageBridgeEnabled([]byte(`{"model":"gpt-5.6","input":"hello"}`)))
	require.False(t, responsesImageBridgeDispatches(false, &service.Account{Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey}))
}
