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
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/shopspring/decimal"
)

const UnifiedGatewayRoutePricingSettingKey = "unified_gateway_route_pricing_v1"

const (
	UnifiedGatewayHaikuLongContextThreshold = 100_000
	// Wokey's published/observed billing evidence has not confirmed whether cached
	// prompt tokens count toward the long-context threshold. Keep cached requests
	// on the native fallback until that boundary is verified with a same-request bill.
	unifiedGatewayWokeyCachePromptThresholdVerified = false
)

type UnifiedGatewayRoutePricingKind string

const (
	UnifiedGatewayRoutePricingToken UnifiedGatewayRoutePricingKind = "token"
	UnifiedGatewayRoutePricingImage UnifiedGatewayRoutePricingKind = "image"
	UnifiedGatewayRoutePricingVideo UnifiedGatewayRoutePricingKind = "video"
)

type UnifiedGatewayImagePricingMode string

const UnifiedGatewayImagePricingFlatPerImage UnifiedGatewayImagePricingMode = "flat_per_image"

// UnifiedGatewayTokenBasePrice is a line-specific rate card. Input, output,
// and cache-read prices are required; optional cache-write prices distinguish
// an uncovered meter from an explicitly free meter. Values are stars per
// million tokens.
type UnifiedGatewayTokenBasePrice struct {
	InputPerMillion        *float64 `json:"input_per_million"`
	OutputPerMillion       *float64 `json:"output_per_million"`
	CacheReadPerMillion    *float64 `json:"cache_read_per_million"`
	CacheWritePerMillion   *float64 `json:"cache_write_per_million,omitempty"`
	CacheWrite5mPerMillion *float64 `json:"cache_write_5m_per_million,omitempty"`
	CacheWrite1hPerMillion *float64 `json:"cache_write_1h_per_million,omitempty"`
}

// UnifiedGatewayWokeyTimeOfDayWindow is a half-open UTC hour range returned
// by Wokey (for example [1,4) means 01:00 through 03:59:59 UTC).
type UnifiedGatewayWokeyTimeOfDayWindow struct {
	StartHour int `json:"start_hour"`
	EndHour   int `json:"end_hour"`
}

// UnifiedGatewayWokeyTimeOfDayTier is one explicit Wokey token-price tier.
// Raw USD prices are retained so changing the configured FX can recalculate
// the display/billing values without another catalog request.
type UnifiedGatewayWokeyTimeOfDayTier struct {
	TokenBasePrice  *UnifiedGatewayTokenBasePrice `json:"token_base_price"`
	SourcePricesUSD map[string]string             `json:"source_prices_usd"`
}

// UnifiedGatewayWokeyTimeOfDayTokenPrice stores both Wokey price tiers in a
// single route card. CurrentTier only cross-checks the catalog snapshot; actual
// request pricing uses PricingAt and PeakWindowsUTC.
type UnifiedGatewayWokeyTimeOfDayTokenPrice struct {
	CurrentTier    string                               `json:"current_tier"`
	PeakWindowsUTC []UnifiedGatewayWokeyTimeOfDayWindow `json:"peak_windows_utc"`
	Peak           UnifiedGatewayWokeyTimeOfDayTier     `json:"peak"`
	OffPeak        UnifiedGatewayWokeyTimeOfDayTier     `json:"off_peak"`
}

