//go:build integration

package repository

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestUpstreamModelRefreshPersistenceAcrossRepositoryInstances(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, ApplyMigrations(ctx, integrationDB))

	account := mustCreateAccount(t, testEntClient(t), &service.Account{
		Name:        fmt.Sprintf("phase4-refresh-%d", time.Now().UnixNano()),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "phase4-integration-key", "base_url": "https://api.openai.com/v1"},
		Extra:       map[string]any{"unrelated": "preserved"},
	})
	group := mustCreateGroup(t, testEntClient(t), &service.Group{
		Name: fmt.Sprintf("phase4-refresh-group-%d", time.Now().UnixNano()), Platform: service.PlatformOpenAI,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_groups WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM groups WHERE id = $1", group.ID)
	})

	accountRepo1 := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	accountRepo2 := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	require.NoError(t, accountRepo1.BindGroups(ctx, account.ID, []int64{group.ID}))
	refreshRepo1 := NewUpstreamModelRefreshFenceRepository(integrationEntClient, accountRepo1)
	refreshRepo2 := NewUpstreamModelRefreshFenceRepository(integrationEntClient, accountRepo2)
	stateRepo1, ok := refreshRepo1.(service.UpstreamModelRefreshStateRepository)
	require.True(t, ok)
	stateRepo2, ok := refreshRepo2.(service.UpstreamModelRefreshStateRepository)
	require.True(t, ok)

	oldToken, err := refreshRepo1.IssueUpstreamModelRefreshToken(ctx, account.ID)
	require.NoError(t, err)
	latestToken, err := refreshRepo2.IssueUpstreamModelRefreshToken(ctx, account.ID)
	require.NoError(t, err)
	require.Greater(t, latestToken, oldToken)

	lastSuccess := time.Now().UTC().Truncate(time.Microsecond)
	snapshot := service.UpstreamModelAvailabilitySnapshot{
		SchemaVersion:    1,
		RefreshToken:     oldToken,
		SourceProfileID:  "openai",
		SourceIdentity:   "phase4-integration-source",
		Status:           "fresh",
		LastAttemptAt:    lastSuccess,
		LastSuccessAt:    &lastSuccess,
		NextDueAt:        lastSuccess.Add(24 * time.Hour),
		RawDigest:        "phase4-integration-digest",
		Normalizer:       "model-catalog-v2",
		RawModels:        []string{"gpt-5.6-sol"},
		PublicModels:     []string{"gpt-5.6-sol"},
		PublicToUpstream: map[string]string{"gpt-5.6-sol": "gpt-5.6-sol"},
	}
	applied, err := refreshRepo1.ApplyUpstreamModelRefreshSnapshot(ctx, account.ID, oldToken, snapshot)
	require.NoError(t, err)
	require.False(t, applied, "an older refresh owner cannot overwrite a newer token")
	snapshot.RefreshToken = latestToken
	applied, err = refreshRepo2.ApplyUpstreamModelRefreshSnapshot(ctx, account.ID, latestToken, snapshot)
	require.NoError(t, err)
	require.True(t, applied)
	var persistedLatestToken, persistedAppliedToken int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT latest_issued_token, last_applied_token FROM upstream_model_refresh_fences WHERE account_id = $1", account.ID,
	).Scan(&persistedLatestToken, &persistedAppliedToken))
	require.Equal(t, latestToken, persistedLatestToken)
	require.Equal(t, latestToken, persistedAppliedToken)

	// An older token remains a no-op after the latest snapshot has committed.
	staleReplay := snapshot
	staleReplay.RefreshToken = oldToken
	staleReplay.RawModels = []string{"stale-model"}
	staleReplay.PublicModels = []string{"stale-model"}
	applied, err = refreshRepo1.ApplyUpstreamModelRefreshSnapshot(ctx, account.ID, oldToken, staleReplay)
	require.NoError(t, err)
	require.False(t, applied)
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT latest_issued_token, last_applied_token FROM upstream_model_refresh_fences WHERE account_id = $1", account.ID,
	).Scan(&persistedLatestToken, &persistedAppliedToken))
	require.Equal(t, latestToken, persistedLatestToken)
	require.Equal(t, latestToken, persistedAppliedToken)

	// A newly constructed repository reads the committed snapshot, modeling a
	// service restart; no process-local cache participates in this readback.
	restartRepo := NewUpstreamModelRefreshFenceRepository(
		integrationEntClient,
		newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil),
	)
	restoredAccount, err := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil).GetByID(ctx, account.ID)
	require.NoError(t, err)
	restoredSnapshot := restoredAccount.GetUpstreamModelAvailabilitySnapshot()
	require.NotNil(t, restoredSnapshot)
	require.Equal(t, latestToken, restoredSnapshot.RefreshToken)
	require.Equal(t, []string{"gpt-5.6-sol"}, restoredSnapshot.PublicModels)
	require.Equal(t, "preserved", restoredAccount.Extra["unrelated"])

	previewID := fmt.Sprintf("phase4-preview-%d", time.Now().UnixNano())
	planHash := strings.Repeat("a", 64)
	currentAccount, err := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil).GetByID(ctx, account.ID)
	require.NoError(t, err)
	var currentGroupUpdatedAt time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT updated_at FROM groups WHERE id = $1", group.ID).Scan(&currentGroupUpdatedAt))
	plan := service.UpstreamModelPolicyPreviewPlan{
		PreviewID:  previewID,
		PlanHash:   planHash,
		ExpiresAt:  time.Now().UTC().Add(5 * time.Minute),
		AccountIDs: []int64{account.ID},
		Revisions: []service.UpstreamModelPolicyAccountRevision{{
			AccountID: account.ID, UpdatedAt: currentAccount.UpdatedAt,
			CurrentPolicy:   service.UpstreamModelPolicyManual,
			SourceProfileID: "openai", Eligible: true,
			GroupRevisions: []service.UpstreamModelPolicyGroupRev{{GroupID: group.ID, UpdatedAt: currentGroupUpdatedAt}},
		}},
	}
	require.NoError(t, stateRepo1.StoreUpstreamModelPolicyPreview(ctx, plan))
	require.NoError(t, stateRepo2.ApplyUpstreamModelPolicyPreview(ctx, previewID, planHash, []int64{account.ID}, 990_004))
	optedInAccount, err := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil).GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, service.UpstreamModelPolicyFollow, optedInAccount.GetUpstreamModelPolicy())

	runID := fmt.Sprintf("phase4-run-%d", time.Now().UnixNano())
	startedAt := time.Now().UTC().Truncate(time.Microsecond)
	run := service.UpstreamModelRefreshRunRecord{RunID: runID, ScheduledAt: startedAt, StartedAt: startedAt}
	require.NoError(t, stateRepo1.StartUpstreamModelRefreshRun(ctx, run))
	finishedAt := startedAt.Add(time.Second)
	run.Status = "completed"
	run.FinishedAt = &finishedAt
	run.Eligible = 1
	run.Succeeded = 1
	run.Accounts = []service.UpstreamModelAccountRunStatus{{AccountID: account.ID, Status: "succeeded", RawCount: 1, CanonicalCount: 1}}
	require.NoError(t, stateRepo1.FinishUpstreamModelRefreshRun(ctx, run))
	stateRestartRepo, ok := restartRepo.(service.UpstreamModelRefreshStateRepository)
	require.True(t, ok)
	lastRun, err := stateRestartRepo.GetLastUpstreamModelRefreshRun(ctx)
	require.NoError(t, err)
	require.Equal(t, runID, lastRun.RunID)
	require.Equal(t, "completed", lastRun.Status)
	require.Equal(t, 1, lastRun.Succeeded)
	require.Len(t, lastRun.Accounts, 1)
	require.Equal(t, account.ID, lastRun.Accounts[0].AccountID)

	// Exercise fencing and durable readback in a separate OS process against
	// the same disposable PostgreSQL database, not only new repository values.
	childRunID := fmt.Sprintf("phase4-child-run-%d", time.Now().UnixNano())
	backendDir, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	goBinary, err := exec.LookPath("go")
	require.NoError(t, err)
	cmd := exec.Command(goBinary, "test", "-tags=integration", "-v", "./internal/repository/upstreamrefreshprobe", "-run", "^TestRefreshPersistenceChildProcess$", "-count=1")
	cmd.Dir = backendDir
	cmd.Env = append(os.Environ(),
		"SUB2API_PHASE4_REFRESH_DSN="+integrationDSN,
		"SUB2API_PHASE4_REFRESH_ACCOUNT="+fmt.Sprint(account.ID),
		"SUB2API_PHASE4_REFRESH_RUN_ID="+childRunID,
	)
	output, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "fresh-process refresh fencing/readback failed:\n%s", output)
	require.Containsf(t, string(output), "Phase 4 refresh state verified from a fresh process", "child-process assertion must execute:\n%s", output)

	postChildAccount, err := accountRepo1.GetByID(ctx, account.ID)
	require.NoError(t, err)
	postChildSnapshot := postChildAccount.GetUpstreamModelAvailabilitySnapshot()
	require.NotNil(t, postChildSnapshot)
	require.Greater(t, postChildSnapshot.RefreshToken, latestToken, "the child process must issue and persist a newer fencing token")
	require.Equal(t, []string{"gpt-5.6-terra"}, postChildSnapshot.PublicModels, "the child process must persist the winning catalog snapshot")
	childRun, err := stateRepo1.GetLastUpstreamModelRefreshRun(ctx)
	require.NoError(t, err)
	require.Equal(t, childRunID, childRun.RunID)
	require.Equal(t, "completed", childRun.Status)
}

