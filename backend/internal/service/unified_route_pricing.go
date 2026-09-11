package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// UnifiedRateMode controls where a unified route obtains its upstream price
// basis. It is intentionally separate from accounts.rate_multiplier, whose
// existing meaning is internal account-cost accounting.
type UnifiedRateMode string

const (
	UnifiedRateModeProbePreferred UnifiedRateMode = "probe_preferred"
	UnifiedRateModeManualOnly     UnifiedRateMode = "manual_only"
	UnifiedRateModeProbeOnly      UnifiedRateMode = "probe_only"
)

// UnifiedRateBasis is the unit to which an upstream rate applies.
type UnifiedRateBasis string

const (
	UnifiedRateBasisToken            UnifiedRateBasis = "token"
	UnifiedRateBasisPerRequest       UnifiedRateBasis = "per_request"
	UnifiedRateBasisImage            UnifiedRateBasis = "image"
	UnifiedRateBasisVideo            UnifiedRateBasis = "video"
	UnifiedRateBasisProviderSpecific UnifiedRateBasis = "provider_specific"
)

// UnifiedBasePriceSemantics prevents a final sale price from being multiplied
// a second time by an upstream or user multiplier.
type UnifiedBasePriceSemantics string

const (
	UnifiedBasePriceProviderBase UnifiedBasePriceSemantics = "provider_base"
	UnifiedBasePriceFinalUser    UnifiedBasePriceSemantics = "final_user_price"
)

type UnifiedRateSource string

const (
	UnifiedRateSourceProbeDeclared  UnifiedRateSource = "probe_declared"
	UnifiedRateSourceManualFallback UnifiedRateSource = "manual_fallback"
	UnifiedRateSourceManualOnly     UnifiedRateSource = "manual_only"
)

const (
	UnifiedProbeStatusOK          = "ok"
	UnifiedProbeStatusUnsupported = "unsupported"
	UnifiedProbeStatusFailed      = "failed"
	UnifiedProbeStatusStale       = "stale"
)

var (
	ErrUnifiedPricingPolicyMissing   = errors.New("unified route pricing policy is missing")
	ErrUnifiedPricingProbeInvalid    = errors.New("unified route pricing probe is invalid")
	ErrUnifiedPricingBasisMismatch   = errors.New("unified route pricing basis mismatch")
	ErrUnifiedPricingBaseMissing     = errors.New("unified route pricing base price is missing")
	ErrUnifiedPricingInvalidRate     = errors.New("unified route pricing rate is invalid")
	ErrUnifiedPricingDoubleCounting  = errors.New("unified route pricing would double count a final price")
	ErrUnifiedPricingBillingMismatch = errors.New("unified route pricing billing mode mismatch")
)

// UnifiedRateRule is one versioned pricing rule. A rule may be partial when
// it is an account or pool override; missing fields inherit from the less
// specific rule. Numeric fields are pointers so an omitted value is different
// from an explicit value and cannot silently become an upstream 1.0.
type UnifiedRateRule struct {
	ProfileID                string                    `json:"profile_id,omitempty"`
	Version                  string                    `json:"version,omitempty"`
	BillingMode              string                    `json:"billing_mode,omitempty"`
	UpstreamRateBasis        UnifiedRateBasis          `json:"upstream_rate_basis,omitempty"`
	BasePriceSemantics       UnifiedBasePriceSemantics `json:"base_price_semantics,omitempty"`
	ProviderBaseUnitPrice    *float64                  `json:"provider_base_unit_price,omitempty"`
	ManualBaseUnitPrice      *float64                  `json:"manual_base_unit_price,omitempty"`
	FinalUserUnitPrice       *float64                  `json:"final_user_unit_price,omitempty"`
	ManualUpstreamMultiplier *float64                  `json:"manual_upstream_multiplier,omitempty"`
	UserMarkupMultiplier     *float64                  `json:"user_markup_multiplier,omitempty"`
	FixedFee                 *float64                  `json:"fixed_fee,omitempty"`
	RoundingPrecision        int                       `json:"rounding_precision,omitempty"`
}

