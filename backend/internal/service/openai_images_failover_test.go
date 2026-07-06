package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestShouldFailoverOpenAIImagesWrappedUpstreamError(t *testing.T) {
	body := []byte(`{"error":{"code":"bad_response_status_code","message":"openai_error","type":"bad_response_status_code"}}`)

	require.True(t, shouldFailoverOpenAIImagesWrappedUpstreamError(http.StatusBadRequest, body))
}

func TestShouldFailoverOpenAIImagesWrappedUpstreamErrorRejectsUserBadRequest(t *testing.T) {
	body := []byte(`{"error":{"code":"invalid_request_error","message":"image is required","type":"invalid_request_error"}}`)

	require.False(t, shouldFailoverOpenAIImagesWrappedUpstreamError(http.StatusBadRequest, body))
	require.False(t, shouldFailoverOpenAIImagesWrappedUpstreamError(http.StatusForbidden, body))
}

func TestNewOpenAIImagesNoOutputFailoverError(t *testing.T) {
	err := error(newOpenAIImagesNoOutputFailoverError(nil))

	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.Contains(t, string(failoverErr.ResponseBody), "image_output_missing")
}

func TestNewOpenAIImagesNoOutputFailoverErrorSummarizesSSE(t *testing.T) {
	body := []byte("event: response.created\n" +
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_secret\",\"status\":\"in_progress\",\"model\":\"gpt-5.4-mini-2026-03-17\",\"output\":[],\"tools\":[{\"type\":\"image_generation\",\"model\":\"gpt-image-2\"}]}}\n\n" +
		"event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-5.4-mini-2026-03-17\",\"output\":[],\"tools\":[{\"type\":\"image_generation\",\"model\":\"gpt-image-2\"}]}}\n\n")

	failoverErr := newOpenAIImagesNoOutputFailoverError(body)
	summary := gjson.GetBytes(failoverErr.ResponseBody, "error.summary")

	require.Equal(t, "image_output_missing", gjson.GetBytes(failoverErr.ResponseBody, "error.code").String())
	require.True(t, summary.Get("completed").Bool())
	require.False(t, summary.Get("has_image_generation_call").Bool())
	require.Equal(t, "completed", summary.Get("response_status").String())
	require.Equal(t, "gpt-5.4-mini-2026-03-17", summary.Get("upstream_model").String())
	require.Equal(t, "gpt-image-2", summary.Get("tool_model").String())
	require.Equal(t, int64(0), summary.Get("output_count").Int())
	require.NotContains(t, string(failoverErr.ResponseBody), "resp_secret")
}
