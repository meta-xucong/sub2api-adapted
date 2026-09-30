package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// unifiedGatewayChargeLedgerRepository keeps the unified gateway's user
// balance hold and its durable idempotency state in one PostgreSQL transaction.
// It intentionally does not use the legacy usage billing repository: the
// unified gateway must never inherit the legacy overdraft behavior.
type unifiedGatewayChargeLedgerRepository struct {
	db *sql.DB
}

const (
	unifiedGatewayChargeLedgerReserved = "reserved"
	unifiedGatewayChargeLedgerCaptured = "captured"
	unifiedGatewayChargeLedgerReleased = "released"

	// users.balance and users.frozen_balance are NUMERIC(20,8).  Tolerating one
	// storage unit keeps retries idempotent when a decimal amount crossed the
	// float64 boundary before it was rounded by PostgreSQL.
	unifiedGatewayChargeLedgerAmountEpsilon = 0.00000001
)

var _ service.UnifiedGatewayChargeLedger = (*unifiedGatewayChargeLedgerRepository)(nil)
var _ service.UnifiedGatewayLedgerInspector = (*unifiedGatewayChargeLedgerRepository)(nil)

func NewUnifiedGatewayChargeLedgerRepository(db *sql.DB) service.UnifiedGatewayChargeLedger {
	return &unifiedGatewayChargeLedgerRepository{db: db}
}

type unifiedGatewayChargeLedgerEntry struct {
	ID             int64
	ReservationKey string
	UserID         int64
	ReservedAmount float64
	CapturedAmount float64
	Status         string
}

func (r *unifiedGatewayChargeLedgerRepository) ready() error {
	if r == nil || r.db == nil {
		return errors.New("unified gateway charge ledger repository db is nil")
	}
	return nil
}

