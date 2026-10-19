package veyra

import (
	"context"
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
