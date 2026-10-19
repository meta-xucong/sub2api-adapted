package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/stretchr/testify/require"
)

type smartRouterOrderingLedger struct {
	SmartRouterHealthLedger
	record func(context.Context, core.HealthEvent) error
}

func (l smartRouterOrderingLedger) RecordEvent(ctx context.Context, event core.HealthEvent) error {
	return l.record(ctx, event)
}

func TestSmartRouterPersistenceSinkOrdersSnapshotsAndReleasesOnFailure(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			var events []core.HealthEvent
			tracker := core.NewHealthTracker(core.DefaultHealthPolicy(), time.Now, func(e core.HealthEvent) { events = append(events, e) })
			result := core.RouteResult{LaneID: "same-key", Capability: core.CapabilityChat, Model: "gpt-5.6", StatusCode: 503}
			tracker.Observe(result)
			tracker.Observe(result)
			tracker.Observe(result)
			var recorded []core.HealthEvent
			svc := &OpenAIGatewayService{smartRouterHealthLedger: smartRouterOrderingLedger{record: func(ctx context.Context, e core.HealthEvent) error {
				deadline, ok := ctx.Deadline()
				require.True(t, ok)
				require.Positive(t, time.Until(deadline))
				require.LessOrEqual(t, time.Until(deadline), 3*time.Second)
				recorded = append(recorded, e)
				if len(recorded) == 1 {
					switch outcome {
					case "failure":
						return errors.New("uncertain ledger failure")
					case "panic":
						panic("simulated ledger panic")
					}
				}
				return nil
			}}}
			func() {
				defer func() {
					p := recover()
					if outcome == "panic" {
						require.NotNil(t, p)
					} else {
						require.Nil(t, p)
					}
				}()
				svc.persistSmartRouterHealthEvent(events[1])
			}()
			svc.persistSmartRouterHealthEvent(events[0])
			svc.persistSmartRouterHealthEvent(events[2])
			require.Len(t, recorded, 3)
			require.True(t, recorded[0].ShouldPersistStateProjection())
			require.False(t, recorded[1].ShouldPersistStateProjection())
			require.True(t, recorded[2].ShouldPersistStateProjection())
		})
	}
}
