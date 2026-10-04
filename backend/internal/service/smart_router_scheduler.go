package service

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
)

const smartRouterExtraKey = "smart_router"

const (
	smartRouterSkipModelUnavailableUntilCalibration = "model_unavailable_until_calibration"
	smartRouterSkipGPTImage2HealthCooldown          = "gpt_image2_health_cooldown"
)

type smartRouterAccountExtra struct {
	Present         bool
	EnabledSet      bool
	Enabled         bool
	CapabilitiesSet bool
	LaneID          string
	SourceGroup     string
	BaseWeight      float64
	CostMultiplier  float64
	MaxConcurrency  int
	Capabilities    map[core.Capability]bool
}

func (s *OpenAIGatewayService) isSmartRouterEnabled() bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.SmartRouter.Enabled
}

func (s *OpenAIGatewayService) smartRouterPolicy() core.Policy {
	policy := core.DefaultPolicy()
	if s == nil || s.cfg == nil {
		return policy
	}
	cfg := s.cfg.Gateway.SmartRouter
	policy.Enabled = cfg.Enabled
	policy.TopK = cfg.TopK
	policy.SameSourceGroupAttempts = cfg.SameSourceGroupAttempts
	policy.CostBiasMax = cfg.CostBiasMax
	policy.Weights = core.ScoreWeights{
		Priority: cfg.Scoring.Priority,
		Cost:     cfg.Scoring.Cost,
		Health:   cfg.Scoring.Health,
		Load:     cfg.Scoring.Load,
		Queue:    cfg.Scoring.Queue,
		Latency:  cfg.Scoring.Latency,
		Recovery: cfg.Scoring.Recovery,
	}
	return policy.Normalize()
}

func (s *OpenAIGatewayService) smartRouterHealth() *core.HealthTracker {
	if s == nil || !s.isSmartRouterEnabled() {
		return nil
	}
	s.smartRouterHealthOnce.Do(func() {
		tracker := core.NewHealthTracker(s.smartRouterHealthPolicy(), time.Now, s.persistSmartRouterHealthEvent)
		if s.smartRouterHealthLedger != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			states, err := s.smartRouterHealthLedger.LoadStates(ctx)
			cancel()
			if err != nil {
				logger.LegacyPrintf("service.openai_gateway", "Smart Router ledger restore failed: %v", err)
			} else {
				for _, state := range states {
					tracker.RestoreWithSourceGroup(
						core.NewHealthKey(state.LaneID, state.Capability, state.ModelFamily),
						state.Snapshot,
						state.LastFailureUnix,
						state.SourceGroup,
					)
				}
			}
		}
		s.smartRouterHealthTracker = tracker
	})
	return s.smartRouterHealthTracker
}

func (s *OpenAIGatewayService) smartRouterHealthPolicy() core.HealthPolicy {
	policy := core.DefaultHealthPolicy()
	if s == nil || s.cfg == nil {
		return policy
	}
	recovery := s.cfg.Gateway.SmartRouter.Recovery
	policy.ModelAvailabilityUntil = nextSmartRouterCalibrationTime
	if recovery.SecondFailureCooldownSeconds > 0 {
		policy.SecondTransientCooldown = time.Duration(recovery.SecondFailureCooldownSeconds) * time.Second
	}
	if recovery.SustainedFailureThreshold > 0 {
		policy.SustainedFailureThreshold = recovery.SustainedFailureThreshold
		policy.SustainedFailureUntil = nextSmartRouterCalibrationTime
	}
	if recovery.RecoveryEscalationFailureThreshold > 0 {
		policy.RecoveryEscalationFailureThreshold = recovery.RecoveryEscalationFailureThreshold
	}
	if recovery.RecoveryPriorityStep > 0 {
		policy.RecoveryPriorityStep = recovery.RecoveryPriorityStep
	}
	return policy
}

func nextSmartRouterCalibrationTime(now time.Time) time.Time {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	local := now.In(loc)
	target := time.Date(local.Year(), local.Month(), local.Day(), 4, 0, 0, 0, loc)
	if !target.After(local) {
		target = target.AddDate(0, 0, 1)
	}
	return target
}

