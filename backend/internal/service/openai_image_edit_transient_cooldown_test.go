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
	accounts         []Account
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

func (r *imageEditCooldownAccountRepoStub) ListByGroup(_ context.Context, _ int64) ([]Account, error) {
	return append([]Account(nil), r.accounts...), nil
}

func (r *imageEditCooldownAccountRepoStub) ListByPlatform(_ context.Context, platform string) ([]Account, error) {
	accounts := make([]Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if account.Platform == platform {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
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

func TestOpenAIImageEditCapacityRetryDelay_WaitsForAllTransientCandidates(t *testing.T) {
	groupID := int64(2)
	until := time.Now().Add(2 * time.Second)
	repo := &imageEditCooldownAccountRepoStub{accounts: []Account{
		openAIImageEditCandidateForTest(12, "aiai", until),
		openAIImageEditCandidateForTest(21, "404token", until.Add(time.Second)),
	}}
	cfg := &config.Config{}
	cfg.Gateway.ImageEditTransientCooldownSeconds = 12
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: cfg}

	delay := svc.OpenAIImageEditCapacityRetryDelay(context.Background(), &groupID, "gpt-image-2")

	require.Greater(t, delay, time.Second)
	require.Less(t, delay, 4*time.Second)
}

func TestOpenAIImageEditCapacityRetryDelay_SkipsWhenCandidateAvailable(t *testing.T) {
	groupID := int64(2)
	until := time.Now().Add(2 * time.Second)
	repo := &imageEditCooldownAccountRepoStub{accounts: []Account{
		openAIImageEditCandidateForTest(12, "aiai", until),
		{
			ID:          21,
			Name:        "404token",
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Schedulable: true,
		},
	}}
	cfg := &config.Config{}
	cfg.Gateway.ImageEditTransientCooldownSeconds = 12
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: cfg}

	delay := svc.OpenAIImageEditCapacityRetryDelay(context.Background(), &groupID, "gpt-image-2")

	require.Zero(t, delay)
}

func TestOpenAIImageEditCapacityRetryDelay_IgnoresNonImageEditSmartRouterLane(t *testing.T) {
	groupID := int64(2)
	until := time.Now().Add(2 * time.Second)
	candidate := openAIImageEditCandidateForTest(22, "chat-only", until)
	candidate.Extra = map[string]any{
		"smart_router": map[string]any{
			"capabilities": []any{"chat", "responses"},
		},
	}
	repo := &imageEditCooldownAccountRepoStub{accounts: []Account{candidate}}
	cfg := &config.Config{}
	cfg.Gateway.ImageEditTransientCooldownSeconds = 12
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: cfg}

	delay := svc.OpenAIImageEditCapacityRetryDelay(context.Background(), &groupID, "gpt-image-2")

	require.Zero(t, delay)
}

func TestOpenAIImageEditTransientRetryAfterSeconds_CeilsPadding(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ImageEditTransientCooldownSeconds = 7
	svc := &OpenAIGatewayService{cfg: cfg}

	require.Equal(t, 8, svc.OpenAIImageEditTransientRetryAfterSeconds())
}

func openAIImageEditCandidateForTest(id int64, name string, until time.Time) Account {
	return Account{
		ID:                      id,
		Name:                    name,
		Platform:                PlatformOpenAI,
		Type:                    AccountTypeAPIKey,
		Status:                  StatusActive,
		Schedulable:             true,
		TempUnschedulableUntil:  &until,
		TempUnschedulableReason: "openai image edit transient cooldown: status=502: upstream flap",
		Concurrency:             1,
		Extra:                   map[string]any{"smart_router": map[string]any{"capabilities": []any{"image_generation", "image_edit"}}},
		Credentials:             map[string]any{},
	}
}
