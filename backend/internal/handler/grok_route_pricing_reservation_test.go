//go:build unit

package handler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type grokRoutePricingBalanceOnlyCache struct {
	service.BillingCache
}

func (grokRoutePricingBalanceOnlyCache) GetUserBalance(context.Context, int64) (float64, error) {
	return 10, nil
}

func (s *grokMediaSlotBindings) ReleaseGrokVideoBilled(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.billed, key)
	return nil
}

type grokRoutePricingCountingUsageLogRepo struct {
	service.UsageLogRepository
	mu      sync.Mutex
	created int
	last    *service.UsageLog
}

type grokRoutePricingCountingUsageBillingRepo struct {
	service.UsageBillingRepository
	mu      sync.Mutex
	applied int
	last    *service.UsageBillingCommand
}

func (r *grokRoutePricingCountingUsageBillingRepo) Apply(_ context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applied++
	copy := *cmd
	r.last = &copy
	return &service.UsageBillingApplyResult{Applied: true}, nil
}

func (r *grokRoutePricingCountingUsageBillingRepo) snapshot() (int, *service.UsageBillingCommand) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		return r.applied, nil
	}
	copy := *r.last
	return r.applied, &copy
}

type grokRoutePricingAccountingCache struct {
	*grokRoutePricingCountingReservationCache
	deducted float64
}

func (c *grokRoutePricingAccountingCache) DeductUserBalance(_ context.Context, _ int64, amount float64) error {
	c.deducted += amount
	return nil
}

func (r *grokRoutePricingCountingUsageLogRepo) Create(_ context.Context, log *service.UsageLog) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.created++
	copy := *log
	r.last = &copy
	return true, nil
}

func (r *grokRoutePricingCountingUsageLogRepo) snapshot() (int, *service.UsageLog) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		return r.created, nil
	}
	copy := *r.last
	return r.created, &copy
}

type grokRoutePricingCountingReservationCache struct {
	*handlerInflightCache
	attempts int
}

func (c *grokRoutePricingCountingReservationCache) ReserveInflightBalance(ctx context.Context, userID int64, requestID string, amount, balance float64, ttl time.Duration) (bool, float64, error) {
	c.attempts++
	return c.handlerInflightCache.ReserveInflightBalance(ctx, userID, requestID, amount, balance, ttl)
}

func grokRoutePricingCompletionFixture(t *testing.T) (*OpenAIGatewayHandler, *gin.Context, *service.APIKey, middleware2.AuthSubject, *service.OpenAIForwardResult, service.UnifiedGatewayRoutePricingDecision, *grokMediaSlotBindings) {
	t.Helper()
	h, _, bindings, _ := newGrokMediaSlotHandler(t, false, false)
	c, _ := grokMediaSlotContext(context.Background(), false)
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	require.True(t, ok)
	apiKey.Group.RateMultiplier = 1
	subject := middleware2.AuthSubject{UserID: 10, Concurrency: 5}
	unitPrice := 0.29
	decision := service.UnifiedGatewayRoutePricingDecision{
		Revision:             4,
		GroupID:              24,
		Model:                "grok-imagine-video-1.5",
		Kind:                 service.UnifiedGatewayRoutePricingVideo,
		VideoResolution:      "720p",
		VideoDurationSeconds: 5,
		Entries: []service.UnifiedGatewayRoutePricingEntry{{
			AccountID:            1,
			Model:                "grok-imagine-video-1.5",
			Kind:                 service.UnifiedGatewayRoutePricingVideo,
			UnitPrice:            &unitPrice,
			VideoResolution:      "720p",
			VideoDurationSeconds: 5,
		}},
	}
	result := &service.OpenAIForwardResult{
		ResponseID:           "task",
		Model:                "grok-imagine-video-1.5",
		BillingModel:         "grok-imagine-video-1.5",
		VideoCount:           1,
		VideoResolution:      "720p",
		VideoDurationSeconds: 5,
		RoutePricingDecision: &decision,
	}
	return h, c, apiKey, subject, result, decision, bindings
}

