//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func TestIsResponsesCompactionSchedulingRequest(t *testing.T) {
	state := apicompat.EncodeAnthropicCompactionEnvelope([]byte(`{"type":"compaction"}`))
	tests := []struct {
		name   string
		body   string
		stream bool
		want   bool
	}{
		{name: "native streaming trigger", body: `{"stream":true,"input":[{"type":"compaction_trigger"}]}`, stream: true, want: true},
		{name: "trigger on non-streaming request", body: `{"input":[{"type":"compaction_trigger"}]}`, want: false},
		{name: "continued compaction state", body: `{"input":[{"type":"compaction","encrypted_content":"` + state + `"}]}`, want: true},
		{name: "ordinary response request", body: `{"input":[{"type":"message","content":"hello"}]}`, stream: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsResponsesCompactionSchedulingRequest([]byte(tt.body), tt.stream))
		})
	}
}

func TestIsAnthropicCompactionSchedulingRequest(t *testing.T) {
	require.True(t, IsAnthropicCompactionSchedulingRequest([]byte(`{"model":"claude-sonnet-5-5","stream":true,"compaction":{"type":"summarize"},"messages":[]}`)))
	require.False(t, IsAnthropicCompactionSchedulingRequest([]byte(`{"model":"claude-sonnet-5-5","stream":true,"messages":[]}`)))
	require.False(t, IsAnthropicCompactionSchedulingRequest([]byte(`{"compaction":{"type":"other"}}`)))
}

func TestCompactionSchedulingAccountGateOnlyAffectsCompactionRequests(t *testing.T) {
	svc := &GatewayService{}
	marked := &Account{Platform: PlatformAnthropic, Extra: map[string]any{ResponsesCompactionEnabledExtraKey: true}}
	unmarked := &Account{Platform: PlatformAnthropic}
	compactCtx := WithResponsesCompactionScheduling(context.Background())
	ordinaryCtx := context.Background()

	require.True(t, svc.isModelSupportedByAccountWithContext(compactCtx, marked, "claude-sonnet-5-5"))
	require.False(t, svc.isModelSupportedByAccountWithContext(compactCtx, unmarked, "claude-sonnet-5-5"))
	require.True(t, svc.isModelSupportedByAccountWithContext(ordinaryCtx, marked, "claude-sonnet-5-5"))
	require.True(t, svc.isModelSupportedByAccountWithContext(ordinaryCtx, unmarked, "claude-sonnet-5-5"))
}

func TestOpenAICompatibleCompactionSchedulingGateOnlyAffectsCompactionRequests(t *testing.T) {
	marked := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{ResponsesCompactionEnabledExtraKey: true}}
	unmarked := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	compactCtx := WithResponsesCompactionScheduling(context.Background())
	ordinaryCtx := context.Background()

	require.Equal(t, "responses_compaction_pool", openAICompatibleAccountEligibilityFailureReasonBeforeProfit(compactCtx, unmarked, PlatformOpenAI, "claude-sonnet-5-5", false, OpenAIEndpointCapabilityResponses))
	require.NotEqual(t, "responses_compaction_pool", openAICompatibleAccountEligibilityFailureReasonBeforeProfit(compactCtx, marked, PlatformOpenAI, "claude-sonnet-5-5", false, OpenAIEndpointCapabilityResponses))
	require.NotEqual(t, "responses_compaction_pool", openAICompatibleAccountEligibilityFailureReasonBeforeProfit(ordinaryCtx, unmarked, PlatformOpenAI, "claude-sonnet-5-5", false, OpenAIEndpointCapabilityResponses))
}
