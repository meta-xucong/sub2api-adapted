package service

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
)

type smartRouterLedgerProbe struct {
	events []smartrouter.HealthEvent
	states []SmartRouterHealthState
}

type smartRouterCalibrationAccountSourceStub struct {
	accounts []Account
	calls    int
}

func (s *smartRouterCalibrationAccountSourceStub) ListActive(context.Context) ([]Account, error) {
	s.calls++
	return append([]Account(nil), s.accounts...), nil
}

type smartRouterModelRefreshStub struct {
	err   error
	calls int
}

func (s *smartRouterModelRefreshStub) SyncUpstreamModelCatalog(context.Context, *Account) (*UpstreamModelCatalog, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &UpstreamModelCatalog{}, nil
}

type smartRouterCalibrationLedgerStub struct {
	run      SmartRouterCalibrationRun
	begin    int
	finish   int
	evidence []SmartRouterCapabilityEvidence
}

func (l *smartRouterCalibrationLedgerStub) LoadStates(context.Context) ([]SmartRouterHealthState, error) {
	return nil, nil
}
func (l *smartRouterCalibrationLedgerStub) RecordEvent(context.Context, smartrouter.HealthEvent) error {
	return nil
}
func (l *smartRouterCalibrationLedgerStub) ListCapabilityEvidence(context.Context) ([]SmartRouterCapabilityEvidence, error) {
	return l.evidence, nil
}
func (l *smartRouterCalibrationLedgerStub) BeginCalibrationRun(context.Context, time.Time) (*SmartRouterCalibrationRun, bool, error) {
	l.begin++
	if l.begin > 1 {
		return &l.run, false, nil
	}
	return &l.run, true, nil
}
func (l *smartRouterCalibrationLedgerStub) RecordCalibrationResult(context.Context, int64, SmartRouterCalibrationResult) error {
	return nil
}
func (l *smartRouterCalibrationLedgerStub) FinishCalibrationRun(context.Context, int64, bool, string) error {
	l.finish++
	return nil
}

func (l *smartRouterLedgerProbe) LoadStates(context.Context) ([]SmartRouterHealthState, error) {
	return l.states, nil
}
func (l *smartRouterLedgerProbe) RecordEvent(_ context.Context, event smartrouter.HealthEvent) error {
	l.events = append(l.events, event)
	return nil
}
func (l *smartRouterLedgerProbe) ListCapabilityEvidence(context.Context) ([]SmartRouterCapabilityEvidence, error) {
	return nil, nil
}
func (l *smartRouterLedgerProbe) BeginCalibrationRun(context.Context, time.Time) (*SmartRouterCalibrationRun, bool, error) {
	return nil, false, nil
}
func (l *smartRouterLedgerProbe) RecordCalibrationResult(context.Context, int64, SmartRouterCalibrationResult) error {
	return nil
}
func (l *smartRouterLedgerProbe) FinishCalibrationRun(context.Context, int64, bool, string) error {
	return nil
}

func TestSmartRouterSchedulerUsesHealthLedgerAndCoreOrder(t *testing.T) {
	ledger := &smartRouterLedgerProbe{}
	cfg := &config.Config{}
	cfg.Gateway.SmartRouter.Enabled = true
	cfg.Gateway.SmartRouter.TopK = 2
	gateway := &OpenAIGatewayService{cfg: cfg, smartRouterHealthLedger: ledger}
	failed := smartRouterTestAccount(1, "failed")
	healthy := smartRouterTestAccount(2, "healthy")

	gateway.ReportSmartRouterChatResult(failed, "gpt-5.5", nil, &UpstreamFailoverError{StatusCode: 500}, 25)
	if len(ledger.events) != 1 {
		t.Fatalf("ledger events = %d, want 1", len(ledger.events))
	}

	scheduler := &defaultOpenAIAccountScheduler{service: gateway}
	ordered, applied := scheduler.buildSmartRouterSelectionOrder(OpenAIAccountScheduleRequest{RequestedModel: "gpt-5.5"}, openAIAccountLoadPlan{
		candidates: []openAIAccountCandidateScore{
			{account: failed, loadInfo: &AccountLoadInfo{AccountID: failed.ID}},
			{account: healthy, loadInfo: &AccountLoadInfo{AccountID: healthy.ID}},
		},
		topK: 2,
	})
	if !applied || len(ordered) != 1 || ordered[0].account.ID != healthy.ID {
		t.Fatalf("smart router order = %#v, applied=%v; want healthy account only", ordered, applied)
	}
}

