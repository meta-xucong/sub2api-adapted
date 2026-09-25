package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestT0CompactionBridgeRejectsFalseSuccess(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"failed"}}`,
		`{"id":"resp_x","status":"failed","output":[{"type":"compaction","encrypted_content":"x"}]}`,
		`{"id":"resp_x","status":"incomplete","output":[{"type":"compaction","encrypted_content":"x"}]}`,
		`{"id":"resp_x","output":[{"type":"message","content":[]}]}`,
		`{"id":"resp_x","output":[{"type":"compaction","encrypted_content":""}]}`,
	} {
		payload, ok := buildOpenAICompactSSEPayload([]byte(body))
		require.False(t, ok)
		require.Empty(t, payload)
		c, rec := newCompactBridgeTestContext(t, true)
		require.True(t, writeOpenAICompactSSEBridge(c, http.StatusOK, []byte(body)))
		require.Equal(t, 502, rec.Code)
		require.Equal(t, "invalid_compaction_response", gjson.Get(rec.Body.String(), "error.code").String())
		require.NotContains(t, rec.Body.String(), "response.completed")
	}
}
