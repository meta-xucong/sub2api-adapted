package service

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// 请求级 hint 仅限 HTTP：缺失表示 unknown，false/true 都表示已完成 canonical 判定。
const openAIImageIntentHintContextKey = "openai_image_intent_hint"
const openAIImageGenerationOutputRequiredContextKey = "openai_image_generation_output_required"

// ErrOpenAIImageGenerationOutputMissing indicates the upstream completed an
// explicit image request but did not return any usable image artifact.
var ErrOpenAIImageGenerationOutputMissing = errors.New("upstream image generation completed without an image result")

type openAIImageIntentClassifier func(endpoint string, requestedModel string, body []byte) bool

// SetOpenAIImageIntentHint 只写入请求级 canonical 判定，不记录 attempt-local 结果。
func SetOpenAIImageIntentHint(c *gin.Context, imageIntent bool) {
	if c == nil || GetOpenAIClientTransport(c) != OpenAIClientTransportHTTP {
		return
	}
	c.Set(openAIImageIntentHintContextKey, imageIntent)
}

func setOpenAIImageGenerationOutputRequired(c *gin.Context, required bool) {
	if c != nil {
		c.Set(openAIImageGenerationOutputRequiredContextKey, required)
	}
}

func openAIImageGenerationOutputRequired(c *gin.Context) bool {
	if c == nil {
		return false
	}
	required, _ := c.Get(openAIImageGenerationOutputRequiredContextKey)
	value, _ := required.(bool)
	return value
}

// isOpenAIImageGenerationOutputRequired uses only explicit request signals.
// Merely advertising image_generation (which Codex does on ordinary turns)
// must not turn a text response into a failed image request.
func isOpenAIImageGenerationOutputRequired(requestedModel string, body []byte) bool {
	if isOpenAIImageGenerationModel(strings.TrimSpace(requestedModel)) {
		return true
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	if isOpenAIImageGenerationModel(strings.TrimSpace(gjson.GetBytes(body, "model").String())) ||
		openAIJSONToolChoiceSelectsImageGeneration(gjson.GetBytes(body, "tool_choice")) {
		return true
	}
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return false
	}
	for _, tool := range tools.Array() {
		if !isOpenAIImageGenerationType(strings.TrimSpace(tool.Get("type").String())) {
			continue
		}
		if isOpenAIImageGenerationModel(strings.TrimSpace(tool.Get("model").String())) {
			return true
		}
	}
	return false
}

func getOpenAIImageIntentHint(c *gin.Context) (imageIntent bool, known bool) {
	if c == nil || GetOpenAIClientTransport(c) != OpenAIClientTransportHTTP {
		return false, false
	}
	value, ok := c.Get(openAIImageIntentHintContextKey)
	if !ok {
		return false, false
	}
	imageIntent, ok = value.(bool)
	return imageIntent, ok
}

func resolveOpenAIImageIntentHint(
	c *gin.Context,
	requestedModel string,
	canonicalBody []byte,
	classify openAIImageIntentClassifier,
) bool {
	if imageIntent, known := getOpenAIImageIntentHint(c); known {
		return imageIntent
	}
	imageIntent := classify(openAIResponsesEndpoint, requestedModel, canonicalBody)
	SetOpenAIImageIntentHint(c, imageIntent)
	return imageIntent
}

func resolveOpenAIPassthroughImageIntent(
	c *gin.Context,
	canonicalRequestedModel string,
	canonicalBody []byte,
	attemptRequestedModel string,
	attemptBody []byte,
	attemptInvalidated bool,
	classify openAIImageIntentClassifier,
) bool {
	imageIntent := resolveOpenAIImageIntentHint(c, canonicalRequestedModel, canonicalBody, classify)
	if attemptInvalidated {
		// strip/compact 改写只重算当前 attempt，不得把变换后的结果写回请求级 canonical hint。
		imageIntent = classify(openAIResponsesEndpoint, attemptRequestedModel, attemptBody)
	}
	return imageIntent
}
