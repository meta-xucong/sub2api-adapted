package core

import (
	"sync"
	"testing"
	"time"
)

func imageAdmissionResult(key HealthKey, decision ImageAdmissionDecision, success bool) RouteResult {
	return RouteResult{
		LaneID: key.LaneID, Capability: key.Capability, Model: key.Model,
		BasePriority: 2, Success: success, ErrorClass: FailureTimeout,
		ImageProbeToken: decision.ProbeToken, ImageHealthGeneration: decision.ImageHealthGeneration,
	}
}

func TestImageAdmissionSequentialFailuresUseExistingRecoveryPolicy(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	calibrationAt := time.Date(2026, 10, 20, 4, 0, 0, 0, now.Location())
	policy := HealthPolicy{
		TransientCooldown: 30 * time.Second, SecondTransientCooldown: 600 * time.Second,
		ImageSustainedFailureThreshold: 3,
		SustainedFailureUntil:          func(time.Time) time.Time { return calibrationAt },
	}
	tracker := NewHealthTracker(policy, func() time.Time { return now }, nil)
	key := NewHealthKey("image-lane", CapabilityImageGeneration, "gpt-image-2")
	var snapshot HealthSnapshot
	for i := 1; i <= 3; i++ {
		decision, release := tracker.AcquireImageAdmission(key, now.Unix(), now.Add(time.Minute).Unix())
		if !decision.Allowed {
			t.Fatalf("request %d was not admitted after its previous cooldown: %+v", i, decision)
		}
		if i > 1 && decision.ProbeToken == 0 {
			t.Fatalf("request %d bypassed half-open admission after cooldown expiry: %+v", i, decision)
		}
		snapshot = tracker.Observe(imageAdmissionResult(key, decision, false))
		release()
		until := calibrationAt
		switch i {
		case 1:
			until = now.Add(30 * time.Second)
		case 2:
			until = now.Add(600 * time.Second)
		}
		if snapshot.CooldownUntilUnix != until.Unix() || snapshot.ConsecutiveFailures != i {
			t.Fatalf("request %d did not use the existing policy: %+v", i, snapshot)
		}
		for _, at := range []int64{now.Unix(), until.Unix() - 1} {
			if admission := tracker.ImageAdmission(key, at); admission.Allowed || admission.RetryAtUnix != until.Unix() {
				t.Fatalf("fresh request bypassed cooldown: %+v", admission)
			}
			if acquired, done := tracker.AcquireImageAdmission(key, at, until.Add(time.Minute).Unix()); acquired.Allowed {
				done()
				t.Fatal("dispatch bypassed cooldown")
			}
		}
		for _, unrelated := range []HealthKey{
			NewHealthKey(key.LaneID, CapabilityImageEdit, key.Model),
			NewHealthKey(key.LaneID, key.Capability, "gpt-image-2.5"),
			NewHealthKey(key.LaneID, CapabilityChat, key.Model),
			NewHealthKey("other-lane", key.Capability, key.Model),
		} {
			if !tracker.ImageAdmission(unrelated, now.Unix()).Allowed {
				t.Fatalf("cooldown leaked into %+v", unrelated)
			}
		}
		now = until
	}
	if snapshot.RecoveryPriority != 32 {
		t.Fatalf("third failure did not enter existing base+30 recovery slot: %+v", snapshot)
	}
}

