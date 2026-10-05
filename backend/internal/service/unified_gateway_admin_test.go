package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type unifiedAdminMemoryRepo struct {
	ready          bool
	configs        map[string]*UnifiedGatewayConfig
	drafts         map[string]*UnifiedGatewayDraft
	revisions      []UnifiedGatewayRevision
	idem           map[string]*UnifiedGatewayIdempotencyRecord
	versionErr     bool
	getConfigCalls int
	getDraftCalls  int
}

func newUnifiedAdminMemoryRepo() *unifiedAdminMemoryRepo {
	return &unifiedAdminMemoryRepo{ready: true, configs: map[string]*UnifiedGatewayConfig{}, drafts: map[string]*UnifiedGatewayDraft{}, idem: map[string]*UnifiedGatewayIdempotencyRecord{}}
}
func (r *unifiedAdminMemoryRepo) CheckSchema(context.Context) (UnifiedGatewaySchemaReadiness, error) {
	return UnifiedGatewaySchemaReadiness{Ready: r.ready, Version: UnifiedGatewayAdminSchemaVersion}, nil
}
func (r *unifiedAdminMemoryRepo) ListConfigs(context.Context, UnifiedGatewayConfigListFilter) ([]UnifiedGatewayConfig, int64, error) {
	out := make([]UnifiedGatewayConfig, 0, len(r.configs))
	for _, value := range r.configs {
		out = append(out, *value)
	}
	return out, int64(len(out)), nil
}
func (r *unifiedAdminMemoryRepo) GetConfig(_ context.Context, id string) (*UnifiedGatewayConfig, error) {
	r.getConfigCalls++
	value := r.configs[id]
	if value == nil {
		return nil, ErrUnifiedGatewayAdminNotFound
	}
	copy := *value
	return &copy, nil
}
func (r *unifiedAdminMemoryRepo) CreateDraft(_ context.Context, draft *UnifiedGatewayDraft) (*UnifiedGatewayDraft, error) {
	if _, ok := r.configs[draft.ConfigID]; ok {
		return nil, ErrUnifiedGatewayAdminVersion
	}
	copy := *draft
	r.drafts[draft.ID] = &copy
	document := draft.Document
	r.configs[draft.ConfigID] = &document
	return &copy, nil
}
func (r *unifiedAdminMemoryRepo) CreateDraftFromConfig(_ context.Context, configID string, draft *UnifiedGatewayDraft) (*UnifiedGatewayDraft, error) {
	if _, ok := r.configs[configID]; !ok {
		return nil, ErrUnifiedGatewayAdminNotFound
	}
	for _, existing := range r.drafts {
		if existing.ConfigID == configID {
			copy := *existing
			return &copy, nil
		}
	}
	copy := *draft
	r.drafts[draft.ID] = &copy
	return &copy, nil
}
func (r *unifiedAdminMemoryRepo) GetDraft(_ context.Context, id string) (*UnifiedGatewayDraft, error) {
	r.getDraftCalls++
	value := r.drafts[id]
	if value == nil {
		return nil, ErrUnifiedGatewayAdminNotFound
	}
	copy := *value
	return &copy, nil
}
func (r *unifiedAdminMemoryRepo) UpdateDraft(_ context.Context, id string, expected int64, document UnifiedGatewayConfig, actor string) (*UnifiedGatewayDraft, error) {
	value := r.drafts[id]
	if value == nil {
		return nil, ErrUnifiedGatewayAdminNotFound
	}
	if value.Revision != expected {
		return nil, ErrUnifiedGatewayAdminVersion
	}
	value.Revision++
	value.Document = document
	value.UpdatedBy = actor
	r.drafts[id] = value
	copy := *value
	return &copy, nil
}
func (r *unifiedAdminMemoryRepo) PublishConfig(_ context.Context, configID, draftID string, expected, expectedDraft int64, document UnifiedGatewayConfig, actor, reason, digest string) (*UnifiedGatewayConfig, error) {
	if r.versionErr {
		return nil, ErrUnifiedGatewayAdminVersion
	}
	value := r.configs[configID]
	if value == nil {
		return nil, ErrUnifiedGatewayAdminNotFound
	}
	if value.Revision != expected {
		return nil, ErrUnifiedGatewayAdminVersion
	}
	draft := r.drafts[draftID]
	if draft == nil || draft.Revision != expectedDraft {
		return nil, ErrUnifiedGatewayAdminVersion
	}
	*value = document
	value.ID = configID
	value.Lifecycle = UnifiedGatewayLifecyclePublished
	value.Revision++
	value.Readiness = UnifiedGatewayReadinessReady
	return value, nil
}
func (r *unifiedAdminMemoryRepo) DisableConfig(_ context.Context, configID string, expected int64, actor, reason string) (*UnifiedGatewayConfig, error) {
	value := r.configs[configID]
	if value == nil {
		return nil, ErrUnifiedGatewayAdminNotFound
	}
	if value.Revision != expected {
		return nil, ErrUnifiedGatewayAdminVersion
	}
	value.Revision++
	value.Lifecycle = UnifiedGatewayLifecycleDisabled
	return value, nil
}
func (r *unifiedAdminMemoryRepo) RestoreConfig(_ context.Context, configID string, source, expected int64, actor, reason string) (*UnifiedGatewayConfig, error) {
	value := r.configs[configID]
	if value == nil {
		return nil, ErrUnifiedGatewayAdminNotFound
	}
	if value.Revision != expected {
		return nil, ErrUnifiedGatewayAdminVersion
	}
	value.Revision++
	value.Lifecycle = UnifiedGatewayLifecyclePublished
	return value, nil
}
func (r *unifiedAdminMemoryRepo) ListRevisions(context.Context, string) ([]UnifiedGatewayRevision, error) {
	return r.revisions, nil
}
func (r *unifiedAdminMemoryRepo) GetIdempotency(_ context.Context, actor, operation, resource, key string) (*UnifiedGatewayIdempotencyRecord, error) {
	value := r.idem[actor+"|"+operation+"|"+resource+"|"+key]
	if value == nil {
		return nil, nil
	}
	copy := *value
	return &copy, nil
}
func (r *unifiedAdminMemoryRepo) PutIdempotency(_ context.Context, item *UnifiedGatewayIdempotencyRecord) error {
	key := item.ActorID + "|" + item.Operation + "|" + item.ResourceID + "|" + item.Key
	if _, ok := r.idem[key]; ok {
		return ErrUnifiedGatewayAdminIdempotency
	}
	copy := *item
	r.idem[key] = &copy
	return nil
}

