package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type responsesImageBridgeStatusHTTPUpstream struct {
	statusCode   int
	body         string
	requestModel string
}

func (u *responsesImageBridgeStatusHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	requestBody, _ := io.ReadAll(req.Body)
	u.requestModel = gjson.GetBytes(requestBody, "model").String()
	return &http.Response{
		StatusCode: u.statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"image-error-req"}},
		Body:       io.NopCloser(strings.NewReader(u.body)),
	}, nil
}

func (u *responsesImageBridgeStatusHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func TestBuildOpenAIResponsesImageBridgeRequest_TextToImage(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"A quiet alpine lake at sunrise."}]}],"tools":[{"type":"image_generation","model":"gpt-image-2","size":"1536x1024","quality":"medium","output_format":"png"}]}`)

	imagesBody, parsed, err := BuildOpenAIResponsesImageBridgeRequest(body)
	require.NoError(t, err)
	require.Equal(t, openAIImagesGenerationsEndpoint, parsed.Endpoint)
	require.Equal(t, "gpt-image-2", parsed.Model, "tool-level image model takes precedence")
	require.Equal(t, "A quiet alpine lake at sunrise.", parsed.Prompt)
	require.Equal(t, "1536x1024", parsed.Size)
	require.Equal(t, "medium", parsed.Quality)
	require.Equal(t, "png", parsed.OutputFormat)
	require.Equal(t, "gpt-image-2", gjson.GetBytes(imagesBody, "model").String())
	require.Equal(t, "b64_json", gjson.GetBytes(imagesBody, "response_format").String())
	require.False(t, gjson.GetBytes(imagesBody, "stream").Bool())
}

func TestBuildOpenAIResponsesImageBridgeRequestUsesTopLevelImageModelWhenValid(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2.5","input":"draw a lighthouse","tools":[{"type":"image_generation"}]}`)
	imagesBody, parsed, err := BuildOpenAIResponsesImageBridgeRequest(body)
	require.NoError(t, err)
	require.Equal(t, "gpt-image-2.5", parsed.Model)
	require.Equal(t, "gpt-image-2.5", gjson.GetBytes(imagesBody, "model").String())

	textModel := []byte(`{"model":"gpt-5.6","input":"draw a lighthouse","tools":[{"type":"image_generation"}]}`)
	imagesBody, parsed, err = BuildOpenAIResponsesImageBridgeRequest(textModel)
	require.NoError(t, err)
	require.Equal(t, "gpt-image-2", parsed.Model)
	require.Equal(t, "gpt-image-2", gjson.GetBytes(imagesBody, "model").String())
}

func TestBuildOpenAIResponsesImageBridgeRequest_DoesNotUseResponsesModelAsImageModel(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":"draw a mountain lake","tools":[{"type":"image_generation"}]}`)

	imagesBody, parsed, err := BuildOpenAIResponsesImageBridgeRequest(body)
	require.NoError(t, err)
	// The text Responses model is not an Images API model. The bridge must
	// select the image tool default and leave account mapping to ForwardImages.
	require.Equal(t, "gpt-image-2", parsed.Model)
	require.Equal(t, "gpt-image-2", gjson.GetBytes(imagesBody, "model").String())
}

func TestBuildOpenAIResponsesImageBridgeRequest_ImageEdit(t *testing.T) {
	body := []byte(`{"input":[{"type":"message","content":[{"type":"input_text","text":"Make the sky warmer."},{"type":"input_image","image_url":"data:image/png;base64,abc"}]}],"tools":[{"type":"image_generation","action":"edit","model":"gpt-image-2"}]}`)

	imagesBody, parsed, err := BuildOpenAIResponsesImageBridgeRequest(body)
	require.NoError(t, err)
	require.Equal(t, openAIImagesEditsEndpoint, parsed.Endpoint)
	require.Equal(t, "data:image/png;base64,abc", gjson.GetBytes(imagesBody, "images.0.image_url").String())
	require.Equal(t, "Make the sky warmer.", gjson.GetBytes(imagesBody, "prompt").String())
}

func TestBuildOpenAIResponsesImageBridgeRequest_RejectsFileID(t *testing.T) {
	body := []byte(`{"input":[{"type":"input_image","file_id":"file_123"}],"tools":[{"type":"image_generation"}]}`)
	_, _, err := BuildOpenAIResponsesImageBridgeRequest(body)
	require.ErrorContains(t, err, "file_id")
}

func TestIsResponsesImageBridgeRequestDoesNotMatchOrdinaryResponses(t *testing.T) {
	require.False(t, IsResponsesImageBridgeRequest([]byte(`{"model":"gpt-5.5","input":"hello"}`)))
	require.True(t, IsResponsesImageBridgeRequest([]byte(`{"model":"gpt-5.5","tools":[{"type":"image_generation"}]}`)))
}

func TestWriteResponsesImageBridgeResponse_NonStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestBody := []byte(`{"model":"gpt-5.5","stream":false,"tools":[{"type":"image_generation","output_format":"png"}]}`)
	imageBody := []byte(`{"created":1710000000,"data":[{"b64_json":"aGVsbG8=","revised_prompt":"draw a lake"}]}`)

	require.NoError(t, writeResponsesImageBridgeResponse(c, requestBody, imageBody))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	require.Equal(t, "image_generation_call", gjson.Get(recorder.Body.String(), "output.0.type").String())
	require.Equal(t, "aGVsbG8=", gjson.Get(recorder.Body.String(), "output.0.result").String())
}

