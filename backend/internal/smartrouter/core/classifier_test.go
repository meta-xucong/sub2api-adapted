package core

import (
	"net/http"
	"testing"
)

func TestClassifyFailureDetails_CompactCapabilityErrorsAreDeterministic(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		message string
		want    FailureClass
	}{
		{name: "unsupported compact", status: 400, message: "compact endpoint is not supported", want: FailureCapabilityError},
		{name: "unknown parameter", status: 400, message: "unknown parameter compact", want: FailureCapabilityError},
		{name: "temporary outage remains transient", status: 503, message: "service temporarily unavailable", want: FailureUpstream5xx},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyFailureDetails(tt.status, CapabilityResponsesCompact, tt.message, "", false); got != tt.want {
				t.Fatalf("ClassifyFailureDetails() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifyFailureDetails_StreamInterruptedIsDistinctFromClientCancel(t *testing.T) {
	tests := []struct {
		name            string
		status          int
		capability      Capability
		message         string
		clientCancelled bool
		want            FailureClass
	}{
		{
			name:       "responses failed after stream started",
			status:     0,
			capability: CapabilityResponses,
			message:    "upstream response failed: An error occurred while processing your request. Please include the request ID req_123",
			want:       FailureStreamInterrupted,
		},
		{
			name:       "chat idle sse timeout",
			status:     http.StatusBadGateway,
			capability: CapabilityChat,
			message:    "idle timeout waiting for SSE",
			want:       FailureStreamInterrupted,
		},
		{
			name:       "compact stream read error",
			status:     0,
			capability: CapabilityResponsesCompact,
			message:    "stream read error: unexpected EOF",
			want:       FailureStreamInterrupted,
		},
		{
			name:       "content rejection is not lane health",
			status:     http.StatusBadRequest,
			capability: CapabilityResponses,
			message:    "upstream response failed: content policy violation",
			want:       FailureContentRejected,
		},
		{
			name:       "image keeps ordinary upstream class",
			status:     http.StatusBadGateway,
			capability: CapabilityImageGeneration,
			message:    "stream read error: unexpected EOF",
			want:       FailureUpstream5xx,
		},
		{
			name:            "downstream cancellation wins",
			status:          http.StatusBadGateway,
			capability:      CapabilityResponses,
			message:         "stream usage incomplete: context canceled",
			clientCancelled: true,
			want:            FailureCancelled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyFailureDetails(tt.status, tt.capability, tt.message, "", tt.clientCancelled); got != tt.want {
				t.Fatalf("ClassifyFailureDetails() = %q, want %q", got, tt.want)
			}
		})
	}
}
