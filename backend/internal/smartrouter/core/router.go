package core

import (
	"hash/fnv"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

func Order(req RouteRequest, lanes []LaneSnapshot, policy Policy) RoutePlan {
	policy = policy.Normalize()
	plan := RoutePlan{
		AttemptBudget: policy.AttemptBudget(req.Capability),
		SkipReasons:   make(map[string]string),
	}
	if !policy.Enabled {
		return plan
	}
	if plan.AttemptBudget > 0 && req.AttemptNumber >= plan.AttemptBudget {
		return plan
	}
	if req.RemainingBudgetSeconds > 0 {
		minimumAttempt := math.Max(req.MinimumAttemptSeconds, 0)
		reserve := math.Max(req.FinalizationReserveSeconds, 0)
		if req.RemainingBudgetSeconds <= minimumAttempt+reserve {
			plan.BudgetBlocked = true
			plan.SkipReasons["__budget__"] = "insufficient_remaining_budget"
			return plan
		}
	}

	nowUnix := req.NowUnix
	if nowUnix <= 0 {
		nowUnix = time.Now().Unix()
	}

	groupCurrent := make(map[string]int)
	for _, lane := range lanes {
		group := normalizeSourceGroup(lane)
		groupCurrent[group] += maxInt(lane.CurrentConcurrency, 0)
	}

	filtered := make([]LaneSnapshot, 0, len(lanes))
	for _, lane := range lanes {
		lane = normalizeLane(lane)
		if _, excluded := req.ExcludedLaneIDs[lane.LaneID]; excluded {
			plan.SkipReasons[lane.LaneID] = "excluded_lane"
			continue
		}
		if _, excluded := req.ExcludedSourceGroups[lane.SourceGroup]; excluded {
			plan.SkipReasons[lane.LaneID] = "excluded_source_group"
			continue
		}
		if !laneSupportsCapability(lane, req.Capability) {
			plan.SkipReasons[lane.LaneID] = "capability_mismatch"
			continue
		}
		if !laneSupportsModel(lane, req.Model) {
			plan.SkipReasons[lane.LaneID] = "model_mismatch"
			continue
		}
		if !laneSupportsImageSizeTier(lane, req.ImageSizeTier) {
			plan.SkipReasons[lane.LaneID] = "image_size_mismatch"
			continue
		}
		// Health cooldown is a soft penalty, not a hard exclusion. The health
		// snapshot raises effective priority and lowers recovery weight, while
		// retaining the lane as a last-resort candidate when every lane is bad.
		if lane.MaxConcurrency > 0 && lane.CurrentConcurrency >= lane.MaxConcurrency {
			plan.SkipReasons[lane.LaneID] = "lane_concurrency_full"
			continue
		}
		if lane.SourceGroupMaxConcurrency > 0 && groupCurrent[lane.SourceGroup] >= lane.SourceGroupMaxConcurrency {
			plan.SkipReasons[lane.LaneID] = "source_group_concurrency_full"
			continue
		}
		filtered = append(filtered, lane)
	}
	if len(filtered) == 0 {
		return plan
	}
	// Explicit size-specialized lanes get first use for their matching tier.
	// Once all of them are unavailable, generic lanes become the fallback.
	filtered = preferExplicitImageSizeTier(filtered, req.ImageSizeTier)
	filtered = filterLowestPriorityLayer(filtered)

	minPriority, maxPriority := filtered[0].Priority, filtered[0].Priority
	minLatency, maxLatency := 0.0, 0.0
	hasLatency := false
	for _, lane := range filtered {
		if lane.Priority < minPriority {
			minPriority = lane.Priority
		}
		if lane.Priority > maxPriority {
			maxPriority = lane.Priority
		}
		if lane.LatencyEWMAms > 0 {
			if !hasLatency {
				minLatency, maxLatency = lane.LatencyEWMAms, lane.LatencyEWMAms
				hasLatency = true
			} else {
				if lane.LatencyEWMAms < minLatency {
					minLatency = lane.LatencyEWMAms
				}
				if lane.LatencyEWMAms > maxLatency {
					maxLatency = lane.LatencyEWMAms
				}
			}
		}
	}

	candidates := make([]CandidateDecision, 0, len(filtered))
	for _, lane := range filtered {
		score := scoreLane(lane, policy, minPriority, maxPriority, minLatency, maxLatency, hasLatency)
		candidates = append(candidates, CandidateDecision{
			LaneID:      lane.LaneID,
			AccountID:   lane.AccountID,
			SourceGroup: lane.SourceGroup,
			Score:       score,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		if candidates[i].AccountID != candidates[j].AccountID {
			return candidates[i].AccountID < candidates[j].AccountID
		}
		return candidates[i].LaneID < candidates[j].LaneID
	})

	topK := policy.TopK
	if topK > len(candidates) {
		topK = len(candidates)
	}
	if topK <= 0 {
		topK = 1
	}
	ordered := weightedOrder(candidates[:topK], req)
	if plan.AttemptBudget > 0 {
		remaining := plan.AttemptBudget - req.AttemptNumber
		if remaining < len(ordered) {
			ordered = ordered[:maxInt(remaining, 0)]
		}
	}

	plan.Candidates = candidates
	plan.OrderedLaneIDs = make([]string, 0, len(ordered))
	seenSourceGroups := make(map[string]int)
	for _, candidate := range ordered {
		if policy.SameSourceGroupAttempts > 0 {
			count := seenSourceGroups[candidate.SourceGroup]
			if count >= policy.SameSourceGroupAttempts {
				continue
			}
			seenSourceGroups[candidate.SourceGroup] = count + 1
		}
		plan.OrderedLaneIDs = append(plan.OrderedLaneIDs, candidate.LaneID)
	}
	return plan
}

func filterLowestPriorityLayer(lanes []LaneSnapshot) []LaneSnapshot {
	if len(lanes) <= 1 {
		return lanes
	}
	minPriority := lanes[0].Priority
	for _, lane := range lanes[1:] {
		if lane.Priority < minPriority {
			minPriority = lane.Priority
		}
	}
	out := lanes[:0]
	for _, lane := range lanes {
		if lane.Priority == minPriority {
			out = append(out, lane)
		}
	}
	return out
}

func scoreLane(lane LaneSnapshot, policy Policy, minPriority int, maxPriority int, minLatency float64, maxLatency float64, hasLatency bool) float64 {
	w := policy.Weights
	priorityFactor := 1.0
	if maxPriority > minPriority {
		priorityFactor = 1 + float64(maxPriority-lane.Priority)/float64(maxPriority-minPriority)
	}

	cost := lane.CostMultiplier
	if cost <= 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
		cost = 1
	}
	costFactor := 1 / cost
	if policy.CostBiasMax > 0 && costFactor > policy.CostBiasMax {
		costFactor = policy.CostBiasMax
	}

	health := lane.HealthScore
	if health <= 0 || math.IsNaN(health) || math.IsInf(health, 0) {
		health = 1
	}
	health *= 1 - clamp01(lane.ErrorRateEWMA)
	if health <= 0 {
		health = 0.01
	}

	loadFactor := 1 + clampMin(float64(lane.LoadRate), 0)/100.0
	if lane.MaxConcurrency > 0 {
		loadFactor += clampMin(float64(lane.CurrentConcurrency), 0) / float64(lane.MaxConcurrency)
	}
	queueFactor := 1 + clampMin(float64(lane.CurrentWaiting), 0)

	latencyFactor := 1.0
	if hasLatency && maxLatency > minLatency && lane.LatencyEWMAms > 0 {
		latencyFactor = 1 + clamp01((lane.LatencyEWMAms-minLatency)/(maxLatency-minLatency))
	}

	recoveryFactor := 1.0
	switch lane.RecoveryStage {
	case RecoveryCooling:
		recoveryFactor = 0
	case RecoveryProbeDue:
		recoveryFactor = 0.05
	case RecoveryWarming5:
		recoveryFactor = 0.05
	case RecoveryWarming25:
		recoveryFactor = 0.25
	}

	baseWeight := lane.BaseWeight
	if baseWeight <= 0 || math.IsNaN(baseWeight) || math.IsInf(baseWeight, 0) {
		baseWeight = 1
	}

	score := 1.0
	score *= math.Pow(priorityFactor, safeWeight(w.Priority))
	score *= math.Pow(costFactor, safeWeight(w.Cost))
	score *= math.Pow(health, safeWeight(w.Health))
	score *= math.Pow(recoveryFactor, safeWeight(w.Recovery))
	score /= math.Pow(loadFactor, safeWeight(w.Load))
	score /= math.Pow(queueFactor, safeWeight(w.Queue))
	score /= math.Pow(latencyFactor, safeWeight(w.Latency))
	score *= baseWeight
	if math.IsNaN(score) || math.IsInf(score, 0) || score <= 0 {
		return 0.01
	}
	return score
}

func weightedOrder(candidates []CandidateDecision, req RouteRequest) []CandidateDecision {
	if len(candidates) <= 1 {
		return append([]CandidateDecision(nil), candidates...)
	}
	pool := append([]CandidateDecision(nil), candidates...)
	ordered := make([]CandidateDecision, 0, len(pool))
	rng := newRNG(routeSeed(req))
	for len(pool) > 0 {
		total := 0.0
		for _, candidate := range pool {
			total += math.Max(candidate.Score, 0.01)
		}
		selected := 0
		if total > 0 {
			threshold := rng.nextFloat64() * total
			accumulated := 0.0
			for i, candidate := range pool {
				accumulated += math.Max(candidate.Score, 0.01)
				if threshold <= accumulated {
					selected = i
					break
				}
			}
		}
		ordered = append(ordered, pool[selected])
		pool = append(pool[:selected], pool[selected+1:]...)
	}
	return ordered
}

func laneSupportsCapability(lane LaneSnapshot, capability Capability) bool {
	if capability == "" || len(lane.Capabilities) == 0 {
		return true
	}
	return lane.Capabilities[capability]
}

func laneSupportsModel(lane LaneSnapshot, model string) bool {
	model = strings.TrimSpace(model)
	if model == "" || len(lane.ModelPatterns) == 0 {
		return true
	}
	for _, pattern := range lane.ModelPatterns {
		if matchPattern(pattern, model) {
			return true
		}
	}
	return false
}

func laneSupportsImageSizeTier(lane LaneSnapshot, requestedTier string) bool {
	requestedTier = normalizeImageSizeTier(requestedTier)
	if requestedTier == "" || len(lane.ImageSizeTiers) == 0 {
		return true
	}
	for _, tier := range lane.ImageSizeTiers {
		if normalizeImageSizeTier(tier) == requestedTier {
			return true
		}
	}
	return false
}

func preferExplicitImageSizeTier(lanes []LaneSnapshot, requestedTier string) []LaneSnapshot {
	requestedTier = normalizeImageSizeTier(requestedTier)
	if requestedTier == "" {
		return lanes
	}
	matched := make([]LaneSnapshot, 0, len(lanes))
	for _, lane := range lanes {
		for _, tier := range lane.ImageSizeTiers {
			if normalizeImageSizeTier(tier) == requestedTier {
				matched = append(matched, lane)
				break
			}
		}
	}
	if len(matched) > 0 {
		return matched
	}
	return lanes
}

func normalizeImageSizeTier(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "1K", "2K", "4K":
		return strings.ToUpper(strings.TrimSpace(value))
	default:
		return ""
	}
}

