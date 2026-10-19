package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/tidwall/gjson"
	_ "golang.org/x/image/webp"
)

const (
	wokeyGrokImageEditMaxDimension = 8192
	wokeyGrokImageEditMaxPixels    = 16 * 1024 * 1024
)

// GrokImageEditInputValidationError identifies request data rejected by the
// Wokey-only image-edit adapter before any upstream request is made.
type GrokImageEditInputValidationError struct {
	Message string
}

func (e *GrokImageEditInputValidationError) Error() string {
	if e == nil {
		return "invalid Wokey Grok image edit input"
	}
	return e.Message
}

func wokeyGrokImageEditInputErrorf(format string, args ...any) error {
	return &GrokImageEditInputValidationError{Message: fmt.Sprintf(format, args...)}
}

// isWokeyGrokImageEdit opts only the canonical Wokey Grok image-edit route
// into Wokey's image-file multipart contract. It deliberately does not use the
// account's video transport setting, which is scoped to video requests.
func isWokeyGrokImageEdit(account *Account, endpoint GrokMediaEndpoint) bool {
	return account != nil && account.IsGrok() &&
		endpoint == GrokMediaEndpointImagesEdits &&
		xai.IsWokeyAPIBaseURL(account.GetGrokMediaBaseURL())
}

// prepareWokeyGrokImageEditBody is the final wire-format adapter for Wokey's
// Grok image-edit endpoint. Its input must already have passed Sub2API's
// generic normalization, model mapping, and request sanitization.
func prepareWokeyGrokImageEditBody(body []byte) ([]byte, string, error) {
	if err := validateWokeyGrokImageEditJSON(body); err != nil {
		return nil, "", err
	}

	imageURL, err := wokeyGrokImageEditSourceURL(body)
	if err != nil {
		return nil, "", err
	}
	imageData, imageContentType, err := decodeWokeyGrokImageEditDataURL(imageURL)
	if err != nil {
		return nil, "", err
	}
	fields, err := wokeyGrokImageEditFormFields(body)
	if err != nil {
		return nil, "", err
	}
	if gjson.GetBytes(body, "model").Type != gjson.String || strings.TrimSpace(fields["model"]) == "" {
		return nil, "", wokeyGrokImageEditInputErrorf("Wokey Grok image edit requires model")
	}
	if gjson.GetBytes(body, "prompt").Type != gjson.String || strings.TrimSpace(fields["prompt"]) == "" {
		return nil, "", wokeyGrokImageEditInputErrorf("Wokey Grok image edit requires prompt")
	}

	var output bytes.Buffer
	writer := multipart.NewWriter(&output)
	for _, name := range sortedWokeyGrokImageEditFieldNames(fields) {
		if err := writer.WriteField(name, fields[name]); err != nil {
			return nil, "", fmt.Errorf("write Wokey Grok image edit field %q: %w", name, err)
		}
	}

	filename := wokeyGrokImageEditFilename(imageContentType)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
		"name":     "image",
		"filename": filename,
	}))
	header.Set("Content-Type", imageContentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, "", fmt.Errorf("create Wokey Grok image edit file part: %w", err)
	}
	if _, err := part.Write(imageData); err != nil {
		return nil, "", fmt.Errorf("write Wokey Grok image edit file part: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("finish Wokey Grok image edit multipart body: %w", err)
	}
	return output.Bytes(), writer.FormDataContentType(), nil
}

