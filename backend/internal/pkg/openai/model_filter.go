package openai

import (
	"sort"
	"strings"
	"unicode"
)

var stableAutoDiscoveryModels = map[string]struct{}{
	"codex-auto-review":   {},
	"gpt-5.3-codex-spark": {},
	"gpt-5.4":             {},
	"gpt-5.4-mini":        {},
	"gpt-5.4-nano":        {},
	"gpt-5.5":             {},
	"gpt-5.5-pro":         {},
	"gpt-5.6":             {},
	"gpt-5.6-luna":        {},
	"gpt-5.6-sol":         {},
	"gpt-5.6-terra":       {},
	"gpt-image-2":         {},
}

var deprecatedAutoDiscoveryModels = map[string]struct{}{
	"chatgpt-image-latest": {},
	"gpt-5-chat-latest":    {},
	"gpt-5.1-chat-latest":  {},
	"gpt-5.1-codex":        {},
	"gpt-5.1-codex-max":    {},
	"gpt-5.1-codex-mini":   {},
	"gpt-5.2":              {},
	"gpt-5.2-chat-latest":  {},
	"gpt-5.2-codex":        {},
	"gpt-5.2-pro":          {},
	"gpt-5.3-chat-latest":  {},
	"gpt-image-1":          {},
	"gpt-image-1-mini":     {},
	"gpt-image-1.5":        {},
}

// FilterAutoDiscoveredModelIDs returns model IDs safe to add through automatic
// upstream sync. It intentionally removes OpenAI snapshot IDs and deprecated
// image/chat aliases while leaving non-OpenAI custom provider IDs alone.
func FilterAutoDiscoveredModelIDs(models []string) []string {
	filtered := make([]string, 0, len(models))
	for _, model := range models {
		if IsAutoDiscoveredModelID(model) {
			filtered = append(filtered, normalizeListedModelID(model))
		}
	}
	return dedupeAndSortModelIDs(filtered)
}

func IsAutoDiscoveredModelID(model string) bool {
	model = normalizeListedModelID(model)
	if model == "" {
		return false
	}
	lower := strings.ToLower(model)
	if _, ok := stableAutoDiscoveryModels[lower]; ok {
		return true
	}
	if _, ok := deprecatedAutoDiscoveryModels[lower]; ok {
		return false
	}
	if isOpenAIDatedSnapshot(lower) || strings.HasPrefix(lower, "ft:") {
		return false
	}
	if !looksLikeOpenAIManagedModel(lower) {
		return true
	}
	return isFutureStableOpenAIModel(lower)
}

func normalizeListedModelID(model string) string {
	return strings.TrimPrefix(strings.TrimSpace(model), "models/")
}

func looksLikeOpenAIManagedModel(model string) bool {
	return strings.HasPrefix(model, "gpt-") ||
		strings.HasPrefix(model, "chatgpt-") ||
		strings.HasPrefix(model, "codex-") ||
		strings.HasPrefix(model, "computer-use") ||
		looksLikeOFamilyModel(model)
}

func looksLikeOFamilyModel(model string) bool {
	if len(model) < 2 || model[0] != 'o' {
		return false
	}
	return model[1] >= '0' && model[1] <= '9'
}

func isFutureStableOpenAIModel(model string) bool {
	if strings.HasPrefix(model, "gpt-image-") {
		return isStableGPTImageModel(model)
	}
	if !strings.HasPrefix(model, "gpt-") {
		return false
	}
	version, suffix, ok := splitGPTVersionAndSuffix(strings.TrimPrefix(model, "gpt-"))
	if !ok || compareGPTVersion(version, []int{5, 6}) < 0 {
		return false
	}
	if suffix == "" {
		return true
	}
	switch suffix {
	case "mini", "nano", "pro", "sol", "terra", "luna":
		return true
	default:
		return false
	}
}

func isStableGPTImageModel(model string) bool {
	versionText := strings.TrimPrefix(model, "gpt-image-")
	if versionText == "" {
		return false
	}
	version, suffix, ok := splitGPTVersionAndSuffix(versionText)
	return ok && suffix == "" && len(version) > 0 && version[0] >= 2
}

func splitGPTVersionAndSuffix(rest string) ([]int, string, bool) {
	versionPart := rest
	suffix := ""
	if idx := strings.IndexByte(rest, '-'); idx >= 0 {
		versionPart = rest[:idx]
		suffix = rest[idx+1:]
	}
	if versionPart == "" || strings.Contains(suffix, "-") {
		return nil, "", false
	}
	parts := strings.Split(versionPart, ".")
	version := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, "", false
		}
		value := 0
		for _, r := range part {
			if !unicode.IsDigit(r) {
				return nil, "", false
			}
			value = value*10 + int(r-'0')
		}
		version = append(version, value)
	}
	return version, suffix, true
}

func compareGPTVersion(a, b []int) int {
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	for i := 0; i < maxLen; i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

func isOpenAIDatedSnapshot(model string) bool {
	parts := strings.Split(model, "-")
	for i := 0; i+2 < len(parts); i++ {
		if isFourDigitYear(parts[i]) && isTwoDigitNumber(parts[i+1]) && isTwoDigitNumber(parts[i+2]) {
			return true
		}
	}
	return false
}

func isFourDigitYear(s string) bool {
	if len(s) != 4 {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return strings.HasPrefix(s, "20") || strings.HasPrefix(s, "19")
}

func isTwoDigitNumber(s string) bool {
	if len(s) != 2 {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func dedupeAndSortModelIDs(models []string) []string {
	seen := make(map[string]struct{}, len(models))
	result := make([]string, 0, len(models))
	for _, model := range models {
		model = normalizeListedModelID(model)
		if model == "" {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		result = append(result, model)
	}
	sort.Strings(result)
	return result
}
