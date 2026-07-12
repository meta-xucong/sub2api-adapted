package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOpenAIGatewayService_SmartRouterDemotesImageGenerationCapabilityAfterDeterministicFailure(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	ctx := context.Background()
	groupID := int64(7301)
	accounts := []Account{
		{
			ID:          73001,
			Name:        "flowyun-image",
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    1,
			GroupIDs:    []int64{groupID},
			Extra: map[string]any{"smart_router": map[string]any{
				"source_group": "flowyun",
			}},
		},
		{
			ID:          73002,
			Name:        "stable-fallback",
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    2,
			GroupIDs:    []int64{groupID},
			Extra: map[string]any{"smart_router": map[string]any{
				"source_group": "stable",
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

	selection, _, err := svc.SelectAccountWithSchedulerForImageOperation(ctx, &groupID, "", "gpt-image-2", nil, OpenAIImagesCapabilityBasic, false)
	require.NoError(t, err)
	require.Equal(t, int64(73001), selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}

	svc.ReportSmartRouterImageResult(
		&accounts[0],
		&OpenAIImagesRequest{Endpoint: openAIImagesGenerationsEndpoint, Model: "gpt-image-2"},
		nil,
		&OpenAIImagesUpstreamError{StatusCode: http.StatusBadRequest, Code: "upstream_text_reply", Message: "requires a usable image target"},
		140000,
	)

	selection, _, err = svc.SelectAccountWithSchedulerForImageOperation(ctx, &groupID, "", "gpt-image-2", nil, OpenAIImagesCapabilityBasic, false)
	require.NoError(t, err)
	require.Equal(t, int64(73002), selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}

	// The same lane remains eligible for edits because health is capability-scoped.
	selection, _, err = svc.SelectAccountWithSchedulerForImageOperation(ctx, &groupID, "", "gpt-image-2", nil, OpenAIImagesCapabilityBasic, true)
	require.NoError(t, err)
	require.Equal(t, int64(73001), selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

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

func TestOpenAIGatewayService_SmartRouterImageBudgetDefaultsAndOverrides(t *testing.T) {
	cfg := newSmartRouterSchedulerTestConfig()
	svc := &OpenAIGatewayService{cfg: cfg}

	budget := svc.OpenAIImageSmartRouterBudget()
	require.Equal(t, 600.0, budget.TotalSeconds)
	require.Equal(t, 180.0, budget.MinimumAttemptSeconds)
	require.Equal(t, 15.0, budget.FinalizationReserveSeconds)

	cfg.Gateway.SmartRouter.ImageTotalBudgetSeconds = 420
	cfg.Gateway.SmartRouter.ImageAttemptSeconds = 120
	cfg.Gateway.SmartRouter.ImageReserveSeconds = 10
	budget = svc.OpenAIImageSmartRouterBudget()
	require.Equal(t, 420.0, budget.TotalSeconds)
	require.Equal(t, 120.0, budget.MinimumAttemptSeconds)
	require.Equal(t, 10.0, budget.FinalizationReserveSeconds)
}

func TestOpenAIGatewayService_SmartRouterPrefersExplicitImageSizeSpecialist(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(7401)
	accounts := []Account{
		{
			ID: 74001, Name: "generic-image", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, GroupIDs: []int64{groupID},
			Extra: map[string]any{"smart_router": map[string]any{
				"capabilities": []any{"image_generation"},
			}},
		},
		{
			ID: 74002, Name: "two-k-specialist", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 9, GroupIDs: []int64{groupID},
			Extra: map[string]any{"smart_router": map[string]any{
				"capabilities": []any{"image_generation"}, "image_size_tiers": []any{"2K", "4K"},
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

	selection, _, err := svc.SelectAccountWithSchedulerForImageOperation(
		WithOpenAIImageSmartRouterSizeTier(context.Background(), "2k"),
		&groupID, "", "gpt-image-2", nil, OpenAIImagesCapabilityBasic, false,
	)
	require.NoError(t, err)
	require.Equal(t, int64(74002), selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}

	selection, _, err = svc.SelectAccountWithSchedulerForImageOperation(
		context.Background(), &groupID, "", "gpt-image-2", nil, OpenAIImagesCapabilityBasic, false,
	)
	require.NoError(t, err)
	require.Equal(t, int64(74001), selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
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

func TestOpenAIGatewayService_ImageRetryUsesFreshDBWhenSnapshotMissesFallback(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	ctx := context.Background()
	groupID := int64(7102)
	snapshotAccounts := []*Account{
		{
			ID:          71021,
			Name:        "k12-a",
			Platform:    PlatformOpenAI,
			Type:        AccountTypeOAuth,
			Status:      StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    1,
			GroupIDs:    []int64{groupID},
			Credentials: map[string]any{"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2"}},
		},
		{
			ID:          71022,
			Name:        "k12-b",
			Platform:    PlatformOpenAI,
			Type:        AccountTypeOAuth,
			Status:      StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    1,
			GroupIDs:    []int64{groupID},
			Credentials: map[string]any{"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2"}},
		},
	}
	fallback := Account{
		ID:          71023,
		Name:        "404token-image-fallback",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    2,
		GroupIDs:    []int64{groupID},
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2"}},
		Extra:       map[string]any{"smart_router": map[string]any{"capabilities": []any{"image_generation", "image_edit"}}},
	}
	accounts := []Account{*snapshotAccounts[0], *snapshotAccounts[1], fallback}
	repo := schedulerTestOpenAIAccountRepo{accounts: accounts}
	svc := &OpenAIGatewayService{
		accountRepo:        repo,
		schedulerSnapshot:  NewSchedulerSnapshotService(&openAISnapshotCacheStub{snapshotAccounts: snapshotAccounts}, nil, repo, nil, nil),
		cache:              &schedulerTestGatewayCache{},
		cfg:                &config.Config{},
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
	failed := map[int64]struct{}{71021: {}, 71022: {}}

	selection, decision, err := svc.SelectAccountWithSchedulerForImageOperation(
		ctx,
		&groupID,
		"",
		"gpt-image-2",
		failed,
		OpenAIImagesCapabilityNative,
		true,
	)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(71023), selection.Account.ID)
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

func TestOpenAIGatewayService_SmartRouterAutoProtectsUnconfiguredBusyLane(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	ctx := context.Background()
	groupID := int64(7251)
	cheapRate := 0.2
	fallbackRate := 1.0
	accounts := []Account{
		{
			ID:             72501,
			Name:           "chatgpt-7646881-plus",
			Platform:       PlatformOpenAI,
			Type:           AccountTypeAPIKey,
			Status:         StatusActive,
			Schedulable:    true,
			Concurrency:    10,
			Priority:       1,
			RateMultiplier: &cheapRate,
		},
		{
			ID:             72502,
			Name:           "stable-new-provider",
			Platform:       PlatformOpenAI,
			Type:           AccountTypeAPIKey,
			Status:         StatusActive,
			Schedulable:    true,
			Concurrency:    10,
			Priority:       50,
			RateMultiplier: &fallbackRate,
		},
	}
	concurrencyCache := schedulerTestConcurrencyCache{
		loadMap: map[int64]*AccountLoadInfo{
			72501: {AccountID: 72501, CurrentConcurrency: 1, WaitingCount: 1, LoadRate: 95},
			72502: {AccountID: 72502, CurrentConcurrency: 0, LoadRate: 10},
		},
		acquireResults: map[int64]bool{72502: true},
	}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              &schedulerTestGatewayCache{},
		cfg:                newSmartRouterSchedulerTestConfig(),
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		concurrencyService: NewConcurrencyService(concurrencyCache),
		openaiAccountStats: newOpenAIAccountRuntimeStats(),
	}
	for i := 0; i < 5; i++ {
		svc.openaiAccountStats.report(72501, false, nil)
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
	require.Equal(t, int64(72502), selection.Account.ID)
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
			"image_size_tiers":             []any{"2K", "4k", "invalid"},
		}},
	}

	lane, ok := smartRouterLaneSnapshot(account, &AccountLoadInfo{
		CurrentConcurrency: 1,
		WaitingCount:       0,
		LoadRate:           30,
	}, 0.10, 1200, true)

	require.True(t, ok)
	require.Equal(t, "configured-lane", lane.LaneID)
	require.Equal(t, "configured-source", lane.SourceGroup)
	require.Equal(t, 1.5, lane.BaseWeight)
	require.Equal(t, 0.4, lane.CostMultiplier)
	require.Equal(t, 2, lane.MaxConcurrency)
	require.Equal(t, 3, lane.SourceGroupMaxConcurrency)
	require.True(t, lane.Capabilities["image_generation"])
	require.True(t, lane.Capabilities["image_edit"])
	require.Equal(t, []string{"2K", "4K"}, lane.ImageSizeTiers)
	require.Equal(t, 1, lane.CurrentConcurrency)
	require.Equal(t, 0, lane.CurrentWaiting)
	require.Equal(t, 30, lane.LoadRate)
	require.Equal(t, 0.10, lane.ErrorRateEWMA)
	require.Equal(t, 1200.0, lane.LatencyEWMAms)
}

func TestSmartRouterLaneSnapshotAutoInfersSourceGroupAndConcurrency(t *testing.T) {
	account := &Account{
		ID:          73002,
		Name:        "404token chatgpt-7646881 plus",
		Concurrency: 10,
		Credentials: map[string]any{
			"base_url": "https://example.invalid/api/v1",
		},
	}

	lane, ok := smartRouterLaneSnapshot(account, &AccountLoadInfo{
		CurrentConcurrency: 1,
		WaitingCount:       1,
		LoadRate:           95,
	}, 0.70, 0, false)

	require.True(t, ok)
	require.Equal(t, "name-key:7646881", lane.SourceGroup)
	require.Equal(t, 1, lane.MaxConcurrency)
	require.Equal(t, 1, lane.SourceGroupMaxConcurrency)
}

func TestSmartRouterLaneSnapshotAutoInfersHostWhenNoNameKey(t *testing.T) {
	account := &Account{
		ID:          73003,
		Name:        "new-provider-plus",
		Concurrency: 4,
		Credentials: map[string]any{
			"base_url": "https://www.vendor.example:8443/openai",
		},
	}

	lane, ok := smartRouterLaneSnapshot(account, nil, 0, 0, false)

	require.True(t, ok)
	require.Equal(t, "host:vendor.example", lane.SourceGroup)
	require.Equal(t, 4, lane.MaxConcurrency)
	require.Equal(t, 0, lane.SourceGroupMaxConcurrency)
}
