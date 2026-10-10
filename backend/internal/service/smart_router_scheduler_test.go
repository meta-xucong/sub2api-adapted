package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/stretchr/testify/require"
)

func enableSmartRouterSchedulerTest(t *testing.T) {
	t.Helper()
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
		enabled:   true,
		expiresAt: time.Now().Add(time.Minute).UnixNano(),
	})
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
}

func smartRouterTestAccount(id int64, accountType string, credentials map[string]any, extra map[string]any) *Account {
	return &Account{
		ID:          id,
		Name:        "smart-router-test-account",
		Platform:    PlatformOpenAI,
		Type:        accountType,
		Credentials: credentials,
		Extra:       extra,
	}
}

func TestSmartRouterAccountAdmissionMatchesT0ModelScope(t *testing.T) {
	tests := []struct {
		name       string
		account    *Account
		capability core.Capability
		model      string
		required   OpenAIEndpointCapability
		want       bool
	}{
		{
			name:       "unmapped oauth",
			account:    smartRouterTestAccount(1, AccountTypeOAuth, nil, nil),
			capability: core.CapabilityResponses,
			model:      "gpt-5.6-sol",
			want:       true,
		},
		{
			name: "native api key",
			account: smartRouterTestAccount(2, AccountTypeAPIKey, map[string]any{
				"base_url": "https://api.openai.com/v1",
			}, nil),
			capability: core.CapabilityResponses,
			model:      "gpt-5.6-sol",
			want:       true,
		},
		{
			name: "unmapped third-party API key",
			account: smartRouterTestAccount(7, AccountTypeAPIKey, map[string]any{
				"base_url": "https://gateway.example/v1",
			}, nil),
			capability: core.CapabilityResponses,
			model:      "gpt-5.6-sol",
			want:       false,
		},
		{
			name: "gpt public alias mapped to non-gpt upstream",
			account: smartRouterTestAccount(3, AccountTypeAPIKey, map[string]any{
				"model_mapping": map[string]any{"gpt-5.5": "kimi-k2"},
			}, nil),
			capability: core.CapabilityResponses,
			model:      "gpt-5.5",
			want:       false,
		},
		{
			name: "compact mapping matches independently",
			account: smartRouterTestAccount(4, AccountTypeAPIKey, map[string]any{
				"compact_model_mapping": map[string]any{"gpt-5.5*": "gpt-5.5-openai-compact"},
			}, nil),
			capability: core.CapabilityResponsesCompact,
			model:      "gpt-5.5-sol",
			want:       true,
		},
		{
			name: "compact mapping mismatch",
			account: smartRouterTestAccount(5, AccountTypeAPIKey, map[string]any{
				"compact_model_mapping": map[string]any{"gpt-5.6*": "gpt-5.6-openai-compact"},
			}, nil),
			capability: core.CapabilityResponsesCompact,
			model:      "gpt-5.5-sol",
			want:       false,
		},
		{
			name: "embedding-only mapping",
			account: smartRouterTestAccount(6, AccountTypeAPIKey, map[string]any{
				"model_mapping": map[string]any{"text-embedding-3-large": "text-embedding-3-large"},
			}, nil),
			capability: core.CapabilityEmbedding,
			model:      "text-embedding-3-large",
			required:   OpenAIEndpointCapabilityEmbeddings,
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, smartRouterAccountEligibleForSmartRouter(tt.account, tt.capability, tt.model, tt.required))
		})
	}
}

