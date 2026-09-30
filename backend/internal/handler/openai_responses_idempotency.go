package handler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Responses wraps the public Responses handler with opt-in request replay.
// Idempotency-Key is intentionally opt-in so existing clients without the
// header retain the official behavior. The inner handler still writes the
// first response immediately; the writer below tees it into the durable
// idempotency coordinator for a later retry.
func (h *OpenAIGatewayHandler) ResponsesWithIdempotency(c *gin.Context) {
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	coordinator := service.DefaultIdempotencyCoordinator()
	if key == "" || coordinator == nil {
		h.Responses(c)
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	originalWriter := c.Writer
	capture := &responsesIdempotencyWriter{ResponseWriter: originalWriter}
	c.Writer = capture

	result, executeErr := coordinator.Execute(c.Request.Context(), service.IdempotencyExecuteOptions{
		Scope:          "openai.responses",
		ActorScope:     openAIResponsesIdempotencyActorScope(c),
		Method:         c.Request.Method,
		Route:          c.FullPath(),
		IdempotencyKey: key,
		Payload:        json.RawMessage(body),
		RequireKey:     true,
		TTL:            service.DefaultWriteIdempotencyTTL(),
	}, func(context.Context) (any, error) {
		h.Responses(c)
		return capture.snapshot(), nil
	})
	c.Writer = originalWriter

	if executeErr != nil {
		// The first request may already have been delivered when persistence
		// fails (for example because the response exceeds the configured store
		// limit). Never replace that valid response with a second error body.
		if capture.Written() {
			return
		}
		h.writeResponsesIdempotencyError(c, executeErr)
		return
	}
	if result == nil || !result.Replayed {
		return
	}

	replayed, ok := decodeResponsesIdempotencyReplay(result.Data)
	if !ok {
		h.errorResponse(c, http.StatusServiceUnavailable, "idempotency_error", "Stored idempotent response is invalid")
		return
	}
	c.Header("X-Idempotency-Replayed", "true")
	if replayed.BodyOmitted {
		h.errorResponse(c, http.StatusServiceUnavailable, "idempotency_response_too_large", "The original response exceeded the replay limit; it was not executed again")
		return
	}
	writeResponsesIdempotencyReplay(c.Writer, replayed)
}

type responsesIdempotencyReplay struct {
	Status      int               `json:"status"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        string            `json:"body,omitempty"`
	BodyOmitted bool              `json:"body_omitted,omitempty"`
}

const responsesIdempotencyReplayMaxBytes = 8 << 20

// MarshalIdempotencyStoredResponse bypasses generic small-response redaction
// and truncation so JSON/SSE is replayed byte-for-byte. Large responses persist
// a terminal marker, preventing a retry from repeating billable work.
func (r responsesIdempotencyReplay) MarshalIdempotencyStoredResponse() (string, error) {
	if len(r.Body) > responsesIdempotencyReplayMaxBytes {
		r.Body = ""
		r.BodyOmitted = true
	}
	raw, err := json.Marshal(r)
	return string(raw), err
}

type responsesIdempotencyWriter struct {
	gin.ResponseWriter
	body        bytes.Buffer
	status      int
	wroteHeader bool
}

func (w *responsesIdempotencyWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.status = code
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *responsesIdempotencyWriter) WriteHeaderNow() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
}

func (w *responsesIdempotencyWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	w.body.Write(data)
	return w.ResponseWriter.Write(data)
}

func (w *responsesIdempotencyWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func (w *responsesIdempotencyWriter) Flush() {
	w.WriteHeaderNow()
	w.ResponseWriter.Flush()
}

func (w *responsesIdempotencyWriter) snapshot() responsesIdempotencyReplay {
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	headers := make(map[string]string)
	for _, key := range []string{"Content-Type", "Cache-Control", "Connection", "X-Accel-Buffering"} {
		if value := w.Header().Get(key); value != "" {
			headers[key] = value
		}
	}
	return responsesIdempotencyReplay{Status: status, Headers: headers, Body: w.body.String()}
}

func (w *responsesIdempotencyWriter) Written() bool {
	return w.wroteHeader || w.body.Len() > 0
}

func openAIResponsesIdempotencyActorScope(c *gin.Context) string {
	if subject, ok := middleware2.GetAuthSubjectFromContext(c); ok {
		return fmt.Sprintf("user:%d", subject.UserID)
	}
	return "user:0"
}

func decodeResponsesIdempotencyReplay(data any) (responsesIdempotencyReplay, bool) {
	if typed, ok := data.(responsesIdempotencyReplay); ok {
		return typed, true
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return responsesIdempotencyReplay{}, false
	}
	var replay responsesIdempotencyReplay
	if err := json.Unmarshal(raw, &replay); err != nil || replay.Status <= 0 {
		return responsesIdempotencyReplay{}, false
	}
	return replay, true
}

func writeResponsesIdempotencyReplay(w gin.ResponseWriter, replay responsesIdempotencyReplay) {
	for key, value := range replay.Headers {
		w.Header().Set(key, value)
	}
	if replay.Status <= 0 {
		replay.Status = http.StatusOK
	}
	w.WriteHeader(replay.Status)
	_, _ = w.Write([]byte(replay.Body))
}

func (h *OpenAIGatewayHandler) writeResponsesIdempotencyError(c *gin.Context, err error) {
	status := http.StatusConflict
	if infraerrors.Code(err) == infraerrors.Code(service.ErrIdempotencyStoreUnavail) {
		status = http.StatusServiceUnavailable
	}
	h.errorResponse(c, status, "idempotency_error", err.Error())
}

// Keep the optional interfaces of gin.ResponseWriter available to handlers
// that inspect them through type assertions while the wrapper is installed.
var _ http.ResponseWriter = (*responsesIdempotencyWriter)(nil)
var _ http.Flusher = (*responsesIdempotencyWriter)(nil)
var _ http.CloseNotifier = (*responsesIdempotencyWriter)(nil)
var _ http.Hijacker = (*responsesIdempotencyWriter)(nil)
var _ http.Pusher = (*responsesIdempotencyWriter)(nil)

func (w *responsesIdempotencyWriter) CloseNotify() <-chan bool { return w.ResponseWriter.CloseNotify() }
func (w *responsesIdempotencyWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.Hijack()
}
func (w *responsesIdempotencyWriter) Push(target string, opts *http.PushOptions) error {
	return w.ResponseWriter.Pusher().Push(target, opts)
}