func TestWriteResponsesImageBridgeResponse_Streaming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestBody := []byte(`{"model":"gpt-5.5","stream":true,"tools":[{"type":"image_generation"}]}`)
	imageBody := []byte(`{"created":1710000000,"data":[{"b64_json":"aGVsbG8="}]}`)

	require.NoError(t, writeResponsesImageBridgeResponse(c, requestBody, imageBody))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
	require.Contains(t, recorder.Body.String(), "response.output_item.done")
	require.Contains(t, recorder.Body.String(), "response.completed")
	require.True(t, strings.HasSuffix(recorder.Body.String(), "data: [DONE]\n\n"))
}

func TestForwardResponsesImageBridgeWritesStructuredImageError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	upstream := &responsesImageBridgeStatusHTTPUpstream{
		statusCode: http.StatusBadRequest,
		body:       `{"error":{"type":"invalid_request_error","code":"invalid_image_input","param":"input[0].content[1].image_url","message":"The image request is invalid. Check the input and try again."}}`,
	}
	svc := NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
	body := []byte(`{"model":"gpt-image-2.5","input":"draw a product","tools":[{"type":"image_generation"}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	account := &Account{ID: 41, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "test-key", "base_url": "https://images.example/v1",
	}}

	_, err := svc.ForwardResponsesImageBridge(context.Background(), c, account, body)
	require.Equal(t, "gpt-image-2.5", upstream.requestModel)
	var imageErr *OpenAIImagesUpstreamError
	require.ErrorAs(t, err, &imageErr)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	var response struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Param   string `json:"param"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, "invalid_request_error", response.Error.Type)
	require.Equal(t, "invalid_image_input", response.Error.Code)
	require.Equal(t, "input[0].content[1].image_url", response.Error.Param)
	require.Equal(t, "The image request is invalid. Check the input and try again.", response.Error.Message)
}

func TestForwardResponsesImageBridgeLeavesRetryableErrorsForFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	upstream := &responsesImageBridgeStatusHTTPUpstream{
		statusCode: http.StatusBadGateway,
		body:       `{"error":{"type":"server_error","message":"temporary upstream failure"}}`,
	}
	svc := NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
	body := []byte(`{"model":"gpt-image-2.5","input":"draw a product","tools":[{"type":"image_generation"}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	account := &Account{ID: 41, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "test-key", "base_url": "https://images.example/v1",
	}}

	_, err := svc.ForwardResponsesImageBridge(context.Background(), c, account, body)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Empty(t, recorder.Body.String(), "failover must remain available to the outer account router")
}

func TestAccountResponsesImageModeDefaultsToNative(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Extra: map[string]any{}}
	require.Equal(t, ResponsesImageModeNative, account.ResponsesImageMode())
	account.Extra[featureKeyResponsesImageMode] = ResponsesImageModeImagesAPI
	require.True(t, account.UsesResponsesImageBridge())
	account.Extra[featureKeyResponsesImageMode] = "unexpected"
	require.Equal(t, ResponsesImageModeNative, account.ResponsesImageMode())
}

func TestAccountResponsesImageModeAutoDetectsThirdPartyImageOnlyAPIKey(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://image-provider.example/v1",
			"model_mapping": map[string]any{
				"gpt-image-2": "gpt-image-2",
			},
		},
	}
	require.Equal(t, ResponsesImageModeImagesAPI, account.ResponsesImageMode())
	require.True(t, account.UsesResponsesImageBridge())
}

func TestAccountResponsesImageModeAutoDetectsUpstreamImageOnlyAccount(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeUpstream,
		Credentials: map[string]any{
			"base_url": "https://image-provider.example/v1",
			"model_mapping": map[string]any{
				"gpt-image-2": "gpt-image-2",
			},
		},
	}
	require.Equal(t, ResponsesImageModeImagesAPI, account.ResponsesImageMode())
}

func TestAccountResponsesImageModeAutoDetectionDoesNotTouchChatOrOfficialAccounts(t *testing.T) {
	chatAccount := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://image-provider.example/v1",
			"model_mapping": map[string]any{
				"gpt-5.5": "gpt-5.5",
			},
		},
	}
	require.Equal(t, ResponsesImageModeNative, chatAccount.ResponsesImageMode())

	officialAccount := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://api.openai.com/v1",
			"model_mapping": map[string]any{
				"gpt-image-2": "gpt-image-2",
			},
		},
	}
	require.Equal(t, ResponsesImageModeNative, officialAccount.ResponsesImageMode())
}

func TestAccountResponsesImageModeExplicitNativeOverridesAutoDetection(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://image-provider.example/v1",
			"model_mapping": map[string]any{
				"gpt-image-2": "gpt-image-2",
			},
		},
		Extra: map[string]any{featureKeyResponsesImageMode: ResponsesImageModeNative},
	}
	require.Equal(t, ResponsesImageModeNative, account.ResponsesImageMode())
}
