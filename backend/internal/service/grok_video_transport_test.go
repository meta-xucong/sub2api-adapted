//go:build unit

package service

import "testing"

func TestGrokVideoTransportDefaultsAndExactProviderDetection(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		transport string
		want      GrokVideoTransport
	}{
		{name: "ordinary grok stays openai compatible", baseURL: "https://api.x.ai/v1", want: GrokVideoTransportOpenAICompat},
		{name: "wokey canonical host opts in", baseURL: "https://api.wokey.ai/v1", want: GrokVideoTransportWokeyMultipart},
		{name: "wokey lookalike stays disabled", baseURL: "https://api.wokey.ai.attacker.example/v1", want: GrokVideoTransportOpenAICompat},
		{name: "kie canonical host opts in", baseURL: "https://api.kie.ai", want: GrokVideoTransportKIEJobs},
		{name: "kie lookalike stays disabled", baseURL: "https://api.kie.ai.attacker.example", want: GrokVideoTransportOpenAICompat},
		{name: "explicit custom kie profile opts in", baseURL: "https://relay.example/v1", transport: "kie_jobs", want: GrokVideoTransportKIEJobs},
		{name: "unknown profile is safe default", baseURL: "https://api.x.ai/v1", transport: "future_native", want: GrokVideoTransportOpenAICompat},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &Account{
				Platform: PlatformGrok,
				Type:     AccountTypeAPIKey,
				Credentials: map[string]any{
					"base_url":             tt.baseURL,
					"grok_video_transport": tt.transport,
				},
			}
			if got := account.GrokVideoTransport(); got != tt.want {
				t.Fatalf("transport = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKIEJobsMediaEndpointGateIsNegativeForUnsupportedRoutes(t *testing.T) {
	account := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://api.kie.ai",
		},
	}
	if account.SupportsGrokMediaEndpoint(GrokMediaEndpointVideosEdits) {
		t.Fatal("KIE jobs account must not accept video edits")
	}
	if !account.SupportsGrokMediaEndpoint(GrokMediaEndpointVideoStatus) {
		t.Fatal("KIE jobs account must accept status polling")
	}
}
