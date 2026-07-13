package core

import "testing"

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
