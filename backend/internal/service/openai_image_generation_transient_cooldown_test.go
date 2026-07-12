//go:build unit

package service

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestTempUnscheduleImageGenerationTransientError_BlocksAndPersists(t *testing.T) {
	repo := &imageEditCooldownAccountRepoStub{}
	cfg := &config.Config{}
	cfg.Gateway.ImageGenerationTransientCooldownSeconds = 7
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: cfg}
	account := &Account{ID: 7878, Name: "generation-lane", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive}
	failoverErr := &UpstreamFailoverError{
		StatusCode:   http.StatusBadGateway,
		ResponseBody: []byte(`{"error":{"message":"upstream connection reset"}}`),
	}

	before := time.Now()
	svc.TempUnscheduleImageGenerationTransientError(context.Background(), account, failoverErr)
	after := time.Now()

	require.Len(t, repo.tempUnschedCalls, 1)
	call := repo.tempUnschedCalls[0]
	require.Equal(t, account.ID, call.accountID)
	require.True(t, call.until.After(before.Add(6*time.Second)))
	require.True(t, call.until.Before(after.Add(8*time.Second)))
	require.Contains(t, call.reason, "status=502")
	require.Contains(t, call.reason, "upstream connection reset")
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.Equal(t, StatusActive, account.Status)
}

func TestTempUnscheduleImageGenerationTransientError_HandlesUpstreamTextReply(t *testing.T) {
	repo := &imageEditCooldownAccountRepoStub{}
	cfg := &config.Config{}
	cfg.Gateway.ImageGenerationTransientCooldownSeconds = 7
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: cfg}
	account := &Account{ID: 8989, Name: "text-reply-lane", Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	svc.TempUnscheduleImageGenerationTransientError(context.Background(), account, &UpstreamFailoverError{
		StatusCode:   http.StatusBadRequest,
		ResponseBody: []byte(`{"error":{"code":"upstream_text_reply","message":"requires a usable image target"}}`),
	})

	require.Len(t, repo.tempUnschedCalls, 1)
	require.Contains(t, repo.tempUnschedCalls[0].reason, "status=400")
}

func TestTempUnscheduleImageGenerationTransientError_IgnoresDeterministicBadRequest(t *testing.T) {
	repo := &imageEditCooldownAccountRepoStub{}
	cfg := &config.Config{}
	cfg.Gateway.ImageGenerationTransientCooldownSeconds = 7
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: cfg}
	account := &Account{ID: 9090, Name: "bad-request", Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	svc.TempUnscheduleImageGenerationTransientError(context.Background(), account, &UpstreamFailoverError{
		StatusCode:   http.StatusBadRequest,
		ResponseBody: []byte(`{"error":{"code":"invalid_parameter","message":"unsupported size"}}`),
	})

	require.Empty(t, repo.tempUnschedCalls)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIImageGenerationTransientCooldownReason_TruncatesLongBody(t *testing.T) {
	reason := openAIImageGenerationTransientCooldownReason(&UpstreamFailoverError{
		StatusCode:   http.StatusServiceUnavailable,
		ResponseBody: []byte(strings.Repeat("x", 2000)),
	})

	require.Contains(t, reason, "status=503")
	require.LessOrEqual(t, len(reason), len(openAIImageGenerationTransientReasonPrefix+": status=503: ")+512)
}

func TestTempUnscheduleImageGenerationTransientError_DisabledByConfig(t *testing.T) {
	repo := &imageEditCooldownAccountRepoStub{}
	cfg := &config.Config{}
	cfg.Gateway.ImageGenerationTransientCooldownSeconds = 0
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: cfg}
	account := &Account{ID: 9191, Name: "disabled", Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	svc.TempUnscheduleImageGenerationTransientError(context.Background(), account, &UpstreamFailoverError{
		StatusCode:   http.StatusServiceUnavailable,
		ResponseBody: []byte(`gateway unavailable`),
	})

	require.Empty(t, repo.tempUnschedCalls)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIImageTransientCooldown_ExpandsWithRecentFailures(t *testing.T) {
	stats := newOpenAIAccountRuntimeStats()
	svc := &OpenAIGatewayService{openaiAccountStats: stats}
	account := &Account{ID: 9292, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	require.Equal(t, 7*time.Second, svc.openAIImageTransientCooldownForAccount(account, 7*time.Second))
	stats.report(account.ID, false, nil)
	stats.report(account.ID, false, nil)
	require.Equal(t, 14*time.Second, svc.openAIImageTransientCooldownForAccount(account, 7*time.Second))
	stats.report(account.ID, false, nil)
	stats.report(account.ID, false, nil)
	require.Equal(t, 28*time.Second, svc.openAIImageTransientCooldownForAccount(account, 7*time.Second))
}