func (s *OpenAIGatewayService) reorderSmartRouterSelectionCandidates(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	candidates []openAIAccountCandidateScore,
) []openAIAccountCandidateScore {
	if len(candidates) == 0 || s == nil || !s.isSmartRouterEnabled() || !s.isOpenAIAdvancedSchedulerEnabled(ctx) {
		return candidates
	}
	capability := req.SmartRouterCapability
	if req.RequireCompact {
		capability = core.CapabilityResponsesCompact
	}
	if capability == "" {
		capability = smartRouterCapabilityForRequest(req)
	}
	if capability == "" {
		return candidates
	}

	policy := s.smartRouterPolicy()
	lanes := make([]core.LaneSnapshot, 0, len(candidates))
	candidateByLane := make(map[string]openAIAccountCandidateScore, len(candidates))
	laneByAccountID := make(map[int64]string, len(candidates))
	for _, candidate := range candidates {
		if candidate.account == nil || !smartRouterAccountEligibleForSmartRouter(candidate.account, capability, req.RequestedModel, req.RequiredCapability) {
			continue
		}
		lane, ok := smartRouterLaneSnapshot(candidate.account, candidate.loadInfo, candidate.loadKnown, candidate.errorRate, candidate.ttft, candidate.hasTTFT, capability)
		if !ok {
			continue
		}
		if capability == core.CapabilityResponsesCompact {
			if lane.Capabilities == nil {
				lane.Capabilities = make(map[core.Capability]bool)
			}
			lane.Capabilities[core.CapabilityResponsesCompact] = true
		}
		// lane_id is user-configurable and is not globally unique. The core
		// ordering API identifies lanes by lane_id, while the native scheduler
		// candidates are account-scoped. If two admitted accounts collide, an
		// ordering cannot be mapped back to candidates without losing one; fail
		// open to the complete native order instead.
		if _, exists := candidateByLane[lane.LaneID]; exists {
			return candidates
		}
		if health := s.smartRouterHealth(); health != nil {
			lane = health.Snapshot(lane, capability, req.RequestedModel, time.Now().Unix())
		}
		lanes = append(lanes, lane)
		candidateByLane[lane.LaneID] = candidate
		laneByAccountID[candidate.account.ID] = lane.LaneID
	}
	if len(lanes) == 0 {
		return candidates
	}

	plan := core.Order(smartRouterRouteRequest(req, capability), lanes, policy)
	return reorderSmartRouterCandidateSlots(candidates, laneByAccountID, candidateByLane, plan, capability)
}

