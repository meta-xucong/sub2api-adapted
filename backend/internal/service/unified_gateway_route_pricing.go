package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const UnifiedGatewayRoutePricingSettingKey = "unified_gateway_route_pricing_v1"

type UnifiedGatewayRoutePricingKind string

const (
	UnifiedGatewayRoutePricingToken UnifiedGatewayRoutePricingKind = "token"
	UnifiedGatewayRoutePricingImage UnifiedGatewayRoutePricingKind = "image"
	UnifiedGatewayRoutePricingVideo UnifiedGatewayRoutePricingKind = "video"
)

// UnifiedGatewayTokenBasePrice is a complete line-specific rate card. Values
// are stars per million tokens; pointers distinguish an explicit zero from an
// omitted price.
type UnifiedGatewayTokenBasePrice struct {
	InputPerMillion        *float64 `json:"input_per_million"`
	OutputPerMillion       *float64 `json:"output_per_million"`
	CacheReadPerMillion    *float64 `json:"cache_read_per_million"`
	CacheWritePerMillion   *float64 `json:"cache_write_per_million"`
	CacheWrite5mPerMillion *float64 `json:"cache_write_5m_per_million"`
	CacheWrite1hPerMillion *float64 `json:"cache_write_1h_per_million"`
}

func (p *UnifiedGatewayTokenBasePrice) validate() bool {
	if p == nil {
		return false
	}
	prices := []*float64{
		p.InputPerMillion, p.OutputPerMillion, p.CacheReadPerMillion,
		p.CacheWritePerMillion, p.CacheWrite5mPerMillion, p.CacheWrite1hPerMillion,
	}
	for _, price := range prices {
		if price == nil || !finiteNonNegative(*price) {
			return false
		}
	}
	return true
}

func (p *UnifiedGatewayTokenBasePrice) inputSideMaxPerToken() float64 {
	if p == nil || !p.validate() {
		return 0
	}
	maxPerMillion := *p.InputPerMillion
	for _, price := range []*float64{p.CacheReadPerMillion, p.CacheWritePerMillion, p.CacheWrite5mPerMillion, p.CacheWrite1hPerMillion} {
		if *price > maxPerMillion {
			maxPerMillion = *price
		}
	}
	return maxPerMillion / 1_000_000
}

func (p *UnifiedGatewayTokenBasePrice) hasPositiveRate() bool {
	if p == nil || !p.validate() {
		return false
	}
	return *p.InputPerMillion > 0 || *p.OutputPerMillion > 0 || *p.CacheReadPerMillion > 0 ||
		*p.CacheWritePerMillion > 0 || *p.CacheWrite5mPerMillion > 0 || *p.CacheWrite1hPerMillion > 0
}

// UnifiedGatewayRoutePricingEntry is keyed by the single target group, selected
// account, native billing model, modality, and exact media specification.
type UnifiedGatewayRoutePricingEntry struct {
	AccountID            int64                          `json:"account_id"`
	Model                string                         `json:"model"`
	Kind                 UnifiedGatewayRoutePricingKind `json:"kind"`
	Multiplier           *float64                       `json:"multiplier,omitempty"`
	TokenBasePrice       *UnifiedGatewayTokenBasePrice  `json:"token_base_price,omitempty"`
	UnitPrice            *float64                       `json:"unit_price,omitempty"`
	ImageSize            string                         `json:"image_size,omitempty"`
	ImageQuality         string                         `json:"image_quality,omitempty"`
	VideoResolution      string                         `json:"video_resolution,omitempty"`
	VideoDurationSeconds int                            `json:"video_duration_seconds,omitempty"`
}

type UnifiedGatewayRoutePricingConfig struct {
	TargetGroupID int64                             `json:"target_group_id"`
	Revision      int64                             `json:"revision"`
	Entries       []UnifiedGatewayRoutePricingEntry `json:"entries"`
}

type UnifiedGatewayRoutePricingGroupOption struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type UnifiedGatewayRoutePricingAccountOption struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type UnifiedGatewayRoutePricingAdminState struct {
	Saved          UnifiedGatewayRoutePricingConfig          `json:"saved"`
	ActiveRevision int64                                     `json:"active_revision"`
	RestartNeeded  bool                                      `json:"restart_needed"`
	Groups         []UnifiedGatewayRoutePricingGroupOption   `json:"groups"`
	Accounts       []UnifiedGatewayRoutePricingAccountOption `json:"accounts"`
}

type UnifiedGatewayRoutePricingUpdate struct {
	ExpectedRevision int64                             `json:"expected_revision"`
	TargetGroupID    int64                             `json:"target_group_id"`
	Entries          []UnifiedGatewayRoutePricingEntry `json:"entries"`
}

