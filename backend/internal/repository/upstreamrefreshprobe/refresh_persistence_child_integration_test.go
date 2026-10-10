//go:build integration

package upstreamrefreshprobe_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestRefreshPersistenceChildProcess(t *testing.T) {
	dsn := os.Getenv("SUB2API_PHASE4_REFRESH_DSN")
	accountIDValue := os.Getenv("SUB2API_PHASE4_REFRESH_ACCOUNT")
	runID := os.Getenv("SUB2API_PHASE4_REFRESH_RUN_ID")
	require.NotEmpty(t, dsn)
	require.NotEmpty(t, accountIDValue)
	require.NotEmpty(t, runID)

	accountID, err := strconv.ParseInt(accountIDValue, 10, 64)
	require.NoError(t, err)

	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.PingContext(context.Background()))

	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	accountRepo := repository.NewAccountRepository(client, db, nil)
	fenceRepo := repository.NewUpstreamModelRefreshFenceRepository(client, accountRepo)
	stateRepo, ok := fenceRepo.(service.UpstreamModelRefreshStateRepository)
	require.True(t, ok)

	ctx := context.Background()
	account, err := accountRepo.GetByID(ctx, accountID)
	require.NoError(t, err)
	require.Equal(t, service.UpstreamModelPolicyFollow, account.GetUpstreamModelPolicy(), "preview opt-in must remain durable across process restart")
	previousSnapshot := account.GetUpstreamModelAvailabilitySnapshot()
	require.NotNil(t, previousSnapshot)
	require.Equal(t, []string{"gpt-5.6-sol"}, previousSnapshot.PublicModels)

	childToken, err := fenceRepo.IssueUpstreamModelRefreshToken(ctx, accountID)
	require.NoError(t, err)
	require.Greater(t, childToken, previousSnapshot.RefreshToken)

	staleSnapshot := *previousSnapshot
	staleSnapshot.PublicModels = []string{"stale-child-catalog"}
	staleApplied, err := fenceRepo.ApplyUpstreamModelRefreshSnapshot(ctx, accountID, previousSnapshot.RefreshToken, staleSnapshot)
	require.NoError(t, err)
	require.False(t, staleApplied, "the child process must observe and enforce the latest cross-process fencing token")

	wonSnapshot := *previousSnapshot
	wonSnapshot.RefreshToken = childToken
	wonSnapshot.Status = "fresh"
	wonSnapshot.LastAttemptAt = time.Now().UTC().Truncate(time.Microsecond)
	wonSnapshot.LastSuccessAt = &wonSnapshot.LastAttemptAt
	wonSnapshot.PublicModels = []string{"gpt-5.6-terra"}
	wonSnapshot.RawModels = []string{"gpt-5.6-terra"}
	wonSnapshot.PublicToUpstream = map[string]string{"gpt-5.6-terra": "gpt-5.6-terra"}
	won, err := fenceRepo.ApplyUpstreamModelRefreshSnapshot(ctx, accountID, childToken, wonSnapshot)
	require.NoError(t, err)
	require.True(t, won)

	startedAt := time.Now().UTC().Truncate(time.Microsecond)
	run := service.UpstreamModelRefreshRunRecord{
		RunID: runID, ScheduledAt: startedAt, StartedAt: startedAt, Eligible: 1,
	}
	require.NoError(t, stateRepo.StartUpstreamModelRefreshRun(ctx, run))
	finishedAt := startedAt.Add(time.Second)
	run.Status = "completed"
	run.FinishedAt = &finishedAt
	run.Succeeded = 1
	run.Accounts = []service.UpstreamModelAccountRunStatus{{
		AccountID: accountID, SourceProfileID: "openai", Policy: service.UpstreamModelPolicyFollow,
		Status: "succeeded", RawCount: 1, CanonicalCount: 1,
	}}
	require.NoError(t, stateRepo.FinishUpstreamModelRefreshRun(ctx, run))

	lastRun, err := stateRepo.GetLastUpstreamModelRefreshRun(ctx)
	require.NoError(t, err)
	require.Equal(t, runID, lastRun.RunID)
	require.Equal(t, "completed", lastRun.Status)
	fmt.Println("Phase 4 refresh state verified from a fresh process")
}
