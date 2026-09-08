package service

import (
	"testing"

	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/stretchr/testify/require"
)

func TestEnsureSmartRouterModelRecoveryProbesKeepsExactModels(t *testing.T) {
	accountsByLane := map[string]*Account{
		"account:1": {ID: 1},
	}
	states := []SmartRouterHealthState{
		{
			LaneID:      "account:1",
			Capability:  smartrouter.CapabilityResponses,
			ModelFamily: "gpt-5.6-luna",
			Snapshot:    smartrouter.HealthSnapshot{RecoveryPriority: 30},
		},
		{
			LaneID:      "account:1",
			Capability:  smartrouter.CapabilityResponses,
			ModelFamily: "gpt-5.6-terra",
			Snapshot:    smartrouter.HealthSnapshot{RecoveryStage: smartrouter.RecoveryCooling},
		},
	}

	probes := ensureSmartRouterModelRecoveryProbes(nil, states, accountsByLane)
	require.ElementsMatch(t, []smartrouter.CalibrationProbe{
		{LaneID: "account:1", Capability: smartrouter.CapabilityResponses, Model: "gpt-5.6-luna", Reason: "model_recovery_due"},
		{LaneID: "account:1", Capability: smartrouter.CapabilityResponses, Model: "gpt-5.6-terra", Reason: "model_recovery_due"},
	}, probes)
}

func TestEnsureSmartRouterModelRecoveryProbesIncludesModelUnavailable(t *testing.T) {
	accountsByLane := map[string]*Account{
		"account:1": {ID: 1},
	}
	states := []SmartRouterHealthState{{
		LaneID:      "account:1",
		Capability:  smartrouter.CapabilityResponses,
		ModelFamily: "gpt-5.6-luna",
		Snapshot: smartrouter.HealthSnapshot{
			RecoveryStage: smartrouter.RecoveryModelUnavailable,
		},
	}}

	probes := ensureSmartRouterModelRecoveryProbes(nil, states, accountsByLane)
	require.Equal(t, []smartrouter.CalibrationProbe{{
		LaneID:     "account:1",
		Capability: smartrouter.CapabilityResponses,
		Model:      "gpt-5.6-luna",
		Reason:     "model_recovery_due",
	}}, probes)
}

func TestEnsureSmartRouterModelRecoveryProbesDoesNotDuplicateExistingProbe(t *testing.T) {
	accountsByLane := map[string]*Account{"account:1": {ID: 1}}
	states := []SmartRouterHealthState{{
		LaneID:      "account:1",
		Capability:  smartrouter.CapabilityResponsesCompact,
		ModelFamily: "gpt-5.6-luna",
		Snapshot:    smartrouter.HealthSnapshot{RecoveryPriority: 30},
	}}
	existing := []smartrouter.CalibrationProbe{{
		LaneID: "account:1", Capability: smartrouter.CapabilityResponsesCompact, Model: "gpt-5.6-luna", Reason: "scheduled_model_compact_probe",
	}}

	probes := ensureSmartRouterModelRecoveryProbes(existing, states, accountsByLane)
	require.Len(t, probes, 1)
	require.Equal(t, existing[0], probes[0])
}

func TestEnsureSmartRouterModelRecoveryProbesIgnoresLegacyFamilyRows(t *testing.T) {
	accountsByLane := map[string]*Account{"account:1": {ID: 1}}
	states := []SmartRouterHealthState{
		{LaneID: "account:1", Capability: smartrouter.CapabilityResponses, ModelFamily: "gpt-5", Snapshot: smartrouter.HealthSnapshot{RecoveryPriority: 30}},
		{LaneID: "account:1", Capability: smartrouter.CapabilityImageGeneration, ModelFamily: "gpt-image", Snapshot: smartrouter.HealthSnapshot{RecoveryStage: smartrouter.RecoveryCooling}},
	}

	require.Empty(t, ensureSmartRouterModelRecoveryProbes(nil, states, accountsByLane))
}

func TestSmartRouterAccountSupportsRecoveryModelUsesExactOrWildcardMapping(t *testing.T) {
	account := &Account{Credentials: map[string]any{
		"model_mapping": map[string]any{
			"gpt-5.6-luna": "gpt-5.6-luna",
			"gpt-5.5*":     "gpt-5.5",
		},
	}}
	require.True(t, smartRouterAccountSupportsRecoveryModel(account, smartrouter.CapabilityResponses, "gpt-5.6-luna"))
	require.True(t, smartRouterAccountSupportsRecoveryModel(account, smartrouter.CapabilityResponses, "gpt-5.5"))
	require.False(t, smartRouterAccountSupportsRecoveryModel(account, smartrouter.CapabilityResponses, "gpt-5.4"))
}
