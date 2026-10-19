package service

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
)

// SetSmartRouterHealthLedger attaches durable state before request handling
// begins. A nil ledger intentionally leaves the router memory-only.
func (s *OpenAIGatewayService) SetSmartRouterHealthLedger(ledger SmartRouterHealthLedger) {
	if s == nil {
		return
	}
	s.smartRouterHealthLedger = ledger
}

func (s *OpenAIGatewayService) persistSmartRouterHealthEvent(event core.HealthEvent) {
	if s == nil || s.smartRouterHealthLedger == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	release, err := event.AcquirePersistence(ctx)
	if err != nil {
		logger.LegacyPrintf("service.openai_gateway", "Smart Router ledger record failed: %v", err)
		return
	}
	defer release()
	if err := s.smartRouterHealthLedger.RecordEvent(ctx, event); err != nil {
		logger.LegacyPrintf("service.openai_gateway", "Smart Router ledger record failed: %v", err)
	}
}

func (s *OpenAIGatewayService) ReportSmartRouterTextResult(ctx context.Context, account *Account, capability core.Capability, requestedModel string, result *OpenAIForwardResult, err error, durationMs int64) {
	// A downstream cancellation is not evidence about provider health. Check
	// the request too: header-wait failures can return no forwarding result.
	if (ctx != nil && ctx.Err() != nil) || IsOpenAIStreamClientDisconnect(err) {
		return
	}
	s.reportSmartRouterTextResult("production", account, capability, requestedModel, result, err, durationMs)
}

func (s *OpenAIGatewayService) ReportSmartRouterTextCalibrationResult(account *Account, capability core.Capability, requestedModel string, result *OpenAIForwardResult, err error, durationMs int64) {
	s.reportSmartRouterTextResult("calibration", account, capability, requestedModel, result, err, durationMs)
}

func (s *OpenAIGatewayService) ReportSmartRouterEmbeddingResult(account *Account, requestedModel string, result *OpenAIForwardResult, err error, durationMs int64) {
	s.reportSmartRouterTextResult("production", account, core.CapabilityEmbedding, requestedModel, result, err, durationMs)
}

func (s *OpenAIGatewayService) ReportSmartRouterEmbeddingCalibrationResult(account *Account, requestedModel string, result *OpenAIForwardResult, err error, durationMs int64) {
	s.reportSmartRouterTextResult("calibration", account, core.CapabilityEmbedding, requestedModel, result, err, durationMs)
}

func (s *OpenAIGatewayService) reportSmartRouterTextResult(source string, account *Account, capability core.Capability, requestedModel string, result *OpenAIForwardResult, err error, durationMs int64) {
	if s == nil || !s.isSmartRouterEnabled() || account == nil || (capability != core.CapabilityChat && capability != core.CapabilityResponses && capability != core.CapabilityEmbedding) {
		return
	}
	if result != nil && result.ClientDisconnect {
		return
	}

	lane, ok := smartRouterLaneSnapshot(account, nil, false, 0, 0, false, capability)
	if !ok {
		return
	}
	statusCode, message, code := smartRouterFailureDetails(err)
	clientCancelled := errors.Is(err, context.Canceled) || strings.Contains(strings.ToLower(message), "context canceled")
	success := err == nil
	if success {
		statusCode = http.StatusOK
	}
	errorClass := core.FailureClass("")
	if !success {
		errorClass = core.ClassifyFailureDetails(statusCode, capability, message, code, clientCancelled)
	}
	errorSummary := ""
	if !success {
		errorSummary = smartRouterSafeErrorSummary(statusCode, errorClass)
	}
	if tracker := s.smartRouterHealth(); tracker != nil {
		tracker.Observe(core.RouteResult{
			Source: source, LaneID: lane.LaneID, AccountID: lane.AccountID, SourceGroup: lane.SourceGroup,
			BasePriority: lane.Priority, Capability: capability, Model: requestedModel,
			Success: success, StatusCode: statusCode, ErrorClass: errorClass,
			TotalLatencyMs: durationMs, ErrorSummary: errorSummary,
		})
	}
}

