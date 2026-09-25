package service

import (
	"context"
	"testing"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"
)

func TestNormalizePublicModelIDsCollapsesOnlyWhenBaseExists(t *testing.T) {
	got := NormalizePublicModelIDs(PlatformDeepseek, []string{
		"claude-sonnet-4-20250514",
		"claude-sonnet-4",
		"deepseek-v4-flash-0731",
	})

	require.Equal(t, []string{
		"claude-sonnet-4",
		"deepseek-v4-flash",
	}, got)
}

func TestNormalizePublicModelIDsHidesConflictingDateOnlyAliases(t *testing.T) {
	got := NormalizePublicModelIDs(PlatformDeepseek, []string{
		"deepseek-v4-flash-0731",
		"deepseek-v4-flash-260425",
		"deepseek-v4-pro",
	})

	require.Equal(t, []string{"deepseek-v4-pro"}, got)
}

func TestNormalizePublicModelIDsHidesAmbiguousParent(t *testing.T) {
	got := NormalizePublicModelIDs(PlatformComposite, []string{
		"gpt-5.6",
		"gpt-5.6-luna",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"claude-opus-4-6",
		"claude-opus-4-6-20250929",
	})

	require.Equal(t, []string{
		"claude-opus-4-6",
		"gpt-5.6-luna",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
	}, got)
}

func TestNormalizePublicModelIDsAppliesOpenAICuratedFilter(t *testing.T) {
	got := NormalizePublicModelIDs(PlatformOpenAI, []string{
		"gpt-5.6",
		"gpt-5.6-sol",
		"gpt-5.5",
		"custom-coding-model",
		"foo-*",
	})

	require.Equal(t, []string{
		"custom-coding-model",
		"gpt-5.5",
		"gpt-5.6-sol",
	}, got)
}

func TestNormalizePublicModelIDsCanonicalizesDateOnlyOfficialID(t *testing.T) {
	got := NormalizePublicModelIDs(PlatformAnthropic, []string{
		"claude-fable-5",
		"claude-sonnet-4-6-20250929",
	})

	require.Equal(t, []string{
		"claude-fable-5",
		"claude-sonnet-4-6",
	}, got)
}

func TestAccountModelMappingResolvesCanonicalDateAlias(t *testing.T) {
	account := Account{
		Platform: PlatformDeepseek,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"deepseek-v4-flash-0731": "deepseek-v4-flash-0731",
			},
		},
	}

	mapped, matched := account.ResolveMappedModel("deepseek-v4-flash")
	require.True(t, matched)
	require.Equal(t, "deepseek-v4-flash-0731", mapped)
	require.True(t, account.IsModelSupported("deepseek-v4-flash"))
}

func TestAccountModelMappingRejectsConflictingDateAliases(t *testing.T) {
	account := Account{
		Platform: PlatformDeepseek,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"deepseek-v4-flash-0731":   "deepseek-v4-flash-0731",
				"deepseek-v4-flash-260425": "deepseek-v4-flash-260425",
			},
		},
	}

	_, matched := account.ResolveMappedModel("deepseek-v4-flash")
	require.False(t, matched)
	require.False(t, account.IsModelSupported("deepseek-v4-flash"))
}

func TestGetAvailableModelsUsesCanonicalIDsAtServiceBoundary(t *testing.T) {
	groupID := int64(7001)
	repo := &modelsListAccountRepoStub{byGroup: map[int64][]Account{
		groupID: {
			{
				Platform: PlatformDeepseek,
				Credentials: map[string]any{"model_mapping": map[string]any{
					"deepseek-v4-flash-0731": "deepseek-v4-flash-0731",
				}},
			},
			{
				Platform: PlatformOpenAI,
				Credentials: map[string]any{"model_mapping": map[string]any{
					"gpt-5.6":       "gpt-5.6",
					"gpt-5.6-luna":  "gpt-5.6-luna",
					"gpt-5.6-sol":   "gpt-5.6-sol",
					"gpt-5.6-terra": "gpt-5.6-terra",
					"gpt-5.5":       "gpt-5.5",
				}},
			},
		},
	}}
	svc := &GatewayService{
		accountRepo:        repo,
		modelsListCache:    gocache.New(time.Minute, time.Minute),
		modelsListCacheTTL: time.Minute,
	}

	require.Equal(t, []string{"deepseek-v4-flash"}, svc.GetAvailableModels(context.Background(), &groupID, PlatformDeepseek))
	require.Equal(t, []string{"gpt-5.5", "gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra"}, svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI))
}

func TestCompositeOwnershipUsesCanonicalDateAlias(t *testing.T) {
	groupID := int64(7002)
	repo := &modelsListAccountRepoStub{byGroup: map[int64][]Account{
		groupID: {{
			Platform: PlatformOpenAI,
			Credentials: map[string]any{"model_mapping": map[string]any{
				"deepseek-v4-flash-0731": "deepseek-v4-flash-0731",
			}},
		}},
	}}
	svc := &GatewayService{
		accountRepo:        repo,
		modelsListCache:    gocache.New(time.Minute, time.Minute),
		modelsListCacheTTL: time.Minute,
	}

	ownership, err := svc.resolveCompositeModelOwnership(context.Background(), groupID, "deepseek-v4-flash")
	require.NoError(t, err)
	require.Equal(t, CompositeModelOwnership{TargetPlatform: PlatformOpenAI, Matched: true}, ownership)
}

func TestCompositeOwnershipUsesPriorityWhenCanonicalModelHasMultiplePlatforms(t *testing.T) {
	groupID := int64(7003)
	repo := &modelsListAccountRepoStub{byGroup: map[int64][]Account{
		groupID: {
			{
				ID:       1,
				Platform: PlatformOpenAI,
				Priority: 10,
				Credentials: map[string]any{"model_mapping": map[string]any{
					"deepseek-v4-flash-0731": "deepseek-v4-flash-0731",
				}},
			},
			{
				ID:       2,
				Platform: PlatformAnthropic,
				Priority: 80,
				Credentials: map[string]any{"model_mapping": map[string]any{
					"deepseek-v4-flash": "deepseek-v4-flash",
				}},
			},
		},
	}}
	svc := &GatewayService{accountRepo: repo}

	ownership, err := svc.resolveCompositeModelOwnership(context.Background(), groupID, "deepseek-v4-flash")
	require.NoError(t, err)
	require.Equal(t, CompositeModelOwnership{TargetPlatform: PlatformOpenAI, Matched: true}, ownership)
}

func TestFilterCodexModelIDsForGroupUsesCanonicalIDs(t *testing.T) {
	got := FilterCodexModelIDsForGroup([]string{
		"gpt-5.6",
		"gpt-5.6-luna",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"claude-sonnet-4-6-20250929",
	}, &Group{Platform: PlatformComposite})

	require.Equal(t, []string{
		"claude-sonnet-4-6",
		"gpt-5.6-luna",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
	}, got)
}