// UnifiedProbeSnapshot is the already-collected upstream declaration. The
// pricing resolver never performs network I/O; probe collection stays in the
// existing upstream billing probe subsystem.
type UnifiedProbeSnapshot struct {
	Status                 string           `json:"status"`
	Basis                  UnifiedRateBasis `json:"basis"`
	ResolvedRateMultiplier *float64         `json:"resolved_rate_multiplier,omitempty"`
	SnapshotRef            string           `json:"snapshot_ref,omitempty"`
	ReceivedAt             time.Time        `json:"received_at,omitempty"`
	FreshUntil             time.Time        `json:"fresh_until,omitempty"`
}

// UnifiedRoutePricingInput is evaluated after the concrete route/account has
// been selected and before the upstream request is sent.
type UnifiedRoutePricingInput struct {
	RouteID          int64                 `json:"route_id"`
	BillingLaneID    string                `json:"billing_lane_id,omitempty"`
	AccountID        int64                 `json:"account_id"`
	ProviderIdentity string                `json:"provider_identity"`
	PublicModel      string                `json:"public_model"`
	UpstreamModel    string                `json:"upstream_model"`
	Endpoint         string                `json:"endpoint"`
	BillingMode      string                `json:"billing_mode"`
	RateMode         UnifiedRateMode       `json:"rate_mode"`
	RateBasis        UnifiedRateBasis      `json:"rate_basis"`
	Now              time.Time             `json:"now"`
	Probe            *UnifiedProbeSnapshot `json:"probe,omitempty"`
	LaneRule         *UnifiedRateRule      `json:"lane_rule,omitempty"`
	PoolRule         *UnifiedRateRule      `json:"pool_rule,omitempty"`
	AccountRule      *UnifiedRateRule      `json:"account_rule,omitempty"`
}

// UnifiedRoutePriceSnapshot is the immutable, request-scoped pricing fact
// produced by the pure resolver. It is deliberately not persisted here: the
// later unified gateway integration must persist this value before forwarding.
type UnifiedRoutePriceSnapshot struct {
	RouteID                 int64                     `json:"route_id"`
	BillingLaneID           string                    `json:"billing_lane_id,omitempty"`
	AccountID               int64                     `json:"account_id"`
	ProviderIdentity        string                    `json:"provider_identity"`
	PublicModel             string                    `json:"public_model"`
	UpstreamModel           string                    `json:"upstream_model"`
	Endpoint                string                    `json:"endpoint"`
	ProfileID               string                    `json:"profile_id"`
	PolicyVersion           string                    `json:"policy_version"`
	RuleScope               string                    `json:"rule_scope"`
	BillingMode             string                    `json:"billing_mode"`
	RateMode                UnifiedRateMode           `json:"rate_mode"`
	RateBasis               UnifiedRateBasis          `json:"rate_basis"`
	RateSource              UnifiedRateSource         `json:"rate_source"`
	ProbeSnapshotRef        string                    `json:"probe_snapshot_ref,omitempty"`
	ProbeReceivedAt         time.Time                 `json:"probe_received_at,omitempty"`
	ProbeFreshUntil         time.Time                 `json:"probe_fresh_until,omitempty"`
	ManualFallbackUsed      bool                      `json:"manual_fallback_used"`
	BasePriceSemantics      UnifiedBasePriceSemantics `json:"base_price_semantics"`
	ProviderBaseUnitPrice   float64                   `json:"provider_base_unit_price"`
	EffectiveRateMultiplier *float64                  `json:"effective_rate_multiplier,omitempty"`
	UserMarkupMultiplier    float64                   `json:"user_markup_multiplier"`
	FixedFee                float64                   `json:"fixed_fee"`
	ProviderUnitPrice       float64                   `json:"provider_unit_price"`
	UserUnitPrice           float64                   `json:"user_unit_price"`
	RoundingPrecision       int                       `json:"rounding_precision"`
	CalculationMode         string                    `json:"calculation_mode"`
	Digest                  string                    `json:"digest"`
}

