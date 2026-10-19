//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/stretchr/testify/require"
)

func imageRecoveryTestService(t *testing.T, advanced, weighted bool, accounts []Account, ledger *smartRouterRuntimeTestLedger) *OpenAIGatewayService {
	t.Helper()
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{enabled: advanced, stickyWeightedEnabled: weighted, expiresAt: time.Now().Add(time.Hour).UnixNano()})
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.SmartRouter.Enabled = true
	cfg.Gateway.SmartRouter.TopK = 2
	cfg.Gateway.OpenAIWS.LBTopK = 2
	cfg.Gateway.ImageEditTransientCooldownSeconds = 12
	cfg.Gateway.ImageGenerationTransientCooldownSeconds = 30
	svc := &OpenAIGatewayService{
		cfg: cfg, accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              &schedulerTestGatewayCache{sessionBindings: map[string]int64{"sticky-a": accounts[0].ID}},
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
	if ledger != nil {
		svc.SetSmartRouterHealthLedger(ledger)
	}
	return svc
}

func imageRecoveryAccounts() []Account {
	return []Account{
		{ID: 701, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 2, Priority: 0},
		{ID: 702, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 2, Priority: 10},
	}
}

func imageRecoveryState(event core.HealthEvent) SmartRouterHealthState {
	return SmartRouterHealthState{LaneID: event.Key.LaneID, AccountID: event.AccountID, SourceGroup: event.SourceGroup, Capability: event.Key.Capability, ModelFamily: event.Key.Model,
		LastFailureUnix: event.OccurredAtUnix,
		Snapshot:        core.HealthSnapshot{HealthPenalty: event.HealthPenalty, RecoveryPriority: event.RecoveryPriority, HealthScore: event.HealthScore, ErrorRateEWMA: event.ErrorRateEWMA, CooldownUntilUnix: event.CooldownUntilUnix, RecoveryStage: event.RecoveryStage, ConsecutiveFailures: event.ConsecutiveFailures, ConsecutiveSuccesses: event.ConsecutiveSuccesses}}
}

