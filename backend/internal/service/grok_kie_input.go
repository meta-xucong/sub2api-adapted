package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

const (
	// KIE documents a 20 MB maximum for each Grok Imagine Video reference image.
	kieJobsVideoReferenceImageMaxBytes  = 20 << 20
	kieJobsVideoImageProbeTimeout       = 10 * time.Second
	kieJobsVideoImageProbeHeaderTimeout = 5 * time.Second
	kieJobsVideoImageProbeBytes         = 512
)

type kieJobsVideoImageURLProbe struct {
	statusCode    int
	contentType   string
	contentLength int64
}

// KIEImageProbeRecord is deliberately URL-blind: the hash lets operators
// correlate a provider-input request without persisting signed URLs or tokens.
type KIEImageProbeRecord struct {
	Index         int    `json:"index"`
	URLSHA256     string `json:"url_sha256"`
	StatusCode    int    `json:"status_code,omitempty"`
	ContentType   string `json:"content_type,omitempty"`
	ContentLength int64  `json:"content_length,omitempty"`
	DurationMs    int64  `json:"duration_ms"`
	ErrorClass    string `json:"error_class,omitempty"`
}

type KIEImageProbeSummary struct {
	ImageCount int                   `json:"image_count"`
	Records    []KIEImageProbeRecord `json:"records"`
}

// Kept injectable so request validation can be tested without network access.
var kieJobsVideoImageURLProber = probeKIEJobsVideoImageURL

// validateKIEJobsVideoImageURLs prevents a paid KIE task from being created
// when an image URL is already known to be unreachable or not an image. KIE's
// native API fetches image_urls asynchronously, so without this small preflight
// a bad public URL is reported later as a generic upstream 502.
func validateKIEJobsVideoImageURLs(ctx context.Context, imageURLs []string) error {
	_, err := validateKIEJobsVideoImageURLsWithSummary(ctx, imageURLs)
	return err
}

func validateKIEJobsVideoImageURLsWithSummary(ctx context.Context, imageURLs []string) (KIEImageProbeSummary, error) {
	summary := KIEImageProbeSummary{ImageCount: len(imageURLs)}
	for index, rawURL := range imageURLs {
		record := KIEImageProbeRecord{
			Index:     index,
			URLSHA256: kieImageURLSHA256(rawURL),
		}
		probeStarted := time.Now()
		normalized, err := urlvalidator.ValidateHTTPSURL(rawURL, urlvalidator.ValidationOptions{AllowPrivate: false})
		if err != nil {
			record.DurationMs = time.Since(probeStarted).Milliseconds()
			record.ErrorClass = "invalid_public_https_url"
			summary.Records = append(summary.Records, record)
			return summary, &GrokVideoInputValidationError{Message: fmt.Sprintf("KIE reference image %d must be a public HTTPS URL", index)}
		}
		record.URLSHA256 = kieImageURLSHA256(normalized)
		probe, err := kieJobsVideoImageURLProber(ctx, normalized)
		record.DurationMs = time.Since(probeStarted).Milliseconds()
		record.StatusCode = probe.statusCode
		record.ContentType = probe.contentType
		record.ContentLength = probe.contentLength
		if err != nil {
			record.ErrorClass = "probe_failed"
			summary.Records = append(summary.Records, record)
			// Do not include the URL or transport error: signed query strings and
			// provider tokens must never be reflected in a client-visible error.
			return summary, &GrokVideoInputValidationError{Message: fmt.Sprintf("KIE reference image %d could not be fetched before submission", index)}
		}
		if probe.statusCode < http.StatusOK || probe.statusCode >= http.StatusMultipleChoices {
			record.ErrorClass = "http_status"
			summary.Records = append(summary.Records, record)
			return summary, &GrokVideoInputValidationError{Message: fmt.Sprintf("KIE reference image %d URL returned HTTP %d", index, probe.statusCode)}
		}
		if probe.contentLength > kieJobsVideoReferenceImageMaxBytes {
			record.ErrorClass = "size_limit"
			summary.Records = append(summary.Records, record)
			return summary, &GrokVideoInputValidationError{Message: fmt.Sprintf("KIE reference image %d exceeds the %d MB limit", index, kieJobsVideoReferenceImageMaxBytes/(1<<20))}
		}
		if !isKIESupportedVideoImageContentType(probe.contentType) {
			record.ErrorClass = "unsupported_mime"
			summary.Records = append(summary.Records, record)
			return summary, &GrokVideoInputValidationError{Message: fmt.Sprintf("KIE reference image %d must return JPEG, PNG, or WebP content", index)}
		}
		summary.Records = append(summary.Records, record)
	}
	return summary, nil
}

