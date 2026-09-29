package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
)

var smartRouterShanghai = time.FixedZone("Asia/Shanghai", 8*60*60)

func smartRouterCalibrationLocation() *time.Location { return smartRouterShanghai }

func (s *OpenAIGatewayService) SetSmartRouterHealthLedger(ledger SmartRouterHealthLedger) {
	if s == nil {
		return
	}
	s.smartRouterHealthLedger = ledger
}

func (s *OpenAIGatewayService) smartRouterEnabled() bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.SmartRouter.Enabled
}

func (s *OpenAIGatewayService) smartRouterPolicy() smartrouter.Policy {
	policy := smartrouter.DefaultPolicy()
	if s == nil || s.cfg == nil {
		return policy
	}
	c := s.cfg.Gateway.SmartRouter
	policy.Enabled = c.Enabled
	policy.TopK = c.TopK
	policy.MaxAttemptsChat = c.MaxAttemptsChat
	policy.MaxAttemptsCompact = c.MaxAttemptsCompact
	policy.MaxAttemptsDefault = c.MaxAttemptsDefault
	policy.SameSourceGroupAttempts = c.SameSourceGroupAttempts
	policy.CostBiasMax = c.CostBiasMax
	policy.Weights = smartrouter.ScoreWeights{
		Priority: c.Scoring.Priority, Cost: c.Scoring.Cost, Health: c.Scoring.Health,
		Load: c.Scoring.Load, Queue: c.Scoring.Queue, Latency: c.Scoring.Latency,
		Recovery: c.Scoring.Recovery,
	}
	return policy.Normalize()
}

func (s *OpenAIGatewayService) smartRouterHealthPolicy() smartrouter.HealthPolicy {
	policy := smartrouter.DefaultHealthPolicy()
	if s != nil && s.cfg != nil {
		recovery := s.cfg.Gateway.SmartRouter.Recovery
		if recovery.SecondFailureCooldownSeconds > 0 {
			policy.SecondTransientCooldown = time.Duration(recovery.SecondFailureCooldownSeconds) * time.Second
		}
		if recovery.SustainedFailureThreshold > 0 {
			policy.SustainedFailureThreshold = recovery.SustainedFailureThreshold
		}
	}
	policy.SustainedFailureUntil = func(now time.Time) time.Time {
		local := now.In(smartRouterShanghai)
		target := time.Date(local.Year(), local.Month(), local.Day(), 4, 0, 0, 0, smartRouterShanghai)
		if !local.Before(target) {
			target = target.Add(24 * time.Hour)
		}
		return target
	}
	return policy
}

func (s *OpenAIGatewayService) smartRouterHealthTracker() *smartrouter.HealthTracker {
	if s == nil {
		return nil
	}
	s.smartRouterHealthOnce.Do(func() {
		s.smartRouterHealth = smartrouter.NewHealthTracker(s.smartRouterHealthPolicy(), nil, func(event smartrouter.HealthEvent) {
			if s.smartRouterHealthLedger == nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := s.smartRouterHealthLedger.RecordEvent(ctx, event); err != nil {
				slog.Warn("smart router health ledger write failed", "lane_id", event.Key.LaneID, "capability", event.Key.Capability, "error", err)
			}
		})
		if s.smartRouterHealthLedger == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		states, err := s.smartRouterHealthLedger.LoadStates(ctx)
		if err != nil {
			slog.Warn("smart router health restore failed", "error", err)
			return
		}
		for _, state := range states {
			s.smartRouterHealth.RestoreWithSourceGroup(
				smartrouter.NewHealthKey(state.LaneID, state.Capability, state.ModelFamily),
				state.Snapshot, state.LastFailureUnix, state.SourceGroup,
			)
		}
	})
	return s.smartRouterHealth
}

// ReportSmartRouterChatResult and ReportSmartRouterResponsesResult are called
// by the existing text forwarding handlers after each real upstream attempt.
// They are intentionally not called from image or compatibility handlers.
func (s *OpenAIGatewayService) ReportSmartRouterChatResult(account *Account, model string, result *OpenAIForwardResult, err error, latencyMs int64) {
	s.reportSmartRouterTextResult(account, smartrouter.CapabilityChat, model, result, err, latencyMs, "production")
}

func (s *OpenAIGatewayService) ReportSmartRouterResponsesResult(account *Account, model string, result *OpenAIForwardResult, err error, latencyMs int64) {
	s.reportSmartRouterTextResult(account, smartrouter.CapabilityResponses, model, result, err, latencyMs, "production")
}

