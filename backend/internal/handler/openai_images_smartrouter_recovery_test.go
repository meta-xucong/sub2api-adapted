//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type imageRecoveryBindings struct {
	testutil.StubGatewayCache
	mu     sync.Mutex
	owners map[string]int64
}

func (b *imageRecoveryBindings) GetSessionAccountID(_ context.Context, group int64, key string) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id, ok := b.owners[fmt.Sprintf("%d:%s", group, key)]
	if !ok {
		return 0, service.ErrStickySessionNotFound
	}
	return id, nil
}
func (b *imageRecoveryBindings) SetSessionAccountID(_ context.Context, group int64, key string, id int64, _ time.Duration) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.owners[fmt.Sprintf("%d:%s", group, key)] = id
	return nil
}
func (b *imageRecoveryBindings) DeleteSessionAccountID(_ context.Context, group int64, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.owners, fmt.Sprintf("%d:%s", group, key))
	return nil
}
func (*imageRecoveryBindings) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

// This fake exercises the production LoadStates restart boundary. It is not a
// PostgreSQL/repository integration test and never connects to an external DB.
type imageRecoveryFakeLedger struct {
	service.SmartRouterHealthLedger
	mu     sync.Mutex
	states map[core.HealthKey]service.SmartRouterHealthState
	events []core.HealthEvent
	loads  int
}

func (l *imageRecoveryFakeLedger) LoadStates(context.Context) ([]service.SmartRouterHealthState, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loads++
	return l.snapshotLocked(), nil
}
func (*imageRecoveryFakeLedger) ListCapabilityEvidence(context.Context) ([]service.SmartRouterCapabilityEvidence, error) {
	return nil, nil
}
func (l *imageRecoveryFakeLedger) RecordEvent(_ context.Context, e core.HealthEvent) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
	if !e.ShouldPersistStateProjection() {
		return nil
	}
	state := service.SmartRouterHealthState{
		LaneID: e.Key.LaneID, AccountID: e.AccountID, SourceGroup: e.SourceGroup, Capability: e.Key.Capability, ModelFamily: e.Key.Model,
		Snapshot: core.HealthSnapshot{HealthPenalty: e.HealthPenalty, RecoveryPriority: e.RecoveryPriority, HealthScore: e.HealthScore, ErrorRateEWMA: e.ErrorRateEWMA,
			CooldownUntilUnix: e.CooldownUntilUnix, PolicyRejectUntilUnix: e.PolicyRejectUntilUnix, RecoveryStage: e.RecoveryStage,
			ConsecutiveFailures: e.ConsecutiveFailures, ConsecutiveSuccesses: e.ConsecutiveSuccesses},
	}
	previous := l.states[e.Key]
	state.LastFailureUnix, state.LastFailureAt, state.LastSuccessAt = previous.LastFailureUnix, previous.LastFailureAt, previous.LastSuccessAt
	if e.Success {
		state.LastSuccessAt = time.Unix(e.OccurredAtUnix, 0)
	} else {
		state.LastFailureUnix, state.LastFailureAt = e.OccurredAtUnix, time.Unix(e.OccurredAtUnix, 0)
	}
	l.states[e.Key] = state
	return nil
}
func (l *imageRecoveryFakeLedger) snapshotLocked() []service.SmartRouterHealthState {
	states := make([]service.SmartRouterHealthState, 0, len(l.states))
	for _, state := range l.states {
		states = append(states, state)
	}
	return states
}
func (l *imageRecoveryFakeLedger) snapshot() []service.SmartRouterHealthState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.snapshotLocked()
}

type imageRecoveryHandlerFixture struct {
	h        *OpenAIGatewayHandler
	gateway  *service.OpenAIGatewayService
	accounts []service.Account
	repo     *imagePollTimeoutAccountRepo
	slots    *grokMediaSlotsCache
	ledger   *imageRecoveryFakeLedger
	upstream *grokMediaSlotUpstream
	key      *service.APIKey
	calls    []int64
	failA    bool
}