func TestSmartRouterImageFreshSelectionStickyRestoreAndLastResort(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		for _, weighted := range []bool{false, true} {
			for _, edit := range []bool{false, true} {
				t.Run(fmt.Sprintf("advanced=%t/weighted=%t/edit=%t", advanced, weighted, edit), func(t *testing.T) {
					accounts := imageRecoveryAccounts()
					ledger := &smartRouterRuntimeTestLedger{}
					svc := imageRecoveryTestService(t, advanced, weighted, accounts, ledger)
					ctx := WithSmartRouterImageRequest(context.Background(), "gpt-image-2", edit)
					parsed := &OpenAIImagesRequest{Model: "client-alias"}
					if edit {
						parsed.Endpoint = openAIImagesEditsEndpoint
					}
					selection, _, err := svc.SelectAccountWithSchedulerForImageOperation(ctx, nil, "sticky-a", "gpt-image-2", nil, OpenAIImagesCapabilityBasic, edit)
					require.NoError(t, err)
					require.Equal(t, accounts[0].ID, selection.Account.ID)
					if selection.ReleaseFunc != nil {
						selection.ReleaseFunc()
					}
					attempt, release, err := svc.AcquireSmartRouterImageAttempt(ctx, selection.Account, parsed)
					require.NoError(t, err)
					failure := openAIImagesHTTPFailure(400, []byte(`{"error":{"code":"image_poll_timeout"}}`), nil)
					svc.ReportSmartRouterImageResult(attempt, selection.Account, parsed, nil, failure, 30)
					release()
					require.Len(t, ledger.events, 1)
					require.Equal(t, "gpt-image-2", ledger.events[0].Key.Model, "report uses selection model, never billing/client alias")
					selection, _, err = svc.SelectAccountWithSchedulerForImageOperation(ctx, nil, "sticky-a", "gpt-image-2", nil, OpenAIImagesCapabilityBasic, edit)
					require.NoError(t, err)
					require.Equal(t, accounts[1].ID, selection.Account.ID, "a fresh request cannot reuse failed sticky A")
					if selection.ReleaseFunc != nil {
						selection.ReleaseFunc()
					}
					restoredLedger := &smartRouterRuntimeTestLedger{states: []SmartRouterHealthState{imageRecoveryState(ledger.events[0])}}
					restarted := imageRecoveryTestService(t, advanced, weighted, accounts, restoredLedger)
					selection, _, err = restarted.SelectAccountWithSchedulerForImageOperation(ctx, nil, "sticky-a", "gpt-image-2", nil, OpenAIImagesCapabilityBasic, edit)
					require.NoError(t, err)
					require.Equal(t, accounts[1].ID, selection.Account.ID, "durable cooldown survives restart")
					if selection.ReleaseFunc != nil {
						selection.ReleaseFunc()
					}
					restarted.ReportSmartRouterImageResult(ctx, &accounts[1], parsed, nil, failure, 30)
					selection, _, err = restarted.SelectAccountWithSchedulerForImageOperation(ctx, nil, "sticky-a", "gpt-image-2", nil, OpenAIImagesCapabilityBasic, edit)
					require.NoError(t, err)
					require.NotNil(t, selection)
					require.True(t, selection.SmartRouterImageLastResort, "all cooling permits a bounded final probe")
					if selection.ReleaseFunc != nil {
						selection.ReleaseFunc()
					}
					_, normalRelease, normalErr := restarted.AcquireSmartRouterImageAttempt(ctx, selection.Account, parsed)
					require.Error(t, normalErr, "normal admission remains strict for a cooling selection")
					normalRelease()
					releases := []func(){}
					for i := range accounts {
						_, release, acquireErr := restarted.AcquireSmartRouterImageAttempt(ctx, &accounts[i], parsed, true)
						require.NoError(t, acquireErr)
						releases = append(releases, release)
					}
					selection, _, err = restarted.SelectAccountWithSchedulerForImageOperation(ctx, nil, "sticky-a", "gpt-image-2", nil, OpenAIImagesCapabilityBasic, edit)
					var unavailable *SmartRouterImageUnavailableError
					require.ErrorAs(t, err, &unavailable)
					require.Nil(t, selection, "occupied local leases cannot be bypassed by last resort")
					require.Positive(t, unavailable.RetryAfterSeconds())
					for _, release := range releases {
						release()
					}
					otherCap := core.CapabilityImageEdit
					if edit {
						otherCap = core.CapabilityImageGeneration
					}
					require.True(t, restarted.smartRouterHealth().ImageAdmission(core.NewHealthKey("account:701", otherCap, "gpt-image-2"), time.Now().Unix()).Allowed)
					require.True(t, restarted.smartRouterHealth().ImageAdmission(core.NewHealthKey("account:701", ledger.events[0].Key.Capability, "gpt-image-1"), time.Now().Unix()).Allowed)
					require.False(t, restarted.isOpenAIAccountRuntimeBlocked(&accounts[0]))
				})
			}
		}
	}
}

