package service

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/tidwall/gjson"
)

type openAIImageOutputCounter struct {
	seen                            map[string]struct{}
	seenSizes                       map[string]string
	seenOrder                       []string
	dataSizes                       []string
	count                           int
	maxDataCount                    int
	hasResponsesImageResult         bool
	hasResponsesImageGenerationCall bool
}

func newOpenAIImageOutputCounter() *openAIImageOutputCounter {
	return &openAIImageOutputCounter{
		seen:      make(map[string]struct{}),
		seenSizes: make(map[string]string),
	}
}

func (c *openAIImageOutputCounter) Count() int {
	if c == nil {
		return 0
	}
	if c.maxDataCount > c.count {
		return c.maxDataCount
	}
	return c.count
}

func (c *openAIImageOutputCounter) Sizes() []string {
	if c == nil {
		return nil
	}
	sizes := make([]string, 0, len(c.seenOrder)+len(c.dataSizes))
	for _, key := range c.seenOrder {
		if size := strings.TrimSpace(c.seenSizes[key]); size != "" {
			sizes = append(sizes, size)
		}
	}
	if len(sizes) == 0 && len(c.dataSizes) > 0 {
		sizes = append(sizes, c.dataSizes...)
	}
	if len(sizes) == 0 {
		return nil
	}
	return sizes
}

func (c *openAIImageOutputCounter) AddJSONResponse(body []byte) {
	if c == nil || len(body) == 0 || !gjson.ValidBytes(body) {
		return
	}
	if hasOpenAIResponsesImageResultJSON(body) {
		c.hasResponsesImageResult = true
	}
	if hasOpenAIResponsesImageGenerationCallJSON(body) {
		c.hasResponsesImageGenerationCall = true
	}
	c.addDataArray(gjson.GetBytes(body, "data"))
	c.addOutputArray(gjson.GetBytes(body, "output"))
	c.addOutputArray(gjson.GetBytes(body, "response.output"))
}

func (c *openAIImageOutputCounter) AddSSEData(data []byte) {
	if c == nil || len(data) == 0 || strings.TrimSpace(string(data)) == "[DONE]" || !gjson.ValidBytes(data) {
		return
	}
	if hasOpenAIResponsesImageResultSSEData(data) {
		c.hasResponsesImageResult = true
	}
	if hasOpenAIResponsesImageGenerationCallSSEData(data) {
		c.hasResponsesImageGenerationCall = true
	}
	root := gjson.ParseBytes(data)
	eventType := strings.TrimSpace(root.Get("type").String())
	switch eventType {
	case "response.image_generation_call.partial_image", "image_generation.partial_image", "image_edit.partial_image":
		return
	}
	c.addDataArray(root.Get("data"))

	switch eventType {
	case "response.output_item.done":
		c.addImageOutputItem(root.Get("item"))
	case "response.completed", "response.done":
		c.addOutputArray(root.Get("response.output"))
	case "image_generation.completed", "image_edit.completed":
		if item := root.Get("item"); item.Exists() {
			c.addImageOutputItem(item)
			return
		}
		if output := root.Get("output"); output.Exists() {
			c.addImageOutputItem(output)
			return
		}
		c.addImageOutputItem(root)
	}
}

func (c *openAIImageOutputCounter) HasResponsesImageResult() bool {
	return c != nil && c.hasResponsesImageResult
}

func (c *openAIImageOutputCounter) HasResponsesImageGenerationCall() bool {
	return c != nil && c.hasResponsesImageGenerationCall
}

func hasOpenAIResponsesImageGenerationCallJSON(body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	return openAIResponsesOutputHasImageGenerationCall(gjson.GetBytes(body, "output")) ||
		openAIResponsesOutputHasImageGenerationCall(gjson.GetBytes(body, "response.output"))
}

func openAIResponsesOutputHasImageGenerationCall(output gjson.Result) bool {
	if !output.IsArray() {
		return false
	}
	for _, item := range output.Array() {
		if strings.TrimSpace(item.Get("type").String()) == "image_generation_call" {
			return true
		}
	}
	return false
}

func hasOpenAIResponsesImageGenerationCallSSEData(data []byte) bool {
	if len(data) == 0 || !gjson.ValidBytes(data) {
		return false
	}
	root := gjson.ParseBytes(data)
	switch strings.TrimSpace(root.Get("type").String()) {
	case "response.output_item.added", "response.output_item.done":
		return strings.TrimSpace(root.Get("item.type").String()) == "image_generation_call"
	case "response.completed", "response.done":
		return openAIResponsesOutputHasImageGenerationCall(root.Get("response.output"))
	case "image_generation.in_progress", "image_generation.partial_image", "image_generation.completed":
		return true
	default:
		return false
	}
}

func hasOpenAIResponsesImageGenerationCallInSSEBody(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if hasOpenAIResponsesImageGenerationCallSSEData([]byte(data)) {
			return true
		}
	}
	return false
}

func hasOpenAIResponsesImageResultJSON(body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	return openAIResponsesOutputHasImageResult(gjson.GetBytes(body, "output")) ||
		openAIResponsesOutputHasImageResult(gjson.GetBytes(body, "response.output"))
}

func openAIResponsesOutputHasImageResult(output gjson.Result) bool {
	if !output.IsArray() {
		return false
	}
	for _, item := range output.Array() {
		if openAIResponsesImageItemHasResult(item) {
			return true
		}
	}
	return false
}

