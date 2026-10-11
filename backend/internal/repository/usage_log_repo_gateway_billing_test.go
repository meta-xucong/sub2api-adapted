package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageLogRepositoryGetUnifiedGatewayUserBillingBreakdown(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)

	queryPattern := `(?s)SELECT\s+ul\.user_id,\s+COALESCE\(u\.username, ''\),\s+COALESCE\(u\.email, ''\),\s+ul\.account_id,\s+ul\.billing_type,\s+COALESCE\(SUM\(ul\.actual_cost\), 0\) AS actual_cost,\s+COALESCE\(SUM\(COALESCE\(ul\.account_stats_cost, ul\.total_cost\) \* COALESCE\(ul\.account_rate_multiplier, 1\)\), 0\) AS account_cost\s+FROM usage_logs ul\s+LEFT JOIN users u ON u\.id = ul\.user_id\s+WHERE ul\.group_id = \$1 AND ul\.created_at >= \$2 AND ul\.created_at < \$3\s+GROUP BY ul\.user_id, u\.username, u\.email, ul\.account_id, ul\.billing_type\s+ORDER BY ul\.user_id, ul\.account_id, ul\.billing_type`
	mock.ExpectQuery(queryPattern).
		WithArgs(int64(33), start, end).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "username", "email", "account_id", "billing_type", "actual_cost", "account_cost"}).
			AddRow(int64(4), "alice", "alice@example.com", int64(7), int8(service.BillingTypeBalance), 1.25, 0.8).
			AddRow(int64(5), "", "deleted@example.com", int64(9), int8(service.BillingTypeSubscription), 0.3, 0.1))

	got, err := repo.GetUnifiedGatewayUserBillingBreakdown(context.Background(), 33, start, end)
	require.NoError(t, err)
	require.Equal(t, []service.UnifiedGatewayUserBillingBreakdown{
		{UserID: 4, Username: "alice", Email: "alice@example.com", AccountID: 7, BillingType: service.BillingTypeBalance, ActualCost: 1.25, AccountCost: 0.8},
		{UserID: 5, Username: "", Email: "deleted@example.com", AccountID: 9, BillingType: service.BillingTypeSubscription, ActualCost: 0.3, AccountCost: 0.1},
	}, got)
	require.NoError(t, mock.ExpectationsWereMet())
}
