package nativeharness

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

type memoryStore struct {
	mu   sync.Mutex
	rows map[string]Operation
}

func (s *memoryStore) Lookup(_ context.Context, u, k int64, key string) (*Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.rows[fmt.Sprint(u, ":", k, ":", key)]
	if !ok {
		return nil, ErrNotFound
	}
	return &v, nil
}
func (s *memoryStore) Freeze(ctx context.Context, v Operation) (*Operation, error) {
	s.mu.Lock()
	key := fmt.Sprint(v.UserID, ":", v.APIKeyID, ":", v.ClientKey)
	if _, ok := s.rows[key]; !ok {
		s.rows[key] = v
	}
	s.mu.Unlock()
	return s.Lookup(ctx, v.UserID, v.APIKeyID, v.ClientKey)
}

const request = `{"model":"synthetic-test","messages":[{"role":"user","content":"hello"}]}`
const completion = `{"id":"chatcmpl-test","created":1,"object":"chat.completion","model":"synthetic-test","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`

func configured(origin string) config.NativeHarnessConfig {
	return config.NativeHarnessConfig{Enabled: true, AcceptingNew: true, AllowedUserID: 7, AllowedAPIKeyID: 9, Routes: map[string]config.NativeHarnessRoute{"synthetic-test": {Origin: origin, CredentialEnv: "TEST_TOKEN", Source: "source", LedgerInstance: "ledger", BindingID: "binding", Revision: 1, ModelAlias: "synthetic-test", NativeModel: "fake-text"}}}
}
func newTestService(origin string) *Service {
	s := New(configured(origin), &memoryStore{rows: map[string]Operation{}})
	s.Getenv = func(string) string { return "synthetic-test-only-token" }
	return s
}
func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	e, ok := err.(*Error)
	if !ok || e.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
}
func TestNormalizeStrict(t *testing.T) {
	for _, raw := range []string{
		`{"model":"synthetic-test","model":"synthetic-test","messages":[]}`,
		`{"model":"synthetic-test","messages":[{"role":"user","role":"user","content":"ok"}]}`,
		strings.Replace(request, "hello", "", 1),
		strings.Replace(request, "hello", `\ud800`, 1),
		strings.Replace(request, "hello", `\udc00`, 1),
		strings.Replace(request, "hello", string([]byte{0xff}), 1),
		strings.Replace(request, `"user"`, `"system"`, 1),
		strings.Replace(request, `"hello"`, `[]`, 1),
		strings.TrimSuffix(request, "}") + `,"n":true}`,
		strings.TrimSuffix(request, "}") + `,"n":1.0}`,
		strings.TrimSuffix(request, "}") + `,"stream":1}`,
		strings.TrimSuffix(request, "}") + `,"tools":[]}`,
		strings.TrimSuffix(request, "}") + `,"user":[]}`,
	} {
		if _, err := normalize([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	a, err := normalize([]byte(request))
	if err != nil {
		t.Fatal(err)
	}
	b, err := normalize([]byte(strings.TrimSuffix(request, "}") + `,"n":1,"stream":false,"user":"ignored"}`))
	if err != nil || string(canonical(a)) != string(canonical(b)) {
		t.Fatal("normalization differs", err)
	}
}
func TestFrozenRouteAndTrustedHeaders(t *testing.T) {
	var hits atomic.Int32
	var original string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		for name, want := range map[string]string{"X-Native-Tenant": "user-7", "X-Native-Principal": "key-9", "Authorization": "Bearer synthetic-test-only-token", "X-Native-Source": "source", "X-Native-Ledger": "ledger"} {
			if r.Header.Get(name) != want {
				t.Errorf("%s not trusted", name)
			}
		}
		if r.Header.Get("Idempotency-Key") != "" {
			t.Error("retry-triggering header forwarded")
		}
		id := r.Header.Get("X-Native-Operation")
		if original == "" {
			original = id
		} else if id != original {
			t.Error("operation changed")
		}
		// Same canonical hash as Python's sort_keys/compact/ensure_ascii=False binding.
		want := hash([]byte(`{"binding_id":"binding","model_alias":"synthetic-test","native_model":"fake-text","revision":1}`))
		if r.Header.Get("X-Native-Binding-Digest") != want {
			t.Error("binding digest differs")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, completion)
	}))
	defer server.Close()
	s := newTestService(server.URL)
	if _, e := s.Invoke(context.Background(), 7, 9, "stable", true, []byte(request)); e != nil {
		t.Fatal(e)
	}
	s.Config.Routes = nil
	s.Config.AcceptingNew = false
	if _, e := s.Invoke(context.Background(), 7, 9, "stable", false, []byte(request)); e != nil {
		t.Fatal(e)
	}
	if hits.Load() != 2 {
		t.Fatal("replay not sent to original route")
	}
	_, e := s.Invoke(context.Background(), 7, 9, "stable", true, []byte(strings.Replace(request, "hello", "different", 1)))
	requireCode(t, e, "IDEMPOTENCY_CONFLICT")
	_, e = s.Invoke(context.Background(), 7, 9, "new", true, []byte(request))
	requireCode(t, e, "NEW_ADMISSION_DISABLED")
	_, e = s.Invoke(context.Background(), 7, 10, "stable", true, []byte(request))
	requireCode(t, e, "NATIVE_FORBIDDEN")
	s.Getenv = func(string) string { return "" }
	_, e = s.Invoke(context.Background(), 7, 9, "stable", true, []byte(request))
	requireCode(t, e, "ROUTE_CREDENTIAL_UNAVAILABLE")
	if hits.Load() != 2 {
		t.Fatal("rejected request had egress")
	}
}
func TestConcurrentFreeze(t *testing.T) {
	var mu sync.Mutex
	ids := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ids[r.Header.Get("X-Native-Operation")] = true
		mu.Unlock()
		fmt.Fprint(w, completion)
	}))
	defer server.Close()
	s := newTestService(server.URL)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.Invoke(context.Background(), 7, 9, "stable", true, []byte(request)); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if len(ids) != 1 {
		t.Fatalf("%d operations", len(ids))
	}
}
func TestFailureNeverRetriesOrRebinds(t *testing.T) {
	for _, status := range []int{429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var hits atomic.Int32
			var admission []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				admission = append(admission, r.Header.Get("X-Native-Admission-Allowed"))
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":{"code":"TEST_FAILURE"}}`)
			}))
			defer server.Close()
			s := newTestService(server.URL)
			_, e := s.Invoke(context.Background(), 7, 9, "stable", true, []byte(request))
			requireCode(t, e, "TEST_FAILURE")
			if hits.Load() != 1 {
				t.Fatal("implicit retry")
			}
			s.Config.Routes = nil
			_, e = s.Invoke(context.Background(), 7, 9, "stable", false, []byte(request))
			requireCode(t, e, "TEST_FAILURE")
			if hits.Load() != 2 || admission[0] != "true" || admission[1] != "false" {
				t.Fatal("receipt-unknown retry changed permission or target")
			}
		})
	}
}
func TestStrictResponse(t *testing.T) {
	for _, raw := range []string{`{"ok":true}`, strings.Replace(completion, `"done"`, `""`, 1), strings.Replace(completion, `"model":"synthetic-test"`, `"model":"other"`, 1), strings.TrimSuffix(completion, "}") + `,"usage":{"total_tokens":0}}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, raw) }))
		s := newTestService(server.URL)
		_, e := s.Invoke(context.Background(), 7, 9, "stable", true, []byte(request))
		server.Close()
		requireCode(t, e, "NATIVE_INVALID_COMPLETION")
	}
}
func TestTruncatedSocketNoRetry(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	var hits atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		hits.Add(1)
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(completion)+100, completion)
	}()
	s := newTestService("http://" + listener.Addr().String())
	_, e = s.Invoke(context.Background(), 7, 9, "stable", true, []byte(request))
	requireCode(t, e, "NATIVE_RECEIPT_UNKNOWN")
	<-done
	if hits.Load() != 1 {
		t.Fatal("retried")
	}
	if _, e = s.Store.Lookup(context.Background(), 7, 9, "stable"); e != nil {
		t.Fatal("route lost")
	}
}
func TestRouteValidation(t *testing.T) {
	base := configured("http://127.0.0.1:1234").Routes["synthetic-test"]
	for _, origin := range []string{"http://example.com", "https://example.com/path", "https://u:p@example.com", "https://example.com?x=1", "file:///tmp/x"} {
		r := base
		r.Origin = origin
		if ValidRoute(r.ModelAlias, r) {
			t.Fatal(origin)
		}
	}
	if ValidRoute("other", base) {
		t.Fatal("alias mismatch")
	}
	base.NativeModel = "real-model"
	if ValidRoute("synthetic-test", base) {
		t.Fatal("real model")
	}
}

func TestUnicodePreserved(t *testing.T) {
	for _, text := range []string{`\ud83d\ude00`, `合法😀`, `\\ud800`, `slash\/ok`} {
		if _, e := normalize([]byte(strings.Replace(request, "hello", text, 1))); e != nil {
			t.Fatal(text, e)
		}
	}
}

func TestCredentialRotationKeepsFrozenAuthority(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Native-Source") != "source" || r.Header.Get("X-Native-Ledger") != "ledger" {
			t.Error("frozen authority changed")
		}
		if calls == 2 && (r.Header.Get("Authorization") != "Bearer rotated-test-only-token" || r.Header.Get("X-Native-Admission-Allowed") != "false") {
			t.Error("rotation or admission contract")
		}
		fmt.Fprint(w, completion)
	}))
	defer server.Close()
	s := newTestService(server.URL)
	if _, e := s.Invoke(context.Background(), 7, 9, "stable", true, []byte(request)); e != nil {
		t.Fatal(e)
	}
	route := s.Config.Routes["synthetic-test"]
	route.Source = "new-source"
	route.LedgerInstance = "new-ledger"
	s.Config.Routes["synthetic-test"] = route
	s.Getenv = func(string) string { return "rotated-test-only-token" }
	if _, e := s.Invoke(context.Background(), 7, 9, "stable", true, []byte(request)); e != nil {
		t.Fatal(e)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}

func TestRefusalAndNoRedirectFallback(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			fmt.Fprint(w, strings.Replace(completion, `"content":"done"`, `"content":null,"refusal":"No"`, 1))
			return
		}
		w.Header().Set("Location", "/unexpected")
		w.WriteHeader(302)
		fmt.Fprint(w, `{"error":{"code":"REDIRECT_FORBIDDEN"}}`)
	}))
	defer server.Close()
	s := newTestService(server.URL)
	if _, e := s.Invoke(context.Background(), 7, 9, "stable", true, []byte(request)); e != nil {
		t.Fatal(e)
	}
	_, e := s.Invoke(context.Background(), 7, 9, "stable", true, []byte(request))
	requireCode(t, e, "REDIRECT_FORBIDDEN")
	if hits != 2 {
		t.Fatal("redirect followed")
	}
	_, e = s.Invoke(context.Background(), 7, 9, "new", false, []byte(request))
	requireCode(t, e, "NEW_ADMISSION_DISABLED")
	if hits != 2 {
		t.Fatal("unpermitted egress")
	}
}

// Each case traverses an actual HTTP server and the full Invoke path. A malformed
// 200 is an uncertain/invalid response, never authority to retry or rebind.
func TestCompletionDTOHTTPContract(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any, map[string]any, map[string]any)
		valid  bool
	}{
		{"text", func(_, _, _ map[string]any) {}, true},
		{"zero created", func(v, _, _ map[string]any) { v["created"] = 0 }, true},
		{"refusal only", func(_, _, m map[string]any) { m["content"] = nil; m["refusal"] = "Cannot comply" }, true},
		{"text and refusal", func(_, _, m map[string]any) { m["refusal"] = "Limited" }, true},
		{"missing id", func(v, _, _ map[string]any) { delete(v, "id") }, false},
		{"empty id", func(v, _, _ map[string]any) { v["id"] = "" }, false},
		{"wrong id type", func(v, _, _ map[string]any) { v["id"] = 123 }, false},
		{"wrong id prefix", func(v, _, _ map[string]any) { v["id"] = "other" }, false},
		{"empty task id", func(v, _, _ map[string]any) { v["id"] = "chatcmpl-" }, false},
		{"invalid task id", func(v, _, _ map[string]any) { v["id"] = "chatcmpl-task/unsafe" }, false},
		{"missing created", func(v, _, _ map[string]any) { delete(v, "created") }, false},
		{"created string", func(v, _, _ map[string]any) { v["created"] = "1" }, false},
		{"created bool", func(v, _, _ map[string]any) { v["created"] = true }, false},
		{"created negative", func(v, _, _ map[string]any) { v["created"] = -1 }, false},
		{"created fraction", func(v, _, _ map[string]any) { v["created"] = 1.5 }, false},
		{"created decimal", func(v, _, _ map[string]any) { v["created"] = json.Number("1.0") }, false},
		{"created exponent", func(v, _, _ map[string]any) { v["created"] = json.Number("1e3") }, false},
		{"created overflow", func(v, _, _ map[string]any) { v["created"] = json.Number("9223372036854775808") }, false},
		{"root extra", func(v, _, _ map[string]any) { v["system_fingerprint"] = "unsupported" }, false},
		{"usage null", func(v, _, _ map[string]any) { v["usage"] = nil }, false},
		{"choice extra", func(_, c, _ map[string]any) { c["logprobs"] = nil }, false},
		{"missing choice index", func(_, c, _ map[string]any) { delete(c, "index") }, false},
		{"wrong choice index", func(_, c, _ map[string]any) { c["index"] = true }, false},
		{"unsupported finish", func(_, c, _ map[string]any) { c["finish_reason"] = "tool_calls" }, false},
		{"missing content", func(_, _, m map[string]any) { delete(m, "content"); m["refusal"] = "no" }, false},
		{"array content with refusal", func(_, _, m map[string]any) { m["content"] = []any{}; m["refusal"] = "no" }, false},
		{"object content with refusal", func(_, _, m map[string]any) { m["content"] = map[string]any{}; m["refusal"] = "no" }, false},
		{"number content with refusal", func(_, _, m map[string]any) { m["content"] = 123; m["refusal"] = "no" }, false},
		{"bool content with refusal", func(_, _, m map[string]any) { m["content"] = false; m["refusal"] = "no" }, false},
		{"null content no refusal", func(_, _, m map[string]any) { m["content"] = nil }, false},
		{"null refusal with text", func(_, _, m map[string]any) { m["refusal"] = nil }, false},
		{"array refusal with text", func(_, _, m map[string]any) { m["refusal"] = []any{} }, false},
		{"number refusal with text", func(_, _, m map[string]any) { m["refusal"] = 123 }, false},
		{"empty refusal with text", func(_, _, m map[string]any) { m["refusal"] = " " }, false},
		{"function call", func(_, _, m map[string]any) { m["function_call"] = map[string]any{"name": "execute"} }, false},
		{"null function call", func(_, _, m map[string]any) { m["function_call"] = nil }, false},
		{"tool calls", func(_, _, m map[string]any) { m["tool_calls"] = []any{} }, false},
		{"null tool calls", func(_, _, m map[string]any) { m["tool_calls"] = nil }, false},
		{"message extra", func(_, _, m map[string]any) { m["audio"] = nil }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, e := decode([]byte(completion))
			if e != nil {
				t.Fatal(e)
			}
			choice := value["choices"].([]any)[0].(map[string]any)
			message := choice["message"].(map[string]any)
			tc.mutate(value, choice, message)
			raw := canonical(value)
			var hits atomic.Int32
			var mu sync.Mutex
			ids := map[string]bool{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				mu.Lock()
				ids[r.Header.Get("X-Native-Operation")] = true
				mu.Unlock()
				fmt.Fprint(w, string(raw))
			}))
			defer server.Close()
			s := newTestService(server.URL)
			for attempt := int32(1); attempt <= 2; attempt++ {
				result, e := s.Invoke(context.Background(), 7, 9, "same-operation", true, []byte(request))
				if tc.valid {
					if e != nil || result == nil || string(result.Body) != string(raw) {
						t.Fatalf("valid response rejected: %v", e)
					}
				} else {
					requireCode(t, e, "NATIVE_INVALID_COMPLETION")
					if result != nil {
						t.Fatal("invalid result leaked")
					}
				}
				if hits.Load() != attempt {
					t.Fatalf("implicit retry: %d requests for %d calls", hits.Load(), attempt)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if len(ids) != 1 {
				t.Fatal("operation regenerated")
			}
		})
	}
}