// ResolveUnifiedRoutePrice validates and freezes one route price. It has no
// database or network dependency, so it can be used as a pre-forward gate.
func ResolveUnifiedRoutePrice(input UnifiedRoutePricingInput) (UnifiedRoutePriceSnapshot, error) {
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if strings.TrimSpace(input.ProviderIdentity) == "" || strings.TrimSpace(input.PublicModel) == "" || input.RateMode == "" || input.RateBasis == "" || strings.TrimSpace(input.BillingMode) == "" {
		return UnifiedRoutePriceSnapshot{}, fmt.Errorf("%w: route, provider, model, billing mode and rate basis are required", ErrUnifiedPricingPolicyMissing)
	}
	if err := validateUnifiedRateMode(input.RateMode); err != nil {
		return UnifiedRoutePriceSnapshot{}, err
	}
	if err := validateUnifiedRateBasis(input.RateBasis); err != nil {
		return UnifiedRoutePriceSnapshot{}, err
	}
	if err := validateUnifiedBillingBasis(input.BillingMode, input.RateBasis); err != nil {
		return UnifiedRoutePriceSnapshot{}, err
	}

	rule, scope := mergeUnifiedRateRules(input.LaneRule, input.PoolRule, input.AccountRule)
	if rule == nil {
		return UnifiedRoutePriceSnapshot{}, fmt.Errorf("%w: no lane, pool or account rule", ErrUnifiedPricingPolicyMissing)
	}
	if rule.UpstreamRateBasis != "" && rule.UpstreamRateBasis != input.RateBasis {
		return UnifiedRoutePriceSnapshot{}, fmt.Errorf("%w: rule=%s request=%s", ErrUnifiedPricingBasisMismatch, rule.UpstreamRateBasis, input.RateBasis)
	}
	if rule.BillingMode != "" && rule.BillingMode != input.BillingMode {
		return UnifiedRoutePriceSnapshot{}, fmt.Errorf("%w: rule=%s request=%s", ErrUnifiedPricingBillingMismatch, rule.BillingMode, input.BillingMode)
	}
	if rule.BasePriceSemantics == "" {
		return UnifiedRoutePriceSnapshot{}, fmt.Errorf("%w: base price semantics are required", ErrUnifiedPricingPolicyMissing)
	}
	if rule.BasePriceSemantics != UnifiedBasePriceProviderBase && rule.BasePriceSemantics != UnifiedBasePriceFinalUser {
		return UnifiedRoutePriceSnapshot{}, fmt.Errorf("%w: unsupported base price semantics %q", ErrUnifiedPricingPolicyMissing, rule.BasePriceSemantics)
	}

	probeValid := unifiedProbeIsFreshAndCompatible(input.Probe, input.RateBasis, now)
	rateSource := UnifiedRateSourceManualOnly
	manualFallbackUsed := false
	var effectiveMultiplier *float64
	if input.RateMode != UnifiedRateModeManualOnly && probeValid {
		value := *input.Probe.ResolvedRateMultiplier
		effectiveMultiplier = &value
		rateSource = UnifiedRateSourceProbeDeclared
	} else if input.RateMode == UnifiedRateModeProbeOnly {
		return UnifiedRoutePriceSnapshot{}, fmt.Errorf("%w: status=%s basis=%s", ErrUnifiedPricingProbeInvalid, probeStatus(input.Probe), probeBasis(input.Probe))
	} else if input.RateMode == UnifiedRateModeProbePreferred {
		manualFallbackUsed = true
		rateSource = UnifiedRateSourceManualFallback
		if rule.ManualUpstreamMultiplier != nil {
			value := *rule.ManualUpstreamMultiplier
			effectiveMultiplier = &value
		} else if rule.ManualBaseUnitPrice == nil && rule.FinalUserUnitPrice == nil {
			return UnifiedRoutePriceSnapshot{}, fmt.Errorf("%w: probe is unavailable and no manual fallback is configured", ErrUnifiedPricingProbeInvalid)
		}
	} else if input.RateMode == UnifiedRateModeManualOnly && rule.ManualUpstreamMultiplier != nil {
		value := *rule.ManualUpstreamMultiplier
		effectiveMultiplier = &value
	}

	if err := validateUnifiedMultiplier(effectiveMultiplier); err != nil {
		return UnifiedRoutePriceSnapshot{}, err
	}
	if rule.FixedFee != nil && (*rule.FixedFee < 0 || math.IsNaN(*rule.FixedFee) || math.IsInf(*rule.FixedFee, 0)) {
		return UnifiedRoutePriceSnapshot{}, fmt.Errorf("%w: fixed fee must be finite and non-negative", ErrUnifiedPricingInvalidRate)
	}
	if rule.RoundingPrecision < 0 || rule.RoundingPrecision > 12 {
		return UnifiedRoutePriceSnapshot{}, fmt.Errorf("%w: rounding precision must be between 0 and 12", ErrUnifiedPricingInvalidRate)
	}

	snapshot := UnifiedRoutePriceSnapshot{
		RouteID:              input.RouteID,
		BillingLaneID:        strings.TrimSpace(input.BillingLaneID),
		AccountID:            input.AccountID,
		ProviderIdentity:     strings.TrimSpace(input.ProviderIdentity),
		PublicModel:          strings.TrimSpace(input.PublicModel),
		UpstreamModel:        strings.TrimSpace(input.UpstreamModel),
		Endpoint:             strings.TrimSpace(input.Endpoint),
		ProfileID:            strings.TrimSpace(rule.ProfileID),
		PolicyVersion:        strings.TrimSpace(rule.Version),
		RuleScope:            scope,
		BillingMode:          input.BillingMode,
		RateMode:             input.RateMode,
		RateBasis:            input.RateBasis,
		RateSource:           rateSource,
		ManualFallbackUsed:   manualFallbackUsed,
		BasePriceSemantics:   rule.BasePriceSemantics,
		UserMarkupMultiplier: 1,
		RoundingPrecision:    rule.RoundingPrecision,
	}
	if input.Probe != nil {
		snapshot.ProbeSnapshotRef = strings.TrimSpace(input.Probe.SnapshotRef)
		snapshot.ProbeReceivedAt = input.Probe.ReceivedAt.UTC()
		snapshot.ProbeFreshUntil = input.Probe.FreshUntil.UTC()
	}
	if effectiveMultiplier != nil {
		value := *effectiveMultiplier
		snapshot.EffectiveRateMultiplier = &value
	}
	if rule.FixedFee != nil {
		snapshot.FixedFee = *rule.FixedFee
	}

	switch rule.BasePriceSemantics {
	case UnifiedBasePriceFinalUser:
		if rule.FinalUserUnitPrice == nil || *rule.FinalUserUnitPrice <= 0 {
			return UnifiedRoutePriceSnapshot{}, fmt.Errorf("%w: final user unit price is required", ErrUnifiedPricingBaseMissing)
		}
		if rule.UserMarkupMultiplier != nil || rule.ManualUpstreamMultiplier != nil || probeValid {
			return UnifiedRoutePriceSnapshot{}, fmt.Errorf("%w: final user price cannot accept another multiplier", ErrUnifiedPricingDoubleCounting)
		}
		snapshot.ProviderBaseUnitPrice = *rule.FinalUserUnitPrice
		snapshot.ProviderUnitPrice = *rule.FinalUserUnitPrice
		snapshot.UserUnitPrice = *rule.FinalUserUnitPrice
		snapshot.CalculationMode = "final_price_override"
	case UnifiedBasePriceProviderBase:
		basePrice, err := unifiedProviderBasePrice(*rule)
		if err != nil {
			return UnifiedRoutePriceSnapshot{}, err
		}
		if input.RateMode == UnifiedRateModeManualOnly && rule.ManualUpstreamMultiplier == nil && rule.ManualBaseUnitPrice == nil {
			return UnifiedRoutePriceSnapshot{}, fmt.Errorf("%w: manual-only route needs an explicit manual multiplier or unit price", ErrUnifiedPricingInvalidRate)
		}
		if rule.UserMarkupMultiplier == nil || *rule.UserMarkupMultiplier <= 0 {
			return UnifiedRoutePriceSnapshot{}, fmt.Errorf("%w: explicit user markup multiplier is required", ErrUnifiedPricingInvalidRate)
		}
		snapshot.ProviderBaseUnitPrice = basePrice
		snapshot.UserMarkupMultiplier = *rule.UserMarkupMultiplier
		if effectiveMultiplier != nil {
			snapshot.ProviderUnitPrice = basePrice * *effectiveMultiplier
		} else {
			snapshot.ProviderUnitPrice = basePrice
		}
		snapshot.UserUnitPrice = snapshot.ProviderUnitPrice * snapshot.UserMarkupMultiplier
		snapshot.CalculationMode = "provider_base_with_markup"
	}
	if !isFinitePositive(snapshot.ProviderUnitPrice) || !isFinitePositive(snapshot.UserUnitPrice) {
		return UnifiedRoutePriceSnapshot{}, fmt.Errorf("%w: calculated unit price is not positive", ErrUnifiedPricingInvalidRate)
	}
	snapshot.Digest = unifiedRoutePriceDigest(snapshot)
	return snapshot, nil
}

