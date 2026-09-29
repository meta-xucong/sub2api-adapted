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

func TestOperatorTestGuardAllowsAnonymousRequest(t *testing.T) {
	router := newOperatorTestGuardRouter(t, nil)

	response := serveOperatorTestGuardRequest(router, "/v1/images/generations", "141.11.138.220:45678", "curl/7.74.0")

	require.Equal(t, http.StatusOK, response.Code)
}

func TestOperatorTestGuardBlocksOrdinaryUserCustomerKey(t *testing.T) {
	router := newOperatorTestGuardRouter(t, operatorTestGuardCustomerKey())

	response := serveOperatorTestGuardRequest(router, "/v1/images/generations", "141.11.138.220:45678", "curl/7.74.0")

	require.Equal(t, http.StatusForbidden, response.Code)
	require.Contains(t, response.Body.String(), "OPERATOR_TEST_KEY_REQUIRED")
}

func TestOperatorTestGuardBlocksAdminWithoutDedicatedOpsKey(t *testing.T) {
	apiKey := operatorTestGuardCustomerKey()
	apiKey.User.Role = service.RoleAdmin
	apiKey.User.Email = "admin@example.com"
	router := newOperatorTestGuardRouter(t, apiKey)

	response := serveOperatorTestGuardRequest(router, "/v1/images/generations", "141.11.138.220:45678", "curl/7.74.0")

	require.Equal(t, http.StatusForbidden, response.Code)
	require.Contains(t, response.Body.String(), "OPERATOR_TEST_KEY_REQUIRED")
}

func TestOperatorTestGuardAllowsDedicatedOpsKey(t *testing.T) {
	router := newOperatorTestGuardRouter(t, operatorTestGuardOpsKey())

	response := serveOperatorTestGuardRequest(router, "/v1/images/generations", "141.11.138.220:45678", "curl/7.74.0")

	require.Equal(t, http.StatusOK, response.Code)
}

func TestOperatorTestGuardAllowsNonScriptClient(t *testing.T) {
	router := newOperatorTestGuardRouter(t, operatorTestGuardCustomerKey())

	response := serveOperatorTestGuardRequest(router, "/v1/images/generations", "141.11.138.220:45678", "Codex CLI")

	require.Equal(t, http.StatusOK, response.Code)
}

func TestOperatorTestGuardAllowsUntrustedClientIP(t *testing.T) {
	router := newOperatorTestGuardRouter(t, operatorTestGuardCustomerKey())

	response := serveOperatorTestGuardRequest(router, "/v1/images/generations", "203.0.113.10:45678", "curl/7.74.0")

	require.Equal(t, http.StatusOK, response.Code)
}

func TestOperatorTestGuardDoesNotTrustSpoofedForwardedIP(t *testing.T) {
	router := newOperatorTestGuardRouter(t, operatorTestGuardCustomerKey())
	request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	request.RemoteAddr = "203.0.113.10:45678"
	request.Header.Set("User-Agent", "curl/7.74.0")
	request.Header.Set("X-Forwarded-For", "141.11.138.220")
	request.Header.Set("X-Real-IP", "141.11.138.220")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code, "untrusted TCP peer must not become blocked solely because the header is spoofed")
}

func TestOperatorTestGuardDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := operatorTestGuardConfig()
	cfg.Gateway.OperatorTestGuard.Enabled = false
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), operatorTestGuardCustomerKey())
		c.Next()
	})
	router.Use(OperatorTestGuard(cfg))
	router.POST("/v1/images/generations", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	response := serveOperatorTestGuardRequest(router, "/v1/images/generations", "141.11.138.220:45678", "curl/7.74.0")

	require.Equal(t, http.StatusOK, response.Code)
}

func newOperatorTestGuardRouter(t *testing.T, apiKey *service.APIKey) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if apiKey != nil {
			c.Set(string(ContextKeyAPIKey), apiKey)
		}
		c.Next()
	})
	router.Use(OperatorTestGuard(operatorTestGuardConfig()))
	router.POST("/v1/images/generations", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return router
}

func serveOperatorTestGuardRequest(router http.Handler, target, remoteAddr, userAgent string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, target, nil)
	request.RemoteAddr = remoteAddr
	request.Header.Set("User-Agent", userAgent)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func operatorTestGuardConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Gateway.OperatorTestGuard = config.GatewayOperatorTestGuardConfig{
		Enabled:            true,
		RequireAdminUser:   true,
		TrustedClientIPs:   []string{"127.0.0.1", "::1", "141.11.138.220"},
		BlockedUserAgents:  []string{"curl/"},
		AllowedUserEmails:  []string{"ops-test@404token.local"},
		AllowedAPIKeyNames: []string{"ops-test*", "operator-test*"},
		Paths:              []string{"/v1/images/*", "/v1/responses"},
	}
	return cfg
}

func operatorTestGuardCustomerKey() *service.APIKey {
	return &service.APIKey{
		ID:     14,
		Name:   "simple-image",
		UserID: 13,
		User: &service.User{
			ID:     13,
			Email:  "customer@example.com",
			Role:   service.RoleUser,
			Status: service.StatusActive,
		},
	}
}

func operatorTestGuardOpsKey() *service.APIKey {
	return &service.APIKey{
		ID:     2001,
		Name:   "ops-test-image",
		UserID: 2001,
		User: &service.User{
			ID:     2001,
			Email:  "ops-test@404token.local",
			Role:   service.RoleAdmin,
			Status: service.StatusActive,
		},
	}
}
