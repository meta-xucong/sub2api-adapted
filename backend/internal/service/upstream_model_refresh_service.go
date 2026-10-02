package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/robfig/cron/v3"
)

const (
	upstreamModelRefreshLeaseTTL = 2 * time.Minute
	upstreamModelRefreshRunKey   = "upstream-model-refresh:daily"
)

var ErrUpstreamModelRefreshAlreadyRunning = errors.New("upstream model refresh is already running")

type UpstreamModelRefreshService struct {
	accounts interface {
		ListActive(context.Context) ([]Account, error)
		GetByID(context.Context, int64) (*Account, error)
	}
	accountTest *AccountTestService
	fences      UpstreamModelRefreshFenceRepository
	state       UpstreamModelRefreshStateRepository
	leases      RenewableLeaderLockCache
	invalidator interface{ InvalidateModelAvailabilityForAccount(*Account) }
	cfg         *config.Config
	cron        *cron.Cron
	startOnce   sync.Once
	stopOnce    sync.Once
	runMu       sync.Mutex
}

func NewUpstreamModelRefreshService(
	accounts interface {
		ListActive(context.Context) ([]Account, error)
		GetByID(context.Context, int64) (*Account, error)
	},
	accountTest *AccountTestService,
	fences UpstreamModelRefreshFenceRepository,
	leases RenewableLeaderLockCache,
	invalidator interface{ InvalidateModelAvailabilityForAccount(*Account) },
	cfg *config.Config,
) *UpstreamModelRefreshService {
	state, _ := fences.(UpstreamModelRefreshStateRepository)
	return &UpstreamModelRefreshService{accounts: accounts, accountTest: accountTest, fences: fences, state: state, leases: leases, invalidator: invalidator, cfg: cfg}
}

func ProvideUpstreamModelRefreshService(
	accounts AccountRepository,
	accountTest *AccountTestService,
	fences UpstreamModelRefreshFenceRepository,
	leases LeaderLockCache,
	gateway *GatewayService,
	cfg *config.Config,
) *UpstreamModelRefreshService {
	renewable, _ := leases.(RenewableLeaderLockCache)
	service := NewUpstreamModelRefreshService(accounts, accountTest, fences, renewable, gateway, cfg)
	service.Start()
	return service
}

func (s *UpstreamModelRefreshService) Start() {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.UpstreamModelRefresh.Enabled || s.accounts == nil || s.accountTest == nil || s.fences == nil || s.leases == nil {
		return
	}
	s.startOnce.Do(func() {
		location, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			location = time.FixedZone("UTC+8", 8*60*60)
		}
		c := cron.New(cron.WithLocation(location))
		hour, minute := s.refreshSchedule()
		expression := fmt.Sprintf("%d %d * * *", minute, hour)
		if _, err := c.AddFunc(expression, func() { s.RunDue(context.Background(), time.Now()) }); err != nil {
			slog.Error("upstream model refresh schedule rejected", "error", err)
			return
		}
		s.cron = c
		c.Start()
		slog.Info("upstream model availability refresh scheduled", "expression", expression, "timezone", "Asia/Shanghai", "interval_hours", 24)
		// Only due accounts are considered. Fresh snapshots are not refreshed on
		// every restart; a missed daily run is caught up once on startup.
		go s.RunDue(context.Background(), time.Now())
	})
}

func (s *UpstreamModelRefreshService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.cron == nil {
			return
		}
		ctx := s.cron.Stop()
		select {
		case <-ctx.Done():
		case <-time.After(3 * time.Second):
			slog.Warn("upstream model refresh scheduler stop timed out")
		}
	})
}

func (s *UpstreamModelRefreshService) refreshSchedule() (hour, minute int) {
	if s == nil || s.cfg == nil {
		return 4, 0
	}
	return normalizeModelRefreshSchedule(s.cfg.Gateway.UpstreamModelRefresh.Hour, s.cfg.Gateway.UpstreamModelRefresh.Minute)
}

func normalizeModelRefreshSchedule(hour, minute int) (int, int) {
	if hour < 0 || hour > 23 {
		hour = 4
	}
	if minute < 0 || minute > 59 {
		minute = 0
	}
	return hour, minute
}

