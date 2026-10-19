//go:build integration

package core_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	core "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/stretchr/testify/require"
)

func TestSmartRouterImageRestoreThroughPostgresLedgerInChildProcess(t *testing.T) {
	dsn := os.Getenv("SUB2API_SMART_ROUTER_IMAGE_RESTORE_DSN")
	if dsn == "" {
		t.Skip("this image probe is run by the parent repository integration test")
	}
	var events []core.HealthEvent
	require.NoError(t, json.Unmarshal([]byte(os.Getenv("SUB2API_SMART_ROUTER_IMAGE_RESTORE_EVENTS")), &events))
	require.Len(t, events, 6)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	require.NoError(t, db.Ping())
	ledger := repository.NewSmartRouterHealthRepository(db)
	states, err := ledger.LoadStates(context.Background())
	require.NoError(t, err)
	initial := make(map[core.HealthKey]service.SmartRouterHealthState)
	for _, state := range states {
		initial[core.NewHealthKey(state.LaneID, state.Capability, state.ModelFamily)] = state
	}
	for _, event := range events {
		state, exists := initial[event.Key]
		require.True(t, exists, "exact persisted image key: %+v", event.Key)
		require.Equal(t, event.AccountID, state.AccountID)
		require.Equal(t, event.SourceGroup, state.SourceGroup)
		require.Equal(t, event.RecoveryStage, state.Snapshot.RecoveryStage)
		require.Equal(t, event.CooldownUntilUnix, state.Snapshot.CooldownUntilUnix)
		require.Equal(t, event.ConsecutiveFailures, state.Snapshot.ConsecutiveFailures)
		require.Equal(t, event.ConsecutiveSuccesses, state.Snapshot.ConsecutiveSuccesses)
		require.Equal(t, event.RecoveryPriority, state.Snapshot.RecoveryPriority)
		require.InDelta(t, event.HealthScore, state.Snapshot.HealthScore, .00001)
		require.InDelta(t, event.ErrorRateEWMA, state.Snapshot.ErrorRateEWMA, .00001)
		if !event.Success {
			require.Equal(t, event.OccurredAtUnix, state.LastFailureUnix)
			assertImagePostgresDeadlineBoundary(t, event.Key, state)
		}
	}

	// Use the same production constructor and lazy ledger attachment as the
	// compact restore probe. No service internals or provider calls are needed.
	cfg := &config.Config{}
	cfg.Gateway.SmartRouter.Enabled = true
	svc := service.NewOpenAIGatewayService(
		nil, nil, nil, nil, nil, nil, nil, cfg,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	svc.SetSmartRouterHealthLedger(ledger)
	for i := 0; i < len(events); i += 3 {
		cooling, unavailable, healthy := events[i], events[i+1], events[i+2]
		t.Run(string(cooling.Key.Capability), func(t *testing.T) {
			account := imagePostgresRestoreAccount(t, cooling)
			otherAccount := imagePostgresRestoreAccount(t, healthy)
			edit := cooling.Key.Capability == core.CapabilityImageEdit
			deadline := time.Now().Add(time.Minute)
			ctx, cancel := context.WithDeadline(service.WithSmartRouterImageRequest(context.Background(), cooling.Key.Model, edit), deadline)
			defer cancel()
			parsed := &service.OpenAIImagesRequest{Model: "client-alias-must-not-be-persisted"}

			_, release, err := svc.AcquireSmartRouterImageAttempt(ctx, account, parsed)
			assertImagePostgresUnavailable(t, err, cooling.CooldownUntilUnix)
			release()
			_, release, err = svc.AcquireSmartRouterImageAttempt(ctx, otherAccount, parsed)
			require.NoError(t, err, "same custom lane on another account remains healthy")
			release()
			otherModelCtx := service.WithSmartRouterImageRequest(context.Background(), cooling.Key.Model+"-unseen", edit)
			_, release, err = svc.AcquireSmartRouterImageAttempt(otherModelCtx, account, parsed)
			require.NoError(t, err, "another exact image model must not inherit a restored cooldown")
			release()
			unavailableCtx := service.WithSmartRouterImageRequest(context.Background(), unavailable.Key.Model, edit)
			for _, lastResort := range []bool{false, true} {
				_, release, err = svc.AcquireSmartRouterImageAttempt(unavailableCtx, account, parsed, lastResort)
				assertImagePostgresUnavailable(t, err, unavailable.CooldownUntilUnix)
				release()
			}

			_, release, err = svc.AcquireSmartRouterImageAttempt(ctx, account, parsed, true)
			require.NoError(t, err, "restored Cooling admits an explicit LastResort probe")
			_, secondRelease, err := svc.AcquireSmartRouterImageAttempt(ctx, account, parsed, true)
			assertImagePostgresUnavailable(t, err, deadline.Unix())
			secondRelease()
			release()
			release() // Cancellation/release is idempotent and cannot clear cooldown.
			_, release, err = svc.AcquireSmartRouterImageAttempt(ctx, account, parsed)
			assertImagePostgresUnavailable(t, err, cooling.CooldownUntilUnix)
			release()

			attempt, release, err := svc.AcquireSmartRouterImageAttempt(ctx, account, parsed, true)
			require.NoError(t, err)
			svc.ReportSmartRouterImageResult(attempt, account, parsed, nil,
				&service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable}, 150)
			release()
			updated, err := ledger.LoadStates(context.Background())
			require.NoError(t, err)
			byKey := make(map[core.HealthKey]service.SmartRouterHealthState)
			for _, state := range updated {
				byKey[core.NewHealthKey(state.LaneID, state.Capability, state.ModelFamily)] = state
			}
			got := byKey[cooling.Key]
			require.Equal(t, cooling.ConsecutiveFailures+1, got.Snapshot.ConsecutiveFailures,
				"fresh-process observation must extend restored state through the service loader")
			require.Equal(t, cooling.AccountID, got.AccountID)
			require.Equal(t, cooling.SourceGroup, got.SourceGroup)
			require.Equal(t, initial[unavailable.Key], byKey[unavailable.Key], "other exact model is unchanged")
			require.Equal(t, initial[healthy.Key], byKey[healthy.Key], "other account is unchanged")
			_, aliasWritten := byKey[core.NewHealthKey(cooling.Key.LaneID, cooling.Key.Capability, parsed.Model)]
			require.False(t, aliasWritten, "persist the selected exact model, not a client/billing alias")
		})
	}
	t.Log("fresh-process image repository state restored with admission boundaries")
}

