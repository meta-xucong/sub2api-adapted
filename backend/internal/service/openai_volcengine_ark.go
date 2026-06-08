package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

const openAIProviderVolcengineArk = "volcengine_ark"

func isVolcengineArkOpenAIAccount(account *Account) bool {
	if account == nil || !account.IsOpenAIApiKey() {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(account.GetExtraString("provider")), openAIProviderVolcengineArk)
}

func sanitizeVolcengineArkResponsesRequest(account *Account, req *apicompat.ResponsesRequest) bool {
	if !isVolcengineArkOpenAIAccount(account) || req == nil {
		return false
	}
	changed := false
	if req.Reasoning != nil && strings.TrimSpace(req.Reasoning.Summary) != "" {
		req.Reasoning.Summary = ""
		changed = true
	}
	if req.Text != nil {
		req.Text = nil
		changed = true
	}
	return changed
}
