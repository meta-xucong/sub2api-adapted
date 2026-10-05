package service

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type refreshTestAccountReader struct {
	account *Account
}

func (r refreshTestAccountReader) ListActive(context.Context) ([]Account, error) {
	return []Account{*r.account}, nil
}

func (r refreshTestAccountReader) GetByID(context.Context, int64) (*Account, error) {
	account := *r.account
	return &account, nil
}

type refreshTestFences struct {
	issued  int
	applied int
}

func (f *refreshTestFences) IssueUpstreamModelRefreshToken(context.Context, int64) (int64, error) {
	f.issued++
	return int64(f.issued), nil
}

func (f *refreshTestFences) ApplyUpstreamModelRefreshSnapshot(_ context.Context, _ int64, token int64, _ UpstreamModelAvailabilitySnapshot) (bool, error) {
	f.applied++
	return token == int64(f.issued), nil
}

type refreshTestLease struct{}

func (refreshTestLease) TryAcquireLeaderLock(context.Context, string, string, time.Duration) (bool, error) {
	return true, nil
}

func (refreshTestLease) ReleaseLeaderLock(context.Context, string, string) error { return nil }

func (refreshTestLease) RenewLeaderLock(context.Context, string, string, time.Duration) (bool, error) {
	return true, nil
}

type refreshLifecycleBlockingAccountReader struct {
	started  chan struct{}
	finished chan struct{}
	once     sync.Once
}

func (r *refreshLifecycleBlockingAccountReader) ListActive(ctx context.Context) ([]Account, error) {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	close(r.finished)
	return nil, ctx.Err()
}

func (*refreshLifecycleBlockingAccountReader) GetByID(context.Context, int64) (*Account, error) {
	return nil, errors.New("not used")
}

type refreshLifecycleStateFences struct{ refreshTestFences }

func (refreshLifecycleStateFences) StoreUpstreamModelPolicyPreview(context.Context, UpstreamModelPolicyPreviewPlan) error {
	return nil
}

func (refreshLifecycleStateFences) ApplyUpstreamModelPolicyPreview(context.Context, string, string, []int64, int64) error {
	return nil
}

func (refreshLifecycleStateFences) StartUpstreamModelRefreshRun(context.Context, UpstreamModelRefreshRunRecord) error {
	return nil
}

func (refreshLifecycleStateFences) FinishUpstreamModelRefreshRun(context.Context, UpstreamModelRefreshRunRecord) error {
	return nil
}

func (refreshLifecycleStateFences) GetLastUpstreamModelRefreshRun(context.Context) (UpstreamModelRefreshRunRecord, error) {
	return UpstreamModelRefreshRunRecord{}, nil
}

func TestUpstreamModelRefreshScheduleUsesAsiaShanghaiDailySlot(t *testing.T) {
	svc := &UpstreamModelRefreshService{}
	hour, minute := svc.refreshSchedule()
	require.Equal(t, 4, hour)
	require.Zero(t, minute)

	// 01:30 UTC is 09:30 in Shanghai. The latest run is 04:00 local today;
	// the next due slot is 04:00 local tomorrow.
	now := time.Date(2026, 10, 5, 1, 30, 0, 0, time.UTC)
	require.Equal(t, time.Date(2026, 10, 4, 20, 0, 0, 0, time.UTC), latestModelRefreshAtForSchedule(now, 4, 0))
	require.Equal(t, time.Date(2026, 10, 5, 20, 0, 0, 0, time.UTC), nextModelRefreshAtForSchedule(now, 4, 0))

	svc.cfg = &config.Config{Gateway: config.GatewayConfig{UpstreamModelRefresh: config.GatewayUpstreamModelRefreshConfig{Hour: 5, Minute: 15}}}
	hour, minute = svc.refreshSchedule()
	require.Equal(t, 5, hour)
	require.Equal(t, 15, minute)
}

func TestUpstreamModelRefreshDisabledDoesNotStartScheduler(t *testing.T) {
	svc := &UpstreamModelRefreshService{cfg: &config.Config{Gateway: config.GatewayConfig{UpstreamModelRefresh: config.GatewayUpstreamModelRefreshConfig{Enabled: false}}}}
	svc.Start()
	require.Nil(t, svc.cron)
}

