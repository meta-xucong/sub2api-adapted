package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const openAIProviderVolcengineArk = "volcengine_ark"

const defaultVolcengineArkImagesBaseURL = "https://ark.cn-beijing.volces.com/api/v3"
const openAIUpstreamBaseURLOverrideContextKey = "openai_upstream_base_url_override"

func isVolcengineArkOpenAIAccount(account *Account) bool {
	if account == nil || !account.IsOpenAIApiKey() {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(account.GetExtraString("provider")), openAIProviderVolcengineArk)
}

func sanitizeVolcengineArkResponsesRequest(account *Account, req *apicompat.ResponsesRequest) bool {
	if !isVolcengineArkOpenAIAccount(account) || req == nil {
		return false
	}
	changed := false
	if req.Reasoning != nil {
		req.Reasoning = nil
		changed = true
	}
	if req.Text != nil {
		req.Text = nil
		changed = true
	}
	return changed
}

func configureVolcengineArkMessagesUpstream(c *gin.Context, account *Account, req *apicompat.ResponsesRequest) bool {
	if !isVolcengineArkOpenAIAccount(account) || req == nil {
		return false
	}
	if restored := volcengineArkMultimodalEndpointModel(account, req.Model); restored != "" {
		if restored == req.Model {
			return false
		}
		req.Model = restored
		return true
	}
	return false
}

func summarizeVolcengineArkAnthropicRequest(req *apicompat.AnthropicRequest) map[string]any {
	summary := map[string]any{}
	if req == nil {
		return summary
	}
	summary["messages"] = len(req.Messages)
	summary["tools"] = len(req.Tools)
	summary["stream"] = req.Stream
	summary["max_tokens"] = req.MaxTokens
	summary["has_thinking"] = req.Thinking != nil
	summary["has_output_config"] = req.OutputConfig != nil
	summary["has_temperature"] = req.Temperature != nil
	summary["has_top_p"] = req.TopP != nil
	if len(req.ToolChoice) > 0 {
		summary["tool_choice_type"] = strings.TrimSpace(gjson.GetBytes(req.ToolChoice, "type").String())
	}
	summary["system_shape"] = summarizeAnthropicSystemShape(req.System)

	roleCounts := map[string]int{}
	blockCounts := map[string]int{}
	imageMediaCounts := map[string]int{}
	imageBytes := make([]int, 0, 4)
	messageTextChars := make([]int, 0, len(req.Messages))
	totalTextChars := 0
	for _, msg := range req.Messages {
		roleCounts[msg.Role]++
		msgTextChars := 0
		var blocks []apicompat.AnthropicContentBlock
		if err := json.Unmarshal(msg.Content, &blocks); err == nil {
			for _, block := range blocks {
				blockCounts[block.Type]++
				if block.Type == "text" {
					textChars := len([]rune(block.Text))
					msgTextChars += textChars
					totalTextChars += textChars
				}
				if block.Type == "image" && block.Source != nil {
					mediaType := strings.TrimSpace(block.Source.MediaType)
					if mediaType == "" {
						mediaType = "image/png"
					}
					imageMediaCounts[mediaType]++
					if block.Source.Data != "" {
						imageBytes = append(imageBytes, decodedBase64ApproxBytes(block.Source.Data))
					}
				}
				if block.Type == "tool_result" && len(block.Content) > 0 {
					var nested []apicompat.AnthropicContentBlock
					if err := json.Unmarshal(block.Content, &nested); err == nil {
						for _, nestedBlock := range nested {
							blockCounts["tool_result."+nestedBlock.Type]++
							if nestedBlock.Type == "text" {
								textChars := len([]rune(nestedBlock.Text))
								msgTextChars += textChars
								totalTextChars += textChars
							}
							if nestedBlock.Type == "image" && nestedBlock.Source != nil {
								mediaType := strings.TrimSpace(nestedBlock.Source.MediaType)
								if mediaType == "" {
									mediaType = "image/png"
								}
								imageMediaCounts[mediaType]++
								if nestedBlock.Source.Data != "" {
									imageBytes = append(imageBytes, decodedBase64ApproxBytes(nestedBlock.Source.Data))
								}
							}
						}
					}
				}
			}
		} else {
			var text string
			if err := json.Unmarshal(msg.Content, &text); err == nil {
				blockCounts["plain_text"]++
				textChars := len([]rune(text))
				msgTextChars += textChars
				totalTextChars += textChars
			} else {
				blockCounts["unparsed"]++
			}
		}
		messageTextChars = append(messageTextChars, msgTextChars)
	}
	summary["role_counts"] = roleCounts
	summary["block_counts"] = blockCounts
	summary["total_text_chars"] = totalTextChars
	summary["message_text_chars"] = messageTextChars
	summary["image_media_counts"] = imageMediaCounts
	sort.Ints(imageBytes)
	summary["image_count"] = len(imageBytes)
	summary["image_decoded_bytes"] = imageBytes
	return summary
}

