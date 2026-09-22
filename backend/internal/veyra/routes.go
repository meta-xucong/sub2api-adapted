package veyra

import (
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

type RoutesConfig struct {
	Enabled               bool
	AlchemyBaseURL        string
	InternalToken         string
	LoginTicketTTLSeconds int
}

type loginTicketRequest struct {
	Intent string `json:"intent"`
}

type exchangeTicketRequest struct {
	Ticket string `json:"ticket"`
}

type debitRequest struct {
	UserID         int64   `json:"user_id"`
	Amount         float64 `json:"amount"`
	IdempotencyKey string  `json:"idempotency_key"`
	Source         string  `json:"source"`
	ReferenceID    string  `json:"reference_id"`
}

type loginTicketResponse struct {
	Ticket    string `json:"ticket"`
	Intent    string `json:"intent"`
	ExpiresAt string `json:"expires_at"`
}

type exchangeTicketResponse struct {
	UserID    int64  `json:"user_id"`
	Intent    string `json:"intent"`
	ExpiresAt string `json:"expires_at"`
}

type debitResponse struct {
	UserID         int64   `json:"user_id"`
	Amount         float64 `json:"amount"`
	BalanceAfter   float64 `json:"balance_after"`
	IdempotencyKey string  `json:"idempotency_key"`
	Source         string  `json:"source,omitempty"`
	ReferenceID    string  `json:"reference_id,omitempty"`
	Replayed       bool    `json:"replayed"`
}

type accountSummaryResponse struct {
	UserID      int64   `json:"user_id"`
	Email       string  `json:"email"`
	Role        string  `json:"role"`
	Balance     float64 `json:"balance"`
	Status      string  `json:"status"`
	Concurrency int     `json:"concurrency"`
}

type portalConfigResponse struct {
	AlchemyBaseURL string `json:"alchemy_base_url"`
}

func RoutesConfigFromConfig(cfg *config.Config) RoutesConfig {
	if cfg == nil {
		return RoutesConfig{}
	}
	return RoutesConfig{
		Enabled:               cfg.Veyra.Enabled,
		AlchemyBaseURL:        cfg.Veyra.AlchemyBaseURL,
		InternalToken:         cfg.Veyra.InternalToken,
		LoginTicketTTLSeconds: cfg.Veyra.LoginTicketTTLSeconds,
	}
}

func RegisterRoutes(base *gin.RouterGroup, jwtAuth servermiddleware.JWTAuthMiddleware, cfg RoutesConfig, store TicketStore, ledger DebitLedger, users BalanceAccountService) {
	if base == nil || !cfg.Enabled {
		return
	}
	if store == nil {
		store = NewMemoryTicketStore()
	}
	if ledger == nil {
		ledger = NewMemoryDebitLedger()
	}

	group := base.Group("/veyra")
	group.GET("/portal/config", portalConfigHandler(cfg))
	group.POST("/login-ticket", gin.HandlerFunc(jwtAuth), issueLoginTicketHandler(cfg, store))

	internal := group.Group("/internal")
	internal.Use(internalTokenGuard(cfg.InternalToken))
	internal.POST("/login-ticket/exchange", exchangeLoginTicketHandler(store))
	internal.GET("/users/:user_id/account", accountSummaryHandler(users))
	internal.POST("/billing/debit", debitHandler(users, ledger))
}

func portalConfigHandler(cfg RoutesConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		response.Success(c, portalConfigResponse{
			AlchemyBaseURL: normalizePortalBaseURL(cfg.AlchemyBaseURL),
		})
	}
}

func normalizePortalBaseURL(raw string) string {
	baseURL := strings.TrimSpace(raw)
	if baseURL == "" {
		return "https://alchemy.aiself.vip"
	}
	return strings.TrimRight(baseURL, "/")
}

