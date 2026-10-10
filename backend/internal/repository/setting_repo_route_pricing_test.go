package repository

import (
	"context"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func TestSettingRepositoryCompareAndSetValueUsesAtomicPredicates(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	repo := NewSettingRepository(client).(*settingRepository)

	mock.ExpectExec(`UPDATE .*settings.*`).
		WithArgs("new", sqlmock.AnyArg(), "route-pricing", "old").
		WillReturnResult(sqlmock.NewResult(0, 1))
	updated, err := repo.CompareAndSetValue(context.Background(), "route-pricing", routePricingTestStringPtr("old"), "new")
	require.NoError(t, err)
	require.True(t, updated)

	insertQuery := `INSERT INTO settings \(key, value, updated_at\)\s+VALUES \(\$1, \$2, \$3\)\s+ON CONFLICT \(key\) DO NOTHING`
	mock.ExpectExec(insertQuery).
		WithArgs("route-pricing", "initial", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	inserted, err := repo.CompareAndSetValue(context.Background(), "route-pricing", nil, "initial")
	require.NoError(t, err)
	require.True(t, inserted)

	// A competing initial insert can leave the same value in storage, but the
	// caller that lost ON CONFLICT DO NOTHING must still report no update.
	mock.ExpectExec(insertQuery).
		WithArgs("route-pricing", "initial", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	inserted, err = repo.CompareAndSetValue(context.Background(), "route-pricing", nil, "initial")
	require.NoError(t, err)
	require.False(t, inserted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func routePricingTestStringPtr(value string) *string { return &value }
