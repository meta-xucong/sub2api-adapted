package openai

import "strings"

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
