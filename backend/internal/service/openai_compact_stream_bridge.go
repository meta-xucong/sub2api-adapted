package service

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// openAICompactClientStreamKey preserves the original streaming intent for
// explicit /responses/compact and legacy promoted requests. Upstream compact
// or portable summary calls may stay unary while the client receives SSE.
const openAICompactClientStreamKey = "openai_compact_client_stream"

// MarkOpenAICompactClientStream records client intent before normalization or
// protocol adaptation removes the upstream stream field.
func MarkOpenAICompactClientStream(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(openAICompactClientStreamKey, true)
}

func OpenAICompactClientStreamKeyForTest() string {
	return openAICompactClientStreamKey
}

// OpenAICompactClientWantsStream returns the original client mode saved before
// compact request normalization. It does not alter the upstream transport.
func OpenAICompactClientWantsStream(c *gin.Context) bool { return openAICompactClientWantsStream(c) }

func openAICompactClientWantsStream(c *gin.Context) bool {
	if c == nil {
		return false
	}
	value, ok := c.Get(openAICompactClientStreamKey)
	if !ok {
		return false
	}
	wants, _ := value.(bool)
	return wants
}

// writeOpenAICompactSSEBridge emits a complete ordered Responses lifecycle
// for a marked compact request. It does not stream the provider's partial
// summary text: compaction items are delivered only after the summary succeeds.
// An unmarked request retains the existing JSON response path.
//
// 若下游心跳已把响应头提交为 200（见 openAICompactSSEKeepalive），则本函数
// 必须接管一切写回：非 2xx 或不可合成的响应降级为 response.failed 终止事件，
// 不能再返回 false（否则调用方的 JSON 写回会与已提交的 SSE 流交错）。
func writeOpenAICompactSSEBridge(c *gin.Context, statusCode int, finalResponse []byte) bool {
	if c == nil || !openAICompactClientWantsStream(c) {
		return false
	}
	// 先停心跳再写回，避免注释行与最终事件交错；停止后经互斥锁与心跳
	// goroutine 建立 happens-before，可安全接管 ResponseWriter。
	committed := StopOpenAICompactSSEKeepaliveCommitted(c)
	if statusCode < 200 || statusCode >= 300 {
		if committed {
			writeOpenAICompactSSEFailure(c, statusCode, finalResponse)
			return true
		}
		return false
	}
	payload, ok := buildOpenAICompactSSEPayload(finalResponse)
	if !ok {
		if committed {
			writeOpenAICompactSSEFailure(c, http.StatusBadGateway, finalResponse)
			return true
		}
		MarkResponseCommitted(c)
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "code": "invalid_compaction_response", "message": "Upstream did not return a completed compaction result"}})
		return true
	}
	if !committed {
		header := c.Writer.Header()
		header.Set("Content-Type", "text/event-stream")
		header.Set("Cache-Control", "no-cache")
		header.Set("Connection", "keep-alive")
		header.Set("X-Accel-Buffering", "no")
		c.Writer.WriteHeader(statusCode)
	}
	_, _ = c.Writer.Write(payload)
	c.Writer.Flush()
	return true
}

// writeOpenAICompactSSEFailure 从上游错误 body 提取错误消息后，以
// response.failed 终止事件回传。仅用于心跳已提交 200、无法再按 HTTP 状态码
// 回传错误的场景。
func writeOpenAICompactSSEFailure(c *gin.Context, statusCode int, errorBody []byte) {
	message := ""
	if len(errorBody) > 0 {
		message = sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(errorBody)))
	}
	if message == "" {
		message = "Upstream compact request failed with HTTP " + strconv.Itoa(statusCode)
	}
	writeOpenAICompactSSEFailureMessage(c, statusCode, "upstream_error", message)
}

// writeOpenAICompactSSEFailureMessage 写出 response.failed 终止事件。Codex 对
// 流式 Responses 请求把 response.failed 作为合法终止事件处理（普通 error 帧
// 不被识别，会退化为 "stream closed before response.completed" 盲重连）。
// 同时标记流内错误，保证挂在 200 流上的失败仍进入 ops 错误看板。
func writeOpenAICompactSSEFailureMessage(c *gin.Context, statusCode int, errType, message string) {
	if c == nil {
		return
	}
	MarkOpsStreamError(c, errType, message, statusCode)
	payload, err := json.Marshal(map[string]any{
		"type": "response.failed",
		"response": map[string]any{
			"id":     "resp_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			"object": "response",
			// 严格客户端把 created_at 当必填字段，缺失会反序列化失败，
			// 终止事件就白发了（退化成盲重连）。与 writeResponsesFailedSSE 对齐。
			"created_at": time.Now().Unix(),
			"status":     "failed",
			"output":     []any{},
			"error": map[string]any{
				"code":    errType,
				"message": message,
			},
		},
	})
	if err != nil {
		return
	}
	_, _ = c.Writer.Write([]byte("event: response.failed\ndata: "))
	_, _ = c.Writer.Write(payload)
	_, _ = c.Writer.Write([]byte("\n\n"))
	c.Writer.Flush()
}

