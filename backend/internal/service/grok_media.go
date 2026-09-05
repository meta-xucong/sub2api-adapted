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
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type GrokMediaEndpoint string

const (
	GrokMediaEndpointImagesGenerations GrokMediaEndpoint = "images_generations"
	GrokMediaEndpointImagesEdits       GrokMediaEndpoint = "images_edits"
	GrokMediaEndpointVideosGenerations GrokMediaEndpoint = "videos_generations"
	GrokMediaEndpointVideosEdits       GrokMediaEndpoint = "videos_edits"
	GrokMediaEndpointVideosExtensions  GrokMediaEndpoint = "videos_extensions"
	GrokMediaEndpointVideoStatus       GrokMediaEndpoint = "video_status"
	GrokMediaEndpointVideoContent      GrokMediaEndpoint = "video_content"

	// Official xAI Imagine image-edit limit.
	grokMediaMaxEditSourceImages = 3

	// Wokey's working image-to-video contract is multipart image[]. Keep the
	// gateway-side fetch bounded because the source URL is supplied by a client.
	wokeyVideoReferenceImageMaxBytes = 8 << 20
	wokeyVideoReferenceImageMaxCount = 4

	// Wokey documents multimodal_reference for Grok Imagine Video 1.5 with up
	// to seven images. This is deliberately separate from the older I2V limit.
	wokeyVideoMultimodalReferenceMaxCount = 7

	// xAI documents a maximum of seven independent images for reference-to-video.
	// This validates the public request shape only; it does not certify an
	// account-specific upstream adapter.
	grokMediaMaxVideoReferenceImages = 7
)

type wokeyVideoReferenceImage struct {
	Data        []byte
	ContentType string
	FileName    string
}

// Kept injectable for unit tests; production always uses the SSRF-protected
// downloader below.
var wokeyVideoReferenceImageDownloader = downloadWokeyVideoReferenceImage

func (e GrokMediaEndpoint) RequiresRequestBody() bool {
	return !e.IsVideoLookupRequest()
}

func (e GrokMediaEndpoint) IsVideoLookupRequest() bool {
	return e == GrokMediaEndpointVideoStatus || e == GrokMediaEndpointVideoContent
}

func (e GrokMediaEndpoint) IsGenerationRequest() bool {
	switch e {
	case GrokMediaEndpointImagesGenerations, GrokMediaEndpointImagesEdits, GrokMediaEndpointVideosGenerations, GrokMediaEndpointVideosEdits, GrokMediaEndpointVideosExtensions:
		return true
	default:
		return false
	}
}

type GrokMediaRequestInfo struct {
	Model              string
	Prompt             string
	AspectRatio        string
	N                  int
	Size               string
	SizeTier           string
	Resolution         string
	DurationSeconds    int
	InputImageURLs     []string
	ReferenceImageURLs []string
	MaskImageURL       string
	Uploads            []OpenAIImagesUpload
	MaskUpload         *OpenAIImagesUpload
}

func (r GrokMediaRequestInfo) ModerationBody() []byte {
	payload := map[string]any{}
	if prompt := strings.TrimSpace(r.Prompt); prompt != "" {
		payload["prompt"] = prompt
	}

	images := make([]map[string]string, 0, len(r.InputImageURLs)+len(r.ReferenceImageURLs)+len(r.Uploads)+1)
	for _, imageURL := range r.InputImageURLs {
		if imageURL = strings.TrimSpace(imageURL); imageURL != "" {
			images = append(images, map[string]string{"image_url": imageURL})
		}
	}
	for _, imageURL := range r.ReferenceImageURLs {
		if imageURL = strings.TrimSpace(imageURL); imageURL != "" {
			images = append(images, map[string]string{"image_url": imageURL})
		}
	}
	for _, upload := range r.Uploads {
		if dataURL := upload.ModerationDataURL(); dataURL != "" {
			images = append(images, map[string]string{"image_url": dataURL})
		}
	}
	if maskURL := strings.TrimSpace(r.MaskImageURL); maskURL != "" {
		images = append(images, map[string]string{"image_url": maskURL})
	}
	if r.MaskUpload != nil {
		if dataURL := r.MaskUpload.ModerationDataURL(); dataURL != "" {
			images = append(images, map[string]string{"image_url": dataURL})
		}
	}
	if len(images) > 0 {
		payload["images"] = images
	}
	if len(payload) == 0 {
		return nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return body
}

func (e GrokMediaEndpoint) httpMethod() string {
	if e.IsVideoLookupRequest() {
		return http.MethodGet
	}
	return http.MethodPost
}

func ExtractGrokMediaModel(contentType string, body []byte) string {
	return ParseGrokMediaRequest(contentType, body).Model
}

func ParseGrokMediaRequest(contentType string, body []byte) GrokMediaRequestInfo {
	info := GrokMediaRequestInfo{N: 1}
	if gjson.ValidBytes(body) {
		parseGrokMediaJSONRequest(body, &info)
	} else {
		parseGrokMediaMultipartRequest(contentType, body, &info)
	}
	info.Model = strings.TrimSpace(info.Model)
	info.Prompt = strings.TrimSpace(info.Prompt)
	info.AspectRatio = strings.TrimSpace(info.AspectRatio)
	info.Size = strings.TrimSpace(info.Size)
	info.SizeTier = NormalizeImageBillingTierOrDefault(info.Size)
	info.Resolution = NormalizeVideoBillingResolutionOrDefault(info.Resolution)
	info.DurationSeconds = NormalizeVideoBillingDurationSecondsOrDefault(info.DurationSeconds)
	if info.N <= 0 {
		info.N = 1
	}
	return info
}

func parseGrokMediaJSONRequest(body []byte, info *GrokMediaRequestInfo) {
	if info == nil {
		return
	}
	info.Model = strings.TrimSpace(gjson.GetBytes(body, "model").String())
	info.Prompt = strings.TrimSpace(gjson.GetBytes(body, "prompt").String())
	info.AspectRatio = strings.TrimSpace(gjson.GetBytes(body, "aspect_ratio").String())
	info.Size = strings.TrimSpace(gjson.GetBytes(body, "size").String())
	info.Resolution = strings.TrimSpace(gjson.GetBytes(body, "resolution").String())
	if info.Resolution == "" {
		info.Resolution = strings.TrimSpace(gjson.GetBytes(body, "video_resolution").String())
	}
	if duration := gjson.GetBytes(body, "duration"); duration.Exists() && duration.Type == gjson.Number {
		info.DurationSeconds = int(duration.Int())
	}
	if info.DurationSeconds == 0 {
		if duration := gjson.GetBytes(body, "duration_seconds"); duration.Exists() && duration.Type == gjson.Number {
			info.DurationSeconds = int(duration.Int())
		}
	}
	if n := gjson.GetBytes(body, "n"); n.Exists() && n.Type == gjson.Number {
		info.N = int(n.Int())
	}
	appendJSONImageURLs := func(value gjson.Result) {
		if !value.Exists() {
			return
		}
		switch {
		case value.IsArray():
			for _, item := range value.Array() {
				if imageURL := extractGrokMediaImageURL(item); imageURL != "" {
					info.InputImageURLs = append(info.InputImageURLs, imageURL)
				}
			}
		default:
			if imageURL := extractGrokMediaImageURL(value); imageURL != "" {
				info.InputImageURLs = append(info.InputImageURLs, imageURL)
			}
		}
	}
	appendJSONImageURLs(gjson.GetBytes(body, "image"))
	appendJSONImageURLs(gjson.GetBytes(body, "images"))
	for _, item := range gjson.GetBytes(body, "reference_images").Array() {
		if imageURL := extractGrokMediaImageURL(item); imageURL != "" {
			info.ReferenceImageURLs = append(info.ReferenceImageURLs, imageURL)
		}
	}
	info.MaskImageURL = extractGrokMediaImageURL(gjson.GetBytes(body, "mask"))
}

// GrokVideoInputValidationError is a client-visible request error. It is kept
// distinct from upstream failures so a rejected R2V request never triggers a
// route failover or marks an account unhealthy.
type GrokVideoInputValidationError struct {
	Message string
}

func (e *GrokVideoInputValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// ValidateGrokVideoGenerationRequest validates the public R2V JSON structure
// before account selection. Account-specific URL scheme and SSRF rules belong
// to the selected adapter; existing multipart I2V requests do not carry the
// reference_images field and remain on their current path.
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

func extractGrokMediaImageURL(value gjson.Result) string {
	if !value.Exists() {
		return ""
	}
	if value.Type == gjson.String {
		return strings.TrimSpace(value.String())
	}
	if imageURL := strings.TrimSpace(value.Get("url").String()); imageURL != "" {
		return imageURL
	}
	if nested := value.Get("image_url"); nested.Exists() {
		if nested.Type == gjson.String {
			return strings.TrimSpace(nested.String())
		}
		if imageURL := strings.TrimSpace(nested.Get("url").String()); imageURL != "" {
			return imageURL
		}
	}
	return strings.TrimSpace(value.Get("image_url").String())
}

func grokMediaImageObject(imageURL string) map[string]string {
	return map[string]string{"url": imageURL, "type": "image_url"}
}

func parseGrokMediaMultipartRequest(contentType string, body []byte, info *GrokMediaRequestInfo) {
	if info == nil {
		return
	}
	mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return
		}
		if err != nil {
			return
		}
		name := strings.TrimSpace(part.FormName())
		if name == "" {
			_ = part.Close()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, openAIImageMaxUploadPartSize))
		_ = part.Close()
		if err != nil {
			return
		}
		fileName := strings.TrimSpace(part.FileName())
		partContentType := strings.TrimSpace(part.Header.Get("Content-Type"))
		if fileName != "" {
			upload := OpenAIImagesUpload{
				FieldName:   name,
				FileName:    fileName,
				ContentType: partContentType,
				Data:        data,
			}
			if name == "mask" {
				info.MaskUpload = &upload
				continue
			}
			if name == "image" || strings.HasPrefix(name, "image[") {
				info.Uploads = append(info.Uploads, upload)
			}
			continue
		}

		value := strings.TrimSpace(string(data))
		switch name {
		case "model":
			info.Model = value
		case "prompt":
			info.Prompt = value
		case "aspect_ratio":
			info.AspectRatio = value
		case "size":
			info.Size = value
		case "resolution":
			info.Resolution = value
		case "video_resolution":
			info.Resolution = value
		case "duration":
			if duration, err := strconv.Atoi(value); err == nil {
				info.DurationSeconds = duration
			}
		case "duration_seconds":
			if duration, err := strconv.Atoi(value); err == nil {
				info.DurationSeconds = duration
			}
		case "n":
			if n, err := strconv.Atoi(value); err == nil {
				info.N = n
			}
		case "image", "image_url":
			if value != "" {
				info.InputImageURLs = append(info.InputImageURLs, value)
			}
		case "mask", "mask_image_url":
			info.MaskImageURL = value
		}
	}
}

