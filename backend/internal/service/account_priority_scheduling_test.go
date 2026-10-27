package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type accountPrioritySchedulingTestRepo struct {
	AccountRepository
	account       *Account
	accounts      []Account
	listErr       error
	errOnListCall int
	listCalls     int
}

func (r *accountPrioritySchedulingTestRepo) Create(_ context.Context, account *Account) error {
	copy := *account
	copy.ID = 1
	r.account = &copy
	account.ID = copy.ID
	return nil
}

func (r *accountPrioritySchedulingTestRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	if r.account != nil && r.account.ID == id {
		copy := *r.account
		return &copy, nil
	}
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			copy := r.accounts[i]
			return &copy, nil
		}
	}
	return nil, nil
}

func (r *accountPrioritySchedulingTestRepo) Update(_ context.Context, account *Account) error {
	copy := *account
	r.account = &copy
	return nil
}

func (r *accountPrioritySchedulingTestRepo) ListSchedulableUngroupedByPlatforms(_ context.Context, platforms []string) ([]Account, error) {
	r.listCalls++
	if r.listErr != nil && r.listCalls == r.errOnListCall {
		return nil, r.listErr
	}
	allowed := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		allowed[platform] = struct{}{}
	}
	accounts := make([]Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if _, ok := allowed[account.Platform]; ok && len(account.AccountGroups) == 0 {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

func (r *accountPrioritySchedulingTestRepo) ListSchedulableUngroupedByPlatform(_ context.Context, platform string) ([]Account, error) {
	r.listCalls++
	if r.listErr != nil && r.listCalls == r.errOnListCall {
		return nil, r.listErr
	}
	accounts := make([]Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if account.Platform == platform && len(account.AccountGroups) == 0 {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

type prioritySchedulingSlotCache struct {
	ConcurrencyCache
	acquireOrder []int64
	available    map[int64]bool
}

func (c *prioritySchedulingSlotCache) AcquireAccountSlot(_ context.Context, accountID int64, _ int, _ string) (bool, error) {
	c.acquireOrder = append(c.acquireOrder, accountID)
	return c.available[accountID], nil
}

func (c *prioritySchedulingSlotCache) ReleaseAccountSlot(context.Context, int64, string) error {
	return nil
}

func TestPrioritySchedulingPoolsPreserveNativeOrder(t *testing.T) {
	accounts := []*Account{
		{ID: 1},
		{ID: 2, PriorityScheduling: true},
		{ID: 3},
		{ID: 4, PriorityScheduling: true},
	}

	preferred, regular := splitPrioritySchedulingAccounts(accounts, true)
	require.Equal(t, []int64{2, 4}, priorityTestAccountIDs(preferred))
	require.Equal(t, []int64{1, 3}, priorityTestAccountIDs(regular))
	require.Equal(t, []int64{2, 4}, priorityTestAccountIDs(prioritySchedulingCandidateSubset(accounts, true)))
	require.Equal(t, []int64{1, 2, 3, 4}, priorityTestAccountIDs(prioritySchedulingCandidateSubset(accounts, false)))
}

func TestPrioritySchedulingReservesSharedOpenAIProbeBudgetForFallbackPools(t *testing.T) {
	budget := newOpenAISelectionProbeBudget()
	budget.enableLimit()

	reserveOpenAIPriorityPoolProbeBudget(budget, 1)
	for i := 0; i < openAIAccountSelectionProbeLimit-1; i++ {
		require.True(t, budget.recordAcquire(int64(i+1)))
	}
	require.False(t, budget.recordAcquire(999), "preferred pool must leave one shared probe for a later ordinary pool")

	reserveOpenAIPriorityPoolProbeBudget(budget, 0)
	require.True(t, budget.recordAcquire(999), "ordinary fallback can consume the reserved probe")
	require.Equal(t, openAIAccountSelectionProbeLimit, budget.acquires, "priority scheduling must not double the native probe budget")
}

func TestPrioritySchedulingKeepsOnlyUsableStickyBindingsOutsidePoolSelection(t *testing.T) {
	groupID := int64(7)
	account := &Account{
		ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive,
		Schedulable: true, AccountGroups: []AccountGroup{{GroupID: groupID}},
	}
	service := &GatewayService{accountRepo: &accountPrioritySchedulingTestRepo{account: account}}
	group := &Group{ID: groupID, Platform: PlatformAnthropic}

	require.True(t, service.hasUsablePriorityStickyBinding(context.Background(), group, &groupID, account.ID, "", nil))
	require.False(t, service.hasUsablePriorityStickyBinding(context.Background(), group, &groupID, account.ID, "", map[int64]struct{}{account.ID: {}}))
	require.False(t, service.hasUsablePriorityStickyBinding(context.Background(), group, &groupID, 99, "", nil))

	ungrouped := &Account{ID: 2, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	service.accountRepo = &accountPrioritySchedulingTestRepo{account: ungrouped}
	require.True(t, service.hasUsablePriorityStickyBinding(context.Background(), nil, nil, ungrouped.ID, "", nil))

	composite := &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, AccountGroups: []AccountGroup{{GroupID: groupID}}}
	service.accountRepo = &accountPrioritySchedulingTestRepo{account: composite}
	compositeGroup := &Group{ID: groupID, Platform: PlatformComposite}
	compositeCtx := WithResolvedTargetPlatform(context.Background(), PlatformOpenAI)
	require.True(t, service.hasUsablePriorityStickyBinding(compositeCtx, compositeGroup, &groupID, composite.ID, "", nil))
}

func TestGenericNoLoadPriorityPoolTriesPreferredThenAvailableRegularAccount(t *testing.T) {
	preferred := Account{ID: 11, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 10, PriorityScheduling: true, Concurrency: 1}
	regular := Account{ID: 12, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 0, Concurrency: 1}
	repo := &accountPrioritySchedulingTestRepo{accounts: []Account{regular, preferred}}
	cache := &prioritySchedulingSlotCache{available: map[int64]bool{preferred.ID: false, regular.ID: true}}
	service := &GatewayService{
		accountRepo: repo, concurrencyService: NewConcurrencyService(cache),
		cfg: &config.Config{},
	}

	selection, err := service.selectFromPriorityPoolWithoutLoadAwareness(context.Background(), nil, "", "", nil, service.schedulingConfig())
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, regular.ID, selection.Account.ID)
	require.Equal(t, []int64{preferred.ID, regular.ID}, cache.acquireOrder)
	selection.ReleaseFunc()
}

func TestGenericNoLoadPriorityPoolPropagatesSelectorErrors(t *testing.T) {
	selectorErr := errors.New("selector query failed")
	preferred := Account{ID: 13, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, PriorityScheduling: true, Concurrency: 1}
	regular := Account{ID: 14, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1}
	repo := &accountPrioritySchedulingTestRepo{accounts: []Account{preferred, regular}, listErr: selectorErr, errOnListCall: 2}
	service := &GatewayService{accountRepo: repo, concurrencyService: NewConcurrencyService(&prioritySchedulingSlotCache{})}

	selection, err := service.selectFromPriorityPoolWithoutLoadAwareness(context.Background(), nil, "", "", nil, service.schedulingConfig())
	require.Nil(t, selection)
	require.ErrorIs(t, err, selectorErr)
}

func TestGenericLoadAwarePriorityPoolFallsBackToRegularAccount(t *testing.T) {
	for _, tt := range []struct {
		name           string
		acquireResults map[int64]bool
		wantAcquired   bool
	}{
		{
			name:           "regular account acquires after preferred slot is full",
			acquireResults: map[int64]bool{31: false, 32: true},
			wantAcquired:   true,
		},
		{
			name:           "regular account wait precedes preferred wait",
			acquireResults: map[int64]bool{31: false, 32: false},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			storeAccountPrioritySchedulingEnabled(true)
			t.Cleanup(func() { storeAccountPrioritySchedulingEnabled(false) })

			preferred := Account{ID: 31, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 10, PriorityScheduling: true, Concurrency: 1}
			regular := Account{ID: 32, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 0, Concurrency: 1}
			accountRepo := &accountPrioritySchedulingTestRepo{accounts: []Account{regular, preferred}}
			acquired := []int64{}
			cache := schedulerTestConcurrencyCache{
				acquireResults:  tt.acquireResults,
				acquiredIDs:     &acquired,
				skipDefaultLoad: true,
				loadMap: map[int64]*AccountLoadInfo{
					preferred.ID: {AccountID: preferred.ID},
					regular.ID:   {AccountID: regular.ID},
				},
			}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			cfg.Gateway.Scheduling.LoadBatchEnabled = true
			service := &GatewayService{
				accountRepo:        accountRepo,
				concurrencyService: NewConcurrencyService(cache),
				settingService:     &SettingService{},
				cfg:                cfg,
			}

			ctx := WithResolvedTargetPlatform(context.Background(), PlatformAnthropic)
			selection, err := service.SelectAccountWithLoadAwareness(ctx, nil, "", "", nil, "", 0)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.Equal(t, regular.ID, selection.Account.ID)
			require.Equal(t, []int64{preferred.ID, regular.ID}, acquired)
			if tt.wantAcquired {
				require.True(t, selection.Acquired)
				selection.ReleaseFunc()
			} else {
				require.False(t, selection.Acquired)
				require.NotNil(t, selection.WaitPlan)
				require.Equal(t, regular.ID, selection.WaitPlan.AccountID)
			}
		})
	}
}

func TestOpenAILegacyNoLoadPriorityPoolTriesPreferredThenAvailableRegularAccount(t *testing.T) {
	preferred := Account{ID: 21, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 10, PriorityScheduling: true, Concurrency: 1}
	regular := Account{ID: 22, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 0, Concurrency: 1}
	repo := &accountPrioritySchedulingTestRepo{accounts: []Account{regular, preferred}}
	cache := &prioritySchedulingSlotCache{available: map[int64]bool{preferred.ID: false, regular.ID: true}}
	service := &OpenAIGatewayService{
		accountRepo: repo, concurrencyService: NewConcurrencyService(cache),
		cfg: &config.Config{},
	}

	selection, err := service.selectOpenAIFromPriorityPoolWithoutLoadAwareness(context.Background(), nil, PlatformOpenAI, "", "", nil, false, "", false, service.schedulingConfig())
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, regular.ID, selection.Account.ID)
	require.Equal(t, []int64{preferred.ID, regular.ID}, cache.acquireOrder)
	selection.ReleaseFunc()
}

func TestOpenAILegacyNoLoadPriorityPoolPropagatesSelectorErrors(t *testing.T) {
	selectorErr := errors.New("selector query failed")
	preferred := Account{ID: 23, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, PriorityScheduling: true, Concurrency: 1}
	regular := Account{ID: 24, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1}
	repo := &accountPrioritySchedulingTestRepo{accounts: []Account{preferred, regular}, listErr: selectorErr, errOnListCall: 2}
	service := &OpenAIGatewayService{accountRepo: repo, concurrencyService: NewConcurrencyService(&prioritySchedulingSlotCache{})}

	selection, err := service.selectOpenAIFromPriorityPoolWithoutLoadAwareness(context.Background(), nil, PlatformOpenAI, "", "", nil, false, "", false, service.schedulingConfig())
	require.Nil(t, selection)
	require.ErrorIs(t, err, selectorErr)
}

func TestOpenAIAdvancedPriorityPoolFallsBackToRegularAccount(t *testing.T) {
	for _, tt := range []struct {
		name           string
		acquireResults map[int64]bool
		wantAcquired   bool
	}{
		{
			name:           "regular account acquires after preferred pool is exhausted",
			acquireResults: map[int64]bool{41: false, 42: true},
			wantAcquired:   true,
		},
		{
			name:           "regular wait precedes preferred wait",
			acquireResults: map[int64]bool{41: false, 42: false},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			preferred := Account{ID: 41, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 10, PriorityScheduling: true, Concurrency: 1}
			regular := Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 0, Concurrency: 1}
			acquired := []int64{}
			cache := schedulerTestConcurrencyCache{
				acquireResults:  tt.acquireResults,
				acquiredIDs:     &acquired,
				skipDefaultLoad: true,
				loadMap: map[int64]*AccountLoadInfo{
					preferred.ID: {AccountID: preferred.ID},
					regular.ID:   {AccountID: regular.ID},
				},
			}
			service := &OpenAIGatewayService{
				accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{regular, preferred}},
				concurrencyService: NewConcurrencyService(cache),
				cfg:                &config.Config{RunMode: config.RunModeStandard},
			}
			scheduler := &defaultOpenAIAccountScheduler{service: service}

			selection, _, _, _, err := scheduler.selectByLoadBalance(context.Background(), OpenAIAccountScheduleRequest{
				Platform: PlatformOpenAI, AccountPrioritySchedulingEnabled: true,
			})
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.Equal(t, regular.ID, selection.Account.ID)
			require.NotEmpty(t, acquired)
			require.Equal(t, preferred.ID, acquired[0])
			require.Contains(t, acquired, regular.ID)
			if tt.wantAcquired {
				require.True(t, selection.Acquired)
				selection.ReleaseFunc()
			} else {
				require.False(t, selection.Acquired)
				require.NotNil(t, selection.WaitPlan)
				require.Equal(t, regular.ID, selection.WaitPlan.AccountID)
			}
		})
	}
}

func TestAccountPrioritySchedulingSettingDefaultsOff(t *testing.T) {
	settings := (&SettingService{cfg: &config.Config{Default: config.DefaultConfig{UserConcurrency: 1}}}).parseSettings(nil)
	require.False(t, settings.AccountPrioritySchedulingEnabled)
	require.False(t, accountPrioritySchedulingEnabled(context.Background(), nil))
}

func TestAccountPrioritySchedulingCreateAndUpdateRoundTrip(t *testing.T) {
	repo := &accountPrioritySchedulingTestRepo{}
	service := NewAccountService(repo, nil)
	created, err := service.Create(context.Background(), CreateAccountRequest{
		Name: "priority-test", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{},
	})
	require.NoError(t, err)
	assert.False(t, created.PriorityScheduling)

	enabled := true
	updated, err := service.Update(context.Background(), created.ID, UpdateAccountRequest{PriorityScheduling: &enabled})
	require.NoError(t, err)
	assert.True(t, updated.PriorityScheduling)
}

func priorityTestAccountIDs(accounts []*Account) []int64 {
	ids := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		ids = append(ids, account.ID)
	}
	return ids
}
