//go:build unit

package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIChatWithheldParserFailureDoesNotBill(t *testing.T) {
	const meteredPartial = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\n"
	for _, mode := range []service.BillingMode{service.BillingModePerRequest, service.BillingModeToken} {
		for _, scenario := range []string{"withheld_invalid", "metered_partial", "cancelled_metered_partial"} {
			t.Run(string(mode)+"/"+scenario, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				payload := "data: {broken}\n"
				if scenario != "withheld_invalid" {
					payload = meteredPartial + payload
				}
				upstream := &compatPartialUsageUpstream{payload: payload}
				if scenario == "cancelled_metered_partial" {
					upstream.cancel = cancel
				}
				accounts := openAIImagesFailoverAccountRepo{accounts: []service.Account{{
					ID: 1, Name: "local-cc", Platform: service.PlatformOpenAI,
					Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
					Credentials: map[string]any{"api_key": "local-test"},
					Extra:       map[string]any{"openai_responses_supported": false},
				}}}
				logs := &grokRoutePricingCountingUsageLogRepo{}
				charges := &grokRoutePricingCountingUsageBillingRepo{}
				cfg := &config.Config{RunMode: config.RunModeStandard}
				billing := service.NewBillingService(cfg, nil)
				gateway := service.NewOpenAIGatewayService(accounts, logs, charges, nil, nil, nil, nil, cfg,
					nil, nil, billing, nil, nil, upstream, &service.DeferredService{}, nil, nil,
					service.NewModelPricingResolver(nil, billing), nil, nil, nil, nil)
				billingCache := service.NewBillingCacheService(grokRoutePricingBalanceOnlyCache{}, nil, nil, nil, nil, nil, cfg, nil)
				t.Cleanup(billingCache.Stop)
				pool := newUsageRecordTestPool(t)
				h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billingCache,
					service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), pool, nil, nil, nil, cfg)
				c, recorder := newOpenAIResponsesFailoverTestContext(t, ctx)
				apiKey, ok := middleware.GetAPIKeyFromContext(c)
				require.True(t, ok)
				price, inputPrice, outputPrice := 0.25, 0.001, 0.002
				apiKey.Group.RateMultiplier = 1
				apiKey.Group.ModelPricing = []service.ChannelModelPricing{{
					Models: []string{"gpt-4.1"}, BillingMode: mode,
					PerRequestPrice: &price, InputPrice: &inputPrice, OutputPrice: &outputPrice,
				}}
				body := `{"model":"gpt-4.1","messages":[{"role":"user","content":"` + strings.Repeat("a", 64*1024) + `"}],"stream":true}`
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)).WithContext(ctx)
				h.ChatCompletions(c)
				pool.Stop()
				require.Equal(t, 1, upstream.calls, "a parser failure must remain non-retryable")
				logCount, usage := logs.snapshot()
				chargeCount, command := charges.snapshot()
				if scenario == "withheld_invalid" {
					require.Zero(t, logCount, "no usage row for an unmetered withheld parser failure")
					require.Zero(t, chargeCount, "per-request pricing must not invent a failed-request charge")
					require.Nil(t, command)
					require.Contains(t, recorder.Body.String(), "upstream_stream")
					return
				}
				require.Equal(t, 1, logCount)
				require.Equal(t, 1, chargeCount, "observed metering must be charged exactly once, including cancellation")
				require.Equal(t, 7, usage.InputTokens)
				require.Equal(t, 3, usage.OutputTokens)
				expected := price
				if mode == service.BillingModeToken {
					expected = 7*inputPrice + 3*outputPrice
				}
				require.InDelta(t, expected, usage.ActualCost, 1e-12)
				require.InDelta(t, expected, command.BalanceCost, 1e-12)
			})
		}
	}
}

