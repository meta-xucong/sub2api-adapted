package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBuildOpenAIResponsesImageBridgeRequest_TextToImage(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"A quiet alpine lake at sunrise."}]}],"tools":[{"type":"image_generation","model":"gpt-image-2","size":"1536x1024","quality":"medium","output_format":"png"}]}`)

	imagesBody, parsed, err := BuildOpenAIResponsesImageBridgeRequest(body)
	require.NoError(t, err)
	require.Equal(t, openAIImagesGenerationsEndpoint, parsed.Endpoint)
	require.Equal(t, "gpt-image-2", parsed.Model)
	require.Equal(t, "A quiet alpine lake at sunrise.", parsed.Prompt)
	require.Equal(t, "1536x1024", parsed.Size)
	require.Equal(t, "medium", parsed.Quality)
	require.Equal(t, "png", parsed.OutputFormat)
	require.Equal(t, "gpt-image-2", gjson.GetBytes(imagesBody, "model").String())
	require.Equal(t, "b64_json", gjson.GetBytes(imagesBody, "response_format").String())
	require.False(t, gjson.GetBytes(imagesBody, "stream").Bool())
}

func TestBuildOpenAIResponsesImageBridgeRequest_DoesNotUseResponsesModelAsImageModel(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":"draw a mountain lake","tools":[{"type":"image_generation"}]}`)

	imagesBody, parsed, err := BuildOpenAIResponsesImageBridgeRequest(body)
	require.NoError(t, err)
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

func TestBuildOpenAIResponsesImageBridgeRequest_RejectsFileIDAndEmptyPrompt(t *testing.T) {
	_, _, err := BuildOpenAIResponsesImageBridgeRequest([]byte(`{"input":[{"type":"input_image","file_id":"file_123"}],"tools":[{"type":"image_generation"}]}`))
	require.ErrorContains(t, err, "file_id")

	_, _, err = BuildOpenAIResponsesImageBridgeRequest([]byte(`{"tools":[{"type":"image_generation"}]}`))
	require.ErrorContains(t, err, "requires input text or instructions")
}

func TestIsResponsesImageBridgeRequestDoesNotMatchOrdinaryResponses(t *testing.T) {
	require.False(t, IsResponsesImageBridgeRequest([]byte(`{"model":"gpt-5.5","input":"hello"}`)))
	require.True(t, IsResponsesImageBridgeRequest([]byte(`{"model":"gpt-5.5","tools":[{"type":"image_generation"}]}`)))
	require.False(t, IsResponsesImageBridgeRequest([]byte(`not-json`)))
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

func TestWriteResponsesImageBridgeResponse_PreservingStreamingCanBeDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestBody := []byte(`{"model":"gpt-5.5","stream":true,"tools":[{"type":"image_generation"}]}`)
	imageBody := []byte(`{"data":[{"url":"https://image.example/result.png"}]}`)

	require.NoError(t, writeResponsesImageBridgeResponse(c, requestBody, imageBody, false))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	require.Equal(t, "https://image.example/result.png", gjson.Get(recorder.Body.String(), "output.0.result").String())
}

func TestWriteResponsesImageBridgeResponse_EmptyResultsAreFailoverErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, imageBody := range map[string][]byte{
		"no data":    []byte(`{"data":[]}`),
		"empty item": []byte(`{"data":[{}]}`),
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			err := writeResponsesImageBridgeResponse(c, []byte(`{"tools":[{"type":"image_generation"}]}`), imageBody)
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
		})
	}
}

func TestAccountResponsesImageModeDefaultsToNativeAndHonorsExplicitOverride(t *testing.T) {
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
