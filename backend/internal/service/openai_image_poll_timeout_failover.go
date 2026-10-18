package service

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

const OpenAIImagePollTimeoutFailoverReason GatewayFailureReason = "openai_image_poll_timeout"

// isOpenAIImagePollTimeoutFailover narrowly recognizes the structured error
// returned with the upstream HTTP status. It intentionally does not inspect
// synthesized SSE/model-text errors or nested JSON embedded in error.message.
func (s *OpenAIGatewayService) isOpenAIImagePollTimeoutFailover(statusCode int, body []byte) bool {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.ImagePollTimeoutFailoverEnabled || statusCode != http.StatusBadRequest {
		return false
	}
	if !gjson.ValidBytes(body) {
		return false
	}
	for _, path := range []string{"error.code", "response.error.code", "code"} {
		if strings.TrimSpace(gjson.GetBytes(body, path).String()) == "image_poll_timeout" {
			return true
		}
	}
	return false
}

func newOpenAIImagePollTimeoutFailover(body []byte, headers http.Header) *UpstreamFailoverError {
	return &UpstreamFailoverError{
		StatusCode:      http.StatusBadRequest,
		ResponseBody:    append([]byte(nil), body...),
		ResponseHeaders: headers.Clone(),
		Reason:          OpenAIImagePollTimeoutFailoverReason,
	}
}