func TestSmartRouterDisabledPreservesNativeCandidateOrder(t *testing.T) {
	tests := []struct {
		name            string
		smartRouterOn   bool
		advancedSchedOn bool
	}{
		{name: "master flag off", smartRouterOn: false, advancedSchedOn: true},
		{name: "official scheduler gate off", smartRouterOn: true, advancedSchedOn: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
				enabled:   tt.advancedSchedOn,
				expiresAt: time.Now().Add(time.Minute).UnixNano(),
			})
			t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)

			ledger := &smartRouterRuntimeTestLedger{}
			svc := &OpenAIGatewayService{cfg: &config.Config{
				Gateway: config.GatewayConfig{SmartRouter: config.GatewaySmartRouterConfig{Enabled: tt.smartRouterOn}},
			}}
			svc.SetSmartRouterHealthLedger(ledger)
			candidates := []openAIAccountCandidateScore{
				{account: smartRouterTestAccount(111, AccountTypeOAuth, nil, nil)},
				{account: smartRouterTestAccount(112, AccountTypeOAuth, nil, nil)},
				{account: smartRouterTestAccount(113, AccountTypeOAuth, nil, nil)},
			}
			request := OpenAIAccountScheduleRequest{
				RequestedModel:        "gpt-5.6-sol",
				RequiredCapability:    OpenAIEndpointCapabilityResponses,
				SmartRouterCapability: core.CapabilityResponses,
			}

			got := svc.reorderSmartRouterSelectionCandidates(context.Background(), request, candidates)
			require.Equal(t, candidates, got, "disabled Smart Router must leave the official candidate order unchanged")
			if !tt.smartRouterOn {
				require.Nil(t, svc.smartRouterHealth(), "master-off Smart Router must not initialize health state")
			} else {
				require.Nil(t, svc.smartRouterHealthTracker, "the official scheduler gate must prevent Smart Router from initializing health state")
			}
			require.Empty(t, ledger.events, "disabled Smart Router must not write health events")
		})
	}
}

func TestSmartRouterReordersOnlyAdmittedCandidateSlots(t *testing.T) {
	enableSmartRouterSchedulerTest(t)
	svc := &OpenAIGatewayService{cfg: &config.Config{
		Gateway: config.GatewayConfig{SmartRouter: config.GatewaySmartRouterConfig{Enabled: true}},
	}}
	first := smartRouterTestAccount(101, AccountTypeOAuth, nil, map[string]any{
		smartRouterExtraKey: map[string]any{"base_weight": 0.001},
	})
	nonAdmitted := smartRouterTestAccount(102, AccountTypeAPIKey, map[string]any{
		"model_mapping": map[string]any{"gpt-5.5": "kimi-k2"},
	}, nil)
	third := smartRouterTestAccount(103, AccountTypeOAuth, nil, map[string]any{
		smartRouterExtraKey: map[string]any{"base_weight": 1000.0},
	})
	candidates := []openAIAccountCandidateScore{
		{account: first},
		{account: nonAdmitted},
		{account: third},
	}
	req := OpenAIAccountScheduleRequest{
		RequestedModel:        "gpt-5.5",
		RequiredCapability:    OpenAIEndpointCapabilityResponses,
		SmartRouterCapability: core.CapabilityResponses,
	}

	ordered := svc.reorderSmartRouterSelectionCandidates(context.Background(), req, candidates)
	require.Len(t, ordered, len(candidates))
	require.Equal(t, nonAdmitted.ID, ordered[1].account.ID, "a non-admitted native candidate must keep its original slot")
	gotSlotIDs := []int64{ordered[0].account.ID, ordered[2].account.ID}
	require.ElementsMatch(t, []int64{first.ID, third.ID}, gotSlotIDs)
}

func TestSmartRouterNonCompactOrderRanksCoreResultsBeforeOmittedAdmittedLanes(t *testing.T) {
	omittedBeforeRanked := openAIAccountCandidateScore{account: smartRouterTestAccount(201, AccountTypeOAuth, nil, nil)}
	nativeOnly := openAIAccountCandidateScore{account: smartRouterTestAccount(202, AccountTypeAPIKey, nil, nil)}
	ranked := openAIAccountCandidateScore{account: smartRouterTestAccount(203, AccountTypeOAuth, nil, nil)}
	omittedAfterRanked := openAIAccountCandidateScore{account: smartRouterTestAccount(204, AccountTypeOAuth, nil, nil)}
	candidates := []openAIAccountCandidateScore{omittedBeforeRanked, nativeOnly, ranked, omittedAfterRanked}
	laneByAccountID := map[int64]string{
		omittedBeforeRanked.account.ID: "lane-omitted-before",
		ranked.account.ID:              "lane-ranked",
		omittedAfterRanked.account.ID:  "lane-omitted-after",
	}
	candidateByLane := map[string]openAIAccountCandidateScore{
		"lane-omitted-before": omittedBeforeRanked,
		"lane-ranked":         ranked,
		"lane-omitted-after":  omittedAfterRanked,
	}

	got := reorderSmartRouterCandidateSlots(candidates, laneByAccountID, candidateByLane, core.RoutePlan{
		OrderedLaneIDs: []string{"lane-ranked"},
	}, core.CapabilityResponses)

	require.Equal(t, []int64{203, 202, 201, 204}, smartRouterCandidateAccountIDs(got))
	require.Equal(t, len(candidates), len(got), "core omissions must not remove an admitted candidate")
	require.Equal(t, nativeOnly.account.ID, got[1].account.ID, "a non-admitted candidate must remain in its native slot")
}

