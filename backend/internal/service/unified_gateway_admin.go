package service

// This file contains the isolated administration plane for the unified
// gateway.  It deliberately does not call CompositeRouteResolver, the legacy
// group/channel price readers, or the legacy usage ledger.  Runtime traffic is
// still gated separately; this service is safe to construct while the gate is
// false.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const UnifiedGatewayAdminContractRevision = "v1.3.1-route-guard"

const (
	UnifiedGatewayAdminSchemaVersion = "unified_gateway_admin_v1"

	UnifiedGatewayEndpointChatCompletions = "chat_completions"
	UnifiedGatewayEndpointResponses       = "responses"
	UnifiedGatewayEndpointImages          = "images_generations"
	UnifiedGatewayEndpointVideos          = "videos"

	UnifiedGatewayLifecycleDraft     = "draft"
	UnifiedGatewayLifecyclePublished = "published"
	UnifiedGatewayLifecycleDisabled  = "disabled"
	UnifiedGatewayLifecycleArchived  = "archived"

	UnifiedGatewayReadinessReady   = "ready"
	UnifiedGatewayReadinessBlocked = "blocked"

	UnifiedGatewayPricingModelProviderMetered       = "provider_metered"
	UnifiedGatewayPricingModelAmortizedSubscription = "amortized_subscription"
	UnifiedGatewayPricingModelFixedRequest          = "fixed_request"
	UnifiedGatewayPricingModelImage                 = "image"
	UnifiedGatewayPricingModelVideo                 = "video"

	UnifiedGatewayRoundingHalfUp = "half_up"

	UnifiedGatewayChargeTriggerSuccess = "success_delivery"
	UnifiedGatewayFailureChargeZero    = "zero"
)

var (
	ErrUnifiedGatewayAdminDisabled       = errors.New("unified gateway admin UI is disabled")
	ErrUnifiedGatewayAdminMigration      = errors.New("unified gateway admin migration is not ready")
	ErrUnifiedGatewayAdminNotFound       = errors.New("unified gateway admin resource not found")
	ErrUnifiedGatewayAdminVersion        = errors.New("unified gateway admin revision conflict")
	ErrUnifiedGatewayAdminIdempotency    = errors.New("unified gateway admin idempotency conflict")
	ErrUnifiedGatewayAdminKeyRequired    = errors.New("unified gateway admin idempotency key is required")
	ErrUnifiedGatewayAdminValidation     = errors.New("unified gateway admin validation failed")
	ErrUnifiedGatewayAdminProbeDisabled  = errors.New("unified gateway provider probe is not enabled")
	ErrUnifiedGatewayAdminInvalidRequest = errors.New("unified gateway admin request is invalid")
)

// UnifiedGatewayAdminError carries a stable reason without exposing SQL,
// credentials, or provider response bodies to the HTTP layer.
type UnifiedGatewayAdminError struct {
	Status   int
	Reason   string
	Message  string
	Metadata map[string]string
	Err      error
}

func (e *UnifiedGatewayAdminError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Message
}

