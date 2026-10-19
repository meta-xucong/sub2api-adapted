//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func streamBillingAdapters() map[string]func(*gin.Context, *http.Response) (*OpenAIForwardResult, error) {
	return map[string]func(*gin.Context, *http.Response) (*OpenAIForwardResult, error){
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
}

func TestCCFailureBillingEligibility(t *testing.T) {
	const broken = "data: {broken}\n\n"
	const metadata = "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}]}\n\n"
	for adapter, forward := range streamBillingAdapters() {
		for _, tc := range []struct {
			name, prefix     string
			tail             error
			cancel, billable bool
		}{
			{name: "first_invalid"},
			{name: "heartbeat", prefix: ": heartbeat\n\n"},
			{name: "metadata", prefix: metadata},
			{name: "empty_usage", prefix: "data: {\"choices\":[],\"usage\":{}}\n\n"},
			{name: "zero_usage", prefix: "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":0,\"cache_creation_input_tokens\":0,\"prompt_tokens_details\":{\"cached_tokens\":0,\"image_tokens\":0},\"completion_tokens_details\":{\"image_tokens\":0}}}\n\n"},
			{name: "negative_usage", prefix: "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":-1,\"completion_tokens\":-1,\"completion_tokens_details\":{\"image_tokens\":-1}}}\n\n"},
			{name: "malformed_tool_values", prefix: "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":123,\"arguments\":{}}}]}}]}\n\n"},
			{name: "cancelled_empty", cancel: true},
			{name: "read_error_empty", tail: io.ErrUnexpectedEOF},
			{name: "cancelled_empty_read", tail: context.Canceled, cancel: true},
			{name: "deadline_empty_read", tail: context.DeadlineExceeded},
			{name: "cancelled_metadata_eof", prefix: metadata, tail: io.EOF, cancel: true},
			{name: "read_error_heartbeat", prefix: ": ping\n\n", tail: io.ErrUnexpectedEOF},
			{name: "read_error_after_finish", prefix: "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n", tail: io.ErrUnexpectedEOF},
			{name: "positive_usage", prefix: "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\n", billable: true},
			{name: "cancelled_positive_usage", prefix: "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\n", cancel: true, billable: true},
			{name: "cache_read_only", prefix: "data: {\"choices\":[],\"usage\":{\"prompt_tokens_details\":{\"cached_tokens\":2}}}\n\n", billable: true},
			{name: "cache_creation_only", prefix: "data: {\"choices\":[],\"usage\":{\"cache_creation_input_tokens\":2}}\n\n", billable: true},
			{name: "image_input_only", prefix: "data: {\"choices\":[],\"usage\":{\"prompt_tokens_details\":{\"image_tokens\":2}}}\n\n", billable: true},
			{name: "image_output_only", prefix: "data: {\"choices\":[],\"usage\":{\"completion_tokens_details\":{\"image_tokens\":2}}}\n\n", billable: true},
			{name: "text_without_usage", prefix: "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n", billable: true},
			{name: "reasoning_without_usage", prefix: "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"partial\"}}]}\n\n", billable: true},
			{name: "tool_without_usage", prefix: "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"tool_1\",\"type\":\"function\",\"function\":{\"name\":\"search\",\"arguments\":\"{\"}}]}}]}\n\n", billable: true},
			{name: "filter_then_error", prefix: "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"content_filter\"}]}\n\n"},
		} {
			t.Run(adapter+"/"+tc.name, func(t *testing.T) {
				c, rec := integrityContext()
				if tc.cancel {
					ctx, cancel := context.WithCancel(c.Request.Context())
					cancel()
					c.Request = c.Request.WithContext(ctx)
				}
				payload := tc.prefix + broken
				var body = io.NopCloser(strings.NewReader(payload))
				if tc.tail != nil {
					body = &errTailReader{data: []byte(tc.prefix), err: tc.tail}
				}
				result, err := forward(c, integrityResponse(body))
				require.Error(t, err)
				var failover *UpstreamFailoverError
				require.Equal(t, adapter == "raw" && tc.name == "read_error_empty", errors.As(err, &failover), "qualification must preserve existing retry classification")
				if tc.billable {
					require.NotNil(t, result)
				} else {
					require.Nil(t, result, "error/metadata bytes and zero or negative counters are not billable output")
				}
				require.NotContains(t, rec.Body.String(), `"type":"response.completed"`)
				require.NotContains(t, rec.Body.String(), `"type":"message_stop"`)
			})
		}
	}
}

func TestCCFailureBillingDoesNotCountFailedWrites(t *testing.T) {
	const content = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"undelivered\"}}]}\n\ndata: {broken}\n\n"
	for adapter, forward := range streamBillingAdapters() {
		t.Run(adapter, func(t *testing.T) {
			c, _ := integrityContext()
			c.Writer = &responsesCompatFailAfterWriter{ResponseWriter: c.Writer, failAfter: 0}
			result, err := forward(c, integrityResponse(io.NopCloser(strings.NewReader(content))))
			require.Error(t, err)
			require.Nil(t, result, "failed write is not model delivery")
			require.True(t, IsOpenAIStreamClientDisconnect(err))
			if adapter == "raw" {
				var upstream *openAIUpstreamStreamReadError
				require.ErrorAs(t, err, &upstream, "disconnect metadata must preserve upstream error classification")
			}
		})
	}
}