func TestSmartRouterImagePostQueueAdmissionAndRecovery(t *testing.T) {
	accounts := imageRecoveryAccounts()
	svc := imageRecoveryTestService(t, true, false, accounts, nil)
	ctx := WithSmartRouterImageRequest(context.Background(), "gpt-image-2", false)
	parsed := &OpenAIImagesRequest{Model: "gpt-image-2"}
	failure := openAIImagesHTTPFailure(400, []byte(`{"error":{"code":"image_poll_timeout"}}`), nil)
	// Selection may have happened before a concurrent attempt failed. The
	// dispatch check after waiting must reject that stale selection.
	svc.ReportSmartRouterImageResult(ctx, &accounts[0], parsed, nil, failure, 1)
	_, release, err := svc.AcquireSmartRouterImageAttempt(ctx, &accounts[0], parsed)
	require.Error(t, err)
	release()
	key := core.NewHealthKey("account:701", core.CapabilityImageGeneration, "gpt-image-2")
	svc.smartRouterHealth().Restore(key, core.HealthSnapshot{HealthScore: .5, RecoveryStage: core.RecoveryCooling, CooldownUntilUnix: time.Now().Add(-time.Second).Unix(), ConsecutiveFailures: 1}, time.Now().Add(-time.Minute).Unix())
	for i := 0; i < 3; i++ {
		attempt, release, err := svc.AcquireSmartRouterImageAttempt(ctx, &accounts[0], parsed)
		require.NoError(t, err)
		_, secondRelease, secondErr := svc.AcquireSmartRouterImageAttempt(ctx, &accounts[0], parsed)
		require.Error(t, secondErr, "only one recovering request per key in this process")
		secondRelease()
		svc.ReportSmartRouterImageResult(attempt, &accounts[0], parsed, &OpenAIForwardResult{ImageCount: 1}, nil, 1)
		release()
	}
	lane, _ := svc.smartRouterImageLane(&accounts[0], core.CapabilityImageGeneration, "gpt-image-2")
	require.Equal(t, core.RecoveryNormal, svc.smartRouterHealth().Snapshot(lane, core.CapabilityImageGeneration, "gpt-image-2", time.Now().Unix()).RecoveryStage)
	require.Equal(t, 0, accounts[0].Priority)
	svc.openaiOAuth429RetryStartedAt.Store(accounts[0].ID, time.Now())
	svc.ReportOpenAIImageScheduleResult(ctx, &accounts[0], "gpt-image-2", true, nil)
	_, retryWindowPresent := svc.openaiOAuth429RetryStartedAt.Load(accounts[0].ID)
	require.False(t, retryWindowPresent, "successful image retains native OAuth429 retry-window cleanup")
}

func TestSmartRouterImageIdentityAndLegacyOwnerRestore(t *testing.T) {
	accounts := imageRecoveryAccounts()
	for i := range accounts {
		accounts[i].Extra = map[string]any{"smart_router": map[string]any{"lane_id": "shared-custom"}}
		accounts[i].GroupIDs = []int64{int64(i + 1)}
	}
	ledger := &smartRouterRuntimeTestLedger{states: []SmartRouterHealthState{{LaneID: "shared-custom", AccountID: accounts[0].ID, Capability: core.CapabilityImageGeneration, ModelFamily: "gpt-image-2", Snapshot: core.HealthSnapshot{HealthScore: .5, CooldownUntilUnix: time.Now().Add(time.Minute).Unix(), RecoveryStage: core.RecoveryCooling}}}}
	svc := imageRecoveryTestService(t, false, false, accounts, ledger)
	ctx := WithSmartRouterImageRequest(context.Background(), "gpt-image-2", false)
	require.False(t, svc.smartRouterImageAccountAvailable(ctx, &accounts[0], core.CapabilityImageGeneration, "gpt-image-2"))
	require.True(t, svc.smartRouterImageAccountAvailable(ctx, &accounts[1], core.CapabilityImageGeneration, "gpt-image-2"), "duplicate custom lane in another group has a different account owner")
	require.Equal(t, "account:701", smartRouterImageLaneID(701, "account:701"))
	for _, raw := range []string{"custom", strings.Repeat("long", 100), "account:701/image/" + strings.Repeat("long", 100)} {
		id := smartRouterImageLaneID(701, raw)
		require.LessOrEqual(t, len(id), 255)
		require.Equal(t, id, smartRouterImageLaneID(701, id))
		require.NotEqual(t, id, smartRouterImageLaneID(702, raw))
	}
}

func TestSmartRouterImageOwnershipMatchesExactCapability(t *testing.T) {
	accounts := imageRecoveryAccounts()
	accounts[0].Extra = map[string]any{"smart_router": map[string]any{"capabilities": []any{"image_generation"}}}
	svc := imageRecoveryTestService(t, true, false, accounts, nil)
	repo := &imageTransientAccountRepo{}
	svc.accountRepo = repo
	failure := &UpstreamFailoverError{StatusCode: http.StatusBadGateway}
	generation := WithSmartRouterImageRequest(context.Background(), "gpt-image-2", false)
	edit := WithSmartRouterImageRequest(context.Background(), "gpt-image-2", true)
	require.True(t, svc.smartRouterOwnsImageHealthForContext(generation, &accounts[0]))
	require.False(t, svc.smartRouterOwnsImageHealthForContext(edit, &accounts[0]))
	svc.ReportOpenAIImageScheduleResult(generation, &accounts[0], "gpt-image-2", false, nil, failure)
	require.Zero(t, svc.openaiAccountStats.size())
	svc.ReportOpenAIImageScheduleResult(edit, &accounts[0], "gpt-image-2", false, nil, failure)
	require.Equal(t, 1, svc.openaiAccountStats.size())
	svc.TempUnscheduleImageGenerationTransientError(generation, &accounts[0], failure)
	require.Empty(t, repo.calls)
	svc.TempUnscheduleImageEditTransientError(edit, &accounts[0], failure)
	require.Len(t, repo.calls, 1, "unmanaged edit retains legacy behavior")
}