func TestImageAdmissionConcurrentRecoveryAllowsOneLocalProbe(t *testing.T) {
	now := time.Unix(1000, 0)
	tracker := NewHealthTracker(HealthPolicy{}, func() time.Time { return now }, nil)
	key := NewHealthKey("lane", CapabilityImageGeneration, "gpt-image-2")
	first, releaseFirst := tracker.AcquireImageAdmission(key, now.Unix(), now.Add(time.Minute).Unix())
	if !first.Allowed || first.ProbeToken != 0 {
		t.Fatalf("healthy initial request was not admitted normally: %+v", first)
	}
	failed := tracker.Observe(imageAdmissionResult(key, first, false))
	releaseFirst()
	if failed.RecoveryStage != RecoveryCooling || failed.CooldownUntilUnix != now.Add(30*time.Second).Unix() {
		t.Fatalf("first actual timeout did not enter the 30-second cooldown: %+v", failed)
	}
	now = now.Add(29 * time.Second)
	if tracker.ImageAdmission(key, now.Unix()).Allowed {
		t.Fatal("first-fault cooldown admitted traffic before expiry")
	}
	now = now.Add(time.Second)
	lane := tracker.Snapshot(LaneSnapshot{LaneID: key.LaneID}, key.Capability, key.Model, now.Unix())
	if lane.RecoveryStage != RecoveryProbeDue || lane.CooldownUntilUnix != 0 {
		t.Fatalf("30-second expiry restored full traffic instead of half-open recovery: %+v", lane)
	}
	for i := 0; i < 3; i++ {
		if !tracker.ImageAdmission(key, now.Unix()).Allowed {
			t.Fatal("eligibility read consumed a recovery lease")
		}
	}
	type admission struct {
		decision ImageAdmissionDecision
		release  func()
	}
	const requests = 32
	results := make(chan admission, requests)
	start := make(chan struct{})
	var group sync.WaitGroup
	for i := 0; i < requests; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			decision, release := tracker.AcquireImageAdmission(key, now.Unix(), now.Add(time.Minute).Unix())
			results <- admission{decision, release}
		}()
	}
	close(start)
	group.Wait()
	close(results)
	var winner admission
	admitted := 0
	for result := range results {
		if result.decision.Allowed {
			admitted++
			winner = result
		} else if result.decision.RetryAtUnix != now.Add(time.Minute).Unix() {
			t.Fatalf("busy probe did not expose its bounded retry time: %+v", result.decision)
		}
	}
	if admitted != 1 || winner.decision.ProbeToken == 0 {
		t.Fatalf("got %d concurrent probes, expected one", admitted)
	}
	winner.release()
	winner.release()
	if !tracker.ImageAdmission(key, now.Unix()).Allowed {
		t.Fatal("cancelled probe lease was not released")
	}
}

func TestImageAdmissionExpiredProbeCannotReleaseOrOverwriteReplacement(t *testing.T) {
	now := time.Unix(2000, 0)
	tracker := NewHealthTracker(HealthPolicy{}, func() time.Time { return now }, nil)
	key := NewHealthKey("lane", CapabilityImageEdit, "gpt-image-2")
	tracker.Restore(key, HealthSnapshot{RecoveryStage: RecoveryProbeDue, HealthScore: .5}, 0)
	old, releaseOld := tracker.AcquireImageAdmission(key, now.Unix(), now.Add(5*time.Second).Unix())
	now = now.Add(5 * time.Second)
	current, releaseCurrent := tracker.AcquireImageAdmission(key, now.Unix(), now.Add(time.Minute).Unix())
	defer releaseCurrent()
	if !current.Allowed || old.ProbeToken == current.ProbeToken {
		t.Fatalf("expired probe did not yield a new fenced lease: old=%+v current=%+v", old, current)
	}
	releaseOld()
	if tracker.ImageAdmission(key, now.Unix()).Allowed {
		t.Fatal("late release freed replacement probe")
	}
	for _, success := range []bool{true, false} {
		snapshot := tracker.Observe(imageAdmissionResult(key, old, success))
		if snapshot.RecoveryStage != RecoveryProbeDue || snapshot.ConsecutiveFailures != 0 || snapshot.ConsecutiveSuccesses != 0 {
			t.Fatalf("superseded probe changed health: %+v", snapshot)
		}
	}
	if len(tracker.EventsSince(0)) != 0 {
		t.Fatal("superseded probe emitted a durable health projection")
	}
	failed := tracker.Observe(imageAdmissionResult(key, current, false))
	if failed.RecoveryStage != RecoveryCooling || failed.ConsecutiveFailures != 1 {
		t.Fatalf("current probe failure was not applied: %+v", failed)
	}
	releaseCurrent()
	tracker.Observe(imageAdmissionResult(key, old, true))
	if tracker.ImageAdmission(key, now.Unix()).Allowed {
		t.Fatal("late probe success cleared newer cooldown")
	}
}

