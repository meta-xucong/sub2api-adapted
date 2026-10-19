//go:build integration

package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/veyra"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// These tests use the repository's isolated PostgreSQL integration harness.
// They never use configured application databases, provider calls, or money.
func newVeyraDebitIntegrationFixture(t *testing.T, balance float64) (context.Context, *userRepository, int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{
		Email:   "veyra-debit-" + uuid.NewString() + "@example.com",
		Balance: balance,
	})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1", user.ID)
		require.NoError(t, err)
	})
	return ctx, newUserRepositoryWithSQL(client, integrationDB), user.ID
}

func veyraDebitIntegrationKey(t *testing.T) string {
	t.Helper()
	key := "veyra-test-" + uuid.NewString()
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(),
			"DELETE FROM idempotency_records WHERE scope = $1 AND idempotency_key_hash = $2",
			veyraDebitIdempotencyScope, veyraDebitKeyHash(key))
		require.NoError(t, err)
	})
	return key
}

func requireVeyraDebitBalance(t *testing.T, ctx context.Context, userID int64, expected string) {
	t.Helper()
	var actual string
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance::text FROM users WHERE id = $1", userID).Scan(&actual))
	require.Equal(t, expected, actual, "compare the stored NUMERIC value without a float tolerance")
}

func requireVeyraDebitReceiptCount(t *testing.T, ctx context.Context, key string, expected int) {
	t.Helper()
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM idempotency_records WHERE scope = $1 AND idempotency_key_hash = $2",
		veyraDebitIdempotencyScope, veyraDebitKeyHash(key)).Scan(&count))
	require.Equal(t, expected, count)
}

func TestVeyraDebitPostgresExpiredReceiptSurvivesCleanupAndReplays(t *testing.T) {
	ctx, repo, userID := newVeyraDebitIntegrationFixture(t, 10)
	key := veyraDebitIntegrationKey(t)
	input := veyra.DebitInput{UserID: userID, Amount: 2.5, IdempotencyKey: key, Source: "video", ReferenceID: "task-1"}
	first, err := veyra.DebitBalance(ctx, repo, veyra.NewMemoryDebitLedger(), input)
	require.NoError(t, err)
	require.False(t, first.Replayed)

	// Make the monetary receipt older than the ordinary expired entry. A
	// batch of one must still clean the latter, rather than be starved by the
	// permanently retained financial record.
	_, err = integrationDB.ExecContext(ctx,
		"UPDATE idempotency_records SET expires_at = NOW() - INTERVAL '31 days' WHERE scope = $1 AND idempotency_key_hash = $2",
		veyraDebitIdempotencyScope, veyraDebitKeyHash(key))
	require.NoError(t, err)
	cleanupRepo := &idempotencyRepository{sql: integrationDB}
	ordinary := &service.IdempotencyRecord{
		Scope:              "veyra-test-ordinary-" + uuid.NewString(),
		IdempotencyKeyHash: veyraDebitKeyHash(uuid.NewString()),
		RequestFingerprint: veyraDebitFingerprint(uuid.NewString()),
		Status:             service.IdempotencyStatusSucceeded,
		ExpiresAt:          time.Now().Add(-time.Hour),
	}
	created, err := cleanupRepo.CreateProcessing(ctx, ordinary)
	require.NoError(t, err)
	require.True(t, created)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), "DELETE FROM idempotency_records WHERE id = $1", ordinary.ID)
		require.NoError(t, err)
	})

	deleted, err := cleanupRepo.DeleteExpired(ctx, time.Now().UTC(), 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)
	remaining, err := cleanupRepo.GetByScopeAndKeyHash(ctx, ordinary.Scope, ordinary.IdempotencyKeyHash)
	require.NoError(t, err)
	require.Nil(t, remaining, "ordinary expired scopes must still be deleted")
	requireVeyraDebitReceiptCount(t, ctx, key, 1)

	// A fresh repository/ledger also proves replay does not depend on memory.
	freshRepo := newUserRepositoryWithSQL(testEntClient(t), integrationDB)
	replayed, err := veyra.DebitBalance(ctx, freshRepo, veyra.NewMemoryDebitLedger(), input)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, first.Amount, replayed.Amount)
	require.Equal(t, first.BalanceAfter, replayed.BalanceAfter)
	requireVeyraDebitBalance(t, ctx, userID, "7.50000000")
}

