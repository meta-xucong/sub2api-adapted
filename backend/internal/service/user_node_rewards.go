package service

import (
	"context"
	"errors"
)

const MaxVeyraNodeRewardImages int64 = 1000000000

var (
	ErrVeyraNodeRewardInvalid  = errors.New("invalid node reward request")
	ErrVeyraNodeRewardInactive = errors.New("node reward account inactive")
)

type VeyraNodeRewardResult struct {
	UserID         int64   `json:"user_id"`
	RedeemedImages int64   `json:"redeemed_images"`
	CreditedImages int64   `json:"credited_images"`
	BalanceAfter   float64 `json:"balance_after"`
}

type nodeRewardRepository interface {
	GetNodeRewardSettlement(context.Context, int64) (*VeyraNodeRewardResult, error)
	SettleNodeRewards(context.Context, int64, int64) (*VeyraNodeRewardResult, error)
}

func (s *UserService) GetNodeRewardSettlement(ctx context.Context, userID int64) (*VeyraNodeRewardResult, error) {
	repository, ok := s.userRepo.(nodeRewardRepository)
	if !ok {
		return nil, errors.New("user repository does not support node rewards")
	}
	return repository.GetNodeRewardSettlement(ctx, userID)
}

func (s *UserService) SettleNodeRewards(ctx context.Context, userID, eligibleImages int64) (*VeyraNodeRewardResult, error) {
	repository, ok := s.userRepo.(nodeRewardRepository)
	if !ok {
		return nil, errors.New("user repository does not support node rewards")
	}
	result, err := repository.SettleNodeRewards(ctx, userID, eligibleImages)
	if err != nil {
		return nil, err
	}
	// Replays also invalidate caches. The original request may have lost its
	// response after commit and before its invalidations completed.
	s.invalidateBalanceCaches(context.WithoutCancel(ctx), userID)
	return result, nil
}
