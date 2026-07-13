package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCalibrationDueUsesShanghaiFourAMBoundary(t *testing.T) {
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	policy := CalibrationPolicy{Location: location, Hour: 4, Minute: 0}
	lastRun := time.Date(2026, 7, 11, 4, 1, 0, 0, location)

	require.False(t, CalibrationDue(time.Date(2026, 7, 12, 3, 59, 0, 0, location), lastRun, policy))
	require.False(t, CalibrationDue(time.Date(2026, 7, 12, 3, 59, 0, 0, location), time.Time{}, policy))
	require.True(t, CalibrationDue(time.Date(2026, 7, 12, 4, 0, 0, 0, location), lastRun, policy))
	require.False(t, CalibrationDue(time.Date(2026, 7, 12, 12, 0, 0, 0, location), time.Date(2026, 7, 12, 4, 2, 0, 0, location), policy))
}

func TestBuildCalibrationPlanSeparatesGenerationAndEditEvidence(t *testing.T) {
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	lanes := []LaneSnapshot{
		{LaneID: "flowyun", Capabilities: map[Capability]bool{CapabilityImageGeneration: true, CapabilityImageEdit: true}},
		{LaneID: "stable", Capabilities: map[Capability]bool{CapabilityImageGeneration: true, CapabilityImageEdit: true}},
	}
	evidence := []CapabilityEvidence{
		{LaneID: "flowyun", GenerationKnown: true, GenerationLastSuccess: now.Add(-2 * time.Hour), GenerationLastFailure: now.Add(-time.Hour), ModesDiverged: true},
		{LaneID: "stable", GenerationKnown: true, GenerationLastSuccess: now.Add(-time.Hour), EditKnown: true, EditLastSuccess: now.Add(-time.Hour)},
	}

	probes := BuildCalibrationPlan(now, lanes, evidence, CalibrationPolicy{FreshEvidenceWindow: 24 * time.Hour})
	require.ElementsMatch(t, []CalibrationProbe{
		{LaneID: "flowyun", Capability: CapabilityImageGeneration, Reason: "failure_after_last_success"},
		{LaneID: "flowyun", Capability: CapabilityImageEdit, Reason: "unknown_capability"},
	}, probes)
}

func TestBuildCalibrationPlanTestsUnknownLaneInBothModes(t *testing.T) {
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	probes := BuildCalibrationPlan(now, []LaneSnapshot{{LaneID: "new-line"}}, nil, CalibrationPolicy{})
	require.ElementsMatch(t, []CalibrationProbe{
		{LaneID: "new-line", Capability: CapabilityImageGeneration, Reason: "unknown_capability"},
		{LaneID: "new-line", Capability: CapabilityImageEdit, Reason: "unknown_capability"},
	}, probes)
}

func TestBuildCalibrationPlanUsesOneGenerationProbeForStableLane(t *testing.T) {
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	lanes := []LaneSnapshot{{
		LaneID: "stable",
		Capabilities: map[Capability]bool{
			CapabilityImageGeneration: true,
			CapabilityImageEdit:       true,
		},
	}}
	evidence := []CapabilityEvidence{{
		LaneID:                "stable",
		GenerationKnown:       true,
		GenerationLastSuccess: now.Add(-25 * time.Hour),
		EditKnown:             true,
		EditLastSuccess:       now.Add(-25 * time.Hour),
	}}

	probes := BuildCalibrationPlan(now, lanes, evidence, CalibrationPolicy{FreshEvidenceWindow: 24 * time.Hour})
	require.Equal(t, []CalibrationProbe{{
		LaneID:     "stable",
		Capability: CapabilityImageGeneration,
		Reason:     "stale_success_evidence",
	}}, probes)
}

func TestBuildCalibrationPlanProbesCompactCapabilityWhenLaneDeclaresIt(t *testing.T) {
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	lanes := []LaneSnapshot{{
		LaneID: "oauth-compact",
		Capabilities: map[Capability]bool{
			CapabilityResponsesCompact: true,
		},
	}}
	probes := BuildCalibrationPlan(now, lanes, nil, CalibrationPolicy{})
	require.Equal(t, []CalibrationProbe{{
		LaneID:     "oauth-compact",
		Capability: CapabilityResponsesCompact,
		Reason:     "unknown_capability",
	}}, probes)
}
