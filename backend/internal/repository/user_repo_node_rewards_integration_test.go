//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestNodeRewardPostgresConcurrentSettlementOnlyCreditsDifference(t *testing.T) {
	ctx, repo, userID := newVeyraDebitIntegrationFixture(t, 10)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(),
			"DELETE FROM veyra_node_reward_redemptions WHERE user_id = $1", userID)
		require.NoError(t, err)
	})
	start := make(chan struct{})
	type response struct {
		result *service.VeyraNodeRewardResult
		err    error
	}
	responses := make(chan response, 4)
	for _, target := range []int64{44, 44, 47, 12} {
		go func() {
			<-start
			result, err := repo.SettleNodeRewards(ctx, userID, target)
			responses <- response{result, err}
		}()
	}
	close(start)
	var credited int64
	for range 4 {
		out := <-responses
		require.NoError(t, out.err)
		credited += out.result.CreditedImages
	}
	require.Equal(t, int64(47), credited)
	requireVeyraDebitBalance(t, ctx, userID, "10.94000000")
	replayed, err := repo.SettleNodeRewards(ctx, userID, 44)
	require.NoError(t, err)
	require.Zero(t, replayed.CreditedImages)
	current, err := repo.GetNodeRewardSettlement(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, int64(47), current.RedeemedImages)
	requireVeyraDebitBalance(t, ctx, userID, "10.94000000")
}

func TestNodeRewardPostgresReceiptsKeepPrecisionAndOwnerScope(t *testing.T) {
	ctx, repo, userID := newVeyraDebitIntegrationFixture(t, 10)
	_, _, otherID := newVeyraDebitIntegrationFixture(t, 0)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(),
			"DELETE FROM veyra_node_reward_redemptions WHERE user_id = $1", userID)
		require.NoError(t, err)
	})
	_, err := integrationDB.ExecContext(ctx, "UPDATE users SET balance = 900719925474.11000001::numeric WHERE id = $1", userID)
	require.NoError(t, err)
	for _, target := range []int64{44, 47, 44} {
		_, err := repo.SettleNodeRewards(ctx, userID, target)
		require.NoError(t, err)
	}
	accounts := service.NewUserService(repo, nil, nil, nil)
	page, err := accounts.ListNodeRewardReceipts(ctx, userID, 0, 1)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.NotNil(t, page.NextBeforeRedeemedImages)
	require.Equal(t, int64(47), *page.NextBeforeRedeemedImages)
	require.Equal(t, int64(3), page.Items[0].CreditedImages)
	require.Equal(t, "0.06", page.Items[0].Amount)
	require.Equal(t, "900719925475.05000001", page.Items[0].BalanceAfter)
	require.False(t, page.Items[0].CreatedAt.IsZero())

	next, err := accounts.ListNodeRewardReceipts(ctx, userID, *page.NextBeforeRedeemedImages, 1)
	require.NoError(t, err)
	require.Len(t, next.Items, 1)
	require.Nil(t, next.NextBeforeRedeemedImages)
	require.Equal(t, int64(44), next.Items[0].CreditedImages)
	require.Equal(t, "0.88", next.Items[0].Amount)
	require.Equal(t, "900719925474.99000001", next.Items[0].BalanceAfter)
	empty, err := accounts.ListNodeRewardReceipts(ctx, otherID, 0, 20)
	require.NoError(t, err)
	require.NotNil(t, empty.Items)
	require.Empty(t, empty.Items)
	_, err = accounts.ListNodeRewardReceipts(ctx, -1, 0, 20)
	require.ErrorIs(t, err, service.ErrVeyraNodeRewardInvalid)

	_, err = integrationDB.ExecContext(ctx, "UPDATE users SET status = 'disabled' WHERE id = $1", userID)
	require.NoError(t, err)
	_, err = accounts.ListNodeRewardReceipts(ctx, userID, 0, 20)
	require.ErrorIs(t, err, service.ErrVeyraNodeRewardInactive)
	items, total, err := repo.ListNodeRewardBalanceHistory(ctx, userID, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	require.Equal(t, next.Items, items)
	items, total, err = repo.ListNodeRewardBalanceHistory(ctx, userID, 2, 1)
	require.NoError(t, err)
	require.Empty(t, items)
	require.Equal(t, int64(2), total)
}
