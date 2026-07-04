package service

import (
	"encoding/json"
	"net"
	"net/url"
	"strconv"
	"strings"

	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
)

const smartRouterExtraKey = "smart_router"

type smartRouterAccountExtra struct {
	Present                   bool
	EnabledSet                bool
	Enabled                   bool
	LaneID                    string
	SourceGroup               string
	BaseWeight                float64
	CostMultiplier            float64
	MaxConcurrency            int
	SourceGroupMaxConcurrency int
	Capabilities              map[smartrouter.Capability]bool
}

func (s *OpenAIGatewayService) smartRouterPolicy() smartrouter.Policy {
	policy := smartrouter.DefaultPolicy()
	if s == nil || s.cfg == nil {
		return policy
	}
	cfg := s.cfg.Gateway.SmartRouter
	policy.Enabled = cfg.Enabled
	policy.TopK = cfg.TopK
	policy.MaxAttemptsImage = cfg.MaxAttemptsImage
	policy.MaxAttemptsChat = cfg.MaxAttemptsChat
	policy.MaxAttemptsDefault = cfg.MaxAttemptsDefault
	policy.SameSourceGroupAttempts = cfg.SameSourceGroupAttempts
	policy.CostBiasMax = cfg.CostBiasMax
	policy.Weights = smartrouter.ScoreWeights{
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

func (s *OpenAIGatewayService) isSmartRouterEnabled() bool {
	return s.smartRouterPolicy().Enabled
}

func (s *OpenAIGatewayService) smartRouterExcludedSourceGroups(accounts []Account, excludedIDs map[int64]struct{}) map[string]struct{} {
	if !s.isSmartRouterEnabled() || len(accounts) == 0 || len(excludedIDs) == 0 {
		return nil
	}
	groups := make(map[string]struct{})
	for i := range accounts {
		account := &accounts[i]
		if _, excluded := excludedIDs[account.ID]; !excluded {
			continue
		}
		groups[smartRouterSourceGroup(account)] = struct{}{}
	}
	if len(groups) == 0 {
		return nil
	}
	return groups
}

func smartRouterLaneSnapshot(account *Account, loadInfo *AccountLoadInfo, errorRate float64, ttft float64, hasTTFT bool) (smartrouter.LaneSnapshot, bool) {
	if account == nil {
		return smartrouter.LaneSnapshot{}, false
	}
	extra := parseSmartRouterAccountExtra(account)
	if extra.Present && extra.EnabledSet && !extra.Enabled {
		return smartrouter.LaneSnapshot{}, false
	}
	laneID := strings.TrimSpace(extra.LaneID)
	if laneID == "" {
		laneID = "account:" + strconv.FormatInt(account.ID, 10)
	}
	sourceGroup := strings.TrimSpace(extra.SourceGroup)
	if sourceGroup == "" {
		sourceGroup = smartRouterAutoSourceGroup(account)
	}
	baseWeight := extra.BaseWeight
	if baseWeight <= 0 {
		baseWeight = 1
	}
	costMultiplier := extra.CostMultiplier
	if costMultiplier <= 0 {
		costMultiplier = account.BillingRateMultiplier()
	}
	maxConcurrency := extra.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = account.Concurrency
	}
	currentConcurrency, waitingCount, loadRate := 0, 0, 0
	if loadInfo != nil {
		currentConcurrency = loadInfo.CurrentConcurrency
		waitingCount = loadInfo.WaitingCount
		loadRate = loadInfo.LoadRate
	}
	maxConcurrency = smartRouterAdaptiveMaxConcurrency(maxConcurrency, errorRate, currentConcurrency, waitingCount, loadRate)
	sourceGroupMaxConcurrency := extra.SourceGroupMaxConcurrency
	if sourceGroupMaxConcurrency <= 0 {
		sourceGroupMaxConcurrency = smartRouterAutoSourceGroupMaxConcurrency(sourceGroup, maxConcurrency, errorRate)
	}
	latency := 0.0
	if hasTTFT && ttft > 0 {
		latency = ttft
	}
	return smartrouter.LaneSnapshot{
		LaneID:                    laneID,
		AccountID:                 account.ID,
		Name:                      account.Name,
		SourceGroup:               sourceGroup,
		Capabilities:              extra.Capabilities,
		ModelPatterns:             smartRouterModelPatterns(account),
		Priority:                  account.Priority,
		CostMultiplier:            costMultiplier,
		BaseWeight:                baseWeight,
		MaxConcurrency:            maxConcurrency,
		SourceGroupMaxConcurrency: sourceGroupMaxConcurrency,
		CurrentConcurrency:        currentConcurrency,
		CurrentWaiting:            waitingCount,
		LoadRate:                  loadRate,
		HealthScore:               1,
		ErrorRateEWMA:             errorRate,
		LatencyEWMAms:             latency,
	}, true
}

func smartRouterSourceGroup(account *Account) string {
	if account == nil {
		return ""
	}
	extra := parseSmartRouterAccountExtra(account)
	if group := strings.TrimSpace(extra.SourceGroup); group != "" {
		return group
	}
	return smartRouterAutoSourceGroup(account)
}

func smartRouterAutoSourceGroup(account *Account) string {
	if account == nil {
		return ""
	}
	if key := smartRouterLongNumericNameKey(account.Name); key != "" {
		return "name-key:" + key
	}
	if host := smartRouterBaseURLHost(account); host != "" {
		return "host:" + host
	}
	return "account:" + strconv.FormatInt(account.ID, 10)
}

func smartRouterLongNumericNameKey(name string) string {
	longest := ""
	current := strings.Builder{}
	flush := func() {
		if current.Len() >= 6 && current.Len() > len(longest) {
			longest = current.String()
		}
		current.Reset()
	}
	for _, r := range name {
		if r >= '0' && r <= '9' {
			current.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return longest
}

func smartRouterBaseURLHost(account *Account) string {
	if account == nil {
		return ""
	}
	raw := strings.TrimSpace(account.GetCredential("base_url"))
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Host))
	if host == "" {
		return ""
	}
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	host = strings.TrimPrefix(host, "www.")
	return strings.Trim(host, ".")
}

func smartRouterAdaptiveMaxConcurrency(base int, errorRate float64, currentConcurrency int, waitingCount int, loadRate int) int {
	if base <= 0 {
		return 0
	}
	limit := base
	switch {
	case errorRate >= 0.60:
		limit = minPositiveInt(limit, 1)
	case errorRate >= 0.35:
		limit = minPositiveInt(limit, 2)
	case errorRate >= 0.20:
		limit = minPositiveInt(limit, ceilDivInt(base, 2))
	}
	if loadRate >= 95 {
		limit = minPositiveInt(limit, ceilDivInt(base, 4))
	} else if loadRate >= 85 {
		limit = minPositiveInt(limit, ceilDivInt(base, 2))
	}
	if waitingCount > 0 {
		switch {
		case currentConcurrency <= 1:
			limit = minPositiveInt(limit, 1)
		default:
			limit = minPositiveInt(limit, currentConcurrency)
		}
	}
	if limit <= 0 {
		return 1
	}
	return limit
}

func smartRouterAutoSourceGroupMaxConcurrency(sourceGroup string, maxConcurrency int, errorRate float64) int {
	if maxConcurrency <= 0 || !strings.HasPrefix(sourceGroup, "name-key:") {
		return 0
	}
	if errorRate >= 0.35 {
		return 1
	}
	if maxConcurrency <= 2 {
		return maxConcurrency
	}
	return 2
}

func smartRouterGroupID(groupID *int64) string {
	if groupID == nil {
		return ""
	}
	return strconv.FormatInt(*groupID, 10)
}

func parseSmartRouterAccountExtra(account *Account) smartRouterAccountExtra {
	if account == nil || account.Extra == nil {
		return smartRouterAccountExtra{}
	}
	raw, ok := account.Extra[smartRouterExtraKey]
	if !ok || raw == nil {
		return smartRouterAccountExtra{}
	}
	block, ok := raw.(map[string]any)
	if !ok {
		return smartRouterAccountExtra{Present: true}
	}
	cfg := smartRouterAccountExtra{Present: true}
	if enabled, ok := smartRouterBool(block["enabled"]); ok {
		cfg.EnabledSet = true
		cfg.Enabled = enabled
	}
	cfg.LaneID = smartRouterString(block["lane_id"])
	cfg.SourceGroup = smartRouterString(block["source_group"])
	cfg.BaseWeight = smartRouterFloat(block["base_weight"])
	cfg.CostMultiplier = smartRouterFloat(block["cost_multiplier"])
	cfg.MaxConcurrency = smartRouterInt(block["max_concurrency"])
	cfg.SourceGroupMaxConcurrency = smartRouterInt(block["source_group_max_concurrency"])
	cfg.Capabilities = smartRouterCapabilities(block["capabilities"])
	return cfg
}

func smartRouterModelPatterns(account *Account) []string {
	if account == nil {
		return nil
	}
	mapping := account.GetModelMapping()
	if len(mapping) == 0 {
		return nil
	}
	patterns := make([]string, 0, len(mapping))
	for pattern := range mapping {
		if trimmed := strings.TrimSpace(pattern); trimmed != "" {
			patterns = append(patterns, trimmed)
		}
	}
	return patterns
}

func smartRouterCapabilities(raw any) map[smartrouter.Capability]bool {
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
			if boolValue, ok := smartRouterBool(enabled); ok && boolValue {
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
	if len(values) == 0 {
		return nil
	}
	capabilities := make(map[smartrouter.Capability]bool)
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		switch value {
		case string(smartrouter.CapabilityChat):
			capabilities[smartrouter.CapabilityChat] = true
		case string(smartrouter.CapabilityResponses):
			capabilities[smartrouter.CapabilityResponses] = true
		case string(smartrouter.CapabilityImageGeneration):
			capabilities[smartrouter.CapabilityImageGeneration] = true
		case string(smartrouter.CapabilityImageEdit):
			capabilities[smartrouter.CapabilityImageEdit] = true
		case string(smartrouter.CapabilityEmbedding):
			capabilities[smartrouter.CapabilityEmbedding] = true
		}
	}
	if len(capabilities) == 0 {
		return nil
	}
	return capabilities
}

func smartRouterString(value any) string {
	if str, ok := value.(string); ok {
		return strings.TrimSpace(str)
	}
	return ""
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
	case float32:
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
	}
	return false, false
}

func ceilDivInt(value int, divisor int) int {
	if divisor <= 0 {
		return value
	}
	if value <= 0 {
		return 0
	}
	return (value + divisor - 1) / divisor
}

func minPositiveInt(left int, right int) int {
	if left <= 0 {
		return right
	}
	if right <= 0 {
		return left
	}
	if left < right {
		return left
	}
	return right
}
