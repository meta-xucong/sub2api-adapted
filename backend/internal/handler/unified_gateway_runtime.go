package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"

	"github.com/gin-gonic/gin"
)

// UnifiedGatewayRuntimeHandler is deliberately separate from GatewayHandler.
// The legacy /v1 handlers keep their existing dispatch and billing behavior;
// this handler is only mounted under /unified/v1 and is protected by the
// runtime feature gate in UnifiedGatewayRuntimeService.
type UnifiedGatewayRuntimeHandler struct {
	runtime *service.UnifiedGatewayRuntimeService
	cfg     *config.Config
}

func NewUnifiedGatewayRuntimeHandler(runtime *service.UnifiedGatewayRuntimeService, cfg *config.Config) *UnifiedGatewayRuntimeHandler {
	return &UnifiedGatewayRuntimeHandler{runtime: runtime, cfg: cfg}
}

func (h *UnifiedGatewayRuntimeHandler) Models(c *gin.Context) {
	apiKey, userID, accessGroupID, ok := unifiedGatewayPrincipal(c)
	if !ok || apiKey == nil {
		writeUnifiedRuntimeError(c, service.ErrUnifiedGatewayUnauthorized)
		return
	}
	_ = userID
	models, err := h.runtime.ListModels(c.Request.Context(), accessGroupID)
	if err != nil {
		writeUnifiedRuntimeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": models})
}

func (h *UnifiedGatewayRuntimeHandler) ChatCompletions(c *gin.Context) {
	h.forward(c, service.UnifiedGatewayEndpointChatCompletions)
}

func (h *UnifiedGatewayRuntimeHandler) Responses(c *gin.Context) {
	h.forward(c, service.UnifiedGatewayEndpointResponses)
}

func (h *UnifiedGatewayRuntimeHandler) Images(c *gin.Context) {
	h.forward(c, service.UnifiedGatewayEndpointImages)
}

func (h *UnifiedGatewayRuntimeHandler) ImageEdits(c *gin.Context) {
	h.forward(c, service.UnifiedGatewayEndpointImageEdits)
}

func (h *UnifiedGatewayRuntimeHandler) Videos(c *gin.Context) {
	h.forward(c, service.UnifiedGatewayEndpointVideos)
}

func (h *UnifiedGatewayRuntimeHandler) VideoStatus(c *gin.Context) {
	apiKey, userID, accessGroupID, ok := unifiedGatewayPrincipal(c)
	if !ok || apiKey == nil {
		writeUnifiedRuntimeError(c, service.ErrUnifiedGatewayUnauthorized)
		return
	}
	result, err := h.runtime.Poll(c.Request.Context(), apiKey.ID, userID, accessGroupID, strings.TrimSpace(c.Param("request_id")))
	if err != nil {
		writeUnifiedRuntimeError(c, err)
		return
	}
	writeUnifiedRuntimeResponse(c, result)
}

func (h *UnifiedGatewayRuntimeHandler) VideoContent(c *gin.Context) {
	apiKey, userID, accessGroupID, ok := unifiedGatewayPrincipal(c)
	if !ok || apiKey == nil {
		writeUnifiedRuntimeError(c, service.ErrUnifiedGatewayUnauthorized)
		return
	}
	result, err := h.runtime.Content(c.Request.Context(), apiKey.ID, userID, accessGroupID, strings.TrimSpace(c.Param("request_id")))
	if err != nil {
		writeUnifiedRuntimeError(c, err)
		return
	}
	writeUnifiedContentResponse(c, result)
}

func (h *UnifiedGatewayRuntimeHandler) forward(c *gin.Context, endpoint string) {
	apiKey, userID, accessGroupID, ok := unifiedGatewayPrincipal(c)
	if !ok || apiKey == nil {
		writeUnifiedRuntimeError(c, service.ErrUnifiedGatewayUnauthorized)
		return
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeUnifiedRuntimeError(c, service.ErrUnifiedGatewayInvalidRequest)
		return
	}
	model, err := unifiedGatewayRequestModel(c.GetHeader("Content-Type"), body)
	if err != nil || model == "" {
		writeUnifiedRuntimeError(c, service.ErrUnifiedGatewayInvalidRequest)
		return
	}
	requestID := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if requestID == "" {
		requestID, _ = c.Request.Context().Value(ctxkey.ClientRequestID).(string)
	}
	if requestID == "" {
		requestID = strings.TrimSpace(c.GetHeader("X-Request-ID"))
	}
	if requestID == "" {
		requestID = uuid.NewString()
	}
	if len(requestID) > 128 {
		requestID = requestID[:128]
	}
	requestContext := service.WithUnifiedGatewayUpstreamContentType(c.Request.Context(), c.GetHeader("Content-Type"))
	response, err := h.runtime.Execute(requestContext, service.UnifiedGatewayRuntimeRequest{
		RequestID:      requestID,
		AttemptID:      "public",
		APIKeyID:       apiKey.ID,
		UserID:         userID,
		AccessGroupID:  accessGroupID,
		PublicModel:    model,
		Endpoint:       endpoint,
		EstimatedUnits: estimateUnifiedGatewayUnits(endpoint, body),
		RawBody:        body,
	})
	if err != nil {
		writeUnifiedRuntimeError(c, err)
		return
	}
	writeUnifiedRuntimeResponse(c, response)
}

func unifiedGatewayPrincipal(c *gin.Context) (*service.APIKey, int64, int64, bool) {
	if c == nil {
		return nil, 0, 0, false
	}
	apiKey, ok := servermiddleware.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		return nil, 0, 0, false
	}
	userID := apiKey.UserID
	if apiKey.User != nil && apiKey.User.ID > 0 {
		userID = apiKey.User.ID
	}
	accessGroupID := int64(0)
	if apiKey.GroupID != nil {
		accessGroupID = *apiKey.GroupID
	}
	if accessGroupID == 0 && apiKey.Group != nil {
		accessGroupID = apiKey.Group.ID
	}
	return apiKey, userID, accessGroupID, apiKey.ID > 0 && userID > 0 && accessGroupID > 0
}