func TestSmartRouterImageDoesNotOverrideAuthOr429Policy(t *testing.T) {
	accounts := imageRecoveryAccounts()
	svc := imageRecoveryTestService(t, true, false, accounts, nil)
	ctx := WithOpenAIImagesEndpoint(WithSmartRouterImageRequest(context.Background(), "gpt-image-2", false))
	body := []byte(`{"error":{"code":"organization_deactivated","message":"organization deactivated"}}`)
	require.True(t, svc.handleOpenAIAccountUpstreamError(ctx, &accounts[0], http.StatusForbidden, nil, body, "gpt-image-2"))
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(&accounts[0]))
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests} {
		require.False(t, smartRouterOwnsImageHTTPFailure(status, []byte(`{"error":{"message":"model unavailable"}}`)))
	}
}

func TestSmartRouterImageExplicitEmptyCapabilitiesOptOutWithoutTextEndpoint(t *testing.T) {
	accounts := imageRecoveryAccounts()
	accounts[0].Credentials = map[string]any{"openai_capabilities": []string{"images"}}
	accounts[0].Extra = map[string]any{"smart_router": map[string]any{"capabilities": []any{}}}
	svc := imageRecoveryTestService(t, true, false, accounts, nil)
	require.False(t, accounts[0].SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions))
	for _, capability := range []core.Capability{core.CapabilityImageGeneration, core.CapabilityImageEdit} {
		_, managed := svc.smartRouterImageLane(&accounts[0], capability, "gpt-image-2")
		require.False(t, managed, "explicit empty capabilities remains unmanaged")
	}
}

func TestSmartRouterMixedNativeResponsesKeepsNativeTopK(t *testing.T) {
	accounts := imageRecoveryAccounts()
	svc := imageRecoveryTestService(t, true, false, accounts, nil)
	scheduler := &defaultOpenAIAccountScheduler{service: svc}
	candidates := []openAIAccountCandidateScore{
		{account: &accounts[0], loadInfo: &AccountLoadInfo{}, score: 10},
		{account: &accounts[1], loadInfo: &AccountLoadInfo{}, score: 1},
	}
	req := OpenAIAccountScheduleRequest{SmartRouterCapability: core.CapabilityImageGeneration, RequestedModel: "gpt-image-2", StickyWeighted: true}
	plan := openAIAccountLoadPlan{candidates: candidates, topK: 1}
	native := scheduler.buildOpenAISelectionOrder(context.Background(), req, plan)
	require.Len(t, native, 1, "image intent alone must preserve native TopK and weighting")
	require.Equal(t, accounts[0].ID, native[0].account.ID)
	req.RequiredImageCapability = OpenAIImagesCapabilityBasic
	explicit := scheduler.buildOpenAISelectionOrder(WithSmartRouterImageRequest(context.Background(), "gpt-image-2", false), req, plan)
	require.Len(t, explicit, 2, "actual Images selection keeps healthy overflow under scoped ordering")

	// The existing optional adapter must not newly admit a non-GPT image lane
	// merely because a native Responses request contains image intent.
	accounts[0].Credentials = map[string]any{"model_mapping": map[string]any{"custom-image": "flux-image"}}
	accounts[1].Credentials = map[string]any{"model_mapping": map[string]any{"custom-image": "gpt-image-2"}}
	accounts[0].Priority, accounts[1].Priority = 10, 0
	req.RequestedModel = "custom-image"
	req.RequiredImageCapability = ""
	native = svc.reorderSmartRouterSelectionCandidates(context.Background(), req, candidates)
	require.Equal(t, accounts[0].ID, native[0].account.ID, "unmanaged native candidate retains its slot")
	req.RequiredImageCapability = OpenAIImagesCapabilityBasic
	explicit = svc.reorderSmartRouterSelectionCandidates(WithSmartRouterImageRequest(context.Background(), "custom-image", false), req, candidates)
	require.Equal(t, accounts[1].ID, explicit[0].account.ID, "explicit Images admits and reorders both image lanes")
}

