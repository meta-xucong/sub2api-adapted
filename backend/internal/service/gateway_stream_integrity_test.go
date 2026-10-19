//go:build unit

package service

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func integrityContext() (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return c, rec
}
func integrityResponse(body io.ReadCloser) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Request-Id": []string{"integrity-request"}}, Body: body}
}

func TestGatewayStreamIntegrityCCAdapters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const partial = "data: {\"id\":\"chat_test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\n"
	const finish = "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"
	const done = "data: [DONE]\n\n"
	adapters := map[string]func(*gin.Context, *http.Response) (*OpenAIForwardResult, error){
		"messages": func(c *gin.Context, r *http.Response) (*OpenAIForwardResult, error) {
			return (&OpenAIGatewayService{}).streamChatCompletionsAsAnthropic(c, r, "m", "m", "m", nil, nil, time.Now())
		},
		"responses": func(c *gin.Context, r *http.Response) (*OpenAIForwardResult, error) {
			return (&OpenAIGatewayService{}).streamChatCompletionsAsResponses(c, r, "m", nil, nil, false, nil, "m", "m", nil, nil, time.Now())
		},
		"raw": func(c *gin.Context, r *http.Response) (*OpenAIForwardResult, error) {
			return (&OpenAIGatewayService{}).streamRawChatCompletions(c, r, rawChatCompletionsTestAccount(), "m", "m", "m", nil, nil, time.Now(), 0)
		},
	}
	cases := []struct {
		name, payload string
		tail          error
		fail          bool
	}{
		{name: "done", payload: partial + finish + done},
		{name: "finish_reason_clean_eof", payload: partial + finish},
		{name: "partial_with_usage_no_terminal", payload: partial, fail: true},
		{name: "in_band_error_then_done", payload: partial + "data: {\"error\":{\"message\":\"capacity failure\"}}\n\n" + done, fail: true},
		{name: "malformed_then_done", payload: partial + "data: {broken}\n\n" + done, fail: true},
		{name: "event_name_error_then_done", payload: partial + "event: error\ndata: {\"message\":\"failed\"}\n\n" + done, fail: true},
		{name: "error_finish_reason", payload: partial + strings.ReplaceAll(finish, "stop", "error") + done, fail: true},
		{name: "numeric_finish_reason", payload: partial + strings.ReplaceAll(finish, `"stop"`, `42`) + done, fail: true},
		{name: "null_then_done", payload: partial + "data: null\n\n" + done, fail: true},
		{name: "partial_read_error", payload: partial, tail: io.ErrUnexpectedEOF, fail: true},
		{name: "finish_reason_read_error", payload: partial + finish, tail: io.ErrUnexpectedEOF, fail: true},
	}
	for name, adapter := range adapters {
		for _, tc := range cases {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				c, rec := integrityContext()
				var body = io.NopCloser(strings.NewReader(tc.payload))
				if tc.tail != nil {
					body = &errTailReader{data: []byte(tc.payload), err: tc.tail}
				}
				result, err := adapter(c, integrityResponse(body))
				require.NotNil(t, result)
				require.Equal(t, 7, result.Usage.InputTokens)
				require.Equal(t, 3, result.Usage.OutputTokens)
				if tc.fail {
					require.Error(t, err)
					var failover *UpstreamFailoverError
					require.False(t, errors.As(err, &failover), "failure after output must not replay the paid request")
					require.NotContains(t, rec.Body.String(), "data: [DONE]")
					require.NotContains(t, rec.Body.String(), `"type":"message_stop"`)
					require.NotContains(t, rec.Body.String(), `"type":"response.completed"`)
				} else {
					require.NoError(t, err)
				}
			})
		}
	}
}

