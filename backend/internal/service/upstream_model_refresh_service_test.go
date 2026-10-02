package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunUpstreamModelRefreshBatchBoundsConcurrencyAndReturnsEveryAccount(t *testing.T) {
	accounts := make([]Account, 20)
	for i := range accounts {
		accounts[i].ID = int64(i + 1)
	}
	var active atomic.Int64
	var maximum atomic.Int64
	results := runUpstreamModelRefreshBatch(context.Background(), accounts, 4, func(_ context.Context, account *Account) (bool, error) {
		current := active.Add(1)
		for previous := maximum.Load(); current > previous && !maximum.CompareAndSwap(previous, current); previous = maximum.Load() {
		}
		time.Sleep(10 * time.Millisecond)
		active.Add(-1)
		if account.ID == 7 {
			return true, errors.New("upstream failed")
		}
		return true, nil
	})

	require.Len(t, results, len(accounts))
	require.EqualValues(t, 4, maximum.Load())
	for i, result := range results {
		require.Equal(t, int64(i+1), result.account.ID, "batch results are returned in deterministic account order")
	}
	require.Error(t, results[6].err)
}

func TestUpstreamModelRefreshBatchStatusDistinguishesSkippedAttempts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		refreshed  bool
		err        error
		wantStatus string
		wantKind   string
	}{
		{name: "completed fetch", refreshed: true, wantStatus: "succeeded"},
		{name: "no longer due after lease", wantStatus: "skipped", wantKind: "no_longer_due"},
		{name: "another worker owns lease", err: ErrUpstreamModelRefreshAlreadyRunning, wantStatus: "skipped", wantKind: "already_running"},
		{name: "upstream failure", refreshed: true, err: errors.New("upstream failed"), wantStatus: "failed", wantKind: "refresh_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, kind := upstreamModelRefreshBatchStatus(tc.refreshed, tc.err)
			require.Equal(t, tc.wantStatus, status)
			require.Equal(t, tc.wantKind, kind)
		})
	}
}