func GrokMediaVideoRequestSessionHash(requestID string, userID, apiKeyID int64) string {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || userID <= 0 || apiKeyID <= 0 {
		return ""
	}
	ownerSeed := fmt.Sprintf("%d:%d:%s", userID, apiKeyID, requestID)
	return "grok-video:" + DeriveSessionHashFromSeed(ownerSeed)
}

func (s *OpenAIGatewayService) BindGrokMediaVideoRequestAccount(
	ctx context.Context,
	groupID *int64,
	requestID string,
	userID, apiKeyID, accountID int64,
) error {
	if s == nil || s.cache == nil {
		return fmt.Errorf("grok video request binding cache is unavailable")
	}
	sessionHash := GrokMediaVideoRequestSessionHash(requestID, userID, apiKeyID)
	cacheKey := s.openAISessionCacheKey(sessionHash)
	if cacheKey == "" || accountID <= 0 {
		return fmt.Errorf("grok video request binding is invalid")
	}
	// Video jobs may complete well after WS sticky TTL (default 1h). Bind at least
	// as long as the pending-billing snapshot so late status/content polls resolve.
	ttl := grokVideoPendingBillingTTL(s.cfg)
	if s.cfg != nil && s.cfg.Gateway.OpenAIWS.StickySessionTTLSeconds > 0 {
		if sticky := time.Duration(s.cfg.Gateway.OpenAIWS.StickySessionTTLSeconds) * time.Second; sticky > ttl {
			ttl = sticky
		}
	}
	return s.cache.SetSessionAccountID(ctx, derefGroupID(groupID), cacheKey, accountID, ttl)
}

func (s *OpenAIGatewayService) ResolveGrokMediaVideoRequestAccount(
	ctx context.Context,
	groupID *int64,
	requestID string,
	userID, apiKeyID int64,
) (int64, error) {
	if s == nil || s.cache == nil {
		return 0, fmt.Errorf("grok video request binding cache is unavailable")
	}
	cacheKey := s.openAISessionCacheKey(GrokMediaVideoRequestSessionHash(requestID, userID, apiKeyID))
	if cacheKey == "" {
		return 0, fmt.Errorf("grok video request binding is invalid")
	}
	return s.cache.GetSessionAccountID(ctx, derefGroupID(groupID), cacheKey)
}

// GrokVideoPendingBilling is the create-time snapshot used when status polling
// first observes a completed video URL. Status may omit model/duration; we fall
// back to this snapshot, then defaults.
type GrokVideoPendingBilling struct {
	Model                string `json:"model"`
	BillingModel         string `json:"billing_model,omitempty"`
	UpstreamModel        string `json:"upstream_model,omitempty"`
	VideoResolution      string `json:"video_resolution,omitempty"`
	VideoDurationSeconds int    `json:"video_duration_seconds,omitempty"`
	OriginalModel        string `json:"original_model,omitempty"`
	// CreatedAt is when the gateway accepted the async create (RFC3339Nano UTC).
	// duration_ms for deferred billing is measured from this instant until the
	// first official done+video.url observation (status poll or content download),
	// not the latency of that single discovery request alone.
	CreatedAt string `json:"created_at,omitempty"`
}

