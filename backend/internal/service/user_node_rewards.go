package service

import (
	"context"
	"errors"
	"time"
)

const MaxVeyraNodeRewardImages int64 = 1000000000

const (
	DefaultVeyraNodeRewardReceiptLimit = 20
	MaxVeyraNodeRewardReceiptLimit     = 50
)

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

type VeyraNodeRewardReceipt struct {
	RedeemedImages int64     `json:"redeemed_images"`
	CreditedImages int64     `json:"credited_images"`
	Amount         string    `json:"amount"`
	BalanceAfter   string    `json:"balance_after"`
	CreatedAt      time.Time `json:"created_at"`
}

type VeyraNodeRewardReceiptPage struct {
	Items                    []VeyraNodeRewardReceipt `json:"items"`
	NextBeforeRedeemedImages *int64                   `json:"next_before_redeemed_images"`
}

type nodeRewardRepository interface {
	GetNodeRewardSettlement(context.Context, int64) (*VeyraNodeRewardResult, error)
	SettleNodeRewards(context.Context, int64, int64) (*VeyraNodeRewardResult, error)
	ListNodeRewardReceipts(context.Context, int64, int64, int) ([]VeyraNodeRewardReceipt, error)
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

func (s *UserService) ListNodeRewardReceipts(ctx context.Context, userID, before int64, limit int) (*VeyraNodeRewardReceiptPage, error) {
	if userID <= 0 || before < 0 || limit < 1 || limit > MaxVeyraNodeRewardReceiptLimit {
		return nil, ErrVeyraNodeRewardInvalid
	}
	repository, ok := s.userRepo.(nodeRewardRepository)
	if !ok {
		return nil, errors.New("user repository does not support node rewards")
	}
	items, err := repository.ListNodeRewardReceipts(ctx, userID, before, limit+1)
	if err != nil {
		return nil, err
	}
	page := &VeyraNodeRewardReceiptPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		cursor := items[limit-1].RedeemedImages
		page.NextBeforeRedeemedImages = &cursor
	}
	return page, nil
}