func issueLoginTicketHandler(cfg RoutesConfig, store TicketStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		subject, ok := servermiddleware.GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			response.Unauthorized(c, "User not authenticated")
			return
		}
		var req loginTicketRequest
		_ = c.ShouldBindJSON(&req)
		ticket, err := store.Create(c.Request.Context(), subject.UserID, req.Intent, TicketTTL(cfg.LoginTicketTTLSeconds))
		if err != nil {
			response.InternalError(c, "Failed to create login ticket")
			return
		}
		response.Success(c, loginTicketResponse{
			Ticket:    ticket.Token,
			Intent:    ticket.Intent,
			ExpiresAt: ticket.ExpiresAt.UTC().Format(http.TimeFormat),
		})
	}
}

func exchangeLoginTicketHandler(store TicketStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req exchangeTicketRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.BadRequest(c, "Invalid request")
			return
		}
		ticket, err := store.Consume(c.Request.Context(), req.Ticket, time.Now().UTC())
		if err != nil {
			response.Unauthorized(c, "Invalid or expired ticket")
			return
		}
		response.Success(c, exchangeTicketResponse{
			UserID:    ticket.UserID,
			Intent:    ticket.Intent,
			ExpiresAt: ticket.ExpiresAt.UTC().Format(http.TimeFormat),
		})
	}
}

func accountSummaryHandler(users SessionUserReader) gin.HandlerFunc {
	return func(c *gin.Context) {
		if users == nil {
			response.InternalError(c, "Veyra account service is not configured")
			return
		}
		userID, err := strconv.ParseInt(strings.TrimSpace(c.Param("user_id")), 10, 64)
		if err != nil || userID <= 0 {
			response.BadRequest(c, "Invalid user_id")
			return
		}
		user, err := users.GetByID(c.Request.Context(), userID)
		if err != nil || user == nil {
			response.NotFound(c, "User not found")
			return
		}
		response.Success(c, accountSummaryResponse{
			UserID:      user.ID,
			Email:       user.Email,
			Role:        user.Role,
			Balance:     user.Balance,
			Status:      user.Status,
			Concurrency: user.Concurrency,
		})
	}
}

func debitHandler(accounts BalanceAccountService, ledger DebitLedger) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req debitRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.BadRequest(c, "Invalid request")
			return
		}
		result, err := DebitBalance(c.Request.Context(), accounts, ledger, DebitInput{
			UserID:         req.UserID,
			Amount:         req.Amount,
			IdempotencyKey: req.IdempotencyKey,
			Source:         req.Source,
			ReferenceID:    req.ReferenceID,
		})
		if err != nil {
			writeDebitError(c, err)
			return
		}
		response.Success(c, debitResponse{
			UserID:         result.UserID,
			Amount:         result.Amount,
			BalanceAfter:   result.BalanceAfter,
			IdempotencyKey: result.IdempotencyKey,
			Source:         result.Source,
			ReferenceID:    result.ReferenceID,
			Replayed:       result.Replayed,
		})
	}
}

func writeDebitError(c *gin.Context, err error) {
	switch err {
	case ErrDebitInvalidRequest, ErrDebitIdempotencyRequired:
		response.BadRequest(c, "Invalid debit request")
	case ErrDebitIdempotencyConflict:
		response.Error(c, http.StatusConflict, "Idempotency conflict")
	case ErrDebitUserNotFound:
		response.NotFound(c, "User not found")
	case ErrDebitInactiveUser:
		response.Forbidden(c, "User is not active")
	case ErrDebitInsufficientBalance:
		response.Error(c, http.StatusPaymentRequired, "Insufficient balance")
	default:
		response.InternalError(c, "Failed to debit balance")
	}
}

func internalTokenGuard(expected string) gin.HandlerFunc {
	return func(c *gin.Context) {
		expected = strings.TrimSpace(expected)
		if expected == "" {
			response.Forbidden(c, "Veyra internal token is not configured")
			c.Abort()
			return
		}
		got := strings.TrimSpace(c.GetHeader("X-Veyra-Internal-Token"))
		if subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			response.Forbidden(c, "Invalid Veyra internal token")
			c.Abort()
			return
		}
		c.Next()
	}
}
