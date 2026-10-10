//go:build unit

package service

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type imageTransientStateCall struct {
	accountID int64
	until     time.Time
	reason    string
}

type imageTransientAccountRepo struct {
	AccountRepository
	accounts []Account
	calls    []imageTransientStateCall
}

func (r *imageTransientAccountRepo) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.calls = append(r.calls, imageTransientStateCall{accountID: id, until: until, reason: reason})
	return nil
}

func (r *imageTransientAccountRepo) ListByGroup(_ context.Context, _ int64) ([]Account, error) {
	return append([]Account(nil), r.accounts...), nil
}

func (r *imageTransientAccountRepo) ListByPlatform(_ context.Context, platform string) ([]Account, error) {
	accounts := make([]Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if account.Platform == platform {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

func TestOpenAIImageFailoverClassifierIsImageOnlyAndNarrow(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	body := []byte(`{"error":{"code":"upstream_text_reply","message":"image lane unavailable"}}`)

	require.True(t, svc.shouldFailoverOpenAIImagesResponse(account, http.StatusBadRequest, "", body))
	require.False(t, svc.shouldFailoverOpenAIUpstreamResponse(account, http.StatusBadRequest, "", body))
	require.False(t, svc.shouldFailoverOpenAIImagesResponse(account, http.StatusBadRequest, "", []byte(`{"error":{"message":"invalid size upstream_text_reply"}}`)))
	require.False(t, svc.shouldFailoverOpenAIImagesResponse(account, http.StatusUnprocessableEntity, "", body))
	require.True(t, svc.shouldFailoverOpenAIImagesResponse(account, http.StatusBadGateway, "", []byte(`gateway unavailable`)))
}

func TestTempUnscheduleOpenAIImageGenerationTransientError(t *testing.T) {
	repo := &imageTransientAccountRepo{}
	cfg := &config.Config{}
	cfg.Gateway.ImageGenerationTransientCooldownSeconds = 7
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: cfg}
	account := &Account{ID: 7878, Name: "generation-lane", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive}
	before := time.Now()
	svc.TempUnscheduleImageGenerationTransientError(context.Background(), account, &UpstreamFailoverError{
		StatusCode: http.StatusBadRequest, ResponseBody: []byte(`{"error":{"code":"upstream_text_reply","message":"image lane unavailable"}}`),
	})
	require.Len(t, repo.calls, 1)
	require.Equal(t, account.ID, repo.calls[0].accountID)
	require.True(t, repo.calls[0].until.After(before.Add(6*time.Second)))
	require.Contains(t, repo.calls[0].reason, "status=400")
	require.Equal(t, StatusActive, account.Status)
}

func TestTempUnscheduleOpenAIImageEditTransientErrorAndErrorRateBackoff(t *testing.T) {
	repo := &imageTransientAccountRepo{}
	cfg := &config.Config{}
	cfg.Gateway.ImageEditTransientCooldownSeconds = 7
	stats := newOpenAIAccountRuntimeStats()
	account := &Account{ID: 3434, Name: "edit-lane", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive}
	stats.report(account.ID, false, nil)
	stats.report(account.ID, false, nil)
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: cfg, openaiAccountStats: stats}
	before := time.Now()
	svc.TempUnscheduleImageEditTransientError(context.Background(), account, &UpstreamFailoverError{
		StatusCode: http.StatusBadGateway, ResponseBody: []byte(`{"error":{"message":"transient upstream failure"}}`),
	})
	require.Len(t, repo.calls, 1)
	require.True(t, repo.calls[0].until.After(before.Add(13*time.Second)))
	require.Contains(t, repo.calls[0].reason, "status=502")
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))

	svc.TempUnscheduleImageEditTransientError(context.Background(), account, &UpstreamFailoverError{StatusCode: http.StatusBadRequest})
	require.Len(t, repo.calls, 1, "deterministic client errors must not trigger a cooldown")
}

