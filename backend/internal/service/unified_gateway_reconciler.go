package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	UnifiedGatewayRecoverySchemaVersion     = "unified_gateway_recovery_v1"
	UnifiedGatewayDefaultRecoveryBatchSize  = 100
	UnifiedGatewayDefaultRecoveryStaleAfter = 24 * time.Hour
	UnifiedGatewayDefaultRecoveryRetryDelay = time.Minute
)

var ErrUnifiedGatewayRecoveryUnsupported = errors.New("unified gateway recovery is unsupported")
var ErrUnifiedGatewayRecoveryLeaseLost = errors.New("unified gateway recovery lease lost")

// UnifiedGatewayRecoveryTask is the durable work item created from a
// snapshot. ReservationDigest is the SHA-256 form used by the ledger table;
// the raw reservation key is reconstructed from the request identity so a NUL
// separator never has to be stored in PostgreSQL text.
type UnifiedGatewayRecoveryTask struct {
	ID                int64
	APIKeyID          int64
	UserID            int64
	AccessGroupID     int64
	RequestID         string
	AttemptID         string
	ReservationDigest string
	SnapshotStatus    UnifiedGatewaySnapshotStatus
	UserCharge        float64
	Attempts          int
}

// UnifiedGatewayRecoveryStore owns the durable discovery queue and its
// lease. A processing task is safe to reclaim after its lease expires.
type UnifiedGatewayRecoveryStore interface {
	DiscoverRecoveryTasks(ctx context.Context, limit int) error
	ClaimRecoveryTasks(ctx context.Context, limit int, now time.Time) ([]UnifiedGatewayRecoveryTask, error)
	CompleteRecoveryTask(ctx context.Context, taskID int64, attempts int) error
	RetryRecoveryTask(ctx context.Context, taskID int64, attempts int, nextAttemptAt time.Time, lastError string) error
}

// UnifiedGatewaySnapshotRecoveryStore performs a guarded, idempotent update
// used only by the recovery worker. It accepts terminal-state repairs because
// a crash may leave the ledger and snapshot in different terminal states.
type UnifiedGatewaySnapshotRecoveryStore interface {
	RecoverSnapshot(ctx context.Context, apiKeyID, userID, accessGroupID int64, requestID, attemptID string, expectedStatus, status UnifiedGatewaySnapshotStatus, measuredUnits, userCharge float64, upstreamRequestID string, responseBody []byte, failureMessage string) error
}

// UnifiedGatewayLedgerState is a read-only view used before a recovery side
// effect. The normal ledger methods remain the only methods that mutate money.
type UnifiedGatewayLedgerState struct {
	Exists         bool
	UserID         int64
	ReservedAmount float64
	CapturedAmount float64
	Status         string
}

type UnifiedGatewayLedgerInspector interface {
	Inspect(ctx context.Context, key string, userID int64) (UnifiedGatewayLedgerState, error)
}

type UnifiedGatewayReconcilerOptions struct {
	BatchSize  int
	StaleAfter time.Duration
	RetryDelay time.Duration
	Now        func() time.Time
}

type UnifiedGatewayRecoveryReport struct {
	Claimed   int
	Completed int
	Retried   int
	Preserved int
	Released  int
	Captured  int
}

type UnifiedGatewayReconciler struct {
	snapshots        UnifiedGatewayPriceSnapshotStore
	ledger           UnifiedGatewayChargeLedger
	recovery         UnifiedGatewayRecoveryStore
	snapshotRecovery UnifiedGatewaySnapshotRecoveryStore
	ledgerInspector  UnifiedGatewayLedgerInspector
	batchSize        int
	staleAfter       time.Duration
	retryDelay       time.Duration
	now              func() time.Time
}

