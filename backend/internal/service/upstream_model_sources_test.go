package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

const modelSourceTestAPIKey = "model-source-test-secret"

// httptestModelSourceUpstream routes the service's trusted-origin request to a
// local test server while keeping the original URL available to assertions.
type httptestModelSourceUpstream struct {
	server   *httptest.Server
	client   *http.Client
	requests []*http.Request
}

func (u *httptestModelSourceUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.do(req)
}

func (u *httptestModelSourceUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.do(req)
}

func (u *httptestModelSourceUpstream) do(req *http.Request) (*http.Response, error) {
	u.requests = append(u.requests, req.Clone(req.Context()))
	wireRequest := req.Clone(req.Context())
	localURL, err := url.Parse(u.server.URL)
	if err != nil {
		return nil, err
	}
	localURL.Path = req.URL.Path
	localURL.RawPath = ""
	localURL.RawQuery = req.URL.RawQuery
	wireRequest.URL = localURL
	wireRequest.Host = req.URL.Host

	response, err := u.client.Do(wireRequest)
	if response != nil {
		// The local server stands in for the trusted official origin.
		response.Request = req
	}
	return response, err
}

func newHttptestModelSourceUpstream(handler http.Handler) *httptestModelSourceUpstream {
	server := httptest.NewServer(handler)
	return &httptestModelSourceUpstream{server: server, client: server.Client()}
}

func (u *httptestModelSourceUpstream) close() {
	u.server.Close()
}

func modelSourceTestAccount(kind, baseURL string) *Account {
	credentials := map[string]any{"api_key": modelSourceTestAPIKey}
	if kind == "anthropic" {
		credentials["base_url"] = baseURL
		return &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: credentials}
	}
	credentials["base_url"] = baseURL
	return &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: credentials}
}

func modelSourceTestResponse(kind string, ids []string, hasMore bool, lastID string, total int, padding string) []byte {
	if kind == "anthropic" {
		data := make([]map[string]string, 0, len(ids))
		for _, id := range ids {
			data = append(data, map[string]string{"id": id})
		}
		payload := map[string]any{"data": data, "has_more": hasMore, "last_id": lastID}
		if padding != "" {
			payload["padding"] = padding
		}
		body, _ := json.Marshal(payload)
		return body
	}

	models := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		models = append(models, map[string]string{"model": id})
	}
	output := map[string]any{"total": total, "models": models}
	payload := map[string]any{"success": true, "output": output}
	if padding != "" {
		payload["padding"] = padding
	}
	body, _ := json.Marshal(payload)
	return body
}

func writeModelSourceTestResponse(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func modelSourceTestResponseExactSize(t *testing.T, kind string, ids []string, hasMore bool, lastID string, total, targetBytes int) []byte {
	t.Helper()
	base := modelSourceTestResponse(kind, ids, hasMore, lastID, total, "")
	require.LessOrEqual(t, len(base), targetBytes)
	withOneByte := modelSourceTestResponse(kind, ids, hasMore, lastID, total, "x")
	paddingOverhead := len(withOneByte) - len(base) - 1
	paddingBytes := targetBytes - len(base) - paddingOverhead
	require.GreaterOrEqual(t, paddingBytes, 1)
	body := modelSourceTestResponse(kind, ids, hasMore, lastID, total, strings.Repeat("x", paddingBytes))
	require.Len(t, body, targetBytes)
	return body
}

func requireModelSourceUpstreamError(t *testing.T, err error) *UpstreamModelSyncError {
	t.Helper()
	var syncErr *UpstreamModelSyncError
	require.Error(t, err)
	require.True(t, errors.As(err, &syncErr), "expected UpstreamModelSyncError, got %T", err)
	require.Equal(t, UpstreamModelSyncErrorUpstream, syncErr.Kind)
	return syncErr
}

func TestFetchUpstreamAvailabilityModelsAnthropicPaginationUsesTrustedOriginAndScopedAuth(t *testing.T) {
	upstream := newHttptestModelSourceUpstream(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after_id") == "" {
			writeModelSourceTestResponse(w, modelSourceTestResponse("anthropic", []string{"claude-sonnet-4-5-20250929"}, true, "cursor-1", 0, ""))
			return
		}
		writeModelSourceTestResponse(w, modelSourceTestResponse("anthropic", []string{"claude-opus-4-1-20250805"}, false, "", 0, ""))
	}))
	defer upstream.close()

	svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
	profile, ids, _, err := svc.FetchUpstreamAvailabilityModels(context.Background(), modelSourceTestAccount("anthropic", "https://api.anthropic.com"))
	require.NoError(t, err)
	require.Equal(t, "anthropic-official", profile.ID)
	require.Equal(t, []string{"claude-sonnet-4-5-20250929", "claude-opus-4-1-20250805"}, ids)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "https://api.anthropic.com/v1/models?limit=100", upstream.requests[0].URL.String())
	require.Equal(t, "https://api.anthropic.com/v1/models?after_id=cursor-1&limit=100", upstream.requests[1].URL.String())
	for _, req := range upstream.requests {
		require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()), "credential-bearing catalog requests must not follow redirect targets")
		require.Equal(t, "/v1/models", req.URL.Path)
		require.Equal(t, "100", req.URL.Query().Get("limit"))
		require.Equal(t, modelSourceTestAPIKey, req.Header.Get("x-api-key"))
		require.Equal(t, "2023-06-01", req.Header.Get("anthropic-version"))
		require.Empty(t, req.Header.Get("Authorization"))
		require.NotContains(t, req.URL.RawQuery, modelSourceTestAPIKey)
	}
	require.Empty(t, upstream.requests[0].URL.Query().Get("after_id"))
	require.Equal(t, "cursor-1", upstream.requests[1].URL.Query().Get("after_id"))
}

