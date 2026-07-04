package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func newSmartRouterSchedulerTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Gateway.SmartRouter.Enabled = true
	cfg.Gateway.SmartRouter.TopK = 5
	cfg.Gateway.SmartRouter.MaxAttemptsImage = 2
	cfg.Gateway.SmartRouter.MaxAttemptsChat = 3
	cfg.Gateway.SmartRouter.MaxAttemptsDefault = 3
	cfg.Gateway.SmartRouter.SameSourceGroupAttempts = 1
	cfg.Gateway.SmartRouter.CostBiasMax = 3
	cfg.Gateway.SmartRouter.Scoring.Priority = 0.8
	cfg.Gateway.SmartRouter.Scoring.Cost = 1
	cfg.Gateway.SmartRouter.Scoring.Health = 1.2
	cfg.Gateway.SmartRouter.Scoring.Load = 1
	cfg.Gateway.SmartRouter.Scoring.Queue = 0.6
	cfg.Gateway.SmartRouter.Scoring.Latency = 0.4
	cfg.Gateway.SmartRouter.Scoring.Recovery = 0.8
	return cfg
}

func TestOpenAIGatewayService_SmartRouterSkipsFailedSourceGroupForImageRetry(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	ctx := context.Background()
	groupID := int64(7101)
	accounts := []Account{
		{
			ID:          71001,
			Name:        "same-primary",
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    1,
			Extra: map[string]any{"smart_router": map[string]any{
				"source_group": "same-upstream",
			}},
		},
		{
			ID:          71002,
			Name:        "same-secondary",
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    1,
			Extra: map[string]any{"smart_router": map[string]any{
				"source_group": "same-upstream",
			}},
		},
		{
			ID:          71003,
			Name:        "other-fallback",
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    50,
			Extra: map[string]any{"smart_router": map[string]any{
				"source_group": "other-upstream",
			}},
		},
	}

	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              &schedulerTestGatewayCache{},
		cfg:                newSmartRouterSchedulerTestConfig(),
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
	failed := map[int64]struct{}{71001: {}}

	selection, decision, err := svc.SelectAccountWithSchedulerForImageOperation(
		ctx,
		&groupID,
		"",
		"gpt-image-2",
		failed,
		OpenAIImagesCapabilityBasic,
		true,
	)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(71003), selection.Account.ID)
	require.Equal(t, openAIAccountScheduleLayerLoadBalance, decision.Layer)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAIGatewayService_SmartRouterHonorsExtraMaxConcurrency(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	ctx := context.Background()
	groupID := int64(7201)
	cheapRate := 0.2
	expensiveRate := 1.0
	accounts := []Account{
		{
			ID:             72001,
			Name:           "cheap-busy",
			Platform:       PlatformOpenAI,
			Type:           AccountTypeAPIKey,
			Status:         StatusActive,
			Schedulable:    true,
			Concurrency:    10,
			Priority:       1,
			RateMultiplier: &cheapRate,
			Extra: map[string]any{"smart_router": map[string]any{
				"source_group":    "cheap",
				"max_concurrency": 1,
			}},
		},
		{
			ID:             72002,
			Name:           "stable-fallback",
			Platform:       PlatformOpenAI,
			Type:           AccountTypeAPIKey,
			Status:         StatusActive,
			Schedulable:    true,
			Concurrency:    10,
			Priority:       20,
			RateMultiplier: &expensiveRate,
			Extra: map[string]any{"smart_router": map[string]any{
				"source_group": "fallback",
			}},
		},
	}
	concurrencyCache := schedulerTestConcurrencyCache{
		loadMap: map[int64]*AccountLoadInfo{
			72001: {AccountID: 72001, CurrentConcurrency: 1, LoadRate: 10},
			72002: {AccountID: 72002, CurrentConcurrency: 0, LoadRate: 20},
		},
	}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              &schedulerTestGatewayCache{},
		cfg:                newSmartRouterSchedulerTestConfig(),
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		concurrencyService: NewConcurrencyService(concurrencyCache),
	}

	selection, decision, err := svc.SelectAccountWithScheduler(
		ctx,
		&groupID,
		"",
		"",
		"gpt-5.5",
		nil,
		OpenAIUpstreamTransportAny,
		false,
	)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(72002), selection.Account.ID)
	require.Equal(t, openAIAccountScheduleLayerLoadBalance, decision.Layer)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestSmartRouterLaneSnapshotParsesAccountExtra(t *testing.T) {
	account := &Account{
		ID:          73001,
		Name:        "configured",
		Concurrency: 5,
		Extra: map[string]any{"smart_router": map[string]any{
			"lane_id":                      "configured-lane",
			"source_group":                 "configured-source",
			"base_weight":                  1.5,
			"cost_multiplier":              0.4,
			"max_concurrency":              2,
			"source_group_max_concurrency": 3,
			"capabilities":                 []any{"image_generation", "image_edit"},
		}},
	}

	lane, ok := smartRouterLaneSnapshot(account, &AccountLoadInfo{
		CurrentConcurrency: 1,
		WaitingCount:       2,
		LoadRate:           30,
	}, 0.25, 1200, true)

	require.True(t, ok)
	require.Equal(t, "configured-lane", lane.LaneID)
	require.Equal(t, "configured-source", lane.SourceGroup)
	require.Equal(t, 1.5, lane.BaseWeight)
	require.Equal(t, 0.4, lane.CostMultiplier)
	require.Equal(t, 2, lane.MaxConcurrency)
	require.Equal(t, 3, lane.SourceGroupMaxConcurrency)
	require.True(t, lane.Capabilities["image_generation"])
	require.True(t, lane.Capabilities["image_edit"])
	require.Equal(t, 1, lane.CurrentConcurrency)
	require.Equal(t, 2, lane.CurrentWaiting)
	require.Equal(t, 30, lane.LoadRate)
	require.Equal(t, 0.25, lane.ErrorRateEWMA)
	require.Equal(t, 1200.0, lane.LatencyEWMAms)
}