func TestGatewayStreamIntegrityCCErrorBeforeOutput(t *testing.T) {
	for _, messages := range []bool{false, true} {
		t.Run(map[bool]string{false: "responses", true: "messages"}[messages], func(t *testing.T) {
			c, rec := integrityContext()
			resp := integrityResponse(io.NopCloser(strings.NewReader("data: {\"error\":{\"message\":\"failed\"}}\n\ndata: [DONE]\n\n")))
			svc := &OpenAIGatewayService{}
			var err error
			if messages {
				_, err = svc.streamChatCompletionsAsAnthropic(c, resp, "m", "m", "m", nil, nil, time.Now())
			} else {
				_, err = svc.streamChatCompletionsAsResponses(c, resp, "m", nil, nil, false, nil, "m", "m", nil, nil, time.Now())
			}
			require.Error(t, err)
			require.Equal(t, http.StatusBadGateway, rec.Code)
			require.NotContains(t, rec.Body.String(), "response.completed")
			require.NotContains(t, rec.Body.String(), "message_stop")
		})
	}
}

func TestGatewayStreamIntegrityAnthropicAdapters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	full := miniAnthropicSSEStream()
	partial := strings.Split(full, "event: message_stop")[0]
	errorEvent := "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"capacity failure\"}}\n\n"
	for _, native := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, tc := range []struct {
				name, payload string
				tail          error
				fail          bool
				input         int
			}{
				{name: "complete", payload: full, input: 10},
				// A second empty data field adds JSON whitespace to the same
				// SSE event; it must not be decoded as another JSON document.
				{name: "trailing_empty_data", payload: strings.ReplaceAll(full, "\n\n", "\ndata:\n\n"), input: 10},
				{name: "split_json_data", payload: strings.ReplaceAll(full, "data: {", "data: {\ndata: "), input: 10},
				{name: "no_data_event_ignored", payload: "event: error\n: no data field\n\n" + full, input: 10},
				{name: "terminal_at_eof", payload: strings.TrimRight(full, "\n"), input: 10},
				{name: "terminal_at_eof_read_error", payload: strings.TrimRight(full, "\n"), tail: io.ErrUnexpectedEOF, fail: true, input: 10},
				{name: "partial_eof", payload: partial, fail: true, input: 10},
				{name: "usage_at_eof", payload: strings.TrimRight(partial, "\n"), fail: true, input: 10},
				{name: "error_then_stop", payload: partial + errorEvent + "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", fail: true, input: 10},
				{name: "empty_data_error_then_stop", payload: partial + "event: error\ndata:\n\n" + "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", fail: true, input: 10},
				{name: "error_before_start", payload: errorEvent, fail: true},
				{name: "stop_before_start", payload: "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", fail: true},
				{name: "malformed", payload: partial + "event: message_stop\ndata: {broken}\n\n", fail: true, input: 10},
				{name: "read_error", payload: partial, tail: io.ErrUnexpectedEOF, fail: true, input: 10},
				{name: "truncated_tool", payload: strings.Split(strings.Replace(toolAnthropicSSEStream(), `{\"query\":\"status\"}`, `{\"query\":`, 1), "event: message_stop")[0], fail: true, input: 10},
			} {
				t.Run(map[bool]string{false: "gateway", true: "native"}[native]+"/"+map[bool]string{false: "buffered", true: "stream"}[stream]+"/"+tc.name, func(t *testing.T) {
					c, rec := integrityContext()
					var body = io.NopCloser(strings.NewReader(tc.payload))
					if tc.tail != nil {
						body = &errTailReader{data: []byte(tc.payload), err: tc.tail}
					}
					resp := integrityResponse(body)
					var err error
					var input int
					if native {
						svc := &OpenAIGatewayService{}
						var result *OpenAIForwardResult
						if stream {
							result, err = svc.handleCCStreamingFromNativeAnthropic(resp, c, "m", "m", "m", nil, time.Now())
						} else {
							result, err = svc.handleCCBufferedFromNativeAnthropic(resp, c, "m", "m", "m", nil, time.Now())
						}
						if tc.fail && tc.input == 0 {
							require.Nil(t, result, "unmetered failures must not create billing records")
						} else {
							require.NotNil(t, result)
							input = result.Usage.InputTokens
						}
					} else {
						svc := &GatewayService{}
						var result *ForwardResult
						if stream {
							result, err = svc.handleCCStreamingFromAnthropic(resp, c, "m", "m", nil, time.Now())
						} else {
							result, err = svc.handleCCBufferedFromAnthropic(resp, c, "m", "m", nil, time.Now())
						}
						if tc.fail && tc.input == 0 {
							require.Nil(t, result, "unmetered failures must not create billing records")
						} else {
							require.NotNil(t, result)
							input = result.Usage.InputTokens
						}
					}
					require.Equal(t, tc.input, input)
					if tc.fail {
						require.Error(t, err)
						require.NotContains(t, rec.Body.String(), "data: [DONE]")
						require.NotContains(t, rec.Body.String(), `"finish_reason":"stop"`)
						if !stream {
							require.Equal(t, http.StatusBadGateway, rec.Code)
						}
					} else {
						require.NoError(t, err)
					}
				})
			}
		}
	}
}

