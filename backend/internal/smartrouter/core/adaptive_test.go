package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testAdaptiveConfig() AdaptiveTimeoutConfig {
	return AdaptiveTimeoutConfig{
		Enabled:           true,
		Default:           180 * time.Second,
		Min:               30 * time.Second,
		Max:               300 * time.Second,
		SafetyMargin:      10 * time.Second,
		Multiplier:        1.25,
		WindowSize:        4,
		ReservePerAttempt: 30 * time.Second,
	}
}

func TestAdaptiveTimeoutEngineSeparatesLaneAndCapability(t *testing.T) {
	engine := NewAdaptiveTimeoutEngine(testAdaptiveConfig())
	for _, duration := range []time.Duration{20 * time.Second, 25 * time.Second, 30 * time.Second, 35 * time.Second} {
		engine.Observe(AttemptObservation{
			LaneID:     "liuyun",
			Capability: CapabilityImageGeneration,
			Duration:   duration,
			Success:    true,
		})
	}

	generation := engine.TimeoutFor(TimeoutRequest{
		LaneID:         "liuyun",
		Capability:     CapabilityImageGeneration,
		DefaultTimeout: 180 * time.Second,
	})
	edit := engine.TimeoutFor(TimeoutRequest{
		LaneID:         "liuyun",
		Capability:     CapabilityImageEdit,
		DefaultTimeout: 180 * time.Second,
	})

	require.Equal(t, "success_step_down", generation.Reason)
	require.Equal(t, 140*time.Second, generation.Timeout)
	require.Equal(t, 180*time.Second, edit.Timeout)
}

func TestAdaptiveTimeoutEngineSeparatesImageSizeAndInputMode(t *testing.T) {
	engine := NewAdaptiveTimeoutEngine(testAdaptiveConfig())
	for i := 0; i < 4; i++ {
		engine.Observe(AttemptObservation{
			LaneID:         "image-lane",
			Capability:     CapabilityImageGeneration,
			ImageSizeTier:  "1K",
			ImageInputMode: ImageInputTextOnly,
			Duration:       80 * time.Second,
			Success:        true,
		})
	}

	textOnly := engine.TimeoutFor(TimeoutRequest{
		LaneID:              "image-lane",
		Capability:          CapabilityImageGeneration,
		ImageSizeTier:       "1k",
		ImageInputMode:      ImageInputTextOnly,
		ProfileDefault:      180 * time.Second,
		ProfileMin:          60 * time.Second,
		ProfileMax:          240 * time.Second,
		ProfileMultiplier:   1.25,
		ProfileSafetyMargin: 20 * time.Second,
	})
	reference := engine.TimeoutFor(TimeoutRequest{
		LaneID:         "image-lane",
		Capability:     CapabilityImageGeneration,
		ImageSizeTier:  "1K",
		ImageInputMode: ImageInputReferenceImage,
		ProfileDefault: 180 * time.Second,
		ProfileMin:     60 * time.Second,
		ProfileMax:     240 * time.Second,
	})

	require.Equal(t, "success_step_down", textOnly.Reason)
	require.Equal(t, 140*time.Second, textOnly.Timeout)
	require.Equal(t, "default", reference.Reason)
	require.Equal(t, 180*time.Second, reference.Timeout)

	ledger := engine.Ledger()
	require.Len(t, ledger, 4)
	require.Equal(t, ImageInputTextOnly, ledger[0].ImageInputMode)
}

func TestAdaptiveTimeoutEngineClampsToRemainingBudgetAndKeepsFallbackRoom(t *testing.T) {
	engine := NewAdaptiveTimeoutEngine(testAdaptiveConfig())
	for i := 0; i < 4; i++ {
		engine.Observe(AttemptObservation{
			LaneID:     "slow",
			Capability: CapabilityImageEdit,
			Duration:   150 * time.Second,
			Success:    true,
		})
	}
	decision := engine.TimeoutFor(TimeoutRequest{
		LaneID:            "slow",
		Capability:        CapabilityImageEdit,
		DefaultTimeout:    180 * time.Second,
		RemainingBudget:   100 * time.Second,
		RemainingAttempts: 1,
	})
	require.Equal(t, 70*time.Second, decision.Timeout)
	require.LessOrEqual(t, decision.Timeout+30*time.Second, 100*time.Second)
}

