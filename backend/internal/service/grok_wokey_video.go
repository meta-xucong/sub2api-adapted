package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	wokeyVideoReferenceImageMaxBytes      = 8 << 20
	wokeyVideoReferenceImageMaxCount      = 4
	wokeyVideoMultimodalReferenceMaxCount = 7
	grokMediaMaxVideoReferenceImages      = 7
)

type wokeyVideoReferenceImage struct {
	Data        []byte
	ContentType string
	FileName    string
}

var wokeyVideoReferenceImageDownloader = downloadWokeyVideoReferenceImage

type GrokVideoInputValidationError struct {
	Message string
}

func (e *GrokVideoInputValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func normalizeWokeyVideoStatusForBilling(statusBody []byte) []byte {
	if len(statusBody) == 0 || !gjson.ValidBytes(statusBody) {
		return statusBody
	}
	status := strings.TrimSpace(gjson.GetBytes(statusBody, "status").String())
	videoURL := strings.TrimSpace(gjson.GetBytes(statusBody, "video_url").String())
	if !strings.EqualFold(status, "completed") || videoURL == "" {
		return statusBody
	}
	out, err := sjson.SetBytes(statusBody, "status", "done")
	if err != nil {
		return statusBody
	}
	if strings.TrimSpace(gjson.GetBytes(out, "video.url").String()) == "" {
		out, err = sjson.SetBytes(out, "video.url", videoURL)
		if err != nil {
			return statusBody
		}
	}
	return out
}

func grokMediaSignedVideoContentURLForAccount(account *Account, body []byte, requestID string) (string, error) {
	if account != nil && account.UsesKIEJobsVideoAPI() {
		return kieJobsSignedVideoContentURL(body)
	}
	return grokMediaSignedVideoContentURL(body, requestID)
}

func ValidateGrokVideoGenerationRequest(contentType string, body []byte) error {
	if !gjson.ValidBytes(body) {
		return nil
	}
	references := gjson.GetBytes(body, "reference_images")
	if !references.Exists() {
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return &GrokVideoInputValidationError{Message: "reference_to_video validation: reference_images requires application/json"}
	}
	if gjson.GetBytes(body, "image").Exists() || gjson.GetBytes(body, "images").Exists() {
		return &GrokVideoInputValidationError{Message: "reference_to_video validation: image/images and reference_images are mutually exclusive"}
	}
	if !references.IsArray() || len(references.Array()) == 0 {
		return &GrokVideoInputValidationError{Message: "reference_to_video validation: reference_images must be a non-empty JSON array"}
	}
	items := references.Array()
	if len(items) > grokMediaMaxVideoReferenceImages {
		return &GrokVideoInputValidationError{Message: fmt.Sprintf("reference_to_video validation: reference_images accepts at most %d images", grokMediaMaxVideoReferenceImages)}
	}
	for index, item := range items {
		if !item.IsObject() {
			return &GrokVideoInputValidationError{Message: fmt.Sprintf("reference_to_video validation: reference_images[%d] must be an object with a url", index)}
		}
		rawURL := strings.TrimSpace(item.Get("url").String())
		if rawURL == "" {
			return &GrokVideoInputValidationError{Message: fmt.Sprintf("reference_to_video validation: reference_images[%d].url is required", index)}
		}
	}
	return nil
}

func validateWokeyVideoReferenceURLs(referenceURLs []string) error {
	for index, rawURL := range referenceURLs {
		rawURL = strings.TrimSpace(rawURL)
		if strings.HasPrefix(strings.ToLower(rawURL), "data:") {
			return &GrokVideoInputValidationError{Message: fmt.Sprintf("reference_to_video validation: reference_images[%d].url must be HTTPS; the Aiself Wokey route does not accept Data URLs", index)}
		}
		rawParsed, parseErr := url.Parse(rawURL)
		if parseErr != nil || !strings.EqualFold(rawParsed.Scheme, "https") || strings.TrimSpace(rawParsed.Hostname()) == "" {
			return &GrokVideoInputValidationError{Message: fmt.Sprintf("reference_to_video validation: reference_images[%d].url must be a public HTTPS URL", index)}
		}
		if urlvalidator.ValidateResolvedIP(rawParsed.Hostname()) != nil {
			return &GrokVideoInputValidationError{Message: fmt.Sprintf("reference_to_video validation: reference_images[%d].url host is not allowed", index)}
		}
		normalized, validateErr := urlvalidator.ValidateHTTPSURL(rawURL, urlvalidator.ValidationOptions{AllowPrivate: false})
		if validateErr != nil {
			return &GrokVideoInputValidationError{Message: fmt.Sprintf("reference_to_video validation: reference_images[%d].url must be a public HTTPS URL", index)}
		}
		parsed, parseErr := url.Parse(normalized)
		if parseErr != nil || urlvalidator.ValidateResolvedIP(parsed.Hostname()) != nil {
			return &GrokVideoInputValidationError{Message: fmt.Sprintf("reference_to_video validation: reference_images[%d].url host is not allowed", index)}
		}
	}
	return nil
}

func normalizeGrokMediaForwardBodyForAccount(account *Account, endpoint GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	if isWokeyVideoGeneration(account, endpoint) {
		return normalizeWokeyVideoForwardBody(body, contentType, ParseGrokMediaRequest(contentType, body))
	}
	return normalizeGrokMediaForwardBody(endpoint, body, contentType)
}

func isWokeyVideoGeneration(account *Account, endpoint GrokMediaEndpoint) bool {
	return account != nil && endpoint == GrokMediaEndpointVideosGenerations && account.UsesWokeyVideoMultipart()
}

func normalizeWokeyVideoForwardBody(body []byte, contentType string, info GrokMediaRequestInfo) ([]byte, string, error) {
	if !gjson.ValidBytes(body) {
		return body, contentType, nil
	}
	out := body
	if info.HasReferenceImages() {
		// Keep the existing Wokey contract for data URLs, but leave HTTPS
		// validation to the downloader that owns the actual outbound request.
		for index, rawURL := range info.ReferenceImageURLs {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(rawURL)), "data:") {
				return nil, "", &GrokVideoInputValidationError{Message: fmt.Sprintf("reference_to_video validation: reference_images[%d].url must be HTTPS; the Aiself Wokey route does not accept Data URLs", index)}
			}
		}
		if mode := strings.TrimSpace(gjson.GetBytes(out, "mode").String()); mode != "" && mode != "multimodal_reference" {
			return nil, "", &GrokVideoInputValidationError{Message: "reference_to_video validation: Wokey requires mode=multimodal_reference"}
		}
		var err error
		out, err = sjson.SetBytes(out, "mode", "multimodal_reference")
		if err != nil {
			return nil, "", fmt.Errorf("set Wokey reference-to-video mode: %w", err)
		}
	}
	if !gjson.GetBytes(out, "video_resolution").Exists() {
		if resolution := strings.TrimSpace(gjson.GetBytes(out, "resolution").String()); resolution != "" {
			var err error
			out, err = sjson.SetBytes(out, "video_resolution", resolution)
			if err != nil {
				return nil, "", fmt.Errorf("rewrite Wokey video resolution: %w", err)
			}
		}
	}
	if gjson.GetBytes(out, "resolution").Exists() {
		var err error
		out, err = sjson.DeleteBytes(out, "resolution")
		if err != nil {
			return nil, "", fmt.Errorf("remove Grok video resolution: %w", err)
		}
	}
	if !gjson.GetBytes(out, "mode").Exists() {
		mode := "text_to_video"
		if info.HasInputImage() {
			mode = "image_to_video"
		}
		var err error
		out, err = sjson.SetBytes(out, "mode", mode)
		if err != nil {
			return nil, "", fmt.Errorf("set Wokey video mode: %w", err)
		}
	}
	if !gjson.GetBytes(out, "ratio").Exists() {
		var err error
		out, err = sjson.SetBytes(out, "ratio", "16:9")
		if err != nil {
			return nil, "", fmt.Errorf("set Wokey video ratio: %w", err)
		}
	}
	return out, contentType, nil
}