func TestGatewayStreamIntegrityConvertedCCFrames(t *testing.T) {
	const partial = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\n"
	const finish = "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"
	for _, tc := range []struct {
		name, payload string
		fail, tailErr bool
	}{
		{name: "trailing_empty_data", payload: strings.ReplaceAll(partial+finish, "\n\n", "\ndata:\n\n")},
		{name: "split_json_data", payload: strings.ReplaceAll(partial+finish, "data: {", "data: {\ndata: ")},
		{name: "no_data_event_ignored", payload: "event: error\n: no data field\n\n" + partial + finish},
		{name: "empty_data_error", payload: partial + "event: error\ndata:\n\n" + finish + "data: [DONE]\n\n", fail: true},
		{name: "empty_data_error_at_eof", payload: partial + "event: error\ndata:", fail: true},
		{name: "event_after_data_error", payload: partial + "data: {}\nevent: error\n\n" + finish, fail: true},
		{name: "finish_at_eof", payload: partial + strings.TrimRight(finish, "\n")},
		{name: "usage_at_eof", payload: strings.TrimRight(partial, "\n"), fail: true},
		{name: "usage_before_read_error", payload: strings.TrimRight(partial, "\n"), fail: true, tailErr: true},
		{name: "finish_before_read_error", payload: partial + strings.TrimRight(finish, "\n"), fail: true, tailErr: true},
	} {
		for _, messages := range []bool{false, true} {
			t.Run(map[bool]string{false: "responses", true: "messages"}[messages]+"/"+tc.name, func(t *testing.T) {
				c, rec := integrityContext()
				var body = io.NopCloser(strings.NewReader(tc.payload))
				if tc.tailErr {
					body = &errTailReader{data: []byte(tc.payload), err: io.ErrUnexpectedEOF}
				}
				resp := integrityResponse(body)
				svc := &OpenAIGatewayService{}
				var result *OpenAIForwardResult
				var err error
				if messages {
					result, err = svc.streamChatCompletionsAsAnthropic(c, resp, "m", "m", "m", nil, nil, time.Now())
				} else {
					result, err = svc.streamChatCompletionsAsResponses(c, resp, "m", nil, nil, false, nil, "m", "m", nil, nil, time.Now())
				}
				require.NotNil(t, result)
				require.Equal(t, 7, result.Usage.InputTokens)
				require.Equal(t, 3, result.Usage.OutputTokens)
				require.Contains(t, rec.Body.String(), "partial")
				if tc.fail {
					require.Error(t, err)
					require.NotContains(t, rec.Body.String(), "data: [DONE]")
					require.NotContains(t, rec.Body.String(), `"type":"message_stop"`)
					require.NotContains(t, rec.Body.String(), `"type":"response.completed"`)
				} else {
					require.NoError(t, err)
					if messages {
						require.Contains(t, rec.Body.String(), `"type":"message_stop"`)
					} else {
						require.Contains(t, rec.Body.String(), `"type":"response.completed"`)
					}
				}
			})
		}
	}
}

