package core

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHealthTrackerCompactTransientFailureUsesBoundedCooldown(t *testing.T) {
	now := time.Date(2026, time.July, 27, 14, 41, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	calibrationAt := time.Date(2026, time.July, 28, 4, 0, 0, 0, now.Location())
	var events []HealthEvent
	tracker := NewHealthTracker(HealthPolicy{
		TransientCooldown:         30 * time.Second,
		SecondTransientCooldown:   10 * time.Minute,
		SustainedFailureThreshold: 3,
		SustainedFailureUntil: func(time.Time) time.Time {
			return calibrationAt
		},
		RecoveryPriorityStep: 30,
	}, func() time.Time { return now }, func(event HealthEvent) { events = append(events, event) })

	snapshot := tracker.Observe(RouteResult{
		LaneID:       "yetoken-value",
		BasePriority: 3,
		Capability:   CapabilityResponsesCompact,
		Model:        "gpt-5.5",
		StatusCode:   http.StatusBadGateway,
		ErrorClass:   FailureUpstream5xx,
		ErrorSummary: "upstream response failed",
	})

	require.Equal(t, now.Add(30*time.Second).Unix(), snapshot.CooldownUntilUnix)
	require.Equal(t, RecoveryCooling, snapshot.RecoveryStage)
	require.Equal(t, 1, snapshot.ConsecutiveFailures)
	require.Equal(t, 1, snapshot.HealthPenalty)
	require.Len(t, events, 1)
	require.Equal(t, "transient_cooldown", events[0].Action)

	degraded := tracker.Snapshot(LaneSnapshot{LaneID: "yetoken-value", Priority: 3}, CapabilityResponsesCompact, "gpt-5.5", now.Unix())
	require.Greater(t, degraded.Priority, 3)
	require.Equal(t, now.Add(30*time.Second).Unix(), degraded.CooldownUntilUnix)

	second := tracker.Observe(RouteResult{
		LaneID:       "yetoken-value",
		BasePriority: 3,
		Capability:   CapabilityResponsesCompact,
		Model:        "gpt-5.5",
		StatusCode:   http.StatusBadGateway,
		ErrorClass:   FailureUpstream5xx,
	})
	require.Equal(t, now.Add(10*time.Minute).Unix(), second.CooldownUntilUnix)

	third := tracker.Observe(RouteResult{
		LaneID:       "yetoken-value",
		BasePriority: 3,
		Capability:   CapabilityResponsesCompact,
		Model:        "gpt-5.5",
		StatusCode:   http.StatusBadGateway,
		ErrorClass:   FailureUpstream5xx,
	})
	require.Equal(t, now.Add(2*time.Minute).Unix(), third.CooldownUntilUnix)
	require.NotEqual(t, calibrationAt.Unix(), third.CooldownUntilUnix)
	require.Equal(t, "transient_cooldown", events[len(events)-1].Action)
}
func TestHealthTrackerCompactInterruptedStreamsDoNotEscalateToCalibration(t *testing.T) {
	now := time.Date(2026, time.July, 27, 14, 41, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	calibrationAt := time.Date(2026, time.July, 28, 4, 0, 0, 0, now.Location())
	var events []HealthEvent
	tracker := NewHealthTracker(HealthPolicy{
		TransientCooldown:         30 * time.Second,
		SecondTransientCooldown:   10 * time.Minute,
		SustainedFailureThreshold: 3,
		SustainedFailureUntil: func(time.Time) time.Time {
			return calibrationAt
		},
	}, func() time.Time { return now }, func(event HealthEvent) { events = append(events, event) })

	result := RouteResult{
		LaneID:     "yetoken-value",
		Capability: CapabilityResponsesCompact,
		Model:      "gpt-5.5",
		StatusCode: http.StatusBadGateway,
		ErrorClass: FailureStreamInterrupted,
	}
	tracker.Observe(result)
	second := tracker.Observe(result)

	require.Equal(t, now.Add(10*time.Minute).Unix(), second.CooldownUntilUnix)
	require.NotEqual(t, calibrationAt.Unix(), second.CooldownUntilUnix)
	require.Equal(t, "stream_interrupted_cooldown", events[len(events)-1].Action)
}
func TestHealthTrackerCompactCapabilityFailureFreezesUntilCalibration(t *testing.T) {
	now := time.Date(2026, time.July, 27, 14, 41, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	calibrationAt := time.Date(2026, time.July, 28, 4, 0, 0, 0, now.Location())
	var events []HealthEvent
	tracker := NewHealthTracker(HealthPolicy{
		SustainedFailureUntil: func(time.Time) time.Time {
			return calibrationAt
		},
	}, func() time.Time { return now }, func(event HealthEvent) { events = append(events, event) })

	snapshot := tracker.Observe(RouteResult{
		LaneID:       "yetoken-value",
		Capability:   CapabilityResponsesCompact,
		Model:        "gpt-5.5",
		StatusCode:   http.StatusServiceUnavailable,
		ErrorClass:   FailureCapabilityError,
		ErrorSummary: "native compact unsupported",
	})

	require.Equal(t, calibrationAt.Unix(), snapshot.CooldownUntilUnix)
	require.Equal(t, RecoveryCooling, snapshot.RecoveryStage)
	require.Len(t, events, 1)
	require.Equal(t, "compact_failure_quarantine", events[0].Action)
}

func TestHealthTrackerCompactTimeoutDoesNotEscalateToCalibration(t *testing.T) {
	now := time.Date(2026, time.July, 27, 14, 41, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	calibrationAt := time.Date(2026, time.July, 28, 4, 0, 0, 0, now.Location())
	var events []HealthEvent
	tracker := NewHealthTracker(HealthPolicy{
		TransientCooldown:         30 * time.Second,
		SecondTransientCooldown:   10 * time.Minute,
		SustainedFailureThreshold: 2,
		SustainedFailureUntil: func(time.Time) time.Time {
			return calibrationAt
		},
	}, func() time.Time { return now }, func(event HealthEvent) { events = append(events, event) })

	result := RouteResult{
		LaneID:     "yetoken-value",
		Capability: CapabilityResponsesCompact,
		Model:      "gpt-5.5",
		ErrorClass: FailureTimeout,
	}
	tracker.Observe(result)
	second := tracker.Observe(result)

	require.Equal(t, now.Add(10*time.Minute).Unix(), second.CooldownUntilUnix)
	require.NotEqual(t, calibrationAt.Unix(), second.CooldownUntilUnix)
	require.Equal(t, "transient_cooldown", events[len(events)-1].Action)
}

func TestHealthTrackerCompactConcurrencyLimitDoesNotQuarantineLane(t *testing.T) {
	now := time.Date(2026, time.July, 27, 14, 41, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	calibrationAt := time.Date(2026, time.July, 28, 4, 0, 0, 0, now.Location())
	var events []HealthEvent
	tracker := NewHealthTracker(HealthPolicy{
		TransientCooldown:         30 * time.Second,
		SustainedFailureThreshold: 1,
		SustainedFailureUntil: func(time.Time) time.Time {
			return calibrationAt
		},
	}, func() time.Time { return now }, func(event HealthEvent) { events = append(events, event) })

	snapshot := tracker.Observe(RouteResult{
		LaneID:       "yetoken-value",
		Capability:   CapabilityResponsesCompact,
		Model:        "gpt-5.5",
		StatusCode:   http.StatusTooManyRequests,
		ErrorClass:   ClassifyFailureDetails(http.StatusTooManyRequests, CapabilityResponsesCompact, "Concurrency limit exceeded for user", "", false),
		ErrorSummary: "Concurrency limit exceeded for user",
	})

	require.Equal(t, FailureConcurrencyLimited, events[len(events)-1].FailureClass)
	require.Equal(t, now.Add(30*time.Second).Unix(), snapshot.CooldownUntilUnix)
	require.NotEqual(t, calibrationAt.Unix(), snapshot.CooldownUntilUnix)
	require.Zero(t, snapshot.RecoveryPriority)
	require.Equal(t, "compact_concurrency_cooldown", events[len(events)-1].Action)
}
