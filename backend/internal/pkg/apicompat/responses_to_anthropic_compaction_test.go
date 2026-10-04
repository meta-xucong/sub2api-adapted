package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesToAnthropicRequest_ReplaysPortableCompactionSummary(t *testing.T) {
	tests := []struct {
		name     string
		itemType string
	}{
		{name: "compaction", itemType: "compaction"},
		{name: "compaction_summary alias", itemType: "compaction_summary"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, err := json.Marshal([]map[string]any{{
				"type":              tt.itemType,
				"encrypted_content": "provider-opaque-state",
				"summary": []map[string]string{{
					"type": "summary_text",
					"text": "保留 Claude 会话摘要",
				}},
			}})
			require.NoError(t, err)

			out, err := ResponsesToAnthropicRequest(&ResponsesRequest{
				Model: "claude-sonnet-4-6",
				Input: input,
			})
			require.NoError(t, err)
			require.Len(t, out.Messages, 1)
			require.Equal(t, "user", out.Messages[0].Role)
			var blocks []AnthropicContentBlock
			require.NoError(t, json.Unmarshal(out.Messages[0].Content, &blocks))
			require.Len(t, blocks, 1)
			require.Equal(t, "text", blocks[0].Type)
			require.Contains(t, blocks[0].Text, "<conversation_summary>")
			require.Contains(t, blocks[0].Text, "保留 Claude 会话摘要")
			require.NotContains(t, string(out.Messages[0].Content), "provider-opaque-state")
		})
	}
}

func TestResponsesToAnthropicRequest_RejectsOpaqueOnlyCompaction(t *testing.T) {
	_, err := ResponsesToAnthropicRequest(&ResponsesRequest{
		Model: "claude-sonnet-4-6",
		Input: json.RawMessage(`[{"type":"compaction","encrypted_content":"provider-opaque-state"}]`),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no portable summary")
}
