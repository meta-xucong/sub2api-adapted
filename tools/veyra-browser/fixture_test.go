package veyra

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// TestLocalBrowserAcceptanceHarness is introduced only through a Go overlay.
// It never connects to a database, Redis, real auth, payment, or model provider.
func TestLocalBrowserAcceptanceHarness(t *testing.T) {
	manifestPath := os.Getenv("VEYRA_BROWSER_MANIFEST")
	stopPath := os.Getenv("VEYRA_BROWSER_STOP")
	if manifestPath == "" || stopPath == "" {
		t.Skip("local browser harness requires explicit output and stop paths")
	}
	gin.SetMode(gin.TestMode)
	const syntheticToken = "audit.synthetic.local-token"
	urls := make(map[string]string)
	modes := []struct {
		name string
		cfg  PortalConfig
	}{
		{"enabled", PortalConfig{Enabled: true, PortalEnabled: true}},
		{"disabled", PortalConfig{}},
		{"bridge_off", PortalConfig{Enabled: false, PortalEnabled: true}},
		{"portal_off", PortalConfig{Enabled: true, PortalEnabled: false}},
	}
	for _, mode := range modes {
		router := gin.New()
		router.Use(servermiddleware.SecurityHeaders(config.CSPConfig{Enabled: true}, nil))
		router.Use(PortalMiddleware(mode.cfg))
		syntheticAuth := servermiddleware.JWTAuthMiddleware(func(c *gin.Context) {
			if c.GetHeader("Authorization") != "Bearer "+syntheticToken {
				c.AbortWithStatus(http.StatusUnauthorized)
				return
			}
			c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 991, Concurrency: 1})
			c.Next()
		})
		RegisterRoutes(router.Group("/api"), syntheticAuth, RoutesConfig{
			Enabled: mode.cfg.Enabled, PortalEnabled: mode.cfg.PortalEnabled,
			AlchemyBaseURL: "https://alchemy.aiself.vip", VideoBaseURL: "https://video.aiself.vip",
			InternalToken: "local-audit-only-not-a-real-secret", LoginTicketTTLSeconds: 120,
		}, NewMemoryTicketStore(), NewMemoryDebitLedger(), nil)
		router.GET("/__audit/ping", func(c *gin.Context) { c.String(http.StatusOK, "local only") })
		router.NoRoute(func(c *gin.Context) {
			// This sentinel represents the pre-existing SPA handler after the portal
			// middleware. It proves pass-through without claiming a Vue app render.
			c.Header("X-Audit-SPA-Fallback", "original-spa-sentinel")
			c.Data(http.StatusOK, "text/html; charset=utf-8", []byte("<!doctype html><html><head><title>Original SPA sentinel</title></head><body><main id=\"original-spa\">Original SPA fallback: "+html.EscapeString(c.Request.URL.Path)+"</main></body></html>"))
		})
		server := httptest.NewServer(router)
		t.Cleanup(server.Close)
		urls[mode.name] = server.URL
	}
	manifest, err := json.MarshalIndent(map[string]any{
		"urls": urls, "synthetic_token": syntheticToken,
		"middleware": "actual default SecurityHeaders -> actual PortalMiddleware",
		"routes": "actual RegisterRoutes; synthetic local auth; in-memory tickets",
		"spa_limitation": "fallback sentinel proves pass-through; production Vue app is not rendered",
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath+".tmp", manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(manifestPath+".tmp", manifestPath); err != nil {
		t.Fatal(err)
	}
	t.Logf("Local-only acceptance servers ready; manifest: %s", manifestPath)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(15 * time.Minute)
	defer deadline.Stop()
	for {
		select {
		case <-ticker.C:
			if _, err := os.Stat(stopPath); err == nil {
				return
			}
		case <-deadline.C:
			t.Fatal("local browser harness timed out awaiting explicit stop")
		}
	}
}