func (r *unifiedGatewayChargeLedgerRepository) Reserve(ctx context.Context, command service.UnifiedGatewayReserveCommand) (_ *service.UnifiedGatewayLedgerResult, err error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	key, err := validateUnifiedGatewayLedgerCommand(command.Key, command.UserID, command.Amount)
	if err != nil {
		return nil, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	balance, frozenBalance, err := lockUnifiedGatewayUser(ctx, tx, command.UserID)
	if err != nil {
		return nil, err
	}
	if !isFiniteNonNegativeLedgerAmount(frozenBalance) {
		return nil, fmt.Errorf("%w: user frozen balance is invalid", service.ErrUnifiedGatewayInvalidRequest)
	}

	var ledgerID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO unified_gateway_charge_ledger (
			reservation_key, user_id, reserved_amount, captured_amount, status
		) VALUES ($1, $2, $3, 0, $4)
		ON CONFLICT (reservation_key) DO NOTHING
		RETURNING id
	`, key, command.UserID, command.Amount, unifiedGatewayChargeLedgerReserved).Scan(&ledgerID)
	if errors.Is(err, sql.ErrNoRows) {
		entry, found, lookupErr := lookupUnifiedGatewayChargeLedgerEntry(ctx, tx, key)
		if lookupErr != nil {
			return nil, lookupErr
		}
		if !found {
			return nil, fmt.Errorf("%w: reservation key disappeared", service.ErrUnifiedGatewaySnapshotConflict)
		}
		result, replayErr := replayUnifiedGatewayReserve(entry, command.UserID, command.Amount, balance)
		if replayErr != nil {
			return nil, replayErr
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return result, nil
	}
	if err != nil {
		return nil, err
	}

	newBalance, newFrozenBalance, err := updateUnifiedGatewayUserForReserve(ctx, tx, command.UserID, command.Amount)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if balance < command.Amount {
				return nil, service.ErrUnifiedGatewayBalanceInsufficient
			}
			return nil, fmt.Errorf("%w: reserve balance update did not apply", service.ErrUnifiedGatewaySnapshotConflict)
		}
		return nil, err
	}
	if !isFiniteNonNegativeLedgerAmount(newBalance) || !isFiniteNonNegativeLedgerAmount(newFrozenBalance) {
		return nil, fmt.Errorf("%w: reserve would create a negative balance", service.ErrUnifiedGatewayInvalidRequest)
	}

	_ = ledgerID
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &service.UnifiedGatewayLedgerResult{Applied: true, Amount: command.Amount, NewBalance: newBalance}, nil
}

func (r *unifiedGatewayChargeLedgerRepository) Capture(ctx context.Context, command service.UnifiedGatewayCaptureCommand) (_ *service.UnifiedGatewayLedgerResult, err error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	key, err := validateUnifiedGatewayLedgerCommand(command.Key, command.UserID, command.Amount)
	if err != nil {
		return nil, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	balance, frozenBalance, err := lockUnifiedGatewayUser(ctx, tx, command.UserID)
	if err != nil {
		return nil, err
	}
	if !isFiniteNonNegativeLedgerAmount(frozenBalance) {
		return nil, fmt.Errorf("%w: user frozen balance is invalid", service.ErrUnifiedGatewayInvalidRequest)
	}

	var ledgerID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO unified_gateway_charge_ledger (
			reservation_key, user_id, reserved_amount, captured_amount, status
		) VALUES ($1, $2, 0, $3, $4)
		ON CONFLICT (reservation_key) DO NOTHING
		RETURNING id
	`, key, command.UserID, command.Amount, unifiedGatewayChargeLedgerCaptured).Scan(&ledgerID)
	if errors.Is(err, sql.ErrNoRows) {
		entry, found, lookupErr := lookupUnifiedGatewayChargeLedgerEntry(ctx, tx, key)
		if lookupErr != nil {
			return nil, lookupErr
		}
		if !found {
			return nil, fmt.Errorf("%w: capture key disappeared", service.ErrUnifiedGatewaySnapshotConflict)
		}
		if entry.UserID != command.UserID {
			return nil, fmt.Errorf("%w: reservation key belongs to another user", service.ErrUnifiedGatewaySnapshotConflict)
		}
		switch entry.Status {
		case unifiedGatewayChargeLedgerCaptured:
			if !unifiedGatewayLedgerAmountsEqual(entry.CapturedAmount, command.Amount) {
				return nil, fmt.Errorf("%w: capture amount differs for an existing key", service.ErrUnifiedGatewaySnapshotConflict)
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return &service.UnifiedGatewayLedgerResult{Applied: false, Amount: entry.CapturedAmount, NewBalance: balance}, nil
		case unifiedGatewayChargeLedgerReleased:
			return nil, fmt.Errorf("%w: released reservation cannot be captured", service.ErrUnifiedGatewaySnapshotConflict)
		case unifiedGatewayChargeLedgerReserved:
			newBalance, newFrozenBalance, settleErr := settleUnifiedGatewayUserCapture(ctx, tx, command.UserID, entry.ReservedAmount, command.Amount)
			if settleErr != nil {
				if errors.Is(settleErr, sql.ErrNoRows) {
					return nil, classifyUnifiedGatewayCaptureMiss(balance, frozenBalance, entry.ReservedAmount, command.Amount)
				}
				return nil, settleErr
			}
			if !isFiniteNonNegativeLedgerAmount(newBalance) || !isFiniteNonNegativeLedgerAmount(newFrozenBalance) {
				return nil, fmt.Errorf("%w: capture would create a negative balance", service.ErrUnifiedGatewayInvalidRequest)
			}
			if err := markUnifiedGatewayLedgerCaptured(ctx, tx, entry.ID, command.Amount); err != nil {
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return &service.UnifiedGatewayLedgerResult{Applied: true, Amount: command.Amount, NewBalance: newBalance}, nil
		default:
			return nil, fmt.Errorf("%w: unsupported ledger status %q", service.ErrUnifiedGatewaySnapshotConflict, entry.Status)
		}
	}
	if err != nil {
		return nil, err
	}

	newBalance, _, err := updateUnifiedGatewayUserForDirectCapture(ctx, tx, command.UserID, command.Amount)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if balance < command.Amount {
				return nil, service.ErrUnifiedGatewayBalanceInsufficient
			}
			return nil, fmt.Errorf("%w: capture balance update did not apply", service.ErrUnifiedGatewaySnapshotConflict)
		}
		return nil, err
	}
	if !isFiniteNonNegativeLedgerAmount(newBalance) {
		return nil, fmt.Errorf("%w: capture would create a negative balance", service.ErrUnifiedGatewayInvalidRequest)
	}

	_ = ledgerID
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &service.UnifiedGatewayLedgerResult{Applied: true, Amount: command.Amount, NewBalance: newBalance}, nil
}

