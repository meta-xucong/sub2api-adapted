//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestOpenAIResponsesEmptyCompletedFailsOver verifies that a Responses stream
// ending with an empty response.completed (no output, no usage, no error) is
// turned into a failover error instead of a successful empty reply (issue
// #5009).
func TestOpenAIResponsesEmptyCompletedFailsOver(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_empty\",\"object\":\"response\",\"status\":\"in_progress\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_empty\",\"object\":\"response\",\"status\":\"completed\"}}\n\n",
		)),
	}}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	c, recorder := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
	account := newOpenAIImageGenerationControlTestAccount()
	account.Extra = map[string]any{"openai_passthrough": true}

	body := []byte(`{
		"model":"gpt-5.6-sol",
		"stream":true,
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}]
	}`)

	_, err := svc.Forward(context.Background(), c, account, body)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr), "empty completed must produce UpstreamFailoverError, got: %v", err)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.Empty(t, recorder.Body.String(), "no empty success stream may reach the client")
}

// TestOpenAIResponsesEmptyCompletedWithOutputSucceeds ensures streams with real
// semantic output are untouched.
func TestOpenAIResponsesEmptyCompletedWithOutputSucceeds(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_ok\",\"object\":\"response\",\"status\":\"in_progress\"}}\n\n" +
				"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"object\":\"response\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":5,\"total_tokens\":15}}}\n\n",
		)),
	}}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	c, recorder := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
	account := newOpenAIImageGenerationControlTestAccount()
	account.Extra = map[string]any{"openai_passthrough": true}

	body := []byte(`{
		"model":"gpt-5.6-sol",
		"stream":true,
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}]
	}`)

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, recorder.Body.String(), "hello")
	require.NotNil(t, result.Usage)
	require.Equal(t, 10, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
}

// TestOpenAIResponsesEmptyCompletedWithInputUsageFailsOver verifies that
// input-only usage cannot turn an empty Responses completion into a success.
func TestOpenAIResponsesEmptyCompletedWithInputUsageFailsOver(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_usage\",\"object\":\"response\",\"status\":\"in_progress\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_usage\",\"object\":\"response\",\"status\":\"completed\",\"usage\":{\"input_tokens\":3,\"output_tokens\":0,\"total_tokens\":3}}}\n\n",
		)),
	}}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	c, recorder := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
	account := newOpenAIImageGenerationControlTestAccount()
	account.Extra = map[string]any{"openai_passthrough": true}

	body := []byte(`{
		"model":"gpt-5.6-sol",
		"stream":true,
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}]
	}`)

	_, err := svc.Forward(context.Background(), c, account, body)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr), "input-only usage with no output must produce UpstreamFailoverError, got: %v", err)
	require.Empty(t, recorder.Body.String(), "no empty success stream may reach the client")
}

func TestOpenAIResponsesEmptyCompletedWithInputUsageFailsOverOnNativeAndPassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		output string
	}{
		{name: "empty_array", output: "[]"},
		{name: "null_output", output: "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, path := range []struct {
				name string
				run  func(*OpenAIGatewayService, *gin.Context, *http.Response, *Account) error
			}{
				{
					name: "native",
					run: func(svc *OpenAIGatewayService, c *gin.Context, resp *http.Response, account *Account) error {
						_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
						return err
					},
				},
				{
					name: "passthrough",
					run: func(svc *OpenAIGatewayService, c *gin.Context, resp *http.Response, account *Account) error {
						_, err := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
						return err
					},
				},
			} {
				t.Run(path.name, func(t *testing.T) {
					stream := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_usage\",\"object\":\"response\",\"status\":\"in_progress\"}}\n\n" +
						"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_usage\",\"object\":\"response\",\"status\":\"completed\",\"output\":" + tc.output + ",\"usage\":{\"input_tokens\":58873,\"output_tokens\":0,\"total_tokens\":58873}}}\n\n"
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(stream))}
					svc := &OpenAIGatewayService{cfg: &config.Config{}}
					err := path.run(svc, c, resp, &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth})
					var failoverErr *UpstreamFailoverError
					require.ErrorAs(t, err, &failoverErr)
					require.False(t, rec.Flushed)
					require.Empty(t, rec.Body.String(), "empty success output must not be sent before account failover")
				})
			}
		})
	}
}