// NewUnifiedGatewayReconciler discovers the optional recovery interfaces from
// the production snapshot and ledger repositories. The legacy in-memory
// implementations remain valid because they simply do not opt into recovery.
func NewUnifiedGatewayReconciler(snapshots UnifiedGatewayPriceSnapshotStore, ledger UnifiedGatewayChargeLedger, options UnifiedGatewayReconcilerOptions) *UnifiedGatewayReconciler {
	reconciler := &UnifiedGatewayReconciler{
		snapshots:  snapshots,
		ledger:     ledger,
		batchSize:  options.BatchSize,
		staleAfter: options.StaleAfter,
		retryDelay: options.RetryDelay,
		now:        options.Now,
	}
	if recovery, ok := snapshots.(UnifiedGatewayRecoveryStore); ok {
		reconciler.recovery = recovery
	}
	if snapshotRecovery, ok := snapshots.(UnifiedGatewaySnapshotRecoveryStore); ok {
		reconciler.snapshotRecovery = snapshotRecovery
	}
	if ledgerInspector, ok := ledger.(UnifiedGatewayLedgerInspector); ok {
		reconciler.ledgerInspector = ledgerInspector
	}
	if reconciler.batchSize <= 0 {
		reconciler.batchSize = UnifiedGatewayDefaultRecoveryBatchSize
	}
	if reconciler.staleAfter <= 0 {
		reconciler.staleAfter = UnifiedGatewayDefaultRecoveryStaleAfter
	}
	if reconciler.retryDelay <= 0 {
		reconciler.retryDelay = UnifiedGatewayDefaultRecoveryRetryDelay
	}
	if reconciler.now == nil {
		reconciler.now = func() time.Time { return time.Now().UTC() }
	}
	return reconciler
}

// UnifiedGatewayReservationKey is the same deterministic key used by the
// gateway before the ledger hashes it. Keeping it exported here lets the SQL
// recovery repository derive the exact key without changing the legacy
// gateway implementation.
func UnifiedGatewayReservationKey(apiKeyID, userID, accessGroupID int64, requestID, attemptID string) string {
	return fmt.Sprintf("%d:%d:%d:%s\x00%s", apiKeyID, userID, accessGroupID, strings.TrimSpace(requestID), strings.TrimSpace(attemptID))
}

// Reconcile performs one bounded, restart-safe pass. It first discovers
// snapshots that predate the worker, then claims durable tasks. A task stays
// pending while a reserved/pending request is still within the grace period;
// after that period the hold is released and the snapshot is zero-charged.
func (r *UnifiedGatewayReconciler) Reconcile(ctx context.Context) (UnifiedGatewayRecoveryReport, error) {
	var report UnifiedGatewayRecoveryReport
	if r == nil || r.snapshots == nil || r.ledger == nil || r.recovery == nil || r.snapshotRecovery == nil || r.ledgerInspector == nil {
		return report, ErrUnifiedGatewayRecoveryUnsupported
	}
	now := r.now().UTC()
	if err := r.recovery.DiscoverRecoveryTasks(ctx, r.batchSize); err != nil {
		return report, err
	}
	tasks, err := r.recovery.ClaimRecoveryTasks(ctx, r.batchSize, now)
	if err != nil {
		return report, err
	}
	report.Claimed = len(tasks)
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		outcome, processErr := r.reconcileTask(ctx, task, now)
		if processErr != nil {
			report.Retried++
			if retryErr := r.recovery.RetryRecoveryTask(ctx, task.ID, task.Attempts, now.Add(r.retryDelay), recoveryErrorMessage(processErr)); retryErr != nil && !errors.Is(retryErr, ErrUnifiedGatewayRecoveryLeaseLost) {
				return report, errors.Join(processErr, retryErr)
			}
			continue
		}
		if outcome.preserved {
			report.Preserved++
			report.Retried++
			if retryErr := r.recovery.RetryRecoveryTask(ctx, task.ID, task.Attempts, now.Add(r.retryDelay), "reservation retained while request is still within recovery grace period"); retryErr != nil && !errors.Is(retryErr, ErrUnifiedGatewayRecoveryLeaseLost) {
				return report, retryErr
			}
			continue
		}
		if err := r.recovery.CompleteRecoveryTask(ctx, task.ID, task.Attempts); err != nil {
			if errors.Is(err, ErrUnifiedGatewayRecoveryLeaseLost) {
				continue
			}
			return report, err
		}
		report.Completed++
		if outcome.released {
			report.Released++
		}
		if outcome.captured {
			report.Captured++
		}
	}
	return report, nil
}

type unifiedGatewayRecoveryOutcome struct {
	preserved bool
	released  bool
	captured  bool
}

func (r *UnifiedGatewayReconciler) reconcileTask(ctx context.Context, task UnifiedGatewayRecoveryTask, now time.Time) (unifiedGatewayRecoveryOutcome, error) {
	record, err := r.snapshots.Get(ctx, task.APIKeyID, task.UserID, task.AccessGroupID, task.RequestID, task.AttemptID)
	if errors.Is(err, ErrUnifiedGatewaySnapshotNotFound) {
		return r.reconcileMissingSnapshot(ctx, task)
	}
	if err != nil {
		return unifiedGatewayRecoveryOutcome{}, err
	}
	return r.reconcileRecord(ctx, record, now)
}

