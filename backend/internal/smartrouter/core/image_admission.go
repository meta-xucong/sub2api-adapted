package core

import (
	"sync"
	"time"
)

// ImageAdmissionDecision describes current scoped image eligibility. RetryAtUnix
// is the active cooldown or local probe lease expiry when admission is denied.
// Tokens are process-local fencing metadata, never durable routing policy.
type ImageAdmissionDecision struct {
	Allowed               bool
	RetryAtUnix           int64
	ProbeToken            uint64
	ImageHealthGeneration uint64
}

// A caller supplies the existing bounded request/attempt deadline. This ceiling
// only prevents a malformed deadline from retaining local admission indefinitely;
// it does not introduce a new health cooldown or attempt timeout.
const maxImageProbeLease = 24 * time.Hour

// ImageAdmission checks the existing exact lane/capability/model health without
// reserving a probe. Callers must recheck with AcquireImageAdmission immediately
// before dispatch, after obtaining any account concurrency slot.
func (t *HealthTracker) ImageAdmission(key HealthKey, nowUnix int64) ImageAdmissionDecision {
	return t.imageAdmission(key, nowUnix, false)
}

// ImageLastResortAdmission is reserved for the caller's explicit second pass,
// after every normally eligible account and bounded wait option is exhausted.
// It permits an existing cooling lane, but never a model-unavailable lane or
// an occupied local probe lease. Native account/auth/model gates still apply.
func (t *HealthTracker) ImageLastResortAdmission(key HealthKey, nowUnix int64) ImageAdmissionDecision {
	return t.imageAdmission(key, nowUnix, true)
}

func (t *HealthTracker) imageAdmission(key HealthKey, nowUnix int64, lastResort bool) ImageAdmissionDecision {
	if t == nil || !isImageCapability(key.Capability) {
		return ImageAdmissionDecision{Allowed: true}
	}
	key = NewHealthKey(key.LaneID, key.Capability, key.Model)
	if key.LaneID == "" {
		return ImageAdmissionDecision{}
	}
	nowUnix = t.imageAdmissionNow(nowUnix)
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.imageAdmissionLocked(key, nowUnix, lastResort)
}

// AcquireImageAdmission atomically checks cooldown and reserves at most one
// recovering image attempt per exact key in this process. It reuses the health
// tracker's recovery stages and state; it is not a cross-process lease. Healthy
// attempts are unrestricted, but receive a generation that fences late success
// after a newer failure. The caller must report the result before releasing and
// must release on every cancellation/error/return path.
func (t *HealthTracker) AcquireImageAdmission(key HealthKey, nowUnix, leaseUntilUnix int64) (ImageAdmissionDecision, func()) {
	return t.acquireImageAdmission(key, nowUnix, leaseUntilUnix, false)
}

// AcquireImageLastResortAdmission rechecks the explicit last-resort candidate
// immediately before dispatch. A future cooldown is preserved while one local
// fenced probe runs; only its actual result may change health. Releasing an
// unobserved/cancelled attempt leaves the previous cooldown unchanged.
func (t *HealthTracker) AcquireImageLastResortAdmission(key HealthKey, nowUnix, leaseUntilUnix int64) (ImageAdmissionDecision, func()) {
	return t.acquireImageAdmission(key, nowUnix, leaseUntilUnix, true)
}

func (t *HealthTracker) acquireImageAdmission(key HealthKey, nowUnix, leaseUntilUnix int64, lastResort bool) (ImageAdmissionDecision, func()) {
	noop := func() {}
	if t == nil || !isImageCapability(key.Capability) {
		return ImageAdmissionDecision{Allowed: true}, noop
	}
	key = NewHealthKey(key.LaneID, key.Capability, key.Model)
	if key.LaneID == "" {
		return ImageAdmissionDecision{}, noop
	}
	nowUnix = t.imageAdmissionNow(nowUnix)
	t.mu.Lock()
	decision := t.imageAdmissionLocked(key, nowUnix, lastResort)
	if !decision.Allowed {
		t.mu.Unlock()
		return decision, noop
	}
	state := t.states[key]
	if state == nil {
		state = &healthState{
			HealthSnapshot: HealthSnapshot{HealthScore: 1, RecoveryStage: RecoveryNormal},
			sourceGroup:    normalizeRecoverySourceGroup("", key),
		}
		t.states[key] = state
	}
	if state.imageHealthGeneration == 0 {
		state.imageHealthGeneration = 1
	}
	decision.ImageHealthGeneration = state.imageHealthGeneration
	forceCoolingProbe := lastResort && state.RecoveryStage == RecoveryCooling && state.CooldownUntilUnix > nowUnix
	if !isImageRecoveryStage(state.RecoveryStage) && !forceCoolingProbe {
		t.mu.Unlock()
		return decision, noop
	}
	if leaseUntilUnix <= nowUnix {
		decision.Allowed = false
		t.mu.Unlock()
		return decision, noop
	}
	if maxUntil := nowUnix + int64(maxImageProbeLease/time.Second); leaseUntilUnix > maxUntil {
		leaseUntilUnix = maxUntil
	}
	state.imageProbeToken++
	// Keep zero reserved for ordinary/non-admitted observations on wraparound.
	if state.imageProbeToken == 0 {
		state.imageProbeToken++
	}
	state.imageProbeUntilUnix = leaseUntilUnix
	decision.ProbeToken = state.imageProbeToken
	token := state.imageProbeToken
	t.mu.Unlock()

	var once sync.Once
	return decision, func() {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			if current := t.states[key]; current != nil && current.imageProbeToken == token {
				current.imageProbeUntilUnix = 0
			}
		})
	}
}

func (t *HealthTracker) imageAdmissionNow(nowUnix int64) int64 {
	if nowUnix > 0 {
		return nowUnix
	}
	now := t.now()
	if now.IsZero() {
		now = time.Now()
	}
	return now.Unix()
}

func (t *HealthTracker) imageAdmissionLocked(key HealthKey, nowUnix int64, lastResort bool) ImageAdmissionDecision {
	decision := ImageAdmissionDecision{Allowed: true}
	state := t.states[key]
	if state == nil {
		return decision
	}
	advanceHealthTimeLocked(state, nowUnix)
	decision.ImageHealthGeneration = state.imageHealthGeneration
	// Last resort relaxes only an ordinary cooling state. Explicit model
	// unavailability and inconsistent restored stages keep their deadline.
	if state.CooldownUntilUnix > nowUnix && (!lastResort || state.RecoveryStage != RecoveryCooling) {
		decision.Allowed = false
		decision.RetryAtUnix = state.CooldownUntilUnix
		return decision
	}
	if state.imageProbeUntilUnix > nowUnix {
		decision.Allowed = false
		decision.RetryAtUnix = state.imageProbeUntilUnix
	}
	return decision
}

func advanceHealthTimeLocked(state *healthState, nowUnix int64) {
	if state.CooldownUntilUnix > 0 && state.CooldownUntilUnix <= nowUnix {
		state.CooldownUntilUnix = 0
		if state.RecoveryStage == RecoveryCooling || state.RecoveryStage == RecoveryModelUnavailable {
			state.RecoveryStage = RecoveryProbeDue
		}
	}
	if state.PolicyRejectUntilUnix > 0 && state.PolicyRejectUntilUnix <= nowUnix {
		state.PolicyRejectUntilUnix = 0
	}
}

func isImageRecoveryStage(stage RecoveryStage) bool {
	return stage == RecoveryProbeDue || stage == RecoveryWarming5 || stage == RecoveryWarming25
}
