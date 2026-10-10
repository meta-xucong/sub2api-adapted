package service

import (
	"context"
	"errors"
	"net/http"
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

// WithOpenAIImageRequestTimeout places one deadline around selection and all
// image-account failover attempts. Individual upstream deadlines are separate.
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

// detachOpenAIImageUpstreamContext retains the request-wide deadline while
// preserving the existing image-forwarding behavior after early client cancel.
func detachOpenAIImageUpstreamContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.Background(), func() {}
	}
	detached := context.WithoutCancel(ctx)
	if deadline, ok := ctx.Deadline(); ok {
		return context.WithDeadline(detached, deadline)
	}
	return detached, func() {}
}

// isOpenAIImageAttemptTimeout distinguishes an attempt-owned timeout from a
// caller cancellation or the request-wide budget expiring. Only attempt-owned
// timeouts may be translated into a cross-account failover.
func isOpenAIImageAttemptTimeout(err error, attemptCtx context.Context, requestCtx context.Context) bool {
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || attemptCtx == nil {
		return false
	}
	if requestCtx != nil && requestCtx.Err() != nil {
		return false
	}
	return errors.Is(attemptCtx.Err(), context.DeadlineExceeded)
}

func newOpenAIImageAttemptTimeoutFailover(resp *http.Response) *UpstreamFailoverError {
	var headers http.Header
	if resp != nil {
		headers = resp.Header.Clone()
	}
	return &UpstreamFailoverError{
		StatusCode:      http.StatusBadGateway,
		ResponseBody:    []byte(`{"error":{"type":"upstream_error","code":"upstream_timeout","message":"Upstream image request timed out"}}`),
		ResponseHeaders: headers,
		Reason:          GatewayFailureReason("openai_image_attempt_timeout"),
	}
}
