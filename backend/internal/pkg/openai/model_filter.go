package openai

import (
	"sort"
	"strings"
	"unicode"
)

var stableAutoDiscoveryModels = map[string]struct{}{
	"gpt-5.3-codex-spark":    {},
	"gpt-5.4":                {},
	"gpt-5.4-mini":           {},
	"gpt-5.4-nano":           {},
	"gpt-5.5":                {},
	"gpt-5.5-pro":            {},
	"gpt-5.6-luna":           {},
	"gpt-5.6-sol":            {},
	"gpt-5.6-terra":          {},
	"gpt-6-astra":            {},
	"gpt-6-luna":             {},
	"gpt-6-sol":              {},
	"gpt-image-2":            {},
	"gpt-image-2.5-flare":    {},
	"gpt-image-2.5-sunburst": {},
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
	"gpt-5.6":              {},
	"gpt-5.3-chat-latest":  {},
	"gpt-image-1":          {},
	"gpt-image-1-mini":     {},
	"gpt-image-1.5":        {},
	"gpt-6":                {},
}

// adminSelectableOpenAIModels is the intentionally small list shown in
// ordinary admin model pickers. Internal aliases and upstream snapshots may
// remain routable, but they should not become accidental user-facing choices.
var adminSelectableOpenAIModels = []string{
	"gpt-5.6-sol",
	"gpt-5.6-terra",
	"gpt-5.6-luna",
	"gpt-6-astra",
	"gpt-6-sol",
	"gpt-6-luna",
	"gpt-5.5",
	"gpt-5.4",
	"gpt-5.4-mini",
	"gpt-image-2",
	"gpt-image-2.5-flare",
	"gpt-image-2.5-sunburst",
}

var adminSelectableOpenAIModelSet = makeModelSet(adminSelectableOpenAIModels)

var adminHiddenOpenAIModels = map[string]struct{}{
	"codex-auto-review":   {},
	"gpt-5.3-codex-spark": {},
	"gpt-5.4-nano":        {},
	"gpt-5.5-pro":         {},
	"gpt-5.6":             {},
	"codex-mini-latest":   {},
}

// AdminSelectableModelIDs returns the curated OpenAI model IDs intended for
// ordinary admin model-list configuration. This is deliberately separate from
// DefaultModelIDs: the latter is also used for routing compatibility.
func AdminSelectableModelIDs() []string {
	return append([]string(nil), adminSelectableOpenAIModels...)
}

// AdminSelectableModels returns the metadata for the curated IDs without
// changing DefaultModels, which is also used for routing compatibility.
func AdminSelectableModels() []Model {
	byID := make(map[string]Model, len(DefaultModels))
	for _, model := range DefaultModels {
		byID[model.ID] = model
	}
	models := make([]Model, 0, len(adminSelectableOpenAIModels))
	for _, id := range adminSelectableOpenAIModels {
		if model, ok := byID[id]; ok {
			models = append(models, model)
			continue
		}
		models = append(models, Model{ID: id, Object: "model", Type: "model", OwnedBy: "openai", DisplayName: id})
	}
	return models
}

// IsAdminSelectableModelID reports whether a model may be offered as a normal
// OpenAI admin picker choice. Non-OpenAI-looking aliases are preserved because
// providers commonly expose custom names such as "aiai-gpt-image-2".
func IsAdminSelectableModelID(model string) bool {
	model = normalizeListedModelID(model)
	if model == "" {
		return false
	}
	lower := strings.ToLower(model)
	if strings.HasPrefix(lower, "codex-auto-") {
		return false
	}
	if _, ok := adminHiddenOpenAIModels[lower]; ok {
		return false
	}
	if _, ok := adminSelectableOpenAIModelSet[lower]; ok {
		return true
	}
	if looksLikeOpenAIManagedModel(lower) {
		return false
	}
	return true
}

// FilterAdminSelectableModelIDs filters OpenAI IDs for admin-facing pickers
// while preserving custom provider aliases. The input order is retained so
// callers can keep their configured priority order.
func FilterAdminSelectableModelIDs(models []string) []string {
	filtered := make([]string, 0, len(models))
	for _, model := range models {
		if IsAdminSelectableModelID(model) {
			filtered = append(filtered, normalizeListedModelID(model))
		}
	}
	return dedupeModelIDs(filtered)
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
	if strings.HasPrefix(lower, "codex-auto-") {
		return false
	}
	if _, ok := stableAutoDiscoveryModels[lower]; ok {
		return true
	}
	if _, ok := deprecatedAutoDiscoveryModels[lower]; ok {
		return false
	}
	if !looksLikeOpenAIManagedModel(lower) {
		// Only OpenAI-managed IDs are subject to the date/ft cleanup rules.
		// A custom provider is allowed to use a date-looking public ID.
		return true
	}
	if isOpenAIDatedSnapshot(lower) || strings.HasPrefix(lower, "ft:") {
		return false
	}
	if isFutureStableOpenAIModel(lower) {
		return true
	}
	// Unknown, undated OpenAI IDs are retained until an explicit deprecation
	// rule exists. This prevents a catalog refresh from hiding a newly released
	// formal model merely because this binary has not learned its suffix yet.
	return !strings.HasSuffix(lower, "-latest")
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
	if !ok {
		return false
	}
	if strings.HasPrefix(suffix, "codex") && compareGPTVersion(version, []int{5, 5}) >= 0 {
		return true
	}
	if compareGPTVersion(version, []int{5, 6}) < 0 {
		return false
	}
	if suffix == "" {
		return true
	}
	switch suffix {
	case "mini", "nano", "pro", "sol", "terra", "luna", "codex", "codex-max", "codex-mini":
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

func dedupeModelIDs(models []string) []string {
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
	return result
}

func makeModelSet(models []string) map[string]struct{} {
	set := make(map[string]struct{}, len(models))
	for _, model := range models {
		set[strings.ToLower(normalizeListedModelID(model))] = struct{}{}
	}
	return set
}
