//go:build unit

package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestImagesNonStreamingObservedUsageBilling(t *testing.T) {
	for _, route := range []string{"oauth_responses", "oauth_direct"} {
		for _, mode := range []service.BillingMode{service.BillingModeToken, service.BillingModeImage, service.BillingModePerRequest} {
			for _, scenario := range []string{"metered_failure", "metered_keepalive", "input_only", "output_only", "cache_read_only", "cache_create_only", "no_usage", "zero_usage", "failover_success", "success", "partial_read", "http_400", "invalid_json"} {
				if scenario == "invalid_json" && route != "oauth_direct" {
					continue
				}
				t.Run(route+"/"+string(mode)+"/"+scenario, func(t *testing.T) {
					testLog, observedLogs := observer.New(zap.DebugLevel)
					ctx := logger.IntoContext(context.Background(), zap.New(testLog))
					model := "gpt-image-1"
					if route == "oauth_direct" {
						model = "gpt-image-2"
					}
					account := service.Account{ID: 1, Name: "mock-images", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true, Concurrency: 2, Credentials: map[string]any{"access_token": "local-test", "chatgpt_account_id": "local-test"}}
					other := account
					other.ID = 2
					accounts := openAIImagesFailoverAccountRepo{accounts: []service.Account{account, other}}
					usage := `"usage":{"input_tokens":7,"output_tokens":3}`
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
					case "no_usage":
						usage = `"usage":{}`
					case "zero_usage":
						usage = `"usage":{"input_tokens":0,"output_tokens":0}`
					}
					const image = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg=="
					upstream := &imageUsageBillingUpstream{respond: func(call int) *http.Response {
						attemptUsage := usage
						errorJSON := `"error":{"type":"invalid_request_error","code":"content_policy_violation","message":"blocked"}`
						if scenario == "failover_success" && call == 1 {
							attemptUsage = `"usage":{"input_tokens":99,"output_tokens":33}`
							errorJSON = `"error":{"type":"server_error","message":"unavailable"}`
						}
						success := scenario == "success" || (scenario == "failover_success" && call == 2)
						body := `{"type":"error",` + errorJSON + `,` + attemptUsage + `}`
						contentType := "application/json"
						if route == "oauth_responses" {
							contentType = "text/event-stream"
							body = "data: {\"type\":\"response.created\"," + attemptUsage + "}\n\ndata: {\"type\":\"error\"," + errorJSON + "}\n\n"
							if success {
								body = "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"image_generation_call\",\"status\":\"completed\",\"result\":\"" + image + "\"}]," + usage + "}}\n\n"
							}
						} else if success {
							body = `{"data":[{"b64_json":"` + image + `"}],` + usage + `}`
						}
						if scenario == "invalid_json" {
							body = `{"type":"error",` + errorJSON + `,` + usage + `} trailing`
						}
						terminal := io.EOF
						if scenario == "partial_read" {
							terminal = context.Canceled
						}
						status := http.StatusOK
						if scenario == "http_400" {
							status = http.StatusBadRequest
							contentType = "application/json"
							body = `{` + errorJSON + `,` + usage + `}`
						}
						return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}, "X-Request-Id": {fmt.Sprintf("mock-nonstream-attempt-%d", call)}}, Body: io.NopCloser(&imageUsageFailureReader{Reader: strings.NewReader(body), terminal: terminal})}
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
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(fmt.Sprintf(`{"model":%q,"prompt":"draw","stream":false}`, model))).WithContext(ctx)
					c.Request.Header.Set("Content-Type", "application/json")
					c.Set(string(middleware.ContextKeyAPIKey), key)
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 10, Concurrency: 2})
					if scenario == "metered_keepalive" {
						stop := service.StartOpenAIImagesJSONKeepalive(c, time.Millisecond)
						defer stop()
						require.Eventually(t, func() bool { return c.Writer.Written() }, time.Second, time.Millisecond)
						require.True(t, service.StopOpenAIImagesJSONKeepaliveCommitted(c))
					}
					h.Images(c)
					pool.Stop()
					slots.assertReleased(t)
					require.Zero(t, observedLogs.FilterMessage("openai.responses_panic_recovered").Len())
					expectedCalls := 1
					if scenario == "failover_success" {
						expectedCalls = 2
					}
					require.Equal(t, expectedCalls, upstream.calls, recorder.Body.String())
					assertTerminalError := func() {
						if scenario == "success" || scenario == "failover_success" || scenario == "partial_read" || scenario == "http_400" || scenario == "invalid_json" {
							return
						}
						wantStatus := http.StatusBadRequest
						if scenario == "metered_keepalive" {
							wantStatus = http.StatusOK // Whitespace keepalive already committed the status.
						}
						require.Equal(t, wantStatus, recorder.Code)
						require.JSONEq(t, `{"error":{"type":"invalid_request_error","code":"content_policy_violation","message":"blocked"}}`, recorder.Body.String())
					}
					logCount, log := logs.snapshot()
					chargeCount, cmd := charges.snapshot()
					if scenario == "no_usage" || scenario == "zero_usage" || scenario == "invalid_json" || scenario == "partial_read" || scenario == "http_400" {
						require.Zero(t, logCount)
						require.Zero(t, chargeCount)
						assertTerminalError()
						return
					}
					require.Equal(t, 1, logCount, recorder.Body.String())
					assertTerminalError()
					require.Equal(t, expectedInput, log.InputTokens)
					require.Equal(t, expectedOutput, log.OutputTokens)
					require.Equal(t, expectedRead, log.CacheReadTokens)
					require.Equal(t, expectedCreate, log.CacheCreationTokens)
					expected := 0.0
					if mode == service.BillingModeToken {
						expected = float64(expectedInput)*inputPrice + float64(expectedOutput)*outputPrice + float64(expectedRead)*cacheReadPrice + float64(expectedCreate)*cacheWritePrice
					} else if scenario == "success" || scenario == "failover_success" {
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