func TestSmartRouterImageHealthUsesDedicatedCapability(t *testing.T) {
	ledger := &smartRouterLedgerProbe{}
	cfg := &config.Config{}
	cfg.Gateway.SmartRouter.Enabled = true
	gateway := &OpenAIGatewayService{cfg: cfg, smartRouterHealthLedger: ledger}
	account := smartRouterTestAccount(9, "image-source")
	gateway.ReportSmartRouterImageResult(account, "gpt-image-2", nil, &OpenAIImagesUpstreamError{StatusCode: 502, ErrorType: "upstream_error"}, 40)
	if len(ledger.events) != 1 {
		t.Fatalf("ledger events = %d, want 1", len(ledger.events))
	}
	if got := ledger.events[0].Key.Capability; got != smartrouter.CapabilityImageGeneration {
		t.Fatalf("image capability = %q, want %q", got, smartrouter.CapabilityImageGeneration)
	}
	if ledger.events[0].Key.Model != "gpt-image" {
		t.Fatalf("image model family = %q, want gpt-image", ledger.events[0].Key.Model)
	}
}

func TestSmartRouterStartupRestorePreservesRecoveryPriority(t *testing.T) {
	ledger := &smartRouterLedgerProbe{states: []SmartRouterHealthState{{
		LaneID: "account:1", SourceGroup: "failed", Capability: smartrouter.CapabilityChat, ModelFamily: "gpt-5.5",
		Snapshot: smartrouter.HealthSnapshot{RecoveryPriority: 30, HealthPenalty: 2, HealthScore: 0.2, RecoveryStage: smartrouter.RecoveryCooling},
	}}}
	cfg := &config.Config{}
	cfg.Gateway.SmartRouter.Enabled = true
	cfg.Gateway.SmartRouter.TopK = 2
	gateway := &OpenAIGatewayService{cfg: cfg, smartRouterHealthLedger: ledger}
	failed := smartRouterTestAccount(1, "failed")
	healthy := smartRouterTestAccount(2, "healthy")
	scheduler := &defaultOpenAIAccountScheduler{service: gateway}
	ordered, applied := scheduler.buildSmartRouterSelectionOrder(OpenAIAccountScheduleRequest{RequestedModel: "gpt-5.5"}, openAIAccountLoadPlan{
		candidates: []openAIAccountCandidateScore{
			{account: failed, loadInfo: &AccountLoadInfo{AccountID: failed.ID}},
			{account: healthy, loadInfo: &AccountLoadInfo{AccountID: healthy.ID}},
		},
		topK: 2,
	})
	if !applied || len(ordered) != 1 || ordered[0].account.ID != healthy.ID {
		t.Fatalf("restored smart router order = %#v, applied=%v; want healthy account only", ordered, applied)
	}
}

func TestSmartRouterCalibrationUsesShanghaiFourAM(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.SmartRouter.Calibration.Hour = 4
	cfg.Gateway.SmartRouter.Calibration.Minute = 0
	service := NewSmartRouterCalibrationService(nil, nil, nil, nil, cfg)
	if got := service.smartRouterCalibrationCronExpression(); got != "0 4 * * *" {
		t.Fatalf("cron expression = %q, want %q", got, "0 4 * * *")
	}
	got := smartRouterScheduledFor(time.Date(2026, 9, 30, 20, 0, 0, 0, time.UTC), 4, 0)
	want := time.Date(2026, 9, 30, 20, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("scheduled_for = %s, want %s", got, want)
	}
	before := smartRouterScheduledFor(time.Date(2026, 9, 30, 19, 59, 59, 0, time.UTC), 4, 0)
	after := smartRouterScheduledFor(time.Date(2026, 9, 30, 20, 0, 0, 0, time.UTC), 4, 0)
	if !before.Equal(time.Date(2026, 9, 30, 20, 0, 0, 0, time.UTC)) || !after.Equal(before) {
		t.Fatalf("Shanghai 04:00 boundary = before %s, after %s", before, after)
	}
	nextDay := smartRouterScheduledFor(time.Date(2026, 10, 1, 20, 0, 0, 0, time.UTC), 4, 0)
	if !nextDay.Equal(time.Date(2026, 10, 1, 20, 0, 0, 0, time.UTC)) {
		t.Fatalf("next Shanghai day scheduled_for = %s", nextDay)
	}
}