func TestUpstreamModelRefreshSnapshotApplyRollsBackExtraWhenFenceUpdateFails(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, ApplyMigrations(ctx, integrationDB))
	account := mustCreateAccount(t, testEntClient(t), &service.Account{
		Name:        fmt.Sprintf("phase4-refresh-rollback-%d", time.Now().UnixNano()),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "phase4-rollback-key", "base_url": "https://api.openai.com/v1"},
		Extra:       map[string]any{"unrelated": "preserved"},
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", account.ID)
	})

	accountRepo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	repo := NewUpstreamModelRefreshFenceRepository(integrationEntClient, accountRepo)
	token, err := repo.IssueUpstreamModelRefreshToken(ctx, account.ID)
	require.NoError(t, err)

	triggerName := fmt.Sprintf("phase4_fence_fail_%d", account.ID)
	functionName := triggerName + "_fn"
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`
		CREATE FUNCTION public.%s() RETURNS trigger
		LANGUAGE plpgsql
		AS $body$
		BEGIN
			IF NEW.account_id = %d THEN
				RAISE EXCEPTION 'forced phase4 fence update failure';
			END IF;
			RETURN NEW;
		END
		$body$;
	`, functionName, account.ID))
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

	snapshot := service.UpstreamModelAvailabilitySnapshot{
		SchemaVersion:   1,
		RefreshToken:    token,
		SourceProfileID: "openai",
		Status:          "fresh",
		RawModels:       []string{"gpt-5.6-sol"},
		PublicModels:    []string{"gpt-5.6-sol"},
	}
	applied, err := repo.ApplyUpstreamModelRefreshSnapshot(ctx, account.ID, token, snapshot)
	require.Error(t, err)
	require.False(t, applied)
	var latestToken, appliedToken int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT latest_issued_token, last_applied_token FROM upstream_model_refresh_fences WHERE account_id = $1", account.ID,
	).Scan(&latestToken, &appliedToken))
	require.Equal(t, token, latestToken)
	require.Zero(t, appliedToken, "failed application must not advance the persistent fence")

	unchanged, err := accountRepo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, "preserved", unchanged.Extra["unrelated"])
	require.Nil(t, unchanged.GetUpstreamModelAvailabilitySnapshot(), "failed fencing must roll back the preceding account extra update")
}

