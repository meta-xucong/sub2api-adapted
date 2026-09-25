package handler

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const responsesRequestRetention = 24 * time.Hour
const responsesReplayMaxBytes = 8 << 20

type responsesRequestRecord struct {
	Version     int               `json:"version"`
	Fingerprint string            `json:"fingerprint"`
	Owner       string            `json:"owner,omitempty"`
	State       string            `json:"state"`
	Status      int               `json:"status,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        []byte            `json:"body,omitempty"`
}

// Authentication is still performed by the normal route middleware. Unkeyed
// clients retain their original behavior; payload similarity is never a key.
// enterResponsesRequest wraps exactly one invocation of the public handler.
// The request-local marker makes the callback enter the original security,
// billing and forwarding body without re-entering this envelope.
func enterResponsesRequest(c *gin.Context, store service.ResponsesIdempotencyStore, cfg *config.Config, next gin.HandlerFunc) bool {
	if entered, _ := c.Get("responses_idempotency_entered"); entered == true {
		return false
	}
	c.Set("responses_idempotency_entered", true)
	c.Writer = &responsesErrorFieldsWriter{c.Writer}
	runResponsesIdempotently(c, store, cfg, next)
	return true
}

func responsesRequestError(c *gin.Context, status int, code, message string) {
	kind := "invalid_request_error"
	if status >= 500 {
		kind = "server_error"
	}
	c.JSON(status, gin.H{"error": gin.H{"type": kind, "code": code, "message": message}})
}

func runResponsesIdempotently(c *gin.Context, store service.ResponsesIdempotencyStore, cfg *config.Config, next gin.HandlerFunc) {
	keys := c.Request.Header.Values("Idempotency-Key")
	if len(keys) == 0 {
		next(c)
		return
	}
	key, err := service.NormalizeIdempotencyKey(c.GetHeader("Idempotency-Key"))
	if err != nil || len(keys) != 1 || key == "" {
		responsesRequestError(c, 400, "invalid_idempotency_key", "Supply exactly one non-empty printable Idempotency-Key of at most 128 bytes")
		return
	}
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	subject, subjectOK := middleware.GetAuthSubjectFromContext(c)
	if !ok || apiKey == nil || apiKey.ID <= 0 || !subjectOK || subject.UserID <= 0 {
		responsesRequestError(c, 401, "authentication_error", "Authenticated user and API key are required")
		return
	}
	groupID := int64(0)
	if apiKey.GroupID != nil {
		groupID = *apiKey.GroupID
	}
	body, err := readLenientJSONRequestBodyWithPrealloc(c.Request, cfg)
	if err != nil {
		status := 400
		if _, ok := extractMaxBytesError(err); ok {
			status = 413
		}
		responsesRequestError(c, status, "invalid_request_error", "Failed to read Responses request")
		return
	}
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		responsesRequestError(c, 400, "invalid_request_error", "Request must be a JSON object")
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	if store == nil {
		responsesRequestError(c, 503, "idempotency_store_unavailable", "Shared Responses request storage is unavailable")
		return
	}
	route := "responses"
	if service.IsOpenAIResponsesCompactPathForTest(c) {
		route = "responses/compact"
	}
	scope := fmt.Sprintf("user:%d:key:%d:group:%d:%s", subject.UserID, apiKey.ID, groupID, route)
	// Compact JSON whitespace, but keep number precision, field order and
	// duplicate-key bytes. A changed representation conflicts conservatively.
	var compact bytes.Buffer
	if err := json.Compact(&compact, body); err != nil {
		responsesRequestError(c, 400, "invalid_request_error", "Invalid JSON request")
		return
	}
	fingerprintInput := scope + "\n" + compact.String() + "\n" + c.GetHeader("OpenAI-Beta") + "\n" + c.GetHeader("x-codex-beta-features")
	fingerprint := sha256.Sum256([]byte(fingerprintInput))
	digest := sha256.Sum256([]byte(scope + "\x00" + key))
	storageKey := hex.EncodeToString(digest[:])
	record := responsesRequestRecord{Version: 1, Fingerprint: hex.EncodeToString(fingerprint[:]), Owner: uuid.NewString(), State: "processing"}
	claim, _ := json.Marshal(record)
	cacheCtx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	existing, acquired, err := store.ClaimResponsesRequest(cacheCtx, storageKey, claim, responsesRequestRetention)
	cancel()
	if err != nil {
		responsesRequestError(c, 503, "idempotency_store_unavailable", "Shared Responses request storage is unavailable")
		return
	}
	if !acquired {
		var saved responsesRequestRecord
		if json.Unmarshal(existing, &saved) != nil || saved.Version != 1 || saved.Fingerprint == "" {
			responsesRequestError(c, 503, "idempotency_state_invalid", "Stored Responses request state is invalid")
			return
		}
		if saved.Fingerprint != record.Fingerprint {
			responsesRequestError(c, 409, "idempotency_key_conflict", "This Idempotency-Key was already used for a different request")
			return
		}
		switch saved.State {
		case "processing":
			c.Header("Retry-After", "2")
			responsesRequestError(c, 409, "idempotency_in_progress", "The original request is still processing or awaiting recovery; do not resubmit it with a new key")
		case "done":
			if len(saved.Body) > responsesReplayMaxBytes || !responsesReplayComplete(saved.Status, saved.Headers["Content-Type"], saved.Body) {
				responsesRequestError(c, 503, "idempotency_state_invalid", "Stored Responses result is invalid")
				return
			}
			for _, name := range responsesReplayHeaderNames {
				if v := saved.Headers[name]; v != "" {
					c.Header(name, v)
				}
			}
			c.Header("Idempotency-Replayed", "true")
			c.Data(saved.Status, saved.Headers["Content-Type"], saved.Body)
		default:
			responsesRequestError(c, 409, "idempotency_outcome_unknown", "The original request outcome cannot be safely replayed; automatic re-execution is blocked")
		}
		return
	}
	c.Header("Idempotency-Replayed", "false")
	writer := &responsesReplayWriter{ResponseWriter: c.Writer}
	c.Writer = writer
	execCtx, execCancel := service.WithResponsesRequestExecution(c.Request.Context())
	defer execCancel()
	c.Request = c.Request.WithContext(execCtx)
	returned := false
	// Finish after all handler defers (including heartbeat shutdown). An
	// interrupted process leaves processing state, never an automatic reclaim.
	defer func() {
		data, overflow := writer.snapshot()
		record.Owner = ""
		record.State = "unknown"
		if returned && !overflow && responsesReplayComplete(c.Writer.Status(), c.Writer.Header().Get("Content-Type"), data) {
			record.State = "done"
			record.Status = c.Writer.Status()
			record.Body = data
			record.Headers = make(map[string]string)
			for _, name := range responsesReplayHeaderNames {
				if v := c.Writer.Header().Get(name); v != "" {
					record.Headers[name] = v
				}
			}
		}
		result, _ := json.Marshal(record)
		persistCtx, persistCancel := context.WithTimeout(context.Background(), 3*time.Second)
		completed, finishErr := store.CompleteResponsesRequest(persistCtx, storageKey, claim, result, responsesRequestRetention)
		persistCancel()
		if finishErr != nil || !completed {
			logger.L().Warn("Responses request replay persistence unavailable", zap.Bool("stored", completed), zap.Bool("store_error", finishErr != nil))
		}
	}()
	next(c)
	returned = true
}

var responsesReplayHeaderNames = []string{"Content-Type", "Cache-Control", "X-Accel-Buffering", "X-Request-Id", "Retry-After"}

type responsesReplayWriter struct {
	gin.ResponseWriter
	mu           sync.Mutex
	buf          bytes.Buffer
	overflow     bool
	disconnected bool
}

func (w *responsesReplayWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.overflow {
		if w.buf.Len()+len(p) > responsesReplayMaxBytes {
			w.overflow = true
			w.buf.Reset()
		} else {
			_, _ = w.buf.Write(p)
		}
	}
	if !w.disconnected {
		n, err := w.ResponseWriter.Write(p)
		if err != nil || n != len(p) {
			w.disconnected = true
		}
	}
	// Downstream failure must not prevent the accepted handler from finishing
	// its response state and one billing dispatch. No work is forked here.
	return len(p), nil
}
func (w *responsesReplayWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *responsesReplayWriter) snapshot() ([]byte, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buf.Bytes()...), w.overflow
}

func responsesReplayComplete(status int, contentType string, body []byte) bool {
	if status < 200 || status >= 600 || len(body) == 0 {
		return false
	}
	if strings.HasPrefix(contentType, "application/json") {
		if !gjson.ValidBytes(body) {
			return false
		}
		root := gjson.ParseBytes(body)
		if !root.IsObject() {
			return false
		}
		if status >= 400 {
			return root.Get("error").IsObject()
		}
		return root.Get("id").String() != "" && root.Get("output").IsArray() && (root.Get("status").String() == "completed" || root.Get("status").String() == "incomplete" || root.Get("status").String() == "failed")
	}
	if !strings.HasPrefix(contentType, "text/event-stream") {
		return false
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), responsesReplayMaxBytes)
	terminal := false
	id := ""
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			continue
		}
		if terminal || !gjson.Valid(data) {
			return false
		}
		event := gjson.Parse(data)
		if event.Get("type").String() == "response.created" {
			id = event.Get("response.id").String()
		}
		switch event.Get("type").String() {
		case "response.completed", "response.incomplete", "response.failed":
			got := event.Get("response.id").String()
			if got == "" || event.Get("response.id").Type != gjson.String || !event.Get("response.output").IsArray() || event.Get("response.status").String() != strings.TrimPrefix(event.Get("type").String(), "response.") || (id != "" && got != id) {
				return false
			}
			terminal = true
		}
	}
	return terminal && scanner.Err() == nil
}