func TestGatewayStreamIntegrityConvertedFramesBounded(t *testing.T) {
	cfg := rawChatCompletionsTestConfig()
	cfg.Gateway.MaxLineSize = 128
	// Every data line fits, but the accumulated event does not.
	payload := "data: {\n" + strings.Repeat("data: "+strings.Repeat(" ", 60)+"\n", 3) + "data: }\n\ndata: [DONE]\n\n"
	c, _ := integrityContext()
	scan := (&OpenAIGatewayService{cfg: cfg}).scanCCStream(c, integrityResponse(io.NopCloser(strings.NewReader(payload))), "test", "", time.Now(), func(*apicompat.ChatCompletionsChunk) bool {
		t.Fatal("oversized frame must not be emitted")
		return false
	})
	require.ErrorContains(t, scan.Err, "frame exceeds")
	require.False(t, scan.SawDone)
	err := scanAnthropicCCEvents(c, integrityResponse(io.NopCloser(strings.NewReader(payload))), cfg, func(*apicompat.AnthropicStreamEvent, string) bool {
		t.Fatal("oversized frame must not be emitted")
		return false
	})
	require.ErrorContains(t, err, "frame exceeds")
}

func TestGatewayStreamIntegrityAnthropicUsageAtEOF(t *testing.T) {
	partial := strings.TrimRight(strings.Split(miniAnthropicSSEStream(), "event: message_stop")[0], "\n")
	for _, readError := range []bool{false, true} {
		c, _ := integrityContext()
		var body = io.NopCloser(strings.NewReader(partial))
		if readError {
			body = &errTailReader{data: []byte(partial), err: io.ErrUnexpectedEOF}
		}
		_, usage, err := readAnthropicCCResponse(c, integrityResponse(body), nil)
		require.Error(t, err)
		require.Equal(t, 10, usage.InputTokens)
		require.Equal(t, 5, usage.OutputTokens)
		if readError {
			require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		}
	}
}

func TestGatewayStreamIntegrityLengthTerminal(t *testing.T) {
	state := apicompat.NewChatCompletionsToResponsesStreamState("m")
	state.FinishReason = "length"
	events := apicompat.FinalizeChatCompletionsResponsesStream(state)
	terminal := events[len(events)-1]
	require.Equal(t, "response.incomplete", terminal.Type)
	require.Equal(t, "incomplete", terminal.Response.Status)
	require.Equal(t, "max_output_tokens", terminal.Response.IncompleteDetails.Reason)
	require.Empty(t, apicompat.FinalizeChatCompletionsResponsesStream(state))
}

type integrityCountBody struct {
	io.ReadCloser
	closes atomic.Int32
}

func (b *integrityCountBody) Close() error { b.closes.Add(1); return b.ReadCloser.Close() }

func TestBoundedSSEScannerIdleClosesBlockedRead(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	body := &integrityCountBody{ReadCloser: pr}
	scanner := newBoundedSSEScanner(context.Background(), body, bufio.NewScanner(body), 30*time.Millisecond, time.Second)
	defer scanner.Close()
	require.False(t, scanner.Scan())
	require.ErrorIs(t, scanner.Err(), errAnthropicNativeStreamIdle)
	scanner.Close()
	scanner.Close()
	require.EqualValues(t, 1, body.closes.Load())
}

