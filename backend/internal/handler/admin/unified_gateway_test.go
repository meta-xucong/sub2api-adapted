package admin

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBindUnifiedJSONRejectsUnknownAndTrailingFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"document":{},"unexpected":true}`,
		`{"document":{}} {"document":{}}`,
	} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(body))
		var target struct {
			Document map[string]any `json:"document"`
		}
		require.Error(t, bindUnifiedJSON(ctx, &target))
	}
}

func TestUnifiedServiceContextRequiresAndBindsAuthenticatedAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("GET", "/", nil)

	_, _, ok := unifiedServiceContext(ctx)
	require.False(t, ok)

	ctx.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 77})
	serviceCtx, actor, ok := unifiedServiceContext(ctx)
	require.True(t, ok)
	require.Equal(t, "77", actor)
	require.NotEqual(t, ctx.Request.Context(), serviceCtx)
}

func TestUnifiedErrorEnvelopeDoesNotExposeCause(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	writeUnifiedError(ctx, &service.UnifiedGatewayAdminError{
		Status:   409,
		Reason:   "UNIFIED_GATEWAY_VERSION_CONFLICT",
		Message:  "resource revision does not match If-Match",
		Metadata: map[string]string{"resource_id": "mc_public"},
		Err:      service.ErrUnifiedGatewayAdminVersion,
	})
	require.Equal(t, 409, recorder.Code)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, float64(409), envelope["code"])
	require.Equal(t, "UNIFIED_GATEWAY_VERSION_CONFLICT", envelope["reason"])
	require.NotContains(t, recorder.Body.String(), "revision conflict")
	require.NotContains(t, recorder.Body.String(), "sql")
}