func TestSmartRouterImageOrdinaryAlternativesPrecedeRecoveryProbe(t *testing.T) {
	for _, stage := range []core.RecoveryStage{core.RecoveryProbeDue, core.RecoveryWarming5, core.RecoveryWarming25} {
		for _, subscription := range []bool{false, true} {
			for _, unmanagedBackup := range []bool{false, true} {
				for _, ready := range []string{"backup", "probe", "none"} {
					t.Run(fmt.Sprintf("stage=%s/subscription=%t/unmanaged=%t/ready=%s", stage, subscription, unmanagedBackup, ready), func(t *testing.T) {
						accounts := append(imageRecoveryAccounts(), Account{ID: 703, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 2, Priority: -10})
						if subscription {
							accounts[2].Type = AccountTypeOAuth
							accounts[2].Credentials = map[string]any{"plan_type": "plus"}
						}
						if unmanagedBackup {
							accounts[1].Extra = map[string]any{"smart_router": map[string]any{"enabled": false}}
						}
						svc := imageRecoveryTestService(t, true, false, accounts, nil)
						openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{enabled: true, subscriptionPriorityEnabled: subscription, expiresAt: time.Now().Add(time.Hour).UnixNano()})
						svc.cfg.Gateway.Scheduling.FallbackWaitTimeout = time.Second
						svc.cfg.Gateway.Scheduling.FallbackMaxWaiting = 3
						acquired := []int64{}
						slots := schedulerTestConcurrencyCache{acquiredIDs: &acquired, acquireResults: map[int64]bool{701: false, 702: ready == "backup", 703: ready == "probe"}}
						svc.concurrencyService = NewConcurrencyService(slots)
						svc.smartRouterHealth().Restore(core.NewHealthKey("account:703", core.CapabilityImageGeneration, "gpt-image-2"), core.HealthSnapshot{HealthScore: .8, RecoveryStage: stage, ConsecutiveFailures: 1}, time.Now().Add(-time.Minute).Unix())
						selection, _, err := svc.SelectAccountWithSchedulerForImageOperation(context.Background(), nil, "", "gpt-image-2", nil, OpenAIImagesCapabilityBasic, false)
						require.NoError(t, err)
						require.NotNil(t, selection)
						if selection.ReleaseFunc != nil {
							defer selection.ReleaseFunc()
						}
						require.LessOrEqual(t, len(acquired), openAIAccountSelectionProbeLimit)
						switch ready {
						case "backup":
							require.Equal(t, int64(702), selection.Account.ID, "ordinary/native usable B precedes recovering P after A slot denial")
							require.NotContains(t, acquired, int64(703))
						case "probe":
							require.Equal(t, int64(703), selection.Account.ID, "a ready probe remains reachable when ordinary slots are full")
							require.GreaterOrEqual(t, len(acquired), 3)
							require.Equal(t, []int64{701, 702, 703}, acquired[:3])
						case "none":
							require.False(t, selection.Acquired)
							require.NotNil(t, selection.WaitPlan)
							require.Equal(t, time.Second, selection.WaitPlan.Timeout)
							require.Equal(t, 3, selection.WaitPlan.MaxWaiting)
						}
					})
				}
			}
		}
	}
}

