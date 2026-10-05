package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// UnifiedGatewayHandler exposes the unified-gateway management plane only.
// Runtime request handlers are deliberately not part of this handler.
type UnifiedGatewayHandler struct {
	svc *service.UnifiedGatewayAdminService
}

func NewUnifiedGatewayHandler(svc *service.UnifiedGatewayAdminService) *UnifiedGatewayHandler {
	return &UnifiedGatewayHandler{svc: svc}
}

func (h *UnifiedGatewayHandler) Meta(c *gin.Context) {
	if h == nil || h.svc == nil {
		response.ErrorWithDetails(c, http.StatusInternalServerError, "internal error", "UNIFIED_GATEWAY_REPOSITORY_UNAVAILABLE", nil)
		return
	}
	ctx, _, ok := unifiedServiceContext(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	response.Success(c, h.svc.Meta(ctx))
}

type unifiedDocumentRequest struct {
	Document service.UnifiedGatewayConfig `json:"document"`
}

type unifiedPreviewRequest struct {
	Document *service.UnifiedGatewayConfig        `json:"document,omitempty"`
	Preview  service.UnifiedGatewayPreviewRequest `json:"preview"`
}

type unifiedPublishRequest struct {
	Reason          string `json:"reason"`
	ValidationToken string `json:"validation_token"`
}

type unifiedDisableRequest struct {
	Reason string `json:"reason"`
}

type unifiedRestoreRequest struct {
	Reason string `json:"reason"`
}

type unifiedPricingImportRequest struct {
	LaneID        string                        `json:"lane_id"`
	SourceGroupID string                        `json:"source_group_id"`
	ImportDigest  string                        `json:"import_digest,omitempty"`
	Document      *service.UnifiedGatewayConfig `json:"document,omitempty"`
}

func (h *UnifiedGatewayHandler) ListOptions(c *gin.Context) {
	ctx, _, ok := unifiedServiceContext(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	page, size := response.ParsePagination(c)
	result, total, err := h.svc.Options(ctx, page, size)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Success(c, gin.H{"items": result, "total": total, "page": page, "page_size": size})
}

func (h *UnifiedGatewayHandler) ListModelCandidates(c *gin.Context) {
	ctx, _, ok := unifiedServiceContext(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	items, err := h.svc.ListModelCandidates(ctx)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Success(c, gin.H{"items": items})
}

func (h *UnifiedGatewayHandler) ListConfigs(c *gin.Context) {
	ctx, _, ok := unifiedServiceContext(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	page, size := response.ParsePagination(c)
	filter := service.UnifiedGatewayConfigListFilter{Page: page, PageSize: size, Lifecycle: strings.TrimSpace(c.Query("status"))}
	items, total, err := h.svc.ListConfigs(ctx, filter)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Paginated(c, items, total, page, size)
}

func (h *UnifiedGatewayHandler) GetConfig(c *gin.Context) {
	ctx, _, ok := unifiedServiceContext(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	item, err := h.svc.GetConfig(ctx, c.Param("id"))
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Success(c, item)
}

func (h *UnifiedGatewayHandler) CreateDraft(c *gin.Context) {
	var req unifiedDocumentRequest
	if err := bindUnifiedJSON(c, &req); err != nil {
		response.ErrorWithDetails(c, http.StatusBadRequest, "invalid request body", "UNIFIED_GATEWAY_INVALID_JSON", nil)
		return
	}
	ctx, actor, ok := unifiedServiceContext(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	item, err := h.svc.CreateDraft(ctx, actor, c.GetHeader("Idempotency-Key"), req.Document)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Created(c, item)
}

func (h *UnifiedGatewayHandler) CreateDraftFromConfig(c *gin.Context) {
	ctx, actor, ok := unifiedServiceContext(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	item, err := h.svc.CreateDraftFromConfig(ctx, actor, c.GetHeader("Idempotency-Key"), c.Param("id"))
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Created(c, item)
}

func (h *UnifiedGatewayHandler) GetDraft(c *gin.Context) {
	ctx, _, ok := unifiedServiceContext(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	item, err := h.svc.GetDraft(ctx, c.Param("id"))
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Success(c, item)
}

func (h *UnifiedGatewayHandler) UpdateDraft(c *gin.Context) {
	var req unifiedDocumentRequest
	if err := bindUnifiedJSON(c, &req); err != nil {
		response.ErrorWithDetails(c, http.StatusBadRequest, "invalid request body", "UNIFIED_GATEWAY_INVALID_JSON", nil)
		return
	}
	revision, err := parseIfMatchHeader(c)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	ctx, actor, ok := unifiedServiceContext(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	item, err := h.svc.UpdateDraft(ctx, actor, c.GetHeader("Idempotency-Key"), c.Param("id"), revision, req.Document)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Success(c, item)
}

func (h *UnifiedGatewayHandler) ValidateDraft(c *gin.Context) {
	var req unifiedDocumentRequest
	var document *service.UnifiedGatewayConfig
	if c.Request.ContentLength != 0 {
		if err := bindUnifiedJSON(c, &req); err != nil && !errors.Is(err, io.EOF) {
			response.ErrorWithDetails(c, http.StatusBadRequest, "invalid request body", "UNIFIED_GATEWAY_INVALID_JSON", nil)
			return
		}
		if req.Document.PublicModel != "" {
			document = &req.Document
		}
	}
	expectedRevision, err := parseIfMatchHeader(c)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	ctx, _, ok := unifiedServiceContext(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	item, err := h.svc.ValidateDraftAtRevision(ctx, c.Param("id"), document, expectedRevision)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Success(c, item)
}

func (h *UnifiedGatewayHandler) PreviewDraft(c *gin.Context) {
	var req unifiedPreviewRequest
	if err := bindUnifiedJSON(c, &req); err != nil {
		response.ErrorWithDetails(c, http.StatusBadRequest, "invalid request body", "UNIFIED_GATEWAY_INVALID_JSON", nil)
		return
	}
	ctx, _, ok := unifiedServiceContext(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	document := req.Document
	if document == nil {
		draft, err := h.svc.GetDraft(ctx, c.Param("id"))
		if err != nil {
			writeUnifiedError(c, err)
			return
		}
		document = &draft.Document
	}
	item, err := h.svc.Preview(ctx, *document, req.Preview)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Success(c, item)
}

func (h *UnifiedGatewayHandler) PricingImportPreview(c *gin.Context) {
	var req unifiedPricingImportRequest
	if err := bindUnifiedJSON(c, &req); err != nil {
		response.ErrorWithDetails(c, http.StatusBadRequest, "invalid request body", "UNIFIED_GATEWAY_INVALID_JSON", nil)
		return
	}
	ctx, _, ok := unifiedServiceContext(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	document := req.Document
	if document == nil {
		draft, err := h.svc.GetDraft(ctx, c.Param("id"))
		if err != nil {
			writeUnifiedError(c, err)
			return
		}
		document = &draft.Document
	}
	request := service.UnifiedGatewayPricingImportRequest{LaneID: req.LaneID, SourceGroupID: req.SourceGroupID}
	item, err := h.svc.PricingImportPreview(ctx, *document, request)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Success(c, item)
}

func (h *UnifiedGatewayHandler) PricingImportApply(c *gin.Context) {
	var req unifiedPricingImportRequest
	if err := bindUnifiedJSON(c, &req); err != nil {
		response.ErrorWithDetails(c, http.StatusBadRequest, "invalid request body", "UNIFIED_GATEWAY_INVALID_JSON", nil)
		return
	}
	expectedRevision, err := parseIfMatchHeader(c)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	ctx, actor, ok := unifiedServiceContext(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	request := service.UnifiedGatewayPricingImportRequest{LaneID: req.LaneID, SourceGroupID: req.SourceGroupID, ImportDigest: req.ImportDigest}
	item, err := h.svc.PricingImportApply(ctx, actor, c.GetHeader("Idempotency-Key"), c.Param("id"), expectedRevision, request)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Success(c, item)
}

func (h *UnifiedGatewayHandler) PublishDraft(c *gin.Context) {
	var req unifiedPublishRequest
	if err := bindUnifiedJSON(c, &req); err != nil {
		response.ErrorWithDetails(c, http.StatusBadRequest, "invalid request body", "UNIFIED_GATEWAY_INVALID_JSON", nil)
		return
	}
	ctx, actor, ok := unifiedServiceContext(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	item, err := h.svc.PublishDraft(ctx, actor, c.GetHeader("Idempotency-Key"), c.Param("id"), c.GetHeader("If-Match"), req.Reason, req.ValidationToken)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Success(c, item)
}

func (h *UnifiedGatewayHandler) DisableConfig(c *gin.Context) {
	var req unifiedDisableRequest
	if err := bindUnifiedJSON(c, &req); err != nil {
		response.ErrorWithDetails(c, http.StatusBadRequest, "invalid request body", "UNIFIED_GATEWAY_INVALID_JSON", nil)
		return
	}
	ctx, actor, ok := unifiedServiceContext(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	item, err := h.svc.DisableConfig(ctx, actor, c.GetHeader("Idempotency-Key"), c.Param("id"), c.GetHeader("If-Match"), req.Reason)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Success(c, item)
}

func (h *UnifiedGatewayHandler) RestoreConfig(c *gin.Context) {
	var req unifiedRestoreRequest
	if err := bindUnifiedJSON(c, &req); err != nil {
		response.ErrorWithDetails(c, http.StatusBadRequest, "invalid request body", "UNIFIED_GATEWAY_INVALID_JSON", nil)
		return
	}
	revision, err := parseIfMatchHeader(c)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	sourceRevision, parseErr := strconv.ParseInt(strings.TrimSpace(c.Param("revision")), 10, 64)
	if parseErr != nil || sourceRevision <= 0 {
		writeUnifiedError(c, serviceError(http.StatusBadRequest, "UNIFIED_GATEWAY_REVISION_INVALID", "revision path parameter must be a positive integer"))
		return
	}
	ctx, actor, ok := unifiedServiceContext(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	item, err := h.svc.RestoreConfig(ctx, actor, c.GetHeader("Idempotency-Key"), c.Param("id"), sourceRevision, revision, req.Reason)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Success(c, item)
}

func (h *UnifiedGatewayHandler) ListRevisions(c *gin.Context) {
	ctx, _, ok := unifiedServiceContext(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	items, err := h.svc.ListRevisions(ctx, c.Param("id"))
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Success(c, items)
}

func unifiedServiceContext(c *gin.Context) (context.Context, string, bool) {
	if c == nil || c.Request == nil {
		return nil, "", false
	}
	actor, ok := unifiedActorID(c)
	if !ok {
		return nil, "", false
	}
	return service.WithUnifiedGatewayAdminActor(c.Request.Context(), actor), actor, true
}

func bindUnifiedJSON(c *gin.Context, target any) error {
	if c.Request == nil || c.Request.Body == nil {
		return io.EOF
	}
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, 2<<20))
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return io.EOF
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func unifiedActorID(c *gin.Context) (string, bool) {
	if c == nil {
		return "", false
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		return "", false
	}
	return strconv.FormatInt(subject.UserID, 10), true
}

func parseIfMatchHeader(c *gin.Context) (int64, error) {
	value := strings.Trim(strings.TrimSpace(c.GetHeader("If-Match")), `"`)
	if value == "" {
		return 0, serviceError(http.StatusBadRequest, "UNIFIED_GATEWAY_IF_MATCH_REQUIRED", "If-Match revision is required")
	}
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil || revision < 0 {
		return 0, serviceError(http.StatusBadRequest, "UNIFIED_GATEWAY_IF_MATCH_INVALID", "If-Match must contain a non-negative revision")
	}
	return revision, nil
}

func serviceError(status int, reason, message string) error {
	return &service.UnifiedGatewayAdminError{Status: status, Reason: reason, Message: message}
}

func writeUnifiedError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	var adminErr *service.UnifiedGatewayAdminError
	if errors.As(err, &adminErr) && adminErr != nil {
		response.ErrorWithDetails(c, adminErr.Status, adminErr.Message, adminErr.Reason, adminErr.Metadata)
		return
	}
	switch {
	case errors.Is(err, service.ErrUnifiedGatewayAdminNotFound):
		response.ErrorWithDetails(c, http.StatusNotFound, "unified gateway resource not found", "UNIFIED_GATEWAY_NOT_FOUND", nil)
	case errors.Is(err, service.ErrUnifiedGatewayAdminVersion):
		response.ErrorWithDetails(c, http.StatusConflict, "resource revision does not match If-Match", "UNIFIED_GATEWAY_VERSION_CONFLICT", nil)
	default:
		response.ErrorWithDetails(c, http.StatusInternalServerError, "internal error", "UNIFIED_GATEWAY_INTERNAL_ERROR", nil)
	}
}