type unifiedAdminAccountReader struct{}

func (unifiedAdminAccountReader) GetByIDs(context.Context, []int64) ([]*Account, error) {
	return []*Account{{ID: 1, Name: "Plus account", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}}, nil
}
func (unifiedAdminAccountReader) List(context.Context, pagination.PaginationParams) ([]Account, *pagination.PaginationResult, error) {
	return nil, &pagination.PaginationResult{}, nil
}

type unifiedAdminGroupReader struct{}

func (unifiedAdminGroupReader) ListActive(context.Context) ([]Group, error) {
	return []Group{{ID: 7, Name: "ChatGPT", Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1}}, nil
}

func (unifiedAdminGroupReader) GetAccountIDsByGroupIDs(context.Context, []int64) ([]int64, error) {
	return []int64{1}, nil
}

type unifiedAdminScopeReader struct{ user *User }

func (r *unifiedAdminScopeReader) GetByID(context.Context, int64) (*User, error) {
	if r == nil || r.user == nil {
		return nil, ErrUnifiedGatewayAdminNotFound
	}
	copy := *r.user
	return &copy, nil
}

func newUnifiedAdminServiceForTest(repo *unifiedAdminMemoryRepo) *UnifiedGatewayAdminService {
	cfg := &config.Config{}
	cfg.Gateway.UnifiedGatewayAdminUIEnabled = true
	cfg.Gateway.UnifiedGatewayAccessGroupID = 7
	return NewUnifiedGatewayAdminService(repo, unifiedAdminGroupReader{}, unifiedAdminAccountReader{}, cfg)
}

/*
	func validUnifiedAdminDocument() UnifiedGatewayConfig {
		return UnifiedGatewayConfig{AccessGroupID: "ag_7", PublicModel: "gpt-5.5", Endpoint: UnifiedGatewayEndpointChatCompletions, Lanes: []UnifiedGatewayBillingLane{{ID: "lane_plus", Code: "plus", Name: "Plus", SelectionStrategy: "fixed_priority", Profile: UnifiedGatewayPricingProfile{ID: "profile_1", Version: "1", PricingModel: UnifiedGatewayPricingModelProviderMetered, BillingMode: "token", RateMode: "manual_only", RateBasis: "token", PricingSchemaID: "token_v1", Currency: "USD", BasePriceSemantics: "provider_base", ManualBaseUnitPrice: strptr("0.000010000000"), ManualUpstreamMultiplier: strptr("1.000000"), UserMarkupMultiplier: strptr("1.200000"), FixedFee: strptr("0"), MinimumCharge: strptr("0"), RoundingMode: "half_up", Precision: 8, FallbackReason: strptr("manual account cost"), ChargeTrigger: "success_delivery", FailureCharge: "zero"}, Targets: []UnifiedGatewayAdminRouteTarget{{ID: "target_1", ProviderIdentity: "openai-chatgpt", UpstreamModel: "gpt-5.5", Endpoint: UnifiedGatewayEndpointChatCompletions, Priority: 10, Bindings: []UnifiedGatewayAdminAccountBinding{{ID: "binding_1", AccountID: "acct_1", Schedulable: true, Eligibility: "eligible", Priority: 10, Enabled: true, Revision: 1}}}}}}}
	}
*/
func strptr(value string) *string { return &value }

