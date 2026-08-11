package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
)

const defaultOpenAIImageUpstreamTimeout = 180 * time.Second
const defaultOpenAIImageRequestTimeout = 600 * time.Second

func (s *OpenAIGatewayService) openAIImageUpstreamTimeout() time.Duration {
	if s == nil || s.cfg == nil {
		return defaultOpenAIImageUpstreamTimeout
	}
	seconds := s.cfg.Gateway.ImageUpstreamTimeoutSeconds
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func (s *OpenAIGatewayService) withOpenAIImageUpstreamTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := s.openAIImageUpstreamTimeout()
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func (s *OpenAIGatewayService) withOpenAIImageUpstreamTimeoutFor(ctx context.Context, account *Account, capability smartrouter.Capability) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := s.openAIImageUpstreamTimeout()
	engine := s.smartRouterAdaptiveTimeoutEngine()
	if engine != nil && engine.Config().Enabled && s.isSmartRouterEnabled() {
		laneID := ""
		if account != nil {
			laneID = "account:" + formatAccountID(account.ID)
		}
		remaining := time.Duration(0)
		if deadline, ok := ctx.Deadline(); ok {
			remaining = time.Until(deadline)
		}
		request := smartrouter.TimeoutRequest{
			LaneID:          laneID,
			Capability:      capability,
			DefaultTimeout:  timeout,
			RemainingBudget: remaining,
		}
		if tier, ok := OpenAIImageSmartRouterSizeTierFromContext(ctx); ok {
			request.ImageSizeTier = tier
		}
		if mode, ok := OpenAIImageSmartRouterInputModeFromContext(ctx); ok {
			request.ImageInputMode = mode
		}
		if model, ok := OpenAIImageSmartRouterModelFamilyFromContext(ctx); ok {
			request.ImageModelFamily = model
		}
		if profile, ok := s.smartRouterImageTimeoutProfile(ctx, capability); ok {
			request.ProfileDefault = profile.Default
			request.ProfileMin = profile.Min
			request.ProfileMax = profile.Max
			request.ProfileSafetyMargin = profile.SafetyMargin
			request.ProfileMultiplier = profile.Multiplier
			request.ProfileReservePerAttempt = profile.ReservePerAttempt
		}
		decision := engine.TimeoutFor(request)
		timeout = decision.Timeout
	}
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func (s *OpenAIGatewayService) smartRouterAdaptiveTimeoutEngine() *smartrouter.AdaptiveTimeoutEngine {
	if s == nil {
		return nil
	}
	s.smartRouterAdaptiveTimeoutOnce.Do(func() {
		s.smartRouterAdaptiveTimeout = smartrouter.NewAdaptiveTimeoutEngine(s.smartRouterAdaptiveTimeoutConfig())
	})
	return s.smartRouterAdaptiveTimeout
}

func (s *OpenAIGatewayService) observeSmartRouterImageAttempt(ctx context.Context, account *Account, capability smartrouter.Capability, duration time.Duration, statusCode int, success bool, requestErr error) {
	engine := s.smartRouterAdaptiveTimeoutEngine()
	if engine == nil || !engine.Config().Enabled || !s.isSmartRouterEnabled() || account == nil {
		return
	}
	failureClass := smartrouter.FailureClass("")
	if !success {
		if errors.Is(requestErr, context.Canceled) {
			failureClass = smartrouter.FailureCancelled
		} else if errors.Is(requestErr, context.DeadlineExceeded) {
			failureClass = smartrouter.FailureTimeout
		} else {
			failureClass = smartrouter.ClassifyFailure(statusCode, capability, false)
		}
	}
	observation := smartrouter.AttemptObservation{
		LaneID:       "account:" + formatAccountID(account.ID),
		Capability:   capability,
		Duration:     duration,
		Success:      success,
		FailureClass: failureClass,
		StatusCode:   statusCode,
		ErrorSummary: imageAttemptErrorSummary(statusCode, requestErr),
	}
	if tier, ok := OpenAIImageSmartRouterSizeTierFromContext(ctx); ok {
		observation.ImageSizeTier = tier
	}
	if mode, ok := OpenAIImageSmartRouterInputModeFromContext(ctx); ok {
		observation.ImageInputMode = mode
	}
	if model, ok := OpenAIImageSmartRouterModelFamilyFromContext(ctx); ok {
		observation.ImageModelFamily = model
	}
	engine.Observe(observation)
}

func imageAttemptErrorSummary(statusCode int, requestErr error) string {
	if requestErr != nil {
		return requestErr.Error()
	}
	if statusCode >= 400 {
		return fmt.Sprintf("status=%d", statusCode)
	}
	return ""
}

func formatAccountID(accountID int64) string {
	return strconv.FormatInt(accountID, 10)
}

func (s *OpenAIGatewayService) openAIImageRequestTimeout() time.Duration {
	if s == nil || s.cfg == nil {
		return defaultOpenAIImageRequestTimeout
	}
	seconds := s.cfg.Gateway.ImageRequestTimeoutSeconds
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// WithOpenAIImageRequestTimeout creates one deadline for the whole image
// request, including all Smart Router failover attempts. Per-upstream
// timeouts remain enforced by withOpenAIImageUpstreamTimeout.
func (s *OpenAIGatewayService) WithOpenAIImageRequestTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := s.openAIImageRequestTimeout()
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

// detachOpenAIImageUpstreamContext preserves an existing request deadline
// while retaining the historical non-stream behavior of ignoring an early
// client cancellation in the detached upstream path.
func detachOpenAIImageUpstreamContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.Background(), func() {}
	}
	if deadline, ok := ctx.Deadline(); ok {
		return context.WithDeadline(context.Background(), deadline)
	}
	return context.WithoutCancel(ctx), func() {}
}