// GrokVideoPendingCreatedAtNow formats a create-accept timestamp for pending billing.
func GrokVideoPendingCreatedAtNow() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// GrokVideoE2EDuration returns wall time from create accept to discovery of completion.
// Returns 0 when CreatedAt is missing or unparseable (caller keeps poll-only Duration).
func GrokVideoE2EDuration(createdAt string, discoveredAt time.Time) time.Duration {
	createdAt = strings.TrimSpace(createdAt)
	if createdAt == "" {
		return 0
	}
	if discoveredAt.IsZero() {
		discoveredAt = time.Now()
	}
	var created time.Time
	var err error
	if created, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		if created, err = time.Parse(time.RFC3339, createdAt); err != nil {
			return 0
		}
	}
	if created.IsZero() {
		return 0
	}
	d := discoveredAt.Sub(created)
	if d < 0 {
		return 0
	}
	return d
}

func grokVideoPendingBillingKey(requestID string, userID, apiKeyID int64) string {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || userID <= 0 || apiKeyID <= 0 {
		return ""
	}
	return fmt.Sprintf("%d:%d:%s", userID, apiKeyID, requestID)
}

func grokVideoPendingBillingTTL(cfg *config.Config) time.Duration {
	// Video generation can take several minutes; keep create-time pricing for a day.
	_ = cfg
	return 24 * time.Hour
}

func grokVideoBilledClaimTTL(cfg *config.Config) time.Duration {
	_ = cfg
	return 48 * time.Hour
}

// StoreGrokVideoPendingBilling persists create-time billing params for deferred status billing.
func (s *OpenAIGatewayService) StoreGrokVideoPendingBilling(
	ctx context.Context,
	requestID string,
	userID, apiKeyID int64,
	pending GrokVideoPendingBilling,
) error {
	if s == nil || s.cache == nil {
		return fmt.Errorf("grok video pending billing cache is unavailable")
	}
	key := grokVideoPendingBillingKey(requestID, userID, apiKeyID)
	if key == "" {
		return fmt.Errorf("grok video pending billing key is invalid")
	}
	pending.Model = strings.TrimSpace(pending.Model)
	pending.BillingModel = strings.TrimSpace(pending.BillingModel)
	pending.UpstreamModel = strings.TrimSpace(pending.UpstreamModel)
	pending.OriginalModel = strings.TrimSpace(pending.OriginalModel)
	if pending.VideoResolution != "" {
		pending.VideoResolution = NormalizeVideoBillingResolutionOrDefault(pending.VideoResolution)
	}
	if pending.VideoDurationSeconds > 0 {
		pending.VideoDurationSeconds = NormalizeVideoBillingDurationSecondsOrDefault(pending.VideoDurationSeconds)
	}
	// Always stamp create-accept time when missing so deferred duration_ms is E2E.
	if strings.TrimSpace(pending.CreatedAt) == "" {
		pending.CreatedAt = GrokVideoPendingCreatedAtNow()
	} else {
		pending.CreatedAt = strings.TrimSpace(pending.CreatedAt)
	}
	payload, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	return s.cache.SetGrokVideoPendingBilling(ctx, key, payload, grokVideoPendingBillingTTL(s.cfg))
}

// LoadGrokVideoPendingBilling returns the create-time snapshot (may be nil on miss).
func (s *OpenAIGatewayService) LoadGrokVideoPendingBilling(
	ctx context.Context,
	requestID string,
	userID, apiKeyID int64,
) (*GrokVideoPendingBilling, error) {
	if s == nil || s.cache == nil {
		return nil, fmt.Errorf("grok video pending billing cache is unavailable")
	}
	key := grokVideoPendingBillingKey(requestID, userID, apiKeyID)
	if key == "" {
		return nil, fmt.Errorf("grok video pending billing key is invalid")
	}
	payload, err := s.cache.GetGrokVideoPendingBilling(ctx, key)
	if err != nil || len(payload) == 0 {
		return nil, err
	}
	var pending GrokVideoPendingBilling
	if err := json.Unmarshal(payload, &pending); err != nil {
		return nil, err
	}
	return &pending, nil
}

// ClaimGrokVideoBilling returns true once for a completed video request so status
// polls do not double-bill. Fail-closed: claim errors are treated as already billed.
func (s *OpenAIGatewayService) ClaimGrokVideoBilling(
	ctx context.Context,
	requestID string,
	userID, apiKeyID int64,
) (bool, error) {
	if s == nil || s.cache == nil {
		return false, fmt.Errorf("grok video billing claim cache is unavailable")
	}
	key := grokVideoPendingBillingKey(requestID, userID, apiKeyID)
	if key == "" {
		return false, fmt.Errorf("grok video billing claim key is invalid")
	}
	return s.cache.ClaimGrokVideoBilled(ctx, key, grokVideoBilledClaimTTL(s.cfg))
}

// ReleaseGrokVideoBilling clears a claim after a failed durable RecordUsage so a
// later status/content poll can retry billing.
func (s *OpenAIGatewayService) ReleaseGrokVideoBilling(
	ctx context.Context,
	requestID string,
	userID, apiKeyID int64,
) error {
	if s == nil || s.cache == nil {
		return fmt.Errorf("grok video billing claim cache is unavailable")
	}
	key := grokVideoPendingBillingKey(requestID, userID, apiKeyID)
	if key == "" {
		return fmt.Errorf("grok video billing claim key is invalid")
	}
	return s.cache.ReleaseGrokVideoBilled(ctx, key)
}

// StableGrokVideoBillingRequestID is the durable usage_logs / dedup key for one
// async video task (not the per-poll gateway request id).
func StableGrokVideoBillingRequestID(taskRequestID string) string {
	taskRequestID = strings.TrimSpace(taskRequestID)
	if taskRequestID == "" {
		return ""
	}
	if strings.HasPrefix(taskRequestID, "grok-video:") {
		return taskRequestID
	}
	return "grok-video:" + taskRequestID
}

// Official xAI async video status success shape (docs.x.ai Video Generation):
//
//	{"status":"done","model":"grok-imagine-video-1.5","video":{"url":"...","duration":8,"respect_moderation":true}}
//
// Request may include resolution ("480p"|"720p"|"1080p"); completed status does not
// document a resolution field — bill resolution from the create-time request snapshot.

// IsGrokVideoStatusBillable matches official success: status == "done" AND non-empty video.url.
// pending / expired / failed, or done without a video URL, are not billable.
func IsGrokVideoStatusBillable(statusBody []byte) bool {
	if len(statusBody) == 0 || !gjson.ValidBytes(statusBody) {
		return false
	}
	if !isOfficialGrokVideoStatusDone(statusBody) {
		return false
	}
	return strings.TrimSpace(gjson.GetBytes(statusBody, "video.url").String()) != ""
}

func isOfficialGrokVideoStatusDone(statusBody []byte) bool {
	// Official enum: pending | done | expired | failed.
	return strings.EqualFold(strings.TrimSpace(gjson.GetBytes(statusBody, "status").String()), "done")
}

