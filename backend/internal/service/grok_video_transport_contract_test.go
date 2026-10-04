//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
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

func TestKIEJobsMediaURLUsesNativeRoutes(t *testing.T) {
	validator := func(raw string) (string, error) { return raw, nil }
	createURL, err := buildKIEJobsMediaURL("https://api.kie.ai/v1", GrokMediaEndpointVideosGenerations, "", validator)
	if err != nil || createURL != "https://api.kie.ai/v1/api/v1/jobs/createTask" {
		t.Fatalf("KIE create URL = %q, err=%v", createURL, err)
	}
	statusURL, err := buildKIEJobsMediaURL("https://api.kie.ai/v1", GrokMediaEndpointVideoStatus, "task/one", validator)
	if err != nil || statusURL != "https://api.kie.ai/v1/api/v1/jobs/recordInfo?taskId=task%2Fone" {
		t.Fatalf("KIE status URL = %q, err=%v", statusURL, err)
	}
	if _, err := buildKIEJobsMediaURL("https://api.kie.ai/v1", GrokMediaEndpointVideoContent, "task-1", validator); err == nil {
		t.Fatal("KIE content must use the signed result URL, not a native content route")
	}
}

func TestWokeyVideoMultipartUsesMockedReferenceImages(t *testing.T) {
	originalDownloader := wokeyVideoReferenceImageDownloader
	var downloaded []string
	wokeyVideoReferenceImageDownloader = func(_ context.Context, rawURL string) (wokeyVideoReferenceImage, error) {
		downloaded = append(downloaded, rawURL)
		return wokeyVideoReferenceImage{Data: []byte("image-bytes"), ContentType: "image/png", FileName: "ref.png"}, nil
	}
	t.Cleanup(func() { wokeyVideoReferenceImageDownloader = originalDownloader })

	body := []byte(`{"model":"grok-imagine-video","prompt":"p","mode":"multimodal_reference","reference_images":[{"url":"https://images.example/one.png"},{"url":"https://images.example/two.png"}]}`)
	info := ParseGrokMediaRequest("application/json", body)
	got, contentType, err := prepareWokeyVideoImageMultipartBody(context.Background(), body, "application/json", info)
	if err != nil {
		t.Fatalf("prepare Wokey multipart: %v", err)
	}
	if len(downloaded) != 2 || downloaded[0] != info.ReferenceImageURLs[0] || downloaded[1] != info.ReferenceImageURLs[1] {
		t.Fatalf("reference image download order = %#v", downloaded)
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("multipart content type = %q, err=%v", contentType, err)
	}
	reader := multipart.NewReader(bytes.NewReader(got), params["boundary"])
	imageParts := 0
	fields := map[string]string{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read multipart: %v", err)
		}
		value, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if part.FormName() == "image[]" {
			imageParts++
			if string(value) != "image-bytes" || part.Header.Get("Content-Type") != "image/png" {
				t.Fatalf("unexpected image part: %q, %q", value, part.Header.Get("Content-Type"))
			}
		} else {
			fields[part.FormName()] = string(value)
		}
	}
	if imageParts != 2 || fields["mode"] != "multimodal_reference" || fields["prompt"] != "p" {
		t.Fatalf("unexpected Wokey multipart fields: images=%d fields=%#v", imageParts, fields)
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

func TestNormalizeWokeyVideoForwardBodyDoesNotPreflightRelayDNS(t *testing.T) {
	body := []byte(`{"model":"grok-imagine-video-1.5","reference_images":[{"url":"https://video.aiself.vip/provider-input/relay-token/frame.png"},{"url":"https://video.aiself.vip.invalid/provider-input/test-only/frame.png"}]}`)
	info := ParseGrokMediaRequest("application/json", body)

	normalized, contentType, err := normalizeWokeyVideoForwardBody(body, "application/json", info)
	if err != nil {
		t.Fatalf("normalization must not preflight DNS: %v", err)
	}
	if contentType != "application/json" || gjson.GetBytes(normalized, "mode").String() != "multimodal_reference" {
		t.Fatalf("unexpected normalized Wokey request: content-type=%q body=%s", contentType, normalized)
	}
	if got := gjson.GetBytes(normalized, "reference_images.0.url").String(); got != "https://video.aiself.vip/provider-input/relay-token/frame.png" {
		t.Fatalf("reference URL changed during normalization: %q", got)
	}
}

func TestNormalizeWokeyVideoForwardBodyRejectsDataURLWithoutDNSPreflight(t *testing.T) {
	body := []byte(`{"model":"grok-imagine-video-1.5","reference_images":[{"url":"data:image/png;base64,AA=="}]}`)
	_, _, err := normalizeWokeyVideoForwardBody(body, "application/json", ParseGrokMediaRequest("application/json", body))
	if err == nil || !strings.Contains(err.Error(), "does not accept Data URLs") {
		t.Fatalf("expected Data URL rejection, got %v", err)
	}
}
