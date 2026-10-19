package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	sharedhttpclient "github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	_ "golang.org/x/image/webp"
	"golang.org/x/net/http/httpproxy"
)

const (
	defaultImageMaxDownloadBytes int64 = 32 << 20 // 32 MiB
	imageStorageMaxDimension           = 16384
	// Gemini 4K non-square outputs exceed 16 Mi pixels (up to 18,874,368).
	// Keep a bounded envelope covering those documented outputs.
	imageStorageMaxPixels int64 = 24 * 1024 * 1024
)

// ImageStorage 把图片字节写入对象存储并返回可访问 URL。
//
// 这是对象存储的可插拔抽象：适配一个新的对象存储厂商，只需实现本接口
// （例如包一个厂商 SDK），无需改动任务/网关逻辑。仓库内自带一个 S3 兼容实现
// （repository.S3ImageStorage），适用于 AWS S3 / Cloudflare R2 / 阿里云 OSS / MinIO 等。
type ImageStorage interface {
	// Save 把 data 以 key 存入对象存储，返回可下载的 URL（公开直链或 presigned 临时链接）。
	// contentType 为图片 MIME 类型，如 "image/png"。
	Save(ctx context.Context, key, contentType string, data []byte) (url string, err error)
}

// ImageResultUploader 是 ImageStorage 的上层编排器（与具体厂商无关）：
// 把上游生图响应里的每张图片（b64_json 解码 / url 下载）转存到对象存储，
// 并把响应结果改写为只含短链接的紧凑 JSON，从而避免大 base64 落 Redis。
type ImageResultUploader struct {
	storage          ImageStorage
	httpClient       *http.Client
	prefix           string
	maxDownloadBytes int64
}

// NewImageResultUploader 构造一个 uploader；storage 为 nil 时 Rewrite 直接透传。
// A nil HTTP client selects the public-IP-pinned production downloader. A supplied
// client is a trusted dependency override (for example, a local test server);
// its caller owns destination and redirect policy. Image bytes are always bounded
// and validated. Production settings intentionally supply nil.
func NewImageResultUploader(storage ImageStorage, prefix string, maxDownloadBytes int64, httpClient *http.Client) *ImageResultUploader {
	if httpClient == nil {
		httpClient = defaultImageDownloadHTTPClient()
	}
	if maxDownloadBytes <= 0 {
		maxDownloadBytes = defaultImageMaxDownloadBytes
	}
	return &ImageResultUploader{
		storage:          storage,
		httpClient:       httpClient,
		prefix:           prefix,
		maxDownloadBytes: maxDownloadBytes,
	}
}

func defaultImageDownloadHTTPClient() *http.Client {
	client, err := sharedhttpclient.GetClient(sharedhttpclient.Options{
		Timeout:                  60 * time.Second,
		ResponseHeaderTimeout:    10 * time.Second,
		RequirePinnedDestination: true,
	})
	guard := &imageDownloadTransport{initErr: err}
	if err == nil {
		guard.base = client.Transport
	}
	return &http.Client{Timeout: 60 * time.Second, Transport: guard}
}

// Validate every request, including redirects, without rewriting the signed URL.
// The underlying client pins validated DNS answers to the actual socket dial.
type imageDownloadTransport struct {
	base    http.RoundTripper
	initErr error
}

func (t *imageDownloadTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.initErr != nil {
		return nil, fmt.Errorf("initialize secure image downloader: %w", t.initErr)
	}
	if req == nil || req.URL == nil || req.URL.User != nil {
		return nil, errors.New("image download URL must not contain credentials")
	}
	if _, err := urlvalidator.ValidateHTTPURL(req.URL.String(), true, urlvalidator.ValidationOptions{}); err != nil {
		return nil, fmt.Errorf("image download URL rejected: %w", err)
	}
	// The old default client honored environment proxies. Do not silently bypass
	// them now: strict destination pinning cannot constrain a proxy's resolution.
	// Re-evaluate NO_PROXY for each redirect target and never expose proxy secrets.
	proxyURL, err := httpproxy.FromEnvironment().ProxyFunc()(req.URL)
	if err != nil {
		return nil, errors.New("image download environment proxy configuration is invalid")
	}
	if proxyURL != nil {
		return nil, errors.New("secure image downloader cannot enforce destination IP validation through an environment proxy")
	}
	if t.base == nil {
		return nil, errors.New("secure image downloader is not configured")
	}
	return t.base.RoundTrip(req)
}

