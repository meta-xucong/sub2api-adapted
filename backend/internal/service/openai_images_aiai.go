package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	aiaiImagesPollInterval = 5 * time.Second
	aiaiImagesPollTimeout  = 3 * time.Minute
)

func isAIAIImageAccount(account *Account) bool {
	return account != nil && account.IsOpenAIApiKey() && isAIAIImageBaseURL(account.GetOpenAIBaseURL())
}

func isAIAIImageBaseURL(baseURL string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(baseURL))
	if trimmed == "" {
		return false
	}
	host := trimmed
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Hostname() != "" {
		host = strings.ToLower(parsed.Hostname())
	}
	host = strings.TrimPrefix(host, "www.")
	return host == "aiai.ac" || host == "llmtoken.shop" || strings.HasSuffix(host, ".llmtoken.shop")
}

func openAIAdaptedImageUploadToDataURL(upload OpenAIImagesUpload) (string, error) {
	// The legacy adapted source sniffed generic octet-stream uploads before
	// turning them into data URLs. Keep that behavior local to these restored
	// provider adapters instead of changing the official shared image helper.
	if strings.EqualFold(strings.TrimSpace(upload.ContentType), "application/octet-stream") {
		upload.ContentType = ""
	}
	return openAIImageUploadToDataURL(upload)
}

func adaptAIAIImagesEditToGeneration(account *Account, parsed *OpenAIImagesRequest) ([]byte, string, string, bool, bool, error) {
	if !isAIAIImageAccount(account) || parsed == nil || (!parsed.IsEdits() && len(parsed.InputImageURLs) == 0 && len(parsed.Uploads) == 0 && strings.TrimSpace(parsed.MaskImageURL) == "" && parsed.MaskUpload == nil) {
		return nil, "", "", false, false, nil
	}
	imageURLs := make([]string, 0, len(parsed.InputImageURLs)+len(parsed.Uploads))
	for _, imageURL := range parsed.InputImageURLs {
		if trimmed := strings.TrimSpace(imageURL); trimmed != "" {
			imageURLs = append(imageURLs, trimmed)
		}
	}
	for _, upload := range parsed.Uploads {
		dataURL, err := openAIAdaptedImageUploadToDataURL(upload)
		if err != nil {
			return nil, "", "", false, false, err
		}
		imageURLs = append(imageURLs, dataURL)
	}
	if len(imageURLs) == 0 {
		return nil, "", "", false, false, nil
	}
	maskURL := strings.TrimSpace(parsed.MaskImageURL)
	if parsed.MaskUpload != nil {
		dataURL, err := openAIAdaptedImageUploadToDataURL(*parsed.MaskUpload)
		if err != nil {
			return nil, "", "", false, false, err
		}
		maskURL = dataURL
	}
	payload := []byte(`{"model":"","prompt":"","image":[],"async":true}`)
	payload, _ = sjson.SetBytes(payload, "model", strings.TrimSpace(parsed.Model))
	payload, _ = sjson.SetBytes(payload, "prompt", strings.TrimSpace(parsed.Prompt))
	payload, _ = sjson.SetRawBytes(payload, "image", []byte(`[]`))
	for _, imageURL := range imageURLs {
		payload, _ = sjson.SetBytes(payload, "image.-1", imageURL)
	}
	if maskURL != "" {
		payload, _ = sjson.SetBytes(payload, "mask", maskURL)
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
	if outputFormat := strings.TrimSpace(parsed.OutputFormat); outputFormat != "" {
		payload, _ = sjson.SetBytes(payload, "output_format", outputFormat)
	}
	return payload, "application/json", openAIImagesGenerationsEndpoint, true, true, nil
}

func (s *OpenAIGatewayService) pollAIAIImagesAsyncResponse(ctx context.Context, requestCtx context.Context, c *gin.Context, account *Account, submitResp *http.Response, token string) (*http.Response, error) {
	if submitResp == nil {
		return nil, fmt.Errorf("aiai image async response missing")
	}
	body, err := io.ReadAll(submitResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read AIAI image async response: %w", err)
	}
	if submitResp.StatusCode >= 400 {
		return &http.Response{StatusCode: submitResp.StatusCode, Header: submitResp.Header.Clone(), Body: io.NopCloser(bytes.NewReader(body))}, nil
	}
	taskID := strings.TrimSpace(gjson.GetBytes(body, "task_id").String())
	if taskID == "" {
		return &http.Response{StatusCode: submitResp.StatusCode, Header: submitResp.Header.Clone(), Body: io.NopCloser(bytes.NewReader(body))}, nil
	}
	baseURL := strings.TrimRight(strings.TrimSpace(account.GetOpenAIBaseURL()), "/")
	if baseURL == "" {
		baseURL = "https://aiai.ac/api/v1"
	}
	pollURL := baseURL + "/images/" + url.PathEscape(taskID)
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	deadline := time.Now().Add(aiaiImagesPollTimeout)
	var lastBody []byte
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			wait := aiaiImagesPollInterval
			if remaining := time.Until(deadline); remaining < wait {
				wait = remaining
			}
			if wait <= 0 {
				break
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
		if err != nil {
			return nil, err
		}
		attemptCtx, cancelAttempt := s.withOpenAIImageUpstreamTimeout(req.Context())
		req = req.WithContext(WithHTTPUpstreamProfile(attemptCtx, HTTPUpstreamProfileOpenAI))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
		if err != nil {
			cancelAttempt()
			if isOpenAIImageAttemptTimeout(err, attemptCtx, requestCtx) && OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c) <= 0 {
				return nil, newOpenAIImageAttemptTimeoutFailover(nil)
			}
			return nil, fmt.Errorf("poll AIAI image task failed: %w", err)
		}
		lastBody, err = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancelAttempt()
		if err != nil {
			if isOpenAIImageAttemptTimeout(err, attemptCtx, requestCtx) && OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c) <= 0 {
				return nil, newOpenAIImageAttemptTimeoutFailover(resp)
			}
			return nil, fmt.Errorf("read AIAI image task response: %w", err)
		}
		status := strings.ToLower(strings.TrimSpace(gjson.GetBytes(lastBody, "task_status").String()))
		if status == "failed" || status == "error" {
			return nil, &UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: lastBody}
		}
		if resp.StatusCode >= 400 {
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: lastBody}
		}
		if status == "succeed" || status == "completed" {
			return &http.Response{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: io.NopCloser(bytes.NewReader(lastBody))}, nil
		}
		if time.Now().After(deadline) {
			break
		}
	}
	if len(lastBody) == 0 {
		lastBody = []byte(`{"error":{"message":"AIAI image task timed out","type":"server_error","code":"aiai_image_timeout"}}`)
	}
	return nil, &UpstreamFailoverError{StatusCode: http.StatusGatewayTimeout, ResponseBody: lastBody}
}
