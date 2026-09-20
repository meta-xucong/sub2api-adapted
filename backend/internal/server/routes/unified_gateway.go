package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

// registerUnifiedGatewayRuntimeRoutes mounts the isolated runtime after the
// legacy route tree. It deliberately does not use compositeTarget or any
// legacy billing middleware; authentication is shared, dispatch and
// settlement are owned by UnifiedGatewayRuntimeHandler.
func registerUnifiedGatewayRuntimeRoutes(
	r *gin.Engine,
	h *handler.UnifiedGatewayRuntimeHandler,
	apiKeyAuth middleware.APIKeyAuthMiddleware,
	bodyLimit gin.HandlerFunc,
	clientRequestID gin.HandlerFunc,
	opsErrorLogger gin.HandlerFunc,
	endpointNorm gin.HandlerFunc,
) {
	if r == nil || h == nil {
		return
	}
	group := r.Group("/unified/v1")
	group.Use(bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth))
	{
		group.GET("/models", h.Models)
		group.POST("/chat/completions", h.ChatCompletions)
		group.POST("/responses", h.Responses)
		group.POST("/images/generations", h.Images)
		group.POST("/images/edits", h.ImageEdits)
		group.POST("/videos", h.Videos)
		group.POST("/videos/generations", h.Videos)
		group.GET("/videos/generations/:request_id/content", h.VideoContent)
		group.GET("/videos/edits/:request_id/content", h.VideoContent)
		group.GET("/videos/extensions/:request_id/content", h.VideoContent)
		group.GET("/videos/:request_id/content", h.VideoContent)
		group.GET("/videos/generations/:request_id", h.VideoStatus)
		group.GET("/videos/edits/:request_id", h.VideoStatus)
		group.GET("/videos/extensions/:request_id", h.VideoStatus)
		group.GET("/videos/:request_id", h.VideoStatus)
	}
}
