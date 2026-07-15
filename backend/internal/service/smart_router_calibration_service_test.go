package service

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSmartRouterCalibrationGenerationRequestIsMinimalAndTextOnly(t *testing.T) {
	body, contentType, endpoint, err := smartRouterCalibrationRequest(smartrouter.CapabilityImageGeneration)
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.Equal(t, openAIImagesGenerationsEndpoint, endpoint)

	var request map[string]any
	require.NoError(t, json.Unmarshal(body, &request))
	require.Equal(t, smartRouterCalibrationModel, request["model"])
	require.Equal(t, float64(1), request["n"])
	require.NotContains(t, request, "image")
	require.NotContains(t, request, "input_images")
}

func TestSmartRouterCalibrationEditRequestContainsTinyImageFixture(t *testing.T) {
	body, contentType, endpoint, err := smartRouterCalibrationRequest(smartrouter.CapabilityImageEdit)
	require.NoError(t, err)
	require.Equal(t, openAIImagesEditsEndpoint, endpoint)

	mediaType, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	reader := multipart.NewReader(strings.NewReader(string(body)), params["boundary"])
	form, err := reader.ReadForm(128 * 1024)
	require.NoError(t, err)
	require.Equal(t, []string{smartRouterCalibrationModel}, form.Value["model"])
	require.Equal(t, []string{"1"}, form.Value["n"])
	require.Len(t, form.File["image"], 1)
	require.Equal(t, "smart-router-probe.png", form.File["image"][0].Filename)
}

func TestOpenAICompactResponseContainsItemRequiresEncryptedContent(t *testing.T) {
	require.True(t, openAICompactResponseContainsItem([]byte(`{"object":"response.compaction","compaction":{"encrypted_content":"opaque"}}`)))
	require.True(t, openAICompactResponseContainsItem([]byte(`{"output":[{"type":"compaction","encrypted_content":"opaque"}]}`)))
	require.False(t, openAICompactResponseContainsItem([]byte(`{"output":[{"type":"compaction"}]}`)))
	require.True(t, openAICompactResponseContainsItem([]byte("data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"compaction\",\"encrypted_content\":\"opaque\"}}\n\n")))
}

func TestRunSmartRouterCompactCalibrationProbeForcesHTTPTransport(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"resp_probe","object":"response","status":"completed","model":"gpt-5.4","output":[{"type":"compaction","encrypted_content":"opaque"}],"usage":{"input_tokens":1,"output_tokens":1}}`)),
	}}
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
	account := &Account{
		ID:          730060,
		Name:        "ws-enabled-compact-lane",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://example.com/v1",
		},
		Extra: map[string]any{
			"use_responses_api":                             true,
			"openai_apikey_responses_websockets_v2_enabled": true,
		},
	}

	statusCode, _, err := svc.RunSmartRouterCompactCalibrationProbe(context.Background(), account, "gpt-5.4")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, statusCode)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "/v1/responses/compact", upstream.requests[0].URL.Path)
}

func TestBuildSmartRouterCompactProbeExtraUpdatesDoesNotQuarantineTransientFailure(t *testing.T) {
	now := time.Date(2026, 7, 13, 4, 0, 0, 0, time.UTC)
	transient := buildSmartRouterCompactProbeExtraUpdates(SmartRouterCalibrationResult{
		StatusCode:   http.StatusServiceUnavailable,
		ErrorSummary: "service temporarily unavailable",
	}, now)
	require.NotContains(t, transient, "openai_compact_supported")

	unsupported := buildSmartRouterCompactProbeExtraUpdates(SmartRouterCalibrationResult{
		StatusCode:   http.StatusNotFound,
		ErrorSummary: "compact endpoint not found",
	}, now)
	require.Equal(t, false, unsupported["openai_compact_supported"])

	success := buildSmartRouterCompactProbeExtraUpdates(SmartRouterCalibrationResult{Success: true, StatusCode: http.StatusOK}, now)
	require.Equal(t, true, success["openai_compact_supported"])
}

func TestCompactCalibrationLanesIncludeTextAndSkipImageOnlyAccounts(t *testing.T) {
	service := &SmartRouterCalibrationService{}
	accounts := []Account{
		{
			ID:       730052,
			Name:     "image-only-lane",
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2"},
			},
			Extra: map[string]any{
				"smart_router": map[string]any{"capabilities": []any{"image_generation"}},
			},
		},
		{
			ID:       730053,
			Name:     "legacy-text-lane",
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"model_mapping": map[string]any{"gpt-5.6-terra": "gpt-5.6-terra"},
			},
			Extra: map[string]any{
				"smart_router": map[string]any{"capabilities": []any{"chat", "responses"}},
			},
		},
	}

	lanes, accountsByLane := service.compactCalibrationLanes(accounts)
	require.Len(t, lanes, 1)
	require.Equal(t, int64(730053), lanes[0].AccountID)
	require.Equal(t, int64(730053), accountsByLane[lanes[0].LaneID].ID)
}

func TestSmartRouterCalibrationScheduleUsesShanghaiTime(t *testing.T) {
	utcNow := time.Date(2026, 7, 11, 20, 5, 0, 0, time.UTC)
	scheduledFor := smartRouterScheduledFor(utcNow, 4, 0)
	require.Equal(t, time.Date(2026, 7, 11, 20, 0, 0, 0, time.UTC), scheduledFor)

	service := &SmartRouterCalibrationService{}
	require.Equal(t, "0 4 * * *", service.smartRouterCalibrationCronExpression())
}

func TestNextSmartRouterCalibrationTimeUsesNextShanghaiBoundary(t *testing.T) {
	beforeBoundary := time.Date(2026, 7, 11, 19, 59, 0, 0, time.UTC)
	require.Equal(t, time.Date(2026, 7, 11, 20, 0, 0, 0, time.UTC), nextSmartRouterCalibrationTime(beforeBoundary).UTC())

	afterBoundary := time.Date(2026, 7, 11, 20, 1, 0, 0, time.UTC)
	require.Equal(t, time.Date(2026, 7, 12, 20, 0, 0, 0, time.UTC), nextSmartRouterCalibrationTime(afterBoundary).UTC())
}