func (s *UpstreamModelRefreshService) RunDue(ctx context.Context, now time.Time) {
	if s == nil || s.accounts == nil || s.leases == nil || !s.runMu.TryLock() {
		return
	}
	defer s.runMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, s.refreshTotalBudget())
	defer cancel()
	runID, idErr := newRefreshLeaseOwner()
	if idErr != nil {
		slog.Warn("upstream model refresh run id generation failed", "error", idErr)
		return
	}
	hour, minute := s.refreshSchedule()
	scheduledAt := latestModelRefreshAtForSchedule(now, hour, minute)
	startedAt := time.Now().UTC()
	record := UpstreamModelRefreshRunRecord{RunID: runID, Status: "running", ScheduledAt: scheduledAt, StartedAt: startedAt, Accounts: []UpstreamModelAccountRunStatus{}}
	err := s.withRenewableLease(runCtx, upstreamModelRefreshRunKey, func(leaseCtx context.Context) error {
		if s.state != nil {
			if err := s.state.StartUpstreamModelRefreshRun(leaseCtx, record); err != nil {
				return err
			}
		}
		finish := func(status string, finishErr error) {
			record.Status = status
			finishedAt := time.Now().UTC()
			record.FinishedAt = &finishedAt
			if finishErr != nil {
				record.ErrorKind = safeModelRefreshError(finishErr)
			}
			if s.state != nil {
				persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(leaseCtx), 5*time.Second)
				defer persistCancel()
				if err := s.state.FinishUpstreamModelRefreshRun(persistCtx, record); err != nil {
					slog.Warn("upstream model refresh run status persist failed", "run_id", record.RunID, "error", safeModelRefreshError(err))
				}
			}
		}
		accounts, err := s.accounts.ListActive(leaseCtx)
		if err != nil {
			finish("failed", err)
			return err
		}
		refreshed, skipped, failed := 0, 0, 0
		dueAccounts := make([]Account, 0, len(accounts))
		for i := range accounts {
			if leaseCtx.Err() != nil {
				finish("partial_failure", leaseCtx.Err())
				return leaseCtx.Err()
			}
			account := &accounts[i]
			profile := DetectUpstreamModelSourceProfile(account)
			if account.GetUpstreamModelPolicy() == UpstreamModelPolicyFollow && profile.ManualOnly {
				record.Unsupported++
				record.Accounts = append(record.Accounts, upstreamModelAccountRunStatus(account, "unsupported", "source_manual_only"))
				continue
			}
			if upstreamModelCatalogEndpointUnsupported(account.GetUpstreamModelAvailabilitySnapshot(), profile) {
				record.Unsupported++
				record.Accounts = append(record.Accounts, upstreamModelAccountRunStatus(account, "unsupported", "catalog_endpoint_unsupported"))
				continue
			}
			if !upstreamModelRefreshDue(account, now) {
				skipped++
				record.Accounts = append(record.Accounts, upstreamModelAccountRunStatus(account, "skipped", ""))
				continue
			}
			record.Eligible++
			dueAccounts = append(dueAccounts, *account)
		}

		results := runUpstreamModelRefreshBatch(leaseCtx, dueAccounts, s.refreshMaxConcurrency(), s.refreshAccountIfDueResult)
		for _, result := range results {
			status, errorKind := upstreamModelRefreshBatchStatus(result.refreshed, result.err)
			if status == "skipped" {
				skipped++
				record.Accounts = append(record.Accounts, upstreamModelAccountRunStatus(&result.account, status, errorKind))
				continue
			}
			if result.err != nil {
				failed++
				record.Accounts = append(record.Accounts, upstreamModelAccountRunStatus(&result.account, status, errorKind))
				slog.Warn("upstream model account refresh failed", "account_id", result.account.ID, "source_profile", DetectUpstreamModelSourceProfile(&result.account).ID, "error", safeModelRefreshError(result.err))
				continue
			}
			refreshed++
			record.Accounts = append(record.Accounts, upstreamModelAccountRunStatus(&result.account, status, errorKind))
		}
		record.Succeeded = refreshed
		record.Failed = failed
		record.Skipped = skipped
		if leaseCtx.Err() != nil {
			sort.Slice(record.Accounts, func(i, j int) bool { return record.Accounts[i].AccountID < record.Accounts[j].AccountID })
			finish("partial_failure", leaseCtx.Err())
			return leaseCtx.Err()
		}
		sort.Slice(record.Accounts, func(i, j int) bool { return record.Accounts[i].AccountID < record.Accounts[j].AccountID })
		if failed > 0 || record.Unsupported > 0 {
			finish("partial_failure", nil)
		} else {
			finish("completed", nil)
		}
		slog.Info("upstream model availability refresh run completed", "refreshed", refreshed, "skipped", skipped, "failed", failed)
		return nil
	})
	if err != nil && !errors.Is(err, ErrUpstreamModelRefreshAlreadyRunning) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		slog.Warn("upstream model refresh run aborted", "error", safeModelRefreshError(err))
	}
}

