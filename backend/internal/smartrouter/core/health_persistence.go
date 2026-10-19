package core

import (
	"context"
	"sync"
	"time"
)

// healthEventPersistence lives with the existing health state, rather than a
// second key registry or a background queue. nextSequence is protected by the
// tracker's mutex; attemptedSequence is protected by gate. Different trackers
// have different tokens, even when they write to the same ledger.
type healthEventPersistence struct {
	gate              chan struct{}
	nextSequence      uint64
	attemptedSequence uint64
}

func newHealthEventPersistence() *healthEventPersistence {
	p := &healthEventPersistence{gate: make(chan struct{}, 1)}
	p.gate <- struct{}{}
	return p
}

// AcquirePersistence orders projection attempts from this tracker and key.
// The caller must use its existing write budget for ctx and defer the returned
// release function around the ledger call. Waiting does not hold the tracker
// mutex. A late older observation still belongs in the event log, but must not
// replace a newer attempted projection. Admission advances the high-water mark
// even if the write fails: a commit error can mean that the newer write committed.
// Legacy/constructed events without an internal token retain their old behavior.
func (e *HealthEvent) AcquirePersistence(ctx context.Context) (func(), error) {
	if e == nil || e.persistence == nil {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p := e.persistence
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.gate:
	}
	// A ready gate and an expired context can both win the select. Do not admit
	// an expired observation or advance its high-water mark in that case.
	if err := ctx.Err(); err != nil {
		p.gate <- struct{}{}
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		p.gate <- struct{}{}
		return nil, context.DeadlineExceeded
	}
	e.skipStateProjection = e.persistenceSequence <= p.attemptedSequence
	if !e.skipStateProjection {
		p.attemptedSequence = e.persistenceSequence
	}
	var once sync.Once
	return func() { once.Do(func() { p.gate <- struct{}{} }) }, nil
}

// ShouldPersistStateProjection lets the ledger retain every admitted event
// while excluding obsolete snapshots from its materialized lane state. The
// decision cannot be set through decoded JSON because its field is private.
func (e HealthEvent) ShouldPersistStateProjection() bool {
	return !e.skipStateProjection
}
