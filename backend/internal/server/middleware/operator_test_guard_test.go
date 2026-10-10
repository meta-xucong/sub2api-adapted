package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func operatorGuardTestConfig() *config.Config {
	return &config.Config{Gateway: config.GatewayConfig{OperatorTestGuard: config.GatewayOperatorTestGuardConfig{
		Enabled:            true,
		RequireAdminUser:   true,
		TrustedClientIPs:   []string{"127.0.0.1/32"},
		BlockedUserAgents:  []string{"curl/", "python-requests/"},
		AllowedUserEmails:  []string{"operator@example.test"},
		AllowedAPIKeyNames: []string{"ops-test*"},
		Paths:              []string{"/v1/responses", "/v1/responses/*"},
	}}}
}

func runOperatorGuardTest(t *testing.T, cfg *config.Config, requestPath, userAgent, remoteAddr, forwardedFor, realIP string, apiKey *service.APIKey) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.POST("/v1/responses", func(c *gin.Context) {
		if apiKey != nil {
			c.Set(string(ContextKeyAPIKey), apiKey)
		}
		c.Next()
	}, OperatorTestGuard(cfg), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, requestPath, nil)
	req.RemoteAddr = remoteAddr
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	if forwardedFor != "" {
		req.Header.Set("X-Forwarded-For", forwardedFor)
	}
	if realIP != "" {
		req.Header.Set("X-Real-IP", realIP)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestOperatorTestGuardBlocksLocalScriptUsingCustomerKey(t *testing.T) {
	customer := &service.APIKey{Name: "customer", User: &service.User{Role: service.RoleUser}}
	recorder := runOperatorGuardTest(t, operatorGuardTestConfig(), "/v1/responses", "curl/8.0", "127.0.0.1:3456", "", "", customer)
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "OPERATOR_TEST_KEY_REQUIRED")
}

func TestOperatorTestGuardAllowsDedicatedAdminKey(t *testing.T) {
	opsKey := &service.APIKey{Name: "ops-test-local", User: &service.User{Email: "ops@example.test", Role: service.RoleAdmin}}
	recorder := runOperatorGuardTest(t, operatorGuardTestConfig(), "/v1/responses", "curl/8.0", "127.0.0.1:3456", "", "", opsKey)
	require.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestOperatorTestGuardRequiresAdminForMatchingKeyName(t *testing.T) {
	customer := &service.APIKey{Name: "ops-test-customer", User: &service.User{Role: service.RoleUser}}
	recorder := runOperatorGuardTest(t, operatorGuardTestConfig(), "/v1/responses", "curl/8.0", "127.0.0.1:3456", "", "", customer)
	require.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestOperatorTestGuardDoesNotTrustForgedForwardedHeaders(t *testing.T) {
	customer := &service.APIKey{Name: "customer", User: &service.User{Role: service.RoleUser}}
	recorder := runOperatorGuardTest(t, operatorGuardTestConfig(), "/v1/responses", "curl/8.0", "198.51.100.8:3456", "127.0.0.1", "127.0.0.1", customer)
	require.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestOperatorTestGuardOnlyAppliesWhenEveryGuardConditionMatches(t *testing.T) {
	customer := &service.APIKey{Name: "customer", User: &service.User{Role: service.RoleUser}}
	tests := []struct {
		name       string
		request    string
		userAgent  string
		remoteAddr string
		cfg        *config.Config
		key        *service.APIKey
	}{
		{name: "ordinary user agent", request: "/v1/responses", userAgent: "Codex/1.0", remoteAddr: "127.0.0.1:3456", cfg: operatorGuardTestConfig(), key: customer},
		{name: "unconfigured path", request: "/v1/responses", userAgent: "curl/8.0", remoteAddr: "127.0.0.1:3456", cfg: &config.Config{Gateway: config.GatewayConfig{OperatorTestGuard: config.GatewayOperatorTestGuardConfig{Enabled: true, TrustedClientIPs: []string{"127.0.0.1"}, BlockedUserAgents: []string{"curl/"}, Paths: []string{"/v1/images/*"}, AllowedAPIKeyNames: []string{"ops-test*"}}}}, key: customer},
		{name: "guard disabled", request: "/v1/responses", userAgent: "curl/8.0", remoteAddr: "127.0.0.1:3456", cfg: &config.Config{}, key: customer},
		{name: "missing authenticated key context", request: "/v1/responses", userAgent: "curl/8.0", remoteAddr: "127.0.0.1:3456", cfg: operatorGuardTestConfig()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := runOperatorGuardTest(t, test.cfg, test.request, test.userAgent, test.remoteAddr, "", "", test.key)
			require.Equal(t, http.StatusNoContent, recorder.Code)
		})
	}
}

func TestOperatorTestGuardWildcardMatchesOnlyOnePathSegment(t *testing.T) {
	patterns := []string{
		"/v1/responses/*",
		"/v1/images/generations/async", "/images/generations/async",
		"/v1/images/edits/async", "/images/edits/async",
	}
	require.True(t, operatorGuardPathMatches("/v1/responses/compact", patterns))
	require.False(t, operatorGuardPathMatches("/v1/responses/a/b", patterns))
	require.False(t, operatorGuardPathMatches("/v1/images/generations", patterns), "synchronous image endpoints are outside the async test guard")
	require.False(t, operatorGuardPathMatches("/v1/images/batches", patterns), "batch image submission is outside the async test guard")
	require.True(t, operatorGuardPathMatches("/v1/images/generations/async", patterns), "the target's nested async image routes are explicitly covered")
	require.True(t, operatorGuardPathMatches("/images/generations/async", patterns))
	require.True(t, operatorGuardPathMatches("/v1/images/edits/async", patterns))
	require.True(t, operatorGuardPathMatches("/images/edits/async", patterns))
	require.False(t, operatorGuardPathMatches("/v1/images/tasks/task_1", patterns), "task polling is not one of the local script submission paths")
}