// validateWokeyGrokImageEditJSON prevents the audit's gjson projection and the
// multipart adapter's encoding/json projection from choosing different values.
// It only reads tokens: decoded names are compared within each object, without
// normalizing case/Unicode or reserializing the request. Completed objects release
// their key sets; space is bounded by the existing request-body limit.
func validateWokeyGrokImageEditJSON(body []byte) error {
	// Valid also preserves encoding/json's syntax and maximum nesting checks;
	// Decoder.Token alone does not apply the decoder's nested-value depth limit.
	if !json.Valid(body) {
		return wokeyGrokImageEditInputErrorf("Wokey Grok image edit requires a valid JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return wokeyGrokImageEditInputErrorf("Wokey Grok image edit requires a JSON object")
	}
	type objectFrame struct {
		keys      map[string]struct{} // nil for arrays
		expectKey bool
	}
	stack := []objectFrame{{keys: make(map[string]struct{}), expectKey: true}}
	for len(stack) > 0 {
		token, err := decoder.Token()
		if err != nil {
			return wokeyGrokImageEditInputErrorf("Wokey Grok image edit requires a valid JSON object")
		}
		frame := &stack[len(stack)-1]
		if delimiter, ok := token.(json.Delim); ok && (delimiter == '}' || delimiter == ']') {
			stack[len(stack)-1] = objectFrame{}
			stack = stack[:len(stack)-1]
			continue
		}
		if frame.keys != nil && frame.expectKey {
			key, ok := token.(string)
			if !ok {
				return wokeyGrokImageEditInputErrorf("Wokey Grok image edit requires a valid JSON object")
			}
			if _, duplicate := frame.keys[key]; duplicate {
				// Keys can contain user data too; do not echo them in the error.
				return wokeyGrokImageEditInputErrorf("Wokey Grok image edit does not support duplicate JSON object keys")
			}
			frame.keys[key] = struct{}{}
			frame.expectKey = false
			continue
		}
		frame.expectKey = true
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{':
				stack = append(stack, objectFrame{keys: make(map[string]struct{}), expectKey: true})
			case '[':
				stack = append(stack, objectFrame{})
			}
		}
	}
	if _, err := decoder.Token(); err != io.EOF {
		return wokeyGrokImageEditInputErrorf("Wokey Grok image edit requires a single JSON object")
	}
	return nil
}

func wokeyGrokImageEditSourceURL(body []byte) (string, error) {
	image := gjson.GetBytes(body, "image")
	images := gjson.GetBytes(body, "images")
	if image.Exists() && images.Exists() {
		return "", wokeyGrokImageEditInputErrorf("Wokey Grok image edit accepts one image field; image and images cannot both be set")
	}
	if gjson.GetBytes(body, "mask").Exists() {
		return "", wokeyGrokImageEditInputErrorf("Wokey Grok image edit does not support mask inputs")
	}
	if gjson.GetBytes(body, "reference_images").Exists() {
		return "", wokeyGrokImageEditInputErrorf("Wokey Grok image edit does not support reference_images")
	}

	var source gjson.Result
	switch {
	case image.Exists():
		if image.IsArray() {
			return "", wokeyGrokImageEditInputErrorf("Wokey Grok image edit accepts one image, not an image array")
		}
		source = image
	case images.Exists():
		if !images.IsArray() || len(images.Array()) != 1 {
			return "", wokeyGrokImageEditInputErrorf("Wokey Grok image edit accepts exactly one image in images")
		}
		source = images.Array()[0]
	default:
		return "", wokeyGrokImageEditInputErrorf("Wokey Grok image edit requires one image Data URL")
	}

	imageURL := strings.TrimSpace(extractGrokMediaImageURL(source))
	if imageURL == "" {
		return "", wokeyGrokImageEditInputErrorf("Wokey Grok image edit requires one image Data URL")
	}
	if !strings.HasPrefix(strings.ToLower(imageURL), "data:") {
		return "", wokeyGrokImageEditInputErrorf("Wokey Grok image edit requires a base64 image Data URL; remote image URLs are not supported")
	}
	return imageURL, nil
}

func decodeWokeyGrokImageEditDataURL(raw string) ([]byte, string, error) {
	metadata, encoded, ok := strings.Cut(strings.TrimSpace(raw), ",")
	if !ok || !strings.HasPrefix(strings.ToLower(metadata), "data:") {
		return nil, "", wokeyGrokImageEditInputErrorf("invalid Wokey Grok image edit Data URL")
	}

	metadataParts := strings.Split(metadata[len("data:"):], ";")
	if len(metadataParts) != 2 || !strings.EqualFold(strings.TrimSpace(metadataParts[1]), "base64") {
		return nil, "", wokeyGrokImageEditInputErrorf("Wokey Grok image edit requires a base64 Data URL")
	}
	declaredType := normalizeWokeyGrokImageEditContentType(metadataParts[0])
	if !isSupportedWokeyGrokImageEditContentType(declaredType) {
		return nil, "", wokeyGrokImageEditInputErrorf("unsupported Wokey Grok image edit image MIME type")
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(openAIImageMaxUploadPartSize) {
		return nil, "", wokeyGrokImageEditInputErrorf("Wokey Grok image edit image exceeds the %d MiB limit", openAIImageMaxUploadPartSize>>20)
	}

	imageData, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		imageData, err = base64.RawStdEncoding.Strict().DecodeString(encoded)
	}
	if err != nil {
		return nil, "", wokeyGrokImageEditInputErrorf("invalid Wokey Grok image edit base64 data: %v", err)
	}
	// The existing multipart parser uses LimitReader and can silently truncate
	// parts at this value. Reject the cap itself as well as larger JSON images.
	if len(imageData) == 0 || len(imageData) >= openAIImageMaxUploadPartSize {
		return nil, "", wokeyGrokImageEditInputErrorf("Wokey Grok image edit image must be smaller than %d MiB", openAIImageMaxUploadPartSize>>20)
	}
	actualType := sniffWokeyGrokImageEditContentType(imageData)
	if actualType == "" || actualType != declaredType {
		return nil, "", wokeyGrokImageEditInputErrorf("Wokey Grok image edit image MIME type does not match its image data")
	}
	if err := validateWokeyGrokImageEditImage(imageData); err != nil {
		return nil, "", err
	}
	return imageData, actualType, nil
}