func TestUpstreamModelPolicyPreviewApplyIsSingleUse(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, ApplyMigrations(ctx, integrationDB))
	account := mustCreateAccount(t, testEntClient(t), &service.Account{
		Name:        fmt.Sprintf("phase4-policy-single-use-%d", time.Now().UnixNano()),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "phase4-single-use-key", "base_url": "https://api.openai.com/v1"},
	})
	group := mustCreateGroup(t, testEntClient(t), &service.Group{
		Name: fmt.Sprintf("phase4-policy-single-use-group-%d", time.Now().UnixNano()), Platform: service.PlatformOpenAI,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_groups WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM groups WHERE id = $1", group.ID)
	})
	accountRepo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	require.NoError(t, accountRepo.BindGroups(ctx, account.ID, []int64{group.ID}))
	currentAccount, err := accountRepo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	var groupUpdatedAt time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT updated_at FROM groups WHERE id = $1", group.ID).Scan(&groupUpdatedAt))

	previewID := fmt.Sprintf("phase4-single-use-preview-%d", account.ID)
	planHash := strings.Repeat("b", 64)
	plan := service.UpstreamModelPolicyPreviewPlan{
		PreviewID: previewID, PlanHash: planHash, ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
		AccountIDs: []int64{account.ID},
		Revisions: []service.UpstreamModelPolicyAccountRevision{{
			AccountID: account.ID, UpdatedAt: currentAccount.UpdatedAt,
			CurrentPolicy: service.UpstreamModelPolicyManual, SourceProfileID: "openai", Eligible: true,
			GroupRevisions: []service.UpstreamModelPolicyGroupRev{{GroupID: group.ID, UpdatedAt: groupUpdatedAt}},
		}},
	}
	refreshRepo := NewUpstreamModelRefreshFenceRepository(integrationEntClient, accountRepo)
	stateRepo := refreshRepo.(service.UpstreamModelRefreshStateRepository)
	require.NoError(t, stateRepo.StoreUpstreamModelPolicyPreview(ctx, plan))
	require.NoError(t, stateRepo.ApplyUpstreamModelPolicyPreview(ctx, previewID, planHash, []int64{account.ID}, 990_006))
	require.ErrorContains(t, stateRepo.ApplyUpstreamModelPolicyPreview(ctx, previewID, planHash, []int64{account.ID}, 990_006), "already consumed")

	updated, err := accountRepo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, service.UpstreamModelPolicyFollow, updated.GetUpstreamModelPolicy())
	var auditCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM upstream_model_policy_audits WHERE preview_id = $1", previewID).Scan(&auditCount))
	require.Equal(t, 1, auditCount, "replaying a consumed preview must not duplicate the audit record")
}