type SettingCompareAndSetRepository interface {
	// CompareAndSetValue atomically writes value iff key is absent when expected
	// is nil, or its current full value exactly equals *expected.
	CompareAndSetValue(ctx context.Context, key string, expected *string, value string) (bool, error)
}

type unifiedGatewayRoutePricingSnapshot struct {
	config  UnifiedGatewayRoutePricingConfig
	entries map[string]UnifiedGatewayRoutePricingEntry
}

func unifiedGatewayRoutePricingKey(groupID, accountID int64, model string, kind UnifiedGatewayRoutePricingKind, imageSize, imageQuality, videoResolution string, videoDuration int) string {
	return fmt.Sprintf("%d\x00%d\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d", groupID, accountID, strings.TrimSpace(model), kind, strings.TrimSpace(imageSize), strings.TrimSpace(imageQuality), strings.TrimSpace(videoResolution), videoDuration)
}

func buildUnifiedGatewayRoutePricingSnapshot(cfg UnifiedGatewayRoutePricingConfig) (*unifiedGatewayRoutePricingSnapshot, error) {
	if cfg.TargetGroupID <= 0 || cfg.Revision < 0 {
		return nil, errors.New("invalid target group or revision")
	}
	if cfg.Entries == nil {
		cfg.Entries = []UnifiedGatewayRoutePricingEntry{}
	}
	index := make(map[string]UnifiedGatewayRoutePricingEntry, len(cfg.Entries))
	for i := range cfg.Entries {
		entry := cfg.Entries[i]
		entry.Model = strings.TrimSpace(entry.Model)
		entry.ImageSize = strings.TrimSpace(entry.ImageSize)
		entry.ImageQuality = strings.ToLower(strings.TrimSpace(entry.ImageQuality))
		entry.VideoResolution = strings.ToLower(strings.TrimSpace(entry.VideoResolution))
		if entry.AccountID <= 0 || entry.Model == "" {
			return nil, fmt.Errorf("entry %d requires an account and model", i)
		}
		switch entry.Kind {
		case UnifiedGatewayRoutePricingToken:
			if (entry.Multiplier == nil && entry.TokenBasePrice == nil) ||
				(entry.Multiplier != nil && !finiteNonNegative(*entry.Multiplier)) ||
				(entry.TokenBasePrice != nil && !entry.TokenBasePrice.validate()) ||
				entry.UnitPrice != nil || entry.ImageSize != "" || entry.ImageQuality != "" || entry.VideoResolution != "" || entry.VideoDurationSeconds != 0 {
				return nil, fmt.Errorf("entry %d has invalid token pricing fields", i)
			}
		case UnifiedGatewayRoutePricingImage:
			tier, ok := ClassifyImageBillingTier(entry.ImageSize)
			if !ok || entry.UnitPrice == nil || !finiteNonNegative(*entry.UnitPrice) || entry.Multiplier != nil || entry.TokenBasePrice != nil || entry.ImageQuality == "" || entry.VideoResolution != "" || entry.VideoDurationSeconds != 0 {
				return nil, fmt.Errorf("entry %d requires an image size, quality, and non-negative unit price", i)
			}
			entry.ImageSize = tier
		case UnifiedGatewayRoutePricingVideo:
			resolution, resolutionKnown := LookupVideoBillingResolution(entry.VideoResolution)
			if entry.UnitPrice == nil || !finiteNonNegative(*entry.UnitPrice) || entry.Multiplier != nil || entry.TokenBasePrice != nil || !resolutionKnown || entry.VideoDurationSeconds < VideoBillingMinDurationSeconds || entry.VideoDurationSeconds > VideoBillingMaxDurationSeconds || entry.ImageSize != "" || entry.ImageQuality != "" {
				return nil, fmt.Errorf("entry %d requires a supported video resolution and duration and a non-negative unit price", i)
			}
			entry.VideoResolution = resolution
		default:
			return nil, fmt.Errorf("entry %d has an unsupported pricing kind", i)
		}
		key := unifiedGatewayRoutePricingKey(cfg.TargetGroupID, entry.AccountID, entry.Model, entry.Kind, entry.ImageSize, entry.ImageQuality, entry.VideoResolution, entry.VideoDurationSeconds)
		if _, exists := index[key]; exists {
			return nil, fmt.Errorf("entry %d duplicates a route pricing key", i)
		}
		index[key] = entry
		cfg.Entries[i] = entry
	}
	return &unifiedGatewayRoutePricingSnapshot{config: cfg, entries: index}, nil
}

func finiteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func (s *SettingService) SetUnifiedGatewayRoutePricingRepositories(groups GroupRepository, accounts AccountRepository) {
	if s == nil {
		return
	}
	s.routePricingGroupRepo = groups
	s.routePricingAccountRepo = accounts
}