// Exercise the real standard-mode entry points and billing service through the
// repository command boundary. All providers, usage rows and charges are fake.
func TestOpenAICCFailureBillingEligibility(t *testing.T) {
	const broken = "data: {broken}\n\n"
	const metered = "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\n"
	for _, endpoint := range []string{"chat/completions", "messages", "responses"} {
		for _, mode := range []service.BillingMode{service.BillingModePerRequest, service.BillingModeToken} {
			for _, tc := range []struct {
				name, prefix                   string
				cancel, billable, writeFailure bool
			}{
				{name: "first_invalid"},
				{name: "empty_usage", prefix: "data: {\"choices\":[],\"usage\":{}}\n\n"},
				{name: "all_zero_usage", prefix: "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":0,\"cache_creation_input_tokens\":0,\"prompt_tokens_details\":{\"cached_tokens\":0,\"image_tokens\":0},\"completion_tokens_details\":{\"image_tokens\":0}}}\n\n"},
				{name: "negative_usage", prefix: "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":-1,\"completion_tokens\":-1}}\n\n"},
				{name: "metadata_only", prefix: ": heartbeat\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}]}\n\n"},
				{name: "cancelled_empty", cancel: true},
				{name: "write_failed_empty", prefix: "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"undelivered\"}}]}\n\n", writeFailure: true},
				{name: "metered_only", prefix: metered, billable: true},
				{name: "repeated_metered", prefix: metered + metered, billable: true},
				{name: "output_without_usage", prefix: "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n", billable: true},
				{name: "cancelled_metered", prefix: metered, cancel: true, billable: true},
			} {
				t.Run(endpoint+"/"+string(mode)+"/"+tc.name, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					upstream := &compatPartialUsageUpstream{payload: tc.prefix + broken}
					if tc.cancel {
						upstream.cancel = cancel
					}
					accounts := openAIImagesFailoverAccountRepo{accounts: []service.Account{{ID: 1, Name: "local-cc", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 2, Credentials: map[string]any{"api_key": "local-test"}, Extra: map[string]any{"openai_responses_supported": false, "openai_responses_mode": "force_chat_completions"}}}}
					logs := &grokRoutePricingCountingUsageLogRepo{}
					charges := &grokRoutePricingCountingUsageBillingRepo{}
					cfg := &config.Config{RunMode: config.RunModeStandard}
					checkScheduling := tc.cancel || tc.writeFailure
					var rateLimit *service.RateLimitService
					if checkScheduling {
						service.ResetOpenAIStreamSchedulerSettingsForUnitTest()
						t.Cleanup(service.ResetOpenAIStreamSchedulerSettingsForUnitTest)
						cfg.Gateway.SmartRouter.Enabled = true
						rateLimit = service.NewRateLimitService(accounts, nil, cfg, nil, nil)
						rateLimit.SetSettingService(service.NewSettingService(ccStreamSchedulerSettings{}, cfg))
					}
					billing := service.NewBillingService(cfg, nil)
					gateway := service.NewOpenAIGatewayService(accounts, logs, charges, nil, nil, nil, nil, cfg, nil, nil, billing, rateLimit, nil, upstream, &service.DeferredService{}, nil, nil, service.NewModelPricingResolver(nil, billing), nil, nil, nil, nil)
					ledger := &ccDisconnectHealthLedger{}
					gateway.SetSmartRouterHealthLedger(ledger)
					capability := core.CapabilityChat
					if endpoint == "responses" {
						capability = core.CapabilityResponses
					}
					fixedNow := time.Now()
					if checkScheduling {
						ttft := 125
						gateway.ReportOpenAIAccountScheduleResult(&accounts.accounts[0], "gpt-4.1", false, &ttft, errors.New("seed failure"))
						gateway.ReportSmartRouterTextResult(context.Background(), &accounts.accounts[0], capability, "gpt-4.1", nil, nil, 20)
					}
					before := gateway.OpenAIStreamSchedulingUnitSnapshot(&accounts.accounts[0], capability, "gpt-4.1", fixedNow)
					ledgerBefore := len(ledger.events)

					billingCache := service.NewBillingCacheService(grokRoutePricingBalanceOnlyCache{}, nil, nil, nil, nil, nil, cfg, nil)
					t.Cleanup(billingCache.Stop)
					pool := newUsageRecordTestPool(t)
					h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billingCache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), pool, nil, nil, nil, cfg)
					c, rec := newOpenAIResponsesFailoverTestContext(t, ctx)
					apiKey, ok := middleware.GetAPIKeyFromContext(c)
					require.True(t, ok)
					price, inputPrice, outputPrice := 0.25, 0.001, 0.002
					apiKey.Group.AllowMessagesDispatch = true
					apiKey.Group.RateMultiplier = 1
					apiKey.Group.ModelPricing = []service.ChannelModelPricing{{Models: []string{"gpt-4.1"}, BillingMode: mode, PerRequestPrice: &price, InputPrice: &inputPrice, OutputPrice: &outputPrice}}
					body := `{"model":"gpt-4.1","messages":[{"role":"user","content":"hello"}],"max_tokens":1024,"stream":true}`
					if endpoint == "responses" {
						body = `{"model":"gpt-4.1","input":"hello","stream":true}`
					}
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, strings.NewReader(body)).WithContext(ctx)
					if tc.writeFailure {
						c.Writer = ccStreamFailedWriter{c.Writer}
					}
					switch endpoint {
					case "messages":
						h.Messages(c)
					case "responses":
						h.Responses(c)
					default:
						h.ChatCompletions(c)
					}
					pool.Stop()
					require.Equal(t, "/v1/chat/completions", service.GetActualOpenAIUpstreamEndpoint(c), "fixture must exercise the CC adapter")
					if checkScheduling {
						after := gateway.OpenAIStreamSchedulingUnitSnapshot(&accounts.accounts[0], capability, "gpt-4.1", fixedNow)
						require.Equal(t, before, after, "disconnect must preserve seeded EWMA, TTFT and Smart Router health")
						require.Len(t, ledger.events, ledgerBefore)
						if tc.writeFailure {
							require.NoError(t, c.Request.Context().Err(), "writer failure fixture must not cancel request context")
						}
					}
					require.Equal(t, 1, upstream.calls, "failures must not replay provider requests: %s", rec.Body.String())
					logCount, usage := logs.snapshot()
					chargeCount, command := charges.snapshot()
					if !tc.billable {
						require.Zero(t, logCount)
						require.Zero(t, chargeCount)
						require.Nil(t, command)
					} else {
						require.Equal(t, 1, logCount)
						require.Equal(t, 1, chargeCount)
						inputTokens, outputTokens := 7, 3
						if tc.name == "output_without_usage" {
							inputTokens, outputTokens = 0, 0
						}
						require.Equal(t, inputTokens, usage.InputTokens)
						require.Equal(t, outputTokens, usage.OutputTokens)
						expected := price
						if mode == service.BillingModeToken {
							expected = float64(inputTokens)*inputPrice + float64(outputTokens)*outputPrice
						}
						require.InDelta(t, expected, usage.ActualCost, 1e-12)
						require.InDelta(t, expected, command.BalanceCost, 1e-12)
					}
					if !tc.cancel && !tc.writeFailure {
						require.Contains(t, rec.Body.String(), "error")
						if endpoint != "chat/completions" && tc.name == "first_invalid" {
							require.Equal(t, http.StatusBadGateway, rec.Code)
						}
					}
					require.NotContains(t, rec.Body.String(), `"type":"response.completed"`)
					require.NotContains(t, rec.Body.String(), `"type":"message_stop"`)
				})
			}
		}
	}
}

type ccStreamFailedWriter struct{ gin.ResponseWriter }

func (ccStreamFailedWriter) Write([]byte) (int, error)       { return 0, io.ErrClosedPipe }
func (ccStreamFailedWriter) WriteString(string) (int, error) { return 0, io.ErrClosedPipe }

type ccStreamSchedulerSettings struct{ service.SettingRepository }

func (ccStreamSchedulerSettings) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{"openai_advanced_scheduler_enabled": "true"}, nil
}
func (ccStreamSchedulerSettings) GetValue(context.Context, string) (string, error) {
	return "", service.ErrSettingNotFound
}