func unifiedProviderBasePrice(rule UnifiedRateRule) (float64, error) {
	if rule.ManualBaseUnitPrice != nil && *rule.ManualBaseUnitPrice > 0 {
		return *rule.ManualBaseUnitPrice, nil
	}
	if rule.ProviderBaseUnitPrice != nil && *rule.ProviderBaseUnitPrice > 0 {
		return *rule.ProviderBaseUnitPrice, nil
	}
	return 0, fmt.Errorf("%w: provider or manual base unit price is required", ErrUnifiedPricingBaseMissing)
}

// Charge returns zero unless the caller has a successful, delivered result.
// The caller must pass the measured units for the same basis as the snapshot.
func (snapshot UnifiedRoutePriceSnapshot) Charge(measuredUnits float64, delivered bool) float64 {
	if !delivered || measuredUnits <= 0 || !isFinitePositive(snapshot.UserUnitPrice) {
		return 0
	}
	charge := snapshot.UserUnitPrice*measuredUnits + snapshot.FixedFee
	if snapshot.RoundingPrecision <= 0 {
		return charge
	}
	factor := math.Pow10(snapshot.RoundingPrecision)
	return math.Round(charge*factor) / factor
}

// WithUnifiedRoutePriceSnapshot keeps the resolved snapshot available to a
// later adapter/billing integration without changing legacy context values.
func WithUnifiedRoutePriceSnapshot(ctx context.Context, snapshot UnifiedRoutePriceSnapshot) context.Context {
	if ctx == nil || snapshot.Digest == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxkey.UnifiedRoutePriceSnapshot, snapshot)
}

