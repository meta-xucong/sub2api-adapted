package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type upstreamModelRefreshFenceRepository struct {
	client      *dbent.Client
	accountRepo service.AccountRepository
}

func NewUpstreamModelRefreshFenceRepository(client *dbent.Client, accountRepo service.AccountRepository) service.UpstreamModelRefreshFenceRepository {
	return &upstreamModelRefreshFenceRepository{client: client, accountRepo: accountRepo}
}

func (r *upstreamModelRefreshFenceRepository) IssueUpstreamModelRefreshToken(ctx context.Context, accountID int64) (int64, error) {
	if r == nil || r.client == nil || accountID <= 0 {
		return 0, errors.New("upstream model refresh fence repository is not configured")
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	client := clientFromContext(txCtx, r.client)
	rows, err := client.QueryContext(txCtx, `
		INSERT INTO upstream_model_refresh_fences (account_id, latest_issued_token, last_applied_token)
		VALUES ($1, 1, 0)
		ON CONFLICT (account_id) DO UPDATE
		SET latest_issued_token = upstream_model_refresh_fences.latest_issued_token + 1,
		    updated_at = NOW()
		RETURNING latest_issued_token`, accountID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, err
		}
		return 0, sql.ErrNoRows
	}
	var token int64
	if err := rows.Scan(&token); err != nil {
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return token, nil
}

func (r *upstreamModelRefreshFenceRepository) ApplyUpstreamModelRefreshSnapshot(ctx context.Context, accountID, token int64, snapshot service.UpstreamModelAvailabilitySnapshot) (bool, error) {
	if r == nil || r.client == nil || r.accountRepo == nil || accountID <= 0 || token <= 0 {
		return false, errors.New("upstream model refresh fence repository is not configured")
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return false, err
	}
	if len(encoded) > 2<<20 {
		return false, errors.New("upstream model availability snapshot exceeds 2 MiB")
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	client := clientFromContext(txCtx, r.client)
	rows, err := client.QueryContext(txCtx, `
		SELECT latest_issued_token, last_applied_token
		FROM upstream_model_refresh_fences
		WHERE account_id = $1
		FOR UPDATE`, accountID)
	if err != nil {
		return false, err
	}
	var latest, applied int64
	if !rows.Next() {
		rowsErr := rows.Err()
		_ = rows.Close()
		if rowsErr != nil {
			return false, rowsErr
		}
		return false, nil
	}
	if err := rows.Scan(&latest, &applied); err != nil {
		_ = rows.Close()
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	if latest != token || token <= applied {
		return false, nil
	}
	if err := r.accountRepo.UpdateExtra(txCtx, accountID, map[string]any{service.UpstreamModelAvailabilityExtraKey: snapshot}); err != nil {
		return false, err
	}
	result, err := client.ExecContext(txCtx, `
		UPDATE upstream_model_refresh_fences
		SET last_applied_token = $1, updated_at = NOW()
		WHERE account_id = $2 AND latest_issued_token = $1 AND last_applied_token < $1`, token, accountID)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if updated != 1 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *upstreamModelRefreshFenceRepository) StoreUpstreamModelPolicyPreview(ctx context.Context, plan service.UpstreamModelPolicyPreviewPlan) error {
	if r == nil || r.client == nil || plan.PreviewID == "" || len(plan.AccountIDs) == 0 || len(plan.AccountIDs) > 100 || len(plan.AccountIDs) != len(plan.Revisions) {
		return errors.New("upstream model policy preview repository is not configured")
	}
	encodedPlan, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	if len(encodedPlan) > 1<<20 {
		return errors.New("upstream model policy preview exceeds 1 MiB")
	}
	encodedIDs, err := json.Marshal(plan.AccountIDs)
	if err != nil {
		return err
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	client := clientFromContext(txCtx, r.client)
	if _, err := client.ExecContext(txCtx, `DELETE FROM upstream_model_policy_previews WHERE expires_at <= NOW()`); err != nil {
		return err
	}
	if _, err := client.ExecContext(txCtx, `
		INSERT INTO upstream_model_policy_previews (preview_id, plan_hash, account_ids, plan, expires_at)
		VALUES ($1, $2, $3::jsonb, $4::jsonb, $5)`,
		plan.PreviewID, plan.PlanHash, string(encodedIDs), string(encodedPlan), plan.ExpiresAt.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *upstreamModelRefreshFenceRepository) ApplyUpstreamModelPolicyPreview(ctx context.Context, previewID, planHash string, confirmIDs []int64, actorID int64) error {
	if r == nil || r.client == nil || r.accountRepo == nil || previewID == "" || actorID <= 0 || len(confirmIDs) == 0 || len(confirmIDs) > 100 {
		return errors.New("upstream model policy repository is not configured")
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	client := clientFromContext(txCtx, r.client)
	rows, err := client.QueryContext(txCtx, `
		SELECT plan_hash, account_ids, plan
		FROM upstream_model_policy_previews
		WHERE preview_id = $1 AND expires_at > NOW() AND consumed_at IS NULL
		FOR UPDATE`, previewID)
	if err != nil {
		return err
	}
	if !rows.Next() {
		rowsErr := rows.Err()
		_ = rows.Close()
		if rowsErr != nil {
			return rowsErr
		}
		return errors.New("upstream model policy preview is missing, expired, or already consumed")
	}
	var storedHash string
	var storedIDsJSON, planJSON []byte
	if err := rows.Scan(&storedHash, &storedIDsJSON, &planJSON); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if storedHash != planHash {
		return errors.New("upstream model policy plan hash mismatch")
	}
	var storedIDs []int64
	var plan service.UpstreamModelPolicyPreviewPlan
	if err := json.Unmarshal(storedIDsJSON, &storedIDs); err != nil {
		return err
	}
	if err := json.Unmarshal(planJSON, &plan); err != nil {
		return err
	}
	ids := append([]int64(nil), confirmIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if !reflect.DeepEqual(storedIDs, ids) || !reflect.DeepEqual(plan.AccountIDs, ids) || len(plan.Revisions) != len(ids) {
		return errors.New("upstream model policy confirmation does not match the complete preview set")
	}
	for _, revision := range plan.Revisions {
		if !revision.Eligible || revision.AccountID <= 0 || revision.CurrentPolicy != service.UpstreamModelPolicyManual && revision.CurrentPolicy != service.UpstreamModelPolicyFollow {
			return errors.New("upstream model policy preview contains an ineligible account")
		}
	}

	groupRevisions := make(map[int64]service.UpstreamModelPolicyGroupRev)
	previousPolicies := make(map[string]string, len(plan.Revisions))
	for _, revision := range plan.Revisions {
		var updatedAt time.Time
		var currentPolicy string
		rows, err := client.QueryContext(txCtx, `
			SELECT updated_at, COALESCE(extra->>'upstream_model_policy', 'manual')
			FROM accounts WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, revision.AccountID)
		if err != nil {
			return err
		}
		if !rows.Next() {
			rowsErr := rows.Err()
			_ = rows.Close()
			if rowsErr != nil {
				return rowsErr
			}
			return fmt.Errorf("preview account %d is no longer available", revision.AccountID)
		}
		if err := rows.Scan(&updatedAt, &currentPolicy); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if !updatedAt.Equal(revision.UpdatedAt) || currentPolicy != revision.CurrentPolicy {
			return fmt.Errorf("preview account %d changed after preview", revision.AccountID)
		}
		previousPolicies[fmt.Sprint(revision.AccountID)] = currentPolicy
		groupRows, err := client.QueryContext(txCtx, `SELECT group_id FROM account_groups WHERE account_id = $1 ORDER BY group_id`, revision.AccountID)
		if err != nil {
			return err
		}
		var currentGroupIDs []int64
		for groupRows.Next() {
			var groupID int64
			if err := groupRows.Scan(&groupID); err != nil {
				_ = groupRows.Close()
				return err
			}
			currentGroupIDs = append(currentGroupIDs, groupID)
		}
		if err := groupRows.Err(); err != nil {
			_ = groupRows.Close()
			return err
		}
		if err := groupRows.Close(); err != nil {
			return err
		}
		expectedGroupIDs := make([]int64, 0, len(revision.GroupRevisions))
		for _, groupRevision := range revision.GroupRevisions {
			expectedGroupIDs = append(expectedGroupIDs, groupRevision.GroupID)
			groupRevisions[groupRevision.GroupID] = groupRevision
		}
		if !reflect.DeepEqual(currentGroupIDs, expectedGroupIDs) {
			return fmt.Errorf("preview account %d group membership changed", revision.AccountID)
		}
	}
	groupIDs := make([]int64, 0, len(groupRevisions))
	for groupID := range groupRevisions {
		groupIDs = append(groupIDs, groupID)
	}
	sort.Slice(groupIDs, func(i, j int) bool { return groupIDs[i] < groupIDs[j] })
	for _, groupID := range groupIDs {
		var updatedAt time.Time
		rows, err := client.QueryContext(txCtx, `SELECT updated_at FROM groups WHERE id = $1 FOR UPDATE`, groupID)
		if err != nil {
			return err
		}
		if !rows.Next() {
			rowsErr := rows.Err()
			_ = rows.Close()
			if rowsErr != nil {
				return rowsErr
			}
			return fmt.Errorf("preview group %d is no longer available", groupID)
		}
		if err := rows.Scan(&updatedAt); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if !updatedAt.Equal(groupRevisions[groupID].UpdatedAt) {
			return fmt.Errorf("preview group %d changed after preview", groupID)
		}
	}

	for _, revision := range plan.Revisions {
		if revision.CurrentPolicy == service.UpstreamModelPolicyFollow {
			continue
		}
		if err := r.accountRepo.UpdateExtra(txCtx, revision.AccountID, map[string]any{service.UpstreamModelPolicyExtraKey: service.UpstreamModelPolicyFollow}); err != nil {
			return err
		}
	}
	encodedIDs, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	encodedPrevious, err := json.Marshal(previousPolicies)
	if err != nil {
		return err
	}
	if _, err := client.ExecContext(txCtx, `
		INSERT INTO upstream_model_policy_audits (actor_user_id, preview_id, plan_hash, account_ids, previous_policies, policy)
		VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, 'follow_upstream')`, actorID, previewID, planHash, string(encodedIDs), string(encodedPrevious)); err != nil {
		return err
	}
	result, err := client.ExecContext(txCtx, `UPDATE upstream_model_policy_previews SET consumed_at = NOW() WHERE preview_id = $1 AND consumed_at IS NULL`, previewID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return errors.New("upstream model policy preview could not be consumed")
	}
	return tx.Commit()
}

func (r *upstreamModelRefreshFenceRepository) StartUpstreamModelRefreshRun(ctx context.Context, run service.UpstreamModelRefreshRunRecord) error {
	if r == nil || r.client == nil || run.RunID == "" {
		return errors.New("upstream model refresh status repository is not configured")
	}
	_, err := r.client.ExecContext(ctx, `
		INSERT INTO upstream_model_refresh_runs (run_id, status, scheduled_at, started_at)
		VALUES ($1, 'running', $2, $3)
		ON CONFLICT (run_id) DO NOTHING`, run.RunID, run.ScheduledAt.UTC(), run.StartedAt.UTC())
	return err
}

func (r *upstreamModelRefreshFenceRepository) FinishUpstreamModelRefreshRun(ctx context.Context, run service.UpstreamModelRefreshRunRecord) error {
	if r == nil || r.client == nil || run.RunID == "" || run.FinishedAt == nil {
		return errors.New("upstream model refresh status repository is not configured")
	}
	accounts, err := json.Marshal(run.Accounts)
	if err != nil {
		return err
	}
	_, err = r.client.ExecContext(ctx, `
		UPDATE upstream_model_refresh_runs
		SET status = $2, finished_at = $3, eligible = $4, succeeded = $5, failed = $6,
		    unsupported = $7, skipped = $8, accounts = $9::jsonb, error_kind = $10
		WHERE run_id = $1`, run.RunID, run.Status, run.FinishedAt.UTC(), run.Eligible, run.Succeeded, run.Failed, run.Unsupported, run.Skipped, string(accounts), run.ErrorKind)
	return err
}

func (r *upstreamModelRefreshFenceRepository) GetLastUpstreamModelRefreshRun(ctx context.Context) (service.UpstreamModelRefreshRunRecord, error) {
	if r == nil || r.client == nil {
		return service.UpstreamModelRefreshRunRecord{}, errors.New("upstream model refresh status repository is not configured")
	}
	var record service.UpstreamModelRefreshRunRecord
	var finishedAt sql.NullTime
	var accounts []byte
	rows, err := r.client.QueryContext(ctx, `
		SELECT run_id, status, scheduled_at, started_at, finished_at, eligible, succeeded, failed, unsupported, skipped, accounts, error_kind
		FROM upstream_model_refresh_runs ORDER BY started_at DESC LIMIT 1`)
	if err != nil {
		return service.UpstreamModelRefreshRunRecord{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return service.UpstreamModelRefreshRunRecord{}, err
		}
		return service.UpstreamModelRefreshRunRecord{}, service.ErrUpstreamModelRefreshRunNotFound
	}
	if err := rows.Scan(&record.RunID, &record.Status, &record.ScheduledAt, &record.StartedAt, &finishedAt, &record.Eligible, &record.Succeeded, &record.Failed, &record.Unsupported, &record.Skipped, &accounts, &record.ErrorKind); err != nil {
		return service.UpstreamModelRefreshRunRecord{}, err
	}
	if finishedAt.Valid {
		record.FinishedAt = &finishedAt.Time
	}
	if len(accounts) > 0 {
		if err := json.Unmarshal(accounts, &record.Accounts); err != nil {
			return service.UpstreamModelRefreshRunRecord{}, err
		}
	}
	return record, nil
}
