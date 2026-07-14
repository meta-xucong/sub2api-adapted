package core

import (
	"math"
	"strings"
	"sync"
	"time"
)

// HealthPolicy controls the local, capability-scoped circuit breaker. It is
// deliberately independent of the persistence layer so a deployment can use
// memory, Redis, or PostgreSQL without changing routing behavior.
type HealthPolicy struct {
	TransientCooldown              time.Duration
	SecondTransientCooldown        time.Duration
	SustainedFailureThreshold      int
	ImageSustainedFailureThreshold int
	SustainedFailureUntil          func(time.Time) time.Time
	RateLimitCooldown              time.Duration
	CapabilityQuarantine           time.Duration
	AuthQuarantine                 time.Duration
	MaxCooldown                    time.Duration
	MaxPenalty                     int
	RecoverySuccessesToNormal      int
	// RecoveryEscalationFailureThreshold is the number of consecutive
	// failures required to move an already degraded lane another recovery
	// step backward in the FIFO. Each step adds RecoveryPriorityStep.
	RecoveryEscalationFailureThreshold int
	RecoveryPriorityStep               int
	ErrorRateAlpha                     float64
}

func DefaultHealthPolicy() HealthPolicy {
	return HealthPolicy{
		TransientCooldown:    30 * time.Second,
		RateLimitCooldown:    5 * time.Minute,
		CapabilityQuarantine: 24 * time.Hour,
		AuthQuarantine:       24 * time.Hour,
		MaxCooldown:          6 * time.Hour,
		// Leave room to move a lane behind a growing pool of alternatives. This
		// changes only effective routing priority, never persisted account priority.
		MaxPenalty:                         32,
		RecoverySuccessesToNormal:          3,
		RecoveryEscalationFailureThreshold: 3,
		RecoveryPriorityStep:               30,
		ErrorRateAlpha:                     0.2,
	}
}

func (p HealthPolicy) normalize() HealthPolicy {
	d := DefaultHealthPolicy()
	if p.TransientCooldown <= 0 {
		p.TransientCooldown = d.TransientCooldown
	}
	if p.RateLimitCooldown <= 0 {
		p.RateLimitCooldown = d.RateLimitCooldown
	}
	if p.CapabilityQuarantine <= 0 {
		p.CapabilityQuarantine = d.CapabilityQuarantine
	}
	if p.AuthQuarantine <= 0 {
		p.AuthQuarantine = d.AuthQuarantine
	}
	if p.MaxCooldown <= 0 {
		p.MaxCooldown = d.MaxCooldown
	}
	if p.MaxPenalty <= 0 {
		p.MaxPenalty = d.MaxPenalty
	}
	if p.RecoverySuccessesToNormal <= 0 {
		p.RecoverySuccessesToNormal = d.RecoverySuccessesToNormal
	}
	if p.RecoveryEscalationFailureThreshold <= 0 {
		p.RecoveryEscalationFailureThreshold = d.RecoveryEscalationFailureThreshold
	}
	if p.RecoveryPriorityStep <= 0 {
		p.RecoveryPriorityStep = d.RecoveryPriorityStep
	}
	if p.ErrorRateAlpha <= 0 || p.ErrorRateAlpha > 1 || math.IsNaN(p.ErrorRateAlpha) || math.IsInf(p.ErrorRateAlpha, 0) {
		p.ErrorRateAlpha = d.ErrorRateAlpha
	}
	return p
}

type HealthKey struct {
	LaneID     string
	Capability Capability
	Model      string
}

func NewHealthKey(laneID string, capability Capability, model string) HealthKey {
	return HealthKey{
		LaneID:     strings.TrimSpace(laneID),
		Capability: capability,
		Model:      modelFamily(model),
	}
}

type HealthSnapshot struct {
	HealthPenalty int
	// RecoveryPriority is an absolute, temporary priority slot assigned by
	// Smart Router. The first degraded lane enters the global capability/model
	// FIFO at base priority + 30; repeated failure windows add another 30.
	// It never overwrites the account's base price priority.
	RecoveryPriority     int
	HealthScore          float64
	ErrorRateEWMA        float64
	CooldownUntilUnix    int64
	RecoveryStage        RecoveryStage
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
}

