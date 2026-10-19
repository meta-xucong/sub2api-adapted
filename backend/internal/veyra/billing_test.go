package veyra

import (
	"context"
	"math"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type balanceAccountStub struct {
	user          *service.User
	updateCalls   int
	updateAmounts []float64
	updateErr     error
}

func (s *balanceAccountStub) GetByID(context.Context, int64) (*service.User, error) {
	return s.user, nil
}

func (s *balanceAccountStub) UpdateBalance(_ context.Context, _ int64, amount float64) error {
	s.updateCalls++
	s.updateAmounts = append(s.updateAmounts, amount)
	if s.updateErr != nil {
		return s.updateErr
	}
	if s.user != nil {
		s.user.Balance += amount
	}
	return nil
}

func TestDebitBalanceDebitsOnceAndReplays(t *testing.T) {
	accounts := &balanceAccountStub{user: &service.User{ID: 42, Status: service.StatusActive, Balance: 10}}
	ledger := NewMemoryDebitLedger()
	input := DebitInput{
		UserID:         42,
		Amount:         3.25,
		IdempotencyKey: "run-1",
		Source:         "alchemy",
		ReferenceID:    "image-1",
	}

	first, err := DebitBalance(context.Background(), accounts, ledger, input)
	require.NoError(t, err)
	require.Equal(t, 6.75, first.BalanceAfter)
	require.False(t, first.Replayed)
	require.Equal(t, 1, accounts.updateCalls)
	require.Equal(t, []float64{-3.25}, accounts.updateAmounts)

	second, err := DebitBalance(context.Background(), accounts, ledger, input)
	require.NoError(t, err)
	require.True(t, second.Replayed)
	require.Equal(t, 6.75, second.BalanceAfter)
	require.Equal(t, 1, accounts.updateCalls)
}

func TestDebitBalanceRejectsInsufficientBalance(t *testing.T) {
	accounts := &balanceAccountStub{user: &service.User{ID: 42, Status: service.StatusActive, Balance: 1}}
	_, err := DebitBalance(context.Background(), accounts, NewMemoryDebitLedger(), DebitInput{
		UserID:         42,
		Amount:         2,
		IdempotencyKey: "run-2",
	})
	require.ErrorIs(t, err, ErrDebitInsufficientBalance)
	require.Equal(t, 0, accounts.updateCalls)
}

func TestDebitBalanceRejectsIdempotencyConflict(t *testing.T) {
	accounts := &balanceAccountStub{user: &service.User{ID: 42, Status: service.StatusActive, Balance: 10}}
	ledger := NewMemoryDebitLedger()
	_, err := DebitBalance(context.Background(), accounts, ledger, DebitInput{
		UserID:         42,
		Amount:         1,
		IdempotencyKey: "same-key",
	})
	require.NoError(t, err)

	_, err = DebitBalance(context.Background(), accounts, ledger, DebitInput{
		UserID:         42,
		Amount:         2,
		IdempotencyKey: "same-key",
	})
	require.ErrorIs(t, err, ErrDebitIdempotencyConflict)
	require.Equal(t, 1, accounts.updateCalls)
}

func TestDebitBalanceUsesAtomicServiceWhenAvailable(t *testing.T) {
	accounts := &atomicBalanceAccountStub{
		result: &service.UserBalanceDebitResult{
			UserID:       42,
			Amount:       2.5,
			BalanceAfter: 7.5,
			Replayed:     true,
		},
	}

	result, err := DebitBalance(context.Background(), accounts, NewMemoryDebitLedger(), DebitInput{
		UserID:         42,
		Amount:         2.5,
		IdempotencyKey: "run-atomic",
		Source:         "alchemy:v2",
		ReferenceID:    "job-1",
	})

	require.NoError(t, err)
	require.Len(t, accounts.inputs, 1)
	require.Equal(t, "run-atomic", accounts.inputs[0].IdempotencyKey)
	require.NotEmpty(t, accounts.inputs[0].RequestFingerprint)
	require.Equal(t, 0, accounts.updateCalls)
	require.True(t, result.Replayed)
	require.Equal(t, 7.5, result.BalanceAfter)
}

func TestDebitBalanceCanonicalizesAmountBeforeAtomicDebit(t *testing.T) {
	accounts := &atomicBalanceAccountStub{}
	input := DebitInput{UserID: 42, Amount: 0.000078125, IdempotencyKey: "rounded", Source: "alchemy", ReferenceID: "job-1"}
	result, err := DebitBalance(context.Background(), accounts, NewMemoryDebitLedger(), input)
	require.NoError(t, err)
	require.Len(t, accounts.inputs, 1)
	require.Equal(t, 0.00007813, accounts.inputs[0].Amount)
	input.Amount = 0.00007813
	require.Equal(t, debitFingerprint(input), accounts.inputs[0].RequestFingerprint)
	require.Equal(t, input.Amount, result.Amount)
	require.Zero(t, accounts.updateCalls)
}

func TestDebitBalanceCanonicalAmountControlsReplayAndBalanceCheck(t *testing.T) {
	accounts := &balanceAccountStub{user: &service.User{ID: 42, Status: service.StatusActive, Balance: 1}}
	ledger := NewMemoryDebitLedger()
	input := DebitInput{UserID: 42, Amount: 1.000000004, IdempotencyKey: "canonical-replay"}
	first, err := DebitBalance(context.Background(), accounts, ledger, input)
	require.NoError(t, err)
	require.Equal(t, 1.0, first.Amount)
	require.Zero(t, first.BalanceAfter)
	require.Equal(t, []float64{-1}, accounts.updateAmounts)

	input.Amount = 1
	replayed, err := DebitBalance(context.Background(), accounts, ledger, input)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, first.Amount, replayed.Amount)
	require.Equal(t, 1, accounts.updateCalls)

	input.Amount = 1.000000006
	_, err = DebitBalance(context.Background(), accounts, ledger, input)
	require.ErrorIs(t, err, ErrDebitIdempotencyConflict)
	require.Equal(t, 1, accounts.updateCalls)
}

