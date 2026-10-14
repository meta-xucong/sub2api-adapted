package service

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
)

type terminalErrorReader struct {
	data []byte
	err  error
	sent bool
}

func (r *terminalErrorReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, r.data), nil
	}
	return 0, r.err
}

func runNativeAnthropicResponsesStream(t *testing.T, body io.Reader, writer http.ResponseWriter) (*OpenAIForwardResult, error, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := newNativeAnthropicHangTestService(5)
	var rec *httptest.ResponseRecorder
	if writer == nil {
		rec = httptest.NewRecorder()
		writer = rec
	} else if recorder, ok := writer.(*httptest.ResponseRecorder); ok {
		rec = recorder
	}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(body)}
	result, err := svc.handleResponsesStreamingFromNativeAnthropic(
		resp, c, "claude-test", "claude-test", "claude-test", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
	return result, err, rec
}

func responsesNativeStreamEvents(t *testing.T, body string) []apicompat.ResponsesStreamEvent {
	t.Helper()
	var events []apicompat.ResponsesStreamEvent
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event apicompat.ResponsesStreamEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatalf("decode downstream event: %v; line=%q", err, line)
		}
		events = append(events, event)
	}
	return events
}

func TestResponsesStreamingFromNativeAnthropic_RequiresMessageStop(t *testing.T) {
	t.Run("normal text", func(t *testing.T) {
		body := miniAnthropicSSEStream() + "event: error\ndata: {bad trailing event}\n\n"
		result, err, rec := runNativeAnthropicResponsesStream(t, strings.NewReader(body), nil)
		if err != nil || result == nil {
			t.Fatalf("expected successful result, got result=%v err=%v", result, err)
		}
		events := responsesNativeStreamEvents(t, rec.Body.String())
		if len(events) == 0 || events[len(events)-1].Type != "response.completed" {
			t.Fatalf("expected response.completed terminal, got %#v", events)
		}
	})

	t.Run("tool use is a successful response", func(t *testing.T) {
		result, err, rec := runNativeAnthropicResponsesStream(t, strings.NewReader(toolAnthropicSSEStream()), nil)
		if err != nil || result == nil {
			t.Fatalf("expected successful tool response, got result=%v err=%v", result, err)
		}
		events := responsesNativeStreamEvents(t, rec.Body.String())
		if len(events) == 0 || events[len(events)-1].Type != "response.completed" || events[len(events)-1].Response == nil {
			t.Fatalf("expected completed tool response, got %#v", events)
		}
		foundCall := false
		for _, item := range events[len(events)-1].Response.Output {
			if item.Type == "function_call" && item.Status == "completed" {
				foundCall = true
			}
		}
		if !foundCall {
			t.Fatalf("expected completed function_call output, got %#v", events[len(events)-1].Response.Output)
		}
	})
}

func TestResponsesStreamingFromNativeAnthropic_FailsInsteadOfFinalizingAbnormalStream(t *testing.T) {
	start := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_partial\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-test\",\"usage\":{\"input_tokens\":3}}}\n\n"
	partial := start + "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n"
	cases := []struct {
		name string
		body io.Reader
	}{
		{name: "EOF before first event", body: strings.NewReader("")},
		{name: "EOF after partial output", body: strings.NewReader(partial)},
		{name: "Anthropic error event", body: strings.NewReader(start + "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"do-not-leak-this\"}}\n\n")},
		{name: "malformed JSON", body: strings.NewReader("event: message_start\ndata: {bad json}\n\n")},
		{name: "event without data line", body: strings.NewReader("event: message_start\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")},
		{name: "message start missing id", body: strings.NewReader("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"type\":\"message\",\"content\":[],\"model\":\"claude-test\"}}\n\n")},
		{name: "orphan message stop", body: strings.NewReader("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")},
		{name: "read error", body: &terminalErrorReader{data: []byte(partial), err: errors.New("upstream socket reset")}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err, rec := runNativeAnthropicResponsesStream(t, tc.body, nil)
			if err == nil || !strings.Contains(err.Error(), "upstream response failed:") {
				t.Fatalf("expected handled upstream stream failure, got %v", err)
			}
			if result == nil {
				t.Fatal("expected result to preserve usage accounting")
			}
			events := responsesNativeStreamEvents(t, rec.Body.String())
			failed, completed := 0, 0
			var terminal *apicompat.ResponsesStreamEvent
			for i := range events {
				switch events[i].Type {
				case "response.failed":
					failed++
					terminal = &events[i]
				case "response.completed", "response.incomplete":
					completed++
				}
			}
			if failed != 1 || completed != 0 || terminal == nil || terminal.Response == nil {
				t.Fatalf("expected one failed terminal and no success terminal, got %#v", events)
			}
			if strings.TrimSpace(terminal.Response.ID) == "" || terminal.Response.Status != "failed" {
				t.Fatalf("failure terminal needs a non-empty ID and failed status: %#v", terminal.Response)
			}
			if strings.Contains(rec.Body.String(), "do-not-leak-this") {
				t.Fatalf("upstream error detail leaked to client: %q", rec.Body.String())
			}
		})
	}
}