func TestImageAdmissionLateHealthySuccessCannotClearNewFailure(t *testing.T) {
	now := time.Unix(3000, 0)
	tracker := NewHealthTracker(HealthPolicy{}, func() time.Time { return now }, nil)
	key := NewHealthKey("lane", CapabilityImageGeneration, "gpt-image-2")
	first, releaseFirst := tracker.AcquireImageAdmission(key, now.Unix(), now.Add(time.Minute).Unix())
	defer releaseFirst()
	second, releaseSecond := tracker.AcquireImageAdmission(key, now.Unix(), now.Add(time.Minute).Unix())
	defer releaseSecond()
	if !first.Allowed || !second.Allowed || first.ProbeToken != 0 || second.ProbeToken != 0 || first.ImageHealthGeneration == 0 {
		t.Fatal("healthy dispatches should remain concurrent and carry a health generation")
	}
	failure := tracker.Observe(imageAdmissionResult(key, second, false))
	late := tracker.Observe(imageAdmissionResult(key, first, true))
	if late != failure || tracker.ImageAdmission(key, now.Unix()).Allowed {
		t.Fatalf("late healthy success cleared a newer failure: before=%+v after=%+v", failure, late)
	}
	if len(tracker.EventsSince(0)) != 1 {
		t.Fatal("stale success projected over the new failure")
	}
	// A genuine failure from an overlapping ordinary request is still evidence.
	laterFailure := tracker.Observe(imageAdmissionResult(key, first, false))
	if laterFailure.ConsecutiveFailures != 2 {
		t.Fatalf("ordinary concurrent failure was incorrectly discarded: %+v", laterFailure)
	}
}

func TestImageAdmissionRecoveryRampRemainsBoundedUntilNormal(t *testing.T) {
	now := time.Unix(4000, 0)
	tracker := NewHealthTracker(HealthPolicy{}, func() time.Time { return now }, nil)
	key := NewHealthKey("lane", CapabilityImageGeneration, "gpt-image-2")
	tracker.Observe(RouteResult{LaneID: key.LaneID, Capability: key.Capability, Model: key.Model, ErrorClass: FailureTimeout})
	now = now.Add(30 * time.Second)
	for _, want := range []RecoveryStage{RecoveryWarming5, RecoveryWarming25, RecoveryNormal} {
		decision, release := tracker.AcquireImageAdmission(key, now.Unix(), now.Add(time.Minute).Unix())
		if !decision.Allowed || decision.ProbeToken == 0 {
			t.Fatalf("recovery stage %s did not acquire a bounded probe: %+v", want, decision)
		}
		if tracker.ImageAdmission(key, now.Unix()).Allowed {
			t.Fatal("another request bypassed the active warming probe")
		}
		snapshot := tracker.Observe(imageAdmissionResult(key, decision, true))
		if snapshot.RecoveryStage != want {
			t.Fatalf("got recovery stage %s, want %s", snapshot.RecoveryStage, want)
		}
		release()
	}
	normal, release := tracker.AcquireImageAdmission(key, now.Unix(), now.Add(time.Minute).Unix())
	defer release()
	if !normal.Allowed || normal.ProbeToken != 0 {
		t.Fatalf("normal recovery did not restore ordinary concurrency: %+v", normal)
	}
}

