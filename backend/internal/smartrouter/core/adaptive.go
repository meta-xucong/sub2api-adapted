package core

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// AdaptiveTimeoutConfig controls per-lane, per-capability upstream timeouts.
// The engine is deliberately provider-neutral and can be used by a sidecar.
type AdaptiveTimeoutConfig struct {
	Enabled                  bool
	Default                  time.Duration
	Min                      time.Duration
	Max                      time.Duration
	SafetyMargin             time.Duration
	Multiplier               float64
	FailureBackoffMultiplier float64
	WindowSize               int
	ReservePerAttempt        time.Duration
}

func DefaultAdaptiveTimeoutConfig() AdaptiveTimeoutConfig {
	return AdaptiveTimeoutConfig{
		Default:                  180 * time.Second,
		Min:                      30 * time.Second,
		Max:                      300 * time.Second,
		SafetyMargin:             20 * time.Second,
		Multiplier:               1.25,
		FailureBackoffMultiplier: 0.5,
		WindowSize:               32,
		ReservePerAttempt:        30 * time.Second,
	}
}

func (c AdaptiveTimeoutConfig) Normalize() AdaptiveTimeoutConfig {
	d := DefaultAdaptiveTimeoutConfig()
	if c.Default <= 0 {
		c.Default = d.Default
	}
	if c.Min <= 0 {
		c.Min = d.Min
	}
	if c.Max <= 0 {
		c.Max = d.Max
	}
	if c.Max < c.Min {
		c.Max = c.Min
	}
	if c.SafetyMargin < 0 {
		c.SafetyMargin = d.SafetyMargin
	}
	if c.Multiplier <= 0 {
		c.Multiplier = d.Multiplier
	}
	if c.FailureBackoffMultiplier <= 0 {
		c.FailureBackoffMultiplier = d.FailureBackoffMultiplier
	}
	if c.FailureBackoffMultiplier > 1 {
		c.FailureBackoffMultiplier = 1
	}
	if c.WindowSize <= 0 {
		c.WindowSize = d.WindowSize
	}
	if c.ReservePerAttempt <= 0 {
		c.ReservePerAttempt = d.ReservePerAttempt
	}
	return c
}

type timeoutKey struct {
	laneID         string
	capability     Capability
	imageSizeTier  string
	imageInputMode ImageInputMode
	imageModel     string
}

type timeoutProfile struct {
	samples                []time.Duration
	ewma                   time.Duration
	lastFailure            time.Duration
	transientFailureStreak int
}

// AttemptObservation is the only input required to update the adaptive
// profile. Callers should report the whole upstream attempt when possible.
type AttemptObservation struct {
	LaneID           string
	Capability       Capability
	ImageSizeTier    string
	ImageInputMode   ImageInputMode
	ImageModelFamily string
	At               time.Time
	Duration         time.Duration
	Success          bool
	FailureClass     FailureClass
	StatusCode       int
	ErrorSummary     string
}

// LedgerRecord is a bounded audit record. It intentionally contains no
// request body, credential, URL, or user content.
type LedgerRecord struct {
	At               time.Time
	LaneID           string
	Capability       Capability
	ImageSizeTier    string
	ImageInputMode   ImageInputMode
	ImageModelFamily string
	DurationMs       int64
	Success          bool
	FailureClass     FailureClass
	StatusCode       int
	ErrorSummary     string
}

type TimeoutRequest struct {
	LaneID                   string
	Capability               Capability
	ImageSizeTier            string
	ImageInputMode           ImageInputMode
	ImageModelFamily         string
	DefaultTimeout           time.Duration
	RemainingBudget          time.Duration
	RemainingAttempts        int
	ProfileDefault           time.Duration
	ProfileMin               time.Duration
	ProfileMax               time.Duration
	ProfileSafetyMargin      time.Duration
	ProfileMultiplier        float64
	ProfileReservePerAttempt time.Duration
}

type TimeoutDecision struct {
	Timeout          time.Duration
	EstimatedLatency time.Duration
	Reason           string
}

