package veyra

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
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
	require.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
	require.Contains(t, rec.Body.String(), "Veyra Agent")
	require.Contains(t, rec.Body.String(), "/_veyra/styles.css")
	require.Contains(t, rec.Body.String(), "Video OS")
	require.Contains(t, rec.Body.String(), "target=video")
}

func TestVideoPortalRetainsOriginalThreeCardLayout(t *testing.T) {
	subFS, err := fs.Sub(portalFS, "portal_dist")
	require.NoError(t, err)

	for _, name := range []string{"index.html", "mobile.html"} {
		content, readErr := fs.ReadFile(subFS, name)
		require.NoError(t, readErr)
		page := string(content)
		cards := strings.Index(page, `class="entry-grid"`)
		gateway := strings.Index(page, `data-route="sub2api-console"`)
		alchemy := strings.Index(page, `data-route="alchemy`)
		video := strings.Index(page, `data-route="video"`)
		require.NotEqual(t, -1, cards, name+" app grid")
		require.NotEqual(t, -1, gateway, name+" gateway card")
		require.NotEqual(t, -1, alchemy, name+" Alchemy card")
		require.NotEqual(t, -1, video, name+" Video OS card")
		require.Less(t, cards, gateway, name+" grid starts before cards")
		require.Less(t, gateway, alchemy, name+" gateway remains first")
		require.Less(t, alchemy, video, name+" Video OS remains third")
		require.NotContains(t, page, "video-quick-link")
		require.NotContains(t, page, "打开 Video OS")
		require.Contains(t, page, `src="/_veyra/assets/showcase/aggregator-platform.jpg"`)
		require.Contains(t, page, `src="/_veyra/assets/showcase/video-os.jpg"`)
	}

	styles, err := fs.ReadFile(subFS, "styles.css")
	require.NoError(t, err)
	require.Contains(t, string(styles), "grid-template-columns: repeat(2, minmax(0, 1fr))")
	require.Contains(t, string(styles), "grid-template-columns: 1fr")
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
	require.Contains(t, rec.Body.String(), "Video OS")
}

func TestPortalMiddlewareServesMobilePortalAtDirectoryRoot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(PortalMiddleware(PortalConfig{Enabled: true, PortalEnabled: true}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_veyra/", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	require.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
	require.Contains(t, rec.Body.String(), "Veyra Agent")
	require.Contains(t, rec.Body.String(), "/_veyra/app.js")
}

func TestPortalAppDefaultsLoginReturnToHome(t *testing.T) {
	subFS, err := fs.Sub(portalFS, "portal_dist")
	require.NoError(t, err)
	content, err := fs.ReadFile(subFS, "app.js")
	require.NoError(t, err)
	script := string(content)

	require.Contains(t, script, `home: "/_veyra/return?target=home"`)
	require.Contains(t, script, `video: "/_veyra/return?target=video"`)
	require.Contains(t, script, `JSON.stringify({ intent: "video" })`)
	require.Contains(t, script, `"/auth/veyra/callback"`)
	require.Contains(t, script, `target === "home"`)
	require.Contains(t, script, `state.authenticated ? "/" : loginUrl(routeTargets.home)`)
	require.NotContains(t, script, `state.authenticated ? "/dashboard" : loginUrl(routeTargets.home)`)
}

func TestPortalAlchemyLaunchTargetsV2WithoutChangingMobileOrConsoleRoutes(t *testing.T) {
	subFS, err := fs.Sub(portalFS, "portal_dist")
	require.NoError(t, err)
	content, err := fs.ReadFile(subFS, "app.js")
	require.NoError(t, err)
	script := string(content)

	require.Contains(t, script, `new URL(mobile ? "/h5" : "/go/v2", state.alchemyBaseUrl)`)
	require.Contains(t, script, `state.authenticated ? "/dashboard" : loginUrl(routeTargets["sub2api-console"])`)
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
	require.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
	require.Contains(t, rec.Body.String(), "--paper")

	for _, asset := range []string{
		"/_veyra/assets/showcase/aggregator-platform.jpg",
		"/_veyra/assets/showcase/video-os.jpg",
	} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, asset, nil)
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code, asset)
		require.Contains(t, rec.Header().Get("Content-Type"), "image/jpeg", asset)
		require.NotEmpty(t, rec.Body.Bytes(), asset)
	}
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
