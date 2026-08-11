package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminSelectableModelIDsIncludeCurrentLunaAndExcludeInternalAliases(t *testing.T) {
	ids := AdminSelectableModelIDs()

	require.Equal(t, []string{
		"gpt-5.4-mini",
		"gpt-5.4",
		"gpt-5.5",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
		"gpt-image-2",
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
	require.True(t, IsAutoDiscoveredModelID("codex-auto-review"))
	require.False(t, IsAdminSelectableModelID("codex-auto-review"))
	require.False(t, IsAdminSelectableModelID("gpt-5.6"))
}