func smartRouterCandidateAccountIDs(candidates []openAIAccountCandidateScore) []int64 {
	ids := make([]int64, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.account != nil {
			ids = append(ids, candidate.account.ID)
		}
	}
	return ids
}

func TestSmartRouterSameSourceOmissionDoesNotBecomeNativeRetryCap(t *testing.T) {
	enableSmartRouterSchedulerTest(t)
	svc := &OpenAIGatewayService{cfg: &config.Config{
		Gateway: config.GatewayConfig{SmartRouter: config.GatewaySmartRouterConfig{Enabled: true}},
	}}
	one := smartRouterTestAccount(111, AccountTypeOAuth, nil, map[string]any{
		smartRouterExtraKey: map[string]any{"source_group": "same-upstream", "base_weight": 1000.0},
	})
	two := smartRouterTestAccount(112, AccountTypeOAuth, nil, map[string]any{
		smartRouterExtraKey: map[string]any{"source_group": "same-upstream", "base_weight": 0.001},
	})
	candidates := []openAIAccountCandidateScore{{account: one}, {account: two}}
	ordered := svc.reorderSmartRouterSelectionCandidates(context.Background(), OpenAIAccountScheduleRequest{
		RequestedModel:        "gpt-5.5",
		RequiredCapability:    OpenAIEndpointCapabilityResponses,
		SmartRouterCapability: core.CapabilityResponses,
	}, candidates)
	require.Equal(t, []int64{one.ID, two.ID}, []int64{ordered[0].account.ID, ordered[1].account.ID})
}

func TestSmartRouterHardModelQuarantineIsNotReinsertedForNonCompact(t *testing.T) {
	enableSmartRouterSchedulerTest(t)
	svc := &OpenAIGatewayService{cfg: &config.Config{
		Gateway: config.GatewayConfig{SmartRouter: config.GatewaySmartRouterConfig{Enabled: true}},
	}}
	quarantined := smartRouterTestAccount(121, AccountTypeOAuth, nil, nil)
	healthy := smartRouterTestAccount(122, AccountTypeOAuth, nil, nil)
	tracker := core.NewHealthTracker(core.DefaultHealthPolicy(), time.Now, nil)
	tracker.Restore(core.NewHealthKey("account:121", core.CapabilityResponses, "gpt-5.5"), core.HealthSnapshot{
		HealthScore:       1,
		RecoveryStage:     core.RecoveryModelUnavailable,
		CooldownUntilUnix: time.Now().Add(time.Hour).Unix(),
	}, time.Now().Unix())
	svc.smartRouterHealthOnce.Do(func() { svc.smartRouterHealthTracker = tracker })
	ordered := svc.reorderSmartRouterSelectionCandidates(context.Background(), OpenAIAccountScheduleRequest{
		RequestedModel:        "gpt-5.5",
		RequiredCapability:    OpenAIEndpointCapabilityResponses,
		SmartRouterCapability: core.CapabilityResponses,
	}, []openAIAccountCandidateScore{{account: quarantined}, {account: healthy}})
	require.Len(t, ordered, 1)
	require.Equal(t, healthy.ID, ordered[0].account.ID)
}

