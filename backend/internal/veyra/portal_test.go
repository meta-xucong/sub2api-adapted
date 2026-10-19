package veyra

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPortalSourceMatchesEmbeddedFiles(t *testing.T) {
	subFS, err := fs.Sub(portalFS, "portal_dist")
	require.NoError(t, err)

	sourceRoot := filepath.Join("..", "..", "..", "extensions", "veyra", "portal")
	files := []string{
		"app.js",
		"mobile-redirect.js",
		"index.html",
		"mobile.html",
		"styles.css",
		"assets/showcase/aggregator-platform.jpg",
		"assets/showcase/luxury-product.jpg",
		"assets/showcase/video-os.jpg",
	}
	for _, name := range files {
		source, err := os.ReadFile(filepath.Join(sourceRoot, filepath.FromSlash(name)))
		require.NoError(t, err, name)
		embedded, err := fs.ReadFile(subFS, name)
		require.NoError(t, err, name)
		require.Equal(t, source, embedded, name)
	}
}

func TestPortalCSPAllowsOnlyExactPortalPages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, portalConfig := range []PortalConfig{
		{}, {Enabled: true}, {PortalEnabled: true}, {Enabled: true, PortalEnabled: true},
	} {
		for _, requestPath := range []string{
			"/", "/_veyra/return?target=video", "/_veyra/", "/_veyra/mobile.html",
			"/_veyra/index.html", "/login", "/admin/dashboard", "/api/veyra/portal/config",
			"/_veyra/app.js", "/_veyra/mobile-redirect.js", "/_veyra/return-extra",
			"/_veyra/alias/../mobile.html",
		} {
			router := gin.New()
			router.Use(servermiddleware.SecurityHeaders(config.CSPConfig{Enabled: true}, nil))
			var originalPolicy string
			router.Use(func(c *gin.Context) {
				originalPolicy = c.Writer.Header().Get("Content-Security-Policy")
				c.Next()
			})
			router.Use(PortalMiddleware(portalConfig))
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, requestPath, nil)
			router.ServeHTTP(rec, req)
			policy := rec.Header().Get("Content-Security-Policy")
			expected := originalPolicy
			if portalConfig.IsPortalEnabled() {
				switch strings.SplitN(requestPath, "?", 2)[0] {
				case "/", "/_veyra/return", "/_veyra/", "/_veyra/mobile.html", "/_veyra/index.html":
					expected = strings.Replace(originalPolicy, "form-action 'self'", "form-action 'self' "+videoCallbackFormSource, 1)
				}
			}
			// Full equality preserves nonce, media-src, and every other directive.
			require.Equal(t, expected, policy, "%+v %s", portalConfig, requestPath)
		}
	}
}

func TestPortalCSPPreservesCustomPolicyAndExplicitDenial(t *testing.T) {
	for _, policy := range []string{
		"", "default-src 'none'; form-action 'none'; media-src https://media.example",
		"script-src 'nonce-test'; media-src 'self' data: https://vidgen.x.ai; form-action 'self'; frame-ancestors 'none'",
		"default-src 'self'; form-action 'self' " + videoCallbackFormSource,
		"default-src 'self'; media-src 'none'",
	} {
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Header("Content-Security-Policy", policy); c.Next() })
		router.Use(func(c *gin.Context) {
			allowPortalVideoFormAction(c, "/")
			allowPortalVideoFormAction(c, "/")
		})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		expected := policy
		if strings.Contains(policy, "form-action 'self';") {
			expected = strings.Replace(policy, "form-action 'self';", "form-action 'self' "+videoCallbackFormSource+";", 1)
		}
		require.Equal(t, expected, rec.Header().Get("Content-Security-Policy"))
	}
}

func TestPortalIndexUsesSameOriginScriptsAndRootBrand(t *testing.T) {
	content, err := fs.ReadFile(portalFS, "portal_dist/index.html")
	require.NoError(t, err)
	html := string(content)
	require.Contains(t, html, `<script src="/_veyra/mobile-redirect.js"></script>`)
	require.NotContains(t, html, "<script>")
	require.Contains(t, html, `class="brand-lockup" href="/"`)
	require.NotContains(t, html, `href="./index.html"`)
}

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
	require.Contains(t, rec.Body.String(), "Video OS")
	require.Contains(t, rec.Body.String(), "target=video")
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

func TestPortalAlchemyLaunchTargetsRootWithoutChangingMobileOrConsoleRoutes(t *testing.T) {
	subFS, err := fs.Sub(portalFS, "portal_dist")
	require.NoError(t, err)
	content, err := fs.ReadFile(subFS, "app.js")
	require.NoError(t, err)
	script := string(content)

	require.Contains(t, script, `new URL(mobile ? "/h5" : "/", state.alchemyBaseUrl)`)
	require.NotContains(t, script, `"/go/v2"`)
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
