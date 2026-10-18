package adminapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestClientAllowlistedReadsSyncAndMappingOnlyWrite(t *testing.T) {
	var seen []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/admin/model-catalog-refresh/status":
			assertAPIKey(t, r)
			_, _ = io.WriteString(w, `{"code":0,"data":{"enabled":true,"timezone":"Asia/Shanghai","last_run_status":"succeeded","accounts":[]}}`)
		case "/api/v1/admin/accounts/42":
			assertAPIKey(t, r)
			_, _ = io.WriteString(w, `{"code":0,"data":{"id":42,"platform":"openai","type":"apikey","api_protocol":"openai","base_url":"https://provider.example/v1","credentials_status":{"has_api_key":true,"has_refresh_token":true},"credentials":{"api_key":"provider-secret","header_overrides":{"Authorization":"header-secret"},"model_mapping":{"public":"upstream"}}}}`)
		case "/api/v1/admin/accounts/42/models/sync-upstream":
			assertAPIKey(t, r)
			if r.Method != http.MethodPost {
				t.Errorf("sync method = %s", r.Method)
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"models":["upstream","new-model"],"live_list_available":true,"warnings":[]}}`)
		case "/api/v1/admin/accounts/bulk-update":
			assertAPIKey(t, r)
			if r.Method != http.MethodPost {
				t.Errorf("bulk update method = %s", r.Method)
			}
			body, _ := io.ReadAll(r.Body)
			var got map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Errorf("invalid update JSON")
			}
			if len(got) != 2 || got["account_ids"] == nil || got["filters"] != nil {
				t.Errorf("bulk update was not single-account scoped")
			}
			credentials, ok := got["credentials"].(map[string]any)
			if !ok || len(credentials) != 1 || credentials["model_mapping"] == nil {
				t.Errorf("credentials update must contain only model_mapping")
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"success_ids":[42],"failed_ids":[]}}`)
		default:
			t.Errorf("unexpected API path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	client := newHandlerClient(t, handler)
	ctx := context.Background()
	if err := client.SetAPIKey([]byte("test-admin-key")); err != nil {
		t.Fatal(err)
	}
	status, err := client.RefreshStatus(ctx)
	if err != nil || !status.Enabled || status.RunStatus != "succeeded" {
		t.Fatalf("unexpected status: %#v, %v", status, err)
	}
	account, err := client.Account(ctx, 42)
	if err != nil || account.ModelMapping["public"] != "upstream" {
		t.Fatalf("unexpected account: %#v, %v", account, err)
	}
	catalog, err := client.SyncUpstream(ctx, 42)
	if err != nil || !catalog.LiveListAvailable || len(catalog.Models) != 2 {
		t.Fatalf("unexpected catalog: %#v, %v", catalog, err)
	}
	if _, err := client.BulkUpdateMapping(ctx, 42, map[string]string{"public": "upstream"}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GET /api/v1/admin/model-catalog-refresh/status",
		"GET /api/v1/admin/accounts/42",
		"POST /api/v1/admin/accounts/42/models/sync-upstream",
		"POST /api/v1/admin/accounts/bulk-update",
	}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Fatalf("requests = %v, want %v", seen, want)
	}
}

