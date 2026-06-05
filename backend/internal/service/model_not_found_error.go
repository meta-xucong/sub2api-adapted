package service

import (
	"net/http"
	"strings"
)

var upstreamModelNotFoundKeywords = []string{"model not found", "unknown model", "not found"}

type upstreamModelUnavailableMatch struct {
	reason string
}

func isUpstreamModelNotFoundError(statusCode int, body []byte) bool {
	if statusCode != http.StatusNotFound {
		return false
	}
	normalized := normalizeModelNotFoundBody(body)
	if normalized == "" || !strings.Contains(normalized, "model") {
		return false
	}
	return containsModelNotFoundKeyword(normalized)
}

func isModelNotFoundError(statusCode int, body []byte) bool {
	return isUpstreamModelNotFoundError(statusCode, body) || statusCode == http.StatusNotFound
}

func classifyUpstreamModelUnavailableError(account *Account, statusCode int, body []byte) (upstreamModelUnavailableMatch, bool) {
	if isUpstreamModelNotFoundError(statusCode, body) {
		return upstreamModelUnavailableMatch{reason: upstreamModelNotFoundReason}, true
	}
	if account != nil && account.Platform == PlatformOpenAI && isOpenAIImageGenerationToolUnavailableError(statusCode, body) {
		return upstreamModelUnavailableMatch{reason: upstreamModelCapabilityUnavailableReason}, true
	}
	return upstreamModelUnavailableMatch{}, false
}

func isOpenAIChatGPTAccountModelUnsupportedError(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}
	return strings.Contains(normalizeOpenAIAccountCapabilityBody(body), "not supported when using codex with a chatgpt account")
}

func isOpenAIImageGenerationToolUnavailableError(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}
	return strings.Contains(normalizeOpenAIAccountCapabilityBody(body), "tool choice 'image_generation' not found in 'tools' parameter")
}

func containsModelNotFoundKeyword(normalizedBody string) bool {
	if normalizedBody == "" {
		return false
	}
	for _, keyword := range upstreamModelNotFoundKeywords {
		if strings.Contains(normalizedBody, keyword) {
			return true
		}
	}
	return false
}

func normalizeModelNotFoundBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	normalized := strings.ToLower(string(body))
	normalized = strings.NewReplacer("_", " ", "-", " ", "\n", " ", "\r", " ", "\t", " ").Replace(normalized)
	return strings.Join(strings.Fields(normalized), " ")
}

func normalizeOpenAIAccountCapabilityBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	msg := strings.TrimSpace(extractUpstreamErrorMessage(body))
	raw := strings.TrimSpace(string(body))
	normalized := strings.ToLower(strings.TrimSpace(msg + " " + raw))
	normalized = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(normalized)
	return strings.Join(strings.Fields(normalized), " ")
}
