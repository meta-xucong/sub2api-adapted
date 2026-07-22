package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultModelsIncludeBareGPT56Alias(t *testing.T) {
	require.Contains(t, DefaultModelIDs(), "gpt-5.6")
}

func TestDefaultModelsPreferCurrentOpenAICatalog(t *testing.T) {
	ids := DefaultModelIDs()

	require.Contains(t, ids, "gpt-5.6-sol")
	require.Contains(t, ids, "gpt-5.6-terra")
	require.Contains(t, ids, "gpt-5.6-luna")
	require.Contains(t, ids, "gpt-5.5")
	require.Contains(t, ids, "gpt-5.4")
	require.Contains(t, ids, "gpt-5.4-mini")
	require.Contains(t, ids, "gpt-image-2")

	require.NotContains(t, ids, "gpt-5.2")
	require.NotContains(t, ids, "gpt-5.4-2026-03-05")
	require.NotContains(t, ids, "gpt-image-1")
	require.NotContains(t, ids, "gpt-image-1.5")
}

func TestFilterAutoDiscoveredModelIDsRemovesOpenAISnapshots(t *testing.T) {
	got := FilterAutoDiscoveredModelIDs([]string{
		"gpt-5.6-sol",
		"gpt-5.6-sol-2026-07-09",
		"gpt-5.5-pro",
		"gpt-5.4-2026-03-05",
		"gpt-5.4-nano",
		"gpt-5.2",
		"gpt-image-1.5",
		"gpt-image-2",
		"chatgpt-image-latest",
		"custom-provider-model",
	})

	require.Equal(t, []string{
		"custom-provider-model",
		"gpt-5.4-nano",
		"gpt-5.5-pro",
		"gpt-5.6-sol",
		"gpt-image-2",
	}, got)
}