func (s *OpenAIGatewayService) ReportSmartRouterCompactResult(account *Account, requestedModel string, result *OpenAIForwardResult, err error, durationMs int64) {
	s.reportSmartRouterCompactResult("production", account, requestedModel, result, err, durationMs)
}

func (s *OpenAIGatewayService) ReportSmartRouterCompactCalibrationResult(account *Account, requestedModel string, result *OpenAIForwardResult, err error, durationMs int64) {
	s.reportSmartRouterCompactResult("calibration", account, requestedModel, result, err, durationMs)
}

func (s *OpenAIGatewayService) reportSmartRouterCompactResult(source string, account *Account, requestedModel string, result *OpenAIForwardResult, err error, durationMs int64) {
	if s == nil || !s.isSmartRouterEnabled() || account == nil {
		return
	}
	lane, ok := smartRouterLaneSnapshot(account, nil, false, 0, 0, false, core.CapabilityResponsesCompact)
	if !ok {
		return
	}
	statusCode, message, _ := smartRouterFailureDetails(err)
	code := ""
	clientCancelled := errors.Is(err, context.Canceled) || strings.Contains(strings.ToLower(message), "context canceled")
	success := err == nil
	if success {
		statusCode = http.StatusOK
	}
	errorClass := core.FailureClass("")
	if !success {
		errorClass = core.ClassifyFailureDetails(statusCode, core.CapabilityResponsesCompact, message, code, clientCancelled)
	}
	errorSummary := ""
	if !success {
		errorSummary = smartRouterSafeErrorSummary(statusCode, errorClass)
	}
	if tracker := s.smartRouterHealth(); tracker != nil {
		tracker.Observe(core.RouteResult{
			Source: source, LaneID: lane.LaneID, AccountID: lane.AccountID, SourceGroup: lane.SourceGroup,
			BasePriority: lane.Priority, Capability: core.CapabilityResponsesCompact, Model: requestedModel,
			Success: success, StatusCode: statusCode, ErrorClass: errorClass,
			TotalLatencyMs: durationMs, ErrorSummary: errorSummary,
		})
	}
}

func (s *OpenAIGatewayService) ReportSmartRouterImageResult(requestCtx context.Context, account *Account, parsed *OpenAIImagesRequest, result *OpenAIForwardResult, err error, durationMs int64) {
	// The request-wide budget may expire before this account's own attempt
	// timeout. Neither that expiry nor caller cancellation measures lane health.
	if OpenAIImageRequestContextErr(requestCtx) != nil {
		return
	}
	s.reportSmartRouterImageResult(requestCtx, "production", account, parsed, result, err, durationMs)
}

func (s *OpenAIGatewayService) ReportSmartRouterImageCalibrationResult(account *Account, parsed *OpenAIImagesRequest, result *OpenAIForwardResult, err error, durationMs int64) {
	s.reportSmartRouterImageResult(context.Background(), "calibration", account, parsed, result, err, durationMs)
}

