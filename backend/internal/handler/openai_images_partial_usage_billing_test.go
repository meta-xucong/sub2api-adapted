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

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type imageUsageFailureReader struct {
	io.Reader
	terminal error
	cancel   context.CancelFunc
}

func (r *imageUsageFailureReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		if r.cancel != nil {
			r.cancel()
		}
		return n, r.terminal
	}
	return n, err
}

type imageUsageBillingUpstream struct {
	service.HTTPUpstream
	calls   int
	respond func(int) *http.Response
}

func (u *imageUsageBillingUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	u.calls++
	return u.respond(u.calls), nil
}

func TestImagesFailedStreamObservedUsageBilling(t *testing.T) {
	for _, route := range []string{"api_key", "oauth_responses", "oauth_direct"} {
		for _, mode := range []service.BillingMode{service.BillingModeToken, service.BillingModeImage, service.BillingModePerRequest} {
			for _, scenario := range []string{"metered_failure", "input_only", "output_only", "cache_read_only", "cache_create_only", "no_usage", "zero_usage", "cancelled", "deadline", "partial_artifact", "failover_success", "server_error_then_read_failure"} {
				if scenario == "server_error_then_read_failure" && route != "oauth_direct" {
					continue
				}
				t.Run(route+"/"+string(mode)+"/"+scenario, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					testLog, observedLogs := observer.New(zap.DebugLevel)
					ctx = logger.IntoContext(ctx, zap.New(testLog))
					deadline := &imageBoundaryDeadlineContext{Context: ctx, publishErr: true}
					var requestCtx context.Context = ctx
					if scenario == "deadline" {
						requestCtx = deadline
					}
					model := "gpt-image-1"
					accountType := service.AccountTypeAPIKey
					if route != "api_key" {
						accountType = service.AccountTypeOAuth
					}
					if route == "oauth_direct" {
						model = "gpt-image-2"
					}
					account := service.Account{ID: 1, Name: "mock-images", Platform: service.PlatformOpenAI, Type: accountType, Status: service.StatusActive, Schedulable: true, Concurrency: 2, Credentials: map[string]any{"api_key": "local-test", "access_token": "local-test", "chatgpt_account_id": "local-test"}}
					other := account
					other.ID = 2
					accounts := openAIImagesFailoverAccountRepo{accounts: []service.Account{account, other}}
					usage := `"usage":{"input_tokens":7,"output_tokens":3}`
					if scenario == "no_usage" {
						usage = `"usage":{}`
					}
					if scenario == "zero_usage" {
						usage = `"usage":{"input_tokens":0,"output_tokens":0}`
					}
					expectedInput, expectedOutput, expectedRead, expectedCreate := 7, 3, 0, 0
					switch scenario {
					case "input_only":
						usage = `"usage":{"input_tokens":7}`
						expectedOutput = 0
					case "output_only":
						usage = `"usage":{"output_tokens":3}`
						expectedInput = 0
					case "cache_read_only":
						usage = `"usage":{"cache_read_input_tokens":5}`
						expectedInput, expectedOutput, expectedRead = 0, 0, 5
					case "cache_create_only":
						usage = `"usage":{"cache_creation_input_tokens":5}`
						expectedInput, expectedOutput, expectedCreate = 0, 0, 5
					}
					payload := "data: {\"type\":\"response.created\"," + usage + "}\n\n"
					if scenario == "partial_artifact" {
						if route == "oauth_responses" {
							payload += "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"image_generation_call\",\"status\":\"completed\",\"result\":\"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==\"}]," + usage + "}}\n\n"
						} else {
							payload += "data: {\"type\":\"image_generation.completed\",\"b64_json\":\"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==\"," + usage + "}\n\n"
						}
					}
					upstream := &imageUsageBillingUpstream{respond: func(call int) *http.Response {
						body := payload
						terminal := errors.New("mock stream read failure")
						if scenario == "server_error_then_read_failure" {
							body = "data: {\"type\":\"error\"," + usage + ",\"error\":{\"type\":\"server_error\",\"message\":\"unavailable\"}}\n\n"
						} else if scenario == "failover_success" {
							// The first attempt ends with its typed server-error event, not an unrelated read failure.
							terminal = io.EOF
							if call == 1 {
								if route == "api_key" {
									return &http.Response{StatusCode: 503, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"unavailable"}}`))}
								}
								body = "data: {\"type\":\"error\"," + usage + ",\"error\":{\"type\":\"server_error\",\"message\":\"unavailable\"}}\n\n"
							} else {
								terminal = io.EOF
								if route == "oauth_responses" {
									body = "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"image_generation_call\",\"status\":\"completed\",\"result\":\"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==\"}]," + usage + "}}\n\n"
								} else {
									body = "data: {\"type\":\"image_generation.completed\",\"b64_json\":\"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==\"," + usage + "}\n\n"
								}
							}
						} else if route != "api_key" && scenario != "cancelled" && scenario != "deadline" && scenario != "partial_artifact" {
							body += "data: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"code\":\"content_policy_violation\",\"message\":\"blocked\"}}\n\n"
							terminal = io.EOF
						}
						reader := &imageUsageFailureReader{Reader: strings.NewReader(body), terminal: terminal}
						if scenario == "cancelled" {
							reader.terminal = context.Canceled
							reader.cancel = cancel
						}
						if scenario == "deadline" {
							reader.terminal = context.DeadlineExceeded
							reader.cancel = func() { deadline.expired.Store(true) }
						}
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {fmt.Sprintf("mock-attempt-%d", call)}}, Body: io.NopCloser(reader)}
					}}
					cfg := &config.Config{RunMode: config.RunModeStandard}
					logs, charges := &grokRoutePricingCountingUsageLogRepo{}, &grokRoutePricingCountingUsageBillingRepo{}
					slots := &grokMediaSlotsCache{accounts: map[string]int64{}, users: map[string]int64{}}
					concurrency := service.NewConcurrencyService(slots)
					billing := service.NewBillingService(cfg, nil)
					gateway := service.NewOpenAIGatewayService(accounts, logs, charges, nil, nil, nil, nil, cfg, nil, concurrency, billing, nil, nil, upstream, &service.DeferredService{}, nil, nil, service.NewModelPricingResolver(nil, billing), nil, nil, nil, nil)
					cache := service.NewBillingCacheService(grokRoutePricingBalanceOnlyCache{}, nil, nil, nil, nil, nil, cfg, nil)
					t.Cleanup(cache.Stop)
					pool := newUsageRecordTestPool(t)
					h := NewOpenAIGatewayHandler(gateway, concurrency, cache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), pool, nil, nil, nil, cfg)
					h.maxAccountSwitches = 1
					price, inputPrice, outputPrice, cacheReadPrice, cacheWritePrice := 0.25, 0.001, 0.002, 0.003, 0.004
					groupID := int64(24)
					key := &service.APIKey{ID: 20, UserID: 10, GroupID: &groupID, User: &service.User{ID: 10, Balance: 100}, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, AllowImageGeneration: true, RateMultiplier: 1, ModelPricing: []service.ChannelModelPricing{{Models: []string{model}, BillingMode: mode, PerRequestPrice: &price, InputPrice: &inputPrice, OutputPrice: &outputPrice, ImageOutputPrice: &outputPrice, CacheReadPrice: &cacheReadPrice, CacheWritePrice: &cacheWritePrice}}}}
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(fmt.Sprintf(`{"model":%q,"prompt":"draw","stream":true}`, model))).WithContext(requestCtx)
					c.Request.Header.Set("Content-Type", "application/json")
					c.Set(string(middleware.ContextKeyAPIKey), key)
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 10, Concurrency: 2})
					h.Images(c)
					pool.Stop()
					slots.assertReleased(t)
					require.Zero(t, observedLogs.FilterMessage("openai.responses_panic_recovered").Len())
					expectedCalls := 1
					if scenario == "failover_success" {
						expectedCalls = 2
					}
					require.Equal(t, expectedCalls, upstream.calls, recorder.Body.String())
					logCount, log := logs.snapshot()
					chargeCount, cmd := charges.snapshot()
					if scenario == "no_usage" || scenario == "zero_usage" {
						require.Zero(t, logCount)
						require.Zero(t, chargeCount)
						return
					}
					require.Equal(t, 1, logCount, recorder.Body.String())
					require.Equal(t, expectedInput, log.InputTokens)
					require.Equal(t, expectedOutput, log.OutputTokens)
					require.Equal(t, expectedRead, log.CacheReadTokens)
					require.Equal(t, expectedCreate, log.CacheCreationTokens)
					expected := 0.0
					if mode == service.BillingModeToken {
						expected = float64(expectedInput)*inputPrice + float64(expectedOutput)*outputPrice + float64(expectedRead)*cacheReadPrice + float64(expectedCreate)*cacheWritePrice
					} else if scenario == "partial_artifact" || scenario == "failover_success" {
						expected = price
					}
					require.InDelta(t, expected, log.ActualCost, 1e-12)
					if expected > 0 {
						require.Equal(t, 1, chargeCount)
						require.InDelta(t, expected, cmd.BalanceCost, 1e-12)
					} else {
						require.LessOrEqual(t, chargeCount, 1)
						if cmd != nil {
							require.Zero(t, cmd.BalanceCost)
						}
					}
				})
			}
		}
	}
}
