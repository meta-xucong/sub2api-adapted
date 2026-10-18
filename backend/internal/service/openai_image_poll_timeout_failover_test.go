//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIImagePollTimeoutFailoverClassifierIsOptInAndExact(t *testing.T) {
	svc := &OpenAIGatewayService{}
	body := []byte(`{"error":{"code":"image_poll_timeout","message":"timed out"}}`)
	require.False(t, svc.isOpenAIImagePollTimeoutFailover(http.StatusBadRequest, body), "nil config is off")

	cfg := &config.Config{}
	cfg.Gateway.ImagePollTimeoutFailoverEnabled = true
	svc.cfg = cfg
	for _, tc := range []struct {
		name   string
		status int
		body   []byte
		want   bool
	}{
		{name: "exact structured upstream code", status: http.StatusBadRequest, body: body, want: true},
		{name: "HTTP 200 SSE-style body", status: http.StatusOK, body: body},
		{name: "other HTTP error", status: http.StatusGatewayTimeout, body: body},
		{name: "ordinary 400", status: http.StatusBadRequest, body: []byte(`{"error":{"code":"invalid_request_error"}}`)},
		{name: "code in message is not structured error code", status: http.StatusBadRequest, body: []byte(`{"error":{"message":"{\"error\":{\"code\":\"image_poll_timeout\"}}"}}`)},
		{name: "plain text", status: http.StatusBadRequest, body: []byte("image_poll_timeout")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, svc.isOpenAIImagePollTimeoutFailover(tc.status, tc.body))
		})
	}
}

func TestOpenAIImagePollTimeoutFailoverPreservesExistingGates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.Gateway.ImagePollTimeoutFailoverEnabled = true
	svc := &OpenAIGatewayService{cfg: cfg}
	baseBody := []byte(`{"error":{"code":"image_poll_timeout","message":"poll timed out"}}`)

	t.Run("admin passthrough", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		ruleSvc := &ErrorPassthroughService{}
		ruleSvc.setLocalCache([]*model.ErrorPassthroughRule{newNonFailoverPassthroughRule(http.StatusBadRequest, "image_poll_timeout", http.StatusTeapot, "custom timeout")})
		BindErrorPassthroughService(c, ruleSvc)
		resp := &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(baseBody))}
		_, err := svc.handleOpenAIImagesErrorResponse(WithOpenAIImagesEndpoint(context.Background()), resp, c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "gpt-image-2")
		var failoverErr *UpstreamFailoverError
		require.Error(t, err)
		require.NotErrorAs(t, err, &failoverErr)
		require.Equal(t, http.StatusTeapot, rec.Code)
	})

	t.Run("account does not handle upstream 400", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		account := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"custom_error_codes_enabled": true, "custom_error_codes": []any{500.0}}}
		resp := &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(baseBody))}
		_, err := svc.handleOpenAIImagesErrorResponse(WithOpenAIImagesEndpoint(context.Background()), resp, c, account, "gpt-image-2")
		var failoverErr *UpstreamFailoverError
		require.Error(t, err)
		require.NotErrorAs(t, err, &failoverErr)
		require.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("OAuth main model rejection", func(t *testing.T) {
		t.Setenv("SUB2API_IMAGES_MAIN_MODEL", "gpt-5.4-mini")
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		body := []byte(`{"error":{"code":"image_poll_timeout","message":"The 'gpt-5.4-mini' model is not supported when using Codex with a ChatGPT account."}}`)
		resp := &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}
		_, err := svc.handleOpenAIImagesErrorResponse(WithOpenAIImagesEndpoint(context.Background()), resp, c, openAICodexPlanGatedOAuthAccount(), "gpt-image-2")
		var failoverErr *UpstreamFailoverError
		require.Error(t, err)
		require.NotErrorAs(t, err, &failoverErr)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("ordinary 400 remains terminal", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		body := []byte(`{"error":{"code":"invalid_request_error","message":"invalid size"}}`)
		resp := &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}
		_, err := svc.handleOpenAIImagesErrorResponse(WithOpenAIImagesEndpoint(context.Background()), resp, c, &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "gpt-image-2")
		var failoverErr *UpstreamFailoverError
		require.Error(t, err)
		require.NotErrorAs(t, err, &failoverErr)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestOpenAIImagePollTimeoutFailoverReasonScopesCooldownAndSmartRouter(t *testing.T) {
	err := newOpenAIImagePollTimeoutFailover([]byte(`{"error":{"code":"image_poll_timeout","message":"sensitive upstream detail"}}`), http.Header{})
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, OpenAIImagePollTimeoutFailoverReason, err.Reason)
	require.False(t, err.RetryableOnSameAccount)
	reason := openAIImageGenerationTransientCooldownReason(err)
	require.Contains(t, reason, "code=image_poll_timeout")
	require.NotContains(t, reason, "sensitive upstream detail")
	require.True(t, isOpenAIImagePollTimeoutFailoverError(err))
	require.False(t, isOpenAIImagePollTimeoutFailoverError(&UpstreamFailoverError{StatusCode: http.StatusBadRequest, ResponseBody: err.ResponseBody}))

	repo := &imageTransientAccountRepo{}
	cfg := &config.Config{}
	cfg.Gateway.ImageGenerationTransientCooldownSeconds = 8
	cfg.Gateway.ImageEditTransientCooldownSeconds = 8
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: cfg}
	generationAccount := &Account{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive}
	editAccount := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive}
	svc.TempUnscheduleImageGenerationTransientError(context.Background(), generationAccount, err)
	svc.TempUnscheduleImageEditTransientError(context.Background(), editAccount, err)
	require.Len(t, repo.calls, 2, "both image capabilities should use their existing transient cooldown")
	require.Contains(t, repo.calls[0].reason, "code=image_poll_timeout")
	require.Contains(t, repo.calls[1].reason, "code=image_poll_timeout")
	svc.TempUnscheduleImageEditTransientError(context.Background(), editAccount, &UpstreamFailoverError{StatusCode: http.StatusBadRequest, ResponseBody: err.ResponseBody})
	require.Len(t, repo.calls, 2, "ordinary 400 must not receive the special cooldown")
}
