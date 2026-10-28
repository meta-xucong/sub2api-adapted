package apicompat

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

const anthropicCompactionEnvelopePrefix = "anthropic-compaction-v1:"

func ResponsesHasCompactionTrigger(input json.RawMessage) bool {
	var items []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(input, &items) != nil {
		return false
	}
	for _, item := range items {
		if item.Type == "compaction_trigger" {
			return true
		}
	}
	return false
}

func ResponsesHasAnthropicCompactionState(input json.RawMessage) bool {
	var items []struct {
		Type             string `json:"type"`
		EncryptedContent string `json:"encrypted_content"`
	}
	if json.Unmarshal(input, &items) != nil {
		return false
	}
	for _, item := range items {
		if (item.Type == "compaction" || item.Type == "compaction_summary") && strings.HasPrefix(item.EncryptedContent, anthropicCompactionEnvelopePrefix) {
			return true
		}
	}
	return false
}

func EncodeAnthropicCompactionEnvelope(block json.RawMessage) string {
	return anthropicCompactionEnvelopePrefix + base64.RawURLEncoding.EncodeToString(block)
}

func decodeAnthropicCompactionEnvelope(value string) (json.RawMessage, error) {
	encoded := strings.TrimPrefix(value, anthropicCompactionEnvelopePrefix)
	return base64.RawURLEncoding.DecodeString(encoded)
}
