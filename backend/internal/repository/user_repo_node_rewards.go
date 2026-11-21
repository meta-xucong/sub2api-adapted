package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *userRepository) GetNodeRewardSettlement(ctx context.Context, userID int64) (*service.VeyraNodeRewardResult, error) {
	result := &service.VeyraNodeRewardResult{UserID: userID}
	if userID <= 0 {
		return nil, service.ErrVeyraNodeRewardInvalid
	}
	var status string
	err := scanSingleRow(ctx, r.sql, `
		SELECT u.status, u.balance, COALESCE((
			SELECT max(redeemed_images) FROM veyra_node_reward_redemptions WHERE user_id = u.id
		), 0)
		FROM users u WHERE u.id = $1 AND u.deleted_at IS NULL
	`, []any{userID}, &status, &result.BalanceAfter, &result.RedeemedImages)
	if err := rewardAccountError(err, status); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *userRepository) SettleNodeRewards(ctx context.Context, userID, eligibleImages int64) (*service.VeyraNodeRewardResult, error) {
	if userID <= 0 || eligibleImages <= 0 || eligibleImages > service.MaxVeyraNodeRewardImages {
		return nil, service.ErrVeyraNodeRewardInvalid
	}
	db, ok := r.sql.(*sql.DB)
	if !ok {
		return nil, errors.New("node reward settlement requires *sql.DB")
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	result := &service.VeyraNodeRewardResult{UserID: userID}
	var status string
	err = tx.QueryRowContext(ctx, `
		SELECT status, balance FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE
	`, userID).Scan(&status, &result.BalanceAfter)
	if err := rewardAccountError(err, status); err != nil {
		return nil, err
	}

	// Take this snapshot after acquiring the account row lock, so a waiter
	// sees the last committed settlement before it calculates the difference.
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(max(redeemed_images), 0)
		FROM veyra_node_reward_redemptions WHERE user_id = $1
	`, userID).Scan(&result.RedeemedImages)
	if err != nil {
		return nil, err
	}
	if eligibleImages > result.RedeemedImages {
		result.CreditedImages = eligibleImages - result.RedeemedImages
		result.RedeemedImages = eligibleImages
		err = tx.QueryRowContext(ctx, `
			WITH credited AS (
				UPDATE users SET balance = balance + $2::bigint * 0.02::numeric, updated_at = NOW()
				WHERE id = $1 RETURNING balance
			)
			INSERT INTO veyra_node_reward_redemptions (user_id, redeemed_images, credited_images, balance_after)
			SELECT $1, $3, $2, balance FROM credited RETURNING balance_after
		`, userID, result.CreditedImages, result.RedeemedImages).Scan(&result.BalanceAfter)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *userRepository) ListNodeRewardReceipts(ctx context.Context, userID, before int64, limit int) ([]service.VeyraNodeRewardReceipt, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT u.status, h.redeemed_images, h.credited_images, (h.credited_images * 0.02::numeric)::text,
		       h.balance_after::text, h.created_at
		FROM users u
		LEFT JOIN LATERAL (
			SELECT redeemed_images, credited_images, balance_after, created_at
			FROM veyra_node_reward_redemptions
			WHERE user_id = u.id AND ($2::bigint = 0 OR redeemed_images < $2)
			ORDER BY redeemed_images DESC
			LIMIT $3
		) h ON u.status = 'active'
		WHERE u.id = $1 AND u.deleted_at IS NULL
		ORDER BY h.redeemed_images DESC
	`, userID, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]service.VeyraNodeRewardReceipt, 0)
	var status string
	for rows.Next() {
		receipt, err := scanNodeRewardReceipt(rows, &status)
		if err := rewardAccountError(err, status); err != nil {
			return nil, err
		}
		if receipt != nil {
			items = append(items, *receipt)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if status == "" {
		return nil, service.ErrUserNotFound
	}
	return items, nil
}

func (r *userRepository) ListNodeRewardBalanceHistory(ctx context.Context, userID int64, offset, limit int) ([]service.VeyraNodeRewardReceipt, int64, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT totals.total, h.redeemed_images, h.credited_images, (h.credited_images * 0.02::numeric)::text,
		       h.balance_after::text, h.created_at
		FROM (SELECT COUNT(*) AS total FROM veyra_node_reward_redemptions WHERE user_id = $1) totals
		LEFT JOIN LATERAL (
			SELECT redeemed_images, credited_images, balance_after, created_at
			FROM veyra_node_reward_redemptions
			WHERE user_id = $1
			ORDER BY created_at DESC, redeemed_images DESC
			OFFSET $2 LIMIT $3
		) h ON true
		ORDER BY h.created_at DESC, h.redeemed_images DESC
	`, userID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]service.VeyraNodeRewardReceipt, 0)
	var total int64
	for rows.Next() {
		receipt, err := scanNodeRewardReceipt(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		if receipt != nil {
			items = append(items, *receipt)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func scanNodeRewardReceipt(rows *sql.Rows, prefix any) (*service.VeyraNodeRewardReceipt, error) {
	var redeemed, credited sql.NullInt64
	var amount, balanceAfter sql.NullString
	var createdAt sql.NullTime
	if err := rows.Scan(prefix, &redeemed, &credited, &amount, &balanceAfter, &createdAt); err != nil {
		return nil, err
	}
	if !redeemed.Valid {
		return nil, nil
	}
	return &service.VeyraNodeRewardReceipt{
		RedeemedImages: redeemed.Int64,
		CreditedImages: credited.Int64,
		Amount:         amount.String,
		BalanceAfter:   balanceAfter.String,
		CreatedAt:      createdAt.Time.UTC(),
	}, nil
}

func rewardAccountError(err error, status string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if status != service.StatusActive {
		return service.ErrVeyraNodeRewardInactive
	}
	return nil
}
