package veyra

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRegisterRoutesDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	v1 := router.Group("/api/v1")

	RegisterRoutes(v1, testJWTAuth(), RoutesConfig{}, NewMemoryTicketStore(), nil, nil)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/veyra/login-ticket", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestLoginTicketIssueAndExchange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMemoryTicketStore()
	store.now = func() time.Time { return time.Now().UTC() }
	router := gin.New()
	v1 := router.Group("/api/v1")
	RegisterRoutes(v1, testJWTAuth(), RoutesConfig{
		Enabled:               true,
		InternalToken:         "internal-secret",
		LoginTicketTTLSeconds: 120,
	}, store, nil, nil)

	issueBody := bytes.NewBufferString(`{"intent":"alchemy"}`)
	issueRec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/veyra/login-ticket", issueBody)
	router.ServeHTTP(issueRec, req)
	require.Equal(t, http.StatusOK, issueRec.Code)

	var issueResp struct {
		Data loginTicketResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(issueRec.Body.Bytes(), &issueResp))
	require.Equal(t, PortalIntentAlchemy, issueResp.Data.Intent)
	require.NotEmpty(t, issueResp.Data.Ticket)

	exchangeBody := bytes.NewBufferString(`{"ticket":"` + issueResp.Data.Ticket + `"}`)
	exchangeRec := httptest.NewRecorder()
	exchangeReq := httptest.NewRequest(http.MethodPost, "/api/v1/veyra/internal/login-ticket/exchange", exchangeBody)
	exchangeReq.Header.Set("X-Veyra-Internal-Token", "internal-secret")
	router.ServeHTTP(exchangeRec, exchangeReq)
	require.Equal(t, http.StatusOK, exchangeRec.Code)

	var exchangeResp struct {
		Data exchangeTicketResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(exchangeRec.Body.Bytes(), &exchangeResp))
	require.Equal(t, int64(42), exchangeResp.Data.UserID)
	require.Equal(t, PortalIntentAlchemy, exchangeResp.Data.Intent)

	reuseRec := httptest.NewRecorder()
	reuseReq := httptest.NewRequest(http.MethodPost, "/api/v1/veyra/internal/login-ticket/exchange", bytes.NewBufferString(`{"ticket":"`+issueResp.Data.Ticket+`"}`))
	reuseReq.Header.Set("X-Veyra-Internal-Token", "internal-secret")
	router.ServeHTTP(reuseRec, reuseReq)
	require.Equal(t, http.StatusUnauthorized, reuseRec.Code)
}

func TestVersionedAndVeyraAPIBaseShareTicketStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMemoryTicketStore()
	router := gin.New()
	v1 := router.Group("/api/v1")
	api := router.Group("/api")
	cfg := RoutesConfig{
		Enabled:               true,
		InternalToken:         "internal-secret",
		LoginTicketTTLSeconds: 120,
	}
	RegisterRoutes(v1, testJWTAuth(), cfg, store, nil, nil)
	RegisterRoutes(api, testJWTAuth(), cfg, store, nil, nil)

	issueRec := httptest.NewRecorder()
	router.ServeHTTP(issueRec, httptest.NewRequest(http.MethodPost, "/api/v1/veyra/login-ticket", bytes.NewBufferString(`{"intent":"alchemy"}`)))
	require.Equal(t, http.StatusOK, issueRec.Code)

	var issueResp struct {
		Data loginTicketResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(issueRec.Body.Bytes(), &issueResp))
	require.NotEmpty(t, issueResp.Data.Ticket)

	exchangeRec := httptest.NewRecorder()
	exchangeReq := httptest.NewRequest(http.MethodPost, "/api/veyra/internal/login-ticket/exchange", bytes.NewBufferString(`{"ticket":"`+issueResp.Data.Ticket+`"}`))
	exchangeReq.Header.Set("X-Veyra-Internal-Token", "internal-secret")
	router.ServeHTTP(exchangeRec, exchangeReq)
	require.Equal(t, http.StatusOK, exchangeRec.Code)
}