func validUnifiedAdminDocument() UnifiedGatewayConfig {
	profile := UnifiedGatewayPricingProfile{
		ID: "profile_1", Version: "1", PricingModel: UnifiedGatewayPricingModelProviderMetered,
		BillingMode: "token", RateMode: "manual_only", RateBasis: "token", PricingSchemaID: "token_v1",
		Currency: "USD", BasePriceSemantics: "provider_base", ManualBaseUnitPrice: strptr("0.000010000000"),
		ManualUpstreamMultiplier: strptr("1.000000"), UserMarkupMultiplier: strptr("1.200000"), FixedFee: strptr("0"),
		MinimumCharge: strptr("0"), RoundingMode: "half_up", Precision: 8, FallbackReason: strptr("manual account cost"),
		ChargeTrigger: "success_delivery", FailureCharge: "zero",
	}
	binding := UnifiedGatewayAdminAccountBinding{ID: "binding_1", AccountID: "acct_1", Schedulable: true, Eligibility: "eligible", Priority: 10, Enabled: true, Revision: 1}
	target := UnifiedGatewayAdminRouteTarget{ID: "target_1", ProviderIdentity: "openai-chatgpt", UpstreamModel: "gpt-5.5", Endpoint: UnifiedGatewayEndpointChatCompletions, Priority: 10, Bindings: []UnifiedGatewayAdminAccountBinding{binding}}
	lane := UnifiedGatewayBillingLane{ID: "lane_plus", Code: "plus", Name: "Plus", SelectionStrategy: "fixed_priority", Profile: profile, Targets: []UnifiedGatewayAdminRouteTarget{target}}
	return UnifiedGatewayConfig{AccessGroupID: "ag_7", PublicModel: "gpt-5.5", Endpoint: UnifiedGatewayEndpointChatCompletions, Lanes: []UnifiedGatewayBillingLane{lane}}
}

func TestUnifiedGatewayAdminMetaFailsClosed(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	repo.ready = false
	service := newUnifiedAdminServiceForTest(repo)
	meta := service.Meta(context.Background())
	require.False(t, meta.MigrationReady)
	require.False(t, meta.RuntimeEffective)
	require.False(t, meta.Capabilities["publish"])
}

func TestUnifiedGatewayAdminRuntimeRemainsIneffectiveInAdminOnlyPhase(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	service := newUnifiedAdminServiceForTest(repo)
	service.cfg.Gateway.UnifiedGatewayRuntimeEnabled = true

	meta := service.Meta(context.Background())
	require.True(t, meta.MigrationReady)
	require.True(t, meta.RuntimeEnabled)
	require.False(t, meta.RuntimeEffective)

	config := service.decorateConfig(UnifiedGatewayConfig{
		Lifecycle: UnifiedGatewayLifecyclePublished,
		Readiness: UnifiedGatewayReadinessReady,
	})
	require.False(t, config.RuntimeEffective)

	repo.ready = false
	meta = service.Meta(context.Background())
	require.False(t, meta.MigrationReady)
	require.False(t, meta.RuntimeEffective)
}

func TestUnifiedGatewayRuntimeEffectiveIsServerOwnedAcrossAdminDocuments(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	service := newUnifiedAdminServiceForTest(repo)
	document := validUnifiedAdminDocument()
	document.RuntimeEffective = true

	created, err := service.CreateDraft(context.Background(), "7", "runtime-effective-key", document)
	require.NoError(t, err)
	require.False(t, created.Document.RuntimeEffective)

	// Simulate older persisted data and confirm read responses never trust it.
	repo.configs[created.ConfigID].RuntimeEffective = true
	repo.drafts[created.ID].Document.RuntimeEffective = true

	config, err := service.GetConfig(context.Background(), created.ConfigID)
	require.NoError(t, err)
	require.False(t, config.RuntimeEffective)

	draft, err := service.GetDraft(context.Background(), created.ID)
	require.NoError(t, err)
	require.False(t, draft.Document.RuntimeEffective)

	configs, _, err := service.ListConfigs(context.Background(), UnifiedGatewayConfigListFilter{})
	require.NoError(t, err)
	require.NotEmpty(t, configs)
	require.False(t, configs[0].RuntimeEffective)

	repo.revisions = []UnifiedGatewayRevision{{Document: UnifiedGatewayConfig{RuntimeEffective: true}}}
	revisions, err := service.ListRevisions(context.Background(), created.ConfigID)
	require.NoError(t, err)
	require.Len(t, revisions, 1)
	require.False(t, revisions[0].Document.RuntimeEffective)
}