func TestListAllAccountsReadsEveryPageAndRetainsOnlySafeSummary(t *testing.T) {
	var pageRequests int
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertAPIKey(t, r)
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/admin/accounts" || r.URL.Query().Get("page_size") != "1000" || r.URL.Query().Get("lite") != "1" || r.URL.Query().Get("sort_by") != "id" || r.URL.Query().Get("sort_order") != "asc" {
			t.Errorf("unexpected account-list request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		pageRequests++
		page := r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		if page == "1" {
			_, _ = io.WriteString(w, `{"code":0,"data":{"items":[`)
			for id := 1; id <= 1000; id++ {
				if id > 1 {
					_, _ = io.WriteString(w, ",")
				}
				if id == 1 {
					_, _ = fmt.Fprintf(w, `{"id":%d,"platform":"openai","type":"apikey","status":"active","credentials":{"api_key":"secret-marker","model_mapping":{}},"extra":{"header":"secret-marker"}}`, id)
				} else {
					_, _ = fmt.Fprintf(w, `{"id":%d,"platform":"openai","type":"apikey","status":"active","credentials":{"api_key":"provider-secret"}}`, id)
				}
			}
			_, _ = io.WriteString(w, `],"total":1001,"page":1,"page_size":1000,"pages":2}}`)
			return
		}
		if page == "2" {
			_, _ = io.WriteString(w, `{"code":0,"data":{"items":[{"id":1001,"platform":"anthropic","type":"apikey","status":"active","credentials":{"api_key":"secret-marker"}}],"total":1001,"page":2,"page_size":1000,"pages":2}}`)
			return
		}
		t.Errorf("unexpected account-list page: %q", page)
	})
	client := newHandlerClient(t, handler)
	if err := client.SetAPIKey([]byte("test-admin-key")); err != nil {
		t.Fatal(err)
	}
	accounts, err := client.ListAllAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pageRequests != 2 || len(accounts) != 1001 || accounts[0].ID != 1 || accounts[1000].ID != 1001 || accounts[1000].Platform != "anthropic" {
		t.Fatalf("account inventory incomplete: requests=%d count=%d first=%#v last=%#v", pageRequests, len(accounts), accounts[0], accounts[len(accounts)-1])
	}
	serialized, _ := json.Marshal(accounts)
	if strings.Contains(string(serialized), "secret-marker") || strings.Contains(string(serialized), "provider-secret") {
		t.Fatal("account list projection retained credential fields")
	}
}

