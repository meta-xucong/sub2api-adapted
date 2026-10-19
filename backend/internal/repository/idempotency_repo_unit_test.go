package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestIdempotencyRepoDeleteExpiredExcludesMonetaryScopeBeforeLimit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		limit     int
		wantLimit int
	}{
		{name: "explicit_batch", limit: 3, wantLimit: 3},
		{name: "default_batch", limit: 0, wantLimit: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			repo := &idempotencyRepository{sql: db}
			now := time.Now().UTC()
			mock.ExpectExec(regexp.QuoteMeta(`
				WITH victims AS (
					SELECT id
					FROM idempotency_records
					WHERE expires_at <= $1
						AND scope <> $3
					ORDER BY expires_at ASC
					LIMIT $2
				)
				DELETE FROM idempotency_records
				WHERE id IN (SELECT id FROM victims)
			`)).WithArgs(now, tc.wantLimit, veyraDebitIdempotencyScope).
				WillReturnResult(sqlmock.NewResult(0, 2))
			deleted, err := repo.DeleteExpired(context.Background(), now, tc.limit)
			require.NoError(t, err)
			require.EqualValues(t, 2, deleted)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