// LoadUnifiedGatewayRoutePricingAtStartup captures one immutable revision for
// this process. Admin saves do not update the active pointer.
func (s *SettingService) LoadUnifiedGatewayRoutePricingAtStartup(ctx context.Context) error {
	if s == nil || s.settingRepo == nil {
		return errors.New("route pricing settings repository is unavailable")
	}
	raw, err := s.settingRepo.GetValue(ctx, UnifiedGatewayRoutePricingSettingKey)
	if errors.Is(err, ErrSettingNotFound) {
		s.routePricingSnapshot.Store(nil)
		return nil
	}
	if err != nil {
		return err
	}
	var cfg UnifiedGatewayRoutePricingConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return fmt.Errorf("decode route pricing settings: %w", err)
	}
	snapshot, err := buildUnifiedGatewayRoutePricingSnapshot(cfg)
	if err != nil {
		return fmt.Errorf("validate route pricing settings: %w", err)
	}
	s.routePricingSnapshot.Store(snapshot)
	return nil
}

func (s *SettingService) ActiveUnifiedGatewayRoutePricing() *UnifiedGatewayRoutePricingConfig {
	if s == nil {
		return nil
	}
	snapshot := s.routePricingSnapshot.Load()
	if snapshot == nil {
		return nil
	}
	copy := snapshot.config
	copy.Entries = append([]UnifiedGatewayRoutePricingEntry(nil), snapshot.config.Entries...)
	return &copy
}

func (s *SettingService) getActiveUnifiedGatewayRoutePricingSnapshot() *unifiedGatewayRoutePricingSnapshot {
	if s == nil {
		return nil
	}
	return s.routePricingSnapshot.Load()
}

func (s *SettingService) GetUnifiedGatewayRoutePricingAdminState(ctx context.Context) (*UnifiedGatewayRoutePricingAdminState, error) {
	if s == nil || s.settingRepo == nil || s.routePricingGroupRepo == nil || s.routePricingAccountRepo == nil {
		return nil, errors.New("route pricing admin dependencies are unavailable")
	}
	groups, err := s.routePricingGroupRepo.ListActiveByPlatform(ctx, PlatformComposite)
	if err != nil {
		return nil, err
	}
	if len(groups) != 1 {
		return nil, errors.New("route pricing requires exactly one active Composite group")
	}
	state := &UnifiedGatewayRoutePricingAdminState{
		Saved:    UnifiedGatewayRoutePricingConfig{Entries: []UnifiedGatewayRoutePricingEntry{}},
		Groups:   make([]UnifiedGatewayRoutePricingGroupOption, 0, len(groups)),
		Accounts: []UnifiedGatewayRoutePricingAccountOption{},
	}
	for _, group := range groups {
		state.Groups = append(state.Groups, UnifiedGatewayRoutePricingGroupOption{ID: group.ID, Name: group.Name})
	}
	raw, err := s.settingRepo.GetValue(ctx, UnifiedGatewayRoutePricingSettingKey)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal([]byte(raw), &state.Saved); err != nil {
			return nil, fmt.Errorf("decode saved route pricing: %w", err)
		}
	}
	if state.Saved.Entries == nil {
		state.Saved.Entries = []UnifiedGatewayRoutePricingEntry{}
	}
	if state.Saved.TargetGroupID == 0 && len(groups) == 1 {
		state.Saved.TargetGroupID = groups[0].ID
	}
	if snapshot := s.routePricingSnapshot.Load(); snapshot != nil {
		state.ActiveRevision = snapshot.config.Revision
	}
	state.RestartNeeded = state.Saved.Revision != state.ActiveRevision
	if state.Saved.TargetGroupID > 0 {
		accounts, listErr := s.routePricingAccountRepo.ListSchedulableByGroupID(ctx, state.Saved.TargetGroupID)
		if listErr != nil {
			return nil, listErr
		}
		state.Accounts = make([]UnifiedGatewayRoutePricingAccountOption, 0, len(accounts))
		for _, account := range accounts {
			state.Accounts = append(state.Accounts, UnifiedGatewayRoutePricingAccountOption{ID: account.ID, Name: account.Name})
		}
		sort.Slice(state.Accounts, func(i, j int) bool { return state.Accounts[i].ID < state.Accounts[j].ID })
	}
	return state, nil
}

