//go:build unit

package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const imagePollTimeoutTestImage = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg=="

type imagePollTimeoutPassthroughRepo struct {
	service.ErrorPassthroughRepository
}

func (imagePollTimeoutPassthroughRepo) List(context.Context) ([]*model.ErrorPassthroughRule, error) {
	status, message := http.StatusTeapot, "configured timeout response"
	return []*model.ErrorPassthroughRule{{
		ID: 1, Name: "poll timeout override", Enabled: true, Priority: 1,
		ErrorCodes: []int{http.StatusBadRequest}, Keywords: []string{"image_poll_timeout"},
		MatchMode: model.MatchModeAll, ResponseCode: &status, CustomMessage: &message,
	}}, nil
}

type imagePollTimeoutAccountRepo struct {
	openAIImagesFailoverAccountRepo
	tempUnscheduleCalls int
}

func (r *imagePollTimeoutAccountRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.tempUnscheduleCalls++
	return nil
}

// All transport calls are in-memory mocks. These assertions establish local
// usage settlement, not whether an uncertain upstream job incurred a charge.
func TestImagesPollTimeoutIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SUB2API_IMAGES_MAIN_MODEL", "gpt-5.4-mini")
	for _, route := range []string{"api_key", "oauth_direct", "oauth_responses"} {
		for _, endpoint := range []string{"generations", "edits"} {
			for _, stream := range []bool{false, true} {
				for _, mode := range []service.BillingMode{service.BillingModeToken, service.BillingModeImage, service.BillingModePerRequest} {
					for _, scenario := range []string{"success", "exhausted", "switch_limit", "cancelled", "total_deadline", "deadline_before_timer", "already_written", "partial_artifact", "ordinary_400", "admin_passthrough", "account_policy", "main_model_gate"} {
						if scenario == "main_model_gate" && route == "api_key" || scenario == "partial_artifact" && !stream {
							continue
						}
						for _, smart := range []bool{false, true} {
							t.Run(fmt.Sprintf("smart_router=%t/%s/%s/stream=%t/%s/%s", smart, route, endpoint, stream, mode, scenario), func(t *testing.T) {
								testImagesPollTimeoutIntegration(t, route, endpoint, stream, mode, scenario, smart)
							})
						}
					}
				}
			}
		}
	}
}

