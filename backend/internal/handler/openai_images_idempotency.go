package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	openAIImagesInFlightTTL = 30 * time.Minute
	openAIImagesResultTTL   = 20 * time.Minute
	openAIImagesMaxEntries  = 64
	openAIImagesMaxBodySize = 32 << 20
)

type openAIImagesCachedResponse struct {
	status int
	header http.Header
	body   []byte
}

func (r *openAIImagesCachedResponse) writeTo(c *gin.Context) {
	if r == nil || c == nil || c.Writer == nil {
		return
	}
	for key, values := range r.header {
		c.Writer.Header().Del(key)
		for _, value := range values {
			c.Writer.Header().Add(key, value)
		}
	}
	c.Header("X-Sub2api-Image-Cache", "hit")
	contentType := r.header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json; charset=utf-8"
	}
	c.Data(r.status, contentType, r.body)
}

type openAIImagesCacheEntry struct {
	key       string
	createdAt time.Time
	expiresAt time.Time
	done      chan struct{}
	result    *openAIImagesCachedResponse
	completed bool
}

func (e *openAIImagesCacheEntry) isCompleted() bool {
	if e == nil {
		return true
	}
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}

type openAIImagesRequestCache struct {
	mu      sync.Mutex
	entries map[string]*openAIImagesCacheEntry
}

func newOpenAIImagesRequestCache() *openAIImagesRequestCache {
	return &openAIImagesRequestCache{entries: make(map[string]*openAIImagesCacheEntry)}
}

func (c *openAIImagesRequestCache) begin(key string, now time.Time) (*openAIImagesCacheEntry, bool, *openAIImagesCachedResponse) {
	if c == nil || key == "" {
		return nil, true, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cleanupLocked(now)
	if entry := c.entries[key]; entry != nil {
		if entry.result != nil && now.Before(entry.expiresAt) {
			return entry, false, entry.result
		}
		if entry.result == nil && now.Sub(entry.createdAt) < openAIImagesInFlightTTL {
			return entry, false, nil
		}
		delete(c.entries, key)
	}
	entry := &openAIImagesCacheEntry{
		key:       key,
		createdAt: now,
		expiresAt: now.Add(openAIImagesInFlightTTL),
		done:      make(chan struct{}),
	}
	c.entries[key] = entry
	return entry, true, nil
}

func (c *openAIImagesRequestCache) finishSuccess(entry *openAIImagesCacheEntry, response *openAIImagesCachedResponse, now time.Time) {
	if c == nil || entry == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries[entry.key] != entry {
		return
	}
	if entry.completed {
		return
	}
	if response != nil && len(response.body) <= openAIImagesMaxBodySize {
		entry.result = response
		entry.expiresAt = now.Add(openAIImagesResultTTL)
	} else {
		delete(c.entries, entry.key)
	}
	entry.completed = true
	close(entry.done)
	c.cleanupLocked(now)
}

func (c *openAIImagesRequestCache) finishError(entry *openAIImagesCacheEntry) {
	if c == nil || entry == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries[entry.key] == entry && !entry.completed {
		delete(c.entries, entry.key)
		entry.completed = true
		close(entry.done)
	}
}

func (c *openAIImagesRequestCache) wait(ctx context.Context, entry *openAIImagesCacheEntry) (*openAIImagesCachedResponse, bool) {
	if entry == nil {
		return nil, false
	}
	select {
	case <-entry.done:
	case <-ctx.Done():
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if current := c.entries[entry.key]; current == entry && entry.result != nil && time.Now().Before(entry.expiresAt) {
		return entry.result, true
	}
	return nil, false
}

func (c *openAIImagesRequestCache) cleanupLocked(now time.Time) {
	for key, entry := range c.entries {
		if entry == nil || now.After(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
	if len(c.entries) <= openAIImagesMaxEntries {
		return
	}
	var oldestKey string
	var oldest time.Time
	for key, entry := range c.entries {
		if entry == nil || entry.result == nil {
			continue
		}
		if oldestKey == "" || entry.createdAt.Before(oldest) {
			oldestKey = key
			oldest = entry.createdAt
		}
	}
	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

func openAIImagesRequestCacheKey(apiKey *service.APIKey, subject middleware2.AuthSubject, parsed *service.OpenAIImagesRequest, body []byte) string {
	if apiKey == nil || parsed == nil || parsed.Stream {
		return ""
	}
	sum := sha256.Sum256(body)
	return fmt.Sprintf(
		"openai-images:v1:user=%d:key=%d:endpoint=%s:model=%s:body=%s",
		subject.UserID,
		apiKey.ID,
		parsed.Endpoint,
		parsed.Model,
		hex.EncodeToString(sum[:]),
	)
}

type openAIImagesBufferedWriter struct {
	gin.ResponseWriter
	header http.Header
	body   bytes.Buffer
	status int
	size   int
}

func newOpenAIImagesBufferedWriter(base gin.ResponseWriter) *openAIImagesBufferedWriter {
	header := make(http.Header, len(base.Header()))
	for key, values := range base.Header() {
		header[key] = append([]string(nil), values...)
	}
	return &openAIImagesBufferedWriter{
		ResponseWriter: base,
		header:         header,
		status:         http.StatusOK,
		size:           -1,
	}
}

func (w *openAIImagesBufferedWriter) Header() http.Header {
	return w.header
}

func (w *openAIImagesBufferedWriter) WriteHeader(code int) {
	if code > 0 && !w.Written() {
		w.status = code
	}
}

func (w *openAIImagesBufferedWriter) WriteHeaderNow() {
	if !w.Written() {
		w.size = 0
	}
}

func (w *openAIImagesBufferedWriter) Write(data []byte) (int, error) {
	w.WriteHeaderNow()
	n, err := w.body.Write(data)
	w.size += n
	return n, err
}

func (w *openAIImagesBufferedWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *openAIImagesBufferedWriter) Status() int {
	return w.status
}

func (w *openAIImagesBufferedWriter) Size() int {
	return w.size
}

func (w *openAIImagesBufferedWriter) Written() bool {
	return w.size >= 0
}

func (w *openAIImagesBufferedWriter) Flush() {}

func (w *openAIImagesBufferedWriter) cachedResponse() *openAIImagesCachedResponse {
	header := make(http.Header, len(w.header))
	for key, values := range w.header {
		header[key] = append([]string(nil), values...)
	}
	return &openAIImagesCachedResponse{
		status: w.status,
		header: header,
		body:   append([]byte(nil), w.body.Bytes()...),
	}
}

func (w *openAIImagesBufferedWriter) flushTo(dst gin.ResponseWriter) {
	if w == nil || dst == nil || !w.Written() {
		return
	}
	for key, values := range w.header {
		dst.Header().Del(key)
		for _, value := range values {
			dst.Header().Add(key, value)
		}
	}
	dst.WriteHeader(w.status)
	_, _ = io.Copy(dst, bytes.NewReader(w.body.Bytes()))
}
