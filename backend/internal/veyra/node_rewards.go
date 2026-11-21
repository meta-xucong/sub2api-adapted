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
		switch {
		case errors.Is(err, service.ErrVeyraNodeRewardInvalid):
			response.BadRequest(c, "Invalid node reward request")
		case errors.Is(err, service.ErrUserNotFound):
			response.NotFound(c, "User not found")
		case errors.Is(err, service.ErrVeyraNodeRewardInactive):
			response.Forbidden(c, "User is not active")
		case err != nil || result == nil:
			response.InternalError(c, "Node reward settlement unavailable")
		default:
			response.Success(c, result)
		}
	}
}
