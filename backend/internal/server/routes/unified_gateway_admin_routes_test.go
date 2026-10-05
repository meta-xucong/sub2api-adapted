package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUnifiedGatewayAdminRoutesStayBehindAdminAuthAndExcludeRuntime(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	adminHandlers := &handler.AdminHandlers{UnifiedGateway: adminhandler.NewUnifiedGatewayHandler(nil)}
	handlers := &handler.Handlers{Admin: adminHandlers}
	adminAuth := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			middleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authorization required")
			return
		}
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Next()
	})
	auditLog := middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepUp := middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), handlers, adminAuth, auditLog, stepUp, nil, nil)

	paths := map[string]bool{}
	for _, route := range router.Routes() {
		paths[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{
		"GET /api/v1/admin/unified-gateway/meta",
		"GET /api/v1/admin/unified-gateway/options",
		"GET /api/v1/admin/unified-gateway/model-candidates",
		"GET /api/v1/admin/unified-gateway/configs",
		"GET /api/v1/admin/unified-gateway/configs/:id",
		"GET /api/v1/admin/unified-gateway/configs/:id/revisions",
		"GET /api/v1/admin/unified-gateway/drafts/:id",
		"POST /api/v1/admin/unified-gateway/drafts",
		"POST /api/v1/admin/unified-gateway/configs/:id/draft",
		"PUT /api/v1/admin/unified-gateway/drafts/:id",
		"POST /api/v1/admin/unified-gateway/drafts/:id/validate",
		"POST /api/v1/admin/unified-gateway/drafts/:id/preview",
		"POST /api/v1/admin/unified-gateway/drafts/:id/pricing-import/preview",
		"POST /api/v1/admin/unified-gateway/drafts/:id/pricing-import/apply",
		"POST /api/v1/admin/unified-gateway/drafts/:id/publish",
		"POST /api/v1/admin/unified-gateway/configs/:id/disable",
		"POST /api/v1/admin/unified-gateway/configs/:id/revisions/:revision/restore",
	} {
		require.Truef(t, paths[route], "missing management route %s", route)
	}
	require.False(t, paths["GET /api/v1/admin/unified-gateway/snapshots"])
	require.False(t, paths["POST /api/v1/admin/unified-gateway/bindings/:binding_id/probe"])
	for _, route := range router.Routes() {
		require.NotContains(t, route.Path, "/unified/v1/")
	}

	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v1/admin/unified-gateway/meta", nil))
	require.Equal(t, http.StatusUnauthorized, unauthenticated.Code)

	// An authenticated direct request reaches the handler; there is no extra
	// route-level feature gate. The nil service returns its explicit unavailable error.
	authenticated := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/unified-gateway/meta", nil)
	request.Header.Set("Authorization", "Bearer admin-token")
	router.ServeHTTP(authenticated, request)
	require.Equal(t, http.StatusInternalServerError, authenticated.Code)
}