func openAIResponsesImageItemHasResult(item gjson.Result) bool {
	if !item.IsObject() {
		return false
	}
	itemType := strings.TrimSpace(item.Get("type").String())
	if itemType != "image_generation_call" && itemType != "image_generation.completed" && itemType != "image_edit.completed" {
		return false
	}
	return nonemptyImageArtifactString(item.Get("result")) != "" ||
		nonemptyImageArtifactString(item.Get("b64_json")) != "" ||
		nonemptyImageArtifactString(item.Get("url")) != ""
}

func hasOpenAIResponsesImageResultSSEData(data []byte) bool {
	if len(data) == 0 || !gjson.ValidBytes(data) {
		return false
	}
	root := gjson.ParseBytes(data)
	switch strings.TrimSpace(root.Get("type").String()) {
	case "response.output_item.done":
		return openAIResponsesImageItemHasResult(root.Get("item"))
	case "response.completed", "response.done":
		return openAIResponsesOutputHasImageResult(root.Get("response.output"))
	case "image_generation.completed", "image_edit.completed":
		item := root.Get("item")
		if !item.Exists() {
			item = root.Get("output")
		}
		if !item.Exists() {
			item = root
		}
		return openAIResponsesImageItemHasResult(item)
	default:
		return false
	}
}

func hasOpenAIResponsesImageResultSSEBody(body string) bool {
	found := false
	forEachOpenAISSEDataPayload(body, func(data []byte) {
		if !found && hasOpenAIResponsesImageResultSSEData(data) {
			found = true
		}
	})
	return found
}

func (c *openAIImageOutputCounter) AddSSEBody(body string) {
	if c == nil || strings.TrimSpace(body) == "" {
		return
	}
	forEachOpenAISSEDataPayload(body, c.AddSSEData)
}

func (c *openAIImageOutputCounter) addDataArray(data gjson.Result) {
	if !data.IsArray() {
		return
	}
	items := data.Array()
	imageCount := 0
	sizes := make([]string, 0, len(items))
	for _, item := range items {
		if !item.IsObject() {
			continue
		}
		hasImageOutput := nonemptyImageArtifactString(item.Get("url")) != "" ||
			nonemptyImageArtifactString(item.Get("b64_json")) != ""
		if !hasImageOutput {
			continue
		}
		imageCount++
		if size := strings.TrimSpace(item.Get("size").String()); size != "" {
			sizes = append(sizes, size)
		}
	}
	if imageCount > c.maxDataCount {
		c.maxDataCount = imageCount
	}
	if len(sizes) > 0 {
		c.dataSizes = sizes
	}
}

func (c *openAIImageOutputCounter) addOutputArray(output gjson.Result) {
	if !output.IsArray() {
		return
	}
	output.ForEach(func(_, item gjson.Result) bool {
		c.addImageOutputItem(item)
		return true
	})
}

func (c *openAIImageOutputCounter) addImageOutputItem(item gjson.Result) {
	if !item.Exists() || !item.IsObject() {
		return
	}
	itemType := strings.TrimSpace(item.Get("type").String())
	if itemType != "" && itemType != "image_generation_call" && itemType != "image_generation.completed" && itemType != "image_edit.completed" {
		return
	}
	// Partial identity comes from protocol fields, never arbitrary prompt,
	// artifact URL, or ID text in an otherwise completed image item.
	switch strings.ToLower(strings.TrimSpace(item.Get("status").String())) {
	case "partial_image", "partial", "in_progress", "generating":
		return
	}
	result := nonemptyImageArtifactString(item.Get("result"))
	if result == "" {
		result = nonemptyImageArtifactString(item.Get("b64_json"))
	}
	if result == "" {
		result = nonemptyImageArtifactString(item.Get("url"))
	}
	if result == "" {
		return
	}
	key := strings.TrimSpace(item.Get("id").String())
	if key == "" {
		key = strings.TrimSpace(item.Get("call_id").String())
	}
	if key == "" {
		key = hashOpenAIImageOutputResult(result)
	}
	if key == "" {
		return
	}
	size := strings.TrimSpace(item.Get("size").String())
	if _, exists := c.seen[key]; exists {
		if size != "" && strings.TrimSpace(c.seenSizes[key]) == "" {
			c.seenSizes[key] = size
		}
		return
	}
	c.seen[key] = struct{}{}
	c.seenOrder = append(c.seenOrder, key)
	if size != "" {
		c.seenSizes[key] = size
	}
	c.count++
}

func hashOpenAIImageOutputResult(result string) string {
	result = strings.TrimSpace(result)
	if result == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(result))
	return hex.EncodeToString(sum[:])
}

func countOpenAIResponseImageOutputsFromJSONBytes(body []byte) int {
	counter := newOpenAIImageOutputCounter()
	counter.AddJSONResponse(body)
	return counter.Count()
}

func collectOpenAIResponseImageOutputSizesFromJSONBytes(body []byte) []string {
	counter := newOpenAIImageOutputCounter()
	counter.AddJSONResponse(body)
	return counter.Sizes()
}

func countOpenAIImageOutputsFromSSEBody(body string) int {
	counter := newOpenAIImageOutputCounter()
	counter.AddSSEBody(body)
	return counter.Count()
}

func collectOpenAIImageOutputSizesFromSSEBody(body string) []string {
	counter := newOpenAIImageOutputCounter()
	counter.AddSSEBody(body)
	return counter.Sizes()
}

// Artifact payloads are strings; numbers, booleans and objects are not images.
func nonemptyImageArtifactString(value gjson.Result) string {
	if value.Type != gjson.String {
		return ""
	}
	return strings.TrimSpace(value.String())
}