func (r *unifiedGatewayChargeLedgerRepository) Release(ctx context.Context, command service.UnifiedGatewayReserveCommand) (_ *service.UnifiedGatewayLedgerResult, err error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if err := validateUnifiedGatewayLedgerIdentity(command.Key, command.UserID); err != nil {
		return nil, err
	}
	key := unifiedGatewayLedgerKeyDigest(command.Key)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	balance, frozenBalance, err := lockUnifiedGatewayUser(ctx, tx, command.UserID)
	if err != nil {
		return nil, err
	}
	entry, found, err := lookupUnifiedGatewayChargeLedgerEntry(ctx, tx, key)
	if err != nil {
		return nil, err
	}
	if !found {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &service.UnifiedGatewayLedgerResult{Applied: false, NewBalance: balance}, nil
	}
	if entry.UserID != command.UserID {
		return nil, fmt.Errorf("%w: reservation key belongs to another user", service.ErrUnifiedGatewaySnapshotConflict)
	}
	switch entry.Status {
	case unifiedGatewayChargeLedgerReleased:
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &service.UnifiedGatewayLedgerResult{Applied: false, NewBalance: balance}, nil
	case unifiedGatewayChargeLedgerCaptured:
		return nil, fmt.Errorf("%w: captured reservation cannot be released", service.ErrUnifiedGatewaySnapshotConflict)
	case unifiedGatewayChargeLedgerReserved:
		newBalance, newFrozenBalance, releaseErr := releaseUnifiedGatewayReservedBalance(ctx, tx, command.UserID, entry.ReservedAmount)
		if releaseErr != nil {
			if errors.Is(releaseErr, sql.ErrNoRows) {
				return nil, classifyUnifiedGatewayReleaseMiss(balance, frozenBalance, entry.ReservedAmount)
			}
			return nil, releaseErr
		}
		if !isFiniteNonNegativeLedgerAmount(newBalance) || !isFiniteNonNegativeLedgerAmount(newFrozenBalance) {
			return nil, fmt.Errorf("%w: release would create a negative balance", service.ErrUnifiedGatewayInvalidRequest)
		}
		if err := markUnifiedGatewayLedgerReleased(ctx, tx, entry.ID); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &service.UnifiedGatewayLedgerResult{Applied: true, Amount: entry.ReservedAmount, NewBalance: newBalance}, nil
	default:
		return nil, fmt.Errorf("%w: unsupported ledger status %q", service.ErrUnifiedGatewaySnapshotConflict, entry.Status)
	}
}

func (r *unifiedGatewayChargeLedgerRepository) Refund(ctx context.Context, command service.UnifiedGatewayCaptureCommand) (_ *service.UnifiedGatewayLedgerResult, err error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if err := validateUnifiedGatewayLedgerIdentity(command.Key, command.UserID); err != nil {
		return nil, err
	}
	key := unifiedGatewayLedgerKeyDigest(command.Key)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	balance, frozenBalance, err := lockUnifiedGatewayUser(ctx, tx, command.UserID)
	if err != nil {
		return nil, err
	}
	entry, found, err := lookupUnifiedGatewayChargeLedgerEntry(ctx, tx, key)
	if err != nil {
		return nil, err
	}
	if !found {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &service.UnifiedGatewayLedgerResult{Applied: false, NewBalance: balance}, nil
	}
	if entry.UserID != command.UserID {
		return nil, fmt.Errorf("%w: reservation key belongs to another user", service.ErrUnifiedGatewaySnapshotConflict)
	}
	switch entry.Status {
	case unifiedGatewayChargeLedgerReleased:
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &service.UnifiedGatewayLedgerResult{Applied: false, NewBalance: balance}, nil
	case unifiedGatewayChargeLedgerReserved:
		newBalance, newFrozenBalance, releaseErr := releaseUnifiedGatewayReservedBalance(ctx, tx, command.UserID, entry.ReservedAmount)
		if releaseErr != nil {
			if errors.Is(releaseErr, sql.ErrNoRows) {
				return nil, classifyUnifiedGatewayReleaseMiss(balance, frozenBalance, entry.ReservedAmount)
			}
			return nil, releaseErr
		}
		if !isFiniteNonNegativeLedgerAmount(newBalance) || !isFiniteNonNegativeLedgerAmount(newFrozenBalance) {
			return nil, fmt.Errorf("%w: refund would create a negative balance", service.ErrUnifiedGatewayInvalidRequest)
		}
		if err := markUnifiedGatewayLedgerReleased(ctx, tx, entry.ID); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &service.UnifiedGatewayLedgerResult{Applied: true, Amount: entry.ReservedAmount, NewBalance: newBalance}, nil
	case unifiedGatewayChargeLedgerCaptured:
		if !isFiniteNonNegativeLedgerAmount(entry.CapturedAmount) {
			return nil, fmt.Errorf("%w: captured ledger amount is invalid", service.ErrUnifiedGatewayInvalidRequest)
		}
		newBalance, _, refundErr := refundUnifiedGatewayCapturedBalance(ctx, tx, command.UserID, entry.CapturedAmount)
		if refundErr != nil {
			return nil, refundErr
		}
		if !isFiniteNonNegativeLedgerAmount(newBalance) {
			return nil, fmt.Errorf("%w: refund would create a negative balance", service.ErrUnifiedGatewayInvalidRequest)
		}
		if err := markUnifiedGatewayLedgerReleased(ctx, tx, entry.ID); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &service.UnifiedGatewayLedgerResult{Applied: true, Amount: entry.CapturedAmount, NewBalance: newBalance}, nil
	default:
		return nil, fmt.Errorf("%w: unsupported ledger status %q", service.ErrUnifiedGatewaySnapshotConflict, entry.Status)
	}
}

