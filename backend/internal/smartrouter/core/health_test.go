package core

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHealthTrackerCapabilityFailureQuarantinesOnlyGeneration(t *testing.T) {
	now := time.Unix(1_000, 0)
	var events []HealthEvent
	tracker := NewHealthTracker(HealthPolicy{CapabilityQuarantine: 24 * time.Hour}, func() time.Time { return now }, func(event HealthEvent) { events = append(events, event) })

	tracker.Observe(RouteResult{
		Source:       "production",
		LaneID:       "flowyun",
		Capability:   CapabilityImageGeneration,
		Model:        "gpt-image-2",
		StatusCode:   http.StatusBadRequest,
		ErrorClass:   ClassifyFailureDetails(http.StatusBadRequest, CapabilityImageGeneration, "requires a usable image target", "upstream_text_reply", false),
		ErrorSummary: "requires a usable image target",
	})

	generation := tracker.Snapshot(LaneSnapshot{LaneID: "flowyun", Priority: 2}, CapabilityImageGeneration, "gpt-image-2", now.Unix())
	edit := tracker.Snapshot(LaneSnapshot{LaneID: "flowyun", Priority: 2}, CapabilityImageEdit, "gpt-image-2", now.Unix())
	require.Equal(t, 4, generation.Priority)
	require.Equal(t, RecoveryCooling, generation.RecoveryStage)
	require.Equal(t, 2, edit.Priority)
	require.Equal(t, RecoveryNormal, edit.RecoveryStage)
	require.Len(t, events, 1)
	require.Equal(t, "capability_quarantine", events[0].Action)
}

func TestHealthTrackerCancelledDoesNotPenalizeLane(t *testing.T) {
	now := time.Unix(2_000, 0)
	tracker := NewHealthTracker(HealthPolicy{}, func() time.Time { return now }, nil)
	tracker.Observe(RouteResult{LaneID: "764", Capability: CapabilityImageGeneration, Model: "gpt-image-2", StatusCode: http.StatusBadGateway, ErrorClass: FailureCancelled})

	snapshot := tracker.Snapshot(LaneSnapshot{LaneID: "764", Priority: 2}, CapabilityImageGeneration, "gpt-image-2", now.Unix())
	require.Equal(t, 2, snapshot.Priority)
	require.Equal(t, RecoveryNormal, snapshot.RecoveryStage)
	require.Zero(t, snapshot.CooldownUntilUnix)
	require.Equal(t, 1.0, snapshot.HealthScore)
}

func TestHealthTrackerConcurrency429DoesNotDemoteLane(t *testing.T) {
	now := time.Unix(2_500, 0)
	var events []HealthEvent
	tracker := NewHealthTracker(HealthPolicy{}, func() time.Time { return now }, func(event HealthEvent) { events = append(events, event) })
	result := RouteResult{
		LaneID:      "busy-lane",
		SourceGroup: "source",
		Capability:  CapabilityImageGeneration,
		Model:       "gpt-image-2",
		StatusCode:  http.StatusTooManyRequests,
		ErrorClass:  ClassifyFailureDetails(http.StatusTooManyRequests, CapabilityImageGeneration, "Concurrency limit exceeded for user, please retry later", "", false),
	}
	snapshot := tracker.Observe(result)
	if snapshot.RecoveryPriority != 0 || snapshot.HealthPenalty != 0 || snapshot.RecoveryStage != RecoveryNormal {
		t.Fatalf("concurrency 429 changed health state: %#v", snapshot)
	}
	if len(events) != 1 || events[0].Action != "rate_limit_backoff" {
		t.Fatalf("unexpected concurrency 429 event: %#v", events)
	}
}