// ExtractGrokVideoBillingFromStatusBody builds usage units from an official done status.
// Field priority (official docs):
//   - duration: video.duration (seconds)
//   - model: top-level model
//   - resolution: not in status response → create-time pending snapshot → default 480p
func ExtractGrokVideoBillingFromStatusBody(statusBody []byte, pending *GrokVideoPendingBilling, requestID string) *OpenAIForwardResult {
	if !IsGrokVideoStatusBillable(statusBody) {
		return nil
	}
	model := ""
	billingModel := ""
	upstreamModel := ""
	resolution := ""
	durationSeconds := 0

	if gjson.ValidBytes(statusBody) {
		// Official: top-level model.
		model = strings.TrimSpace(gjson.GetBytes(statusBody, "model").String())
		// Official: video.duration (number of seconds).
		if v := gjson.GetBytes(statusBody, "video.duration"); v.Exists() && v.Type == gjson.Number {
			durationSeconds = int(v.Int())
			if durationSeconds == 0 && v.Float() > 0 {
				// Sub-second values are unexpected for this API; still accept truncated int path above.
				durationSeconds = int(v.Float())
			}
		}
	}
	if pending != nil {
		if model == "" {
			model = firstNonEmpty(pending.BillingModel, pending.Model, pending.OriginalModel)
		}
		if billingModel == "" {
			billingModel = firstNonEmpty(pending.BillingModel, pending.Model)
		}
		if upstreamModel == "" {
			upstreamModel = pending.UpstreamModel
		}
		// Official status has no resolution — always take create request when available.
		resolution = pending.VideoResolution
		if durationSeconds <= 0 {
			durationSeconds = pending.VideoDurationSeconds
		}
	}
	if model == "" {
		// Official default video model family when status omits model.
		model = "grok-imagine-video"
	}
	if billingModel == "" {
		billingModel = model
	}
	// Resolution is request-only per docs; empty → handler applies official default 480p.
	if resolution != "" {
		resolution = NormalizeVideoBillingResolutionOrDefault(resolution)
	}
	if durationSeconds > 0 {
		durationSeconds = NormalizeVideoBillingDurationSecondsOrDefault(durationSeconds)
	}
	responseID := extractGrokMediaVideoRequestID(statusBody)
	if responseID == "" {
		responseID = strings.TrimSpace(requestID)
	}
	return &OpenAIForwardResult{
		ResponseID:           responseID,
		Model:                model,
		BillingModel:         billingModel,
		UpstreamModel:        upstreamModel,
		VideoCount:           1,
		VideoResolution:      resolution,
		VideoDurationSeconds: durationSeconds,
	}
}

func (s *OpenAIGatewayService) ForwardGrokMedia(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	endpoint GrokMediaEndpoint,
	requestID string,
	body []byte,
	contentType string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()
	if account == nil {
		return nil, fmt.Errorf("grok account is required")
	}
	if account.Platform != PlatformGrok {
		return nil, fmt.Errorf("account platform %s is not supported for grok media", account.Platform)
	}

	token, _, err := s.getRequestCredential(ctx, c, account)
	if err != nil {
		return nil, err
	}
	if endpoint == GrokMediaEndpointVideoContent {
		return s.forwardGrokMediaVideoContent(ctx, c, account, token, requestID, startTime)
	}
	targetURL, err := buildGrokMediaURL(account, s.cfg, endpoint, requestID)
	if err != nil {
		return nil, err
	}

	body, contentType, err = prepareGrokMediaForwardBody(endpoint, body, contentType)
	if err != nil {
		return nil, err
	}
	if endpoint == GrokMediaEndpointVideosGenerations {
		if err := ValidateGrokVideoGenerationRequest(contentType, body); err != nil {
			return nil, err
		}
	}
	body, contentType, err = normalizeGrokMediaForwardBodyForAccount(account, endpoint, body, contentType)
	if err != nil {
		return nil, err
	}
	requestInfo := ParseGrokMediaRequest(contentType, body)
	upstreamModel := requestInfo.Model
	if endpoint.RequiresRequestBody() && gjson.ValidBytes(body) {
		if mappedModel := strings.TrimSpace(account.GetMappedModel(requestInfo.Model)); mappedModel != "" {
			upstreamModel = mappedModel
		}
		if upstreamModel != requestInfo.Model {
			body, err = sjson.SetBytes(body, "model", upstreamModel)
			if err != nil {
				return nil, fmt.Errorf("rewrite grok media account mapped model: %w", err)
			}
		}
	}
	if account.UsesKIEJobsVideoAPI() && endpoint == GrokMediaEndpointVideosGenerations {
		imageURLs := append([]string{}, requestInfo.InputImageURLs...)
		imageURLs = append(imageURLs, requestInfo.ReferenceImageURLs...)
		probeSummary, err := validateKIEJobsVideoImageURLsWithSummary(ctx, imageURLs)
		SetOpsKIEImageProbe(c, probeSummary)
		if err != nil {
			return nil, err
		}
		body, contentType, err = prepareKIEJobsVideoCreateBody(requestInfo, upstreamModel)
		if err != nil {
			return nil, err
		}
		body, err = s.materializeKIEJobsVideoImageURLs(ctx, account, token, body)
		if err != nil {
			return nil, err
		}
	}
	if isWokeyVideoGeneration(account, endpoint) {
		prepCtx, prepCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		body, contentType, err = prepareWokeyVideoImageMultipartBody(
			prepCtx,
			body,
			contentType,
			requestInfo,
		)
		prepCancel()
		if err != nil {
			return nil, fmt.Errorf("prepare Wokey image-to-video input: %w", err)
		}
	}
	body, contentType, err = sanitizeGrokMediaForwardBody(endpoint, body, contentType)
	if err != nil {
		return nil, err
	}

	var bodyReader io.Reader
	if endpoint.RequiresRequestBody() {
		bodyReader = bytes.NewReader(body)
	}
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()
	upstreamReq, err := http.NewRequestWithContext(upstreamCtx, endpoint.httpMethod(), targetURL, bodyReader)
	if err != nil {
		return nil, err
	}
	upstreamReq.Header.Set("Authorization", "Bearer "+token)
	upstreamReq.Header.Set("Accept", "application/json")
	if account.IsGrokOAuth() && isGrokCLIProxyTarget(targetURL) {
		applyGrokCLIHeaders(upstreamReq.Header)
	}
	if endpoint.RequiresRequestBody() {
		contentType = strings.TrimSpace(contentType)
		if contentType == "" {
			contentType = "application/json"
		}
		upstreamReq.Header.Set("Content-Type", contentType)
	}
	// 账号级请求头覆写最后应用，配置值优先于内置默认头。
	account.ApplyHeaderOverrides(upstreamReq.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstreamStart := time.Now()
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	defer func() { _ = resp.Body.Close() }()

	requestIDHeader := firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id"))
	requestModel := requestInfo.Model
	if resp.StatusCode >= 400 {
		return s.handleGrokMediaErrorResponse(ctx, resp, c, account, requestIDHeader, requestModel)
	}

	s.updateGrokUsageFromResponse(ctx, account, resp.Header, resp.StatusCode)
	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	if account.UsesKIEJobsVideoAPI() {
		rawResponseBody := respBody
		switch endpoint {
		case GrokMediaEndpointVideosGenerations:
			respBody, err = normalizeKIEJobsVideoCreateResponse(respBody, requestInfo.Model)
		case GrokMediaEndpointVideoStatus:
			respBody, err = normalizeKIEJobsVideoStatusResponse(respBody, requestID)
		}
		if err != nil {
			if account.UsesKIEJobsVideoAPI() {
				providerErrorCode := kieJobsErrorCode(rawResponseBody)
				providerErrorMessage := sanitizeUpstreamErrorMessage(kieJobsErrorMessage(rawResponseBody))
				if providerErrorMessage == "" {
					providerErrorMessage = "KIE task creation response was invalid"
				}
				var kieProbe *KIEImageProbeSummary
				if summary, ok := GetOpsKIEImageProbe(c); ok {
					kieProbe = &summary
				}
				kieErrorSummary := KIEJobsUpstreamErrorSummary(rawResponseBody)
				SetOpsUpstreamError(c, http.StatusBadGateway, providerErrorMessage, kieErrorSummary)
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
					Platform:           account.Platform,
					AccountID:          account.ID,
					AccountName:        account.Name,
					UpstreamStatusCode: http.StatusBadGateway,
					Kind:               "response_invalid",
					Message:            providerErrorMessage,
					Detail:             kieErrorSummary,
					ProviderErrorCode:  providerErrorCode,
					KIEImageProbe:      kieProbe,
				})
			}
			return nil, &UpstreamFailoverError{
				StatusCode:      http.StatusBadGateway,
				ResponseBody:    rawResponseBody,
				ResponseHeaders: resp.Header.Clone(),
			}
		}
	}
	if endpoint == GrokMediaEndpointImagesGenerations || endpoint == GrokMediaEndpointImagesEdits {
		if countOpenAIResponseImageOutputsFromJSONBytes(respBody) <= 0 {
			setOpsUpstreamError(c, http.StatusBadGateway, "xAI upstream returned no image output", truncateString(string(respBody), 512))
			return nil, &UpstreamFailoverError{
				StatusCode:      http.StatusBadGateway,
				ResponseBody:    respBody,
				ResponseHeaders: resp.Header.Clone(),
			}
		}
	}
	if endpoint == GrokMediaEndpointVideoStatus {
		if account.UsesKIEJobsVideoAPI() {
			respBody = rewriteKIEJobsVideoContentURL(respBody, grokMediaContentProxyURL(c, requestID))
		}
		respBody = rewriteGrokMediaVideoContentURLs(
			respBody,
			requestID,
			grokMediaContentProxyURL(c, requestID),
		)
	}
	writeGrokMediaResponse(c, resp, respBody, s.responseHeaderFilter)
	usage := grokMediaUsageFromResponse(endpoint, requestInfo, respBody)
	resultModel := requestModel
	resultBillingModel := requestModel
	if endpoint == GrokMediaEndpointVideoStatus {
		// Status has no request body model; use upstream status fields when billable.
		if m := strings.TrimSpace(usage.Model); m != "" {
			resultModel = m
		}
		if m := strings.TrimSpace(usage.BillingModel); m != "" {
			resultBillingModel = m
		}
	}
	return &OpenAIForwardResult{
		RequestID:            requestIDHeader,
		ResponseID:           usage.ResponseID,
		Usage:                usage.Usage,
		Model:                resultModel,
		BillingModel:         resultBillingModel,
		UpstreamModel:        upstreamModel,
		ResponseHeaders:      resp.Header.Clone(),
		Duration:             time.Since(startTime),
		ImageCount:           usage.ImageCount,
		ImageSize:            usage.ImageSize,
		ImageInputSize:       usage.ImageInputSize,
		ImageOutputSizes:     usage.ImageOutputSizes,
		VideoCount:           usage.VideoCount,
		VideoResolution:      usage.VideoResolution,
		VideoDurationSeconds: usage.VideoDurationSeconds,
	}, nil
}

