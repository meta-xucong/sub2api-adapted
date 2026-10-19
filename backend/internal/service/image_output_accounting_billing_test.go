package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Carry a real image through the production forwarding and RecordUsage paths.
// Repositories and HTTP are isolated mocks; no provider or balance is touched.
func TestImageOutputAccountingForwardingAndBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	png := completeAccountingPNG(t)
	for _, route := range []string{"native_json", "native_stream", "native_sse_buffered", "passthrough_json", "passthrough_stream", "passthrough_sse_buffered", "images_bridge_buffered", "images_bridge_stream", "responses_images_bridge"} {
		for _, mode := range []string{"image", "token"} {
			t.Run(route+"/"+mode, func(t *testing.T) {
				stream := strings.HasSuffix(route, "stream")
				item := map[string]any{"id": "ig_partial_image", "type": "image_generation_call", "status": "completed", "result": png, "revised_prompt": "render the literal label partial_image", "size": "1024x1024", "output_format": "png"}
				usage := map[string]any{"input_tokens": 100, "output_tokens": 50, "output_tokens_details": map[string]any{"image_tokens": 20}}
				response := map[string]any{"id": "resp_image", "object": "response", "status": "completed", "model": "gpt-5.4", "usage": usage, "output": []any{
					map[string]any{"id": "msg_1", "type": "message", "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Here is the partial_image label."}}}, item,
				}}
				responseBody := accountingJSON(t, response)
				contentType := "application/json"
				if stream || strings.Contains(route, "sse_buffered") || strings.HasPrefix(route, "images_bridge_") {
					contentType = "text/event-stream"
					responseBody = []byte("data: " + string(accountingJSON(t, map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_image", "status": "in_progress"}})) + "\n\n" +
						"data: " + string(accountingJSON(t, map[string]any{"type": "response.image_generation_call.partial_image", "item_id": "ig_partial_image", "partial_image_b64": png, "partial_image_index": 0})) + "\n\n" +
						"data: " + string(accountingJSON(t, map[string]any{"type": "response.output_item.done", "item": item})) + "\n\n" +
						"data: " + string(accountingJSON(t, map[string]any{"type": "response.completed", "response": response})) + "\n\n" +
						"data: [DONE]\n\n")
				}
				if route == "responses_images_bridge" {
					responseBody = accountingJSON(t, map[string]any{"data": []any{map[string]any{"b64_json": png, "revised_prompt": "partial_image", "size": "1024x1024"}}, "usage": usage})
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {contentType}, "X-Request-Id": {"accounting_" + route + "_" + mode}},
					Body:       io.NopCloser(bytes.NewReader(responseBody)),
				}}
				account := newOpenAIImageGenerationControlTestAccount()
				var result *OpenAIForwardResult
				var err error
				if strings.HasPrefix(route, "images_bridge_") {
					request := []byte(fmt.Sprintf(`{"model":"gpt-image-2","prompt":"partial_image","n":3,"size":"1024x1024","stream":%t,"response_format":"b64_json"}`, stream))
					c, rec := newOpenAIImagesTestContext(t, request)
					svc := newOpenAIImagesTestService(upstream)
					parsed, parseErr := svc.ParseOpenAIImagesRequest(c, request)
					require.NoError(t, parseErr)
					account = &Account{ID: 31, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "synthetic-token"}}
					// gpt-image-2 normally selects direct Images JSON. Exercise the
					// existing Responses compatibility branch with its SSE contract.
					result, err = svc.ForwardImages(withOpenAIImagesForceResponses(context.Background()), c, account, request, parsed, "")
					require.NoError(t, err)
					require.NotNil(t, upstream.lastReq)
					require.True(t, strings.HasSuffix(upstream.lastReq.URL.Path, "/responses"))
					require.Equal(t, http.StatusOK, rec.Code)
					require.Contains(t, rec.Body.String(), png)
				} else {
					request := []byte(fmt.Sprintf(`{"model":"gpt-5.4","stream":%t,"tools":[{"type":"image_generation","model":"gpt-image-2","size":"1024x1024"}],"input":"draw partial_image"}`, stream))
					c, rec := newOpenAIImageGenerationControlTestContext(true, "curl/8.0")
					svc := newOpenAIImageGenerationControlTestService(upstream)
					if route == "responses_images_bridge" {
						result, err = svc.ForwardResponsesImageBridge(context.Background(), c, account, request)
					} else {
						if strings.HasPrefix(route, "passthrough_") {
							account.Extra = map[string]any{"openai_passthrough": true}
						}
						result, err = svc.Forward(context.Background(), c, account, request)
						if result != nil {
							require.Equal(t, "gpt-image-2", result.BillingModel, "completed image must select the image tariff even when prompt contains partial_image")
						}
					}
					require.NoError(t, err)
					require.Equal(t, http.StatusOK, rec.Code)
					require.Contains(t, rec.Body.String(), png)
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, upstream.requests, 1)
				require.Equal(t, 1, result.ImageCount, "count delivered PNGs, never requested n or repeated terminal representations")
				require.Equal(t, "1K", result.ImageSize)
				require.Equal(t, "1024x1024", result.ImageInputSize)
				wantOutputSize := "1024x1024"
				if strings.HasPrefix(route, "images_bridge_") {
					// The Images compatibility bridge reconciles output size from
					// the actual PNG, while retaining the requested input size.
					wantOutputSize = "2x2"
				}
				require.Equal(t, []string{wantOutputSize}, result.ImageOutputSizes)
				require.Equal(t, 100, result.Usage.InputTokens)
				require.Equal(t, 50, result.Usage.OutputTokens)
				require.Equal(t, 20, result.Usage.ImageOutputTokens)
				assertImageAccountingBill(t, result, account, mode)
			})
		}
	}
}

