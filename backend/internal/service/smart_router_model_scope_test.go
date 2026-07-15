package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSmartRouterAccountHasChatGPTModelUsesMappedUpstreamModel(t *testing.T) {
	chatGPT := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-5.5": "gpt-5.5"},
		},
	}
	kimi := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-5.5": "kimi-k2"},
		},
	}

	require.True(t, smartRouterAccountHasChatGPTModel(chatGPT))
	require.False(t, smartRouterAccountHasChatGPTModel(kimi))
}

func TestSmartRouterAccountHasChatGPTModelDefaultsOnlyNativeOAuth(t *testing.T) {
	oauth := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	unknownAPIKey := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://unknown-provider.example/v1",
		},
	}
	claude := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-5.5": "claude-sonnet-4-6"},
		},
	}

	require.True(t, smartRouterAccountHasChatGPTModel(oauth))
	require.False(t, smartRouterAccountHasChatGPTModel(unknownAPIKey))
	require.False(t, smartRouterAccountHasChatGPTModel(claude))
}

func TestIsChatGPTModelIdentifier(t *testing.T) {
	for _, model := range []string{"gpt-5.6", "gpt-image-2", "models/gpt-5.5-openai-compact", "codex-mini", "o3"} {
		require.True(t, isChatGPTModelIdentifier(model), model)
	}
	for _, model := range []string{"kimi-k2", "claude-sonnet-4-6", "gemini-3-pro", "doubao-seed-1-6"} {
		require.False(t, isChatGPTModelIdentifier(model), model)
	}
}