func (r *unifiedGatewayChargeLedgerRepository) Balance(ctx context.Context, userID int64) (float64, error) {
	if err := r.ready(); err != nil {
		return 0, err
	}
	if userID <= 0 {
		return 0, service.ErrUnifiedGatewayInvalidRequest
	}
	var balance float64
	err := r.db.QueryRowContext(ctx, `
		SELECT balance
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`, userID).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, service.ErrUserNotFound
	}
	if err != nil {
		return 0, err
	}
	return balance, nil
}

// Inspect returns the durable ledger state without changing money. The
// reconciler reads this state before choosing an idempotent capture, release,
// or refund operation after a process crash.
func (r *unifiedGatewayChargeLedgerRepository) Inspect(ctx context.Context, key string, userID int64) (service.UnifiedGatewayLedgerState, error) {
	if err := r.ready(); err != nil {
		return service.UnifiedGatewayLedgerState{}, err
	}
	if userID <= 0 || strings.TrimSpace(key) == "" {
		return service.UnifiedGatewayLedgerState{}, service.ErrUnifiedGatewayInvalidRequest
	}
	keyDigest := unifiedGatewayLedgerKeyDigest(key)
	var state service.UnifiedGatewayLedgerState
	state.UserID = userID
	err := r.db.QueryRowContext(ctx, `
		SELECT user_id, reserved_amount, captured_amount, status
		FROM unified_gateway_charge_ledger
		WHERE reservation_key = $1
	`, keyDigest).Scan(&state.UserID, &state.ReservedAmount, &state.CapturedAmount, &state.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return service.UnifiedGatewayLedgerState{UserID: userID}, nil
	}
	if err != nil {
		return service.UnifiedGatewayLedgerState{}, err
	}
	state.Exists = true
	if state.UserID != userID {
		return state, fmt.Errorf("%w: reservation key belongs to another user", service.ErrUnifiedGatewaySnapshotConflict)
	}
	if !isFiniteNonNegativeLedgerAmount(state.ReservedAmount) || !isFiniteNonNegativeLedgerAmount(state.CapturedAmount) {
		return service.UnifiedGatewayLedgerState{}, fmt.Errorf("%w: ledger entry is invalid", service.ErrUnifiedGatewayInvalidRequest)
	}
	return state, nil
}

func validateUnifiedGatewayLedgerCommand(key string, userID int64, amount float64) (string, error) {
	if err := validateUnifiedGatewayLedgerIdentity(key, userID); err != nil {
		return "", err
	}
	if !isFiniteNonNegativeLedgerAmount(amount) {
		return "", service.ErrUnifiedGatewayInvalidRequest
	}
	return unifiedGatewayLedgerKeyDigest(key), nil
}

func validateUnifiedGatewayLedgerIdentity(key string, userID int64) error {
	key = strings.TrimSpace(key)
	if userID <= 0 || key == "" || len(key) > 255 {
		return service.ErrUnifiedGatewayInvalidRequest
	}
	return nil
}

