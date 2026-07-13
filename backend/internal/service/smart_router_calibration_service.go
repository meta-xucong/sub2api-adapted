package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"
	"github.com/tidwall/gjson"
)

const (
	smartRouterCalibrationModel     = "gpt-image-2"
	smartRouterCalibrationTextModel = "gpt-5.5"
)

// SmartRouterCalibrationService performs one low-volume, capability-aware
// probe cycle at the configured Shanghai calendar time. The ledger's unique
// scheduled_for key prevents duplicate runs if a process is restarted.
type SmartRouterCalibrationService struct {
	accountRepo AccountRepository
	gateway     *OpenAIGatewayService
	ledger      SmartRouterHealthLedger
	cfg         *config.Config

	cron       *cron.Cron
	startOnce  sync.Once
	stopOnce   sync.Once
	probeMu    sync.Mutex
	enrollMu   sync.Mutex
	enrollSeen map[int64]string
	enrollStop context.CancelFunc
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
		if s.cfg.Gateway.SmartRouter.Calibration.AutoEnrollEnabled {
			s.startAutoEnrollment()
		}
	})
}

func (s *SmartRouterCalibrationService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.enrollStop != nil {
			s.enrollStop()
		}
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

func (s *SmartRouterCalibrationService) startAutoEnrollment() {
	if s == nil || s.accountRepo == nil || s.cfg == nil {
		return
	}
	accounts, err := s.accountRepo.ListActive(context.Background())
	if err != nil {
		logger.LegacyPrintf("service.smart_router_calibration", "auto-enrollment baseline failed: %v", err)
		return
	}
	s.enrollMu.Lock()
	s.enrollSeen = make(map[int64]string, len(accounts))
	for index := range accounts {
		s.enrollSeen[accounts[index].ID] = smartRouterEnrollmentFingerprint(&accounts[index])
	}
	s.enrollMu.Unlock()
	interval := time.Duration(s.cfg.Gateway.SmartRouter.Calibration.AutoEnrollIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.enrollStop = cancel
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.runAutoEnrollment()
			case <-ctx.Done():
				return
			}
		}
	}()
	logger.LegacyPrintf("service.smart_router_calibration", "Smart Router auto-enrollment enabled; reconciliation interval=%s", interval)
}

func (s *SmartRouterCalibrationService) runAutoEnrollment() {
	if s == nil || s.accountRepo == nil || s.gateway == nil || s.cfg == nil {
		return
	}
	if !s.probeMu.TryLock() {
		return
	}
	defer s.probeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), smartRouterEnrollmentBudget(s.cfg))
	defer cancel()
	accounts, err := s.accountRepo.ListActive(ctx)
	if err != nil {
		logger.LegacyPrintf("service.smart_router_calibration", "auto-enrollment account scan failed: %v", err)
		return
	}
	if s.enrollSeen == nil {
		s.enrollSeen = make(map[int64]string)
	}
	for index := range accounts {
		if ctx.Err() != nil {
			return
		}
		account := &accounts[index]
		fingerprint := smartRouterEnrollmentFingerprint(account)
		s.enrollMu.Lock()
		previous, known := s.enrollSeen[account.ID]
		if known && previous == fingerprint {
			s.enrollMu.Unlock()
			continue
		}
		s.enrollSeen[account.ID] = fingerprint
		s.enrollMu.Unlock()
		if !smartRouterAutoEnrollmentEligible(account) {
			continue
		}
		probes := smartRouterAutoEnrollmentProbes(account)
		if len(probes) == 0 {
			_ = s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{
				"smart_router_enrollment": map[string]any{
					"status":      "pending_capability",
					"fingerprint": fingerprint,
					"checked_at":  time.Now().UTC().Format(time.RFC3339),
					"last_error":  "no declared or inferred capability",
				},
			})
			continue
		}
		allSuccess := true
		lastError := ""
		for _, probe := range probes {
			result := s.runProbe(ctx, account, probe)
			if !result.Success {
				allSuccess = false
				lastError = result.ErrorSummary
			}
		}
		status := "ready"
		if !allSuccess {
			status = "degraded"
		}
		updates := map[string]any{
			"smart_router_enrollment": map[string]any{
				"status":       status,
				"fingerprint":  fingerprint,
				"checked_at":   time.Now().UTC().Format(time.RFC3339),
				"capabilities": smartRouterEnrollmentCapabilityNames(probes),
				"last_error":   truncateString(sanitizeUpstreamErrorMessage(lastError), 256),
			},
		}
		if err := s.accountRepo.UpdateExtra(ctx, account.ID, updates); err != nil {
			logger.LegacyPrintf("service.smart_router_calibration", "auto-enrollment state update failed for account %d: %v", account.ID, err)
		}
	}
}