type HealthEvent struct {
	OccurredAtUnix       int64
	Source               string
	Key                  HealthKey
	AccountID            int64
	SourceGroup          string
	StatusCode           int
	FailureClass         FailureClass
	Success              bool
	Action               string
	CooldownUntilUnix    int64
	HealthPenalty        int
	RecoveryPriority     int
	HealthScore          float64
	ErrorRateEWMA        float64
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	RecoveryStage        RecoveryStage
	LatencyMs            int64
	ErrorSummary         string
}

// Restore seeds a tracker from durable state after a process restart. The
// caller owns persistence; the core deliberately does not depend on storage.
func (t *HealthTracker) Restore(key HealthKey, snapshot HealthSnapshot, lastFailureUnix int64) {
	t.RestoreWithSourceGroup(key, snapshot, lastFailureUnix, "")
}

// RestoreWithSourceGroup restores a durable lane state and re-registers its
// recovery slot so a process restart does not reorder degraded lanes.
func (t *HealthTracker) RestoreWithSourceGroup(key HealthKey, snapshot HealthSnapshot, lastFailureUnix int64, sourceGroup string) {
	if t == nil || key.LaneID == "" || key.Capability == "" {
		return
	}
	t.mu.Lock()
	state := &healthState{
		HealthSnapshot:  snapshot,
		lastFailureUnix: lastFailureUnix,
		sourceGroup:     normalizeRecoverySourceGroup(sourceGroup, key),
	}
	t.states[key] = state
	if snapshot.RecoveryPriority >= firstRecoveryPriority {
		state.RecoveryPriority = t.registerRecoverySlotLocked(key, snapshot.RecoveryPriority)
	}
	t.mu.Unlock()
}

// HealthEventSink is the ledger boundary. The core never writes credentials,
// prompts, or raw upstream bodies; callers may persist this safe event shape.
type HealthEventSink func(HealthEvent)

type healthState struct {
	HealthSnapshot
	lastFailureUnix           int64
	sourceGroup               string
	recoveryFailuresSinceStep int
	basePriority              int
}

type recoverySlotKey struct {
	capability  Capability
	modelFamily string
}

type HealthTracker struct {
	mu            sync.Mutex
	policy        HealthPolicy
	now           func() time.Time
	sink          HealthEventSink
	states        map[HealthKey]*healthState
	recoverySlots map[recoverySlotKey]map[HealthKey]int
	events        []HealthEvent
	maxEvents     int
}

const firstRecoveryPriority = 30

func NewHealthTracker(policy HealthPolicy, now func() time.Time, sink HealthEventSink) *HealthTracker {
	if now == nil {
		now = time.Now
	}
	return &HealthTracker{
		policy:        policy.normalize(),
		now:           now,
		sink:          sink,
		states:        make(map[HealthKey]*healthState),
		recoverySlots: make(map[recoverySlotKey]map[HealthKey]int),
		maxEvents:     4096,
	}
}

// EventsSince returns a bounded in-process ledger view. A production adapter
// may also persist events through the sink; retaining a small local window is
// useful for diagnostics when no external ledger is configured.
func (t *HealthTracker) EventsSince(occurredAfterUnix int64) []HealthEvent {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]HealthEvent, 0, len(t.events))
	for _, event := range t.events {
		if event.OccurredAtUnix > occurredAfterUnix {
			out = append(out, event)
		}
	}
	return out
}

func (t *HealthTracker) Snapshot(lane LaneSnapshot, capability Capability, model string, nowUnix int64) LaneSnapshot {
	if t == nil {
		return lane
	}
	key := NewHealthKey(lane.LaneID, capability, model)
	t.mu.Lock()
	state := t.states[key]
	if state == nil {
		t.mu.Unlock()
		if lane.RecoveryStage == "" {
			lane.RecoveryStage = RecoveryNormal
		}
		return lane
	}
	if nowUnix <= 0 {
		nowUnix = t.now().Unix()
	}
	if state.CooldownUntilUnix > 0 && state.CooldownUntilUnix <= nowUnix {
		state.CooldownUntilUnix = 0
		if state.RecoveryStage == RecoveryCooling {
			state.RecoveryStage = RecoveryProbeDue
		}
	}
	snapshot := state.HealthSnapshot
	t.mu.Unlock()

	if snapshot.RecoveryPriority >= firstRecoveryPriority {
		lane.Priority = snapshot.RecoveryPriority
		lane.PriorityPenalty = 0
	} else if snapshot.HealthPenalty > 0 {
		lane.Priority += snapshot.HealthPenalty
	}
	if snapshot.HealthScore > 0 {
		lane.HealthScore = snapshot.HealthScore
	}
	lane.ErrorRateEWMA = snapshot.ErrorRateEWMA
	lane.CooldownUntilUnix = snapshot.CooldownUntilUnix
	lane.RecoveryStage = snapshot.RecoveryStage
	return lane
}

