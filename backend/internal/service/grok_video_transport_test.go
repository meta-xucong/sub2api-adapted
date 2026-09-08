//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

func TestGrokVideoTransportAutoDetection(t *testing.T) {
	tests := []struct {
		name       string
		baseURL    string
		configured string
		want       GrokVideoTransport
	}{
		{name: "default relay stays OpenAI compatible", baseURL: "https://relay.example.test/v1", want: GrokVideoTransportOpenAICompat},
		{name: "Subrouter alias stays OpenAI compatible", baseURL: "https://relay.example.test/v1", configured: "subrouter", want: GrokVideoTransportOpenAICompat},
		{name: "Wokey host is detected", baseURL: xai.WokeyAPIBaseURL, want: GrokVideoTransportWokeyMultipart},
		{name: "official KIE host is detected", baseURL: "https://api.kie.ai", want: GrokVideoTransportKIEJobs},
		{name: "explicit KIE wins on a custom relay", baseURL: "https://kie-relay.example.test", configured: "kie_jobs", want: GrokVideoTransportKIEJobs},
		{name: "explicit Wokey wins on a custom relay", baseURL: "https://relay.example.test/v1", configured: "wokey-multipart", want: GrokVideoTransportWokeyMultipart},
		{name: "unknown profile remains safe default", baseURL: "https://relay.example.test/v1", configured: "future-provider", want: GrokVideoTransportOpenAICompat},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			credentials := map[string]any{"base_url": tt.baseURL}
			if tt.configured != "" {
				credentials["grok_video_transport"] = tt.configured
			}
			account := &Account{Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: credentials}
			require.Equal(t, tt.want, account.GrokVideoTransport())
			require.Equal(t, tt.want == GrokVideoTransportKIEJobs, account.UsesKIEJobsVideoAPI())
			require.Equal(t, tt.want == GrokVideoTransportWokeyMultipart, account.UsesWokeyVideoMultipart())
		})
	}
}

func TestGrokVideoTransportDoesNotApplyToOtherPlatforms(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url":             "https://api.kie.ai",
			"grok_video_transport": "kie_jobs",
		},
	}
	require.Empty(t, account.GrokVideoTransport())
	require.False(t, account.UsesKIEJobsVideoAPI())
	require.False(t, account.UsesWokeyVideoMultipart())
}

func TestGrokVideoTransportRejectsLookalikeKIEHost(t *testing.T) {
	account := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://api.kie.ai.attacker.example/v1",
		},
	}
	require.Equal(t, GrokVideoTransportOpenAICompat, account.GrokVideoTransport())
}
