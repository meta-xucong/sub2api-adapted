//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
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

// These transport regressions compare Smart Router off/on with in-memory
// upstreams. Native image timeout classification must not change ordinary
// text/video forwarding, usage or error behavior in either scheduler mode.
func TestSmartRouterImageRecoveryPreservesTextCompatibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, route := range []string{"responses_passthrough", "chat_raw"} {
		for _, stream := range []bool{false, true} {
			for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
				t.Run(fmt.Sprintf("%s/stream=%t/status=%d", route, stream, status), func(t *testing.T) {
					var baseline []any
					for _, enabled := range []bool{false, true} {
						t.Run(fmt.Sprintf("smart_router=%t", enabled), func(t *testing.T) {
							body := []byte(fmt.Sprintf(`{"model":"gpt-5.4","input":"hello","stream":%t}`, stream))
							path, contentType := "/v1/responses", "application/json"
							response := `{"id":"resp_mock","status":"completed","model":"gpt-5.4","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":3,"output_tokens":2}}`
							if stream {
								contentType = "text/event-stream"
								response = "data: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"
							}
							if route == "chat_raw" {
								path = "/v1/chat/completions"
								body = []byte(fmt.Sprintf(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":%t}`, stream))
								response = `{"id":"chat_mock","object":"chat.completion","model":"gpt-5.4","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`
								if stream {
									response = "data: {\"id\":\"chat_mock\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-5.4\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n"
								}
							}
							if status == http.StatusBadRequest {
								contentType = "application/json"
								response = `{"error":{"code":"image_poll_timeout","message":"poll timed out"}}`
							}
							recorder := httptest.NewRecorder()
							c, _ := gin.CreateTestContext(recorder)
							c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
							c.Request.Header.Set("Content-Type", "application/json")
							upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(response))}}
							cfg := rawChatCompletionsTestConfig()
							cfg.Gateway.SmartRouter.Enabled = enabled
							svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
							account := rawChatCompletionsTestAccount()
							var result *OpenAIForwardResult
							var err error
							if route == "chat_raw" {
								result, err = svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
							} else {
								result, err = svc.forwardOpenAIPassthrough(context.Background(), c, account, body, body, "gpt-5.4", false, nil, stream, time.Now())
							}
							require.Len(t, upstream.requests, 1, "no image-only retry on a text route")
							if status == http.StatusOK {
								require.NoError(t, err)
								require.NotNil(t, result)
								require.False(t, result.ImageRequest, "ordinary text must not be classified as image generation")
								require.Equal(t, 3, result.Usage.InputTokens)
								require.Equal(t, 2, result.Usage.OutputTokens)
								require.Contains(t, recorder.Body.String(), "ok")
							} else {
								require.Error(t, err)
								var failover *UpstreamFailoverError
								require.NotErrorAs(t, err, &failover)
							}
							require.Equal(t, status, recorder.Code)
							snapshot := []any{recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String(), upstream.lastReq.URL.String(), string(upstream.lastBody), imagePollTimeoutCompatibilityResult(result)}
							if enabled {
								require.Equal(t, baseline, snapshot, "Smart Router must preserve text transport behavior")
							} else {
								baseline = snapshot
							}
						})
					}
				})
			}
		}
	}
}