func (e *UnifiedGatewayAdminError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func adminError(status int, reason, message string, metadata map[string]string, cause error) error {
	return &UnifiedGatewayAdminError{Status: status, Reason: reason, Message: message, Metadata: metadata, Err: cause}
}

type UnifiedGatewayIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

// The public document uses opaque IDs and decimal strings.  The repository
// may map access_group_id to a BIGINT internally, but the UI never depends on
// that implementation detail.
type UnifiedGatewayConfig struct {
	ID               string                      `json:"id,omitempty"`
	AccessGroupID    string                      `json:"access_group_id"`
	PublicModel      string                      `json:"public_model"`
	Endpoint         string                      `json:"endpoint"`
	Lifecycle        string                      `json:"lifecycle,omitempty"`
	Revision         int64                       `json:"revision,omitempty"`
	Lanes            []UnifiedGatewayBillingLane `json:"lanes"`
	Readiness        string                      `json:"readiness,omitempty"`
	Blockers         []UnifiedGatewayIssue       `json:"blockers,omitempty"`
	Warnings         []UnifiedGatewayIssue       `json:"warnings,omitempty"`
	RuntimeEffective bool                        `json:"runtime_effective"`
}

type UnifiedGatewayBillingLane struct {
	ID                    string                           `json:"id"`
	Code                  string                           `json:"code"`
	Name                  string                           `json:"name"`
	SelectionStrategy     string                           `json:"selection_strategy"`
	PricingSourceGroupID  string                           `json:"pricing_source_group_id,omitempty"`
	PricingSourceRevision string                           `json:"pricing_source_revision,omitempty"`
	Profile               UnifiedGatewayPricingProfile     `json:"profile"`
	Targets               []UnifiedGatewayAdminRouteTarget `json:"targets"`
}

type UnifiedGatewayPricingProfile struct {
	ID                       string                    `json:"id"`
	Version                  string                    `json:"version"`
	PricingModel             string                    `json:"pricing_model"`
	BillingMode              string                    `json:"billing_mode"`
	RateMode                 string                    `json:"rate_mode"`
	RateBasis                string                    `json:"rate_basis"`
	PricingSchemaID          string                    `json:"pricing_schema_id"`
	Currency                 string                    `json:"currency"`
	BasePriceSemantics       string                    `json:"base_price_semantics"`
	ProviderBaseUnitPrice    *string                   `json:"provider_base_unit_price"`
	ManualBaseUnitPrice      *string                   `json:"manual_base_unit_price"`
	ManualUpstreamMultiplier *string                   `json:"manual_upstream_multiplier"`
	UserMarkupMultiplier     *string                   `json:"user_markup_multiplier"`
	FinalUserUnitPrice       *string                   `json:"final_user_unit_price"`
	FixedFee                 *string                   `json:"fixed_fee"`
	MinimumCharge            *string                   `json:"minimum_charge"`
	RoundingMode             string                    `json:"rounding_mode"`
	Precision                int                       `json:"precision"`
	FallbackReason           *string                   `json:"fallback_reason"`
	ManualPricingRules       *UnifiedGatewayManualRule `json:"manual_pricing_rules"`
	ChargeTrigger            string                    `json:"charge_trigger"`
	FailureCharge            string                    `json:"failure_charge"`
}

type UnifiedGatewayManualRule struct {
	FormulaID string  `json:"formula_id"`
	Unit      string  `json:"unit"`
	UnitPrice *string `json:"unit_price,omitempty"`
}

type UnifiedGatewayAdminRouteTarget struct {
	ID               string                              `json:"id"`
	ProviderIdentity string                              `json:"provider_identity"`
	UpstreamModel    string                              `json:"upstream_model"`
	Endpoint         string                              `json:"endpoint"`
	Priority         int                                 `json:"priority"`
	Bindings         []UnifiedGatewayAdminAccountBinding `json:"bindings"`
}

type UnifiedGatewayAdminAccountBinding struct {
	ID          string                    `json:"id"`
	AccountID   string                    `json:"account_id"`
	DisplayName string                    `json:"display_name,omitempty"`
	Schedulable bool                      `json:"schedulable"`
	Eligibility string                    `json:"eligibility"`
	Probe       *UnifiedGatewayAdminProbe `json:"probe,omitempty"`
	Priority    int                       `json:"priority"`
	Enabled     bool                      `json:"enabled"`
	Revision    int64                     `json:"revision"`
}

type UnifiedGatewayAdminProbe struct {
	Status                 string  `json:"status"`
	Basis                  string  `json:"basis"`
	ResolvedRateMultiplier *string `json:"resolved_rate_multiplier,omitempty"`
	SnapshotRef            string  `json:"snapshot_ref,omitempty"`
	ReceivedAt             string  `json:"received_at,omitempty"`
	FreshUntil             string  `json:"fresh_until,omitempty"`
}

type UnifiedGatewayDraft struct {
	ID        string               `json:"id"`
	ConfigID  string               `json:"config_id"`
	Revision  int64                `json:"revision"`
	Document  UnifiedGatewayConfig `json:"document"`
	CreatedBy string               `json:"created_by,omitempty"`
	UpdatedBy string               `json:"updated_by,omitempty"`
	CreatedAt time.Time            `json:"created_at,omitempty"`
	UpdatedAt time.Time            `json:"updated_at,omitempty"`
}

type UnifiedGatewayRevision struct {
	Revision  int64                `json:"revision"`
	Lifecycle string               `json:"lifecycle"`
	Digest    string               `json:"digest"`
	ActorID   string               `json:"actor_id,omitempty"`
	Reason    string               `json:"reason,omitempty"`
	CreatedAt time.Time            `json:"created_at"`
	Document  UnifiedGatewayConfig `json:"document"`
}

type UnifiedGatewaySnapshotView struct {
	ID               string     `json:"id"`
	RequestID        string     `json:"request_id"`
	AttemptID        string     `json:"attempt_id"`
	AccessGroupID    string     `json:"access_group_id"`
	PublicModel      string     `json:"public_model"`
	BillingLaneID    string     `json:"billing_lane_id"`
	AccountID        string     `json:"account_id"`
	ProviderIdentity string     `json:"provider_identity"`
	Status           string     `json:"status"`
	RateSource       string     `json:"rate_source,omitempty"`
	PolicyVersion    string     `json:"policy_version,omitempty"`
	MeasuredUnits    string     `json:"measured_units"`
	UserCharge       string     `json:"user_charge"`
	Currency         string     `json:"currency"`
	CreatedAt        time.Time  `json:"created_at"`
	FinalizedAt      *time.Time `json:"finalized_at,omitempty"`
}

type UnifiedGatewayValidationResult struct {
	Valid           bool                  `json:"valid"`
	Blockers        []UnifiedGatewayIssue `json:"blockers"`
	Warnings        []UnifiedGatewayIssue `json:"warnings"`
	FieldErrors     map[string]string     `json:"field_errors"`
	ValidationToken string                `json:"validation_token,omitempty"`
	ServerRevision  int64                 `json:"server_revision"`
}

type UnifiedGatewayPreviewRequest struct {
	LaneID        string  `json:"lane_id"`
	TargetID      string  `json:"target_id"`
	BindingID     string  `json:"binding_id"`
	InputTokens   *string `json:"input_tokens,omitempty"`
	OutputTokens  *string `json:"output_tokens,omitempty"`
	Units         *string `json:"units,omitempty"`
	DeliveryState string  `json:"delivery_state"`
}

type UnifiedGatewayPreviewResult struct {
	Valid                bool              `json:"valid"`
	PreviewDigest        string            `json:"preview_digest"`
	QuoteID              string            `json:"quote_id"`
	Persisted            bool              `json:"persisted"`
	SelectionStatus      string            `json:"selection_status"`
	BillingMode          string            `json:"billing_mode"`
	RateMode             string            `json:"rate_mode"`
	ResolvedRateSource   string            `json:"resolved_rate_source"`
	ProbeStatus          string            `json:"probe_status,omitempty"`
	UpstreamDeclaredRate *string           `json:"upstream_declared_rate,omitempty"`
	ManualUpstreamRate   *string           `json:"manual_upstream_multiplier,omitempty"`
	UserMarkupMultiplier *string           `json:"user_markup_multiplier,omitempty"`
	EffectiveMultiplier  *string           `json:"effective_multiplier,omitempty"`
	BillableUnits        map[string]string `json:"billable_units"`
	EstimatedCharge      string            `json:"estimated_charge"`
	Currency             string            `json:"currency"`
	RoundingMode         string            `json:"rounding_mode"`
	Precision            int               `json:"precision"`
	FallbackReason       *string           `json:"fallback_reason,omitempty"`
	PolicyVersion        string            `json:"policy_version"`
	ProfileID            string            `json:"profile_id"`
	ProbeSnapshotRef     string            `json:"probe_snapshot_ref,omitempty"`
	ChargeTrigger        string            `json:"charge_trigger"`
	FailureCharge        string            `json:"failure_charge"`
}

type UnifiedGatewayPricingImportRequest struct {
	LaneID        string                `json:"lane_id"`
	SourceGroupID string                `json:"source_group_id"`
	ImportDigest  string                `json:"import_digest,omitempty"`
	Document      *UnifiedGatewayConfig `json:"document,omitempty"`
}

type UnifiedGatewayPricingImportResult struct {
	Draft          *UnifiedGatewayDraft         `json:"draft,omitempty"`
	LaneID         string                       `json:"lane_id"`
	SourceGroupID  string                       `json:"source_group_id"`
	SourceRevision string                       `json:"source_revision"`
	Profile        UnifiedGatewayPricingProfile `json:"profile"`
	ImportDigest   string                       `json:"import_digest"`
}

type UnifiedGatewayAdminMeta struct {
	ContractRev            string                `json:"contract_rev"`
	ServerTime             time.Time             `json:"server_time"`
	AdminUIEnabled         bool                  `json:"admin_ui_enabled"`
	RuntimeEnabled         bool                  `json:"runtime_enabled"`
	MigrationReady         bool                  `json:"migration_ready"`
	SchemaVersion          string                `json:"schema_version"`
	Capabilities           map[string]bool       `json:"capabilities"`
	SupportedEndpoints     []string              `json:"supported_endpoints"`
	SupportedBillingModes  []string              `json:"supported_billing_modes"`
	SupportedRateBases     []string              `json:"supported_rate_bases"`
	SupportedRateModes     []string              `json:"supported_rate_modes"`
	SupportedPricingModels []string              `json:"supported_pricing_models"`
	Blockers               []UnifiedGatewayIssue `json:"blockers,omitempty"`
}

type UnifiedGatewayOption struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Platform     string   `json:"platform,omitempty"`
	Status       string   `json:"status,omitempty"`
	Schedulable  bool     `json:"schedulable,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type UnifiedGatewayOptions struct {
	AccessGroups        []UnifiedGatewayOption `json:"access_groups"`
	PricingSourceGroups []UnifiedGatewayOption `json:"pricing_source_groups"`
	Accounts            []UnifiedGatewayOption `json:"accounts"`
}

type UnifiedGatewaySchemaReadiness struct {
	Ready   bool
	Version string
	Missing []string
}

type UnifiedGatewayIdempotencyRecord struct {
	ActorID       string
	Operation     string
	ResourceID    string
	Key           string
	RequestDigest string
	StatusCode    int
	ResponseJSON  []byte
	CreatedAt     time.Time
}

type UnifiedGatewayConfigListFilter struct {
	Page           int
	PageSize       int
	Lifecycle      string
	AccessGroupIDs []int64
}

type UnifiedGatewaySnapshotFilter struct {
	Page           int
	PageSize       int
	AccessGroupID  string
	AccessGroupIDs []int64
	PublicModel    string
	Status         string
	From           time.Time
	To             time.Time
}

// UnifiedGatewayAdminRepository is deliberately narrower than the legacy
// repositories.  SQL implementations must keep mutation and idempotency
// writes in the same transaction; in-memory fakes are used only by unit tests.
type UnifiedGatewayAdminRepository interface {
	CheckSchema(ctx context.Context) (UnifiedGatewaySchemaReadiness, error)
	ListConfigs(ctx context.Context, filter UnifiedGatewayConfigListFilter) ([]UnifiedGatewayConfig, int64, error)
	GetConfig(ctx context.Context, id string) (*UnifiedGatewayConfig, error)
	CreateDraft(ctx context.Context, draft *UnifiedGatewayDraft) (*UnifiedGatewayDraft, error)
	CreateDraftFromConfig(ctx context.Context, configID string, draft *UnifiedGatewayDraft) (*UnifiedGatewayDraft, error)
	GetDraft(ctx context.Context, id string) (*UnifiedGatewayDraft, error)
	UpdateDraft(ctx context.Context, id string, expectedRevision int64, document UnifiedGatewayConfig, actorID string) (*UnifiedGatewayDraft, error)
	PublishConfig(ctx context.Context, configID, draftID string, expectedRevision, expectedDraftRevision int64, document UnifiedGatewayConfig, actorID, reason, digest string) (*UnifiedGatewayConfig, error)
	DisableConfig(ctx context.Context, configID string, expectedRevision int64, actorID, reason string) (*UnifiedGatewayConfig, error)
	RestoreConfig(ctx context.Context, configID string, sourceRevision, expectedRevision int64, actorID, reason string) (*UnifiedGatewayConfig, error)
	ListRevisions(ctx context.Context, configID string) ([]UnifiedGatewayRevision, error)
	ListSnapshots(ctx context.Context, filter UnifiedGatewaySnapshotFilter) ([]UnifiedGatewaySnapshotView, int64, error)
	BindingExists(ctx context.Context, bindingID string) (bool, error)
	BindingExistsInGroups(ctx context.Context, bindingID string, accessGroupIDs []int64) (bool, error)
	GetIdempotency(ctx context.Context, actorID, operation, resourceID, key string) (*UnifiedGatewayIdempotencyRecord, error)
	PutIdempotency(ctx context.Context, record *UnifiedGatewayIdempotencyRecord) error
}

// UnifiedGatewayAdminAtomicRepository is implemented by the production SQL
// repository.  Each mutating operation rechecks the idempotency key while
// holding a database transaction lock and persists the response in that same
// transaction as the state change.  The base interface remains small so the
// service can still be exercised with an in-memory repository in unit tests.
type UnifiedGatewayAdminAtomicRepository interface {
	CreateDraftAtomic(ctx context.Context, draft *UnifiedGatewayDraft, record *UnifiedGatewayIdempotencyRecord) (*UnifiedGatewayDraft, bool, error)
	CreateDraftFromConfigAtomic(ctx context.Context, configID string, draft *UnifiedGatewayDraft, record *UnifiedGatewayIdempotencyRecord) (*UnifiedGatewayDraft, bool, error)
	UpdateDraftAtomic(ctx context.Context, id string, expectedRevision int64, document UnifiedGatewayConfig, actorID string, record *UnifiedGatewayIdempotencyRecord) (*UnifiedGatewayDraft, bool, error)
	PublishConfigAtomic(ctx context.Context, configID, draftID string, expectedRevision, expectedDraftRevision int64, document UnifiedGatewayConfig, actorID, reason, digest string, record *UnifiedGatewayIdempotencyRecord) (*UnifiedGatewayConfig, bool, error)
	DisableConfigAtomic(ctx context.Context, configID string, expectedRevision int64, actorID, reason string, record *UnifiedGatewayIdempotencyRecord) (*UnifiedGatewayConfig, bool, error)
	RestoreConfigAtomic(ctx context.Context, configID string, sourceRevision, expectedRevision int64, actorID, reason string, record *UnifiedGatewayIdempotencyRecord) (*UnifiedGatewayConfig, bool, error)
}

type UnifiedGatewayAdminPricingImportAtomicRepository interface {
	ApplyPricingImportAtomic(ctx context.Context, id string, expectedRevision int64, document UnifiedGatewayConfig, actorID string, result *UnifiedGatewayPricingImportResult, record *UnifiedGatewayIdempotencyRecord) (*UnifiedGatewayPricingImportResult, bool, error)
}

type UnifiedGatewayAdminProbeAtomicRepository interface {
	ProbeBindingAtomic(ctx context.Context, actorID, bindingID string, accessGroupIDs []int64, result map[string]any, record *UnifiedGatewayIdempotencyRecord) (map[string]any, bool, error)
}

type unifiedGatewayGroupReader interface {
	ListActive(ctx context.Context) ([]Group, error)
}

type unifiedGatewayGroupAccountReader interface {
	GetAccountIDsByGroupIDs(ctx context.Context, groupIDs []int64) ([]int64, error)
}

type unifiedGatewayAccountReader interface {
	GetByIDs(ctx context.Context, ids []int64) ([]*Account, error)
	List(ctx context.Context, params pagination.PaginationParams) ([]Account, *pagination.PaginationResult, error)
}

// UnifiedGatewayAdminScopeReader supplies the server-side group scope for the
// authenticated administrator.  The legacy admin role remains the outer
// permission check; a non-empty AllowedGroups list narrows the unified admin
// plane to those access groups.  An empty list preserves the existing
// super-admin behavior and is intentionally explicit in the authorization
// code below rather than inferred from request data.
type UnifiedGatewayAdminScopeReader interface {
	GetByID(ctx context.Context, id int64) (*User, error)
}

type unifiedGatewayActorContextKey struct{}

// WithUnifiedGatewayAdminActor binds the authenticated admin identity to the
// request context so read-only service methods can enforce the same group
// scope as mutation methods, without trusting body/query actor fields.
func WithUnifiedGatewayAdminActor(ctx context.Context, actorID string) context.Context {
	return context.WithValue(ctx, unifiedGatewayActorContextKey{}, strings.TrimSpace(actorID))
}

type UnifiedGatewayAdminService struct {
	repo     UnifiedGatewayAdminRepository
	groups   unifiedGatewayGroupReader
	accounts unifiedGatewayAccountReader
	scope    UnifiedGatewayAdminScopeReader
	cfg      *config.Config
	now      func() time.Time
}

func NewUnifiedGatewayAdminService(repo UnifiedGatewayAdminRepository, groups unifiedGatewayGroupReader, accounts unifiedGatewayAccountReader, cfg *config.Config, scopeReaders ...UnifiedGatewayAdminScopeReader) *UnifiedGatewayAdminService {
	var scope UnifiedGatewayAdminScopeReader
	if len(scopeReaders) > 0 {
		scope = scopeReaders[0]
	}
	return &UnifiedGatewayAdminService{repo: repo, groups: groups, accounts: accounts, scope: scope, cfg: cfg, now: func() time.Time { return time.Now().UTC() }}
}

func (s *UnifiedGatewayAdminService) adminEnabled() bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.UnifiedGatewayAdminUIEnabled
}
func (s *UnifiedGatewayAdminService) runtimeEnabled() bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.UnifiedGatewayRuntimeEnabled
}

func (s *UnifiedGatewayAdminService) Meta(ctx context.Context) UnifiedGatewayAdminMeta {
	meta := UnifiedGatewayAdminMeta{
		ContractRev:            UnifiedGatewayAdminContractRevision,
		ServerTime:             s.nowUTC(),
		AdminUIEnabled:         s.adminEnabled(),
		RuntimeEnabled:         s.runtimeEnabled(),
		SchemaVersion:          UnifiedGatewayAdminSchemaVersion,
		Capabilities:           map[string]bool{"read": true, "draft": false, "publish": false, "probe": false},
		SupportedEndpoints:     []string{UnifiedGatewayEndpointChatCompletions, UnifiedGatewayEndpointResponses, UnifiedGatewayEndpointImages, UnifiedGatewayEndpointVideos},
		SupportedBillingModes:  []string{"token", "per_request", "image", "video"},
		SupportedRateBases:     []string{"token", "per_request", "image", "video", "provider_specific"},
		SupportedRateModes:     []string{"probe_preferred", "manual_only", "probe_only"},
		SupportedPricingModels: []string{UnifiedGatewayPricingModelProviderMetered, UnifiedGatewayPricingModelAmortizedSubscription, UnifiedGatewayPricingModelFixedRequest, UnifiedGatewayPricingModelImage, UnifiedGatewayPricingModelVideo},
	}
	if s == nil || s.repo == nil {
		meta.Blockers = append(meta.Blockers, UnifiedGatewayIssue{Code: "repository_unavailable", Message: "unified gateway admin repository is unavailable"})
		return meta
	}
	readiness, err := s.repo.CheckSchema(ctx)
	if err != nil || !readiness.Ready {
		meta.Blockers = append(meta.Blockers, UnifiedGatewayIssue{Code: "migration_not_ready", Message: "unified gateway admin migration is not ready"})
		return meta
	}
	meta.MigrationReady = true
	if meta.AdminUIEnabled {
		meta.Capabilities["draft"] = true
		meta.Capabilities["publish"] = true
		meta.Capabilities["probe"] = false // provider adapters remain opt-in in this phase
	}
	return meta
}

func (s *UnifiedGatewayAdminService) nowUTC() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func (s *UnifiedGatewayAdminService) requireAdminMutation(ctx context.Context) error {
	if s == nil || s.repo == nil {
		return adminError(http.StatusInternalServerError, "UNIFIED_GATEWAY_REPOSITORY_UNAVAILABLE", "unified gateway admin repository is unavailable", nil, ErrUnifiedGatewayAdminInvalidRequest)
	}
	if !s.adminEnabled() {
		return adminError(http.StatusNotFound, "UNIFIED_GATEWAY_ADMIN_DISABLED", "unified gateway admin is disabled", nil, ErrUnifiedGatewayAdminDisabled)
	}
	readiness, err := s.repo.CheckSchema(ctx)
	if err != nil || !readiness.Ready {
		return adminError(http.StatusServiceUnavailable, "UNIFIED_GATEWAY_MIGRATION_NOT_READY", "unified gateway admin migration is not ready", nil, ErrUnifiedGatewayAdminMigration)
	}
	return nil
}

func (s *UnifiedGatewayAdminService) requireAdminRead(ctx context.Context) error {
	if err := s.requireAdminMutation(ctx); err != nil {
		return err
	}
	return nil
}

func (s *UnifiedGatewayAdminService) adminScope(ctx context.Context) (map[int64]struct{}, bool, error) {
	if s.scope == nil {
		return nil, false, nil
	}
	actorID, _ := ctx.Value(unifiedGatewayActorContextKey{}).(string)
	actorID = strings.TrimSpace(actorID)
	parsed, err := strconv.ParseInt(actorID, 10, 64)
	if err != nil || parsed <= 0 {
		return nil, false, adminError(http.StatusForbidden, "UNIFIED_GATEWAY_SCOPE_REQUIRED", "authenticated admin scope is unavailable", nil, ErrUnifiedGatewayAdminInvalidRequest)
	}
	user, err := s.scope.GetByID(ctx, parsed)
	if err != nil || user == nil || !user.IsAdmin() {
		return nil, false, adminError(http.StatusForbidden, "UNIFIED_GATEWAY_SCOPE_UNAVAILABLE", "authenticated admin scope is unavailable", nil, ErrUnifiedGatewayAdminInvalidRequest)
	}
	if len(user.AllowedGroups) == 0 {
		return nil, false, nil
	}
	out := make(map[int64]struct{}, len(user.AllowedGroups))
	for _, groupID := range user.AllowedGroups {
		if groupID > 0 {
			out[groupID] = struct{}{}
		}
	}
	return out, true, nil
}

func (s *UnifiedGatewayAdminService) authorizeAccessGroup(ctx context.Context, accessGroupID string) error {
	groupID, err := parseAccessGroupID(accessGroupID)
	if err != nil {
		return adminError(http.StatusNotFound, "UNIFIED_GATEWAY_SCOPE_DENIED", "resource is outside the administrator scope", nil, ErrUnifiedGatewayAdminNotFound)
	}
	allowed, restricted, err := s.adminScope(ctx)
	if err != nil {
		return err
	}
	if restricted {
		if _, ok := allowed[groupID]; !ok {
			return adminError(http.StatusNotFound, "UNIFIED_GATEWAY_SCOPE_DENIED", "resource is outside the administrator scope", nil, ErrUnifiedGatewayAdminNotFound)
		}
	}
	return nil
}

func (s *UnifiedGatewayAdminService) authorizeDocumentScope(ctx context.Context, document UnifiedGatewayConfig) error {
	return s.authorizeAccessGroup(ctx, document.AccessGroupID)
}

func (s *UnifiedGatewayAdminService) authorizeConfigScope(ctx context.Context, config *UnifiedGatewayConfig) error {
	if config == nil {
		return adminError(http.StatusNotFound, "UNIFIED_GATEWAY_NOT_FOUND", "unified gateway config was not found", nil, ErrUnifiedGatewayAdminNotFound)
	}
	return s.authorizeDocumentScope(ctx, *config)
}

func (s *UnifiedGatewayAdminService) authorizeDraftScope(ctx context.Context, draft *UnifiedGatewayDraft) error {
	if draft == nil {
		return adminError(http.StatusNotFound, "UNIFIED_GATEWAY_NOT_FOUND", "unified gateway draft was not found", nil, ErrUnifiedGatewayAdminNotFound)
	}
	return s.authorizeDocumentScope(ctx, draft.Document)
}

func (s *UnifiedGatewayAdminService) scopeFilter(ctx context.Context) ([]int64, bool, error) {
	allowed, restricted, err := s.adminScope(ctx)
	if err != nil || !restricted {
		return nil, restricted, err
	}
	ids := make([]int64, 0, len(allowed))
	for id := range allowed {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, true, nil
}

func (s *UnifiedGatewayAdminService) ListConfigs(ctx context.Context, filter UnifiedGatewayConfigListFilter) ([]UnifiedGatewayConfig, int64, error) {
	if err := s.requireAdminRead(ctx); err != nil {
		return nil, 0, err
	}
	filter.Page, filter.PageSize = normalizePage(filter.Page, filter.PageSize)
	ids, restricted, err := s.scopeFilter(ctx)
	if err != nil {
		return nil, 0, err
	}
	if restricted {
		filter.AccessGroupIDs = ids
	}
	return s.repo.ListConfigs(ctx, filter)
}

func (s *UnifiedGatewayAdminService) GetConfig(ctx context.Context, id string) (*UnifiedGatewayConfig, error) {
	if err := s.requireAdminRead(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(id) == "" {
		return nil, adminError(http.StatusBadRequest, "UNIFIED_GATEWAY_INVALID_ID", "config id is required", nil, ErrUnifiedGatewayAdminInvalidRequest)
	}
	item, err := s.repo.GetConfig(ctx, strings.TrimSpace(id))
	if err != nil {
		return nil, mapAdminRepoError(err)
	}
	if err := s.authorizeConfigScope(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *UnifiedGatewayAdminService) CreateDraft(ctx context.Context, actorID, key string, document UnifiedGatewayConfig) (*UnifiedGatewayDraft, error) {
	if err := s.requireAdminMutation(ctx); err != nil {
		return nil, err
	}
	if err := s.authorizeDocumentScope(ctx, document); err != nil {
		return nil, err
	}
	// Hash the caller's document before server-generated opaque IDs are added;
	// otherwise retrying the same request with the same idempotency key would
	// produce a different digest on every attempt.
	idempotencyPayload := document
	document = normalizeUnifiedGatewayDocument(document)
	if document.ID == "" {
		document.ID = opaqueID("mc")
	}
	draft := &UnifiedGatewayDraft{ID: opaqueID("draft"), ConfigID: document.ID, Revision: 0, Document: document, CreatedBy: actorID, UpdatedBy: actorID, CreatedAt: s.nowUTC(), UpdatedAt: s.nowUTC()}
	replay, err := s.findIdempotent(ctx, actorID, "draft.create", "", key, idempotencyPayload)
	if err != nil {
		return nil, err
	}
	if replay != nil {
		var out UnifiedGatewayDraft
		if json.Unmarshal(replay.ResponseJSON, &out) == nil {
			if err := s.authorizeDraftScope(ctx, &out); err != nil {
				return nil, err
			}
			return &out, nil
		}
		return nil, ErrUnifiedGatewayAdminIdempotency
	}
	if atomicRepo, ok := s.repo.(UnifiedGatewayAdminAtomicRepository); ok {
		result, _, err := atomicRepo.CreateDraftAtomic(ctx, draft, s.newIdempotencyRecord(actorID, "draft.create", "", key, idempotencyPayload))
		if err != nil {
			return nil, mapAdminRepoError(err)
		}
		draft = result
	} else {
		if result, err := s.repo.CreateDraft(ctx, draft); err != nil {
			return nil, mapAdminRepoError(err)
		} else {
			draft = result
		}
		if err := s.saveIdempotent(ctx, actorID, "draft.create", "", key, idempotencyPayload, http.StatusCreated, draft); err != nil {
			return nil, err
		}
	}
	return draft, nil
}

// CreateDraftFromConfig opens an editable aggregate draft for an existing
// published/disabled config. It keeps the config row and its published
// document untouched until the draft is published.
func (s *UnifiedGatewayAdminService) CreateDraftFromConfig(ctx context.Context, actorID, key, configID string) (*UnifiedGatewayDraft, error) {
	if err := s.requireAdminMutation(ctx); err != nil {
		return nil, err
	}
	configID = strings.TrimSpace(configID)
	if configID == "" {
		return nil, adminError(http.StatusBadRequest, "UNIFIED_GATEWAY_INVALID_ID", "config id is required", nil, ErrUnifiedGatewayAdminInvalidRequest)
	}
	payload := map[string]any{"config_id": configID}
	replay, err := s.findIdempotent(ctx, actorID, "draft.create_from_config", configID, key, payload)
	if err != nil {
		return nil, err
	}
	if replay != nil {
		var out UnifiedGatewayDraft
		if json.Unmarshal(replay.ResponseJSON, &out) == nil {
			if err := s.authorizeDraftScope(ctx, &out); err != nil {
				return nil, err
			}
			return &out, nil
		}
		return nil, ErrUnifiedGatewayAdminIdempotency
	}
	config, err := s.repo.GetConfig(ctx, configID)
	if err != nil {
		return nil, mapAdminRepoError(err)
	}
	if err := s.authorizeConfigScope(ctx, config); err != nil {
		return nil, err
	}
	now := s.nowUTC()
	draft := &UnifiedGatewayDraft{ID: opaqueID("draft"), ConfigID: configID, Revision: 0, Document: normalizeUnifiedGatewayDocument(*config), CreatedBy: actorID, UpdatedBy: actorID, CreatedAt: now, UpdatedAt: now}
	draft.Document.ID = configID
	var result *UnifiedGatewayDraft
	if atomicRepo, ok := s.repo.(UnifiedGatewayAdminAtomicRepository); ok {
		result, _, err = atomicRepo.CreateDraftFromConfigAtomic(ctx, configID, draft, s.newIdempotencyRecord(actorID, "draft.create_from_config", configID, key, payload))
		if err != nil {
			return nil, mapAdminRepoError(err)
		}
	} else {
		result, err = s.repo.CreateDraftFromConfig(ctx, configID, draft)
		if err != nil {
			return nil, mapAdminRepoError(err)
		}
		if err := s.saveIdempotent(ctx, actorID, "draft.create_from_config", configID, key, payload, http.StatusCreated, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *UnifiedGatewayAdminService) GetDraft(ctx context.Context, id string) (*UnifiedGatewayDraft, error) {
	if err := s.requireAdminRead(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(id) == "" {
		return nil, adminError(http.StatusBadRequest, "UNIFIED_GATEWAY_INVALID_ID", "draft id is required", nil, ErrUnifiedGatewayAdminInvalidRequest)
	}
	draft, err := s.repo.GetDraft(ctx, strings.TrimSpace(id))
	if err != nil {
		return nil, mapAdminRepoError(err)
	}
	if err := s.authorizeDraftScope(ctx, draft); err != nil {
		return nil, err
	}
	return draft, nil
}

func (s *UnifiedGatewayAdminService) UpdateDraft(ctx context.Context, actorID, key, id string, expectedRevision int64, document UnifiedGatewayConfig) (*UnifiedGatewayDraft, error) {
	if err := s.requireAdminMutation(ctx); err != nil {
		return nil, err
	}
	if expectedRevision < 0 {
		return nil, versionError(expectedRevision, "draft", id)
	}
	current, err := s.repo.GetDraft(ctx, id)
	if err != nil {
		return nil, mapAdminRepoError(err)
	}
	if err := s.authorizeDraftScope(ctx, current); err != nil {
		return nil, err
	}
	document = normalizeUnifiedGatewayDocument(document)
	if document.ID == "" {
		document.ID = current.ConfigID
	}
	if document.AccessGroupID == "" {
		document.AccessGroupID = current.Document.AccessGroupID
	}
	if document.AccessGroupID != current.Document.AccessGroupID {
		return nil, adminError(http.StatusNotFound, "UNIFIED_GATEWAY_SCOPE_DENIED", "draft access group cannot be changed", nil, ErrUnifiedGatewayAdminNotFound)
	}
	replay, err := s.findIdempotent(ctx, actorID, "draft.update", id, key, document)
	if err != nil {
		return nil, err
	}
	if replay != nil {
		var out UnifiedGatewayDraft
		if json.Unmarshal(replay.ResponseJSON, &out) == nil {
			return &out, nil
		}
		return nil, ErrUnifiedGatewayAdminIdempotency
	}
	var result *UnifiedGatewayDraft
	if atomicRepo, ok := s.repo.(UnifiedGatewayAdminAtomicRepository); ok {
		var atomicErr error
		result, _, atomicErr = atomicRepo.UpdateDraftAtomic(ctx, id, expectedRevision, document, actorID, s.newIdempotencyRecord(actorID, "draft.update", id, key, document))
		if atomicErr != nil {
			return nil, mapAdminRepoError(atomicErr)
		}
	} else {
		result, err = s.repo.UpdateDraft(ctx, id, expectedRevision, document, actorID)
		if err != nil {
			return nil, mapAdminRepoError(err)
		}
		if err := s.saveIdempotent(ctx, actorID, "draft.update", id, key, document, http.StatusOK, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *UnifiedGatewayAdminService) ValidateDraft(ctx context.Context, id string, document *UnifiedGatewayConfig) (*UnifiedGatewayValidationResult, error) {
	if err := s.requireAdminRead(ctx); err != nil {
		return nil, err
	}
	return s.validateDraft(ctx, id, document, nil)
}

// ValidateDraftAtRevision binds validation to the draft revision observed by
// the caller. This closes the validate->publish TOCTOU window for the admin UI
// without changing the compatibility method used by local tests.
func (s *UnifiedGatewayAdminService) ValidateDraftAtRevision(ctx context.Context, id string, document *UnifiedGatewayConfig, expectedRevision int64) (*UnifiedGatewayValidationResult, error) {
	if err := s.requireAdminRead(ctx); err != nil {
		return nil, err
	}
	if expectedRevision < 0 {
		return nil, versionError(expectedRevision, "draft", id)
	}
	return s.validateDraft(ctx, id, document, &expectedRevision)
}

func (s *UnifiedGatewayAdminService) validateDraft(ctx context.Context, id string, document *UnifiedGatewayConfig, expectedRevision *int64) (*UnifiedGatewayValidationResult, error) {
	var doc UnifiedGatewayConfig
	draft, err := s.repo.GetDraft(ctx, id)
	if err != nil {
		return nil, mapAdminRepoError(err)
	}
	if err := s.authorizeDraftScope(ctx, draft); err != nil {
		return nil, err
	}
	draftRevision := draft.Revision
	if expectedRevision != nil && draft.Revision != *expectedRevision {
		return nil, versionError(draft.Revision, "draft", id)
	}
	if document == nil {
		doc = draft.Document
	} else {
		doc = normalizeUnifiedGatewayDocument(*document)
		if doc.ID == "" {
			doc.ID = draft.ConfigID
		}
		if doc.AccessGroupID == "" {
			doc.AccessGroupID = draft.Document.AccessGroupID
		}
		if doc.AccessGroupID != draft.Document.AccessGroupID {
			return nil, adminError(http.StatusNotFound, "UNIFIED_GATEWAY_SCOPE_DENIED", "draft access group cannot be changed", nil, ErrUnifiedGatewayAdminNotFound)
		}
	}
	result, err := s.validateDocument(ctx, doc)
	if err != nil {
		return nil, err
	}
	if expectedRevision != nil || document == nil {
		result.ServerRevision = draftRevision
	} else {
		result.ServerRevision = doc.Revision
	}
	result.ValidationToken = validationToken(doc)
	return result, nil
}

func (s *UnifiedGatewayAdminService) PublishDraft(ctx context.Context, actorID, key, id, ifMatch, reason, validationTokenValue string) (*UnifiedGatewayConfig, error) {
	if err := s.requireAdminMutation(ctx); err != nil {
		return nil, err
	}
	expectedRevision, err := parseIfMatch(ifMatch)
	if err != nil {
		return nil, err
	}
	draft, err := s.repo.GetDraft(ctx, id)
	if err != nil {
		return nil, mapAdminRepoError(err)
	}
	if err := s.authorizeDraftScope(ctx, draft); err != nil {
		return nil, err
	}
	if validationTokenValue == "" || validationTokenValue != validationToken(draft.Document) {
		return nil, adminError(http.StatusUnprocessableEntity, "UNIFIED_GATEWAY_VALIDATION_TOKEN_REQUIRED", "publish requires a current validation token", nil, ErrUnifiedGatewayAdminValidation)
	}
	validation, err := s.validateDocument(ctx, draft.Document)
	if err != nil {
		return nil, err
	}
	if !validation.Valid {
		return nil, adminError(http.StatusUnprocessableEntity, "UNIFIED_GATEWAY_VALIDATION_FAILED", "draft is not ready to publish", map[string]string{"blocker_count": strconv.Itoa(len(validation.Blockers))}, ErrUnifiedGatewayAdminValidation)
	}
	digest := canonicalDigest(draft.Document)
	publishPayload := map[string]any{"draft_id": id, "if_match": expectedRevision, "draft_revision": draft.Revision, "reason": reason, "validation_token": validationTokenValue}
	replay, err := s.findIdempotent(ctx, actorID, "config.publish", draft.ConfigID, key, publishPayload)
	if err != nil {
		return nil, err
	}
	if replay != nil {
		var out UnifiedGatewayConfig
		if json.Unmarshal(replay.ResponseJSON, &out) == nil {
			return &out, nil
		}
		return nil, ErrUnifiedGatewayAdminIdempotency
	}
	var result *UnifiedGatewayConfig
	if atomicRepo, ok := s.repo.(UnifiedGatewayAdminAtomicRepository); ok {
		var atomicErr error
		result, _, atomicErr = atomicRepo.PublishConfigAtomic(ctx, draft.ConfigID, draft.ID, expectedRevision, draft.Revision, draft.Document, actorID, strings.TrimSpace(reason), digest, s.newIdempotencyRecord(actorID, "config.publish", draft.ConfigID, key, publishPayload))
		if atomicErr != nil {
			return nil, mapAdminRepoError(atomicErr)
		}
	} else {
		result, err = s.repo.PublishConfig(ctx, draft.ConfigID, draft.ID, expectedRevision, draft.Revision, draft.Document, actorID, strings.TrimSpace(reason), digest)
		if err != nil {
			return nil, mapAdminRepoError(err)
		}
	}
	*result = s.decorateConfig(*result)
	if _, ok := s.repo.(UnifiedGatewayAdminAtomicRepository); !ok {
		if err := s.saveIdempotent(ctx, actorID, "config.publish", draft.ConfigID, key, publishPayload, http.StatusOK, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *UnifiedGatewayAdminService) DisableConfig(ctx context.Context, actorID, key, id, ifMatch, reason string) (*UnifiedGatewayConfig, error) {
	if err := s.requireAdminMutation(ctx); err != nil {
		return nil, err
	}
	expectedRevision, err := parseIfMatch(ifMatch)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(reason) == "" {
		return nil, adminError(http.StatusBadRequest, "UNIFIED_GATEWAY_DISABLE_REASON_REQUIRED", "disable reason is required", nil, ErrUnifiedGatewayAdminInvalidRequest)
	}
	config, err := s.repo.GetConfig(ctx, id)
	if err != nil {
		return nil, mapAdminRepoError(err)
	}
	if err := s.authorizeConfigScope(ctx, config); err != nil {
		return nil, err
	}
	payload := map[string]any{"if_match": expectedRevision, "reason": reason}
	replay, err := s.findIdempotent(ctx, actorID, "config.disable", id, key, payload)
	if err != nil {
		return nil, err
	}
	if replay != nil {
		var out UnifiedGatewayConfig
		if json.Unmarshal(replay.ResponseJSON, &out) == nil {
			return &out, nil
		}
		return nil, ErrUnifiedGatewayAdminIdempotency
	}
	var result *UnifiedGatewayConfig
	if atomicRepo, ok := s.repo.(UnifiedGatewayAdminAtomicRepository); ok {
		var atomicErr error
		result, _, atomicErr = atomicRepo.DisableConfigAtomic(ctx, id, expectedRevision, actorID, strings.TrimSpace(reason), s.newIdempotencyRecord(actorID, "config.disable", id, key, payload))
		if atomicErr != nil {
			return nil, mapAdminRepoError(atomicErr)
		}
	} else {
		result, err = s.repo.DisableConfig(ctx, id, expectedRevision, actorID, strings.TrimSpace(reason))
		if err != nil {
			return nil, mapAdminRepoError(err)
		}
	}
	*result = s.decorateConfig(*result)
	if _, ok := s.repo.(UnifiedGatewayAdminAtomicRepository); !ok {
		if err := s.saveIdempotent(ctx, actorID, "config.disable", id, key, payload, http.StatusOK, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *UnifiedGatewayAdminService) RestoreConfig(ctx context.Context, actorID, key, id string, sourceRevision, expectedRevision int64, reason string) (*UnifiedGatewayConfig, error) {
	if err := s.requireAdminMutation(ctx); err != nil {
		return nil, err
	}
	if sourceRevision <= 0 {
		return nil, adminError(http.StatusBadRequest, "UNIFIED_GATEWAY_REVISION_REQUIRED", "source revision is required", nil, ErrUnifiedGatewayAdminInvalidRequest)
	}
	config, err := s.repo.GetConfig(ctx, id)
	if err != nil {
		return nil, mapAdminRepoError(err)
	}
	if err := s.authorizeConfigScope(ctx, config); err != nil {
		return nil, err
	}
	payload := map[string]any{"source_revision": sourceRevision, "if_match": expectedRevision, "reason": reason}
	replay, err := s.findIdempotent(ctx, actorID, "config.restore", id, key, payload)
	if err != nil {
		return nil, err
	}
	if replay != nil {
		var out UnifiedGatewayConfig
		if json.Unmarshal(replay.ResponseJSON, &out) == nil {
			return &out, nil
		}
		return nil, ErrUnifiedGatewayAdminIdempotency
	}
	var result *UnifiedGatewayConfig
	if atomicRepo, ok := s.repo.(UnifiedGatewayAdminAtomicRepository); ok {
		var atomicErr error
		result, _, atomicErr = atomicRepo.RestoreConfigAtomic(ctx, id, sourceRevision, expectedRevision, actorID, strings.TrimSpace(reason), s.newIdempotencyRecord(actorID, "config.restore", id, key, payload))
		if atomicErr != nil {
			return nil, mapAdminRepoError(atomicErr)
		}
	} else {
		result, err = s.repo.RestoreConfig(ctx, id, sourceRevision, expectedRevision, actorID, strings.TrimSpace(reason))
		if err != nil {
			return nil, mapAdminRepoError(err)
		}
	}
	*result = s.decorateConfig(*result)
	if _, ok := s.repo.(UnifiedGatewayAdminAtomicRepository); !ok {
		if err := s.saveIdempotent(ctx, actorID, "config.restore", id, key, payload, http.StatusOK, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *UnifiedGatewayAdminService) ListRevisions(ctx context.Context, id string) ([]UnifiedGatewayRevision, error) {
	if err := s.requireAdminRead(ctx); err != nil {
		return nil, err
	}
	config, err := s.repo.GetConfig(ctx, id)
	if err != nil {
		return nil, mapAdminRepoError(err)
	}
	if err := s.authorizeConfigScope(ctx, config); err != nil {
		return nil, err
	}
	items, err := s.repo.ListRevisions(ctx, id)
	if err != nil {
		return nil, mapAdminRepoError(err)
	}
	return items, nil
}

func (s *UnifiedGatewayAdminService) ListSnapshots(ctx context.Context, filter UnifiedGatewaySnapshotFilter) ([]UnifiedGatewaySnapshotView, int64, error) {
	if err := s.requireAdminRead(ctx); err != nil {
		return nil, 0, err
	}
	filter.Page, filter.PageSize = normalizePage(filter.Page, filter.PageSize)
	ids, restricted, err := s.scopeFilter(ctx)
	if err != nil {
		return nil, 0, err
	}
	if restricted {
		filter.AccessGroupIDs = ids
	}
	items, total, err := s.repo.ListSnapshots(ctx, filter)
	if err != nil {
		return nil, 0, mapAdminRepoError(err)
	}
	return items, total, nil
}

func (s *UnifiedGatewayAdminService) Options(ctx context.Context, page, pageSize int) (*UnifiedGatewayOptions, int64, error) {
	if err := s.requireAdminRead(ctx); err != nil {
		return nil, 0, err
	}
	page, pageSize = normalizePage(page, pageSize)
	out := &UnifiedGatewayOptions{}
	groupIDs, restricted, err := s.scopeFilter(ctx)
	if err != nil {
		return nil, 0, err
	}
	allowedAccounts := map[int64]struct{}(nil)
	if restricted {
		reader, ok := s.groups.(unifiedGatewayGroupAccountReader)
		if !ok {
			return nil, 0, adminError(http.StatusServiceUnavailable, "UNIFIED_GATEWAY_SCOPE_UNAVAILABLE", "account group scope is unavailable", nil, ErrUnifiedGatewayAdminInvalidRequest)
		}
		accountIDs, err := reader.GetAccountIDsByGroupIDs(ctx, groupIDs)
		if err != nil {
			return nil, 0, err
		}
		allowedAccounts = make(map[int64]struct{}, len(accountIDs))
		for _, id := range accountIDs {
			allowedAccounts[id] = struct{}{}
		}
	}
	if s.groups != nil {
		groups, err := s.groups.ListActive(ctx)
		if err != nil {
			return nil, 0, err
		}
		for _, group := range groups {
			if restricted {
				if !containsInt64(groupIDs, group.ID) {
					continue
				}
			}
			opt := UnifiedGatewayOption{ID: opaqueNumericID("ag", group.ID), Name: group.Name, Platform: group.Platform, Status: group.Status}
			out.AccessGroups = append(out.AccessGroups, opt)
			out.PricingSourceGroups = append(out.PricingSourceGroups, opt)
		}
	}
	var total int64
	if s.accounts != nil {
		var accounts []Account
		if restricted {
			ids := make([]int64, 0, len(allowedAccounts))
			for id := range allowedAccounts {
				ids = append(ids, id)
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			if len(ids) > 0 {
				loaded, err := s.accounts.GetByIDs(ctx, ids)
				if err != nil {
					return nil, 0, err
				}
				for _, account := range loaded {
					if account != nil {
						accounts = append(accounts, *account)
					}
				}
			}
			total = int64(len(accounts))
		} else {
			var result *pagination.PaginationResult
			accounts, result, err = s.accounts.List(ctx, pagination.PaginationParams{Page: page, PageSize: pageSize})
			if err != nil {
				return nil, 0, err
			}
			if result != nil {
				total = result.Total
			}
		}
		for _, account := range accounts {
			out.Accounts = append(out.Accounts, UnifiedGatewayOption{ID: opaqueNumericID("acct", account.ID), Name: maskAccountName(account.Name, account.ID), Platform: account.Platform, Status: account.Status, Schedulable: account.Schedulable, Capabilities: []string{account.Platform}})
		}
	}
	return out, total, nil
}

func (s *UnifiedGatewayAdminService) ProbeBinding(ctx context.Context, actorID, key, bindingID string) (map[string]any, error) {
	if err := s.requireAdminMutation(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(bindingID) == "" {
		return nil, adminError(http.StatusBadRequest, "UNIFIED_GATEWAY_INVALID_ID", "binding id is required", nil, ErrUnifiedGatewayAdminInvalidRequest)
	}
	scopeGroupIDs, scopeRestricted, err := s.scopeFilter(ctx)
	if err != nil {
		return nil, err
	}
	if scopeRestricted && len(scopeGroupIDs) == 0 {
		return nil, adminError(http.StatusNotFound, "UNIFIED_GATEWAY_SCOPE_DENIED", "binding is outside the administrator scope", nil, ErrUnifiedGatewayAdminNotFound)
	}
	if scopeRestricted {
		exists, err := s.repo.BindingExistsInGroups(ctx, bindingID, scopeGroupIDs)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, adminError(http.StatusNotFound, "UNIFIED_GATEWAY_BINDING_NOT_FOUND", "binding does not belong to a unified gateway config", nil, ErrUnifiedGatewayAdminNotFound)
		}
	}
	// No provider network call is made in the candidate phase.  Returning a
	// deterministic unsupported result prevents the UI from treating a local
	// probe as an authoritative upstream declaration.
	payload := map[string]any{"binding_id": bindingID}
	replay, err := s.findIdempotent(ctx, actorID, "binding.probe", bindingID, key, payload)
	if err != nil {
		return nil, err
	}
	if replay != nil {
		var out map[string]any
		if json.Unmarshal(replay.ResponseJSON, &out) == nil {
			return out, nil
		}
		return nil, ErrUnifiedGatewayAdminIdempotency
	}
	out := map[string]any{"binding_id": bindingID, "status": "unsupported", "reason": "provider_adapter_not_enabled", "runtime_effective": false}
	if s.repo != nil {
		var exists bool
		if scopeRestricted {
			exists, err = s.repo.BindingExistsInGroups(ctx, bindingID, scopeGroupIDs)
		} else {
			exists, err = s.repo.BindingExists(ctx, bindingID)
		}
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, adminError(http.StatusNotFound, "UNIFIED_GATEWAY_BINDING_NOT_FOUND", "binding does not belong to a unified gateway config", nil, ErrUnifiedGatewayAdminNotFound)
		}
	}
	if atomicRepo, ok := s.repo.(UnifiedGatewayAdminProbeAtomicRepository); ok {
		atomicScope := scopeGroupIDs
		if !scopeRestricted {
			atomicScope = nil
		}
		result, _, err := atomicRepo.ProbeBindingAtomic(ctx, actorID, bindingID, atomicScope, out, s.newIdempotencyRecord(actorID, "binding.probe", bindingID, key, payload))
		if err != nil {
			return nil, mapAdminRepoError(err)
		}
		return result, nil
	}
	if err := s.saveIdempotent(ctx, actorID, "binding.probe", bindingID, key, payload, http.StatusOK, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *UnifiedGatewayAdminService) Preview(ctx context.Context, document UnifiedGatewayConfig, request UnifiedGatewayPreviewRequest) (*UnifiedGatewayPreviewResult, error) {
	if err := s.requireAdminRead(ctx); err != nil {
		return nil, err
	}
	if err := s.authorizeDocumentScope(ctx, document); err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(request.DeliveryState)) {
	case "success", "failed":
	default:
		return nil, adminError(http.StatusBadRequest, "UNIFIED_GATEWAY_DELIVERY_STATE_INVALID", "delivery_state must be success or failed", nil, ErrUnifiedGatewayAdminInvalidRequest)
	}
	document = normalizeUnifiedGatewayDocument(document)
	validation, err := s.validateDocument(ctx, document)
	if err != nil {
		return nil, err
	}
	if !validation.Valid {
		return nil, adminError(http.StatusUnprocessableEntity, "UNIFIED_GATEWAY_VALIDATION_FAILED", "configuration is not ready for preview", map[string]string{"blocker_count": strconv.Itoa(len(validation.Blockers))}, ErrUnifiedGatewayAdminValidation)
	}
	lane, _, binding, err := locateUnifiedGatewayBinding(document, request.LaneID, request.TargetID, request.BindingID)
	if err != nil {
		return nil, err
	}
	profile := lane.Profile
	base, err := previewDecimal(profile.ProviderBaseUnitPrice)
	if err != nil {
		return nil, err
	}
	manualBase, err := previewDecimal(profile.ManualBaseUnitPrice)
	if err != nil {
		return nil, err
	}
	manualMultiplier, err := previewDecimal(profile.ManualUpstreamMultiplier)
	if err != nil {
		return nil, err
	}
	markup, err := previewDecimal(profile.UserMarkupMultiplier)
	if err != nil {
		return nil, err
	}
	finalPrice, err := previewDecimal(profile.FinalUserUnitPrice)
	if err != nil {
		return nil, err
	}
	fixedFee, err := previewDecimal(profile.FixedFee)
	if err != nil {
		return nil, err
	}
	minimumCharge, err := previewDecimal(profile.MinimumCharge)
	if err != nil {
		return nil, err
	}
	probeMultiplier, probeStatus, probeRef := validProbeMultiplierAt(binding.Probe, profile.RateBasis, s.nowUTC())
	rateSource := "manual_only"
	effectiveMultiplier := decimal.NewFromInt(1)
	if profile.RateMode == "probe_preferred" && probeMultiplier != nil {
		effectiveMultiplier, rateSource = *probeMultiplier, "probe_declared"
	} else if profile.RateMode == "probe_preferred" && manualMultiplier != nil {
		effectiveMultiplier, rateSource = *manualMultiplier, "manual_fallback"
	} else if profile.RateMode == "probe_only" {
		return nil, adminError(http.StatusUnprocessableEntity, "UNIFIED_GATEWAY_PROBE_REQUIRED", "preview requires a fresh compatible probe", nil, ErrUnifiedGatewayAdminValidation)
	} else if profile.RateMode == "manual_only" && manualMultiplier != nil {
		effectiveMultiplier, rateSource = *manualMultiplier, "manual_only"
	} else if profile.RateMode == "probe_preferred" {
		rateSource = "manual_fallback"
	}
	providerUnit := decimal.Zero
	userUnit := decimal.Zero
	manualRulePrice, err := manualRuleUnitPrice(profile)
	if err != nil {
		return nil, err
	}
	if manualRulePrice != nil {
		providerUnit = *manualRulePrice
		userUnit = *manualRulePrice
	} else if profile.BasePriceSemantics == "final_user_price" {
		userUnit = valueOrZeroAdmin(finalPrice)
		providerUnit = valueOrZeroAdmin(finalPrice)
	} else {
		if manualBase != nil {
			base = manualBase
		}
		if base == nil {
			return nil, adminError(http.StatusUnprocessableEntity, "UNIFIED_GATEWAY_BASE_PRICE_REQUIRED", "provider base unit price is required", nil, ErrUnifiedGatewayAdminValidation)
		}
		providerUnit = base.Mul(effectiveMultiplier)
		if markup == nil {
			return nil, adminError(http.StatusUnprocessableEntity, "UNIFIED_GATEWAY_MARKUP_REQUIRED", "user markup multiplier is required", nil, ErrUnifiedGatewayAdminValidation)
		}
		userUnit = providerUnit.Mul(*markup)
	}
	units, unitsMap, err := previewBillableUnits(profile.BillingMode, request)
	if err != nil {
		return nil, err
	}
	charge := decimal.Zero
	if strings.EqualFold(strings.TrimSpace(request.DeliveryState), "success") {
		charge = userUnit.Mul(units).Add(valueOrZeroAdmin(fixedFee))
		if minimumCharge != nil && charge.LessThan(*minimumCharge) {
			charge = *minimumCharge
		}
	}
	precision := profile.Precision
	if precision < 0 {
		precision = 0
	}
	if precision > 12 {
		precision = 12
	}
	charge = charge.Round(int32(precision))
	digest := canonicalDigest(map[string]any{"document": document, "request": request})
	declared := (*string)(nil)
	if probeMultiplier != nil {
		v := probeMultiplier.StringFixed(6)
		declared = &v
	}
	manualValue := (*string)(nil)
	if manualMultiplier != nil {
		v := manualMultiplier.StringFixed(6)
		manualValue = &v
	}
	markupValue := (*string)(nil)
	if markup != nil {
		v := markup.StringFixed(6)
		markupValue = &v
	}
	effectiveValue := effectiveMultiplier.StringFixed(6)
	fallback := profile.FallbackReason
	return &UnifiedGatewayPreviewResult{Valid: true, PreviewDigest: "sha256:" + digest, QuoteID: opaqueID("quote"), Persisted: false, SelectionStatus: "ready", BillingMode: profile.BillingMode, RateMode: profile.RateMode, ResolvedRateSource: rateSource, ProbeStatus: probeStatus, UpstreamDeclaredRate: declared, ManualUpstreamRate: manualValue, UserMarkupMultiplier: markupValue, EffectiveMultiplier: &effectiveValue, BillableUnits: unitsMap, EstimatedCharge: charge.StringFixed(int32(precision)), Currency: profile.Currency, RoundingMode: profile.RoundingMode, Precision: precision, FallbackReason: fallback, PolicyVersion: profile.Version, ProfileID: profile.ID, ProbeSnapshotRef: probeRef, ChargeTrigger: profile.ChargeTrigger, FailureCharge: profile.FailureCharge}, nil
}

func (s *UnifiedGatewayAdminService) PricingImportPreview(ctx context.Context, document UnifiedGatewayConfig, request UnifiedGatewayPricingImportRequest) (*UnifiedGatewayPricingImportResult, error) {
	if err := s.requireAdminRead(ctx); err != nil {
		return nil, err
	}
	if err := s.authorizeDocumentScope(ctx, document); err != nil {
		return nil, err
	}
	return s.applyPricingImportToDocument(ctx, document, request.LaneID, request.SourceGroupID)
}

func (s *UnifiedGatewayAdminService) PricingImportApply(ctx context.Context, actorID, key, draftID string, expectedRevision int64, request UnifiedGatewayPricingImportRequest) (*UnifiedGatewayPricingImportResult, error) {
	if err := s.requireAdminMutation(ctx); err != nil {
		return nil, err
	}
	if expectedRevision < 0 {
		return nil, versionError(expectedRevision, "draft", draftID)
	}
	draft, err := s.repo.GetDraft(ctx, draftID)
	if err != nil {
		return nil, mapAdminRepoError(err)
	}
	if err := s.authorizeDraftScope(ctx, draft); err != nil {
		return nil, err
	}
	result, err := s.applyPricingImportToDocument(ctx, draft.Document, request.LaneID, request.SourceGroupID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.ImportDigest) != "" && request.ImportDigest != result.ImportDigest {
		return nil, adminError(http.StatusConflict, "UNIFIED_GATEWAY_PRICING_IMPORT_PREVIEW_STALE", "pricing import preview no longer matches the current draft", map[string]string{"expected_digest": request.ImportDigest, "actual_digest": result.ImportDigest}, ErrUnifiedGatewayAdminVersion)
	}
	payload := map[string]any{"draft_id": draftID, "draft_revision": expectedRevision, "lane_id": request.LaneID, "source_group_id": request.SourceGroupID, "import_digest": request.ImportDigest}
	replay, err := s.findIdempotent(ctx, actorID, "pricing.import.apply", draftID, key, payload)
	if err != nil {
		return nil, err
	}
	if replay != nil {
		var out UnifiedGatewayPricingImportResult
		if json.Unmarshal(replay.ResponseJSON, &out) == nil {
			return &out, nil
		}
		return nil, ErrUnifiedGatewayAdminIdempotency
	}
	if atomicRepo, ok := s.repo.(UnifiedGatewayAdminPricingImportAtomicRepository); ok {
		out, _, err := atomicRepo.ApplyPricingImportAtomic(ctx, draftID, expectedRevision, result.Draft.Document, actorID, result, s.newIdempotencyRecord(actorID, "pricing.import.apply", draftID, key, payload))
		if err != nil {
			return nil, mapAdminRepoError(err)
		}
		return out, nil
	}
	updated, err := s.repo.UpdateDraft(ctx, draftID, expectedRevision, result.Draft.Document, actorID)
	if err != nil {
		return nil, mapAdminRepoError(err)
	}
	result.Draft = updated
	if err := s.saveIdempotent(ctx, actorID, "pricing.import.apply", draftID, key, payload, http.StatusOK, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *UnifiedGatewayAdminService) applyPricingImportToDocument(ctx context.Context, document UnifiedGatewayConfig, laneID, sourceGroupID string) (*UnifiedGatewayPricingImportResult, error) {
	document = normalizeUnifiedGatewayDocument(document)
	laneIndex := -1
	for i := range document.Lanes {
		if document.Lanes[i].ID == strings.TrimSpace(laneID) {
			laneIndex = i
			break
		}
	}
	if laneIndex < 0 {
		return nil, adminError(http.StatusNotFound, "UNIFIED_GATEWAY_LANE_NOT_FOUND", "billing lane was not found", nil, ErrUnifiedGatewayAdminNotFound)
	}
	groupID, err := parseAccessGroupID(sourceGroupID)
	if err != nil {
		return nil, adminError(http.StatusBadRequest, "UNIFIED_GATEWAY_SOURCE_GROUP_INVALID", "source_group_id must be a valid opaque group id", nil, ErrUnifiedGatewayAdminInvalidRequest)
	}
	if s.groups == nil {
		return nil, adminError(http.StatusUnprocessableEntity, "UNIFIED_GATEWAY_PRICING_SOURCE_UNAVAILABLE", "pricing source groups are unavailable", nil, ErrUnifiedGatewayAdminValidation)
	}
	groups, err := s.groups.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	var source *Group
	for i := range groups {
		if groups[i].ID == groupID {
			source = &groups[i]
			break
		}
	}
	if source == nil {
		return nil, adminError(http.StatusNotFound, "UNIFIED_GATEWAY_SOURCE_GROUP_NOT_FOUND", "pricing source group is not active", nil, ErrUnifiedGatewayAdminNotFound)
	}
	if err := s.authorizeAccessGroup(ctx, opaqueNumericID("ag", source.ID)); err != nil {
		return nil, err
	}
	lane := &document.Lanes[laneIndex]
	profile := lane.Profile
	if profile.BasePriceSemantics == "final_user_price" && profile.ManualPricingRules == nil {
		return nil, adminError(http.StatusUnprocessableEntity, "UNIFIED_GATEWAY_PRICING_IMPORT_SEMANTICS", "pricing import requires provider_base semantics unless a flat rule is selected", nil, ErrUnifiedGatewayAdminValidation)
	}
	multiplier := source.RateMultiplier
	switch profile.BillingMode {
	case "image":
		if source.ImageRateIndependent && source.ImageRateMultiplier > 0 {
			multiplier = source.ImageRateMultiplier
		}
	case "video":
		if source.VideoRateIndependent && source.VideoRateMultiplier > 0 {
			multiplier = source.VideoRateMultiplier
		}
	}
	profile.RateMode = "manual_only"
	reason := "imported from active legacy pricing group " + sourceGroupID
	profile.FallbackReason = &reason
	if profile.ManualPricingRules != nil {
		var flatPrice *float64
		switch profile.BillingMode {
		case "image":
			flatPrice = source.GetImagePrice("2K")
		case "video":
			flatPrice = source.VideoPrice720P
		}
		if flatPrice == nil {
			return nil, adminError(http.StatusUnprocessableEntity, "UNIFIED_GATEWAY_PRICING_IMPORT_FLAT_PRICE_UNAVAILABLE", "source group has no compatible flat unit price", nil, ErrUnifiedGatewayAdminValidation)
		}
		flatValue := decimal.NewFromFloat(*flatPrice).StringFixed(8)
		profile.ManualPricingRules.UnitPrice = &flatValue
		profile.ManualUpstreamMultiplier = nil
	} else {
		multiplierValue := decimal.NewFromFloat(multiplier).StringFixed(6)
		profile.ManualUpstreamMultiplier = &multiplierValue
	}
	lane.Profile = profile
	lane.PricingSourceGroupID = sourceGroupID
	sourceRevision := "sha256:" + canonicalDigest(map[string]any{
		"group_id":              source.ID,
		"rate_multiplier":       source.RateMultiplier,
		"image_rate_multiplier": source.ImageRateMultiplier,
		"video_rate_multiplier": source.VideoRateMultiplier,
		"image_prices":          map[string]any{"1K": source.ImagePrice1K, "2K": source.ImagePrice2K, "4K": source.ImagePrice4K},
		"video_prices":          map[string]any{"480p": source.VideoPrice480P, "720p": source.VideoPrice720P, "1080p": source.VideoPrice1080P},
	})
	lane.PricingSourceRevision = sourceRevision
	document = normalizeUnifiedGatewayDocument(document)
	profile = document.Lanes[laneIndex].Profile
	profileDigest := canonicalDigest(profile)
	return &UnifiedGatewayPricingImportResult{LaneID: lane.ID, SourceGroupID: sourceGroupID, SourceRevision: sourceRevision, Profile: profile, ImportDigest: "sha256:" + profileDigest, Draft: &UnifiedGatewayDraft{Document: document}}, nil
}

func (s *UnifiedGatewayAdminService) validateDocument(ctx context.Context, document UnifiedGatewayConfig) (*UnifiedGatewayValidationResult, error) {
	result := &UnifiedGatewayValidationResult{FieldErrors: map[string]string{}}
	if err := s.authorizeDocumentScope(ctx, document); err != nil {
		return nil, err
	}
	add := func(code, message, path string) {
		result.Blockers = append(result.Blockers, UnifiedGatewayIssue{Code: code, Message: message, Path: path})
		if path != "" {
			result.FieldErrors[path] = message
		}
	}
	warn := func(code, message, path string) {
		result.Warnings = append(result.Warnings, UnifiedGatewayIssue{Code: code, Message: message, Path: path})
	}
	groupID, groupErr := parseAccessGroupID(document.AccessGroupID)
	var scopedAccountIDs map[int64]struct{}
	var scopeRestricted bool
	if groupErr != nil {
		add("access_group_required", "access_group_id must be a positive opaque group id", "access_group_id")
	} else {
		_, scopeRestricted, scopeErr := s.scopeFilter(ctx)
		if scopeErr != nil {
			return nil, scopeErr
		}
		if scopeRestricted {
			reader, ok := s.groups.(unifiedGatewayGroupAccountReader)
			if s.groups == nil || s.accounts == nil || !ok {
				add("account_scope_unavailable", "account group scope is unavailable", "lanes")
			} else {
				accountIDs, err := reader.GetAccountIDsByGroupIDs(ctx, []int64{groupID})
				if err != nil {
					return nil, err
				}
				scopedAccountIDs = make(map[int64]struct{}, len(accountIDs))
				for _, accountID := range accountIDs {
					scopedAccountIDs[accountID] = struct{}{}
				}
			}
		}
		if s.groups != nil {
			groups, err := s.groups.ListActive(ctx)
			if err != nil {
				return nil, err
			}
			found := false
			for _, group := range groups {
				if group.ID == groupID {
					found = true
					break
				}
			}
			if !found {
				add("access_group_not_found", "access_group_id is not an active access group", "access_group_id")
			}
		}
	}
	if strings.TrimSpace(document.PublicModel) == "" {
		add("public_model_required", "public_model is required", "public_model")
	}
	if !validUnifiedEndpoint(document.Endpoint) {
		add("endpoint_unsupported", "endpoint is not supported by the unified admin contract", "endpoint")
	}
	if len(document.Lanes) == 0 {
		add("lane_required", "at least one billing lane is required", "lanes")
	}
	seenLane := map[string]bool{}
	accountIDs := make([]int64, 0)
	seenBinding := map[string]bool{}
	for i := range document.Lanes {
		lane := &document.Lanes[i]
		path := fmt.Sprintf("lanes[%d]", i)
		if strings.TrimSpace(lane.ID) == "" || seenLane[lane.ID] {
			add("lane_id_duplicate", "lane id must be present and unique", path+".id")
		}
		seenLane[lane.ID] = true
		if strings.TrimSpace(lane.Code) == "" {
			add("lane_code_required", "lane code is required", path+".code")
		}
		if lane.SelectionStrategy == "" {
			lane.SelectionStrategy = "fixed_priority"
		}
		if lane.SelectionStrategy != "fixed_priority" {
			add("selection_strategy_unsupported", "only fixed_priority is supported in v1", path+".selection_strategy")
		}
		validatePricingProfile(&result.Blockers, &result.Warnings, lane.Profile, path+".profile", document.Endpoint)
		if len(lane.Targets) == 0 {
			add("target_required", "lane must contain at least one target", path+".targets")
		}
		seenTarget := map[string]bool{}
		laneHasEnabledBinding := false
		for j := range lane.Targets {
			target := &lane.Targets[j]
			tpath := fmt.Sprintf("%s.targets[%d]", path, j)
			if strings.TrimSpace(target.ID) == "" || seenTarget[target.ID] {
				add("target_id_duplicate", "target id must be present and unique", tpath+".id")
			}
			seenTarget[target.ID] = true
			if strings.TrimSpace(target.ProviderIdentity) == "" {
				add("provider_required", "provider_identity is required", tpath+".provider_identity")
			}
			if strings.TrimSpace(target.UpstreamModel) == "" {
				add("upstream_model_required", "upstream_model is required", tpath+".upstream_model")
			}
			if target.Endpoint == "" {
				target.Endpoint = document.Endpoint
			}
			if !validUnifiedEndpoint(target.Endpoint) {
				add("target_endpoint_unsupported", "target endpoint is not supported by the unified admin contract", tpath+".endpoint")
			}
			if len(target.Bindings) == 0 {
				add("binding_required", "target must contain at least one account binding", tpath+".bindings")
			}
			for k := range target.Bindings {
				binding := &target.Bindings[k]
				bpath := fmt.Sprintf("%s.bindings[%d]", tpath, k)
				if strings.TrimSpace(binding.ID) == "" || seenBinding[binding.ID] {
					add("binding_id_duplicate", "binding id must be present and unique", bpath+".id")
				}
				seenBinding[binding.ID] = true
				accountID, err := parseOpaqueNumericID(binding.AccountID, "acct")
				if err != nil || accountID <= 0 {
					add("account_required", "account_id must be a positive account id", bpath+".account_id")
				} else {
					accountIDs = append(accountIDs, accountID)
				}
				if binding.Enabled && binding.Eligibility != "" && binding.Eligibility != "eligible" {
					add("account_ineligible", "enabled binding is not eligible", bpath+".eligibility")
				}
				if binding.Enabled && !binding.Schedulable {
					add("account_unschedulable", "enabled binding must be schedulable", bpath+".schedulable")
				}
				if binding.Enabled && binding.Eligibility == "eligible" && binding.Schedulable {
					laneHasEnabledBinding = true
				}
			}
		}
		if !laneHasEnabledBinding {
			add("schedulable_binding_required", "lane must contain at least one enabled eligible schedulable binding", path+".targets")
		}
	}
	if s.accounts != nil && len(accountIDs) > 0 {
		seen := map[int64]bool{}
		unique := make([]int64, 0, len(accountIDs))
		for _, id := range accountIDs {
			if !seen[id] {
				seen[id] = true
				unique = append(unique, id)
			}
		}
		accounts, err := s.accounts.GetByIDs(ctx, unique)
		if err != nil {
			return nil, err
		}
		byID := map[int64]*Account{}
		for _, account := range accounts {
			if account != nil {
				byID[account.ID] = account
			}
		}
		for i := range document.Lanes {
			for j := range document.Lanes[i].Targets {
				for k := range document.Lanes[i].Targets[j].Bindings {
					b := &document.Lanes[i].Targets[j].Bindings[k]
					id, _ := parseOpaqueNumericID(b.AccountID, "acct")
					account := byID[id]
					if account == nil {
						add("account_not_found", "account does not exist", fmt.Sprintf("lanes[%d].targets[%d].bindings[%d].account_id", i, j, k))
						continue
					}
					if scopeRestricted {
						if _, ok := scopedAccountIDs[id]; !ok {
							add("account_outside_access_group", "account is not bound to the selected access group", fmt.Sprintf("lanes[%d].targets[%d].bindings[%d].account_id", i, j, k))
						}
					}
					b.DisplayName = maskAccountName(account.Name, account.ID)
					b.Schedulable = account.Schedulable
					if !account.IsActive() {
						add("account_inactive", "account is not active", fmt.Sprintf("lanes[%d].targets[%d].bindings[%d]", i, j, k))
					}
					if b.Enabled && !account.Schedulable {
						add("account_unschedulable", "account is not schedulable", fmt.Sprintf("lanes[%d].targets[%d].bindings[%d]", i, j, k))
					}
				}
			}
		}
	}
	for i := range document.Lanes {
		profile := document.Lanes[i].Profile
		if profile.RateMode == "probe_preferred" && !profileHasFallback(profile) && !laneHasFreshProbeAt(document.Lanes[i], profile.RateBasis, s.nowUTC()) {
			add("probe_or_manual_price_required", "probe_preferred needs a fresh compatible probe or manual fallback", fmt.Sprintf("lanes[%d].profile", i))
		}
		if profile.RateMode == "probe_preferred" && profileHasFallback(profile) && !laneHasFreshProbeAt(document.Lanes[i], profile.RateBasis, s.nowUTC()) {
			warn("probe_fallback", "probe is unavailable; manual fallback will be used", fmt.Sprintf("lanes[%d].profile", i))
		}
		if profile.RateMode == "probe_only" && !laneHasFreshProbeAt(document.Lanes[i], profile.RateBasis, s.nowUTC()) {
			add("probe_required", "probe_only needs a fresh compatible probe", fmt.Sprintf("lanes[%d].profile.rate_mode", i))
		}
	}
	result.Valid = len(result.Blockers) == 0
	return result, nil
}

func validatePricingProfile(blockers, warnings *[]UnifiedGatewayIssue, profile UnifiedGatewayPricingProfile, path, endpoint string) {
	add := func(code, message, field string) {
		*blockers = append(*blockers, UnifiedGatewayIssue{Code: code, Message: message, Path: path + field})
	}
	if !validPricingModel(profile.PricingModel) {
		add("pricing_model_unsupported", "pricing_model is not supported", ".pricing_model")
	}
	if !validBillingMode(profile.BillingMode) {
		add("billing_mode_unsupported", "billing_mode is not supported", ".billing_mode")
	}
	if !validRateMode(profile.RateMode) {
		add("rate_mode_unsupported", "rate_mode is not supported", ".rate_mode")
	}
	if !validRateBasis(profile.RateBasis) {
		add("rate_basis_unsupported", "rate_basis is not supported", ".rate_basis")
	}
	if !validPricingSchema(profile.PricingSchemaID) {
		add("pricing_schema_unsupported", "pricing_schema_id is not supported", ".pricing_schema_id")
	}
	if profile.PricingSchemaID == "flat_unit_price_v1" && profile.BillingMode == "token" {
		add("pricing_schema_billing_mismatch", "flat_unit_price_v1 is not valid for token billing", ".pricing_schema_id")
	}
	if profile.BillingMode == "token" && profile.PricingSchemaID != "token_v1" {
		add("pricing_schema_billing_mismatch", "token billing requires token_v1", ".pricing_schema_id")
	}
	if profile.BillingMode == "per_request" && profile.PricingSchemaID != "request_v1" && profile.PricingSchemaID != "flat_unit_price_v1" {
		add("pricing_schema_billing_mismatch", "per_request billing requires request_v1 or flat_unit_price_v1", ".pricing_schema_id")
	}
	if profile.BillingMode == "image" && profile.PricingSchemaID != "image_delivery_v1" && profile.PricingSchemaID != "flat_unit_price_v1" {
		add("pricing_schema_billing_mismatch", "image billing requires image_delivery_v1 or flat_unit_price_v1", ".pricing_schema_id")
	}
	if profile.BillingMode == "video" && profile.PricingSchemaID != "video_delivery_v1" && profile.PricingSchemaID != "flat_unit_price_v1" {
		add("pricing_schema_billing_mismatch", "video billing requires video_delivery_v1 or flat_unit_price_v1", ".pricing_schema_id")
	}
	if profile.Currency != "USD" {
		add("currency_unsupported", "only USD is supported", ".currency")
	}
	if profile.RoundingMode != UnifiedGatewayRoundingHalfUp {
		add("rounding_mode_unsupported", "rounding_mode must be half_up", ".rounding_mode")
	}
	if profile.Precision < 0 || profile.Precision > 12 {
		add("precision_invalid", "precision must be between 0 and 12", ".precision")
	}
	if profile.ChargeTrigger != UnifiedGatewayChargeTriggerSuccess {
		add("charge_trigger_invalid", "charge_trigger must be success_delivery", ".charge_trigger")
	}
	if profile.FailureCharge != UnifiedGatewayFailureChargeZero {
		add("failure_charge_invalid", "failure_charge must be zero", ".failure_charge")
	}
	if profile.BasePriceSemantics != "provider_base" && profile.BasePriceSemantics != "final_user_price" {
		add("base_semantics_invalid", "base_price_semantics is invalid", ".base_price_semantics")
	}
	if endpoint == UnifiedGatewayEndpointImages && profile.BillingMode != "image" {
		add("billing_mode_endpoint_mismatch", "images endpoint requires image billing", ".billing_mode")
	}
	if endpoint == UnifiedGatewayEndpointVideos && profile.BillingMode != "video" {
		add("billing_mode_endpoint_mismatch", "videos endpoint requires video billing", ".billing_mode")
	}
	if (endpoint == UnifiedGatewayEndpointChatCompletions || endpoint == UnifiedGatewayEndpointResponses) && profile.BillingMode != "token" && profile.BillingMode != "per_request" {
		add("billing_mode_endpoint_mismatch", "chat/responses endpoint requires token or per_request billing", ".billing_mode")
	}
	if profile.RateBasis != "provider_specific" && profile.RateBasis != profile.BillingMode && !(profile.BillingMode == "per_request" && profile.RateBasis == "per_request") {
		add("rate_basis_billing_mismatch", "rate_basis must match billing_mode unless provider_specific", ".rate_basis")
	}
	if profile.FallbackReason != nil && strings.TrimSpace(*profile.FallbackReason) == "" {
		*warnings = append(*warnings, UnifiedGatewayIssue{Code: "fallback_reason_empty", Message: "fallback_reason is empty and will not be displayed", Path: path + ".fallback_reason"})
	}
	if (profile.RateMode == "manual_only" || profile.RateMode == "probe_preferred") && (profile.FallbackReason == nil || strings.TrimSpace(valueOrString(profile.FallbackReason)) == "") {
		add("fallback_reason_required", "manual pricing or fallback requires an operator reason", ".fallback_reason")
	}
	for field, value := range map[string]*string{"provider_base_unit_price": profile.ProviderBaseUnitPrice, "manual_base_unit_price": profile.ManualBaseUnitPrice, "manual_upstream_multiplier": profile.ManualUpstreamMultiplier, "user_markup_multiplier": profile.UserMarkupMultiplier, "final_user_unit_price": profile.FinalUserUnitPrice, "fixed_fee": profile.FixedFee, "minimum_charge": profile.MinimumCharge} {
		if value == nil {
			continue
		}
		parsed, err := decimal.NewFromString(strings.TrimSpace(*value))
		if err != nil || parsed.IsNegative() {
			add("decimal_invalid", "value must be a non-negative decimal string", "."+field)
		}
	}
	if profile.ManualPricingRules != nil && profile.ManualPricingRules.FormulaID != "flat_unit_price" {
		add("manual_formula_unsupported", "only flat_unit_price is allowed", ".manual_pricing_rules.formula_id")
	}
	if profile.ManualPricingRules != nil {
		rule := profile.ManualPricingRules
		if profile.PricingSchemaID != "flat_unit_price_v1" {
			add("manual_formula_schema_mismatch", "manual_pricing_rules requires flat_unit_price_v1", ".manual_pricing_rules")
		}
		if profile.RateMode != "manual_only" {
			add("manual_formula_rate_mode_mismatch", "flat_unit_price_v1 requires manual_only", ".rate_mode")
		}
		if rule.Unit != "request" && rule.Unit != "image" && rule.Unit != "video_task" {
			add("manual_formula_unit_invalid", "manual pricing unit is invalid", ".manual_pricing_rules.unit")
		}
		expectedUnit := map[string]string{"per_request": "request", "image": "image", "video": "video_task"}[profile.BillingMode]
		if expectedUnit == "" || rule.Unit != expectedUnit {
			add("manual_formula_unit_mismatch", "manual pricing unit must match billing mode", ".manual_pricing_rules.unit")
		}
		if rule.UnitPrice == nil {
			add("manual_formula_price_required", "manual pricing unit_price is required", ".manual_pricing_rules.unit_price")
		} else if value, err := decimal.NewFromString(strings.TrimSpace(*rule.UnitPrice)); err != nil || value.IsNegative() {
			add("decimal_invalid", "manual pricing unit_price must be a non-negative decimal string", ".manual_pricing_rules.unit_price")
		}
		if profile.BasePriceSemantics != "final_user_price" {
			add("manual_formula_semantics_mismatch", "flat_unit_price_v1 requires final_user_price semantics", ".base_price_semantics")
		}
		if profile.FinalUserUnitPrice != nil || profile.ProviderBaseUnitPrice != nil || profile.ManualBaseUnitPrice != nil || profile.ManualUpstreamMultiplier != nil || profile.UserMarkupMultiplier != nil {
			add("manual_formula_double_counting", "flat unit price cannot be combined with base or multiplier fields", ".manual_pricing_rules")
		}
	} else if profile.PricingSchemaID == "flat_unit_price_v1" {
		add("manual_formula_required", "flat_unit_price_v1 requires manual_pricing_rules", ".manual_pricing_rules")
	}
	if profile.BasePriceSemantics == "final_user_price" {
		if profile.FinalUserUnitPrice == nil && profile.ManualPricingRules == nil {
			add("final_price_required", "final_user_unit_price is required", ".final_user_unit_price")
		}
		if profile.ManualPricingRules == nil && (profile.ProviderBaseUnitPrice != nil || profile.ManualBaseUnitPrice != nil || profile.ManualUpstreamMultiplier != nil || profile.UserMarkupMultiplier != nil) {
			add("double_counting_risk", "final user price cannot be combined with base or multiplier fields", ".")
		}
	} else {
		if profile.ProviderBaseUnitPrice == nil && profile.ManualBaseUnitPrice == nil {
			add("base_price_required", "provider_base requires a provider or manual base unit price", ".provider_base_unit_price")
		}
		if profile.UserMarkupMultiplier == nil {
			add("markup_required", "provider_base requires user_markup_multiplier", ".user_markup_multiplier")
		}
		if profile.RateMode == "manual_only" && profile.ManualBaseUnitPrice == nil && profile.ManualUpstreamMultiplier == nil && profile.ManualPricingRules == nil {
			add("manual_fallback_required", "manual_only requires a manual base, multiplier or whitelisted rule", ".")
		}
	}
}

func (s *UnifiedGatewayAdminService) findIdempotent(ctx context.Context, actorID, operation, resourceID, key string, payload any) (*UnifiedGatewayIdempotencyRecord, error) {
	key = strings.TrimSpace(key)
	if !validIdempotencyKey(key) {
		return nil, adminError(http.StatusBadRequest, "UNIFIED_GATEWAY_IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key must contain 1-128 printable ASCII characters", nil, ErrUnifiedGatewayAdminKeyRequired)
	}
	digest := canonicalDigest(payload)
	existing, err := s.repo.GetIdempotency(ctx, actorID, operation, resourceID, key)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, nil
	}
	if existing.RequestDigest != digest {
		return nil, adminError(http.StatusConflict, "UNIFIED_GATEWAY_IDEMPOTENCY_CONFLICT", "the idempotency key was already used with a different request", map[string]string{"operation": operation, "resource_id": resourceID}, ErrUnifiedGatewayAdminIdempotency)
	}
	return existing, nil
}

func (s *UnifiedGatewayAdminService) saveIdempotent(ctx context.Context, actorID, operation, resourceID, key string, payload any, status int, result any) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return s.repo.PutIdempotency(ctx, &UnifiedGatewayIdempotencyRecord{ActorID: actorID, Operation: operation, ResourceID: resourceID, Key: strings.TrimSpace(key), RequestDigest: canonicalDigest(payload), StatusCode: status, ResponseJSON: encoded, CreatedAt: s.nowUTC()})
}

func (s *UnifiedGatewayAdminService) newIdempotencyRecord(actorID, operation, resourceID, key string, payload any) *UnifiedGatewayIdempotencyRecord {
	return &UnifiedGatewayIdempotencyRecord{ActorID: actorID, Operation: operation, ResourceID: resourceID, Key: strings.TrimSpace(key), RequestDigest: canonicalDigest(payload), StatusCode: http.StatusOK, CreatedAt: s.nowUTC()}
}

func validateUnifiedEndpoint(endpoint string) bool {
	switch endpoint {
	case UnifiedGatewayEndpointChatCompletions, UnifiedGatewayEndpointResponses, UnifiedGatewayEndpointImages, UnifiedGatewayEndpointVideos:
		return true
	}
	return false
}
func validUnifiedEndpoint(endpoint string) bool {
	return validateUnifiedEndpoint(strings.TrimSpace(endpoint))
}
func validBillingMode(value string) bool {
	switch value {
	case "token", "per_request", "image", "video":
		return true
	}
	return false
}
func validRateMode(value string) bool {
	switch value {
	case "probe_preferred", "manual_only", "probe_only":
		return true
	}
	return false
}
func validRateBasis(value string) bool {
	switch value {
	case "token", "per_request", "image", "video", "provider_specific":
		return true
	}
	return false
}
func validPricingModel(value string) bool {
	switch value {
	case UnifiedGatewayPricingModelProviderMetered, UnifiedGatewayPricingModelAmortizedSubscription, UnifiedGatewayPricingModelFixedRequest, UnifiedGatewayPricingModelImage, UnifiedGatewayPricingModelVideo:
		return true
	}
	return false
}
func validPricingSchema(value string) bool {
	switch value {
	case "token_v1", "request_v1", "image_delivery_v1", "video_delivery_v1", "flat_unit_price_v1":
		return true
	}
	return false
}
func normalizePage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	return page, size
}
func validIdempotencyKey(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}
func opaqueID(prefix string) string {
	return prefix + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}
func opaqueNumericID(prefix string, id int64) string { return prefix + "_" + strconv.FormatInt(id, 10) }
func parseOpaqueNumericID(value, prefix string) (int64, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, prefix+"_") {
		value = strings.TrimPrefix(value, prefix+"_")
	}
	return strconv.ParseInt(value, 10, 64)
}
func parseAccessGroupID(value string) (int64, error) { return parseOpaqueNumericID(value, "ag") }
func maskAccountName(name string, id int64) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Account ••••" + strconv.FormatInt(id%1000, 10)
	}
	if len([]rune(name)) > 32 {
		return string([]rune(name)[:32]) + "…"
	}
	return name
}
func canonicalDigest(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
func validationToken(document UnifiedGatewayConfig) string {
	return "sha256:" + canonicalDigest(document)
}
func opaqueIDFromBigint(prefix string, id int64) string { return opaqueNumericID(prefix, id) }

func normalizeUnifiedGatewayDocument(document UnifiedGatewayConfig) UnifiedGatewayConfig {
	if document.Lifecycle == "" {
		document.Lifecycle = UnifiedGatewayLifecycleDraft
	}
	for i := range document.Lanes {
		lane := &document.Lanes[i]
		if lane.ID == "" {
			lane.ID = opaqueID("lane")
		}
		if lane.SelectionStrategy == "" {
			lane.SelectionStrategy = "fixed_priority"
		}
		if lane.Profile.ID == "" {
			lane.Profile.ID = opaqueID("profile")
		}
		if lane.Profile.Version == "" {
			lane.Profile.Version = "1"
		}
		if lane.Profile.Currency == "" {
			lane.Profile.Currency = "USD"
		}
		if lane.Profile.RoundingMode == "" {
			lane.Profile.RoundingMode = UnifiedGatewayRoundingHalfUp
		}
		if lane.Profile.ChargeTrigger == "" {
			lane.Profile.ChargeTrigger = UnifiedGatewayChargeTriggerSuccess
		}
		if lane.Profile.FailureCharge == "" {
			lane.Profile.FailureCharge = UnifiedGatewayFailureChargeZero
		}
		// Profiles are immutable versions.  Derive the persisted identity from
		// the lane and the profile content so editing a draft cannot overwrite a
		// previously published profile while retries remain deterministic.
		profileForDigest := lane.Profile
		profileForDigest.ID = ""
		lane.Profile.ID = "profile_" + canonicalDigest(map[string]any{"lane_id": lane.ID, "profile": profileForDigest})[:24]
		for j := range lane.Targets {
			target := &lane.Targets[j]
			if target.ID == "" {
				target.ID = opaqueID("target")
			}
			if target.Endpoint == "" {
				target.Endpoint = document.Endpoint
			}
			for k := range target.Bindings {
				binding := &target.Bindings[k]
				if binding.ID == "" {
					binding.ID = opaqueID("binding")
				}
				if binding.Eligibility == "" {
					binding.Eligibility = "unknown"
				}
			}
		}
	}
	return document
}

func versionError(revision int64, kind, id string) error {
	return adminError(http.StatusConflict, "UNIFIED_GATEWAY_VERSION_CONFLICT", "resource revision does not match If-Match", map[string]string{"resource_kind": kind, "resource_id": id, "expected_revision": strconv.FormatInt(revision, 10)}, ErrUnifiedGatewayAdminVersion)
}
func parseIfMatch(value string) (int64, error) {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"")
	if value == "" {
		return 0, adminError(http.StatusBadRequest, "UNIFIED_GATEWAY_IF_MATCH_REQUIRED", "If-Match revision is required", nil, ErrUnifiedGatewayAdminVersion)
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, adminError(http.StatusBadRequest, "UNIFIED_GATEWAY_IF_MATCH_INVALID", "If-Match must contain a non-negative revision", nil, ErrUnifiedGatewayAdminVersion)
	}
	return parsed, nil
}

func mapAdminRepoError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrUnifiedGatewayAdminNotFound) || errors.Is(err, ErrUnifiedGatewayRouteNotFound) {
		return adminError(http.StatusNotFound, "UNIFIED_GATEWAY_NOT_FOUND", "unified gateway resource not found", nil, err)
	}
	if errors.Is(err, ErrUnifiedGatewayAdminVersion) {
		return versionError(0, "resource", "")
	}
	if errors.Is(err, ErrUnifiedGatewayAdminIdempotency) {
		return adminError(http.StatusConflict, "UNIFIED_GATEWAY_IDEMPOTENCY_CONFLICT", "idempotency conflict", nil, err)
	}
	return err
}

func (s *UnifiedGatewayAdminService) decorateConfig(config UnifiedGatewayConfig) UnifiedGatewayConfig {
	config.RuntimeEffective = s.runtimeEnabled() && config.Lifecycle == UnifiedGatewayLifecyclePublished && config.Readiness == UnifiedGatewayReadinessReady
	return config
}

func validateProbeTime(probe *UnifiedGatewayAdminProbe, basis string) bool {
	return validateProbeTimeAt(probe, basis, time.Now().UTC())
}

func validateProbeTimeAt(probe *UnifiedGatewayAdminProbe, basis string, now time.Time) bool {
	if probe == nil || probe.Status != "ok" || probe.Basis != basis || probe.ResolvedRateMultiplier == nil {
		return false
	}
	value, err := decimal.NewFromString(strings.TrimSpace(*probe.ResolvedRateMultiplier))
	if err != nil || value.LessThanOrEqual(decimal.Zero) {
		return false
	}
	if strings.TrimSpace(probe.FreshUntil) == "" {
		return false
	}
	until, err := time.Parse(time.RFC3339, probe.FreshUntil)
	if err != nil || !until.After(now.UTC()) {
		return false
	}
	return true
}

func validProbeMultiplier(probe *UnifiedGatewayAdminProbe, basis string) (*decimal.Decimal, string, string) {
	return validProbeMultiplierAt(probe, basis, time.Now().UTC())
}

func validProbeMultiplierAt(probe *UnifiedGatewayAdminProbe, basis string, now time.Time) (*decimal.Decimal, string, string) {
	if !validateProbeTimeAt(probe, basis, now) {
		if probe == nil {
			return nil, "missing", ""
		}
		return nil, probe.Status, probe.SnapshotRef
	}
	value, _ := decimal.NewFromString(*probe.ResolvedRateMultiplier)
	return &value, probe.Status, probe.SnapshotRef
}
func laneHasFreshProbe(lane UnifiedGatewayBillingLane, basis string) bool {
	return laneHasFreshProbeAt(lane, basis, time.Now().UTC())
}
func laneHasFreshProbeAt(lane UnifiedGatewayBillingLane, basis string, now time.Time) bool {
	for _, target := range lane.Targets {
		for _, binding := range target.Bindings {
			if value, _, _ := validProbeMultiplierAt(binding.Probe, basis, now); value != nil {
				return true
			}
		}
	}
	return false
}
func profileHasFallback(profile UnifiedGatewayPricingProfile) bool {
	return profile.ManualBaseUnitPrice != nil || profile.ManualUpstreamMultiplier != nil || profile.FinalUserUnitPrice != nil || profile.ManualPricingRules != nil
}

func valueOrString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func previewDecimal(value *string) (*decimal.Decimal, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}
	parsed, err := decimal.NewFromString(strings.TrimSpace(*value))
	if err != nil || parsed.IsNegative() {
		return nil, adminError(http.StatusUnprocessableEntity, "UNIFIED_GATEWAY_DECIMAL_INVALID", "price and multiplier values must be non-negative decimal strings", nil, ErrUnifiedGatewayAdminValidation)
	}
	return &parsed, nil
}

func manualRuleUnitPrice(profile UnifiedGatewayPricingProfile) (*decimal.Decimal, error) {
	if profile.ManualPricingRules == nil || profile.ManualPricingRules.UnitPrice == nil {
		return nil, nil
	}
	value, err := previewDecimal(profile.ManualPricingRules.UnitPrice)
	if err != nil {
		return nil, err
	}
	return value, nil
}
func valueOrZeroAdmin(value *decimal.Decimal) decimal.Decimal {
	if value == nil {
		return decimal.Zero
	}
	return *value
}

func locateUnifiedGatewayBinding(document UnifiedGatewayConfig, laneID, targetID, bindingID string) (UnifiedGatewayBillingLane, UnifiedGatewayAdminRouteTarget, UnifiedGatewayAdminAccountBinding, error) {
	for _, lane := range document.Lanes {
		if lane.ID != laneID {
			continue
		}
		for _, target := range lane.Targets {
			if target.ID != targetID {
				continue
			}
			for _, binding := range target.Bindings {
				if binding.ID == bindingID {
					return lane, target, binding, nil
				}
			}
		}
	}
	return UnifiedGatewayBillingLane{}, UnifiedGatewayAdminRouteTarget{}, UnifiedGatewayAdminAccountBinding{}, adminError(http.StatusNotFound, "UNIFIED_GATEWAY_RESOURCE_SCOPE_DENIED", "lane, target or binding was not found in this config", nil, ErrUnifiedGatewayAdminNotFound)
}

func previewBillableUnits(mode string, request UnifiedGatewayPreviewRequest) (decimal.Decimal, map[string]string, error) {
	units := map[string]string{}
	if mode == "token" {
		input, err := previewDecimal(request.InputTokens)
		if err != nil {
			return decimal.Zero, nil, err
		}
		output, err := previewDecimal(request.OutputTokens)
		if err != nil {
			return decimal.Zero, nil, err
		}
		inputValue, outputValue := valueOrZeroAdmin(input), valueOrZeroAdmin(output)
		total := inputValue.Add(outputValue)
		if total.LessThanOrEqual(decimal.Zero) {
			return decimal.Zero, nil, adminError(http.StatusBadRequest, "UNIFIED_GATEWAY_UNITS_REQUIRED", "token preview needs input_tokens or output_tokens", nil, ErrUnifiedGatewayAdminInvalidRequest)
		}
		units["input_tokens"] = inputValue.String()
		units["output_tokens"] = outputValue.String()
		units["total_tokens"] = total.String()
		return total, units, nil
	}
	value, err := previewDecimal(request.Units)
	if err != nil {
		return decimal.Zero, nil, err
	}
	if value == nil || value.LessThanOrEqual(decimal.Zero) {
		value = ptrDecimal(decimal.NewFromInt(1))
	}
	units["units"] = value.String()
	return *value, units, nil
}
func ptrDecimal(value decimal.Decimal) *decimal.Decimal { return &value }
