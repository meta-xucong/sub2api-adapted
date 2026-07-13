package core

import (
	"strings"
	"time"
)

// AttemptContext is the stable contract between the router and optional
// strategy plugins. Plugins receive metadata only, never request payloads.
type AttemptContext struct {
	Request           RouteRequest
	Lane              LaneSnapshot
	RemainingBudget   time.Duration
	RemainingAttempts int
}

type AttemptDirective struct {
	Skip            bool
	Timeout         time.Duration
	PriorityPenalty int
	Reason          string
}

type StrategyPlugin interface {
	Name() string
	BeforeAttempt(AttemptContext) AttemptDirective
	AfterAttempt(AttemptContext, RouteResult)
}

// StrategyChain lets deployments add or remove policy modules without
// changing the router's account/database adapter.
type StrategyChain struct {
	plugins []StrategyPlugin
}

func NewStrategyChain(plugins ...StrategyPlugin) StrategyChain {
	return StrategyChain{plugins: append([]StrategyPlugin(nil), plugins...)}
}

func (c StrategyChain) BeforeAttempt(ctx AttemptContext) AttemptDirective {
	result := AttemptDirective{}
	reasons := make([]string, 0, len(c.plugins))
	for _, plugin := range c.plugins {
		if plugin == nil {
			continue
		}
		directive := plugin.BeforeAttempt(ctx)
		result.Skip = result.Skip || directive.Skip
		if directive.Timeout > 0 && (result.Timeout <= 0 || directive.Timeout < result.Timeout) {
			result.Timeout = directive.Timeout
		}
		if directive.PriorityPenalty > result.PriorityPenalty {
			result.PriorityPenalty = directive.PriorityPenalty
		}
		if reason := strings.TrimSpace(directive.Reason); reason != "" {
			reasons = append(reasons, reason)
		}
	}
	result.Reason = strings.Join(reasons, ";")
	return result
}

func (c StrategyChain) AfterAttempt(ctx AttemptContext, result RouteResult) {
	for _, plugin := range c.plugins {
		if plugin != nil {
			plugin.AfterAttempt(ctx, result)
		}
	}
}

type AdaptiveTimeoutPlugin struct {
	Engine *AdaptiveTimeoutEngine
}

func (p AdaptiveTimeoutPlugin) Name() string { return "adaptive_timeout" }

func (p AdaptiveTimeoutPlugin) BeforeAttempt(ctx AttemptContext) AttemptDirective {
	if p.Engine == nil {
		return AttemptDirective{}
	}
	decision := p.Engine.TimeoutFor(TimeoutRequest{
		LaneID:            ctx.Lane.LaneID,
		Capability:        ctx.Request.Capability,
		RemainingBudget:   ctx.RemainingBudget,
		RemainingAttempts: ctx.RemainingAttempts,
	})
	return AttemptDirective{Timeout: decision.Timeout, Reason: decision.Reason}
}

func (p AdaptiveTimeoutPlugin) AfterAttempt(ctx AttemptContext, result RouteResult) {
	if p.Engine == nil {
		return
	}
	p.Engine.Observe(AttemptObservation{
		LaneID:       result.LaneID,
		Capability:   result.Capability,
		Duration:     time.Duration(result.TotalLatencyMs) * time.Millisecond,
		Success:      result.Success,
		FailureClass: result.ErrorClass,
		StatusCode:   result.StatusCode,
		ErrorSummary: result.ErrorSummary,
	})
}
