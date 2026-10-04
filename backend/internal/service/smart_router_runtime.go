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
	if err := s.smartRouterHealthLedger.RecordEvent(ctx, event); err != nil {
		logger.LegacyPrintf("service.openai_gateway", "Smart Router ledger record failed: %v", err)
	}
}

func (s *OpenAIGatewayService) ReportSmartRouterTextResult(account *Account, capability core.Capability, requestedModel string, result *OpenAIForwardResult, err error, durationMs int64) {
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

func (s *OpenAIGatewayService) ReportSmartRouterImageResult(account *Account, parsed *OpenAIImagesRequest, result *OpenAIForwardResult, err error, durationMs int64) {
	s.reportSmartRouterImageResult("production", account, parsed, result, err, durationMs)
}

func (s *OpenAIGatewayService) ReportSmartRouterImageCalibrationResult(account *Account, parsed *OpenAIImagesRequest, result *OpenAIForwardResult, err error, durationMs int64) {
	s.reportSmartRouterImageResult("calibration", account, parsed, result, err, durationMs)
}

func (s *OpenAIGatewayService) reportSmartRouterImageResult(source string, account *Account, parsed *OpenAIImagesRequest, result *OpenAIForwardResult, err error, durationMs int64) {
	if s == nil || !s.isSmartRouterEnabled() || account == nil || parsed == nil {
		return
	}
	capability := core.CapabilityImageGeneration
	if parsed.IsEdits() {
		capability = core.CapabilityImageEdit
	}
	lane, ok := smartRouterLaneSnapshot(account, nil, false, 0, 0, false, capability)
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
		if failoverErr != nil && failoverErr.Reason == GatewayFailureReason("openai_image_attempt_timeout") {
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
			BasePriority: lane.Priority, Capability: capability, Model: parsed.Model,
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