func TestResponsesStreamingFromNativeAnthropic_ClientDisconnectAndAbnormalEOFDoesNotSynthesizeTerminal(t *testing.T) {
	partial := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_disconnect\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-test\",\"usage\":{\"input_tokens\":3}}}\n\n"
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Writer = &failingGinWriter{ResponseWriter: c.Writer, failAfter: 0}
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(partial))}
	result, err := newNativeAnthropicHangTestService(5).handleResponsesStreamingFromNativeAnthropic(
		resp, c, "claude-test", "claude-test", "claude-test", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
	if err == nil || result == nil || !result.ClientDisconnect {
		t.Fatalf("expected disconnected client plus upstream EOF failure, result=%+v err=%v", result, err)
	}
	if strings.Contains(rec.Body.String(), `"type":"response.failed"`) || strings.Contains(rec.Body.String(), `"type":"response.completed"`) {
		t.Fatalf("must not write a terminal event after client disconnect, got %q", rec.Body.String())
	}
}

func TestResponsesBufferedFromNativeAnthropic_RequiresMessageStop(t *testing.T) {
	cases := []struct {
		name string
		body io.Reader
	}{
		{name: "EOF after partial response", body: strings.NewReader("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_buffered\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-test\"}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial-secret\"}}\n\n")},
		{name: "error event", body: strings.NewReader("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"do-not-leak-this\"}}\n\n")},
		{name: "malformed JSON", body: strings.NewReader("event: message_start\ndata: {bad json}\n\n")},
		{name: "event without data line", body: strings.NewReader("event: message_start\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")},
		{name: "orphan message stop", body: strings.NewReader("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")},
		{name: "read error", body: &terminalErrorReader{data: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_buffered_error\",\"type\":\"message\",\"content\":[],\"model\":\"claude-test\"}}\n\n"), err: errors.New("upstream socket reset")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(tc.body)}
			result, err := newNativeAnthropicHangTestService(5).handleResponsesBufferedFromNativeAnthropic(
				resp, c, "claude-test", "claude-test", "claude-test", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
			if err == nil || !strings.Contains(err.Error(), "upstream response failed:") || result == nil {
				t.Fatalf("expected handled buffered stream failure and usage result, result=%+v err=%v", result, err)
			}
			if rec.Code != http.StatusBadGateway {
				t.Fatalf("expected HTTP 502, got %d: %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "partial-secret") || strings.Contains(rec.Body.String(), "do-not-leak-this") {
				t.Fatalf("partial output or upstream detail leaked as buffered success/error: %q", rec.Body.String())
			}
		})
	}
}

func TestResponsesBufferedFromNativeAnthropic_IgnoresEventsAfterMessageStop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	body := miniAnthropicSSEStream() + "event: error\ndata: {bad trailing event}\n\n"
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	result, err := newNativeAnthropicHangTestService(5).handleResponsesBufferedFromNativeAnthropic(
		resp, c, "claude-test", "claude-test", "claude-test", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
	if err != nil || result == nil {
		t.Fatalf("expected the valid message_stop to complete the response, result=%+v err=%v", result, err)
	}
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"completed"`) {
		t.Fatalf("expected HTTP 200 completed response, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestResponsesBufferedFromNativeAnthropic_IdleTimeoutReturns502(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: pr}
	result, err := newNativeAnthropicHangTestService(1).handleResponsesBufferedFromNativeAnthropic(
		resp, c, "claude-test", "claude-test", "claude-test", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
	_ = pw.Close()
	if err == nil || result == nil {
		t.Fatalf("expected timeout error with a usage result, result=%+v err=%v", result, err)
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected HTTP 502, got %d: %s", rec.Code, rec.Body.String())
	}
}