// Rewrite 将 result（上游生图响应 JSON）里的每张图片转存到对象存储，
// 返回改写后的紧凑结果（data[i].url 指向对象存储，b64_json 被移除）。
// 任一图片转存失败即返回 error（调用方据此将任务标记为失败，绝不把大 blob 落 Redis）。
func (u *ImageResultUploader) Rewrite(ctx context.Context, taskID string, result json.RawMessage) (json.RawMessage, error) {
	if u == nil || u.storage == nil {
		return result, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(result, &top); err != nil {
		return nil, fmt.Errorf("parse image response: %w", err)
	}
	rawData, ok := top["data"]
	if !ok {
		// 没有 data 数组（结构不符合预期），保持原样返回，交由上层决定。
		return result, nil
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(rawData, &items); err != nil {
		return nil, fmt.Errorf("parse image response data: %w", err)
	}
	if len(items) == 0 {
		return result, nil
	}
	for i, item := range items {
		data, contentType, err := u.fetchImageBytes(ctx, item)
		if err != nil {
			return nil, fmt.Errorf("image %d: %w", i, err)
		}
		key := u.buildKey(taskID, i, contentType)
		url, err := u.storage.Save(ctx, key, contentType, data)
		if err != nil {
			return nil, fmt.Errorf("image %d: upload to object storage: %w", i, err)
		}
		urlRaw, err := json.Marshal(url)
		if err != nil {
			return nil, fmt.Errorf("image %d: encode url: %w", i, err)
		}
		item["url"] = urlRaw
		delete(item, "b64_json")
		items[i] = item
	}
	newData, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("encode image response data: %w", err)
	}
	top["data"] = newData
	out, err := json.Marshal(top)
	if err != nil {
		return nil, fmt.Errorf("encode image response: %w", err)
	}
	return out, nil
}

func (u *ImageResultUploader) fetchImageBytes(ctx context.Context, item map[string]json.RawMessage) ([]byte, string, error) {
	if raw, ok := item["b64_json"]; ok {
		var b64 string
		if err := json.Unmarshal(raw, &b64); err == nil {
			if b64 = strings.TrimSpace(b64); b64 != "" {
				data, err := u.decodeBoundedImageBase64(b64, "b64_json")
				if err != nil {
					return nil, "", err
				}
				contentType, err := validatedStorageImageContentType(data)
				return data, contentType, err
			}
		}
	}
	if raw, ok := item["url"]; ok {
		var rawURL string
		if err := json.Unmarshal(raw, &rawURL); err == nil {
			if rawURL = strings.TrimSpace(rawURL); rawURL != "" {
				if len(rawURL) >= len("data:") && strings.EqualFold(rawURL[:len("data:")], "data:") {
					return u.decodeImageDataURL(rawURL)
				}
				return u.download(ctx, rawURL)
			}
		}
	}
	return nil, "", errors.New("image item has neither b64_json nor url")
}

func (u *ImageResultUploader) decodeImageDataURL(rawURL string) ([]byte, string, error) {
	header, payload, ok := strings.Cut(rawURL[len("data:"):], ",")
	if !ok {
		return nil, "", errors.New("decode image data URL: missing comma separator")
	}

	parts := strings.Split(header, ";")
	if strings.TrimSpace(parts[0]) == "" {
		return nil, "", errors.New("decode image data URL: missing media type")
	}
	base64Index := len(parts) - 1
	if base64Index < 1 || !strings.EqualFold(strings.TrimSpace(parts[base64Index]), "base64") {
		for i := 1; i < base64Index; i++ {
			if strings.EqualFold(strings.TrimSpace(parts[i]), "base64") {
				return nil, "", errors.New("decode image data URL: base64 marker must be the final header token")
			}
		}
		return nil, "", errors.New("decode image data URL: payload is not base64 encoded")
	}
	for i := 1; i < base64Index; i++ {
		if strings.EqualFold(strings.TrimSpace(parts[i]), "base64") {
			return nil, "", errors.New("decode image data URL: duplicate base64 marker")
		}
	}
	mediaTypeHeader := strings.Join(parts[:base64Index], ";")
	declaredType, _, err := mime.ParseMediaType(mediaTypeHeader)
	if err != nil {
		return nil, "", fmt.Errorf("decode image data URL: invalid media type: %w", err)
	}
	declaredType = strings.ToLower(declaredType)
	if !strings.HasPrefix(declaredType, "image/") {
		return nil, "", fmt.Errorf("decode image data URL: media type %q is not an image", declaredType)
	}

	data, err := u.decodeBoundedImageBase64(payload, "image data URL")
	if err != nil {
		return nil, "", err
	}
	contentType, err := validatedStorageImageContentType(data)
	return data, contentType, err
}

func (u *ImageResultUploader) imageByteLimit() int64 {
	if u.maxDownloadBytes > 0 {
		return u.maxDownloadBytes
	}
	return defaultImageMaxDownloadBytes
}

func (u *ImageResultUploader) decodeBoundedImageBase64(payload, source string) ([]byte, error) {
	limit := u.imageByteLimit()
	decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(payload))
	data, err := io.ReadAll(io.LimitReader(decoder, limit+1))
	if err != nil {
		return nil, fmt.Errorf("decode %s base64 payload: %w", source, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("decoded %s exceeds %d bytes", source, limit)
	}
	return data, nil
}

func (u *ImageResultUploader) download(ctx context.Context, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build download request: %w", err)
	}
	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download image: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, "", fmt.Errorf("download image: unexpected status %d", resp.StatusCode)
	}
	limit := u.imageByteLimit()
	if resp.ContentLength > limit {
		return nil, "", fmt.Errorf("downloaded image exceeds %d bytes", limit)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", fmt.Errorf("read image body: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, "", fmt.Errorf("downloaded image exceeds %d bytes", limit)
	}
	// Never trust a remote Content-Type header to turn arbitrary bytes into an image.
	contentType, err := validatedStorageImageContentType(data)
	return data, contentType, err
}

