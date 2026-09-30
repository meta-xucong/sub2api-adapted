package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeOpenAIResponsesJSONIdentity(t *testing.T) {
	patched, id, err := normalizeOpenAIResponsesJSONIdentity([]byte(`{"id":"chatcmpl_upstream","object":"response","output":[]}`))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(id, "resp_"))
	require.Contains(t, string(patched), `"id":"`+id+`"`)

	unchanged, existing, err := normalizeOpenAIResponsesJSONIdentity([]byte(`{"id":"resp_existing","output":[]}`))
	require.NoError(t, err)
	require.Equal(t, "resp_existing", existing)
	require.Equal(t, `{"id":"resp_existing","output":[]}`, string(unchanged))
}

func TestNormalizeOpenAIResponsesSSEIdentityKeepsOneID(t *testing.T) {
	first, id, err := normalizeOpenAIResponsesSSEIdentity([]byte(`{"type":"response.created","response":{"id":"provider-id"}}`), "")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(id, "resp_"))
	require.Contains(t, string(first), `"id":"`+id+`"`)

	second, same, err := normalizeOpenAIResponsesSSEIdentity([]byte(`{"type":"response.completed","response":{"id":"provider-id"}}`), id)
	require.NoError(t, err)
	require.Equal(t, id, same)
	require.Contains(t, string(second), `"id":"`+id+`"`)
}
