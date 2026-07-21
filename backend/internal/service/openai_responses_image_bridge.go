package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// IsResponsesImageBridgeRequest is intentionally narrower than the general
// image-intent classifier. A model name alone must never rewrite a normal
// Responses request into an Images API request.
func IsResponsesImageBridgeRequest(body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return false
	}
	for _, tool := range tools.Array() {
		if strings.EqualFold(strings.TrimSpace(tool.Get("type").String()), "image_generation") {
			return true
		}
	}
	return false
}

// BuildOpenAIResponsesImageBridgeRequest translates one explicit Responses
// image tool request into the JSON form accepted by /v1/images/generations or
// /v1/images/edits. It returns the parsed image request so routing and billing
// use the same capability classification as the public Images endpoint.
func BuildOpenAIResponsesImageBridgeRequest(body []byte) ([]byte, *OpenAIImagesRequest, error) {
	if !IsResponsesImageBridgeRequest(body) {
		return nil, nil, fmt.Errorf("Responses image_generation tool is required")
	}

	var tool gjson.Result
	for _, candidate := range gjson.GetBytes(body, "tools").Array() {
		if strings.EqualFold(strings.TrimSpace(candidate.Get("type").String()), "image_generation") {
			tool = candidate
			break
		}
	}

	prompt, imageURLs, hasFileID := collectResponsesImageInputs(gjson.GetBytes(body, "input"))
	if hasFileID {
		return nil, nil, fmt.Errorf("Responses input_image file_id is not supported by the Images API bridge")
	}
	if prompt == "" {
		prompt = strings.TrimSpace(gjson.GetBytes(body, "instructions").String())
	}
	if prompt == "" {
		return nil, nil, fmt.Errorf("image_generation request requires input text or instructions")
	}

	model := strings.TrimSpace(tool.Get("model").String())
	if !isOpenAIImageGenerationModel(model) {
		model = "gpt-image-2"
	}
	endpoint := openAIImagesGenerationsEndpoint
	if len(imageURLs) > 0 || strings.EqualFold(strings.TrimSpace(tool.Get("action").String()), "edit") {
		endpoint = openAIImagesEditsEndpoint
	}

	payload := map[string]any{
		"model":           model,
		"prompt":          prompt,
		"n":               positiveIntOrDefault(int(tool.Get("n").Int()), 1),
		"response_format": "b64_json",
	}
	for _, field := range []string{"size", "quality", "background", "output_format", "moderation", "input_fidelity", "style"} {
		if value := strings.TrimSpace(tool.Get(field).String()); value != "" {
			payload[field] = value
		}
	}
	if value := tool.Get("output_compression"); value.Exists() && value.Type == gjson.Number {
		payload["output_compression"] = value.Int()
	}
	if value := tool.Get("partial_images"); value.Exists() && value.Type == gjson.Number {
		payload["partial_images"] = value.Int()
	}
	if len(imageURLs) > 0 {
		images := make([]map[string]string, 0, len(imageURLs))
		for _, imageURL := range imageURLs {
			images = append(images, map[string]string{"image_url": imageURL})
		}
		payload["images"] = images
	}
	if mask := strings.TrimSpace(tool.Get("input_image_mask.image_url").String()); mask != "" {
		payload["mask"] = map[string]string{"image_url": mask}
	}

	translated, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal Images API request: %w", err)
	}
	parsed := &OpenAIImagesRequest{
		Endpoint:    endpoint,
		ContentType: "application/json",
		Body:        translated,
		N:           1,
	}
	if sum := sha256.Sum256(translated); len(sum) > 0 {
		parsed.bodyHash = hex.EncodeToString(sum[:8])
	}
	if err := parseOpenAIImagesJSONRequest(translated, parsed); err != nil {
		return nil, nil, err
	}
	applyOpenAIImagesDefaults(parsed)
	if err := validateOpenAIImagesRequestModel(parsed.Model); err != nil {
		return nil, nil, err
	}
	parsed.SizeTier = normalizeOpenAIImageSizeTier(parsed.Size)
	parsed.RequiredCapability = classifyOpenAIImagesCapability(parsed)
	return translated, parsed, nil
}