func prepareWokeyVideoImageMultipartBody(
	ctx context.Context,
	body []byte,
	contentType string,
	info GrokMediaRequestInfo,
) ([]byte, string, error) {
	if !gjson.ValidBytes(body) {
		return body, contentType, nil
	}
	imageURLs := info.InputImageURLs
	maxImages := wokeyVideoReferenceImageMaxCount
	mode := "image_to_video"
	if info.HasReferenceImages() {
		imageURLs = info.ReferenceImageURLs
		maxImages = wokeyVideoMultimodalReferenceMaxCount
		mode = "multimodal_reference"
	}
	if len(imageURLs) == 0 {
		return body, contentType, nil
	}
	if len(imageURLs) > maxImages {
		return nil, "", fmt.Errorf("Wokey %s accepts at most %d images", mode, maxImages)
	}

	images := make([]wokeyVideoReferenceImage, 0, len(imageURLs))
	for _, rawURL := range imageURLs {
		image, err := wokeyVideoReferenceImageDownloader(ctx, rawURL)
		if err != nil {
			return nil, "", err
		}
		images = append(images, image)
	}
	return buildWokeyVideoMultipartBody(body, images)
}

func buildWokeyVideoMultipartBody(body []byte, images []wokeyVideoReferenceImage) ([]byte, string, error) {
	if len(images) == 0 {
		return body, "application/json", nil
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, "", fmt.Errorf("decode Wokey video request: %w", err)
	}

	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if isWokeyVideoImageField(key) {
			continue
		}
		value := fields[key]
		var scalar string
		switch {
		case len(value) > 0 && value[0] == '"':
			if err := json.Unmarshal(value, &scalar); err != nil {
				return nil, "", fmt.Errorf("decode Wokey video field %s: %w", key, err)
			}
		case len(value) > 0 && (value[0] == '-' || value[0] >= '0' && value[0] <= '9' || value[0] == 't' || value[0] == 'f'):
			scalar = string(value)
		default:
			continue
		}
		if err := writer.WriteField(key, scalar); err != nil {
			return nil, "", fmt.Errorf("write Wokey video field %s: %w", key, err)
		}
	}

	for _, image := range images {
		if len(image.Data) == 0 {
			return nil, "", errors.New("Wokey reference image is empty")
		}
		filename := safeWokeyVideoFileName(image.FileName, image.ContentType)
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, "image[]", filename))
		header.Set("Content-Type", image.ContentType)
		part, err := writer.CreatePart(header)
		if err != nil {
			return nil, "", fmt.Errorf("create Wokey image part: %w", err)
		}
		if _, err := part.Write(image.Data); err != nil {
			return nil, "", fmt.Errorf("write Wokey image part: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("close Wokey multipart body: %w", err)
	}
	return buffer.Bytes(), writer.FormDataContentType(), nil
}