func TestSmartRouterImageRecoveryPreservesVideoCompatibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, provider := range []string{"relay", "wokey"} {
		for _, endpoint := range []GrokMediaEndpoint{GrokMediaEndpointVideosGenerations, GrokMediaEndpointVideoStatus, GrokMediaEndpointVideoContent} {
			for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
				t.Run(fmt.Sprintf("%s/%s/status=%d", provider, endpoint, status), func(t *testing.T) {
					var baseline []any
					for _, enabled := range []bool{false, true} {
						t.Run(fmt.Sprintf("smart_router=%t", enabled), func(t *testing.T) {
							account := grokMediaContentTestAccount()
							if provider == "wokey" {
								account.Credentials["base_url"] = "https://api.wokey.ai/v1"
							}
							body := []byte(`{"model":"grok-imagine-video-1.5","prompt":"move","resolution":"720p","duration":5}`)
							response := `{"request_id":"task-1","status":"processing"}`
							contentType := "application/json"
							if endpoint == GrokMediaEndpointVideoStatus {
								response = `{"request_id":"task-1","status":"done","model":"grok-imagine-video-1.5","video":{"url":"https://vidgen.x.ai/task-1.mp4","duration":5}}`
								if provider == "wokey" {
									response = `{"request_id":"task-1","status":"completed","model":"grok-imagine-video-1.5","content_url":"https://api.wokey.ai/v1/videos/task-1/content","video":{"duration":5}}`
								}
							}
							if endpoint == GrokMediaEndpointVideoContent {
								contentType, response = "video/mp4", "mock-video-bytes"
							}
							if status == http.StatusBadRequest {
								contentType, response = "application/json", `{"error":{"code":"image_poll_timeout","message":"poll timed out"}}`
							}
							upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(response))}}
							expectedCalls := 1
							if endpoint == GrokMediaEndpointVideoContent {
								upstream.responses = []*http.Response{grokMediaContentStatusResponse(`{"status":"completed"}`), upstream.resp}
								expectedCalls = 2
							}
							cfg := rawChatCompletionsTestConfig()
							cfg.Gateway.SmartRouter.Enabled = enabled
							svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
							c, recorder := grokMediaContentTestContext(endpoint.httpMethod(), "https://client.example/v1/videos/task-1", nil)
							result, err := svc.ForwardGrokMedia(context.Background(), c, account, endpoint, "task-1", body, "application/json")
							require.Len(t, upstream.requests, expectedCalls, "image-only retry must not affect video")
							if status == http.StatusOK {
								require.NoError(t, err)
								require.NotNil(t, result)
								require.False(t, result.ImageRequest)
								if endpoint == GrokMediaEndpointVideosGenerations {
									require.Zero(t, result.VideoCount, "video creation defers billing until completion")
									require.Equal(t, "task-1", result.ResponseID)
								}
								if endpoint == GrokMediaEndpointVideoStatus {
									require.Equal(t, 1, result.VideoCount)
									require.Equal(t, "task-1", result.ResponseID)
									require.Equal(t, 5, result.VideoDurationSeconds)
								}
								if endpoint == GrokMediaEndpointVideoContent {
									require.Equal(t, "mock-video-bytes", recorder.Body.String())
								}
							} else {
								require.Error(t, err)
								var failover *UpstreamFailoverError
								require.NotErrorAs(t, err, &failover)
							}
							require.Equal(t, status, recorder.Code)
							snapshot := []any{recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String(), upstream.lastReq.URL.String(), string(upstream.lastBody), imagePollTimeoutCompatibilityResult(result)}
							if enabled {
								require.Equal(t, baseline, snapshot, "Smart Router must preserve video transport behavior")
							} else {
								baseline = snapshot
							}
						})
					}
				})
			}
		}
	}
}

func imagePollTimeoutCompatibilityResult(result *OpenAIForwardResult) []any {
	if result == nil {
		return nil
	}
	return []any{result.Usage, result.Model, result.BillingModel, result.UpstreamModel, result.ResponseID, result.ImageRequest, result.ImageCount, result.VideoCount, result.VideoResolution, result.VideoDurationSeconds}
}

// Explicit image_generation Responses traffic uses the image bridge and is
// always uses native image timeout retries, independently of Smart Router.
func TestSmartRouterImageRecoveryPreservesResponsesImageBridge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("smart_router=%t", enabled), func(t *testing.T) {
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Gateway.SmartRouter.Enabled = enabled
			errorBody := `{"error":{"code":"image_poll_timeout","message":"poll timed out"}}`
			upstream := &responsesImageBridgeStatusHTTPUpstream{statusCode: http.StatusBadRequest, body: errorBody}
			svc := NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
			body := []byte(`{"model":"gpt-image-2.5","input":"draw a product","tools":[{"type":"image_generation"}]}`)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			account := &Account{ID: 41, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "mock-only", "base_url": "https://images.example/v1"}}
			result, err := svc.ForwardResponsesImageBridge(context.Background(), c, account, body)
			require.Error(t, err)
			require.Equal(t, 1, upstream.calls)
			var failover *UpstreamFailoverError
			require.Nil(t, result)
			require.ErrorAs(t, err, &failover)
			require.Equal(t, openAIImageTimeoutReason, failover.Reason)
			require.Equal(t, http.StatusBadRequest, failover.StatusCode)
			require.Equal(t, errorBody, string(failover.ResponseBody))
			require.False(t, c.Writer.Written(), "the outer handler still owns failover")
			require.Empty(t, recorder.Body.String())
		})
	}
}