func TestPortalConfigReturnsAlchemyBaseURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	api := router.Group("/api")
	RegisterRoutes(api, testJWTAuth(), RoutesConfig{
		Enabled:        true,
		AlchemyBaseURL: "https://alchemy.example.com/",
	}, NewMemoryTicketStore(), nil, nil)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/veyra/portal/config", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Data portalConfigResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "https://alchemy.example.com", resp.Data.AlchemyBaseURL)
}

func TestInternalTicketExchangeRequiresToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	v1 := router.Group("/api/v1")
	RegisterRoutes(v1, testJWTAuth(), RoutesConfig{Enabled: true}, NewMemoryTicketStore(), nil, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/veyra/internal/login-ticket/exchange", bytes.NewBufferString(`{"ticket":"x"}`))
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAccountSummaryRequiresInternalTokenAndReturnsUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	v1 := router.Group("/api/v1")
	RegisterRoutes(v1, testJWTAuth(), RoutesConfig{
		Enabled:       true,
		InternalToken: "internal-secret",
	}, NewMemoryTicketStore(), nil, &balanceAccountStub{user: &service.User{
		ID:          42,
		Email:       "user@example.com",
		Role:        service.RoleUser,
		Balance:     8.75,
		Status:      service.StatusActive,
		Concurrency: 2,
	}})

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/veyra/internal/users/42/account", nil))
	require.Equal(t, http.StatusForbidden, unauthorized.Code)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/veyra/internal/users/42/account", nil)
	req.Header.Set("X-Veyra-Internal-Token", "internal-secret")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Data accountSummaryResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, int64(42), resp.Data.UserID)
	require.Equal(t, "user@example.com", resp.Data.Email)
	require.Equal(t, 8.75, resp.Data.Balance)
}

func TestBillingDebitRouteDebitsAndReplays(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accounts := &balanceAccountStub{user: &service.User{ID: 42, Status: service.StatusActive, Balance: 10}}
	router := gin.New()
	v1 := router.Group("/api/v1")
	RegisterRoutes(v1, testJWTAuth(), RoutesConfig{
		Enabled:       true,
		InternalToken: "internal-secret",
	}, NewMemoryTicketStore(), nil, accounts)

	body := `{"user_id":42,"amount":2.5,"idempotency_key":"run-1","source":"alchemy","reference_id":"img-1"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/veyra/internal/billing/debit", bytes.NewBufferString(body))
	req.Header.Set("X-Veyra-Internal-Token", "internal-secret")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, accounts.updateCalls)

	var resp struct {
		Data debitResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, 7.5, resp.Data.BalanceAfter)
	require.False(t, resp.Data.Replayed)

	replay := httptest.NewRecorder()
	replayReq := httptest.NewRequest(http.MethodPost, "/api/v1/veyra/internal/billing/debit", bytes.NewBufferString(body))
	replayReq.Header.Set("X-Veyra-Internal-Token", "internal-secret")
	router.ServeHTTP(replay, replayReq)
	require.Equal(t, http.StatusOK, replay.Code)
	require.Equal(t, 1, accounts.updateCalls)

	var replayResp struct {
		Data debitResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(replay.Body.Bytes(), &replayResp))
	require.True(t, replayResp.Data.Replayed)
	require.Equal(t, 7.5, replayResp.Data.BalanceAfter)
}

func TestBillingDebitRouteRejectsInsufficientBalance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accounts := &balanceAccountStub{user: &service.User{ID: 42, Status: service.StatusActive, Balance: 1}}
	router := gin.New()
	v1 := router.Group("/api/v1")
	RegisterRoutes(v1, testJWTAuth(), RoutesConfig{
		Enabled:       true,
		InternalToken: "internal-secret",
	}, NewMemoryTicketStore(), nil, accounts)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/veyra/internal/billing/debit", bytes.NewBufferString(`{"user_id":42,"amount":2,"idempotency_key":"run-low"}`))
	req.Header.Set("X-Veyra-Internal-Token", "internal-secret")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusPaymentRequired, rec.Code)
	require.Equal(t, 0, accounts.updateCalls)
}

func testJWTAuth() servermiddleware.JWTAuthMiddleware {
	return servermiddleware.JWTAuthMiddleware(func(c *gin.Context) {
		c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 42, Concurrency: 3})
		c.Next()
	})
}
