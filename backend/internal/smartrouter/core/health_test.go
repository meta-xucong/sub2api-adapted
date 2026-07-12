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