func TestUnifiedGatewayPublishIdempotentReplayClearsRuntimeEffective(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	service := newUnifiedAdminServiceForTest(repo)
	draft, err := service.CreateDraft(context.Background(), "7", "runtime-effective-draft", validUnifiedAdminDocument())
	require.NoError(t, err)
	validation, err := service.ValidateDraft(context.Background(), draft.ID, nil)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Blockers)
	token := validation.ValidationToken

	first, err := service.PublishDraft(context.Background(), "7", "runtime-effective-publish", draft.ID, "0", "test", token)
	require.NoError(t, err)
	require.False(t, first.RuntimeEffective)

	idemKey := "7|config.publish|" + draft.ConfigID + "|runtime-effective-publish"
	record := repo.idem[idemKey]
	require.NotNil(t, record)
	var cached UnifiedGatewayConfig
	require.NoError(t, json.Unmarshal(record.ResponseJSON, &cached))
	cached.RuntimeEffective = true
	record.ResponseJSON, err = json.Marshal(cached)
	require.NoError(t, err)

	replayed, err := service.PublishDraft(context.Background(), "7", "runtime-effective-publish", draft.ID, "0", "test", token)
	require.NoError(t, err)
	require.False(t, replayed.RuntimeEffective)
}

func TestUnifiedGatewayAdminGetConfigAndDraftCheckDesignatedGroupScopeBeforeRead(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	document := validUnifiedAdminDocument()
	repo.configs["mc_private"] = &document
	repo.drafts["draft_private"] = &UnifiedGatewayDraft{ID: "draft_private", Document: document}
	wrongGroupDocument := document
	wrongGroupDocument.AccessGroupID = "ag_8"
	repo.configs["mc_wrong_group"] = &wrongGroupDocument
	repo.drafts["draft_wrong_group"] = &UnifiedGatewayDraft{ID: "draft_wrong_group", Document: wrongGroupDocument}

	scope := &unifiedAdminScopeReader{user: &User{ID: 9, Role: RoleAdmin, AllowedGroups: []int64{8}}}
	cfg := &config.Config{}
	cfg.Gateway.UnifiedGatewayAdminUIEnabled = true
	cfg.Gateway.UnifiedGatewayAccessGroupID = 7
	service := NewUnifiedGatewayAdminService(repo, unifiedAdminGroupReader{}, unifiedAdminAccountReader{}, cfg, scope)
	ctx := WithUnifiedGatewayAdminActor(context.Background(), "9")

	_, configErr := service.GetConfig(ctx, "mc_private")
	_, missingConfigErr := service.GetConfig(ctx, "mc_missing")
	var deniedConfig, missingConfig *UnifiedGatewayAdminError
	require.ErrorAs(t, configErr, &deniedConfig)
	require.ErrorAs(t, missingConfigErr, &missingConfig)
	require.Equal(t, missingConfig.Status, deniedConfig.Status)
	require.Equal(t, missingConfig.Reason, deniedConfig.Reason)
	require.Equal(t, missingConfig.Message, deniedConfig.Message)
	require.ErrorIs(t, configErr, ErrUnifiedGatewayAdminNotFound)

	_, draftErr := service.GetDraft(ctx, "draft_private")
	_, missingDraftErr := service.GetDraft(ctx, "draft_missing")
	var deniedDraft, missingDraft *UnifiedGatewayAdminError
	require.ErrorAs(t, draftErr, &deniedDraft)
	require.ErrorAs(t, missingDraftErr, &missingDraft)
	require.Equal(t, missingDraft.Status, deniedDraft.Status)
	require.Equal(t, missingDraft.Reason, deniedDraft.Reason)
	require.Equal(t, missingDraft.Message, deniedDraft.Message)
	require.ErrorIs(t, draftErr, ErrUnifiedGatewayAdminNotFound)
	require.Zero(t, repo.getConfigCalls)
	require.Zero(t, repo.getDraftCalls)

	operations := []struct {
		name string
		call func() error
	}{
		{name: "create draft from config", call: func() error { _, err := service.CreateDraftFromConfig(ctx, "9", "key", "mc_private"); return err }},
		{name: "update draft", call: func() error { _, err := service.UpdateDraft(ctx, "9", "key", "draft_private", 0, document); return err }},
		{name: "validate draft", call: func() error { _, err := service.ValidateDraft(ctx, "draft_private", nil); return err }},
		{name: "validate draft at revision", call: func() error { _, err := service.ValidateDraftAtRevision(ctx, "draft_private", nil, 0); return err }},
		{name: "publish draft", call: func() error {
			_, err := service.PublishDraft(ctx, "9", "key", "draft_private", "0", "test", "token")
			return err
		}},
		{name: "disable config", call: func() error { _, err := service.DisableConfig(ctx, "9", "key", "mc_private", "0", "test"); return err }},
		{name: "restore config", call: func() error { _, err := service.RestoreConfig(ctx, "9", "key", "mc_private", 1, 0, "test"); return err }},
		{name: "list revisions", call: func() error { _, err := service.ListRevisions(ctx, "mc_private"); return err }},
		{name: "apply pricing import", call: func() error {
			_, err := service.PricingImportApply(ctx, "9", "key", "draft_private", 0, UnifiedGatewayPricingImportRequest{LaneID: "lane_plus", SourceGroupID: "ag_8"})
			return err
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			err := operation.call()
			require.ErrorIs(t, err, ErrUnifiedGatewayAdminNotFound)
			var adminErr *UnifiedGatewayAdminError
			require.ErrorAs(t, err, &adminErr)
			require.Equal(t, http.StatusNotFound, adminErr.Status)
			require.Equal(t, "UNIFIED_GATEWAY_SCOPE_DENIED", adminErr.Reason)
		})
	}
	require.Zero(t, repo.getConfigCalls)
	require.Zero(t, repo.getDraftCalls)

	scope.user.AllowedGroups = []int64{7}
	_, err := service.GetConfig(ctx, "mc_private")
	require.NoError(t, err)
	_, err = service.GetDraft(ctx, "draft_private")
	require.NoError(t, err)

	_, mismatchedConfigErr := service.GetConfig(ctx, "mc_wrong_group")
	_, missingConfigErr = service.GetConfig(ctx, "mc_absent")
	var mismatchedConfig, absentConfig *UnifiedGatewayAdminError
	require.ErrorAs(t, mismatchedConfigErr, &mismatchedConfig)
	require.ErrorAs(t, missingConfigErr, &absentConfig)
	require.Equal(t, absentConfig.Status, mismatchedConfig.Status)
	require.Equal(t, absentConfig.Reason, mismatchedConfig.Reason)
	require.Equal(t, absentConfig.Message, mismatchedConfig.Message)

	_, mismatchedDraftErr := service.GetDraft(ctx, "draft_wrong_group")
	_, missingDraftErr = service.GetDraft(ctx, "draft_absent")
	var mismatchedDraft, absentDraft *UnifiedGatewayAdminError
	require.ErrorAs(t, mismatchedDraftErr, &mismatchedDraft)
	require.ErrorAs(t, missingDraftErr, &absentDraft)
	require.Equal(t, absentDraft.Status, mismatchedDraft.Status)
	require.Equal(t, absentDraft.Reason, mismatchedDraft.Reason)
	require.Equal(t, absentDraft.Message, mismatchedDraft.Message)
}

