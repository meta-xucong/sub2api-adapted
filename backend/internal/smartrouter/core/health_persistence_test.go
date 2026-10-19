package core

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func persistenceTestResult() RouteResult {
	return RouteResult{LaneID: "account:1", AccountID: 1, Capability: CapabilityChat, Model: "gpt-5.6", BasePriority: 1, StatusCode: 503, ErrorClass: FailureUpstream5xx}
}

func capturePersistenceTestEvents(count int) (*HealthTracker, []HealthEvent) {
	var events []HealthEvent
	tracker := NewHealthTracker(DefaultHealthPolicy(), func() time.Time { return time.Unix(1802860000, 0) }, func(e HealthEvent) { events = append(events, e) })
	for i := 0; i < count; i++ {
		tracker.Observe(persistenceTestResult())
	}
	return tracker, events
}

func TestHealthPersistenceKeepsNewerProjectionAndAllEvents(t *testing.T) {
	for _, delta := range []time.Duration{0, -time.Hour} {
		t.Run(delta.String(), func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(1802860000)
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var mu sync.Mutex
			var ledger []HealthEvent
			var projection HealthEvent
			sink := func(e HealthEvent) {
				if !e.Success {
					close(entered)
					<-release
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				unlock, err := e.AcquirePersistence(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				defer unlock()
				mu.Lock()
				defer mu.Unlock()
				ledger = append(ledger, e)
				if e.ShouldPersistStateProjection() {
					projection = e
				}
			}
			tracker := NewHealthTracker(DefaultHealthPolicy(), func() time.Time { return time.Unix(clock.Load(), 0) }, sink)
			go func() { defer close(done); tracker.Observe(persistenceTestResult()) }()
			<-entered
			clock.Add(int64(delta / time.Second))
			success := persistenceTestResult()
			success.Success = true
			success.StatusCode = 200
			success.Source = "calibration"
			success.ErrorClass = ""
			tracker.Observe(success)
			close(release)
			<-done
			mu.Lock()
			defer mu.Unlock()
			if len(ledger) != 2 || !ledger[0].Success || ledger[1].Success {
				t.Fatalf("lost or misidentified events: %+v", ledger)
			}
			if ledger[1].ShouldPersistStateProjection() {
				t.Fatal("late old event can overwrite newer projection")
			}
			if !projection.Success || projection.HealthPenalty != 0 || projection.CooldownUntilUnix != 0 {
				t.Fatalf("stale projection: %+v", projection)
			}
			if ledger[0].OccurredAtUnix > ledger[1].OccurredAtUnix {
				t.Fatal("fixture did not exercise equal or reverse clock")
			}
		})
	}
}

func TestHealthPersistenceWaitUsesCallerBudget(t *testing.T) {
	_, events := capturePersistenceTestEvents(2)
	unlock, err := events[0].AcquirePersistence(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if release, err := events[1].AcquirePersistence(ctx); !errors.Is(err, context.DeadlineExceeded) || release != nil {
		t.Fatalf("wait result: release=%v err=%v", release != nil, err)
	}
	unlock()
	// Expiry while the gate is ready must not advance the sequence either.
	for i := 0; i < 20; i++ {
		if release, err := events[1].AcquirePersistence(ctx); !errors.Is(err, context.DeadlineExceeded) || release != nil {
			t.Fatalf("expired admission: %v", err)
		}
	}
	release, err := events[1].AcquirePersistence(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if !events[1].ShouldPersistStateProjection() {
		t.Fatal("expired attempt advanced high-water mark")
	}
}

func TestHealthPersistenceAttemptFenceSurvivesWriteFailureAndPanic(t *testing.T) {
	for _, outcome := range []string{"write_failure", "commit_uncertain", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			_, events := capturePersistenceTestEvents(3)
			func() {
				defer func() {
					p := recover()
					if outcome == "panic" && p == nil {
						t.Error("missing simulated panic")
					} else if outcome != "panic" && p != nil {
						t.Errorf("unexpected panic: %v", p)
					}
				}()
				unlock, err := events[1].AcquirePersistence(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer unlock()
				if !events[1].ShouldPersistStateProjection() {
					t.Fatal("newer attempt rejected")
				}
				// A failed write or uncertain commit intentionally has no success callback.
				if outcome == "panic" {
					panic("simulated ledger panic")
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			unlock, err := events[0].AcquirePersistence(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if events[0].ShouldPersistStateProjection() {
				t.Fatal("older event may overwrite a failed/uncertain newer attempt")
			}
			unlock()
			unlock, err = events[2].AcquirePersistence(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			if !events[2].ShouldPersistStateProjection() {
				t.Fatal("future event cannot recover persistence")
			}
		})
	}
}

func TestHealthPersistenceBlockedSinkDoesNotBlockOtherKeyOrSnapshot(t *testing.T) {
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	tracker := NewHealthTracker(DefaultHealthPolicy(), time.Now, func(e HealthEvent) {
		unlock, err := e.AcquirePersistence(context.Background())
		if err != nil {
			t.Error(err)
			return
		}
		defer unlock()
		if e.Key.LaneID == "account:1" {
			close(held)
			<-release
		}
	})
	go func() { defer close(done); tracker.Observe(persistenceTestResult()) }()
	<-held
	checked := make(chan struct{})
	go func() {
		other := persistenceTestResult()
		other.LaneID = "account:2"
		tracker.Observe(other)
		tracker.Snapshot(LaneSnapshot{LaneID: "account:1"}, CapabilityChat, "gpt-5.6", time.Now().Unix())
		close(checked)
	}()
	select {
	case <-checked:
	case <-time.After(time.Second):
		close(release)
		<-done
		t.Fatal("other key or snapshot blocked by persistence")
	}
	close(release)
	<-done
}

func TestHealthPersistenceRestorePreservesTokenAndSequence(t *testing.T) {
	tracker, events := capturePersistenceTestEvents(1)
	first := events[0]
	tracker.Restore(first.Key, HealthSnapshot{HealthScore: 1, RecoveryStage: RecoveryNormal}, 0)
	tracker.Observe(persistenceTestResult())
	latest := tracker.EventsSince(0)[1]
	if latest.persistence != first.persistence || latest.persistenceSequence <= first.persistenceSequence {
		t.Fatal("Restore reset publication identity/sequence")
	}
	unlock, err := latest.AcquirePersistence(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	unlock, err = first.AcquirePersistence(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if first.ShouldPersistStateProjection() {
		t.Fatal("pre-Restore event overwrites newer projection")
	}
}

func TestHealthPersistenceTrackersAndLegacyEventsStayIndependent(t *testing.T) {
	_, a := capturePersistenceTestEvents(2)
	_, b := capturePersistenceTestEvents(1)
	if a[0].persistence == b[0].persistence {
		t.Fatal("independent trackers share an ordering token")
	}
	unlock, err := a[1].AcquirePersistence(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// A second tracker for the same key must not wait on the first tracker's gate.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	otherUnlock, err := b[0].AcquirePersistence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !b[0].ShouldPersistStateProjection() {
		t.Fatal("one tracker suppresses another")
	}
	otherUnlock()
	unlock()
	legacy := HealthEvent{}
	if err := json.Unmarshal([]byte(`{"skipStateProjection":true,"SkipStateProjection":true,"persistenceSequence":99}`), &legacy); err != nil {
		t.Fatal(err)
	}
	unlock, err = legacy.AcquirePersistence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if !legacy.ShouldPersistStateProjection() {
		t.Fatal("legacy/JSON event changed projection semantics")
	}
	data, err := json.Marshal(a[1])
	if err != nil {
		t.Fatal(err)
	}
	var decoded HealthEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.persistence != nil || decoded.persistenceSequence != 0 || !decoded.ShouldPersistStateProjection() {
		t.Fatal("ordering metadata was serialized")
	}
}

type healthPersistenceLaggingDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c healthPersistenceLaggingDeadlineContext) Deadline() (time.Time, bool) {
	return c.deadline, true
}

func TestHealthPersistenceRejectsExpiredAbsoluteDeadlineBeforeErr(t *testing.T) {
	_, events := capturePersistenceTestEvents(1)
	ctx := healthPersistenceLaggingDeadlineContext{Context: context.Background(), deadline: time.Now().Add(-time.Hour)}
	if ctx.Err() != nil {
		t.Fatal("fixture must not publish Err")
	}
	release, err := events[0].AcquirePersistence(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || release != nil {
		t.Fatalf("expired absolute deadline admitted: %v", err)
	}
	release, err = events[0].AcquirePersistence(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if !events[0].ShouldPersistStateProjection() {
		t.Fatal("expired admission advanced high-water mark")
	}
}
