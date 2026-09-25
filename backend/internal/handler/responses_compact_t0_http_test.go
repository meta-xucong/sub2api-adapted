package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Only authentication, account storage and concurrency are substituted. Requests
// traverse a real HTTP socket, the production handler, scheduler and adapter,
// then a second HTTP socket to a synthetic upstream. No production credentials.
type t0CompactHTTPTransport struct{ client *http.Client }

func (u *t0CompactHTTPTransport) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.client.Do(r)
}
func (u *t0CompactHTTPTransport) DoWithTLS(r *http.Request, p string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(r, p, id, n)
}

type t0CompactHTTPAccounts struct {
	openAIWSFailoverHandlerAccountRepoStub
}

func (r *t0CompactHTTPAccounts) ListSchedulable(context.Context) ([]service.Account, error) {
	return r.accounts, nil
}
func (r *t0CompactHTTPAccounts) ListSchedulableByGroupID(context.Context, int64) ([]service.Account, error) {
	return r.accounts, nil
}
func (r *t0CompactHTTPAccounts) ListSchedulableByPlatforms(context.Context, []string) ([]service.Account, error) {
	return r.accounts, nil
}
func (r *t0CompactHTTPAccounts) ListSchedulableByGroupIDAndPlatforms(context.Context, int64, []string) ([]service.Account, error) {
	return r.accounts, nil
}
func (r *t0CompactHTTPAccounts) ListSchedulableUngroupedByPlatforms(context.Context, []string) ([]service.Account, error) {
	return r.accounts, nil
}
func (r *t0CompactHTTPAccounts) UpdateLastUsed(context.Context, int64) error   { return nil }
func (r *t0CompactHTTPAccounts) SetError(context.Context, int64, string) error { return nil }

func (r *t0CompactHTTPAccounts) ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]service.Account, error) {
	return r.accounts, nil
}

type t0CompactHTTPGroups struct {
	service.GroupRepository
	group *service.Group
}

func (r *t0CompactHTTPGroups) GetByID(context.Context, int64) (*service.Group, error) {
	return r.group, nil
}
func (r *t0CompactHTTPGroups) GetByIDLite(context.Context, int64) (*service.Group, error) {
	return r.group, nil
}

type t0CompactObservation struct {
	path string
	body []byte
}

