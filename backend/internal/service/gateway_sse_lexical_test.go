//go:build unit

package service

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

type bytewiseSSEReader struct{ io.Reader }

func (r bytewiseSSEReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}

func TestBoundedSSELexicalLines(t *testing.T) {
	for _, sep := range []string{"\n", "\r\n", "\r"} {
		for _, bytewise := range []bool{false, true} {
			t.Run(strings.ReplaceAll(strings.ReplaceAll(sep, "\r", "CR"), "\n", "LF")+map[bool]string{false: "/whole", true: "/split"}[bytewise], func(t *testing.T) {
				input := "\ufeff: ping" + sep + "data:  first" + sep + "data:\tsecond" + sep + "data" + sep + sep + "event: error" + sep + sep + "data: last"
				var reader io.Reader = strings.NewReader(input)
				if bytewise {
					reader = bytewiseSSEReader{reader}
				}
				c, _ := integrityContext()
				scanner := boundedGatewaySSEScanner(c, integrityResponse(io.NopCloser(reader)), nil)
				defer scanner.Close()
				frames := newBoundedGatewaySSEFrames(nil)
				var payloads []string
				for scanner.Scan() {
					frame, ready, err := frames.AddLine(scanner.Text())
					require.NoError(t, err)
					if ready {
						payloads = append(payloads, frame.Data)
					}
				}
				require.NoError(t, scanner.Err())
				if frame, ready := frames.Finish(); ready {
					payloads = append(payloads, frame.Data)
				}
				require.Equal(t, []string{" first\n\tsecond\n", "last"}, payloads)
			})
		}
	}
}

func TestBoundedSSELexicalInitialBOMOnly(t *testing.T) {
	frames := newBoundedGatewaySSEFrames(nil)
	_, ready, err := frames.AddLine("\ufeffdata: first")
	require.NoError(t, err)
	require.False(t, ready)
	frame, ready, err := frames.AddLine("")
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "first", frame.Data)
	_, _, err = frames.AddLine("\ufeffdata: ignored")
	require.NoError(t, err)
	_, ready, err = frames.AddLine("")
	require.NoError(t, err)
	require.False(t, ready)
	_, _, err = frames.AddLine("data: \ufeffretained")
	require.NoError(t, err)
	frame, ready, err = frames.AddLine("")
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "\ufeffretained", frame.Data)
}

func TestBoundedSSELexicalLineLimit(t *testing.T) {
	// The line limit counts content bytes, excluding CR/LF delimiters. Frame
	// accounting separately charges each data line plus its normalized newline.
	for _, sep := range []string{"\n", "\r\n", "\r", ""} {
		for _, size := range []int{15, 16, 17} {
			t.Run(strings.ReplaceAll(strings.ReplaceAll(sep, "\r", "CR"), "\n", "LF")+"/"+strings.Repeat("x", size), func(t *testing.T) {
				c, _ := integrityContext()
				cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: 16}}
				scanner := boundedGatewaySSEScanner(c, integrityResponse(io.NopCloser(bytewiseSSEReader{strings.NewReader(strings.Repeat("x", size) + sep)})), cfg)
				defer scanner.Close()
				if size <= 16 {
					require.True(t, scanner.Scan())
					require.Len(t, scanner.Text(), size)
					require.False(t, scanner.Scan())
					require.NoError(t, scanner.Err())
				} else {
					require.False(t, scanner.Scan())
					require.Error(t, scanner.Err())
				}
			})
		}
	}
}

