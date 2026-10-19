//go:build unit

package handler

// Adapted from the public, unmerged upstream proposal Wei-Shaw/sub2api#7791
// (086f1bd34eae), scoped to the Chat Completions handler repaired here.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// compatUsageLogRepository lets the capture embed the full repository
// interface while only implementing Create.
type compatUsageLogRepository = service.UsageLogRepository

type compatPartialUsageRepo struct {
	compatUsageLogRepository
	mu   sync.Mutex
	logs []service.UsageLog
}

func (r *compatPartialUsageRepo) Create(ctx context.Context, usage *service.UsageLog) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, *usage)
	return true, nil
}

type compatPartialUsageUpstream struct {
	payload string
	cancel  context.CancelFunc
	calls   int
}

func (u *compatPartialUsageUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxyURL, accountID, accountConcurrency, nil)
}

func (u *compatPartialUsageUpstream) DoWithTLS(_ *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.calls++
	if u.cancel != nil {
		u.cancel()
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(u.payload))}, nil
}

const compatPartialMessageStartSSE = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_partial\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-5\",\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n" +
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n"

// Occupy the sole worker until the test has replaced the completed request's
// Gin request pointer. This deterministically exercises context reuse without
// relying on sync.Pool scheduling or racing the worker.
func blockCompatUsageWorker(t *testing.T, pool *service.UsageRecordWorkerPool) func() {
	t.Helper()
	started := make(chan struct{})
	released := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(released) }) }
	t.Cleanup(release)
	mode := pool.Submit(func(context.Context) {
		close(started)
		<-released
	})
	require.False(t, mode.Dropped())
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("usage worker did not start")
	}
	return release
}

func reuseCompatUsageContext(c *gin.Context) {
	ctx := context.WithValue(context.Background(), ctxkey.RequestedPublicModel, "next-request-public-alias")
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
}

// Streams through the Chat Completions and Responses compatibility entries on
// an Anthropic account must persist the upstream-metered usage exactly once
// whether the stream completes, is truncated, or the client left first.
func TestGatewayChatHandlerPreservesPartialUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"chat/completions"} {
		for _, ending := range []string{"complete", "truncated", "disconnected_truncated", "before_start"} {
			t.Run(endpoint+"/"+ending, func(t *testing.T) {
				groupID := int64(9200)
				group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive, RateMultiplier: 1}
				account := &service.Account{
					ID: 9201, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey,
					Status: service.StatusActive, Schedulable: true, Concurrency: 1,
					Credentials:   map[string]any{"api_key": "local-test"},
					AccountGroups: []service.AccountGroup{{AccountID: 9201, GroupID: groupID}},
				}
				payload := compatPartialMessageStartSSE + "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":15}}\n\n"
				switch ending {
				case "complete":
					payload += "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				case "before_start":
					payload = "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Unavailable\"}}\n\n"
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				upstream := &compatPartialUsageUpstream{payload: payload}
				if ending == "disconnected_truncated" {
					upstream.cancel = cancel
				}
				usageRepo := &compatPartialUsageRepo{}
				cfg := &config.Config{RunMode: config.RunModeSimple}
				snapshot := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil)
				gateway := service.NewGatewayService(
					nil, &fakeGroupRepo{group: group}, usageRepo, nil, nil, nil, nil, nil, cfg,
					snapshot, nil, service.NewBillingService(cfg, nil), nil, nil, nil, upstream,
					&service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
				)
				billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				t.Cleanup(billingCache.Stop)
				pool := newUsageRecordTestPool(t)
				releaseWorker := blockCompatUsageWorker(t, pool)
				h := &GatewayHandler{
					gatewayService: gateway, billingCacheService: billingCache, cfg: cfg,
					concurrencyHelper:     NewConcurrencyHelper(service.NewConcurrencyService(&fakeConcurrencyCache{}), SSEPingFormatClaude, 0),
					usageRecordWorkerPool: pool,
				}
				apiKey := &service.APIKey{ID: 9202, UserID: 9203, GroupID: &groupID, Group: group, Status: service.StatusActive,
					User: &service.User{ID: 9203, Concurrency: 10, Balance: 100}}
				body := `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello"}],"stream":true}`
				if endpoint == "responses" {
					body = `{"model":"claude-sonnet-4-5","input":"hello","stream":true}`
				}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				requestCtx := context.WithValue(context.WithValue(ctx, ctxkey.Group, group), ctxkey.RequestedPublicModel, "first-request-public-alias")
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, strings.NewReader(body)).WithContext(requestCtx)
				c.Request.Header.Set("Content-Type", "application/json")
				c.Set(string(middleware.ContextKeyAPIKey), apiKey)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})
				if endpoint == "responses" {
					h.Responses(c)
				} else {
					h.ChatCompletions(c)
				}
				reuseCompatUsageContext(c)
				releaseWorker()
				pool.Stop()
				require.Equal(t, 1, upstream.calls, "no replay after a started or cancelled request")
				usageRepo.mu.Lock()
				defer usageRepo.mu.Unlock()
				if ending == "before_start" {
					require.Empty(t, usageRepo.logs, "an upstream failure before any usage must not be billed")
					return
				}
				require.Len(t, usageRepo.logs, 1, "upstream-metered usage must be persisted exactly once; response=%s", recorder.Body.String())
				require.Equal(t, 10, usageRepo.logs[0].InputTokens)
				require.Equal(t, 15, usageRepo.logs[0].OutputTokens)
				require.Equal(t, apiKey.ID, usageRepo.logs[0].APIKeyID)
				require.Equal(t, account.ID, usageRepo.logs[0].AccountID)
				require.Equal(t, "first-request-public-alias", usageRepo.logs[0].RequestedModel,
					"queued billing must not read the next request's Gin context")
			})
		}
	}
}

