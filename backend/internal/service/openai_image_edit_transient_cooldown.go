package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"go.uber.org/zap"
)

const defaultOpenAIImageEditTransientCooldown = 12 * time.Second

const (
	openAIImageEditTransientReasonPrefix = "openai image edit transient cooldown"
	openAIImageEditCapacityRetryPadding  = 750 * time.Millisecond
	openAIImageEditCapacityRetryMax      = 30 * time.Second
)

func (s *OpenAIGatewayService) TempUnscheduleImageEditTransientError(ctx context.Context, account *Account, failoverErr *UpstreamFailoverError) {
	if s == nil || account == nil || failoverErr == nil {
		return
	}
	if !isOpenAIImageTransientStatus(failoverErr.StatusCode) {
		return
	}
	cooldown := s.openAIImageTransientCooldownForAccount(account, s.openAIImageEditTransientCooldown())
	if cooldown <= 0 {
		return
	}
	if s.isSmartRouterEnabled() {
		// Smart Router owns transient health state. Do not write the legacy
		// temp-unschedulable flag, which would remove this lane instead of
		// lowering its effective priority.
		logger.L().With(zap.String("component", "service.openai_gateway")).Info(
			"openai.image_edit_transient_soft_penalty",
			zap.Int64("account_id", account.ID),
			zap.Int("status_code", failoverErr.StatusCode),
			zap.Duration("cooldown_hint", cooldown),
		)
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
	cooldown := s.openAIImageEditTransientCooldown()
	if cooldown <= 0 {
		return 0
	}
	until, ok := s.nextOpenAIImageEditTransientCapacityTime(ctx, groupID, requestedModel)
	if !ok {
		return 0
	}
	delay := time.Until(until) + openAIImageEditCapacityRetryPadding
	if delay <= 0 {
		delay = openAIImageEditCapacityRetryPadding
	}
	if delay > openAIImageEditCapacityRetryMax {
		delay = openAIImageEditCapacityRetryMax
	}
	return delay
}

func (s *OpenAIGatewayService) nextOpenAIImageEditTransientCapacityTime(ctx context.Context, groupID *int64, requestedModel string) (time.Time, bool) {
	accounts, err := s.listOpenAIImageEditCapacityAccounts(ctx, groupID)
	if err != nil || len(accounts) == 0 {
		return time.Time{}, false
	}

	now := time.Now()
	candidateCount := 0
	blockedCount := 0
	var earliest time.Time
	for i := range accounts {
		account := &accounts[i]
		if !isOpenAIImageEditCapacityCandidate(account, requestedModel, now) {
			continue
		}
		candidateCount++
		until, ok := openAIImageEditTransientBlockedUntil(account, now)
		if !ok {
			return time.Time{}, false
		}
		blockedCount++
		if earliest.IsZero() || until.Before(earliest) {
			earliest = until
		}
	}
	if candidateCount == 0 || blockedCount != candidateCount || earliest.IsZero() {
		return time.Time{}, false
	}
	return earliest, true
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
	if !account.IsModelSupported(requestedModel) {
		return false
	}
	if !account.SupportsOpenAIImageCapability(OpenAIImagesCapabilityBasic) {
		return false
	}
	extra := parseSmartRouterAccountExtra(account)
	if len(extra.Capabilities) > 0 && !extra.Capabilities[smartrouter.CapabilityImageEdit] {
		return false
	}
	return true
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