func TestUnifiedGatewayAdminCreateIsIdempotentBeforeGeneratedIDs(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	service := newUnifiedAdminServiceForTest(repo)
	input := validUnifiedAdminDocument()
	first, err := service.CreateDraft(context.Background(), "7", "same-key", input)
	require.NoError(t, err)
	second, err := service.CreateDraft(context.Background(), "7", "same-key", input)
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, first.ConfigID, second.ConfigID)
}

func TestUnifiedGatewayAdminValidationPreviewAndFailureDoesNotCharge(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	service := newUnifiedAdminServiceForTest(repo)
	draft, err := service.CreateDraft(context.Background(), "7", "draft-key", validUnifiedAdminDocument())
	require.NoError(t, err)
	validation, err := service.ValidateDraft(context.Background(), draft.ID, nil)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Blockers)
	preview, err := service.Preview(context.Background(), draft.Document, UnifiedGatewayPreviewRequest{LaneID: "lane_plus", TargetID: "target_1", BindingID: "binding_1", InputTokens: strptr("1000"), OutputTokens: strptr("500"), DeliveryState: "success"})
	require.NoError(t, err)
	require.Equal(t, "0.01800000", preview.EstimatedCharge)
	require.Equal(t, "openai-chatgpt", preview.ProviderIdentity)
	require.Equal(t, "gpt-5.5", preview.UpstreamModel)
	require.Equal(t, "Plus account", preview.AccountDisplayName)
	require.Equal(t, "token", preview.BillingUnit)
	require.Equal(t, "0.00001200", preview.UserUnitPrice)
	require.Equal(t, "0.00000000", preview.FailureChargeAmount)
	require.Equal(t, "manual_only", preview.ResolvedRateSource)
	failed, err := service.Preview(context.Background(), draft.Document, UnifiedGatewayPreviewRequest{LaneID: "lane_plus", TargetID: "target_1", BindingID: "binding_1", InputTokens: strptr("1000"), OutputTokens: strptr("500"), DeliveryState: "failed"})
	require.NoError(t, err)
	require.Equal(t, "0.00000000", failed.EstimatedCharge)
}

