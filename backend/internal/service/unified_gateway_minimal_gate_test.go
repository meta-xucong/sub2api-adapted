package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type unifiedGatewayGateAccountReader struct{}

func (unifiedGatewayGateAccountReader) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	accounts := make([]*Account, 0, len(ids))
	for _, id := range ids {
		accounts = append(accounts, &Account{ID: id, Name: "account", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true})
	}
	return accounts, nil
}

func (unifiedGatewayGateAccountReader) List(context.Context, pagination.PaginationParams) ([]Account, *pagination.PaginationResult, error) {
	return nil, &pagination.PaginationResult{}, nil
}

func TestUnifiedGatewayAdminFailsClosedWithoutDesignatedAccessGroup(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	service := newUnifiedAdminServiceForTest(repo)
	service.cfg.Gateway.UnifiedGatewayAccessGroupID = 0

	_, err := service.CreateDraft(context.Background(), "7", "unconfigured-group", validUnifiedAdminDocument())
	require.ErrorIs(t, err, ErrUnifiedGatewayAdminInvalidRequest)
	meta := service.Meta(context.Background())
	require.Contains(t, meta.Blockers, UnifiedGatewayIssue{Code: "access_group_unconfigured", Message: "unified gateway access group is not configured"})
}

func TestUnifiedGatewayAdminRejectsNonDesignatedAccessGroup(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	service := newUnifiedAdminServiceForTest(repo)
	document := validUnifiedAdminDocument()
	document.AccessGroupID = "ag_8"

	_, err := service.CreateDraft(context.Background(), "7", "wrong-group", document)
	require.ErrorIs(t, err, ErrUnifiedGatewayAdminNotFound)
}

type unifiedGatewayCandidateAdminReader struct {
	accounts []*Account
}

func (r unifiedGatewayCandidateAdminReader) GetByIDs(context.Context, []int64) ([]*Account, error) {
	return r.accounts, nil
}

func (unifiedGatewayCandidateAdminReader) List(context.Context, pagination.PaginationParams) ([]Account, *pagination.PaginationResult, error) {
	return nil, &pagination.PaginationResult{}, nil
}

func TestUnifiedGatewayAdminModelCandidatesReadCurrentMappings(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	cfg := &config.Config{}
	cfg.Gateway.UnifiedGatewayAdminUIEnabled = true
	cfg.Gateway.UnifiedGatewayAccessGroupID = 7
	account := &Account{
		ID: 1, Name: "yetoken sanitized", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"base_url": "https://sanitized-yetoken.invalid/v1", "model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra", "deepseek-v4-pro-0813": "deepseek-v4-pro-0813"}},
	}
	service := NewUnifiedGatewayAdminService(repo, unifiedAdminGroupReader{}, unifiedGatewayCandidateAdminReader{accounts: []*Account{account}}, cfg)

	candidates, err := service.ListModelCandidates(context.Background())
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	require.Equal(t, "deepseek-v4-pro-0813", candidates[0].PublicModel)
	require.Equal(t, UnifiedGatewayProviderOpenAICompatible, candidates[0].ProviderIdentity)
	require.True(t, candidates[0].RuntimeEligible)
	require.Equal(t, "gpt-6-astra", candidates[1].PublicModel)
}

func TestUnifiedGatewayAdminModelCandidatesRecognizeClaudeGroupAccounts(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	cfg := &config.Config{}
	cfg.Gateway.UnifiedGatewayAdminUIEnabled = true
	cfg.Gateway.UnifiedGatewayAccessGroupID = 14
	account := &Account{
		ID: 123, Name: "claude sanitized", Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{
			"claude-opus-4-6":   "claude-opus-4-6",
			"claude-sonnet-4-6": "claude-sonnet-4-6",
		}},
	}
	service := NewUnifiedGatewayAdminService(repo, unifiedAdminGroupReader{}, unifiedGatewayCandidateAdminReader{accounts: []*Account{account}}, cfg)

	candidates, err := service.ListModelCandidates(context.Background())
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	for _, candidate := range candidates {
		require.Equal(t, UnifiedGatewayProviderAnthropicAPIKey, candidate.ProviderIdentity)
		require.Equal(t, UnifiedGatewayEndpointChatCompletions, candidate.Endpoint)
		require.True(t, candidate.RuntimeEligible, candidate.Blockers)
	}
}