func matchPattern(pattern string, value string) bool {
	pattern = strings.TrimSpace(pattern)
	value = strings.TrimSpace(value)
	if pattern == "" || value == "" {
		return false
	}
	if pattern == "*" || pattern == value {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(value, strings.TrimSuffix(pattern, "*"))
	}
	return false
}

func normalizeLane(lane LaneSnapshot) LaneSnapshot {
	if strings.TrimSpace(lane.LaneID) == "" {
		if lane.AccountID > 0 {
			lane.LaneID = "account:" + strconv.FormatInt(lane.AccountID, 10)
		} else {
			lane.LaneID = lane.Name
		}
	}
	lane.SourceGroup = normalizeSourceGroup(lane)
	if lane.RecoveryStage == "" {
		lane.RecoveryStage = RecoveryNormal
	}
	return lane
}

func normalizeSourceGroup(lane LaneSnapshot) string {
	if group := strings.TrimSpace(lane.SourceGroup); group != "" {
		return group
	}
	if lane.AccountID > 0 {
		return "account:" + strconv.FormatInt(lane.AccountID, 10)
	}
	if lane.LaneID != "" {
		return lane.LaneID
	}
	return lane.Name
}

func routeSeed(req RouteRequest) uint64 {
	if req.Seed != 0 {
		return req.Seed
	}
	h := fnv.New64a()
	write := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	write(req.RequestID)
	write(req.GroupID)
	write(req.Model)
	write(string(req.Capability))
	write(req.StickyLaneID)
	write(req.PreviousResponseID)
	if h.Sum64() == 0 {
		return uint64(time.Now().UnixNano())
	}
	return h.Sum64()
}

type rng64 struct {
	state uint64
}

func newRNG(seed uint64) rng64 {
	if seed == 0 {
		seed = 0x9e3779b97f4a7c15
	}
	return rng64{state: seed}
}

func (r *rng64) nextUint64() uint64 {
	x := r.state
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	r.state = x
	return x * 2685821657736338717
}

func (r *rng64) nextFloat64() float64 {
	return float64(r.nextUint64()>>11) / (1 << 53)
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func clampMin(value float64, min float64) float64 {
	if value < min {
		return min
	}
	return value
}

func safeWeight(value float64) float64 {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}
