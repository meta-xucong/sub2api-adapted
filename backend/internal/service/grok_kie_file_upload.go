package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	// KIE's file-stream endpoint returns a temporary public downloadUrl which
	// can be used by createTask without making KIE reach the Aiself relay.
	kieJobsVideoFileUploadURL        = "https://kieai.redpandaai.co/api/file-stream-upload"
	kieJobsVideoImageDownloadLimit   = 20 << 20
	kieJobsVideoImageDownloadTimeout = 30 * time.Second
	kieJobsVideoUploadTimeout        = 45 * time.Second
	kieJobsVideoUploadResponseLimit  = 1 << 20
	kieJobsVideoRelayHost            = "video.aiself.vip"
)

type kieJobsVideoReferenceImage struct {
	Data        []byte
	ContentType string
	FileName    string
}

// Kept injectable so the request projection can be tested without contacting
// the relay. Production uses the SSRF-protected downloader below.
var kieJobsVideoReferenceImageDownloader = downloadKIEJobsVideoReferenceImage

func (a *Account) kieJobsVideoReferenceUploadMode() string {
	if a == nil {
		return "relay"
	}
	mode := strings.ToLower(strings.TrimSpace(a.GetCredential("kie_reference_upload_mode")))
	switch mode {
	case "always", "all", "enabled", "on":
		return "always"
	case "off", "disabled", "none":
		return "off"
	default:
		return "relay"
	}
}

