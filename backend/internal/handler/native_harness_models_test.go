//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/nativeharness"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type modelsAuthRepo struct {
	nativeAuthRepo
	touches atomic.Int32
}

func (r *modelsAuthRepo) UpdateLastUsed(context.Context, int64, time.Time) error {
	r.touches.Add(1)
	return nil
}

func TestNativeModelsRealAuthAndAllowlist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, status, userStatus, credential           string
		allowKey                                       int64
		draining, groupFilter, groupDisabled, ipDenied bool
		want, count                                    int
	}{
		{name: "valid free key", status: "active", userStatus: "active", credential: "test-key", allowKey: 9, want: 200, count: 2},
		{name: "draining", status: "active", userStatus: "active", credential: "test-key", allowKey: 9, draining: true, want: 200},
		{name: "quota empty", status: "quota_exhausted", userStatus: "active", credential: "test-key", allowKey: 9, want: 200},
		{name: "expired empty", status: "expired", userStatus: "active", credential: "test-key", allowKey: 9, want: 200},
		{name: "group model filter", status: "active", userStatus: "active", credential: "test-key", allowKey: 9, groupFilter: true, want: 200, count: 1},
		{name: "missing credential", status: "active", userStatus: "active", allowKey: 9, want: 401},
		{name: "wrong credential", status: "active", userStatus: "active", credential: "wrong", allowKey: 9, want: 401},
		{name: "disabled key", status: "disabled", userStatus: "active", credential: "test-key", allowKey: 9, want: 401},
		{name: "disabled user", status: "active", userStatus: "disabled", credential: "test-key", allowKey: 9, want: 401},
		{name: "other key", status: "active", userStatus: "active", credential: "test-key", allowKey: 10, want: 403},
		{name: "disabled group", status: "active", userStatus: "active", credential: "test-key", allowKey: 9, groupDisabled: true, want: 403},
		{name: "IP denied", status: "active", userStatus: "active", credential: "test-key", allowKey: 9, ipDenied: true, want: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := nativeConfig("https://not-contacted.invalid")
			cfg.Gateway.NativeHarness.AllowedAPIKeyID = tc.allowKey
			cfg.Gateway.NativeHarness.AcceptingNew = !tc.draining
			route := cfg.Gateway.NativeHarness.Routes["synthetic-test"]
			route.ExecutionMode = "workbuddy_local_trial"
			route.ModelAlias = "workbuddy-test"
			route.NativeModel = "private-model"
			cfg.Gateway.NativeHarness.Routes[route.ModelAlias] = route
			key := &service.APIKey{ID: 9, UserID: 7, Key: "test-key", Status: tc.status, User: &service.User{ID: 7, Status: tc.userStatus, Balance: 0}}
			if tc.groupFilter || tc.groupDisabled {
				id := int64(1)
				key.GroupID = &id
				key.Group = &service.Group{ID: id, Status: "active", ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"workbuddy-test"}}}
				if tc.groupDisabled {
					key.Group.Status = "disabled"
				}
			}
			if tc.ipDenied {
				key.IPWhitelist = []string{"192.0.2.1"}
			}
			repo := &modelsAuthRepo{nativeAuthRepo: nativeAuthRepo{key: key}}
			auth := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
			svc := nativeharness.New(cfg.Gateway.NativeHarness, nil)
			svc.Getenv = func(string) string { panic("must not access route credentials") }
			h := &OpenAIGatewayHandler{cfg: cfg, nativeHarness: svc}
			router := gin.New()
			if err := router.SetTrustedProxies(nil); err != nil {
				t.Fatal(err)
			}
			router.GET(nativeharness.ModelsPath, gin.HandlerFunc(middleware2.NewAPIKeyAuthMiddleware(auth, nil, cfg)), h.NativeHarnessModels)
			request := httptest.NewRequest(http.MethodGet, nativeharness.ModelsPath, nil)
			request.RemoteAddr = "127.0.0.1:1234"
			if tc.credential != "" {
				request.Header.Set("Authorization", "Bearer "+tc.credential)
			}
			request.Header.Set("X-Native-Tenant", "attacker")
			request.Header.Set("X-Native-Principal", "attacker")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("%d %s", response.Code, response.Body.String())
			}
			if tc.want != 200 {
				return
			}
			var result nativeharness.ModelList
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Object != "list" || result.Data == nil || len(result.Data) != tc.count || result.NativeHarness.LiveReadinessChecked {
				t.Fatalf("%+v", result)
			}
			if result.NativeHarness.AcceptingNew != (tc.status == "active" && !tc.draining) {
				t.Fatal("admission flag")
			}
			if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Native-Billing") != "disabled-read-only" {
				t.Fatal("read headers")
			}
			for _, text := range []string{"not-contacted", "private-model", "NATIVE_TEST_SERVICE_TOKEN", "source-test", "ledger-test"} {
				if strings.Contains(response.Body.String(), text) {
					t.Fatal("configuration leaked")
				}
			}
			if repo.touches.Load() != 0 {
				t.Fatal("preflight updated key last-used")
			}
		})
	}
}

// The route still checks its own allowlist even if an auth context was supplied.
func TestNativeModelsDisabled(t *testing.T) {
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.NativeHarness.Enabled = false
	key := &service.APIKey{ID: 9, UserID: 7, Key: "test-key", Status: "active", User: &service.User{ID: 7, Status: "active"}}
	auth := service.NewAPIKeyService(&nativeAuthRepo{key: key}, nil, nil, nil, nil, nil, cfg)
	h := &OpenAIGatewayHandler{nativeHarness: nativeharness.New(cfg.Gateway.NativeHarness, nil)}
	r := gin.New()
	r.GET(nativeharness.ModelsPath, gin.HandlerFunc(middleware2.NewAPIKeyAuthMiddleware(auth, nil, cfg)), h.NativeHarnessModels)
	req := httptest.NewRequest(http.MethodGet, nativeharness.ModelsPath, nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 503 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