func isWokeyVideoImageField(key string) bool {
	switch strings.TrimSpace(key) {
	case "image", "images", "reference_images", "mask", "image_url", "mask_image_url":
		return true
	default:
		return false
	}
}

func downloadWokeyVideoReferenceImage(ctx context.Context, rawURL string) (wokeyVideoReferenceImage, error) {
	rawURL = strings.TrimSpace(rawURL)
	if strings.HasPrefix(strings.ToLower(rawURL), "data:") {
		return decodeWokeyVideoDataURL(rawURL)
	}
	normalized, err := urlvalidator.ValidateHTTPSURL(rawURL, urlvalidator.ValidationOptions{AllowPrivate: false})
	if err != nil {
		return wokeyVideoReferenceImage{}, fmt.Errorf("reference image URL rejected: %w", err)
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return wokeyVideoReferenceImage{}, fmt.Errorf("parse reference image URL: %w", err)
	}
	if err := urlvalidator.ValidateResolvedIP(parsed.Hostname()); err != nil {
		return wokeyVideoReferenceImage{}, fmt.Errorf("reference image host rejected: %w", err)
	}

	client, err := httpclient.GetClient(httpclient.Options{
		Timeout:               30 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ValidateResolvedIP:    true,
		AllowPrivateHosts:     false,
		MaxConnsPerHost:       2,
	})
	if err != nil {
		return wokeyVideoReferenceImage{}, fmt.Errorf("build reference image client: %w", err)
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
		redirectURL, redirectErr := urlvalidator.ValidateHTTPSURL(req.URL.String(), urlvalidator.ValidationOptions{AllowPrivate: false})
		if redirectErr != nil {
			return redirectErr
		}
		redirectParsed, parseErr := url.Parse(redirectURL)
		if parseErr != nil {
			return parseErr
		}
		return urlvalidator.ValidateResolvedIP(redirectParsed.Hostname())
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, normalized, nil)
	if err != nil {
		return wokeyVideoReferenceImage{}, fmt.Errorf("build reference image request: %w", err)
	}
	request.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,image/*;q=0.8")
	response, err := clientCopy.Do(request)
	if err != nil {
		return wokeyVideoReferenceImage{}, fmt.Errorf("download reference image: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return wokeyVideoReferenceImage{}, fmt.Errorf("download reference image: HTTP %d", response.StatusCode)
	}
	if response.ContentLength > wokeyVideoReferenceImageMaxBytes {
		return wokeyVideoReferenceImage{}, fmt.Errorf("reference image exceeds %d bytes", wokeyVideoReferenceImageMaxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, wokeyVideoReferenceImageMaxBytes+1))
	if err != nil {
		return wokeyVideoReferenceImage{}, fmt.Errorf("read reference image: %w", err)
	}
	if len(data) == 0 || len(data) > wokeyVideoReferenceImageMaxBytes {
		return wokeyVideoReferenceImage{}, fmt.Errorf("reference image exceeds %d bytes", wokeyVideoReferenceImageMaxBytes)
	}
	contentType := firstWokeyImageContentType(response.Header.Get("Content-Type"), data)
	if contentType == "" {
		return wokeyVideoReferenceImage{}, errors.New("reference image is not a supported image")
	}
	return wokeyVideoReferenceImage{
		Data:        data,
		ContentType: contentType,
		FileName:    safeWokeyVideoFileName(path.Base(parsed.Path), contentType),
	}, nil
}

func decodeWokeyVideoDataURL(rawURL string) (wokeyVideoReferenceImage, error) {
	meta, encoded, ok := strings.Cut(rawURL, ",")
	if !ok {
		return wokeyVideoReferenceImage{}, errors.New("invalid reference image data URL")
	}
	mediaType := strings.TrimPrefix(strings.TrimSpace(strings.SplitN(meta, ";", 2)[0]), "data:")
	var data []byte
	var err error
	if strings.Contains(strings.ToLower(meta), ";base64") {
		data, err = base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	} else {
		decoded, decodeErr := url.PathUnescape(encoded)
		err = decodeErr
		data = []byte(decoded)
	}
	if err != nil {
		return wokeyVideoReferenceImage{}, fmt.Errorf("decode reference image data URL: %w", err)
	}
	contentType := firstWokeyImageContentType(mediaType, data)
	if contentType == "" {
		return wokeyVideoReferenceImage{}, errors.New("reference image data URL is not a supported image")
	}
	if len(data) == 0 || len(data) > wokeyVideoReferenceImageMaxBytes {
		return wokeyVideoReferenceImage{}, fmt.Errorf("reference image exceeds %d bytes", wokeyVideoReferenceImageMaxBytes)
	}
	return wokeyVideoReferenceImage{Data: data, ContentType: contentType, FileName: safeWokeyVideoFileName("reference", contentType)}, nil
}

func firstWokeyImageContentType(declared string, data []byte) string {
	declared = strings.ToLower(strings.TrimSpace(strings.SplitN(declared, ";", 2)[0]))
	detected := strings.ToLower(strings.TrimSpace(strings.SplitN(http.DetectContentType(data), ";", 2)[0]))
	allowed := func(value string) bool {
		switch value {
		case "image/png", "image/jpeg", "image/webp", "image/gif":
			return true
		default:
			return false
		}
	}
	if allowed(detected) {
		return detected
	}
	if allowed(declared) {
		return declared
	}
	return ""
}

func safeWokeyVideoFileName(name, contentType string) string {
	name = path.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, name)
	if name == "" || name == "." || name == "_" {
		name = "reference"
	}
	if !strings.Contains(name, ".") {
		ext := ".png"
		switch strings.ToLower(contentType) {
		case "image/jpeg":
			ext = ".jpg"
		case "image/webp":
			ext = ".webp"
		case "image/gif":
			ext = ".gif"
		}
		name += ext
	}
	return name
}
