package service

import (
	"strings"

	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
)

func smartRouterAccountHasEmbeddingMapping(account *Account) bool {
	if account == nil {
		return false
	}
	for pattern, mapped := range account.GetModelMapping() {
		if strings.Contains(strings.ToLower(strings.TrimSpace(pattern)), "embedding") ||
			strings.Contains(strings.ToLower(strings.TrimSpace(mapped)), "embedding") {
			return true
		}
	}
	return false
}

// smartRouterAccountHasChatGPTModel is the provider-independent admission
// check for Smart Router. Account groups and account labels are deployment
// metadata, so they are deliberately ignored here. For mapped API-key lanes,
// the upstream target model is the evidence; an unmapped OpenAI OAuth account
// is trusted as native ChatGPT because its model capability is defined by the
// OAuth provider rather than a user-entered alias.
func smartRouterAccountHasChatGPTModel(account *Account) bool {
	if account == nil || !account.IsOpenAI() {
		return false
	}

	if mapping := account.GetModelMapping(); len(mapping) > 0 {
		for _, model := range mapping {
			if isChatGPTModelIdentifier(model) {
				return true
			}
		}
		return false
	}
	if mapping := account.GetCompactModelMapping(); len(mapping) > 0 {
		for _, model := range mapping {
			if isChatGPTModelIdentifier(model) {
				return true
			}
		}
		return false
	}

	if account.IsOpenAIOAuth() {
		return true
	}
	// An unmapped API key is trusted only when it uses the native OpenAI
	// endpoint. Non-OpenAI endpoints must expose an explicit GPT-family target
	// through model_mapping before they enter Smart Router.
	baseURL := strings.ToLower(strings.TrimRight(strings.TrimSpace(account.GetOpenAIBaseURL()), "/"))
	return baseURL == "https://api.openai.com" || baseURL == "https://api.openai.com/v1"
}

func isChatGPTModelIdentifier(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	model = strings.TrimPrefix(model, "models/")
	if model == "" {
		return false
	}
	for _, prefix := range []string{"gpt-", "chatgpt-", "codex-", "o1", "o3", "o4"} {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}

// smartRouterAccountEligibleForSmartRouter admits the same ChatGPT-family
// lanes as before and additionally admits a genuine embeddings lane when the
// request itself is an embeddings request. This keeps Claude/Kimi/Volcengine
// lanes out while allowing the exact-model health rule to cover every
// OpenAI-compatible capability that the endpoint exposes.
func smartRouterAccountEligibleForSmartRouter(account *Account, capability smartrouter.Capability, requestedModel string, requiredCapability OpenAIEndpointCapability) bool {
	if capability == smartrouter.CapabilityResponsesCompact {
		if account == nil || openAICompactSupportTier(account) == 0 {
			return false
		}
		// Compact support is an endpoint capability, but a declared exact
		// compact mapping still has to match the requested model. A historical
		// false probe is intentionally not consulted here.
		mapping := account.GetCompactModelMapping()
		if len(mapping) == 0 {
			return true
		}
		for pattern := range mapping {
			if smartRouterModelPatternMatches(pattern, requestedModel) {
				return true
			}
		}
		return false
	}
	if smartRouterAccountHasChatGPTModel(account) {
		return true
	}
	if account == nil || (capability != smartrouter.CapabilityEmbedding && requiredCapability != OpenAIEndpointCapabilityEmbeddings) {
		return false
	}
	if !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityEmbeddings) || !smartRouterAccountHasEmbeddingMapping(account) {
		return false
	}
	requestedModel = strings.ToLower(strings.TrimSpace(requestedModel))
	if requestedModel == "" {
		return true
	}
	for pattern := range account.GetModelMapping() {
		if smartRouterModelPatternMatches(pattern, requestedModel) {
			return true
		}
	}
	return false
}
