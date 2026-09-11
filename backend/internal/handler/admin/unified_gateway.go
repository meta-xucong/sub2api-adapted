package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// UnifiedGatewayHandler exposes only the aggregate configuration plane.  It
// is not the /v1 runtime handler and cannot turn the runtime gate on.
type UnifiedGatewayHandler struct {
	svc *service.UnifiedGatewayAdminService
}

func NewUnifiedGatewayHandler(svc *service.UnifiedGatewayAdminService) *UnifiedGatewayHandler {
	return &UnifiedGatewayHandler{svc: svc}
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

func (h *UnifiedGatewayHandler) Meta(c *gin.Context) {
	if h == nil || h.svc == nil {
		response.ErrorWithDetails(c, http.StatusInternalServerError, "internal error", "UNIFIED_GATEWAY_REPOSITORY_UNAVAILABLE", nil)
		return
	}
	response.Success(c, h.svc.Meta(c.Request.Context()))
}

func (h *UnifiedGatewayHandler) ListOptions(c *gin.Context) {
	page, size := response.ParsePagination(c)
	result, total, err := h.svc.Options(c.Request.Context(), page, size)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Success(c, gin.H{"items": result, "total": total, "page": page, "page_size": size})
}

func (h *UnifiedGatewayHandler) ListConfigs(c *gin.Context) {
	page, size := response.ParsePagination(c)
	items, total, err := h.svc.ListConfigs(c.Request.Context(), service.UnifiedGatewayConfigListFilter{Page: page, PageSize: size, Lifecycle: strings.TrimSpace(c.Query("status"))})
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Paginated(c, items, total, page, size)
}

func (h *UnifiedGatewayHandler) GetConfig(c *gin.Context) {
	item, err := h.svc.GetConfig(c.Request.Context(), c.Param("id"))
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
	actor, ok := unifiedActorID(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	item, err := h.svc.CreateDraft(c.Request.Context(), actor, c.GetHeader("Idempotency-Key"), req.Document)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Created(c, item)
}

func (h *UnifiedGatewayHandler) CreateDraftFromConfig(c *gin.Context) {
	actor, ok := unifiedActorID(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	item, err := h.svc.CreateDraftFromConfig(c.Request.Context(), actor, c.GetHeader("Idempotency-Key"), c.Param("id"))
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Created(c, item)
}

func (h *UnifiedGatewayHandler) GetDraft(c *gin.Context) {
	item, err := h.svc.GetDraft(c.Request.Context(), c.Param("id"))
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
	actor, ok := unifiedActorID(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	item, err := h.svc.UpdateDraft(c.Request.Context(), actor, c.GetHeader("Idempotency-Key"), c.Param("id"), revision, req.Document)
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
	item, err := h.svc.ValidateDraftAtRevision(c.Request.Context(), c.Param("id"), document, expectedRevision)
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
	document := req.Document
	if document == nil {
		draft, err := h.svc.GetDraft(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeUnifiedError(c, err)
			return
		}
		document = &draft.Document
	}
	item, err := h.svc.Preview(c.Request.Context(), *document, req.Preview)
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
	document := req.Document
	if document == nil {
		draft, err := h.svc.GetDraft(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeUnifiedError(c, err)
			return
		}
		document = &draft.Document
	}
	item, err := h.svc.PricingImportPreview(c.Request.Context(), *document, service.UnifiedGatewayPricingImportRequest{LaneID: req.LaneID, SourceGroupID: req.SourceGroupID})
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
	actor, ok := unifiedActorID(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	expectedRevision, err := parseIfMatchHeader(c)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	item, err := h.svc.PricingImportApply(c.Request.Context(), actor, c.GetHeader("Idempotency-Key"), c.Param("id"), expectedRevision, service.UnifiedGatewayPricingImportRequest{LaneID: req.LaneID, SourceGroupID: req.SourceGroupID, ImportDigest: req.ImportDigest})
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
	actor, ok := unifiedActorID(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	revision := c.GetHeader("If-Match")
	item, err := h.svc.PublishDraft(c.Request.Context(), actor, c.GetHeader("Idempotency-Key"), c.Param("id"), revision, req.Reason, req.ValidationToken)
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
	actor, ok := unifiedActorID(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	item, err := h.svc.DisableConfig(c.Request.Context(), actor, c.GetHeader("Idempotency-Key"), c.Param("id"), c.GetHeader("If-Match"), req.Reason)
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
	actor, ok := unifiedActorID(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
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
	item, err := h.svc.RestoreConfig(c.Request.Context(), actor, c.GetHeader("Idempotency-Key"), c.Param("id"), sourceRevision, revision, req.Reason)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Success(c, item)
}

func (h *UnifiedGatewayHandler) ListRevisions(c *gin.Context) {
	items, err := h.svc.ListRevisions(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Success(c, items)
}

func (h *UnifiedGatewayHandler) ListSnapshots(c *gin.Context) {
	page, size := response.ParsePagination(c)
	filter := service.UnifiedGatewaySnapshotFilter{Page: page, PageSize: size, AccessGroupID: c.Query("access_group_id"), PublicModel: c.Query("public_model"), Status: c.Query("status")}
	if value := c.Query("from"); value != "" {
		filter.From, _ = time.Parse(time.RFC3339, value)
	}
	if value := c.Query("to"); value != "" {
		filter.To, _ = time.Parse(time.RFC3339, value)
	}
	items, total, err := h.svc.ListSnapshots(c.Request.Context(), filter)
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Paginated(c, items, total, page, size)
}

func (h *UnifiedGatewayHandler) ProbeBinding(c *gin.Context) {
	actor, ok := unifiedActorID(c)
	if !ok {
		response.Unauthorized(c, "Authorization required")
		return
	}
	item, err := h.svc.ProbeBinding(c.Request.Context(), actor, c.GetHeader("Idempotency-Key"), c.Param("binding_id"))
	if err != nil {
		writeUnifiedError(c, err)
		return
	}
	response.Success(c, item)
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
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		return "", false
	}
	return strconv.FormatInt(subject.UserID, 10), true
}
func parseIfMatchHeader(c *gin.Context) (int64, error) {
	value := strings.Trim(strings.TrimSpace(c.GetHeader("If-Match")), "\"")
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
