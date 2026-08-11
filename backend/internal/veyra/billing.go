package veyra

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var (
	ErrDebitInvalidRequest       = errors.New("veyra debit: invalid request")
	ErrDebitIdempotencyRequired  = errors.New("veyra debit: idempotency key required")
	ErrDebitIdempotencyConflict  = errors.New("veyra debit: idempotency conflict")
	ErrDebitAccountUnavailable   = errors.New("veyra debit: account service unavailable")
	ErrDebitUserNotFound         = errors.New("veyra debit: user not found")
	ErrDebitInactiveUser         = errors.New("veyra debit: inactive user")
	ErrDebitInsufficientBalance  = errors.New("veyra debit: insufficient balance")
	ErrDebitBalanceMutationError = errors.New("veyra debit: balance mutation failed")
)

type BalanceAccountService interface {
	GetByID(ctx context.Context, id int64) (*service.User, error)
	UpdateBalance(ctx context.Context, userID int64, amount float64) error
}

type AtomicBalanceDebitService interface {
	DebitBalanceIfSufficient(ctx context.Context, input service.UserBalanceDebitInput) (*service.UserBalanceDebitResult, error)
}

type DebitInput struct {
	UserID         int64
	Amount         float64
	IdempotencyKey string
	Source         string
	ReferenceID    string
}

type DebitResult struct {
	UserID         int64
	Amount         float64
	BalanceAfter   float64
	IdempotencyKey string
	Source         string
	ReferenceID    string
	Replayed       bool
}

type DebitLedger interface {
	Execute(ctx context.Context, input DebitInput, mutate func() (*DebitResult, error)) (*DebitResult, error)
}

type MemoryDebitLedger struct {
	mu      sync.Mutex
	records map[string]debitRecord
}

type debitRecord struct {
	Fingerprint string
	Result      *DebitResult
}

func NewMemoryDebitLedger() *MemoryDebitLedger {
	return &MemoryDebitLedger{records: make(map[string]debitRecord)}
}

func (l *MemoryDebitLedger) Execute(ctx context.Context, input DebitInput, mutate func() (*DebitResult, error)) (*DebitResult, error) {
	if l == nil {
		return nil, ErrDebitAccountUnavailable
	}
	key := strings.TrimSpace(input.IdempotencyKey)
	if key == "" {
		return nil, ErrDebitIdempotencyRequired
	}
	fingerprint := debitFingerprint(input)
	l.mu.Lock()
	defer l.mu.Unlock()
	if existing, ok := l.records[key]; ok {
		if existing.Fingerprint != fingerprint {
			return nil, ErrDebitIdempotencyConflict
		}
		result := cloneDebitResult(existing.Result)
		result.Replayed = true
		return result, nil
	}
	result, err := mutate()
	if err != nil {
		return nil, err
	}
	result.IdempotencyKey = key
	l.records[key] = debitRecord{Fingerprint: fingerprint, Result: cloneDebitResult(result)}
	return cloneDebitResult(result), nil
}

func DebitBalance(ctx context.Context, accounts BalanceAccountService, ledger DebitLedger, input DebitInput) (*DebitResult, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.Source = strings.TrimSpace(input.Source)
	input.ReferenceID = strings.TrimSpace(input.ReferenceID)
	if input.UserID <= 0 || input.Amount <= 0 || math.IsNaN(input.Amount) || math.IsInf(input.Amount, 0) {
		return nil, ErrDebitInvalidRequest
	}
	if input.IdempotencyKey == "" {
		return nil, ErrDebitIdempotencyRequired
	}
	if accounts == nil || ledger == nil {
		return nil, ErrDebitAccountUnavailable
	}
	if atomicAccounts, ok := accounts.(AtomicBalanceDebitService); ok && atomicAccounts != nil {
		result, err := atomicAccounts.DebitBalanceIfSufficient(ctx, service.UserBalanceDebitInput{
			UserID:             input.UserID,
			Amount:             input.Amount,
			IdempotencyKey:     input.IdempotencyKey,
			RequestFingerprint: debitFingerprint(input),
		})
		if err != nil {
			return nil, translateAtomicDebitError(err)
		}
		return &DebitResult{
			UserID:         result.UserID,
			Amount:         result.Amount,
			BalanceAfter:   result.BalanceAfter,
			IdempotencyKey: input.IdempotencyKey,
			Source:         input.Source,
			ReferenceID:    input.ReferenceID,
			Replayed:       result.Replayed,
		}, nil
	}
	return ledger.Execute(ctx, input, func() (*DebitResult, error) {
		user, err := accounts.GetByID(ctx, input.UserID)
		if err != nil || user == nil {
			return nil, ErrDebitUserNotFound
		}
		if !user.IsActive() {
			return nil, ErrDebitInactiveUser
		}
		if user.Balance+1e-9 < input.Amount {
			return nil, ErrDebitInsufficientBalance
		}
		oldBalance := user.Balance
		if err := accounts.UpdateBalance(ctx, input.UserID, -input.Amount); err != nil {
			return nil, ErrDebitBalanceMutationError
		}
		return &DebitResult{
			UserID:       input.UserID,
			Amount:       input.Amount,
			BalanceAfter: oldBalance - input.Amount,
			Source:       input.Source,
			ReferenceID:  input.ReferenceID,
		}, nil
	})
}

func translateAtomicDebitError(err error) error {
	switch {
	case errors.Is(err, service.ErrUserNotFound):
		return ErrDebitUserNotFound
	case errors.Is(err, service.ErrInsufficientBalance):
		return ErrDebitInsufficientBalance
	case errors.Is(err, service.ErrInsufficientPerms):
		return ErrDebitInactiveUser
	case errors.Is(err, service.ErrUsageBillingRequestConflict):
		return ErrDebitIdempotencyConflict
	case errors.Is(err, service.ErrIdempotencyInProgress):
		return ErrDebitIdempotencyConflict
	default:
		return err
	}
}

func debitFingerprint(input DebitInput) string {
	return strings.Join([]string{
		strings.TrimSpace(input.IdempotencyKey),
		formatInt64(input.UserID),
		formatFloat(input.Amount),
		strings.TrimSpace(input.Source),
		strings.TrimSpace(input.ReferenceID),
	}, "\x00")
}

func cloneDebitResult(result *DebitResult) *DebitResult {
	if result == nil {
		return nil
	}
	clone := *result
	return &clone
}

func formatInt64(v int64) string {
	return strconv.FormatInt(v, 10)
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 8, 64)
}