func TestSmartRouterCompactHealthStateOnlyReordersAdmittedSlots(t *testing.T) {
	enableSmartRouterSchedulerTest(t)
	svc := &OpenAIGatewayService{cfg: &config.Config{
		Gateway: config.GatewayConfig{SmartRouter: config.GatewaySmartRouterConfig{Enabled: true}},
	}}
	unavailable := smartRouterTestAccount(123, AccountTypeOAuth, nil, nil)
	nonAdmitted := smartRouterTestAccount(124, AccountTypeAPIKey, map[string]any{
		"compact_model_mapping": map[string]any{"gpt-5.5*": "compact-gpt-5.5"},
	}, nil)
	healthy := smartRouterTestAccount(125, AccountTypeOAuth, nil, nil)
	tracker := smartRouterTrackerWithHealthStates(map[int64]core.RecoveryStage{
		unavailable.ID: core.RecoveryModelUnavailable,
	}, core.CapabilityResponsesCompact, "gpt-5.6-sol")
	svc.smartRouterHealthOnce.Do(func() { svc.smartRouterHealthTracker = tracker })
	request := OpenAIAccountScheduleRequest{
		RequestedModel:        "gpt-5.6-sol",
		RequireCompact:        true,
		RequiredCapability:    OpenAIEndpointCapabilityResponses,
		SmartRouterCapability: core.CapabilityResponsesCompact,
	}
	candidates := []openAIAccountCandidateScore{{account: unavailable}, {account: nonAdmitted}, {account: healthy}}

	ordered := svc.reorderSmartRouterSelectionCandidates(context.Background(), request, candidates)
	require.Len(t, ordered, len(candidates), "compact health must not remove a native candidate")
	require.Equal(t, healthy.ID, ordered[0].account.ID, "ranked healthy lane should lead admitted slots")
	require.Equal(t, nonAdmitted.ID, ordered[1].account.ID, "non-admitted candidate must retain its exact native slot")
	require.Equal(t, unavailable.ID, ordered[2].account.ID, "model-unavailable lane remains as the last admitted fallback")
}

func TestSmartRouterCompactEmptyLaneOrderFallsBackToNativeOrder(t *testing.T) {
	enableSmartRouterSchedulerTest(t)
	svc := &OpenAIGatewayService{cfg: &config.Config{
		Gateway: config.GatewayConfig{SmartRouter: config.GatewaySmartRouterConfig{Enabled: true}},
	}}
	first := smartRouterTestAccount(126, AccountTypeOAuth, nil, nil)
	second := smartRouterTestAccount(127, AccountTypeOAuth, nil, nil)
	tracker := smartRouterTrackerWithHealthStates(map[int64]core.RecoveryStage{
		first.ID:  core.RecoveryModelUnavailable,
		second.ID: core.RecoveryModelUnavailable,
	}, core.CapabilityResponsesCompact, "gpt-5.6-sol")
	svc.smartRouterHealthOnce.Do(func() { svc.smartRouterHealthTracker = tracker })
	request := OpenAIAccountScheduleRequest{
		RequestedModel:        "gpt-5.6-sol",
		RequireCompact:        true,
		RequiredCapability:    OpenAIEndpointCapabilityResponses,
		SmartRouterCapability: core.CapabilityResponsesCompact,
	}
	candidates := []openAIAccountCandidateScore{{account: first}, {account: second}}

	ordered := svc.reorderSmartRouterSelectionCandidates(context.Background(), request, candidates)
	require.Len(t, ordered, len(candidates))
	require.Equal(t, []int64{first.ID, second.ID}, []int64{ordered[0].account.ID, ordered[1].account.ID})
}