func TestCCSSELexicalAdapters(t *testing.T) {
	const content = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\n"
	for adapter, forward := range streamBillingAdapters() {
		for _, sep := range []string{"\n", "\r\n", "\r"} {
			for _, prefix := range []string{"", "\ufeff"} {
				t.Run(adapter+"/"+strings.ReplaceAll(strings.ReplaceAll(sep, "\r", "CR"), "\n", "LF")+"/"+map[bool]string{false: "plain", true: "bom"}[prefix != ""], func(t *testing.T) {
					input := prefix + "event: error\n: no data field\n\n" + content + "data: [DONE]\n\n"
					c, rec := integrityContext()
					result, err := forward(c, integrityResponse(io.NopCloser(bytewiseSSEReader{strings.NewReader(strings.ReplaceAll(input, "\n", sep))})))
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, 7, result.Usage.InputTokens)
					require.Equal(t, 3, result.Usage.OutputTokens)
					if adapter == "raw" {
						require.Equal(t, input, rec.Body.String(), "raw preserves original fields/BOM but continues its existing LF-normalized forwarding")
					}
				})
			}
		}
	}
}

func TestCCSSEExplicitEmptyErrorAndBOMFailure(t *testing.T) {
	for adapter, forward := range streamBillingAdapters() {
		for _, payload := range []string{
			"event: error\ndata:\n\n", "event: error\ndata\n\n", "event: error\ndata", "\ufeffdata: {broken}\n\n", "\ufeffdata: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n",
		} {
			t.Run(adapter+"/"+payload, func(t *testing.T) {
				c, rec := integrityContext()
				_, err := forward(c, integrityResponse(io.NopCloser(strings.NewReader(payload))))
				require.Error(t, err)
				require.NotContains(t, rec.Body.String(), "data: [DONE]")
			})
		}
	}
}

func TestBoundedSSELexicalBOMPosition(t *testing.T) {
	for _, lines := range [][]string{
		{"\ufeff\ufeffdata: ignored", ""},
		{"", "\ufeffdata: ignored", ""},
		{": comment", "\ufeffdata: ignored", ""},
	} {
		frames := newBoundedGatewaySSEFrames(nil)
		for _, line := range lines {
			_, ready, err := frames.AddLine(line)
			require.NoError(t, err)
			require.False(t, ready, "only one BOM at byte zero is ignored")
		}
	}
}

type sseNoExtraRead struct {
	data  string
	reads int
}

func (r *sseNoExtraRead) Read(p []byte) (int, error) {
	r.reads++
	if r.data == "" {
		return 0, io.ErrUnexpectedEOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}
func (*sseNoExtraRead) Close() error { return nil }

func TestCCSSEStandaloneDoneAfterCR(t *testing.T) {
	for adapter, forward := range streamBillingAdapters() {
		t.Run(adapter, func(t *testing.T) {
			c, _ := integrityContext()
			body := &sseNoExtraRead{data: "data: [DONE]\r"}
			_, err := forward(c, integrityResponse(body))
			require.NoError(t, err)
			require.Equal(t, 1, body.reads, "terminal CR must not wait for another byte to decide CRLF")
		})
	}
}

func TestBoundedSSELexicalMaxIntConfiguration(t *testing.T) {
	c, _ := integrityContext()
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: int(^uint(0) >> 1)}}
	scanner := boundedGatewaySSEScanner(c, integrityResponse(io.NopCloser(strings.NewReader("data: small\n"))), cfg)
	defer scanner.Close()
	require.True(t, scanner.Scan())
	require.Equal(t, "data: small", scanner.Text())
	require.False(t, scanner.Scan())
	require.NoError(t, scanner.Err())
}

func TestRawNoDataErrorPreservesSilentRefusal(t *testing.T) {
	const empty = "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	for _, prefix := range []string{"", "event: error\n: no data\n\n", "event: reasoning\n\n"} {
		c, rec := integrityContext()
		result, err := (&OpenAIGatewayService{}).streamRawChatCompletions(c, integrityResponse(io.NopCloser(strings.NewReader(prefix+empty))), rawChatCompletionsTestAccount(), "m", "m", "m", nil, nil, time.Now(), openAISilentRefusalMinRequestBodyBytes)
		require.Error(t, err)
		require.Nil(t, result)
		var failover *UpstreamFailoverError
		require.ErrorAs(t, err, &failover)
		require.Contains(t, string(failover.ResponseBody), "openai_silent_refusal")
		require.Empty(t, rec.Body.String(), "no-data events must not release withheld output")
	}
}

