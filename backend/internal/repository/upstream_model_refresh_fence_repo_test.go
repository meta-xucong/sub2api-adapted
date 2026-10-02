//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpstreamModelRefreshFenceIssueIncrementsPersistently(t *testing.T) {
	ctx := context.Background()
	accountID := createUpstreamModelRefreshFenceTestAccount(t)
	repo := newUpstreamModelRefreshFenceTestRepository(t)

	first, err := repo.IssueUpstreamModelRefreshToken(ctx, accountID)
	require.NoError(t, err)
	require.EqualValues(t, 1, first)
	latest, applied := readUpstreamModelRefreshFenceTokens(t, accountID)
	require.EqualValues(t, 1, latest)
	require.Zero(t, applied)

	second, err := repo.IssueUpstreamModelRefreshToken(ctx, accountID)
	require.NoError(t, err)
	require.EqualValues(t, 2, second)
	latest, applied = readUpstreamModelRefreshFenceTokens(t, accountID)
	require.EqualValues(t, 2, latest)
	require.Zero(t, applied)
}

func TestUpstreamModelRefreshFenceApplyAcceptsOnlyLatestToken(t *testing.T) {
	ctx := context.Background()
	accountID := createUpstreamModelRefreshFenceTestAccount(t)
	repo := newUpstreamModelRefreshFenceTestRepository(t)

	staleToken, err := repo.IssueUpstreamModelRefreshToken(ctx, accountID)
	require.NoError(t, err)
	latestToken, err := repo.IssueUpstreamModelRefreshToken(ctx, accountID)
	require.NoError(t, err)

	staleSnapshot := upstreamModelRefreshFenceTestSnapshot(staleToken, "stale-model")
	applied, err := repo.ApplyUpstreamModelRefreshSnapshot(ctx, accountID, staleToken, staleSnapshot)
	require.NoError(t, err)
	require.False(t, applied)
	assertUpstreamModelRefreshFenceState(t, accountID, latestToken, 0, "sentinel", "preserved", nil)

	currentSnapshot := upstreamModelRefreshFenceTestSnapshot(latestToken, "current-model")
	applied, err = repo.ApplyUpstreamModelRefreshSnapshot(ctx, accountID, latestToken, currentSnapshot)
	require.NoError(t, err)
	require.True(t, applied)
	assertUpstreamModelRefreshFenceState(t, accountID, latestToken, latestToken, "sentinel", "preserved", &currentSnapshot)

	// An older token remains a no-op after the latest snapshot has committed.
	applied, err = repo.ApplyUpstreamModelRefreshSnapshot(ctx, accountID, staleToken, staleSnapshot)
	require.NoError(t, err)
	require.False(t, applied)
	assertUpstreamModelRefreshFenceState(t, accountID, latestToken, latestToken, "sentinel", "preserved", &currentSnapshot)
}

func TestUpstreamModelRefreshFenceApplyRollsBackExtraWhenFenceUpdateFails(t *testing.T) {
	ctx := context.Background()
	accountID := createUpstreamModelRefreshFenceTestAccount(t)
	repo := newUpstreamModelRefreshFenceTestRepository(t)
	token, err := repo.IssueUpstreamModelRefreshToken(ctx, accountID)
	require.NoError(t, err)

	triggerName := fmt.Sprintf("upstream_model_refresh_fence_fail_%d", accountID)
	functionName := triggerName + "_fn"
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`
		CREATE FUNCTION public.%s() RETURNS trigger
		LANGUAGE plpgsql
		AS $body$
		BEGIN
			IF NEW.account_id = %d THEN
				RAISE EXCEPTION 'forced upstream model refresh fence update failure';
			END IF;
			RETURN NEW;
		END
		$body$;
	`, functionName, accountID))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, dropTriggerErr := integrationDB.ExecContext(context.Background(), fmt.Sprintf(
			`DROP TRIGGER IF EXISTS %s ON upstream_model_refresh_fences`, triggerName))
		require.NoError(t, dropTriggerErr)
		_, dropFunctionErr := integrationDB.ExecContext(context.Background(), fmt.Sprintf(
			`DROP FUNCTION IF EXISTS public.%s()`, functionName))
		require.NoError(t, dropFunctionErr)
	})
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`
		CREATE TRIGGER %s
		BEFORE UPDATE OF last_applied_token ON upstream_model_refresh_fences
		FOR EACH ROW EXECUTE FUNCTION public.%s();
	`, triggerName, functionName))
	require.NoError(t, err)

	snapshot := upstreamModelRefreshFenceTestSnapshot(token, "must-roll-back")
	applied, err := repo.ApplyUpstreamModelRefreshSnapshot(ctx, accountID, token, snapshot)
	require.Error(t, err)
	require.False(t, applied)
	assertUpstreamModelRefreshFenceState(t, accountID, token, 0, "sentinel", "preserved", nil)
}