func TestImageAdmissionRestoreEnforcesCooldownButDoesNotClaimGlobalLease(t *testing.T) {
	now := time.Unix(5000, 0)
	key := NewHealthKey("lane", CapabilityImageGeneration, "gpt-image-2")
	snapshot := HealthSnapshot{CooldownUntilUnix: now.Add(time.Minute).Unix(), RecoveryStage: RecoveryNormal, ConsecutiveFailures: 2}
	trackers := []*HealthTracker{
		NewHealthTracker(HealthPolicy{}, func() time.Time { return now }, nil),
		NewHealthTracker(HealthPolicy{}, func() time.Time { return now }, nil),
	}
	for _, tracker := range trackers {
		tracker.Restore(key, snapshot, now.Unix())
		if tracker.ImageAdmission(key, now.Unix()).Allowed {
			t.Fatal("restored deadline was bypassed because of inconsistent stage")
		}
		tracker.Restore(key, HealthSnapshot{RecoveryStage: RecoveryProbeDue}, now.Unix())
	}
	// Durable state does not provide atomic cross-process admission. Each
	// tracker may admit one local probe; only shared-lease infrastructure could
	// make a stronger claim.
	for _, tracker := range trackers {
		decision, release := tracker.AcquireImageAdmission(key, now.Unix(), now.Add(time.Minute).Unix())
		defer release()
		if !decision.Allowed || decision.ProbeToken == 0 {
			t.Fatal("separate process-local tracker did not admit its own probe")
		}
	}
}

func TestImageAdmissionRequiresBoundedRecoveryLeaseDeadline(t *testing.T) {
	now := time.Unix(6000, 0)
	tracker := NewHealthTracker(HealthPolicy{}, func() time.Time { return now }, nil)
	key := NewHealthKey("lane", CapabilityImageGeneration, "gpt-image-2")
	tracker.Restore(key, HealthSnapshot{RecoveryStage: RecoveryProbeDue}, 0)
	for _, until := range []int64{0, now.Unix() - 1, now.Unix()} {
		decision, release := tracker.AcquireImageAdmission(key, now.Unix(), until)
		release()
		if decision.Allowed {
			t.Fatalf("invalid deadline %d admitted recovery", until)
		}
	}
	decision, release := tracker.AcquireImageAdmission(key, now.Unix(), now.Add(7*24*time.Hour).Unix())
	defer release()
	blocked := tracker.ImageAdmission(key, now.Unix())
	if !decision.Allowed || blocked.Allowed || blocked.RetryAtUnix != now.Add(maxImageProbeLease).Unix() {
		t.Fatalf("excessive lease deadline was not capped: %+v", blocked)
	}
}

func TestImageAdmissionProbeTimeoutAtLeaseDeadlineStillEscalates(t *testing.T) {
	now := time.Unix(7000, 0)
	tracker := NewHealthTracker(HealthPolicy{SecondTransientCooldown: 600 * time.Second}, func() time.Time { return now }, nil)
	key := NewHealthKey("lane", CapabilityImageGeneration, "gpt-image-2")
	tracker.Observe(RouteResult{LaneID: key.LaneID, Capability: key.Capability, Model: key.Model, ErrorClass: FailureTimeout})
	now = now.Add(30 * time.Second)
	decision, release := tracker.AcquireImageAdmission(key, now.Unix(), now.Add(time.Minute).Unix())
	defer release()
	if !decision.Allowed {
		t.Fatal("due probe was not admitted")
	}
	now = now.Add(time.Minute)
	// Reporting at the exact attempt/lease deadline is the normal timeout path.
	snapshot := tracker.Observe(imageAdmissionResult(key, decision, false))
	if snapshot.ConsecutiveFailures != 2 || snapshot.CooldownUntilUnix != now.Add(600*time.Second).Unix() {
		t.Fatalf("current timeout was discarded at lease expiry: %+v", snapshot)
	}
	release()
	if tracker.ImageAdmission(key, now.Unix()).Allowed {
		t.Fatal("timed-out probe reopened without cooldown")
	}
}

