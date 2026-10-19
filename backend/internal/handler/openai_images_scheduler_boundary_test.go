//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type imageBoundarySettings struct {
	service.SettingRepository
	enabled bool
}

func (r imageBoundarySettings) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{"openai_advanced_scheduler_enabled": strconv.FormatBool(r.enabled)}, nil
}
func (r imageBoundarySettings) GetValue(_ context.Context, key string) (string, error) {
	if key == service.SettingKeyOpenAIAPIKeyHealthBreakerSettings {
		return `{"enabled":true,"window_minutes":2,"failure_threshold":100,"cooldown_minutes":5}`, nil
	}
	return "", service.ErrSettingNotFound
}

type imageBoundaryBreakerCache struct{ records int }

func (c *imageBoundaryBreakerCache) RecordOpenAIAPIKeyHealthFailure(context.Context, int64, int, int) (int64, bool, error) {
	c.records++
	return int64(c.records), false, nil
}

// Expiry is published only at the transport boundary, after selection. This
// models the absolute-deadline/Err timer race deterministically, without sleeps.
type imageBoundaryDeadlineContext struct {
	context.Context
	expired    atomic.Bool
	publishErr bool
}

func (c *imageBoundaryDeadlineContext) Deadline() (time.Time, bool) {
	if c.expired.Load() {
		return time.Now().Add(-time.Hour), true
	}
	return time.Now().Add(time.Hour), true
}
func (c *imageBoundaryDeadlineContext) Err() error {
	if c.publishErr && c.expired.Load() {
		return context.DeadlineExceeded
	}
	return nil
}

type imageBoundaryCancelReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r imageBoundaryCancelReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		r.cancel()
	}
	return n, err
}

func TestImagesSchedulerRequestBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var pngBytes bytes.Buffer
	require.NoError(t, png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	encoded := base64.StdEncoding.EncodeToString(pngBytes.Bytes())
	for _, smart := range []bool{false, true} {
		for _, advanced := range []bool{false, true} {
			for _, seeded := range []bool{false, true} {
				for _, scenario := range []string{"canceled", "budget_expired", "deadline_before_timer", "attempt_timeout", "provider_5xx", "provider_5xx_then_cancel", "success", "success_then_cancel"} {
					t.Run(fmt.Sprintf("smart=%t/advanced=%t/seeded=%t/%s", smart, advanced, seeded, scenario), func(t *testing.T) {
						service.ResetOpenAIImageSchedulerSettingsForUnitTest()
						t.Cleanup(service.ResetOpenAIImageSchedulerSettingsForUnitTest)
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()
						var requestCtx context.Context = ctx
						deadline := &imageBoundaryDeadlineContext{Context: context.Background(), publishErr: scenario == "budget_expired"}
						if scenario == "budget_expired" || scenario == "deadline_before_timer" {
							requestCtx = deadline
						}
						account := service.Account{ID: 1, Name: "mock-image", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 2, Credentials: map[string]any{"api_key": "mock-only", "base_url": "https://images.example/v1", "pool_mode": true, "pool_mode_retry_status_codes": []any{}}}
						accounts := openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}
						slots := &grokMediaSlotsCache{accounts: map[string]int64{}, users: map[string]int64{}}
						concurrency := service.NewConcurrencyService(slots)
						cfg := &config.Config{RunMode: config.RunModeStandard}
						cfg.Gateway.ImageConcurrency.Enabled = true
						cfg.Gateway.ImageConcurrency.MaxConcurrentRequests = 1
						cfg.Gateway.SmartRouter.Enabled = smart
						// Request timeout is deliberately inherited from the controlled context.
						// Independent attempt expiry uses the actual configured attempt timer.
						if scenario == "attempt_timeout" {
							cfg.Gateway.ImageUpstreamTimeoutSeconds = 1
						}
						rateLimit := service.NewRateLimitService(accounts, nil, cfg, nil, nil)
						rateLimit.SetSettingService(service.NewSettingService(imageBoundarySettings{enabled: advanced}, cfg))
						breakerCache := &imageBoundaryBreakerCache{}
						if seeded {
							breakerCache.records = 2
						}
						rateLimit.SetOpenAIAPIKeyHealthCache(breakerCache)
						breakerBefore := breakerCache.records
						upstream := &grokMediaSlotUpstream{call: func(req *http.Request, _ int64) (*http.Response, error) {
							switch scenario {
							case "canceled":
								cancel()
								return nil, context.Canceled
							case "budget_expired", "deadline_before_timer":
								deadline.expired.Store(true)
								return nil, context.DeadlineExceeded
							case "attempt_timeout":
								<-req.Context().Done()
								return nil, req.Context().Err()
							case "provider_5xx", "provider_5xx_then_cancel":
								var responseBody io.Reader = strings.NewReader(`{"error":{"message":"provider unavailable"}}`)
								if scenario == "provider_5xx_then_cancel" {
									responseBody = imageBoundaryCancelReader{Reader: responseBody, cancel: cancel}
								}
								return &http.Response{StatusCode: http.StatusInternalServerError, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(responseBody)}, nil
							}
							var responseBody io.Reader = strings.NewReader(`{"created":1,"data":[{"b64_json":"` + encoded + `"}],"usage":{"input_tokens":7,"output_tokens":3}}`)
							if scenario == "success_then_cancel" {
								responseBody = imageBoundaryCancelReader{Reader: responseBody, cancel: cancel}
							}
							return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(responseBody)}, nil
						}}
						logs, charges := &grokRoutePricingCountingUsageLogRepo{}, &grokRoutePricingCountingUsageBillingRepo{}
						billing := service.NewBillingService(cfg, nil)
						gateway := service.NewOpenAIGatewayService(accounts, logs, charges, nil, nil, nil, nil, cfg, nil, concurrency, billing, rateLimit, nil, upstream, &service.DeferredService{}, nil, nil, service.NewModelPricingResolver(nil, billing), nil, nil, nil, nil)
						ledger := &ccDisconnectHealthLedger{}
						gateway.SetSmartRouterHealthLedger(ledger)
						parsed := &service.OpenAIImagesRequest{Model: "gpt-image-2"}
						if seeded {
							ttft := 125
							gateway.ReportOpenAIAccountScheduleResult(&account, parsed.Model, false, &ttft, errors.New("seed failure"))
							gateway.ReportSmartRouterImageCalibrationResult(&account, parsed, &service.OpenAIForwardResult{ImageCount: 1}, nil, 20)
						}
						fixedNow := time.Now()
						before := gateway.OpenAIImageSchedulingUnitSnapshot(&account, parsed.Model, fixedNow)
						ledgerBefore := len(ledger.events)
						billingCache := service.NewBillingCacheService(grokRoutePricingBalanceOnlyCache{}, nil, nil, nil, nil, nil, cfg, nil)
						t.Cleanup(billingCache.Stop)
						pool := newUsageRecordTestPool(t)
						h := NewOpenAIGatewayHandler(gateway, concurrency, billingCache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), pool, nil, nil, nil, cfg)
						h.maxAccountSwitches = 0
						groupID := int64(24)
						price := 0.25
						key := &service.APIKey{ID: 20, UserID: 10, GroupID: &groupID, User: &service.User{ID: 10, Balance: 100}, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, AllowImageGeneration: true, RateMultiplier: 1, ModelPricing: []service.ChannelModelPricing{{Models: []string{parsed.Model}, BillingMode: service.BillingModePerRequest, PerRequestPrice: &price}}}}
						recorder := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(recorder)
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewBufferString(`{"model":"gpt-image-2","prompt":"draw"}`)).WithContext(requestCtx)
						c.Request.Header.Set("Content-Type", "application/json")
						c.Set(string(middleware.ContextKeyAPIKey), key)
						c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 10, Concurrency: 2})
						h.Images(c)
						pool.Stop()
						slots.assertReleased(t)
						h.imageLimiter.mu.Lock()
						imageActive, imageWaiting := h.imageLimiter.active, h.imageLimiter.waiting
						h.imageLimiter.mu.Unlock()
						require.Zero(t, imageActive, "global image slot is released")
						require.Zero(t, imageWaiting)
						require.Equal(t, 1, upstream.calls, recorder.Body.String())
						require.Positive(t, slots.acquired)
						require.Positive(t, slots.userAcquired)
						after := gateway.OpenAIImageSchedulingUnitSnapshot(&account, parsed.Model, fixedNow)
						expectedBreaker := breakerBefore
						if !smart && (scenario == "attempt_timeout" || strings.HasPrefix(scenario, "provider_5xx")) {
							expectedBreaker++
						}
						require.Equal(t, expectedBreaker, breakerCache.records, "Smart Router-owned image recovery must not change the shared account circuit breaker; legacy mode retains its behavior")
						logCount, _ := logs.snapshot()
						chargeCount, command := charges.snapshot()
						requestEnded := scenario == "canceled" || scenario == "budget_expired" || scenario == "deadline_before_timer"
						if requestEnded {
							require.Equal(t, before, after, "request cancellation must preserve both existing and absent health stats")
							require.Len(t, ledger.events, ledgerBefore)
						} else if smart {
							require.Equal(t, before.Accounts, after.Accounts, "image outcomes must not create shared scheduler stats")
							require.Equal(t, before.ErrorRate, after.ErrorRate, "image failure and success must not change shared account EWMA")
							require.Equal(t, before.TTFT, after.TTFT)
							require.Equal(t, before.HasTTFT, after.HasTTFT)
							if strings.HasSuffix(scenario, "then_cancel") {
								require.Equal(t, before.Health, after.Health, "caller cancellation after forwarding must not alter image health")
							}
						} else if advanced {
							require.Equal(t, 1, after.Accounts)
							if strings.HasPrefix(scenario, "success") {
								require.LessOrEqual(t, after.ErrorRate, before.ErrorRate)
							} else {
								require.Greater(t, after.ErrorRate, before.ErrorRate)
							}
							require.Equal(t, before.TTFT, after.TTFT)
							require.Equal(t, before.HasTTFT, after.HasTTFT)
						} else {
							require.Zero(t, after.Accounts)
						}
						if strings.HasPrefix(scenario, "success") {
							require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
							require.Equal(t, 1, logCount)
							require.Equal(t, 1, chargeCount)
							require.InDelta(t, price, command.BalanceCost, 1e-12)
						} else {
							require.Zero(t, logCount)
							require.Zero(t, chargeCount)
							require.Nil(t, command)
							if scenario == "canceled" || scenario == "provider_5xx_then_cancel" {
								// The existing disconnect path does not write an HTTP error
								// after the caller has gone away.
								require.Empty(t, recorder.Body.String())
							} else {
								require.Equal(t, http.StatusBadGateway, recorder.Code, recorder.Body.String())
							}
						}
						if smart && !requestEnded && !strings.HasSuffix(scenario, "then_cancel") {
							require.Len(t, ledger.events, ledgerBefore+1)
							if scenario == "attempt_timeout" {
								require.Equal(t, core.FailureTimeout, ledger.events[len(ledger.events)-1].FailureClass)
								require.Positive(t, ledger.events[len(ledger.events)-1].HealthPenalty)
							}
							if scenario == "provider_5xx" {
								require.Positive(t, ledger.events[len(ledger.events)-1].ConsecutiveFailures)
							}
						}
					})
				}
			}
		}
	}
}
