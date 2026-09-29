package handler

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func TestResponsesImageBridgeEnabledIsOptInAndNarrow(t *testing.T) {
	h := &OpenAIGatewayHandler{cfg: &config.Config{Gateway: config.GatewayConfig{
		ResponsesImageBridge: config.ResponsesImageBridgeConfig{
			Enabled:         true,
			ApplyToProtocol: "images_api_only",
		},
	}}}
	imageRequest := []byte(`{"model":"gpt-5.5","input":"draw","tools":[{"type":"image_generation"}]}`)

	require.True(t, h.responsesImageBridgeEnabled(imageRequest))
	require.False(t, h.responsesImageBridgeEnabled([]byte(`{"model":"gpt-5.5","input":"hello"}`)))

	h.cfg.Gateway.ResponsesImageBridge.Enabled = false
	require.False(t, h.responsesImageBridgeEnabled(imageRequest))
	h.cfg.Gateway.ResponsesImageBridge.Enabled = true
	h.cfg.Gateway.ResponsesImageBridge.ApplyToProtocol = "all"
	require.False(t, h.responsesImageBridgeEnabled(imageRequest))

	// The helper must not make an ordinary image-looking payload eligible just
	// because it contains a model field.
	require.False(t, h.responsesImageBridgeEnabled([]byte(`{"model":"gpt-image-2","input":"draw"}`)))
}