func (s *OpenAIGatewayService) forwardGrokMediaVideoContent(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	token, requestID string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	statusURL, err := buildGrokMediaURL(account, s.cfg, GrokMediaEndpointVideoStatus, requestID)
	if err != nil {
		return nil, err
	}

	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()
	statusReq, err := http.NewRequestWithContext(
		WithHTTPUpstreamRedirectsDisabled(upstreamCtx),
		http.MethodGet,
		statusURL,
		nil,
	)
	if err != nil {
		return nil, err
	}
	statusReq.Header.Set("Authorization", "Bearer "+token)
	statusReq.Header.Set("Accept", "application/json")
	if account.IsGrokOAuth() && isGrokCLIProxyTarget(statusURL) {
		applyGrokCLIHeaders(statusReq.Header)
	}
	account.ApplyHeaderOverrides(statusReq.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstreamStart := time.Now()
	statusResp, err := s.httpUpstream.Do(statusReq, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	statusRequestID := firstNonEmpty(statusResp.Header.Get("x-request-id"), statusResp.Header.Get("xai-request-id"))
	if statusResp.StatusCode >= 300 {
		defer func() { _ = statusResp.Body.Close() }()
		SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
		if statusResp.StatusCode < 400 {
			return nil, fmt.Errorf("grok media status redirect is not allowed")
		}
		return s.handleGrokMediaErrorResponse(ctx, statusResp, c, account, statusRequestID, "")
	}
	statusBody, err := ReadUpstreamResponseBody(statusResp.Body, s.cfg, c, openAITooLargeError)
	_ = statusResp.Body.Close()
	if err != nil {
		SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
		return nil, err
	}

	if account.UsesKIEJobsVideoAPI() {
		statusBody, err = normalizeKIEJobsVideoStatusResponse(statusBody, requestID)
		if err != nil {
			SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
			return nil, err
		}
	}
	contentURL, err := grokMediaSignedVideoContentURLForAccount(account, statusBody, requestID)
	if err != nil {
		SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
		return nil, err
	}
	signedContent := contentURL != ""
	if !signedContent {
		contentURL, err = buildGrokMediaURL(account, s.cfg, GrokMediaEndpointVideoContent, requestID)
		if err != nil {
			SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
			return nil, err
		}
	}

	contentReq, err := http.NewRequestWithContext(
		WithHTTPUpstreamRedirectsDisabled(upstreamCtx),
		http.MethodGet,
		contentURL,
		nil,
	)
	if err != nil {
		SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
		return nil, err
	}
	contentReq.Header.Set("Accept", "*/*")
	if c != nil {
		if rangeHeader := strings.TrimSpace(c.GetHeader("Range")); rangeHeader != "" {
			contentReq.Header.Set("Range", rangeHeader)
		}
	}
	if !signedContent {
		contentReq.Header.Set("Authorization", "Bearer "+token)
		if account.IsGrokOAuth() && isGrokCLIProxyTarget(contentURL) {
			applyGrokCLIHeaders(contentReq.Header)
		}
		account.ApplyHeaderOverrides(contentReq.Header)
	}

	contentResp, err := s.httpUpstream.Do(contentReq, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	defer func() { _ = contentResp.Body.Close() }()
	contentRequestID := firstNonEmpty(contentResp.Header.Get("x-request-id"), contentResp.Header.Get("xai-request-id"), statusRequestID)
	if contentResp.StatusCode >= 300 && contentResp.StatusCode < 400 {
		return nil, fmt.Errorf("grok media signed content redirect is not allowed")
	}
	if contentResp.StatusCode >= 400 && contentResp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		return s.handleGrokMediaErrorResponse(ctx, contentResp, c, account, contentRequestID, "")
	}

	s.updateGrokUsageFromResponse(ctx, account, contentResp.Header, contentResp.StatusCode)
	if err := writeGrokMediaContentResponse(c, contentResp); err != nil {
		return nil, err
	}
	// Content download is an alternate completion observation: when status body is
	// official done+video.url, attach billable units so the handler can claim once
	// (same path as status polling). Pending snapshot is merged in the handler.
	result := &OpenAIForwardResult{
		RequestID:       contentRequestID,
		ResponseHeaders: contentResp.Header.Clone(),
		Duration:        time.Since(startTime),
	}
	if billed := ExtractGrokVideoBillingFromStatusBody(statusBody, nil, requestID); billed != nil {
		result.ResponseID = firstNonEmpty(billed.ResponseID, strings.TrimSpace(requestID))
		result.Model = billed.Model
		result.BillingModel = billed.BillingModel
		result.UpstreamModel = billed.UpstreamModel
		result.VideoCount = billed.VideoCount
		result.VideoResolution = billed.VideoResolution
		result.VideoDurationSeconds = billed.VideoDurationSeconds
	}
	return result, nil
}

func grokMediaSignedVideoContentURL(body []byte, requestID string) (string, error) {
	rawURL := strings.TrimSpace(gjson.GetBytes(body, "video.url").String())
	if rawURL == "" {
		return "", nil
	}
	// An upstream Sub2API rewrites protected content URLs to its own proxy
	// endpoint. Treat that as an authenticated relay path, not as a signed URL;
	// the caller will rebuild it against the configured account base URL and
	// attach the upstream API key.
	if isGrokMediaVideoContentURL(rawURL, requestID) {
		return "", nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") ||
		!strings.EqualFold(parsed.Hostname(), "vidgen.x.ai") ||
		(parsed.Port() != "" && parsed.Port() != "443") || parsed.User != nil {
		return "", fmt.Errorf("grok media status returned an unsupported video content URL")
	}
	return parsed.String(), nil
}

func grokMediaSignedVideoContentURLForAccount(account *Account, body []byte, requestID string) (string, error) {
	if account != nil && account.UsesKIEJobsVideoAPI() {
		return kieJobsSignedVideoContentURL(body)
	}
	return grokMediaSignedVideoContentURL(body, requestID)
}

func isGrokCLIProxyTarget(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	return err == nil && strings.EqualFold(parsed.Hostname(), "cli-chat-proxy.grok.com")
}

func prepareGrokMediaForwardBody(endpoint GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	if endpoint != GrokMediaEndpointImagesEdits {
		return body, contentType, nil
	}
	if gjson.ValidBytes(body) {
		out, err := normalizeGrokMediaJSONImageRefs(body)
		return out, contentType, err
	}
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return body, contentType, nil
	}

	info := ParseGrokMediaRequest(contentType, body)
	payload := make(map[string]any)
	if info.Model != "" {
		payload["model"] = info.Model
	}
	if info.Prompt != "" {
		payload["prompt"] = info.Prompt
	}
	if info.N > 1 {
		payload["n"] = info.N
	}
	if info.Size != "" {
		payload["size"] = info.Size
	}

	images := make([]map[string]string, 0, len(info.InputImageURLs)+len(info.Uploads))
	for _, imageURL := range info.InputImageURLs {
		if imageURL = strings.TrimSpace(imageURL); imageURL != "" {
			images = append(images, grokMediaImageObject(imageURL))
		}
	}
	for _, upload := range info.Uploads {
		dataURL, err := openAIImageUploadToDataURL(upload)
		if err != nil {
			return nil, "", err
		}
		images = append(images, grokMediaImageObject(dataURL))
	}
	if len(images) > grokMediaMaxEditSourceImages {
		return nil, "", fmt.Errorf("a maximum of %d source images is supported for image edits", grokMediaMaxEditSourceImages)
	}
	if len(images) > 0 {
		payload["image"] = images[0]
		if len(images) > 1 {
			payload["images"] = images
		}
	}

	maskImageURL := strings.TrimSpace(info.MaskImageURL)
	if info.MaskUpload != nil {
		dataURL, err := openAIImageUploadToDataURL(*info.MaskUpload)
		if err != nil {
			return nil, "", err
		}
		maskImageURL = dataURL
	}
	if maskImageURL != "" {
		payload["mask"] = grokMediaImageObject(maskImageURL)
	}

	out, err := marshalOpenAIUpstreamJSON(payload)
	if err != nil {
		return nil, "", err
	}
	return out, "application/json", nil
}

func normalizeGrokMediaJSONImageRefs(body []byte) ([]byte, error) {
	info := ParseGrokMediaRequest("application/json", body)
	if len(info.InputImageURLs) > grokMediaMaxEditSourceImages {
		return nil, fmt.Errorf("a maximum of %d source images is supported for image edits", grokMediaMaxEditSourceImages)
	}
	out := body
	var err error
	for _, field := range []string{"image", "images", "mask"} {
		out, err = rewriteGrokMediaJSONImageField(out, field)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func rewriteGrokMediaJSONImageField(body []byte, path string) ([]byte, error) {
	value := gjson.GetBytes(body, path)
	if !value.Exists() {
		return body, nil
	}
	if value.IsArray() {
		rewritten := make([]map[string]string, 0, len(value.Array()))
		for _, item := range value.Array() {
			imageURL := extractGrokMediaImageURL(item)
			if imageURL == "" {
				return body, nil
			}
			rewritten = append(rewritten, grokMediaImageObject(imageURL))
		}
		out, err := sjson.SetBytes(body, path, rewritten)
		if err != nil {
			return nil, fmt.Errorf("rewrite grok media %s: %w", path, err)
		}
		return out, nil
	}
	imageURL := extractGrokMediaImageURL(value)
	if imageURL == "" {
		return body, nil
	}
	out, err := sjson.SetBytes(body, path, grokMediaImageObject(imageURL))
	if err != nil {
		return nil, fmt.Errorf("rewrite grok media %s: %w", path, err)
	}
	return out, nil
}

func normalizeGrokMediaForwardBody(endpoint GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	if !endpoint.RequiresRequestBody() || !gjson.ValidBytes(body) {
		return body, contentType, nil
	}
	var imageFields []string
	switch endpoint {
	case GrokMediaEndpointImagesEdits:
		imageFields = []string{"image", "images", "mask"}
	case GrokMediaEndpointVideosGenerations:
		imageFields = []string{"image", "images", "reference_images"}
	}
	var err error
	body, err = canonicalizeGrokMediaImageURLFields(body, imageFields...)
	if err != nil {
		return nil, "", err
	}
	info := ParseGrokMediaRequest(contentType, body)
	upstreamModel := NormalizeGrokMediaModelForEndpoint(endpoint, info.Model, info.HasInputImage())
	if upstreamModel == "" || upstreamModel == info.Model {
		return body, contentType, nil
	}
	out, err := sjson.SetBytes(body, "model", upstreamModel)
	if err != nil {
		return nil, "", fmt.Errorf("rewrite grok media model: %w", err)
	}
	return out, contentType, nil
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

// Wokey accepts the standard Grok request shape after this narrow field translation.
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

// prepareWokeyVideoImageMultipartBody bridges public JSON image URLs to Wokey's
// multipart image[] file parts. R2V references remain separate from I2V until
// this point, then each reference becomes one ordered multipart file part.
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

func canonicalizeGrokMediaImageURLFields(body []byte, fields ...string) ([]byte, error) {
	out := body
	for _, field := range fields {
		value := gjson.GetBytes(out, field)
		if !value.Exists() {
			continue
		}
		if value.IsArray() {
			for index := range value.Array() {
				var err error
				out, err = canonicalizeGrokMediaImageURLObject(out, fmt.Sprintf("%s.%d", field, index))
				if err != nil {
					return nil, err
				}
			}
			continue
		}
		var err error
		out, err = canonicalizeGrokMediaImageURLObject(out, field)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func canonicalizeGrokMediaImageURLObject(body []byte, path string) ([]byte, error) {
	legacyPath := path + ".image_url"
	legacy := gjson.GetBytes(body, legacyPath)
	if !legacy.Exists() {
		return body, nil
	}

	out := body
	if strings.TrimSpace(gjson.GetBytes(out, path+".url").String()) == "" {
		var err error
		out, err = sjson.SetBytes(out, path+".url", legacy.Value())
		if err != nil {
			return nil, fmt.Errorf("normalize grok media image url: %w", err)
		}
	}
	out, err := sjson.DeleteBytes(out, legacyPath)
	if err != nil {
		return nil, fmt.Errorf("remove legacy grok media image url: %w", err)
	}
	return out, nil
}

func sanitizeGrokMediaForwardBody(endpoint GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	if !endpoint.RequiresRequestBody() || !gjson.ValidBytes(body) {
		return body, contentType, nil
	}
	switch endpoint {
	case GrokMediaEndpointImagesGenerations, GrokMediaEndpointImagesEdits:
		if !gjson.GetBytes(body, "size").Exists() {
			return body, contentType, nil
		}
		out, err := sjson.DeleteBytes(body, "size")
		if err != nil {
			return nil, "", fmt.Errorf("sanitize grok media size: %w", err)
		}
		return out, contentType, nil
	default:
		return body, contentType, nil
	}
}

func (r GrokMediaRequestInfo) HasInputImage() bool {
	return len(r.InputImageURLs) > 0 || len(r.Uploads) > 0
}

func (r GrokMediaRequestInfo) HasReferenceImages() bool {
	return len(r.ReferenceImageURLs) > 0
}

// NormalizeGrokMediaModelForEndpoint resolves the built-in upstream model alias
// for a media endpoint before account-level model mapping and scheduling.
func NormalizeGrokMediaModelForEndpoint(endpoint GrokMediaEndpoint, model string, hasInputImage bool) string {
	model = strings.TrimSpace(model)
	switch endpoint {
	case GrokMediaEndpointImagesGenerations, GrokMediaEndpointImagesEdits:
		if model == "grok-imagine" {
			return "grok-imagine-image-quality"
		}
	case GrokMediaEndpointVideosGenerations:
		// xAI's 1.5 model is image-to-video only. Keep the requested model
		// unchanged when the image is missing so the upstream returns its
		// documented invalid-argument response instead of silently switching
		// models and pricing.
		_ = hasInputImage
	}
	return model
}

type grokMediaUsageMetadata struct {
	ResponseID           string
	Usage                OpenAIUsage
	Model                string
	BillingModel         string
	ImageCount           int
	ImageSize            string
	ImageInputSize       string
	ImageOutputSizes     []string
	VideoCount           int
	VideoResolution      string
	VideoDurationSeconds int
}

func grokMediaUsageFromResponse(endpoint GrokMediaEndpoint, requestInfo GrokMediaRequestInfo, responseBody []byte) grokMediaUsageMetadata {
	usage, _ := extractOpenAIUsageFromJSONBytes(responseBody)
	meta := grokMediaUsageMetadata{Usage: usage}
	switch endpoint {
	case GrokMediaEndpointImagesGenerations, GrokMediaEndpointImagesEdits:
		meta.ImageCount = countOpenAIResponseImageOutputsFromJSONBytes(responseBody)
		meta.ImageSize = requestInfo.SizeTier
		meta.ImageInputSize = requestInfo.Size
		meta.ImageOutputSizes = collectOpenAIResponseImageOutputSizesFromJSONBytes(responseBody)
	case GrokMediaEndpointVideosGenerations, GrokMediaEndpointVideosEdits, GrokMediaEndpointVideosExtensions:
		// Async video: capture request_id + create-time pricing params only.
		// Billable VideoCount is set later when status polling observes video.url.
		meta.ResponseID = extractGrokMediaVideoRequestID(responseBody)
		meta.VideoResolution = requestInfo.Resolution
		meta.VideoDurationSeconds = requestInfo.DurationSeconds
	case GrokMediaEndpointVideoStatus:
		// Prefer status-body URL success + upstream duration/resolution when present.
		if IsGrokVideoStatusBillable(responseBody) {
			// provisional units; handler merges with pending snapshot before RecordUsage.
			if billed := ExtractGrokVideoBillingFromStatusBody(responseBody, nil, ""); billed != nil {
				meta.ResponseID = billed.ResponseID
				meta.Model = billed.Model
				meta.BillingModel = billed.BillingModel
				meta.VideoCount = billed.VideoCount
				meta.VideoResolution = billed.VideoResolution
				meta.VideoDurationSeconds = billed.VideoDurationSeconds
			}
		}
	}
	return meta
}

func extractGrokMediaVideoRequestID(body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	for _, path := range []string{"request_id", "id", "data.request_id", "data.id", "video.request_id", "video.id", "task_id", "data.task_id", "video.task_id"} {
		if id := strings.TrimSpace(gjson.GetBytes(body, path).String()); id != "" {
			return id
		}
	}
	return ""
}

func (s *OpenAIGatewayService) handleGrokMediaErrorResponse(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
	requestIDHeader string,
	requestedModel string,
) (*OpenAIForwardResult, error) {
	body := s.readUpstreamErrorBody(resp)
	// Reconcile readiness before configurable passthrough branches can return;
	// otherwise a Grok 429 can remain schedulable.
	s.handleGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body)
	upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	providerErrorCode := ""
	var kieProbe *KIEImageProbeSummary
	if account != nil && account.UsesKIEJobsVideoAPI() {
		if kieMessage := strings.TrimSpace(kieJobsErrorMessage(body)); kieMessage != "" {
			upstreamMsg = sanitizeUpstreamErrorMessage(kieMessage)
		}
		providerErrorCode = kieJobsErrorCode(body)
		if summary, ok := GetOpsKIEImageProbe(c); ok {
			kieProbe = &summary
		}
	}
	if upstreamMsg == "" {
		upstreamMsg = fmt.Sprintf("xAI upstream returned status %d", resp.StatusCode)
	}

	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = truncateString(string(body), maxBytes)
	}
	if account != nil && account.UsesKIEJobsVideoAPI() {
		upstreamDetail = KIEJobsUpstreamErrorSummary(body)
	}
	setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	if isGrokContentPolicyRejection(resp.StatusCode, body) {
		clientMsg := grokContentPolicyClientMessage(body)
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  requestIDHeader,
			Kind:               "http_error",
			Message:            clientMsg,
			Detail:             upstreamDetail,
			ProviderErrorCode:  providerErrorCode,
			KIEImageProbe:      kieProbe,
		})
		MarkResponseCommitted(c)
		writeGrokMediaErrorResponse(c, http.StatusForbidden, "invalid_request_error", clientMsg)
		return nil, fmt.Errorf("grok content policy rejection: %s", clientMsg)
	}

	if status, errType, errMsg, matched := applyErrorPassthroughRule(
		c,
		account.Platform,
		resp.StatusCode,
		body,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	); matched {
		MarkResponseCommitted(c)
		writeGrokMediaErrorResponse(c, status, errType, errMsg)
		return nil, fmt.Errorf("upstream error: %d (passthrough rule matched) message=%s", resp.StatusCode, upstreamMsg)
	}

	if !account.ShouldHandleErrorCode(resp.StatusCode) {
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  requestIDHeader,
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
			ProviderErrorCode:  providerErrorCode,
			KIEImageProbe:      kieProbe,
		})
		MarkResponseCommitted(c)
		writeGrokMediaErrorResponse(c, http.StatusInternalServerError, "upstream_error", "Upstream gateway error")
		return nil, fmt.Errorf("upstream error: %d (not in custom error codes) message=%s", resp.StatusCode, upstreamMsg)
	}

	kind := "http_error"
	if s.shouldFailoverGrokUpstreamError(resp.StatusCode, body) {
		kind = "failover"
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  requestIDHeader,
		Kind:               kind,
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
		ProviderErrorCode:  providerErrorCode,
		KIEImageProbe:      kieProbe,
	})
	if kind == "failover" {
		return nil, &UpstreamFailoverError{
			StatusCode:             resp.StatusCode,
			ResponseBody:           body,
			ResponseHeaders:        resp.Header.Clone(),
			RetryableOnSameAccount: account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
		}
	}

	MarkResponseCommitted(c)
	writeGrokMediaErrorResponse(c, resp.StatusCode, grokMediaErrorType(resp.StatusCode), upstreamMsg)
	return nil, fmt.Errorf("upstream error: %d %s", resp.StatusCode, upstreamMsg)
}