func (a *Account) shouldUploadKIEJobsReference(rawURL string) bool {
	if a == nil || a.kieJobsVideoReferenceUploadMode() == "off" {
		return false
	}
	if a.kieJobsVideoReferenceUploadMode() == "always" {
		return true
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	configured := strings.TrimSpace(a.GetCredential("kie_reference_upload_hosts"))
	if configured != "" {
		for _, candidate := range strings.Split(configured, ",") {
			if strings.EqualFold(strings.TrimSpace(candidate), host) {
				return true
			}
		}
		return false
	}
	return host == kieJobsVideoRelayHost
}

// materializeKIEJobsVideoImageURLs replaces only configured relay URLs with
// KIE-hosted temporary URLs. It is deliberately called after normal input
// validation and before createTask, so an upload failure cannot create a paid
// provider task.
func (s *OpenAIGatewayService) materializeKIEJobsVideoImageURLs(
	ctx context.Context,
	account *Account,
	token string,
	body []byte,
) ([]byte, error) {
	if account == nil || len(body) == 0 {
		return body, nil
	}
	imageURLs := gjson.GetBytes(body, "input.image_urls").Array()
	if len(imageURLs) == 0 {
		return body, nil
	}
	for index, value := range imageURLs {
		rawURL := strings.TrimSpace(value.String())
		if rawURL == "" || !account.shouldUploadKIEJobsReference(rawURL) {
			continue
		}
		image, err := kieJobsVideoReferenceImageDownloader(ctx, rawURL)
		if err != nil {
			return nil, &GrokVideoInputValidationError{Message: fmt.Sprintf("KIE reference image %d could not be materialized", index)}
		}
		downloadURL, err := s.uploadKIEJobsReferenceImage(ctx, account, token, image, rawURL)
		if err != nil {
			return nil, &GrokVideoInputValidationError{Message: fmt.Sprintf("KIE reference image %d could not be uploaded", index)}
		}
		body, err = sjson.SetBytes(body, fmt.Sprintf("input.image_urls.%d", index), downloadURL)
		if err != nil {
			return nil, fmt.Errorf("rewrite KIE reference image URL: %w", err)
		}
	}
	return body, nil
}

func (s *OpenAIGatewayService) uploadKIEJobsReferenceImage(
	ctx context.Context,
	account *Account,
	token string,
	image kieJobsVideoReferenceImage,
	sourceURL string,
) (string, error) {
	if len(image.Data) == 0 || !isKIESupportedVideoImageContentType(image.ContentType) {
		return "", errors.New("unsupported reference image")
	}
	if len(image.Data) > kieJobsVideoReferenceImageMaxBytes {
		return "", errors.New("reference image is too large")
	}
	fileName := strings.TrimSpace(image.FileName)
	if fileName == "" {
		fileName = safeKIEJobsReferenceFileName(sourceURL, image.ContentType)
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return "", err
	}
	if _, err = part.Write(image.Data); err != nil {
		return "", err
	}
	if err = writer.WriteField("uploadPath", "images/sub2api/grok-video"); err != nil {
		return "", err
	}
	if err = writer.WriteField("fileName", fileName); err != nil {
		return "", err
	}
	if err = writer.Close(); err != nil {
		return "", err
	}

	requestCtx, cancel := context.WithTimeout(ctx, kieJobsVideoUploadTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, kieJobsVideoFileUploadURL, bytes.NewReader(buffer.Bytes()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	account.ApplyHeaderOverrides(request.Header)
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	if s == nil || s.httpUpstream == nil {
		return "", errors.New("KIE file upload upstream is unavailable")
	}
	response, err := s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return "", err
	}
	if response == nil {
		return "", errors.New("KIE file upload returned no response")
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, kieJobsVideoUploadResponseLimit))
	if readErr != nil {
		return "", readErr
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("KIE file upload returned HTTP %d", response.StatusCode)
	}
	if !gjson.ValidBytes(responseBody) {
		return "", errors.New("KIE file upload returned invalid JSON")
	}
	if success := gjson.GetBytes(responseBody, "success"); success.Exists() && !success.Bool() {
		return "", errors.New("KIE file upload was not successful")
	}
	downloadURL := strings.TrimSpace(firstNonEmpty(
		gjson.GetBytes(responseBody, "data.downloadUrl").String(),
		gjson.GetBytes(responseBody, "data.download_url").String(),
	))
	if downloadURL == "" {
		return "", errors.New("KIE file upload did not return downloadUrl")
	}
	normalized, err := urlvalidator.ValidateHTTPSURL(downloadURL, urlvalidator.ValidationOptions{AllowPrivate: false})
	if err != nil {
		return "", errors.New("KIE file upload returned an unsafe downloadUrl")
	}
	return normalized, nil
}

func downloadKIEJobsVideoReferenceImage(ctx context.Context, rawURL string) (kieJobsVideoReferenceImage, error) {
	normalized, err := urlvalidator.ValidateHTTPSURL(rawURL, urlvalidator.ValidationOptions{AllowPrivate: false})
	if err != nil {
		return kieJobsVideoReferenceImage{}, err
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return kieJobsVideoReferenceImage{}, err
	}
	if err := urlvalidator.ValidateResolvedIP(parsed.Hostname()); err != nil {
		return kieJobsVideoReferenceImage{}, err
	}
	client, err := httpclient.GetClient(httpclient.Options{
		Timeout:               kieJobsVideoImageDownloadTimeout,
		ResponseHeaderTimeout: 10 * time.Second,
		ValidateResolvedIP:    true,
		AllowPrivateHosts:     false,
		MaxConnsPerHost:       2,
	})
	if err != nil {
		return kieJobsVideoReferenceImage{}, err
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
		return kieJobsVideoReferenceImage{}, err
	}
	request.Header.Set("Accept", "image/png,image/jpeg,image/webp,image/*;q=0.8")
	response, err := clientCopy.Do(request)
	if err != nil {
		return kieJobsVideoReferenceImage{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return kieJobsVideoReferenceImage{}, fmt.Errorf("reference image returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > kieJobsVideoImageDownloadLimit {
		return kieJobsVideoReferenceImage{}, errors.New("reference image is too large")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, kieJobsVideoImageDownloadLimit+1))
	if err != nil {
		return kieJobsVideoReferenceImage{}, err
	}
	if len(data) == 0 || len(data) > kieJobsVideoImageDownloadLimit {
		return kieJobsVideoReferenceImage{}, errors.New("reference image is empty or too large")
	}
	contentType := kieVideoImageContentType(response.Header.Get("Content-Type"), data)
	if !isKIESupportedVideoImageContentType(contentType) {
		return kieJobsVideoReferenceImage{}, errors.New("reference image is not a supported image")
	}
	return kieJobsVideoReferenceImage{
		Data:        data,
		ContentType: contentType,
		FileName:    safeKIEJobsReferenceFileName(path.Base(parsed.Path), contentType),
	}, nil
}

func safeKIEJobsReferenceFileName(source string, contentType string) string {
	name := strings.TrimSpace(path.Base(source))
	if name == "" || name == "." || name == "/" || strings.ContainsAny(name, "\\\x00\r\n") {
		name = "reference"
	}
	if !strings.Contains(name, ".") {
		switch contentType {
		case "image/jpeg", "image/jpg":
			name += ".jpg"
		case "image/webp":
			name += ".webp"
		default:
			name += ".png"
		}
	}
	return name
}
