package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"
	"github.com/tidwall/gjson"
)

// SmartRouterCalibrationService owns the one daily, low-volume calibration
// cycle. It probes only the text/Responses capabilities implemented by the
// gateway calibration adapter. Image health is learned from real image
// requests; silently issuing paid image probes at 04:00 would be unsafe.
type SmartRouterCalibrationService struct {
	accountRepo  smartRouterCalibrationAccountSource
	modelRefresh smartRouterCalibrationModelRefresher
	gateway      *OpenAIGatewayService
	ledger       SmartRouterHealthLedger
	cfg          *config.Config
	cron         *cron.Cron
	startOnce    sync.Once
	stopOnce     sync.Once
	probeMu      sync.Mutex
}

type smartRouterCalibrationAccountSource interface {
	ListActive(ctx context.Context) ([]Account, error)
}

type smartRouterCalibrationModelRefresher interface {
	SyncUpstreamModelCatalog(ctx context.Context, account *Account) (*UpstreamModelCatalog, error)
}

func NewSmartRouterCalibrationService(accountRepo smartRouterCalibrationAccountSource, modelRefresh smartRouterCalibrationModelRefresher, gateway *OpenAIGatewayService, ledger SmartRouterHealthLedger, cfg *config.Config) *SmartRouterCalibrationService {
	return &SmartRouterCalibrationService{accountRepo: accountRepo, modelRefresh: modelRefresh, gateway: gateway, ledger: ledger, cfg: cfg}
}

// ProvideSmartRouterCalibrationService is kept in service wire.go so the
// scheduler, ledger and daily job are part of the production dependency graph.
func ProvideSmartRouterCalibrationService(accountRepo AccountRepository, accountTest *AccountTestService, gateway *OpenAIGatewayService, ledger SmartRouterHealthLedger, cfg *config.Config) *SmartRouterCalibrationService {
	svc := NewSmartRouterCalibrationService(accountRepo, accountTest, gateway, ledger, cfg)
	svc.Start()
	return svc
}

func (s *SmartRouterCalibrationService) Start() {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.SmartRouter.Enabled || !s.cfg.Gateway.SmartRouter.Calibration.Enabled || s.accountRepo == nil || s.gateway == nil || s.ledger == nil {
		return
	}
	s.startOnce.Do(func() {
		c := cron.New(cron.WithLocation(smartRouterCalibrationLocation()))
		expression := s.smartRouterCalibrationCronExpression()
		if _, err := c.AddFunc(expression, s.runCalibration); err != nil {
			slog.Warn("smart router calibration schedule rejected", "expression", expression, "error", err)
			return
		}
		s.cron = c
		c.Start()
		slog.Info("smart router calibration scheduled", "expression", expression, "timezone", "Asia/Shanghai")
	})
}

func (s *SmartRouterCalibrationService) Stop() {
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
			slog.Warn("smart router calibration stop timed out")
		}
	})
}

func (s *SmartRouterCalibrationService) smartRouterCalibrationCronExpression() string {
	if s == nil || s.cfg == nil {
		return "0 4 * * *"
	}
	c := s.cfg.Gateway.SmartRouter.Calibration
	hour, minute := smartRouterCalibrationSchedule(c.Hour, c.Minute)
	return fmt.Sprintf("%d %d * * *", minute, hour)
}