func UnifiedRoutePriceSnapshotFromContext(ctx context.Context) (UnifiedRoutePriceSnapshot, bool) {
	if ctx == nil {
		return UnifiedRoutePriceSnapshot{}, false
	}
	snapshot, ok := ctx.Value(ctxkey.UnifiedRoutePriceSnapshot).(UnifiedRoutePriceSnapshot)
	if !ok || snapshot.Digest == "" {
		return UnifiedRoutePriceSnapshot{}, false
	}
	return snapshot, true
}

func mergeUnifiedRateRules(lane, pool, account *UnifiedRateRule) (*UnifiedRateRule, string) {
	if lane == nil && pool == nil && account == nil {
		return nil, ""
	}
	merged := UnifiedRateRule{}
	scope := "lane"
	for _, item := range []*UnifiedRateRule{lane, pool, account} {
		if item == nil {
			continue
		}
		mergeUnifiedRateRule(&merged, item)
		if item == pool {
			scope = "pool"
		}
		if item == account {
			scope = "account"
		}
	}
	return &merged, scope
}

func mergeUnifiedRateRule(dst, src *UnifiedRateRule) {
	if src == nil {
		return
	}
	if src.ProfileID != "" {
		dst.ProfileID = src.ProfileID
	}
	if src.Version != "" {
		dst.Version = src.Version
	}
	if src.BillingMode != "" {
		dst.BillingMode = src.BillingMode
	}
	if src.UpstreamRateBasis != "" {
		dst.UpstreamRateBasis = src.UpstreamRateBasis
	}
	if src.BasePriceSemantics != "" {
		dst.BasePriceSemantics = src.BasePriceSemantics
	}
	if src.ProviderBaseUnitPrice != nil {
		dst.ProviderBaseUnitPrice = src.ProviderBaseUnitPrice
	}
	if src.ManualBaseUnitPrice != nil {
		dst.ManualBaseUnitPrice = src.ManualBaseUnitPrice
	}
	if src.FinalUserUnitPrice != nil {
		dst.FinalUserUnitPrice = src.FinalUserUnitPrice
	}
	if src.ManualUpstreamMultiplier != nil {
		dst.ManualUpstreamMultiplier = src.ManualUpstreamMultiplier
	}
	if src.UserMarkupMultiplier != nil {
		dst.UserMarkupMultiplier = src.UserMarkupMultiplier
	}
	if src.FixedFee != nil {
		dst.FixedFee = src.FixedFee
	}
	if src.RoundingPrecision != 0 {
		dst.RoundingPrecision = src.RoundingPrecision
	}
}

