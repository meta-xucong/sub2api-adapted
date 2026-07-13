package core

import (
	"net/http"
	"strings"
)

func ClassifyFailure(statusCode int, capability Capability, clientCancelled bool) FailureClass {
	return ClassifyFailureDetails(statusCode, capability, "", "", clientCancelled)
}

// ClassifyFailureDetails separates deterministic capability failures from
// transient provider failures. The distinction is important for image lanes:
// a provider that returns an edit-style text reply for a generation request
// should be quarantined for that capability instead of retried every 30s.
func ClassifyFailureDetails(statusCode int, capability Capability, message string, code string, clientCancelled bool) FailureClass {
	if clientCancelled {
		return FailureCancelled
	}
	lower := strings.ToLower(strings.TrimSpace(message + " " + code))
	if strings.Contains(lower, "upstream_text_reply") ||
		(strings.Contains(lower, "requires a usable image target") && capability == CapabilityImageGeneration) ||
		(strings.Contains(lower, "upload the reference image") && capability == CapabilityImageGeneration) {
		return FailureCapabilityError
	}
	if capability == CapabilityResponsesCompact && strings.Contains(lower, "compact") {
		for _, marker := range []string{"unsupported", "not support", "not available", "disabled", "unknown parameter", "no matching"} {
			if strings.Contains(lower, marker) {
				return FailureCapabilityError
			}
		}
	}
	if strings.Contains(lower, "moderation") || strings.Contains(lower, "content policy") || strings.Contains(lower, "safety violation") {
		return FailureContentRejected
	}
	switch statusCode {
	case http.StatusBadRequest:
		if strings.Contains(lower, "unsupported") || strings.Contains(lower, "invalid parameter") || strings.Contains(lower, "invalid size") {
			return FailurePayloadRejected
		}
		return FailureClientError
	case http.StatusUnauthorized:
		return FailureAuthForbidden
	case http.StatusForbidden:
		if capability == CapabilityImageGeneration || capability == CapabilityImageEdit {
			return FailureTransientForbidden
		}
		return FailureAuthForbidden
	case http.StatusTooManyRequests:
		return FailureRateLimited
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return FailureTimeout
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
		return FailureUpstream5xx
	default:
		if statusCode >= 500 {
			return FailureUpstream5xx
		}
		if statusCode >= 400 {
			return FailureClientError
		}
		return FailureUnknown
	}
}