func (s *SmartRouterCalibrationService) runCalibration() {
	if s == nil || s.accountRepo == nil || s.gateway == nil || s.ledger == nil || s.cfg == nil || !s.probeMu.TryLock() {
		return
	}
	defer s.probeMu.Unlock()

	budget := time.Duration(s.cfg.Gateway.SmartRouter.Calibration.TotalBudgetSeconds) * time.Second
	if budget <= 0 {
		budget = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	now := time.Now()
	hour, minute := smartRouterCalibrationSchedule(s.cfg.Gateway.SmartRouter.Calibration.Hour, s.cfg.Gateway.SmartRouter.Calibration.Minute)
	scheduledFor := smartRouterScheduledFor(now, hour, minute)
	run, acquired, err := s.ledger.BeginCalibrationRun(ctx, scheduledFor)
	if err != nil || !acquired {
		return
	}
	success := true
	summary := "no probes required"
	defer func() {
		if err := s.ledger.FinishCalibrationRun(context.Background(), run.ID, success, summary); err != nil {
			slog.Warn("smart router calibration finish failed", "run_id", run.ID, "error", err)
		}
	}()

	accounts, err := s.accountRepo.ListActive(ctx)
	if err != nil {
		success = false
		summary = "list active accounts failed"
		return
	}
	if s.cfg.Gateway.SmartRouter.Calibration.AutoEnrollEnabled {
		if !s.refreshOpenAIModelCatalog(ctx, accounts) {
			success = false
		}
		if err := ctx.Err(); err != nil {
			summary = "calibration budget exhausted"
			return
		}
	}

	lanes := make([]smartrouter.LaneSnapshot, 0, len(accounts))
	accountsByLane := make(map[string]*Account, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if !account.IsOpenAI() {
			continue
		}
		lane, ok := s.gateway.smartRouterLaneSnapshot(account, nil, 0, 0, false)
		if !ok {
			continue
		}
		lanes = append(lanes, lane)
		accountsByLane[lane.LaneID] = account
	}
	evidenceRows, err := s.ledger.ListCapabilityEvidence(ctx)
	if err != nil {
		success = false
		summary = "load health evidence failed"
		return
	}
	probes := smartrouter.BuildCalibrationPlan(now, lanes, toCoreCapabilityEvidence(evidenceRows), smartRouterCalibrationPolicy(s.cfg))
	probes = textCalibrationProbes(probes)
	if len(probes) == 0 {
		if !success {
			summary = "model refresh completed with failures"
		}
		return
	}
	summary = fmt.Sprintf("probes=%d", len(probes))
	for _, probe := range probes {
		if err := ctx.Err(); err != nil {
			success = false
			summary = "calibration budget exhausted"
			return
		}
		account := accountsByLane[probe.LaneID]
		if account == nil {
			continue
		}
		result := s.runProbe(ctx, account, probe)
		if !result.Success {
			success = false
		}
		if err := s.ledger.RecordCalibrationResult(ctx, run.ID, result); err != nil {
			success = false
		}
	}
}

func textCalibrationProbes(probes []smartrouter.CalibrationProbe) []smartrouter.CalibrationProbe {
	filtered := make([]smartrouter.CalibrationProbe, 0, len(probes))
	for _, probe := range probes {
		switch probe.Capability {
		case smartrouter.CapabilityChat, smartrouter.CapabilityResponses, smartrouter.CapabilityResponsesCompact:
			filtered = append(filtered, probe)
		}
	}
	return filtered
}

// refreshOpenAIModelCatalog is deliberately best-effort. SyncUpstreamModelCatalog
// persists a new snapshot only after a complete refresh; on an error, the
// account's existing catalog remains the fallback for the subsequent probes.
func (s *SmartRouterCalibrationService) refreshOpenAIModelCatalog(ctx context.Context, accounts []Account) bool {
	if s == nil || s.modelRefresh == nil {
		return true
	}
	success := true
	for i := range accounts {
		if err := ctx.Err(); err != nil {
			return false
		}
		if !accounts[i].IsOpenAI() {
			continue
		}
		if _, err := s.modelRefresh.SyncUpstreamModelCatalog(ctx, &accounts[i]); err != nil {
			success = false
			slog.Warn("smart router model catalog refresh failed", "account_id", accounts[i].ID, "error", err)
		}
	}
	return success
}

func (s *SmartRouterCalibrationService) runProbe(ctx context.Context, account *Account, probe smartrouter.CalibrationProbe) SmartRouterCalibrationResult {
	model := s.calibrationModel(account, probe.Capability)
	result := SmartRouterCalibrationResult{LaneID: probe.LaneID, AccountID: account.ID, SourceGroup: smartRouterSourceGroup(account), Capability: probe.Capability, ModelFamily: model, Reason: probe.Reason}
	timeout := time.Duration(s.cfg.Gateway.SmartRouter.Calibration.ProbeTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch probe.Capability {
	case smartrouter.CapabilityChat:
		result.StatusCode, result.LatencyMs, _ = s.gateway.RunSmartRouterChatCalibrationProbe(probeCtx, account, model)
	case smartrouter.CapabilityResponses:
		result.StatusCode, result.LatencyMs, _ = s.gateway.RunSmartRouterResponsesCalibrationProbe(probeCtx, account, model)
	case smartrouter.CapabilityResponsesCompact:
		result.ModelFamily = s.compactCalibrationModel()
		result.StatusCode, result.LatencyMs, _ = s.gateway.RunSmartRouterCompactCalibrationProbe(probeCtx, account, result.ModelFamily)
	default:
		result.ErrorSummary = "unsupported calibration capability"
		return result
	}
	// The probe methods record the detailed health event. Reconstruct success
	// from the status because the result interface deliberately stays small.
	result.Success = result.StatusCode >= 200 && result.StatusCode < 300
	if !result.Success && result.ErrorSummary == "" {
		result.ErrorSummary = fmt.Sprintf("probe status %d", result.StatusCode)
	}
	return result
}

func (s *SmartRouterCalibrationService) calibrationModel(account *Account, capability smartrouter.Capability) string {
	if capability == smartrouter.CapabilityResponsesCompact {
		return s.compactCalibrationModel()
	}
	for _, model := range []string{"gpt-5.5", "gpt-5.4", "gpt-5.4-mini"} {
		for _, mapped := range account.GetModelMapping() {
			if strings.EqualFold(strings.TrimSpace(mapped), model) {
				return model
			}
		}
	}
	return "gpt-5.5"
}

func (s *SmartRouterCalibrationService) compactCalibrationModel() string {
	if s != nil && s.cfg != nil && strings.TrimSpace(s.cfg.Gateway.OpenAICompactModel) != "" {
		return strings.TrimSpace(s.cfg.Gateway.OpenAICompactModel)
	}
	return "gpt-5.5"
}

func smartRouterScheduledFor(now time.Time, hour, minute int) time.Time {
	local := now.In(smartRouterCalibrationLocation())
	return time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, smartRouterCalibrationLocation()).UTC()
}

func smartRouterCalibrationSchedule(hour, minute int) (int, int) {
	if hour == 0 && minute == 0 {
		return 4, 0
	}
	return hour, minute
}

func toCoreCapabilityEvidence(rows []SmartRouterCapabilityEvidence) []smartrouter.CapabilityEvidence {
	result := make([]smartrouter.CapabilityEvidence, 0, len(rows))
	for _, row := range rows {
		result = append(result, smartrouter.CapabilityEvidence{
			LaneID: row.LaneID, ChatKnown: row.ChatKnown, ChatLastSuccess: row.ChatLastSuccess, ChatLastFailure: row.ChatLastFailure, ChatRecoveryPriority: row.ChatRecoveryPriority,
			ResponsesKnown: row.ResponsesKnown, ResponsesLastSuccess: row.ResponsesLastSuccess, ResponsesLastFailure: row.ResponsesLastFailure, ResponsesRecoveryPriority: row.ResponsesRecoveryPriority,
			CompactKnown: row.CompactKnown, CompactLastSuccess: row.CompactLastSuccess, CompactLastFailure: row.CompactLastFailure, CompactRecoveryPriority: row.CompactRecoveryPriority,
		})
	}
	return result
}

func (s *OpenAIGatewayService) RunSmartRouterChatCalibrationProbe(ctx context.Context, account *Account, model string) (int, int64, error) {
	return s.runSmartRouterTextProbe(ctx, account, smartrouter.CapabilityChat, model, false)
}

func (s *OpenAIGatewayService) RunSmartRouterResponsesCalibrationProbe(ctx context.Context, account *Account, model string) (int, int64, error) {
	return s.runSmartRouterTextProbe(ctx, account, smartrouter.CapabilityResponses, model, false)
}

func (s *OpenAIGatewayService) RunSmartRouterCompactCalibrationProbe(ctx context.Context, account *Account, model string) (int, int64, error) {
	return s.runSmartRouterTextProbe(ctx, account, smartrouter.CapabilityResponsesCompact, model, true)
}

func (s *OpenAIGatewayService) runSmartRouterTextProbe(ctx context.Context, account *Account, capability smartrouter.Capability, model string, compact bool) (int, int64, error) {
	if s == nil || account == nil {
		return 0, 0, errors.New("smart router calibration account is required")
	}
	var payload any
	path := "/v1/responses"
	if capability == smartrouter.CapabilityChat {
		path = "/v1/chat/completions"
		payload = map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "Reply with OK."}}, "max_tokens": 1, "stream": false}
	} else {
		payload = map[string]any{"model": model, "input": "Reply with OK.", "max_output_tokens": 1, "stream": false}
		if compact {
			path = "/v1/responses/compact"
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, 0, err
	}
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)).WithContext(ctx)
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	started := time.Now()
	var result *OpenAIForwardResult
	if capability == smartrouter.CapabilityChat {
		result, err = s.ForwardAsChatCompletions(ctx, ginCtx, account, body, "", "")
	} else {
		result, err = s.Forward(ctx, ginCtx, account, body)
	}
	latency := time.Since(started).Milliseconds()
	status := smartRouterProbeStatusCode(err)
	if err == nil && result != nil {
		status = http.StatusOK
		if compact && !smartRouterCompactResponseContainsItem(recorder.Body.Bytes()) {
			err = errors.New("compact calibration returned no compaction output item")
			status = http.StatusBadGateway
		}
	}
	s.reportSmartRouterTextResult(account, capability, model, result, err, latency, "calibration")
	return status, latency, err
}

func smartRouterProbeStatusCode(err error) int {
	var failover *UpstreamFailoverError
	if errors.As(err, &failover) && failover != nil {
		return failover.StatusCode
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	if err == nil {
		return http.StatusOK
	}
	return 0
}

func smartRouterCompactResponseContainsItem(body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	if strings.TrimSpace(gjson.GetBytes(body, "compaction.encrypted_content").String()) != "" {
		return true
	}
	for _, item := range gjson.GetBytes(body, "output").Array() {
		if strings.Contains(item.Get("type").String(), "compaction") && strings.TrimSpace(item.Get("encrypted_content").String()) != "" {
			return true
		}
	}
	return false
}

func (s *OpenAIGatewayService) reportSmartRouterTextCalibrationResult(account *Account, capability smartrouter.Capability, model string, result *OpenAIForwardResult, err error, latencyMs int64) {
	s.reportSmartRouterTextResult(account, capability, model, result, err, latencyMs, "calibration")
}