func (r *UnifiedGatewayReconciler) reconcileRecord(ctx context.Context, record *UnifiedGatewayPriceSnapshotRecord, now time.Time) (unifiedGatewayRecoveryOutcome, error) {
	if record == nil {
		return unifiedGatewayRecoveryOutcome{}, ErrUnifiedGatewaySnapshotNotFound
	}
	key := UnifiedGatewayReservationKey(record.APIKeyID, record.UserID, record.AccessGroupID, record.RequestID, record.AttemptID)
	state, err := r.ledgerInspector.Inspect(ctx, key, record.UserID)
	if err != nil {
		return unifiedGatewayRecoveryOutcome{}, err
	}
	if state.Exists && state.UserID != record.UserID {
		return unifiedGatewayRecoveryOutcome{}, fmt.Errorf("%w: recovery ledger belongs to another user", ErrUnifiedGatewaySnapshotConflict)
	}
	if err := validateUnifiedGatewayLedgerState(state); err != nil {
		return unifiedGatewayRecoveryOutcome{}, err
	}

	switch record.Status {
	case UnifiedGatewaySnapshotQuoted:
		return r.reconcileQuoted(ctx, record, state, now)
	case UnifiedGatewaySnapshotReserved, UnifiedGatewaySnapshotPending:
		return r.reconcileHeld(ctx, record, state, now)
	case UnifiedGatewaySnapshotCaptured:
		return r.reconcileCaptured(ctx, record, state)
	case UnifiedGatewaySnapshotReleased, UnifiedGatewaySnapshotSettlementFailed:
		return r.reconcileTerminal(ctx, record, state)
	default:
		return unifiedGatewayRecoveryOutcome{}, fmt.Errorf("%w: unsupported snapshot status %q", ErrUnifiedGatewaySnapshotConflict, record.Status)
	}
}

func (r *UnifiedGatewayReconciler) reconcileQuoted(ctx context.Context, record *UnifiedGatewayPriceSnapshotRecord, state UnifiedGatewayLedgerState, now time.Time) (unifiedGatewayRecoveryOutcome, error) {
	if !state.Exists {
		if !r.isStale(record, now) {
			return unifiedGatewayRecoveryOutcome{preserved: true}, nil
		}
		if err := r.recoverSnapshot(ctx, record, UnifiedGatewaySnapshotReleased, 0, "recovery: quoted snapshot has no ledger entry"); err != nil {
			return unifiedGatewayRecoveryOutcome{}, err
		}
		return unifiedGatewayRecoveryOutcome{released: true}, nil
	}
	switch state.Status {
	case unifiedGatewayRecoveryLedgerCaptured:
		if err := r.recoverSnapshot(ctx, record, UnifiedGatewaySnapshotCaptured, state.CapturedAmount, ""); err != nil {
			return unifiedGatewayRecoveryOutcome{}, err
		}
		return unifiedGatewayRecoveryOutcome{captured: true}, nil
	case unifiedGatewayRecoveryLedgerReleased:
		if err := r.recoverSnapshot(ctx, record, UnifiedGatewaySnapshotReleased, 0, "recovery: ledger was already released"); err != nil {
			return unifiedGatewayRecoveryOutcome{}, err
		}
		return unifiedGatewayRecoveryOutcome{released: true}, nil
	case unifiedGatewayRecoveryLedgerReserved:
		if r.isStale(record, now) {
			if err := r.release(ctx, record); err != nil {
				return unifiedGatewayRecoveryOutcome{}, err
			}
			if err := r.recoverSnapshot(ctx, record, UnifiedGatewaySnapshotReleased, 0, "recovery: stale reservation released"); err != nil {
				return unifiedGatewayRecoveryOutcome{}, err
			}
			return unifiedGatewayRecoveryOutcome{released: true}, nil
		}
		if err := r.recoverSnapshot(ctx, record, UnifiedGatewaySnapshotReserved, state.ReservedAmount, ""); err != nil {
			return unifiedGatewayRecoveryOutcome{}, err
		}
		return unifiedGatewayRecoveryOutcome{preserved: true}, nil
	default:
		return unifiedGatewayRecoveryOutcome{}, fmt.Errorf("%w: unsupported ledger status %q", ErrUnifiedGatewaySnapshotConflict, state.Status)
	}
}