func (s *SettingService) UpdateUnifiedGatewayRoutePricing(ctx context.Context, update UnifiedGatewayRoutePricingUpdate) (*UnifiedGatewayRoutePricingAdminState, error) {
	if s == nil || s.settingRepo == nil || s.routePricingGroupRepo == nil || s.routePricingAccountRepo == nil {
		return nil, errors.New("route pricing admin dependencies are unavailable")
	}
	groups, err := s.routePricingGroupRepo.ListActiveByPlatform(ctx, PlatformComposite)
	if err != nil {
		return nil, err
	}
	if len(groups) != 1 {
		return nil, errors.New("route pricing requires exactly one active Composite group")
	}
	var target *Group
	for i := range groups {
		if groups[i].ID == update.TargetGroupID {
			group := groups[i]
			target = &group
			break
		}
	}
	if target == nil || target.Platform != PlatformComposite || !target.IsActive() {
		return nil, errors.New("target must be an active Composite group")
	}
	allowedAccounts, err := s.routePricingAccountRepo.ListSchedulableByGroupID(ctx, update.TargetGroupID)
	if err != nil {
		return nil, err
	}
	allowed := make(map[int64]struct{}, len(allowedAccounts))
	for _, account := range allowedAccounts {
		allowed[account.ID] = struct{}{}
	}
	for _, entry := range update.Entries {
		if _, ok := allowed[entry.AccountID]; !ok {
			return nil, fmt.Errorf("account %d is not schedulable in the target group", entry.AccountID)
		}
	}
	candidate := UnifiedGatewayRoutePricingConfig{TargetGroupID: update.TargetGroupID, Revision: update.ExpectedRevision + 1, Entries: update.Entries}
	validated, err := buildUnifiedGatewayRoutePricingSnapshot(candidate)
	if err != nil {
		return nil, err
	}
	candidate = validated.config
	currentRaw, err := s.settingRepo.GetValue(ctx, UnifiedGatewayRoutePricingSettingKey)
	var expected *string
	currentRevision := int64(0)
	if err == nil {
		expected = &currentRaw
		var current UnifiedGatewayRoutePricingConfig
		if unmarshalErr := json.Unmarshal([]byte(currentRaw), &current); unmarshalErr != nil {
			return nil, fmt.Errorf("decode current route pricing: %w", unmarshalErr)
		}
		currentRevision = current.Revision
	} else if !errors.Is(err, ErrSettingNotFound) {
		return nil, err
	}
	if currentRevision != update.ExpectedRevision {
		return nil, ErrUnifiedGatewayRoutePricingRevisionConflict
	}
	encoded, err := json.Marshal(candidate)
	if err != nil {
		return nil, err
	}
	casRepo, ok := s.settingRepo.(SettingCompareAndSetRepository)
	if !ok {
		return nil, errors.New("settings repository does not support atomic route pricing updates")
	}
	updated, err := casRepo.CompareAndSetValue(ctx, UnifiedGatewayRoutePricingSettingKey, expected, string(encoded))
	if err != nil {
		return nil, err
	}
	if !updated {
		return nil, ErrUnifiedGatewayRoutePricingRevisionConflict
	}
	return s.GetUnifiedGatewayRoutePricingAdminState(ctx)
}

var ErrUnifiedGatewayRoutePricingRevisionConflict = infraerrors.Conflict("ROUTE_PRICING_REVISION_CONFLICT", "route pricing changed by another administrator; reload and retry")

type UnifiedGatewayRoutePricingDecision struct {
	Allowed              bool                              `json:"allowed"`
	Revision             int64                             `json:"revision"`
	GroupID              int64                             `json:"group_id"`
	Model                string                            `json:"model"`
	Kind                 UnifiedGatewayRoutePricingKind    `json:"kind"`
	ImageSize            string                            `json:"image_size,omitempty"`
	ImageQuality         string                            `json:"image_quality,omitempty"`
	VideoResolution      string                            `json:"video_resolution,omitempty"`
	VideoDurationSeconds int                               `json:"video_duration_seconds,omitempty"`
	Entries              []UnifiedGatewayRoutePricingEntry `json:"entries"`
}

func (d UnifiedGatewayRoutePricingDecision) HasTokenBasePrice() bool {
	if d.Kind != UnifiedGatewayRoutePricingToken {
		return false
	}
	for _, entry := range d.Entries {
		if entry.Kind == UnifiedGatewayRoutePricingToken && entry.TokenBasePrice != nil {
			return true
		}
	}
	return false
}

func (d UnifiedGatewayRoutePricingDecision) HasTokenBasePriceFor(accountID int64, model string) bool {
	if d.Kind != UnifiedGatewayRoutePricingToken || model == "" || d.Model != model {
		return false
	}
	for _, entry := range d.Entries {
		if entry.AccountID == accountID && entry.Model == model && entry.Kind == UnifiedGatewayRoutePricingToken && entry.TokenBasePrice != nil {
			return true
		}
	}
	return false
}

func (d UnifiedGatewayRoutePricingDecision) HasPositiveTokenBasePrice() bool {
	if d.Kind != UnifiedGatewayRoutePricingToken {
		return false
	}
	for _, entry := range d.Entries {
		if entry.Kind == UnifiedGatewayRoutePricingToken && entry.TokenBasePrice.hasPositiveRate() {
			return true
		}
	}
	return false
}

