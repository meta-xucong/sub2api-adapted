package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const maxOpenAIImageTransientBackoff = 10 * time.Minute
const defaultOpenAIImageGenerationTransientCooldown = 30 * time.Second
const defaultOpenAIImageEditTransientCooldown = 12 * time.Second

const (
	openAIImageGenerationTransientReasonPrefix = "openai image generation transient cooldown"
	openAIImageEditTransientReasonPrefix       = "openai image edit transient cooldown"
	openAIImageEditCapacityRetryPadding        = 750 * time.Millisecond
	openAIImageEditCapacityRetryMax            = 30 * time.Second
)

// openAIImageTransientCooldownForAccount applies the legacy error-rate tiers
// without changing permanent account status or priority.
func (s *OpenAIGatewayService) openAIImageTransientCooldownForAccount(account *Account, base time.Duration) time.Duration {
	if base <= 0 || s == nil || account == nil || s.openaiAccountStats == nil {
		return base
	}
	errorRate, _, _ := s.openaiAccountStats.snapshot(account.ID)
	multiplier := 1
	switch {
	case errorRate >= 0.75:
		multiplier = 8
	case errorRate >= 0.50:
		multiplier = 4
	case errorRate >= 0.25:
		multiplier = 2
	}

	cooldown := base * time.Duration(multiplier)
	if cooldown > maxOpenAIImageTransientBackoff {
		return maxOpenAIImageTransientBackoff
	}
	return cooldown
}

func (s *OpenAIGatewayService) TempUnscheduleImageGenerationTransientError(ctx context.Context, account *Account, failoverErr *UpstreamFailoverError) {
	if s == nil || account == nil || failoverErr == nil || !isOpenAIImageGenerationTransientError(failoverErr) {
		return
	}
	cooldown := s.openAIImageTransientCooldownForAccount(account, s.openAIImageGenerationTransientCooldown())
	if cooldown <= 0 {
		return
	}
	s.tempUnscheduleOpenAIImageTransient(ctx, account, time.Now().Add(cooldown), openAIImageGenerationTransientCooldownReason(failoverErr), "image_generation_transient", "openai.image_generation_transient_cooldown")
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
	return isOpenAIImageUpstreamTextReplyFailover(failoverErr.StatusCode, failoverErr.ResponseBody)
}

func isOpenAIImageUpstreamTextReply(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	for _, path := range []string{"error.code", "response.error.code", "code"} {
		if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, path).String()), "upstream_text_reply") {
			return true
		}
	}
	// Structured JSON is authoritative; arbitrary echoed request fields do not
	// make an image lane mismatch.
	if gjson.ValidBytes(body) {
		return false
	}
	return strings.Contains(strings.ToLower(string(body)), "upstream_text_reply")
}

func isOpenAIImageUpstreamTextReplyFailover(statusCode int, body []byte) bool {
	return statusCode == http.StatusBadRequest && isOpenAIImageUpstreamTextReply(body)
}

func openAIImageGenerationTransientCooldownReason(failoverErr *UpstreamFailoverError) string {
	return openAIImageTransientCooldownReason(openAIImageGenerationTransientReasonPrefix, failoverErr)
}

func (s *OpenAIGatewayService) TempUnscheduleImageEditTransientError(ctx context.Context, account *Account, failoverErr *UpstreamFailoverError) {
	if s == nil || account == nil || failoverErr == nil || !isOpenAIImageTransientStatus(failoverErr.StatusCode) {
		return
	}
	cooldown := s.openAIImageTransientCooldownForAccount(account, s.openAIImageEditTransientCooldown())
	if cooldown <= 0 {
		return
	}
	s.tempUnscheduleOpenAIImageTransient(ctx, account, time.Now().Add(cooldown), openAIImageEditTransientCooldownReason(failoverErr), "image_edit_transient", "openai.image_edit_transient_cooldown")
}

