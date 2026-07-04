package core

import "net/http"

func ClassifyFailure(statusCode int, capability Capability, clientCancelled bool) FailureClass {
	if clientCancelled {
		return FailureCancelled
	}
	switch statusCode {
	case http.StatusBadRequest:
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