func unifiedGatewayRequestModel(contentType string, body []byte) (string, error) {
	mediaType, params, mediaTypeErr := mime.ParseMediaType(strings.TrimSpace(contentType))
	if mediaTypeErr == nil && strings.EqualFold(mediaType, "multipart/form-data") {
		boundary := strings.TrimSpace(params["boundary"])
		if boundary == "" {
			return "", service.ErrUnifiedGatewayInvalidRequest
		}
		reader := multipart.NewReader(bytes.NewReader(body), boundary)
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", service.ErrUnifiedGatewayInvalidRequest
			}
			name := strings.TrimSpace(part.FormName())
			if name == "model" && strings.TrimSpace(part.FileName()) == "" {
				value, readErr := io.ReadAll(io.LimitReader(part, 4097))
				_ = part.Close()
				if readErr != nil || len(value) > 4096 {
					return "", service.ErrUnifiedGatewayInvalidRequest
				}
				model := strings.TrimSpace(string(value))
				if model == "" {
					return "", service.ErrUnifiedGatewayInvalidRequest
				}
				return model, nil
			}
			_ = part.Close()
		}
		return "", service.ErrUnifiedGatewayInvalidRequest
	}
	if mediaTypeErr != nil && strings.TrimSpace(contentType) != "" {
		return "", service.ErrUnifiedGatewayInvalidRequest
	}
	var fields map[string]json.RawMessage
	if len(body) == 0 || json.Unmarshal(body, &fields) != nil {
		return "", service.ErrUnifiedGatewayInvalidRequest
	}
	var model string
	if raw := fields["model"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &model); err != nil {
			return "", err
		}
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return "", service.ErrUnifiedGatewayInvalidRequest
	}
	return model, nil
}

func estimateUnifiedGatewayUnits(endpoint string, body []byte) float64 {
	if endpoint == service.UnifiedGatewayEndpointImages || endpoint == service.UnifiedGatewayEndpointImageEdits || endpoint == service.UnifiedGatewayEndpointVideos {
		return 1
	}
	// Reserve a bounded conservative token estimate. The final capture always
	// uses upstream usage, so this estimate is never the amount charged.
	units := math.Ceil(float64(len(body)) / 4)
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) == nil {
		for _, key := range []string{"max_tokens", "max_output_tokens"} {
			if raw := fields[key]; len(raw) > 0 {
				var value float64
				if json.Unmarshal(raw, &value) == nil && value > 0 && value < 10_000_000 {
					units += math.Ceil(value)
					break
				}
			}
		}
	}
	if units < 1 {
		return 1
	}
	if units > 10_000_000 {
		return 10_000_000
	}
	return units
}

