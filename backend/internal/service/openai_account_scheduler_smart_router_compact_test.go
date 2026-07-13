package service

import (
	"math"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/stretchr/testify/require"
)

func TestSmartRouterRouteRequest_CompactUsesDedicatedCapability(t *testing.T) {
	scheduler := &defaultOpenAIAccountScheduler{}
	request := scheduler.smartRouterRouteRequest(OpenAIAccountScheduleRequest{
		RequireCompact:        true,
		RequestedModel:        "gpt-5.5",
		SmartRouterCapability: smartrouter.CapabilityImageGeneration,
	})

	require.Equal(t, smartrouter.CapabilityResponsesCompact, request.Capability)
}

func TestBuildOpenAISelectionOrder_CompactUsesSmartRouterInsideSupportedTier(t *testing.T) {
	service := &OpenAIGatewayService{cfg: &config.Config{}}
	service.cfg.Gateway.SmartRouter.Enabled = true
	scheduler := &defaultOpenAIAccountScheduler{service: service, stats: newOpenAIAccountRuntimeStats()}

	bad := &Account{ID: 81001, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Priority: 0, Extra: map[string]any{"openai_compact_supported": true}}
	good := &Account{ID: 81002, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Priority: 10, Extra: map[string]any{"openai_compact_supported": true}}
	badStat := scheduler.stats.loadOrCreate(bad.ID)
	badStat.errorRateEWMABits.Store(math.Float64bits(0.8))

	plan := openAIAccountLoadPlan{
		allCandidates: []openAIAccountCandidateScore{
			{account: bad, loadInfo: &AccountLoadInfo{AccountID: bad.ID}},
			{account: good, loadInfo: &AccountLoadInfo{AccountID: good.ID}},
		},
		candidates: []openAIAccountCandidateScore{
			{account: bad, loadInfo: &AccountLoadInfo{AccountID: bad.ID}, errorRate: 0.8},
			{account: good, loadInfo: &AccountLoadInfo{AccountID: good.ID}, errorRate: 0},
		},
		topK: 2,
	}
	order := scheduler.buildOpenAISelectionOrder(OpenAIAccountScheduleRequest{
		RequireCompact:        true,
		RequestedModel:        "gpt-5.5",
		SmartRouterCapability: smartrouter.CapabilityResponsesCompact,
	}, plan)

	require.Len(t, order, 1)
	require.Equal(t, good.ID, order[0].account.ID, "flapping compact lane should yield inside the supported tier")
}