// AdaptiveTimeoutEngine owns the runtime profiles and bounded ledger for one
// router process. A restart clears only the optimization cache; durable usage
// logs remain the source of truth for later calibration.
type AdaptiveTimeoutEngine struct {
	mu       sync.RWMutex
	config   AdaptiveTimeoutConfig
	profiles map[timeoutKey]*timeoutProfile
	records  []LedgerRecord
}

func NewAdaptiveTimeoutEngine(config AdaptiveTimeoutConfig) *AdaptiveTimeoutEngine {
	config = config.Normalize()
	return &AdaptiveTimeoutEngine{
		config:   config,
		profiles: make(map[timeoutKey]*timeoutProfile),
		records:  make([]LedgerRecord, 0, config.WindowSize),
	}
}

func (e *AdaptiveTimeoutEngine) Config() AdaptiveTimeoutConfig {
	if e == nil {
		return DefaultAdaptiveTimeoutConfig()
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.config
}

func (e *AdaptiveTimeoutEngine) TimeoutFor(request TimeoutRequest) TimeoutDecision {
	if e == nil {
		return TimeoutDecision{Timeout: request.DefaultTimeout, Reason: "engine_unavailable"}
	}
	e.mu.RLock()
	config := e.config
	profileConfig := config
	if request.ProfileSafetyMargin > 0 {
		profileConfig.SafetyMargin = request.ProfileSafetyMargin
	}
	if request.ProfileMultiplier > 0 {
		profileConfig.Multiplier = request.ProfileMultiplier
	}
	profile := e.profiles[makeTimeoutKey(request.LaneID, request.Capability, request.ImageSizeTier, request.ImageInputMode, request.ImageModelFamily)]
	var estimate time.Duration
	failureBackoff := false
	if profile != nil {
		estimate, failureBackoff = adaptiveEstimate(profile, profileConfig)
	}
	e.mu.RUnlock()

	base := request.DefaultTimeout
	if request.ProfileDefault > 0 {
		base = request.ProfileDefault
	}
	if base <= 0 {
		base = config.Default
	}
	if !config.Enabled {
		return TimeoutDecision{Timeout: base, Reason: "disabled"}
	}
	if estimate <= 0 {
		estimate = base
	}
	minimum := config.Min
	if request.ProfileMin > 0 {
		minimum = request.ProfileMin
	}
	maximum := config.Max
	if request.ProfileMax > 0 {
		maximum = request.ProfileMax
	}
	if maximum < minimum {
		maximum = minimum
	}
	if estimate < minimum {
		estimate = minimum
	}
	if estimate > maximum {
		estimate = maximum
	}
	if request.RemainingBudget > 0 {
		available := request.RemainingBudget
		remainingAttempts := request.RemainingAttempts
		if remainingAttempts <= 0 {
			remainingAttempts = 1
		}
		reservePerAttempt := config.ReservePerAttempt
		if request.ProfileReservePerAttempt > 0 {
			reservePerAttempt = request.ProfileReservePerAttempt
		}
		reserve := reservePerAttempt * time.Duration(remainingAttempts)
		if available > reserve {
			available -= reserve
		}
		if available > 0 && estimate > available {
			estimate = available
		}
	}
	if estimate <= 0 {
		estimate = minDuration(base, request.RemainingBudget)
	}
	reason := "default"
	if failureBackoff {
		reason = "failure_backoff"
	} else if profile != nil && len(profile.samples) > 0 {
		reason = "observed_p95"
	}
	return TimeoutDecision{Timeout: estimate, EstimatedLatency: estimate, Reason: reason}
}

func (e *AdaptiveTimeoutEngine) Observe(observation AttemptObservation) {
	if e == nil {
		return
	}
	laneID := strings.TrimSpace(observation.LaneID)
	if laneID == "" || observation.Capability == "" {
		return
	}
	if observation.At.IsZero() {
		observation.At = time.Now()
	}
	if observation.Duration < 0 {
		observation.Duration = 0
	}
	key := makeTimeoutKey(laneID, observation.Capability, observation.ImageSizeTier, observation.ImageInputMode, observation.ImageModelFamily)
	e.mu.Lock()
	defer e.mu.Unlock()
	profile := e.profiles[key]
	if profile == nil {
		profile = &timeoutProfile{}
		e.profiles[key] = profile
	}
	if observation.Success {
		if observation.Duration > 0 {
			if len(profile.samples) >= e.config.WindowSize {
				copy(profile.samples, profile.samples[1:])
				profile.samples[len(profile.samples)-1] = observation.Duration
			} else {
				profile.samples = append(profile.samples, observation.Duration)
			}
			if profile.ewma <= 0 {
				profile.ewma = observation.Duration
			} else {
				const alpha = 0.2
				profile.ewma = time.Duration(alpha*float64(observation.Duration) + (1-alpha)*float64(profile.ewma))
			}
		}
		profile.transientFailureStreak = 0
		profile.lastFailure = 0
	} else {
		if isAdaptiveFailure(observation.FailureClass) {
			profile.transientFailureStreak++
			if observation.Duration > 0 {
				profile.lastFailure = observation.Duration
			}
		}
	}
	e.records = append(e.records, LedgerRecord{
		At:               observation.At,
		LaneID:           laneID,
		Capability:       observation.Capability,
		ImageSizeTier:    normalizeImageSizeTier(observation.ImageSizeTier),
		ImageInputMode:   normalizeImageInputMode(observation.ImageInputMode),
		ImageModelFamily: normalizeImageModelFamily(observation.ImageModelFamily),
		DurationMs:       observation.Duration.Milliseconds(),
		Success:          observation.Success,
		FailureClass:     observation.FailureClass,
		StatusCode:       observation.StatusCode,
		ErrorSummary:     truncateLedgerSummary(observation.ErrorSummary),
	})
	if len(e.records) > e.config.WindowSize*4 {
		copy(e.records, e.records[len(e.records)-e.config.WindowSize*4:])
		e.records = e.records[:e.config.WindowSize*4]
	}
}

func (e *AdaptiveTimeoutEngine) Ledger() []LedgerRecord {
	if e == nil {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]LedgerRecord(nil), e.records...)
}

