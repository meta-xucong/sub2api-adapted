package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

const (
	upstreamModelRefreshCronSpec       = "0 4 * * *"
	upstreamModelRefreshLeaderLockKey  = "sub2api:upstream-model-refresh:leader"
	upstreamModelRefreshLeaderLockTTL  = 6 * time.Hour
	upstreamModelRefreshRunTimeout     = 6 * time.Hour
	upstreamModelRefreshDefaultTimeout = 30 * time.Second
	upstreamModelRefreshDefaultGrace   = 48 * time.Hour
	upstreamModelRefreshDefaultWorkers = 4
)

var upstreamModelRefreshCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// UpstreamModelRefreshRunResult is intentionally small and safe to log or
// expose to maintenance tooling.
type UpstreamModelRefreshRunResult struct {
	Accounts  int
	Succeeded int
	Failed    int
	Skipped   bool
}

// UpstreamModelRefreshService runs one bounded, leader-elected model catalog
// refresh batch per day at 04:00 Asia/Shanghai (UTC+8).
type UpstreamModelRefreshService struct {
	accountRepo    AccountRepository
	accountTestSvc *AccountTestService
	lockCache      LeaderLockCache
	db             *sql.DB
	cfg            *config.Config
	instanceID     string
	cron           *cron.Cron
	startOnce      sync.Once
	stopOnce       sync.Once
}

func NewUpstreamModelRefreshService(
	accountRepo AccountRepository,
	accountTestSvc *AccountTestService,
	lockCache LeaderLockCache,
	db *sql.DB,
	cfg *config.Config,
) *UpstreamModelRefreshService {
	return &UpstreamModelRefreshService{
		accountRepo:    accountRepo,
		accountTestSvc: accountTestSvc,
		lockCache:      lockCache,
		db:             db,
		cfg:            cfg,
		instanceID:     uuid.NewString(),
	}
}

func (s *UpstreamModelRefreshService) enabled() bool {
	if s == nil || s.cfg == nil {
		return true
	}
	return s.cfg.Gateway.UpstreamModelRefreshEnabled
}

func (s *UpstreamModelRefreshService) requestTimeout() time.Duration {
	if s != nil && s.cfg != nil && s.cfg.Gateway.UpstreamModelRefreshRequestTimeoutSeconds > 0 {
		return time.Duration(s.cfg.Gateway.UpstreamModelRefreshRequestTimeoutSeconds) * time.Second
	}
	return upstreamModelRefreshDefaultTimeout
}

func (s *UpstreamModelRefreshService) staleGrace() time.Duration {
	if s != nil && s.cfg != nil && s.cfg.Gateway.UpstreamModelRefreshStaleGraceHours > 0 {
		return time.Duration(s.cfg.Gateway.UpstreamModelRefreshStaleGraceHours) * time.Hour
	}
	return upstreamModelRefreshDefaultGrace
}

func (s *UpstreamModelRefreshService) maxConcurrency() int {
	if s != nil && s.cfg != nil && s.cfg.Gateway.UpstreamModelRefreshMaxConcurrency > 0 {
		return s.cfg.Gateway.UpstreamModelRefreshMaxConcurrency
	}
	return upstreamModelRefreshDefaultWorkers
}

// Start begins the fixed 04:00 Asia/Shanghai schedule. It intentionally does
// not perform an immediate catch-up run on process startup.
func (s *UpstreamModelRefreshService) Start() {
	if s == nil || !s.enabled() {
		return
	}
	s.startOnce.Do(func() {
		location, err := time.LoadLocation("Asia/Shanghai")
		if err != nil || location == nil {
			location = time.FixedZone("UTC+8", 8*60*60)
		}
		c := cron.New(cron.WithParser(upstreamModelRefreshCronParser), cron.WithLocation(location))
		if _, err := c.AddFunc(upstreamModelRefreshCronSpec, func() { s.runScheduled() }); err != nil {
			logger.LegacyPrintf("service.upstream_model_refresh", "[UpstreamModelRefresh] not started: %v", err)
			return
		}
		s.cron = c
		s.cron.Start()
		logger.LegacyPrintf("service.upstream_model_refresh", "[UpstreamModelRefresh] started (schedule=04:00 Asia/Shanghai, concurrency=%d)", s.maxConcurrency())
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
			logger.LegacyPrintf("service.upstream_model_refresh", "[UpstreamModelRefresh] cron stop timed out")
		}
	})
}