func (t *HealthTracker) Observe(result RouteResult) HealthSnapshot {
	if t == nil {
		return HealthSnapshot{}
	}
	now := t.now()
	if now.IsZero() {
		now = time.Now()
	}
	key := NewHealthKey(result.LaneID, result.Capability, result.Model)
	t.mu.Lock()
	state := t.states[key]
	if state == nil {
		state = &healthState{HealthSnapshot: HealthSnapshot{HealthScore: 1, RecoveryStage: RecoveryNormal}, sourceGroup: normalizeRecoverySourceGroup(result.SourceGroup, key)}
		t.states[key] = state
	} else if strings.TrimSpace(result.SourceGroup) != "" {
		state.sourceGroup = normalizeRecoverySourceGroup(result.SourceGroup, key)
	}
	if result.BasePriority > 0 && state.basePriority == 0 {
		state.basePriority = result.BasePriority
	}
	class := result.ErrorClass
	if !result.Success && class == "" {
		class = ClassifyFailure(result.StatusCode, result.Capability, false)
	}
	action := "record_only"
	if result.Success {
		state.ConsecutiveSuccesses++
		state.ConsecutiveFailures = 0
		state.recoveryFailuresSinceStep = 0
		state.ErrorRateEWMA *= 1 - t.policy.ErrorRateAlpha
		state.HealthScore = minFloat(1, maxFloat(0.05, state.HealthScore+0.12))
		state.CooldownUntilUnix = 0
		if defaultSource(result.Source) == "calibration" && (state.RecoveryStage != RecoveryNormal || state.RecoveryPriority >= firstRecoveryPriority || state.HealthPenalty > 0) {
			state.HealthPenalty = 0
			state.RecoveryStage = RecoveryNormal
			t.releaseRecoverySlotLocked(state, key)
			state.RecoveryPriority = 0
			state.recoveryFailuresSinceStep = 0
			action = "calibration_recovered"
		} else {
			switch state.RecoveryStage {
			case RecoveryCooling, RecoveryProbeDue:
				state.RecoveryStage = RecoveryWarming5
				action = "probe_success_warming_5"
			case RecoveryWarming5:
				if state.ConsecutiveSuccesses >= 2 {
					state.RecoveryStage = RecoveryWarming25
					action = "recovery_warming_25"
				}
			case RecoveryWarming25:
				if state.ConsecutiveSuccesses >= t.policy.RecoverySuccessesToNormal {
					state.RecoveryStage = RecoveryNormal
					state.HealthPenalty = maxInt(state.HealthPenalty-1, 0)
					action = "recovered_normal"
				}
			default:
				state.HealthPenalty = maxInt(state.HealthPenalty-1, 0)
			}
			if state.RecoveryStage == RecoveryNormal && state.RecoveryPriority >= firstRecoveryPriority {
				t.releaseRecoverySlotLocked(state, key)
				state.RecoveryPriority = 0
				state.recoveryFailuresSinceStep = 0
			}
		}
	} else {
		switch class {
		case FailureCancelled, FailureClientError, FailureContentRejected, FailurePayloadRejected:
			action = "no_penalty"
			// Client cancellations, request-specific rejections, and content
			// policy blocks are not evidence that the upstream lane is unhealthy.
		case FailureCapabilityError:
			state.ConsecutiveFailures++
			state.ConsecutiveSuccesses = 0
			state.ErrorRateEWMA = state.ErrorRateEWMA*(1-t.policy.ErrorRateAlpha) + t.policy.ErrorRateAlpha
			state.HealthScore = maxFloat(0.05, state.HealthScore*0.65)
			state.HealthPenalty = minInt(state.HealthPenalty+2, t.policy.MaxPenalty)
			state.CooldownUntilUnix = now.Add(t.policy.CapabilityQuarantine).Unix()
			state.RecoveryStage = RecoveryCooling
			action = "capability_quarantine"
		case FailureAuthForbidden:
			state.ConsecutiveFailures++
			state.ConsecutiveSuccesses = 0
			state.ErrorRateEWMA = state.ErrorRateEWMA*(1-t.policy.ErrorRateAlpha) + t.policy.ErrorRateAlpha
			state.HealthScore = maxFloat(0.05, state.HealthScore*0.65)
			state.HealthPenalty = minInt(state.HealthPenalty+2, t.policy.MaxPenalty)
			state.CooldownUntilUnix = now.Add(t.policy.AuthQuarantine).Unix()
			state.RecoveryStage = RecoveryCooling
			action = "auth_quarantine"
		case FailureConcurrencyLimited:
			// An explicit concurrency/busy 429 is not evidence that the lane is
			// broken. The request layer applies bounded exponential backoff;
			// keep the lane at its configured priority and do not allocate a
			// recovery slot.
			state.ConsecutiveFailures = 0
			state.ConsecutiveSuccesses = 0
			action = "rate_limit_backoff"
		case FailureRateLimited:
			state.ConsecutiveFailures++
			state.ConsecutiveSuccesses = 0
			state.ErrorRateEWMA = state.ErrorRateEWMA*(1-t.policy.ErrorRateAlpha) + t.policy.ErrorRateAlpha
			state.HealthScore = maxFloat(0.05, state.HealthScore*0.65)
			state.HealthPenalty = minInt(state.HealthPenalty+1, t.policy.MaxPenalty)
			state.CooldownUntilUnix = now.Add(t.policy.RateLimitCooldown).Unix()
			state.RecoveryStage = RecoveryCooling
			action = "rate_limit_cooldown"
		default:
			state.ConsecutiveFailures++
			state.ConsecutiveSuccesses = 0
			state.ErrorRateEWMA = state.ErrorRateEWMA*(1-t.policy.ErrorRateAlpha) + t.policy.ErrorRateAlpha
			state.HealthScore = maxFloat(0.05, state.HealthScore*0.65)
			state.HealthPenalty = minInt(state.HealthPenalty+1, t.policy.MaxPenalty)
			until := now.Add(t.cooldownFor(state.ConsecutiveFailures))
			if state.ConsecutiveFailures == 2 && t.policy.SecondTransientCooldown > 0 {
				until = now.Add(t.policy.SecondTransientCooldown)
			}
			if threshold := t.policy.sustainedFailureThreshold(result.Capability); threshold > 0 && state.ConsecutiveFailures >= threshold && t.policy.SustainedFailureUntil != nil {
				if sustainedUntil := t.policy.SustainedFailureUntil(now); sustainedUntil.After(now) {
					until = sustainedUntil
					action = "sustained_failure_quarantine"
				}
			}
			state.CooldownUntilUnix = until.Unix()
			state.RecoveryStage = RecoveryCooling
			if action == "record_only" {
				action = "transient_cooldown"
			}
		}
		if action != "no_penalty" && action != "rate_limit_backoff" {
			t.ensureRecoverySlotLocked(state, key, result.Capability)
		}
		state.lastFailureUnix = now.Unix()
	}
	snapshot := state.HealthSnapshot
	event := HealthEvent{
		OccurredAtUnix:       now.Unix(),
		Source:               defaultSource(result.Source),
		Key:                  key,
		AccountID:            result.AccountID,
		SourceGroup:          result.SourceGroup,
		StatusCode:           result.StatusCode,
		FailureClass:         class,
		Success:              result.Success,
		Action:               action,
		CooldownUntilUnix:    snapshot.CooldownUntilUnix,
		HealthPenalty:        snapshot.HealthPenalty,
		RecoveryPriority:     snapshot.RecoveryPriority,
		HealthScore:          snapshot.HealthScore,
		ErrorRateEWMA:        snapshot.ErrorRateEWMA,
		ConsecutiveFailures:  snapshot.ConsecutiveFailures,
		ConsecutiveSuccesses: snapshot.ConsecutiveSuccesses,
		RecoveryStage:        snapshot.RecoveryStage,
		LatencyMs:            result.TotalLatencyMs,
		ErrorSummary:         truncateSummary(result.ErrorSummary, 256),
	}
	t.events = append(t.events, event)
	if len(t.events) > t.maxEvents {
		t.events = append([]HealthEvent(nil), t.events[len(t.events)-t.maxEvents:]...)
	}
	t.mu.Unlock()
	if t.sink != nil {
		t.sink(event)
	}
	return snapshot
}