func (s *OpenAIGatewayService) reportSmartRouterImageResult(requestCtx context.Context, source string, account *Account, parsed *OpenAIImagesRequest, result *OpenAIForwardResult, err error, durationMs int64) {
	if s == nil || !s.isSmartRouterEnabled() || account == nil || parsed == nil {
		return
	}
	scope := smartRouterImageRequestScope(requestCtx, parsed)
	capability := scope.Capability
	lane, ok := s.smartRouterImageLane(account, capability, scope.Model)
	if !ok {
		return
	}
	statusCode, message, code := 0, "", ""
	var imageErr *OpenAIImagesUpstreamError
	if errors.As(err, &imageErr) && imageErr != nil {
		statusCode, message, code = imageErr.StatusCode, imageErr.Message, imageErr.Code
	}
	var failoverErr *UpstreamFailoverError
	if errors.As(err, &failoverErr) && failoverErr != nil {
		statusCode, message = failoverErr.StatusCode, string(failoverErr.ResponseBody)
	}
	clientCancelled := errors.Is(err, context.Canceled) || strings.Contains(strings.ToLower(message), "context canceled")
	success := err == nil || (result != nil && result.ImageCount > 0)
	if success {
		statusCode = http.StatusOK
	}
	errorClass := core.FailureClass("")
	if !success {
		if failoverErr != nil && (failoverErr.Reason == GatewayFailureReason("openai_image_attempt_timeout") || isOpenAIImagesPollTimeout(failoverErr.StatusCode, failoverErr.ResponseBody)) {
			errorClass = core.FailureTimeout
		} else if errors.Is(err, context.DeadlineExceeded) || smartRouterIsNetTimeout(err) {
			errorClass = core.FailureTimeout
		} else {
			errorClass = core.ClassifyFailureDetails(statusCode, capability, message, code, clientCancelled)
		}
	}
	errorSummary := ""
	if !success {
		errorSummary = smartRouterSafeErrorSummary(statusCode, errorClass)
	}
	if tracker := s.smartRouterHealth(); tracker != nil {
		tracker.Observe(core.RouteResult{
			Source: source, LaneID: lane.LaneID, AccountID: lane.AccountID, SourceGroup: lane.SourceGroup,
			BasePriority: lane.Priority, Capability: capability, Model: scope.Model,
			ImageProbeToken: scope.ProbeToken, ImageHealthGeneration: scope.Generation,
			Success: success, StatusCode: statusCode, ErrorClass: errorClass,
			TotalLatencyMs: durationMs, ErrorSummary: errorSummary,
		})
	}
}

// smartRouterSafeErrorSummary returns only an internal failure class or a
// fixed HTTP status label. Upstream error bodies may contain echoed prompts,
// credentials, or other user-controlled data and must not enter durable health
// or calibration telemetry.
func smartRouterSafeErrorSummary(statusCode int, failureClass core.FailureClass) string {
	if failureClass != "" {
		return string(failureClass)
	}
	if statusCode > 0 {
		return "upstream_http_" + strconv.Itoa(statusCode)
	}
	return "upstream_failure"
}

func smartRouterFailureDetails(err error) (statusCode int, message string, code string) {
	var failoverErr *UpstreamFailoverError
	if errors.As(err, &failoverErr) && failoverErr != nil {
		statusCode = failoverErr.StatusCode
		message = string(failoverErr.ResponseBody)
		code = string(failoverErr.Reason)
	}
	if err != nil && message == "" {
		message = err.Error()
	}
	return statusCode, message, code
}

func smartRouterIsNetTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr != nil && netErr.Timeout()
}

// smartRouterImageScope is shared by selection, post-queue admission and result
// reporting. Billing and the original request model are deliberately untouched.
type smartRouterImageScope struct {
	Model      string
	Capability core.Capability
	ProbeToken uint64
	Generation uint64
	LastResort bool
}
type smartRouterImageScopeKey struct{}
type smartRouterImageSelectionKey struct{}
type smartRouterImageSelectionState struct {
	RetryAtUnix int64
	ProbeBudget *openAISelectionProbeBudget
}

func WithSmartRouterImageRequest(ctx context.Context, model string, isEdit bool) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	capability := core.CapabilityImageGeneration
	if isEdit {
		capability = core.CapabilityImageEdit
	}
	return context.WithValue(ctx, smartRouterImageScopeKey{}, smartRouterImageScope{Model: strings.TrimSpace(model), Capability: capability})
}

