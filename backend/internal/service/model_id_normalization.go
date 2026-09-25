package service

import (
	"sort"
	"strings"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// NormalizePublicModelIDs converts account mapping keys into the small, stable
// set that may be exposed to downstream model pickers. A safe suffix is
// removed when it has an exact base, or when it is the only candidate for that
// base in the current account set. Multiple suffix variants without a base
// are hidden instead of guessing which one is canonical.
//
// A bare parent with multiple descriptive children is ambiguous (for example
// gpt-5.6 next to gpt-5.6-luna/sol/terra) and is omitted instead of being
// guessed into one child. OpenAI-specific curated filtering is applied after
// this generic pass so the existing admin model policy also covers /v1/models.
func NormalizePublicModelIDs(platform string, modelIDs []string) []string {
	cleaned := uniqueNonWildcardModelIDs(modelIDs)
	if len(cleaned) == 0 {
		return nil
	}

	byLower := make(map[string]string, len(cleaned))
	for _, modelID := range cleaned {
		key := strings.ToLower(modelID)
		if _, exists := byLower[key]; !exists {
			byLower[key] = modelID
		}
	}

	baseCandidates := make(map[string][]string)
	for _, modelID := range cleaned {
		base, ok := safeModelBaseCandidate(modelID)
		if !ok {
			continue
		}
		baseCandidates[strings.ToLower(base)] = append(baseCandidates[strings.ToLower(base)], modelID)
	}

	canonical := make(map[string]struct{}, len(cleaned))
	for _, modelID := range cleaned {
		publicID := modelID
		if base, ok := safeModelBaseCandidate(modelID); ok {
			if exact, exists := byLower[strings.ToLower(base)]; exists {
				publicID = exact
			} else if candidates := baseCandidates[strings.ToLower(base)]; len(candidates) == 1 && isDateModelAlias(modelID) {
				// The only dated/preview/experimental candidate can safely be
				// addressed by its stable family name. Account-level routing
				// resolves that family name back to this mapping key.
				publicID = base
			} else {
				// Several aliases share a family but no explicit base exists;
				// exposing any one of them would make the picker depend on map
				// order and could route to the wrong upstream model.
				continue
			}
		}
		canonical[publicID] = struct{}{}
	}

	for modelID := range canonical {
		if hasAmbiguousDescriptiveChildren(modelID, cleaned) &&
			strings.HasPrefix(strings.ToLower(modelID), "gpt-") &&
			!openai.IsAdminSelectableModelID(modelID) {
			delete(canonical, modelID)
		}
	}

	result := make([]string, 0, len(canonical))
	for modelID := range canonical {
		result = append(result, modelID)
	}
	if platform == PlatformOpenAI {
		result = openai.FilterAdminSelectableModelIDs(result)
	}
	if len(result) == 0 {
		return nil
	}
	sort.Strings(result)
	return result
}

func uniqueNonWildcardModelIDs(modelIDs []string) []string {
	seen := make(map[string]struct{}, len(modelIDs))
	result := make([]string, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" || strings.Contains(modelID, "*") {
			continue
		}
		if _, exists := seen[strings.ToLower(modelID)]; exists {
			continue
		}
		seen[strings.ToLower(modelID)] = struct{}{}
		result = append(result, modelID)
	}
	return result
}

func safeModelBaseCandidate(modelID string) (string, bool) {
	modelID = strings.TrimSpace(modelID)
	separator := strings.LastIndexByte(modelID, '-')
	if separator <= 0 || separator == len(modelID)-1 {
		return "", false
	}
	suffix := strings.ToLower(modelID[separator+1:])
	if suffix == "preview" || suffix == "exp" || isModelDateSuffix(suffix) {
		return modelID[:separator], true
	}
	return "", false
}

func isModelDateSuffix(suffix string) bool {
	if len(suffix) != 4 && len(suffix) != 6 && len(suffix) != 8 {
		return false
	}
	for _, r := range suffix {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	if len(suffix) == 8 {
		return strings.HasPrefix(suffix, "19") || strings.HasPrefix(suffix, "20")
	}
	return true
}

func isDateModelAlias(modelID string) bool {
	separator := strings.LastIndexByte(strings.TrimSpace(modelID), '-')
	if separator <= 0 || separator == len(strings.TrimSpace(modelID))-1 {
		return false
	}
	return isModelDateSuffix(strings.TrimSpace(modelID)[separator+1:])
}

func hasAmbiguousDescriptiveChildren(parent string, modelIDs []string) bool {
	children := 0
	prefix := parent + "-"
	for _, modelID := range modelIDs {
		if modelID == parent || !strings.HasPrefix(modelID, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(modelID, prefix)
		if suffix == "" || safeVariantSuffix(suffix) {
			continue
		}
		children++
		if children >= 2 {
			return true
		}
	}
	return false
}

func safeVariantSuffix(suffix string) bool {
	suffix = strings.ToLower(strings.TrimSpace(suffix))
	if suffix == "preview" || suffix == "exp" || isModelDateSuffix(suffix) {
		return true
	}
	return false
}

// resolveSafeModelAlias maps a stable family name and a safe dated/preview
// alias to the same configured target. It only returns a route when every
// matching key agrees on one target; conflicting mappings remain unsupported.
func resolveSafeModelAlias(mapping map[string]string, requestedModel string) (string, bool) {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return "", false
	}

	targets := make(map[string]string)
	for modelID, target := range mapping {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" || strings.Contains(modelID, "*") {
			continue
		}
		if !safeModelFamilyMatch(modelID, requestedModel) {
			continue
		}
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		targets[target] = target
	}
	if len(targets) != 1 {
		return "", false
	}
	for target := range targets {
		return target, true
	}
	return "", false
}

func safeModelFamilyMatch(left, right string) bool {
	if strings.EqualFold(left, right) {
		return true
	}
	leftBase, leftHasBase := safeModelBaseCandidate(left)
	rightBase, rightHasBase := safeModelBaseCandidate(right)
	if leftHasBase && strings.EqualFold(leftBase, right) {
		return true
	}
	if rightHasBase && strings.EqualFold(rightBase, left) {
		return true
	}
	return leftHasBase && rightHasBase && strings.EqualFold(leftBase, rightBase)
}
