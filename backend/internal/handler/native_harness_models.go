package handler

import (
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/nativeharness"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// NativeHarnessModels is a read-only configuration preflight, not a model probe.
// It authenticates as the completion route but never creates an operation or
// contacts the Facade, Bridge, local computer or native account.
func (h *OpenAIGatewayHandler) NativeHarnessModels(c *gin.Context) {
	key, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || key == nil {
		nativeHarnessError(c, 401, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID != key.UserID {
		nativeHarnessError(c, 403, "NATIVE_FORBIDDEN", "Native channel unavailable")
		return
	}
	if h.nativeHarness == nil {
		nativeHarnessError(c, 503, "NATIVE_DISABLED", "Native channel unavailable")
		return
	}
	canExecute := key.IsActive() && !key.IsExpired() && !key.IsQuotaExhausted()
	result, err := h.nativeHarness.ConfiguredModels(subject.UserID, key.ID, canExecute)
	if err != nil {
		var e *nativeharness.Error
		if errors.As(err, &e) {
			nativeHarnessError(c, e.Status, e.Code, "Native channel unavailable")
		} else {
			nativeHarnessError(c, 503, "NATIVE_UNAVAILABLE", "Native channel unavailable")
		}
		return
	}
	if key.Group != nil && key.Group.ModelAllowlistEnabled() {
		visible := make([]nativeharness.ModelInfo, 0, len(result.Data))
		for _, model := range result.Data {
			if key.Group.ModelAllowlist.Allows(model.ID) {
				visible = append(visible, model)
			}
		}
		result.Data = visible
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Native-Billing", "disabled-read-only")
	c.JSON(200, result)
}