func TestUpstreamModelRefreshStopCancelsStartupCatchUpBeforeInfrastructureCleanup(t *testing.T) {
	reader := &refreshLifecycleBlockingAccountReader{started: make(chan struct{}), finished: make(chan struct{})}
	svc := NewUpstreamModelRefreshService(reader, &AccountTestService{}, &refreshLifecycleStateFences{}, refreshTestLease{}, nil,
		&config.Config{Gateway: config.GatewayConfig{UpstreamModelRefresh: config.GatewayUpstreamModelRefreshConfig{Enabled: true}}})
	svc.Start()
	select {
	case <-reader.started:
	case <-time.After(3 * time.Second):
		t.Fatal("startup catch-up did not begin")
	}

	stopped := make(chan struct{})
	go func() { svc.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not cancel and wait for the startup refresh task")
	}
	select {
	case <-reader.finished:
	default:
		t.Fatal("Stop returned while the refresh task could still use shared infrastructure")
	}
}

func TestUpstreamModelRefreshStopCancelsPolicyCatchUpWhenDailyScheduleDisabled(t *testing.T) {
	reader := &refreshLifecycleBlockingAccountReader{started: make(chan struct{}), finished: make(chan struct{})}
	svc := NewUpstreamModelRefreshService(reader, &AccountTestService{}, &refreshLifecycleStateFences{}, refreshTestLease{}, nil,
		&config.Config{Gateway: config.GatewayConfig{UpstreamModelRefresh: config.GatewayUpstreamModelRefreshConfig{Enabled: false}}})
	// The flag controls the automatic schedule/startup catch-up. An explicit
	// administrator opt-in retains the frozen source's due-only catch-up behavior.
	require.NoError(t, svc.SetAccountPolicies(context.Background(), "preview-id", "sha256:test", []int64{7}, 99))
	select {
	case <-reader.started:
	case <-time.After(3 * time.Second):
		t.Fatal("policy confirmation catch-up did not begin")
	}

	stopped := make(chan struct{})
	go func() { svc.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not cancel and wait for the policy-triggered refresh task")
	}
	select {
	case <-reader.finished:
	default:
		t.Fatal("Stop returned while the policy-triggered refresh task was still active")
	}
}

func TestRefreshAccountCatalogNowReusesSingleAvailabilityFetch(t *testing.T) {
	upstream := newHttptestModelSourceUpstream(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeModelSourceTestResponse(w, []byte(`{"data":[{"id":"gpt-5.6-sol","display_name":"GPT-5.6 Sol","reasoning":true,"supported_reasoning_levels":[{"effort":"low"}],"input_modalities":["text"],"context_window":128000,"max_output_tokens":8192}]}`))
	}))
	defer upstream.close()

	account := modelSourceTestAccount("openai", "https://api.openai.com/v1")
	account.ID = 88_044
	reader := refreshTestAccountReader{account: account}
	fences := &refreshTestFences{}
	metadataRepo := &upstreamModelMetadataRepoStub{}
	accountTest := &AccountTestService{accountRepo: metadataRepo, httpUpstream: upstream}
	refresh := NewUpstreamModelRefreshService(reader, accountTest, fences, refreshTestLease{}, nil, nil)

	catalog, err := refresh.RefreshAccountCatalogNow(context.Background(), account)
	require.NoError(t, err)
	require.NotNil(t, catalog)
	require.Equal(t, []string{"gpt-5.6-sol"}, catalog.Models)
	modelMetadata, exists := catalog.Metadata["gpt-5.6-sol"]
	require.True(t, exists, "manual catalog sync must retain direct capability metadata from its single source response")
	require.Equal(t, "GPT-5.6 Sol", modelMetadata.DisplayName)
	require.NotNil(t, modelMetadata.Reasoning)
	require.True(t, *modelMetadata.Reasoning)
	require.Equal(t, []string{"low"}, modelMetadata.SupportedReasoningLevels)
	require.Equal(t, []string{"text"}, modelMetadata.InputModalities)
	require.EqualValues(t, 128000, modelMetadata.ContextWindow)
	require.Equal(t, account.ID, metadataRepo.accountID)
	metadataSnapshot, ok := metadataRepo.updates[UpstreamModelMetadataExtraKey].(UpstreamModelMetadataSnapshot)
	require.True(t, ok, "manual catalog sync must persist the preserved capability metadata")
	require.Contains(t, metadataSnapshot.Models, "gpt-5.6-sol")
	require.Len(t, upstream.requests, 1, "admin sync must use the refresh result instead of fetching the model list twice")
	require.Equal(t, "https://api.openai.com/v1/models", upstream.requests[0].URL.String())
	require.Equal(t, 1, fences.issued)
	require.Equal(t, 1, fences.applied)
}

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