func (r *UnifiedGatewayReconciler) reconcileHeld(ctx context.Context, record *UnifiedGatewayPriceSnapshotRecord, state UnifiedGatewayLedgerState, now time.Time) (unifiedGatewayRecoveryOutcome, error) {
	if !state.Exists {
		if !r.isStale(record, now) {
			return unifiedGatewayRecoveryOutcome{preserved: true}, nil
		}
		if err := r.recoverSnapshot(ctx, record, UnifiedGatewaySnapshotReleased, 0, "recovery: held snapshot has no ledger entry"); err != nil {
			return unifiedGatewayRecoveryOutcome{}, err
		}
		return unifiedGatewayRecoveryOutcome{released: true}, nil
	}
	switch state.Status {
	case unifiedGatewayRecoveryLedgerCaptured:
		if err := r.recoverSnapshot(ctx, record, UnifiedGatewaySnapshotCaptured, state.CapturedAmount, ""); err != nil {
			return unifiedGatewayRecoveryOutcome{}, err
		}
		return unifiedGatewayRecoveryOutcome{captured: true}, nil
	case unifiedGatewayRecoveryLedgerReleased:
		if err := r.recoverSnapshot(ctx, record, UnifiedGatewaySnapshotReleased, 0, "recovery: ledger was already released"); err != nil {
			return unifiedGatewayRecoveryOutcome{}, err
		}
		return unifiedGatewayRecoveryOutcome{released: true}, nil
	case unifiedGatewayRecoveryLedgerReserved:
		if !r.isStale(record, now) {
			return unifiedGatewayRecoveryOutcome{preserved: true}, nil
		}
		if err := r.release(ctx, record); err != nil {
			return unifiedGatewayRecoveryOutcome{}, err
		}
		if err := r.recoverSnapshot(ctx, record, UnifiedGatewaySnapshotReleased, 0, "recovery: stale held reservation released"); err != nil {
			return unifiedGatewayRecoveryOutcome{}, err
		}
		return unifiedGatewayRecoveryOutcome{released: true}, nil
	default:
		return unifiedGatewayRecoveryOutcome{}, fmt.Errorf("%w: unsupported ledger status %q", ErrUnifiedGatewaySnapshotConflict, state.Status)
	}
}

func (r *UnifiedGatewayReconciler) reconcileCaptured(ctx context.Context, record *UnifiedGatewayPriceSnapshotRecord, state UnifiedGatewayLedgerState) (unifiedGatewayRecoveryOutcome, error) {
	if !state.Exists {
		if err := r.recoverSnapshot(ctx, record, UnifiedGatewaySnapshotSettlementFailed, 0, "recovery: captured snapshot has no ledger entry"); err != nil {
			return unifiedGatewayRecoveryOutcome{}, err
		}
		return unifiedGatewayRecoveryOutcome{released: true}, nil
	}
	switch state.Status {
	case unifiedGatewayRecoveryLedgerCaptured:
		if !unifiedGatewayLedgerAmountsEqual(state.CapturedAmount, record.UserCharge) {
			if err := r.recoverSnapshot(ctx, record, UnifiedGatewaySnapshotCaptured, state.CapturedAmount, "recovery: ledger amount is authoritative"); err != nil {
				return unifiedGatewayRecoveryOutcome{}, err
			}
		}
		return unifiedGatewayRecoveryOutcome{captured: true}, nil
	case unifiedGatewayRecoveryLedgerReserved:
		captureAmount := record.UserCharge
		result, err := r.ledger.Capture(ctx, UnifiedGatewayCaptureCommand{Key: UnifiedGatewayReservationKey(record.APIKeyID, record.UserID, record.AccessGroupID, record.RequestID, record.AttemptID), UserID: record.UserID, Amount: captureAmount})
		if err != nil {
			return unifiedGatewayRecoveryOutcome{}, err
		}
		if result != nil {
			captureAmount = result.Amount
		}
		if err := r.recoverSnapshot(ctx, record, UnifiedGatewaySnapshotCaptured, captureAmount, ""); err != nil {
			return unifiedGatewayRecoveryOutcome{}, err
		}
		return unifiedGatewayRecoveryOutcome{captured: true}, nil
	case unifiedGatewayRecoveryLedgerReleased:
		if err := r.recoverSnapshot(ctx, record, UnifiedGatewaySnapshotSettlementFailed, 0, "recovery: captured snapshot lost its ledger hold"); err != nil {
			return unifiedGatewayRecoveryOutcome{}, err
		}
		return unifiedGatewayRecoveryOutcome{released: true}, nil
	default:
		return unifiedGatewayRecoveryOutcome{}, fmt.Errorf("%w: unsupported ledger status %q", ErrUnifiedGatewaySnapshotConflict, state.Status)
	}
}

