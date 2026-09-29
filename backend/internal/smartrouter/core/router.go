package core

import (
	"hash/fnv"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Order filters and ranks snapshots without side effects. All admission
// inputs are request and snapshot values, which keeps the core independent of
// repositories, transports, and service wiring.
func Order(req RouteRequest, lanes []LaneSnapshot, policy Policy) RoutePlan {
	policy = policy.Normalize()
	plan := RoutePlan{AttemptBudget: policy.AttemptBudget(req.Capability), SkipReasons: make(map[string]string)}
	if !policy.Enabled {
		return plan
	}
	if plan.AttemptBudget > 0 && req.AttemptNumber >= plan.AttemptBudget {
		return plan
	}

	nowUnix := req.NowUnix
	if nowUnix <= 0 {
		nowUnix = time.Now().Unix()
	}
	groupCurrent := make(map[string]int)
	for _, lane := range lanes {
		groupCurrent[NormalizeSourceGroup(lane)] += maxInt(lane.CurrentConcurrency, 0)
	}

	filtered := make([]LaneSnapshot, 0, len(lanes))
	for _, raw := range lanes {
		lane := normalizeLane(raw)
		if _, excluded := req.ExcludedLaneIDs[lane.LaneID]; excluded {
			plan.SkipReasons[lane.LaneID] = "excluded_lane"
			continue
		}
		if _, excluded := req.ExcludedSourceGroups[lane.SourceGroup]; excluded {
			plan.SkipReasons[lane.LaneID] = "excluded_source_group"
			continue
		}
		if !SupportsCapability(lane, req.Capability) {
			plan.SkipReasons[lane.LaneID] = "capability_mismatch"
			continue
		}
		if !supportsModel(lane, req.Model) {
			plan.SkipReasons[lane.LaneID] = "model_mismatch"
			continue
		}
		if lane.RecoveryStage == RecoveryModelUnavailable && lane.CooldownUntilUnix > nowUnix {
			plan.SkipReasons[lane.LaneID] = "model_unavailable_until_calibration"
			continue
		}
		if lane.CooldownUntilUnix > nowUnix {
			plan.SkipReasons[lane.LaneID] = "cooldown"
			continue
		}
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
				minLatency, maxLatency, hasLatency = lane.LatencyEWMAms, lane.LatencyEWMAms, true
			} else {
				minLatency = math.Min(minLatency, lane.LatencyEWMAms)
				maxLatency = math.Max(maxLatency, lane.LatencyEWMAms)
			}
		}
	}

	candidates := make([]CandidateDecision, 0, len(filtered))
	for _, lane := range filtered {
		candidates = append(candidates, CandidateDecision{
			LaneID: lane.LaneID, AccountID: lane.AccountID, SourceGroup: lane.SourceGroup,
			Score: scoreLane(lane, policy, minPriority, maxPriority, minLatency, maxLatency, hasLatency),
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

	topK := minInt(policy.TopK, len(candidates))
	ordered := weightedOrder(candidates[:topK], req)
	if plan.AttemptBudget > 0 {
		remaining := plan.AttemptBudget - req.AttemptNumber
		if remaining < len(ordered) {
			ordered = ordered[:maxInt(remaining, 0)]
		}
	}
	plan.Candidates = candidates
	seenGroups := make(map[string]int)
	for _, candidate := range ordered {
		if seenGroups[candidate.SourceGroup] >= policy.SameSourceGroupAttempts {
			continue
		}
		seenGroups[candidate.SourceGroup]++
		plan.OrderedLaneIDs = append(plan.OrderedLaneIDs, candidate.LaneID)
	}
	return plan
}

func filterLowestPriorityLayer(lanes []LaneSnapshot) []LaneSnapshot {
	if len(lanes) <= 1 {
		return lanes
	}
	priority := lanes[0].Priority
	for _, lane := range lanes[1:] {
		priority = minInt(priority, lane.Priority)
	}
	out := lanes[:0]
	for _, lane := range lanes {
		if lane.Priority == priority {
			out = append(out, lane)
		}
	}
	return out
}

func scoreLane(lane LaneSnapshot, policy Policy, minPriority, maxPriority int, minLatency, maxLatency float64, hasLatency bool) float64 {
	w := policy.Weights
	priorityFactor := 1.0
	if maxPriority > minPriority {
		priorityFactor = 1 + float64(maxPriority-lane.Priority)/float64(maxPriority-minPriority)
	}
	cost := lane.CostMultiplier
	if cost <= 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
		cost = 1
	}
	costFactor := math.Min(1/cost, policy.CostBiasMax)
	health := lane.HealthScore
	if health <= 0 || math.IsNaN(health) || math.IsInf(health, 0) {
		health = 1
	}
	health *= 1 - clamp01(lane.ErrorRateEWMA)
	if health <= 0 {
		health = 0.01
	}
	loadFactor := 1 + clampMin(float64(lane.LoadRate), 0)/100
	if lane.MaxConcurrency > 0 {
		loadFactor += clampMin(float64(lane.CurrentConcurrency), 0) / float64(lane.MaxConcurrency)
	}
	queueFactor := 1 + clampMin(float64(lane.CurrentWaiting), 0)
	latencyFactor := 1.0
	if hasLatency && maxLatency > minLatency && lane.LatencyEWMAms > 0 {
		latencyFactor += clamp01((lane.LatencyEWMAms - minLatency) / (maxLatency - minLatency))
	}
	baseWeight := lane.BaseWeight
	if baseWeight <= 0 || math.IsNaN(baseWeight) || math.IsInf(baseWeight, 0) {
		baseWeight = 1
	}
	score := baseWeight
	score *= math.Pow(priorityFactor, safeWeight(w.Priority))
	score *= math.Pow(costFactor, safeWeight(w.Cost))
	score *= math.Pow(health, safeWeight(w.Health))
	score /= math.Pow(loadFactor, safeWeight(w.Load))
	score /= math.Pow(queueFactor, safeWeight(w.Queue))
	score /= math.Pow(latencyFactor, safeWeight(w.Latency))
	if math.IsNaN(score) || math.IsInf(score, 0) || score <= 0 {
		return 0.01
	}
	return score
}

func weightedOrder(candidates []CandidateDecision, req RouteRequest) []CandidateDecision {
	pool := append([]CandidateDecision(nil), candidates...)
	ordered := make([]CandidateDecision, 0, len(pool))
	rng := newRNG(routeSeed(req))
	for len(pool) > 0 {
		total := 0.0
		for _, candidate := range pool {
			total += math.Max(candidate.Score, 0.01)
		}
		threshold := rng.nextFloat64() * total
		selected, accumulated := 0, 0.0
		for i, candidate := range pool {
			accumulated += math.Max(candidate.Score, 0.01)
			if threshold <= accumulated {
				selected = i
				break
			}
		}
		ordered = append(ordered, pool[selected])
		pool = append(pool[:selected], pool[selected+1:]...)
	}
	return ordered
}

// ExactModelKey is the canonical identity used by model scoped policy.
func ExactModelKey(model string) string { return strings.ToLower(strings.TrimSpace(model)) }

// ModelMatches accepts an exact model name or a trailing-* prefix pattern.
func ModelMatches(pattern, model string) bool {
	pattern, model = ExactModelKey(pattern), ExactModelKey(model)
	if pattern == "" || model == "" {
		return false
	}
	if pattern == "*" || pattern == model {
		return true
	}
	return strings.HasSuffix(pattern, "*") && strings.HasPrefix(model, strings.TrimSuffix(pattern, "*"))
}

func supportsModel(lane LaneSnapshot, model string) bool {
	if ExactModelKey(model) == "" || len(lane.ModelPatterns) == 0 {
		return true
	}
	for _, pattern := range lane.ModelPatterns {
		if ModelMatches(pattern, model) {
			return true
		}
	}
	return false
}

// SupportsCapability treats an absent capability map as legacy "unknown" and
// therefore eligible, while a populated map is an explicit allowlist.
func SupportsCapability(lane LaneSnapshot, capability Capability) bool {
	if capability == "" || len(lane.Capabilities) == 0 {
		return true
	}
	return lane.Capabilities[capability]
}

func normalizeLane(lane LaneSnapshot) LaneSnapshot {
	if strings.TrimSpace(lane.LaneID) == "" {
		if lane.AccountID > 0 {
			lane.LaneID = "account:" + strconv.FormatInt(lane.AccountID, 10)
		} else {
			lane.LaneID = strings.TrimSpace(lane.Name)
		}
	}
	lane.SourceGroup = NormalizeSourceGroup(lane)
	if lane.RecoveryStage == "" {
		lane.RecoveryStage = RecoveryNormal
	}
	return lane
}

// NormalizeSourceGroup supplies a stable per-account fallback when a caller
// has no configured source group.
func NormalizeSourceGroup(lane LaneSnapshot) string {
	if group := strings.TrimSpace(lane.SourceGroup); group != "" {
		return group
	}
	if lane.AccountID > 0 {
		return "account:" + strconv.FormatInt(lane.AccountID, 10)
	}
	if id := strings.TrimSpace(lane.LaneID); id != "" {
		return id
	}
	return strings.TrimSpace(lane.Name)
}

func routeSeed(req RouteRequest) uint64 {
	if req.Seed != 0 {
		return req.Seed
	}
	h := fnv.New64a()
	for _, value := range []string{req.RequestID, req.GroupID, req.Model, string(req.Capability), req.StickyLaneID, req.PreviousResponseID} {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	if seed := h.Sum64(); seed != 0 {
		return seed
	}
	return 0x9e3779b97f4a7c15
}

type rng64 struct{ state uint64 }

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

func (r *rng64) nextFloat64() float64 { return float64(r.nextUint64()>>11) / (1 << 53) }

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func clampMin(value, minimum float64) float64 {
	if value < minimum {
		return minimum
	}
	return value
}

func safeWeight(value float64) float64 {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
