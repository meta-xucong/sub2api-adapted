package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestMaxOutputTokens(t *testing.T) {
	require.Equal(t, 1024, requestMaxOutputTokens([]byte(`{"max_tokens":1024}`)))
	require.Equal(t, 2048, requestMaxOutputTokens([]byte(`{"max_completion_tokens":2048}`)))
	require.Equal(t, 4096, requestMaxOutputTokens([]byte(`{"max_output_tokens":4096}`)))
	require.Equal(t, 512, requestMaxOutputTokens([]byte(`{"generationConfig":{"maxOutputTokens":512}}`)))
	require.Equal(t, 0, requestMaxOutputTokens([]byte(`{"max_tokens":"x"}`)))
	require.Equal(t, 0, requestMaxOutputTokens([]byte(`{}`)))
}

func TestGrokImageInflightEstimateDoesNotInventQualityTier(t *testing.T) {
	estimate := grokMediaInflightEstimate(
		service.GrokMediaEndpointImagesGenerations,
		"grok-imagine-image",
		service.GrokMediaRequestInfo{N: 1, Size: "1024x1024", SizeTier: "1K"},
		[]byte(`{"model":"grok-imagine-image"}`),
	)
	require.Equal(t, service.InflightEstimateImage, estimate.Kind)
	require.Equal(t, "1024x1024", estimate.ImageSize)
	require.Empty(t, estimate.ImageQuality, "unknown quality must not match a configured fixed price")
}

type countingEstimator struct {
	calls  int
	cost   float64
	priced bool
}

func (e *countingEstimator) EstimateInflightReservation(context.Context, *service.APIKey, service.InflightEstimateRequest) (float64, bool) {
	e.calls++
	return e.cost, e.priced
}

type routePricingReservationEstimator struct {
	nativeEstimate float64
	nativePriced   bool
	routeEstimate  float64
	decision       service.UnifiedGatewayRoutePricingDecision
	allowed        bool
	prepared       bool
}

func (e *routePricingReservationEstimator) EstimateInflightReservation(context.Context, *service.APIKey, service.InflightEstimateRequest) (float64, bool) {
	return e.nativeEstimate, e.nativePriced
}

func (e *routePricingReservationEstimator) PrepareUnifiedGatewayRoutePricingReservation(ctx context.Context, _ *service.APIKey, _ service.InflightEstimateRequest, nativeEstimate float64, nativePriced bool) (context.Context, float64, bool) {
	e.prepared = true
	// Keep the exact values passed by the reservation planner observable in test
	// through a decision revision and a non-zero estimate check below.
	if nativeEstimate != e.nativeEstimate || nativePriced != e.nativePriced {
		return ctx, 0, false
	}
	return service.WithUnifiedGatewayRoutePricingDecision(ctx, e.decision), e.routeEstimate, true
}

func routePricingTestDecision() service.UnifiedGatewayRoutePricingDecision {
	multiplier := 2.0
	return service.UnifiedGatewayRoutePricingDecision{
		Revision: 3,
		GroupID:  7,
		Model:    "m",
		Kind:     service.UnifiedGatewayRoutePricingToken,
		Entries: []service.UnifiedGatewayRoutePricingEntry{{
			AccountID:  55,
			Model:      "m",
			Kind:       service.UnifiedGatewayRoutePricingToken,
			Multiplier: &multiplier,
		}},
	}
}

func routePricingBasePriceTestDecision() service.UnifiedGatewayRoutePricingDecision {
	input, output, cacheRead := 10.0, 20.0, 5.0
	cacheWrite, cacheWrite5m, cacheWrite1h := 7.0, 15.0, 30.0
	return service.UnifiedGatewayRoutePricingDecision{
		Revision: 3,
		GroupID:  7,
		Model:    "m",
		Kind:     service.UnifiedGatewayRoutePricingToken,
		Entries: []service.UnifiedGatewayRoutePricingEntry{{
			AccountID: 55, Model: "m", Kind: service.UnifiedGatewayRoutePricingToken,
			TokenBasePrice: &service.UnifiedGatewayTokenBasePrice{
				InputPerMillion: &input, OutputPerMillion: &output, CacheReadPerMillion: &cacheRead,
				CacheWritePerMillion: &cacheWrite, CacheWrite5mPerMillion: &cacheWrite5m, CacheWrite1hPerMillion: &cacheWrite1h,
			},
		}},
	}
}

func newInflightTestGinContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return c
}

