package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUnifiedGatewayReconcilerCaptureThenCrash(t *testing.T) {
	now := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)
	store := newRecoveryTestSnapshotStore(recoveryTestRecord(UnifiedGatewaySnapshotReserved, now))
	ledger := newRecoveryTestLedger()
	key := UnifiedGatewayReservationKey(10, 20, 30, "request-1", "attempt-1")
	ledger.seed(key, UnifiedGatewayLedgerState{Exists: true, UserID: 20, ReservedAmount: 3, Status: "reserved"})
	_, err := ledger.Capture(context.Background(), UnifiedGatewayCaptureCommand{Key: key, UserID: 20, Amount: 2.5})
	require.NoError(t, err)

	reconciler := NewUnifiedGatewayReconciler(store, ledger, UnifiedGatewayReconcilerOptions{
		StaleAfter: time.Hour,
		Now:        func() time.Time { return now },
	})
	report, err := reconciler.Reconcile(context.Background())
	require.NoError(t, err)
	require.Equal(t, UnifiedGatewaySnapshotCaptured, store.record.Status)
	require.Equal(t, 2.5, store.record.UserCharge)
	require.Equal(t, 1, ledger.captureCalls)
	require.Equal(t, 1, report.Captured)

	store.enqueueDuplicate()
	_, err = reconciler.Reconcile(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, ledger.captureCalls, "replaying a captured task must not debit again")
}

func TestUnifiedGatewayReconcilerReserveThenCrashEventuallyReleases(t *testing.T) {
	now := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)
	store := newRecoveryTestSnapshotStore(recoveryTestRecordAt(UnifiedGatewaySnapshotQuoted, now.Add(-2*time.Hour)))
	ledger := newRecoveryTestLedger()
	key := UnifiedGatewayReservationKey(10, 20, 30, "request-1", "attempt-1")
	ledger.seed(key, UnifiedGatewayLedgerState{Exists: true, UserID: 20, ReservedAmount: 3, Status: "reserved"})

	reconciler := NewUnifiedGatewayReconciler(store, ledger, UnifiedGatewayReconcilerOptions{
		StaleAfter: time.Hour,
		Now:        func() time.Time { return now },
	})
	_, err := reconciler.Reconcile(context.Background())
	require.NoError(t, err)
	require.Equal(t, UnifiedGatewaySnapshotReleased, store.record.Status)
	require.Zero(t, store.record.UserCharge)
	require.Equal(t, 1, ledger.releaseCalls)
	require.Equal(t, "released", ledger.states[key].Status)
}

func TestUnifiedGatewayReconcilerPendingRecoveryRetainsThenReleasesFunds(t *testing.T) {
	now := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)
	store := newRecoveryTestSnapshotStore(recoveryTestRecord(UnifiedGatewaySnapshotPending, now))
	ledger := newRecoveryTestLedger()
	key := UnifiedGatewayReservationKey(10, 20, 30, "request-1", "attempt-1")
	ledger.seed(key, UnifiedGatewayLedgerState{Exists: true, UserID: 20, ReservedAmount: 3, Status: "reserved"})
	current := now
	reconciler := NewUnifiedGatewayReconciler(store, ledger, UnifiedGatewayReconcilerOptions{
		StaleAfter: time.Hour,
		RetryDelay: time.Minute,
		Now:        func() time.Time { return current },
	})

	report, err := reconciler.Reconcile(context.Background())
	require.NoError(t, err)
	require.Equal(t, UnifiedGatewaySnapshotPending, store.record.Status)
	require.Zero(t, ledger.releaseCalls, "pending work keeps its reserve during the grace period")
	require.Equal(t, 1, report.Preserved)

	current = now.Add(2 * time.Hour)
	report, err = reconciler.Reconcile(context.Background())
	require.NoError(t, err)
	require.Equal(t, UnifiedGatewaySnapshotReleased, store.record.Status)
	require.Zero(t, store.record.UserCharge)
	require.Equal(t, 1, ledger.releaseCalls)
	require.Equal(t, 1, report.Released)
}

func TestUnifiedGatewayReconcilerIdempotentReplayAfterLedgerCapture(t *testing.T) {
	now := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)
	store := newRecoveryTestSnapshotStore(recoveryTestRecord(UnifiedGatewaySnapshotReserved, now))
	ledger := newRecoveryTestLedger()
	key := UnifiedGatewayReservationKey(10, 20, 30, "request-1", "attempt-1")
	ledger.seed(key, UnifiedGatewayLedgerState{Exists: true, UserID: 20, ReservedAmount: 3, CapturedAmount: 2.5, Status: "captured"})
	reconciler := NewUnifiedGatewayReconciler(store, ledger, UnifiedGatewayReconcilerOptions{
		StaleAfter: time.Hour,
		Now:        func() time.Time { return now },
	})

	_, err := reconciler.Reconcile(context.Background())
	require.NoError(t, err)
	require.Equal(t, UnifiedGatewaySnapshotCaptured, store.record.Status)
	require.Equal(t, 2.5, store.record.UserCharge)
	require.Zero(t, ledger.captureCalls)

	store.enqueueDuplicate()
	_, err = reconciler.Reconcile(context.Background())
	require.NoError(t, err)
	require.Zero(t, ledger.captureCalls, "replaying a ledger-captured task must not call capture")
	require.Zero(t, ledger.refundCalls)
}