func TestFetchUpstreamAvailabilityModelsAnthropicRejectsMissingPageFields(t *testing.T) {
	tests := []struct {
		name      string
		finalPage string
		wantError string
	}{
		{
			name:      "missing data",
			finalPage: `{"has_more":false,"last_id":"cursor-2"}`,
			wantError: "omitted its data array",
		},
		{
			name:      "missing has_more",
			finalPage: `{"data":[{"id":"claude-final"}],"last_id":"cursor-2"}`,
			wantError: "omitted has_more",
		},
		{
			name:      "null data",
			finalPage: `{"data":null,"has_more":false}`,
			wantError: "omitted its data array",
		},
		{
			name:      "invalid has_more type",
			finalPage: `{"data":[{"id":"claude-final"}],"has_more":"false"}`,
			wantError: "has_more must be a boolean",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := 0
			upstream := newHttptestModelSourceUpstream(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				page++
				if page == 1 {
					writeModelSourceTestResponse(w, modelSourceTestResponse("anthropic", []string{"claude-first"}, true, "cursor-1", 0, ""))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.finalPage)
			}))
			defer upstream.close()

			svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
			_, ids, _, err := svc.FetchUpstreamAvailabilityModels(context.Background(), modelSourceTestAccount("anthropic", "https://api.anthropic.com"))
			require.Empty(t, ids, "an incomplete catalog must not return earlier-page IDs")
			require.ErrorContains(t, err, tt.wantError)
			require.Len(t, upstream.requests, 2)
		})
	}
}

func TestFetchUpstreamAvailabilityModelsAcceptsExactlyMaxPagesAndRejectsMore(t *testing.T) {
	for _, tc := range []struct {
		name        string
		remotePages int
		wantError   bool
	}{
		{name: "exact page cap", remotePages: modelSourceMaxPages},
		{name: "provider reports one more page", remotePages: modelSourceMaxPages + 1, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := 0
			upstream := newHttptestModelSourceUpstream(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				page++
				id := fmt.Sprintf("model-%03d", page)
				hasMore := page < tc.remotePages
				writeModelSourceTestResponse(w, modelSourceTestResponse("anthropic", []string{id}, hasMore, fmt.Sprintf("cursor-%03d", page), 0, ""))
			}))
			defer upstream.close()
			svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
			_, ids, _, err := svc.FetchUpstreamAvailabilityModels(context.Background(), modelSourceTestAccount("anthropic", "https://api.anthropic.com"))
			if tc.wantError {
				require.ErrorContains(t, err, "exceeded the page limit")
				require.Empty(t, ids)
			} else {
				require.NoError(t, err)
				require.Len(t, ids, modelSourceMaxPages)
			}
			require.Len(t, upstream.requests, modelSourceMaxPages)
		})
	}
}

