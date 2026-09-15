package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIStreamFailedEventSemanticStatus_RecognizesPendingRequests(t *testing.T) {
	payload := []byte(`{"type":"response.failed","response":{"error":{"type":"server_error","code":"server_error","message":"Too many pending requests, please retry later"}}}`)
	require.Equal(t, http.StatusTooManyRequests, openAIStreamFailedEventSemanticStatus(payload, "Too many pending requests, please retry later"))
	invalidRequestPayload := []byte(`{"type":"response.failed","response":{"error":{"type":"invalid_request_error","message":"Too many pending requests"}}}`)
	require.Equal(t, http.StatusTooManyRequests, openAIStreamFailedEventSemanticStatus(invalidRequestPayload, "Too many pending requests"))
}

func TestMarkOpenAICompactFailedEventRecordsSemanticFailureAfterHTTP200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
	payload := []byte(`{"type":"response.failed","response":{"error":{"type":"server_error","code":"server_error","message":"Too many pending requests: https://upstream.invalid/path?key=secret-value"}}}`)

	markOpenAICompactFailedEvent(c, payload, "Too many pending requests: https://upstream.invalid/path?key=secret-value")
	streamErr, ok := GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, http.StatusTooManyRequests, streamErr.IntendedStatus)
	require.Equal(t, "server_error", streamErr.Code)
	require.True(t, streamErr.CountTowardsSLA)
	require.NotContains(t, streamErr.Message, "secret-value")
	require.Contains(t, streamErr.Message, "key=***")
}

func TestHandleStreamingResponse_CompactHTTP200FailedEventMarksOutcome(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.output_text.delta","delta":"partial"}`,
			"",
			`data: {"type":"response.failed","response":{"error":{"type":"server_error","code":"server_error","message":"Too many pending requests"}}}`,
			"",
		}, "\n"))),
	}

	_, err := (&OpenAIGatewayService{}).handleStreamingResponse(
		context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "gpt-5.5", "gpt-5.5",
	)
	require.Error(t, err)
	require.Equal(t, http.StatusOK, rec.Code, "wire status remains committed at 200")
	streamErr, ok := GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, http.StatusTooManyRequests, streamErr.IntendedStatus)
	require.Equal(t, "server_error", streamErr.Code)
	require.True(t, streamErr.CountTowardsSLA)
}

func TestMarkOpenAICompactFailedEventPreservesDeterministic4xxCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
	payload := []byte(`{"type":"response.failed","response":{"error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"prompt is too large"}}}`)

	markOpenAICompactFailedEvent(c, payload, "prompt is too large")
	streamErr, ok := GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, http.StatusBadRequest, streamErr.IntendedStatus)
	require.Equal(t, "context_length_exceeded", streamErr.Code)
	require.False(t, streamErr.CountTowardsSLA)
}