func TestUnifiedGatewayAdminVersionAndIdempotencyConflicts(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	service := newUnifiedAdminServiceForTest(repo)
	draft, err := service.CreateDraft(context.Background(), "7", "version-key", validUnifiedAdminDocument())
	require.NoError(t, err)
	_, err = service.UpdateDraft(context.Background(), "7", "update-key", draft.ID, 7, draft.Document)
	require.ErrorIs(t, err, ErrUnifiedGatewayAdminVersion)
	_, err = service.CreateDraft(context.Background(), "7", "version-key", UnifiedGatewayConfig{AccessGroupID: "ag_7", PublicModel: "other", Endpoint: UnifiedGatewayEndpointResponses})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUnifiedGatewayAdminIdempotency) || errors.Is(err, ErrUnifiedGatewayAdminValidation))
}

func TestUnifiedGatewayCanonicalDigestStable(t *testing.T) {
	left := map[string]any{"b": "2", "a": "1"}
	right := map[string]any{"a": "1", "b": "2"}
	require.Equal(t, canonicalDigest(left), canonicalDigest(right))
	_, _ = json.Marshal(time.Now())
}

func TestUnifiedGatewayAdminManualFlatRuleAndDeliveryState(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	service := newUnifiedAdminServiceForTest(repo)
	document := validUnifiedAdminDocument()
	profile := &document.Lanes[0].Profile
	profile.PricingSchemaID = "flat_unit_price_v1"
	profile.BasePriceSemantics = "final_user_price"
	profile.ProviderBaseUnitPrice = nil
	profile.ManualBaseUnitPrice = nil
	profile.ManualUpstreamMultiplier = nil
	profile.UserMarkupMultiplier = nil
	profile.FinalUserUnitPrice = nil
	profile.BillingMode = "per_request"
	profile.RateBasis = "per_request"
	profile.ManualPricingRules = &UnifiedGatewayManualRule{FormulaID: "flat_unit_price", Unit: "request", UnitPrice: strptr("0.02500000")}
	validation, err := service.validateDocument(context.Background(), document)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Blockers)
	preview, err := service.Preview(context.Background(), document, UnifiedGatewayPreviewRequest{LaneID: "lane_plus", TargetID: "target_1", BindingID: "binding_1", DeliveryState: "success"})
	require.NoError(t, err)
	require.Equal(t, "0.02500000", preview.EstimatedCharge)
	require.Equal(t, "request", preview.BillingUnit)
	require.Equal(t, "0.02500000", preview.UserUnitPrice)
	failed, err := service.Preview(context.Background(), document, UnifiedGatewayPreviewRequest{LaneID: "lane_plus", TargetID: "target_1", BindingID: "binding_1", DeliveryState: "failed"})
	require.NoError(t, err)
	require.Equal(t, "0.00000000", failed.EstimatedCharge)
	_, err = service.Preview(context.Background(), document, UnifiedGatewayPreviewRequest{LaneID: "lane_plus", TargetID: "target_1", BindingID: "binding_1", DeliveryState: "pending"})
	require.ErrorIs(t, err, ErrUnifiedGatewayAdminInvalidRequest)
}

func TestUnifiedGatewayAdminPreviewUsesStoredProbeMetadata(t *testing.T) {
	service := newUnifiedAdminServiceForTest(newUnifiedAdminMemoryRepo())
	document := validUnifiedAdminDocument()
	profile := &document.Lanes[0].Profile
	profile.RateMode = "probe_preferred"
	document.Lanes[0].Targets[0].Bindings[0].Probe = &UnifiedGatewayAdminProbe{
		Status:                 "ok",
		Basis:                  profile.RateBasis,
		ResolvedRateMultiplier: strptr("2.000000"),
		SnapshotRef:            "probe-snapshot-ref",
		ReceivedAt:             time.Now().UTC().Format(time.RFC3339),
		FreshUntil:             time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}

	preview, err := service.Preview(context.Background(), document, UnifiedGatewayPreviewRequest{
		LaneID: "lane_plus", TargetID: "target_1", BindingID: "binding_1", InputTokens: strptr("1000"), OutputTokens: strptr("500"), DeliveryState: "success",
	})
	require.NoError(t, err)
	require.Equal(t, "probe_declared", preview.ResolvedRateSource)
	require.Equal(t, "2.000000", *preview.UpstreamDeclaredRate)
	require.Equal(t, "probe-snapshot-ref", preview.ProbeSnapshotRef)
	require.False(t, preview.Persisted)
}