func TestSmartRouterImageHealthySubscriptionPreferenceIsPreserved(t *testing.T) {
	accounts := imageRecoveryAccounts()
	accounts[1].Type = AccountTypeOAuth
	accounts[1].Credentials = map[string]any{"plan_type": "plus"}
	svc := imageRecoveryTestService(t, true, false, accounts, nil)
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{enabled: true, subscriptionPriorityEnabled: true, expiresAt: time.Now().Add(time.Hour).UnixNano()})
	selection, _, err := svc.SelectAccountWithSchedulerForImageOperation(context.Background(), nil, "", "gpt-image-2", nil, OpenAIImagesCapabilityBasic, false)
	require.NoError(t, err)
	require.Equal(t, accounts[1].ID, selection.Account.ID, "healthy subscription preference remains ahead of native priority")
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestSmartRouterImageWeightedStickyCannotResurrectRecoveryProbe(t *testing.T) {
	for _, stage := range []core.RecoveryStage{core.RecoveryProbeDue, core.RecoveryWarming5, core.RecoveryWarming25} {
		t.Run(string(stage), func(t *testing.T) {
			accounts := imageRecoveryAccounts()
			svc := imageRecoveryTestService(t, true, true, accounts, nil)
			svc.smartRouterHealth().Restore(core.NewHealthKey("account:701", core.CapabilityImageGeneration, "gpt-image-2"), core.HealthSnapshot{HealthScore: .8, RecoveryStage: stage}, time.Now().Add(-time.Minute).Unix())
			ctx := WithSmartRouterImageRequest(context.Background(), "gpt-image-2", false)
			req := OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, RequestedModel: "gpt-image-2", RequiredImageCapability: OpenAIImagesCapabilityBasic, SmartRouterCapability: core.CapabilityImageGeneration, StickyWeighted: true, StickyAccountID: accounts[0].ID}
			scheduler := &defaultOpenAIAccountScheduler{service: svc, stats: newOpenAIAccountRuntimeStats()}
			selection, err := scheduler.tryFallbackToWeightedSticky(ctx, req)
			require.NoError(t, err)
			require.Nil(t, selection, "recovering sticky must defer to core's chosen probe")
			svc.cfg.Gateway.SmartRouter.Enabled = false
			selection, err = scheduler.tryFallbackToWeightedSticky(ctx, req)
			require.NoError(t, err)
			require.NotNil(t, selection, "disabled Smart Router keeps native weighted fallback")
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}
}

func TestSmartRouterImageLastResortKeepsNativeExclusions(t *testing.T) {
	for _, veto := range []string{"excluded", "disabled", "auth", "model", "group", "model_unavailable"} {
		t.Run(veto, func(t *testing.T) {
			accounts := imageRecoveryAccounts()
			var groupID *int64
			excluded := map[int64]struct{}{}
			switch veto {
			case "excluded":
				excluded[accounts[0].ID] = struct{}{}
			case "disabled":
				accounts[0].Schedulable = false
			case "auth":
				// Native auth rejection is persisted as an error account. A local
				// runtime block alone is intentionally not authoritative in this repo.
				accounts[0].Status = StatusError
				accounts[0].ErrorMessage = "organization_deactivated"
			case "model":
				accounts[0].Credentials = map[string]any{"model_mapping": map[string]any{"other-image": "other-image"}}
			case "group":
				id := int64(7)
				groupID = &id
				accounts[0].GroupIDs, accounts[1].GroupIDs = []int64{8}, []int64{7}
			}
			svc := imageRecoveryTestService(t, true, false, accounts, nil)
			if veto == "group" {
				svc.cfg.RunMode = config.RunModeStandard
				svc.accountRepo = schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}}
			}
			if veto == "auth" {
				svc.BlockAccountScheduling(&accounts[0], time.Now().Add(time.Hour), "openai_access_state")
				require.False(t, accounts[0].IsSchedulable(), "persisted native auth veto exists before either selection pass")
			}
			for i := range accounts {
				stage := core.RecoveryCooling
				if i == 0 && veto == "model_unavailable" {
					stage = core.RecoveryModelUnavailable
				}
				svc.smartRouterHealth().Restore(core.NewHealthKey(fmt.Sprintf("account:%d", accounts[i].ID), core.CapabilityImageGeneration, "gpt-image-2"), core.HealthSnapshot{HealthScore: .5, RecoveryStage: stage, CooldownUntilUnix: time.Now().Add(time.Minute).Unix()}, time.Now().Add(-time.Minute).Unix())
			}
			selection, _, err := svc.SelectAccountWithSchedulerForImageOperation(context.Background(), groupID, "", "gpt-image-2", excluded, OpenAIImagesCapabilityBasic, false)
			require.NoError(t, err)
			require.Equal(t, accounts[1].ID, selection.Account.ID, "last resort cannot override a native or exact-model veto")
			require.True(t, selection.SmartRouterImageLastResort)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}
}

func TestSmartRouterImageLastResortSharesSelectionProbeBudget(t *testing.T) {
	accounts := imageRecoveryAccounts()
	svc := imageRecoveryTestService(t, true, false, accounts, nil)
	acquired := []int64{}
	svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{acquiredIDs: &acquired})
	ctx := WithSmartRouterImageRequest(context.Background(), "gpt-image-2", false)
	budget := newOpenAISelectionProbeBudget()
	budget.enableLimit()
	budget.acquires = openAIAccountSelectionProbeLimit
	ctx = context.WithValue(ctx, smartRouterImageSelectionKey{}, &smartRouterImageSelectionState{ProbeBudget: budget})
	scope := smartRouterImageRequestScope(ctx, nil)
	scope.LastResort = true
	ctx = context.WithValue(ctx, smartRouterImageScopeKey{}, scope)
	for i := range accounts {
		svc.smartRouterHealth().Restore(core.NewHealthKey(fmt.Sprintf("account:%d", accounts[i].ID), core.CapabilityImageGeneration, "gpt-image-2"), core.HealthSnapshot{HealthScore: .5, RecoveryStage: core.RecoveryCooling, CooldownUntilUnix: time.Now().Add(time.Minute).Unix()}, time.Now().Add(-time.Minute).Unix())
	}
	scheduler := &defaultOpenAIAccountScheduler{service: svc, stats: newOpenAIAccountRuntimeStats()}
	selection, _, _, _, _ := scheduler.selectByLoadBalance(ctx, OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, RequestedModel: "gpt-image-2", RequiredImageCapability: OpenAIImagesCapabilityBasic, SmartRouterCapability: core.CapabilityImageGeneration})
	require.Empty(t, acquired, "final pass cannot reset exhausted slot probe budget")
	if selection != nil {
		require.False(t, selection.Acquired)
	}
	require.Equal(t, openAIAccountSelectionProbeLimit, budget.acquires)
}

