package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func TestSanitizeVolcengineArkResponsesRequest(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			"provider": "volcengine_ark",
		},
	}
	req := &apicompat.ResponsesRequest{
		Reasoning: &apicompat.ResponsesReasoning{
			Effort:  "medium",
			Summary: "auto",
		},
		Text: &apicompat.ResponsesText{Verbosity: "medium"},
	}

	changed := sanitizeVolcengineArkResponsesRequest(account, req)

	require.True(t, changed)
	require.Equal(t, "medium", req.Reasoning.Effort)
	require.Empty(t, req.Reasoning.Summary)
	require.Nil(t, req.Text)
}

func TestSanitizeVolcengineArkResponsesRequestLeavesOtherProvidersUntouched(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			"provider": "other",
		},
	}
	req := &apicompat.ResponsesRequest{
		Reasoning: &apicompat.ResponsesReasoning{
			Effort:  "medium",
			Summary: "auto",
		},
		Text: &apicompat.ResponsesText{Verbosity: "medium"},
	}

	changed := sanitizeVolcengineArkResponsesRequest(account, req)

	require.False(t, changed)
	require.Equal(t, "auto", req.Reasoning.Summary)
	require.NotNil(t, req.Text)
}