func unifiedGatewayLedgerKeyDigest(key string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return hex.EncodeToString(digest[:])
}

func isFiniteNonNegativeLedgerAmount(amount float64) bool {
	return amount >= 0 && !math.IsNaN(amount) && !math.IsInf(amount, 0)
}

func unifiedGatewayLedgerAmountsEqual(left, right float64) bool {
	return math.Abs(left-right) <= unifiedGatewayChargeLedgerAmountEpsilon
}

func lockUnifiedGatewayUser(ctx context.Context, tx *sql.Tx, userID int64) (balance, frozenBalance float64, err error) {
	err = tx.QueryRowContext(ctx, `
		SELECT balance, COALESCE(frozen_balance, 0)
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE
	`, userID).Scan(&balance, &frozenBalance)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, service.ErrUserNotFound
	}
	return balance, frozenBalance, err
}

func updateUnifiedGatewayUserForReserve(ctx context.Context, tx *sql.Tx, userID int64, amount float64) (balance, frozenBalance float64, err error) {
	err = tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance - $1,
			frozen_balance = COALESCE(frozen_balance, 0) + $1,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
			AND balance >= $1
			AND COALESCE(frozen_balance, 0) >= 0
		RETURNING balance, frozen_balance
	`, amount, userID).Scan(&balance, &frozenBalance)
	return balance, frozenBalance, err
}

func updateUnifiedGatewayUserForDirectCapture(ctx context.Context, tx *sql.Tx, userID int64, amount float64) (balance, frozenBalance float64, err error) {
	err = tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance - $1,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
			AND balance >= $1
			AND COALESCE(frozen_balance, 0) >= 0
		RETURNING balance, frozen_balance
	`, amount, userID).Scan(&balance, &frozenBalance)
	return balance, frozenBalance, err
}

func settleUnifiedGatewayUserCapture(ctx context.Context, tx *sql.Tx, userID int64, reservedAmount, capturedAmount float64) (balance, frozenBalance float64, err error) {
	err = tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance + $1,
			frozen_balance = COALESCE(frozen_balance, 0) - $2,
			updated_at = NOW()
		WHERE id = $3 AND deleted_at IS NULL
			AND COALESCE(frozen_balance, 0) >= $2
			AND balance + $1 >= 0
		RETURNING balance, frozen_balance
	`, reservedAmount-capturedAmount, reservedAmount, userID).Scan(&balance, &frozenBalance)
	return balance, frozenBalance, err
}

func releaseUnifiedGatewayReservedBalance(ctx context.Context, tx *sql.Tx, userID int64, reservedAmount float64) (balance, frozenBalance float64, err error) {
	err = tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance + $1,
			frozen_balance = COALESCE(frozen_balance, 0) - $1,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
			AND COALESCE(frozen_balance, 0) >= $1
			AND balance + $1 >= 0
		RETURNING balance, frozen_balance
	`, reservedAmount, userID).Scan(&balance, &frozenBalance)
	return balance, frozenBalance, err
}