func TestBoundedSSELexicalFrameLimit(t *testing.T) {
	for _, limit := range []int{4, 5, 6} {
		frames := newBoundedGatewaySSEFrames(&config.Config{Gateway: config.GatewayConfig{MaxLineSize: limit}})
		_, _, err := frames.AddLine("data")
		if limit < 5 {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		frame, ready, err := frames.AddLine("")
		require.NoError(t, err)
		require.True(t, ready)
		require.Empty(t, frame.Data)
	}
}

// Lexical field values stay exact, while provider protocol matching retains
// the pre-existing whitespace tolerance. This is not browser EventSource dispatch.
func TestBoundedSSELexicalEventWhitespacePreserved(t *testing.T) {
	frames := newBoundedGatewaySSEFrames(nil)
	_, _, err := frames.AddLine("event: \terror \t")
	require.NoError(t, err)
	_, _, err = frames.AddLine("data: {}")
	require.NoError(t, err)
	frame, ready, err := frames.AddLine("")
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "\terror \t", frame.EventType)
}

func TestCCSSEProviderErrorWhitespaceCompatibility(t *testing.T) {
	const empty = "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	for _, event := range []string{"error ", "\terror\t", " error "} {
		payload := "event: " + event + "\ndata: {}\n\n" + empty
		for adapter, forward := range streamBillingAdapters() {
			t.Run(adapter+"/"+event, func(t *testing.T) {
				c, rec := integrityContext()
				result, err := forward(c, integrityResponse(io.NopCloser(strings.NewReader(payload))))
				require.ErrorContains(t, err, "error event")
				require.Nil(t, result)
				require.NotContains(t, rec.Body.String(), "data: [DONE]")
				require.NotContains(t, rec.Body.String(), `"type":"response.completed"`)
			})
		}
		t.Run("raw_withheld/"+event, func(t *testing.T) {
			c, rec := integrityContext()
			result, err := (&OpenAIGatewayService{}).streamRawChatCompletions(c, integrityResponse(io.NopCloser(strings.NewReader(payload))), rawChatCompletionsTestAccount(), "m", "m", "m", nil, nil, time.Now(), openAISilentRefusalMinRequestBodyBytes)
			require.ErrorContains(t, err, "error event")
			require.Nil(t, result)
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover), "explicit provider errors must not become silent-refusal retries")
			require.NotContains(t, rec.Body.String(), "data: [DONE]")
		})
	}
}

func TestSSEProviderEventNameWhitespaceCompatibility(t *testing.T) {
	lines := strings.Split(miniAnthropicSSEStream(), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "event:") {
			lines[i] = "event: \t" + strings.TrimSpace(strings.TrimPrefix(line, "event:")) + " \t"
		}
	}
	payload := strings.Join(lines, "\n")
	for _, adapter := range []string{"anthropic_cc", "native_buffered", "native_stream"} {
		for _, mismatch := range []bool{false, true} {
			t.Run(adapter+map[bool]string{false: "/valid", true: "/mismatch"}[mismatch], func(t *testing.T) {
				input := payload
				if mismatch {
					input = strings.Replace(input, "event: \tmessage_start \t", "event: \tunknown \t", 1)
				}
				c, _ := integrityContext()
				resp := integrityResponse(io.NopCloser(strings.NewReader(input)))
				svc := &OpenAIGatewayService{}
				var err error
				switch adapter {
				case "anthropic_cc":
					err = scanAnthropicCCEvents(c, resp, nil, func(*apicompat.AnthropicStreamEvent, string) bool { return false })
				case "native_buffered":
					_, err = svc.handleResponsesBufferedFromNativeAnthropic(resp, c, "m", "m", "m", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
				case "native_stream":
					_, err = svc.handleResponsesStreamingFromNativeAnthropic(resp, c, "m", "m", "m", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
				}
				if mismatch {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
			})
		}
	}
	for _, event := range []string{"ping ", "\tping\t", " ping "} {
		require.Equal(t, ": ping\n\n", filterGrokPingTestInput(t, "event: "+event+"\ndata: {}\n\n"))
	}
}
