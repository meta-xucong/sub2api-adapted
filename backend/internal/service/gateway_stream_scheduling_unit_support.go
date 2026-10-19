//go:build unit

package service

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
)

// A unit-only view of existing scheduler state for the real handler boundary.
// This adds no production endpoint or replacement scheduler.
type OpenAIStreamSchedulingUnitSnapshot struct {
	Accounts        int
	ErrorRate, TTFT float64
	HasTTFT         bool
	Health          core.LaneSnapshot
}

func (s *OpenAIGatewayService) OpenAIStreamSchedulingUnitSnapshot(account *Account, capability core.Capability, model string, now time.Time) OpenAIStreamSchedulingUnitSnapshot {
	snapshot := OpenAIStreamSchedulingUnitSnapshot{}
	if s.openaiAccountStats != nil {
		snapshot.Accounts = s.openaiAccountStats.size()
		snapshot.ErrorRate, snapshot.TTFT, snapshot.HasTTFT = s.openaiAccountStats.snapshot(account.ID)
	}
	if tracker := s.smartRouterHealth(); tracker != nil {
		lane, ok := smartRouterLaneSnapshot(account, nil, false, 0, 0, false, capability)
		if ok {
			snapshot.Health = tracker.Snapshot(lane, capability, model, now.Unix())
		}
	}
	return snapshot
}
func ResetOpenAIStreamSchedulerSettingsForUnitTest() {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
}