func unifiedProbeIsFreshAndCompatible(probe *UnifiedProbeSnapshot, basis UnifiedRateBasis, now time.Time) bool {
	return probe != nil && strings.EqualFold(strings.TrimSpace(probe.Status), UnifiedProbeStatusOK) &&
		probe.Basis == basis && probe.ResolvedRateMultiplier != nil &&
		isFinitePositive(*probe.ResolvedRateMultiplier) && !probe.FreshUntil.IsZero() &&
		strings.TrimSpace(probe.SnapshotRef) != "" && !probe.ReceivedAt.IsZero() &&
		!now.After(probe.FreshUntil.UTC())
}

func probeStatus(probe *UnifiedProbeSnapshot) string {
	if probe == nil {
		return "missing"
	}
	return strings.TrimSpace(probe.Status)
}

func probeBasis(probe *UnifiedProbeSnapshot) UnifiedRateBasis {
	if probe == nil {
		return ""
	}
	return probe.Basis
}

func validateUnifiedRateMode(mode UnifiedRateMode) error {
	switch mode {
	case UnifiedRateModeProbePreferred, UnifiedRateModeManualOnly, UnifiedRateModeProbeOnly:
		return nil
	default:
		return fmt.Errorf("%w: unsupported rate mode %q", ErrUnifiedPricingPolicyMissing, mode)
	}
}

func validateUnifiedRateBasis(basis UnifiedRateBasis) error {
	switch basis {
	case UnifiedRateBasisToken, UnifiedRateBasisPerRequest, UnifiedRateBasisImage, UnifiedRateBasisVideo, UnifiedRateBasisProviderSpecific:
		return nil
	default:
		return fmt.Errorf("%w: unsupported rate basis %q", ErrUnifiedPricingBasisMismatch, basis)
	}
}

func validateUnifiedBillingBasis(billingMode string, basis UnifiedRateBasis) error {
	normalized := strings.ToLower(strings.TrimSpace(billingMode))
	switch normalized {
	case string(BillingModeToken):
		if basis != UnifiedRateBasisToken {
			return fmt.Errorf("%w: token billing requires token basis", ErrUnifiedPricingBillingMismatch)
		}
	case string(BillingModePerRequest):
		if basis != UnifiedRateBasisPerRequest && basis != UnifiedRateBasisProviderSpecific {
			return fmt.Errorf("%w: per-request billing requires per_request/provider_specific basis", ErrUnifiedPricingBillingMismatch)
		}
	case string(BillingModeImage):
		if basis != UnifiedRateBasisImage {
			return fmt.Errorf("%w: image billing requires image basis", ErrUnifiedPricingBillingMismatch)
		}
	case string(BillingModeVideo):
		if basis != UnifiedRateBasisVideo {
			return fmt.Errorf("%w: video billing requires video basis", ErrUnifiedPricingBillingMismatch)
		}
	default:
		return fmt.Errorf("%w: unsupported billing mode %q", ErrUnifiedPricingBillingMismatch, billingMode)
	}
	return nil
}

func validateUnifiedMultiplier(multiplier *float64) error {
	if multiplier == nil {
		return nil
	}
	if !isFinitePositive(*multiplier) {
		return fmt.Errorf("%w: multiplier must be finite and greater than zero", ErrUnifiedPricingInvalidRate)
	}
	return nil
}

func isFinitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func unifiedRoutePriceDigest(snapshot UnifiedRoutePriceSnapshot) string {
	snapshot.Digest = ""
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