type unifiedGatewayRoutePricingDecisionContextKey struct{}

func WithUnifiedGatewayRoutePricingDecision(ctx context.Context, decision UnifiedGatewayRoutePricingDecision) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, unifiedGatewayRoutePricingDecisionContextKey{}, decision)
}

func UnifiedGatewayRoutePricingDecisionFromContext(ctx context.Context) (UnifiedGatewayRoutePricingDecision, bool) {
	if ctx == nil {
		return UnifiedGatewayRoutePricingDecision{}, false
	}
	decision, ok := ctx.Value(unifiedGatewayRoutePricingDecisionContextKey{}).(UnifiedGatewayRoutePricingDecision)
	return decision, ok
}

func CopyUnifiedGatewayRoutePricingDecision(from, to context.Context) context.Context {
	decision, ok := UnifiedGatewayRoutePricingDecisionFromContext(from)
	if !ok {
		return to
	}
	return WithUnifiedGatewayRoutePricingDecision(to, decision)
}

// UnifiedGatewayRouteTokenUsage supplies the native token quantities and
// already-resolved user/group rate for a token base-price calculation.
type UnifiedGatewayRouteTokenUsage struct {
	Tokens         UsageTokens
	RateMultiplier float64
	Eligible       bool
}

func unifiedGatewayTokenBasePriceHasDynamicGroupPeak(apiKey *APIKey, pricingAt time.Time) bool {
	return apiKey != nil && apiKey.Group != nil && apiKey.Group.PeakMultiplierAt(pricingAt) != 1
}

func flatUnifiedGatewayServiceTier(tier *string) bool {
	if tier == nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(*tier)) {
	case "", "default", "standard":
		return true
	default:
		return false
	}
}

func (s *SettingService) unifiedGatewayRoutePricingEntry(groupID, accountID int64, model string, kind UnifiedGatewayRoutePricingKind, imageSize, imageQuality, videoResolution string, videoDuration int) (UnifiedGatewayRoutePricingEntry, bool) {
	snapshot := s.getActiveUnifiedGatewayRoutePricingSnapshot()
	if snapshot == nil || snapshot.config.TargetGroupID != groupID {
		return UnifiedGatewayRoutePricingEntry{}, false
	}
	entry, ok := snapshot.entries[unifiedGatewayRoutePricingKey(groupID, accountID, model, kind, imageSize, imageQuality, videoResolution, videoDuration)]
	return entry, ok
}

func (s *SettingService) unifiedGatewayRoutePricingMaxEntries(groupID int64, model string, kind UnifiedGatewayRoutePricingKind, imageSize, imageQuality, videoResolution string, videoDuration int) []UnifiedGatewayRoutePricingEntry {
	snapshot := s.getActiveUnifiedGatewayRoutePricingSnapshot()
	if snapshot == nil || snapshot.config.TargetGroupID != groupID {
		return nil
	}
	entries := make([]UnifiedGatewayRoutePricingEntry, 0)
	for _, entry := range snapshot.config.Entries {
		if entry.Model == strings.TrimSpace(model) && entry.Kind == kind && entry.ImageSize == imageSize && entry.ImageQuality == imageQuality && entry.VideoResolution == videoResolution && entry.VideoDurationSeconds == videoDuration {
			entries = append(entries, entry)
		}
	}
	return entries
}