func adaptiveEstimate(profile *timeoutProfile, config AdaptiveTimeoutConfig) (time.Duration, bool) {
	var estimate time.Duration
	if len(profile.samples) > 0 {
		samples := append([]time.Duration(nil), profile.samples...)
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		p95 := samples[(len(samples)*95+99)/100-1]
		estimate = time.Duration(float64(p95)*config.Multiplier) + config.SafetyMargin
		if profile.ewma > estimate {
			estimate = profile.ewma + config.SafetyMargin
		}
	}
	if profile.transientFailureStreak > 0 && profile.lastFailure > 0 {
		failureEstimate := time.Duration(float64(profile.lastFailure) * math.Pow(config.FailureBackoffMultiplier, float64(profile.transientFailureStreak)))
		if estimate <= 0 || failureEstimate < estimate {
			estimate = failureEstimate
		}
		return estimate, true
	}
	return estimate, false
}

func isAdaptiveFailure(class FailureClass) bool {
	switch class {
	case FailureTransientForbidden, FailureRateLimited, FailureUpstream5xx, FailureTimeout, FailureUnknown:
		return true
	default:
		return false
	}
}

func makeTimeoutKey(laneID string, capability Capability, sizeTier string, inputMode ImageInputMode, modelFamily string) timeoutKey {
	key := timeoutKey{laneID: strings.TrimSpace(laneID), capability: capability}
	if capability == CapabilityImageGeneration || capability == CapabilityImageEdit {
		key.imageSizeTier = normalizeImageSizeTier(sizeTier)
		key.imageInputMode = normalizeImageInputMode(inputMode)
		key.imageModel = normalizeImageModelFamily(modelFamily)
	}
	return key
}

func normalizeImageInputMode(value ImageInputMode) ImageInputMode {
	switch value {
	case ImageInputTextOnly, ImageInputReferenceImage:
		return value
	default:
		return ""
	}
}

func normalizeImageModelFamily(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func minDuration(left, right time.Duration) time.Duration {
	if right > 0 && (left <= 0 || right < left) {
		return right
	}
	return left
}

func truncateLedgerSummary(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	lower := strings.ToLower(value)
	for _, marker := range []string{"token", "secret", "password", "api_key", "apikey", "cookie"} {
		if strings.Contains(lower, marker) {
			return "redacted_error_summary"
		}
	}
	if len(value) > 256 {
		return value[:256]
	}
	return value
}