func TestVeyraDebitPostgresConcurrentSameKeyChargesOnce(t *testing.T) {
	ctx, repo, userID := newVeyraDebitIntegrationFixture(t, 10)
	key := veyraDebitIntegrationKey(t)
	input := veyra.DebitInput{UserID: userID, Amount: 1.25, IdempotencyKey: key, Source: "video", ReferenceID: "task-1"}
	const attempts = 16
	type outcome struct {
		result *veyra.DebitResult
		err    error
	}
	start := make(chan struct{})
	results := make(chan outcome, attempts)
	for range attempts {
		go func() {
			<-start
			result, err := veyra.DebitBalance(ctx, repo, veyra.NewMemoryDebitLedger(), input)
			results <- outcome{result: result, err: err}
		}()
	}
	close(start)
	outcomes := make([]outcome, 0, attempts)
	for range attempts {
		outcomes = append(outcomes, <-results)
	}
	charged := 0
	for _, got := range outcomes {
		require.NoError(t, got.err)
		require.NotNil(t, got.result)
		require.Equal(t, 1.25, got.result.Amount)
		require.Equal(t, 8.75, got.result.BalanceAfter)
		if !got.result.Replayed {
			charged++
		}
	}
	require.Equal(t, 1, charged)
	requireVeyraDebitBalance(t, ctx, userID, "8.75000000")
	requireVeyraDebitReceiptCount(t, ctx, key, 1)
}

func TestVeyraDebitPostgresConcurrentDifferentKeysCannotOverdraw(t *testing.T) {
	ctx, repo, userID := newVeyraDebitIntegrationFixture(t, 10)
	keys := []string{veyraDebitIntegrationKey(t), veyraDebitIntegrationKey(t)}
	type outcome struct {
		key string
		err error
	}
	start := make(chan struct{})
	results := make(chan outcome, len(keys))
	for _, key := range keys {
		go func(key string) {
			<-start
			_, err := veyra.DebitBalance(ctx, repo, veyra.NewMemoryDebitLedger(), veyra.DebitInput{
				UserID: userID, Amount: 7, IdempotencyKey: key, Source: "video", ReferenceID: key,
			})
			results <- outcome{key: key, err: err}
		}(key)
	}
	close(start)
	outcomes := make([]outcome, 0, len(keys))
	for range keys {
		outcomes = append(outcomes, <-results)
	}
	succeeded, insufficient := 0, 0
	for _, got := range outcomes {
		switch {
		case got.err == nil:
			succeeded++
			requireVeyraDebitReceiptCount(t, ctx, got.key, 1)
		case errors.Is(got.err, veyra.ErrDebitInsufficientBalance):
			insufficient++
			requireVeyraDebitReceiptCount(t, ctx, got.key, 0)
		default:
			require.NoError(t, got.err)
		}
	}
	require.Equal(t, 1, succeeded)
	require.Equal(t, 1, insufficient)
	requireVeyraDebitBalance(t, ctx, userID, "3.00000000")
}