func writeUnifiedRuntimeResponse(c *gin.Context, result *service.UnifiedGatewayRuntimeResponse) {
	if result == nil {
		writeUnifiedRuntimeError(c, service.ErrUnifiedGatewayRuntimeUnsupported)
		return
	}
	for key, values := range result.Headers {
		if strings.EqualFold(key, "Content-Length") || strings.EqualFold(key, "Transfer-Encoding") {
			continue
		}
		for _, value := range values {
			c.Header(key, value)
		}
	}
	status := result.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	contentType := strings.TrimSpace(result.ContentType)
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(status, contentType, result.Body)
}

func writeUnifiedContentResponse(c *gin.Context, result *service.UnifiedGatewayContentResult) {
	if result == nil {
		writeUnifiedRuntimeError(c, service.ErrUnifiedGatewayRuntimeUnsupported)
		return
	}
	for key, values := range result.Headers {
		if strings.EqualFold(key, "Content-Length") || strings.EqualFold(key, "Transfer-Encoding") {
			continue
		}
		for _, value := range values {
			c.Header(key, value)
		}
	}
	contentType := strings.TrimSpace(result.ContentType)
	if contentType == "" {
		contentType = http.DetectContentType(result.Body)
	}
	c.Data(result.StatusCode, contentType, result.Body)
}

func writeUnifiedRuntimeError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	code := "UNIFIED_GATEWAY_ERROR"
	message := "Unified gateway request failed"
	switch {
	case errors.Is(err, service.ErrUnifiedGatewayRuntimeDisabled):
		status, code, message = http.StatusNotFound, "UNIFIED_GATEWAY_DISABLED", "Unified gateway is not enabled"
	case errors.Is(err, service.ErrUnifiedGatewayRouteNotFound), errors.Is(err, service.ErrUnifiedGatewayRuntimeRequestNotFound):
		status, code, message = http.StatusNotFound, "MODEL_NOT_FOUND", "The requested unified model or task was not found"
	case errors.Is(err, service.ErrUnifiedGatewayNoEligibleAccount):
		status, code, message = http.StatusServiceUnavailable, "NO_ELIGIBLE_UPSTREAM", "No eligible upstream is available"
	case errors.Is(err, service.ErrUnifiedGatewayBalanceInsufficient):
		status, code, message = http.StatusPaymentRequired, "INSUFFICIENT_BALANCE", "Insufficient balance"
	case errors.Is(err, service.ErrUnifiedGatewayInvalidRequest):
		status, code, message = http.StatusBadRequest, "INVALID_REQUEST", "Invalid unified gateway request"
	case errors.Is(err, service.ErrUnifiedGatewayUnauthorized):
		status, code, message = http.StatusForbidden, "ACCESS_DENIED", "Unified gateway access denied"
	case errors.Is(err, service.ErrUnifiedGatewayRuntimeUnsupported):
		status, code, message = http.StatusNotImplemented, "UNSUPPORTED_OPERATION", "Unified gateway operation is not supported"
	case errors.Is(err, service.ErrUnifiedGatewaySnapshotInProgress):
		status, code, message = http.StatusConflict, "REQUEST_IN_PROGRESS", "The unified gateway request is still in progress"
	case errors.Is(err, service.ErrUnifiedGatewayUsageMissing), errors.Is(err, service.ErrUnifiedGatewayUpstreamFailed):
		status, code, message = http.StatusBadGateway, "UPSTREAM_FAILED", "The upstream request did not complete successfully"
	case errors.Is(err, service.ErrUnifiedGatewaySettlementFailed):
		status, code, message = http.StatusInternalServerError, "SETTLEMENT_FAILED", "The request completed but settlement needs review"
	}
	if err == nil {
		message = "Unified gateway request failed"
	}
	c.JSON(status, gin.H{"error": gin.H{"type": code, "code": code, "message": message}})
}