func kieImageURLSHA256(rawURL string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(rawURL)))
	return hex.EncodeToString(digest[:])
}

func probeKIEJobsVideoImageURL(ctx context.Context, normalizedURL string) (kieJobsVideoImageURLProbe, error) {
	parsed, err := url.Parse(normalizedURL)
	if err != nil {
		return kieJobsVideoImageURLProbe{}, err
	}
	if err := urlvalidator.ValidateResolvedIP(parsed.Hostname()); err != nil {
		return kieJobsVideoImageURLProbe{}, err
	}
	client, err := httpclient.GetClient(httpclient.Options{
		Timeout:               kieJobsVideoImageProbeTimeout,
		ResponseHeaderTimeout: kieJobsVideoImageProbeHeaderTimeout,
		ValidateResolvedIP:    true,
		AllowPrivateHosts:     false,
		MaxConnsPerHost:       2,
	})
	if err != nil {
		return kieJobsVideoImageURLProbe{}, err
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
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, normalizedURL, nil)
	if err != nil {
		return kieJobsVideoImageURLProbe{}, err
	}
	// A small range makes the probe cheap while still allowing MIME sniffing.
	// Servers that ignore Range are bounded by the read below and the response
	// body is closed immediately after the sample.
	request.Header.Set("Range", "bytes=0-511")
	request.Header.Set("Accept", "image/jpeg,image/png,image/webp,image/*;q=0.8")
	request.Header.Set("User-Agent", "Sub2API-KIE-Input-Check/1.0")
	response, err := clientCopy.Do(request)
	if err != nil {
		return kieJobsVideoImageURLProbe{}, err
	}
	defer response.Body.Close()

	contentLength := response.ContentLength
	if contentRange := strings.TrimSpace(response.Header.Get("Content-Range")); contentRange != "" {
		parts := strings.SplitN(contentRange, "/", 2)
		if len(parts) == 2 {
			total := strings.TrimSpace(parts[1])
			if total != "" && total != "*" {
				if parsedTotal, parseErr := strconv.ParseInt(total, 10, 64); parseErr == nil {
					contentLength = parsedTotal
				}
			}
		}
	}
	if contentLength > kieJobsVideoReferenceImageMaxBytes {
		return kieJobsVideoImageURLProbe{
			statusCode:    response.StatusCode,
			contentType:   strings.TrimSpace(response.Header.Get("Content-Type")),
			contentLength: contentLength,
		}, nil
	}
	sample, err := io.ReadAll(io.LimitReader(response.Body, kieJobsVideoImageProbeBytes))
	if err != nil {
		return kieJobsVideoImageURLProbe{}, err
	}
	// Some CDNs ignore Range and omit both Content-Length and Content-Range.
	// Count only up to the configured limit in that case so an oversized body is
	// rejected without buffering an unbounded response in memory.
	if contentLength < 0 {
		remaining := int64(kieJobsVideoReferenceImageMaxBytes+1) - int64(len(sample))
		if remaining > 0 {
			consumed, copyErr := io.Copy(io.Discard, io.LimitReader(response.Body, remaining))
			if copyErr != nil {
				return kieJobsVideoImageURLProbe{}, copyErr
			}
			if consumed == remaining {
				contentLength = int64(kieJobsVideoReferenceImageMaxBytes + 1)
			} else {
				contentLength = int64(len(sample)) + consumed
			}
		}
	}
	return kieJobsVideoImageURLProbe{
		statusCode:    response.StatusCode,
		contentType:   kieVideoImageContentType(response.Header.Get("Content-Type"), sample),
		contentLength: contentLength,
	}, nil
}

func kieVideoImageContentType(declared string, sample []byte) string {
	if len(sample) == 0 {
		return ""
	}
	declared = strings.ToLower(strings.TrimSpace(strings.SplitN(declared, ";", 2)[0]))
	detected := strings.ToLower(strings.TrimSpace(strings.SplitN(http.DetectContentType(sample), ";", 2)[0]))
	if isKIESupportedVideoImageContentType(detected) {
		return detected
	}
	// Trust a declared image type only when sniffing is inconclusive. If the
	// bytes clearly identify HTML/JSON (a common CDN error response), do not let
	// a misleading Content-Type turn that error page into a valid image URL.
	if detected == "application/octet-stream" && isKIESupportedVideoImageContentType(declared) {
		return declared
	}
	return detected
}

func isKIESupportedVideoImageContentType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0])) {
	case "image/jpeg", "image/jpg", "image/png", "image/webp":
		return true
	default:
		return false
	}
}
