package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type unifiedAdminMemoryRepo struct {
	ready      bool
	configs    map[string]*UnifiedGatewayConfig
	drafts     map[string]*UnifiedGatewayDraft
	idem       map[string]*UnifiedGatewayIdempotencyRecord
	versionErr bool
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
	return nil, nil
}
func (r *unifiedAdminMemoryRepo) ListSnapshots(context.Context, UnifiedGatewaySnapshotFilter) ([]UnifiedGatewaySnapshotView, int64, error) {
	return nil, 0, nil
}
func (r *unifiedAdminMemoryRepo) BindingExists(_ context.Context, bindingID string) (bool, error) {
	for _, draft := range r.drafts {
		for _, lane := range draft.Document.Lanes {
			for _, target := range lane.Targets {
				for _, binding := range target.Bindings {
					if binding.ID == bindingID {
						return true, nil
					}
				}
			}
		}
	}
	return false, nil
}
func (r *unifiedAdminMemoryRepo) BindingExistsInGroups(ctx context.Context, bindingID string, accessGroupIDs []int64) (bool, error) {
	if len(accessGroupIDs) == 0 {
		return false, nil
	}
	for _, draft := range r.drafts {
		groupID, err := parseAccessGroupID(draft.Document.AccessGroupID)
		if err != nil || !containsInt64(accessGroupIDs, groupID) {
			continue
		}
		exists, err := r.BindingExists(ctx, bindingID)
		return exists, err
	}
	return false, nil
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
	return []*Account{{ID: 1, Name: "Plus account", Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}}, nil
}
func (unifiedAdminAccountReader) List(context.Context, pagination.PaginationParams) ([]Account, *pagination.PaginationResult, error) {
	return nil, &pagination.PaginationResult{}, nil
}

type unifiedAdminGroupReader struct{}

func (unifiedAdminGroupReader) ListActive(context.Context) ([]Group, error) {
	return []Group{{ID: 7, Name: "ChatGPT", Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1}}, nil
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
	require.False(t, meta.Capabilities["publish"])
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
	failed, err := service.Preview(context.Background(), document, UnifiedGatewayPreviewRequest{LaneID: "lane_plus", TargetID: "target_1", BindingID: "binding_1", DeliveryState: "failed"})
	require.NoError(t, err)
	require.Equal(t, "0.00000000", failed.EstimatedCharge)
	_, err = service.Preview(context.Background(), document, UnifiedGatewayPreviewRequest{LaneID: "lane_plus", TargetID: "target_1", BindingID: "binding_1", DeliveryState: "pending"})
	require.ErrorIs(t, err, ErrUnifiedGatewayAdminInvalidRequest)
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

func TestUnifiedGatewayAdminProbeRequiresKnownBinding(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	svc := newUnifiedAdminServiceForTest(repo)
	draft, err := svc.CreateDraft(context.Background(), "7", "probe-draft", validUnifiedAdminDocument())
	require.NoError(t, err)
	_, err = svc.ProbeBinding(context.Background(), "7", "probe-key", "binding_1")
	require.NoError(t, err)
	_, err = svc.ProbeBinding(context.Background(), "7", "probe-missing-key", "binding_missing")
	require.ErrorIs(t, err, ErrUnifiedGatewayAdminNotFound)
	_ = draft
}

func TestUnifiedGatewayAdminIdempotentReplayRechecksCurrentGroupScope(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	document := validUnifiedAdminDocument()
	document.ID = "mc_scope"
	repo.configs[document.ID] = &document
	scope := &unifiedAdminScopeReader{user: &User{ID: 9, Role: RoleAdmin, AllowedGroups: []int64{7}}}
	cfg := &config.Config{}
	cfg.Gateway.UnifiedGatewayAdminUIEnabled = true
	service := NewUnifiedGatewayAdminService(repo, unifiedAdminGroupReader{}, unifiedAdminAccountReader{}, cfg, scope)
	ctx := WithUnifiedGatewayAdminActor(context.Background(), "9")

	created, err := service.CreateDraftFromConfig(ctx, "9", "draft-replay", document.ID)
	require.NoError(t, err)
	require.NotNil(t, created)

	scope.user.AllowedGroups = []int64{8}
	_, err = service.CreateDraftFromConfig(ctx, "9", "draft-replay", document.ID)
	require.ErrorIs(t, err, ErrUnifiedGatewayAdminNotFound)

	scope.user.AllowedGroups = []int64{7}
	probe, err := service.ProbeBinding(ctx, "9", "probe-replay", "binding_1")
	require.NoError(t, err)
	require.Equal(t, "unsupported", probe["status"])

	scope.user.AllowedGroups = []int64{8}
	_, err = service.ProbeBinding(ctx, "9", "probe-replay", "binding_1")
	require.ErrorIs(t, err, ErrUnifiedGatewayAdminNotFound)
}

func TestUnifiedGatewayProbeTreatsMissingOrMalformedFreshUntilAsStale(t *testing.T) {
	multiplier := "1.2"
	base := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	for _, freshUntil := range []string{"", "not-a-time", base.Add(-time.Minute).Format(time.RFC3339)} {
		probe := &UnifiedGatewayAdminProbe{Status: "ok", Basis: "token", ResolvedRateMultiplier: &multiplier, FreshUntil: freshUntil}
		require.False(t, validateProbeTimeAt(probe, "token", base), freshUntil)
	}
}