func TestUpstreamModelPolicyPreviewApplyIsAtomicAndSingleUse(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	group := createUpstreamModelPolicyTestGroup(t, client)
	accountID := createUpstreamModelRefreshFenceTestAccount(t)
	account := loadUpstreamModelPolicyTestAccount(t, accountID)
	linkUpstreamModelPolicyTestAccount(t, account.ID, group.ID)
	repo := newUpstreamModelRefreshStateTestRepository(t)
	plan := upstreamModelPolicyPreviewTestPlan(fmt.Sprintf("policy-preview-single-use-%d", time.Now().UnixNano()), []*service.Account{account}, group)

	require.NoError(t, repo.StoreUpstreamModelPolicyPreview(ctx, plan))
	require.NoError(t, repo.ApplyUpstreamModelPolicyPreview(ctx, plan.PreviewID, plan.PlanHash, plan.AccountIDs, 1))
	assertUpstreamModelPolicyTestState(t, account.ID, "follow_upstream", plan.PreviewID, 1, true)

	err := repo.ApplyUpstreamModelPolicyPreview(ctx, plan.PreviewID, plan.PlanHash, plan.AccountIDs, 1)
	require.ErrorContains(t, err, "already consumed")
	assertUpstreamModelPolicyTestState(t, account.ID, "follow_upstream", plan.PreviewID, 1, true)
}

func TestUpstreamModelPolicyPreviewStaleRevisionDoesNotPartiallyApply(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	group := createUpstreamModelPolicyTestGroup(t, client)
	firstID := createUpstreamModelRefreshFenceTestAccount(t)
	secondID := createUpstreamModelRefreshFenceTestAccount(t)
	first := loadUpstreamModelPolicyTestAccount(t, firstID)
	second := loadUpstreamModelPolicyTestAccount(t, secondID)
	linkUpstreamModelPolicyTestAccount(t, first.ID, group.ID)
	linkUpstreamModelPolicyTestAccount(t, second.ID, group.ID)
	repo := newUpstreamModelRefreshStateTestRepository(t)
	plan := upstreamModelPolicyPreviewTestPlan(fmt.Sprintf("policy-preview-stale-revision-%d", time.Now().UnixNano()), []*service.Account{first, second}, group)

	require.NoError(t, repo.StoreUpstreamModelPolicyPreview(ctx, plan))
	_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET updated_at = updated_at + INTERVAL '1 second' WHERE id = $1`, second.ID)
	require.NoError(t, err)

	err = repo.ApplyUpstreamModelPolicyPreview(ctx, plan.PreviewID, plan.PlanHash, plan.AccountIDs, 1)
	require.ErrorContains(t, err, "changed after preview")
	assertUpstreamModelPolicyTestState(t, first.ID, "manual", plan.PreviewID, 0, false)
	assertUpstreamModelPolicyTestState(t, second.ID, "manual", plan.PreviewID, 0, false)
}

func createUpstreamModelPolicyTestGroup(t *testing.T, client *dbent.Client) *service.Group {
	t.Helper()
	group := mustCreateGroup(t, client, &service.Group{
		Name:     fmt.Sprintf("upstream-model-policy-%d", time.Now().UnixNano()),
		Platform: service.PlatformOpenAI,
	})
	err := integrationDB.QueryRowContext(context.Background(), `SELECT updated_at FROM groups WHERE id = $1`, group.ID).Scan(&group.UpdatedAt)
	require.NoError(t, err, "load policy preview test group revision")
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM groups WHERE id = $1`, group.ID)
		require.NoError(t, err, "delete policy preview test group")
	})
	return group
}

func loadUpstreamModelPolicyTestAccount(t *testing.T, accountID int64) *service.Account {
	t.Helper()
	account := &service.Account{ID: accountID}
	err := integrationDB.QueryRowContext(context.Background(), `SELECT updated_at FROM accounts WHERE id = $1`, accountID).Scan(&account.UpdatedAt)
	require.NoError(t, err, "load policy preview test account revision")
	return account
}

func linkUpstreamModelPolicyTestAccount(t *testing.T, accountID, groupID int64) {
	t.Helper()
	_, err := integrationDB.ExecContext(context.Background(), `INSERT INTO account_groups (account_id, group_id) VALUES ($1, $2)`, accountID, groupID)
	require.NoError(t, err, "link policy preview test account to group")
}

func upstreamModelPolicyPreviewTestPlan(previewID string, accounts []*service.Account, group *service.Group) service.UpstreamModelPolicyPreviewPlan {
	accountIDs := make([]int64, 0, len(accounts))
	revisions := make([]service.UpstreamModelPolicyAccountRevision, 0, len(accounts))
	for _, account := range accounts {
		accountIDs = append(accountIDs, account.ID)
		revisions = append(revisions, service.UpstreamModelPolicyAccountRevision{
			AccountID:       account.ID,
			UpdatedAt:       account.UpdatedAt,
			CurrentPolicy:   service.UpstreamModelPolicyManual,
			SourceProfileID: "openai-official",
			Eligible:        true,
			MappingDigest:   "mapping-digest",
			GroupRevisions: []service.UpstreamModelPolicyGroupRev{{
				GroupID:         group.ID,
				UpdatedAt:       group.UpdatedAt,
				AllowlistDigest: "allowlist-digest",
			}},
		})
	}
	return service.UpstreamModelPolicyPreviewPlan{
		PreviewID:  previewID,
		PlanHash:   "sha256:test-plan-hash",
		ExpiresAt:  time.Now().UTC().Add(10 * time.Minute),
		AccountIDs: accountIDs,
		Revisions:  revisions,
	}
}

