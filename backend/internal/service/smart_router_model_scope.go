package service

import "strings"

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
