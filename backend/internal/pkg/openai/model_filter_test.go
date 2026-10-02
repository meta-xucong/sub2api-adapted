package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminSelectableModelIDsIncludeCurrentLunaAndExcludeInternalAliases(t *testing.T) {
	ids := AdminSelectableModelIDs()

	require.Equal(t, []string{
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
		"gpt-6-astra",
		"gpt-6-sol",
		"gpt-6-luna",
		"gpt-5.5",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-image-2",
		"gpt-image-2.5-flare",
		"gpt-image-2.5-sunburst",
	}, ids)
	require.NotContains(t, ids, "gpt-5.6")
	require.NotContains(t, ids, "codex-auto-review")
	require.NotContains(t, ids, "gpt-5.3-codex-spark")
}

func TestAdminSelectableModelsMatchCuratedIDs(t *testing.T) {
	models := AdminSelectableModels()
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}

	require.Equal(t, AdminSelectableModelIDs(), ids)
}

func TestFilterAdminSelectableModelIDsPreservesCustomAliases(t *testing.T) {
	got := FilterAdminSelectableModelIDs([]string{
		"gpt-5.6-luna",
		"gpt-5.6",
		"gpt-5.5-pro",
		"gpt-5.4-2026-03-05",
		"codex-auto-review",
		"codex-auto-calibration",
		"aiai-gpt-image-2",
		"models/gpt-5.6-luna",
		"aiai-gpt-image-2",
	})

	require.Equal(t, []string{
		"gpt-5.6-luna",
		"aiai-gpt-image-2",
	}, got)
}

func TestAdminSelectableModelIDDoesNotChangeRoutingCompatibility(t *testing.T) {
	require.Contains(t, DefaultModelIDs(), "gpt-5.6")
	require.False(t, IsAutoDiscoveredModelID("codex-auto-review"))
	require.False(t, IsAdminSelectableModelID("codex-auto-review"))
	require.False(t, IsAdminSelectableModelID("codex-auto-calibration"))
	require.False(t, IsAdminSelectableModelID("gpt-5.6"))
}

func TestFilterAutoDiscoveredModelIDsKeepsFormalModelsAndCustomIDs(t *testing.T) {
	got := FilterAutoDiscoveredModelIDs([]string{
		"gpt-5.6-sol-2026-07-09",
		"gpt-5.6-sol",
		"gpt-5.5-codex",
		"gpt-6-astra",
		"gpt-image-2.5-flare",
		"gpt-image-1.5",
		"custom-provider-model",
	})

	require.Equal(t, []string{
		"custom-provider-model",
		"gpt-5.5-codex",
		"gpt-5.6-sol",
		"gpt-6-astra",
		"gpt-image-2.5-flare",
	}, got)
}

func TestFilterAutoDiscoveredModelIDsOnlyAppliesDateRulesToOpenAIIDs(t *testing.T) {
	require.False(t, IsAutoDiscoveredModelID("gpt-5.6-sol-2026-07-09"))
	require.True(t, IsAutoDiscoveredModelID("custom-provider-2026-07-09"))
	require.False(t, IsAutoDiscoveredModelID("gpt-5.6"))
	require.False(t, IsAutoDiscoveredModelID("gpt-6"))
}
