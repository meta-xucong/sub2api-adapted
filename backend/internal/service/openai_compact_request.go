package service

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
)

const openAIInBandCompactionKey = "openai_in_band_compaction"

type openAICompactModelMappingContextKey struct{}

// MarkOpenAIInBandCompaction marks a native Responses compaction request.
// Unlike the legacy compact endpoint this marker must not alter the upstream
// URL or request body.
func MarkOpenAIInBandCompaction(c *gin.Context) {
	if c != nil {
		c.Set(openAIInBandCompactionKey, true)
	}
}

// IsOpenAIInBandCompaction reports whether the current request carries the
// native Responses API compaction instruction.
func IsOpenAIInBandCompaction(c *gin.Context) bool {
	if c == nil {
		return false
	}
	value, ok := c.Get(openAIInBandCompactionKey)
	marked, _ := value.(bool)
	return ok && marked
}

// IsOpenAIResponsesCompactRequest combines the legacy path signal with the
// native in-band marker. It is intentionally not used to build the upstream
// URL; it only scopes compact-specific observability and compatibility logic.
func IsOpenAIResponsesCompactRequest(c *gin.Context) bool {
	return IsOpenAIResponsesCompactPathForTest(c) || IsOpenAIInBandCompaction(c)
}

// IsOpenAIResponsesCompactPathForTest reports the legacy compact endpoint
// without depending on a particular router prefix. The suffix match keeps
// this helper usable by unit tests and by installations mounted below a
// reverse-proxy prefix while avoiding a match for unrelated paths.
func IsOpenAIResponsesCompactPathForTest(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}
	path := strings.TrimRight(strings.ToLower(strings.TrimSpace(c.Request.URL.Path)), "/")
	return path == "/responses/compact" || strings.HasSuffix(path, "/responses/compact")
}

// WithOpenAIInBandCompaction keeps native Responses compaction on the user's
// requested model. compact_model_mapping belongs only to the legacy unary
// protocol, so scheduling must not use it to reject a native request before
// the unchanged model reaches the upstream.
func WithOpenAIInBandCompaction(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAICompactModelMappingContextKey{}, false)
}

func useOpenAICompactModelMapping(ctx context.Context, requireCompact bool) bool {
	if ctx != nil {
		if useMapping, ok := ctx.Value(openAICompactModelMappingContextKey{}).(bool); ok {
			return useMapping
		}
	}
	return requireCompact
}

// OpenAIInBandCompactionKeyForTest exposes the marker key to focused tests
// without exposing the request context implementation.
func OpenAIInBandCompactionKeyForTest() string {
	return openAIInBandCompactionKey
}