func TestOpenAIImageTransientCooldownBackoffThresholdsAndCap(t *testing.T) {
	stats := newOpenAIAccountRuntimeStats()
	svc := &OpenAIGatewayService{openaiAccountStats: stats}
	account := &Account{ID: 4545, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	stat := stats.loadOrCreate(account.ID)
	for _, tc := range []struct {
		rate float64
		want time.Duration
	}{
		{rate: 0.24, want: 10 * time.Second},
		{rate: 0.25, want: 20 * time.Second},
		{rate: 0.50, want: 40 * time.Second},
		{rate: 0.75, want: 80 * time.Second},
	} {
		stat.errorRateEWMABits.Store(math.Float64bits(tc.rate))
		require.Equal(t, tc.want, svc.openAIImageTransientCooldownForAccount(account, 10*time.Second))
	}
	stat.errorRateEWMABits.Store(math.Float64bits(0.75))
	require.Equal(t, maxOpenAIImageTransientBackoff, svc.openAIImageTransientCooldownForAccount(account, 2*time.Minute))
}

func TestOpenAIImageEditCapacityRetryDelayRequiresAllEligibleCandidatesInCooldown(t *testing.T) {
	groupID := int64(2)
	until := time.Now().Add(2 * time.Second)
	candidate := func(id int64) Account {
		return Account{
			ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
			TempUnschedulableUntil: &until, TempUnschedulableReason: openAIImageEditTransientReasonPrefix + ": status=502",
			Credentials: map[string]any{"models": []any{"gpt-image-2"}},
		}
	}
	repo := &imageTransientAccountRepo{accounts: []Account{candidate(1), candidate(2)}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.ImageEditTransientCooldownSeconds = 12
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: cfg}
	delay := svc.OpenAIImageEditCapacityRetryDelay(context.Background(), &groupID, "gpt-image-2")
	require.Greater(t, delay, time.Second)
	require.LessOrEqual(t, delay, 3*time.Second)

	repo.accounts = append(repo.accounts, Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"models": []any{"gpt-image-2"}}})
	require.Zero(t, svc.OpenAIImageEditCapacityRetryDelay(context.Background(), &groupID, "gpt-image-2"))
}

func TestOpenAIImageEditTransientRetryAfterSeconds(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ImageEditTransientCooldownSeconds = 7
	svc := &OpenAIGatewayService{cfg: cfg}
	require.Equal(t, 8, svc.OpenAIImageEditTransientRetryAfterSeconds())
	cfg.Gateway.ImageEditTransientCooldownSeconds = 60
	require.Equal(t, 31, svc.OpenAIImageEditTransientRetryAfterSeconds())
	cfg.Gateway.ImageEditTransientCooldownSeconds = 0
	require.Zero(t, svc.OpenAIImageEditTransientRetryAfterSeconds())
}

func TestOpenAIImageRequestAndAttemptTimeoutClassification(t *testing.T) {
	svc := &OpenAIGatewayService{}
	require.Equal(t, 180*time.Second, svc.openAIImageUpstreamTimeout())
	require.Equal(t, 600*time.Second, svc.openAIImageRequestTimeout())

	requestCtx, cancelRequest := svc.WithOpenAIImageRequestTimeout(context.Background())
	defer cancelRequest()
	deadline, ok := requestCtx.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(600*time.Second), deadline, 2*time.Second)

	attemptCtx, cancelAttempt := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelAttempt()
	require.True(t, isOpenAIImageAttemptTimeout(context.DeadlineExceeded, attemptCtx, context.Background()))
	require.False(t, isOpenAIImageAttemptTimeout(context.Canceled, attemptCtx, context.Background()))

	callerCtx, cancelCaller := context.WithCancel(context.Background())
	cancelCaller()
	require.False(t, isOpenAIImageAttemptTimeout(context.DeadlineExceeded, attemptCtx, callerCtx))

	requestDeadlineCtx, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	require.False(t, isOpenAIImageAttemptTimeout(context.DeadlineExceeded, attemptCtx, requestDeadlineCtx))

	cfg := &config.Config{}
	svc = &OpenAIGatewayService{cfg: cfg}
	ctx, cancel := svc.withOpenAIImageUpstreamTimeout(context.Background())
	defer cancel()
	_, hasDeadline := ctx.Deadline()
	require.False(t, hasDeadline)
	ctx, cancel = svc.WithOpenAIImageRequestTimeout(context.Background())
	defer cancel()
	_, hasDeadline = ctx.Deadline()
	require.False(t, hasDeadline)
}

func TestOpenAIImageAttemptTimeoutFailoverContract(t *testing.T) {
	resp := &http.Response{Header: http.Header{"X-Request-Id": []string{"image-timeout-id"}}}
	failoverErr := newOpenAIImageAttemptTimeoutFailover(resp)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.Equal(t, GatewayFailureReason("openai_image_attempt_timeout"), failoverErr.Reason)
	require.Equal(t, "image-timeout-id", failoverErr.ResponseHeaders.Get("X-Request-Id"))
	require.JSONEq(t, `{"error":{"type":"upstream_error","code":"upstream_timeout","message":"Upstream image request timed out"}}`, string(failoverErr.ResponseBody))
	require.False(t, errors.Is(failoverErr, context.DeadlineExceeded))
}

func TestOpenAIImageTransientCooldownReasonDoesNotRetainUnboundedBody(t *testing.T) {
	reason := openAIImageGenerationTransientCooldownReason(&UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, ResponseBody: []byte(strings.Repeat("x", 2000))})
	require.Contains(t, reason, "status=503")
	require.LessOrEqual(t, len(reason), len(openAIImageGenerationTransientReasonPrefix+": status=503: ")+512)
}