func TestOpenAIResponsesEmptyTerminalAfterMeaningfulDeltaDoesNotReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stream := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_partial\",\"object\":\"response\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"already sent\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_partial\",\"object\":\"response\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":58873,\"output_tokens\":0,\"total_tokens\":58873}}}\n\n"
	for _, path := range []struct {
		name string
		run  func(*OpenAIGatewayService, *gin.Context, *http.Response, *Account) error
	}{
		{
			name: "native",
			run: func(svc *OpenAIGatewayService, c *gin.Context, resp *http.Response, account *Account) error {
				_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
				return err
			},
		},
		{
			name: "passthrough",
			run: func(svc *OpenAIGatewayService, c *gin.Context, resp *http.Response, account *Account) error {
				_, err := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
				return err
			},
		},
	} {
		t.Run(path.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(stream))}
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			err := path.run(svc, c, resp, &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth})
			require.NoError(t, err, "a stream with prior semantic output must not be failed over")
			require.Contains(t, rec.Body.String(), "already sent")
			require.NotContains(t, rec.Body.String(), "upstream_empty_response")
		})
	}
}

func TestOpenAIResponsesCompletedEventIsEmpty(t *testing.T) {
	cases := []struct {
		name  string
		data  string
		usage *OpenAIUsage
		want  bool
	}{
		{
			name: "bare completed",
			data: `{"type":"response.completed"}`,
			want: true,
		},
		{
			name: "completed with empty output array",
			data: `{"type":"response.completed","response":{"id":"r1","status":"completed","output":[]}}`,
			want: true,
		},
		{
			name: "completed with usage",
			data: `{"type":"response.completed","response":{"id":"r1","status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`,
			want: false,
		},
		{
			name: "completed with input-only usage",
			data: `{"type":"response.completed","response":{"id":"r1","status":"completed","output":[],"usage":{"input_tokens":58873,"output_tokens":0,"total_tokens":58873}}}`,
			want: true,
		},
		{
			name: "completed with empty assistant message shell",
			data: `{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":""}]}],"usage":{"input_tokens":5,"output_tokens":0}}}`,
			want: true,
		},
		{
			name: "completed with refusal",
			data: `{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"I cannot help with that."}]}],"usage":{"input_tokens":5,"output_tokens":0}}}`,
			want: false,
		},
		{
			name: "completed with empty refusal part remains semantic",
			data: `{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":""}]}],"usage":{"input_tokens":5,"output_tokens":0}}}`,
			want: false,
		},
		{
			name: "completed with empty tool arguments remains semantic",
			data: `{"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","name":"lookup","arguments":"{}"}],"usage":{"input_tokens":5,"output_tokens":0}}}`,
			want: false,
		},
		{
			name: "completed with reasoning item remains semantic",
			data: `{"type":"response.completed","response":{"status":"completed","output":[{"type":"reasoning","content":[{"type":"reasoning_text","text":"thinking"}]}],"usage":{"input_tokens":5,"output_tokens":0}}}`,
			want: false,
		},
		{
			name: "incomplete terminal is not treated as empty success",
			data: `{"type":"response.done","response":{"status":"incomplete","output":[],"usage":{"input_tokens":5,"output_tokens":0}}}`,
			want: false,
		},
		{
			name: "completed with error",
			data: `{"type":"response.completed","response":{"id":"r1","status":"completed","error":{"code":"x"}}}`,
			want: false,
		},
		{
			name: "completed with output item",
			data: `{"type":"response.completed","response":{"id":"r1","status":"completed","output":[{"type":"message","id":"msg_1"}]}}`,
			want: false,
		},
		{
			name: "accumulated usage",
			data: `{"type":"response.completed"}`,
			usage: &OpenAIUsage{
				InputTokens:  7,
				OutputTokens: 2,
			},
			want: false,
		},
		{
			name:  "accumulated input-only usage",
			data:  `{"type":"response.completed"}`,
			usage: &OpenAIUsage{InputTokens: 7, CacheReadInputTokens: 3},
			want:  true,
		},
		{
			name: "invalid json",
			data: `{"type":`,
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, openAIResponsesCompletedEventIsEmpty([]byte(tc.data), tc.usage))
		})
	}
}

func TestOpenAIImageGenerationCompletedWithoutArtifactIsNotRetried(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"resp_missing_image","status":"completed","output":[{"type":"image_generation_call"}],"usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15}}`)),
	}}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	c, recorder := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
	account := newOpenAIImageGenerationControlTestAccount()
	account.Extra = map[string]any{"openai_passthrough": true}
	body := []byte(`{"model":"gpt-5.6-sol","stream":false,"tools":[{"type":"image_generation","model":"gpt-image-2.5"}],"tool_choice":{"type":"image_generation"},"input":"draw a ceramic cup"}`)

	result, err := svc.Forward(context.Background(), c, account, body)

	require.ErrorIs(t, err, ErrOpenAIImageGenerationOutputMissing)
	require.NotNil(t, result)
	require.NotNil(t, result.Usage)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Len(t, upstream.requests, 1, "a completed upstream image request without an artifact must not be retried")
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, recorder.Body.String(), "without an image result")
}

