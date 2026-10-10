package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type blockingImageHTTPUpstream struct{}

type imageRequestContextKey struct{}

func TestDetachOpenAIImageUpstreamContextPreservesValuesAndRequestDeadline(t *testing.T) {
	parent, cancelParent := context.WithDeadline(context.WithValue(context.Background(), imageRequestContextKey{}, "trace"), time.Now().Add(time.Minute))
	defer cancelParent()

	detached, cancelDetached := detachOpenAIImageUpstreamContext(parent)
	defer cancelDetached()

	require.Equal(t, "trace", detached.Value(imageRequestContextKey{}))
	deadline, ok := detached.Deadline()
	require.True(t, ok)
	parentDeadline, _ := parent.Deadline()
	require.Equal(t, parentDeadline, deadline)
	cancelParent()
	require.NoError(t, detached.Err(), "client cancellation must not cancel the detached upstream request")
}

func (blockingImageHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func (u blockingImageHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func TestForwardOpenAIImagesAttemptTimeoutFailoverForAPIKeyAndOAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name    string
		account *Account
	}{
		{
			name: "api key",
			account: &Account{
				ID: 41, Name: "image-api-key", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "test-key", "base_url": "https://api.openai.com/v1"},
			},
		},
		{
			name: "oauth",
			account: &Account{
				ID: 42, Name: "image-oauth", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "acct-test"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.ImageUpstreamTimeoutSeconds = 1
			cfg.Gateway.ImageRequestTimeoutSeconds = 5
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: blockingImageHTTPUpstream{}}
			body := []byte(`{"model":"gpt-image-2","prompt":"timeout test"}`)
			c, _ := newOpenAIImagesTestContext(t, body)
			c.Set("api_key", &APIKey{ID: 7})
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)

			ctx, cancel := svc.WithOpenAIImageRequestTimeout(context.Background())
			defer cancel()
			started := time.Now()
			_, err = svc.ForwardImages(ctx, c, tc.account, body, parsed, "")
			require.Less(t, time.Since(started), 4*time.Second)
			var failoverErr *UpstreamFailoverError
			require.True(t, errors.As(err, &failoverErr), "attempt timeout should be eligible for image-account failover: %v", err)
			require.Equal(t, GatewayFailureReason("openai_image_attempt_timeout"), failoverErr.Reason)
		})
	}
}