func TestUnifiedGatewayAdminRequiresAnEnabledEligibleBinding(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	service := newUnifiedAdminServiceForTest(repo)
	document := validUnifiedAdminDocument()
	document.Lanes[0].Targets[0].Bindings[0].Enabled = false
	validation, err := service.validateDocument(context.Background(), document)
	require.NoError(t, err)
	require.False(t, validation.Valid)
	require.Contains(t, validation.FieldErrors, "lanes[0].targets")
}

func TestUnifiedGatewayAdminProfileIdentityChangesWithPrice(t *testing.T) {
	first := normalizeUnifiedGatewayDocument(validUnifiedAdminDocument())
	second := validUnifiedAdminDocument()
	second.Lanes[0].Profile.ManualUpstreamMultiplier = strptr("1.300000")
	second = normalizeUnifiedGatewayDocument(second)
	require.NotEqual(t, first.Lanes[0].Profile.ID, second.Lanes[0].Profile.ID)
	third := validUnifiedAdminDocument()
	third = normalizeUnifiedGatewayDocument(third)
	require.Equal(t, first.Lanes[0].Profile.ID, third.Lanes[0].Profile.ID)
}

func TestUnifiedGatewayAdminValidateBindsDraftRevision(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	svc := newUnifiedAdminServiceForTest(repo)
	draft, err := svc.CreateDraft(context.Background(), "7", "revision-draft", validUnifiedAdminDocument())
	require.NoError(t, err)
	_, err = svc.UpdateDraft(context.Background(), "7", "revision-update", draft.ID, 0, draft.Document)
	require.NoError(t, err)
	_, err = svc.ValidateDraftAtRevision(context.Background(), draft.ID, nil, 0)
	require.ErrorIs(t, err, ErrUnifiedGatewayAdminVersion)
	validation, err := svc.ValidateDraftAtRevision(context.Background(), draft.ID, nil, 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), validation.ServerRevision)
}

func TestUnifiedGatewayAdminPricingImportUsesSourceGroupAndIsScoped(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	svc := newUnifiedAdminServiceForTest(repo)
	draft, err := svc.CreateDraft(context.Background(), "7", "pricing-draft", validUnifiedAdminDocument())
	require.NoError(t, err)
	result, err := svc.PricingImportPreview(context.Background(), draft.Document, UnifiedGatewayPricingImportRequest{LaneID: "lane_plus", SourceGroupID: "ag_7"})
	require.NoError(t, err)
	require.Equal(t, "ag_7", result.SourceGroupID)
	require.Equal(t, "manual_only", result.Profile.RateMode)
	require.Equal(t, "1.000000", valueOrString(result.Profile.ManualUpstreamMultiplier))
	require.NotEmpty(t, result.SourceRevision)

	_, err = svc.PricingImportPreview(context.Background(), draft.Document, UnifiedGatewayPricingImportRequest{LaneID: "missing", SourceGroupID: "ag_7"})
	require.ErrorIs(t, err, ErrUnifiedGatewayAdminNotFound)
}

type unifiedPricingImportGroupReader struct {
	groups          []Group
	listActiveCalls int
}

func (r *unifiedPricingImportGroupReader) ListActive(context.Context) ([]Group, error) {
	r.listActiveCalls++
	return r.groups, nil
}

func pricingImportDocument() UnifiedGatewayConfig {
	document := validUnifiedAdminDocument()
	profile := &document.Lanes[0].Profile
	profile.PricingModel = UnifiedGatewayPricingModelProviderMetered
	profile.BillingMode = "token"
	profile.RateBasis = "token"
	profile.PricingSchemaID = "token_v1"
	profile.BasePriceSemantics = "provider_base"
	profile.ProviderBaseUnitPrice = strptr("0.000010000000")
	profile.ManualBaseUnitPrice = nil
	profile.ManualPricingRules = nil
	return document
}

func TestUnifiedGatewayPricingImportAllowsAccessibleNonDesignatedSourceGroup(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	groups := &unifiedPricingImportGroupReader{groups: []Group{
		{ID: 7, Name: "Unified", Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1},
		{ID: 8, Name: "Pricing source", Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1.5},
	}}
	scope := &unifiedAdminScopeReader{user: &User{ID: 9, Role: RoleAdmin, AllowedGroups: []int64{7, 8}}}
	cfg := &config.Config{}
	cfg.Gateway.UnifiedGatewayAdminUIEnabled = true
	cfg.Gateway.UnifiedGatewayAccessGroupID = 7
	service := NewUnifiedGatewayAdminService(repo, groups, unifiedAdminAccountReader{}, cfg, scope)
	ctx := WithUnifiedGatewayAdminActor(context.Background(), "9")

	result, err := service.PricingImportPreview(ctx, pricingImportDocument(), UnifiedGatewayPricingImportRequest{LaneID: "lane_plus", SourceGroupID: "ag_8"})
	require.NoError(t, err)
	require.Equal(t, "ag_8", result.SourceGroupID)
	require.Equal(t, "1.500000", valueOrString(result.Profile.ManualUpstreamMultiplier))
	require.Equal(t, 1, groups.listActiveCalls)
}