func (r *UnifiedGatewayReconciler) reconcileTerminal(ctx context.Context, record *UnifiedGatewayPriceSnapshotRecord, state UnifiedGatewayLedgerState) (unifiedGatewayRecoveryOutcome, error) {
	if !state.Exists {
		if record.Status == UnifiedGatewaySnapshotReleased && record.UserCharge == 0 {
			return unifiedGatewayRecoveryOutcome{}, nil
		}
		if err := r.recoverSnapshot(ctx, record, record.Status, 0, record.FailureMessage); err != nil {
			return unifiedGatewayRecoveryOutcome{}, err
		}
		return unifiedGatewayRecoveryOutcome{released: true}, nil
	}
	switch state.Status {
	case unifiedGatewayRecoveryLedgerReserved:
		if err := r.release(ctx, record); err != nil {
			return unifiedGatewayRecoveryOutcome{}, err
		}
	case unifiedGatewayRecoveryLedgerCaptured:
		if err := r.refund(ctx, record); err != nil {
			return unifiedGatewayRecoveryOutcome{}, err
		}
	case unifiedGatewayRecoveryLedgerReleased:
	default:
		return unifiedGatewayRecoveryOutcome{}, fmt.Errorf("%w: unsupported ledger status %q", ErrUnifiedGatewaySnapshotConflict, state.Status)
	}
	if err := r.recoverSnapshot(ctx, record, record.Status, 0, record.FailureMessage); err != nil {
		return unifiedGatewayRecoveryOutcome{}, err
	}
	return unifiedGatewayRecoveryOutcome{released: true}, nil
}

func (r *UnifiedGatewayReconciler) reconcileMissingSnapshot(ctx context.Context, task UnifiedGatewayRecoveryTask) (unifiedGatewayRecoveryOutcome, error) {
	key := UnifiedGatewayReservationKey(task.APIKeyID, task.UserID, task.AccessGroupID, task.RequestID, task.AttemptID)
	state, err := r.ledgerInspector.Inspect(ctx, key, task.UserID)
	if err != nil {
		return unifiedGatewayRecoveryOutcome{}, err
	}
	if !state.Exists {
		return unifiedGatewayRecoveryOutcome{}, nil
	}
	switch state.Status {
	case unifiedGatewayRecoveryLedgerReserved:
		if _, err := r.ledger.Release(ctx, UnifiedGatewayReserveCommand{Key: key, UserID: task.UserID}); err != nil {
			return unifiedGatewayRecoveryOutcome{}, err
		}
	case unifiedGatewayRecoveryLedgerCaptured:
		if _, err := r.ledger.Refund(ctx, UnifiedGatewayCaptureCommand{Key: key, UserID: task.UserID, Amount: state.CapturedAmount}); err != nil {
			return unifiedGatewayRecoveryOutcome{}, err
		}
	case unifiedGatewayRecoveryLedgerReleased:
	default:
		return unifiedGatewayRecoveryOutcome{}, fmt.Errorf("%w: unsupported ledger status %q", ErrUnifiedGatewaySnapshotConflict, state.Status)
	}
	return unifiedGatewayRecoveryOutcome{released: true}, nil
}