func normalizeRecoverySourceGroup(sourceGroup string, key HealthKey) string {
	if value := strings.TrimSpace(sourceGroup); value != "" {
		return value
	}
	return "lane:" + key.LaneID
}

func (t *HealthTracker) ensureRecoverySlotLocked(state *healthState, key HealthKey, capability Capability) {
	if state == nil {
		return
	}
	pool := recoverySlotKey{capability: capability, modelFamily: key.Model}
	threshold := t.policy.RecoveryEscalationFailureThreshold
	// Preserve the established text/compact behavior: their first failure
	// yields a recovery slot immediately, while image lanes require the full
	// three-failure window before entering the +30 queue.
	if state.RecoveryPriority < firstRecoveryPriority && capability != CapabilityImageGeneration && capability != CapabilityImageEdit {
		threshold = 1
	}
	state.recoveryFailuresSinceStep++
	if state.RecoveryPriority < firstRecoveryPriority {
		if state.recoveryFailuresSinceStep < threshold {
			return
		}
		start := firstRecoveryPriority
		if state.basePriority > 0 {
			start = state.basePriority + t.policy.RecoveryPriorityStep
		}
		state.RecoveryPriority = t.allocateRecoverySlotLocked(pool, key, start)
		state.recoveryFailuresSinceStep = 0
		return
	}
	if state.RecoveryPriority >= firstRecoveryPriority {
		if state.recoveryFailuresSinceStep < t.policy.RecoveryEscalationFailureThreshold {
			return
		}
		t.releaseRecoverySlotLocked(state, key)
		state.RecoveryPriority = t.allocateRecoverySlotLocked(pool, key, state.RecoveryPriority+t.policy.RecoveryPriorityStep)
		state.recoveryFailuresSinceStep = 0
		return
	}
}

