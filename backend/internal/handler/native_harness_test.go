//go:build unit

package handler

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/nativeharness"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
)

type nativeTestStore struct {
	mu sync.Mutex
	op *nativeharness.Operation
}

func (s *nativeTestStore) Lookup(_ context.Context, u, k int64, key string) (*nativeharness.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.op == nil {
		return nil, nativeharness.ErrNotFound
	}
	v := *s.op
	return &v, nil
}
func (s *nativeTestStore) Freeze(ctx context.Context, op nativeharness.Operation) (*nativeharness.Operation, error) {
	s.mu.Lock()
	if s.op == nil {
		s.op = &op
	}
	s.mu.Unlock()
	return s.Lookup(ctx, op.UserID, op.APIKeyID, op.ClientKey)
}

// Only credential lookup is a fixture; the real APIKeyService and middleware
// validate the provided HTTP credential and current user/key/group/IP policy.
type nativeAuthRepo struct {
	service.APIKeyRepository
	key *service.APIKey
}

func (r *nativeAuthRepo) GetByKeyForAuth(ctx context.Context, key string) (*service.APIKey, error) {
	return r.GetByKey(ctx, key)
}
func (r *nativeAuthRepo) GetByKey(_ context.Context, key string) (*service.APIKey, error) {
	if key != r.key.Key {
		return nil, service.ErrAPIKeyNotFound
	}
	v := *r.key
	return &v, nil
}
func (r *nativeAuthRepo) UpdateLastUsed(context.Context, int64, time.Time) error { return nil }
func nativeRouter(cfg *config.Config, store nativeharness.Store) *gin.Engine {
	svc := nativeharness.New(cfg.Gateway.NativeHarness, store)
	svc.Getenv = func(string) string { return "synthetic-test-service-token" }
	h := &OpenAIGatewayHandler{cfg: cfg, nativeHarness: svc}
	key := &service.APIKey{ID: 9, UserID: 7, Key: "synthetic-test-user-key", Status: service.StatusActive, User: &service.User{ID: 7, Status: service.StatusActive, Balance: 0}}
	auth := service.NewAPIKeyService(&nativeAuthRepo{key: key}, nil, nil, nil, nil, nil, cfg)
	r := gin.New()
	r.POST(nativeharness.Path, gin.HandlerFunc(middleware2.NewAPIKeyAuthMiddleware(auth, nil, cfg)), h.NativeHarnessChat)
	return r
}
func nativeConfig(origin string) *config.Config {
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Gateway.NativeHarness = config.NativeHarnessConfig{Enabled: true, AcceptingNew: true, AllowedUserID: 7, AllowedAPIKeyID: 9, Routes: map[string]config.NativeHarnessRoute{"synthetic-test": {Origin: origin, CredentialEnv: "NATIVE_TEST_SERVICE_TOKEN", Source: "source-test", LedgerInstance: "ledger-test", BindingID: "binding-test", Revision: 1, ModelAlias: "synthetic-test", NativeModel: "fake-text"}}}
	return cfg
}

const nativeRequest = `{"model":"synthetic-test","messages":[{"role":"user","content":"hello"}]}`

func performNative(r *gin.Engine, key, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, nativeharness.Path, strings.NewReader(nativeRequest))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Idempotency-Key", id)
	req.Header.Set("X-Native-Tenant", "attacker")
	req.Header.Set("X-Native-Principal", "attacker")
	req.Header.Set("X-Native-Operation", "attacker")
	req.Header.Set("X-Native-Admission-Allowed", "true")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
func TestNativeHarnessRealMiddlewareAndHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("X-Native-Tenant") != "user-7" || r.Header.Get("X-Native-Principal") != "key-9" || r.Header.Get("X-Native-Operation") == "attacker" {
			t.Error("client headers trusted")
		}
		fmt.Fprint(w, `{"id":"chatcmpl-test","created":1,"object":"chat.completion","model":"synthetic-test","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	cfg := nativeConfig(upstream.URL)
	r := nativeRouter(cfg, &nativeTestStore{})
	w := performNative(r, "wrong", "op1")
	if w.Code != 401 || hits != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = performNative(r, "synthetic-test-user-key", "op1")
	if w.Code != 200 || hits != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w.Header().Get("X-Native-Billing") != "disabled-synthetic-pilot" || strings.Contains(w.Body.String(), `"usage"`) {
		t.Fatal("usage/billing contract")
	}
	w = performNative(r, "synthetic-test-user-key", "op1")
	if w.Code != 200 || hits != 2 {
		t.Fatal(w.Code, w.Body.String())
	}
}

// Explicit opt-in integration test. Requires a disposable database with migration
// 248 already applied, and a running real Bridge + Facade + synthetic Node.
// It does not start servers, create credentials, run migrations, or call a model.
func TestNativeHarnessExternalBridge(t *testing.T) {
	dsn, origin := os.Getenv("NATIVE_HARNESS_TEST_DSN"), os.Getenv("NATIVE_HARNESS_TEST_FACADE")
	if dsn == "" || origin == "" {
		t.Skip("explicit disposable PostgreSQL and Facade required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := nativeConfig(origin)
	cfg.Gateway.NativeHarness.Routes["synthetic-test"] = config.NativeHarnessRoute{Origin: origin, CredentialEnv: "NATIVE_TEST_SERVICE_TOKEN", Source: "source-test", LedgerInstance: "ledger-test", BindingID: "binding-test", Revision: 1, ModelAlias: "synthetic-test", NativeModel: "fake-text"}
	r := nativeRouter(cfg, &nativeharness.SQLStore{DB: db})
	key := fmt.Sprintf("integration-%d", time.Now().UnixNano())
	var first string
	for i := 0; i < 100; i++ {
		w := performNative(r, "synthetic-test-user-key", key)
		if w.Code == 200 {
			first = w.Body.String()
			break
		}
		if w.Code != 504 && w.Code != 503 && w.Code != 429 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if first == "" {
		t.Fatal("no completed result")
	}
	// Removing new routes and admission must not change the frozen target.
	cfg.Gateway.NativeHarness.AcceptingNew = false
	cfg.Gateway.NativeHarness.Routes = nil
	replay := nativeRouter(cfg, &nativeharness.SQLStore{DB: db})
	w := performNative(replay, "synthetic-test-user-key", key)
	if w.Code != 200 || w.Body.String() != first {
		t.Fatalf("replay: %d %s", w.Code, w.Body.String())
	}
	t.Log("real middleware/handler/PostgreSQL/HTTP completion and restart-style replay passed; credential lookup fixture, no ordinary provider billing")
}

// This verifies the three repositories' HTTP path, NOT PostgreSQL persistence.
// BOTH credential lookup and operation-route storage are explicit test doubles.
func TestNativeHarnessExternalBridgeMemoryRoute(t *testing.T) {
	origin := os.Getenv("NATIVE_HARNESS_TEST_FACADE")
	if origin == "" {
		t.Skip("explicit real Bridge/Facade/synthetic Node fixture required")
	}
	cfg := nativeConfig(origin)
	alias := os.Getenv("NATIVE_HARNESS_TEST_ALIAS")
	body := nativeRequest
	billing := "disabled-synthetic-pilot"
	if alias != "" {
		route := cfg.Gateway.NativeHarness.Routes["synthetic-test"]
		route.ModelAlias, route.NativeModel, route.ExecutionMode = alias, "fixture-text", "harness_local_trial"
		cfg.Gateway.NativeHarness.Routes = map[string]config.NativeHarnessRoute{alias: route}
		body = strings.ReplaceAll(body, "synthetic-test", alias)
		billing = "disabled-local-trial"
	}
	store := &nativeTestStore{}
	r := nativeRouter(cfg, store)
	server := httptest.NewServer(r)
	defer server.Close()
	send := func(url, key string) (int, string, http.Header) {
		req, err := http.NewRequest(http.MethodPost, url+nativeharness.Path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer synthetic-test-user-key")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, string(data), response.Header
	}
	key := fmt.Sprintf("memory-integration-%d", time.Now().UnixNano())
	var first string
	for i := 0; i < 100; i++ {
		status, data, _ := send(server.URL, key)
		if status == 200 {
			first = data
			break
		}
		if status != 504 && status != 503 && status != 429 {
			t.Fatalf("%d %s", status, data)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if first == "" {
		t.Fatal("no completed result")
	}
	cfg.Gateway.NativeHarness.AcceptingNew = false
	cfg.Gateway.NativeHarness.Routes = nil
	replay := httptest.NewServer(nativeRouter(cfg, store))
	defer replay.Close()
	status, data, headers := send(replay.URL, key)
	if status != 200 || data != first {
		t.Fatalf("replay: %d %s", status, data)
	}
	if headers.Get("X-Native-Billing") != billing || strings.Contains(first, `"usage"`) {
		t.Fatal("unknown usage/free pilot contract")
	}
	t.Log("three-repository HTTP path passed with credential-lookup and operation-route test doubles; PostgreSQL migration/concurrency/recovery NOT_RUN")
}