type recoveryTestSnapshotStore struct {
	record    *UnifiedGatewayPriceSnapshotRecord
	nextID    int64
	tasks     []UnifiedGatewayRecoveryTask
	taskState map[int64]string
	nextTask  int64
}

func newRecoveryTestSnapshotStore(record *UnifiedGatewayPriceSnapshotRecord) *recoveryTestSnapshotStore {
	store := &recoveryTestSnapshotStore{record: record, nextID: 1, nextTask: 1, taskState: make(map[int64]string)}
	store.enqueueDuplicate()
	return store
}

func (s *recoveryTestSnapshotStore) Create(_ context.Context, record *UnifiedGatewayPriceSnapshotRecord) (*UnifiedGatewayPriceSnapshotRecord, error) {
	if s.record != nil {
		return nil, ErrUnifiedGatewaySnapshotConflict
	}
	s.record = cloneRecoveryTestRecord(record)
	s.record.ID = s.nextID
	s.nextID++
	return cloneRecoveryTestRecord(s.record), nil
}

func (s *recoveryTestSnapshotStore) Get(_ context.Context, apiKeyID, userID, accessGroupID int64, requestID, attemptID string) (*UnifiedGatewayPriceSnapshotRecord, error) {
	if s.record == nil || s.record.APIKeyID != apiKeyID || s.record.UserID != userID || s.record.AccessGroupID != accessGroupID || s.record.RequestID != requestID || s.record.AttemptID != attemptID {
		return nil, ErrUnifiedGatewaySnapshotNotFound
	}
	return cloneRecoveryTestRecord(s.record), nil
}

func (s *recoveryTestSnapshotStore) Finalize(_ context.Context, apiKeyID, userID, accessGroupID int64, requestID, attemptID string, status UnifiedGatewaySnapshotStatus, measuredUnits, userCharge float64, upstreamRequestID string, responseBody []byte, failureMessage string) error {
	record, err := s.Get(context.Background(), apiKeyID, userID, accessGroupID, requestID, attemptID)
	if err != nil {
		return err
	}
	return s.RecoverSnapshot(context.Background(), apiKeyID, userID, accessGroupID, requestID, attemptID, record.Status, status, measuredUnits, userCharge, upstreamRequestID, responseBody, failureMessage)
}

func (s *recoveryTestSnapshotStore) RecoverSnapshot(_ context.Context, apiKeyID, userID, accessGroupID int64, requestID, attemptID string, expectedStatus, status UnifiedGatewaySnapshotStatus, measuredUnits, userCharge float64, upstreamRequestID string, responseBody []byte, failureMessage string) error {
	if s.record == nil || s.record.APIKeyID != apiKeyID || s.record.UserID != userID || s.record.AccessGroupID != accessGroupID || s.record.RequestID != requestID || s.record.AttemptID != attemptID {
		return ErrUnifiedGatewaySnapshotNotFound
	}
	if s.record.Status != expectedStatus {
		if s.record.Status == status {
			return nil
		}
		return ErrUnifiedGatewaySnapshotConflict
	}
	s.record.Status = status
	s.record.MeasuredUnits = measuredUnits
	s.record.UserCharge = userCharge
	s.record.UpstreamRequestID = upstreamRequestID
	s.record.ResponseBody = append([]byte(nil), responseBody...)
	s.record.FailureMessage = failureMessage
	s.record.FinalizedAt = time.Now().UTC()
	return nil
}

func (s *recoveryTestSnapshotStore) DiscoverRecoveryTasks(_ context.Context, _ int) error {
	return nil
}

func (s *recoveryTestSnapshotStore) ClaimRecoveryTasks(_ context.Context, limit int, now time.Time) ([]UnifiedGatewayRecoveryTask, error) {
	var claimed []UnifiedGatewayRecoveryTask
	for i := range s.tasks {
		if len(claimed) >= limit {
			break
		}
		if s.taskState[s.tasks[i].ID] != "pending" {
			continue
		}
		s.taskState[s.tasks[i].ID] = "processing"
		s.tasks[i].Attempts++
		claimed = append(claimed, s.tasks[i])
	}
	_ = now
	return claimed, nil
}

func (s *recoveryTestSnapshotStore) CompleteRecoveryTask(_ context.Context, taskID int64, _ int) error {
	s.taskState[taskID] = "completed"
	return nil
}

func (s *recoveryTestSnapshotStore) RetryRecoveryTask(_ context.Context, taskID int64, _ int, _ time.Time, _ string) error {
	s.taskState[taskID] = "pending"
	return nil
}