func (t *HealthTracker) allocateRecoverySlotLocked(pool recoverySlotKey, key HealthKey, start int) int {
	if start < firstRecoveryPriority {
		start = firstRecoveryPriority
	}
	used := t.recoverySlots[pool]
	if used == nil {
		used = make(map[HealthKey]int)
		t.recoverySlots[pool] = used
	}
	for priority := start; ; priority++ {
		occupied := false
		for _, assigned := range used {
			if assigned == priority {
				occupied = true
				break
			}
		}
		if occupied {
			continue
		}
		used[key] = priority
		return priority
	}
}

func (t *HealthTracker) registerRecoverySlotLocked(key HealthKey, priority int) int {
	if priority < firstRecoveryPriority {
		return 0
	}
	pool := recoverySlotKey{capability: key.Capability, modelFamily: key.Model}
	used := t.recoverySlots[pool]
	if used == nil {
		used = make(map[HealthKey]int)
		t.recoverySlots[pool] = used
	}
	for assignedKey, assignedPriority := range used {
		if assignedKey == key {
			delete(used, assignedKey)
			continue
		}
		if assignedPriority == priority {
			return t.allocateRecoverySlotLocked(pool, key, priority)
		}
	}
	used[key] = priority
	return priority
}

func (t *HealthTracker) releaseRecoverySlotLocked(state *healthState, key HealthKey) {
	if state == nil || state.RecoveryPriority < firstRecoveryPriority {
		return
	}
	pool := recoverySlotKey{capability: key.Capability, modelFamily: key.Model}
	if used := t.recoverySlots[pool]; used != nil {
		delete(used, key)
		if len(used) == 0 {
			delete(t.recoverySlots, pool)
		}
	}
}

func (p HealthPolicy) sustainedFailureThreshold(capability Capability) int {
	if (capability == CapabilityImageGeneration || capability == CapabilityImageEdit) && p.ImageSustainedFailureThreshold > 0 {
		return p.ImageSustainedFailureThreshold
	}
	return p.SustainedFailureThreshold
}

func (t *HealthTracker) cooldownFor(consecutiveFailures int) time.Duration {
	if consecutiveFailures < 1 {
		consecutiveFailures = 1
	}
	shift := consecutiveFailures - 1
	if shift > 6 {
		shift = 6
	}
	duration := t.policy.TransientCooldown * time.Duration(1<<shift)
	if duration > t.policy.MaxCooldown {
		return t.policy.MaxCooldown
	}
	return duration
}

func defaultSource(source string) string {
	if strings.TrimSpace(source) == "" {
		return "production"
	}
	return strings.TrimSpace(source)
}

func modelFamily(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if strings.HasPrefix(model, "gpt-image-") {
		return "gpt-image"
	}
	if strings.HasPrefix(model, "gpt-5") {
		return "gpt-5"
	}
	return model
}

func truncateSummary(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