func assertUpstreamModelPolicyTestState(t *testing.T, accountID int64, wantPolicy, previewID string, wantAudits int, wantConsumed bool) {
	t.Helper()
	var policy string
	err := integrationDB.QueryRowContext(context.Background(), `SELECT COALESCE(extra->>'upstream_model_policy', 'manual') FROM accounts WHERE id = $1`, accountID).Scan(&policy)
	require.NoError(t, err)
	require.Equal(t, wantPolicy, policy)
	var auditCount int
	err = integrationDB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM upstream_model_policy_audits WHERE preview_id = $1`, previewID).Scan(&auditCount)
	require.NoError(t, err)
	require.Equal(t, wantAudits, auditCount)
	var consumed bool
	err = integrationDB.QueryRowContext(context.Background(), `SELECT consumed_at IS NOT NULL FROM upstream_model_policy_previews WHERE preview_id = $1`, previewID).Scan(&consumed)
	require.NoError(t, err)
	require.Equal(t, wantConsumed, consumed)
}

func createUpstreamModelRefreshFenceTestAccount(t *testing.T) int64 {
	t.Helper()
	client := testEntClient(t)
	account := mustCreateAccount(t, client, &service.Account{
		Name:        fmt.Sprintf("upstream-model-refresh-fence-%d", time.Now().UnixNano()),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-refresh-fence-test"},
		Extra:       map[string]any{"sentinel": "preserved"},
	})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id = $1`, account.ID)
		require.NoError(t, err, "delete refresh fence test account")
	})
	return account.ID
}

func newUpstreamModelRefreshFenceTestRepository(t *testing.T) service.UpstreamModelRefreshFenceRepository {
	t.Helper()
	client := testEntClient(t)
	accountRepo := NewAccountRepository(client, integrationDB, nil)
	return NewUpstreamModelRefreshFenceRepository(client, accountRepo)
}

func newUpstreamModelRefreshStateTestRepository(t *testing.T) service.UpstreamModelRefreshStateRepository {
	t.Helper()
	client := testEntClient(t)
	accountRepo := NewAccountRepository(client, integrationDB, nil)
	repository := NewUpstreamModelRefreshFenceRepository(client, accountRepo)
	stateRepository, ok := repository.(service.UpstreamModelRefreshStateRepository)
	require.True(t, ok, "refresh repository must implement the policy preview state contract")
	return stateRepository
}

func upstreamModelRefreshFenceTestSnapshot(token int64, model string) service.UpstreamModelAvailabilitySnapshot {
	now := time.Now().UTC()
	return service.UpstreamModelAvailabilitySnapshot{
		SchemaVersion:   1,
		RefreshToken:    token,
		SourceProfileID: "test-profile",
		Status:          "fresh",
		LastAttemptAt:   now,
		NextDueAt:       now.Add(time.Hour),
		RawModels:       []string{model},
		PublicModels:    []string{model},
		PublicToUpstream: map[string]string{
			model: model,
		},
	}
}

func readUpstreamModelRefreshFenceTokens(t *testing.T, accountID int64) (latest, applied int64) {
	t.Helper()
	err := integrationDB.QueryRowContext(context.Background(), `
		SELECT latest_issued_token, last_applied_token
		FROM upstream_model_refresh_fences
		WHERE account_id = $1
	`, accountID).Scan(&latest, &applied)
	require.NoError(t, err)
	return latest, applied
}

func assertUpstreamModelRefreshFenceState(
	t *testing.T,
	accountID, latest, applied int64,
	sentinelKey, sentinelValue string,
	wantSnapshot *service.UpstreamModelAvailabilitySnapshot,
) {
	t.Helper()
	gotLatest, gotApplied := readUpstreamModelRefreshFenceTokens(t, accountID)
	require.Equal(t, latest, gotLatest)
	require.Equal(t, applied, gotApplied)

	var extraJSON []byte
	err := integrationDB.QueryRowContext(context.Background(), `SELECT extra FROM accounts WHERE id = $1`, accountID).Scan(&extraJSON)
	require.NoError(t, err)
	var extra map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(extraJSON, &extra))
	var sentinel string
	require.NoError(t, json.Unmarshal(extra[sentinelKey], &sentinel))
	require.Equal(t, sentinelValue, sentinel)
	if wantSnapshot == nil {
		require.NotContains(t, extra, service.UpstreamModelAvailabilityExtraKey)
		return
	}
	var gotSnapshot service.UpstreamModelAvailabilitySnapshot
	require.NoError(t, json.Unmarshal(extra[service.UpstreamModelAvailabilityExtraKey], &gotSnapshot))
	require.Equal(t, wantSnapshot.RefreshToken, gotSnapshot.RefreshToken)
	require.Equal(t, wantSnapshot.RawModels, gotSnapshot.RawModels)
}