func TestHealthTrackerTransientBackoffAndRecoveryRamp(t *testing.T) {
	now := time.Unix(3_000, 0)
	tracker := NewHealthTracker(HealthPolicy{TransientCooldown: time.Minute, MaxCooldown: 10 * time.Minute}, func() time.Time { return now }, nil)
	result := RouteResult{LaneID: "764", Capability: CapabilityImageGeneration, Model: "gpt-image-2", StatusCode: http.StatusBadGateway, ErrorClass: FailureUpstream5xx}
	first := tracker.Observe(result)
	require.Equal(t, now.Add(time.Minute).Unix(), first.CooldownUntilUnix)
	second := tracker.Observe(result)
	require.Equal(t, now.Add(2*time.Minute).Unix(), second.CooldownUntilUnix)

	now = now.Add(3 * time.Minute)
	probeDue := tracker.Snapshot(LaneSnapshot{LaneID: "764", Priority: 2}, CapabilityImageGeneration, "gpt-image-2", now.Unix())
	require.Equal(t, RecoveryProbeDue, probeDue.RecoveryStage)

	for i := 0; i < 3; i++ {
		tracker.Observe(RouteResult{LaneID: "764", Capability: CapabilityImageGeneration, Model: "gpt-image-2", Success: true, StatusCode: http.StatusOK})
	}
	require.Equal(t, RecoveryNormal, tracker.Snapshot(LaneSnapshot{LaneID: "764", Priority: 2}, CapabilityImageGeneration, "gpt-image-2", now.Unix()).RecoveryStage)
}