func (p *UnifiedGatewayTokenBasePrice) validate() bool {
	if p == nil {
		return false
	}
	required := []*float64{p.InputPerMillion, p.OutputPerMillion, p.CacheReadPerMillion}
	for _, price := range required {
		if price == nil || !finiteNonNegative(*price) {
			return false
		}
	}
	optional := []*float64{p.CacheWritePerMillion, p.CacheWrite5mPerMillion, p.CacheWrite1hPerMillion}
	for _, price := range optional {
		if price != nil && !finiteNonNegative(*price) {
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
		if price != nil && *price > maxPerMillion {
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
		(p.CacheWritePerMillion != nil && *p.CacheWritePerMillion > 0) ||
		(p.CacheWrite5mPerMillion != nil && *p.CacheWrite5mPerMillion > 0) ||
		(p.CacheWrite1hPerMillion != nil && *p.CacheWrite1hPerMillion > 0)
}

func (p *UnifiedGatewayWokeyTimeOfDayTokenPrice) validate(sourceFX string) bool {
	if p == nil || (p.CurrentTier != "peak" && p.CurrentTier != "off_peak") ||
		len(p.PeakWindowsUTC) == 0 || !p.Peak.TokenBasePrice.validate() || !p.OffPeak.TokenBasePrice.validate() ||
		!validateUnifiedGatewayWokeyTimeOfDayTier(p.Peak, sourceFX) || !validateUnifiedGatewayWokeyTimeOfDayTier(p.OffPeak, sourceFX) {
		return false
	}
	lastEnd := -1
	for _, window := range p.PeakWindowsUTC {
		if window.StartHour < 0 || window.StartHour >= window.EndHour || window.EndHour > 24 || window.StartHour < lastEnd {
			return false
		}
		lastEnd = window.EndHour
	}
	return true
}

func (p *UnifiedGatewayWokeyTimeOfDayTokenPrice) hasPositiveRate() bool {
	return p != nil && (p.Peak.TokenBasePrice.hasPositiveRate() || p.OffPeak.TokenBasePrice.hasPositiveRate())
}

// UnifiedGatewayRoutePricingEntry is keyed by the single target group, selected
// account, native billing model, modality, and exact media specification.
type UnifiedGatewayRoutePricingEntry struct {
	AccountID                 int64                                   `json:"account_id"`
	Model                     string                                  `json:"model"`
	Kind                      UnifiedGatewayRoutePricingKind          `json:"kind"`
	Multiplier                *float64                                `json:"multiplier,omitempty"`
	TokenBasePrice            *UnifiedGatewayTokenBasePrice           `json:"token_base_price,omitempty"`
	LongContextTokenBasePrice *UnifiedGatewayTokenBasePrice           `json:"long_context_token_base_price,omitempty"`
	TimeOfDayTokenPrice       *UnifiedGatewayWokeyTimeOfDayTokenPrice `json:"time_of_day_token_price,omitempty"`
	UnitPrice                 *float64                                `json:"unit_price,omitempty"`
	ImagePricingMode          UnifiedGatewayImagePricingMode          `json:"image_pricing_mode,omitempty"`
	ImageSize                 string                                  `json:"image_size,omitempty"`
	ImageQuality              string                                  `json:"image_quality,omitempty"`
	VideoResolution           string                                  `json:"video_resolution,omitempty"`
	VideoDurationSeconds      int                                     `json:"video_duration_seconds,omitempty"`
	Source                    string                                  `json:"source,omitempty"`
	SourceFX                  string                                  `json:"source_fx,omitempty"`
	SourceFetchedAt           *time.Time                              `json:"source_fetched_at,omitempty"`
	SourceCatalogSHA256       string                                  `json:"source_catalog_sha256,omitempty"`
	SourceSKUs                []UnifiedGatewayWokeySourceSKU          `json:"source_skus,omitempty"`
	SyncState                 string                                  `json:"sync_state,omitempty"`
	SyncReason                string                                  `json:"sync_reason,omitempty"`
}

type UnifiedGatewayWokeySyncConfig struct {
	Enabled         bool                          `json:"enabled"`
	AccountIDs      []int64                       `json:"account_ids"`
	FX              string                        `json:"fx"`
	IntervalMinutes int                           `json:"interval_minutes"`
	Status          UnifiedGatewayWokeySyncStatus `json:"status"`
}

type UnifiedGatewayWokeySyncStatus struct {
	LastAttemptAt       *time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt       *time.Time `json:"last_success_at,omitempty"`
	LastErrorCode       string     `json:"last_error_code,omitempty"`
	LastHTTPStatus      int        `json:"last_http_status,omitempty"`
	CatalogSHA256       string     `json:"catalog_sha256,omitempty"`
	ManagedCardCount    int        `json:"managed_card_count"`
	ManualConflictCount int        `json:"manual_conflict_count"`
	UnsupportedCount    int        `json:"unsupported_count"`
	NotReturnedCount    int        `json:"not_returned_count"`
	Reasons             []string   `json:"reasons,omitempty"`
}

type UnifiedGatewayRoutePricingConfig struct {
	TargetGroupID int64                             `json:"target_group_id"`
	Revision      int64                             `json:"revision"`
	Entries       []UnifiedGatewayRoutePricingEntry `json:"entries"`
	WokeySync     *UnifiedGatewayWokeySyncConfig    `json:"wokey_sync,omitempty"`
}

type UnifiedGatewayRoutePricingGroupOption struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type UnifiedGatewayRoutePricingAccountOption struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	WokeyEligible bool   `json:"wokey_eligible"`
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
	WokeySync        *UnifiedGatewayWokeySyncConfig    `json:"wokey_sync,omitempty"`
}

type UnifiedGatewayRoutePricingKey struct {
	AccountID            int64                          `json:"account_id"`
	Model                string                         `json:"model"`
	Kind                 UnifiedGatewayRoutePricingKind `json:"kind"`
	ImagePricingMode     UnifiedGatewayImagePricingMode `json:"image_pricing_mode,omitempty"`
	ImageSize            string                         `json:"image_size,omitempty"`
	ImageQuality         string                         `json:"image_quality,omitempty"`
	VideoResolution      string                         `json:"video_resolution,omitempty"`
	VideoDurationSeconds int                            `json:"video_duration_seconds,omitempty"`
}

type UnifiedGatewayWokeyManualizeRequest struct {
	ExpectedRevision int64                         `json:"expected_revision"`
	Key              UnifiedGatewayRoutePricingKey `json:"key"`
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

func unifiedGatewayRoutePricingKey(groupID, accountID int64, model string, kind UnifiedGatewayRoutePricingKind, imagePricingMode UnifiedGatewayImagePricingMode, imageSize, imageQuality, videoResolution string, videoDuration int) string {
	return fmt.Sprintf("%d\x00%d\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d", groupID, accountID, strings.TrimSpace(model), kind, imagePricingMode, strings.TrimSpace(imageSize), strings.TrimSpace(imageQuality), strings.TrimSpace(videoResolution), videoDuration)
}

func buildUnifiedGatewayRoutePricingSnapshot(cfg UnifiedGatewayRoutePricingConfig) (*unifiedGatewayRoutePricingSnapshot, error) {
	if cfg.TargetGroupID <= 0 || cfg.Revision < 0 {
		return nil, errors.New("invalid target group or revision")
	}
	if cfg.WokeySync != nil {
		if err := validateUnifiedGatewayWokeySyncConfig(*cfg.WokeySync); err != nil {
			return nil, err
		}
	}
	if cfg.Entries == nil {
		cfg.Entries = []UnifiedGatewayRoutePricingEntry{}
	}
	index := make(map[string]UnifiedGatewayRoutePricingEntry, len(cfg.Entries))
	imageModeByCard := make(map[string]UnifiedGatewayImagePricingMode)
	for i := range cfg.Entries {
		entry := cfg.Entries[i]
		entry.Model = strings.TrimSpace(entry.Model)
		entry.ImageSize = strings.TrimSpace(entry.ImageSize)
		entry.ImageQuality = strings.ToLower(strings.TrimSpace(entry.ImageQuality))
		entry.VideoResolution = strings.ToLower(strings.TrimSpace(entry.VideoResolution))
		if entry.AccountID <= 0 || entry.Model == "" {
			return nil, fmt.Errorf("entry %d requires an account and model", i)
		}
		if err := validateUnifiedGatewayWokeyEntryMetadata(entry); err != nil {
			return nil, fmt.Errorf("entry %d has invalid source metadata: %w", i, err)
		}
		switch entry.Kind {
		case UnifiedGatewayRoutePricingToken:
			if (entry.Multiplier == nil && entry.TokenBasePrice == nil && entry.TimeOfDayTokenPrice == nil) ||
				(entry.Multiplier != nil && !finiteNonNegative(*entry.Multiplier)) ||
				(entry.TokenBasePrice != nil && !entry.TokenBasePrice.validate()) ||
				(entry.TimeOfDayTokenPrice != nil && (entry.Source != UnifiedGatewayWokeySource || entry.TokenBasePrice != nil || entry.LongContextTokenBasePrice != nil || !entry.TimeOfDayTokenPrice.validate(entry.SourceFX))) ||
				(entry.LongContextTokenBasePrice != nil && (entry.TokenBasePrice == nil || !entry.LongContextTokenBasePrice.validate() || entry.Model != "claude-haiku-5-5")) ||
				entry.UnitPrice != nil || entry.ImagePricingMode != "" || entry.ImageSize != "" || entry.ImageQuality != "" || entry.VideoResolution != "" || entry.VideoDurationSeconds != 0 {
				return nil, fmt.Errorf("entry %d has invalid token pricing fields", i)
			}
		case UnifiedGatewayRoutePricingImage:
			if entry.UnitPrice == nil || !finiteNonNegative(*entry.UnitPrice) || entry.Multiplier != nil || entry.TokenBasePrice != nil || entry.LongContextTokenBasePrice != nil || entry.TimeOfDayTokenPrice != nil || entry.VideoResolution != "" || entry.VideoDurationSeconds != 0 {
				return nil, fmt.Errorf("entry %d has invalid image pricing fields", i)
			}
			switch entry.ImagePricingMode {
			case "":
				tier, ok := ClassifyImageBillingTier(entry.ImageSize)
				if !ok || entry.ImageQuality == "" {
					return nil, fmt.Errorf("entry %d requires an image size, quality, and non-negative unit price", i)
				}
				entry.ImageSize = tier
			case UnifiedGatewayImagePricingFlatPerImage:
				if entry.ImageSize != "" || entry.ImageQuality != "" {
					return nil, fmt.Errorf("entry %d flat per-image pricing cannot include an image size or quality", i)
				}
			default:
				return nil, fmt.Errorf("entry %d has an unsupported image pricing mode", i)
			}
			cardKey := fmt.Sprintf("%d\x00%d\x00%s", cfg.TargetGroupID, entry.AccountID, entry.Model)
			if previousMode, exists := imageModeByCard[cardKey]; exists && (previousMode == UnifiedGatewayImagePricingFlatPerImage || entry.ImagePricingMode == UnifiedGatewayImagePricingFlatPerImage) {
				return nil, fmt.Errorf("entry %d cannot combine flat per-image pricing with exact image specs for the same account and model", i)
			}
			imageModeByCard[cardKey] = entry.ImagePricingMode
		case UnifiedGatewayRoutePricingVideo:
			resolution, resolutionKnown := LookupVideoBillingResolution(entry.VideoResolution)
			if entry.UnitPrice == nil || !finiteNonNegative(*entry.UnitPrice) || entry.Multiplier != nil || entry.TokenBasePrice != nil || entry.LongContextTokenBasePrice != nil || entry.TimeOfDayTokenPrice != nil || entry.ImagePricingMode != "" || !resolutionKnown || entry.VideoDurationSeconds < VideoBillingMinDurationSeconds || entry.VideoDurationSeconds > VideoBillingMaxDurationSeconds || entry.ImageSize != "" || entry.ImageQuality != "" {
				return nil, fmt.Errorf("entry %d requires a supported video resolution and duration and a non-negative unit price", i)
			}
			entry.VideoResolution = resolution
		default:
			return nil, fmt.Errorf("entry %d has an unsupported pricing kind", i)
		}
		key := unifiedGatewayRoutePricingKey(cfg.TargetGroupID, entry.AccountID, entry.Model, entry.Kind, entry.ImagePricingMode, entry.ImageSize, entry.ImageQuality, entry.VideoResolution, entry.VideoDurationSeconds)
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
// this process. Admin saves publish a validated snapshot after a successful CAS.
func (s *SettingService) LoadUnifiedGatewayRoutePricingAtStartup(ctx context.Context) error {
	if s == nil || s.settingRepo == nil {
		return errors.New("route pricing settings repository is unavailable")
	}
	s.routePricingMu.Lock()
	defer s.routePricingMu.Unlock()
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
	copy.Entries = cloneUnifiedGatewayRoutePricingEntries(snapshot.config.Entries)
	if copy.WokeySync != nil {
		wokey := *copy.WokeySync
		wokey.AccountIDs = append([]int64(nil), copy.WokeySync.AccountIDs...)
		wokey.Status.Reasons = append([]string(nil), copy.WokeySync.Status.Reasons...)
		copy.WokeySync = &wokey
	}
	return &copy
}

func cloneUnifiedGatewayRoutePricingEntries(entries []UnifiedGatewayRoutePricingEntry) []UnifiedGatewayRoutePricingEntry {
	cloned := make([]UnifiedGatewayRoutePricingEntry, len(entries))
	for i, entry := range entries {
		cloned[i] = entry
		if entry.SourceFetchedAt != nil {
			fetched := *entry.SourceFetchedAt
			cloned[i].SourceFetchedAt = &fetched
		}
		cloned[i].SourceSKUs = append([]UnifiedGatewayWokeySourceSKU(nil), entry.SourceSKUs...)
		if entry.TokenBasePrice != nil {
			price := *entry.TokenBasePrice
			cloneFloat := func(value *float64) *float64 {
				if value == nil {
					return nil
				}
				copy := *value
				return &copy
			}
			price.InputPerMillion = cloneFloat(entry.TokenBasePrice.InputPerMillion)
			price.OutputPerMillion = cloneFloat(entry.TokenBasePrice.OutputPerMillion)
			price.CacheReadPerMillion = cloneFloat(entry.TokenBasePrice.CacheReadPerMillion)
			price.CacheWritePerMillion = cloneFloat(entry.TokenBasePrice.CacheWritePerMillion)
			price.CacheWrite5mPerMillion = cloneFloat(entry.TokenBasePrice.CacheWrite5mPerMillion)
			price.CacheWrite1hPerMillion = cloneFloat(entry.TokenBasePrice.CacheWrite1hPerMillion)
			cloned[i].TokenBasePrice = &price
		}
		if entry.LongContextTokenBasePrice != nil {
			price := *entry.LongContextTokenBasePrice
			cloneFloat := func(value *float64) *float64 {
				if value == nil {
					return nil
				}
				copy := *value
				return &copy
			}
			price.InputPerMillion = cloneFloat(entry.LongContextTokenBasePrice.InputPerMillion)
			price.OutputPerMillion = cloneFloat(entry.LongContextTokenBasePrice.OutputPerMillion)
			price.CacheReadPerMillion = cloneFloat(entry.LongContextTokenBasePrice.CacheReadPerMillion)
			price.CacheWritePerMillion = cloneFloat(entry.LongContextTokenBasePrice.CacheWritePerMillion)
			price.CacheWrite5mPerMillion = cloneFloat(entry.LongContextTokenBasePrice.CacheWrite5mPerMillion)
			price.CacheWrite1hPerMillion = cloneFloat(entry.LongContextTokenBasePrice.CacheWrite1hPerMillion)
			cloned[i].LongContextTokenBasePrice = &price
		}
		if entry.TimeOfDayTokenPrice != nil {
			price := *entry.TimeOfDayTokenPrice
			price.PeakWindowsUTC = append([]UnifiedGatewayWokeyTimeOfDayWindow(nil), entry.TimeOfDayTokenPrice.PeakWindowsUTC...)
			price.Peak.TokenBasePrice = cloneUnifiedGatewayTokenBasePrice(entry.TimeOfDayTokenPrice.Peak.TokenBasePrice)
			price.OffPeak.TokenBasePrice = cloneUnifiedGatewayTokenBasePrice(entry.TimeOfDayTokenPrice.OffPeak.TokenBasePrice)
			price.Peak.SourcePricesUSD = cloneStringMap(entry.TimeOfDayTokenPrice.Peak.SourcePricesUSD)
			price.OffPeak.SourcePricesUSD = cloneStringMap(entry.TimeOfDayTokenPrice.OffPeak.SourcePricesUSD)
			cloned[i].TimeOfDayTokenPrice = &price
		}
		if entry.UnitPrice != nil {
			value := *entry.UnitPrice
			cloned[i].UnitPrice = &value
		}
		if entry.Multiplier != nil {
			value := *entry.Multiplier
			cloned[i].Multiplier = &value
		}
	}
	return cloned
}

func cloneUnifiedGatewayTokenBasePrice(source *UnifiedGatewayTokenBasePrice) *UnifiedGatewayTokenBasePrice {
	if source == nil {
		return nil
	}
	cloned := *source
	cloneFloat := func(value *float64) *float64 {
		if value == nil {
			return nil
		}
		copy := *value
		return &copy
	}
	cloned.InputPerMillion = cloneFloat(source.InputPerMillion)
	cloned.OutputPerMillion = cloneFloat(source.OutputPerMillion)
	cloned.CacheReadPerMillion = cloneFloat(source.CacheReadPerMillion)
	cloned.CacheWritePerMillion = cloneFloat(source.CacheWritePerMillion)
	cloned.CacheWrite5mPerMillion = cloneFloat(source.CacheWrite5mPerMillion)
	cloned.CacheWrite1hPerMillion = cloneFloat(source.CacheWrite1hPerMillion)
	return &cloned
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func buildUnifiedGatewayRoutePricingAdminState(cfg UnifiedGatewayRoutePricingConfig, groups []Group, accounts []Account, activeRevision int64) *UnifiedGatewayRoutePricingAdminState {
	if cfg.Entries == nil {
		cfg.Entries = []UnifiedGatewayRoutePricingEntry{}
	}
	if cfg.WokeySync == nil {
		defaultWokey := defaultUnifiedGatewayWokeySyncConfig()
		cfg.WokeySync = &defaultWokey
	} else {
		wokey := *cfg.WokeySync
		wokey.AccountIDs = append([]int64(nil), cfg.WokeySync.AccountIDs...)
		wokey.Status.Reasons = append([]string(nil), cfg.WokeySync.Status.Reasons...)
		cfg.WokeySync = &wokey
	}
	cfg.Entries = cloneUnifiedGatewayRoutePricingEntries(cfg.Entries)
	for i := range cfg.Entries {
		entry := &cfg.Entries[i]
		if entry.Source == UnifiedGatewayWokeySource && entry.SyncState == "current" && entry.SourceFetchedAt != nil && time.Since(*entry.SourceFetchedAt) > UnifiedGatewayWokeyStaleAfter {
			entry.SyncState = "stale"
			entry.SyncReason = "catalog_stale"
		}
	}
	state := &UnifiedGatewayRoutePricingAdminState{
		Saved: cfg, ActiveRevision: activeRevision, RestartNeeded: cfg.Revision != activeRevision,
		Groups:   make([]UnifiedGatewayRoutePricingGroupOption, 0, len(groups)),
		Accounts: make([]UnifiedGatewayRoutePricingAccountOption, 0, len(accounts)),
	}
	for _, group := range groups {
		state.Groups = append(state.Groups, UnifiedGatewayRoutePricingGroupOption{ID: group.ID, Name: group.Name})
	}
	for _, account := range accounts {
		state.Accounts = append(state.Accounts, UnifiedGatewayRoutePricingAccountOption{ID: account.ID, Name: account.Name, WokeyEligible: isWokeyBaseURL(account.GetBaseURL())})
	}
	sort.Slice(state.Accounts, func(i, j int) bool { return state.Accounts[i].ID < state.Accounts[j].ID })
	return state
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
	s.routePricingMu.Lock()
	defer s.routePricingMu.Unlock()
	return s.getUnifiedGatewayRoutePricingAdminStateLocked(ctx)
}

func (s *SettingService) getUnifiedGatewayRoutePricingAdminStateLocked(ctx context.Context) (*UnifiedGatewayRoutePricingAdminState, error) {
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
	if state.Saved.WokeySync == nil {
		defaultWokey := defaultUnifiedGatewayWokeySyncConfig()
		state.Saved.WokeySync = &defaultWokey
	}
	state.Saved.Entries = cloneUnifiedGatewayRoutePricingEntries(state.Saved.Entries)
	for i := range state.Saved.Entries {
		entry := &state.Saved.Entries[i]
		if entry.Source == UnifiedGatewayWokeySource && entry.SyncState == "current" && entry.SourceFetchedAt != nil && time.Since(*entry.SourceFetchedAt) > UnifiedGatewayWokeyStaleAfter {
			entry.SyncState = "stale"
			entry.SyncReason = "catalog_stale"
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
			state.Accounts = append(state.Accounts, UnifiedGatewayRoutePricingAccountOption{ID: account.ID, Name: account.Name, WokeyEligible: isWokeyBaseURL(account.GetBaseURL())})
		}
		sort.Slice(state.Accounts, func(i, j int) bool { return state.Accounts[i].ID < state.Accounts[j].ID })
	}
	return state, nil
}

func (s *SettingService) UpdateUnifiedGatewayRoutePricing(ctx context.Context, update UnifiedGatewayRoutePricingUpdate) (*UnifiedGatewayRoutePricingAdminState, error) {
	if s == nil || s.settingRepo == nil || s.routePricingGroupRepo == nil || s.routePricingAccountRepo == nil {
		return nil, errors.New("route pricing admin dependencies are unavailable")
	}
	var response *UnifiedGatewayRoutePricingAdminState
	err := func() error {
		s.routePricingMu.Lock()
		defer s.routePricingMu.Unlock()
		groups, err := s.routePricingGroupRepo.ListActiveByPlatform(ctx, PlatformComposite)
		if err != nil {
			return err
		}
		if len(groups) != 1 {
			return errors.New("route pricing requires exactly one active Composite group")
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
			return errors.New("target must be an active Composite group")
		}
		currentRaw, readErr := s.settingRepo.GetValue(ctx, UnifiedGatewayRoutePricingSettingKey)
		var expected *string
		current := UnifiedGatewayRoutePricingConfig{Entries: []UnifiedGatewayRoutePricingEntry{}}
		currentRevision := int64(0)
		if readErr == nil {
			expected = &currentRaw
			if unmarshalErr := json.Unmarshal([]byte(currentRaw), &current); unmarshalErr != nil {
				return fmt.Errorf("decode current route pricing: %w", unmarshalErr)
			}
			currentRevision = current.Revision
		} else if !errors.Is(readErr, ErrSettingNotFound) {
			return readErr
		}
		if currentRevision != update.ExpectedRevision {
			return ErrUnifiedGatewayRoutePricingRevisionConflict
		}
		allowedAccounts, err := s.routePricingAccountRepo.ListSchedulableByGroupID(ctx, update.TargetGroupID)
		if err != nil {
			return err
		}
		allowed := make(map[int64]struct{}, len(allowedAccounts))
		for _, account := range allowedAccounts {
			allowed[account.ID] = struct{}{}
		}
		manualEntries := cloneUnifiedGatewayRoutePricingEntries(update.Entries)
		for _, entry := range manualEntries {
			if entry.Source != "" || entry.SourceFX != "" || entry.SourceFetchedAt != nil || entry.SourceCatalogSHA256 != "" || len(entry.SourceSKUs) != 0 || entry.SyncState != "" || entry.SyncReason != "" {
				return errors.New("Wokey managed entries cannot be submitted through the general route pricing update")
			}
			if _, ok := allowed[entry.AccountID]; !ok {
				return fmt.Errorf("account %d is not schedulable in the target group", entry.AccountID)
			}
		}
		wokeyCfg := defaultUnifiedGatewayWokeySyncConfig()
		if current.WokeySync != nil {
			wokeyCfg = *current.WokeySync
		}
		if update.WokeySync != nil {
			wokeyCfg.Enabled = update.WokeySync.Enabled
			wokeyCfg.AccountIDs = append([]int64(nil), update.WokeySync.AccountIDs...)
			wokeyCfg.FX = update.WokeySync.FX
			wokeyCfg.IntervalMinutes = update.WokeySync.IntervalMinutes
		}
		wokeyCfg, err = normalizeUnifiedGatewayWokeySyncConfig(wokeyCfg)
		if err != nil {
			return err
		}
		if len(wokeyCfg.AccountIDs) > 0 {
			if _, err := s.validatedWokeyAccountIDs(ctx, update.TargetGroupID, wokeyCfg.AccountIDs); err != nil {
				return err
			}
		}
		managedEntries := make([]UnifiedGatewayRoutePricingEntry, 0)
		oldFX := defaultUnifiedGatewayWokeySyncConfig().FX
		if current.WokeySync != nil {
			oldFX = current.WokeySync.FX
		}
		oldFXDecimal, _ := decimal.NewFromString(oldFX)
		newFXDecimal, _ := decimal.NewFromString(wokeyCfg.FX)
		fxChanged := !oldFXDecimal.Equal(newFXDecimal)
		for _, entry := range current.Entries {
			if entry.Source != UnifiedGatewayWokeySource {
				continue
			}
			if fxChanged {
				entry, err = recalculateWokeyManagedEntry(entry, newFXDecimal)
				if err != nil {
					return fmt.Errorf("cannot recalculate a Wokey managed card for the new FX: %w", err)
				}
			}
			managedEntries = append(managedEntries, entry)
		}
		candidate := UnifiedGatewayRoutePricingConfig{TargetGroupID: update.TargetGroupID, Revision: update.ExpectedRevision + 1, Entries: append(manualEntries, managedEntries...), WokeySync: &wokeyCfg}
		validated, err := buildUnifiedGatewayRoutePricingSnapshot(candidate)
		if err != nil {
			return err
		}
		candidate = validated.config
		encoded, err := json.Marshal(candidate)
		if err != nil {
			return err
		}
		casRepo, ok := s.settingRepo.(SettingCompareAndSetRepository)
		if !ok {
			return errors.New("settings repository does not support atomic route pricing updates")
		}
		updated, err := casRepo.CompareAndSetValue(ctx, UnifiedGatewayRoutePricingSettingKey, expected, string(encoded))
		if err != nil {
			return err
		}
		if !updated {
			return ErrUnifiedGatewayRoutePricingRevisionConflict
		}
		s.routePricingSnapshot.Store(validated)
		response = buildUnifiedGatewayRoutePricingAdminState(candidate, groups, allowedAccounts, candidate.Revision)
		return nil
	}()
	if err != nil {
		return nil, err
	}
	if err := s.reconfigureWokeyPriceSync(ctx); err != nil {
		// The accepted configuration is already saved and hot-published. A worker
		// startup error is logged without claiming the update itself was rolled back.
		logger.LegacyPrintf("service.setting", "Wokey price worker reconfiguration failed: %s", sanitizedWokeyErrorCode(err))
	}
	return response, nil
}

func (s *SettingService) ManualizeUnifiedGatewayWokeyPriceCard(ctx context.Context, request UnifiedGatewayWokeyManualizeRequest) (*UnifiedGatewayRoutePricingAdminState, error) {
	if s == nil || s.settingRepo == nil {
		return nil, errors.New("route pricing settings repository is unavailable")
	}
	if request.Key.AccountID <= 0 || strings.TrimSpace(request.Key.Model) == "" {
		return nil, errors.New("a precise route pricing key is required")
	}
	s.routePricingMu.Lock()
	defer s.routePricingMu.Unlock()
	raw, err := s.settingRepo.GetValue(ctx, UnifiedGatewayRoutePricingSettingKey)
	if err != nil {
		return nil, err
	}
	var cfg UnifiedGatewayRoutePricingConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("decode current route pricing: %w", err)
	}
	if cfg.Revision != request.ExpectedRevision {
		return nil, ErrUnifiedGatewayRoutePricingRevisionConflict
	}
	key := unifiedGatewayRoutePricingKey(cfg.TargetGroupID, request.Key.AccountID, request.Key.Model, request.Key.Kind,
		request.Key.ImagePricingMode, request.Key.ImageSize, request.Key.ImageQuality, request.Key.VideoResolution, request.Key.VideoDurationSeconds)
	found := false
	for i := range cfg.Entries {
		entry := &cfg.Entries[i]
		entryKey := unifiedGatewayRoutePricingKey(cfg.TargetGroupID, entry.AccountID, entry.Model, entry.Kind,
			entry.ImagePricingMode, entry.ImageSize, entry.ImageQuality, entry.VideoResolution, entry.VideoDurationSeconds)
		if entryKey != key {
			continue
		}
		if entry.Source != UnifiedGatewayWokeySource {
			return nil, errors.New("route pricing card is not managed by the Wokey catalog")
		}
		if entry.TimeOfDayTokenPrice != nil {
			return nil, ErrUnifiedGatewayWokeyTimeOfDayManualizeUnsupported
		}
		entry.Source = ""
		entry.SourceFX = ""
		entry.SourceFetchedAt = nil
		entry.SourceCatalogSHA256 = ""
		entry.SourceSKUs = nil
		entry.SyncState = ""
		entry.SyncReason = ""
		found = true
		break
	}
	if !found {
		return nil, errors.New("Wokey managed route pricing card was not found")
	}
	cfg.Revision++
	if cfg.WokeySync != nil {
		managed := 0
		for _, entry := range cfg.Entries {
			if entry.Source == UnifiedGatewayWokeySource {
				managed++
			}
		}
		cfg.WokeySync.Status.ManagedCardCount = managed
	}
	snapshot, err := buildUnifiedGatewayRoutePricingSnapshot(cfg)
	if err != nil {
		return nil, err
	}
	groups, err := s.routePricingGroupRepo.ListActiveByPlatform(ctx, PlatformComposite)
	if err != nil {
		return nil, err
	}
	if len(groups) != 1 {
		return nil, errors.New("route pricing requires exactly one active Composite group")
	}
	accounts, err := s.routePricingAccountRepo.ListSchedulableByGroupID(ctx, cfg.TargetGroupID)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(snapshot.config)
	if err != nil {
		return nil, err
	}
	casRepo, ok := s.settingRepo.(SettingCompareAndSetRepository)
	if !ok {
		return nil, errors.New("settings repository does not support atomic route pricing updates")
	}
	updated, err := casRepo.CompareAndSetValue(ctx, UnifiedGatewayRoutePricingSettingKey, &raw, string(encoded))
	if err != nil {
		return nil, err
	}
	if !updated {
		return nil, ErrUnifiedGatewayRoutePricingRevisionConflict
	}
	s.routePricingSnapshot.Store(snapshot)
	return buildUnifiedGatewayRoutePricingAdminState(snapshot.config, groups, accounts, snapshot.config.Revision), nil
}

var ErrUnifiedGatewayRoutePricingRevisionConflict = infraerrors.Conflict("ROUTE_PRICING_REVISION_CONFLICT", "route pricing changed by another administrator; reload and retry")
var ErrUnifiedGatewayWokeyTimeOfDayManualizeUnsupported = infraerrors.Conflict("WOKEY_TIME_OF_DAY_MANUALIZE_UNSUPPORTED", "time-of-day Wokey price cards cannot be converted to a static manual card")

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
		if entry.Kind == UnifiedGatewayRoutePricingToken && (entry.TokenBasePrice != nil || entry.TimeOfDayTokenPrice != nil) {
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
		if entry.AccountID == accountID && entry.Model == model && entry.Kind == UnifiedGatewayRoutePricingToken && (entry.TokenBasePrice != nil || entry.TimeOfDayTokenPrice != nil) {
			return true
		}
	}
	return false
}

func (d UnifiedGatewayRoutePricingDecision) HasLongContextTokenBasePriceFor(accountID int64, model string) bool {
	if d.Kind != UnifiedGatewayRoutePricingToken || model == "" || d.Model != model {
		return false
	}
	for _, entry := range d.Entries {
		if entry.AccountID == accountID && entry.Model == model && entry.Kind == UnifiedGatewayRoutePricingToken && entry.LongContextTokenBasePrice != nil {
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
		if entry.Kind == UnifiedGatewayRoutePricingToken && (entry.TokenBasePrice.hasPositiveRate() || entry.LongContextTokenBasePrice.hasPositiveRate() || entry.TimeOfDayTokenPrice.hasPositiveRate()) {
			return true
		}
	}
	return false
}

func unifiedGatewayLongContextRouteCardAvailable(ctx context.Context, accountID int64, model string) bool {
	decision, ok := UnifiedGatewayRoutePricingDecisionFromContext(ctx)
	return ok && decision.HasLongContextTokenBasePriceFor(accountID, model)
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
	PricingAt      time.Time
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
	entry, ok := snapshot.entries[unifiedGatewayRoutePricingKey(groupID, accountID, model, kind, "", imageSize, imageQuality, videoResolution, videoDuration)]
	if !ok && kind == UnifiedGatewayRoutePricingImage {
		entry, ok = snapshot.entries[unifiedGatewayRoutePricingKey(groupID, accountID, model, kind, UnifiedGatewayImagePricingFlatPerImage, "", "", "", 0)]
	}
	return entry, ok
}

func unifiedGatewayRoutePricingMaxEntries(snapshot *unifiedGatewayRoutePricingSnapshot, model string, kind UnifiedGatewayRoutePricingKind, imageSize, imageQuality, videoResolution string, videoDuration int) []UnifiedGatewayRoutePricingEntry {
	if snapshot == nil {
		return nil
	}
	entries := make([]UnifiedGatewayRoutePricingEntry, 0)
	for _, entry := range snapshot.config.Entries {
		if entry.Model != strings.TrimSpace(model) || entry.Kind != kind {
			continue
		}
		switch kind {
		case UnifiedGatewayRoutePricingImage:
			if entry.ImagePricingMode == UnifiedGatewayImagePricingFlatPerImage ||
				(imageSize != "" && imageQuality != "" && entry.ImagePricingMode == "" && entry.ImageSize == imageSize && entry.ImageQuality == imageQuality) {
				entries = append(entries, entry)
			}
		case UnifiedGatewayRoutePricingToken:
			entries = append(entries, entry)
		case UnifiedGatewayRoutePricingVideo:
			if entry.VideoResolution == videoResolution && entry.VideoDurationSeconds == videoDuration {
				entries = append(entries, entry)
			}
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
		if requestedSize != "" && !strings.EqualFold(requestedSize, "auto") {
			imageSize, _ = ClassifyImageBillingTier(requestedSize)
		}
		imageQuality = strings.ToLower(strings.TrimSpace(req.ImageQuality))
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
	entries := unifiedGatewayRoutePricingMaxEntries(snapshot, req.Model, kind, imageSize, imageQuality, videoResolution, videoDuration)
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
			lineMultiplier := 1.0
			if entry.Multiplier != nil {
				lineMultiplier = *entry.Multiplier
			}
			for _, card := range unifiedGatewayRouteTokenPriceCards(entry) {
				if card == nil {
					continue
				}
				cardEstimate := (float64(inputTokens)*card.inputSideMaxPerToken() + float64(outputTokens)*(*card.OutputPerMillion)/1_000_000) * lineMultiplier * tokenRateMultiplier
				if cardEstimate > routeEstimate {
					routeEstimate = cardEstimate
				}
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
		if entry.TimeOfDayTokenPrice != nil && (tokenUsage == nil || !tokenUsage.Eligible || tokenUsage.PricingAt.IsZero()) {
			// A dynamic Wokey card must use the original request snapshot. Do not
			// substitute settlement time or apply a dynamic card's line fallback.
			return cost
		}
		if tokenUsage != nil && tokenUsage.Eligible {
			lineMultiplier := 1.0
			if entry.Multiplier != nil {
				lineMultiplier = *entry.Multiplier
			}
			card, covered := unifiedGatewayTokenBasePriceForRequest(entry, tokenUsage.Tokens, tokenUsage.PricingAt)
			if covered {
				if baseCost, ok := calculateUnifiedGatewayTokenBasePrice(card, tokenUsage.Tokens, tokenUsage.RateMultiplier*lineMultiplier); ok {
					if cost == nil {
						cost = &CostBreakdown{BillingMode: string(BillingModeToken)}
					}
					cost.ActualCost = baseCost.ActualCost
					cost.routePricingApplied = true
					return cost
				}
			}
		}
		if cost == nil || entry.Multiplier == nil || !finiteNonNegative(cost.ActualCost**entry.Multiplier) {
			return cost
		}
		cost.ActualCost *= *entry.Multiplier
	case UnifiedGatewayRoutePricingImage:
		if imageCount <= 0 || entry.UnitPrice == nil {
			return cost
		}
		if entry.ImagePricingMode == UnifiedGatewayImagePricingFlatPerImage {
			// A flat card intentionally ignores requested and returned size/quality.
		} else {
			actualImageSize, sizeKnown := ClassifyImageBillingTier(imageBillingSize)
			actualQuality := strings.ToLower(strings.TrimSpace(imageQuality))
			if entry.ImagePricingMode != "" || entry.ImageSize != decision.ImageSize || entry.ImageQuality != decision.ImageQuality || !sizeKnown || actualImageSize != decision.ImageSize || actualQuality == "" || actualQuality != decision.ImageQuality {
				return cost
			}
			if len(imageSizeBreakdown) > 0 && (len(imageSizeBreakdown) != 1 || imageSizeBreakdown[decision.ImageSize] != imageCount) {
				return cost
			}
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

func unifiedGatewayPromptTokensExceedHaikuThreshold(tokens UsageTokens) bool {
	total := 0
	cacheCreationCounts := []int{tokens.CacheCreationTokens}
	if tokens.CacheCreationTokens <= 0 {
		cacheFive, cacheOneHour := normalizeCacheCreationBreakdown(tokens)
		cacheCreationCounts = []int{cacheFive, cacheOneHour}
	}
	counts := []int{tokens.InputTokens, tokens.CacheReadTokens}
	counts = append(counts, cacheCreationCounts...)
	for _, count := range counts {
		if count <= 0 {
			continue
		}
		if count > UnifiedGatewayHaikuLongContextThreshold-total {
			return true
		}
		total += count
	}
	return false
}

func unifiedGatewayHasCachedPromptTokens(tokens UsageTokens) bool {
	return tokens.CacheReadTokens > 0 || tokens.CacheCreationTokens > 0 || tokens.CacheCreation5mTokens > 0 || tokens.CacheCreation1hTokens > 0
}

func unifiedGatewayTokenBasePriceForUsage(entry *UnifiedGatewayRoutePricingEntry, tokens UsageTokens) (*UnifiedGatewayTokenBasePrice, bool) {
	if entry == nil || entry.TokenBasePrice == nil {
		return nil, false
	}
	if entry.Model != "claude-haiku-5-5" || !unifiedGatewayPromptTokensExceedHaikuThreshold(tokens) {
		return entry.TokenBasePrice, true
	}
	if entry.LongContextTokenBasePrice == nil {
		return nil, false
	}
	if entry.Source == UnifiedGatewayWokeySource && unifiedGatewayHasCachedPromptTokens(tokens) && !unifiedGatewayWokeyCachePromptThresholdVerified {
		return nil, false
	}
	return entry.LongContextTokenBasePrice, true
}

func unifiedGatewayRouteTokenPriceCards(entry UnifiedGatewayRoutePricingEntry) []*UnifiedGatewayTokenBasePrice {
	if entry.TimeOfDayTokenPrice != nil {
		return []*UnifiedGatewayTokenBasePrice{
			entry.TimeOfDayTokenPrice.Peak.TokenBasePrice,
			entry.TimeOfDayTokenPrice.OffPeak.TokenBasePrice,
		}
	}
	return []*UnifiedGatewayTokenBasePrice{entry.TokenBasePrice, entry.LongContextTokenBasePrice}
}

func unifiedGatewayTokenBasePriceForRequest(entry *UnifiedGatewayRoutePricingEntry, tokens UsageTokens, pricingAt time.Time) (*UnifiedGatewayTokenBasePrice, bool) {
	if entry == nil {
		return nil, false
	}
	if entry.TimeOfDayTokenPrice != nil {
		return entry.TimeOfDayTokenPrice.priceAt(pricingAt)
	}
	return unifiedGatewayTokenBasePriceForUsage(entry, tokens)
}

func (p *UnifiedGatewayWokeyTimeOfDayTokenPrice) priceAt(pricingAt time.Time) (*UnifiedGatewayTokenBasePrice, bool) {
	if p == nil || pricingAt.IsZero() {
		return nil, false
	}
	hour := pricingAt.UTC().Hour()
	for _, window := range p.PeakWindowsUTC {
		if hour >= window.StartHour && hour < window.EndHour {
			return p.Peak.TokenBasePrice, p.Peak.TokenBasePrice != nil
		}
	}
	return p.OffPeak.TokenBasePrice, p.OffPeak.TokenBasePrice != nil
}

func unifiedGatewayUsageTokensAreNonNegative(tokens UsageTokens) bool {
	return tokens.InputTokens >= 0 &&
		tokens.OutputTokens >= 0 &&
		tokens.CacheReadTokens >= 0 &&
		tokens.CacheCreationTokens >= 0 &&
		tokens.CacheCreation5mTokens >= 0 &&
		tokens.CacheCreation1hTokens >= 0 &&
		tokens.ImageInputTokens >= 0 &&
		tokens.ImageCacheReadTokens >= 0 &&
		tokens.ImageOutputTokens >= 0
}

func calculateUnifiedGatewayTokenBasePrice(card *UnifiedGatewayTokenBasePrice, tokens UsageTokens, rateMultiplier float64) (*CostBreakdown, bool) {
	if !card.validate() || !finiteNonNegative(rateMultiplier) || !unifiedGatewayUsageTokensAreNonNegative(tokens) || tokens.ImageInputTokens > 0 || tokens.ImageCacheReadTokens > 0 || tokens.ImageOutputTokens > 0 {
		return nil, false
	}
	cacheFive, cacheOneHour := normalizeCacheCreationBreakdown(tokens)
	cacheWriteResidual := 0
	if tokens.CacheCreationTokens > 0 {
		// normalizeCacheCreationBreakdown preserves an under-reported detail sum
		// and caps contradictory details at the aggregate, so only the remaining
		// positive aggregate amount uses the generic cache-write rate.
		cacheWriteResidual = tokens.CacheCreationTokens - cacheFive - cacheOneHour
	}
	if cacheOneHour > 0 && (card.CacheWrite1hPerMillion == nil || *card.CacheWrite1hPerMillion == 0) {
		return nil, false
	}
	if cacheFive > 0 && card.CacheWrite5mPerMillion == nil {
		return nil, false
	}
	if cacheWriteResidual > 0 && card.CacheWritePerMillion == nil {
		return nil, false
	}
	cacheWriteRate := 0.0
	if card.CacheWritePerMillion != nil {
		cacheWriteRate = *card.CacheWritePerMillion
	}
	pricing := &ModelPricing{
		InputPricePerToken:         *card.InputPerMillion / 1_000_000,
		OutputPricePerToken:        *card.OutputPerMillion / 1_000_000,
		CacheReadPricePerToken:     *card.CacheReadPerMillion / 1_000_000,
		CacheCreationPricePerToken: cacheWriteRate / 1_000_000,
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
		if cacheFive > 0 {
			cacheCreation += float64(cacheFive) * (*card.CacheWrite5mPerMillion / 1_000_000)
		}
		if cacheOneHour > 0 {
			cacheCreation += float64(cacheOneHour) * (*card.CacheWrite1hPerMillion / 1_000_000)
		}
	}
	if cacheWriteResidual > 0 {
		cacheCreation += float64(cacheWriteResidual) * (*card.CacheWritePerMillion / 1_000_000)
	}
	cost.CacheCreationCost = cacheCreation
	cost.TotalCost += cacheCreation
	cost.ActualCost += cacheCreation * rateMultiplier
	if !finiteNonNegative(cost.TotalCost) || !finiteNonNegative(cost.ActualCost) {
		return nil, false
	}
	return cost, true
}