func TestFetchUpstreamAvailabilityModelsAlibabaPaginationUsesTrustedOriginAndScopedAuth(t *testing.T) {
	upstream := newHttptestModelSourceUpstream(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page_no") == "1" {
			writeModelSourceTestResponse(w, modelSourceTestResponse("aliyun", []string{"qwen-plus"}, false, "", 2, ""))
			return
		}
		writeModelSourceTestResponse(w, modelSourceTestResponse("aliyun", []string{"qwen-max"}, false, "", 2, ""))
	}))
	defer upstream.close()

	svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
	profile, ids, _, err := svc.FetchUpstreamAvailabilityModels(context.Background(), modelSourceTestAccount("aliyun", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"))
	require.NoError(t, err)
	require.Equal(t, "aliyun-model-studio", profile.ID)
	require.Equal(t, []string{"qwen-plus", "qwen-max"}, ids)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "https://dashscope-intl.aliyuncs.com/api/v1/models?page_no=1&page_size=100&supports=inference", upstream.requests[0].URL.String())
	require.Equal(t, "https://dashscope-intl.aliyuncs.com/api/v1/models?page_no=2&page_size=100&supports=inference", upstream.requests[1].URL.String())
	for _, req := range upstream.requests {
		require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()), "credential-bearing catalog requests must not follow redirect targets")
		require.Equal(t, "/api/v1/models", req.URL.Path)
		require.Equal(t, "inference", req.URL.Query().Get("supports"))
		require.Equal(t, "100", req.URL.Query().Get("page_size"))
		require.Equal(t, modelSourceTestAPIKey, strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "))
		require.Equal(t, "Bearer "+modelSourceTestAPIKey, req.Header.Get("Authorization"))
		require.Empty(t, req.Header.Get("x-api-key"))
		require.NotContains(t, req.URL.RawQuery, modelSourceTestAPIKey)
	}
	require.Equal(t, "1", upstream.requests[0].URL.Query().Get("page_no"))
	require.Equal(t, "2", upstream.requests[1].URL.Query().Get("page_no"))
}

func TestFetchUpstreamAvailabilityModelsKeepsUnverifiedResellersManualOnly(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		baseURL string
	}{
		{name: "Anthropic lookalike origin", kind: "anthropic", baseURL: "https://api.anthropic.com.reseller.example"},
		{name: "Anthropic reseller", kind: "anthropic", baseURL: "https://partner.example"},
		{name: "Alibaba compatible reseller", kind: "aliyun", baseURL: "https://dashscope-proxy.example/compatible-mode/v1"},
		{name: "Alibaba nonstandard port", kind: "aliyun", baseURL: "https://dashscope-intl.aliyuncs.com:8443/compatible-mode/v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{}
			svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
			profile, ids, _, err := svc.FetchUpstreamAvailabilityModels(context.Background(), modelSourceTestAccount(tt.kind, tt.baseURL))
			require.Empty(t, ids)
			require.True(t, profile.ManualOnly)
			require.Equal(t, "manual_only", profile.ID)
			var syncErr *UpstreamModelSyncError
			require.Error(t, err)
			require.True(t, errors.As(err, &syncErr))
			require.Equal(t, UpstreamModelSyncErrorUnsupported, syncErr.Kind)
			require.Empty(t, upstream.requests, "manual-only sources must not receive credentials")
		})
	}
}

