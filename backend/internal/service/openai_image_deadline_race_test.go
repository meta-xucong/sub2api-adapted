//go:build unit

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

// Model the interval after the absolute deadline but before the timer goroutine
// publishes Err. No sleeps or scheduler ordering are needed for this regression.
type openAIImageLaggingDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c openAIImageLaggingDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }

func TestOpenAIImageRequestContextErrChecksAbsoluteDeadline(t *testing.T) {
	expired := openAIImageLaggingDeadlineContext{Context: context.Background(), deadline: time.Now().Add(-time.Hour)}
	require.NoError(t, expired.Err(), "timer delivery is deliberately pending")
	require.ErrorIs(t, OpenAIImageRequestContextErr(expired), context.DeadlineExceeded)
	future := openAIImageLaggingDeadlineContext{Context: context.Background(), deadline: time.Now().Add(time.Hour)}
	require.NoError(t, OpenAIImageRequestContextErr(future))
	require.NoError(t, OpenAIImageRequestContextErr(context.Background()))
	//nolint:staticcheck // SA1012: Deliberately test the helper's supported nil-context boundary.
	require.NoError(t, OpenAIImageRequestContextErr(nil))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired.Context = canceled
	require.ErrorIs(t, OpenAIImageRequestContextErr(expired), context.Canceled)
}

func TestOpenAIImageAttemptTimeoutDoesNotRetryExpiredRequestBeforeTimerDelivery(t *testing.T) {
	attempt, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer cancel()
	require.ErrorIs(t, attempt.Err(), context.DeadlineExceeded)
	request := openAIImageLaggingDeadlineContext{Context: context.Background(), deadline: time.Now().Add(-time.Hour)}
	require.NoError(t, request.Err())
	require.False(t, isOpenAIImageAttemptTimeout(context.DeadlineExceeded, attempt, request))
	request.deadline = time.Now().Add(time.Hour)
	require.True(t, isOpenAIImageAttemptTimeout(context.DeadlineExceeded, attempt, request), "an independent attempt timeout with remaining request budget stays retryable")
	require.True(t, isOpenAIImageAttemptTimeout(context.DeadlineExceeded, attempt, context.Background()))
}

func TestSmartRouterImageRequestBudgetDoesNotPenalize(t *testing.T) {
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer cancelExpired()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	future := openAIImageLaggingDeadlineContext{Context: context.Background(), deadline: time.Now().Add(time.Hour)}
	for _, tc := range []struct {
		name       string
		requestCtx context.Context
		err        error
		penalize   bool
	}{
		{"request_deadline", expired, context.DeadlineExceeded, false},
		{"request_deadline_before_timer", openAIImageLaggingDeadlineContext{Context: context.Background(), deadline: time.Now().Add(-time.Hour)}, errors.New("upstream request failed: context deadline exceeded"), false},
		{"caller_canceled", canceled, errors.New("image stream incomplete after timeout"), false},
		{"independent_attempt_timeout", future, context.DeadlineExceeded, true},
		{"independent_attempt_failover", future, newOpenAIImageAttemptTimeoutFailover(nil), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ledger := &smartRouterRuntimeTestLedger{}
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{SmartRouter: config.GatewaySmartRouterConfig{Enabled: true}}}}
			svc.SetSmartRouterHealthLedger(ledger)
			account := smartRouterTestAccount(206, AccountTypeOAuth, nil, nil)
			parsed := &OpenAIImagesRequest{Model: "gpt-image-2"}
			lane := core.LaneSnapshot{LaneID: "account:206"}
			before := svc.smartRouterHealth().Snapshot(lane, core.CapabilityImageGeneration, parsed.Model, time.Now().Unix())
			svc.ReportSmartRouterImageResult(tc.requestCtx, account, parsed, nil, tc.err, 20)
			if tc.penalize {
				require.Len(t, ledger.events, 1)
				require.Equal(t, core.FailureTimeout, ledger.events[0].FailureClass)
				require.Positive(t, ledger.events[0].HealthPenalty)
				require.Positive(t, ledger.events[0].ConsecutiveFailures)
				return
			}
			after := svc.smartRouterHealth().Snapshot(lane, core.CapabilityImageGeneration, parsed.Model, time.Now().Unix())
			require.Equal(t, before, after)
			require.Empty(t, ledger.events)

			// Calibration is independently owned and must still record its timeout.
			svc.ReportSmartRouterImageCalibrationResult(account, parsed, nil, context.DeadlineExceeded, 20)
			require.Len(t, ledger.events, 1)
			require.Equal(t, "calibration", ledger.events[0].Source)
			require.Equal(t, core.FailureTimeout, ledger.events[0].FailureClass)
			require.Positive(t, ledger.events[0].HealthPenalty)
		})
	}
}