func TestReserveInflightBalance_SkipsWhenDisabledOrSubscription(t *testing.T) {
	cfg := &config.Config{}
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	est := &countingEstimator{cost: 1, priced: true}
	apiKey := &service.APIKey{User: &service.User{ID: 1}}

	done, err := reserveInflightBalance(newInflightTestGinContext(), billing, est, apiKey, nil, tokenInflightEstimate("m", []byte(`{}`)))
	require.NoError(t, err)
	done()
	require.Equal(t, 0, est.calls, "disabled switch must not even estimate")

	cfg.Billing.InflightReservation.Enabled = true
	apiKey.Group = &service.Group{SubscriptionType: service.SubscriptionTypeSubscription}
	done, err = reserveInflightBalance(newInflightTestGinContext(), billing, est, apiKey, &service.UserSubscription{}, tokenInflightEstimate("m", []byte(`{}`)))
	require.NoError(t, err)
	done()
	require.Equal(t, 0, est.calls, "subscription mode must be unaffected")
}

func TestReserveInflightBalance_UnpricedFailOpenByDefaultFailClosedOptIn(t *testing.T) {
	cache := newHandlerInflightCache(10)
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	apiKey := &service.APIKey{User: &service.User{ID: 1}}
	est := &countingEstimator{priced: false}

	done, err := reserveInflightBalance(newInflightTestGinContext(), billing, est, apiKey, nil, tokenInflightEstimate("unknown", nil))
	require.NoError(t, err)
	done()

	cfg.Billing.InflightReservation.FailClosedOnUnpriced = true
	_, err = reserveInflightBalance(newInflightTestGinContext(), billing, est, apiKey, nil, tokenInflightEstimate("unknown", nil))
	require.ErrorIs(t, err, service.ErrInsufficientBalance)
}

func TestReserveInflightBalance_RouteEstimateUsesMaxAndEnablesOnlyAfterReservation(t *testing.T) {
	for _, tc := range []struct {
		name           string
		nativeEstimate float64
		routeEstimate  float64
		wantEstimate   float64
	}{
		{name: "native estimate is larger", nativeEstimate: 0.75, routeEstimate: 0.5, wantEstimate: 0.75},
		{name: "route estimate is larger", nativeEstimate: 0.4, routeEstimate: 1.2, wantEstimate: 1.2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := newHandlerInflightCache(5)
			cfg := &config.Config{}
			cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
			billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			groupID := int64(7)
			apiKey := &service.APIKey{ID: 9, GroupID: &groupID, User: &service.User{ID: 1}, Group: &service.Group{ID: groupID, Platform: service.PlatformComposite}}
			estimator := &routePricingReservationEstimator{
				nativeEstimate: tc.nativeEstimate,
				nativePriced:   true,
				routeEstimate:  tc.routeEstimate,
				decision:       routePricingTestDecision(),
			}

			ctx, done, err := reserveInflightBalanceCtx(context.Background(), billing, estimator, apiKey, nil, tokenInflightEstimate("m", nil))
			require.NoError(t, err)
			require.True(t, estimator.prepared)
			decision, ok := service.UnifiedGatewayRoutePricingDecisionFromContext(ctx)
			require.True(t, ok)
			require.True(t, decision.Allowed, "a successful reservation enables route pricing")
			cache.mu.Lock()
			var reserved float64
			for _, amount := range cache.res {
				reserved = amount
			}
			cache.mu.Unlock()
			require.InDelta(t, tc.wantEstimate, reserved, 1e-9, "reserve max(native estimate, route estimate)")
			done()
			require.Zero(t, cache.count())
		})
	}
}