func TestSmartRouterImageSelectionEntrypointSharesStickyProbeBudget(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		for _, weighted := range []bool{false, true} {
			if weighted && !advanced {
				continue // Weighted sticky is only active with the advanced scheduler.
			}
			for _, capability := range []OpenAIImagesCapability{OpenAIImagesCapabilityBasic, OpenAIImagesCapabilityNative} {
				t.Run(fmt.Sprintf("advanced=%t/weighted=%t/capability=%s", advanced, weighted, capability), func(t *testing.T) {
					accounts := make([]Account, openAIAccountSelectionProbeLimit+1)
					denied := make(map[int64]bool, len(accounts))
					for i := range accounts {
						accounts[i] = Account{ID: int64(701 + i), Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 2, Priority: i}
						denied[accounts[i].ID] = false
					}
					svc := imageRecoveryTestService(t, advanced, weighted, accounts, nil)
					svc.cfg.Gateway.Scheduling.FallbackWaitTimeout = time.Second
					svc.cfg.Gateway.Scheduling.FallbackMaxWaiting = 3
					var acquired []int64
					svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{acquiredIDs: &acquired, acquireResults: denied})

					selection, _, err := svc.SelectAccountWithSchedulerForImageOperation(context.Background(), nil, "sticky-a", "gpt-image-2", nil, capability, false)
					require.NoError(t, err)
					require.NotNil(t, selection)
					require.False(t, selection.Acquired)
					require.NotNil(t, selection.WaitPlan, "exhausting fast probes must retain a bounded healthy wait option")
					require.Equal(t, time.Second, selection.WaitPlan.Timeout)
					require.Equal(t, 3, selection.WaitPlan.MaxWaiting)
					require.False(t, selection.SmartRouterImageLastResort)
					require.Len(t, acquired, openAIAccountSelectionProbeLimit, "sticky escape and weighted fallback cannot add a 65th slot probe")
				})
			}
		}
	}
}

