package handler

import (
	"errors"
	"io"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/nativeharness"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// NativeHarnessChat is deliberately separate from ordinary account dispatch.
// No request reaches provider selection, retries, balance reservation or billing.
func (h *OpenAIGatewayHandler) NativeHarnessChat(c *gin.Context) {
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
	// Duplicated keys are ambiguous; never silently choose one.
	keys := c.Request.Header.Values("Idempotency-Key")
	if len(keys) != 1 {
		nativeHarnessError(c, 400, "IDEMPOTENCY_KEY_REQUIRED", "One Idempotency-Key is required")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, nativeharness.MaxRequest+1))
	if err != nil || len(raw) > nativeharness.MaxRequest {
		nativeHarnessError(c, http.StatusRequestEntityTooLarge, "INVALID_REQUEST", "Invalid native request")
		return
	}
	bufferedStream := false
	if c.Request.URL.Path == nativeharness.StandardChatPath && h.nativeHarness.Config.StandardAPI {
		var prepareErr error
		raw, bufferedStream, prepareErr = nativeharness.PrepareBufferedStream(raw)
		if prepareErr != nil {
			var e *nativeharness.Error
			if errors.As(prepareErr, &e) {
				nativeHarnessError(c, e.Status, e.Code, "Unsupported native request")
			} else {
				nativeHarnessError(c, 400, "INVALID_REQUEST", "Invalid native request")
			}
			return
		}
	}
	// Expired/quota-exhausted keys may read an already accepted result, but cannot
	// cause new execution. Disabled keys/users remain rejected by auth middleware.
	canExecute := key.IsActive() && !key.IsExpired() && !key.IsQuotaExhausted()
	if key.Group != nil && key.Group.ModelAllowlistEnabled() {
		model, err := nativeharness.RequestModel(raw)
		canExecute = canExecute && err == nil && key.Group.ModelAllowlist.Allows(model)
	}
	var result *nativeharness.Response
	if c.Request.URL.Path == nativeharness.StandardChatPath && h.nativeHarness.Config.StandardAPI {
		result, err = h.nativeHarness.InvokeStandard(c.Request.Context(), subject.UserID, key.ID, keys[0], canExecute, raw)
	} else {
		result, err = h.nativeHarness.Invoke(c.Request.Context(), subject.UserID, key.ID, keys[0], canExecute, raw)
	}
	if err != nil {
		var e *nativeharness.Error
		if errors.As(err, &e) {
			nativeHarnessError(c, e.Status, e.Code, "Native request could not be completed")
		} else {
			nativeHarnessError(c, 503, "NATIVE_UNAVAILABLE", "Native channel unavailable")
		}
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Native-Usage-State", "unknown")
	c.Header("X-Native-Billing", result.Billing)
	if result.Replay == "true" || result.Replay == "false" {
		c.Header("X-Native-Replay", result.Replay)
	}
	if bufferedStream {
		data, encodeErr := nativeharness.BufferedCompletionSSE(result.Body)
		if encodeErr != nil {
			nativeHarnessError(c, 502, "NATIVE_INVALID_COMPLETION", "Native request could not be completed")
			return
		}
		c.Header("X-Native-Stream-Mode", "buffered-final")
		c.Data(result.Status, "text/event-stream; charset=utf-8", data)
		return
	}
	c.Data(result.Status, "application/json", result.Body)
}

func nativeHarnessError(c *gin.Context, status int, code, message string) {
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{"error": gin.H{"code": code, "type": "native_channel_error", "message": message}})
}