func TestListAllAccountsRejectsIncompletePaginationAndDuplicateIDs(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "missing pages", body: `{"code":0,"data":{"items":[{"id":1}],"total":1,"page":1,"page_size":1000}}`},
		{name: "duplicate ID", body: `{"code":0,"data":{"items":[{"id":1},{"id":1}],"total":2,"page":1,"page_size":1000,"pages":1}}`},
		{name: "short page", body: `{"code":0,"data":{"items":[{"id":1}],"total":2,"page":1,"page_size":1000,"pages":1}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assertAPIKey(t, r)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			})
			client := newHandlerClient(t, handler)
			if err := client.SetAPIKey([]byte("test-admin-key")); err != nil {
				t.Fatal(err)
			}
			if _, err := client.ListAllAccounts(context.Background()); err == nil {
				t.Fatal("incomplete or inconsistent list was accepted")
			}
		})
	}
}

func TestAccountResponseNeverReturnsCredentialSecrets(t *testing.T) {
	const providerSecret = "provider-secret-marker"
	const headerSecret = "header-secret-marker"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertAPIKey(t, r)
		_, _ = fmt.Fprintf(w, `{"code":0,"data":{"id":7,"platform":"openai","type":"apikey","credentials_status":{"has_api_key":true},"credentials":{"api_protocol":"openai","base_url":"https://provider.example/v1","api_key":%q,"header_overrides":{"Authorization":%q},"model_mapping":{"a":"b"}}}}`, providerSecret, headerSecret)
	})
	client := newHandlerClient(t, handler)
	if err := client.SetAPIKey([]byte("test-admin-key")); err != nil {
		t.Fatal(err)
	}
	account, err := client.Account(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	serialized, _ := json.Marshal(account)
	if strings.Contains(string(serialized), providerSecret) || strings.Contains(string(serialized), headerSecret) {
		t.Fatal("account projection returned credential secrets")
	}
	if account.Protocol != "openai" || account.BaseURL != "https://provider.example/v1" || account.ModelMapping["a"] != "b" {
		t.Fatalf("nested safe account fields were not decoded: %#v", account)
	}
	if account.APIKeyPresent == nil || !*account.APIKeyPresent {
		t.Fatal("redacted API-key-presence marker was not decoded")
	}
}

func TestAccountDecodePreservesMissingNestedAPIProtocolAsEmpty(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertAPIKey(t, r)
		_, _ = io.WriteString(w, `{"code":0,"data":{"id":9001,"platform":"openai","type":"apikey","credentials_status":{"has_api_key":true},"credentials":{"base_url":"https://provider.example/v1","model_mapping":{"model-a":"model-a"}}}}`)
	})
	client := newHandlerClient(t, handler)
	if err := client.SetAPIKey([]byte("test-admin-key")); err != nil {
		t.Fatal(err)
	}
	account, err := client.Account(context.Background(), 9001)
	if err != nil {
		t.Fatal(err)
	}
	if account.Protocol != "" || account.BaseURL != "https://provider.example/v1" || account.ModelMapping["model-a"] != "model-a" {
		t.Fatalf("missing protocol was inferred or safe nested fields were lost: %#v", account)
	}
}

func TestAccountPreservesModelMappingPresence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		data    string
		present bool
		wantLen int
	}{
		{
			name:    "credentials object without mapping",
			data:    `{"id":7,"platform":"openai","type":"apikey","status":"error","credentials":{}}`,
			present: false,
		},
		{
			name:    "explicit empty nested mapping",
			data:    `{"id":7,"platform":"openai","type":"apikey","credentials":{"model_mapping":{}}}`,
			present: true,
		},
		{
			name:    "legacy top-level mapping",
			data:    `{"id":7,"platform":"openai","type":"apikey","model_mapping":{"public":"upstream"}}`,
			present: true,
			wantLen: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assertAPIKey(t, r)
				_, _ = fmt.Fprintf(w, `{"code":0,"data":%s}`, tc.data)
			})
			client := newHandlerClient(t, handler)
			if err := client.SetAPIKey([]byte("test-admin-key")); err != nil {
				t.Fatal(err)
			}
			account, err := client.Account(context.Background(), 7)
			if err != nil {
				t.Fatal(err)
			}
			if account.HasModelMapping() != tc.present || len(account.ModelMapping) != tc.wantLen {
				t.Fatalf("model mapping presence changed: present=%v mapping=%#v want_present=%v want_len=%d", account.HasModelMapping(), account.ModelMapping, tc.present, tc.wantLen)
			}
		})
	}
}

func TestAccountOnlyKeepsRedactedAPIKeyPresenceBoolean(t *testing.T) {
	for _, tc := range []struct {
		name string
		json string
		want *bool
	}{
		{name: "true", json: `{"has_api_key":true}`, want: boolPointerTest(true)},
		{name: "false", json: `{"has_api_key":false,"has_refresh_token":true}`, want: boolPointerTest(false)},
		{name: "missing", json: `{}`, want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assertAPIKey(t, r)
				_, _ = io.WriteString(w, `{"code":0,"data":{"id":7,"platform":"openai","type":"apikey","api_protocol":"openai","base_url":"https://provider.example","credentials_status":`+tc.json+`,"credentials":{"model_mapping":{"a":"b"}}}}`)
			})
			client := newHandlerClient(t, handler)
			if err := client.SetAPIKey([]byte("test-admin-key")); err != nil {
				t.Fatal(err)
			}
			account, err := client.Account(context.Background(), 7)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == nil {
				if account.APIKeyPresent != nil {
					t.Fatalf("unexpected presence marker: %v", *account.APIKeyPresent)
				}
			} else if account.APIKeyPresent == nil || *account.APIKeyPresent != *tc.want {
				t.Fatalf("presence marker=%v, want %v", account.APIKeyPresent, *tc.want)
			}
		})
	}
}

func TestAPIErrorDoesNotExposeResponseBodyOrSecrets(t *testing.T) {
	const secret = "server-error-secret-marker"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, secret)
	})
	client := newHandlerClient(t, handler)
	if err := client.SetAPIKey([]byte("test-admin-key")); err != nil {
		t.Fatal(err)
	}
	_, err := client.RefreshStatus(context.Background())
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe API error: %v", err)
	}
}

func TestRedirectIsNotFollowedAndUnsafePathsAreBlocked(t *testing.T) {
	requests := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Redirect(w, r, "/api/v1/admin/accounts/99", http.StatusFound)
	})
	client := newHandlerClient(t, handler)
	if err := client.SetAPIKey([]byte("test-admin-key")); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RefreshStatus(context.Background()); err == nil {
		t.Fatal("expected redirect status to fail")
	}
	if requests != 1 {
		t.Fatalf("redirect was followed: requests=%d", requests)
	}
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/accounts/42/models"},
		{http.MethodGet, "/api/v1/admin/accounts/42/models/sync-upstream"},
		{http.MethodPost, "/api/v1/admin/accounts/42/models"},
		{http.MethodPut, "/api/v1/admin/accounts/42"},
		{http.MethodPatch, "/api/v1/admin/accounts/42"},
		{http.MethodDelete, "/api/v1/admin/accounts/42"},
		{http.MethodPost, "/api/v1/admin/accounts"},
		{http.MethodDelete, "/api/v1/admin/accounts/42/models/sync-upstream"},
		{http.MethodPost, "/api/v1/admin/accounts/42/models/sync-upstream-preview"},
		{http.MethodPost, "/api/v1/admin/accounts/models/sync-upstream-preview"},
		{http.MethodPost, "/api/v1/admin/accounts/042/models/sync-upstream"},
		{http.MethodPost, "/v1/responses"},
		{http.MethodPost, "/v1/responses/compact"},
		{http.MethodPost, "/v1/chat/completions"},
		{http.MethodPost, "/v1/images/generations"},
		{http.MethodPost, "/v1/videos"},
		{http.MethodPost, "/api/v1/admin/accounts/bulk-update/anything"},
		{http.MethodPost, "/api/v1/auth/login"},
	} {
		if allowed(tc.method, tc.path) {
			t.Errorf("unsafe path allowed: %s %s", tc.method, tc.path)
		}
		if err := client.request(context.Background(), tc.method, tc.path, nil, nil); err == nil {
			t.Errorf("unsafe request was accepted: %s %s", tc.method, tc.path)
		}
	}
	if requests != 1 {
		t.Fatalf("blocked paths still reached the mock server: requests=%d", requests)
	}
}

func TestOversizedResponseFailsWithoutReturningBody(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"code":0,"data":{"access_token":"x"},"padding":"`)
		_, _ = io.CopyN(w, strings.NewReader(strings.Repeat("a", maxResponseBytes+1024)), maxResponseBytes+1024)
		_, _ = io.WriteString(w, `"}`)
	})
	client := newHandlerClient(t, handler)
	if err := client.SetAPIKey([]byte("test-admin-key")); err != nil {
		t.Fatal(err)
	}
	_, err := client.RefreshStatus(context.Background())
	if err == nil || strings.Contains(err.Error(), "aaaa") {
		t.Fatalf("oversized response was not safely rejected: %v", err)
	}
}