func smartRouterEnrollmentBudget(cfg *config.Config) time.Duration {
	if cfg != nil && cfg.Gateway.SmartRouter.Calibration.TotalBudgetSeconds > 0 {
		return time.Duration(cfg.Gateway.SmartRouter.Calibration.TotalBudgetSeconds) * time.Second
	}
	return 10 * time.Minute
}

func smartRouterAutoEnrollmentEligible(account *Account) bool {
	if account == nil || !account.IsOpenAI() || !account.IsActive() || !account.Schedulable {
		return false
	}
	if account.TempUnschedulableUntil != nil && time.Now().Before(*account.TempUnschedulableUntil) {
		return false
	}
	_, ok := smartRouterLaneSnapshot(account, nil, 0, 0, false)
	return ok
}

func smartRouterAutoEnrollmentProbes(account *Account) []smartrouter.CalibrationProbe {
	if account == nil {
		return nil
	}
	lane, ok := smartRouterLaneSnapshot(account, nil, 0, 0, false)
	if !ok {
		return nil
	}
	capabilities := make(map[smartrouter.Capability]bool)
	for capability := range smartRouterCalibrationTextCapabilities(account) {
		capabilities[capability] = true
	}
	if smartRouterAccountHasImageMapping(account) {
		capabilities[smartrouter.CapabilityImageGeneration] = true
		extra := parseSmartRouterAccountExtra(account)
		if extra.Capabilities[smartrouter.CapabilityImageEdit] {
			capabilities[smartrouter.CapabilityImageEdit] = true
		}
	}
	extra := parseSmartRouterAccountExtra(account)
	if account.AllowsOpenAICompact() && (!extra.CapabilitiesSet || extra.Capabilities[smartrouter.CapabilityResponsesCompact]) {
		capabilities[smartrouter.CapabilityResponsesCompact] = true
	}
	ordered := []smartrouter.Capability{
		smartrouter.CapabilityResponses,
		smartrouter.CapabilityChat,
		smartrouter.CapabilityImageGeneration,
		smartrouter.CapabilityImageEdit,
		smartrouter.CapabilityResponsesCompact,
	}
	probes := make([]smartrouter.CalibrationProbe, 0, len(capabilities))
	for _, capability := range ordered {
		if capabilities[capability] {
			probes = append(probes, smartrouter.CalibrationProbe{LaneID: lane.LaneID, Capability: capability, Reason: "new_or_changed_lane"})
		}
	}
	return probes
}

func smartRouterEnrollmentCapabilityNames(probes []smartrouter.CalibrationProbe) []string {
	result := make([]string, 0, len(probes))
	for _, probe := range probes {
		result = append(result, string(probe.Capability))
	}
	return result
}

func smartRouterEnrollmentFingerprint(account *Account) string {
	if account == nil {
		return ""
	}
	baseURL := strings.TrimSpace(account.GetCredential("base_url"))
	if parsed, err := url.Parse(baseURL); err == nil {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		baseURL = parsed.String()
	}
	mapping, _ := json.Marshal(account.GetModelMapping())
	groups := append([]int64(nil), account.GroupIDs...)
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })
	extra := parseSmartRouterAccountExtra(account)
	capabilityNames := make([]string, 0, len(extra.Capabilities))
	for capability, enabled := range extra.Capabilities {
		if enabled {
			capabilityNames = append(capabilityNames, string(capability))
		}
	}
	sort.Strings(capabilityNames)
	material := fmt.Sprintf("%d|%s|%s|%s|%s|%s|%v|%d|%d|%s", account.ID, account.Name, account.Platform, account.Type, baseURL, mapping, groups, account.Priority, account.Concurrency, strings.Join(capabilityNames, ","))
	digest := sha256.Sum256([]byte(material))
	return fmt.Sprintf("%x", digest[:])
}