func TestOpenAIChatHandlerCancellationPreservesUsageWithoutHealthPenalty(t *testing.T) {
	for _, disconnected := range []bool{false, true} {
		t.Run(map[bool]string{false: "upstream_truncated", true: "client_cancelled"}[disconnected], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstream := &compatPartialUsageUpstream{payload: "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\n"}
			if disconnected {
				upstream.cancel = cancel
			}
			accounts := openAIImagesFailoverAccountRepo{accounts: []service.Account{{
				ID: 1, Name: "local-cc", Platform: service.PlatformOpenAI,
				Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
				Credentials: map[string]any{"api_key": "local-test"},
				Extra:       map[string]any{"openai_responses_supported": false},
			}}}
			usageRepo := &compatPartialUsageRepo{}
			cfg := &config.Config{RunMode: config.RunModeSimple, Gateway: config.GatewayConfig{SmartRouter: config.GatewaySmartRouterConfig{Enabled: true}}}
			gateway := service.NewOpenAIGatewayService(accounts, usageRepo, nil, nil, nil, nil, nil, cfg,
				nil, nil, service.NewBillingService(cfg, nil), nil, nil, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
			ledger := &ccDisconnectHealthLedger{}
			gateway.SetSmartRouterHealthLedger(ledger)
			billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billingCache.Stop)
			pool := newUsageRecordTestPool(t)
			releaseWorker := blockCompatUsageWorker(t, pool)
			h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billingCache,
				service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), pool, nil, nil, nil, cfg)
			c, rec := newOpenAIResponsesFailoverTestContext(t, ctx)
			requestCtx := context.WithValue(ctx, ctxkey.RequestedPublicModel, "first-request-public-alias")
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","messages":[{"role":"user","content":"hi"}],"stream":true}`)).WithContext(requestCtx)
			h.ChatCompletions(c)
			reuseCompatUsageContext(c)
			releaseWorker()
			pool.Stop()
			require.Equal(t, 1, upstream.calls, "a metered partial stream must not be replayed")
			usageRepo.mu.Lock()
			defer usageRepo.mu.Unlock()
			require.Len(t, usageRepo.logs, 1, "partial usage must be recorded once: %s", rec.Body.String())
			require.Equal(t, 7, usageRepo.logs[0].InputTokens)
			require.Equal(t, 3, usageRepo.logs[0].OutputTokens)
			require.Equal(t, "first-request-public-alias", usageRepo.logs[0].RequestedModel,
				"queued billing must not read the next request's Gin context")
			if disconnected {
				require.Empty(t, ledger.events, "cancellation must not penalize Smart Router")
				require.Empty(t, rec.Body.String(), "no error write after cancellation")
			} else {
				require.Len(t, ledger.events, 1)
				require.Equal(t, core.FailureStreamInterrupted, ledger.events[0].FailureClass)
				require.Positive(t, ledger.events[0].HealthPenalty, "connected upstream failure must penalize the active router")
				require.Positive(t, ledger.events[0].ConsecutiveFailures)
				require.Positive(t, ledger.events[0].CooldownUntilUnix)
				require.Less(t, ledger.events[0].HealthScore, 1.0)
			}
		})
	}
}

type ccDisconnectHealthLedger struct {
	service.SmartRouterHealthLedger
	events []core.HealthEvent
}

func (l *ccDisconnectHealthLedger) LoadStates(context.Context) ([]service.SmartRouterHealthState, error) {
	return nil, nil
}
func (l *ccDisconnectHealthLedger) ListCapabilityEvidence(context.Context) ([]service.SmartRouterCapabilityEvidence, error) {
	return nil, nil
}
func (l *ccDisconnectHealthLedger) RecordEvent(_ context.Context, event core.HealthEvent) error {
	l.events = append(l.events, event)
	return nil
}