func TestBoundedSSEScannerCancellationIsAbsoluteDespiteData(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	body := &integrityCountBody{ReadCloser: pr}
	scanner := newBoundedSSEScanner(ctx, body, bufio.NewScanner(body), 0, 40*time.Millisecond)
	defer scanner.Close()
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			if _, err := io.WriteString(pw, ": ping\n"); err != nil {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	require.True(t, scanner.Scan())
	cancel()
	start := time.Now()
	for scanner.Scan() {
	}
	require.ErrorIs(t, scanner.Err(), errUpstreamCancelDrainTimeout)
	require.Less(t, time.Since(start), time.Second)
	select {
	case <-writerDone:
	case <-time.After(time.Second):
		t.Fatal("body close did not release writer")
	}
	require.EqualValues(t, 1, body.closes.Load())
}

func TestBoundedSSEScannerDisabledIdleStillBoundsDisconnect(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	scanner := newBoundedSSEScanner(context.Background(), pr, bufio.NewScanner(pr), 0, 30*time.Millisecond)
	defer scanner.Close()
	done := make(chan bool, 1)
	go func() { done <- scanner.Scan() }()
	select {
	case <-done:
		t.Fatal("idle=0 must not expire an active request")
	case <-time.After(40 * time.Millisecond):
	}
	scanner.startDrain()
	select {
	case ok := <-done:
		require.False(t, ok)
	case <-time.After(time.Second):
		t.Fatal("disconnect drain did not close blocked reader")
	}
	require.ErrorIs(t, scanner.Err(), errUpstreamCancelDrainTimeout)
}

func TestUpstreamDrainContextOwnsResponseLifetime(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	ctx, release := newUpstreamDrainContext(parent, 40*time.Millisecond)
	defer release()
	cancel()
	select {
	case <-ctx.Done():
		t.Fatal("client cancellation must allow a short usage drain")
	case <-time.After(10 * time.Millisecond):
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("header wait must respect cancel drain deadline")
	}

	parentDeadline, cancelDeadline := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancelDeadline()
	bounded, releaseDeadline := newUpstreamDrainContext(parentDeadline, time.Second)
	defer releaseDeadline()
	select {
	case <-bounded.Done():
		require.ErrorIs(t, bounded.Err(), context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("caller total deadline was lost")
	}

	owner, ownerRelease := newUpstreamDrainContext(context.Background(), time.Second)
	body := &integrityCountBody{ReadCloser: io.NopCloser(strings.NewReader("body"))}
	wrapped := &upstreamReleaseBody{ReadCloser: body, release: ownerRelease}
	require.NoError(t, owner.Err())
	require.NoError(t, wrapped.Close())
	require.NoError(t, wrapped.Close())
	require.ErrorIs(t, owner.Err(), context.Canceled)
	require.EqualValues(t, 1, body.closes.Load())
}

// Cancel only when the handler is blocked reading its next line, after the last
// usage callback. The final result must not depend on another data event arriving.
type integrityCancelAtEOF struct {
	io.Reader
	cancel context.CancelFunc
}

func (b *integrityCancelAtEOF) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		b.cancel()
	}
	return n, err
}
func (b *integrityCancelAtEOF) Close() error { return nil }

func TestGatewayStreamIntegrityCancellationAtEOF(t *testing.T) {
	for _, name := range []string{"messages", "responses", "raw", "native", "gateway"} {
		t.Run(name, func(t *testing.T) {
			c, _ := integrityContext()
			ctx, cancel := context.WithCancel(c.Request.Context())
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			payload := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\n"
			if name == "native" || name == "gateway" {
				payload = strings.Split(miniAnthropicSSEStream(), "event: message_stop")[0]
			}
			resp := integrityResponse(&integrityCancelAtEOF{Reader: strings.NewReader(payload), cancel: cancel})
			svc := &OpenAIGatewayService{}
			var result *OpenAIForwardResult
			var err error
			switch name {
			case "messages":
				result, err = svc.streamChatCompletionsAsAnthropic(c, resp, "m", "m", "m", nil, nil, time.Now())
			case "responses":
				result, err = svc.streamChatCompletionsAsResponses(c, resp, "m", nil, nil, false, nil, "m", "m", nil, nil, time.Now())
			case "raw":
				result, err = svc.streamRawChatCompletions(c, resp, rawChatCompletionsTestAccount(), "m", "m", "m", nil, nil, time.Now(), 0)
			case "native":
				result, err = svc.handleCCStreamingFromNativeAnthropic(resp, c, "m", "m", "m", nil, time.Now())
			case "gateway":
				r, e := (&GatewayService{}).handleCCStreamingFromAnthropic(resp, c, "m", "m", nil, time.Now())
				require.Error(t, e)
				require.NotNil(t, r)
				require.True(t, r.ClientDisconnect)
				return
			}
			require.Error(t, err)
			require.NotNil(t, result)
			require.True(t, result.ClientDisconnect)
		})
	}
}