func (s *SmartRouterCalibrationService) runCalibration() {
	if s == nil || s.ledger == nil || s.gateway == nil || s.accountRepo == nil || s.cfg == nil {
		return
	}
	if !s.probeMu.TryLock() {
		logger.LegacyPrintf("service.smart_router_calibration", "calibration skipped because another probe cycle is active")
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
	lanes, accountsByLane := s.ordinaryCalibrationLanes(accounts)
	imageLanes, imageAccounts := s.imageCalibrationLanes(accounts)
	lanes, accountsByLane = mergeSmartRouterCalibrationLanes(lanes, accountsByLane, imageLanes, imageAccounts)
	compactLanes, compactAccounts := s.compactCalibrationLanes(accounts)
	lanes, accountsByLane = mergeSmartRouterCalibrationLanes(lanes, accountsByLane, compactLanes, compactAccounts)
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
		if probe.Capability == smartrouter.CapabilityResponsesCompact {
			if updates := buildSmartRouterCompactProbeExtraUpdates(result, time.Now()); len(updates) > 0 {
				if err := s.accountRepo.UpdateExtra(ctx, account.ID, updates); err != nil {
					success = false
					logger.LegacyPrintf("service.smart_router_calibration", "persist compact probe result failed: %v", err)
				}
			}
		}
	}
}