func configureGrokRoutePricingUsageRecorder(t *testing.T, h *OpenAIGatewayHandler, bindings *grokMediaSlotBindings) (*grokRoutePricingCountingUsageLogRepo, *grokRoutePricingCountingUsageBillingRepo, *grokRoutePricingAccountingCache) {
	t.Helper()
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	account := service.Account{ID: 1, Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey}
	repo := grokMediaSlotRepo{openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}}
	usageLogs := &grokRoutePricingCountingUsageLogRepo{}
	usageBilling := &grokRoutePricingCountingUsageBillingRepo{}
	accountingCache := &grokRoutePricingAccountingCache{grokRoutePricingCountingReservationCache: &grokRoutePricingCountingReservationCache{handlerInflightCache: newHandlerInflightCache(10)}}
	billingCache := service.NewBillingCacheService(accountingCache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	billing := service.NewBillingService(cfg, nil)
	deferred := service.NewDeferredService(repo, nil, time.Second)
	h.gatewayService = service.NewOpenAIGatewayService(
		repo, usageLogs, usageBilling, nil, nil, nil, bindings, cfg, nil, nil, billing,
		nil, billingCache, nil, deferred, nil, nil, nil, nil, nil, nil, nil,
	)
	h.billingCacheService = billingCache
	return usageLogs, usageBilling, accountingCache
}

func TestReserveGrokVideoRoutePricing_EnablesExactFixedPriceAfterSuccessfulReserve(t *testing.T) {
	h, c, apiKey, subject, _, decision, bindings := grokRoutePricingCompletionFixture(t)
	err := h.gatewayService.StoreGrokVideoPendingBilling(c.Request.Context(), "task", subject.UserID, apiKey.ID, service.GrokVideoPendingBilling{
		Model:                decision.Model,
		BillingModel:         decision.Model,
		VideoResolution:      decision.VideoResolution,
		VideoDurationSeconds: decision.VideoDurationSeconds,
		RoutePricingDecision: &decision,
	})
	require.NoError(t, err)
	status := &service.OpenAIForwardResult{ResponseID: "task", Model: decision.Model, VideoCount: 1}
	completion := prepareGrokVideoCompletionBilling(c.Request.Context(), h, zap.NewNop(), apiKey, subject, "task", status)
	require.NotNil(t, completion, "completion claim is acquired before the reserve attempt")
	require.NotNil(t, completion.RoutePricingDecision)
	require.False(t, completion.RoutePricingDecision.Allowed)
	require.Len(t, bindings.billed, 1)

	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	cache := &grokRoutePricingCountingReservationCache{handlerInflightCache: newHandlerInflightCache(10)}
	h.billingCacheService = service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(h.billingCacheService.Stop)

	done := reserveGrokVideoRoutePricing(c, h, apiKey, nil, &service.Account{ID: 1}, completion)
	require.NotNil(t, completion.RoutePricingDecision)
	require.True(t, completion.RoutePricingDecision.Allowed, "matching fixed price becomes eligible after its completion-time reserve succeeds")
	active, ok := service.UnifiedGatewayRoutePricingDecisionFromContext(c.Request.Context())
	require.True(t, ok)
	require.True(t, active.Allowed)
	require.Equal(t, decision.Revision, active.Revision)

	cache.mu.Lock()
	var reserved float64
	for _, amount := range cache.res {
		reserved = amount
	}
	cache.mu.Unlock()
	require.InDelta(t, 0.29, reserved, 1e-9, "reserve the exact configured 720p/5s unit price")
	require.Equal(t, 1, cache.attempts)

	nativeCost := &service.CostBreakdown{ActualCost: 0.41, TotalCost: 0.32}
	service.ApplyUnifiedGatewayRoutePricing(c.Request.Context(), apiKey, 1, decision.Model, "", "", nil, 0, 1, decision.VideoResolution, decision.VideoDurationSeconds, nativeCost, 1, 1)
	require.Equal(t, 0.29, nativeCost.ActualCost, "settlement applies the route's fixed video price")
	require.Equal(t, 0.32, nativeCost.TotalCost, "native upstream cost remains unchanged")

	competing := prepareGrokVideoCompletionBilling(c.Request.Context(), h, zap.NewNop(), apiKey, subject, "task", status)
	require.Nil(t, competing, "the second completion cannot obtain the one-shot billing claim")
	require.Equal(t, 1, cache.attempts, "the competing completion must not issue another reserve")
	require.Equal(t, 1, cache.count(), "only the first completion's reservation remains held")
	done()
	require.Zero(t, cache.count())
}