func TestVeyraDebitPostgresCanonicalPrecisionAndReplay(t *testing.T) {
	for _, tc := range []struct {
		name      string
		amount    float64
		canonical float64
		balance   string
	}{
		{name: "half_boundary", amount: 0.000078125, canonical: 0.00007813, balance: "9.99992187"},
		{name: "minimum_unit", amount: 0.000000005, canonical: 0.00000001, balance: "9.99999999"},
		{name: "canonical_equivalence", amount: 1.000000004, canonical: 1, balance: "9.00000000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, repo, userID := newVeyraDebitIntegrationFixture(t, 10)
			key := veyraDebitIntegrationKey(t)
			input := veyra.DebitInput{UserID: userID, Amount: tc.amount, IdempotencyKey: key, Source: "video", ReferenceID: "task-1"}
			first, err := veyra.DebitBalance(ctx, repo, veyra.NewMemoryDebitLedger(), input)
			require.NoError(t, err)
			require.Equal(t, tc.canonical, first.Amount)
			require.False(t, first.Replayed)
			requireVeyraDebitBalance(t, ctx, userID, tc.balance)

			input.Amount = tc.canonical
			replayed, err := veyra.DebitBalance(ctx, repo, veyra.NewMemoryDebitLedger(), input)
			require.NoError(t, err)
			require.True(t, replayed.Replayed)
			require.Equal(t, first.Amount, replayed.Amount)
			require.Equal(t, first.BalanceAfter, replayed.BalanceAfter)
			requireVeyraDebitBalance(t, ctx, userID, tc.balance)

			input.Amount = tc.canonical + 0.00000001
			_, err = veyra.DebitBalance(ctx, repo, veyra.NewMemoryDebitLedger(), input)
			require.ErrorIs(t, err, veyra.ErrDebitIdempotencyConflict)
			requireVeyraDebitReceiptCount(t, ctx, key, 1)
			requireVeyraDebitBalance(t, ctx, userID, tc.balance)
		})
	}
}

func TestVeyraDebitPostgresDirectRepositoryCanonicalizesAndRejectsZero(t *testing.T) {
	ctx, repo, userID := newVeyraDebitIntegrationFixture(t, 10)
	key := veyraDebitIntegrationKey(t)
	result, err := repo.DebitBalanceIfSufficient(ctx, service.UserBalanceDebitInput{
		UserID: userID, Amount: 0.000078125, IdempotencyKey: key, RequestFingerprint: "direct-canonical",
	})
	require.NoError(t, err)
	require.Equal(t, 0.00007813, result.Amount)
	requireVeyraDebitBalance(t, ctx, userID, "9.99992187")

	zeroKey := veyraDebitIntegrationKey(t)
	_, err = repo.DebitBalanceIfSufficient(ctx, service.UserBalanceDebitInput{
		UserID: userID, Amount: 0.000000004, IdempotencyKey: zeroKey,
	})
	require.ErrorIs(t, err, service.ErrUsageBillingRequestIDRequired)
	requireVeyraDebitReceiptCount(t, ctx, zeroKey, 0)
	requireVeyraDebitBalance(t, ctx, userID, "9.99992187")
}

func TestVeyraDebitPostgresFailedDebitRollsBackClaimAndCanRetry(t *testing.T) {
	ctx, repo, userID := newVeyraDebitIntegrationFixture(t, 1)
	key := veyraDebitIntegrationKey(t)
	input := veyra.DebitInput{UserID: userID, Amount: 2, IdempotencyKey: key, Source: "video", ReferenceID: "task-1"}
	_, err := veyra.DebitBalance(ctx, repo, veyra.NewMemoryDebitLedger(), input)
	require.ErrorIs(t, err, veyra.ErrDebitInsufficientBalance)
	requireVeyraDebitReceiptCount(t, ctx, key, 0)
	requireVeyraDebitBalance(t, ctx, userID, "1.00000000")

	_, err = integrationDB.ExecContext(ctx, "UPDATE users SET balance = 3 WHERE id = $1", userID)
	require.NoError(t, err)
	result, err := veyra.DebitBalance(ctx, repo, veyra.NewMemoryDebitLedger(), input)
	require.NoError(t, err)
	require.False(t, result.Replayed)
	requireVeyraDebitReceiptCount(t, ctx, key, 1)
	requireVeyraDebitBalance(t, ctx, userID, "1.00000000")
}
