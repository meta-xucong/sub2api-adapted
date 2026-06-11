package veyra

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPortalMiddlewareDisabledPassesThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	nextCalled := false
	router.Use(PortalMiddleware(PortalConfig{}))
	router.GET("/", func(c *gin.Context) {
		nextCalled = true
		c.String(http.StatusOK, "original frontend")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	router.ServeHTTP(rec, req)

	require.True(t, nextCalled)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "original frontend", rec.Body.String())
}

func TestPortalMiddlewareServesRootWhenEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(PortalMiddleware(PortalConfig{Enabled: true, PortalEnabled: true}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	require.Contains(t, rec.Body.String(), "Veyra Agent")
	require.Contains(t, rec.Body.String(), "/_veyra/styles.css")
}

func TestPortalMiddlewareServesVeyraReturnWhenEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(PortalMiddleware(PortalConfig{Enabled: true, PortalEnabled: true}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_veyra/return?target=alchemy", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	require.Contains(t, rec.Body.String(), "Veyra Agent")
	require.Contains(t, rec.Body.String(), "/_veyra/app.js")
}

func TestPortalMiddlewareServesAssetsWhenEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(PortalMiddleware(PortalConfig{Enabled: true, PortalEnabled: true}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_veyra/styles.css", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/css")
	require.Contains(t, rec.Body.String(), "--paper")
}

func TestPortalMiddlewareDoesNotCaptureAPIRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(PortalMiddleware(PortalConfig{Enabled: true, PortalEnabled: true}))
	router.GET("/api/v1/ping", func(c *gin.Context) {
		c.String(http.StatusOK, "api ok")
	})
	router.GET("/v1/models", func(c *gin.Context) {
		c.String(http.StatusOK, "models ok")
	})

	for path, expected := range map[string]string{
		"/api/v1/ping": "api ok",
		"/v1/models":   "models ok",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code, path)
		require.Equal(t, expected, rec.Body.String(), path)
	}
}
