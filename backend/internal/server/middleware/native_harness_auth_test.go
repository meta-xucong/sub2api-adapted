//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/nativeharness"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func TestNativeHarnessAuthDoesNotBypassAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name                                    string
		enabled                                 bool
		userStatus, keyStatus, path, credential string
		allowedKey                              int64
		want                                    int
	}{
		{"free native", true, "active", "active", nativeharness.Path, "test-key", 9, 200},
		{"quota replay auth", true, "active", "quota_exhausted", nativeharness.Path, "test-key", 9, 200},
		{"disabled feature", false, "active", "active", nativeharness.Path, "test-key", 9, 403},
		{"ordinary endpoint unchanged", true, "active", "active", "/v1/chat/completions", "test-key", 9, 403},
		{"wrong key", true, "active", "active", nativeharness.Path, "wrong", 9, 401},
		{"missing key", true, "active", "active", nativeharness.Path, "", 9, 401},
		{"disabled key", true, "active", "disabled", nativeharness.Path, "test-key", 9, 401},
		{"disabled user", true, "disabled", "active", nativeharness.Path, "test-key", 9, 401},
		{"not allowlisted", true, "active", "active", nativeharness.Path, "test-key", 10, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{RunMode: config.RunModeStandard}
			cfg.Gateway.NativeHarness = config.NativeHarnessConfig{Enabled: tc.enabled, AllowedUserID: 7, AllowedAPIKeyID: tc.allowedKey}
			repo := &stubApiKeyRepo{getByKey: func(_ context.Context, key string) (*service.APIKey, error) {
				if key != "test-key" {
					return nil, service.ErrAPIKeyNotFound
				}
				return &service.APIKey{ID: 9, UserID: 7, Key: key, Status: tc.keyStatus, User: &service.User{ID: 7, Status: tc.userStatus, Balance: 0}}, nil
			}}
			svc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
			router := gin.New()
			router.POST(tc.path, gin.HandlerFunc(NewAPIKeyAuthMiddleware(svc, nil, cfg)), func(c *gin.Context) { c.Status(200) })
			req := httptest.NewRequest(http.MethodPost, tc.path, nil)
			if tc.credential != "" {
				req.Header.Set("Authorization", "Bearer "+tc.credential)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestNativeHarnessAuthPreservesGroupAndIPRestrictions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, policy := range []string{"group", "ip"} {
		t.Run(policy, func(t *testing.T) {
			cfg := &config.Config{RunMode: config.RunModeStandard}
			cfg.Gateway.NativeHarness = config.NativeHarnessConfig{Enabled: true, AllowedUserID: 7, AllowedAPIKeyID: 9}
			repo := &stubApiKeyRepo{getByKey: func(context.Context, string) (*service.APIKey, error) {
				key := &service.APIKey{ID: 9, UserID: 7, Status: service.StatusActive, User: &service.User{ID: 7, Status: service.StatusActive}}
				if policy == "group" {
					id := int64(1)
					key.GroupID = &id
					key.Group = &service.Group{ID: id, Status: "disabled"}
				} else {
					key.IPWhitelist = []string{"192.0.2.1"}
				}
				return key, nil
			}}
			svc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
			r := gin.New()
			_ = r.SetTrustedProxies(nil)
			r.POST(nativeharness.Path, gin.HandlerFunc(NewAPIKeyAuthMiddleware(svc, nil, cfg)), func(c *gin.Context) { c.Status(200) })
			req := httptest.NewRequest(http.MethodPost, nativeharness.Path, nil)
			req.RemoteAddr = "127.0.0.1:1234"
			req.Header.Set("Authorization", "Bearer fixture-key")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != 403 {
				t.Fatalf("policy %s bypassed: %d %s", policy, w.Code, w.Body.String())
			}
		})
	}
}
