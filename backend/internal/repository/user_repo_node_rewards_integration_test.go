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
