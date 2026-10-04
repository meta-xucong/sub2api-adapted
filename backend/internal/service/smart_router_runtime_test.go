package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/stretchr/testify/require"
)

type smartRouterRuntimeTestLedger struct {
	states []SmartRouterHealthState
	events []core.HealthEvent
}

func (l *smartRouterRuntimeTestLedger) LoadStates(context.Context) ([]SmartRouterHealthState, error) {
	return l.states, nil
}

func (l *smartRouterRuntimeTestLedger) RecordEvent(_ context.Context, event core.HealthEvent) error {
	l.events = append(l.events, event)
	return nil
}

func (l *smartRouterRuntimeTestLedger) ListCapabilityEvidence(context.Context) ([]SmartRouterCapabilityEvidence, error) {
	return nil, nil
}

func (l *smartRouterRuntimeTestLedger) BeginCalibrationRun(context.Context, time.Time) (*SmartRouterCalibrationRun, bool, error) {
	return nil, false, nil
}

func (l *smartRouterRuntimeTestLedger) RecordCalibrationResult(context.Context, int64, SmartRouterCalibrationResult) error {
	return nil
}

func (l *smartRouterRuntimeTestLedger) FinishCalibrationRun(context.Context, int64, bool, string) error {
	return nil
}

func TestSmartRouterHealthRestoresDurableModelScopedState(t *testing.T) {
	now := time.Now()
	ledger := &smartRouterRuntimeTestLedger{states: []SmartRouterHealthState{{
		LaneID:      "account:201",
		AccountID:   201,
		SourceGroup: "upstream-a",
		Capability:  core.CapabilityResponses,
		ModelFamily: "gpt-5.5",
		Snapshot: core.HealthSnapshot{
			HealthScore:       0.5,
			RecoveryStage:     core.RecoveryModelUnavailable,
			CooldownUntilUnix: now.Add(time.Hour).Unix(),
		},
		LastFailureUnix: now.Unix(),
	}}}
	svc := &OpenAIGatewayService{cfg: &config.Config{
		Gateway: config.GatewayConfig{SmartRouter: config.GatewaySmartRouterConfig{Enabled: true}},
	}}
	svc.SetSmartRouterHealthLedger(ledger)
	tracker := svc.smartRouterHealth()
	require.NotNil(t, tracker)

	got := tracker.Snapshot(core.LaneSnapshot{LaneID: "account:201"}, core.CapabilityResponses, "gpt-5.5", now.Unix())
	require.Equal(t, core.RecoveryModelUnavailable, got.RecoveryStage)
	require.Equal(t, now.Add(time.Hour).Unix(), got.CooldownUntilUnix)
	otherModel := tracker.Snapshot(core.LaneSnapshot{LaneID: "account:201"}, core.CapabilityResponses, "gpt-5.6-sol", now.Unix())
	require.NotEqual(t, core.RecoveryModelUnavailable, otherModel.RecoveryStage)
}

func TestSmartRouterReportsPersistDistinctCapabilityAndModel(t *testing.T) {
	ledger := &smartRouterRuntimeTestLedger{}
	svc := &OpenAIGatewayService{cfg: &config.Config{
		Gateway: config.GatewayConfig{SmartRouter: config.GatewaySmartRouterConfig{Enabled: true}},
	}}
	svc.SetSmartRouterHealthLedger(ledger)
	account := smartRouterTestAccount(202, AccountTypeOAuth, nil, nil)

	svc.ReportSmartRouterTextResult(account, core.CapabilityResponses, "gpt-5.6-sol", nil, errors.New("temporary upstream failure"), 73)
	svc.ReportSmartRouterCompactResult(account, "gpt-5.6-sol", nil, errors.New("compact request failed"), 91)

	require.Len(t, ledger.events, 2)
	require.Equal(t, core.CapabilityResponses, ledger.events[0].Key.Capability)
	require.Equal(t, "gpt-5.6-sol", ledger.events[0].Key.Model)
	require.Equal(t, core.CapabilityResponsesCompact, ledger.events[1].Key.Capability)
	require.Equal(t, "gpt-5.6-sol", ledger.events[1].Key.Model)
}

func TestSmartRouterHealthEventsDoNotPersistUpstreamErrorBodies(t *testing.T) {
	ledger := &smartRouterRuntimeTestLedger{}
	svc := &OpenAIGatewayService{cfg: &config.Config{
		Gateway: config.GatewayConfig{SmartRouter: config.GatewaySmartRouterConfig{Enabled: true}},
	}}
	svc.SetSmartRouterHealthLedger(ledger)
	account := smartRouterTestAccount(203, AccountTypeOAuth, nil, nil)
	responseBody := []byte(`{"error":"echoed prompt: create a report; authorization=Bearer sk-test-sensitive; https://provider.invalid/path?api_key=query-secret"}`)
	upstreamErr := &UpstreamFailoverError{
		StatusCode:   503,
		ResponseBody: responseBody,
		Reason:       GatewayFailureReason("upstream_error"),
	}

	svc.ReportSmartRouterTextResult(account, core.CapabilityResponses, "gpt-5.6-sol", nil, upstreamErr, 12)
	svc.ReportSmartRouterCompactResult(account, "gpt-5.6-sol", nil, upstreamErr, 13)
	svc.ReportSmartRouterImageResult(account, &OpenAIImagesRequest{Model: "gpt-image-2"}, nil, upstreamErr, 14)

	require.Len(t, ledger.events, 3)
	for _, event := range ledger.events {
		require.Equal(t, core.FailureUpstream5xx, event.FailureClass, "failure classification must still use the upstream response")
		require.Equal(t, string(core.FailureUpstream5xx), event.ErrorSummary)
		require.NotContains(t, event.ErrorSummary, "echoed prompt")
		require.NotContains(t, event.ErrorSummary, "sk-test-sensitive")
		require.NotContains(t, event.ErrorSummary, "query-secret")
		require.NotContains(t, event.ErrorSummary, "provider.invalid")
	}
}