func (s *UpstreamModelRefreshService) refreshTotalBudget() time.Duration {
	seconds := 1800
	if s != nil && s.cfg != nil && s.cfg.Gateway.UpstreamModelRefresh.TotalBudgetSeconds > 0 {
		seconds = s.cfg.Gateway.UpstreamModelRefresh.TotalBudgetSeconds
	}
	return time.Duration(seconds) * time.Second
}

func (s *UpstreamModelRefreshService) refreshMaxConcurrency() int {
	concurrency := 4
	if s != nil && s.cfg != nil && s.cfg.Gateway.UpstreamModelRefresh.MaxConcurrency > 0 {
		concurrency = s.cfg.Gateway.UpstreamModelRefresh.MaxConcurrency
	}
	if concurrency > 8 {
		return 8
	}
	return concurrency
}

type upstreamModelRefreshBatchResult struct {
	account   Account
	refreshed bool
	err       error
}

func runUpstreamModelRefreshBatch(
	ctx context.Context,
	accounts []Account,
	concurrency int,
	refresh func(context.Context, *Account) (bool, error),
) []upstreamModelRefreshBatchResult {
	if len(accounts) == 0 || refresh == nil {
		return nil
	}
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 8 {
		concurrency = 8
	}
	if concurrency > len(accounts) {
		concurrency = len(accounts)
	}
	jobs := make(chan Account)
	results := make(chan upstreamModelRefreshBatchResult, len(accounts))
	var workers sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for account := range jobs {
				refreshed, refreshErr := refresh(ctx, &account)
				results <- upstreamModelRefreshBatchResult{account: account, refreshed: refreshed, err: refreshErr}
			}
		}()
	}
	for _, account := range accounts {
		jobs <- account
	}
	close(jobs)
	workers.Wait()
	close(results)
	ordered := make([]upstreamModelRefreshBatchResult, 0, len(accounts))
	for result := range results {
		ordered = append(ordered, result)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].account.ID < ordered[j].account.ID })
	return ordered
}

func upstreamModelRefreshBatchStatus(refreshed bool, err error) (status, errorKind string) {
	if errors.Is(err, ErrUpstreamModelRefreshAlreadyRunning) {
		return "skipped", "already_running"
	}
	if err != nil {
		return "failed", safeModelRefreshError(err)
	}
	if !refreshed {
		return "skipped", "no_longer_due"
	}
	return "succeeded", ""
}

func latestModelRefreshAt(now time.Time) time.Time {
	return latestModelRefreshAtForSchedule(now, 4, 0)
}

func latestModelRefreshAtForSchedule(now time.Time, hour, minute int) time.Time {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.FixedZone("UTC+8", 8*60*60)
	}
	hour, minute = normalizeModelRefreshSchedule(hour, minute)
	localNow := now.In(location)
	latest := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, location)
	if latest.After(localNow) {
		latest = latest.AddDate(0, 0, -1)
	}
	return latest.UTC()
}

func upstreamModelAccountRunStatus(account *Account, status, errorKind string) UpstreamModelAccountRunStatus {
	row := UpstreamModelAccountRunStatus{Status: status, ErrorKind: errorKind}
	if account == nil {
		return row
	}
	row.AccountID = account.ID
	row.Policy = account.GetUpstreamModelPolicy()
	row.SourceProfileID = DetectUpstreamModelSourceProfile(account).ID
	if snapshot := account.GetUpstreamModelAvailabilitySnapshot(); snapshot != nil {
		attempt, due := snapshot.LastAttemptAt, snapshot.NextDueAt
		row.LastAttemptAt, row.NextDueAt = &attempt, &due
		row.LastSuccessAt = snapshot.LastSuccessAt
		row.RawCount = len(snapshot.RawModels)
		row.CanonicalCount = len(snapshot.PublicModels)
		if len(snapshot.RawDigest) > 0 {
			row.DigestPrefix = snapshot.RawDigest
			if len(row.DigestPrefix) > 23 {
				row.DigestPrefix = row.DigestPrefix[:23]
			}
		}
	}
	return row
}

