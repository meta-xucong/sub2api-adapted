//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHasCompactionTriggerInInput_DetectsCompactSignal(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.5",
		"stream":true,
		"input":[
			{"type":"message","role":"user","content":"hello"},
			{"type":"compaction_trigger"}
		]
	}`)
	require.True(t, HasCompactionTriggerInInput(body))
}

func TestHasCompactionTriggerInInput_NoTrigger(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.5",
		"input":[
			{"type":"message","role":"user","content":"hello"}
		]
	}`)
	require.False(t, HasCompactionTriggerInInput(body))
}

func TestHasCompactionTriggerInInput_EmptyInput(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[]}`)
	require.False(t, HasCompactionTriggerInInput(body))
}

func TestHasCompactionTriggerInInput_NoInputField(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5"}`)
	require.False(t, HasCompactionTriggerInInput(body))
}

func TestHasCompactionTriggerInInput_EmptyBody(t *testing.T) {
	require.False(t, HasCompactionTriggerInInput(nil))
	require.False(t, HasCompactionTriggerInInput([]byte{}))
}

func TestHasCompactionTriggerInInput_StringInput(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":"compaction_trigger"}`)
	require.False(t, HasCompactionTriggerInInput(body))
}

func TestHasCompactionTriggerInInput_CompactTriggerOnly(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[{"type":"compaction_trigger"}]}`)
	require.True(t, HasCompactionTriggerInInput(body))
}

func TestDetectOpenAICompactBodySignal_CodexPreSamplingCompaction(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.6-luna",
		"stream":true,
		"client_metadata":{
			"x-codex-turn-metadata":"{\"request_kind\":\"compaction\",\"thread_id\":\"redacted\"}"
		},
		"input":[
			{"type":"additional_tools","role":"developer","tools":[]},
			{"type":"message","role":"user","content":[]},
			{"type":"compaction","id":"cmp_test","encrypted_content":"opaque"}
		]
	}`)
	signal := DetectOpenAICompactBodySignal(body)
	require.Equal(t, OpenAICompactBodySignal{Detected: true, Kind: "codex.request_kind_compaction"}, signal)
}

func TestDetectOpenAICompactBodySignal_OrdinaryCodex56TurnIsUntouched(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.6-terra",
		"stream":true,
		"client_metadata":{
			"x-codex-turn-metadata":"{\"request_kind\":\"turn\"}"
		},
		"input":[
			{"type":"additional_tools","role":"developer","tools":[]},
			{"type":"message","role":"user","content":[]}
		]
	}`)
	require.Equal(t, OpenAICompactBodySignal{}, DetectOpenAICompactBodySignal(body))
}

func TestDetectOpenAICompactBodySignal_CompactionItemWithoutCodexMetadataIsUntouched(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.6-luna",
		"input":[{"type":"compaction","encrypted_content":"opaque"}]
	}`)
	require.Equal(t, OpenAICompactBodySignal{}, DetectOpenAICompactBodySignal(body))
}

func TestDetectOpenAICompactBodySignal_EmptyEncryptedContentIsUntouched(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.6-luna",
		"client_metadata":{"x-codex-turn-metadata":"{\"request_kind\":\"compaction\"}"},
		"input":[{"type":"compaction","encrypted_content":""}]
	}`)
	require.Equal(t, OpenAICompactBodySignal{}, DetectOpenAICompactBodySignal(body))
}