func TestSmartRouterDuplicateLaneIDFallsBackToNativeCandidates(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  OpenAIAccountScheduleRequest
	}{
		{
			name: "responses",
			req: OpenAIAccountScheduleRequest{
				RequestedModel:        "gpt-5.6-sol",
				RequiredCapability:    OpenAIEndpointCapabilityResponses,
				SmartRouterCapability: core.CapabilityResponses,
			},
		},
		{
			name: "legacy compact",
			req: OpenAIAccountScheduleRequest{
				RequestedModel:        "gpt-5.6-sol",
				RequireCompact:        true,
				RequiredCapability:    OpenAIEndpointCapabilityResponses,
				SmartRouterCapability: core.CapabilityResponsesCompact,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enableSmartRouterSchedulerTest(t)
			svc := &OpenAIGatewayService{cfg: &config.Config{
				Gateway: config.GatewayConfig{SmartRouter: config.GatewaySmartRouterConfig{Enabled: true}},
			}}
			first := smartRouterTestAccount(181, AccountTypeOAuth, nil, map[string]any{
				smartRouterExtraKey: map[string]any{"lane_id": "shared-custom-lane"},
			})
			nonAdmitted := smartRouterTestAccount(182, AccountTypeAPIKey, map[string]any{
				"compact_model_mapping": map[string]any{"gpt-5.5*": "compact-gpt-5.5"},
			}, nil)
			second := smartRouterTestAccount(183, AccountTypeOAuth, nil, map[string]any{
				smartRouterExtraKey: map[string]any{"lane_id": "shared-custom-lane"},
			})
			candidates := []openAIAccountCandidateScore{
				{account: first},
				{account: nonAdmitted},
				{account: second},
			}

			got := svc.reorderSmartRouterSelectionCandidates(context.Background(), tc.req, candidates)
			require.Equal(t, candidates, got, "ambiguous lane-to-account mapping must leave the complete native candidate order intact")
		})
	}
}

func smartRouterTrackerWithHealthStates(states map[int64]core.RecoveryStage, capability core.Capability, model string) *core.HealthTracker {
	tracker := core.NewHealthTracker(core.DefaultHealthPolicy(), time.Now, nil)
	now := time.Now()
	for accountID, stage := range states {
		tracker.Restore(core.NewHealthKey("account:"+strconv.FormatInt(accountID, 10), capability, model), core.HealthSnapshot{
			HealthScore:       1,
			RecoveryStage:     stage,
			CooldownUntilUnix: now.Add(time.Hour).Unix(),
		}, now.Unix())
	}
	return tracker
}

func TestSmartRouterEmptyLaneOrderFallsBackToNativeCandidates(t *testing.T) {
	enableSmartRouterSchedulerTest(t)
	svc := &OpenAIGatewayService{cfg: &config.Config{
		Gateway: config.GatewayConfig{SmartRouter: config.GatewaySmartRouterConfig{Enabled: true}},
	}}
	first := smartRouterTestAccount(151, AccountTypeOAuth, nil, nil)
	second := smartRouterTestAccount(152, AccountTypeOAuth, nil, nil)
	nonAdmitted := smartRouterTestAccount(153, AccountTypeAPIKey, map[string]any{
		"model_mapping": map[string]any{"gpt-5.5": "kimi-k2"},
	}, nil)
	tracker := smartRouterTrackerWithHealthStates(map[int64]core.RecoveryStage{
		first.ID:  core.RecoveryModelUnavailable,
		second.ID: core.RecoveryModelUnavailable,
	}, core.CapabilityResponses, "gpt-5.5")
	svc.smartRouterHealthOnce.Do(func() { svc.smartRouterHealthTracker = tracker })
	request := OpenAIAccountScheduleRequest{
		RequestedModel:        "gpt-5.5",
		RequiredCapability:    OpenAIEndpointCapabilityResponses,
		SmartRouterCapability: core.CapabilityResponses,
	}

	got := svc.reorderSmartRouterSelectionCandidates(context.Background(), request, []openAIAccountCandidateScore{
		{account: first}, {account: nonAdmitted}, {account: second},
	})
	require.Len(t, got, 3)
	require.Equal(t, []int64{first.ID, nonAdmitted.ID, second.ID}, []int64{
		got[0].account.ID,
		got[1].account.ID,
		got[2].account.ID,
	})

	allAdmitted := svc.reorderSmartRouterSelectionCandidates(context.Background(), request, []openAIAccountCandidateScore{
		{account: first}, {account: second},
	})
	require.Len(t, allAdmitted, 2)
	require.Equal(t, []int64{first.ID, second.ID}, []int64{
		allAdmitted[0].account.ID,
		allAdmitted[1].account.ID,
	})
}