func TestFetchUpstreamAvailabilityModelsRejectsCrossOriginRedirectResponsesWithoutExposingKeys(t *testing.T) {
	for _, kind := range []string{"anthropic", "aliyun"} {
		t.Run(kind, func(t *testing.T) {
			// Model a completed redirect by reporting its final request URL. The
			// adapter must reject the response before parsing or accepting IDs.
			redirectURL := &url.URL{Scheme: "https", Host: "redirect.example", Path: "/v1/models"}
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"untrusted-model"}],"has_more":false}`)),
				Request:    &http.Request{URL: redirectURL},
			}}
			svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
			baseURL := "https://api.anthropic.com"
			if kind == "aliyun" {
				baseURL = "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"
			}
			profile, ids, _, err := svc.FetchUpstreamAvailabilityModels(context.Background(), modelSourceTestAccount(kind, baseURL))
			require.Error(t, err)
			wantProfileID := "anthropic-official"
			if kind == "aliyun" {
				wantProfileID = "aliyun-model-studio"
			}
			require.Equal(t, wantProfileID, profile.ID)
			require.Empty(t, ids)
			require.Contains(t, err.Error(), "different origin")
			require.NotContains(t, err.Error(), modelSourceTestAPIKey)
			require.Len(t, upstream.requests, 1)
			if kind == "aliyun" {
				require.Equal(t, "Bearer "+modelSourceTestAPIKey, upstream.requests[0].Header.Get("Authorization"))
				require.Empty(t, upstream.requests[0].Header.Get("x-api-key"))
			} else {
				require.Equal(t, modelSourceTestAPIKey, upstream.requests[0].Header.Get("x-api-key"))
				require.Equal(t, "2023-06-01", upstream.requests[0].Header.Get("anthropic-version"))
			}
		})
	}
}

func TestFetchUpstreamAvailabilityModelsAnthropicPaginationRejectsMissingRepeatedAndExcessiveCursors(t *testing.T) {
	tests := []struct {
		name      string
		pageCount int
		response  func(page int) []byte
		wantErr   string
	}{
		{
			name:      "missing cursor",
			pageCount: 1,
			response: func(int) []byte {
				return modelSourceTestResponse("anthropic", []string{"claude-a"}, true, "", 0, "")
			},
			wantErr: "cursor is missing",
		},
		{
			name:      "repeated cursor",
			pageCount: 2,
			response: func(int) []byte {
				return modelSourceTestResponse("anthropic", []string{"claude-a"}, true, "cursor-loop", 0, "")
			},
			wantErr: "cursor repeated",
		},
		{
			name:      "page cap",
			pageCount: modelSourceMaxPages,
			response: func(page int) []byte {
				return modelSourceTestResponse("anthropic", []string{fmt.Sprintf("claude-%d", page)}, true, fmt.Sprintf("cursor-%d", page), 0, "")
			},
			wantErr: "exceeded the page limit",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := 0
			upstream := newHttptestModelSourceUpstream(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				page++
				writeModelSourceTestResponse(w, tt.response(page))
			}))
			defer upstream.close()
			svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
			_, ids, _, err := svc.FetchUpstreamAvailabilityModels(context.Background(), modelSourceTestAccount("anthropic", "https://api.anthropic.com"))
			require.Empty(t, ids, "incomplete catalogs must not return partial model IDs")
			require.Contains(t, err.Error(), tt.wantErr)
			require.Equal(t, tt.pageCount, len(upstream.requests))
		})
	}
}

func TestFetchUpstreamAvailabilityModelsAlibabaRejectsIncompletePagination(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want string
	}{
		{name: "missing total", body: modelSourceTestResponse("aliyun", []string{"qwen-a"}, false, "", 0, ""), want: "omitted its total count"},
		{name: "empty page before total", body: modelSourceTestResponse("aliyun", nil, false, "", 2, ""), want: "pagination was incomplete"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := newHttptestModelSourceUpstream(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeModelSourceTestResponse(w, tt.body)
			}))
			defer upstream.close()
			svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
			_, ids, _, err := svc.FetchUpstreamAvailabilityModels(context.Background(), modelSourceTestAccount("aliyun", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"))
			require.Empty(t, ids)
			require.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestFetchUpstreamAvailabilityModelsAlibabaRejectsPageTotalChanges(t *testing.T) {
	tests := []struct {
		name          string
		firstTotal    int
		lastTotal     int
		repeatModelID bool
		wantError     string
	}{
		{name: "total grew during pagination", firstTotal: 2, lastTotal: 3, wantError: "total count changed during pagination"},
		{name: "total shrank during pagination", firstTotal: 3, lastTotal: 2, wantError: "total count changed during pagination"},
		{name: "repeated model ID cannot satisfy the total", firstTotal: 2, lastTotal: 2, repeatModelID: true, wantError: "repeated a model ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := 0
			upstream := newHttptestModelSourceUpstream(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				page++
				total := tt.firstTotal
				id := "qwen-1"
				if page > 1 {
					total = tt.lastTotal
					id = "qwen-2"
					if tt.repeatModelID {
						id = "qwen-1"
					}
				}
				writeModelSourceTestResponse(w, modelSourceTestResponse("aliyun", []string{id}, false, "", total, ""))
			}))
			defer upstream.close()
			svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}

			_, ids, _, err := svc.FetchUpstreamAvailabilityModels(context.Background(), modelSourceTestAccount("aliyun", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"))
			require.Empty(t, ids, "a catalog whose page totals changed must not be accepted as complete")
			require.ErrorContains(t, err, tt.wantError)
			require.Equal(t, 2, len(upstream.requests))
		})
	}
}

func TestFetchUpstreamAvailabilityModelsAlibabaRejectsProviderFailure(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"success":false,"output":{"total":0,"models":[]}}`)),
		Request:    &http.Request{URL: &url.URL{Scheme: "https", Host: "dashscope-intl.aliyuncs.com"}},
	}}
	svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
	_, ids, _, err := svc.FetchUpstreamAvailabilityModels(context.Background(), modelSourceTestAccount("aliyun", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"))
	require.Empty(t, ids)
	require.Contains(t, err.Error(), "unsuccessful model list")
}

func TestFetchUpstreamAvailabilityModelsReturnsStatusFailuresForAnthropicAndAlibaba(t *testing.T) {
	for _, kind := range []string{"anthropic", "aliyun"} {
		t.Run(kind, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":"temporarily unavailable"}`)),
				Request:    &http.Request{URL: &url.URL{Scheme: "https", Host: map[string]string{"anthropic": "api.anthropic.com", "aliyun": "dashscope-intl.aliyuncs.com"}[kind]}},
			}}
			svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
			baseURL := "https://api.anthropic.com"
			if kind == "aliyun" {
				baseURL = "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"
			}
			_, ids, _, err := svc.FetchUpstreamAvailabilityModels(context.Background(), modelSourceTestAccount(kind, baseURL))
			require.Empty(t, ids)
			syncErr := requireModelSourceUpstreamError(t, err)
			require.Equal(t, http.StatusServiceUnavailable, syncErr.StatusCode)
		})
	}
}

