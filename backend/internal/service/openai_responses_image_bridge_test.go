package service

import (
	"bufio"
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
)

type responsesImageBridgeHTTPUpstream struct{}

func (responsesImageBridgeHTTPUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"image-req"}},
		Body:       io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"aGVsbG8="}]}`)),
	}, nil
}

func (u responsesImageBridgeHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

type responsesImageBridgeFailingWriter struct{ gin.ResponseWriter }

func (responsesImageBridgeFailingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func (responsesImageBridgeFailingWriter) WriteString(string) (int, error) { return 0, io.ErrClosedPipe }

func TestResponsesImageModeExplicitOverridesAndAutomaticDetection(t *testing.T) {
	auto := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://images.example/v1", "model_mapping": map[string]any{"gpt-image-2": "image-model"}},
	}
	require.True(t, auto.UsesResponsesImageBridge())

	nativeOverride := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: auto.Credentials,
		Extra:       map[string]any{"responses_image_mode": "native"},
	}
	require.False(t, nativeOverride.UsesResponsesImageBridge(), "top-level explicit native mode must beat automatic detection")

	nestedOverride := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: auto.Credentials,
		Extra:       map[string]any{PlatformOpenAI: map[string]any{"responses_image_mode": "images_api"}},
	}
	require.True(t, nestedOverride.UsesResponsesImageBridge())

	upstream := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeUpstream,
		Credentials: auto.Credentials,
	}
	require.False(t, upstream.UsesResponsesImageBridge(), "automatic detection is APIKey-only")

	official := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.openai.com/v1", "model_mapping": map[string]any{"gpt-image-2": "image-model"}},
	}
	require.False(t, official.UsesResponsesImageBridge())

	mixedMapping := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://images.example/v1", "model_mapping": map[string]any{"gpt-image-2": "image-model", "gpt-5.5": "text-model"}},
	}
	require.False(t, mixedMapping.UsesResponsesImageBridge())
}

func TestBuildOpenAIResponsesImageBridgeRequestRequiresExplicitTool(t *testing.T) {
	ordinaryImageModel := []byte(`{"model":"gpt-image-2","input":"draw a lighthouse"}`)
	require.False(t, IsResponsesImageBridgeRequest(ordinaryImageModel))
	_, _, err := BuildOpenAIResponsesImageBridgeRequest(ordinaryImageModel)
	require.ErrorContains(t, err, "image_generation tool is required")

	body := []byte(`{"model":"gpt-5.6","instructions":"from instructions","tools":[{"type":"image_generation","model":"gpt-image-2","size":"1024x1024"}]}`)
	translated, parsed, err := BuildOpenAIResponsesImageBridgeRequest(body)
	require.NoError(t, err)
	require.Equal(t, openAIImagesGenerationsEndpoint, parsed.Endpoint)
	require.Equal(t, "gpt-image-2", parsed.Model)
	require.Contains(t, string(translated), `"prompt":"from instructions"`)

	withInputImage := []byte(`{"model":"gpt-5.6","input":[{"type":"input_text","text":"edit this"},{"type":"input_image","image_url":"https://example.test/in.png"}],"tools":[{"type":"image_generation","action":"edit"}]}`)
	_, parsed, err = BuildOpenAIResponsesImageBridgeRequest(withInputImage)
	require.NoError(t, err)
	require.Equal(t, openAIImagesEditsEndpoint, parsed.Endpoint)
	require.Equal(t, []string{"https://example.test/in.png"}, parsed.InputImageURLs)

	fileID := []byte(`{"model":"gpt-5.6","input":[{"type":"input_image","file_id":"file_1"}],"tools":[{"type":"image_generation"}]}`)
	_, _, err = BuildOpenAIResponsesImageBridgeRequest(fileID)
	require.ErrorContains(t, err, "file_id is not supported")
}

func TestWriteResponsesImageBridgeRejectsInvalidOrEmptyImageResults(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want string
	}{
		{name: "invalid JSON", body: []byte(`{`), want: "parse Images API response failed"},
		{name: "no images", body: []byte(`{"data":[]}`), want: "image upstream returned no image"},
		{name: "empty result", body: []byte(`{"data":[{"b64_json":" ","url":""}]}`), want: "empty image result"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			err := writeResponsesImageBridgeResponse(c, []byte(`{"model":"gpt-5.6"}`), tt.body)
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.Equal(t, http.StatusBadGateway, failover.ClientStatusCode)
			require.Contains(t, failover.ClientMessage, tt.want)
			require.Empty(t, recorder.Body.String(), "bridge must not emit success before a valid complete image exists")
		})
	}
}

func TestWriteResponsesImageBridgeJSONAndPreserveStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requestBody := []byte(`{"model":"gpt-5.6","tools":[{"type":"image_generation","model":"gpt-image-2","size":"1024x1024"}]}`)
	imageBody := []byte(`{"created":1720000000,"data":[{"b64_json":"aGVsbG8=","revised_prompt":"a lighthouse"}]}`)

	t.Run("nonstream JSON", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		require.NoError(t, writeResponsesImageBridgeResponse(c, requestBody, imageBody))
		require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
		require.Equal(t, http.StatusOK, recorder.Code)
		var response map[string]any
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		require.Equal(t, "response", response["object"])
		require.Equal(t, "completed", response["status"])
		require.Len(t, response["output"], 1)
	})

	t.Run("stream preserved", func(t *testing.T) {
		streamBody := []byte(`{"model":"gpt-5.6","stream":true,"tools":[{"type":"image_generation"}]}`)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		require.NoError(t, writeResponsesImageBridgeResponse(c, streamBody, imageBody, true))
		require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
		var events []map[string]any
		scanner := bufio.NewScanner(strings.NewReader(recorder.Body.String()))
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
				continue
			}
			var event map[string]any
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
			events = append(events, event)
		}
		require.NoError(t, scanner.Err())
		require.Equal(t, []string{"response.created", "response.in_progress", "response.output_item.added", "response.output_item.done", "response.completed"}, eventTypes(events))
		for i, event := range events {
			require.Equal(t, float64(i), event["sequence_number"])
		}
		require.Contains(t, recorder.Body.String(), "data: [DONE]\n\n")
	})

	t.Run("stream suppressed by preserve option", func(t *testing.T) {
		streamBody := []byte(`{"model":"gpt-5.6","stream":true,"tools":[{"type":"image_generation"}]}`)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		require.NoError(t, writeResponsesImageBridgeResponse(c, streamBody, imageBody, false))
		require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
		require.NotContains(t, recorder.Body.String(), "response.created")
		require.Contains(t, recorder.Body.String(), `"status":"completed"`)
	})
}

func TestForwardResponsesImageBridgeReturnsUsageWhenClientWriteFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	service := NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, responsesImageBridgeHTTPUpstream{}, nil, nil, nil, nil, nil, nil, nil, nil)

	requestBody := []byte(`{"model":"gpt-5.6","input":"draw","tools":[{"type":"image_generation","model":"gpt-image-2"}]}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Writer = responsesImageBridgeFailingWriter{c.Writer}
	account := &Account{ID: 41, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "test-key", "base_url": "https://images.example/v1",
	}}

	result, err := service.ForwardResponsesImageBridge(context.Background(), c, account, requestBody, false)
	require.ErrorIs(t, err, io.ErrClosedPipe)
	require.NotNil(t, result, "upstream usage result must survive a client write failure")
	require.Equal(t, 1, result.ImageCount)
}

func eventTypes(events []map[string]any) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event["type"].(string))
	}
	return types
}