func TestSmartRouterGPTImageCooldownUsesLastResortOnlyWhenAllLanesCool(t *testing.T) {
	for _, model := range []string{"gpt-image-2", "gpt-image-2.5"} {
		t.Run(model, func(t *testing.T) {
			makeService := func(ids []int64, stages map[int64]core.RecoveryStage) (*OpenAIGatewayService, []openAIAccountCandidateScore) {
				enableSmartRouterSchedulerTest(t)
				svc := &OpenAIGatewayService{cfg: &config.Config{
					Gateway: config.GatewayConfig{SmartRouter: config.GatewaySmartRouterConfig{Enabled: true}},
				}}
				candidates := make([]openAIAccountCandidateScore, 0, len(ids))
				for _, id := range ids {
					candidates = append(candidates, openAIAccountCandidateScore{account: smartRouterTestAccount(id, AccountTypeOAuth, nil, nil)})
				}
				tracker := smartRouterTrackerWithHealthStates(stages, core.CapabilityImageGeneration, model)
				svc.smartRouterHealthOnce.Do(func() { svc.smartRouterHealthTracker = tracker })
				return svc, candidates
			}
			request := OpenAIAccountScheduleRequest{
				RequestedModel:          model,
				RequiredImageCapability: OpenAIImagesCapabilityBasic,
				SmartRouterCapability:   core.CapabilityImageGeneration,
			}

			coolingService, coolingCandidates := makeService([]int64{161, 162}, map[int64]core.RecoveryStage{
				161: core.RecoveryCooling,
				162: core.RecoveryCooling,
			})
			lastResort := coolingService.reorderSmartRouterSelectionCandidates(context.Background(), request, coolingCandidates)
			require.Len(t, lastResort, 2, "all-cooling GPT image lanes remain as last-resort candidates")

			mixedService, mixedCandidates := makeService([]int64{163, 164}, map[int64]core.RecoveryStage{
				163: core.RecoveryCooling,
			})
			healthyAlternative := mixedService.reorderSmartRouterSelectionCandidates(context.Background(), request, mixedCandidates)
			require.Len(t, healthyAlternative, 1)
			require.Equal(t, int64(164), healthyAlternative[0].account.ID)
		})
	}
}

func TestSmartRouterNativeStickyAndPreviousResponsePathsBypassHealthReorder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request OpenAIAccountScheduleRequest
	}{
		{
			name: "weighted sticky",
			request: OpenAIAccountScheduleRequest{
				StickyWeighted:  true,
				StickyAccountID: 171,
			},
		},
		{
			name:    "previous response",
			request: OpenAIAccountScheduleRequest{PreviousResponseID: "resp_existing"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enableSmartRouterSchedulerTest(t)
			svc := &OpenAIGatewayService{cfg: &config.Config{
				Gateway: config.GatewayConfig{SmartRouter: config.GatewaySmartRouterConfig{Enabled: true}},
			}}
			quarantined := smartRouterTestAccount(171, AccountTypeOAuth, nil, nil)
			healthy := smartRouterTestAccount(172, AccountTypeOAuth, nil, nil)
			tracker := smartRouterTrackerWithHealthStates(map[int64]core.RecoveryStage{
				quarantined.ID: core.RecoveryModelUnavailable,
			}, core.CapabilityResponses, "gpt-5.5")
			svc.smartRouterHealthOnce.Do(func() { svc.smartRouterHealthTracker = tracker })
			tc.request.RequestedModel = "gpt-5.5"
			tc.request.RequiredCapability = OpenAIEndpointCapabilityResponses
			tc.request.SmartRouterCapability = core.CapabilityResponses
			if tc.name != "weighted sticky" {
				tc.request.StickyWeighted = false
			}
			scheduler := &defaultOpenAIAccountScheduler{service: svc}
			candidates := []openAIAccountCandidateScore{
				{account: quarantined, loadInfo: &AccountLoadInfo{}},
				{account: healthy, loadInfo: &AccountLoadInfo{}},
			}
			require.NotNil(t, candidates[0].account)
			require.NotNil(t, candidates[1].account)
			ordered := scheduler.buildOpenAISelectionOrder(context.Background(), tc.request, openAIAccountLoadPlan{
				candidates: candidates,
				topK:       2,
			})
			require.Len(t, ordered, 2, "native sticky/continuation ordering must not be hard-filtered by Smart Router")
		})
	}
}