func assertAPIKey(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Header.Get("x-api-key") != "test-admin-key" || r.Header.Get("Authorization") != "" {
		t.Errorf("missing API-key auth or unexpected Authorization header")
	}
}

func boolPointerTest(value bool) *bool { return &value }

type handlerRoundTripper struct{ handler http.Handler }

func (h handlerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, req)
	return recorder.Result(), nil
}

func newHandlerClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	base, err := url.Parse("https://404token.xyz")
	if err != nil {
		t.Fatal(err)
	}
	client := newClient(base)
	client.http.Transport = handlerRoundTripper{handler: handler}
	t.Cleanup(client.Close)
	return client
}

func TestNewPinsTrustedOriginAndRequestsRequireKey(t *testing.T) {
	valid, err := New("https://404token.xyz")
	if err != nil {
		t.Fatalf("trusted target rejected: %v", err)
	}
	valid.Close()
	for _, target := range []string{"https://example.invalid", "http://404token.xyz", "https://404token.xyz:443", "http://127.0.0.1:18081"} {
		if _, err := New(target); err == nil {
			t.Errorf("non-trusted destination accepted: %q", target)
		}
	}
	requests := 0
	client := newHandlerClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
	}))
	if _, err := client.RefreshStatus(context.Background()); err == nil {
		t.Fatal("request without an API key succeeded")
	}
	if requests != 0 {
		t.Fatalf("request without an API key reached transport: %d", requests)
	}
}
