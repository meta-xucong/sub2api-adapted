//go:build unit

package service

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
)

// This unit-build-only view lets handler regression tests observe the real
// scheduler's existing EWMA/TTFT and Smart Router state without adding a
// production API, replacing the scheduler, or inferring state from random
// tie-breaking during account selection.
type OpenAIImageSchedulingUnitSnapshot struct {
	Accounts  int
	ErrorRate float64
	TTFT      float64
	HasTTFT   bool
	Health    core.LaneSnapshot
}

func (s *OpenAIGatewayService) OpenAIImageSchedulingUnitSnapshot(account *Account, model string, now time.Time) OpenAIImageSchedulingUnitSnapshot {
	snapshot := OpenAIImageSchedulingUnitSnapshot{}
	if s.openaiAccountStats != nil {
		snapshot.Accounts = s.openaiAccountStats.size()
		snapshot.ErrorRate, snapshot.TTFT, snapshot.HasTTFT = s.openaiAccountStats.snapshot(account.ID)
	}
	if tracker := s.smartRouterHealth(); tracker != nil {
		lane, ok := smartRouterLaneSnapshot(account, nil, false, 0, 0, false, core.CapabilityImageGeneration)
		if ok {
			snapshot.Health = tracker.Snapshot(lane, core.CapabilityImageGeneration, model, now.Unix())
		}
	}
	return snapshot
}

// Advanced scheduler settings use a process-wide cache. Unit handler cases
// must isolate their persisted-setting fixtures, including when mode is off.
func ResetOpenAIImageSchedulerSettingsForUnitTest() {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
}