func (s *recoveryTestSnapshotStore) enqueueDuplicate() {
	task := UnifiedGatewayRecoveryTask{
		ID:             s.nextTask,
		APIKeyID:       s.record.APIKeyID,
		UserID:         s.record.UserID,
		AccessGroupID:  s.record.AccessGroupID,
		RequestID:      s.record.RequestID,
		AttemptID:      s.record.AttemptID,
		SnapshotStatus: s.record.Status,
		UserCharge:     s.record.UserCharge,
	}
	s.nextTask++
	s.tasks = append(s.tasks, task)
	s.taskState[task.ID] = "pending"
}

type recoveryTestLedger struct {
	states       map[string]UnifiedGatewayLedgerState
	captureCalls int
	releaseCalls int
	refundCalls  int
}

func newRecoveryTestLedger() *recoveryTestLedger {
	return &recoveryTestLedger{states: make(map[string]UnifiedGatewayLedgerState)}
}

func (l *recoveryTestLedger) seed(key string, state UnifiedGatewayLedgerState) {
	l.states[key] = state
}

func (l *recoveryTestLedger) Inspect(_ context.Context, key string, userID int64) (UnifiedGatewayLedgerState, error) {
	state, ok := l.states[key]
	if !ok {
		return UnifiedGatewayLedgerState{UserID: userID}, nil
	}
	if state.UserID != userID {
		return UnifiedGatewayLedgerState{}, ErrUnifiedGatewaySnapshotConflict
	}
	return state, nil
}

func (l *recoveryTestLedger) Reserve(_ context.Context, command UnifiedGatewayReserveCommand) (*UnifiedGatewayLedgerResult, error) {
	state, ok := l.states[command.Key]
	if ok {
		return &UnifiedGatewayLedgerResult{Applied: false, Amount: state.ReservedAmount}, nil
	}
	l.states[command.Key] = UnifiedGatewayLedgerState{Exists: true, UserID: command.UserID, ReservedAmount: command.Amount, Status: "reserved"}
	return &UnifiedGatewayLedgerResult{Applied: true, Amount: command.Amount}, nil
}

func (l *recoveryTestLedger) Capture(_ context.Context, command UnifiedGatewayCaptureCommand) (*UnifiedGatewayLedgerResult, error) {
	state, ok := l.states[command.Key]
	if !ok {
		return nil, errors.New("missing ledger state")
	}
	if state.Status == "captured" {
		return &UnifiedGatewayLedgerResult{Applied: false, Amount: state.CapturedAmount}, nil
	}
	if state.Status == "released" {
		return nil, ErrUnifiedGatewaySnapshotConflict
	}
	l.captureCalls++
	state.Status = "captured"
	state.CapturedAmount = command.Amount
	l.states[command.Key] = state
	return &UnifiedGatewayLedgerResult{Applied: true, Amount: command.Amount}, nil
}

func (l *recoveryTestLedger) Refund(_ context.Context, command UnifiedGatewayCaptureCommand) (*UnifiedGatewayLedgerResult, error) {
	state, ok := l.states[command.Key]
	if !ok || state.Status == "released" {
		return &UnifiedGatewayLedgerResult{Applied: false}, nil
	}
	if state.Status != "captured" {
		return nil, ErrUnifiedGatewaySnapshotConflict
	}
	l.refundCalls++
	state.Status = "released"
	l.states[command.Key] = state
	return &UnifiedGatewayLedgerResult{Applied: true, Amount: state.CapturedAmount}, nil
}

func (l *recoveryTestLedger) Release(_ context.Context, command UnifiedGatewayReserveCommand) (*UnifiedGatewayLedgerResult, error) {
	state, ok := l.states[command.Key]
	if !ok || state.Status == "released" {
		return &UnifiedGatewayLedgerResult{Applied: false}, nil
	}
	if state.Status != "reserved" {
		return nil, ErrUnifiedGatewaySnapshotConflict
	}
	l.releaseCalls++
	state.Status = "released"
	l.states[command.Key] = state
	return &UnifiedGatewayLedgerResult{Applied: true, Amount: state.ReservedAmount}, nil
}

func (l *recoveryTestLedger) Balance(_ context.Context, _ int64) (float64, error) {
	return 0, nil
}

func recoveryTestRecord(status UnifiedGatewaySnapshotStatus, now time.Time) *UnifiedGatewayPriceSnapshotRecord {
	return recoveryTestRecordAt(status, now)
}

func recoveryTestRecordAt(status UnifiedGatewaySnapshotStatus, createdAt time.Time) *UnifiedGatewayPriceSnapshotRecord {
	return &UnifiedGatewayPriceSnapshotRecord{
		ID:            1,
		APIKeyID:      10,
		UserID:        20,
		AccessGroupID: 30,
		RequestID:     "request-1",
		AttemptID:     "attempt-1",
		Status:        status,
		CreatedAt:     createdAt,
		MeasuredUnits: 1,
		UserCharge:    3,
		Snapshot:      UnifiedRoutePriceSnapshot{Digest: "digest-1"},
	}
}

func cloneRecoveryTestRecord(record *UnifiedGatewayPriceSnapshotRecord) *UnifiedGatewayPriceSnapshotRecord {
	if record == nil {
		return nil
	}
	clone := *record
	clone.ResponseBody = append([]byte(nil), record.ResponseBody...)
	return &clone
}
