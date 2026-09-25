package service

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func collectNormalizedResponseEvents(t *testing.T, body []byte) ([]string, []int) {
	t.Helper()
	var types []string
	var sequences []int
	for _, line := range strings.Split(string(body), "\n") {
		data, ok := extractOpenAISSEDataLine(line)
		if !ok || strings.TrimSpace(data) == "" || strings.TrimSpace(data) == "[DONE]" {
			continue
		}
		types = append(types, gjson.Get(data, "type").String())
		if gjson.Get(data, "sequence_number").Exists() {
			sequences = append(sequences, int(gjson.Get(data, "sequence_number").Int()))
		}
	}
	return types, sequences
}

func TestOpenAIResponsesLifecycleNormalizerInsertsMissingInProgressAndShiftsSequence(t *testing.T) {
	input := strings.Join([]string{
		`event: response.created`,
		`data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_missing_progress","status":"in_progress"}}`,
		"",
		`event: response.output_item.added`,
		`data: {"type":"response.output_item.added","sequence_number":1,"output_index":0}`,
		"",
		`event: response.completed`,
		`data: {"type":"response.completed","sequence_number":2,"response":{"id":"resp_missing_progress","status":"completed","output":[]}}`,
		"",
	}, "\n")

	normalized := newOpenAIResponsesLifecycleNormalizer(io.NopCloser(strings.NewReader(input)))
	body, err := io.ReadAll(normalized)
	require.NoError(t, err)
	types, sequences := collectNormalizedResponseEvents(t, body)
	require.Equal(t, []string{"response.created", "response.in_progress", "response.output_item.added", "response.completed"}, types)
	require.Equal(t, []int{0, 1, 2, 3}, sequences)
	require.Contains(t, string(body), `"type":"response.in_progress"`)
}

func TestOpenAIResponsesLifecycleNormalizerPreservesExistingLifecycle(t *testing.T) {
	input := strings.Join([]string{
		`event: response.created`,
		`data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_existing_progress","status":"in_progress"}}`,
		"",
		`event: response.in_progress`,
		`data: {"type":"response.in_progress","sequence_number":1,"response":{"id":"resp_existing_progress","status":"in_progress"}}`,
		"",
		`event: response.completed`,
		`data: {"type":"response.completed","sequence_number":2,"response":{"id":"resp_existing_progress","status":"completed","output":[]}}`,
		"",
	}, "\n")

	normalized := newOpenAIResponsesLifecycleNormalizer(io.NopCloser(strings.NewReader(input)))
	body, err := io.ReadAll(normalized)
	require.NoError(t, err)
	require.Equal(t, input, string(body))
}

func TestOpenAIResponsesLifecycleNormalizerPassesNonResponsesSSEThrough(t *testing.T) {
	input := "data: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\"}\n\n"
	normalized := newOpenAIResponsesLifecycleNormalizer(io.NopCloser(strings.NewReader(input)))
	body, err := io.ReadAll(normalized)
	require.NoError(t, err)
	require.Equal(t, input, string(body))
}
