package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
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
