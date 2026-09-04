package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	kieJobsVideoDefaultAspectRatio = "16:9"
	// KIE's Marketplace form advertises a seven-image limit, while one prose
	// description of image_urls is stale and says one. The native createTask API
	// accepted and completed a four-image request on 2026-09-03; preserve the
	// documented seven-image ceiling rather than regressing to that stale limit.
	kieJobsVideoMaxExternalImages = 7
)

// prepareKIEJobsVideoCreateBody projects the public Grok video request into
// KIE's native Market jobs shape. KIE fetches its own public image URL, so do
// not forward Data URLs or multipart uploads that it cannot resolve.
func prepareKIEJobsVideoCreateBody(info GrokMediaRequestInfo, upstreamModel string) ([]byte, string, error) {
	prompt := strings.TrimSpace(info.Prompt)
	if prompt == "" {
		return nil, "", &GrokVideoInputValidationError{Message: "KIE video generation requires a non-empty prompt"}
	}
	if len(info.Uploads) > 0 {
		return nil, "", &GrokVideoInputValidationError{Message: "KIE video generation accepts public HTTPS image URLs, not uploaded image files"}
	}
	imageURLs := append([]string{}, info.InputImageURLs...)
	imageURLs = append(imageURLs, info.ReferenceImageURLs...)
	if len(imageURLs) > kieJobsVideoMaxExternalImages {
		return nil, "", &GrokVideoInputValidationError{Message: fmt.Sprintf("KIE Grok video accepts at most %d reference images", kieJobsVideoMaxExternalImages)}
	}
	for index, imageURL := range imageURLs {
		normalized, err := urlvalidator.ValidateHTTPSURL(imageURL, urlvalidator.ValidationOptions{AllowPrivate: false})
		if err != nil {
			return nil, "", &GrokVideoInputValidationError{Message: fmt.Sprintf("KIE reference image %d must be a public HTTPS URL", index)}
		}
		imageURLs[index] = normalized
	}

	aspectRatio := strings.TrimSpace(info.AspectRatio)
	if aspectRatio == "" {
		aspectRatio = kieJobsVideoDefaultAspectRatio
	}
	switch aspectRatio {
	case "1:1", "2:3", "3:2", "9:16", "16:9":
	default:
		return nil, "", &GrokVideoInputValidationError{Message: "KIE video aspect_ratio must be one of 1:1, 2:3, 3:2, 9:16, or 16:9"}
	}

	// The native 1.5-preview createTask schema does not define the legacy
	// Grok/Wokey `mode` field; sending it can make the KIE gateway reject an
	// otherwise valid image-to-video request.
	input := map[string]any{
		"prompt":       prompt,
		"aspect_ratio": aspectRatio,
		"resolution":   info.Resolution,
		"duration":     info.DurationSeconds,
	}
	if len(imageURLs) > 0 {
		input["image_urls"] = imageURLs
	}
	body, err := json.Marshal(map[string]any{
		"model": strings.TrimSpace(upstreamModel),
		"input": input,
	})
	if err != nil {
		return nil, "", fmt.Errorf("encode KIE video task: %w", err)
	}
	return body, "application/json", nil
}

func normalizeKIEJobsVideoCreateResponse(body []byte, requestedModel string) ([]byte, error) {
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("KIE task creation returned invalid JSON")
	}
	taskID := strings.TrimSpace(gjson.GetBytes(body, "data.taskId").String())
	if taskID == "" {
		return nil, fmt.Errorf("KIE task creation failed: %s", kieJobsResponseMessage(body))
	}
	return json.Marshal(map[string]any{
		"id":         taskID,
		"request_id": taskID,
		"status":     "pending",
		"model":      strings.TrimSpace(requestedModel),
	})
}

// normalizeKIEJobsVideoStatusResponse makes KIE's Market job record look like
// xAI's video status response so the established polling and deferred billing
// paths remain unchanged. KIE result URLs are subsequently proxied by Aiself.
func normalizeKIEJobsVideoStatusResponse(body []byte, requestID string) ([]byte, error) {
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("KIE task status returned invalid JSON")
	}
	data := gjson.GetBytes(body, "data")
	if !data.Exists() || !data.IsObject() {
		return nil, fmt.Errorf("KIE task status failed: %s", kieJobsResponseMessage(body))
	}
	taskID := strings.TrimSpace(gjson.GetBytes(body, "data.taskId").String())
	if taskID == "" {
		taskID = strings.TrimSpace(requestID)
	}
	if taskID == "" {
		return nil, fmt.Errorf("KIE task status failed: %s", kieJobsResponseMessage(body))
	}

	state := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "data.state").String()))
	status := "pending"
	switch state {
	case "success":
		status = "done"
	case "fail", "failed", "error", "cancelled", "canceled":
		status = "failed"
	case "waiting", "queuing", "queued", "generating", "processing", "pending", "":
		status = "pending"
	default:
		status = "pending"
	}

	payload := map[string]any{
		"id":         taskID,
		"request_id": taskID,
		"status":     status,
	}
	if model := strings.TrimSpace(gjson.GetBytes(body, "data.model").String()); model != "" {
		payload["model"] = model
	}
	if progress := gjson.GetBytes(body, "data.progress"); progress.Exists() && progress.Type == gjson.Number {
		payload["progress"] = progress.Value()
	}
	if resultURL := kieJobsResultURL(body); resultURL != "" {
		payload["video"] = map[string]any{"url": resultURL}
	}
	if status == "failed" {
		message := strings.TrimSpace(gjson.GetBytes(body, "data.failMsg").String())
		if message == "" {
			message = kieJobsResponseMessage(body)
		}
		if message != "" {
			payload["error"] = map[string]any{"message": message}
		}
	}
	return json.Marshal(payload)
}

func kieJobsResultURL(body []byte) string {
	result := gjson.GetBytes(body, "data.resultJson")
	if !result.Exists() {
		return ""
	}
	if result.Type == gjson.String {
		if !gjson.Valid(result.String()) {
			return ""
		}
		result = gjson.Parse(result.String())
	}
	return strings.TrimSpace(result.Get("resultUrls.0").String())
}

func kieJobsResponseMessage(body []byte) string {
	if message := strings.TrimSpace(gjson.GetBytes(body, "msg").String()); message != "" {
		return message
	}
	if message := strings.TrimSpace(gjson.GetBytes(body, "message").String()); message != "" {
		return message
	}
	if message := strings.TrimSpace(gjson.GetBytes(body, "data.failMsg").String()); message != "" {
		return message
	}
	return "upstream did not return a task ID"
}

func rewriteKIEJobsVideoContentURL(body []byte, proxyURL string) []byte {
	if strings.TrimSpace(proxyURL) == "" || strings.TrimSpace(gjson.GetBytes(body, "video.url").String()) == "" {
		return body
	}
	rewritten, err := sjson.SetBytes(body, "video.url", proxyURL)
	if err != nil {
		return body
	}
	return rewritten
}

func kieJobsSignedVideoContentURL(body []byte) (string, error) {
	rawURL := strings.TrimSpace(gjson.GetBytes(body, "video.url").String())
	if rawURL == "" {
		return "", nil
	}
	normalized, err := urlvalidator.ValidateHTTPSURL(rawURL, urlvalidator.ValidationOptions{AllowPrivate: false})
	if err != nil {
		return "", fmt.Errorf("KIE task status returned an unsupported video content URL")
	}
	return normalized, nil
}
