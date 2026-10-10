package service

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
)

// shouldRouteChatCompletionsViaResponses selects an upstream protocol for the
// explicit /v1/chat/completions ingress. Ordinary OpenAI API-key accounts stay
// on Chat Completions unless an administrator explicitly forces Responses.
// Account-native OAuth routes and explicitly configured provider adapters keep
// their existing behavior.
func shouldRouteChatCompletionsViaResponses(account *Account) bool {
	if account != nil && account.Platform == PlatformOpenAI && account.Type == AccountTypeAPIKey && !account.IsOpenCodeGo() {
		mode := openai_compat.NormalizeResponsesSupportMode(account.GetExtraString(openai_compat.ExtraKeyResponsesMode))
		return mode == openai_compat.ResponsesSupportModeForceResponses
	}
	return !shouldForwardOpenAIResponsesViaRawChatCompletions(account)
}

// shouldRouteResponsesViaChatCompletions selects an upstream protocol for the
// explicit /v1/responses ingress. Ordinary OpenAI API-key accounts stay on
// Responses unless an administrator explicitly forces Chat Completions.
// Legacy compact keeps its existing official eligibility/route semantics, and
// native Responses compaction v2 always remains on the official Responses path.
func shouldRouteResponsesViaChatCompletions(c *gin.Context, account *Account) bool {
	if c != nil && isOpenAINativeCompactionV2(c) {
		return false
	}
	if c != nil && isOpenAIResponsesCompactPath(c) {
		return shouldForwardOpenAIResponsesViaRawChatCompletions(account)
	}
	if account != nil && account.Platform == PlatformOpenAI && account.Type == AccountTypeAPIKey && !account.IsOpenCodeGo() {
		mode := openai_compat.NormalizeResponsesSupportMode(account.GetExtraString(openai_compat.ExtraKeyResponsesMode))
		return mode == openai_compat.ResponsesSupportModeForceChatCompletions
	}
	return shouldForwardOpenAIResponsesViaRawChatCompletions(account)
}