func (s *UpstreamModelRefreshService) runScheduled() {
	ctx, cancel := context.WithTimeout(context.Background(), upstreamModelRefreshRunTimeout)
	defer cancel()
	result, err := s.RunOnce(ctx)
	if err != nil {
		logger.LegacyPrintf("service.upstream_model_refresh", "[UpstreamModelRefresh] run failed: %v", err)
		return
	}
	if result.Skipped {
		logger.LegacyPrintf("service.upstream_model_refresh", "[UpstreamModelRefresh] skipped: another instance owns the leader lock")
		return
	}
	logger.LegacyPrintf("service.upstream_model_refresh", "[UpstreamModelRefresh] completed: accounts=%d succeeded=%d failed=%d", result.Accounts, result.Succeeded, result.Failed)
}

// RunOnce executes one leader-elected refresh batch. It is public so an
// operator/test can trigger the exact same core path without waiting for 04:00.
func (s *UpstreamModelRefreshService) RunOnce(ctx context.Context) (UpstreamModelRefreshRunResult, error) {
	if s == nil {
		return UpstreamModelRefreshRunResult{}, errors.New("upstream model refresh service is nil")
	}
	if !s.enabled() {
		return UpstreamModelRefreshRunResult{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	release, acquired := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, upstreamModelRefreshLeaderLockKey, s.instanceID, upstreamModelRefreshLeaderLockTTL)
	if !acquired {
		return UpstreamModelRefreshRunResult{Skipped: true}, nil
	}
	defer release()

	if s.accountRepo == nil {
		return UpstreamModelRefreshRunResult{}, errors.New("account repository is not configured")
	}
	if s.accountTestSvc == nil {
		return UpstreamModelRefreshRunResult{}, errors.New("account test service is not configured")
	}
	accounts, err := s.accountRepo.ListActive(ctx)
	if err != nil {
		return UpstreamModelRefreshRunResult{}, fmt.Errorf("list active accounts: %w", err)
	}
	refreshableAccounts := make([]Account, 0, len(accounts))
	for _, account := range accounts {
		// Linked/shadow accounts do not own credentials. Their parent account's
		// snapshot is the source of truth, so probing them would only create
		// predictable configuration errors and duplicate upstream traffic.
		if account.ParentAccountID != nil {
			continue
		}
		refreshableAccounts = append(refreshableAccounts, account)
	}
	result := UpstreamModelRefreshRunResult{Accounts: len(refreshableAccounts)}
	if len(refreshableAccounts) == 0 {
		return result, nil
	}

	workerCount := s.maxConcurrency()
	if workerCount > len(refreshableAccounts) {
		workerCount = len(refreshableAccounts)
	}
	sem := make(chan struct{}, workerCount)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for index := range refreshableAccounts {
		account := refreshableAccounts[index]
		sem <- struct{}{}
		wg.Add(1)
		go func(account Account) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := s.refreshAccount(ctx, &account); err != nil {
				logger.LegacyPrintf("service.upstream_model_refresh", "[UpstreamModelRefresh] account=%d failed: %v", account.ID, err)
				mu.Lock()
				result.Failed++
				mu.Unlock()
				return
			}
			mu.Lock()
			result.Succeeded++
			mu.Unlock()
		}(account)
	}
	wg.Wait()
	return result, nil
}

func (s *UpstreamModelRefreshService) refreshAccount(ctx context.Context, account *Account) error {
	if account == nil {
		return errors.New("account is nil")
	}
	requestCtx, cancel := context.WithTimeout(ctx, s.requestTimeout())
	defer cancel()
	catalog, err := s.accountTestSvc.SyncUpstreamModelCatalog(requestCtx, account)
	if err == nil {
		for _, warning := range catalog.Warnings {
			if warning.Code != UpstreamModelRefreshUnsupportedCode {
				continue
			}
			unsupportedErr := newUpstreamModelSyncUnsupportedError(warning.Message, nil)
			if persistErr := s.accountTestSvc.persistUpstreamModelRefreshFailure(ctx, account, unsupportedErr, time.Now(), s.staleGrace()); persistErr != nil {
				return fmt.Errorf("%w; persist refresh failure: %v", unsupportedErr, persistErr)
			}
			return unsupportedErr
		}
		return nil
	}
	if persistErr := s.accountTestSvc.persistUpstreamModelRefreshFailure(ctx, account, err, time.Now(), s.staleGrace()); persistErr != nil {
		return fmt.Errorf("%w; persist refresh failure: %v", err, persistErr)
	}
	return err
}
