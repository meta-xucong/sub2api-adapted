package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageLogRepositoryGetAPIKeyAccountBillingBreakdown(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery("(?s)SELECT\\s+account_id,\\s+billing_type,\\s+COALESCE\\(SUM\\(actual_cost\\), 0\\) AS actual_cost,\\s+COALESCE\\(SUM\\(COALESCE\\(account_stats_cost, total_cost\\) \\* COALESCE\\(account_rate_multiplier, 1\\)\\), 0\\) AS account_cost\\s+FROM usage_logs\\s+WHERE api_key_id = \\$1 AND created_at >= \\$2 AND created_at < \\$3\\s+GROUP BY account_id, billing_type\\s+ORDER BY account_id, billing_type").
		WithArgs(int64(22), start, end).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "billing_type", "actual_cost", "account_cost"}).
			AddRow(int64(7), int8(service.BillingTypeBalance), 1.25, 0.8).
			AddRow(int64(7), int8(service.BillingTypeSubscription), 0.3, 0.1).
			AddRow(int64(99), int8(service.BillingTypeBalance), 2.0, 1.0))

	got, err := repo.GetAPIKeyAccountBillingBreakdown(context.Background(), 22, start, end)
	require.NoError(t, err)
	require.Equal(t, []service.AccountBillingBreakdown{
		{AccountID: 7, BillingType: service.BillingTypeBalance, ActualCost: 1.25, AccountCost: 0.8},
		{AccountID: 7, BillingType: service.BillingTypeSubscription, ActualCost: 0.3, AccountCost: 0.1},
		{AccountID: 99, BillingType: service.BillingTypeBalance, ActualCost: 2.0, AccountCost: 1.0},
	}, got)
	require.NoError(t, mock.ExpectationsWereMet())
}