func grokMediaErrorType(statusCode int) string {
	switch statusCode {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	default:
		return "upstream_error"
	}
}

func writeGrokMediaErrorResponse(c *gin.Context, statusCode int, errType, message string) {
	if c == nil || c.Writer == nil || c.Writer.Written() {
		return
	}
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"type":    strings.TrimSpace(errType),
			"message": strings.TrimSpace(message),
		},
	})
}

func writeGrokMediaResponse(c *gin.Context, resp *http.Response, body []byte, filter *responseheaders.CompiledHeaderFilter) {
	if c == nil || resp == nil {
		return
	}
	writeOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, filter)
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(resp.StatusCode, contentType, body)
}

func writeGrokMediaContentResponse(c *gin.Context, resp *http.Response) error {
	if c == nil || resp == nil || resp.Body == nil {
		return fmt.Errorf("grok media content response is incomplete")
	}

	for _, name := range []string{
		"Content-Type",
		"Content-Length",
		"Content-Range",
		"Accept-Ranges",
		"Content-Disposition",
	} {
		if value := strings.TrimSpace(resp.Header.Get(name)); value != "" {
			c.Header(name, value)
		}
	}
	if strings.TrimSpace(c.Writer.Header().Get("Content-Length")) == "" && resp.ContentLength >= 0 {
		c.Header("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	if strings.TrimSpace(c.Writer.Header().Get("Content-Type")) == "" {
		c.Header("Content-Type", "application/octet-stream")
	}
	c.Status(resp.StatusCode)
	MarkResponseCommitted(c)
	_, err := io.Copy(c.Writer, resp.Body)
	return err
}

func rewriteGrokMediaVideoContentURLs(body []byte, requestID, proxyURL string) []byte {
	if len(body) == 0 || strings.TrimSpace(requestID) == "" || strings.TrimSpace(proxyURL) == "" || !gjson.ValidBytes(body) {
		return body
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return body
	}
	changed := rewriteGrokMediaKnownVideoURL(&value, proxyURL)
	if rewriteGrokMediaVideoContentURLValue(&value, requestID, proxyURL) {
		changed = true
	}
	if !changed {
		return body
	}
	rewritten, err := json.Marshal(value)
	if err != nil {
		return body
	}
	return rewritten
}

func rewriteGrokMediaKnownVideoURL(value *any, proxyURL string) bool {
	if value == nil {
		return false
	}
	root, ok := (*value).(map[string]any)
	if !ok {
		return false
	}
	video, ok := root["video"].(map[string]any)
	if !ok {
		return false
	}
	rawURL, ok := video["url"].(string)
	if !ok || strings.TrimSpace(rawURL) == "" {
		return false
	}
	video["url"] = proxyURL
	return true
}

func rewriteGrokMediaVideoContentURLValue(value *any, requestID, proxyURL string) bool {
	if value == nil {
		return false
	}
	switch typed := (*value).(type) {
	case map[string]any:
		changed := false
		for key, child := range typed {
			childValue := child
			if rewriteGrokMediaVideoContentURLValue(&childValue, requestID, proxyURL) {
				typed[key] = childValue
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for index, child := range typed {
			childValue := child
			if rewriteGrokMediaVideoContentURLValue(&childValue, requestID, proxyURL) {
				typed[index] = childValue
				changed = true
			}
		}
		return changed
	case string:
		if isGrokMediaVideoContentURL(typed, requestID) {
			*value = proxyURL
			return true
		}
	}
	return false
}

func isGrokMediaVideoContentURL(rawURL, requestID string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Path == "" {
		return false
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(segments) < 3 {
		return false
	}
	requestID = strings.Trim(requestID, "/")
	decodedID, err := url.PathUnescape(segments[len(segments)-2])
	if err != nil {
		return false
	}
	return segments[len(segments)-3] == "videos" &&
		decodedID == requestID &&
		segments[len(segments)-1] == "content"
}

func grokMediaContentProxyURL(c *gin.Context, requestID string) string {
	if c == nil || c.Request == nil || c.Request.URL == nil || strings.TrimSpace(requestID) == "" {
		return ""
	}
	pathPrefix := ""
	if strings.HasPrefix(c.Request.URL.Path, "/v1/") {
		pathPrefix = "/v1"
	}
	return pathPrefix + "/videos/" + url.PathEscape(strings.Trim(requestID, "/")) + "/content"
}
