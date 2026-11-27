package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/nativeharness"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type nativeStandardRepo struct{ keyBillingRouteAPIKeyRepo }

func (*nativeStandardRepo) UpdateLastUsed(context.Context, int64, time.Time) error { return nil }

func TestNativeStandardDispatchKeyScopedNoFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name         string
		enabled      bool
		uid, kid     int64
		method, path string
		want         int
	}{
		{"chat", true, 7, 9, "POST", nativeharness.StandardChatPath, 202},
		{"models", true, 7, 9, "GET", nativeharness.StandardModelsPath, 203},
		{"wrong method", true, 7, 9, "GET", nativeharness.StandardChatPath, 405},
		{"flag off", false, 7, 9, "POST", nativeharness.StandardChatPath, 204},
		{"other key", true, 7, 10, "POST", nativeharness.StandardChatPath, 204},
		{"other owner", true, 8, 9, "POST", nativeharness.StandardChatPath, 204},
		{"other path", true, 7, 9, "POST", "/v1/responses", 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.NativeHarness = config.NativeHarnessConfig{StandardAPI: tc.enabled, AllowedUserID: 7, AllowedAPIKeyID: 9}
			ordinary := 0
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: tc.kid, UserID: tc.uid})
				c.Next()
			})
			r.Use(nativeStandardDispatch(cfg, func(c *gin.Context) { c.Status(202) }, func(c *gin.Context) { c.Status(203) }))
			r.Handle(tc.method, tc.path, func(c *gin.Context) { ordinary++; c.Status(204) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"model":"removed-native-alias"}`)))
			if w.Code != tc.want || (tc.want != 204 && ordinary != 0) {
				t.Fatalf("status=%d ordinary=%d", w.Code, ordinary)
			}
		})
	}
}

// Exercise the real route registration and authentication with zero balance,
// no group, and no ordinary gateway dependencies. Correct interception returns
// native-unavailable, never attempts normal account dispatch or group routing.
func TestNativeStandardRealGatewayOrdering(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{nativeharness.StandardChatPath, nativeharness.StandardModelsPath} {
		cfg := &config.Config{RunMode: config.RunModeStandard}
		cfg.Gateway.NativeHarness = config.NativeHarnessConfig{Enabled: true, StandardAPI: true, AllowedUserID: 7, AllowedAPIKeyID: 9}
		key := &service.APIKey{ID: 9, UserID: 7, Key: "native-fixture", Status: service.StatusActive, User: &service.User{ID: 7, Status: service.StatusActive}}
		auth := service.NewAPIKeyService(&nativeStandardRepo{keyBillingRouteAPIKeyRepo{apiKey: key}}, nil, nil, nil, nil, nil, cfg)
		r := gin.New()
		RegisterGatewayRoutes(r, &handler.Handlers{Gateway: &handler.GatewayHandler{}, OpenAIGateway: &handler.OpenAIGatewayHandler{}}, middleware.NewAPIKeyAuthMiddleware(auth, nil, cfg), auth, nil, nil, nil, nil, cfg)
		method := http.MethodPost
		if path == nativeharness.StandardModelsPath {
			method = http.MethodGet
		}
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer native-fixture")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 503 || !strings.Contains(w.Body.String(), "NATIVE_DISABLED") {
			t.Fatalf("%s status=%d %s", path, w.Code, w.Body.String())
		}
	}
}
