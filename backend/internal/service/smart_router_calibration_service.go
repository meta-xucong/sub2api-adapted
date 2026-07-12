package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"
)

const smartRouterCalibrationModel = "gpt-image-2"

// SmartRouterCalibrationService performs one low-volume, capability-aware
// probe cycle at the configured Shanghai calendar time. The ledger's unique
// scheduled_for key prevents duplicate runs if a process is restarted.
type SmartRouterCalibrationService struct {
	accountRepo AccountRepository
	gateway     *OpenAIGatewayService
	ledger      SmartRouterHealthLedger
	cfg         *config.Config

	cron      *cron.Cron
	startOnce sync.Once
	stopOnce  sync.Once
}

func NewSmartRouterCalibrationService(
	accountRepo AccountRepository,
	gateway *OpenAIGatewayService,
	ledger SmartRouterHealthLedger,
	cfg *config.Config,
) *SmartRouterCalibrationService {
	return &SmartRouterCalibrationService{
		accountRepo: accountRepo,
		gateway:     gateway,
		ledger:      ledger,
		cfg:         cfg,
	}
}

func (s *SmartRouterCalibrationService) Start() {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.SmartRouter.Enabled || !s.cfg.Gateway.SmartRouter.Calibration.Enabled || s.ledger == nil || s.gateway == nil || s.accountRepo == nil {
		return
	}
	s.startOnce.Do(func() {
		loc := smartRouterCalibrationLocation()
		c := cron.New(cron.WithLocation(loc))
		_, err := c.AddFunc(s.smartRouterCalibrationCronExpression(), func() {
			s.runCalibration()
		})
		if err != nil {
			logger.LegacyPrintf("service.smart_router_calibration", "Smart Router calibration not started: %v", err)
			return
		}
		s.cron = c
		c.Start()
		logger.LegacyPrintf("service.smart_router_calibration", "Smart Router calibration scheduled at %s Asia/Shanghai", s.smartRouterCalibrationCronExpression())
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
			logger.LegacyPrintf("service.smart_router_calibration", "Smart Router calibration stop timed out")
		}
	})
}

func (s *SmartRouterCalibrationService) smartRouterCalibrationCronExpression() string {
	if s == nil || s.cfg == nil {
		return "0 4 * * *"
	}
	cfg := s.cfg.Gateway.SmartRouter.Calibration
	return fmt.Sprintf("%d %d * * *", cfg.Minute, cfg.Hour)
}

func (s *SmartRouterCalibrationService) runCalibration() {
	if s == nil || s.ledger == nil || s.gateway == nil || s.accountRepo == nil || s.cfg == nil {
		return
	}
	budget := time.Duration(s.cfg.Gateway.SmartRouter.Calibration.TotalBudgetSeconds) * time.Second
	if budget <= 0 {
		budget = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	now := time.Now()
	scheduledFor := smartRouterScheduledFor(now, s.cfg.Gateway.SmartRouter.Calibration.Hour, s.cfg.Gateway.SmartRouter.Calibration.Minute)
	run, acquired, err := s.ledger.BeginCalibrationRun(ctx, scheduledFor)
	if err != nil {
		logger.LegacyPrintf("service.smart_router_calibration", "begin calibration failed: %v", err)
		return
	}
	if !acquired {
		return
	}

	success := true
	summary := "no probes required"
	defer func() {
		if err := s.ledger.FinishCalibrationRun(context.Background(), run.ID, success, summary); err != nil {
			logger.LegacyPrintf("service.smart_router_calibration", "finish calibration failed: %v", err)
		}
	}()

	accounts, err := s.accountRepo.ListActive(ctx)
	if err != nil {
		success = false
		summary = "list active accounts failed"
		logger.LegacyPrintf("service.smart_router_calibration", "%s: %v", summary, err)
		return
	}
	lanes, accountsByLane := s.imageCalibrationLanes(accounts)
	evidenceRows, err := s.ledger.ListCapabilityEvidence(ctx)
	if err != nil {
		success = false
		summary = "load health evidence failed"
		logger.LegacyPrintf("service.smart_router_calibration", "%s: %v", summary, err)
		return
	}
	probes := smartrouter.BuildCalibrationPlan(now, lanes, toCoreCapabilityEvidence(evidenceRows), smartrouter.CalibrationPolicy{
		Location:            smartRouterCalibrationLocation(),
		Hour:                s.cfg.Gateway.SmartRouter.Calibration.Hour,
		Minute:              s.cfg.Gateway.SmartRouter.Calibration.Minute,
		FreshEvidenceWindow: 24 * time.Hour,
	})
	if len(probes) == 0 {
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
			logger.LegacyPrintf("service.smart_router_calibration", "record calibration result failed: %v", err)
		}
	}
}

func (s *SmartRouterCalibrationService) imageCalibrationLanes(accounts []Account) ([]smartrouter.LaneSnapshot, map[string]*Account) {
	lanes := make([]smartrouter.LaneSnapshot, 0)
	accountsByLane := make(map[string]*Account)
	for index := range accounts {
		account := &accounts[index]
		if !account.IsOpenAI() || !account.SupportsOpenAIImageCapability(OpenAIImagesCapabilityBasic) {
			continue
		}
		if err := validateOpenAIImagesModelForAccount(account, smartRouterCalibrationModel); err != nil {
			continue
		}
		lane, ok := smartRouterLaneSnapshot(account, nil, 0, 0, false)
		if !ok || strings.TrimSpace(lane.LaneID) == "" {
			continue
		}
		if len(lane.Capabilities) == 0 {
			lane.Capabilities = map[smartrouter.Capability]bool{
				smartrouter.CapabilityImageGeneration: true,
				smartrouter.CapabilityImageEdit:       true,
			}
		}
		lanes = append(lanes, lane)
		accountsByLane[lane.LaneID] = account
	}
	return lanes, accountsByLane
}

