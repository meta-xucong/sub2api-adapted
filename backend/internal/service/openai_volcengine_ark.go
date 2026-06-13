package service

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const openAIProviderVolcengineArk = "volcengine_ark"

const defaultVolcengineArkImagesBaseURL = "https://ark.cn-beijing.volces.com/api/v3"

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
	if req.Reasoning != nil && strings.TrimSpace(req.Reasoning.Summary) != "" {
		req.Reasoning.Summary = ""
		changed = true
	}
	if req.Text != nil {
		req.Text = nil
		changed = true
	}
	return changed
}

func isVolcengineArkImageModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "doubao-seedream-") || strings.HasPrefix(model, "seedream-")
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