func (s *OpenAIGatewayService) reportSmartRouterTextResult(account *Account, capability smartrouter.Capability, model string, result *OpenAIForwardResult, err error, latencyMs int64, source string) {
	if !s.smartRouterEnabled() || account == nil {
		return
	}
	tracker := s.smartRouterHealthTracker()
	if tracker == nil {
		return
	}
	status := 0
	if result != nil && result.ResponseHeaders != nil {
		status = 200
	}
	if upstreamErr := smartRouterUpstreamError(err); upstreamErr != nil {
		status = upstreamErr.StatusCode
	}
	summary := smartRouterErrorSummary(err)
	success := err == nil
	class := smartrouter.FailureClass("")
	if !success {
		class = smartrouter.ClassifyFailureDetails(status, capability, summary, "", errors.Is(err, context.Canceled))
	}
	tracker.Observe(smartrouter.RouteResult{
		Source: source, LaneID: smartRouterLaneID(account), AccountID: account.ID,
		SourceGroup: smartRouterSourceGroup(account), Capability: capability, Model: model,
		Success: success, StatusCode: status, ErrorClass: class, TotalLatencyMs: latencyMs,
		FirstTokenMs: func() *int {
			if result != nil {
				return result.FirstTokenMs
			}
			return nil
		}(),
		ErrorSummary: summary,
	})
}

func smartRouterUpstreamError(err error) *UpstreamFailoverError {
	if err == nil {
		return nil
	}
	var upstreamErr *UpstreamFailoverError
	if errors.As(err, &upstreamErr) {
		return upstreamErr
	}
	return nil
}

func smartRouterErrorSummary(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 256 {
		message = message[:256]
	}
	return message
}

func smartRouterLaneID(account *Account) string {
	if account == nil {
		return ""
	}
	if value := strings.TrimSpace(account.GetExtraString("smart_router_lane_id")); value != "" {
		return value
	}
	return fmt.Sprintf("account:%d", account.ID)
}

func smartRouterSourceGroup(account *Account) string {
	if account == nil {
		return ""
	}
	if value := strings.TrimSpace(account.GetExtraString("smart_router_source_group")); value != "" {
		return value
	}
	if account.ProxyID != nil {
		return fmt.Sprintf("proxy:%d", *account.ProxyID)
	}
	return strings.TrimSpace(account.Platform)
}

func smartRouterModelPatterns(account *Account) []string {
	if account == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var patterns []string
	for _, model := range account.GetModelMapping() {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; !ok {
			seen[model] = struct{}{}
			patterns = append(patterns, model)
		}
	}
	return patterns
}

func (s *OpenAIGatewayService) smartRouterLaneSnapshot(account *Account, loadInfo *AccountLoadInfo, errorRate, ttft float64, hasTTFT bool) (smartrouter.LaneSnapshot, bool) {
	if account == nil || !account.IsOpenAI() {
		return smartrouter.LaneSnapshot{}, false
	}
	capabilities := map[smartrouter.Capability]bool{}
	if account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions) {
		capabilities[smartrouter.CapabilityChat] = true
	}
	if account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityResponses) {
		capabilities[smartrouter.CapabilityResponses] = true
	}
	if account.AllowsOpenAICompact() {
		capabilities[smartrouter.CapabilityResponsesCompact] = true
	}
	if len(capabilities) == 0 {
		return smartrouter.LaneSnapshot{}, false
	}
	load := AccountLoadInfo{}
	if loadInfo != nil {
		load = *loadInfo
	}
	weight := 1.0
	if account.RateMultiplier != nil && *account.RateMultiplier > 0 && !math.IsNaN(*account.RateMultiplier) {
		weight = *account.RateMultiplier
	}
	lane := smartrouter.LaneSnapshot{
		LaneID: smartRouterLaneID(account), AccountID: account.ID, Name: account.Name,
		SourceGroup: smartRouterSourceGroup(account), Capabilities: capabilities,
		ModelPatterns: smartRouterModelPatterns(account), Priority: account.Priority,
		CostMultiplier: weight, BaseWeight: weight, MaxConcurrency: account.Concurrency,
		CurrentConcurrency: load.CurrentConcurrency, CurrentWaiting: load.WaitingCount,
		LoadRate: load.LoadRate, HealthScore: 1, ErrorRateEWMA: errorRate, Metadata: map[string]string{},
	}
	if hasTTFT {
		lane.LatencyEWMAms = ttft
	}
	return lane, true
}

func smartRouterRouteCapability(req OpenAIAccountScheduleRequest) smartrouter.Capability {
	if req.RequireCompact {
		return smartrouter.CapabilityResponsesCompact
	}
	if req.RequiredCapability == OpenAIEndpointCapabilityResponses {
		return smartrouter.CapabilityResponses
	}
	return smartrouter.CapabilityChat
}

func smartRouterCalibrationPolicy(cfg *config.Config) smartrouter.CalibrationPolicy {
	policy := smartrouter.DefaultCalibrationPolicy()
	if cfg == nil {
		return policy
	}
	c := cfg.Gateway.SmartRouter.Calibration
	// Config loaded through Viper has the 04:00 defaults applied. Keep the
	// same safe default for zero-value configs used by embedded callers/tests;
	// a non-zero pair is an explicit schedule override.
	if c.Hour != 0 || c.Minute != 0 {
		policy.Hour, policy.Minute = c.Hour, c.Minute
	}
	return policy
}