func (s *OpenAIGatewayService) tempUnscheduleOpenAIImageTransient(ctx context.Context, account *Account, until time.Time, reason, blockReason, logEvent string) {
	s.BlockAccountScheduling(account, until, blockReason)
	if s.accountRepo == nil {
		logger.L().With(zap.String("component", "service.openai_gateway")).Warn(
			logEvent+"_memory_only", zap.Int64("account_id", account.ID), zap.String("account_name", account.Name), zap.Time("until", until), zap.String("reason", reason),
		)
		return
	}
	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	if err := s.accountRepo.SetTempUnschedulable(stateCtx, account.ID, until, reason); err != nil {
		logger.L().With(zap.String("component", "service.openai_gateway")).Warn(logEvent+"_failed", zap.Int64("account_id", account.ID), zap.Error(err))
		return
	}
	logger.L().With(zap.String("component", "service.openai_gateway")).Warn(logEvent,
		zap.Int64("account_id", account.ID), zap.String("account_name", account.Name), zap.Duration("cooldown", time.Until(until)), zap.Time("until", until), zap.String("reason", reason),
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

func (s *OpenAIGatewayService) OpenAIImageEditTransientRetryAfterSeconds() int {
	cooldown := s.openAIImageEditTransientCooldown()
	if cooldown <= 0 {
		return 0
	}
	if cooldown > openAIImageEditCapacityRetryMax {
		cooldown = openAIImageEditCapacityRetryMax
	}
	return durationCeilSeconds(cooldown + openAIImageEditCapacityRetryPadding)
}

func (s *OpenAIGatewayService) OpenAIImageEditCapacityRetryDelay(ctx context.Context, groupID *int64, requestedModel string) time.Duration {
	if s.openAIImageEditTransientCooldown() <= 0 {
		return 0
	}
	accounts, err := s.listOpenAIImageEditCapacityAccounts(ctx, groupID)
	if err != nil || len(accounts) == 0 {
		return 0
	}
	now := time.Now()
	candidateCount := 0
	var earliest time.Time
	for i := range accounts {
		account := &accounts[i]
		if !isOpenAIImageEditCapacityCandidate(account, requestedModel, now) {
			continue
		}
		candidateCount++
		until, ok := openAIImageEditTransientBlockedUntil(account, now)
		if !ok {
			return 0
		}
		if earliest.IsZero() || until.Before(earliest) {
			earliest = until
		}
	}
	if candidateCount == 0 || earliest.IsZero() {
		return 0
	}
	delay := time.Until(earliest) + openAIImageEditCapacityRetryPadding
	if delay <= 0 {
		delay = openAIImageEditCapacityRetryPadding
	}
	if delay > openAIImageEditCapacityRetryMax {
		delay = openAIImageEditCapacityRetryMax
	}
	return delay
}

func (s *OpenAIGatewayService) listOpenAIImageEditCapacityAccounts(ctx context.Context, groupID *int64) ([]Account, error) {
	if s == nil || s.accountRepo == nil {
		return nil, nil
	}
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		return s.accountRepo.ListByPlatform(ctx, PlatformOpenAI)
	}
	if groupID != nil {
		return s.accountRepo.ListByGroup(ctx, *groupID)
	}
	return s.accountRepo.ListByPlatform(ctx, PlatformOpenAI)
}

func isOpenAIImageEditCapacityCandidate(account *Account, requestedModel string, now time.Time) bool {
	if account == nil || !account.IsOpenAI() || !account.IsActive() || !account.Schedulable {
		return false
	}
	if account.AutoPauseOnExpired && account.ExpiresAt != nil && !now.Before(*account.ExpiresAt) {
		return false
	}
	if account.OverloadUntil != nil && now.Before(*account.OverloadUntil) {
		return false
	}
	if account.RateLimitResetAt != nil && now.Before(*account.RateLimitResetAt) {
		return false
	}
	return account.IsModelSupported(requestedModel) && account.SupportsOpenAIImageCapability(OpenAIImagesCapabilityBasic)
}

func openAIImageEditTransientBlockedUntil(account *Account, now time.Time) (time.Time, bool) {
	if account == nil || account.TempUnschedulableUntil == nil || !now.Before(*account.TempUnschedulableUntil) {
		return time.Time{}, false
	}
	if !strings.Contains(account.TempUnschedulableReason, openAIImageEditTransientReasonPrefix) {
		return time.Time{}, false
	}
	return *account.TempUnschedulableUntil, true
}

func durationCeilSeconds(value time.Duration) int {
	if value <= 0 {
		return 0
	}
	return int((value + time.Second - time.Nanosecond) / time.Second)
}

func isOpenAIImageTransientStatus(statusCode int) bool {
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
	return openAIImageTransientCooldownReason(openAIImageEditTransientReasonPrefix, failoverErr)
}

func openAIImageTransientCooldownReason(prefix string, failoverErr *UpstreamFailoverError) string {
	statusCode := 0
	if failoverErr != nil {
		statusCode = failoverErr.StatusCode
	}
	reason := fmt.Sprintf("%s: status=%d", prefix, statusCode)
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