func reorderSmartRouterCandidateSlots(
	candidates []openAIAccountCandidateScore,
	laneByAccountID map[int64]string,
	candidateByLane map[string]openAIAccountCandidateScore,
	plan core.RoutePlan,
	capability core.Capability,
) []openAIAccountCandidateScore {
	orderedLanes := make([]openAIAccountCandidateScore, 0, len(candidates))
	seenLaneIDs := make(map[string]struct{}, len(candidateByLane))
	for _, laneID := range plan.OrderedLaneIDs {
		candidate, ok := candidateByLane[laneID]
		if !ok || candidate.account == nil {
			continue
		}
		if _, exists := seenLaneIDs[laneID]; exists {
			continue
		}
		seenLaneIDs[laneID] = struct{}{}
		orderedLanes = append(orderedLanes, candidate)
	}
	orderedLaneIDs := seenLaneIDs
	hardHealthSkips := make(map[string]struct{})
	for laneID, reason := range plan.SkipReasons {
		if reason == smartRouterSkipModelUnavailableUntilCalibration || reason == smartRouterSkipGPTImage2HealthCooldown {
			hardHealthSkips[laneID] = struct{}{}
		}
	}
	// Match the frozen T0 adapter contract: if Smart Router produces no usable
	// lane order, it is unapplied and the native candidate order remains intact.
	if len(orderedLaneIDs) == 0 {
		return candidates
	}
	if capability == core.CapabilityResponsesCompact {
		// This is the legacy RequireCompact lane. Native remote compaction v2
		// keeps RequireCompact=false and follows ordinary Responses scheduling.
		return reorderSmartRouterCompactCandidateSlots(candidates, laneByAccountID, orderedLanes, orderedLaneIDs)
	}
	// In the non-compact path, replace only the slots of admitted candidates
	// that remain after the existing non-compact health skips. Ranked lanes lead;
	// admitted candidates omitted by core follow in native relative order.
	// Non-admitted candidates retain their native slots. Legacy compact uses a
	// separate ordering-only path above and never applies these health skips.
	remainingAdmitted := make([]openAIAccountCandidateScore, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.account == nil {
			continue
		}
		laneID, admitted := laneByAccountID[candidate.account.ID]
		if !admitted {
			continue
		}
		if _, hardSkipped := hardHealthSkips[laneID]; hardSkipped {
			continue
		}
		if _, ranked := orderedLaneIDs[laneID]; ranked {
			continue
		}
		remainingAdmitted = append(remainingAdmitted, candidate)
	}
	orderedAdmitted := append([]openAIAccountCandidateScore(nil), orderedLanes...)
	orderedAdmitted = append(orderedAdmitted, remainingAdmitted...)
	if len(orderedAdmitted) == 0 {
		return candidates
	}

	ordered := make([]openAIAccountCandidateScore, 0, len(candidates))
	nextLane := 0
	for _, candidate := range candidates {
		if candidate.account == nil {
			ordered = append(ordered, candidate)
			continue
		}
		laneID, admitted := laneByAccountID[candidate.account.ID]
		if !admitted {
			ordered = append(ordered, candidate)
			continue
		}
		if _, hardSkipped := hardHealthSkips[laneID]; hardSkipped {
			continue
		}
		if nextLane >= len(orderedAdmitted) {
			// The adapter order is unusable. Fail open rather than shrinking the
			// official candidate sequence or fabricating a partial permutation.
			return candidates
		}
		ordered = append(ordered, orderedAdmitted[nextLane])
		nextLane++
	}
	if nextLane != len(orderedAdmitted) {
		return candidates
	}
	return ordered
}

func reorderSmartRouterCompactCandidateSlots(
	candidates []openAIAccountCandidateScore,
	laneByAccountID map[int64]string,
	orderedLanes []openAIAccountCandidateScore,
	orderedLaneIDs map[string]struct{},
) []openAIAccountCandidateScore {
	// Compact health and core omissions are ordering signals, not a second
	// candidate-eligibility or retry policy. Keep every admitted candidate in
	// the original admitted slots, placing ranked lanes first and the remaining
	// admitted candidates in their native relative order.
	orderedAdmitted := make([]openAIAccountCandidateScore, 0, len(laneByAccountID))
	orderedAdmitted = append(orderedAdmitted, orderedLanes...)
	for _, candidate := range candidates {
		if candidate.account == nil {
			continue
		}
		laneID, admitted := laneByAccountID[candidate.account.ID]
		if !admitted {
			continue
		}
		if _, ranked := orderedLaneIDs[laneID]; ranked {
			continue
		}
		orderedAdmitted = append(orderedAdmitted, candidate)
	}

	ordered := append([]openAIAccountCandidateScore(nil), candidates...)
	nextAdmitted := 0
	for index, candidate := range candidates {
		if candidate.account == nil {
			continue
		}
		if _, admitted := laneByAccountID[candidate.account.ID]; !admitted {
			continue
		}
		if nextAdmitted >= len(orderedAdmitted) {
			break
		}
		ordered[index] = orderedAdmitted[nextAdmitted]
		nextAdmitted++
	}
	return ordered
}

func smartRouterRouteRequest(req OpenAIAccountScheduleRequest, capability core.Capability) core.RouteRequest {
	groupID := ""
	if req.GroupID != nil {
		groupID = strconv.FormatInt(*req.GroupID, 10)
	}
	return core.RouteRequest{
		GroupID:            groupID,
		Model:              req.RequestedModel,
		Capability:         capability,
		PreviousResponseID: req.PreviousResponseID,
	}
}