func newImageRecoveryHandlerFixture(t *testing.T, smart, advanced, oauth bool, restored []service.SmartRouterHealthState, wrapSlots ...func(*grokMediaSlotsCache) service.ConcurrencyCache) *imageRecoveryHandlerFixture {
	t.Helper()
	service.ResetOpenAIImageSchedulerSettingsForUnitTest()
	t.Cleanup(service.ResetOpenAIImageSchedulerSettingsForUnitTest)
	f := &imageRecoveryHandlerFixture{failA: true}
	accountType := service.AccountTypeAPIKey
	if oauth {
		accountType = service.AccountTypeOAuth
	}
	f.accounts = make([]service.Account, 2)
	for i := range f.accounts {
		f.accounts[i] = service.Account{ID: int64(i + 1), Name: fmt.Sprintf("mock-image-%d", i+1), Platform: service.PlatformOpenAI, Type: accountType,
			Status: service.StatusActive, Schedulable: true, Concurrency: 2, Priority: i * 10, GroupIDs: []int64{24},
			Credentials: map[string]any{"api_key": "mock-only", "access_token": "mock-only", "chatgpt_account_id": "mock-only", "base_url": "https://images.example/v1"}}
	}
	f.repo = &imagePollTimeoutAccountRepo{openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{accounts: f.accounts}}
	f.slots = &grokMediaSlotsCache{accounts: map[string]int64{}, users: map[string]int64{}}
	var slotCache service.ConcurrencyCache = f.slots
	if len(wrapSlots) > 0 {
		slotCache = wrapSlots[0](f.slots)
	}
	concurrency := service.NewConcurrencyService(slotCache)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Gateway.SmartRouter.Enabled = smart
	cfg.Gateway.ImageConcurrency.Enabled = true
	cfg.Gateway.ImageConcurrency.MaxConcurrentRequests = 1
	cfg.Gateway.ImageGenerationTransientCooldownSeconds = 8
	cfg.Gateway.ImageEditTransientCooldownSeconds = 8
	cfg.Gateway.Scheduling.StickySessionMaxWaiting = 3
	cfg.Gateway.Scheduling.StickySessionWaitTimeout = time.Second
	cfg.Gateway.Scheduling.FallbackMaxWaiting = 3
	cfg.Gateway.Scheduling.FallbackWaitTimeout = time.Second
	// Explicit nonzero thresholds keep the disabled escape setting from being
	// interpreted as an unconfigured default. Busy sticky A must enter the queue.
	cfg.Gateway.OpenAIScheduler.StickyEscapeEnabled = false
	cfg.Gateway.OpenAIScheduler.StickyEscapeTTFTMs = 15000
	cfg.Gateway.OpenAIScheduler.StickyEscapeErrorRate = 0.5
	rateLimit := service.NewRateLimitService(f.repo, nil, cfg, nil, nil)
	rateLimit.SetSettingService(service.NewSettingService(imageBoundarySettings{enabled: advanced}, cfg))
	upstream := &grokMediaSlotUpstream{call: func(req *http.Request, accountID int64) (*http.Response, error) {
		f.calls = append(f.calls, accountID)
		require.Contains(t, req.URL.Path, "/images/", "all transport is a direct in-memory image mock")
		status, body := http.StatusOK, `{"created":1,"data":[{"b64_json":"`+imagePollTimeoutTestImage+`"}],"usage":{"input_tokens":7,"output_tokens":3}}`
		if accountID == 1 && f.failA {
			status, body = http.StatusBadRequest, `{"error":{"type":"server_error","code":"image_poll_timeout","message":"poll timed out"}}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}}
	f.upstream = upstream
	logs, charges := &grokRoutePricingCountingUsageLogRepo{}, &grokRoutePricingCountingUsageBillingRepo{}
	billing := service.NewBillingService(cfg, nil)
	bindings := &imageRecoveryBindings{owners: map[string]int64{}}
	f.gateway = service.NewOpenAIGatewayService(f.repo, logs, charges, nil, nil, nil, bindings, cfg, nil, concurrency, billing, rateLimit, nil, upstream, &service.DeferredService{}, nil, nil, service.NewModelPricingResolver(nil, billing), nil, nil, nil, nil)
	f.ledger = &imageRecoveryFakeLedger{states: map[core.HealthKey]service.SmartRouterHealthState{}}
	for _, state := range restored {
		f.ledger.states[core.NewHealthKey(state.LaneID, state.Capability, state.ModelFamily)] = state
	}
	f.gateway.SetSmartRouterHealthLedger(f.ledger)
	cache := service.NewBillingCacheService(grokRoutePricingBalanceOnlyCache{}, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(cache.Stop)
	f.h = NewOpenAIGatewayHandler(f.gateway, concurrency, cache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), newUsageRecordTestPool(t), nil, nil, nil, cfg)
	f.h.maxAccountSwitches = 2
	groupID, price := int64(24), 0.25
	f.key = &service.APIKey{ID: 20, UserID: 10, GroupID: &groupID, User: &service.User{ID: 10, Balance: 100}, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, AllowImageGeneration: true, RateMultiplier: 1,
		ModelPricing: []service.ChannelModelPricing{{Models: []string{"gpt-image-2"}, BillingMode: service.BillingModePerRequest, PerRequestPrice: &price}}}}
	return f
}

func imageRecoveryParsed(edit bool) *service.OpenAIImagesRequest {
	parsed := &service.OpenAIImagesRequest{Model: "gpt-image-2", Endpoint: "/v1/images/generations"}
	if edit {
		parsed.Endpoint = "/v1/images/edits"
	}
	return parsed
}

func (f *imageRecoveryHandlerFixture) request(t *testing.T, edit bool) *httptest.ResponseRecorder {
	t.Helper()
	endpoint, body := "/v1/images/generations", `{"model":"gpt-image-2","prompt":"draw"}`
	if edit {
		endpoint, body = "/v1/images/edits", `{"model":"gpt-image-2","prompt":"draw","images":[{"image_url":"data:image/png;base64,`+imagePollTimeoutTestImage+`"}]}`
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("session-id", "fresh-image-request-sticky-control")
	c.Set(string(middleware.ContextKeyAPIKey), f.key)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 10, Concurrency: 2})
	// Force the failed account back into the persistent sticky binding before
	// each request. Every Images invocation creates its own exclusion map.
	hash := f.gateway.GenerateExplicitSessionHash(c, []byte(body))
	require.NotEmpty(t, hash)
	require.NoError(t, f.gateway.BindStickySession(c.Request.Context(), f.key.GroupID, hash, 1))
	f.h.Images(c)
	f.slots.assertReleased(t)
	f.h.imageLimiter.mu.Lock()
	active, waiting := f.h.imageLimiter.active, f.h.imageLimiter.waiting
	f.h.imageLimiter.mu.Unlock()
	require.Zero(t, active)
	require.Zero(t, waiting)
	require.Zero(t, f.repo.tempUnscheduleCalls, "poll timeout must never block shared account capacity")
	return recorder
}

func (f *imageRecoveryHandlerFixture) markCooling(edit bool, accountIndex int) {
	parsed := imageRecoveryParsed(edit)
	ctx := service.WithSmartRouterImageRequest(context.Background(), parsed.Model, edit)
	f.gateway.ReportSmartRouterImageResult(ctx, &f.accounts[accountIndex], parsed, nil, &service.UpstreamFailoverError{
		StatusCode: http.StatusBadRequest, ResponseBody: []byte(`{"error":{"code":"image_poll_timeout"}}`),
	}, 30)
}

func (f *imageRecoveryHandlerFixture) assertTextAndOtherModelSelectable(t *testing.T, edit bool) {
	t.Helper()
	ctx, hash := context.Background(), "fresh-text-control"
	require.NoError(t, f.gateway.BindStickySession(ctx, f.key.GroupID, hash, 1))
	selection, _, err := f.gateway.SelectAccountWithSchedulerForCapability(ctx, f.key.GroupID, "", hash, "gpt-5.5", nil, service.OpenAIUpstreamTransportAny, service.OpenAIEndpointCapabilityResponses, false, true, true)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(1), selection.Account.ID, "actual ordinary text selection still admits the image-failed account")
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	ctx = service.WithSmartRouterImageRequest(context.Background(), "gpt-image-1", edit)
	selection, _, err = f.gateway.SelectAccountWithSchedulerForImageOperation(ctx, f.key.GroupID, hash, "gpt-image-1", nil, service.OpenAIImagesCapabilityBasic, edit)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(1), selection.Account.ID, "image cooldown is exact-model scoped")
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	f.slots.assertReleased(t)
}

func TestImagesSmartRouterFreshRequestsStickyAndFakeLedgerRestore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, smart := range []bool{false, true} {
		for _, advanced := range []bool{false, true} {
			for _, oauth := range []bool{false, true} {
				for _, edit := range []bool{false, true} {
					t.Run(fmt.Sprintf("smart=%t/advanced=%t/oauth=%t/edit=%t", smart, advanced, oauth, edit), func(t *testing.T) {
						f := newImageRecoveryHandlerFixture(t, smart, advanced, oauth, nil)
						r := f.request(t, edit)
						require.Equal(t, http.StatusOK, r.Code, r.Body.String())
						require.Contains(t, r.Body.String(), imagePollTimeoutTestImage)
						require.Equal(t, []int64{1, 2}, f.calls)
						before := len(f.calls)
						r = f.request(t, edit)
						require.Equal(t, http.StatusOK, r.Code, r.Body.String())
						want := []int64{1, 2}
						if smart {
							want = []int64{2}
						}
						require.Equal(t, want, f.calls[before:], "a separate request must consult cross-request health even when sticky is reset to A")
						f.assertTextAndOtherModelSelectable(t, edit)
						// The opposite image operation uses the same account and model.
						// Success here proves capability isolation through the real handler.
						f.failA, before = false, len(f.calls)
						r = f.request(t, !edit)
						require.Equal(t, http.StatusOK, r.Code, r.Body.String())
						require.Equal(t, []int64{1}, f.calls[before:])
						if !smart {
							require.Empty(t, f.ledger.snapshot())
							return
						}
						states := f.ledger.snapshot()
						capability := core.CapabilityImageGeneration
						if edit {
							capability = core.CapabilityImageEdit
						}
						found := false
						for _, state := range states {
							if state.AccountID == 1 && state.Capability == capability {
								found = true
								require.Equal(t, "gpt-image-2", state.ModelFamily)
								require.Greater(t, state.Snapshot.CooldownUntilUnix, time.Now().Unix())
							}
						}
						require.True(t, found, "failed lane projection must exist for its exact capability and model")
						restarted := newImageRecoveryHandlerFixture(t, smart, advanced, oauth, states)
						r = restarted.request(t, edit)
						require.Equal(t, http.StatusOK, r.Code, r.Body.String())
						require.Equal(t, []int64{2}, restarted.calls, "a fresh service restores the fake durable projection before sticky selection")
						require.Equal(t, 1, restarted.ledger.loads)
					})
				}
			}
		}
	}
}

func TestImagesSmartRouterAllCoolingAndPostQueueRecheck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, smart := range []bool{false, true} {
		for _, advanced := range []bool{false, true} {
			for _, edit := range []bool{false, true} {
				for _, queued := range []bool{false, true} {
					t.Run(fmt.Sprintf("smart=%t/advanced=%t/edit=%t/queued=%t", smart, advanced, edit, queued), func(t *testing.T) {
						f := newImageRecoveryHandlerFixture(t, smart, advanced, false, nil)
						f.failA = false
						waits := 0
						if queued {
							// Scheduler and handler each try one fast acquisition
							// before the handler increments the queue counter.
							f.slots.denied = 2
							f.slots.onWait = func() {
								waits++
								f.markCooling(edit, 0)
								f.slots.mu.Lock()
								f.slots.onWait = nil
								f.slots.mu.Unlock()
							}
						} else {
							f.markCooling(edit, 0)
							f.markCooling(edit, 1)
						}
						r := f.request(t, edit)
						require.Equal(t, http.StatusOK, r.Code, r.Body.String())
						want := int64(1)
						if smart && queued {
							want = 2
						}
						require.Equal(t, []int64{want}, f.calls, "healthy B is preferred after A cools in queue; with no usable alternative the intentional last-resort A attempt remains available")
						if queued {
							require.Equal(t, 1, waits, "cooldown is introduced after account selection, inside the slot queue")
						}
					})
				}
			}
		}
	}
}

// Every account first enters the real handler wait path. The queue callback
// models another request cooling that exact account before this one dispatches.
type imageRecoveryQueuedCoolingSlots struct {
	*grokMediaSlotsCache
	queued  map[int64]bool
	onQueue func(int64)
}

func (s *imageRecoveryQueuedCoolingSlots) AcquireAccountSlot(ctx context.Context, id int64, limit int, request string) (bool, error) {
	if !s.queued[id] {
		return false, nil
	}
	return s.grokMediaSlotsCache.AcquireAccountSlot(ctx, id, limit, request)
}

func (s *imageRecoveryQueuedCoolingSlots) IncrementAccountWaitCount(ctx context.Context, id int64, limit int) (bool, error) {
	allowed, err := s.grokMediaSlotsCache.IncrementAccountWaitCount(ctx, id, limit)
	if allowed && err == nil {
		s.queued[id] = true
		s.onQueue(id)
	}
	return allowed, err
}

func TestImagesSmartRouterQueuedCoolingExclusionsRemainRequestLocal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, advanced := range []bool{false, true} {
		for _, edit := range []bool{false, true} {
			for _, count := range []int{1, 2} {
				t.Run(fmt.Sprintf("advanced=%t/edit=%t/accounts=%d", advanced, edit, count), func(t *testing.T) {
					var slots *imageRecoveryQueuedCoolingSlots
					f := newImageRecoveryHandlerFixture(t, true, advanced, false, nil, func(base *grokMediaSlotsCache) service.ConcurrencyCache {
						slots = &imageRecoveryQueuedCoolingSlots{grokMediaSlotsCache: base, queued: map[int64]bool{}}
						return slots
					})
					f.accounts = f.accounts[:count]
					f.repo.accounts = f.accounts
					f.failA = false
					slots.onQueue = func(id int64) { f.markCooling(edit, int(id)-1) }
					r := f.request(t, edit)
					require.Equal(t, http.StatusServiceUnavailable, r.Code, r.Body.String())
					require.Len(t, slots.queued, count)
					require.Empty(t, f.calls, "every admission-denied account remains excluded for this request, including LastResort")
					require.Len(t, f.ledger.events, count, "queue rejection itself must not create an additional health failure")

					r = f.request(t, edit)
					require.Equal(t, http.StatusOK, r.Code, r.Body.String())
					require.Equal(t, []int64{1}, f.calls, "a new request may explicitly LastResort the still-cooling account")
				})
			}
		}
	}
}

func TestImagesSmartRouterLastResortExclusionsAndLeaseContention(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, advanced := range []bool{false, true} {
		for _, edit := range []bool{false, true} {
			for _, scenario := range []string{"all_attempts_timeout", "one_local_lease_held", "all_local_leases_held"} {
				t.Run(fmt.Sprintf("advanced=%t/edit=%t/%s", advanced, edit, scenario), func(t *testing.T) {
					f := newImageRecoveryHandlerFixture(t, true, advanced, false, nil)
					f.markCooling(edit, 0)
					f.markCooling(edit, 1)
					if scenario == "all_attempts_timeout" {
						f.upstream.call = func(_ *http.Request, id int64) (*http.Response, error) {
							f.calls = append(f.calls, id)
							return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": {"application/json"}},
								Body: io.NopCloser(strings.NewReader(`{"error":{"code":"image_poll_timeout","message":"poll timed out"}}`))}, nil
						}
						r := f.request(t, edit)
						require.Equal(t, http.StatusBadGateway, r.Code, r.Body.String())
						require.Equal(t, []int64{1, 2}, f.calls, "last-resort passes retain request exclusions; neither timed-out account may repeat")
						return
					}
					// Leases are process-local and exact account/capability/model
					// scoped. Hold both keys, rather than claiming a pool-global
					// or distributed semaphore that production does not implement.
					parsed := imageRecoveryParsed(edit)
					ctx := service.WithSmartRouterImageRequest(context.Background(), parsed.Model, edit)
					capability := core.CapabilityImageGeneration
					if edit {
						capability = core.CapabilityImageEdit
					}
					var releases []func()
					for i := range f.accounts {
						if scenario == "one_local_lease_held" && i == 1 {
							break
						}
						_, release, err := f.gateway.AcquireSmartRouterImageAttempt(ctx, &f.accounts[i], parsed, true)
						require.NoError(t, err)
						t.Cleanup(release)
						releases = append(releases, release)
						snapshot := f.gateway.OpenAIStreamSchedulingUnitSnapshot(&f.accounts[i], capability, parsed.Model, time.Now())
						require.Greater(t, snapshot.Health.CooldownUntilUnix, time.Now().Unix(), "forced admission must not erase cooling state")
					}
					r := f.request(t, edit)
					if scenario == "one_local_lease_held" {
						require.Equal(t, http.StatusOK, r.Code, r.Body.String())
						require.Equal(t, []int64{2}, f.calls, "occupied forced A scope uses the other eligible lane without a duplicate A probe")
						return
					}
					require.Equal(t, http.StatusServiceUnavailable, r.Code, r.Body.String())
					retry, err := strconv.Atoi(r.Header().Get("Retry-After"))
					require.NoError(t, err)
					require.Positive(t, retry)
					require.Empty(t, f.calls, "no additional upstream dispatch while both exact local probe leases are held")
					for _, release := range releases {
						release()
					}
					f.failA = false
					r = f.request(t, edit)
					require.Equal(t, http.StatusOK, r.Code, r.Body.String())
					require.Equal(t, []int64{1}, f.calls, "released lease permits the bounded all-cooling last-resort attempt")
				})
			}
		}
	}
}

func TestImagesSmartRouterHealthyWaitPlanPrecedesLastResort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, smart := range []bool{false, true} {
		for _, advanced := range []bool{false, true} {
			for _, edit := range []bool{false, true} {
				t.Run(fmt.Sprintf("smart=%t/advanced=%t/edit=%t", smart, advanced, edit), func(t *testing.T) {
					f := newImageRecoveryHandlerFixture(t, smart, advanced, false, nil)
					f.failA = false
					f.markCooling(edit, 0)
					// Native/basic scheduler passes may each retry a fast slot.
					// Keep it full until the actual queue boundary instead of
					// assuming a fixed number of prequeue acquisitions.
					f.slots.full = true
					waits := 0
					f.slots.onWait = func() {
						waits++
						f.slots.mu.Lock()
						f.slots.full = false
						f.slots.mu.Unlock()
					}
					r := f.request(t, edit)
					require.Equal(t, http.StatusOK, r.Code, r.Body.String())
					require.Equal(t, 1, waits, "use the bounded normal WaitPlan rather than forcing a cooling lane; dispatched=%v", f.calls)
					want := int64(1)
					if smart {
						want = 2
					}
					require.Equal(t, []int64{want}, f.calls, "Smart Router waits for healthy B; disabled mode retains sticky A")
				})
			}
		}
	}
}

// Explicit account-native Responses forwarding keeps its existing HTTP error
// behavior. Parsing an image bridge request only adds scoped admission and one
// image observation; it does not reclassify native Forward's ordinary HTTP400.
func TestImagesSmartRouterParsedNativeResponsesBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, smart := range []bool{false, true} {
		for _, advanced := range []bool{false, true} {
			for _, seeded := range []bool{false, true} {
				t.Run(fmt.Sprintf("smart=%t/advanced=%t/cooling=%t", smart, advanced, seeded), func(t *testing.T) {
					f := newImageRecoveryHandlerFixture(t, smart, advanced, false, nil)
					f.h.cfg.Gateway.ResponsesImageBridge = config.ResponsesImageBridgeConfig{Enabled: true, ApplyToProtocol: "images_api_only"}
					for i := range f.repo.accounts {
						f.repo.accounts[i].Extra = map[string]any{"responses_image_mode": "native"}
					}
					if seeded {
						f.markCooling(false, 0)
					}
					before := len(f.ledger.events)
					f.upstream.call = func(req *http.Request, accountID int64) (*http.Response, error) {
						f.calls = append(f.calls, accountID)
						require.True(t, strings.HasSuffix(req.URL.Path, "/responses"), "account-native mode must not dispatch the Images bridge")
						forwarded, err := io.ReadAll(req.Body)
						require.NoError(t, err)
						require.Contains(t, string(forwarded), `"image_generation"`)
						return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": {"application/json"}},
							Body: io.NopCloser(strings.NewReader(`{"error":{"code":"image_poll_timeout","message":"poll timed out"}}`))}, nil
					}
					body := []byte(`{"model":"gpt-5.5","input":"draw","tools":[{"type":"image_generation","model":"gpt-image-2"}]}`)
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
					c.Request.Header.Set("Content-Type", "application/json")
					c.Request.Header.Set("session-id", "native-responses-image-control")
					c.Set(string(middleware.ContextKeyAPIKey), f.key)
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 10, Concurrency: 2})
					hash := f.gateway.GenerateExplicitSessionHash(c, body)
					require.NoError(t, f.gateway.BindStickySession(c.Request.Context(), f.key.GroupID, hash, 1))
					f.h.Responses(c)
					require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
					require.Contains(t, recorder.Body.String(), "poll timed out")
					want := int64(1)
					if smart && seeded {
						want = 2
					}
					require.Equal(t, []int64{want}, f.calls, "native HTTP400 is terminal; scoped cooling only changes pre-dispatch admission")
					f.slots.assertReleased(t)
					if smart {
						require.Len(t, f.ledger.events, before+1, "native parsed image outcome is observed once")
						event := f.ledger.events[before]
						require.Equal(t, want, event.AccountID)
						require.Equal(t, core.CapabilityImageGeneration, event.Key.Capability)
						require.Equal(t, "gpt-image-2", event.Key.Model, "admission and observation use the translated image model")
					} else {
						require.Empty(t, f.ledger.events)
					}
				})
			}
		}
	}
}

func TestImagesSmartRouterPostQueueRequestDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, smart := range []bool{false, true} {
		for _, advanced := range []bool{false, true} {
			for _, scenario := range []string{"images_absolute_deadline_before_timer", "responses_absolute_deadline_before_timer", "images_internal_budget"} {
				t.Run(fmt.Sprintf("smart=%t/advanced=%t/%s", smart, advanced, scenario), func(t *testing.T) {
					f := newImageRecoveryHandlerFixture(t, smart, advanced, false, nil)
					f.h.cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60, DefaultMaxTokens: 10}
					reservation := &grokRoutePricingCountingReservationCache{handlerInflightCache: newHandlerInflightCache(100)}
					billingCache := service.NewBillingCacheService(reservation, nil, nil, nil, nil, nil, f.h.cfg, nil)
					t.Cleanup(billingCache.Stop)
					f.h.billingCacheService = billingCache
					f.key.Group.ModelPricing[0].Models = []string{"gpt-image-2", "gpt-5.5"}
					f.failA = false
					f.slots.denied = 2
					deadline := &imageBoundaryDeadlineContext{Context: context.Background()}
					var requestCtx context.Context = deadline
					if scenario == "images_internal_budget" {
						requestCtx = context.Background()
						f.h.cfg.Gateway.ImageRequestTimeoutSeconds = 1
					}
					waits := 0
					f.slots.onWait = func() {
						waits++
						require.Equal(t, 1, reservation.count(), "request reservation is held while queued")
						if scenario == "images_internal_budget" {
							// The request-owned timer is created before selection;
							// its budget expires while the caller stays connected.
							timer := time.NewTimer(1100 * time.Millisecond)
							defer timer.Stop()
							<-timer.C
						} else {
							// Model an expired absolute deadline before Err() is
							// published. The slot wait still sees a live caller.
							deadline.expired.Store(true)
						}
					}
					path, body := "/v1/images/generations", `{"model":"gpt-image-2","prompt":"draw"}`
					responses := strings.HasPrefix(scenario, "responses_")
					if responses {
						path, body = "/v1/responses", `{"model":"gpt-5.5","input":"draw","tools":[{"type":"image_generation","model":"gpt-image-2"}]}`
						f.h.cfg.Gateway.ResponsesImageBridge = config.ResponsesImageBridgeConfig{Enabled: true, ApplyToProtocol: "images_api_only"}
						for i := range f.repo.accounts {
							f.repo.accounts[i].Extra = map[string]any{"responses_image_mode": "native"}
						}
					}
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)).WithContext(requestCtx)
					c.Request.Header.Set("Content-Type", "application/json")
					c.Request.Header.Set("session-id", "image-postqueue-deadline-control")
					c.Set(string(middleware.ContextKeyAPIKey), f.key)
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 10, Concurrency: 2})
					hash := f.gateway.GenerateExplicitSessionHash(c, []byte(body))
					require.NoError(t, f.gateway.BindStickySession(c.Request.Context(), f.key.GroupID, hash, 1))
					if responses {
						f.h.Responses(c)
					} else {
						f.h.Images(c)
					}
					require.Equal(t, 1, waits, "expiry must occur inside the queue, after initial selection")
					require.NoError(t, requestCtx.Err(), "caller has not disconnected or published a timer error")
					require.Equal(t, http.StatusBadGateway, recorder.Code, recorder.Body.String(), "preserve existing mapUpstreamError(504) public status")
					require.Equal(t, http.StatusGatewayTimeout, c.GetInt(service.OpsUpstreamStatusCodeKey), "operations retain the request-budget504 cause")
					var payload struct {
						Error struct {
							Type    string `json:"type"`
							Message string `json:"message"`
						} `json:"error"`
					}
					require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
					require.Equal(t, "upstream_error", payload.Error.Type)
					require.NotEmpty(t, payload.Error.Message)
					require.Empty(t, f.calls)
					require.Empty(t, f.ledger.events, "request-budget expiry must not penalize image lane health")
					require.Zero(t, f.repo.tempUnscheduleCalls)
					require.Equal(t, 1, reservation.attempts)
					require.Zero(t, reservation.count(), "deadline exit releases its single reservation")
					f.slots.assertReleased(t)
					f.h.imageLimiter.mu.Lock()
					active, waiting := f.h.imageLimiter.active, f.h.imageLimiter.waiting
					f.h.imageLimiter.mu.Unlock()
					require.Zero(t, active)
					require.Zero(t, waiting)
				})
			}
		}
	}
}