func (s *UpstreamModelRefreshService) GetRunStatus(ctx context.Context) UpstreamModelRefreshStatus {
	status := UpstreamModelRefreshStatus{Enabled: s != nil && s.cfg != nil && s.cfg.Gateway.UpstreamModelRefresh.Enabled, Timezone: "Asia/Shanghai", StatusSource: "unavailable"}
	if s == nil {
		status.RunStatus = "unavailable"
		return status
	}
	hour, minute := s.refreshSchedule()
	status.Schedule = fmt.Sprintf("%02d:%02d", hour, minute)
	if !status.Enabled {
		status.RunStatus = "disabled"
		status.StatusSource = "config"
		return status
	}
	if s.state == nil {
		status.RunStatus = "unavailable"
		return status
	}
	record, err := s.state.GetLastUpstreamModelRefreshRun(ctx)
	if err != nil {
		if errors.Is(err, ErrUpstreamModelRefreshRunNotFound) {
			status.RunStatus = "never_run"
			status.StatusSource = "postgres"
		} else {
			status.RunStatus = "unavailable"
			status.StatusSource = "unavailable"
		}
		return status
	}
	status.RunStatus = record.Status
	status.StatusSource = "postgres"
	status.LastRunID = record.RunID
	status.LastScheduledAt = &record.ScheduledAt
	status.LastStartedAt = &record.StartedAt
	status.LastFinishedAt = record.FinishedAt
	status.Eligible = record.Eligible
	status.Succeeded = record.Succeeded
	status.Failed = record.Failed
	status.Unsupported = record.Unsupported
	status.Skipped = record.Skipped
	status.Accounts = append([]UpstreamModelAccountRunStatus(nil), record.Accounts...)
	if record.Status == "running" && time.Since(record.StartedAt) > 50*time.Minute {
		status.RunStatus = "stale_running"
	} else {
		status.LeaderActive = record.Status == "running"
	}
	return status
}

func (s *UpstreamModelRefreshService) RefreshAccountNow(ctx context.Context, account *Account) error {
	_, err := s.refreshAccount(ctx, account, true)
	return err
}

// RefreshAccountIfDue serializes against the daily job and re-reads the
// account after acquiring its lease. Smart Router calibration and manual
// scheduler catch-up therefore cannot issue a second upstream catalog request
// for a snapshot another instance has just refreshed.
func (s *UpstreamModelRefreshService) RefreshAccountIfDue(ctx context.Context, account *Account) error {
	_, err := s.refreshAccount(ctx, account, false)
	return err
}

func (s *UpstreamModelRefreshService) refreshAccountIfDueResult(ctx context.Context, account *Account) (bool, error) {
	return s.refreshAccount(ctx, account, false)
}

func (s *UpstreamModelRefreshService) refreshAccount(ctx context.Context, account *Account, force bool) (bool, error) {
	if s == nil || account == nil || account.ID <= 0 || s.accountTest == nil || s.fences == nil || s.leases == nil {
		return false, errors.New("upstream model refresh is not configured")
	}
	profile := DetectUpstreamModelSourceProfile(account)
	if profile.ManualOnly {
		return false, fmt.Errorf("automatic model refresh is unsupported for this source")
	}
	refreshed := false
	err := s.withRenewableLease(ctx, fmt.Sprintf("upstream-model-refresh:account:%d", account.ID), func(leaseCtx context.Context) error {
		current := account
		if s.accounts != nil {
			loaded, err := s.accounts.GetByID(leaseCtx, account.ID)
			if err != nil {
				return err
			}
			current = loaded
		}
		if current == nil {
			return errors.New("refresh account no longer exists")
		}
		if !force && !upstreamModelRefreshDue(current, time.Now()) {
			*account = *current
			return nil
		}
		profile := DetectUpstreamModelSourceProfile(current)
		if profile.ManualOnly {
			return nil
		}
		refreshed = true
		err := s.refreshAccountUnderLease(leaseCtx, current, profile)
		*account = *current
		return err
	})
	return refreshed, err
}