func testImagesPollTimeoutIntegration(t *testing.T, route, endpoint string, stream bool, mode service.BillingMode, scenario string, smart bool) {
	t.Helper()
	service.ResetOpenAIImageSchedulerSettingsForUnitTest()
	t.Cleanup(service.ResetOpenAIImageSchedulerSettingsForUnitTest)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	testLog, observedLogs := observer.New(zap.DebugLevel)
	ctx = logger.IntoContext(ctx, zap.New(testLog))
	var requestCtx context.Context = ctx
	deadline := &imageBoundaryDeadlineContext{Context: ctx}
	if scenario == "deadline_before_timer" {
		requestCtx = deadline
	}
	requestedModel, accountType := "gpt-image-2", service.AccountTypeAPIKey
	if route != "api_key" {
		accountType = service.AccountTypeOAuth
	}
	if route == "oauth_responses" {
		requestedModel = "gpt-image-1"
	}
	accounts := make([]service.Account, 3)
	for i := range accounts {
		accounts[i] = service.Account{
			ID: int64(i + 1), Name: fmt.Sprintf("mock-image-%d", i+1), Platform: service.PlatformOpenAI,
			Type: accountType, Status: service.StatusActive, Schedulable: true, Concurrency: 2, Priority: i,
			Credentials: map[string]any{"api_key": "mock-only", "access_token": "mock-only", "chatgpt_account_id": "mock-only"},
		}
		if scenario == "account_policy" {
			accounts[i].Credentials["custom_error_codes_enabled"] = true
			accounts[i].Credentials["custom_error_codes"] = []any{500.0}
		}
	}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Gateway.SmartRouter.Enabled = smart
	// This matrix asserts exact failover order while exercising the advanced
	// scheduler. Sample only its highest-ranked candidate, rather than native
	// randomized TopK ordering when Smart Router is disabled.
	cfg.Gateway.OpenAIWS.LBTopK = 1
	cfg.Gateway.ImageGenerationTransientCooldownSeconds = 8
	cfg.Gateway.ImageEditTransientCooldownSeconds = 8
	if scenario == "total_deadline" {
		cfg.Gateway.ImageRequestTimeoutSeconds = 1
	}
	cfg.Gateway.ImageConcurrency.Enabled = true
	cfg.Gateway.ImageConcurrency.MaxConcurrentRequests = 1
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60, DefaultMaxTokens: 10}
	logs, charges := &grokRoutePricingCountingUsageLogRepo{}, &grokRoutePricingCountingUsageBillingRepo{}
	slots := &grokMediaSlotsCache{accounts: map[string]int64{}, users: map[string]int64{}}
	concurrency := service.NewConcurrencyService(slots)
	reservationCache := &grokRoutePricingCountingReservationCache{handlerInflightCache: newHandlerInflightCache(100)}
	cache := service.NewBillingCacheService(reservationCache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(cache.Stop)
	billing := service.NewBillingService(cfg, nil)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	var attempted []int64
	upstream := &grokMediaSlotUpstream{call: func(req *http.Request, accountID int64) (*http.Response, error) {
		attempted = append(attempted, accountID)
		require.Equal(t, 1, reservationCache.count(), "the request keeps one reservation across account switches")
		if len(attempted) == 2 {
			logCount, _ := logs.snapshot()
			chargeCount, _ := charges.snapshot()
			require.Zero(t, logCount, "the failed attempt must not settle independently")
			require.Zero(t, chargeCount)
		}
		if route == "api_key" || route == "oauth_direct" {
			require.True(t, strings.HasSuffix(req.URL.Path, "/images/"+endpoint), req.URL.String())
		} else {
			require.True(t, strings.HasSuffix(req.URL.Path, "/responses"), req.URL.String())
		}
		if scenario == "success" && len(attempted) == 2 || scenario == "partial_artifact" {
			body := `{"created":1,"data":[{"b64_json":"` + imagePollTimeoutTestImage + `"}],"usage":{"input_tokens":7,"output_tokens":3}}`
			contentType := "application/json"
			if route == "oauth_responses" {
				contentType = "text/event-stream"
				body = "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"image_generation_call\",\"status\":\"completed\",\"result\":\"" + imagePollTimeoutTestImage + "\"}],\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}}\n\n"
			} else if stream {
				contentType = "text/event-stream"
				body = "data: {\"type\":\"image_generation.completed\",\"b64_json\":\"" + imagePollTimeoutTestImage + "\",\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}\n\n"
			}
			if scenario == "partial_artifact" {
				// HTTP-200 SSE errors remain under existing output-boundary policy;
				// this code in an event must not turn a delivered image into a retry.
				body += "data: {\"type\":\"error\",\"error\":{\"type\":\"server_error\",\"code\":\"image_poll_timeout\",\"message\":\"poll timed out\"}}\n\n"
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}, "X-Request-Id": {"mock-success"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		}
		if scenario == "total_deadline" {
			// Let the configured request-wide timer expire while the mock
			// produces a poll-timeout HTTP response. The original caller stays
			// connected, unlike a caller-owned context deadline.
			<-req.Context().Done()
		}
		body := `{"error":{"type":"server_error","code":"image_poll_timeout","message":"poll timed out"},"usage":{"input_tokens":99,"output_tokens":33}}`
		switch scenario {
		case "ordinary_400":
			body = `{"error":{"type":"invalid_request_error","code":"invalid_size","message":"invalid size"}}`
		case "main_model_gate":
			body = `{"error":{"code":"image_poll_timeout","message":"The 'gpt-5.4-mini' model is not supported when using Codex with a ChatGPT account."}}`
		case "already_written":
			// Deliberately model output committed during forwarding, before a
			// failover result reaches the handler's write-size boundary check.
			_, err := c.Writer.WriteString("data: {\"partial_image\":\"already-sent\"}\n\n")
			require.NoError(t, err)
			c.Writer.Flush()
		}
		var responseBody io.Reader = strings.NewReader(body)
		if scenario == "cancelled" {
			responseBody = imageBoundaryCancelReader{Reader: responseBody, cancel: cancel}
		}
		if scenario == "deadline_before_timer" {
			responseBody = imageBoundaryCancelReader{Reader: responseBody, cancel: func() { deadline.expired.Store(true) }}
		}
		return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"mock-timeout"}}, Body: io.NopCloser(responseBody)}, nil
	}}
	accountRepo := &imagePollTimeoutAccountRepo{openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{accounts: accounts}}
	rateLimit := service.NewRateLimitService(accountRepo, nil, cfg, nil, nil)
	rateLimit.SetSettingService(service.NewSettingService(imageBoundarySettings{enabled: true}, cfg))
	gateway := service.NewOpenAIGatewayService(accountRepo, logs, charges, nil, nil, nil, nil, cfg, nil, concurrency, billing, rateLimit, nil, upstream, &service.DeferredService{}, nil, nil, service.NewModelPricingResolver(nil, billing), nil, nil, nil, nil)
	ledger := &ccDisconnectHealthLedger{}
	gateway.SetSmartRouterHealthLedger(ledger)
	fixedNow := time.Now()
	sharedBefore := make([]service.OpenAIStreamSchedulingUnitSnapshot, len(accounts))
	imageBefore := make([]service.OpenAIStreamSchedulingUnitSnapshot, len(accounts))
	imageCapability := core.CapabilityImageGeneration
	if endpoint == "edits" {
		imageCapability = core.CapabilityImageEdit
	}
	for i := range accounts {
		ttft := 125
		gateway.ReportOpenAIAccountScheduleResult(&accounts[i], "gpt-5.5", false, &ttft, errors.New("seed text scheduler failure"))
	}
	for i := range accounts {
		sharedBefore[i] = gateway.OpenAIStreamSchedulingUnitSnapshot(&accounts[i], core.CapabilityResponses, "gpt-5.5", fixedNow)
		imageBefore[i] = gateway.OpenAIStreamSchedulingUnitSnapshot(&accounts[i], imageCapability, requestedModel, fixedNow)
		require.Positive(t, sharedBefore[i].Accounts, "the shared scheduler must be seeded so isolation assertions are meaningful")
	}
	pool := newUsageRecordTestPool(t)
	h := NewOpenAIGatewayHandler(gateway, concurrency, cache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), pool, nil, nil, nil, cfg)
	h.maxAccountSwitches = 10
	if scenario == "switch_limit" {
		h.maxAccountSwitches = 1
	}
	if scenario == "admin_passthrough" {
		h.errorPassthroughService = service.NewErrorPassthroughService(imagePollTimeoutPassthroughRepo{}, nil)
	}
	price, inputPrice, outputPrice := 0.25, 0.001, 0.002
	groupID := int64(24)
	key := &service.APIKey{ID: 20, UserID: 10, GroupID: &groupID, User: &service.User{ID: 10, Balance: 100}, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, AllowImageGeneration: true, RateMultiplier: 1, ModelPricing: []service.ChannelModelPricing{{Models: []string{requestedModel}, BillingMode: mode, PerRequestPrice: &price, InputPrice: &inputPrice, OutputPrice: &outputPrice, ImageOutputPrice: &outputPrice}}}}
	extra := ""
	if endpoint == "edits" {
		extra = `,"images":[{"image_url":"data:image/png;base64,` + imagePollTimeoutTestImage + `"}]`
	}
	body := fmt.Sprintf(`{"model":%q,"prompt":"draw","stream":%t%s}`, requestedModel, stream, extra)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/"+endpoint, strings.NewReader(body)).WithContext(requestCtx)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), key)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 10, Concurrency: 2})
	h.Images(c)
	pool.Stop()
	if scenario != "partial_artifact" {
		require.Zero(t, accountRepo.tempUnscheduleCalls, "poll timeout must not poison shared-account text/video availability")
	}
	if smart {
		for i := range accounts {
			after := gateway.OpenAIStreamSchedulingUnitSnapshot(&accounts[i], core.CapabilityResponses, "gpt-5.5", fixedNow)
			require.Equal(t, sharedBefore[i], after, "image failure and success must not mutate shared account EWMA/TTFT or text health")
			if scenario == "ordinary_400" || scenario == "cancelled" || scenario == "total_deadline" || scenario == "deadline_before_timer" {
				require.Equal(t, imageBefore[i], gateway.OpenAIStreamSchedulingUnitSnapshot(&accounts[i], imageCapability, requestedModel, fixedNow), "client errors and request-wide termination must not penalize image health")
			}
		}
	}
	if scenario == "cancelled" || scenario == "total_deadline" || scenario == "deadline_before_timer" {
		require.Empty(t, ledger.events, "request-wide termination must not persist image health events")
	}
	slots.assertReleased(t)
	require.Positive(t, slots.acquired)
	require.Positive(t, slots.userAcquired)
	require.Equal(t, 1, reservationCache.attempts, "one reservation belongs to the whole request, not each attempt")
	require.Zero(t, reservationCache.count(), "billing and handler references must release the reservation")
	h.imageLimiter.mu.Lock()
	active, waiting := h.imageLimiter.active, h.imageLimiter.waiting
	h.imageLimiter.mu.Unlock()
	require.Zero(t, active)
	require.Zero(t, waiting)
	require.Zero(t, observedLogs.FilterMessage("openai.responses_panic_recovered").Len())
	expectedAccounts := []int64{1}
	switch scenario {
	case "success", "switch_limit":
		expectedAccounts = []int64{1, 2}
	case "exhausted":
		expectedAccounts = []int64{1, 2, 3}
	}
	if scenario == "account_policy" && route != "api_key" {
		// Custom HTTP error-code policies are an existing API-key-only
		// feature. OAuth ignores these credential fields and still retries.
		expectedAccounts = []int64{1, 2, 3}
	}
	require.Equal(t, expectedAccounts, attempted, recorder.Body.String())
	logCount, log := logs.snapshot()
	chargeCount, command := charges.snapshot()
	if scenario == "success" || scenario == "partial_artifact" {
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		require.Contains(t, recorder.Body.String(), imagePollTimeoutTestImage)
		require.Equal(t, 1, logCount)
		require.Equal(t, 1, chargeCount)
		require.Equal(t, 7, log.InputTokens)
		require.Equal(t, 3, log.OutputTokens)
		expected := price
		if mode == service.BillingModeToken {
			expected = 7*inputPrice + 3*outputPrice
		}
		require.InDelta(t, expected, log.ActualCost, 1e-12)
		require.InDelta(t, expected, command.BalanceCost, 1e-12)
		return
	}
	require.Zero(t, logCount, "HTTP error attempts with no delivered image do not enter local settlement")
	require.Zero(t, chargeCount)
	switch scenario {
	case "ordinary_400", "main_model_gate":
		require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	case "admin_passthrough":
		require.Equal(t, http.StatusTeapot, recorder.Code, recorder.Body.String())
		require.Contains(t, recorder.Body.String(), "configured timeout response")
	case "account_policy":
		status := http.StatusInternalServerError
		if route != "api_key" {
			status = http.StatusBadGateway
		}
		require.Equal(t, status, recorder.Code, recorder.Body.String())
	case "already_written":
		require.Contains(t, recorder.Body.String(), "already-sent")
	case "cancelled":
		require.Empty(t, recorder.Body.String())
	default:
		require.Equal(t, http.StatusBadGateway, recorder.Code, recorder.Body.String())
	}
}