func TestAdaptiveTimeoutEngineResetsAfterTransientFailures(t *testing.T) {
	engine := NewAdaptiveTimeoutEngine(testAdaptiveConfig())
	engine.Observe(AttemptObservation{
		LaneID:       "flapping",
		Capability:   CapabilityImageGeneration,
		Duration:     180 * time.Second,
		FailureClass: FailureUpstream5xx,
		StatusCode:   502,
	})

	decision := engine.TimeoutFor(TimeoutRequest{
		LaneID:         "flapping",
		Capability:     CapabilityImageGeneration,
		DefaultTimeout: 180 * time.Second,
	})
	require.Equal(t, "failure_reset", decision.Reason)
	require.Equal(t, 180*time.Second, decision.Timeout)

	engine.Observe(AttemptObservation{
		LaneID:       "flapping",
		Capability:   CapabilityImageGeneration,
		Duration:     180 * time.Second,
		FailureClass: FailureTimeout,
	})
	decision = engine.TimeoutFor(TimeoutRequest{
		LaneID:         "flapping",
		Capability:     CapabilityImageGeneration,
		DefaultTimeout: 180 * time.Second,
	})
	require.Equal(t, 180*time.Second, decision.Timeout)

	engine.Observe(AttemptObservation{
		LaneID:     "flapping",
		Capability: CapabilityImageGeneration,
		Duration:   40 * time.Second,
		Success:    true,
	})
	decision = engine.TimeoutFor(TimeoutRequest{
		LaneID:         "flapping",
		Capability:     CapabilityImageGeneration,
		DefaultTimeout: 180 * time.Second,
	})
	require.Equal(t, "success_step_down", decision.Reason)
	require.Equal(t, 170*time.Second, decision.Timeout)
}

func TestAdaptiveTimeoutEngineReservesOneWindowWhenAttemptsAreUnknown(t *testing.T) {
	engine := NewAdaptiveTimeoutEngine(testAdaptiveConfig())
	decision := engine.TimeoutFor(TimeoutRequest{
		LaneID:          "lane",
		Capability:      CapabilityImageGeneration,
		DefaultTimeout:  180 * time.Second,
		RemainingBudget: 100 * time.Second,
	})
	require.Equal(t, 70*time.Second, decision.Timeout)
}

func TestAdaptiveTimeoutEngineIgnoresDeterministicFailuresAndCancellation(t *testing.T) {
	engine := NewAdaptiveTimeoutEngine(testAdaptiveConfig())
	for _, class := range []FailureClass{FailureClientError, FailureAuthForbidden, FailureCancelled} {
		engine.Observe(AttemptObservation{
			LaneID:       "lane",
			Capability:   CapabilityImageGeneration,
			Duration:     180 * time.Second,
			FailureClass: class,
		})
	}

	decision := engine.TimeoutFor(TimeoutRequest{
		LaneID:         "lane",
		Capability:     CapabilityImageGeneration,
		DefaultTimeout: 180 * time.Second,
	})
	require.Equal(t, "default", decision.Reason)
	require.Equal(t, 180*time.Second, decision.Timeout)
}

func TestAdaptiveTimeoutEngineNeverDropsBelowAveragePlusSafetyMargin(t *testing.T) {
	engine := NewAdaptiveTimeoutEngine(testAdaptiveConfig())
	for i := 0; i < 20; i++ {
		engine.Observe(AttemptObservation{
			LaneID:     "fast",
			Capability: CapabilityImageGeneration,
			Duration:   80 * time.Second,
			Success:    true,
		})
	}
	decision := engine.TimeoutFor(TimeoutRequest{
		LaneID:         "fast",
		Capability:     CapabilityImageGeneration,
		DefaultTimeout: 180 * time.Second,
	})
	require.Equal(t, 110*time.Second, decision.Timeout)
}

func TestAdaptiveTimeoutEngineLedgerIsBoundedAndSummariesAreSanitized(t *testing.T) {
	engine := NewAdaptiveTimeoutEngine(testAdaptiveConfig())
	for i := 0; i < 40; i++ {
		engine.Observe(AttemptObservation{
			LaneID:       "lane",
			Capability:   CapabilityImageGeneration,
			Duration:     time.Second,
			Success:      false,
			ErrorSummary: "  status=502\nsecret=should-not-be-logged " + string(make([]byte, 300)),
		})
	}
	ledger := engine.Ledger()
	require.Len(t, ledger, 16)
	require.LessOrEqual(t, len(ledger[len(ledger)-1].ErrorSummary), 256)
	require.NotContains(t, ledger[len(ledger)-1].ErrorSummary, "\n")
}

func TestStrategyChainUsesShortestTimeoutAndReportsToPlugin(t *testing.T) {
	engine := NewAdaptiveTimeoutEngine(testAdaptiveConfig())
	chain := NewStrategyChain(AdaptiveTimeoutPlugin{Engine: engine})
	ctx := AttemptContext{
		Request: RouteRequest{Capability: CapabilityImageGeneration},
		Lane:    LaneSnapshot{LaneID: "lane"},
	}
	directive := chain.BeforeAttempt(ctx)
	require.Equal(t, 180*time.Second, directive.Timeout)
	chain.AfterAttempt(ctx, RouteResult{
		LaneID:         "lane",
		Capability:     CapabilityImageGeneration,
		Success:        true,
		TotalLatencyMs: 40000,
	})
	require.Len(t, engine.Ledger(), 1)
}
