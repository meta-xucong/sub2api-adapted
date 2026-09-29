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
	LaneID                     string
	ChatKnown                  bool
	ChatLastSuccess            time.Time
	ChatLastFailure            time.Time
	ChatRecoveryPriority       int
	ResponsesKnown             bool
	ResponsesLastSuccess       time.Time
	ResponsesLastFailure       time.Time
	ResponsesRecoveryPriority  int
	GenerationKnown            bool
	EditKnown                  bool
	GenerationRecoveryPriority int
	EditRecoveryPriority       int
	GenerationLastSuccess      time.Time
	GenerationLastFailure      time.Time
	EditLastSuccess            time.Time
	EditLastFailure            time.Time
	CompactKnown               bool
	CompactLastSuccess         time.Time
	CompactLastFailure         time.Time
	CompactRecoveryPriority    int
	ModesDiverged              bool
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
	probes := make([]CalibrationProbe, 0, len(lanes)*3)
	for _, lane := range lanes {
		item := evidenceByLane[lane.LaneID]
		if lane.Capabilities[CapabilityChat] && needsCapabilityProbe(now, item.ChatKnown, item.ChatLastSuccess, item.ChatLastFailure, item.ChatRecoveryPriority, policy.FreshEvidenceWindow) {
			probes = append(probes, CalibrationProbe{LaneID: lane.LaneID, Capability: CapabilityChat, Reason: capabilityProbeReason(item.ChatKnown, item.ChatLastFailure, item.ChatRecoveryPriority)})
		}
		if lane.Capabilities[CapabilityResponses] && needsCapabilityProbe(now, item.ResponsesKnown, item.ResponsesLastSuccess, item.ResponsesLastFailure, item.ResponsesRecoveryPriority, policy.FreshEvidenceWindow) {
			probes = append(probes, CalibrationProbe{LaneID: lane.LaneID, Capability: CapabilityResponses, Reason: capabilityProbeReason(item.ResponsesKnown, item.ResponsesLastFailure, item.ResponsesRecoveryPriority)})
		}
		generationKnown := lane.Capabilities[CapabilityImageGeneration]
		editKnown := lane.Capabilities[CapabilityImageEdit]
		if len(lane.Capabilities) == 0 {
			generationKnown = true
			editKnown = true
		}
		if generationKnown && needsCapabilityProbe(now, item.GenerationKnown, item.GenerationLastSuccess, item.GenerationLastFailure, item.GenerationRecoveryPriority, policy.FreshEvidenceWindow) {
			probes = append(probes, CalibrationProbe{LaneID: lane.LaneID, Capability: CapabilityImageGeneration, Reason: capabilityProbeReason(item.GenerationKnown, item.GenerationLastFailure, item.GenerationRecoveryPriority)})
		}
		// A lane with fresh, consistent text-to-image and image-edit evidence only
		// receives the inexpensive text-to-image daily probe. Image edit is added
		// when it is unknown, has failed since its last success, or diverges from
		// generation. This keeps calibration useful without creating needless edits.
		needEditProbe := item.EditRecoveryPriority >= firstRecoveryPriority || !item.EditKnown || item.ModesDiverged || (!item.EditLastFailure.IsZero() && item.EditLastFailure.After(item.EditLastSuccess))
		if editKnown && needEditProbe {
			probes = append(probes, CalibrationProbe{LaneID: lane.LaneID, Capability: CapabilityImageEdit, Reason: capabilityProbeReason(item.EditKnown, item.EditLastFailure, item.EditRecoveryPriority)})
		} else if editKnown && item.ModesDiverged && item.EditLastSuccess.IsZero() {
			probes = append(probes, CalibrationProbe{LaneID: lane.LaneID, Capability: CapabilityImageEdit, Reason: "mode_divergence_requires_edit_probe"})
		}
		compactKnown := lane.Capabilities[CapabilityResponsesCompact]
		if compactKnown && needsCapabilityProbe(now, item.CompactKnown, item.CompactLastSuccess, item.CompactLastFailure, item.CompactRecoveryPriority, policy.FreshEvidenceWindow) {
			probes = append(probes, CalibrationProbe{LaneID: lane.LaneID, Capability: CapabilityResponsesCompact, Reason: capabilityProbeReason(item.CompactKnown, item.CompactLastFailure, item.CompactRecoveryPriority)})
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

func needsCapabilityProbe(now time.Time, known bool, lastSuccess, lastFailure time.Time, recoveryPriority int, freshWindow time.Duration) bool {
	return recoveryPriority >= firstRecoveryPriority || needsProbe(now, known, lastSuccess, lastFailure, freshWindow)
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

func capabilityProbeReason(known bool, lastFailure time.Time, recoveryPriority int) string {
	if recoveryPriority >= firstRecoveryPriority {
		return "recovery_slot_due"
	}
	return probeReason(known, lastFailure)
}