func summarizeAnthropicSystemShape(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "empty"
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			return "empty_string"
		}
		return "string"
	}
	var blocks []apicompat.AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		return fmt.Sprintf("blocks:%d", len(blocks))
	}
	return "unknown"
}

func decodedBase64ApproxBytes(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	padding := 0
	if strings.HasSuffix(value, "==") {
		padding = 2
	} else if strings.HasSuffix(value, "=") {
		padding = 1
	}
	return len(value)*3/4 - padding
}

func responsesRequestHasInputImage(req *apicompat.ResponsesRequest) bool {
	if req == nil || len(req.Input) == 0 {
		return false
	}
	var decoded any
	if err := json.Unmarshal(req.Input, &decoded); err != nil {
		return false
	}
	return jsonValueContainsInputImage(decoded)
}

func jsonValueContainsInputImage(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		if strings.TrimSpace(firstNonEmptyString(typed["type"])) == "input_image" {
			return true
		}
		for _, child := range typed {
			if jsonValueContainsInputImage(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if jsonValueContainsInputImage(child) {
				return true
			}
		}
	}
	return false
}

func isVolcengineArkImageModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "doubao-seedream-") || strings.HasPrefix(model, "seedream-")
}

func volcengineArkMultimodalEndpointModel(account *Account, model string) string {
	if !isVolcengineArkOpenAIAccount(account) {
		return ""
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	for _, key := range []string{"openai_multimodal_model", "multimodal_model"} {
		if value := strings.TrimSpace(account.GetExtraString(key)); value != "" {
			return value
		}
	}
	switch model {
	case "doubao-seed-2.0-lite":
		return "doubao-seed-2-0-lite-260428"
	default:
		return ""
	}
}

func volcengineArkImagesBaseURL(account *Account) string {
	if !isVolcengineArkOpenAIAccount(account) {
		return ""
	}
	for _, key := range []string{"openai_images_base_url", "images_base_url"} {
		if value := strings.TrimSpace(account.GetExtraString(key)); value != "" {
			return value
		}
	}
	return defaultVolcengineArkImagesBaseURL
}

func validateOpenAIImagesModelForAccount(account *Account, model string) error {
	if isVolcengineArkOpenAIAccount(account) && isVolcengineArkImageModel(model) {
		return nil
	}
	return validateOpenAIImagesModel(model)
}

func sanitizeVolcengineArkImagesRequest(account *Account, body []byte, contentType string, parsed *OpenAIImagesRequest) ([]byte, string, error) {
	if !isVolcengineArkOpenAIAccount(account) || parsed == nil || !isVolcengineArkImageModel(parsed.Model) {
		return body, contentType, nil
	}
	if parsed.IsEdits() {
		return nil, "", fmt.Errorf("volcengine ark image models only support /images/generations")
	}
	if strings.Contains(strings.ToLower(contentType), "multipart/form-data") {
		return nil, "", fmt.Errorf("volcengine ark image models do not support multipart image requests")
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body, contentType, nil
	}

	rewritten := body
	var err error
	for _, path := range []string{
		"background",
		"moderation",
		"partial_images",
		"quality",
		"style",
		"input_fidelity",
		"output_compression",
		"output_format",
	} {
		if gjson.GetBytes(rewritten, path).Exists() {
			rewritten, err = sjson.DeleteBytes(rewritten, path)
			if err != nil {
				return nil, "", err
			}
		}
	}
	if responseFormat := strings.TrimSpace(gjson.GetBytes(rewritten, "response_format").String()); responseFormat == "" {
		rewritten, err = sjson.SetBytes(rewritten, "response_format", "b64_json")
		if err != nil {
			return nil, "", err
		}
	}
	return rewritten, contentType, nil
}

func adaptVolcengineArkImagesToGeneration(account *Account, parsed *OpenAIImagesRequest) ([]byte, string, string, bool, error) {
	if !isVolcengineArkOpenAIAccount(account) || parsed == nil || !isVolcengineArkImageModel(parsed.Model) {
		return nil, "", "", false, nil
	}
	if parsed.HasMask || parsed.MaskUpload != nil || strings.TrimSpace(parsed.MaskImageURL) != "" {
		return nil, "", "", false, openAIImagesUnsupportedVolcengineArkFailoverError()
	}
	if !parsed.IsEdits() && len(parsed.InputImageURLs) == 0 && len(parsed.Uploads) == 0 {
		return nil, "", "", false, nil
	}

	images := make([]string, 0, len(parsed.InputImageURLs)+len(parsed.Uploads))
	for _, imageURL := range parsed.InputImageURLs {
		if trimmed := strings.TrimSpace(imageURL); trimmed != "" {
			images = append(images, trimmed)
		}
	}
	for _, upload := range parsed.Uploads {
		dataURL, err := openAIImageUploadToDataURL(upload)
		if err != nil {
			return nil, "", "", false, err
		}
		images = append(images, dataURL)
	}
	if len(images) == 0 {
		if parsed.IsEdits() {
			return nil, "", "", false, openAIImagesUnsupportedVolcengineArkFailoverError()
		}
		return nil, "", "", false, nil
	}

	payload := []byte(`{"model":"","prompt":"","image":[]}`)
	payload, _ = sjson.SetBytes(payload, "model", strings.TrimSpace(parsed.Model))
	payload, _ = sjson.SetBytes(payload, "prompt", strings.TrimSpace(parsed.Prompt))
	payload, _ = sjson.SetRawBytes(payload, "image", []byte(`[]`))
	for _, image := range images {
		payload, _ = sjson.SetBytes(payload, "image.-1", image)
	}
	if parsed.N > 0 {
		payload, _ = sjson.SetBytes(payload, "n", parsed.N)
	}
	if size := strings.TrimSpace(parsed.Size); size != "" {
		payload, _ = sjson.SetBytes(payload, "size", size)
	}
	if responseFormat := strings.TrimSpace(parsed.ResponseFormat); responseFormat != "" {
		payload, _ = sjson.SetBytes(payload, "response_format", responseFormat)
	} else {
		payload, _ = sjson.SetBytes(payload, "response_format", "b64_json")
	}
	return payload, "application/json", openAIImagesGenerationsEndpoint, true, nil
}

func openAIImagesUnsupportedVolcengineArkFailoverError() error {
	return &UpstreamFailoverError{
		StatusCode:   http.StatusBadRequest,
		ResponseBody: []byte(`{"error":{"message":"Volcengine Ark image models do not support this OpenAI image edit shape; trying the next image-capable account.","type":"invalid_request_error","code":"unsupported_image_edit_shape"}}`),
	}
}

func setOpenAIUpstreamBaseURLOverride(c *gin.Context, baseURL string) {
	if c == nil || strings.TrimSpace(baseURL) == "" {
		return
	}
	c.Set(openAIUpstreamBaseURLOverrideContextKey, strings.TrimSpace(baseURL))
}

func openAIUpstreamBaseURLOverride(c *gin.Context) string {
	if c == nil {
		return ""
	}
	value, ok := c.Get(openAIUpstreamBaseURLOverrideContextKey)
	if !ok {
		return ""
	}
	baseURL, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(baseURL)
}