func TestUnifiedGatewayAdminChecksBindingOwnershipForSuperadmin(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	cfg := &config.Config{}
	cfg.Gateway.UnifiedGatewayAdminUIEnabled = true
	cfg.Gateway.UnifiedGatewayAccessGroupID = 7
	service := NewUnifiedGatewayAdminService(repo, unifiedAdminGroupReader{}, unifiedGatewayGateAccountReader{}, cfg)
	document := validUnifiedAdminDocument()
	document.Lanes[0].Targets[0].ProviderIdentity = UnifiedGatewayProviderOpenAIAPIKey
	document.Lanes[0].Targets[0].Bindings[0].AccountID = "acct_2"

	validation, err := service.ValidateDraft(context.Background(), "missing", &document)
	if err != nil {
		// ValidateDraft requires a stored draft; call the document validator so
		// the test exercises the same server-side superadmin path directly.
		validation, err = service.validateDocument(context.Background(), document)
	}
	require.NoError(t, err)
	require.False(t, validation.Valid)
	require.Contains(t, validation.FieldErrors, "lanes[0].targets[0].bindings[0].account_id")
	require.Contains(t, validation.FieldErrors["lanes[0].targets[0].bindings[0].account_id"], "selected access group")
}

type unifiedGatewayLiveAccountReader struct {
	accounts map[int64]*Account
}

func (r unifiedGatewayLiveAccountReader) GetByID(_ context.Context, id int64) (*Account, error) {
	return r.accounts[id], nil
}

func TestUnifiedGatewayModelListAndRuntimeUseDesignatedGroupAndLiveEligibility(t *testing.T) {
	catalog := NewMemoryUnifiedGatewayRouteCatalog()
	activeID, err := catalog.CreateTarget(UnifiedGatewayRouteTarget{
		ID: 1, AccessGroupID: 42, BillingLaneID: "gpt", PublicModel: "gpt-5.5", ProviderIdentity: UnifiedGatewayProviderOpenAIAPIKey, UpstreamModel: "gpt-5.5", Endpoint: UnifiedGatewayEndpointResponses,
		BillingMode: string(BillingModeToken), RateMode: UnifiedRateModeManualOnly, RateBasis: UnifiedRateBasisToken, LaneRule: unifiedGatewayRule(string(BillingModeToken), UnifiedRateBasisToken, 0.01, 1, 1), Enabled: true, Priority: 1,
	})
	require.NoError(t, err)
	unschedulableID, err := catalog.CreateTarget(UnifiedGatewayRouteTarget{
		ID: 2, AccessGroupID: 42, BillingLaneID: "gpt-fallback", PublicModel: "gpt-5.5", ProviderIdentity: UnifiedGatewayProviderOpenAIAPIKey, UpstreamModel: "gpt-5.5", Endpoint: UnifiedGatewayEndpointResponses,
		BillingMode: string(BillingModeToken), RateMode: UnifiedRateModeManualOnly, RateBasis: UnifiedRateBasisToken, LaneRule: unifiedGatewayRule(string(BillingModeToken), UnifiedRateBasisToken, 0.02, 1, 1), Enabled: true, Priority: 2,
	})
	require.NoError(t, err)
	require.NoError(t, catalog.AddBinding(UnifiedGatewayAccountBinding{ID: 11, RouteTargetID: activeID, AccountID: 101, Enabled: true}))
	require.NoError(t, catalog.AddBinding(UnifiedGatewayAccountBinding{ID: 21, RouteTargetID: unschedulableID, AccountID: 202, Enabled: true}))

	accounts := unifiedGatewayLiveAccountReader{accounts: map[int64]*Account{
		101: {ID: 101, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		202: {ID: 202, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: false},
	}}
	store := NewMemoryUnifiedGatewayPriceSnapshotStore()
	ledger := NewMemoryUnifiedGatewayChargeLedger(map[int64]float64{7: 100})
	upstream := UnifiedGatewayUpstreamExecutorFunc(func(context.Context, UnifiedGatewayRouteSelection, UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error) {
		return UnifiedGatewayUpstreamResult{Delivered: true, MeasuredUnits: 1, ResponseBody: []byte(`{"ok":true}`)}, nil
	})
	gateway := NewUnifiedGateway(catalog, store, ledger, upstream)
	gateway.SetAccountReader(accounts)

	models, err := gateway.ListModels(context.Background(), 42)
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "gpt-5.5", models[0].ID)

	cfg := &config.Config{}
	cfg.Gateway.UnifiedGatewayRuntimeEnabled = true
	cfg.Gateway.UnifiedGatewayAccessGroupID = 42
	runtime := NewUnifiedGatewayRuntimeService(gateway, catalog, store, upstream, cfg)
	_, err = runtime.ListModels(context.Background(), 41)
	require.ErrorIs(t, err, ErrUnifiedGatewayUnauthorized)
	models, err = runtime.ListModels(context.Background(), 42)
	require.NoError(t, err)
	require.Len(t, models, 1)
}