func TestFetchUpstreamAvailabilityModelsClassifiesMissingCatalogEndpointsAsUnsupported(t *testing.T) {
	for _, statusCode := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: statusCode,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":"catalog endpoint unavailable"}`)),
				Request:    &http.Request{URL: &url.URL{Scheme: "https", Host: "api.anthropic.com"}},
			}}
			svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
			_, ids, _, err := svc.FetchUpstreamAvailabilityModels(context.Background(), modelSourceTestAccount("anthropic", "https://api.anthropic.com"))
			require.Empty(t, ids)
			var syncErr *UpstreamModelSyncError
			require.ErrorAs(t, err, &syncErr)
			require.Equal(t, UpstreamModelSyncErrorUnsupported, syncErr.Kind)
			require.Equal(t, statusCode, syncErr.StatusCode)
		})
	}
}

func TestFetchUpstreamAvailabilityModelsEnforcesPerPageAndAggregateByteLimits(t *testing.T) {
	for _, kind := range []string{"anthropic", "aliyun"} {
		t.Run(kind, func(t *testing.T) {
			baseURL := "https://api.anthropic.com"
			if kind == "aliyun" {
				baseURL = "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"
			}
			t.Run("single page", func(t *testing.T) {
				body := modelSourceTestResponse(kind, []string{"model-a"}, false, "", 1, strings.Repeat("x", int(modelSourceMaxResponse)))
				upstream := newHttptestModelSourceUpstream(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					writeModelSourceTestResponse(w, body)
				}))
				defer upstream.close()
				svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
				_, ids, _, err := svc.FetchUpstreamAvailabilityModels(context.Background(), modelSourceTestAccount(kind, baseURL))
				require.Empty(t, ids)
				require.Contains(t, err.Error(), "response exceeds the size limit")
			})

			t.Run("aggregate pages", func(t *testing.T) {
				padding := strings.Repeat("x", int(modelSourceMaxResponse/2)+1)
				page := 0
				upstream := newHttptestModelSourceUpstream(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					page++
					if kind == "anthropic" && page == 1 {
						writeModelSourceTestResponse(w, modelSourceTestResponse(kind, []string{"model-a"}, true, "cursor-1", 0, padding))
						return
					}
					if kind == "anthropic" {
						writeModelSourceTestResponse(w, modelSourceTestResponse(kind, []string{"model-b"}, false, "", 0, padding))
						return
					}
					id := fmt.Sprintf("model-%c", 'a'+rune(page-1))
					writeModelSourceTestResponse(w, modelSourceTestResponse(kind, []string{id}, false, "", 2, padding))
				}))
				defer upstream.close()
				svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
				_, ids, _, err := svc.FetchUpstreamAvailabilityModels(context.Background(), modelSourceTestAccount(kind, baseURL))
				require.Empty(t, ids)
				require.Contains(t, err.Error(), "total response limit")
				require.Equal(t, 2, len(upstream.requests))
			})
		})
	}
}

