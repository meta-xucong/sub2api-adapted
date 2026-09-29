//go:build unit

package service

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestKIEJobsVideoSchemaAndStatusNegativeCases(t *testing.T) {
	info := GrokMediaRequestInfo{
		Model:           "veo-3.1",
		Prompt:          "a red kite over water",
		AspectRatio:     "16:9",
		Resolution:      "720p",
		DurationSeconds: 8,
		InputImageURLs: []string{
			"https://images.example/one.png",
		},
	}
	body, contentType, err := prepareKIEJobsVideoCreateBody(info, "veo-3.1")
	if err != nil {
		t.Fatalf("prepare KIE body: %v", err)
	}
	if contentType != "application/json" || !gjson.GetBytes(body, "input.image_urls.0").Exists() {
		t.Fatalf("unexpected KIE body: %s", body)
	}
	if gjson.GetBytes(body, "mode").Exists() {
		t.Fatal("KIE body must not carry Wokey mode")
	}

	badInfo := info
	badInfo.InputImageURLs = []string{"http://127.0.0.1/private.png"}
	if _, _, err := prepareKIEJobsVideoCreateBody(badInfo, "veo-3.1"); err == nil {
		t.Fatal("private KIE image URL must be rejected")
	}

	tooMany := info
	tooMany.InputImageURLs = make([]string, kieJobsVideoMaxExternalImages+1)
	if _, _, err := prepareKIEJobsVideoCreateBody(tooMany, "veo-3.1"); err == nil {
		t.Fatal("KIE image limit must be enforced")
	}

	success, err := normalizeKIEJobsVideoStatusResponse([]byte(`{"data":{"taskId":"task-1","state":"success","resultJson":"{\"resultUrls\":[\"https://cdn.example/video.mp4\"]}"}}`), "")
	if err != nil || gjson.GetBytes(success, "status").String() != "done" || gjson.GetBytes(success, "video.url").String() == "" {
		t.Fatalf("KIE success normalization failed: %s (%v)", success, err)
	}
	if _, err := normalizeKIEJobsVideoStatusResponse([]byte(`{"data":{"state":"success"}}`), ""); err == nil {
		t.Fatal("KIE status without task ID must be rejected")
	}
}

func TestWokeyVideoNegativeModeAndDefaultNormalization(t *testing.T) {
	body := []byte(`{"model":"grok-imagine-video","prompt":"p","reference_images":[{"url":"https://images.example/a.png"}],"mode":"image_to_video"}`)
	info := ParseGrokMediaRequest("application/json", body)
	if !info.HasReferenceImages() {
		t.Fatal("reference_images must remain distinct for provider adapters")
	}
	if _, _, err := normalizeWokeyVideoForwardBody(body, "application/json", info); err == nil || !strings.Contains(err.Error(), "multimodal_reference") {
		t.Fatalf("wrong Wokey reference mode should be rejected, got %v", err)
	}

	textBody, _, err := normalizeWokeyVideoForwardBody([]byte(`{"model":"grok-imagine-video","prompt":"p"}`), "application/json", GrokMediaRequestInfo{})
	if err != nil || gjson.GetBytes(textBody, "mode").String() != "text_to_video" || gjson.GetBytes(textBody, "ratio").String() != "16:9" {
		t.Fatalf("Wokey defaults not applied: %s (%v)", textBody, err)
	}
}
