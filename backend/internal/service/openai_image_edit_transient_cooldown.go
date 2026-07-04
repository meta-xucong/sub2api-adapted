package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const defaultOpenAIImageEditTransientCooldown = 12 * time.Second

func (s *OpenAIGatewayService) TempUnscheduleImageEditTransientError(ctx context.Context, account *Account, failoverErr *UpstreamFailoverError) {
	if s == nil || account == nil || failoverErr == nil {
		return
	}
	if !isOpenAIImageEditTransientStatus(failoverErr.StatusCode) {
		return
	}
	cooldown := s.openAIImageEditTransientCooldown()
	if cooldown <= 0 {
		return
	}

	until := time.Now().Add(cooldown)
	reason := openAIImageEditTransientCooldownReason(failoverErr)
	s.BlockAccountScheduling(account, until, "image_edit_transient")

	if s.accountRepo == nil {
		logger.L().With(zap.String("component", "service.openai_gateway")).Warn(
			"openai.image_edit_transient_cooldown_memory_only",
			zap.Int64("account_id", account.ID),
			zap.String("account_name", account.Name),
			zap.Int("status_code", failoverErr.StatusCode),
			zap.Duration("cooldown", cooldown),
			zap.Time("until", until),
			zap.String("reason", reason),
		)
		return
	}

	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	if err := s.accountRepo.SetTempUnschedulable(stateCtx, account.ID, until, reason); err != nil {
		logger.L().With(zap.String("component", "service.openai_gateway")).Warn(
			"openai.image_edit_transient_cooldown_failed",
			zap.Int64("account_id", account.ID),
			zap.Int("status_code", failoverErr.StatusCode),
			zap.Error(err),
		)
		return
	}

	logger.L().With(zap.String("component", "service.openai_gateway")).Warn(
		"openai.image_edit_transient_cooldown",
		zap.Int64("account_id", account.ID),
		zap.String("account_name", account.Name),
		zap.Int("status_code", failoverErr.StatusCode),
		zap.Duration("cooldown", cooldown),
		zap.Time("until", until),
		zap.String("reason", reason),
	)
}

func (s *OpenAIGatewayService) openAIImageEditTransientCooldown() time.Duration {
	if s == nil || s.cfg == nil {
		return defaultOpenAIImageEditTransientCooldown
	}
	seconds := s.cfg.Gateway.ImageEditTransientCooldownSeconds
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func isOpenAIImageEditTransientStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusForbidden,
		http.StatusRequestTimeout,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func openAIImageEditTransientCooldownReason(failoverErr *UpstreamFailoverError) string {
	statusCode := 0
	if failoverErr != nil {
		statusCode = failoverErr.StatusCode
	}
	reason := fmt.Sprintf("openai image edit transient cooldown: status=%d", statusCode)
	if failoverErr == nil || len(failoverErr.ResponseBody) == 0 {
		return reason
	}
	message := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(failoverErr.ResponseBody)))
	if message == "" {
		message = sanitizeUpstreamErrorMessage(strings.TrimSpace(string(failoverErr.ResponseBody)))
	}
	if message == "" {
		return reason
	}
	return reason + ": " + truncateString(message, 512)
}