func TestUpstreamModelPolicyPreviewStaleSecondAccountDoesNotPartiallyApply(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, ApplyMigrations(ctx, integrationDB))
	first := mustCreateAccount(t, testEntClient(t), &service.Account{
		Name:        fmt.Sprintf("phase4-policy-stale-first-%d", time.Now().UnixNano()),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "phase4-stale-first", "base_url": "https://api.openai.com/v1"},
	})
	second := mustCreateAccount(t, testEntClient(t), &service.Account{
		Name:        fmt.Sprintf("phase4-policy-stale-second-%d", time.Now().UnixNano()),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "phase4-stale-second", "base_url": "https://api.openai.com/v1"},
	})
	group := mustCreateGroup(t, testEntClient(t), &service.Group{
		Name: fmt.Sprintf("phase4-policy-stale-group-%d", time.Now().UnixNano()), Platform: service.PlatformOpenAI,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_groups WHERE account_id = ANY($1)", pq.Array([]int64{first.ID, second.ID}))
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = ANY($1)", pq.Array([]int64{first.ID, second.ID}))
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM groups WHERE id = $1", group.ID)
	})

	accountRepo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	for _, account := range []*service.Account{first, second} {
		require.NoError(t, accountRepo.BindGroups(ctx, account.ID, []int64{group.ID}))
	}
	accounts := []*service.Account{first, second}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	revisions := make([]service.UpstreamModelPolicyAccountRevision, 0, len(accounts))
	accountIDs := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		current, err := accountRepo.GetByID(ctx, account.ID)
		require.NoError(t, err)
		accountIDs = append(accountIDs, account.ID)
		revisions = append(revisions, service.UpstreamModelPolicyAccountRevision{
			AccountID: account.ID, UpdatedAt: current.UpdatedAt,
			CurrentPolicy: service.UpstreamModelPolicyManual, SourceProfileID: "openai", Eligible: true,
			GroupRevisions: []service.UpstreamModelPolicyGroupRev{{GroupID: group.ID, UpdatedAt: current.UpdatedAt}},
		})
	}
	var groupUpdatedAt time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT updated_at FROM groups WHERE id = $1", group.ID).Scan(&groupUpdatedAt))
	for i := range revisions {
		revisions[i].GroupRevisions[0].UpdatedAt = groupUpdatedAt
	}
	previewID := fmt.Sprintf("phase4-stale-multi-preview-%d", time.Now().UnixNano())
	planHash := strings.Repeat("c", 64)
	plan := service.UpstreamModelPolicyPreviewPlan{
		PreviewID: previewID, PlanHash: planHash, ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
		AccountIDs: accountIDs, Revisions: revisions,
	}
	refreshRepo := NewUpstreamModelRefreshFenceRepository(integrationEntClient, accountRepo)
	stateRepo := refreshRepo.(service.UpstreamModelRefreshStateRepository)
	require.NoError(t, stateRepo.StoreUpstreamModelPolicyPreview(ctx, plan))
	_, err := integrationDB.ExecContext(ctx, "UPDATE accounts SET updated_at = updated_at + INTERVAL '1 second' WHERE id = $1", second.ID)
	require.NoError(t, err)

	require.ErrorContains(t, stateRepo.ApplyUpstreamModelPolicyPreview(ctx, previewID, planHash, accountIDs, 990_007), "changed after preview")
	for _, accountID := range accountIDs {
		var policy string
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COALESCE(extra->>'upstream_model_policy', 'manual') FROM accounts WHERE id = $1", accountID).Scan(&policy))
		require.Equal(t, service.UpstreamModelPolicyManual, policy, "stale second account must not partially opt in the first account")
	}
	var auditCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM upstream_model_policy_audits WHERE preview_id = $1", previewID).Scan(&auditCount))
	require.Zero(t, auditCount)
	var consumed bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT consumed_at IS NOT NULL FROM upstream_model_policy_previews WHERE preview_id = $1", previewID).Scan(&consumed))
	require.False(t, consumed)
}