func TestSmartRouterCalibrationDeduplicatesScheduledRun(t *testing.T) {
	accounts := &smartRouterCalibrationAccountSourceStub{}
	ledger := &smartRouterCalibrationLedgerStub{run: SmartRouterCalibrationRun{ID: 41}}
	cfg := &config.Config{}
	cfg.Gateway.SmartRouter.Enabled = true
	cfg.Gateway.SmartRouter.Calibration.Enabled = true
	cfg.Gateway.SmartRouter.Calibration.Hour = 4
	service := NewSmartRouterCalibrationService(accounts, nil, &OpenAIGatewayService{}, ledger, cfg)

	service.runCalibration()
	service.runCalibration()

	if ledger.begin != 2 || ledger.finish != 1 {
		t.Fatalf("calibration begin/finish = %d/%d, want 2/1", ledger.begin, ledger.finish)
	}
	if accounts.calls != 1 {
		t.Fatalf("active account loads = %d, want one acquired run", accounts.calls)
	}
}

func TestSmartRouterCalibrationRefreshFailureKeepsExistingCatalog(t *testing.T) {
	account := *smartRouterTestAccount(7, "openai")
	old := UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
		"gpt-5.5": {ID: "gpt-5.5", DisplayName: "old catalog", ContextWindow: 128000},
	}}
	account.SetUpstreamModelMetadataSnapshot(old)
	accounts := &smartRouterCalibrationAccountSourceStub{accounts: []Account{account}}
	refresher := &smartRouterModelRefreshStub{err: errors.New("registry unavailable")}
	service := NewSmartRouterCalibrationService(accounts, refresher, nil, nil, &config.Config{})

	if service.refreshOpenAIModelCatalog(context.Background(), accounts.accounts) {
		t.Fatal("refresh should report failure")
	}
	if refresher.calls != 1 {
		t.Fatalf("model refresh calls = %d, want 1", refresher.calls)
	}
	if got := accounts.accounts[0].GetUpstreamModelMetadataSnapshot(); !reflect.DeepEqual(got, &old) {
		t.Fatalf("existing catalog changed after refresh failure: %#v", got)
	}
}

func TestSmartRouterCalibrationStartStopDoesNotLeakCronGoroutine(t *testing.T) {
	before := runtime.NumGoroutine()
	cfg := &config.Config{}
	cfg.Gateway.SmartRouter.Enabled = true
	cfg.Gateway.SmartRouter.Calibration.Enabled = true
	service := NewSmartRouterCalibrationService(
		&smartRouterCalibrationAccountSourceStub{},
		nil,
		&OpenAIGatewayService{},
		&smartRouterCalibrationLedgerStub{run: SmartRouterCalibrationRun{ID: 42}},
		cfg,
	)
	service.Start()
	service.Start()
	if service.cron == nil || service.smartRouterCalibrationCronExpression() != "0 4 * * *" {
		t.Fatalf("calibration cron was not started with the 04:00 default")
	}
	service.Stop()
	service.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && runtime.NumGoroutine() > before+2 {
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before+2 {
		t.Fatalf("goroutines after calibration stop = %d, baseline %d", got, before)
	}
}

func smartRouterTestAccount(id int64, sourceGroup string) *Account {
	return &Account{
		ID: id, Name: sourceGroup, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test", "base_url": "https://api.openai.com/v1"},
		Extra:       map[string]any{"openai_responses_supported": true, "smart_router_source_group": sourceGroup},
		Priority:    1,
	}
}

var _ SmartRouterHealthLedger = (*smartRouterLedgerProbe)(nil)
