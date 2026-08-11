package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// TestOpenAIGatewayService_SelectAccountWithScheduler_CompactPreservesManualPriority
// 验证 compact 调度不会让能力探测结果覆盖人工优先级。
func TestOpenAIGatewayService_SelectAccountWithScheduler_CompactPreservesManualPriority(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()

	ctx := context.Background()
	groupID := int64(91001)
	accounts := []Account{
		{
			ID:          71001,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra:       map[string]any{}, // unknown
		},
		{
			ID:          71002,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    9,
			Extra:       map[string]any{"openai_compact_supported": true}, // probe telemetry only
		},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              &schedulerTestGatewayCache{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}

	selection, _, err := svc.SelectAccountWithScheduler(
		ctx,
		&groupID,
		"",
		"",
		"gpt-5.4",
		nil,
		OpenAIUpstreamTransportAny,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(71001), selection.Account.ID, "compact metadata must not override manual priority")
}

// TestOpenAIGatewayService_SelectAccountWithScheduler_CompactRejectsExplicitlyUnsupported
// 验证 force_off 账号不会被 compact 请求选中。
// Explicit hard opt-outs remain excluded from compact requests.
func TestOpenAIGatewayService_SelectAccountWithScheduler_CompactRejectsExplicitlyUnsupported(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()

	ctx := context.Background()
	groupID := int64(91002)
	accounts := []Account{
		{
			ID:          71010,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra:       map[string]any{"openai_compact_mode": OpenAICompactModeForceOff},
		},
		{
			ID:          71011,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra:       map[string]any{"openai_compact_mode": OpenAICompactModeForceOff, "openai_compact_supported": false},
		},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              &schedulerTestGatewayCache{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}

	selection, _, err := svc.SelectAccountWithScheduler(
		ctx,
		&groupID,
		"",
		"",
		"gpt-5.4",
		nil,
		OpenAIUpstreamTransportAny,
		true,
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNoAvailableCompactAccounts), "compact-only accounts should rejected explicitly unsupported and return compact error")
	require.Nil(t, selection)
}

// TestOpenAIGatewayService_SelectAccountWithScheduler_CompactAllowsFailedProbeAsSoftEvidence
// 验证历史探测失败不会成为永久硬门槛，线路恢复后仍可回到候选池。
func TestOpenAIGatewayService_SelectAccountWithScheduler_CompactAllowsFailedProbeAsSoftEvidence(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()

	ctx := context.Background()
	groupID := int64(91003)
	accounts := []Account{
		{
			ID:          71020,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Credentials: map[string]any{"base_url": "https://third-party.example/v1"},
			Extra:       map[string]any{"openai_compact_supported": false},
		},
		{
			ID:          71021,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    9,
			Extra:       map[string]any{"openai_compact_supported": true},
		},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              &schedulerTestGatewayCache{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}

	selection, _, err := svc.SelectAccountWithScheduler(
		ctx,
		&groupID,
		"",
		"",
		"gpt-5.4",
		nil,
		OpenAIUpstreamTransportAny,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(71020), selection.Account.ID, "historical compact false must not permanently override manual priority")
}

// TestOpenAIGatewayService_SelectAccountWithScheduler_CompactAllowsGrok verifies
// that OpenAI-compatible compact routing does not reject Grok accounts.
func TestOpenAIGatewayService_SelectAccountWithScheduler_CompactAllowsGrok(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()

	ctx := context.Background()
	groupID := int64(91004)
	accounts := []Account{
		{
			ID:          71030,
			Platform:    PlatformGrok,
			Type:        AccountTypeOAuth,
			Status:      StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Credentials: map[string]any{
				"model_mapping": map[string]any{"grok-4.5": "grok-4.5"},
			},
		},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              &schedulerTestGatewayCache{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}

	selection, _, err := svc.SelectAccountWithSchedulerForCapability(
		ctx,
		&groupID,
		"",
		"",
		"grok-4.5",
		nil,
		OpenAIUpstreamTransportAny,
		OpenAIEndpointCapabilityChatCompletions,
		true,
		false,
		true,
		PlatformGrok,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(71030), selection.Account.ID)
}

// TestOpenAICompactSupportTier 验证 tier 分类逻辑。
func TestOpenAICompactSupportTier(t *testing.T) {
	tests := []struct {
		name    string
		account *Account
		want    int
	}{
		{name: "nil", account: nil, want: 0},
		{name: "non openai", account: &Account{Platform: PlatformAnthropic}, want: 0},
		{name: "grok", account: &Account{Platform: PlatformGrok}, want: 2},
		{name: "openai unknown", account: &Account{Platform: PlatformOpenAI, Extra: map[string]any{}}, want: 1},
		{name: "openai supported", account: &Account{Platform: PlatformOpenAI, Extra: map[string]any{"openai_compact_supported": true}}, want: 2},
		{name: "failed probe remains soft evidence", account: &Account{Platform: PlatformOpenAI, Extra: map[string]any{"openai_compact_supported": false}}, want: 1},
		{name: "force on", account: &Account{Platform: PlatformOpenAI, Extra: map[string]any{"openai_compact_mode": OpenAICompactModeForceOn}}, want: 2},
		{name: "force off overrides probe true", account: &Account{Platform: PlatformOpenAI, Extra: map[string]any{"openai_compact_mode": OpenAICompactModeForceOff, "openai_compact_supported": true}}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := openAICompactSupportTier(tt.account); got != tt.want {
				t.Fatalf("openAICompactSupportTier(...) = %d, want %d", got, tt.want)
			}
		})
	}
}
