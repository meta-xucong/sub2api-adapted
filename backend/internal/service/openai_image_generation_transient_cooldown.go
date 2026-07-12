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

const defaultOpenAIImageGenerationTransientCooldown = 30 * time.Second

const openAIImageGenerationTransientReasonPrefix = "openai image generation transient cooldown"

// TempUnscheduleImageGenerationTransientError keeps a generation lane out of
// scheduling briefly after an upstream flap. It deliberately leaves the
// account active so the lane returns automatically when the cooldown expires.
func (s *OpenAIGatewayService) TempUnscheduleImageGenerationTransientError(ctx context.Context, account *Account, failoverErr *UpstreamFailoverError) {
	if s == nil || account == nil || failoverErr == nil {
		return
	}
	if !isOpenAIImageGenerationTransientError(failoverErr) {
		return
	}
	cooldown := s.openAIImageTransientCooldownForAccount(account, s.openAIImageGenerationTransientCooldown())
	if cooldown <= 0 {
		return
	}

	until := time.Now().Add(cooldown)
	reason := openAIImageGenerationTransientCooldownReason(failoverErr)
	s.BlockAccountScheduling(account, until, "image_generation_transient")

	if s.accountRepo == nil {
		logger.L().With(zap.String("component", "service.openai_gateway")).Warn(
			"openai.image_generation_transient_cooldown_memory_only",
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
			"openai.image_generation_transient_cooldown_failed",
			zap.Int64("account_id", account.ID),
			zap.Int("status_code", failoverErr.StatusCode),
			zap.Error(err),
		)
		return
	}

	logger.L().With(zap.String("component", "service.openai_gateway")).Warn(
		"openai.image_generation_transient_cooldown",
		zap.Int64("account_id", account.ID),
		zap.String("account_name", account.Name),
		zap.Int("status_code", failoverErr.StatusCode),
		zap.Duration("cooldown", cooldown),
		zap.Time("until", until),
		zap.String("reason", reason),
	)
}

func (s *OpenAIGatewayService) openAIImageGenerationTransientCooldown() time.Duration {
	if s == nil || s.cfg == nil {
		return defaultOpenAIImageGenerationTransientCooldown
	}
	seconds := s.cfg.Gateway.ImageGenerationTransientCooldownSeconds
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func isOpenAIImageGenerationTransientError(failoverErr *UpstreamFailoverError) bool {
	if failoverErr == nil {
		return false
	}
	if isOpenAIImageTransientStatus(failoverErr.StatusCode) {
		return true
	}
	return failoverErr.StatusCode == http.StatusBadRequest && isOpenAIImageUpstreamTextReply(failoverErr.ResponseBody)
}

func openAIImageGenerationTransientCooldownReason(failoverErr *UpstreamFailoverError) string {
	statusCode := 0
	if failoverErr != nil {
		statusCode = failoverErr.StatusCode
	}
	reason := fmt.Sprintf("%s: status=%d", openAIImageGenerationTransientReasonPrefix, statusCode)
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