func (s *SettingService) prepareUnifiedGatewayRoutePricingDecision(ctx context.Context, apiKey *APIKey, req InflightEstimateRequest, nativeEstimate float64, nativePriced bool, baseMultiplier float64) (context.Context, float64, bool) {
	if s == nil || apiKey == nil || apiKey.GroupID == nil || apiKey.Group == nil || apiKey.Group.Platform != PlatformComposite {
		return ctx, 0, false
	}
	groupID := *apiKey.GroupID
	kind := UnifiedGatewayRoutePricingToken
	imageSize, imageQuality, videoResolution, videoDuration := "", "", "", 0
	switch req.Kind {
	case InflightEstimateToken:
		kind = UnifiedGatewayRoutePricingToken
	case InflightEstimateImage:
		kind = UnifiedGatewayRoutePricingImage
		requestedSize := strings.TrimSpace(req.ImageSize)
		if requestedSize == "" || strings.EqualFold(requestedSize, "auto") {
			return ctx, 0, false
		}
		var ok bool
		imageSize, ok = ClassifyImageBillingTier(requestedSize)
		if !ok {
			return ctx, 0, false
		}
		imageQuality = strings.ToLower(strings.TrimSpace(req.ImageQuality))
		if imageQuality == "" {
			return ctx, 0, false
		}
	case InflightEstimateVideo:
		kind = UnifiedGatewayRoutePricingVideo
		videoResolution = NormalizeVideoBillingResolutionOrDefault(req.VideoResolution)
		videoDuration = NormalizeVideoBillingDurationSecondsOrDefault(req.VideoDurationSeconds)
	default:
		return ctx, 0, false
	}
	snapshot := s.getActiveUnifiedGatewayRoutePricingSnapshot()
	if snapshot == nil || snapshot.config.TargetGroupID != groupID {
		return ctx, 0, false
	}
	entries := s.unifiedGatewayRoutePricingMaxEntries(groupID, req.Model, kind, imageSize, imageQuality, videoResolution, videoDuration)
	if len(entries) == 0 {
		return ctx, 0, false
	}
	decision := UnifiedGatewayRoutePricingDecision{
		Allowed:              false,
		Revision:             snapshot.config.Revision,
		GroupID:              groupID,
		Model:                strings.TrimSpace(req.Model),
		Kind:                 kind,
		ImageSize:            imageSize,
		ImageQuality:         imageQuality,
		VideoResolution:      videoResolution,
		VideoDurationSeconds: videoDuration,
		Entries:              append([]UnifiedGatewayRoutePricingEntry(nil), entries...),
	}
	routeEstimate := 0.0
	switch kind {
	case UnifiedGatewayRoutePricingToken:
		maxMultiplier := 1.0 // native user price remains a reservation candidate
		for _, entry := range entries {
			if entry.Multiplier != nil && *entry.Multiplier > maxMultiplier {
				maxMultiplier = *entry.Multiplier
			}
		}
		if nativePriced {
			routeEstimate = nativeEstimate * maxMultiplier
		}
		inputTokens := max(req.BodyBytes, 0) / inflightInputBytesPerTokenEstimate
		outputTokens := req.MaxTokens
		if outputTokens <= 0 {
			outputTokens = defaultInflightDefaultMaxTokens
		}
		// The route card is the upstream base price. Apply the line multiplier
		// and then the effective user/group multiplier; peak pricing is not stacked.
		tokenRateMultiplier := baseMultiplier
		for _, entry := range entries {
			if entry.TokenBasePrice == nil {
				continue
			}
			lineMultiplier := 1.0
			if entry.Multiplier != nil {
				lineMultiplier = *entry.Multiplier
			}
			card := entry.TokenBasePrice
			cardEstimate := (float64(inputTokens)*card.inputSideMaxPerToken() + float64(outputTokens)*(*card.OutputPerMillion)/1_000_000) * lineMultiplier * tokenRateMultiplier
			if cardEstimate > routeEstimate {
				routeEstimate = cardEstimate
			}
		}
	case UnifiedGatewayRoutePricingImage:
		modalityRate := resolveImageRateMultiplier(apiKey, baseMultiplier)
		maxPrice := 0.0
		for _, entry := range entries {
			if entry.UnitPrice != nil && *entry.UnitPrice > maxPrice {
				maxPrice = *entry.UnitPrice
			}
		}
		units := req.Units
		if units <= 0 {
			units = 1
		}
		routeEstimate = maxPrice * float64(units) * modalityRate
	case UnifiedGatewayRoutePricingVideo:
		modalityRate := resolveVideoRateMultiplier(apiKey, baseMultiplier)
		maxPrice := 0.0
		for _, entry := range entries {
			if entry.UnitPrice != nil && *entry.UnitPrice > maxPrice {
				maxPrice = *entry.UnitPrice
			}
		}
		units := req.Units
		if units <= 0 {
			units = 1
		}
		routeEstimate = maxPrice * float64(units) * modalityRate
	}
	if !finiteNonNegative(routeEstimate) {
		return ctx, 0, false
	}
	return WithUnifiedGatewayRoutePricingDecision(ctx, decision), routeEstimate, true
}

func setUnifiedGatewayRoutePricingDecisionAllowed(ctx context.Context, allowed bool) context.Context {
	decision, ok := UnifiedGatewayRoutePricingDecisionFromContext(ctx)
	if !ok {
		return ctx
	}
	decision.Allowed = allowed
	return WithUnifiedGatewayRoutePricingDecision(ctx, decision)
}

func WithUnifiedGatewayRoutePricingDecisionAllowed(ctx context.Context, allowed bool) context.Context {
	return setUnifiedGatewayRoutePricingDecisionAllowed(ctx, allowed)
}

func prepareUnifiedGatewayRoutePricingForGateway(ctx context.Context, settings *SettingService, apiKey *APIKey, req InflightEstimateRequest, nativeEstimate float64, nativePriced bool, baseMultiplier float64) (context.Context, float64, bool) {
	return settings.prepareUnifiedGatewayRoutePricingDecision(ctx, apiKey, req, nativeEstimate, nativePriced, baseMultiplier)
}