func (s *SmartRouterCalibrationService) runProbe(ctx context.Context, account *Account, probe smartrouter.CalibrationProbe) SmartRouterCalibrationResult {
	result := SmartRouterCalibrationResult{
		LaneID:      probe.LaneID,
		AccountID:   account.ID,
		SourceGroup: smartRouterSourceGroup(account),
		Capability:  probe.Capability,
		ModelFamily: "gpt-image",
		Reason:      probe.Reason,
	}
	timeout := time.Duration(s.cfg.Gateway.SmartRouter.Calibration.ProbeTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	statusCode, latencyMs, err := s.gateway.RunSmartRouterImageCalibrationProbe(probeCtx, account, probe.Capability)
	result.StatusCode = statusCode
	result.LatencyMs = latencyMs
	result.Success = err == nil
	if err != nil {
		result.ErrorSummary = safeSmartRouterProbeError(err)
	}
	return result
}

// RunSmartRouterImageCalibrationProbe directly tests one account so a daily
// probe cannot recursively enter normal account selection or consume user quota.
func (s *OpenAIGatewayService) RunSmartRouterImageCalibrationProbe(ctx context.Context, account *Account, capability smartrouter.Capability) (int, int64, error) {
	if s == nil || account == nil {
		return 0, 0, errors.New("smart router image calibration account is required")
	}
	body, contentType, endpoint, err := smartRouterCalibrationRequest(capability)
	if err != nil {
		return 0, 0, err
	}
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", contentType)
	ginCtx.Request = req
	parsed, err := s.ParseOpenAIImagesRequest(ginCtx, body)
	if err != nil {
		return 0, 0, err
	}

	startedAt := time.Now()
	forwardResult, forwardErr := s.ForwardImages(ctx, ginCtx, account, body, parsed, "")
	latencyMs := time.Since(startedAt).Milliseconds()
	statusCode := smartRouterProbeStatusCode(forwardErr)
	if forwardErr == nil && forwardResult != nil && forwardResult.ImageCount > 0 {
		statusCode = http.StatusOK
	}
	if forwardErr == nil && (forwardResult == nil || forwardResult.ImageCount == 0) {
		forwardErr = errors.New("image calibration returned no image")
		statusCode = http.StatusBadGateway
	}
	s.ReportSmartRouterImageCalibrationResult(account, parsed, forwardResult, forwardErr, latencyMs)
	return statusCode, latencyMs, forwardErr
}

func smartRouterCalibrationRequest(capability smartrouter.Capability) ([]byte, string, string, error) {
	if capability == smartrouter.CapabilityImageGeneration {
		body, err := json.Marshal(map[string]any{
			"model":  smartRouterCalibrationModel,
			"prompt": "A single small red circle on a plain white background. No text.",
			"n":      1,
		})
		return body, "application/json", openAIImagesGenerationsEndpoint, err
	}
	if capability != smartrouter.CapabilityImageEdit {
		return nil, "", "", fmt.Errorf("unsupported smart router calibration capability: %s", capability)
	}
	fixture, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL+XQAAAABJRU5ErkJggg==")
	if err != nil {
		return nil, "", "", err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", smartRouterCalibrationModel); err != nil {
		return nil, "", "", err
	}
	if err := writer.WriteField("prompt", "Keep the image unchanged except make the red circle blue. No text."); err != nil {
		return nil, "", "", err
	}
	if err := writer.WriteField("n", "1"); err != nil {
		return nil, "", "", err
	}
	part, err := writer.CreateFormFile("image", "smart-router-probe.png")
	if err != nil {
		return nil, "", "", err
	}
	if _, err := part.Write(fixture); err != nil {
		return nil, "", "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", "", err
	}
	return body.Bytes(), writer.FormDataContentType(), openAIImagesEditsEndpoint, nil
}

func smartRouterProbeStatusCode(err error) int {
	if err == nil {
		return http.StatusOK
	}
	var imageErr *OpenAIImagesUpstreamError
	if errors.As(err, &imageErr) && imageErr != nil {
		return imageErr.StatusCode
	}
	var failoverErr *UpstreamFailoverError
	if errors.As(err, &failoverErr) && failoverErr != nil {
		return failoverErr.StatusCode
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	return 0
}

func safeSmartRouterProbeError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 256 {
		message = message[:256]
	}
	return message
}

func smartRouterCalibrationLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return loc
}

func smartRouterScheduledFor(now time.Time, hour, minute int) time.Time {
	loc := smartRouterCalibrationLocation()
	local := now.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc).UTC()
}

func toCoreCapabilityEvidence(rows []SmartRouterCapabilityEvidence) []smartrouter.CapabilityEvidence {
	evidence := make([]smartrouter.CapabilityEvidence, 0, len(rows))
	for _, row := range rows {
		modesDiverged := row.GenerationLastFailure.After(row.GenerationLastSuccess) != row.EditLastFailure.After(row.EditLastSuccess)
		evidence = append(evidence, smartrouter.CapabilityEvidence{
			LaneID:                row.LaneID,
			GenerationKnown:       row.GenerationKnown,
			EditKnown:             row.EditKnown,
			GenerationLastSuccess: row.GenerationLastSuccess,
			GenerationLastFailure: row.GenerationLastFailure,
			EditLastSuccess:       row.EditLastSuccess,
			EditLastFailure:       row.EditLastFailure,
			ModesDiverged:         modesDiverged,
		})
	}
	return evidence
}