// buildOpenAICompactSSEPayload emits created/in_progress, each item's added/done
// pair, and completed with continuous sequence_number values. The final response
// retains its original item fields; missing required lifecycle metadata is filled.
func buildOpenAICompactSSEPayload(finalResponse []byte) ([]byte, bool) {
	if len(finalResponse) == 0 || !gjson.ValidBytes(finalResponse) {
		return nil, false
	}
	if !gjson.ParseBytes(finalResponse).IsObject() {
		return nil, false
	}
	parsed := gjson.ParseBytes(finalResponse)
	if parsed.Get("error").Exists() && parsed.Get("error").Type != gjson.Null {
		return nil, false
	}
	if status := parsed.Get("status"); status.Exists() && status.String() != "completed" {
		return nil, false
	}
	found := false
	for _, item := range parsed.Get("output").Array() {
		if (item.Get("type").String() == "compaction" || item.Get("type").String() == "compaction_summary") && item.Get("encrypted_content").Type == gjson.String && strings.TrimSpace(item.Get("encrypted_content").String()) != "" {
			found = true
		}
	}
	if !found {
		return nil, false
	}
	// SSE 的 data 行不允许出现裸换行：上游 JSON 可能是 pretty-printed 形态，
	// 嵌入前必须压缩为单行。
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, finalResponse); err != nil {
		return nil, false
	}
	response := compacted.Bytes()
	root := gjson.ParseBytes(response)
	if strings.TrimSpace(root.Get("id").String()) == "" {
		next, err := sjson.SetBytes(response, "id", "resp_"+strings.ReplaceAll(uuid.NewString(), "-", ""))
		if err != nil {
			return nil, false
		}
		response = next
	}
	if usage := gjson.GetBytes(response, "usage"); usage.Exists() && !openAICompactUsageParsableByCodex(usage) {
		next, err := sjson.DeleteBytes(response, "usage")
		if err != nil {
			return nil, false
		}
		response = next
	}

	// Both the explicit compact API and the legacy marked path expose a full
	// Responses lifecycle. Keep upstream-only fields in the final response.
	defaults := map[string]any{"object": "response", "status": "completed", "created_at": time.Now().Unix()}
	for field, value := range defaults {
		if !gjson.GetBytes(response, field).Exists() {
			next, err := sjson.SetBytes(response, field, value)
			if err != nil {
				return nil, false
			}
			response = next
		}
	}
	initial := append([]byte(nil), response...)
	var err error
	initial, err = sjson.SetBytes(initial, "status", "in_progress")
	if err != nil {
		return nil, false
	}
	initial, err = sjson.SetBytes(initial, "output", []any{})
	if err != nil {
		return nil, false
	}
	initial, err = sjson.DeleteBytes(initial, "usage")
	if err != nil {
		return nil, false
	}

	var buf bytes.Buffer
	sequence := 0
	appendEvent := func(eventType string, data []byte) bool {
		data, err = sjson.SetBytes(data, "sequence_number", sequence)
		if err != nil {
			return false
		}
		sequence++
		_, _ = buf.WriteString("event: " + eventType + "\ndata: ")
		_, _ = buf.Write(data)
		_, _ = buf.WriteString("\n\n")
		return true
	}
	for _, eventType := range []string{"response.created", "response.in_progress"} {
		event, err := json.Marshal(map[string]any{"type": eventType, "response": json.RawMessage(initial)})
		if err != nil || !appendEvent(eventType, event) {
			return nil, false
		}
	}
	outputIndex := 0
	for _, item := range gjson.GetBytes(response, "output").Array() {
		if !item.IsObject() {
			continue
		}
		added := []byte(item.Raw)
		added, err = sjson.SetBytes(added, "status", "in_progress")
		if err != nil {
			return nil, false
		}
		for _, eventType := range []string{"response.output_item.added", "response.output_item.done"} {
			rawItem := []byte(item.Raw)
			if eventType == "response.output_item.added" {
				rawItem = added
			}
			event, err := json.Marshal(map[string]any{"type": eventType, "output_index": outputIndex, "item": json.RawMessage(rawItem)})
			if err != nil || !appendEvent(eventType, event) {
				return nil, false
			}
		}
		outputIndex++
	}
	completed, err := json.Marshal(map[string]any{"type": "response.completed", "response": json.RawMessage(response)})
	if err != nil || !appendEvent("response.completed", completed) {
		return nil, false
	}
	return buf.Bytes(), true
}

func openAICompactUsageParsableByCodex(usage gjson.Result) bool {
	if !usage.IsObject() {
		return false
	}
	for _, field := range []string{"input_tokens", "output_tokens", "total_tokens"} {
		if usage.Get(field).Type != gjson.Number {
			return false
		}
	}
	return true
}