func (s *GatewayService) PrepareUnifiedGatewayRoutePricingReservation(ctx context.Context, apiKey *APIKey, req InflightEstimateRequest, nativeEstimate float64, nativePriced bool) (context.Context, float64, bool) {
	base := 1.0
	if s != nil && s.cfg != nil {
		base = s.cfg.Default.RateMultiplier
	}
	if apiKey != nil && apiKey.GroupID != nil && apiKey.Group != nil {
		userID := int64(0)
		if apiKey.User != nil {
			userID = apiKey.User.ID
		}
		base = s.ResolveUserGroupRateMultiplier(ctx, userID, *apiKey.GroupID, apiKey.Group.RateMultiplier)
	}
	return prepareUnifiedGatewayRoutePricingForGateway(ctx, s.settingService, apiKey, req, nativeEstimate, nativePriced, base)
}

func (s *OpenAIGatewayService) PrepareUnifiedGatewayRoutePricingReservation(ctx context.Context, apiKey *APIKey, req InflightEstimateRequest, nativeEstimate float64, nativePriced bool) (context.Context, float64, bool) {
	base := 1.0
	if s != nil && s.cfg != nil {
		base = s.cfg.Default.RateMultiplier
	}
	if apiKey != nil && apiKey.GroupID != nil && apiKey.Group != nil && apiKey.User != nil {
		base = s.ResolveUserGroupRateMultiplier(ctx, apiKey.User.ID, *apiKey.GroupID, apiKey.Group.RateMultiplier)
	}
	return prepareUnifiedGatewayRoutePricingForGateway(ctx, s.settingService, apiKey, req, nativeEstimate, nativePriced, base)
}

func ApplyUnifiedGatewayRoutePricing(ctx context.Context, apiKey *APIKey, accountID int64, billingModel, imageBillingSize, imageQuality string, imageSizeBreakdown map[string]int, imageCount, videoCount int, videoResolution string, videoDurationSeconds int, cost *CostBreakdown, imageMultiplier, videoMultiplier float64) *CostBreakdown {
	return applyUnifiedGatewayRoutePricing(ctx, apiKey, accountID, billingModel, imageBillingSize, imageQuality, imageSizeBreakdown, imageCount, videoCount, videoResolution, videoDurationSeconds, cost, imageMultiplier, videoMultiplier, nil)
}

func ApplyUnifiedGatewayRoutePricingWithTokenUsage(ctx context.Context, apiKey *APIKey, accountID int64, billingModel, imageBillingSize, imageQuality string, imageSizeBreakdown map[string]int, imageCount, videoCount int, videoResolution string, videoDurationSeconds int, cost *CostBreakdown, imageMultiplier, videoMultiplier float64, tokenUsage UnifiedGatewayRouteTokenUsage) *CostBreakdown {
	return applyUnifiedGatewayRoutePricing(ctx, apiKey, accountID, billingModel, imageBillingSize, imageQuality, imageSizeBreakdown, imageCount, videoCount, videoResolution, videoDurationSeconds, cost, imageMultiplier, videoMultiplier, &tokenUsage)
}