func TestUnifiedGatewayPricingImportRejectsOutOfScopeSourceBeforeReadingGroups(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	groups := &unifiedPricingImportGroupReader{groups: []Group{
		{ID: 7, Name: "Unified", Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1},
		{ID: 8, Name: "Out of scope", Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1.5},
	}}
	scope := &unifiedAdminScopeReader{user: &User{ID: 9, Role: RoleAdmin, AllowedGroups: []int64{7}}}
	cfg := &config.Config{}
	cfg.Gateway.UnifiedGatewayAdminUIEnabled = true
	cfg.Gateway.UnifiedGatewayAccessGroupID = 7
	service := NewUnifiedGatewayAdminService(repo, groups, unifiedAdminAccountReader{}, cfg, scope)
	ctx := WithUnifiedGatewayAdminActor(context.Background(), "9")

	_, err := service.PricingImportPreview(ctx, pricingImportDocument(), UnifiedGatewayPricingImportRequest{LaneID: "lane_plus", SourceGroupID: "ag_8"})
	require.ErrorIs(t, err, ErrUnifiedGatewayAdminNotFound)
	require.Zero(t, groups.listActiveCalls)
}

func TestUnifiedGatewayAdminIdempotentReplayRechecksCurrentGroupScope(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	document := validUnifiedAdminDocument()
	document.ID = "mc_scope"
	repo.configs[document.ID] = &document
	scope := &unifiedAdminScopeReader{user: &User{ID: 9, Role: RoleAdmin, AllowedGroups: []int64{7}}}
	cfg := &config.Config{}
	cfg.Gateway.UnifiedGatewayAdminUIEnabled = true
	cfg.Gateway.UnifiedGatewayAccessGroupID = 7
	service := NewUnifiedGatewayAdminService(repo, unifiedAdminGroupReader{}, unifiedAdminAccountReader{}, cfg, scope)
	ctx := WithUnifiedGatewayAdminActor(context.Background(), "9")

	created, err := service.CreateDraftFromConfig(ctx, "9", "draft-replay", document.ID)
	require.NoError(t, err)
	require.NotNil(t, created)

	scope.user.AllowedGroups = []int64{8}
	_, err = service.CreateDraftFromConfig(ctx, "9", "draft-replay", document.ID)
	require.ErrorIs(t, err, ErrUnifiedGatewayAdminNotFound)
}

type unifiedCandidateReadCounters struct {
	groupAccountReads int
	accountReads      int
}

type unifiedCandidateGroupReader struct{ counters *unifiedCandidateReadCounters }

func (unifiedCandidateGroupReader) ListActive(context.Context) ([]Group, error) { return nil, nil }
func (r unifiedCandidateGroupReader) GetAccountIDsByGroupIDs(context.Context, []int64) ([]int64, error) {
	r.counters.groupAccountReads++
	return []int64{1}, nil
}

type unifiedCandidateAccountReader struct{ counters *unifiedCandidateReadCounters }

func (r unifiedCandidateAccountReader) GetByIDs(context.Context, []int64) ([]*Account, error) {
	r.counters.accountReads++
	return []*Account{{ID: 1, Name: "candidate", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}}, nil
}
func (unifiedCandidateAccountReader) List(context.Context, pagination.PaginationParams) ([]Account, *pagination.PaginationResult, error) {
	return nil, &pagination.PaginationResult{}, nil
}

func TestUnifiedGatewayAdminCandidateScopeCheckedBeforeCandidateReads(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	counters := &unifiedCandidateReadCounters{}
	scope := &unifiedAdminScopeReader{user: &User{ID: 9, Role: RoleAdmin, AllowedGroups: []int64{8}}}
	cfg := &config.Config{}
	cfg.Gateway.UnifiedGatewayAdminUIEnabled = true
	cfg.Gateway.UnifiedGatewayAccessGroupID = 7
	service := NewUnifiedGatewayAdminService(repo, unifiedCandidateGroupReader{counters}, unifiedCandidateAccountReader{counters}, cfg, scope)
	ctx := WithUnifiedGatewayAdminActor(context.Background(), "9")

	_, err := service.ListModelCandidates(ctx)
	require.ErrorIs(t, err, ErrUnifiedGatewayAdminNotFound)
	require.Zero(t, counters.groupAccountReads)
	require.Zero(t, counters.accountReads)

	scope.user.AllowedGroups = []int64{7}
	_, err = service.ListModelCandidates(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, counters.groupAccountReads)
	require.Equal(t, 1, counters.accountReads)
}