func TestResponsesInvalidToolArgumentsBillingEligibility(t *testing.T) {
	const payload = "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
	for _, delivered := range []bool{false, true} {
		t.Run(map[bool]string{false: "buffered_unnamed", true: "delivered_named"}[delivered], func(t *testing.T) {
			c, rec := integrityContext()
			input := payload
			if delivered {
				input = strings.ReplaceAll(input, `"function":{"arguments"`, `"function":{"name":"search","arguments"`)
			}
			// Declared custom tools make the converter hold unnamed calls until their type is known.
			result, err := (&OpenAIGatewayService{}).streamChatCompletionsAsResponses(c, integrityResponse(io.NopCloser(strings.NewReader(input))), "m", map[string]bool{"custom": true}, nil, false, nil, "m", "m", nil, nil, time.Now())
			require.ErrorContains(t, err, "invalid JSON")
			if delivered {
				require.NotNil(t, result)
			} else {
				require.Nil(t, result)
			}
			require.NotContains(t, rec.Body.String(), `"type":"response.completed"`)
		})
	}
}

func TestStreamFailurePositiveUsageCounters(t *testing.T) {
	for _, usage := range []OpenAIUsage{
		{InputTokens: 1}, {OutputTokens: 1}, {CacheCreationInputTokens: 1}, {CacheReadInputTokens: 1},
		{ImageInputTokens: 1}, {ImageCacheReadTokens: 1}, {ImageOutputTokens: 1},
	} {
		require.True(t, openAIStreamFailureHasBillableEvidence(usage, false))
	}
	require.False(t, openAIStreamFailureHasBillableEvidence(OpenAIUsage{}, false))
	require.False(t, openAIStreamFailureHasBillableEvidence(OpenAIUsage{InputTokens: -1, OutputTokens: -1, CacheCreationInputTokens: -1, CacheReadInputTokens: -1, ImageInputTokens: -1, ImageCacheReadTokens: -1, ImageOutputTokens: -1}, false))
	require.True(t, openAIStreamFailureHasBillableEvidence(OpenAIUsage{}, true), "preserve existing delivered-output/no-usage policy")
}

func TestCCBufferedFailureBillingEligibility(t *testing.T) {
	for _, messages := range []bool{false, true} {
		t.Run(map[bool]string{false: "responses", true: "messages"}[messages], func(t *testing.T) {
			c, rec := integrityContext()
			resp := integrityResponse(io.NopCloser(strings.NewReader("{broken}")))
			var result *OpenAIForwardResult
			var err error
			if messages {
				result, err = (&OpenAIGatewayService{}).bufferChatCompletionsAsAnthropic(c, resp, "m", "m", "m", nil, nil, time.Now())
			} else {
				result, err = (&OpenAIGatewayService{}).bufferChatCompletionsAsResponses(c, resp, "m", nil, nil, false, nil, "m", "m", nil, nil, time.Now())
			}
			require.Error(t, err)
			require.Nil(t, result)
			require.Equal(t, http.StatusBadGateway, rec.Code)
		})
	}
}

func TestRawFailurePreservesReasoningAliases(t *testing.T) {
	for _, field := range []string{"reasoning", "reasoning_content", "reasoning_summary"} {
		t.Run(field, func(t *testing.T) {
			c, rec := integrityContext()
			payload := `data: {"choices":[{"index":0,"delta":{"` + field + `":"partial reasoning"}}]}` + "\n\ndata: {broken}\n\n"
			result, err := streamBillingAdapters()["raw"](c, integrityResponse(io.NopCloser(strings.NewReader(payload))))
			require.Error(t, err)
			require.NotNil(t, result)
			require.Contains(t, rec.Body.String(), "partial reasoning")
		})
	}
}

type shortCCStreamWriter struct{ gin.ResponseWriter }

func (w shortCCStreamWriter) Write(p []byte) (int, error)       { return len(p) - 1, nil }
func (w shortCCStreamWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func TestCCFailureBillingDoesNotCountShortWrites(t *testing.T) {
	const payload = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"undelivered\"}}]}\n\ndata: {broken}\n\n"
	for adapter, forward := range streamBillingAdapters() {
		t.Run(adapter, func(t *testing.T) {
			c, _ := integrityContext()
			c.Writer = shortCCStreamWriter{c.Writer}
			result, err := forward(c, integrityResponse(io.NopCloser(strings.NewReader(payload))))
			require.Error(t, err)
			require.Nil(t, result, "a short write cannot count as complete semantic delivery")
			require.True(t, IsOpenAIStreamClientDisconnect(err))
		})
	}
}

func TestUnbillableStreamDisconnectPreservesError(t *testing.T) {
	original := newOpenAIUpstreamStreamReadError(io.ErrUnexpectedEOF)
	wrapped := openAIUnbillableStreamError(original, true)
	require.Equal(t, original.Error(), wrapped.Error())
	require.ErrorIs(t, wrapped, io.ErrUnexpectedEOF)
	var upstream *openAIUpstreamStreamReadError
	require.ErrorAs(t, wrapped, &upstream)
	require.True(t, IsOpenAIStreamClientDisconnect(wrapped))
	require.False(t, IsOpenAIStreamClientDisconnect(original))
	require.Same(t, original, openAIUnbillableStreamError(original, false))
}