func TestImageAdmissionLastResortPreservesCooldownAndExistingFailureLadder(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	calibrationAt := time.Date(2026, 10, 20, 4, 0, 0, 0, now.Location())
	tracker := NewHealthTracker(HealthPolicy{
		TransientCooldown: 30 * time.Second, SecondTransientCooldown: 600 * time.Second,
		ImageSustainedFailureThreshold: 3,
		SustainedFailureUntil:          func(time.Time) time.Time { return calibrationAt },
	}, func() time.Time { return now }, nil)
	key := NewHealthKey("lane", CapabilityImageGeneration, "gpt-image-2")
	first, releaseFirst := tracker.AcquireImageAdmission(key, now.Unix(), now.Add(time.Minute).Unix())
	failed := tracker.Observe(imageAdmissionResult(key, first, false))
	releaseFirst()
	if failed.CooldownUntilUnix != now.Add(30*time.Second).Unix() {
		t.Fatalf("first failure did not use existing cooldown: %+v", failed)
	}
	if tracker.ImageAdmission(key, now.Unix()).Allowed || !tracker.ImageLastResortAdmission(key, now.Unix()).Allowed {
		t.Fatal("ordinary and explicit last-resort admission were not separated")
	}

	cancelled, releaseCancelled := tracker.AcquireImageLastResortAdmission(key, now.Unix(), now.Add(time.Minute).Unix())
	if !cancelled.Allowed || cancelled.ProbeToken == 0 {
		t.Fatalf("last-resort attempt did not reserve a bounded probe: %+v", cancelled)
	}
	lane := tracker.Snapshot(LaneSnapshot{LaneID: key.LaneID}, key.Capability, key.Model, now.Unix())
	if lane.CooldownUntilUnix != failed.CooldownUntilUnix || lane.RecoveryStage != RecoveryCooling || len(tracker.EventsSince(0)) != 1 {
		t.Fatalf("last-resort admission changed health before a result: %+v", lane)
	}
	if admission := tracker.ImageLastResortAdmission(key, now.Unix()); admission.Allowed || admission.RetryAtUnix != now.Add(time.Minute).Unix() {
		t.Fatalf("second request bypassed active last-resort probe: %+v", admission)
	}
	releaseCancelled()
	lane = tracker.Snapshot(LaneSnapshot{LaneID: key.LaneID}, key.Capability, key.Model, now.Unix())
	if lane.CooldownUntilUnix != failed.CooldownUntilUnix || lane.RecoveryStage != RecoveryCooling || tracker.ImageAdmission(key, now.Unix()).Allowed {
		t.Fatal("cancelled last-resort attempt cleared the original cooldown")
	}

	for i, wantUntil := range []int64{now.Add(600 * time.Second).Unix(), calibrationAt.Unix()} {
		decision, release := tracker.AcquireImageLastResortAdmission(key, now.Unix(), now.Add(time.Minute).Unix())
		if !decision.Allowed || decision.ProbeToken == 0 || decision.ProbeToken == cancelled.ProbeToken {
			t.Fatalf("new last-resort attempt did not receive its own fenced token: %+v", decision)
		}
		snapshot := tracker.Observe(imageAdmissionResult(key, decision, false))
		release()
		if snapshot.CooldownUntilUnix != wantUntil || snapshot.ConsecutiveFailures != i+2 || snapshot.RecoveryStage != RecoveryCooling {
			t.Fatalf("last-resort failure bypassed existing ladder: %+v", snapshot)
		}
	}
}