func TestReserveInflightBalance_RoutePricingDisabledWhenReservationFailsOpen(t *testing.T) {
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	// No reservation cache makes ReserveInflight return nil without error (native
	// fail-open). Route price must stay disabled so settlement uses native cost.
	billing := service.NewBillingCacheService(&handlerBalanceOnlyCache{}, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	groupID := int64(7)
	apiKey := &service.APIKey{ID: 9, GroupID: &groupID, User: &service.User{ID: 1}, Group: &service.Group{ID: groupID, Platform: service.PlatformComposite}}
	estimator := &routePricingReservationEstimator{
		nativeEstimate: 0.4,
		nativePriced:   true,
		routeEstimate:  1.2,
		decision:       routePricingTestDecision(),
	}

	ctx, done, err := reserveInflightBalanceCtx(context.Background(), billing, estimator, apiKey, nil, tokenInflightEstimate("m", nil))
	require.NoError(t, err)
	done()
	decision, ok := service.UnifiedGatewayRoutePricingDecisionFromContext(ctx)
	require.True(t, ok)
	require.False(t, decision.Allowed, "nil fail-open reservation must preserve native settlement")

	cost := &service.CostBreakdown{ActualCost: 3.5, TotalCost: 2.1}
	service.ApplyUnifiedGatewayRoutePricing(ctx, apiKey, 55, "m", "", "", nil, 0, 0, "", 0, cost, 1, 1)
	require.Equal(t, 3.5, cost.ActualCost, "settlement remains at native actual cost")
	require.Equal(t, 2.1, cost.TotalCost, "route pricing never changes native cost-side total")
}

func TestReserveInflightBalance_TokenBasePriceRequiresSuccessfulReservation(t *testing.T) {
	groupID := int64(7)
	apiKey := &service.APIKey{ID: 9, GroupID: &groupID, User: &service.User{ID: 1}, Group: &service.Group{ID: groupID, Platform: service.PlatformComposite}}
	estimator := &routePricingReservationEstimator{
		nativeEstimate: 0.1, nativePriced: true, routeEstimate: 0.5, decision: routePricingBasePriceTestDecision(),
	}

	t.Run("disabled reservation blocks before route price is authorized", func(t *testing.T) {
		cfg := &config.Config{}
		billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(billing.Stop)
		disabledEstimator := &routePricingReservationEstimator{decision: routePricingBasePriceTestDecision(), routeEstimate: 0.5}
		ctx, done, err := reserveInflightBalanceCtx(context.Background(), billing, disabledEstimator, apiKey, nil, tokenInflightEstimate("m", []byte(`{"max_tokens":100}`)))
		done()
		require.ErrorIs(t, err, service.ErrInsufficientBalance)
		decision, ok := service.UnifiedGatewayRoutePricingDecisionFromContext(ctx)
		require.True(t, ok)
		require.False(t, decision.Allowed)
	})

	t.Run("fail-open reservation facility blocks base price", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
		billing := service.NewBillingCacheService(&handlerBalanceOnlyCache{}, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(billing.Stop)
		ctx, done, err := reserveInflightBalanceCtx(context.Background(), billing, estimator, apiKey, nil, tokenInflightEstimate("m", []byte(`{"max_tokens":100}`)))
		done()
		require.ErrorIs(t, err, service.ErrInsufficientBalance)
		decision, ok := service.UnifiedGatewayRoutePricingDecisionFromContext(ctx)
		require.True(t, ok)
		require.False(t, decision.Allowed)
	})

	t.Run("reservation cap cannot silently under-reserve", func(t *testing.T) {
		cache := newHandlerInflightCache(10)
		cfg := &config.Config{}
		cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60, MaxReservationUSD: 0.2}
		billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(billing.Stop)
		ctx, done, err := reserveInflightBalanceCtx(context.Background(), billing, estimator, apiKey, nil, tokenInflightEstimate("m", []byte(`{"max_tokens":100}`)))
		done()
		require.ErrorIs(t, err, service.ErrInsufficientBalance)
		require.Zero(t, cache.count(), "request is rejected before an undersized reservation is registered")
		decision, ok := service.UnifiedGatewayRoutePricingDecisionFromContext(ctx)
		require.True(t, ok)
		require.False(t, decision.Allowed)
	})
}

func TestReserveInflightBalance_ZeroEffectiveTokenBasePriceDoesNotRequireReservation(t *testing.T) {
	cache := newHandlerInflightCache(10)
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60, FailClosedOnUnpriced: true}
	billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	groupID := int64(7)
	apiKey := &service.APIKey{ID: 9, GroupID: &groupID, User: &service.User{ID: 1}, Group: &service.Group{ID: groupID, Platform: service.PlatformComposite}}
	estimator := &routePricingReservationEstimator{
		nativeEstimate: 0,
		nativePriced:   false,
		routeEstimate:  0, // positive unit prices multiplied by an effective group/user rate of zero
		decision:       routePricingBasePriceTestDecision(),
	}

	ctx, done, err := reserveInflightBalanceCtx(context.Background(), billing, estimator, apiKey, nil, tokenInflightEstimate("m", []byte(`{"max_tokens":100}`)))
	done()
	require.NoError(t, err, "a valid zero-charge request has no amount to reserve and must not be treated as unpriced")
	require.Zero(t, cache.count(), "zero estimate must not create an empty reservation")
	decision, ok := service.UnifiedGatewayRoutePricingDecisionFromContext(ctx)
	require.True(t, ok)
	require.True(t, decision.Allowed, "the exact matched base card remains eligible for settlement at zero effective rate")
}

