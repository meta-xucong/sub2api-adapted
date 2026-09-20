package service

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// UnifiedGatewaySchemaReadinessChecker is the narrow startup dependency used
// to prevent a gated recovery worker from querying tables before migrations
// are ready.  The admin repository already implements this contract.
type UnifiedGatewaySchemaReadinessChecker interface {
	CheckSchema(context.Context) (UnifiedGatewaySchemaReadiness, error)
}

// UnifiedGatewayRecoveryRuntime runs the durable settlement reconciler only
// for the isolated unified gateway.  It is deliberately disabled while the
// unified runtime gate is disabled, so existing /v1 traffic never gains a
// dependency on the new recovery tables or worker.
type UnifiedGatewayRecoveryRuntime struct {
	reconciler *UnifiedGatewayReconciler
	cfg        *config.Config
	readiness  UnifiedGatewaySchemaReadinessChecker

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewUnifiedGatewayRecoveryRuntime(reconciler *UnifiedGatewayReconciler, cfg *config.Config, readiness ...UnifiedGatewaySchemaReadinessChecker) *UnifiedGatewayRecoveryRuntime {
	runtime := &UnifiedGatewayRecoveryRuntime{reconciler: reconciler, cfg: cfg}
	if len(readiness) > 0 {
		runtime.readiness = readiness[0]
	}
	return runtime
}

// Start is idempotent.  The provider calls it during application wiring; a
// disabled gate makes this a no-op and therefore preserves legacy startup.
func (r *UnifiedGatewayRecoveryRuntime) Start() {
	if r == nil || r.reconciler == nil || r.cfg == nil || !r.cfg.Gateway.UnifiedGatewayRuntimeEnabled {
		return
	}
	if r.readiness != nil {
		readiness, err := r.readiness.CheckSchema(context.Background())
		if err != nil || !readiness.Ready {
			if err != nil {
				log.Printf("[UnifiedGatewayRecovery] migration readiness check failed; worker remains disabled: %v", err)
			} else {
				log.Printf("[UnifiedGatewayRecovery] migration is not ready; worker remains disabled: %v", strings.Join(readiness.Missing, ", "))
			}
			return
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	r.cancel = cancel
	r.done = done
	go r.run(ctx, done)
}

func (r *UnifiedGatewayRecoveryRuntime) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	r.reconcileOnce(ctx)
	ticker := time.NewTicker(UnifiedGatewayDefaultRecoveryRetryDelay)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reconcileOnce(ctx)
		}
	}
}

func (r *UnifiedGatewayRecoveryRuntime) reconcileOnce(ctx context.Context) {
	if r == nil || r.reconciler == nil {
		return
	}
	report, err := r.reconciler.Reconcile(ctx)
	if err != nil {
		if errors.Is(err, ErrUnifiedGatewayRecoveryUnsupported) {
			return
		}
		log.Printf("[UnifiedGatewayRecovery] reconciliation failed: %v", err)
		return
	}
	if report.Claimed > 0 {
		log.Printf("[UnifiedGatewayRecovery] claimed=%d completed=%d retried=%d preserved=%d released=%d captured=%d", report.Claimed, report.Completed, report.Retried, report.Preserved, report.Released, report.Captured)
	}
}

// Stop is safe before Start and is idempotent after startup.
func (r *UnifiedGatewayRecoveryRuntime) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	cancel := r.cancel
	done := r.done
	r.cancel = nil
	r.done = nil
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (r *UnifiedGatewayRecoveryRuntime) Running() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cancel != nil
}
