package service

import (
	"time"

	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
)

// buildSmartRouterSelectionOrder is the only scheduler/core bridge in this
// batch. It receives the candidates already filtered by the existing scheduler
// (group, privacy, model, transport and compact tier rules), then lets the core
// order that same pool using restored health state. An empty/unrepresentable
// pool falls back to the existing scheduler rather than blocking traffic.
func (s *defaultOpenAIAccountScheduler) buildSmartRouterSelectionOrder(req OpenAIAccountScheduleRequest, plan openAIAccountLoadPlan) ([]openAIAccountCandidateScore, bool) {
	if s == nil || s.service == nil || !s.service.smartRouterEnabled() || len(plan.candidates) == 0 {
		return nil, false
	}
	capability := smartRouterRouteCapability(req)
	policy := s.service.smartRouterPolicy()
	if !policy.Enabled {
		return nil, false
	}
	tracker := s.service.smartRouterHealthTracker()
	if tracker == nil {
		return nil, false
	}
	lanes := make([]smartrouter.LaneSnapshot, 0, len(plan.candidates))
	byLane := make(map[string]openAIAccountCandidateScore, len(plan.candidates))
	nowUnix := time.Now().Unix()
	for _, candidate := range plan.candidates {
		lane, ok := s.service.smartRouterLaneSnapshot(candidate.account, candidate.loadInfo, candidate.errorRate, candidate.ttft, candidate.hasTTFT)
		if !ok {
			continue
		}
		lane = tracker.Snapshot(lane, capability, req.RequestedModel, nowUnix)
		lanes = append(lanes, lane)
		byLane[lane.LaneID] = candidate
	}
	if len(lanes) == 0 {
		return nil, false
	}
	excluded := make(map[string]struct{}, len(req.ExcludedIDs))
	for id := range req.ExcludedIDs {
		excluded["account:"+formatInt64(id)] = struct{}{}
	}
	groupID := ""
	if req.GroupID != nil {
		groupID = formatInt64(*req.GroupID)
	}
	route := smartrouter.Order(smartrouter.RouteRequest{
		GroupID: groupID, Model: req.RequestedModel, Capability: capability,
		PreviousResponseID: req.PreviousResponseID, ExcludedLaneIDs: excluded,
		AttemptNumber: len(req.ExcludedIDs), NowUnix: nowUnix,
	}, lanes, policy)
	ordered := make([]openAIAccountCandidateScore, 0, len(route.OrderedLaneIDs))
	for _, laneID := range route.OrderedLaneIDs {
		if candidate, ok := byLane[laneID]; ok {
			ordered = append(ordered, candidate)
		}
	}
	if len(ordered) == 0 {
		return nil, false
	}
	return ordered, true
}

func formatInt64(value int64) string {
	// Kept local to avoid converting scheduler hot-path identifiers through fmt.
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var buf [32]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