func TestReserveGrokVideoRoutePricing_SubscriptionUsesClaimWithoutBalanceReserve(t *testing.T) {
	h, c, apiKey, subject, _, decision, _ := grokRoutePricingCompletionFixture(t)
	err := h.gatewayService.StoreGrokVideoPendingBilling(c.Request.Context(), "task", subject.UserID, apiKey.ID, service.GrokVideoPendingBilling{
		Model:                decision.Model,
		BillingModel:         decision.Model,
		VideoResolution:      decision.VideoResolution,
		VideoDurationSeconds: decision.VideoDurationSeconds,
		RoutePricingDecision: &decision,
	})
	require.NoError(t, err)

	status := &service.OpenAIForwardResult{ResponseID: "task", Model: decision.Model, VideoCount: 1}
	completion := prepareGrokVideoCompletionBilling(c.Request.Context(), h, zap.NewNop(), apiKey, subject, "task", status)
	require.NotNil(t, completion, "completion claim is acquired before subscription settlement")
	require.NotNil(t, completion.RoutePricingDecision)
	require.False(t, completion.RoutePricingDecision.Allowed)

	apiKey.Group.SubscriptionType = service.SubscriptionTypeSubscription
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	cache := &grokRoutePricingCountingReservationCache{handlerInflightCache: newHandlerInflightCache(10)}
	h.billingCacheService = service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(h.billingCacheService.Stop)

	done := reserveGrokVideoRoutePricing(c, h, apiKey, &service.UserSubscription{ID: 42}, &service.Account{ID: 1}, completion)
	defer done()
	active, ok := service.UnifiedGatewayRoutePricingDecisionFromContext(c.Request.Context())
	require.True(t, ok)
	require.True(t, active.Allowed, "native subscription billing does not use a balance reservation")
	require.Zero(t, cache.attempts, "subscription completion must not attempt an inflight balance reserve")

	nativeCost := &service.CostBreakdown{ActualCost: 0.41, TotalCost: 0.32}
	service.ApplyUnifiedGatewayRoutePricing(c.Request.Context(), apiKey, 1, decision.Model, "", "", nil, 0, 1, decision.VideoResolution, decision.VideoDurationSeconds, nativeCost, 1, 1)
	require.Equal(t, 0.29, nativeCost.ActualCost, "exact pending video price applies to subscription usage")
	require.Equal(t, 0.32, nativeCost.TotalCost, "native upstream cost remains unchanged")
	competing := prepareGrokVideoCompletionBilling(c.Request.Context(), h, zap.NewNop(), apiKey, subject, "task", status)
	require.Nil(t, competing, "the subscription completion claim prevents duplicate usage settlement")
}

