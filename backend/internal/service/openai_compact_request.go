package service

import (
	"context"

	"github.com/gin-gonic/gin"
)

const openAIInBandCompactionKey = "openai_in_band_compaction"

type openAICompactModelMappingContextKey struct{}

// MarkOpenAIInBandCompaction marks a native /responses compaction request.
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

// IsOpenAIResponsesCompactRequest combines path-based and native in-band
// compaction. It is intentionally not used to build the upstream URL.
func IsOpenAIResponsesCompactRequest(c *gin.Context) bool {
	return IsOpenAIResponsesCompactPathForTest(c) || IsOpenAIInBandCompaction(c)
}

// WithOpenAIInBandCompaction keeps native /responses compaction on the user's
// requested model. compact_model_mapping belongs only to the legacy unary
// /responses/compact protocol, so scheduling must not use it to reject a
// native request before the unchanged model reaches the upstream.
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

func OpenAIInBandCompactionKeyForTest() string {
	return openAIInBandCompactionKey
}