func TestUpstreamModelPolicyPreviewRejectsGroupMembershipChangesBeforeConfirmation(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, ApplyMigrations(ctx, integrationDB))

	account := mustCreateAccount(t, testEntClient(t), &service.Account{
		Name:     fmt.Sprintf("phase4-policy-membership-%d", time.Now().UnixNano()),
		Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "phase4-membership-key", "base_url": "https://api.openai.com/v1"},
	})
	group1 := mustCreateGroup(t, testEntClient(t), &service.Group{
		Name: fmt.Sprintf("phase4-policy-group-a-%d", time.Now().UnixNano()), Platform: service.PlatformOpenAI,
	})
	group2 := mustCreateGroup(t, testEntClient(t), &service.Group{
		Name: fmt.Sprintf("phase4-policy-group-b-%d", time.Now().UnixNano()), Platform: service.PlatformOpenAI,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_groups WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM groups WHERE id = ANY($1)", pq.Array([]int64{group1.ID, group2.ID}))
	})

	accountRepo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	groupRepo := newGroupRepositoryWithSQL(integrationEntClient, integrationDB)
	refreshRepo := NewUpstreamModelRefreshFenceRepository(integrationEntClient, accountRepo)
	stateRepo := refreshRepo.(service.UpstreamModelRefreshStateRepository)
	require.NoError(t, accountRepo.BindGroups(ctx, account.ID, []int64{group1.ID}))

	storePreview := func(index int) (string, string) {
		currentAccount, err := accountRepo.GetByID(ctx, account.ID)
		require.NoError(t, err)
		groupIDs, err := accountRepo.loadAccountGroupIDs(ctx, account.ID)
		require.NoError(t, err)
		sort.Slice(groupIDs, func(i, j int) bool { return groupIDs[i] < groupIDs[j] })
		groupRevisions := make([]service.UpstreamModelPolicyGroupRev, 0, len(groupIDs))
		for _, groupID := range groupIDs {
			var updatedAt time.Time
			require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT updated_at FROM groups WHERE id = $1", groupID).Scan(&updatedAt))
			groupRevisions = append(groupRevisions, service.UpstreamModelPolicyGroupRev{GroupID: groupID, UpdatedAt: updatedAt})
		}
		previewID := fmt.Sprintf("phase4-membership-preview-%d-%d", account.ID, index)
		planHash := fmt.Sprintf("sha256:%064x", index+1)
		plan := service.UpstreamModelPolicyPreviewPlan{
			PreviewID: previewID, PlanHash: planHash, ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
			AccountIDs: []int64{account.ID},
			Revisions: []service.UpstreamModelPolicyAccountRevision{{
				AccountID: account.ID, UpdatedAt: currentAccount.UpdatedAt,
				CurrentPolicy: service.UpstreamModelPolicyManual, SourceProfileID: "openai", Eligible: true,
				GroupRevisions: groupRevisions,
			}},
		}
		require.NoError(t, stateRepo.StoreUpstreamModelPolicyPreview(ctx, plan))
		return previewID, planHash
	}

	cases := []struct {
		name   string
		change func() error
	}{
		{name: "add group", change: func() error { return accountRepo.AddToGroup(ctx, account.ID, group2.ID, 2) }},
		{name: "remove group", change: func() error { return accountRepo.RemoveFromGroup(ctx, account.ID, group2.ID) }},
		{name: "bind same group set and advance revision", change: func() error { return accountRepo.BindGroups(ctx, account.ID, []int64{group1.ID}) }},
		{name: "bind account to group", change: func() error { return groupRepo.BindAccountsToGroup(ctx, group2.ID, []int64{account.ID}) }},
		{name: "clear group memberships", change: func() error { _, err := groupRepo.DeleteAccountGroupsByGroupID(ctx, group2.ID); return err }},
		{name: "bind account before group cascade", change: func() error { return groupRepo.BindAccountsToGroup(ctx, group2.ID, []int64{account.ID}) }},
		{name: "delete group cascade", change: func() error { _, err := groupRepo.DeleteCascade(ctx, group2.ID); return err }},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			previewID, planHash := storePreview(index + 1)
			time.Sleep(2 * time.Millisecond)
			require.NoError(t, tc.change())
			err := stateRepo.ApplyUpstreamModelPolicyPreview(ctx, previewID, planHash, []int64{account.ID}, 990_005)
			require.Error(t, err, "the persisted preview must become stale after account-group changes")
			currentAccount, getErr := accountRepo.GetByID(ctx, account.ID)
			require.NoError(t, getErr)
			require.Equal(t, service.UpstreamModelPolicyManual, currentAccount.GetUpstreamModelPolicy(), "stale preview must not partially apply")
		})
	}
}

func TestBindGroupsRejectsSoftDeletedAccount(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, ApplyMigrations(ctx, integrationDB))
	account := mustCreateAccount(t, testEntClient(t), &service.Account{
		Name:     fmt.Sprintf("phase4-deleted-account-%d", time.Now().UnixNano()),
		Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "phase4-deleted-key", "base_url": "https://api.openai.com/v1"},
	})
	group := mustCreateGroup(t, testEntClient(t), &service.Group{
		Name: fmt.Sprintf("phase4-deleted-account-group-%d", time.Now().UnixNano()), Platform: service.PlatformOpenAI,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_groups WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM groups WHERE id = $1", group.ID)
	})
	_, err := integrationDB.ExecContext(ctx, "UPDATE accounts SET deleted_at = NOW() WHERE id = $1", account.ID)
	require.NoError(t, err)
	accountRepo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	require.ErrorIs(t, accountRepo.BindGroups(ctx, account.ID, []int64{group.ID}), service.ErrAccountNotFound)
}