func smartRouterCapabilityForRequest(req OpenAIAccountScheduleRequest) core.Capability {
	switch {
	case req.RequireCompact:
		return core.CapabilityResponsesCompact
	case req.RequiredImageCapability != "":
		return core.CapabilityImageGeneration
	case req.RequiredCapability == OpenAIEndpointCapabilityEmbeddings:
		return core.CapabilityEmbedding
	case req.RequiredCapability == OpenAIEndpointCapabilityResponses:
		return core.CapabilityResponses
	case req.RequiredCapability == OpenAIEndpointCapabilityChatCompletions:
		return core.CapabilityChat
	default:
		return ""
	}
}

func smartRouterCapabilityForSelection(ctx context.Context, req OpenAIAccountScheduleRequest) core.Capability {
	if req.RequireCompact {
		return core.CapabilityResponsesCompact
	}
	if req.RequiredImageCapability != "" || OpenAIImageGenerationIntentFromContext(ctx) {
		return core.CapabilityImageGeneration
	}
	return smartRouterCapabilityForRequest(req)
}

func smartRouterAccountHasEmbeddingMapping(account *Account) bool {
	if account == nil {
		return false
	}
	for pattern, mapped := range account.GetModelMapping() {
		if strings.Contains(strings.ToLower(strings.TrimSpace(pattern)), "embedding") ||
			strings.Contains(strings.ToLower(strings.TrimSpace(mapped)), "embedding") {
			return true
		}
	}
	return false
}

func smartRouterAccountHasChatGPTModel(account *Account) bool {
	if account == nil || !account.IsOpenAI() {
		return false
	}
	if mapping := account.GetModelMapping(); len(mapping) > 0 {
		for _, model := range mapping {
			if isChatGPTModelIdentifier(model) {
				return true
			}
		}
		return false
	}
	if mapping := account.GetCompactModelMapping(); len(mapping) > 0 {
		for _, model := range mapping {
			if isChatGPTModelIdentifier(model) {
				return true
			}
		}
		return false
	}
	if account.IsOpenAIOAuth() {
		return true
	}
	baseURL := strings.ToLower(strings.TrimRight(strings.TrimSpace(account.GetOpenAIBaseURL()), "/"))
	return baseURL == "https://api.openai.com" || baseURL == "https://api.openai.com/v1"
}

func isChatGPTModelIdentifier(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	model = strings.TrimPrefix(model, "models/")
	if model == "" {
		return false
	}
	for _, prefix := range []string{"gpt-", "chatgpt-", "codex-", "o1", "o3", "o4"} {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}

func smartRouterAccountEligibleForSmartRouter(account *Account, capability core.Capability, requestedModel string, requiredCapability OpenAIEndpointCapability) bool {
	if capability == core.CapabilityResponsesCompact {
		if account == nil || openAICompactSupportTier(account) == 0 {
			return false
		}
		mapping := account.GetCompactModelMapping()
		if len(mapping) == 0 {
			return true
		}
		for pattern := range mapping {
			if smartRouterModelPatternMatches(pattern, requestedModel) {
				return true
			}
		}
		return false
	}
	if smartRouterAccountHasChatGPTModel(account) {
		return true
	}
	if account == nil || (capability != core.CapabilityEmbedding && requiredCapability != OpenAIEndpointCapabilityEmbeddings) {
		return false
	}
	if !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityEmbeddings) || !smartRouterAccountHasEmbeddingMapping(account) {
		return false
	}
	requestedModel = strings.ToLower(strings.TrimSpace(requestedModel))
	if requestedModel == "" {
		return true
	}
	for pattern := range account.GetModelMapping() {
		if smartRouterModelPatternMatches(pattern, requestedModel) {
			return true
		}
	}
	return false
}

func smartRouterModelPatternMatches(pattern, model string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	model = strings.ToLower(strings.TrimSpace(model))
	if pattern == "*" || pattern == model {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(model, strings.TrimSuffix(pattern, "*"))
	}
	return false
}

func isOpenAIImageModelName(model string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(lower, "gpt-image-") || strings.HasPrefix(lower, "image-")
}