func TestImageAdmissionLastResortSuccessUsesExistingWarmingRamp(t *testing.T) {
	now := time.Unix(8000, 0)
	tracker := NewHealthTracker(HealthPolicy{}, func() time.Time { return now }, nil)
	key := NewHealthKey("lane", CapabilityImageEdit, "gpt-image-2")
	for i := 0; i < 3; i++ {
		tracker.Observe(imageAdmissionResult(key, ImageAdmissionDecision{}, false))
	}
	decision, release := tracker.AcquireImageLastResortAdmission(key, now.Unix(), now.Add(time.Minute).Unix())
	if !decision.Allowed || decision.ProbeToken == 0 {
		t.Fatalf("cooling recovery attempt was not admitted: %+v", decision)
	}
	warming := tracker.Observe(imageAdmissionResult(key, decision, true))
	if warming.RecoveryStage != RecoveryWarming5 || warming.ConsecutiveSuccesses != 1 || warming.RecoveryPriority != 32 || warming.CooldownUntilUnix != 0 {
		t.Fatalf("successful last resort skipped gradual recovery: %+v", warming)
	}
	release()
	for _, want := range []RecoveryStage{RecoveryWarming25, RecoveryNormal} {
		decision, release = tracker.AcquireImageAdmission(key, now.Unix(), now.Add(time.Minute).Unix())
		if !decision.Allowed || decision.ProbeToken == 0 {
			t.Fatalf("warming lane admitted full traffic prematurely: %+v", decision)
		}
		snapshot := tracker.Observe(imageAdmissionResult(key, decision, true))
		release()
		if snapshot.RecoveryStage != want {
			t.Fatalf("unexpected warming stage: got %s, want %s", snapshot.RecoveryStage, want)
		}
	}
}

func TestImageAdmissionLastResortConcurrentRequestsShareExistingLease(t *testing.T) {
	now := time.Unix(9000, 0)
	tracker := NewHealthTracker(HealthPolicy{}, func() time.Time { return now }, nil)
	key := NewHealthKey("lane", CapabilityImageGeneration, "gpt-image-2")
	failed := tracker.Observe(imageAdmissionResult(key, ImageAdmissionDecision{}, false))
	type outcome struct {
		decision ImageAdmissionDecision
		release  func()
	}
	const requests = 32
	results := make(chan outcome, requests)
	start := make(chan struct{})
	var group sync.WaitGroup
	for i := 0; i < requests; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			decision, release := tracker.AcquireImageLastResortAdmission(key, now.Unix(), now.Add(time.Minute).Unix())
			results <- outcome{decision, release}
		}()
	}
	close(start)
	group.Wait()
	close(results)
	admitted := 0
	var releaseWinner func()
	for result := range results {
		if result.decision.Allowed {
			admitted++
			releaseWinner = result.release
			if result.decision.ProbeToken == 0 {
				t.Fatal("last-resort dispatch was not fenced")
			}
		}
	}
	if admitted != 1 {
		t.Fatalf("got %d concurrent last-resort requests, want one", admitted)
	}
	lane := tracker.Snapshot(LaneSnapshot{LaneID: key.LaneID}, key.Capability, key.Model, now.Unix())
	if lane.CooldownUntilUnix != failed.CooldownUntilUnix || lane.RecoveryStage != RecoveryCooling {
		t.Fatal("concurrent last-resort admission cleared the existing cooldown")
	}
	releaseWinner()
}

func TestImageAdmissionLastResortDoesNotBypassModelUnavailable(t *testing.T) {
	now := time.Unix(10000, 0)
	tracker := NewHealthTracker(HealthPolicy{}, func() time.Time { return now }, nil)
	key := NewHealthKey("lane", CapabilityImageGeneration, "gpt-image-2")
	tracker.Restore(key, HealthSnapshot{RecoveryStage: RecoveryModelUnavailable, CooldownUntilUnix: now.Add(time.Hour).Unix()}, now.Unix())
	if tracker.ImageLastResortAdmission(key, now.Unix()).Allowed {
		t.Fatal("last resort bypassed deterministic model unavailability")
	}
	decision, release := tracker.AcquireImageLastResortAdmission(key, now.Unix(), now.Add(time.Minute).Unix())
	defer release()
	if decision.Allowed || decision.ProbeToken != 0 || decision.RetryAtUnix != now.Add(time.Hour).Unix() {
		t.Fatalf("last-resort dispatch bypassed deterministic model unavailability: %+v", decision)
	}
}