func refundUnifiedGatewayCapturedBalance(ctx context.Context, tx *sql.Tx, userID int64, capturedAmount float64) (balance, frozenBalance float64, err error) {
	err = tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance + $1,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
			AND COALESCE(frozen_balance, 0) >= 0
			AND balance + $1 >= 0
		RETURNING balance, frozen_balance
	`, capturedAmount, userID).Scan(&balance, &frozenBalance)
	return balance, frozenBalance, err
}

func lookupUnifiedGatewayChargeLedgerEntry(ctx context.Context, tx *sql.Tx, key string) (*unifiedGatewayChargeLedgerEntry, bool, error) {
	var entry unifiedGatewayChargeLedgerEntry
	err := tx.QueryRowContext(ctx, `
		SELECT id, reservation_key, user_id, reserved_amount, captured_amount, status
		FROM unified_gateway_charge_ledger
		WHERE reservation_key = $1
		FOR UPDATE
	`, key).Scan(&entry.ID, &entry.ReservationKey, &entry.UserID, &entry.ReservedAmount, &entry.CapturedAmount, &entry.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if entry.ReservationKey == "" || entry.UserID <= 0 || !isFiniteNonNegativeLedgerAmount(entry.ReservedAmount) || !isFiniteNonNegativeLedgerAmount(entry.CapturedAmount) {
		return nil, false, fmt.Errorf("%w: ledger entry is invalid", service.ErrUnifiedGatewayInvalidRequest)
	}
	return &entry, true, nil
}

func replayUnifiedGatewayReserve(entry *unifiedGatewayChargeLedgerEntry, userID int64, amount, balance float64) (*service.UnifiedGatewayLedgerResult, error) {
	if entry == nil || entry.UserID != userID {
		return nil, fmt.Errorf("%w: reservation key belongs to another user", service.ErrUnifiedGatewaySnapshotConflict)
	}
	if !unifiedGatewayLedgerAmountsEqual(entry.ReservedAmount, amount) {
		return nil, fmt.Errorf("%w: reserve amount differs for an existing key", service.ErrUnifiedGatewaySnapshotConflict)
	}
	switch entry.Status {
	case unifiedGatewayChargeLedgerReserved, unifiedGatewayChargeLedgerCaptured, unifiedGatewayChargeLedgerReleased:
		return &service.UnifiedGatewayLedgerResult{Applied: false, Amount: entry.ReservedAmount, NewBalance: balance}, nil
	default:
		return nil, fmt.Errorf("%w: unsupported ledger status %q", service.ErrUnifiedGatewaySnapshotConflict, entry.Status)
	}
}

func markUnifiedGatewayLedgerCaptured(ctx context.Context, tx *sql.Tx, ledgerID int64, amount float64) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE unified_gateway_charge_ledger
		SET status = $2, captured_amount = $3, updated_at = NOW()
		WHERE id = $1 AND status = $4
	`, ledgerID, unifiedGatewayChargeLedgerCaptured, amount, unifiedGatewayChargeLedgerReserved)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("%w: capture state changed concurrently", service.ErrUnifiedGatewaySnapshotConflict)
	}
	return nil
}

func markUnifiedGatewayLedgerReleased(ctx context.Context, tx *sql.Tx, ledgerID int64) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE unified_gateway_charge_ledger
		SET status = $2, updated_at = NOW()
		WHERE id = $1 AND status IN ($3, $4)
	`, ledgerID, unifiedGatewayChargeLedgerReleased, unifiedGatewayChargeLedgerReserved, unifiedGatewayChargeLedgerCaptured)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("%w: release state changed concurrently", service.ErrUnifiedGatewaySnapshotConflict)
	}
	return nil
}

func classifyUnifiedGatewayCaptureMiss(balance, frozenBalance, reservedAmount, capturedAmount float64) error {
	if !isFiniteNonNegativeLedgerAmount(reservedAmount) || !isFiniteNonNegativeLedgerAmount(capturedAmount) || !isFiniteNonNegativeLedgerAmount(frozenBalance) {
		return fmt.Errorf("%w: capture ledger state is invalid", service.ErrUnifiedGatewayInvalidRequest)
	}
	if frozenBalance+unifiedGatewayChargeLedgerAmountEpsilon < reservedAmount {
		return fmt.Errorf("%w: frozen balance is smaller than the reservation", service.ErrUnifiedGatewaySnapshotConflict)
	}
	if balance+(reservedAmount-capturedAmount) < -unifiedGatewayChargeLedgerAmountEpsilon {
		return service.ErrUnifiedGatewayBalanceInsufficient
	}
	return fmt.Errorf("%w: capture balance update did not apply", service.ErrUnifiedGatewaySnapshotConflict)
}

func classifyUnifiedGatewayReleaseMiss(balance, frozenBalance, reservedAmount float64) error {
	if !isFiniteNonNegativeLedgerAmount(reservedAmount) || !isFiniteNonNegativeLedgerAmount(frozenBalance) {
		return fmt.Errorf("%w: release ledger state is invalid", service.ErrUnifiedGatewayInvalidRequest)
	}
	if frozenBalance+unifiedGatewayChargeLedgerAmountEpsilon < reservedAmount {
		return fmt.Errorf("%w: frozen balance is smaller than the reservation", service.ErrUnifiedGatewaySnapshotConflict)
	}
	if balance+reservedAmount < -unifiedGatewayChargeLedgerAmountEpsilon {
		return fmt.Errorf("%w: release would create a negative balance", service.ErrUnifiedGatewaySnapshotConflict)
	}
	return fmt.Errorf("%w: release balance update did not apply", service.ErrUnifiedGatewaySnapshotConflict)
}