func TestSmartRouterPolicyUsesT0RankingControls(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{
		Gateway: config.GatewayConfig{SmartRouter: config.GatewaySmartRouterConfig{
			Enabled:                 true,
			TopK:                    2,
			SameSourceGroupAttempts: 3,
			CostBiasMax:             4.5,
			Scoring: config.GatewaySmartRouterScoringConfig{
				Priority: 0.4,
				Cost:     1.1,
				Health:   1.3,
				Load:     0.8,
				Queue:    0.5,
				Latency:  0.2,
				Recovery: 0.7,
			},
		}},
	}}
	policy := svc.smartRouterPolicy()
	require.True(t, policy.Enabled)
	require.Equal(t, 2, policy.TopK)
	require.Equal(t, 3, policy.SameSourceGroupAttempts)
	require.Equal(t, 4.5, policy.CostBiasMax)
	require.Equal(t, core.ScoreWeights{Priority: 0.4, Cost: 1.1, Health: 1.3, Load: 0.8, Queue: 0.5, Latency: 0.2, Recovery: 0.7}, policy.Weights)
}

func TestSmartRouterCapabilityForSelectionKeepsResponsesImagesSeparate(t *testing.T) {
	request := OpenAIAccountScheduleRequest{RequiredCapability: OpenAIEndpointCapabilityResponses}
	require.Equal(t, core.CapabilityResponses, smartRouterCapabilityForSelection(context.Background(), request))
	require.Equal(t, core.CapabilityImageGeneration, smartRouterCapabilityForSelection(
		WithOpenAIImageGenerationIntent(context.Background()), request,
	))
	request.RequireCompact = true
	require.Equal(t, core.CapabilityResponsesCompact, smartRouterCapabilityForSelection(
		WithOpenAIImageGenerationIntent(context.Background()), request,
	), "compact remains the explicit capability even if a request context carries an image intent")
}

func TestSmartRouterCompactLaneUsesCompactModelMapping(t *testing.T) {
	account := smartRouterTestAccount(131, AccountTypeAPIKey, map[string]any{
		"model_mapping":         map[string]any{"gpt-5.6-*": "gpt-5.6-sol"},
		"compact_model_mapping": map[string]any{"gpt-5.5": "compact-gpt-5.5"},
	}, nil)
	require.Equal(t, []string{"gpt-5.5"}, smartRouterModelPatterns(account, core.CapabilityResponsesCompact))
	require.Equal(t, []string{"gpt-5.6-*"}, smartRouterModelPatterns(account, core.CapabilityResponses))
}

func TestSmartRouterLaneSnapshotHandlesExplicitEmptyCapabilities(t *testing.T) {
	account := smartRouterTestAccount(141, AccountTypeAPIKey, nil, map[string]any{
		smartRouterExtraKey: map[string]any{"capabilities": []any{}},
	})
	lane, ok := smartRouterLaneSnapshot(account, nil, false, 0, 0, false, core.CapabilityChat)
	require.True(t, ok)
	require.Contains(t, lane.Capabilities, core.CapabilityResponsesCompact)
}

func TestSmartRouterLaneSnapshotDoesNotApplyDeferredConcurrencyCaps(t *testing.T) {
	account := smartRouterTestAccount(142, AccountTypeAPIKey, nil, map[string]any{
		smartRouterExtraKey: map[string]any{
			"max_concurrency":              1,
			"source_group_max_concurrency": 1,
		},
	})
	lane, ok := smartRouterLaneSnapshot(account, &AccountLoadInfo{CurrentConcurrency: 3}, true, 0, 0, false, core.CapabilityResponses)

	require.True(t, ok)
	require.Equal(t, 3, lane.CurrentConcurrency, "observed load remains available to Smart Router")
	require.Zero(t, lane.MaxConcurrency, "the deferred T0 cap must not become a second candidate-exclusion policy")
	require.Zero(t, lane.SourceGroupMaxConcurrency, "the deferred source-group cap must not become a second candidate-exclusion policy")
}
