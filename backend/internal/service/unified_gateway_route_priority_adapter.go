package service

import (
	"context"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/routepriority"
	"github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
)

type unifiedGatewayRoutePriorityRequestKey struct{}

type unifiedGatewayRoutePriorityRequest struct {
	groupID   int64
	model     string
	pricing   *unifiedGatewayRoutePricingSnapshot
	pricingAt time.Time
}

func withUnifiedGatewayRoutePriorityRequest(
	ctx context.Context,
	cfg *config.Config,
	settings *SettingService,
	groupID *int64,
	model string,
	unsupportedEndpoint bool,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if existing, ok := ctx.Value(unifiedGatewayRoutePriorityRequestKey{}).(*unifiedGatewayRoutePriorityRequest); ok && existing != nil {
		return ctx
	}
	if unsupportedEndpoint || OpenAIImageGenerationIntentFromContext(ctx) || cfg == nil || !cfg.Gateway.UnifiedRoutePriority.Enabled || settings == nil || groupID == nil || *groupID <= 0 || !unifiedGatewayRoutePriorityTextModel(model) {
		return ctx
	}
	pricing := settings.getActiveUnifiedGatewayRoutePricingSnapshot()
	if pricing == nil || pricing.config.TargetGroupID != *groupID {
		return ctx
	}
	request := &unifiedGatewayRoutePriorityRequest{
		groupID:   *groupID,
		model:     strings.TrimSpace(model),
		pricing:   pricing,
		pricingAt: time.Now().UTC(),
	}
	return context.WithValue(ctx, unifiedGatewayRoutePriorityRequestKey{}, request)
}

func unifiedGatewayRoutePriorityTextModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return false
	}
	for _, excluded := range []string{"image", "video", "audio", "embedding"} {
		if strings.Contains(model, excluded) {
			return false
		}
	}
	return true
}

func unifiedGatewayRoutePriorityRequestFromContext(ctx context.Context) *unifiedGatewayRoutePriorityRequest {
	if ctx == nil {
		return nil
	}
	request, _ := ctx.Value(unifiedGatewayRoutePriorityRequestKey{}).(*unifiedGatewayRoutePriorityRequest)
	if request == nil || request.pricing == nil || request.groupID != request.pricing.config.TargetGroupID {
		return nil
	}
	return request
}

func unifiedGatewayRoutePriorityRanks(
	ctx context.Context,
	accounts []*Account,
	billingModel func(*Account, string) string,
	healthLayers map[int64]int,
) map[int64]routepriority.Rank {
	request := unifiedGatewayRoutePriorityRequestFromContext(ctx)
	if request == nil || len(accounts) < 2 {
		return nil
	}
	if billingModel == nil {
		billingModel = func(account *Account, model string) string { return account.GetMappedModel(model) }
	}
	inputs := make([]routepriority.Candidate, 0, len(accounts))
	for nativeIndex, account := range accounts {
		if account == nil || account.ID <= 0 {
			continue
		}
		model := strings.TrimSpace(billingModel(account, request.model))
		if model == "" {
			model = request.model
		}
		candidate := routepriority.Candidate{ID: account.ID, NativeIndex: nativeIndex}
		candidate.HealthLayer = healthLayers[account.ID]
		candidate.ComparisonKey, candidate.Prices = unifiedGatewayRoutePriorityPrice(request, account.ID, model)
		inputs = append(inputs, candidate)
	}
	ranks, err := routepriority.RankCandidates(inputs)
	if err != nil {
		return nil
	}
	return ranks
}

func unifiedGatewayRoutePriorityPrice(request *unifiedGatewayRoutePriorityRequest, accountID int64, model string) (string, []float64) {
	if request == nil || request.pricing == nil || request.groupID != request.pricing.config.TargetGroupID || accountID <= 0 || strings.TrimSpace(model) == "" {
		return "", nil
	}
	key := unifiedGatewayRoutePricingKey(request.groupID, accountID, model, UnifiedGatewayRoutePricingToken, "", "", "", "", 0)
	entry, found := request.pricing.entries[key]
	if !found || (entry.Multiplier != nil && entry.TokenBasePrice == nil && entry.TimeOfDayTokenPrice == nil) {
		multiplier := 1.0
		if found && entry.Multiplier != nil && finiteNonNegative(*entry.Multiplier) {
			multiplier = *entry.Multiplier
		} else if found && entry.Multiplier != nil {
			return "", nil
		}
		return "model:" + model + "|multiplier", []float64{multiplier}
	}

	lineMultiplier := 1.0
	if entry.Multiplier != nil {
		if !finiteNonNegative(*entry.Multiplier) {
			return "", nil
		}
		lineMultiplier = *entry.Multiplier
	}
	base := entry.TokenBasePrice
	if entry.TimeOfDayTokenPrice != nil {
		var ok bool
		base, ok = entry.TimeOfDayTokenPrice.priceAt(request.pricingAt)
		if !ok {
			return "", nil
		}
	}
	if base == nil || !base.validate() {
		return "", nil
	}
	prices, meterShape, ok := unifiedGatewayRoutePriorityBaseVector(base, lineMultiplier)
	if !ok {
		return "", nil
	}
	longContextShape := "|long:none"
	if entry.LongContextTokenBasePrice != nil {
		longPrices, longShape, valid := unifiedGatewayRoutePriorityBaseVector(entry.LongContextTokenBasePrice, lineMultiplier)
		if !valid {
			return "", nil
		}
		longContextShape = "|long:" + longShape
		prices = append(prices, longPrices...)
	}
	return "model:" + model + "|base|meters:" + meterShape + longContextShape, prices
}

