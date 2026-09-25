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

// Preserve downstream streaming intent independently of the upstream summary
// transport. A handler may already have removed stream after setting the marker.
func markResponsesCompatCompactStream(c *gin.Context, stream bool) {
	if stream {
		MarkOpenAICompactClientStream(c)
	}
}
