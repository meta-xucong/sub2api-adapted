package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUserRepositoryDebitBalanceIfSufficientAppliesAtomicDebit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := newUserRepositoryWithSQL(nil, db)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO idempotency_records")).
		WithArgs("veyra.billing.debit", sqlmock.AnyArg(), sqlmock.AnyArg(), service.IdempotencyStatusProcessing, http.StatusOK).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(77)))
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE users")).
		WithArgs(2.5, int64(42), service.StatusActive).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(7.5))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE idempotency_records")).
		WithArgs(int64(77), service.IdempotencyStatusSucceeded, http.StatusOK, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	result, err := repo.DebitBalanceIfSufficient(context.Background(), service.UserBalanceDebitInput{
		UserID:             42,
		Amount:             2.5,
		IdempotencyKey:     "run-1",
		RequestFingerprint: "alchemy:v2\x00job-1",
	})

	require.NoError(t, err)
	require.Equal(t, int64(42), result.UserID)
	require.Equal(t, 2.5, result.Amount)
	require.Equal(t, 7.5, result.BalanceAfter)
	require.False(t, result.Replayed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepositoryDebitBalanceIfSufficientCanonicalizesDirectInput(t *testing.T) {
	for _, tc := range []struct {
		name      string
		amount    float64
		canonical float64
	}{
		{name: "half_boundary", amount: 0.000078125, canonical: 0.00007813},
		{name: "minimum_unit", amount: 0.000000005, canonical: 0.00000001},
		{name: "extra_precision", amount: 1.000000004, canonical: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			repo := newUserRepositoryWithSQL(nil, db)
			want := service.UserBalanceDebitResult{UserID: 42, Amount: tc.canonical, BalanceAfter: 7.5}
			body, err := json.Marshal(want)
			require.NoError(t, err)

			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO idempotency_records")).
				WithArgs(veyraDebitIdempotencyScope, sqlmock.AnyArg(), sqlmock.AnyArg(), service.IdempotencyStatusProcessing, http.StatusOK).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(77)))
			mock.ExpectQuery(regexp.QuoteMeta("UPDATE users")).
				WithArgs(tc.canonical, int64(42), service.StatusActive).
				WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(want.BalanceAfter))
			mock.ExpectExec(regexp.QuoteMeta("UPDATE idempotency_records")).
				WithArgs(int64(77), service.IdempotencyStatusSucceeded, http.StatusOK, string(body)).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()

			result, err := repo.DebitBalanceIfSufficient(context.Background(), service.UserBalanceDebitInput{
				UserID: 42, Amount: tc.amount, IdempotencyKey: "canonical-input", RequestFingerprint: "canonical-request",
			})
			require.NoError(t, err)
			require.Equal(t, want, *result)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUserRepositoryDebitBalanceIfSufficientRejectsInvalidCanonicalAmountBeforeTransaction(t *testing.T) {
	for _, tc := range []struct {
		name   string
		amount float64
	}{
		{name: "zero", amount: 0},
		{name: "negative", amount: -1},
		{name: "rounds_to_zero", amount: 0.000000004},
		{name: "nan", amount: math.NaN()},
		{name: "infinite", amount: math.Inf(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			repo := newUserRepositoryWithSQL(nil, db)
			_, err = repo.DebitBalanceIfSufficient(context.Background(), service.UserBalanceDebitInput{
				UserID: 42, Amount: tc.amount, IdempotencyKey: "invalid-input",
			})
			require.ErrorIs(t, err, service.ErrUsageBillingRequestIDRequired)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUserRepositoryDebitBalanceIfSufficientReplaysSucceededRecord(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := newUserRepositoryWithSQL(nil, db)
	input := service.UserBalanceDebitInput{
		UserID:             42,
		Amount:             2.5,
		IdempotencyKey:     "run-1",
		RequestFingerprint: "alchemy:v2\x00job-1",
	}
	fingerprint := veyraDebitFingerprint(input.RequestFingerprint)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO idempotency_records")).
		WithArgs("veyra.billing.debit", sqlmock.AnyArg(), fingerprint, service.IdempotencyStatusProcessing, http.StatusOK).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT request_fingerprint, status, response_body")).
		WithArgs("veyra.billing.debit", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"request_fingerprint", "status", "response_body"}).
			AddRow(fingerprint, service.IdempotencyStatusSucceeded, `{"UserID":42,"Amount":2.5,"BalanceAfter":7.5,"Replayed":false}`))
	mock.ExpectCommit()

	result, err := repo.DebitBalanceIfSufficient(context.Background(), input)

	require.NoError(t, err)
	require.True(t, result.Replayed)
	require.Equal(t, 7.5, result.BalanceAfter)
	require.NoError(t, mock.ExpectationsWereMet())
}