func smartRouterLaneSnapshot(account *Account, loadInfo *AccountLoadInfo, loadKnown bool, errorRate float64, ttft float64, hasTTFT bool, capability core.Capability) (core.LaneSnapshot, bool) {
	if account == nil {
		return core.LaneSnapshot{}, false
	}
	extra := parseSmartRouterAccountExtra(account)
	if extra.Present && extra.EnabledSet && !extra.Enabled {
		return core.LaneSnapshot{}, false
	}
	laneID := strings.TrimSpace(extra.LaneID)
	if laneID == "" {
		laneID = "account:" + strconv.FormatInt(account.ID, 10)
	}
	sourceGroup := smartRouterSourceGroup(account)
	baseWeight := extra.BaseWeight
	if baseWeight <= 0 {
		baseWeight = 1
	}
	costMultiplier := extra.CostMultiplier
	if costMultiplier <= 0 {
		costMultiplier = account.BillingRateMultiplier()
	}
	capabilities := extra.Capabilities
	if len(capabilities) > 0 || extra.CapabilitiesSet {
		capabilities = cloneSmartRouterCapabilities(capabilities)
		if account.IsOpenAI() && account.AllowsOpenAICompact() && smartRouterAccountHasTextCapability(account) {
			if capabilities == nil {
				capabilities = make(map[core.Capability]bool)
			}
			capabilities[core.CapabilityResponsesCompact] = true
		}
	}
	currentConcurrency, waiting, loadRate := 0, 0, 0
	if loadKnown && loadInfo != nil {
		currentConcurrency = loadInfo.CurrentConcurrency
		waiting = loadInfo.WaitingCount
		loadRate = loadInfo.LoadRate
	}
	latency := 0.0
	if hasTTFT && ttft > 0 {
		latency = ttft
	}
	return core.LaneSnapshot{
		LaneID:             laneID,
		AccountID:          account.ID,
		Name:               account.Name,
		SourceGroup:        sourceGroup,
		Capabilities:       capabilities,
		ModelPatterns:      smartRouterModelPatterns(account, capability),
		Priority:           account.Priority,
		CostMultiplier:     costMultiplier,
		BaseWeight:         baseWeight,
		CurrentConcurrency: currentConcurrency,
		CurrentWaiting:     waiting,
		LoadRate:           loadRate,
		HealthScore:        1,
		ErrorRateEWMA:      errorRate,
		LatencyEWMAms:      latency,
	}, true
}

func smartRouterAccountHasTextCapability(account *Account) bool {
	return account != nil && (account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions) || account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityResponses))
}

func smartRouterAutoSourceGroup(account *Account) string {
	if account == nil {
		return ""
	}
	if key := smartRouterLongNumericNameKey(account.Name); key != "" {
		return "name-key:" + key
	}
	raw := strings.TrimSpace(account.GetCredential("base_url"))
	if raw != "" {
		if !strings.Contains(raw, "://") {
			raw = "https://" + raw
		}
		if parsed, err := url.Parse(raw); err == nil {
			host := strings.ToLower(strings.TrimSpace(parsed.Host))
			if splitHost, _, err := net.SplitHostPort(host); err == nil {
				host = splitHost
			}
			host = strings.TrimPrefix(host, "www.")
			if host = strings.Trim(host, "."); host != "" {
				return "host:" + host
			}
		}
	}
	return "account:" + strconv.FormatInt(account.ID, 10)
}

func smartRouterSourceGroup(account *Account) string {
	if account == nil {
		return ""
	}
	if group := strings.TrimSpace(parseSmartRouterAccountExtra(account).SourceGroup); group != "" {
		return group
	}
	return smartRouterAutoSourceGroup(account)
}

func smartRouterLongNumericNameKey(name string) string {
	longest := ""
	var current strings.Builder
	flush := func() {
		if current.Len() >= 6 && current.Len() > len(longest) {
			longest = current.String()
		}
		current.Reset()
	}
	for _, char := range name {
		if char >= '0' && char <= '9' {
			current.WriteRune(char)
			continue
		}
		flush()
	}
	flush()
	return longest
}