func validateWokeyGrokImageEditImage(data []byte) error {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return wokeyGrokImageEditInputErrorf("invalid Wokey Grok image edit image structure")
	}
	if !isSupportedWokeyGrokImageEditContentType("image/" + format) {
		return wokeyGrokImageEditInputErrorf("unsupported Wokey Grok image edit image format")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > wokeyGrokImageEditMaxDimension || config.Height > wokeyGrokImageEditMaxDimension || int64(config.Width)*int64(config.Height) > wokeyGrokImageEditMaxPixels {
		return wokeyGrokImageEditInputErrorf("Wokey Grok image edit image dimensions exceed the supported limit")
	}
	decoded, decodedFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil || decodedFormat != format || decoded.Bounds().Dx() != config.Width || decoded.Bounds().Dy() != config.Height {
		return wokeyGrokImageEditInputErrorf("invalid Wokey Grok image edit image structure")
	}
	return nil
}

func wokeyGrokImageEditFormFields(body []byte) (map[string]string, error) {
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(body, &rawFields); err != nil {
		return nil, wokeyGrokImageEditInputErrorf("parse normalized Wokey Grok image edit JSON: %v", err)
	}
	fields := make(map[string]string, len(rawFields))
	for name, raw := range rawFields {
		switch name {
		case "image", "images", "mask", "reference_images":
			continue
		}
		if !isWokeyGrokImageEditFieldName(name) {
			return nil, wokeyGrokImageEditInputErrorf("unsupported Wokey Grok image edit field name")
		}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 {
			return nil, wokeyGrokImageEditInputErrorf("empty Wokey Grok image edit field %q", name)
		}
		switch trimmed[0] {
		case '"':
			var value string
			if err := json.Unmarshal(trimmed, &value); err != nil {
				return nil, wokeyGrokImageEditInputErrorf("parse Wokey Grok image edit field %q: %v", name, err)
			}
			fields[name] = value
		case 't', 'f', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			fields[name] = string(trimmed)
		case 'n':
			// JSON null has no multipart value; omit it without inventing a string.
		case '{', '[':
			return nil, wokeyGrokImageEditInputErrorf("Wokey Grok image edit field %q must be a scalar", name)
		default:
			return nil, wokeyGrokImageEditInputErrorf("invalid Wokey Grok image edit field %q", name)
		}
	}
	return fields, nil
}

func isWokeyGrokImageEditFieldName(name string) bool {
	if name == "" {
		return false
	}
	first := name[0]
	if (first < 'A' || first > 'Z') && (first < 'a' || first > 'z') && first != '_' {
		return false
	}
	for _, char := range name[1:] {
		if (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') &&
			(char < '0' || char > '9') && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

func sortedWokeyGrokImageEditFieldNames(fields map[string]string) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func normalizeWokeyGrokImageEditContentType(value string) string {
	contentType := strings.ToLower(strings.TrimSpace(value))
	if contentType == "image/jpg" {
		return "image/jpeg"
	}
	return contentType
}

func isSupportedWokeyGrokImageEditContentType(contentType string) bool {
	switch contentType {
	case "image/jpeg", "image/png", "image/webp":
		return true
	default:
		return false
	}
}

func sniffWokeyGrokImageEditContentType(data []byte) string {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return "image/png"
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return "image/jpeg"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	default:
		return ""
	}
}

func wokeyGrokImageEditFilename(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return "reference.jpg"
	case "image/webp":
		return "reference.webp"
	default:
		return "reference.png"
	}
}