func TestReserveGrokVideoRoutePricing_FailOpenKeepsNativeCostAndBillingClaim(t *testing.T) {
	h, c, apiKey, subject, _, decision, _ := grokRoutePricingCompletionFixture(t)
	err := h.gatewayService.StoreGrokVideoPendingBilling(c.Request.Context(), "task", subject.UserID, apiKey.ID, service.GrokVideoPendingBilling{
		Model:                decision.Model,
		BillingModel:         decision.Model,
		VideoResolution:      decision.VideoResolution,
		VideoDurationSeconds: decision.VideoDurationSeconds,
		RoutePricingDecision: &decision,
	})
	require.NoError(t, err)

	status := &service.OpenAIForwardResult{ResponseID: "task", Model: decision.Model, VideoCount: 1}
	completion := prepareGrokVideoCompletionBilling(c.Request.Context(), h, zap.NewNop(), apiKey, subject, "task", status)
	require.NotNil(t, completion, "completion claim is acquired before the reserve attempt")
	require.NotNil(t, completion.RoutePricingDecision)
	require.False(t, completion.RoutePricingDecision.Allowed)

	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	// This cache can read a balance but has no inflight-reservation capability,
	// so the native billing service fails open with a nil reservation.
	h.billingCacheService = service.NewBillingCacheService(grokRoutePricingBalanceOnlyCache{}, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(h.billingCacheService.Stop)
	done := reserveGrokVideoRoutePricing(c, h, apiKey, nil, &service.Account{ID: 1}, completion)
	done()

	active, ok := service.UnifiedGatewayRoutePricingDecisionFromContext(c.Request.Context())
	require.True(t, ok)
	require.False(t, active.Allowed, "fail-open reservation must keep route pricing disabled")
	nativeCost := &service.CostBreakdown{ActualCost: 0.41, TotalCost: 0.32}
	service.ApplyUnifiedGatewayRoutePricing(c.Request.Context(), apiKey, 1, decision.Model, "", "", nil, 0, 1, decision.VideoResolution, decision.VideoDurationSeconds, nativeCost, 1, 1)
	require.Equal(t, 0.41, nativeCost.ActualCost, "failed custom reserve leaves user-facing native actual cost unchanged")
	require.Equal(t, 0.32, nativeCost.TotalCost, "route pricing never changes native cost-side total")

	claimed, err := h.gatewayService.ClaimGrokVideoBilling(c.Request.Context(), "task", subject.UserID, apiKey.ID)
	require.NoError(t, err)
	require.False(t, claimed, "reserve failure must not release the existing one-shot billing claim")
}

func TestReserveGrokVideoRoutePricing_UsageRecordFailureReleasesClaim(t *testing.T) {
	h, c, apiKey, subject, _, decision, bindings := grokRoutePricingCompletionFixture(t)
	err := h.gatewayService.StoreGrokVideoPendingBilling(c.Request.Context(), "task", subject.UserID, apiKey.ID, service.GrokVideoPendingBilling{
		Model:                decision.Model,
		BillingModel:         decision.Model,
		VideoResolution:      decision.VideoResolution,
		VideoDurationSeconds: decision.VideoDurationSeconds,
		RoutePricingDecision: &decision,
	})
	require.NoError(t, err)
	status := &service.OpenAIForwardResult{ResponseID: "task", Model: decision.Model, VideoCount: 1}
	completion := prepareGrokVideoCompletionBilling(c.Request.Context(), h, zap.NewNop(), apiKey, subject, "task", status)
	require.NotNil(t, completion, "first completion obtains the claim")
	require.Len(t, bindings.billed, 1)

	missingParentID := int64(999)
	shadowAccount := &service.Account{
		ID:              1,
		Platform:        service.PlatformOpenAI,
		Type:            service.AccountTypeOAuth,
		ParentAccountID: &missingParentID,
	}
	// The fixture repository contains no account 999, so native RecordUsage must
	// fail while resolving this shadow's credential parent.
	recordErr := h.gatewayService.RecordUsage(c.Request.Context(), &service.OpenAIRecordUsageInput{
		Result:  completion,
		APIKey:  apiKey,
		User:    apiKey.User,
		Account: shadowAccount,
	})
	require.Error(t, recordErr)

	recordGrokMediaUsage(c, h, zap.NewNop(), apiKey, subject, nil, shadowAccount, completion, decision.Model, nil, "task")
	claimed, err := h.gatewayService.ClaimGrokVideoBilling(c.Request.Context(), "task", subject.UserID, apiKey.ID)
	require.NoError(t, err)
	require.True(t, claimed, "native usage-record failure releases the one-shot claim for retry")
}

func TestReserveGrokVideoRoutePricing_ConcurrentCompletionClaimsAndSettlesOnce(t *testing.T) {
	h, _, apiKey, subject, _, decision, bindings := grokRoutePricingCompletionFixture(t)
	usageLogs, usageBilling, cache := configureGrokRoutePricingUsageRecorder(t, h, bindings)
	err := h.gatewayService.StoreGrokVideoPendingBilling(context.Background(), "task", subject.UserID, apiKey.ID, service.GrokVideoPendingBilling{
		Model:                decision.Model,
		BillingModel:         decision.Model,
		VideoResolution:      decision.VideoResolution,
		VideoDurationSeconds: decision.VideoDurationSeconds,
		RoutePricingDecision: &decision,
	})
	require.NoError(t, err)

	type completionAttempt struct {
		ctx        *gin.Context
		key        *service.APIKey
		completion *service.OpenAIForwardResult
		reserved   bool
		recorded   bool
	}
	attempts := make([]completionAttempt, 2)
	for i := range attempts {
		attempts[i].ctx, _ = grokMediaSlotContext(context.Background(), false)
		var ok bool
		attempts[i].key, ok = middleware2.GetAPIKeyFromContext(attempts[i].ctx)
		require.True(t, ok)
		attempts[i].key.Group.RateMultiplier = 1
	}

	start := make(chan struct{})
	var ready, finished sync.WaitGroup
	ready.Add(len(attempts))
	finished.Add(len(attempts))
	for i := range attempts {
		go func(i int) {
			defer finished.Done()
			ready.Done()
			<-start
			status := &service.OpenAIForwardResult{ResponseID: "task", Model: decision.Model, VideoCount: 1}
			attempts[i].completion = prepareGrokVideoCompletionBilling(
				attempts[i].ctx.Request.Context(), h, zap.NewNop(), attempts[i].key, subject, "task", status,
			)
			if attempts[i].completion == nil {
				return
			}
			account := &service.Account{ID: 1, Platform: service.PlatformGrok}
			done := reserveGrokVideoRoutePricing(attempts[i].ctx, h, attempts[i].key, nil, account, attempts[i].completion)
			attempts[i].reserved = true
			defer done()
			recordGrokMediaUsage(attempts[i].ctx, h, zap.NewNop(), attempts[i].key, subject, nil,
				account, attempts[i].completion, decision.Model, nil, "task")
			attempts[i].recorded = true
		}(i)
	}
	ready.Wait()
	close(start)
	finished.Wait()

	winner := -1
	for i := range attempts {
		if attempts[i].completion == nil {
			require.False(t, attempts[i].reserved)
			require.False(t, attempts[i].recorded)
			continue
		}
		require.Equal(t, -1, winner, "only one simultaneous completion may acquire the billing claim")
		require.True(t, attempts[i].reserved, "only the claim winner continues to the reserve path")
		require.True(t, attempts[i].recorded, "only the claim winner submits usage")
		winner = i
	}
	require.NotEqual(t, -1, winner, "one completion should acquire the billing claim")
	bindings.mu.Lock()
	require.Len(t, bindings.billed, 1)
	bindings.mu.Unlock()

	require.Equal(t, 1, cache.attempts, "only the claim winner may request a completion-time reserve")
	require.Zero(t, cache.count(), "usage submission releases the winner's reservation")
	created, usageLog := usageLogs.snapshot()
	require.Equal(t, 1, created, "only one final usage ledger row is submitted")
	require.NotNil(t, usageLog)
	require.Equal(t, 1, usageLog.VideoCount)
	require.Equal(t, 0.29, usageLog.ActualCost, "winner's usage log contains the exact fixed route price")
	billingApplies, billingCommand := usageBilling.snapshot()
	require.Equal(t, 1, billingApplies, "only one native balance-accounting transaction is submitted")
	require.NotNil(t, billingCommand)
	require.Equal(t, 0.29, billingCommand.BalanceCost, "the native accounting command carries the route price")
	require.Equal(t, 0.29, cache.deducted, "the committed user-balance cache deduction occurs once")

	for i := range attempts {
		if i == winner {
			continue
		}
		require.Nil(t, attempts[i].completion)
	}
	claimed, err := h.gatewayService.ClaimGrokVideoBilling(context.Background(), "task", subject.UserID, apiKey.ID)
	require.NoError(t, err)
	require.False(t, claimed, "successful usage submission retains the one-shot billing claim")
}
