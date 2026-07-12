package service

import (
	"context"
	"time"
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
