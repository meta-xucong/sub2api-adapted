package service

import (
	"encoding/json"
	"mime"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
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