func TestFetchUpstreamAvailabilityModelsAcceptsExactPageAndAggregateByteLimits(t *testing.T) {
	for _, kind := range []string{"anthropic", "aliyun"} {
		t.Run(kind, func(t *testing.T) {
			baseURL := "https://api.anthropic.com"
			if kind == "aliyun" {
				baseURL = "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"
			}
			t.Run("single page exact limit", func(t *testing.T) {
				body := modelSourceTestResponseExactSize(t, kind, []string{"model-a"}, false, "", 1, int(modelSourceMaxResponse))
				upstream := newHttptestModelSourceUpstream(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					writeModelSourceTestResponse(w, body)
				}))
				defer upstream.close()
				svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
				_, ids, _, err := svc.FetchUpstreamAvailabilityModels(context.Background(), modelSourceTestAccount(kind, baseURL))
				require.NoError(t, err)
				require.Equal(t, []string{"model-a"}, ids)
			})

			t.Run("aggregate exact limit", func(t *testing.T) {
				firstHasMore, firstCursor := kind == "anthropic", "cursor-1"
				firstBase := modelSourceTestResponse(kind, []string{"model-a"}, firstHasMore, firstCursor, 2, "")
				secondBase := modelSourceTestResponse(kind, []string{"model-b"}, false, "", 2, "")
				padding := int(modelSourceMaxResponse) - len(firstBase) - len(secondBase)
				require.GreaterOrEqual(t, padding, 2)
				firstPadding := padding / 2
				first := modelSourceTestResponseExactSize(t, kind, []string{"model-a"}, firstHasMore, firstCursor, 2, len(firstBase)+firstPadding)
				second := modelSourceTestResponseExactSize(t, kind, []string{"model-b"}, false, "", 2, len(secondBase)+padding-firstPadding)
				require.Equal(t, int(modelSourceMaxResponse), len(first)+len(second))
				page := 0
				upstream := newHttptestModelSourceUpstream(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					page++
					if page == 1 {
						writeModelSourceTestResponse(w, first)
						return
					}
					writeModelSourceTestResponse(w, second)
				}))
				defer upstream.close()
				svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
				_, ids, _, err := svc.FetchUpstreamAvailabilityModels(context.Background(), modelSourceTestAccount(kind, baseURL))
				require.NoError(t, err)
				require.ElementsMatch(t, []string{"model-a", "model-b"}, ids)
				require.Equal(t, 2, page)
			})
		})
	}
}

func TestFetchUpstreamAvailabilityModelsEnforcesModelCountLimitForAnthropicAndAlibaba(t *testing.T) {
	ids := make([]string, upstreamAvailabilityMaxIDs+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("model-%04d", i)
	}
	for _, kind := range []string{"anthropic", "aliyun"} {
		t.Run(kind, func(t *testing.T) {
			baseURL := "https://api.anthropic.com"
			if kind == "aliyun" {
				baseURL = "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"
			}
			body := modelSourceTestResponse(kind, ids, false, "", len(ids), "")
			upstream := newHttptestModelSourceUpstream(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeModelSourceTestResponse(w, body)
			}))
			defer upstream.close()
			svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
			_, gotIDs, _, err := svc.FetchUpstreamAvailabilityModels(context.Background(), modelSourceTestAccount(kind, baseURL))
			require.Empty(t, gotIDs)
			require.Contains(t, err.Error(), "model count limit")
		})
	}
}