func (s *SmartRouterCalibrationService) imageCalibrationLanes(accounts []Account) ([]smartrouter.LaneSnapshot, map[string]*Account) {
	lanes := make([]smartrouter.LaneSnapshot, 0)
	accountsByLane := make(map[string]*Account)
	for index := range accounts {
		account := &accounts[index]
		if !account.IsOpenAI() || !account.SupportsOpenAIImageCapability(OpenAIImagesCapabilityBasic) || !smartRouterAccountHasImageMapping(account) {
			continue
		}
		if err := validateOpenAIImagesModelForAccount(account, smartRouterCalibrationModel); err != nil {
			continue
		}
		lane, ok := smartRouterLaneSnapshot(account, nil, 0, 0, false)
		if !ok || strings.TrimSpace(lane.LaneID) == "" {
			continue
		}
		if !parseSmartRouterAccountExtra(account).CapabilitiesSet {
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

func (s *SmartRouterCalibrationService) ordinaryCalibrationLanes(accounts []Account) ([]smartrouter.LaneSnapshot, map[string]*Account) {
	lanes := make([]smartrouter.LaneSnapshot, 0)
	accountsByLane := make(map[string]*Account)
	for index := range accounts {
		account := &accounts[index]
		if !account.IsOpenAI() {
			continue
		}
		lane, ok := smartRouterLaneSnapshot(account, nil, 0, 0, false)
		if !ok || strings.TrimSpace(lane.LaneID) == "" {
			continue
		}
		capabilities := smartRouterCalibrationTextCapabilities(account)
		if len(capabilities) == 0 {
			continue
		}
		if lane.Capabilities == nil {
			lane.Capabilities = make(map[smartrouter.Capability]bool)
		}
		for capability := range capabilities {
			lane.Capabilities[capability] = true
		}
		lanes = append(lanes, lane)
		accountsByLane[lane.LaneID] = account
	}
	return lanes, accountsByLane
}

func smartRouterCalibrationTextCapabilities(account *Account) map[smartrouter.Capability]bool {
	if account == nil {
		return nil
	}
	extra := parseSmartRouterAccountExtra(account)
	if extra.CapabilitiesSet {
		result := make(map[smartrouter.Capability]bool)
		for _, capability := range []smartrouter.Capability{smartrouter.CapabilityChat, smartrouter.CapabilityResponses} {
			if extra.Capabilities[capability] {
				result[capability] = true
			}
		}
		return result
	}
	if smartRouterAccountHasImageMapping(account) {
		return nil
	}
	if account.Type == AccountTypeOAuth || openai_compat.ShouldUseResponsesAPI(account.Extra) {
		return map[smartrouter.Capability]bool{smartrouter.CapabilityResponses: true}
	}
	return map[smartrouter.Capability]bool{smartrouter.CapabilityChat: true}
}

func smartRouterAccountHasImageMapping(account *Account) bool {
	if account == nil {
		return false
	}
	extra := parseSmartRouterAccountExtra(account)
	if extra.CapabilitiesSet {
		return extra.Capabilities[smartrouter.CapabilityImageGeneration] || extra.Capabilities[smartrouter.CapabilityImageEdit]
	}
	for pattern := range account.GetModelMapping() {
		lower := strings.ToLower(strings.TrimSpace(pattern))
		if strings.HasPrefix(lower, "gpt-image") || strings.HasPrefix(lower, "image-") {
			return true
		}
	}
	return false
}

func (s *SmartRouterCalibrationService) compactCalibrationLanes(accounts []Account) ([]smartrouter.LaneSnapshot, map[string]*Account) {
	lanes := make([]smartrouter.LaneSnapshot, 0)
	accountsByLane := make(map[string]*Account)
	for index := range accounts {
		account := &accounts[index]
		if !account.IsOpenAI() || !account.AllowsOpenAICompact() {
			continue
		}
		lane, ok := smartRouterLaneSnapshot(account, nil, 0, 0, false)
		if !ok || strings.TrimSpace(lane.LaneID) == "" {
			continue
		}
		if lane.Capabilities == nil {
			lane.Capabilities = map[smartrouter.Capability]bool{}
		}
		lane.Capabilities[smartrouter.CapabilityResponsesCompact] = true
		lanes = append(lanes, lane)
		accountsByLane[lane.LaneID] = account
	}
	return lanes, accountsByLane
}

func mergeSmartRouterCalibrationLanes(
	base []smartrouter.LaneSnapshot,
	accountsByLane map[string]*Account,
	additional []smartrouter.LaneSnapshot,
	additionalAccounts map[string]*Account,
) ([]smartrouter.LaneSnapshot, map[string]*Account) {
	indexByLane := make(map[string]int, len(base))
	for index := range base {
		indexByLane[base[index].LaneID] = index
	}
	for _, lane := range additional {
		if index, exists := indexByLane[lane.LaneID]; exists {
			if base[index].Capabilities == nil {
				base[index].Capabilities = map[smartrouter.Capability]bool{}
			}
			for capability, enabled := range lane.Capabilities {
				if enabled {
					base[index].Capabilities[capability] = true
				}
			}
			continue
		}
		indexByLane[lane.LaneID] = len(base)
		base = append(base, lane)
	}
	if accountsByLane == nil {
		accountsByLane = make(map[string]*Account)
	}
	for laneID, account := range additionalAccounts {
		accountsByLane[laneID] = account
	}
	return base, accountsByLane
}

func (s *SmartRouterCalibrationService) runProbe(ctx context.Context, account *Account, probe smartrouter.CalibrationProbe) SmartRouterCalibrationResult {
	model := s.calibrationModelForCapability(account, probe.Capability)
	result := SmartRouterCalibrationResult{
		LaneID:      probe.LaneID,
		AccountID:   account.ID,
		SourceGroup: smartRouterSourceGroup(account),
		Capability:  probe.Capability,
		ModelFamily: model,
		Reason:      probe.Reason,
	}
	timeout := time.Duration(s.cfg.Gateway.SmartRouter.Calibration.ProbeTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var statusCode int
	var latencyMs int64
	var err error
	switch probe.Capability {
	case smartrouter.CapabilityResponsesCompact:
		model = s.compactCalibrationModel()
		result.ModelFamily = model
		statusCode, latencyMs, err = s.gateway.RunSmartRouterCompactCalibrationProbe(probeCtx, account, model)
	case smartrouter.CapabilityResponses:
		statusCode, latencyMs, err = s.gateway.RunSmartRouterResponsesCalibrationProbe(probeCtx, account, model)
	case smartrouter.CapabilityChat:
		statusCode, latencyMs, err = s.gateway.RunSmartRouterChatCalibrationProbe(probeCtx, account, model)
	default:
		statusCode, latencyMs, err = s.gateway.RunSmartRouterImageCalibrationProbe(probeCtx, account, probe.Capability)
	}
	result.StatusCode = statusCode
	result.LatencyMs = latencyMs
	result.Success = err == nil
	if err != nil {
		result.ErrorSummary = safeSmartRouterProbeError(err)
	}
	return result
}

func (s *SmartRouterCalibrationService) calibrationModelForCapability(account *Account, capability smartrouter.Capability) string {
	if capability == smartrouter.CapabilityImageGeneration || capability == smartrouter.CapabilityImageEdit {
		return smartRouterCalibrationModel
	}
	if capability == smartrouter.CapabilityResponsesCompact {
		return s.compactCalibrationModel()
	}
	preferred := []string{"gpt-5.5", "gpt-5.4", "gpt-5.4-mini"}
	if account != nil {
		mapping := account.GetModelMapping()
		patterns := make([]string, 0, len(mapping))
		for pattern := range mapping {
			patterns = append(patterns, strings.TrimSpace(pattern))
		}
		sort.Strings(patterns)
		for _, candidate := range preferred {
			for _, pattern := range patterns {
				if smartRouterModelPatternMatches(pattern, candidate) {
					return candidate
				}
			}
		}
		for _, pattern := range patterns {
			lower := strings.ToLower(pattern)
			if strings.HasPrefix(lower, "gpt-") && !strings.HasPrefix(lower, "gpt-image") {
				return pattern
			}
		}
	}
	return smartRouterCalibrationTextModel
}

func smartRouterModelPatternMatches(pattern, model string) bool {
	pattern = strings.TrimSpace(pattern)
	model = strings.TrimSpace(model)
	if pattern == "*" || pattern == model {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(model, strings.TrimSuffix(pattern, "*"))
	}
	return false
}

func (s *SmartRouterCalibrationService) compactCalibrationModel() string {
	if s != nil && s.cfg != nil {
		if model := strings.TrimSpace(s.cfg.Gateway.OpenAICompactModel); model != "" {
			return model
		}
	}
	return "gpt-5.4"
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

// RunSmartRouterResponsesCalibrationProbe performs a minimal, non-streaming
// Responses request directly against one account. It never re-enters account
// selection and does not expose the probe to a downstream user.
func (s *OpenAIGatewayService) RunSmartRouterResponsesCalibrationProbe(ctx context.Context, account *Account, model string) (int, int64, error) {
	if s == nil || account == nil {
		return 0, 0, errors.New("smart router responses calibration account is required")
	}
	body, err := json.Marshal(map[string]any{
		"model":             strings.TrimSpace(model),
		"input":             "Reply with OK.",
		"max_output_tokens": 1,
		"stream":            false,
	})
	if err != nil {
		return 0, 0, err
	}
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)).WithContext(ctx)
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	startedAt := time.Now()
	forwardResult, forwardErr := s.Forward(ctx, ginCtx, account, body)
	latencyMs := time.Since(startedAt).Milliseconds()
	statusCode := smartRouterProbeStatusCode(forwardErr)
	if forwardErr == nil && (forwardResult == nil || recorder.Code >= http.StatusBadRequest) {
		forwardErr = errors.New("responses calibration returned no valid response")
		if statusCode == 0 {
			statusCode = http.StatusBadGateway
		}
	}
	if forwardErr == nil {
		statusCode = http.StatusOK
	}
	s.ReportSmartRouterTextCalibrationResult(account, smartrouter.CapabilityResponses, model, forwardResult, forwardErr, latencyMs)
	return statusCode, latencyMs, forwardErr
}

// RunSmartRouterChatCalibrationProbe performs a minimal Chat Completions
// request directly against one account.
func (s *OpenAIGatewayService) RunSmartRouterChatCalibrationProbe(ctx context.Context, account *Account, model string) (int, int64, error) {
	if s == nil || account == nil {
		return 0, 0, errors.New("smart router chat calibration account is required")
	}
	body, err := json.Marshal(map[string]any{
		"model":      strings.TrimSpace(model),
		"messages":   []map[string]string{{"role": "user", "content": "Reply with OK."}},
		"max_tokens": 1,
		"stream":     false,
	})
	if err != nil {
		return 0, 0, err
	}
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)).WithContext(ctx)
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	startedAt := time.Now()
	forwardResult, forwardErr := s.ForwardAsChatCompletions(ctx, ginCtx, account, body, "", model)
	latencyMs := time.Since(startedAt).Milliseconds()
	statusCode := smartRouterProbeStatusCode(forwardErr)
	if forwardErr == nil && (forwardResult == nil || recorder.Code >= http.StatusBadRequest) {
		forwardErr = errors.New("chat calibration returned no valid response")
		if statusCode == 0 {
			statusCode = http.StatusBadGateway
		}
	}
	if forwardErr == nil {
		statusCode = http.StatusOK
	}
	s.ReportSmartRouterTextCalibrationResult(account, smartrouter.CapabilityChat, model, forwardResult, forwardErr, latencyMs)
	return statusCode, latencyMs, forwardErr
}

// RunSmartRouterCompactCalibrationProbe directly tests one account's compact
// endpoint. It validates that the response contains a compaction output item,
// not merely a successful HTTP status or usage object.
func (s *OpenAIGatewayService) RunSmartRouterCompactCalibrationProbe(ctx context.Context, account *Account, model string) (int, int64, error) {
	if s == nil || account == nil {
		return 0, 0, errors.New("smart router compact calibration account is required")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "gpt-5.4"
	}
	body, err := json.Marshal(createOpenAICompactProbePayload(model))
	if err != nil {
		return 0, 0, err
	}
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	ginCtx.Request = req

	startedAt := time.Now()
	forwardResult, forwardErr := s.Forward(ctx, ginCtx, account, body)
	latencyMs := time.Since(startedAt).Milliseconds()
	statusCode := smartRouterProbeStatusCode(forwardErr)
	if forwardErr == nil && !openAICompactResponseContainsItem(recorder.Body.Bytes()) {
		forwardErr = errors.New("compact calibration returned no compaction output item")
		statusCode = http.StatusBadGateway
	}
	if forwardErr == nil {
		statusCode = http.StatusOK
	}
	s.ReportSmartRouterCompactCalibrationResult(account, model, forwardResult, forwardErr, latencyMs)
	return statusCode, latencyMs, forwardErr
}

func openAICompactResponseContainsItem(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	if gjson.ValidBytes(body) {
		if strings.TrimSpace(gjson.GetBytes(body, "compaction.encrypted_content").String()) != "" {
			return true
		}
		for _, item := range gjson.GetBytes(body, "output").Array() {
			if !isResponsesCompactionItemType(item.Get("type").String()) {
				continue
			}
			if strings.TrimSpace(item.Get("encrypted_content").String()) != "" {
				return true
			}
		}
		return false
	}
	item, found := findRawCompactionItemFromSSE(string(body))
	return found && strings.TrimSpace(gjson.GetBytes(item, "encrypted_content").String()) != ""
}

func buildSmartRouterCompactProbeExtraUpdates(result SmartRouterCalibrationResult, now time.Time) map[string]any {
	updates := map[string]any{
		"openai_compact_checked_at":  now.Format(time.RFC3339),
		"openai_compact_last_status": nil,
	}
	if result.StatusCode > 0 {
		updates["openai_compact_last_status"] = result.StatusCode
	}
	if result.Success {
		updates["openai_compact_supported"] = true
		updates["openai_compact_last_error"] = ""
		return updates
	}
	errorSummary := truncateString(sanitizeUpstreamErrorMessage(result.ErrorSummary), 2048)
	updates["openai_compact_last_error"] = errorSummary
	if result.StatusCode == http.StatusNotFound || result.StatusCode == http.StatusMethodNotAllowed || result.StatusCode == http.StatusNotImplemented || strings.Contains(strings.ToLower(errorSummary), "compact") && strings.Contains(strings.ToLower(errorSummary), "unsupported") {
		updates["openai_compact_supported"] = false
	}
	return updates
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
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": []string{`form-data; name="image"; filename="smart-router-probe.png"`},
		"Content-Type":        []string{"image/png"},
	})
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
			LaneID:                     row.LaneID,
			ChatKnown:                  row.ChatKnown,
			ChatLastSuccess:            row.ChatLastSuccess,
			ChatLastFailure:            row.ChatLastFailure,
			ChatRecoveryPriority:       row.ChatRecoveryPriority,
			ResponsesKnown:             row.ResponsesKnown,
			ResponsesLastSuccess:       row.ResponsesLastSuccess,
			ResponsesLastFailure:       row.ResponsesLastFailure,
			ResponsesRecoveryPriority:  row.ResponsesRecoveryPriority,
			GenerationKnown:            row.GenerationKnown,
			EditKnown:                  row.EditKnown,
			GenerationLastSuccess:      row.GenerationLastSuccess,
			GenerationLastFailure:      row.GenerationLastFailure,
			GenerationRecoveryPriority: row.GenerationRecoveryPriority,
			EditLastSuccess:            row.EditLastSuccess,
			EditLastFailure:            row.EditLastFailure,
			EditRecoveryPriority:       row.EditRecoveryPriority,
			CompactKnown:               row.CompactKnown,
			CompactLastSuccess:         row.CompactLastSuccess,
			CompactLastFailure:         row.CompactLastFailure,
			CompactRecoveryPriority:    row.CompactRecoveryPriority,
			ModesDiverged:              modesDiverged,
		})
	}
	return evidence
}