func unifiedGatewayRoutePriorityBaseVector(base *UnifiedGatewayTokenBasePrice, multiplier float64) ([]float64, string, bool) {
	if base == nil || !base.validate() || !finiteNonNegative(multiplier) {
		return nil, "", false
	}
	prices := []float64{*base.InputPerMillion, *base.OutputPerMillion, *base.CacheReadPerMillion}
	shape := "input,output,cache_read"
	for _, meter := range []struct {
		name  string
		price *float64
	}{{"cache_write", base.CacheWritePerMillion}, {"cache_write_5m", base.CacheWrite5mPerMillion}, {"cache_write_1h", base.CacheWrite1hPerMillion}} {
		if meter.price != nil {
			shape += "," + meter.name
			prices = append(prices, *meter.price)
		}
	}
	for index := range prices {
		prices[index] *= multiplier
		if !finiteNonNegative(prices[index]) {
			return nil, "", false
		}
	}
	return prices, shape, true
}

func unifiedGatewayRoutePriorityRankLess(leftID, rightID int64, ranks map[int64]routepriority.Rank) bool {
	if len(ranks) == 0 || leftID == rightID {
		return false
	}
	left, leftOK := ranks[leftID]
	right, rightOK := ranks[rightID]
	if !leftOK || !rightOK {
		return false
	}
	if left.HealthLayer != right.HealthLayer {
		return left.HealthLayer < right.HealthLayer
	}
	return left.PriceLayer < right.PriceLayer
}

func openAIRoutePriorityHealthLayers(
	s *OpenAIGatewayService,
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	accounts []*Account,
) map[int64]int {
	if s == nil || req.RequireCompact || !s.isSmartRouterEnabled() || !s.isOpenAIAdvancedSchedulerEnabled(ctx) {
		return nil
	}
	capability := req.SmartRouterCapability
	if capability == "" {
		capability = smartRouterCapabilityForRequest(req)
	}
	if capability != core.CapabilityChat && capability != core.CapabilityResponses {
		return nil
	}
	health := s.smartRouterHealth()
	if health == nil {
		return nil
	}
	request := unifiedGatewayRoutePriorityRequestFromContext(ctx)
	now := time.Now().UTC()
	if request != nil {
		now = request.pricingAt
	}
	layers := make(map[int64]int, len(accounts))
	for _, account := range accounts {
		if account == nil || !smartRouterAccountEligibleForSmartRouter(account, capability, req.RequestedModel, req.RequiredCapability) {
			continue
		}
		lane, ok := smartRouterLaneSnapshot(account, nil, false, 0, 0, false, capability)
		if !ok {
			continue
		}
		lane = health.Snapshot(lane, capability, req.RequestedModel, now.Unix())
		if lane.RecoveryStage != core.RecoveryNormal || lane.CooldownUntilUnix > now.Unix() || lane.Priority > account.Priority {
			layers[account.ID] = 1
		}
	}
	return layers
}

func (s *OpenAIGatewayService) openAIRoutePriorityRanksForRequest(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	accounts []*Account,
) map[int64]routepriority.Rank {
	if s == nil || req.RequireCompact || req.GuardianParentAccountID > 0 || req.StickyAccountID > 0 || req.StickyPreviousAccountID > 0 || req.StickyWeighted || req.PreserveStickyBinding || strings.TrimSpace(req.PreviousResponseID) != "" || req.RequiredImageCapability != "" || req.RequiredCapability == OpenAIEndpointCapabilityEmbeddings || OpenAIImageGenerationIntentFromContext(ctx) {
		return nil
	}
	switch req.RequiredCapability {
	case OpenAIEndpointCapabilityChatCompletions, OpenAIEndpointCapabilityResponses:
	default:
		// Fail open for media, realtime, search, and any future capability until
		// its full billing shape is explicitly supported by route pricing.
		return nil
	}
	capability := req.SmartRouterCapability
	if capability != "" && capability != core.CapabilityChat && capability != core.CapabilityResponses {
		return nil
	}
	ctx = withUnifiedGatewayRoutePriorityRequest(ctx, s.cfg, s.settingService, req.GroupID, req.RequestedModel, false)
	healthLayers := openAIRoutePriorityHealthLayers(s, ctx, req, accounts)
	return unifiedGatewayRoutePriorityRanks(ctx, accounts, func(account *Account, model string) string {
		billingModel, _ := resolveOpenAIForwardMappedModels(account, model, false)
		return billingModel
	}, healthLayers)
}
