package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

func TestMergeBalanceHistoryCodesIncludesAffiliateTransfersByDefault(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	older := now.Add(-2 * time.Hour)
	newer := now.Add(time.Hour)

	usedBy := int64(10)
	redeemCodes := []RedeemCode{
		{
			ID:        1,
			Type:      RedeemTypeBalance,
			Value:     8,
			Status:    StatusUsed,
			UsedBy:    &usedBy,
			UsedAt:    &now,
			CreatedAt: now,
		},
		{
			ID:        2,
			Type:      RedeemTypeConcurrency,
			Value:     1,
			Status:    StatusUsed,
			UsedBy:    &usedBy,
			UsedAt:    &older,
			CreatedAt: older,
		},
	}
	affiliateCodes := []RedeemCode{
		{
			ID:        -20,
			Type:      RedeemTypeAffiliateBalance,
			Value:     3.5,
			Status:    StatusUsed,
			UsedBy:    &usedBy,
			UsedAt:    &newer,
			CreatedAt: newer,
		},
	}

	got := mergeBalanceHistoryCodes(redeemCodes, affiliateCodes, nil, pagination.PaginationParams{
		Page:     1,
		PageSize: 2,
	})

	require.Len(t, got, 2)
	require.Equal(t, RedeemTypeAffiliateBalance, got[0].Type)
	require.Equal(t, RedeemTypeBalance, got[1].Type)
}

func TestMergeBalanceHistoryCodesPaginatesAfterCombiningSources(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	usedBy := int64(10)
	at := func(hours int) *time.Time {
		v := base.Add(time.Duration(hours) * time.Hour)
		return &v
	}

	got := mergeBalanceHistoryCodes(
		[]RedeemCode{
			{ID: 1, Type: RedeemTypeBalance, UsedBy: &usedBy, UsedAt: at(4), CreatedAt: *at(4)},
			{ID: 2, Type: RedeemTypeConcurrency, UsedBy: &usedBy, UsedAt: at(2), CreatedAt: *at(2)},
		},
		[]RedeemCode{
			{ID: -3, Type: RedeemTypeAffiliateBalance, UsedBy: &usedBy, UsedAt: at(3), CreatedAt: *at(3)},
			{ID: -4, Type: RedeemTypeAffiliateBalance, UsedBy: &usedBy, UsedAt: at(1), CreatedAt: *at(1)},
		},
		nil,
		pagination.PaginationParams{Page: 2, PageSize: 2},
	)

	require.Len(t, got, 2)
	require.Equal(t, RedeemTypeConcurrency, got[0].Type)
	require.Equal(t, int64(-4), got[1].ID)
}

type nodeRewardAdminHistoryRepositoryStub struct {
	UserRepository
	receipts []VeyraNodeRewardReceipt
}

func (s *nodeRewardAdminHistoryRepositoryStub) ListNodeRewardBalanceHistory(_ context.Context, userID int64, offset, limit int) ([]VeyraNodeRewardReceipt, int64, error) {
	if userID != 41 {
		return nil, 0, nil
	}
	total := len(s.receipts)
	if offset >= total {
		return nil, int64(total), nil
	}
	return s.receipts[offset:min(offset+limit, total)], int64(total), nil
}

type nodeRewardAdminRedeemRepoStub struct {
	RedeemCodeRepository
	at time.Time
}

func (s *nodeRewardAdminRedeemRepoStub) ListByUserPaginated(_ context.Context, _ int64, _ pagination.PaginationParams, _ string) ([]RedeemCode, *pagination.PaginationResult, error) {
	return []RedeemCode{{ID: 44, Type: RedeemTypeBalance, Value: 5, UsedAt: &s.at}}, &pagination.PaginationResult{Total: 1}, nil
}

func (*nodeRewardAdminRedeemRepoStub) SumPositiveBalanceByUser(context.Context, int64) (float64, error) {
	return 5, nil
}

func TestAdminBalanceHistoryMergesAndFiltersNodeRewardsWithoutChangingRechargeTotals(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	s := &adminServiceImpl{
		userRepo: &nodeRewardAdminHistoryRepositoryStub{
			receipts: []VeyraNodeRewardReceipt{
				{RedeemedImages: 47, CreditedImages: 3, Amount: "0.06", BalanceAfter: "6.00000001", CreatedAt: base.Add(time.Hour)},
				{RedeemedImages: 44, CreditedImages: 44, Amount: "0.88", BalanceAfter: "5.94000001", CreatedAt: base.Add(-time.Hour)},
			},
		},
		redeemCodeRepo: &nodeRewardAdminRedeemRepoStub{at: base},
	}
	ctx := context.Background()
	items, total, recharged, err := s.GetUserBalanceHistory(ctx, 41, 2, 2, "")
	require.NoError(t, err)
	require.Equal(t, int64(3), total)
	require.Equal(t, 5.0, recharged)
	require.Len(t, items, 1)
	require.Equal(t, RedeemTypeNodeReward, items[0].Type)
	require.Equal(t, "NODE-REWARD-44", items[0].Code)
	require.Equal(t, 0.88, items[0].Value)
	require.NotNil(t, items[0].BalanceAfter)
	require.Equal(t, "5.94000001", *items[0].BalanceAfter)
	items, total, recharged, err = s.GetUserBalanceHistory(ctx, 41, 2, 1, RedeemTypeNodeReward)
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	require.Equal(t, 5.0, recharged)
	require.Len(t, items, 1)
	require.Equal(t, int64(44), items[0].ID)
	items, total, _, err = s.GetUserBalanceHistory(ctx, 42, 1, 20, RedeemTypeNodeReward)
	require.NoError(t, err)
	require.Empty(t, items)
	require.Zero(t, total)
}