func (u *ImageResultUploader) buildKey(taskID string, index int, contentType string) string {
	return u.prefix + taskID + "-" + strconv.Itoa(index) + extensionForContentType(contentType)
}

// DecodeConfig validates the supported image header and geometry without
// allocating width*height pixels. This uploader stores opaque original bytes;
// it intentionally does not fully decode every potentially large image.
func validatedStorageImageContentType(data []byte) (string, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", errors.New("image content is not a supported image")
	}
	var contentType string
	switch format {
	case "png", "jpeg", "gif", "webp":
		contentType = "image/" + format
	default:
		return "", errors.New("image content is not a supported image")
	}
	if detectedImageContentType(data) != contentType {
		return "", errors.New("image content does not match its format")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > imageStorageMaxDimension || config.Height > imageStorageMaxDimension || int64(config.Width)*int64(config.Height) > imageStorageMaxPixels {
		return "", errors.New("image dimensions exceed the supported limit")
	}
	return contentType, nil
}

func detectedImageContentType(data []byte) string {
	ct := strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0])
	if strings.HasPrefix(ct, "image/") {
		return ct
	}
	return ""
}

func extensionForContentType(ct string) string {
	switch {
	case strings.Contains(ct, "png"):
		return ".png"
	case strings.Contains(ct, "jpeg"), strings.Contains(ct, "jpg"):
		return ".jpg"
	case strings.Contains(ct, "webp"):
		return ".webp"
	case strings.Contains(ct, "gif"):
		return ".gif"
	default:
		return ".png"
	}
}
