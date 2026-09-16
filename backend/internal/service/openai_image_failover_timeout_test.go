//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type delayedImageTimeoutBody struct {
	delay time.Duration
}

func (b *delayedImageTimeoutBody) Read([]byte) (int, error) {
	time.Sleep(b.delay)
	return 0, context.DeadlineExceeded
}

func (b *delayedImageTimeoutBody) Close() error { return nil }

type captureImageHealthLedger struct {
	event *smartrouter.HealthEvent
}

func (l *captureImageHealthLedger) LoadStates(context.Context) ([]SmartRouterHealthState, error) {
	return nil, nil
}

func (l *captureImageHealthLedger) RecordEvent(_ context.Context, event smartrouter.HealthEvent) error {
	l.event = &event
	return nil
}

func (l *captureImageHealthLedger) ListCapabilityEvidence(context.Context) ([]SmartRouterCapabilityEvidence, error) {
	return nil, nil
}

func (l *captureImageHealthLedger) BeginCalibrationRun(context.Context, time.Time) (*SmartRouterCalibrationRun, bool, error) {
	return nil, false, nil
}

func (l *captureImageHealthLedger) RecordCalibrationResult(context.Context, int64, SmartRouterCalibrationResult) error {
	return nil
}

func (l *captureImageHealthLedger) FinishCalibrationRun(context.Context, int64, bool, string) error {
	return nil
}

func TestIsOpenAIImageAttemptTimeoutSeparatesRequestDeadline(t *testing.T) {
	attemptCtx, cancelAttempt := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelAttempt()

	require.True(t, isOpenAIImageAttemptTimeout(context.DeadlineExceeded, attemptCtx, context.Background()))

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	cancelRequest()
	require.False(t, isOpenAIImageAttemptTimeout(context.DeadlineExceeded, attemptCtx, requestCtx))
	require.False(t, isOpenAIImageAttemptTimeout(context.Canceled, attemptCtx, context.Background()))
}

func TestOpenAIImageAttemptTimeoutFailoverPreservesPublicContract(t *testing.T) {
	resp := &http.Response{Header: http.Header{"X-Request-Id": []string{"req-timeout"}}}
	err := newOpenAIImageAttemptTimeoutFailover(resp)

	require.Equal(t, http.StatusBadGateway, err.StatusCode)
	require.Equal(t, GatewayFailureReason("openai_image_attempt_timeout"), err.Reason)
	require.Equal(t, "req-timeout", err.ResponseHeaders.Get("X-Request-Id"))
	require.JSONEq(t, `{"error":{"type":"upstream_error","code":"upstream_timeout","message":"Upstream image request timed out"}}`, string(err.ResponseBody))
}

func TestReportSmartRouterImageResultClassifiesAttemptTimeout(t *testing.T) {
	ledger := &captureImageHealthLedger{}
	svc := &OpenAIGatewayService{
		cfg:                     &config.Config{Gateway: config.GatewayConfig{}},
		smartRouterHealthLedger: ledger,
	}
	svc.cfg.Gateway.SmartRouter.Enabled = true
	parsed := &OpenAIImagesRequest{Model: "gpt-image-2"}
	account := &Account{ID: 9010, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	svc.ReportSmartRouterImageResult(account, parsed, nil, newOpenAIImageAttemptTimeoutFailover(nil), 10)

	require.NotNil(t, ledger.event)
	require.Equal(t, smartrouter.FailureTimeout, ledger.event.FailureClass)
	require.Equal(t, int64(9010), ledger.event.AccountID)
}

func TestForwardImagesAPIKeyBodyTimeoutBecomesFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := &OpenAIGatewayService{
		cfg: &config.Config{Gateway: config.GatewayConfig{ImageUpstreamTimeoutSeconds: 1}},
		httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       &delayedImageTimeoutBody{delay: 1100 * time.Millisecond},
		}},
	}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	account := &Account{ID: 9001, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "test-key", "base_url": "https://image.example/v1",
	}}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, GatewayFailureReason("openai_image_attempt_timeout"), failoverErr.Reason)
	require.False(t, c.Writer.Written())
}

func TestForwardImagesOAuthBodyTimeoutBecomesFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := &OpenAIGatewayService{
		cfg: &config.Config{Gateway: config.GatewayConfig{ImageUpstreamTimeoutSeconds: 1}},
		httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       &delayedImageTimeoutBody{delay: 1100 * time.Millisecond},
		}},
	}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	account := &Account{ID: 9002, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{
		"access_token": "test-token",
	}}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, GatewayFailureReason("openai_image_attempt_timeout"), failoverErr.Reason)
	require.False(t, errors.Is(err, context.DeadlineExceeded))
}

func TestForwardImagesBodyTimeoutDoesNotStartAnotherAttemptAfterRequestDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := &OpenAIGatewayService{
		cfg: &config.Config{Gateway: config.GatewayConfig{ImageUpstreamTimeoutSeconds: 1}},
		httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       &delayedImageTimeoutBody{delay: 1100 * time.Millisecond},
		}},
	}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	account := &Account{ID: 9003, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "test-key", "base_url": "https://image.example/v1",
	}}
	requestCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	result, err := svc.ForwardImages(requestCtx, c, account, body, parsed, "")
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.Error(t, err)
}
