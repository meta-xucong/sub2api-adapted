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

type imageEditCooldownTempUnschedCall struct {
	accountID int64
	until     time.Time
	reason    string
}

type imageEditCooldownAccountRepoStub struct {
	AccountRepository
	tempUnschedCalls []imageEditCooldownTempUnschedCall
}

func (r *imageEditCooldownAccountRepoStub) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.tempUnschedCalls = append(r.tempUnschedCalls, imageEditCooldownTempUnschedCall{
		accountID: id,
		until:     until,
		reason:    reason,
	})
	return nil
}

func TestTempUnscheduleImageEditTransientError_BlocksAndPersists(t *testing.T) {
	repo := &imageEditCooldownAccountRepoStub{}
	cfg := &config.Config{}
	cfg.Gateway.ImageEditTransientCooldownSeconds = 7
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: cfg}
	account := &Account{ID: 1212, Name: "aiai-gpt-image-2", Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	failoverErr := &UpstreamFailoverError{
		StatusCode:   http.StatusInternalServerError,
		ResponseBody: []byte(`{"error":{"message":"internal_server_error from aiai"}}`),
	}

	before := time.Now()
	svc.TempUnscheduleImageEditTransientError(context.Background(), account, failoverErr)
	after := time.Now()

	require.Len(t, repo.tempUnschedCalls, 1)
	call := repo.tempUnschedCalls[0]
	require.Equal(t, account.ID, call.accountID)
	require.True(t, call.until.After(before.Add(6*time.Second)))
	require.True(t, call.until.Before(after.Add(8*time.Second)))
	require.Contains(t, call.reason, "status=500")
	require.Contains(t, call.reason, "internal_server_error from aiai")
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestTempUnscheduleImageEditTransientError_IgnoresNonTransientStatus(t *testing.T) {
	repo := &imageEditCooldownAccountRepoStub{}
	cfg := &config.Config{}
	cfg.Gateway.ImageEditTransientCooldownSeconds = 7
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: cfg}
	account := &Account{ID: 3434, Name: "bad-request", Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	svc.TempUnscheduleImageEditTransientError(context.Background(), account, &UpstreamFailoverError{
		StatusCode:   http.StatusBadRequest,
		ResponseBody: []byte(`{"error":{"message":"bad request"}}`),
	})

	require.Empty(t, repo.tempUnschedCalls)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestTempUnscheduleImageEditTransientError_DisabledByConfig(t *testing.T) {
	repo := &imageEditCooldownAccountRepoStub{}
	cfg := &config.Config{}
	cfg.Gateway.ImageEditTransientCooldownSeconds = 0
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: cfg}
	account := &Account{ID: 5656, Name: "disabled", Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	svc.TempUnscheduleImageEditTransientError(context.Background(), account, &UpstreamFailoverError{
		StatusCode:   http.StatusBadGateway,
		ResponseBody: []byte(`gateway bad`),
	})

	require.Empty(t, repo.tempUnschedCalls)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIImageEditTransientCooldownReason_TruncatesLongBody(t *testing.T) {
	reason := openAIImageEditTransientCooldownReason(&UpstreamFailoverError{
		StatusCode:   http.StatusServiceUnavailable,
		ResponseBody: []byte(strings.Repeat("x", 2000)),
	})

	require.Contains(t, reason, "status=503")
	require.LessOrEqual(t, len(reason), len("openai image edit transient cooldown: status=503: ")+512)
}
