package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminSelectableModelIDsAdaptFrozenFilterToCurrentOfficialCatalog(t *testing.T) {
	require.Equal(t, []string{
		"gpt-5.6-sol",
		"gpt-6",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
		"gpt-6.1-sol",
		"gpt-6-sol",
		"gpt-6-luna",
		"gpt-6-astra",
		"gpt-5.5",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-image-2",
		"gpt-image-2.5-flare",
		"gpt-image-2.5-sunburst",
	}, AdminSelectableModelIDs())
	require.Equal(t, AdminSelectableModelIDs(), modelIDs(AdminSelectableModels()))
}

func TestFilterAdminSelectableModelIDsPreservesCustomAliases(t *testing.T) {
	require.Equal(t, []string{
		"gpt-6-astra",
		"gpt-custom",
		"gpt-5.6-luna",
		"aiai-gpt-image-2",
		"deepseek-v4-pro",
	}, FilterAdminSelectableModelIDs([]string{
		"gpt-6-astra",
		"gpt-custom",
		"gpt-5.6-luna",
		"gpt-5.6",
		"gpt-5.5-pro",
		"gpt-5.4-2026-03-05",
		"codex-auto-review",
		"aiai-gpt-image-2",
		"models/gpt-5.6-luna",
		"aiai-gpt-image-2",
		"deepseek-v4-pro",
	}))
}

func TestFilterAdminSelectableModelsRetainsProviderMetadata(t *testing.T) {
	models := FilterAdminSelectableModels([]Model{
		{ID: "gpt-5.6", DisplayName: "internal alias"},
		{ID: "deepseek-v4-pro", DisplayName: "DeepSeek", OwnedBy: "custom"},
	})
	require.Len(t, models, 1)
	require.Equal(t, "deepseek-v4-pro", models[0].ID)
	require.Equal(t, "DeepSeek", models[0].DisplayName)
}

func TestAdminSelectorFilterDoesNotChangeRoutingCatalog(t *testing.T) {
	require.Contains(t, DefaultModelIDs(), "gpt-5.6")
	require.Contains(t, DefaultModelIDs(), "codex-auto-review")
	require.Contains(t, AdminSelectableModelIDs(), "gpt-6-astra")
	require.Contains(t, AdminSelectableModelIDs(), "gpt-image-2.5-flare")
	require.False(t, IsAdminSelectableModelID("gpt-5.6"))
	require.False(t, IsAdminSelectableModelID("codex-auto-review"))
}

func modelIDs(models []Model) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}