func TestGatewayStreamIntegrityRawChoiceTerminals(t *testing.T) {
	const first = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"one\"},\"finish_reason\":\"stop\"},{\"index\":1,\"delta\":{\"content\":\"two\"},\"finish_reason\":null}]}\n\n"
	const second = "data: {\"choices\":[{\"index\":1,\"delta\":{},\"finish_reason\":\"length\"}]}\n\n"
	for _, tc := range []struct {
		name, payload string
		fail          bool
	}{
		{"second_unfinished_eof", first, true},
		{"second_unfinished_done", first + "data: [DONE]\n\n", true},
		{"both_finished_eof", first + second, false},
		{"both_finished_done", first + second + "data: [DONE]\n\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := integrityContext()
			_, err := (&OpenAIGatewayService{}).streamRawChatCompletions(c, integrityResponse(io.NopCloser(strings.NewReader(tc.payload))), rawChatCompletionsTestAccount(), "m", "m", "m", nil, nil, time.Now(), 0, 2)
			if tc.fail {
				require.Error(t, err)
				require.NotContains(t, rec.Body.String(), "data: [DONE]")
			} else {
				require.NoError(t, err)
			}
		})
	}
	c, _ := integrityContext()
	_, err := (&OpenAIGatewayService{}).streamRawChatCompletions(c, integrityResponse(&errTailReader{data: []byte(": comment\n\n"), err: io.ErrUnexpectedEOF}), rawChatCompletionsTestAccount(), "m", "m", "m", nil, nil, time.Now(), 0)
	require.Error(t, err, "non-SSE clean EOF exemption must not swallow transport failures")
}

func TestGatewayStreamIntegrityConvertedChoicesRejected(t *testing.T) {
	for _, messages := range []bool{false, true} {
		c, rec := integrityContext()
		resp := integrityResponse(io.NopCloser(strings.NewReader("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"one\"},\"finish_reason\":\"stop\"},{\"index\":1,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")))
		var err error
		if messages {
			_, err = (&OpenAIGatewayService{}).streamChatCompletionsAsAnthropic(c, resp, "m", "m", "m", nil, nil, time.Now())
		} else {
			_, err = (&OpenAIGatewayService{}).streamChatCompletionsAsResponses(c, resp, "m", nil, nil, false, nil, "m", "m", nil, nil, time.Now())
		}
		require.Error(t, err)
		require.Equal(t, http.StatusBadGateway, rec.Code)
	}
}

func TestGatewayStreamIntegrityRawMultilineSSE(t *testing.T) {
	const content = "data: {\"id\":\"multi\",\n" +
		"data: \"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}],\n" +
		"data: \"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\n"
	for _, tc := range []struct {
		name, tail string
		fail       bool
	}{
		{"complete", "data: [DONE]\n\n", false},
		{"explicit_error", "event: error\ndata: {\n" + "data: \"message\":\"failed\"}\n\ndata: [DONE]\n\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := integrityContext()
			result, err := (&OpenAIGatewayService{}).streamRawChatCompletions(c, integrityResponse(io.NopCloser(strings.NewReader(content+tc.tail))), rawChatCompletionsTestAccount(), "m", "m", "m", nil, nil, time.Now(), 0)
			require.NotNil(t, result)
			require.Equal(t, 7, result.Usage.InputTokens)
			require.Equal(t, 3, result.Usage.OutputTokens)
			if tc.fail {
				require.Error(t, err)
				require.NotContains(t, rec.Body.String(), "data: [DONE]")
			} else {
				require.NoError(t, err)
				require.Equal(t, content+tc.tail, rec.Body.String(), "native multiline bytes must remain unchanged")
			}
		})
	}
	c, rec := integrityContext()
	cfg := rawChatCompletionsTestConfig()
	cfg.Gateway.MaxLineSize = 128
	largeFrame := "data: {\n" + strings.Repeat("data: "+strings.Repeat(" ", 60)+"\n", 3) + "data: }\n\ndata: [DONE]\n\n"
	_, err := (&OpenAIGatewayService{cfg: cfg}).streamRawChatCompletions(c, integrityResponse(io.NopCloser(strings.NewReader(largeFrame))), rawChatCompletionsTestAccount(), "m", "m", "m", nil, nil, time.Now(), 0)
	require.Error(t, err)
	require.NotContains(t, rec.Body.String(), "data: [DONE]")
}