func assertImageAccountingBill(t *testing.T, result *OpenAIForwardResult, account *Account, mode string) {
	t.Helper()
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	userRepo := &openAIRecordUsageUserRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
	groupID := int64(12)
	apiKey := &APIKey{ID: 1007, GroupID: &groupID, Group: &Group{ID: groupID, RateMultiplier: 1}}
	wantCost := 0.25
	if mode == "token" {
		svc.resolver = newOpenAITokenImageChannelPricingResolverForTest(t, groupID, "gpt-image-2")
		wantCost = 100*3e-6 + 50*15e-6
	} else {
		svc.resolver = newOpenAIImageChannelPricingResolverForTest(t, groupID, "gpt-image-2", wantCost)
	}
	require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: result, APIKey: apiKey, User: &User{ID: 2007}, Account: account,
	}))
	require.Equal(t, 1, usageRepo.calls)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.Equal(t, result.RequestID, billingRepo.lastCmd.RequestID)
	require.Equal(t, result.Model, usageRepo.lastLog.Model, "usage display model remains the requested model")
	require.Equal(t, 1, billingRepo.lastCmd.ImageCount)
	require.Equal(t, mode, *usageRepo.lastLog.BillingMode)
	require.InDelta(t, wantCost, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, wantCost, billingRepo.lastCmd.BalanceCost, 1e-12)
	require.Zero(t, userRepo.deductCalls, "atomic billing command must not also invoke the legacy balance deduction")
}

func TestImageOutputAccountingTextOnlyKeepsExplicitTokenTariff(t *testing.T) {
	for _, explicitImageIntent := range []bool{false, true} {
		name := "optional_image_tool"
		if explicitImageIntent {
			name = "explicit_image_tool_requires_artifact"
		}
		t.Run(name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"text_no_image_" + name}},
				Body: io.NopCloser(strings.NewReader(`{"id":"resp_text","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"partial_image is only text"}]}],"usage":{"input_tokens":100,"output_tokens":50}}`)),
			}}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			c, rec := newOpenAIImageGenerationControlTestContext(true, "curl/8.0")
			account := newOpenAIImageGenerationControlTestAccount()
			tool := map[string]any{"type": "image_generation"}
			if explicitImageIntent {
				tool["model"] = "gpt-image-2"
			}
			request := accountingJSON(t, map[string]any{"model": "gpt-5.4", "input": "explain", "tools": []any{tool}, "stream": false})
			result, err := svc.Forward(context.Background(), c, account, request)
			if explicitImageIntent {
				// An explicit image model still requires an artifact; the error
				// retains observed usage under the existing settlement policy.
				require.ErrorIs(t, err, ErrOpenAIImageGenerationOutputMissing)
				require.Equal(t, http.StatusBadGateway, rec.Code)
				require.Contains(t, rec.Body.String(), "without an image result")
			} else {
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, rec.Code)
				require.Equal(t, "partial_image is only text", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
			}
			require.NotNil(t, result)
			require.Len(t, upstream.requests, 1)
			require.Zero(t, result.ImageCount)
			require.False(t, result.ImageRequest)
			require.Equal(t, "gpt-5.4", result.BillingModel)
			require.Equal(t, 100, result.Usage.InputTokens)
			require.Equal(t, 50, result.Usage.OutputTokens)
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			billingRepo := &openAIRecordUsageBillingRepoStub{}
			billing := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			billing.resolver = newOpenAITokenImageChannelPricingResolverForTest(t, 12, "gpt-5.4")
			require.NoError(t, billing.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: result, APIKey: &APIKey{ID: 1, GroupID: i64p(12), Group: &Group{ID: 12, RateMultiplier: 1}}, User: &User{ID: 2}, Account: account,
			}))
			require.Equal(t, 1, usageRepo.calls)
			require.Equal(t, 1, billingRepo.calls)
			require.InDelta(t, 100*3e-6+50*15e-6, billingRepo.lastCmd.BalanceCost, 1e-12)
			require.Equal(t, "token", *usageRepo.lastLog.BillingMode)
		})
	}
}