func imagePostgresRestoreAccount(t *testing.T, event core.HealthEvent) *service.Account {
	t.Helper()
	parts := strings.SplitN(event.Key.LaneID, "/image/", 2)
	require.Len(t, parts, 2)
	return &service.Account{
		ID: event.AccountID, Name: "pr13-image-restore", Platform: service.PlatformOpenAI,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Extra: map[string]any{"smart_router": map[string]any{
			"lane_id": parts[1], "source_group": event.SourceGroup,
		}},
	}
}

func assertImagePostgresUnavailable(t *testing.T, err error, retryAt int64) {
	t.Helper()
	var unavailable *service.SmartRouterImageUnavailableError
	require.ErrorAs(t, err, &unavailable)
	require.Equal(t, retryAt, unavailable.RetryAtUnix)
}

func assertImagePostgresDeadlineBoundary(t *testing.T, key core.HealthKey, state service.SmartRouterHealthState) {
	t.Helper()
	cutoff := state.Snapshot.CooldownUntilUnix
	tracker := core.NewHealthTracker(core.DefaultHealthPolicy(), nil, nil)
	tracker.RestoreWithSourceGroup(key, state.Snapshot, state.LastFailureUnix, state.SourceGroup)
	require.False(t, tracker.ImageAdmission(key, cutoff-1).Allowed)
	require.Equal(t, state.Snapshot.RecoveryStage == core.RecoveryCooling,
		tracker.ImageLastResortAdmission(key, cutoff-1).Allowed)
	admission, release := tracker.AcquireImageAdmission(key, cutoff, cutoff+5)
	require.True(t, admission.Allowed, "the persisted cooldown is exclusive at its deadline")
	require.NotZero(t, admission.ProbeToken, "expiry requires one local recovery probe")
	require.False(t, tracker.ImageAdmission(key, cutoff+4).Allowed)
	require.False(t, tracker.ImageLastResortAdmission(key, cutoff+4).Allowed)
	require.True(t, tracker.ImageAdmission(key, cutoff+5).Allowed, "local lease is bounded at its deadline")
	release()
	snapshot := tracker.Snapshot(core.LaneSnapshot{LaneID: key.LaneID}, key.Capability, key.Model, cutoff+5)
	require.Equal(t, core.RecoveryProbeDue, snapshot.RecoveryStage)
	require.Zero(t, snapshot.CooldownUntilUnix)
}
