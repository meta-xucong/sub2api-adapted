package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSyncUnifiedGatewayWokeyPricesRejectsDisabledConfigWithoutLeakingDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewSettingHandler(&service.SettingService{}, nil, nil, nil, nil, nil, nil)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/admin/settings/unified-gateway-route-pricing/wokey-sync", nil)

	handler.SyncUnifiedGatewayWokeyPrices(ctx)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "api_key")
	require.NotContains(t, recorder.Body.String(), "response body")
}

func TestManualizeUnifiedGatewayWokeyPriceRejectsMalformedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewSettingHandler(&service.SettingService{}, nil, nil, nil, nil, nil, nil)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/admin/settings/unified-gateway-route-pricing/wokey-sync/manualize", strings.NewReader(`{"expected_revision":"bad"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.ManualizeUnifiedGatewayWokeyPrice(ctx)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "secret")
}
