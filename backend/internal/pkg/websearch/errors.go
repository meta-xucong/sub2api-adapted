package websearch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// FailureKind is a local diagnostic category, never an upstream response body.
type FailureKind string

const (
	FailureInvalidKey     FailureKind = "invalid_api_key"
	FailureAuthentication FailureKind = "authentication_failed"
	FailureRateLimit      FailureKind = "rate_limited"
	FailureTimeout        FailureKind = "timeout"
	FailureCanceled       FailureKind = "canceled"
	FailureProxy          FailureKind = "proxy_failed"
	FailureRequest        FailureKind = "request_failed"
	FailureResponse       FailureKind = "invalid_response"
	FailureUpstream       FailureKind = "upstream_error"
)

// ProviderError contains only safe local diagnostics. Raw transport errors and
// upstream bodies are deliberately neither retained nor exposed by Unwrap.
type ProviderError struct {
	Provider     string
	Kind         FailureKind
	StatusCode   int // upstream HTTP status; never the application's auth status
	proxyFailure bool
}

func (e *ProviderError) Error() string {
	provider := safeProviderName(e.Provider)
	if e.StatusCode != 0 {
		return fmt.Sprintf("%s: status %d (%s)", provider, e.StatusCode, e.Kind)
	}
	if e.Kind == FailureResponse {
		return provider + ": decode response failed"
	}
	return fmt.Sprintf("%s: %s", provider, e.Kind)
}

// Is preserves cancellation/deadline checks without retaining the unsafe cause.
func (e *ProviderError) Is(target error) bool {
	return (e.Kind == FailureTimeout && target == context.DeadlineExceeded) ||
		(e.Kind == FailureCanceled && target == context.Canceled)
}

func safeProviderName(name string) string {
	if name == ProviderTypeBrave || name == ProviderTypeTavily {
		return name
	}
	return "websearch"
}

// ValidAPIKey rejects embedded whitespace/control/non-ASCII characters before
// constructing a credential header. Callers trim only leading/trailing paste
// whitespace; credentials are otherwise left unchanged.
func ValidAPIKey(key string) bool {
	if key == "" {
		return false
	}
	for _, c := range key {
		if c <= ' ' || c >= 127 {
			return false
		}
	}
	return true
}

func providerRequestError(provider string, err error) *ProviderError {
	kind := FailureRequest
	var netErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		kind = FailureTimeout
	case errors.Is(err, context.Canceled):
		kind = FailureCanceled
	case errors.As(err, &netErr) && netErr.Timeout():
		kind = FailureTimeout
	case isProxyError(err):
		kind = FailureProxy
	}
	return &ProviderError{Provider: provider, Kind: kind, proxyFailure: isProxyError(err)}
}

func providerStatusError(provider string, status int, body []byte) *ProviderError {
	kind := FailureUpstream
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		kind = FailureAuthentication
	case http.StatusTooManyRequests:
		kind = FailureRateLimit
	}
	// Preserve the existing production proxy-failure heuristic before discarding
	// untrusted text. Diagnostics must not change provider routing/quota policy.
	proxyFailure := isProxyError(errors.New(truncateBody(body)))
	return &ProviderError{Provider: provider, Kind: kind, StatusCode: status, proxyFailure: proxyFailure}
}

var (
	ErrNoTestProvider        = errors.New("websearch: no available provider for this test")
	ErrInvalidTestProvider   = errors.New("websearch: invalid test provider type")
	ErrAmbiguousTestProvider = errors.New("websearch: duplicate test provider type; save a valid configuration first")
	ErrEmptyTestQuery        = errors.New("websearch: empty search query")
)

// TestSearchError distinguishes attempted-but-failed searches from no candidates.
// Failures contain only redacted ProviderErrors, in the original attempt order.
type TestSearchError struct{ Failures []*ProviderError }

func (e *TestSearchError) Error() string {
	messages := make([]string, 0, len(e.Failures))
	for _, failure := range e.Failures {
		messages = append(messages, failure.Error())
	}
	return "websearch: all tested providers failed (" + strings.Join(messages, "; ") + ")"
}
