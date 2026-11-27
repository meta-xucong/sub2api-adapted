package routes

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/nativeharness"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// Key-scoped, not model-scoped: removed/reconfigured models must not fall through
// to ordinary providers on replay. Runs after real authentication and before
// composite routing, account selection, retries or billing handlers.
func nativeStandardDispatch(cfg *config.Config, chat, models gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cfg == nil || !cfg.Gateway.NativeHarness.StandardAPI {
			c.Next()
			return
		}
		key, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || key == nil || key.ID <= 0 || key.UserID <= 0 || key.ID != cfg.Gateway.NativeHarness.AllowedAPIKeyID || key.UserID != cfg.Gateway.NativeHarness.AllowedUserID {
			c.Next()
			return
		}
		switch c.Request.URL.Path {
		case nativeharness.StandardChatPath:
			if c.Request.Method == http.MethodPost {
				chat(c)
				c.Abort()
				return
			}
		case nativeharness.StandardModelsPath:
			if c.Request.Method == http.MethodGet {
				models(c)
				c.Abort()
				return
			}
		default:
			c.Next()
			return
		}
		c.Header("Cache-Control", "no-store")
		c.AbortWithStatusJSON(http.StatusMethodNotAllowed, gin.H{"error": gin.H{"code": "NATIVE_METHOD_NOT_SUPPORTED", "type": "native_channel_error", "message": "Unsupported native method"}})
	}
}