func smartRouterImageRequestScope(ctx context.Context, parsed *OpenAIImagesRequest) smartRouterImageScope {
	if ctx != nil {
		if scope, ok := ctx.Value(smartRouterImageScopeKey{}).(smartRouterImageScope); ok {
			return scope
		}
	}
	scope := smartRouterImageScope{Capability: core.CapabilityImageGeneration}
	if parsed != nil {
		scope.Model = parsed.Model
		if parsed.IsEdits() {
			scope.Capability = core.CapabilityImageEdit
		}
	}
	return scope
}

func (s *OpenAIGatewayService) smartRouterImageLane(account *Account, capability core.Capability, model string) (core.LaneSnapshot, bool) {
	if s == nil || !s.isSmartRouterEnabled() || account == nil || !account.IsOpenAI() ||
		(capability != core.CapabilityImageGeneration && capability != core.CapabilityImageEdit) ||
		!smartRouterAccountEligibleForSmartRouter(account, capability, model, "") {
		return core.LaneSnapshot{}, false
	}
	// An explicit empty capability list is an opt-out, even when no text
	// capability is present to make the normalized lane map non-nil.
	extra := parseSmartRouterAccountExtra(account)
	if extra.CapabilitiesSet && !extra.Capabilities[capability] {
		return core.LaneSnapshot{}, false
	}
	lane, ok := smartRouterLaneSnapshot(account, nil, false, 0, 0, false, capability)
	if !ok || (lane.Capabilities != nil && !lane.Capabilities[capability]) {
		return core.LaneSnapshot{}, false
	}
	return lane, true
}

func (s *OpenAIGatewayService) smartRouterOwnsImageHealth(account *Account, capability core.Capability) bool {
	_, managed := s.smartRouterImageLane(account, capability, "")
	return managed
}

func (s *OpenAIGatewayService) smartRouterOwnsImageHealthForContext(ctx context.Context, account *Account) bool {
	scope := smartRouterImageRequestScope(ctx, nil)
	_, managed := s.smartRouterImageLane(account, scope.Capability, scope.Model)
	return managed
}

// SmartRouterImageUnavailableError is retryable capacity, not a bad user request.
// RetryAtUnix comes from the same health state that rejected the lane.
type SmartRouterImageUnavailableError struct{ RetryAtUnix int64 }

func (e *SmartRouterImageUnavailableError) Error() string {
	return "image lanes are recovering; retry later"
}
func (e *SmartRouterImageUnavailableError) Unwrap() error { return ErrNoAvailableAccounts }
func (e *SmartRouterImageUnavailableError) RetryAfterSeconds() int {
	seconds := e.RetryAtUnix - time.Now().Unix()
	if seconds < 1 {
		return 1
	}
	return int(seconds)
}

func (s *OpenAIGatewayService) smartRouterImageAccountAvailable(ctx context.Context, account *Account, capability core.Capability, model string) bool {
	if ctx == nil {
		return true
	}
	if _, imageRequest := ctx.Value(smartRouterImageScopeKey{}).(smartRouterImageScope); !imageRequest {
		return true
	}
	lane, managed := s.smartRouterImageLane(account, capability, model)
	if !managed {
		return true
	}
	health := s.smartRouterHealth()
	now := time.Now().Unix()
	key := core.NewHealthKey(lane.LaneID, capability, model)
	decision := health.ImageAdmission(key, now)
	if smartRouterImageRequestScope(ctx, nil).LastResort {
		// Ordinary lanes (including their bounded wait plans) were exhausted in
		// the preceding pass. Do not let stale load snapshots select them again
		// and hide cooling fallback. Admission never clears their health state.
		snapshot := health.Snapshot(lane, capability, model, now)
		if snapshot.RecoveryStage != core.RecoveryCooling || snapshot.CooldownUntilUnix <= now {
			return false
		}
		decision = health.ImageLastResortAdmission(key, now)
	}
	if !decision.Allowed && ctx != nil {
		if state, ok := ctx.Value(smartRouterImageSelectionKey{}).(*smartRouterImageSelectionState); ok && state != nil {
			if state.RetryAtUnix == 0 || decision.RetryAtUnix < state.RetryAtUnix {
				state.RetryAtUnix = decision.RetryAtUnix
			}
		}
	}
	return decision.Allowed
}