func (r *UnifiedGatewayReconciler) recoverSnapshot(ctx context.Context, record *UnifiedGatewayPriceSnapshotRecord, status UnifiedGatewaySnapshotStatus, userCharge float64, failureMessage string) error {
	if userCharge < 0 || math.IsNaN(userCharge) || math.IsInf(userCharge, 0) {
		return fmt.Errorf("%w: recovery charge is invalid", ErrUnifiedGatewayInvalidRequest)
	}
	if !unifiedGatewayRecoverySnapshotTransitionAllowed(record.Status, status) {
		return fmt.Errorf("%w: recovery transition %s -> %s is not allowed", ErrUnifiedGatewaySnapshotConflict, record.Status, status)
	}
	return r.snapshotRecovery.RecoverSnapshot(
		ctx,
		record.APIKeyID,
		record.UserID,
		record.AccessGroupID,
		record.RequestID,
		record.AttemptID,
		record.Status,
		status,
		record.MeasuredUnits,
		userCharge,
		record.UpstreamRequestID,
		record.ResponseBody,
		strings.TrimSpace(failureMessage),
	)
}

func (r *UnifiedGatewayReconciler) release(ctx context.Context, record *UnifiedGatewayPriceSnapshotRecord) error {
	_, err := r.ledger.Release(ctx, UnifiedGatewayReserveCommand{
		Key:    UnifiedGatewayReservationKey(record.APIKeyID, record.UserID, record.AccessGroupID, record.RequestID, record.AttemptID),
		UserID: record.UserID,
	})
	return err
}

func (r *UnifiedGatewayReconciler) refund(ctx context.Context, record *UnifiedGatewayPriceSnapshotRecord) error {
	_, err := r.ledger.Refund(ctx, UnifiedGatewayCaptureCommand{
		Key:    UnifiedGatewayReservationKey(record.APIKeyID, record.UserID, record.AccessGroupID, record.RequestID, record.AttemptID),
		UserID: record.UserID,
		Amount: record.UserCharge,
	})
	return err
}

func (r *UnifiedGatewayReconciler) isStale(record *UnifiedGatewayPriceSnapshotRecord, now time.Time) bool {
	return record == nil || record.CreatedAt.IsZero() || !record.CreatedAt.Add(r.staleAfter).After(now)
}

func unifiedGatewayRecoverySnapshotTransitionAllowed(from, to UnifiedGatewaySnapshotStatus) bool {
	if from == to {
		return true
	}
	switch from {
	case UnifiedGatewaySnapshotQuoted:
		return to == UnifiedGatewaySnapshotReserved || to == UnifiedGatewaySnapshotCaptured || to == UnifiedGatewaySnapshotReleased || to == UnifiedGatewaySnapshotSettlementFailed
	case UnifiedGatewaySnapshotReserved:
		return to == UnifiedGatewaySnapshotPending || to == UnifiedGatewaySnapshotCaptured || to == UnifiedGatewaySnapshotReleased || to == UnifiedGatewaySnapshotSettlementFailed
	case UnifiedGatewaySnapshotPending:
		return to == UnifiedGatewaySnapshotCaptured || to == UnifiedGatewaySnapshotReleased || to == UnifiedGatewaySnapshotSettlementFailed
	case UnifiedGatewaySnapshotCaptured:
		return to == UnifiedGatewaySnapshotSettlementFailed
	case UnifiedGatewaySnapshotReleased, UnifiedGatewaySnapshotSettlementFailed:
		return false
	default:
		return false
	}
}

func validateUnifiedGatewayLedgerState(state UnifiedGatewayLedgerState) error {
	if !state.Exists {
		return nil
	}
	if state.UserID <= 0 || !isFiniteRecoveryNonNegative(state.ReservedAmount) || !isFiniteRecoveryNonNegative(state.CapturedAmount) {
		return fmt.Errorf("%w: recovery ledger state is invalid", ErrUnifiedGatewayInvalidRequest)
	}
	switch state.Status {
	case unifiedGatewayRecoveryLedgerReserved, unifiedGatewayRecoveryLedgerCaptured, unifiedGatewayRecoveryLedgerReleased:
		return nil
	default:
		return fmt.Errorf("%w: unsupported ledger status %q", ErrUnifiedGatewaySnapshotConflict, state.Status)
	}
}

func isFiniteRecoveryNonNegative(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func unifiedGatewayLedgerAmountsEqual(left, right float64) bool {
	return math.Abs(left-right) <= 0.00000001
}

func recoveryErrorMessage(err error) string {
	message := "recovery failed"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = strings.TrimSpace(err.Error())
	}
	if len(message) > 2048 {
		return message[:2048]
	}
	return message
}

const (
	unifiedGatewayRecoveryLedgerReserved = "reserved"
	unifiedGatewayRecoveryLedgerCaptured = "captured"
	unifiedGatewayRecoveryLedgerReleased = "released"
)