func applyUnifiedGatewayRoutePricing(ctx context.Context, apiKey *APIKey, accountID int64, billingModel, imageBillingSize, imageQuality string, imageSizeBreakdown map[string]int, imageCount, videoCount int, videoResolution string, videoDurationSeconds int, cost *CostBreakdown, imageMultiplier, videoMultiplier float64, tokenUsage *UnifiedGatewayRouteTokenUsage) *CostBreakdown {
	decision, ok := UnifiedGatewayRoutePricingDecisionFromContext(ctx)
	if !ok || !decision.Allowed || apiKey == nil || apiKey.GroupID == nil || *apiKey.GroupID != decision.GroupID || accountID <= 0 {
		return cost
	}
	billingModel = strings.TrimSpace(billingModel)
	if billingModel == "" || billingModel != decision.Model {
		return cost
	}
	var entry *UnifiedGatewayRoutePricingEntry
	for i := range decision.Entries {
		candidate := &decision.Entries[i]
		if candidate.AccountID == accountID && candidate.Model == billingModel && candidate.Kind == decision.Kind {
			entry = candidate
			break
		}
	}
	if entry == nil {
		return cost
	}
	switch decision.Kind {
	case UnifiedGatewayRoutePricingToken:
		if entry.TokenBasePrice != nil && tokenUsage != nil && tokenUsage.Eligible {
			lineMultiplier := 1.0
			if entry.Multiplier != nil {
				lineMultiplier = *entry.Multiplier
			}
			if baseCost, ok := calculateUnifiedGatewayTokenBasePrice(entry.TokenBasePrice, tokenUsage.Tokens, tokenUsage.RateMultiplier*lineMultiplier); ok {
				if cost == nil {
					cost = &CostBreakdown{BillingMode: string(BillingModeToken)}
				}
				cost.ActualCost = baseCost.ActualCost
				cost.routePricingApplied = true
				return cost
			}
		}
		if cost == nil || entry.Multiplier == nil || !finiteNonNegative(cost.ActualCost**entry.Multiplier) {
			return cost
		}
		cost.ActualCost *= *entry.Multiplier
	case UnifiedGatewayRoutePricingImage:
		actualImageSize, sizeKnown := ClassifyImageBillingTier(imageBillingSize)
		actualQuality := strings.ToLower(strings.TrimSpace(imageQuality))
		if imageCount <= 0 || entry.UnitPrice == nil || entry.ImageSize != decision.ImageSize || entry.ImageQuality != decision.ImageQuality || !sizeKnown || actualImageSize != decision.ImageSize || actualQuality == "" || actualQuality != decision.ImageQuality {
			return cost
		}
		if len(imageSizeBreakdown) > 0 && (len(imageSizeBreakdown) != 1 || imageSizeBreakdown[decision.ImageSize] != imageCount) {
			return cost
		}
		customCost := *entry.UnitPrice * float64(imageCount) * imageMultiplier
		if !finiteNonNegative(customCost) {
			return cost
		}
		if cost == nil {
			cost = &CostBreakdown{}
		}
		cost.ActualCost = customCost
	case UnifiedGatewayRoutePricingVideo:
		if videoCount <= 0 || entry.UnitPrice == nil || entry.VideoResolution != decision.VideoResolution || entry.VideoDurationSeconds != decision.VideoDurationSeconds {
			return cost
		}
		if NormalizeVideoBillingResolutionOrDefault(videoResolution) != decision.VideoResolution || NormalizeVideoBillingDurationSecondsOrDefault(videoDurationSeconds) != decision.VideoDurationSeconds {
			return cost
		}
		customCost := *entry.UnitPrice * float64(videoCount) * videoMultiplier
		if !finiteNonNegative(customCost) {
			return cost
		}
		if cost == nil {
			cost = &CostBreakdown{}
		}
		cost.ActualCost = customCost
	}
	if !finiteNonNegative(cost.ActualCost) {
		return cost
	}
	cost.routePricingApplied = true
	return cost
}

func calculateUnifiedGatewayTokenBasePrice(card *UnifiedGatewayTokenBasePrice, tokens UsageTokens, rateMultiplier float64) (*CostBreakdown, bool) {
	if !card.validate() || !finiteNonNegative(rateMultiplier) || tokens.ImageInputTokens > 0 || tokens.ImageCacheReadTokens > 0 || tokens.ImageOutputTokens > 0 {
		return nil, false
	}
	cacheFive, cacheOneHour := normalizeCacheCreationBreakdown(tokens)
	if tokens.CacheCreation1hTokens > 0 && *card.CacheWrite1hPerMillion == 0 {
		return nil, false
	}
	if tokens.CacheCreationTokens > 0 && cacheFive+cacheOneHour == 0 && *card.CacheWrite5mPerMillion != *card.CacheWrite1hPerMillion {
		return nil, false
	}
	pricing := &ModelPricing{
		InputPricePerToken:         *card.InputPerMillion / 1_000_000,
		OutputPricePerToken:        *card.OutputPerMillion / 1_000_000,
		CacheReadPricePerToken:     *card.CacheReadPerMillion / 1_000_000,
		CacheCreationPricePerToken: *card.CacheWritePerMillion / 1_000_000,
	}
	textTokens := tokens
	textTokens.CacheCreationTokens = 0
	textTokens.CacheCreation5mTokens = 0
	textTokens.CacheCreation1hTokens = 0
	cost := (&BillingService{}).computeTokenBreakdown(pricing, textTokens, rateMultiplier, "", false)
	if cost == nil {
		return nil, false
	}
	cacheCreation := 0.0
	if cacheFive > 0 || cacheOneHour > 0 {
		cacheCreation = float64(cacheFive)*(*card.CacheWrite5mPerMillion/1_000_000) +
			float64(cacheOneHour)*(*card.CacheWrite1hPerMillion/1_000_000)
	} else if tokens.CacheCreationTokens > 0 {
		cacheCreation = float64(tokens.CacheCreationTokens) * (*card.CacheWritePerMillion / 1_000_000)
	}
	cost.CacheCreationCost = cacheCreation
	cost.TotalCost += cacheCreation
	cost.ActualCost += cacheCreation * rateMultiplier
	if !finiteNonNegative(cost.TotalCost) || !finiteNonNegative(cost.ActualCost) {
		return nil, false
	}
	return cost, true
}