func TestOpenAIImageGenerationCompletedWithoutArtifactStreamIsNotRetried(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		name := "native"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"resp-missing-image"}},
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_missing_image\",\"status\":\"in_progress\"}}\n\n" +
						"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_missing_image\",\"status\":\"completed\",\"output\":[{\"type\":\"image_generation_call\"}],\"usage\":{\"input_tokens\":12,\"output_tokens\":3,\"total_tokens\":15}}}\n\n",
				)),
			}}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			c, recorder := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
			account := newOpenAIImageGenerationControlTestAccount()
			if passthrough {
				account.Extra = map[string]any{"openai_passthrough": true}
			}
			body := []byte(`{"model":"gpt-5.6-sol","stream":true,"tools":[{"type":"image_generation","model":"gpt-image-2.5"}],"tool_choice":{"type":"image_generation"},"input":"draw a ceramic cup"}`)

			result, err := svc.Forward(context.Background(), c, account, body)

			require.ErrorIs(t, err, ErrOpenAIImageGenerationOutputMissing)
			require.NotNil(t, result)
			require.Equal(t, 12, result.Usage.InputTokens, "available upstream usage must be preserved")
			require.Len(t, upstream.requests, 1, "completed-without-artifact must never trigger another upstream attempt")
			require.Contains(t, recorder.Body.String(), "without an image result")
		})
	}
}

func TestOpenAIImageGenerationCompletedWithoutArtifactSSEToJSONIsNotRetried(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		name := "native"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"resp-missing-image-sse"}},
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_missing_image\",\"status\":\"completed\",\"output\":[{\"type\":\"image_generation_call\"}],\"usage\":{\"input_tokens\":8,\"output_tokens\":2,\"total_tokens\":10}}}\n\n",
				)),
			}}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			c, recorder := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
			account := newOpenAIImageGenerationControlTestAccount()
			if passthrough {
				account.Extra = map[string]any{"openai_passthrough": true}
			}
			body := []byte(`{"model":"gpt-5.6-sol","stream":false,"tools":[{"type":"image_generation","model":"gpt-image-2.5"}],"tool_choice":{"type":"image_generation"},"input":"draw a ceramic cup"}`)

			result, err := svc.Forward(context.Background(), c, account, body)

			require.ErrorIs(t, err, ErrOpenAIImageGenerationOutputMissing)
			require.NotNil(t, result)
			require.Equal(t, 8, result.Usage.InputTokens)
			require.Len(t, upstream.requests, 1, "SSE-to-JSON conversion must not replay a completed image request")
			require.Equal(t, http.StatusBadGateway, recorder.Code)
			require.Contains(t, recorder.Body.String(), "without an image result")
		})
	}
}

func TestOpenAIAdvertisedImageToolDoesNotFailOrdinaryTextStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_text\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}],\"usage\":{\"input_tokens\":4,\"output_tokens\":1,\"total_tokens\":5}}}\n\n",
		)),
	}}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	c, recorder := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
	account := newOpenAIImageGenerationControlTestAccount()
	account.Extra = map[string]any{"openai_passthrough": true}
	body := []byte(`{"model":"gpt-5.6-sol","stream":true,"tools":[{"type":"image_generation"}],"input":"say hello"}`)

	result, err := svc.Forward(context.Background(), c, account, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 4, result.Usage.InputTokens)
	require.Len(t, upstream.requests, 1)
	require.Contains(t, recorder.Body.String(), "hello")
	require.NotContains(t, recorder.Body.String(), "image_output_missing")
}

func TestOpenAIImageGenerationMissingArtifactClassifier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "completed image call without result",
			body: `{"model":"gpt-5.6-sol","tools":[{"type":"image_generation","model":"gpt-image-2.5"}],"status":"completed","output":[{"type":"image_generation_call"}]}`,
			want: true,
		},
		{
			name: "completed image call with result",
			body: `{"model":"gpt-5.6-sol","tools":[{"type":"image_generation","model":"gpt-image-2.5"}],"status":"completed","output":[{"type":"image_generation_call","result":"aGVsbG8="}]}`,
			want: false,
		},
		{
			name: "incomplete image response",
			body: `{"model":"gpt-image-2.5","status":"incomplete","output":[]}`,
			want: false,
		},
		{
			name: "ordinary text response with advertised image tool",
			body: `{"model":"gpt-5.6-sol","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}]}`,
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
			body := []byte(tc.body)
			setOpenAIImageGenerationOutputRequired(c, isOpenAIImageGenerationOutputRequired("gpt-5.6-sol", body))
			require.Equal(t, tc.want, openAIImageGenerationOutputMissing(c, body))
		})
	}
}
