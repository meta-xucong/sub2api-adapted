package core

import "time"

type CalibrationPolicy struct {
	Location            *time.Location
	Hour                int
	Minute              int
	FreshEvidenceWindow time.Duration
}

func DefaultCalibrationPolicy() CalibrationPolicy {
	return CalibrationPolicy{
		Location:            time.FixedZone("Asia/Shanghai", 8*60*60),
		Hour:                4,
		Minute:              0,
		FreshEvidenceWindow: 24 * time.Hour,
	}
}

type CapabilityEvidence struct {
	LaneID                string
	GenerationKnown       bool
	EditKnown             bool
	GenerationLastSuccess time.Time
	GenerationLastFailure time.Time
	EditLastSuccess       time.Time
	EditLastFailure       time.Time
	ModesDiverged         bool
}

type CalibrationProbe struct {
	LaneID     string
	Capability Capability
	Reason     string
}

// CalibrationDue uses calendar time in the configured location and stores no
// local timezone state in the ledger. A run after today's scheduled time is
// considered complete until the next calendar day.
func CalibrationDue(now, lastRun time.Time, policy CalibrationPolicy) bool {
	policy = normalizeCalibrationPolicy(policy)
	localNow := now.In(policy.Location)
	todayTarget := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), policy.Hour, policy.Minute, 0, 0, policy.Location)
	if localNow.Before(todayTarget) {
		return false
	}
	return lastRun.Before(todayTarget)
}

// BuildCalibrationPlan decides which capabilities need a probe. It does not
// perform HTTP calls; the adapter owns probe payloads, budgets, and locking.
func BuildCalibrationPlan(now time.Time, lanes []LaneSnapshot, evidence []CapabilityEvidence, policy CalibrationPolicy) []CalibrationProbe {
	policy = normalizeCalibrationPolicy(policy)
	evidenceByLane := make(map[string]CapabilityEvidence, len(evidence))
	for _, item := range evidence {
		evidenceByLane[item.LaneID] = item
	}
	probes := make([]CalibrationProbe, 0, len(lanes)*2)
	for _, lane := range lanes {
		item := evidenceByLane[lane.LaneID]
		generationKnown := lane.Capabilities[CapabilityImageGeneration]
		editKnown := lane.Capabilities[CapabilityImageEdit]
		if len(lane.Capabilities) == 0 {
			generationKnown = true
			editKnown = true
		}
		if generationKnown && needsProbe(now, item.GenerationKnown, item.GenerationLastSuccess, item.GenerationLastFailure, policy.FreshEvidenceWindow) {
			probes = append(probes, CalibrationProbe{LaneID: lane.LaneID, Capability: CapabilityImageGeneration, Reason: probeReason(item.GenerationKnown, item.GenerationLastFailure)})
		}
		// A lane with fresh, consistent text-to-image and image-edit evidence only
		// receives the inexpensive text-to-image daily probe. Image edit is added
		// when it is unknown, has failed since its last success, or diverges from
		// generation. This keeps calibration useful without creating needless edits.
		needEditProbe := !item.EditKnown || item.ModesDiverged || (!item.EditLastFailure.IsZero() && item.EditLastFailure.After(item.EditLastSuccess))
		if editKnown && needEditProbe {
			probes = append(probes, CalibrationProbe{LaneID: lane.LaneID, Capability: CapabilityImageEdit, Reason: probeReason(item.EditKnown, item.EditLastFailure)})
		} else if editKnown && item.ModesDiverged && item.EditLastSuccess.IsZero() {
			probes = append(probes, CalibrationProbe{LaneID: lane.LaneID, Capability: CapabilityImageEdit, Reason: "mode_divergence_requires_edit_probe"})
		}
	}
	return probes
}

func normalizeCalibrationPolicy(policy CalibrationPolicy) CalibrationPolicy {
	d := DefaultCalibrationPolicy()
	if policy.Location == nil {
		policy.Location = d.Location
	}
	if policy.Hour < 0 || policy.Hour > 23 {
		policy.Hour = d.Hour
	}
	if policy.Minute < 0 || policy.Minute > 59 {
		policy.Minute = d.Minute
	}
	if policy.FreshEvidenceWindow <= 0 {
		policy.FreshEvidenceWindow = d.FreshEvidenceWindow
	}
	return policy
}

func needsProbe(now time.Time, known bool, lastSuccess, lastFailure time.Time, freshWindow time.Duration) bool {
	if !known || lastSuccess.IsZero() {
		return true
	}
	if !lastFailure.IsZero() && lastFailure.After(lastSuccess) {
		return true
	}
	return now.Sub(lastSuccess) >= freshWindow
}

func probeReason(known bool, lastFailure time.Time) string {
	if !known {
		return "unknown_capability"
	}
	if !lastFailure.IsZero() {
		return "failure_after_last_success"
	}
	return "stale_success_evidence"
}
