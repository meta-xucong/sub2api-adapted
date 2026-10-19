//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIImagePollTimeoutClassifierIsNativeAndExact(t *testing.T) {
	body := []byte(`{"error":{"code":"image_poll_timeout","message":"timed out"}}`)
	for _, tc := range []struct {
		name   string
		status int
		body   []byte
		want   bool
	}{
		{name: "exact structured upstream code", status: http.StatusBadRequest, body: body, want: true},
		{name: "nested response code", status: http.StatusBadRequest, body: []byte(`{"response":{"error":{"code":"image_poll_timeout"}}}`), want: true},
		{name: "top level trimmed code", status: http.StatusBadRequest, body: []byte(`{"code":"  image_poll_timeout\t"}`), want: true},
		{name: "case remains exact", status: http.StatusBadRequest, body: []byte(`{"error":{"code":"IMAGE_POLL_TIMEOUT"}}`)},
		{name: "prefix is not exact", status: http.StatusBadRequest, body: []byte(`{"error":{"code":"image_poll_timeout_extra"}}`)},
		{name: "invalid trailing JSON", status: http.StatusBadRequest, body: []byte(`{"error":{"code":"image_poll_timeout"}} trailing`)},
		{name: "invalid truncated JSON", status: http.StatusBadRequest, body: []byte(`{"error":{"code":"image_poll_timeout"}`)},
		{name: "object code", status: http.StatusBadRequest, body: []byte(`{"error":{"code":{"value":"image_poll_timeout"}}}`)},
		{name: "array code", status: http.StatusBadRequest, body: []byte(`{"error":{"code":["image_poll_timeout"]}}`)},
		{name: "quoted whole response", status: http.StatusBadRequest, body: []byte(`"{\"error\":{\"code\":\"image_poll_timeout\"}}"`)},
		{name: "HTTP 200 SSE-style body", status: http.StatusOK, body: body},
		{name: "other HTTP error", status: http.StatusGatewayTimeout, body: body},
		{name: "ordinary 400", status: http.StatusBadRequest, body: []byte(`{"error":{"code":"invalid_request_error"}}`)},
		{name: "code in message is not structured error code", status: http.StatusBadRequest, body: []byte(`{"error":{"message":"{\"error\":{\"code\":\"image_poll_timeout\"}}"}}`)},
		{name: "plain text", status: http.StatusBadRequest, body: []byte("image_poll_timeout")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isOpenAIImagesPollTimeout(tc.status, tc.body))
		})
	}
}

func testOpenAIImagePollTimeoutPreservesExistingGates(t *testing.T, smart bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.Gateway.SmartRouter.Enabled = smart
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

func TestOpenAIImagePollTimeoutPreservesExistingGates(t *testing.T) {
	for _, smart := range []bool{false, true} {
		t.Run(fmt.Sprintf("smart_router=%t", smart), func(t *testing.T) {
			testOpenAIImagePollTimeoutPreservesExistingGates(t, smart)
		})
	}
}

func TestOpenAIImagePollTimeoutRecoveryNeverBlocksSharedAccount(t *testing.T) {
	body := []byte(`{"error":{"code":"image_poll_timeout","message":"sensitive upstream detail"}}`)
	headers := http.Header{"X-Request-Id": []string{"mock-request"}}
	failure := openAIImagesHTTPFailure(http.StatusBadRequest, body, headers)
	require.Equal(t, http.StatusBadRequest, failure.StatusCode)
	require.Equal(t, openAIImageTimeoutReason, failure.Reason)
	require.False(t, failure.RetryableOnSameAccount)
	require.Equal(t, body, failure.ResponseBody)
	require.Equal(t, headers, failure.ResponseHeaders)
	headers.Set("X-Request-Id", "mutated")
	require.Equal(t, "mock-request", failure.ResponseHeaders.Get("X-Request-Id"), "failure owns a header snapshot")
	require.NotEqual(t, openAIImageTimeoutReason, openAIImagesHTTPFailure(http.StatusBadRequest, []byte(`{"error":{"code":"invalid_size"}}`), nil).Reason)
	for _, smart := range []bool{false, true} {
		t.Run(fmt.Sprintf("smart_router=%t", smart), func(t *testing.T) {
			repo := &imageTransientAccountRepo{}
			cfg := &config.Config{}
			cfg.Gateway.SmartRouter.Enabled = smart
			cfg.Gateway.ImageGenerationTransientCooldownSeconds = 8
			cfg.Gateway.ImageEditTransientCooldownSeconds = 8
			svc := &OpenAIGatewayService{accountRepo: repo, cfg: cfg}
			ledger := &smartRouterRuntimeTestLedger{}
			svc.SetSmartRouterHealthLedger(ledger)
			generationAccount := &Account{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive}
			editAccount := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive}
			for _, err := range []*UpstreamFailoverError{failure, {StatusCode: http.StatusBadRequest, ResponseBody: failure.ResponseBody}} {
				svc.TempUnscheduleImageGenerationTransientError(context.Background(), generationAccount, err)
				svc.TempUnscheduleImageEditTransientError(context.Background(), editAccount, err)
			}
			require.Empty(t, repo.calls, "native poll timeout must not persist an account-wide cooldown in either router mode")
			for _, account := range []*Account{generationAccount, editAccount} {
				require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
				for _, model := range []string{"gpt-5.5", "grok-imagine-video-1.5"} {
					require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, model), "shared text/video scheduling must remain available")
				}
			}
			svc.ReportSmartRouterImageResult(context.Background(), generationAccount, &OpenAIImagesRequest{Model: "gpt-image-2"}, nil, failure, 8)
			svc.ReportSmartRouterImageResult(context.Background(), editAccount, &OpenAIImagesRequest{Model: "gpt-image-2", Endpoint: openAIImagesEditsEndpoint}, nil, failure, 8)
			if !smart {
				require.Empty(t, ledger.events, "disabled Smart Router retains native retries without persistent image health")
				return
			}
			require.Len(t, ledger.events, 2)
			for i, capability := range []core.Capability{core.CapabilityImageGeneration, core.CapabilityImageEdit} {
				require.Equal(t, capability, ledger.events[i].Key.Capability)
				require.Equal(t, "gpt-image-2", ledger.events[i].Key.Model)
				require.Equal(t, core.FailureTimeout, ledger.events[i].FailureClass)
				require.NotContains(t, ledger.events[i].ErrorSummary, "sensitive upstream detail")
			}
		})
	}
}
