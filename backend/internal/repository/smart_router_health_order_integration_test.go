//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/stretchr/testify/require"
)

func TestSmartRouterHealthPostgresLateEventPreservesNewerProjection(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, ApplyMigrations(ctx, integrationDB))
	for _, firstSuccess := range []bool{false, true} {
		t.Run(fmt.Sprintf("first_success=%t", firstSuccess), func(t *testing.T) {
			laneID := fmt.Sprintf("phase3-reordered-health-%d", time.Now().UnixNano())
			t.Cleanup(func() {
				_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM smart_router_health_events WHERE lane_id=$1", laneID)
				_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM smart_router_lane_state WHERE lane_id=$1", laneID)
			})
			now := time.Now().UTC().Truncate(time.Second)
			evidenceAt := now
			var events []smartrouter.HealthEvent
			tracker := smartrouter.NewHealthTracker(smartrouter.DefaultHealthPolicy(), func() time.Time { return now }, func(e smartrouter.HealthEvent) { events = append(events, e) })
			result := smartrouter.RouteResult{LaneID: laneID, AccountID: 880022, SourceGroup: "isolated-integration-fixture", Capability: smartrouter.CapabilityChat, Model: "gpt-5.6", StatusCode: 503, ErrorClass: smartrouter.FailureUpstream5xx, Success: firstSuccess}
			tracker.Observe(result)
			result.Success = !firstSuccess
			if result.Success {
				result.StatusCode = 200
				result.ErrorClass = ""
				result.Source = "calibration"
			}
			expected := tracker.Observe(result)
			require.Len(t, events, 2)
			require.Equal(t, events[0].OccurredAtUnix, events[1].OccurredAtUnix)
			repo := NewSmartRouterHealthRepository(integrationDB)
			persist := func(index int, wantProjection bool) {
				writeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				defer cancel()
				event := events[index]
				release, err := event.AcquirePersistence(writeCtx)
				require.NoError(t, err)
				defer release()
				require.Equal(t, wantProjection, event.ShouldPersistStateProjection())
				require.NoError(t, repo.RecordEvent(writeCtx, event))
			}
			// This is the production sink contract with deliberately reversed arrival.
			persist(1, true)
			var updatedAtBefore time.Time
			require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT updated_at FROM smart_router_lane_state WHERE lane_id=$1", laneID).Scan(&updatedAtBefore))
			persist(0, false)
			var total, successes, failures int
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE success), COUNT(*) FILTER (WHERE NOT success) FROM smart_router_health_events WHERE lane_id=$1`, laneID).Scan(&total, &successes, &failures))
			require.Equal(t, 2, total)
			require.Equal(t, 1, successes)
			require.Equal(t, 1, failures)
			var penalty, consecutiveFailures, consecutiveSuccesses, priority int
			var cooldown, lastSuccess, lastFailure sql.NullTime
			var ewma float64
			var lastFailureUnix int64
			var updatedAt time.Time
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT health_penalty, consecutive_failures, consecutive_successes, recovery_priority, cooldown_until, error_rate_ewma, last_success_at, last_failure_at, last_failure_unix, updated_at FROM smart_router_lane_state WHERE lane_id=$1 AND capability=$2 AND model_family=$3`, laneID, smartrouter.CapabilityChat, "gpt-5.6").Scan(&penalty, &consecutiveFailures, &consecutiveSuccesses, &priority, &cooldown, &ewma, &lastSuccess, &lastFailure, &lastFailureUnix, &updatedAt))
			require.Equal(t, expected.HealthPenalty, penalty)
			require.Equal(t, expected.ConsecutiveFailures, consecutiveFailures)
			require.Equal(t, expected.ConsecutiveSuccesses, consecutiveSuccesses)
			require.Equal(t, expected.RecoveryPriority, priority)
			require.Equal(t, expected.CooldownUntilUnix > 0, cooldown.Valid)
			if cooldown.Valid {
				require.Equal(t, expected.CooldownUntilUnix, cooldown.Time.Unix())
			}
			require.InDelta(t, expected.ErrorRateEWMA, ewma, 1e-12)
			require.True(t, lastSuccess.Valid)
			require.Equal(t, evidenceAt, lastSuccess.Time.UTC())
			require.True(t, lastFailure.Valid)
			require.Equal(t, evidenceAt, lastFailure.Time.UTC())
			require.Equal(t, updatedAtBefore, updatedAt, "late evidence must not masquerade as a fresh health projection")
			if result.Success {
				require.Zero(t, lastFailureUnix)
			} else {
				require.Equal(t, evidenceAt.Unix(), lastFailureUnix)
			}
			// A subsequent current projection with a rolled-back wall clock must not
			// regress either independent success/failure timestamp.
			now = now.Add(-time.Hour)
			tracker.Observe(result)
			persist(2, true)
			require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT last_success_at, last_failure_at FROM smart_router_lane_state WHERE lane_id=$1", laneID).Scan(&lastSuccess, &lastFailure))
			require.Equal(t, evidenceAt, lastSuccess.Time.UTC())
			require.Equal(t, evidenceAt, lastFailure.Time.UTC())
		})
	}
}
