package service

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestHandleResponsesAnthropicCompactionStreamReturnsSingleOpaqueItem(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"msg_compact","usage":{"input_tokens":0,"output_tokens":0}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"compaction","content":"summary","signature":"signed-state"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"compaction"},"usage":{"input_tokens":0,"output_tokens":0,"iterations":[{"type":"compaction","input_tokens":144,"output_tokens":276}]}}`,
		`data: {"type":"message_stop"}`,
	}, "\n\n")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Request-Id": []string{"msg_compact"}}, Body: ioNopCloserString(body)}

	result, err := (&GatewayService{}).handleResponsesAnthropicCompactionStream(resp, c, "claude-sonnet-5-5", time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 144, result.Usage.InputTokens)
	require.Equal(t, 276, result.Usage.OutputTokens)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))

	var outputItems []json.RawMessage
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := []byte(strings.TrimPrefix(line, "data: "))
		if gjson.GetBytes(data, "type").String() == "response.output_item.done" {
			outputItems = append(outputItems, json.RawMessage(gjson.GetBytes(data, "item").Raw))
		}
		if gjson.GetBytes(data, "type").String() == "response.completed" {
			require.Equal(t, "compaction", gjson.GetBytes(data, "response.output.0.type").String())
		}
	}
	require.Len(t, outputItems, 1)
	require.Equal(t, "compaction", gjson.GetBytes(outputItems[0], "type").String())
	envelope := gjson.GetBytes(outputItems[0], "encrypted_content").String()
	require.True(t, strings.HasPrefix(envelope, "anthropic-compaction-v1:"))
	encoded := strings.TrimPrefix(envelope, "anthropic-compaction-v1:")
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"compaction","content":"summary","signature":"signed-state"}`, string(decoded))
}

func TestMergeAnthropicCompactionUsageIgnoresOtherIterations(t *testing.T) {
	usage := ClaudeUsage{}
	mergeAnthropicCompactionUsage(&usage, json.RawMessage(`{"iterations":[{"type":"compaction","input_tokens":144,"output_tokens":276},{"type":"other","input_tokens":1000,"output_tokens":2000}]}`))
	require.Equal(t, 144, usage.InputTokens)
	require.Equal(t, 276, usage.OutputTokens)
}

func TestHandleResponsesAnthropicCompactionStreamDoesNotReturnEmptySuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Body: ioNopCloserString(body)}

	result, err := (&GatewayService{}).handleResponsesAnthropicCompactionStream(resp, c, "claude-sonnet-5-5", time.Now())
	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, recorder.Body.String(), `"type":"response.failed"`)
	require.NotContains(t, recorder.Body.String(), `"type":"response.completed"`)
}

func ioNopCloserString(value string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(value))
}