func TestGatewayStreamIntegrityRawBufferedFrameEOF(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		metered       bool
	}{
		{"content", `{"choices":[{"index":0,"delta":{"content":"partial"}}]}`, false},
		{"tool", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"search","arguments":"{}"}}]}}]}`, false},
		{"usage", `{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3}}`, true},
		{"content_usage", `{"choices":[{"index":0,"delta":{"content":"partial"}}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`, true},
	} {
		for _, blank := range []bool{false, true} {
			for _, readError := range []bool{false, true} {
				name := tc.name + map[bool]string{false: "/no_blank", true: "/blank"}[blank] + map[bool]string{false: "/eof", true: "/read_error"}[readError]
				t.Run(name, func(t *testing.T) {
					c, rec := integrityContext()
					payload := "data: " + tc.payload + "\n"
					if blank {
						payload += "\n"
					}
					var body = io.NopCloser(strings.NewReader(payload))
					if readError {
						body = &errTailReader{data: []byte(payload), err: io.ErrUnexpectedEOF}
					}
					result, err := (&OpenAIGatewayService{}).streamRawChatCompletions(c, integrityResponse(body), rawChatCompletionsTestAccount(), "m", "m", "m", nil, nil, time.Now(), openAISilentRefusalMinRequestBodyBytes)
					require.Error(t, err)
					require.NotNil(t, result)
					var failover *UpstreamFailoverError
					require.False(t, errors.As(err, &failover), "observed content, tools or usage must not be replayed at EOF")
					if tc.metered {
						require.Equal(t, 7, result.Usage.InputTokens)
						require.Equal(t, 3, result.Usage.OutputTokens)
					}
					require.Equal(t, "data: "+tc.payload+"\n\n", rec.Body.String(), "release original pending bytes and close their incomplete frame before the handler error")
				})
			}
		}
	}
}

func TestGatewayStreamIntegrityRawBufferedFrameCancelledEOF(t *testing.T) {
	c, rec := integrityContext()
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	payload := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n"
	body := &integrityCancelAtEOF{Reader: strings.NewReader(payload), cancel: cancel}
	result, err := (&OpenAIGatewayService{}).streamRawChatCompletions(c, integrityResponse(body), rawChatCompletionsTestAccount(), "m", "m", "m", nil, nil, time.Now(), openAISilentRefusalMinRequestBodyBytes)
	require.Error(t, err)
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
	require.Empty(t, rec.Body.String(), "EOF observation must not flush withheld bytes after cancellation")
}

func TestGatewayStreamIntegrityRawEmptyDataFrames(t *testing.T) {
	for _, payload := range []string{"data:\n\n", "data:   \n\n"} {
		for _, requestSize := range []int{0, openAISilentRefusalMinRequestBodyBytes} {
			c, rec := integrityContext()
			_, err := (&OpenAIGatewayService{}).streamRawChatCompletions(c, integrityResponse(io.NopCloser(strings.NewReader(payload))), rawChatCompletionsTestAccount(), "m", "m", "m", nil, nil, time.Now(), requestSize)
			require.Error(t, err, "an empty SSE data frame must not use the non-SSE EOF exemption")
			require.NotContains(t, rec.Body.String(), "data: [DONE]")
		}
	}
}

func TestGatewayStreamIntegrityRawErrorFrameBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		maxFrame      int
		buffered      bool
		readError     bool
	}{
		{name: "invalid_eof", payload: "data: {broken}\n"},
		{name: "invalid_read_error", payload: "data: {broken}\n", readError: true},
		{name: "explicit_error_eof", payload: "event: error\ndata: {\"message\":\"failed\"}\n"},
		{name: "explicit_error_blank", payload: "event: error\ndata: {\"message\":\"failed\"}\n\n"},
		{name: "oversized_open_frame", payload: "data: {\ndata: " + strings.Repeat(" ", 128) + "\n", maxFrame: 128},
		{name: "withheld_invalid_eof", payload: "data: {broken}\n", buffered: true},
		{name: "withheld_metered_error", payload: "data: {\"error\":{\"message\":\"failed\"},\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n", buffered: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := integrityContext()
			cfg := rawChatCompletionsTestConfig()
			if tc.maxFrame > 0 {
				cfg.Gateway.MaxLineSize = tc.maxFrame
			}
			requestSize := 0
			if tc.buffered {
				requestSize = openAISilentRefusalMinRequestBodyBytes
			}
			var body = io.NopCloser(strings.NewReader(tc.payload))
			if tc.readError {
				body = &errTailReader{data: []byte(tc.payload), err: io.ErrUnexpectedEOF}
			}
			result, err := (&OpenAIGatewayService{cfg: cfg}).streamRawChatCompletions(c, integrityResponse(body), rawChatCompletionsTestAccount(), "m", "m", "m", nil, nil, time.Now(), requestSize)
			require.Error(t, err)
			if tc.buffered {
				require.Empty(t, rec.Body.String(), "closing an emitted frame must not release withheld content")
				if tc.name == "withheld_invalid_eof" {
					require.Nil(t, result, "an unmetered rejected frame must not create a per-request billing result")
					var failover *UpstreamFailoverError
					require.False(t, errors.As(err, &failover), "the billing guard must not introduce a retry")
				}
			} else {
				require.True(t, strings.HasSuffix(rec.Body.String(), "\n\n"), "upstream frame must end before a terminal handler error: %q", rec.Body.String())
			}
			if tc.name == "withheld_metered_error" {
				require.NotNil(t, result)
				require.Equal(t, 7, result.Usage.InputTokens)
				require.Equal(t, 3, result.Usage.OutputTokens)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover))
			}
			_, err = rec.WriteString("event: error\ndata: {\"type\":\"error\",\"message\":\"terminal handler error\"}\n\n")
			require.NoError(t, err)
			var parser openAICompatSSEFrameParser
			var last openAICompatSSEFrame
			for _, line := range strings.Split(rec.Body.String(), "\n") {
				if frame, ready := parser.AddLine(line); ready {
					last = frame
				}
			}
			require.Equal(t, "error", last.EventType)
			require.JSONEq(t, `{"type":"error","message":"terminal handler error"}`, last.Data, "terminal failure must be a separate parseable frame")
		})
	}
}

func TestGatewayStreamIntegrityRawBufferedTerminalEOF(t *testing.T) {
	const payload = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n"
	for _, blank := range []bool{false, true} {
		for _, readError := range []bool{false, true} {
			c, rec := integrityContext()
			input := payload
			if blank {
				input += "\n"
			}
			var body = io.NopCloser(strings.NewReader(input))
			if readError {
				body = &errTailReader{data: []byte(input), err: io.ErrUnexpectedEOF}
			}
			result, err := (&OpenAIGatewayService{}).streamRawChatCompletions(c, integrityResponse(body), rawChatCompletionsTestAccount(), "m", "m", "m", nil, nil, time.Now(), openAISilentRefusalMinRequestBodyBytes)
			require.NotNil(t, result)
			require.Equal(t, 7, result.Usage.InputTokens)
			require.Equal(t, 3, result.Usage.OutputTokens)
			if readError {
				require.Error(t, err)
				require.Equal(t, payload+"\n", rec.Body.String())
			} else {
				require.NoError(t, err, "valid finish_reason plus clean EOF remains accepted")
				require.Equal(t, input, rec.Body.String(), "successful EOF must preserve original bytes")
			}
		}
	}
}

func TestGatewayStreamIntegrityRawWithheldUnmeteredEOF(t *testing.T) {
	for _, payload := range []string{
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n",
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n",
	} {
		c, rec := integrityContext()
		result, err := (&OpenAIGatewayService{}).streamRawChatCompletions(c, integrityResponse(io.NopCloser(strings.NewReader(payload))), rawChatCompletionsTestAccount(), "m", "m", "m", nil, nil, time.Now(), openAISilentRefusalMinRequestBodyBytes)
		require.Error(t, err)
		require.Nil(t, result)
		var failover *UpstreamFailoverError
		require.True(t, errors.As(err, &failover), "withheld unmetered pre-output and silent-refusal retries remain compatible")
		require.Empty(t, rec.Body.String())
	}
}
