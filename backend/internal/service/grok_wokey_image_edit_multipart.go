package service

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// Keep scalar options through multipart -> JSON -> provider serialization.
// Unsupported file inputs and duplicate scalar options must not disappear.
func wokeyGrokImageEditMultipartScalarFields(body []byte, contentType string) (map[string]any, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil || params["boundary"] == "" {
		return nil, wokeyGrokImageEditInputErrorf("invalid image edit multipart boundary")
	}
	fields := make(map[string]any)
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return fields, nil
		}
		if err != nil {
			return nil, wokeyGrokImageEditInputErrorf("read image edit multipart: %v", err)
		}
		name := strings.TrimSpace(part.FormName())
		data, readErr := io.ReadAll(io.LimitReader(part, openAIImageMaxUploadPartSize+1))
		_ = part.Close()
		isImage := name == "image" || strings.HasPrefix(name, "image[")
		if len(data) > openAIImageMaxUploadPartSize && isImage && part.FileName() != "" {
			return nil, wokeyGrokImageEditInputErrorf("Wokey Grok image edit image must be smaller than %d MiB", openAIImageMaxUploadPartSize>>20)
		}
		if readErr != nil || len(data) > openAIImageMaxUploadPartSize {
			return nil, wokeyGrokImageEditInputErrorf("image edit multipart field %q is unreadable or too large", name)
		}
		if part.FileName() != "" {
			if !isImage && name != "mask" {
				return nil, wokeyGrokImageEditInputErrorf("unsupported image edit file field %q", name)
			}
			continue
		}
		if isImage || name == "image_url" || name == "mask_image_url" {
			continue // converted to canonical image objects by the caller
		}
		if name == "images" {
			// The request-side multipart parser does not include structured
			// images in moderation. Do not add a new image source after that
			// audit; callers can use the existing image file or JSON route.
			return nil, wokeyGrokImageEditInputErrorf("Wokey Grok image edit multipart does not support images; use one image file or a JSON request")
		}
		if !isWokeyGrokImageEditFieldName(name) {
			return nil, wokeyGrokImageEditInputErrorf("unsupported image edit multipart field %q", name)
		}
		if _, exists := fields[name]; exists {
			return nil, wokeyGrokImageEditInputErrorf("duplicate image edit multipart field %q", name)
		}
		value := string(data)
		if name == "n" {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n <= 0 {
				return nil, wokeyGrokImageEditInputErrorf("image edit n must be a positive integer")
			}
		}
		fields[name] = value
		// Preserve structured image inputs so account-specific validation sees
		// them, including Wokey's explicit reference_images/mask rejection.
		if name == "reference_images" || name == "mask" {
			var decoded any
			if json.Unmarshal(data, &decoded) == nil {
				fields[name] = decoded
			}
		}
	}
}

// Preserve multipart scalars only for the fork's Wokey image-file adapter.
// Other providers keep their existing upstream multipart normalization contract.
func prepareGrokMediaForwardBodyForAccount(account *Account, endpoint GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	if !isWokeyGrokImageEdit(account, endpoint) {
		return prepareGrokMediaForwardBody(endpoint, body, contentType)
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if gjson.ValidBytes(body) || err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		// Check the original JSON before normalization or model rewriting can
		// erase a duplicate that selected a different value during moderation.
		if err := validateWokeyGrokImageEditJSON(body); err != nil {
			return nil, "", err
		}
		return prepareGrokMediaForwardBody(endpoint, body, contentType)
	}
	fields, err := wokeyGrokImageEditMultipartScalarFields(body, contentType)
	if err != nil {
		return nil, "", err
	}
	prepared, preparedContentType, err := prepareGrokMediaForwardBody(endpoint, body, contentType)
	if err != nil {
		return nil, "", err
	}
	var payload map[string]any
	if err := json.Unmarshal(prepared, &payload); err != nil {
		return nil, "", wokeyGrokImageEditInputErrorf("invalid normalized image edit multipart input")
	}
	for name, value := range fields {
		payload[name] = value
	}
	prepared, err = json.Marshal(payload)
	return prepared, preparedContentType, err
}
