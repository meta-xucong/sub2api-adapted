package veyra

import (
	"context"
	"crypto/subtle"
	"errors"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type nodeRewardAccounts interface {
	GetNodeRewardSettlement(context.Context, int64) (*service.VeyraNodeRewardResult, error)
	SettleNodeRewards(context.Context, int64, int64) (*service.VeyraNodeRewardResult, error)
	ListNodeRewardReceipts(context.Context, int64, int64, int) (*service.VeyraNodeRewardReceiptPage, error)
}

func registerNodeRewardRoutes(internal *gin.RouterGroup, users BalanceAccountService, cfg RoutesConfig) {
	accounts, ok := users.(nodeRewardAccounts)
	token := strings.TrimSpace(cfg.NodeRewardCreditToken)
	if !ok || len(token) < 32 || token == strings.TrimSpace(cfg.InternalToken) {
		return
	}
	rewards := internal.Group("/users/:user_id/node-rewards")
	rewards.Use(func(c *gin.Context) {
		values := c.Request.Header.Values("X-Veyra-Node-Reward-Token")
		if len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), []byte(token)) != 1 {
			response.Forbidden(c, "Invalid node reward service credential")
			c.Abort()
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Next()
	})
	rewards.GET("", nodeRewardHandler(accounts, false))
	rewards.POST("", nodeRewardHandler(accounts, true))
	rewards.GET("/receipts", nodeRewardReceiptHandler(accounts))
}

func nodeRewardHandler(accounts nodeRewardAccounts, redeem bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
		if err != nil || userID <= 0 {
			response.BadRequest(c, "Invalid account")
			return
		}
		ctx := c.Request.Context()
		var result *service.VeyraNodeRewardResult
		if redeem {
			var request struct {
				EligibleImages int64 `json:"eligible_images"`
			}
			if c.ShouldBindJSON(&request) != nil || request.EligibleImages <= 0 ||
				request.EligibleImages > service.MaxVeyraNodeRewardImages {
				response.BadRequest(c, "Invalid node reward count")
				return
			}
			result, err = accounts.SettleNodeRewards(ctx, userID, request.EligibleImages)
		} else {
			result, err = accounts.GetNodeRewardSettlement(ctx, userID)
		}
		if err != nil || result == nil {
			nodeRewardErrorResponse(c, err)
			return
		}
		response.Success(c, result)
	}
}

func nodeRewardReceiptHandler(accounts nodeRewardAccounts) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
		if err != nil || userID <= 0 {
			response.BadRequest(c, "Invalid account")
			return
		}
		query := c.Request.URL.Query()
		before, err := optionalPositiveReceiptInteger(query["before_redeemed_images"], 0)
		if err != nil {
			response.BadRequest(c, "Invalid node reward receipt cursor")
			return
		}
		limit, err := optionalPositiveReceiptInteger(query["limit"], service.DefaultVeyraNodeRewardReceiptLimit)
		if err != nil || limit > service.MaxVeyraNodeRewardReceiptLimit {
			response.BadRequest(c, "Invalid node reward receipt limit")
			return
		}
		page, err := accounts.ListNodeRewardReceipts(c.Request.Context(), userID, before, int(limit))
		if err != nil || page == nil {
			nodeRewardErrorResponse(c, err)
			return
		}
		response.Success(c, page)
	}
}

func optionalPositiveReceiptInteger(values []string, fallback int64) (int64, error) {
	if len(values) == 0 {
		return fallback, nil
	}
	if len(values) != 1 {
		return 0, service.ErrVeyraNodeRewardInvalid
	}
	value, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || value <= 0 {
		return 0, service.ErrVeyraNodeRewardInvalid
	}
	return value, nil
}

func nodeRewardErrorResponse(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrVeyraNodeRewardInvalid):
		response.BadRequest(c, "Invalid node reward request")
	case errors.Is(err, service.ErrUserNotFound):
		response.NotFound(c, "User not found")
	case errors.Is(err, service.ErrVeyraNodeRewardInactive):
		response.Forbidden(c, "User is not active")
	default:
		response.InternalError(c, "Node reward settlement unavailable")
	}
}