func TestDebitBalanceRoundsHalfUnitToMinimumDebit(t *testing.T) {
	accounts := &balanceAccountStub{user: &service.User{ID: 42, Status: service.StatusActive, Balance: 0.00000001}}
	result, err := DebitBalance(context.Background(), accounts, NewMemoryDebitLedger(), DebitInput{
		UserID: 42, Amount: 0.000000005, IdempotencyKey: "minimum-unit",
	})
	require.NoError(t, err)
	require.Equal(t, 0.00000001, result.Amount)
	require.Zero(t, result.BalanceAfter)
	require.Equal(t, []float64{-0.00000001}, accounts.updateAmounts)
}

func TestDebitBalanceRejectsNonPositiveCanonicalAndNonFiniteAmounts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		amount float64
	}{
		{name: "zero", amount: 0},
		{name: "negative", amount: -1},
		{name: "rounds_to_zero", amount: 0.000000004},
		{name: "smallest_float", amount: math.SmallestNonzeroFloat64},
		{name: "nan", amount: math.NaN()},
		{name: "positive_infinity", amount: math.Inf(1)},
		{name: "negative_infinity", amount: math.Inf(-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accounts := &atomicBalanceAccountStub{}
			_, err := DebitBalance(context.Background(), accounts, NewMemoryDebitLedger(), DebitInput{
				UserID: 42, Amount: tc.amount, IdempotencyKey: "invalid-amount",
			})
			require.ErrorIs(t, err, ErrDebitInvalidRequest)
			require.Empty(t, accounts.inputs)
			require.Zero(t, accounts.updateCalls)
		})
	}
}

func TestDebitBalanceFingerprintBindsUserSourceAndReference(t *testing.T) {
	base := DebitInput{UserID: 42, Amount: 1, IdempotencyKey: "bound-key", Source: "alchemy", ReferenceID: "job-1"}
	for _, tc := range []struct {
		name   string
		change func(*DebitInput)
	}{
		{name: "user", change: func(input *DebitInput) { input.UserID = 43 }},
		{name: "source", change: func(input *DebitInput) { input.Source = "video" }},
		{name: "reference", change: func(input *DebitInput) { input.ReferenceID = "job-2" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accounts := &balanceAccountStub{user: &service.User{ID: 42, Status: service.StatusActive, Balance: 10}}
			ledger := NewMemoryDebitLedger()
			_, err := DebitBalance(context.Background(), accounts, ledger, base)
			require.NoError(t, err)
			changed := base
			tc.change(&changed)
			_, err = DebitBalance(context.Background(), accounts, ledger, changed)
			require.ErrorIs(t, err, ErrDebitIdempotencyConflict)
			require.Equal(t, 1, accounts.updateCalls)
		})
	}
}

type atomicBalanceAccountStub struct {
	updateCalls int
	result      *service.UserBalanceDebitResult
	err         error
	inputs      []service.UserBalanceDebitInput
}

func (s *atomicBalanceAccountStub) GetByID(context.Context, int64) (*service.User, error) {
	return &service.User{ID: 42, Status: service.StatusActive, Balance: 10}, nil
}

func (s *atomicBalanceAccountStub) UpdateBalance(context.Context, int64, float64) error {
	s.updateCalls++
	return nil
}

func (s *atomicBalanceAccountStub) DebitBalanceIfSufficient(_ context.Context, input service.UserBalanceDebitInput) (*service.UserBalanceDebitResult, error) {
	s.inputs = append(s.inputs, input)
	if s.err != nil {
		return nil, s.err
	}
	if s.result != nil {
		return s.result, nil
	}
	return &service.UserBalanceDebitResult{
		UserID:       input.UserID,
		Amount:       input.Amount,
		BalanceAfter: 7.5,
	}, nil
}
