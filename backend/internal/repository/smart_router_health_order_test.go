package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/stretchr/testify/require"
)

func TestSmartRouterHealthRepositoryLateEventKeepsLogOnly(t *testing.T) {
	for _, firstSuccess := range []bool{false, true} {
		for _, uncertain := range []bool{false, true} {
			name := "confirmed_commit"
			if uncertain {
				name = "uncertain_commit"
			}
			if firstSuccess {
				name += "_success_first"
			}
			t.Run(name, func(t *testing.T) {
				db, mock, err := sqlmock.New()
				require.NoError(t, err)
				defer func() { _ = db.Close() }()
				var events []smartrouter.HealthEvent
				tracker := smartrouter.NewHealthTracker(smartrouter.DefaultHealthPolicy(), time.Now, func(e smartrouter.HealthEvent) { events = append(events, e) })
				result := smartrouter.RouteResult{LaneID: "same-key", Capability: smartrouter.CapabilityChat, Model: "gpt-5.6", StatusCode: 503}
				result.Success = firstSuccess
				tracker.Observe(result)
				result.Success = !firstSuccess
				if result.Success {
					result.Source = "calibration"
					result.StatusCode = 200
				}
				tracker.Observe(result)
				repo := NewSmartRouterHealthRepository(db)
				mock.ExpectBegin()
				mock.ExpectExec("INSERT INTO smart_router_health_events").WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectExec("INSERT INTO smart_router_lane_state").WillReturnResult(sqlmock.NewResult(1, 1))
				commit := mock.ExpectCommit()
				if uncertain {
					commit.WillReturnError(errors.New("commit outcome unknown"))
				}
				func() {
					release, err := events[1].AcquirePersistence(context.Background())
					require.NoError(t, err)
					defer release()
					err = repo.RecordEvent(context.Background(), events[1])
					if uncertain {
						require.ErrorContains(t, err, "commit outcome unknown")
					} else {
						require.NoError(t, err)
					}
				}()
				mock.ExpectBegin()
				mock.ExpectExec("INSERT INTO smart_router_health_events").WillReturnResult(sqlmock.NewResult(2, 1))
				// Preserve independent evidence, without a health-state UPSERT, including after uncertain commit.
				mock.ExpectExec("UPDATE smart_router_lane_state SET").WithArgs(events[0].Key.LaneID, events[0].Key.Capability, events[0].Key.Model, firstSuccess, time.Unix(events[0].OccurredAtUnix, 0).UTC()).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
				release, err := events[0].AcquirePersistence(context.Background())
				require.NoError(t, err)
				defer release()
				require.False(t, events[0].ShouldPersistStateProjection())
				require.NoError(t, repo.RecordEvent(context.Background(), events[0]))
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}