func TestSmartRouterImageStickyProbeBudgetPreservesNativePaths(t *testing.T) {
	for _, smart := range []bool{false, true} {
		for _, explicitImage := range []bool{false, true} {
			for _, weighted := range []bool{false, true} {
				for _, exhausted := range []string{"slot", "database"} {
					t.Run(fmt.Sprintf("smart=%t/image=%t/weighted=%t/exhausted=%s", smart, explicitImage, weighted, exhausted), func(t *testing.T) {
						accounts := imageRecoveryAccounts()
						svc := imageRecoveryTestService(t, true, weighted, accounts, nil)
						svc.cfg.Gateway.SmartRouter.Enabled = smart
						byID := map[int64]*Account{accounts[0].ID: &accounts[0]}
						repo := &upstreamCostCountingAccountRepo{accounts: byID}
						svc.accountRepo = repo
						svc.schedulerSnapshot = &SchedulerSnapshotService{cache: &openAISnapshotCacheStub{accountsByID: byID}}
						var acquired []int64
						svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{acquiredIDs: &acquired})
						budget := newOpenAISelectionProbeBudget()
						budget.enableLimit()
						if exhausted == "slot" {
							budget.acquires = openAIAccountSelectionProbeLimit
						} else {
							budget.rechecks = openAIAccountSelectionProbeLimit
						}
						ctx := WithSmartRouterImageRequest(context.Background(), "gpt-image-2", false)
						ctx = context.WithValue(ctx, smartRouterImageSelectionKey{}, &smartRouterImageSelectionState{ProbeBudget: budget})
						req := OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, RequestedModel: "gpt-image-2", SessionHash: "sticky-a", StickyAccountID: accounts[0].ID, StickyWeighted: weighted}
						if explicitImage {
							req.RequiredImageCapability = OpenAIImagesCapabilityBasic
							req.SmartRouterCapability = core.CapabilityImageGeneration
						}
						scheduler := &defaultOpenAIAccountScheduler{service: svc, stats: newOpenAIAccountRuntimeStats()}
						var selection *AccountSelectionResult
						var err error
						if weighted {
							selection, err = scheduler.tryFallbackToWeightedSticky(ctx, req)
						} else {
							selection, _, err = scheduler.selectBySessionHash(ctx, req)
						}
						require.NoError(t, err)
						if smart && explicitImage {
							require.Nil(t, selection)
							require.Empty(t, acquired)
							if exhausted == "database" {
								require.Zero(t, repo.calls(), "sticky must not bypass an exhausted shared DB recheck budget")
							}
						} else {
							require.NotNil(t, selection, "other native paths must ignore the image-only budget")
							require.True(t, selection.Acquired)
							require.Equal(t, []int64{accounts[0].ID}, acquired)
							require.Equal(t, 1, repo.calls())
							selection.ReleaseFunc()
						}
						require.LessOrEqual(t, budget.acquires, openAIAccountSelectionProbeLimit)
						require.LessOrEqual(t, budget.rechecks, openAIAccountSelectionProbeLimit)
					})
				}
			}
		}
	}
}

func TestSmartRouterImageRuntimeBlockRespectsPersistedNativeState(t *testing.T) {
	for _, smart := range []bool{false, true} {
		for _, persisted := range []bool{false, true} {
			t.Run(fmt.Sprintf("smart=%t/persisted=%t", smart, persisted), func(t *testing.T) {
				accounts := imageRecoveryAccounts()
				until := time.Now().Add(time.Hour)
				if persisted {
					accounts[0].TempUnschedulableUntil = &until
				}
				svc := imageRecoveryTestService(t, true, false, accounts, nil)
				svc.cfg.Gateway.SmartRouter.Enabled = smart
				svc.BlockAccountScheduling(&accounts[0], until, "test_native_state")
				require.True(t, svc.isOpenAIAccountRuntimeBlocked(&accounts[0]))
				require.Equal(t, persisted, svc.isOpenAIAccountRequestRuntimeBlocked(&accounts[0], "gpt-image-2"))
				require.Equal(t, persisted, svc.isOpenAIAccountRuntimeBlocked(&accounts[0]), "native fresh persisted state remains authoritative; a stale local-only block is cleared")
			})
		}
	}
}