func (s *UpstreamModelRefreshService) refreshAccountUnderLease(ctx context.Context, account *Account, profile UpstreamModelSourceProfile) error {
	token, err := s.fences.IssueUpstreamModelRefreshToken(ctx, account.ID)
	if err != nil {
		return fmt.Errorf("issue account refresh fence: %w", err)
	}
	timeout := 30 * time.Second
	if s.cfg != nil && s.cfg.Gateway.UpstreamModelRefresh.AccountTimeoutSeconds > 0 {
		timeout = time.Duration(s.cfg.Gateway.UpstreamModelRefresh.AccountTimeoutSeconds) * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	profile, models, shutdownDates, fetchErr := s.accountTest.FetchUpstreamAvailabilityModels(requestCtx, account)
	now := time.Now().UTC()
	var snapshot UpstreamModelAvailabilitySnapshot
	if fetchErr == nil {
		snapshot, fetchErr = buildTrustedAvailabilitySnapshot(profile, models, shutdownDates, now)
		if fetchErr == nil {
			fetchErr = applyCompleteCatalogMappingNegatives(&snapshot, account, profile)
		}
	}
	hour, minute := s.refreshSchedule()
	if fetchErr == nil {
		snapshot.NextDueAt = nextModelRefreshAtForSchedule(now, hour, minute)
	}
	if fetchErr != nil {
		snapshot = failedModelAvailabilitySnapshot(account.GetUpstreamModelAvailabilitySnapshot(), profile, now, fetchErr)
		snapshot.NextDueAt = nextModelRefreshAtForSchedule(now, hour, minute)
		snapshot.UnsupportedEndpoint = nil
		var syncErr *UpstreamModelSyncError
		if errors.As(fetchErr, &syncErr) && syncErr.Kind == UpstreamModelSyncErrorUnsupported &&
			(syncErr.StatusCode == 404 || syncErr.StatusCode == 405) {
			snapshot.UnsupportedEndpoint = &UnsupportedModelCatalogEndpoint{
				SourceIdentity: profile.IdentityFingerprint,
				Normalizer:     upstreamModelNormalizerVersion,
				StatusCode:     syncErr.StatusCode,
			}
		}
	}
	snapshot.RefreshToken = token
	applied, applyErr := s.fences.ApplyUpstreamModelRefreshSnapshot(ctx, account.ID, token, snapshot)
	if applyErr != nil {
		return fmt.Errorf("save account refresh snapshot: %w", applyErr)
	}
	if !applied {
		return errors.New("refresh fence lost before snapshot commit")
	}
	account.SetUpstreamModelAvailabilitySnapshot(snapshot)
	if s.invalidator != nil {
		s.invalidator.InvalidateModelAvailabilityForAccount(account)
	}
	if fetchErr != nil {
		return fetchErr
	}
	return nil
}

func failedModelAvailabilitySnapshot(previous *UpstreamModelAvailabilitySnapshot, profile UpstreamModelSourceProfile, now time.Time, refreshErr error) UpstreamModelAvailabilitySnapshot {
	snapshot := UpstreamModelAvailabilitySnapshot{
		SchemaVersion:   upstreamAvailabilitySchemaVersion,
		SourceProfileID: profile.ID,
		Status:          "error",
		LastAttemptAt:   now,
		NextDueAt:       nextModelRefreshAt(now),
		Normalizer:      upstreamModelNormalizerVersion,
	}
	if previous != nil {
		snapshot = *previous
		snapshot.LastAttemptAt = now
		snapshot.NextDueAt = nextModelRefreshAt(now)
		if previous.LastSuccessAt != nil && now.Sub(*previous.LastSuccessAt) <= upstreamAvailabilityStaleGrace {
			snapshot.Status = "stale"
		} else {
			snapshot.Status = "expired"
		}
	}
	snapshot.LastError = safeModelRefreshError(refreshErr)
	return snapshot
}

func safeModelRefreshError(err error) string {
	if err == nil {
		return ""
	}
	var syncErr *UpstreamModelSyncError
	if errors.As(err, &syncErr) {
		if syncErr.StatusCode > 0 {
			return fmt.Sprintf("%s_http_%d", syncErr.Kind, syncErr.StatusCode)
		}
		return string(syncErr.Kind)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "refresh_failed"
}

func (s *UpstreamModelRefreshService) withRenewableLease(ctx context.Context, key string, run func(context.Context) error) error {
	if s == nil || s.leases == nil || run == nil {
		return errors.New("distributed refresh lease is unavailable")
	}
	owner, err := newRefreshLeaseOwner()
	if err != nil {
		return err
	}
	acquired, err := s.leases.TryAcquireLeaderLock(ctx, key, owner, upstreamModelRefreshLeaseTTL)
	if err != nil {
		return fmt.Errorf("acquire distributed refresh lease: %w", err)
	}
	if !acquired {
		return ErrUpstreamModelRefreshAlreadyRunning
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.leases.ReleaseLeaderLock(releaseCtx, key, owner)
	}()
	leaseCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopRenew := make(chan struct{})
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		ticker := time.NewTicker(upstreamModelRefreshLeaseTTL / 3)
		defer ticker.Stop()
		for {
			select {
			case <-stopRenew:
				return
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				renewCtx, renewCancel := context.WithTimeout(context.Background(), 5*time.Second)
				ok, renewErr := s.leases.RenewLeaderLock(renewCtx, key, owner, upstreamModelRefreshLeaseTTL)
				renewCancel()
				if renewErr != nil || !ok {
					cancel()
					return
				}
			}
		}
	}()
	err = run(leaseCtx)
	close(stopRenew)
	<-renewDone
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if leaseCtx.Err() != nil && err == nil {
		return errors.New("distributed refresh lease was lost")
	}
	return err
}

func newRefreshLeaseOwner() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
