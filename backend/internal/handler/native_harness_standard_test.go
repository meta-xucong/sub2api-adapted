//go:build unit

package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/nativeharness"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func TestNativeStandardSameLedgerAndStrictContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hits := 0
	status := 200
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/v1/native-harness/events" && status == 200 {
			fmt.Fprint(w, `{"schema_version":"native-events-v1","remote_task_id":"task-1","events":[{"seq":1,"type":"completion","data":{"id":"chatcmpl-test","created":1,"object":"chat.completion","model":"synthetic-test","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}}],"next_cursor":1,"has_more":false,"terminal_seq":1}`)
			return
		}
		w.WriteHeader(status)
		fmt.Fprint(w, `{"id":"chatcmpl-test","created":1,"object":"chat.completion","model":"synthetic-test","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	cfg := nativeConfig(upstream.URL)
	cfg.Gateway.NativeHarness.StandardAPI = true
	cfg.Gateway.NativeHarness.StandardWaitSeconds = 2
	svc := nativeharness.New(cfg.Gateway.NativeHarness, &nativeTestStore{})
	svc.Getenv = func(string) string { return "synthetic-test-service-token" }
	h := &OpenAIGatewayHandler{cfg: cfg, nativeHarness: svc}
	key := &service.APIKey{ID: 9, UserID: 7, Key: "native-key", Status: service.StatusActive, User: &service.User{ID: 7, Status: service.StatusActive}}
	auth := service.NewAPIKeyService(&nativeAuthRepo{key: key}, nil, nil, nil, nil, nil, cfg)
	r := gin.New()
	for _, path := range []string{nativeharness.Path, nativeharness.StandardChatPath} {
		r.POST(path, gin.HandlerFunc(middleware2.NewAPIKeyAuthMiddleware(auth, nil, cfg)), h.NativeHarnessChat)
	}
	invoke := func(path, body, id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer native-key")
		if id != "" {
			req.Header.Set("Idempotency-Key", id)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(&nativeStreamRecorder{w}, req)
		return w
	}
	for _, body := range []string{`{"model":"synthetic-test","stream":1,"messages":[{"role":"user","content":"hi"}]}`, `{"model":"synthetic-test","tools":[],"messages":[{"role":"user","content":"hi"}]}`} {
		w := invoke(nativeharness.StandardChatPath, body, "invalid")
		if w.Code != 400 || hits != 0 {
			t.Fatalf("unsupported %d hits%d %s", w.Code, hits, w.Body.String())
		}
	}
	if w := invoke(nativeharness.StandardChatPath, nativeRequest, ""); w.Code != 400 || hits != 0 {
		t.Fatalf("missing idempotency %d", w.Code)
	}
	if w := invoke(nativeharness.StandardChatPath, nativeRequest, "same-operation"); w.Code != 200 || hits != 1 {
		t.Fatalf("standard status%d %s", w.Code, w.Body.String())
	}
	streamRequest := strings.TrimSuffix(nativeRequest, "}") + `,"stream":true}`
	if w := invoke(nativeharness.StandardChatPath, streamRequest, "same-operation"); w.Code != 200 || hits != 2 || w.Header().Get("X-Native-Stream-Mode") != "native-events-v1" || !strings.Contains(w.Body.String(), "event: completion") {
		t.Fatalf("live same-ledger %d %s", w.Code, w.Body.String())
	}
	if w := invoke(nativeharness.Path, streamRequest, "same-operation"); w.Code != 400 || hits != 2 {
		t.Fatalf("dedicated nonstream unchanged %d %s", w.Code, w.Body.String())
	}
	status = 202
	if w := invoke(nativeharness.StandardChatPath, streamRequest, "same-operation"); w.Code != 502 || hits != 3 || strings.Contains(w.Header().Get("Content-Type"), "event-stream") || strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatalf("pending must not emit success SSE: %d %s", w.Code, w.Body.String())
	}
	status = 200
	svc.Config.Routes = nil
	svc.Config.AcceptingNew = false
	if w := invoke(nativeharness.Path, nativeRequest, "same-operation"); w.Code != 200 || hits != 4 {
		t.Fatalf("same ledger replay %d %s", w.Code, w.Body.String())
	}
}

// Explicit test transport fixture. Real server connections own actual deadlines.
type nativeStreamRecorder struct{ *httptest.ResponseRecorder }

func (w *nativeStreamRecorder) SetWriteDeadline(time.Time) error { return nil }