func positiveIntOrDefault(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func collectResponsesImageInputs(value gjson.Result) (string, []string, bool) {
	var prompt strings.Builder
	images := make([]string, 0, 2)
	hasFileID := false
	var walk func(gjson.Result)
	walk = func(current gjson.Result) {
		if current.IsArray() {
			for _, item := range current.Array() {
				walk(item)
			}
			return
		}
		if !current.IsObject() {
			if current.Type == gjson.String && prompt.Len() == 0 {
				prompt.WriteString(strings.TrimSpace(current.String()))
			}
			return
		}
		typ := strings.ToLower(strings.TrimSpace(current.Get("type").String()))
		switch typ {
		case "input_text":
			appendResponsesImageText(&prompt, current.Get("text").String())
		case "input_image":
			if imageURL := strings.TrimSpace(current.Get("image_url").String()); imageURL != "" {
				images = append(images, imageURL)
			}
			if current.Get("file_id").Exists() {
				hasFileID = true
			}
		case "message":
			walk(current.Get("content"))
		default:
			if content := current.Get("content"); content.Exists() {
				walk(content)
			}
		}
	}
	walk(value)
	return strings.TrimSpace(prompt.String()), images, hasFileID
}

func appendResponsesImageText(prompt *strings.Builder, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if prompt.Len() > 0 {
		prompt.WriteByte('\n')
	}
	prompt.WriteString(value)
}

// ForwardResponsesImageBridge executes exactly one Images API attempt. The
// public Responses context is only written after the image service has
// returned a complete result, so a failover never emits a partial Responses
// stream or duplicates a top-level request.
func (s *OpenAIGatewayService) ForwardResponsesImageBridge(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	channelMappedModel string,
) (*OpenAIForwardResult, error) {
	imageBody, parsed, err := BuildOpenAIResponsesImageBridgeRequest(body)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, fmt.Errorf("image bridge account is required")
	}

	recorder := httptest.NewRecorder()
	bridgeContext, _ := gin.CreateTestContext(recorder)
	bridgeContext.Request = c.Request.Clone(ctx)
	requestURL := *c.Request.URL
	if parsed.IsEdits() {
		requestURL.Path = openAIImagesEditsEndpoint
	} else {
		requestURL.Path = openAIImagesGenerationsEndpoint
	}
	bridgeContext.Request.URL = &requestURL
	bridgeContext.Request.Header = c.Request.Header.Clone()
	bridgeContext.Request.Header.Set("Content-Type", "application/json")
	bridgeContext.Params = c.Params
	for key, value := range c.Keys {
		bridgeContext.Set(key, value)
	}

	result, err := s.ForwardImages(ctx, bridgeContext, account, imageBody, parsed, channelMappedModel)
	if err != nil {
		return result, err
	}
	imageResponse := bytes.TrimSpace(recorder.Body.Bytes())
	if len(imageResponse) == 0 || !gjson.ValidBytes(imageResponse) {
		return nil, &UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: []byte(`{"error":{"message":"image upstream returned an invalid response"}}`)}
	}
	if err := writeResponsesImageBridgeResponse(c, body, imageResponse); err != nil {
		return nil, err
	}
	return result, nil
}

func writeResponsesImageBridgeResponse(c *gin.Context, requestBody, imageBody []byte) error {
	var imageResponse struct {
		Created int64 `json:"created"`
		Data    []struct {
			B64JSON       string `json:"b64_json"`
			URL           string `json:"url"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(imageBody, &imageResponse); err != nil {
		return fmt.Errorf("parse Images API response: %w", err)
	}
	if len(imageResponse.Data) == 0 {
		return &UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: []byte(`{"error":{"message":"image upstream returned no image"}}`)}
	}
	model := strings.TrimSpace(gjson.GetBytes(requestBody, "model").String())
	if model == "" {
		model = "gpt-5.4-mini"
	}
	responseID := "resp_img_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	created := imageResponse.Created
	if created <= 0 {
		created = time.Now().Unix()
	}
	output := make([]map[string]any, 0, len(imageResponse.Data))
	for index, image := range imageResponse.Data {
		result := strings.TrimSpace(image.B64JSON)
		if result == "" {
			result = strings.TrimSpace(image.URL)
		}
		item := map[string]any{
			"type":           "image_generation_call",
			"id":             fmt.Sprintf("ig_%s_%d", strings.TrimPrefix(responseID, "resp_"), index),
			"status":         "completed",
			"result":         result,
			"revised_prompt": image.RevisedPrompt,
		}
		for _, field := range []string{"model", "size", "quality", "background", "output_format"} {
			if value := responsesImageToolField(requestBody, field); value != "" {
				item[field] = value
			}
		}
		output = append(output, item)
	}
	completed := map[string]any{
		"id":         responseID,
		"object":     "response",
		"model":      model,
		"status":     "completed",
		"output":     output,
		"created_at": created,
		"tool_usage": map[string]any{"image_gen": map[string]any{"images": len(output)}},
	}
	if gjson.GetBytes(requestBody, "stream").Bool() {
		return writeResponsesImageBridgeStream(c, responseID, created, output, completed)
	}
	c.Header("Content-Type", "application/json")
	c.JSON(http.StatusOK, completed)
	return nil
}

func responsesImageToolField(body []byte, field string) string {
	for _, tool := range gjson.GetBytes(body, "tools").Array() {
		if strings.EqualFold(strings.TrimSpace(tool.Get("type").String()), "image_generation") {
			return strings.TrimSpace(tool.Get(field).String())
		}
	}
	return ""
}

func writeResponsesImageBridgeStream(c *gin.Context, responseID string, created int64, output []map[string]any, completed map[string]any) error {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Status(http.StatusOK)
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return fmt.Errorf("Responses image bridge requires a streaming response writer")
	}
	writeEvent := func(event map[string]any) error {
		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := c.Writer.Write(append(append([]byte("data: "), payload...), []byte("\n\n")...)); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	if err := writeEvent(map[string]any{
		"type":     "response.created",
		"response": map[string]any{"id": responseID, "object": "response", "status": "in_progress", "created_at": created, "output": []any{}},
	}); err != nil {
		return err
	}
	for index, item := range output {
		if err := writeEvent(map[string]any{"type": "response.output_item.added", "output_index": index, "item": item}); err != nil {
			return err
		}
		if err := writeEvent(map[string]any{"type": "response.output_item.done", "output_index": index, "item": item}); err != nil {
			return err
		}
	}
	completed["status"] = "completed"
	if err := writeEvent(map[string]any{"type": "response.completed", "response": completed}); err != nil {
		return err
	}
	_, err := io.WriteString(c.Writer, "data: [DONE]\n\n")
	if err == nil {
		flusher.Flush()
	}
	return err
}