// AcquireSmartRouterImageAttempt rechecks after account-slot waiting. Only
// recovering lanes take a process-local lease; no extra request is generated.
func (s *OpenAIGatewayService) AcquireSmartRouterImageAttempt(ctx context.Context, account *Account, parsed *OpenAIImagesRequest, lastResort ...bool) (context.Context, func(), error) {
	if err := OpenAIImageRequestContextErr(ctx); err != nil {
		return ctx, func() {}, err
	}
	scope := smartRouterImageRequestScope(ctx, parsed)
	lane, managed := s.smartRouterImageLane(account, scope.Capability, scope.Model)
	if !managed {
		return ctx, func() {}, nil
	}
	now := time.Now()
	// Even when operators disable transport timeouts, half-open admission is
	// bounded by the existing default image request budget.
	leaseUntil := now.Add(defaultOpenAIImageRequestTimeout)
	if timeout := s.openAIImageUpstreamTimeout(); timeout > 0 {
		leaseUntil = now.Add(timeout)
	}
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(leaseUntil) {
		leaseUntil = deadline
	}
	key := core.NewHealthKey(lane.LaneID, scope.Capability, scope.Model)
	var decision core.ImageAdmissionDecision
	var release func()
	if len(lastResort) > 0 && lastResort[0] {
		decision, release = s.smartRouterHealth().AcquireImageLastResortAdmission(key, now.Unix(), leaseUntil.Unix())
	} else {
		decision, release = s.smartRouterHealth().AcquireImageAdmission(key, now.Unix(), leaseUntil.Unix())
	}
	if !decision.Allowed {
		return ctx, release, &SmartRouterImageUnavailableError{RetryAtUnix: decision.RetryAtUnix}
	}
	scope.ProbeToken, scope.Generation = decision.ProbeToken, decision.ImageHealthGeneration
	return context.WithValue(ctx, smartRouterImageScopeKey{}, scope), release, nil
}

// ReportOpenAIImageScheduleResult retains legacy behavior only for lanes that
// are outside Smart Router. Image observations must not train account-wide
// EWMA/circuit breakers shared with text and video.
func (s *OpenAIGatewayService) ReportOpenAIImageScheduleResult(ctx context.Context, account *Account, model string, success bool, firstTokenMs *int, observedErr ...error) bool {
	if s.smartRouterOwnsImageHealthForContext(ctx, account) {
		if success {
			// Keep native retry-window/model bookkeeping without changing the
			// shared account EWMA or account-wide health breaker.
			s.openaiOAuth429RetryStartedAt.Delete(account.ID)
			s.clearOpenAIAccountModelTransientState(account.ID, normalizeOpenAIAccountModelTransientModel(model))
		}
		return false
	}
	if len(observedErr) > 0 {
		var failure *UpstreamFailoverError
		if errors.As(observedErr[0], &failure) && failure != nil && isOpenAIImagesPollTimeout(failure.StatusCode, failure.ResponseBody) {
			return false
		}
	}
	return s.ReportOpenAIAccountScheduleResult(account, model, success, firstTokenMs, observedErr...)
}

func smartRouterOwnsImageHTTPFailure(statusCode int, body []byte) bool {
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusTooManyRequests || isOpenAIHTTPUpstreamAccessStateError(statusCode, "", body) {
		return false
	}
	if isOpenAIImagesPollTimeout(statusCode, body) {
		return true
	}
	failure := core.ClassifyFailureDetails(statusCode, core.CapabilityImageGeneration, string(body), "", false)
	switch failure {
	case core.FailureTimeout, core.FailureUpstream5xx, core.FailureTransientForbidden, core.FailureCapabilityError:
		return true
	default:
		return false
	}
}