type handlerBalanceOnlyCache struct{ service.BillingCache }

func (handlerBalanceOnlyCache) GetUserBalance(context.Context, int64) (float64, error) {
	return 10, nil
}

// handlerInflightCache 内存版余额缓存 + 在途预留（语义同 Redis Lua）。
type handlerInflightCache struct {
	service.BillingCache
	mu      sync.Mutex
	balance float64
	res     map[string]float64
}

func newHandlerInflightCache(balance float64) *handlerInflightCache {
	return &handlerInflightCache{balance: balance, res: map[string]float64{}}
}

func (m *handlerInflightCache) GetUserBalance(context.Context, int64) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.balance, nil
}

func (m *handlerInflightCache) GetUserPlatformQuotaCache(context.Context, int64, string) (*service.UserPlatformQuotaCacheEntry, bool, error) {
	return nil, false, nil
}

func (m *handlerInflightCache) ReserveInflightBalance(_ context.Context, _ int64, id string, amount, balance float64, _ time.Duration) (bool, float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sum := 0.0
	for _, v := range m.res {
		sum += v
	}
	if len(m.res) > 0 && balance-sum < amount {
		return false, sum, nil
	}
	m.res[id] = amount
	return true, sum, nil
}

func (m *handlerInflightCache) ReleaseInflightBalance(_ context.Context, _ int64, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.res, id)
	return nil
}

func (m *handlerInflightCache) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.res)
}

func TestWrapUsageRecordTaskContext_HandsReservationToBillingTask(t *testing.T) {
	cache := newHandlerInflightCache(1)
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	apiKey := &service.APIKey{User: &service.User{ID: 5}}

	c := newInflightTestGinContext()
	done, err := reserveInflightBalance(c, billing, &countingEstimator{cost: 0.9, priced: true}, apiKey, nil, tokenInflightEstimate("m", nil))
	require.NoError(t, err)
	require.Equal(t, 1, cache.count())

	ran := false
	task, abandon := wrapUsageRecordTaskContext(c.Request.Context(), func(context.Context) { ran = true })
	done() // handler returns; billing still pending
	require.Equal(t, 1, cache.count(), "reservation held until the billing task finishes")
	task(context.Background())
	require.True(t, ran)
	require.Equal(t, 0, cache.count())
	abandon() // idempotent with the task's own done

	// Dropped task: the submitter abandons it and the reservation is released.
	c2 := newInflightTestGinContext()
	done2, err := reserveInflightBalance(c2, billing, &countingEstimator{cost: 0.9, priced: true}, apiKey, nil, tokenInflightEstimate("m", nil))
	require.NoError(t, err)
	_, abandon2 := wrapUsageRecordTaskContext(c2.Request.Context(), func(context.Context) {})
	done2()
	require.Equal(t, 1, cache.count())
	abandon2()
	require.Equal(t, 0, cache.count())
}

// 新接入的端点（独立 web_search）：在途预留超过余额时拒绝，且不残留预留。
func TestWebSearch_RejectsWhenInflightExceedsBalance(t *testing.T) {
	cache := newHandlerInflightCache(1.5)
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	billingCache := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	billing := service.NewBillingService(cfg, nil)
	gw := service.NewGatewayService(
		nil, nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, billing, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, service.NewModelPricingResolver(nil, billing), nil, nil, nil,
	)
	h := &GatewayHandler{gatewayService: gw, billingCacheService: billingCache}

	groupID := int64(3)
	perK := 1000.0 // $1 per search
	apiKey := &service.APIKey{
		ID: 9, User: &service.User{ID: 42, Balance: 1.5}, GroupID: &groupID,
		Group: &service.Group{ID: groupID, Platform: service.PlatformGrok, RateMultiplier: 1, SearchPricePer1k: &perK},
	}

	// Another in-flight request of this user already holds $1.
	held, err := billingCache.ReserveInflight(context.Background(), apiKey.User, apiKey.Group, nil, 1.0)
	require.NoError(t, err)
	defer held.HandlerDone()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/web_search", bytes.NewBufferString(`{"query":"sub2api"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), apiKey)

	h.WebSearch(c)

	require.NotEqual(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "balance")
	require.Equal(t, 1, cache.count(), "rejected request must not leave a reservation behind")
}
