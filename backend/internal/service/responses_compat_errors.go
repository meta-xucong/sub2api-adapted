package service

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// responsesCompatError separates a safe wire error from internal cache/parser
// details. Cause is retained for errors.Is/As, never rendered to the client.
type responsesCompatError struct {
	status  int
	code    string
	message string
	param   string
	cause   error
}

func (e *responsesCompatError) Error() string { return e.message }
func (e *responsesCompatError) Unwrap() error { return e.cause }

func writeResponsesCompatError(c *gin.Context, err error) {
	wire := &responsesCompatError{status: http.StatusBadRequest, code: "invalid_request_error", message: "Invalid compatibility continuation input", param: "input"}
	var classified *responsesCompatError
	if errors.As(err, &classified) {
		wire = classified
	}
	kind := "invalid_request_error"
	if wire.status >= 500 {
		kind = "server_error"
	}
	// A previously committed compact keepalive cannot be followed by JSON.
	if StopOpenAICompactSSEKeepaliveCommitted(c) {
		writeOpenAICompactSSEFailureMessage(c, wire.status, wire.code, wire.message)
		return
	}
	MarkResponseCommitted(c)
	c.JSON(wire.status, gin.H{"error": gin.H{
		"type": kind, "code": wire.code, "message": wire.message, "param": wire.param,
	}})
}

// The legacy compact endpoint is unary. The existing marked body-signal
// bridge is different: it retains the original Responses client's SSE intent.
func validateResponsesCompatCompactStream(c *gin.Context, stream bool) error {
	if stream && !openAICompactClientWantsStream(c) {
		return &responsesCompatError{status: http.StatusBadRequest, code: "unsupported_parameter", message: "The legacy /responses/compact endpoint requires stream=false or an omitted stream field", param: "stream"}
	}
	return nil
}