func TestHealthTrackerSustainedTransientFailuresFreezeUntilCalibration(t *testing.T) {
	now := time.Date(2026, time.July, 12, 3, 55, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	calibrationAt := time.Date(2026, time.July, 12, 4, 0, 0, 0, now.Location())
	tracker := NewHealthTracker(HealthPolicy{
		TransientCooldown:         30 * time.Second,
		SecondTransientCooldown:   10 * time.Minute,
		SustainedFailureThreshold: 3,
		SustainedFailureUntil: func(time.Time) time.Time {
			return calibrationAt
		},
	}, func() time.Time { return now }, nil)
	result := RouteResult{LaneID: "764", Capability: CapabilityImageGeneration, Model: "gpt-image-2", StatusCode: http.StatusBadGateway, ErrorClass: FailureUpstream5xx}

	first := tracker.Observe(result)
	require.Equal(t, now.Add(30*time.Second).Unix(), first.CooldownUntilUnix)
	require.Equal(t, 1, first.HealthPenalty)
	second := tracker.Observe(result)
	require.Equal(t, now.Add(10*time.Minute).Unix(), second.CooldownUntilUnix)
	third := tracker.Observe(result)
	require.Equal(t, calibrationAt.Unix(), third.CooldownUntilUnix)
	require.Equal(t, 3, third.ConsecutiveFailures)
	blocked := tracker.Snapshot(LaneSnapshot{LaneID: "764", Priority: 2}, CapabilityImageGeneration, "gpt-image-2", now.Unix())
	require.Equal(t, calibrationAt.Unix(), blocked.CooldownUntilUnix)
	require.Greater(t, blocked.Priority, 2)
}

func TestHealthTrackerImageThresholdFreezesBeforeChat(t *testing.T) {
	now := time.Date(2026, time.July, 12, 3, 55, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	calibrationAt := time.Date(2026, time.July, 12, 4, 0, 0, 0, now.Location())
	tracker := NewHealthTracker(HealthPolicy{
		TransientCooldown:              30 * time.Second,
		SecondTransientCooldown:        10 * time.Minute,
		SustainedFailureThreshold:      3,
		ImageSustainedFailureThreshold: 2,
		SustainedFailureUntil: func(time.Time) time.Time {
			return calibrationAt
		},
	}, func() time.Time { return now }, nil)

	image := RouteResult{LaneID: "image", Capability: CapabilityImageGeneration, Model: "gpt-image-2", StatusCode: http.StatusBadGateway, ErrorClass: FailureUpstream5xx}
	tracker.Observe(image)
	imageSecond := tracker.Observe(image)
	require.Equal(t, calibrationAt.Unix(), imageSecond.CooldownUntilUnix)

	chat := RouteResult{LaneID: "chat", Capability: CapabilityResponses, Model: "gpt-5.5", StatusCode: http.StatusBadGateway, ErrorClass: FailureUpstream5xx}
	tracker.Observe(chat)
	chatSecond := tracker.Observe(chat)
	require.Equal(t, now.Add(10*time.Minute).Unix(), chatSecond.CooldownUntilUnix)
	require.NotEqual(t, calibrationAt.Unix(), chatSecond.CooldownUntilUnix)
}

func TestHealthTrackerRestorePreservesCapabilityScopedCooldown(t *testing.T) {
	now := time.Unix(12_000, 0)
	tracker := NewHealthTracker(HealthPolicy{}, func() time.Time { return now }, nil)
	key := NewHealthKey("image:flowyun", CapabilityImageGeneration, "gpt-image-2")
	tracker.Restore(key, HealthSnapshot{
		HealthPenalty:       2,
		HealthScore:         0.4,
		ErrorRateEWMA:       0.6,
		CooldownUntilUnix:   now.Add(time.Hour).Unix(),
		RecoveryStage:       RecoveryCooling,
		ConsecutiveFailures: 3,
	}, now.Add(-time.Minute).Unix())

	generation := tracker.Snapshot(LaneSnapshot{LaneID: "image:flowyun", Priority: 2}, CapabilityImageGeneration, "gpt-image-2", now.Unix())
	edit := tracker.Snapshot(LaneSnapshot{LaneID: "image:flowyun", Priority: 2}, CapabilityImageEdit, "gpt-image-2", now.Unix())
	require.Equal(t, 4, generation.Priority)
	require.Equal(t, RecoveryCooling, generation.RecoveryStage)
	require.Equal(t, 2, edit.Priority)
}

func TestHealthTrackerAssignsSequentialRecoveryPrioritiesPerCapability(t *testing.T) {
	now := time.Unix(15_000, 0)
	tracker := NewHealthTracker(HealthPolicy{}, func() time.Time { return now }, nil)
	failed := func(lane string) {
		tracker.Observe(RouteResult{
			LaneID:      lane,
			SourceGroup: "same-upstream",
			Capability:  CapabilityResponses,
			Model:       "gpt-5.5",
			StatusCode:  http.StatusBadGateway,
			ErrorClass:  FailureUpstream5xx,
		})
	}
	failed("line-a")
	failed("line-b")
	first := tracker.Snapshot(LaneSnapshot{LaneID: "line-a", SourceGroup: "same-upstream", Priority: 1}, CapabilityResponses, "gpt-5.5", now.Unix())
	second := tracker.Snapshot(LaneSnapshot{LaneID: "line-b", SourceGroup: "same-upstream", Priority: 2}, CapabilityResponses, "gpt-5.5", now.Unix())
	require.Equal(t, 30, first.Priority)
	require.Equal(t, 31, second.Priority)

	tracker.Observe(RouteResult{Source: "calibration", LaneID: "line-a", SourceGroup: "same-upstream", Capability: CapabilityResponses, Model: "gpt-5.5", Success: true, StatusCode: http.StatusOK})
	recovered := tracker.Snapshot(LaneSnapshot{LaneID: "line-a", SourceGroup: "same-upstream", Priority: 1}, CapabilityResponses, "gpt-5.5", now.Unix())
	require.Equal(t, 1, recovered.Priority)
	failed("line-c")
	third := tracker.Snapshot(LaneSnapshot{LaneID: "line-c", SourceGroup: "same-upstream", Priority: 3}, CapabilityResponses, "gpt-5.5", now.Unix())
	require.Equal(t, 30, third.Priority)
}

func TestHealthTrackerRecoveryFIFOIsGlobalAcrossSourceGroups(t *testing.T) {
	now := time.Unix(15_500, 0)
	tracker := NewHealthTracker(HealthPolicy{}, func() time.Time { return now }, nil)
	fail := func(lane, sourceGroup string) {
		tracker.Observe(RouteResult{
			LaneID:      lane,
			SourceGroup: sourceGroup,
			Capability:  CapabilityImageGeneration,
			Model:       "gpt-image-2",
			StatusCode:  http.StatusBadGateway,
			ErrorClass:  FailureUpstream5xx,
		})
	}
	for _, item := range []struct{ lane, source string }{
		{lane: "flowyun", source: "flowyun-source"},
		{lane: "764", source: "764-source"},
		{lane: "aiai", source: "aiai-source"},
	} {
		for i := 0; i < 3; i++ {
			fail(item.lane, item.source)
		}
	}
	require.Equal(t, 30, tracker.Snapshot(LaneSnapshot{LaneID: "flowyun", Priority: 1}, CapabilityImageGeneration, "gpt-image-2", now.Unix()).Priority)
	require.Equal(t, 31, tracker.Snapshot(LaneSnapshot{LaneID: "764", Priority: 1}, CapabilityImageGeneration, "gpt-image-2", now.Unix()).Priority)
	require.Equal(t, 32, tracker.Snapshot(LaneSnapshot{LaneID: "aiai", Priority: 1}, CapabilityImageGeneration, "gpt-image-2", now.Unix()).Priority)
}

func TestHealthTrackerImageWaitsThreeFailuresBeforeFirstPlusThirty(t *testing.T) {
	now := time.Unix(16_000, 0)
	tracker := NewHealthTracker(HealthPolicy{}, func() time.Time { return now }, nil)
	fail := func() HealthSnapshot {
		return tracker.Observe(RouteResult{
			LaneID:       "yetoken-1k",
			BasePriority: 2,
			Capability:   CapabilityImageGeneration,
			Model:        "gpt-image-2",
			StatusCode:   http.StatusBadGateway,
			ErrorClass:   FailureUpstream5xx,
		})
	}

	firstObserved := fail()
	first := tracker.Snapshot(LaneSnapshot{LaneID: "yetoken-1k", Priority: 2}, CapabilityImageGeneration, "gpt-image-2", now.Unix())
	require.Equal(t, 3, first.Priority)
	require.Zero(t, firstObserved.RecoveryPriority)
	secondObserved := fail()
	second := tracker.Snapshot(LaneSnapshot{LaneID: "yetoken-1k", Priority: 2}, CapabilityImageGeneration, "gpt-image-2", now.Unix())
	require.Equal(t, 4, second.Priority)
	require.Zero(t, secondObserved.RecoveryPriority)
	thirdObserved := fail()
	third := tracker.Snapshot(LaneSnapshot{LaneID: "yetoken-1k", Priority: 2}, CapabilityImageGeneration, "gpt-image-2", now.Unix())
	require.Equal(t, 32, third.Priority)
	require.Equal(t, 32, thirdObserved.RecoveryPriority)
}

func TestHealthTrackerRecoveryPriorityEscalatesByThirtyAndKeepsFIFO(t *testing.T) {
	now := time.Unix(16_500, 0)
	tracker := NewHealthTracker(HealthPolicy{
		RecoveryEscalationFailureThreshold: 3,
		RecoveryPriorityStep:               30,
	}, func() time.Time { return now }, nil)
	fail := func(lane string) HealthSnapshot {
		return tracker.Observe(RouteResult{
			LaneID:       lane,
			BasePriority: 2,
			Capability:   CapabilityImageGeneration,
			Model:        "gpt-image-2",
			StatusCode:   http.StatusBadGateway,
			ErrorClass:   FailureUpstream5xx,
		})
	}

	for i := 0; i < 3; i++ {
		fail("line-a")
	}
	for i := 0; i < 3; i++ {
		fail("line-b")
	}
	require.Equal(t, 32, tracker.Snapshot(LaneSnapshot{LaneID: "line-a", Priority: 2}, CapabilityImageGeneration, "gpt-image-2", now.Unix()).Priority)
	require.Equal(t, 33, tracker.Snapshot(LaneSnapshot{LaneID: "line-b", Priority: 2}, CapabilityImageGeneration, "gpt-image-2", now.Unix()).Priority)

	// The sixth failure is the first escalation window: line-a moves from 32
	// to 60 and frees its old FIFO slot for a newly degraded lane.
	for i := 0; i < 3; i++ {
		fail("line-a")
	}
	require.Equal(t, 62, tracker.Snapshot(LaneSnapshot{LaneID: "line-a", Priority: 2}, CapabilityImageGeneration, "gpt-image-2", now.Unix()).Priority)

	for i := 0; i < 3; i++ {
		fail("line-c")
	}
	require.Equal(t, 32, tracker.Snapshot(LaneSnapshot{LaneID: "line-c", Priority: 2}, CapabilityImageGeneration, "gpt-image-2", now.Unix()).Priority)

	// The next three failures advance the same lane by another +30, not by a
	// fixed band or a permanent disable.
	for i := 0; i < 3; i++ {
		fail("line-a")
	}
	require.Equal(t, 92, tracker.Snapshot(LaneSnapshot{LaneID: "line-a", Priority: 2}, CapabilityImageGeneration, "gpt-image-2", now.Unix()).Priority)
}

func TestHealthTrackerRestoreDeduplicatesGlobalRecoveryFIFO(t *testing.T) {
	now := time.Unix(17_500, 0)
	tracker := NewHealthTracker(HealthPolicy{}, func() time.Time { return now }, nil)
	tracker.RestoreWithSourceGroup(NewHealthKey("a", CapabilityImageGeneration, "gpt-image-2"), HealthSnapshot{RecoveryPriority: 30, RecoveryStage: RecoveryCooling}, 0, "source-a")
	tracker.RestoreWithSourceGroup(NewHealthKey("b", CapabilityImageGeneration, "gpt-image-2"), HealthSnapshot{RecoveryPriority: 30, RecoveryStage: RecoveryCooling}, 0, "source-b")
	require.Equal(t, 30, tracker.Snapshot(LaneSnapshot{LaneID: "a", Priority: 1}, CapabilityImageGeneration, "gpt-image-2", now.Unix()).Priority)
	require.Equal(t, 31, tracker.Snapshot(LaneSnapshot{LaneID: "b", Priority: 1}, CapabilityImageGeneration, "gpt-image-2", now.Unix()).Priority)
}

func TestHealthTrackerCalibrationSuccessImmediatelyRestoresCapability(t *testing.T) {
	now := time.Unix(16_000, 0)
	tracker := NewHealthTracker(HealthPolicy{}, func() time.Time { return now }, nil)
	for i := 0; i < 3; i++ {
		tracker.Observe(RouteResult{LaneID: "line", SourceGroup: "source", Capability: CapabilityResponsesCompact, Model: "gpt-5.5", StatusCode: http.StatusServiceUnavailable, ErrorClass: FailureUpstream5xx})
	}
	degraded := tracker.Snapshot(LaneSnapshot{LaneID: "line", SourceGroup: "source", Priority: 2}, CapabilityResponsesCompact, "gpt-5.5", now.Unix())
	require.Equal(t, 30, degraded.Priority)

	event := tracker.Observe(RouteResult{Source: "calibration", LaneID: "line", SourceGroup: "source", Capability: CapabilityResponsesCompact, Model: "gpt-5.5", Success: true, StatusCode: http.StatusOK})
	require.Equal(t, RecoveryNormal, event.RecoveryStage)
	require.Zero(t, event.RecoveryPriority)
	require.Equal(t, 2, tracker.Snapshot(LaneSnapshot{LaneID: "line", SourceGroup: "source", Priority: 2}, CapabilityResponsesCompact, "gpt-5.5", now.Unix()).Priority)
}

func TestClassifyFailureDetailsDetectsTextReplyCapabilityMismatch(t *testing.T) {
	require.Equal(t, FailureCapabilityError, ClassifyFailureDetails(http.StatusBadRequest, CapabilityImageGeneration, "Please upload the reference image", "upstream_text_reply", false))
	require.Equal(t, FailurePayloadRejected, ClassifyFailureDetails(http.StatusBadRequest, CapabilityImageGeneration, "invalid size", "", false))
	require.Equal(t, FailureCancelled, ClassifyFailureDetails(http.StatusBadGateway, CapabilityImageGeneration, "context canceled", "", true))
}

func TestHealthTrackerKeepsSafeLedgerEvents(t *testing.T) {
	now := time.Unix(4_000, 0)
	tracker := NewHealthTracker(HealthPolicy{}, func() time.Time { return now }, nil)
	tracker.Observe(RouteResult{Source: "calibration", LaneID: "new", Capability: CapabilityImageGeneration, Model: "gpt-image-2", Success: true, StatusCode: http.StatusOK, TotalLatencyMs: 1200})
	events := tracker.EventsSince(now.Unix() - 1)
	require.Len(t, events, 1)
	require.Equal(t, "calibration", events[0].Source)
	require.Equal(t, CapabilityImageGeneration, events[0].Key.Capability)
	require.Equal(t, int64(1200), events[0].LatencyMs)
}