func newT0CompactHTTPServer(t *testing.T, provider, outcome string) (*httptest.Server, string, <-chan t0CompactObservation, *atomic.Int32) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	observed := make(chan t0CompactObservation, 16)
	hits := &atomic.Int32{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "synthetic read failure", 400)
			return
		}
		hits.Add(1)
		observed <- t0CompactObservation{r.URL.Path, body}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "synthetic-upstream-request")
		if outcome == "delayed_bad_json" {
			time.Sleep(1200 * time.Millisecond)
		}
		if outcome == "bad_json" || outcome == "delayed_bad_json" {
			_, _ = io.WriteString(w, `not-json`)
			return
		}
		if outcome == "forbidden" {
			w.WriteHeader(403)
			_, _ = io.WriteString(w, `{"error":{"type":"permission_error","code":"permission_denied","message":"synthetic forbidden"}}`)
			return
		}
		switch provider {
		case "anthropic":
			_, _ = io.WriteString(w, `{"id":"msg_t0","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"summary"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`)
		case "chat":
			_, _ = io.WriteString(w, `{"id":"chatcmpl_t0","model":"glm-5.3","choices":[{"index":0,"message":{"role":"assistant","content":"summary"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
		default:
			_, _ = io.WriteString(w, `{"id":"resp_t0","object":"response","model":"gpt-5.4","created_at":1,"status":"completed","output":[{"id":"cmp_t0","type":"compaction","status":"completed","encrypted_content":"synthetic-opaque"}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`)
		}
	}))
	t.Cleanup(upstream.Close)
	model, platform := "gpt-5.4", service.PlatformOpenAI
	if provider == "chat" {
		model = "glm-5.3"
		platform = service.PlatformOpenAI
	}
	if provider == "anthropic" {
		model = "claude-sonnet-4-6"
		platform = service.PlatformAnthropic
	}
	groupID := int64(9500)
	group := &service.Group{ID: groupID, Platform: platform, Status: service.StatusActive}
	account := service.Account{ID: 9501, Name: "synthetic-compact", Platform: platform, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "synthetic-only", "base_url": upstream.URL, "model_mapping": map[string]any{model: model}},
		Extra:       map[string]any{"openai_responses_supported": provider != "chat"},
	}
	accounts := &t0CompactHTTPAccounts{openAIWSFailoverHandlerAccountRepoStub: openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Default.RateMultiplier = 1
	cfg.Gateway.MaxAccountSwitches = 1
	if outcome == "delayed_bad_json" {
		cfg.Gateway.StreamKeepaliveInterval = 1
	}
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	concurrency := service.NewConcurrencyService(&concurrencyCacheMock{
		acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
	})
	transport := &t0CompactHTTPTransport{client: &http.Client{Timeout: 5 * time.Second}}
	usage := &openAIWSUsageHandlerUsageLogRepoStub{}
	rate := service.NewRateLimitService(accounts, nil, cfg, nil, nil)
	apiKey := &service.APIKey{ID: 9502, GroupID: &groupID, Group: group, User: &service.User{ID: 9503, Status: service.StatusActive}}
	var endpoint gin.HandlerFunc
	if provider == "anthropic" {
		gateway := service.NewGatewayService(accounts, &t0CompactHTTPGroups{group: group}, usage, nil, nil, nil, nil, nil, cfg, nil, nil,
			service.NewBillingService(cfg, nil), rate, billing, nil, transport, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
		h := &GatewayHandler{gatewayService: gateway, billingCacheService: billing, apiKeyService: &service.APIKeyService{}, concurrencyHelper: NewConcurrencyHelper(concurrency, SSEPingFormatComment, 0), maxAccountSwitches: 1, cfg: cfg}
		endpoint = h.Responses
	} else {
		gateway := service.NewOpenAIGatewayService(accounts, usage, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), rate, billing, transport, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
		h := NewOpenAIGatewayHandler(gateway, concurrency, billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
		endpoint = h.Responses
	}
	router := gin.New()
	router.Use(gin.Recovery(), func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.User.ID, Concurrency: 1})
		c.Next()
	})
	for _, path := range []string{"/v1/responses/compact", "/openai/v1/responses/compact", "/responses/compact", "/backend-api/codex/responses/compact"} {
		router.POST(path, endpoint)
	}
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server, model, observed, hits
}

func t0ReadHTTP(t *testing.T, server *httptest.Server, path, body string) (*http.Response, []byte) {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(server.URL+path, "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	require.NoError(t, err)
	return resp, data
}

func TestT0CompactHTTP_StreamAndJSON(t *testing.T) {
	for _, provider := range []string{"chat", "anthropic", "native"} {
		for _, mode := range []string{"true", "false", "omitted"} {
			t.Run(provider+"/"+mode, func(t *testing.T) {
				server, model, observed, hits := newT0CompactHTTPServer(t, provider, "")
				streamField := ""
				if mode != "omitted" {
					streamField = `,"stream":` + mode
				}
				resp, data := t0ReadHTTP(t, server, "/v1/responses/compact", fmt.Sprintf(`{"model":%q,"input":"synthetic history"%s}`, model, streamField))
				require.Equal(t, 200, resp.StatusCode, string(data))
				require.Equal(t, int32(1), hits.Load(), "one upstream attempt")
				if mode == "true" {
					require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream", string(data))
					types := []string{}
					id := ""
					for _, line := range strings.Split(string(data), "\n") {
						if !strings.HasPrefix(line, "data: {") {
							continue
						}
						event := gjson.Parse(strings.TrimPrefix(line, "data: "))
						require.True(t, event.Get("sequence_number").Exists(), line)
						require.Equal(t, int64(len(types)), event.Get("sequence_number").Int())
						types = append(types, event.Get("type").String())
						if event.Get("type").String() == "response.created" {
							id = event.Get("response.id").String()
						}
						if event.Get("type").String() == "response.completed" {
							require.NotEmpty(t, id)
							require.Equal(t, id, event.Get("response.id").String())
							require.Equal(t, "compaction", event.Get("response.output.0.type").String())
							require.NotEmpty(t, event.Get("response.output.0.encrypted_content").String())
						}
					}
					require.Equal(t, []string{"response.created", "response.in_progress", "response.output_item.added", "response.output_item.done", "response.completed"}, types)
				} else {
					require.Contains(t, resp.Header.Get("Content-Type"), "application/json")
					require.Equal(t, "compaction", gjson.GetBytes(data, "output.0.type").String())
					require.NotContains(t, string(data), "event:")
				}
				select {
				case got := <-observed:
					require.False(t, gjson.GetBytes(got.body, "stream").Bool(), "the upstream summary transport stays unary")
					expectedPath := map[string]string{"chat": "/v1/chat/completions", "anthropic": "/v1/messages", "native": "/v1/responses/compact"}[provider]
					require.Equal(t, expectedPath, got.path)
				default:
					t.Fatal("upstream request was not observed")
				}
			})
		}
	}
}

func TestT0CompactHTTP_InvalidStreamRejected(t *testing.T) {
	for _, provider := range []string{"chat", "anthropic", "native"} {
		for _, value := range []string{`"true"`, `"false"`, `1`, `{}`, `[]`} {
			t.Run(provider+"/"+value, func(t *testing.T) {
				server, model, _, hits := newT0CompactHTTPServer(t, provider, "")
				resp, data := t0ReadHTTP(t, server, "/v1/responses/compact", fmt.Sprintf(`{"model":%q,"input":"test","stream":%s}`, model, value))
				require.Equal(t, 400, resp.StatusCode, string(data))
				require.True(t, gjson.GetBytes(data, "error").IsObject())
				require.Equal(t, int32(0), hits.Load())
			})
		}
	}
}

func TestT0CompactHTTP_AliasesAndValidation(t *testing.T) {
	for _, provider := range []string{"chat", "anthropic", "native"} {
		for _, path := range []string{"/openai/v1/responses/compact", "/responses/compact", "/backend-api/codex/responses/compact"} {
			t.Run(provider+path, func(t *testing.T) {
				server, model, _, hits := newT0CompactHTTPServer(t, provider, "")
				resp, data := t0ReadHTTP(t, server, path, fmt.Sprintf(`{"model":%q,"input":"synthetic","stream":true}`, model))
				require.Equal(t, 200, resp.StatusCode, string(data))
				require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")
				require.Equal(t, 1, strings.Count(string(data), "event: response.completed\n"))
				require.Equal(t, int32(1), hits.Load())
			})
		}
		for _, body := range []string{`{"stream":true}`, `{"model":"test","stream":true`, `[]`, `{}`, ``} {
			t.Run(provider+"/invalid/"+body, func(t *testing.T) {
				server, _, _, hits := newT0CompactHTTPServer(t, provider, "")
				resp, data := t0ReadHTTP(t, server, "/v1/responses/compact", body)
				require.Equal(t, 400, resp.StatusCode, string(data))
				require.True(t, gjson.GetBytes(data, "error").IsObject())
				require.Equal(t, int32(0), hits.Load())
			})
		}
	}
}

func TestT0CompactHTTP_UpstreamErrorsNeverComplete(t *testing.T) {
	for _, provider := range []string{"chat", "anthropic", "native"} {
		for _, outcome := range []string{"bad_json", "delayed_bad_json"} {
			t.Run(provider+"/"+outcome, func(t *testing.T) {
				server, model, _, hits := newT0CompactHTTPServer(t, provider, outcome)
				resp, data := t0ReadHTTP(t, server, "/v1/responses/compact", fmt.Sprintf(`{"model":%q,"input":"synthetic","stream":true}`, model))
				require.Equal(t, int32(1), hits.Load())
				require.NotContains(t, string(data), "event: response.completed")
				if outcome == "delayed_bad_json" && provider != "anthropic" {
					require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")
					require.Equal(t, 1, strings.Count(string(data), "event: response.failed\n"), string(data))
					for _, line := range strings.Split(string(data), "\n") {
						if strings.HasPrefix(line, "data: {") {
							event := gjson.Parse(strings.TrimPrefix(line, "data: "))
							require.Equal(t, "response.failed", event.Get("type").String())
							require.Equal(t, "failed", event.Get("response.status").String())
							require.NotEmpty(t, event.Get("response.error.code").String())
						}
					}
				} else {
					require.Equal(t, 502, resp.StatusCode, string(data))
					require.True(t, gjson.GetBytes(data, "error").IsObject(), string(data))
				}
			})
		}
	}
}
