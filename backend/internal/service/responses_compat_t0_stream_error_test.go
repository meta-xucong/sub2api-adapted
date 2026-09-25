package service

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type t0AuditInterruptedReader struct{}

func (t0AuditInterruptedReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestT0Audit_StreamErrorsHaveTerminalFailure(t *testing.T) {
	for _, scenario := range []string{"read_error", "invalid_arguments"} {
		t.Run(scenario, func(t *testing.T) {
			c, rec := t0AuditContext("/v1/responses", `{"model":"test-model","stream":true}`)
			var body io.Reader
			code := "upstream_stream_error"
			if scenario == "read_error" {
				body = io.MultiReader(strings.NewReader("data: {\"id\":\"chat_stream_test\",\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n"), t0AuditInterruptedReader{})
			} else {
				code = "invalid_tool_arguments"
				body = strings.NewReader("data: {\"id\":\"chat_stream_test\",\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_exec\",\"type\":\"function\",\"function\":{\"name\":\"unified_exec\",\"arguments\":\"{\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			}
			svc := &OpenAIGatewayService{}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(body)}
			_, err := svc.streamChatCompletionsAsResponses(c, resp, "test-model", nil, nil, false, nil, "test-model", "test-model", nil, nil, time.Now())
			require.Error(t, err)
			require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.failed\n"))
			require.NotContains(t, rec.Body.String(), "event: response.completed\n")
			responseID := ""
			sequence := int64(0)
			for _, line := range strings.Split(rec.Body.String(), "\n") {
				if !strings.HasPrefix(line, "data: {") {
					continue
				}
				payload := strings.TrimPrefix(line, "data: ")
				require.Equal(t, sequence, gjson.Get(payload, "sequence_number").Int())
				sequence++
				switch gjson.Get(payload, "type").String() {
				case "response.created":
					responseID = gjson.Get(payload, "response.id").String()
				case "response.failed":
					require.NotEmpty(t, responseID)
					require.Equal(t, responseID, gjson.Get(payload, "response.id").String())
					require.Equal(t, "failed", gjson.Get(payload, "response.status").String())
					require.Equal(t, code, gjson.Get(payload, "response.error.code").String())
				}
			}
		})
	}
}