func parseSmartRouterAccountExtra(account *Account) smartRouterAccountExtra {
	if account == nil || account.Extra == nil {
		return smartRouterAccountExtra{}
	}
	raw, exists := account.Extra[smartRouterExtraKey]
	if !exists || raw == nil {
		return smartRouterAccountExtra{}
	}
	block, ok := raw.(map[string]any)
	if !ok {
		return smartRouterAccountExtra{Present: true}
	}
	parsed := smartRouterAccountExtra{Present: true}
	if enabled, ok := smartRouterBool(block["enabled"]); ok {
		parsed.EnabledSet = true
		parsed.Enabled = enabled
	}
	if _, exists := block["capabilities"]; exists {
		parsed.CapabilitiesSet = true
	}
	parsed.LaneID = smartRouterString(block["lane_id"])
	parsed.SourceGroup = smartRouterString(block["source_group"])
	parsed.BaseWeight = smartRouterFloat(block["base_weight"])
	parsed.CostMultiplier = smartRouterFloat(block["cost_multiplier"])
	parsed.MaxConcurrency = smartRouterInt(block["max_concurrency"])
	parsed.Capabilities = smartRouterCapabilities(block["capabilities"])
	return parsed
}

func smartRouterCapabilities(raw any) map[core.Capability]bool {
	values := make([]string, 0)
	switch typed := raw.(type) {
	case []any:
		for _, item := range typed {
			if value, ok := item.(string); ok {
				values = append(values, value)
			}
		}
	case []string:
		values = append(values, typed...)
	case string:
		values = strings.Split(typed, ",")
	case map[string]any:
		for key, enabled := range typed {
			if value, ok := smartRouterBool(enabled); ok && value {
				values = append(values, key)
			}
		}
	case map[string]bool:
		for key, enabled := range typed {
			if enabled {
				values = append(values, key)
			}
		}
	}
	capabilities := make(map[core.Capability]bool)
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case string(core.CapabilityChat):
			capabilities[core.CapabilityChat] = true
		case string(core.CapabilityResponses):
			capabilities[core.CapabilityResponses] = true
		case string(core.CapabilityResponsesCompact), "compact", "responses/compact", "openai_compact":
			capabilities[core.CapabilityResponsesCompact] = true
		case string(core.CapabilityEmbedding):
			capabilities[core.CapabilityEmbedding] = true
		case string(core.CapabilityImageGeneration):
			capabilities[core.CapabilityImageGeneration] = true
		case string(core.CapabilityImageEdit):
			capabilities[core.CapabilityImageEdit] = true
		}
	}
	if len(capabilities) == 0 {
		return nil
	}
	return capabilities
}

func cloneSmartRouterCapabilities(input map[core.Capability]bool) map[core.Capability]bool {
	if len(input) == 0 {
		return nil
	}
	output := make(map[core.Capability]bool, len(input))
	for capability, enabled := range input {
		if enabled {
			output[capability] = true
		}
	}
	return output
}

func smartRouterModelPatterns(account *Account, capability core.Capability) []string {
	if account == nil {
		return nil
	}
	mapping := account.GetModelMapping()
	if capability == core.CapabilityResponsesCompact {
		mapping = account.GetCompactModelMapping()
	}
	if len(mapping) == 0 {
		return nil
	}
	patterns := make([]string, 0, len(mapping))
	for pattern := range mapping {
		if pattern = strings.TrimSpace(pattern); pattern != "" {
			patterns = append(patterns, pattern)
		}
	}
	return patterns
}

func smartRouterString(value any) string {
	if value, ok := value.(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func smartRouterBool(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "on":
			return true, true
		case "false", "0", "no", "off":
			return false, true
		}
	case float64:
		return typed != 0, true
	case int:
		return typed != 0, true
	}
	return false, false
}

func smartRouterFloat(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		parsed, _ := strconv.ParseFloat(string(typed), 64)
		return parsed
	case string:
		parsed, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed
	default:
		return 0
	}
}

func smartRouterInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := strconv.Atoi(string(typed))
		return parsed
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(typed))
		return parsed
	default:
		return 0
	}
}
