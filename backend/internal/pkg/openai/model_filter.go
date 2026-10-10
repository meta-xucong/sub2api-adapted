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

// legacyAdminSelectableOpenAIModels preserves the source patch's explicit
// choices. Current official catalog entries are also eligible unless the
// frozen source explicitly classifies them as hidden or deprecated.
var legacyAdminSelectableOpenAIModels = map[string]struct{}{
	"gpt-5.4-mini":  {},
	"gpt-5.4":       {},
	"gpt-5.5":       {},
	"gpt-5.6-sol":   {},
	"gpt-5.6-terra": {},
	"gpt-5.6-luna":  {},
	"gpt-image-2":   {},
}

var adminHiddenOpenAIModels = map[string]struct{}{
	"codex-auto-review":   {},
	"gpt-5.3-codex-spark": {},
	"gpt-5.4-nano":        {},
	"gpt-5.5-pro":         {},
	"gpt-5.6":             {},
	"codex-mini-latest":   {},
}

var adminDeprecatedOpenAIModels = map[string]struct{}{
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

// AdminSelectableModelIDs returns curated IDs intended for ordinary OpenAI
// admin model pickers. It is deliberately separate from DefaultModelIDs,
// which remains the gateway routing compatibility catalog.
func AdminSelectableModelIDs() []string {
	ids := make([]string, 0, len(DefaultModels))
	for _, model := range DefaultModels {
		if IsAdminSelectableModelID(model.ID) {
			ids = append(ids, model.ID)
		}
	}
	return ids
}

// AdminSelectableModels returns the curated model descriptors, retaining the
// target catalog's current display metadata for IDs that are present there.
func AdminSelectableModels() []Model {
	defaultsByID := make(map[string]Model, len(DefaultModels))
	for _, model := range DefaultModels {
		defaultsByID[model.ID] = model
	}

	modelIDs := AdminSelectableModelIDs()
	models := make([]Model, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		if model, ok := defaultsByID[modelID]; ok {
			models = append(models, model)
			continue
		}
		models = append(models, Model{
			ID:          modelID,
			Object:      "model",
			Type:        "model",
			OwnedBy:     "openai",
			DisplayName: modelID,
		})
	}
	return models
}

// IsAdminSelectableModelID reports whether a model should be shown in normal
// admin OpenAI selectors. Non-OpenAI aliases are preserved for custom providers.
func IsAdminSelectableModelID(model string) bool {
	model = normalizeListedModelID(model)
	if model == "" {
		return false
	}
	lower := strings.ToLower(model)
	if _, ok := adminHiddenOpenAIModels[lower]; ok {
		return false
	}
	if _, ok := adminDeprecatedOpenAIModels[lower]; ok {
		return false
	}
	for _, defaultModel := range DefaultModels {
		if strings.EqualFold(defaultModel.ID, lower) {
			return true
		}
	}
	if _, ok := legacyAdminSelectableOpenAIModels[lower]; ok {
		return true
	}
	if looksLikeOpenAIManagedModel(lower) {
		return false
	}
	return true
}

// FilterAdminSelectableModelIDs filters OpenAI-managed IDs while preserving
// custom provider aliases and input order.
func FilterAdminSelectableModelIDs(models []string) []string {
	filtered := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		if !IsAdminSelectableModelID(model) {
			continue
		}
		model = normalizeListedModelID(model)
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		filtered = append(filtered, model)
	}
	return filtered
}

// FilterAutoDiscoveredModelIDs returns IDs safe for automatic upstream catalog
// refresh. OpenAI snapshots/internal aliases are removed without applying
// OpenAI naming rules to custom provider IDs.
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
	if !looksLikeAutoDiscoveredOpenAIManagedModel(lower) {
		return true
	}
	if isOpenAIDatedSnapshot(lower) {
		return false
	}
	if isFutureStableOpenAIModel(lower) {
		return true
	}
	return !strings.HasSuffix(lower, "-latest")
}

// FilterAdminSelectableModels applies the same selector-only filter to a
// discovered account model list without changing the stored catalog.
func FilterAdminSelectableModels(models []Model) []Model {
	ids := make([]string, 0, len(models))
	byID := make(map[string]Model, len(models))
	for _, model := range models {
		id := normalizeListedModelID(model.ID)
		ids = append(ids, id)
		if _, exists := byID[id]; !exists {
			byID[id] = model
		}
	}

	filteredIDs := FilterAdminSelectableModelIDs(ids)
	filtered := make([]Model, 0, len(filteredIDs))
	for _, id := range filteredIDs {
		model, ok := byID[id]
		if !ok {
			continue
		}
		model.ID = id
		filtered = append(filtered, model)
	}
	return filtered
}

func normalizeListedModelID(model string) string {
	return strings.TrimPrefix(strings.TrimSpace(model), "models/")
}

func looksLikeOpenAIManagedModel(model string) bool {
	return looksLikeNumberedGPTModel(model) ||
		strings.HasPrefix(model, "chatgpt-") ||
		strings.HasPrefix(model, "codex-") ||
		strings.HasPrefix(model, "computer-use") ||
		looksLikeOFamilyModel(model)
}

func looksLikeAutoDiscoveredOpenAIManagedModel(model string) bool {
	return strings.HasPrefix(model, "gpt-") ||
		strings.HasPrefix(model, "chatgpt-") ||
		strings.HasPrefix(model, "codex-") ||
		strings.HasPrefix(model, "computer-use") ||
		looksLikeOFamilyModel(model)
}

func looksLikeNumberedGPTModel(model string) bool {
	if !strings.HasPrefix(model, "gpt-") {
		return false
	}
	name := strings.TrimPrefix(model, "gpt-")
	if strings.HasPrefix(name, "image-") {
		return true
	}
	return len(name) > 0 && name[0] >= '0' && name[0] <= '9'
}

func looksLikeOFamilyModel(model string) bool {
	return len(model) >= 2 && model[0] == 'o' && model[1] >= '0' && model[1] <= '9'
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
