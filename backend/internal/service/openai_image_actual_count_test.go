//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Extends upstream #3215's actual-artifact rule through the Images wrappers.
// This deliberately preserves the existing response status/body and retry policy.
func TestImageActualCountDoesNotFallBackToRequestedN(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		count          int
	}{
		{"empty", `{"data":[],"usage":{"input_tokens":12,"output_tokens":34}}`, 0},
		{"empty item", `{"data":[{}]}`, 0},
		{"invalid types", `{"data":[{"url":12},{"b64_json":true},{"url":{"value":"not an image"}}]}`, 0},
		{"in-band error", `{"error":{"type":"server_error","message":"backend failed"},"usage":{"input_tokens":12,"output_tokens":34}}`, 0},
		{"malformed", `not JSON`, 0},
		{"partial", `{"data":[{}, {"url":"https://images.example/one.png"}]}`, 1},
		{"full", `{"data":[{"url":"https://images.example/one.png"},{"b64_json":"aGVsbG8="}]}`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-image-2","prompt":"draw","n":2,"response_format":"url"}`)
			c, recorder := newOpenAIImagesTestContext(t, body)
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.response))}}
			svc := newOpenAIImagesTestService(upstream)
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			result, err := svc.ForwardImages(context.Background(), c, newOpenAIImagesAPIKeyAccount(), body, parsed, "")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.True(t, result.ImageRequest)
			require.Equal(t, tc.count, result.ImageCount)
			if strings.Contains(tc.response, `"usage"`) {
				require.Equal(t, 12, result.Usage.InputTokens)
				require.Equal(t, 34, result.Usage.OutputTokens)
			}
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, tc.response, recorder.Body.String())
		})
	}
}

func TestImageActualCountRejectsNonStringArtifactsAcrossExtractors(t *testing.T) {
	for _, raw := range []string{`123`, `{}`, `true`, `null`, `""`, `"  "`, `[]`} {
		t.Run(raw, func(t *testing.T) {
			completed := []byte(`{"type":"response.completed","response":{"output":[{"type":"image_generation_call","result":` + raw + `}]}}`)
			results, _, _, _, err := extractOpenAIImagesFromResponsesCompleted(completed)
			require.NoError(t, err)
			require.Empty(t, results)
			require.Zero(t, countOpenAIResponseImageOutputsFromJSONBytes(completed))
			require.False(t, hasOpenAIResponsesImageResultJSON(completed))
			item := []byte(`{"type":"response.output_item.done","item":{"type":"image_generation_call","result":` + raw + `}}`)
			_, _, found, err := extractOpenAIImageFromResponsesOutputItemDone(item)
			require.NoError(t, err)
			require.False(t, found)
			require.False(t, hasOpenAIResponsesImageResultSSEData(item))
			results, err = parseCodexDirectImagesResponse([]byte(`{"data":[{"b64_json":` + raw + `}]}`))
			require.Error(t, err)
			require.Empty(t, results)
		})
	}
	valid := []byte(`{"type":"response.completed","response":{"output":[{"type":"image_generation_call","result":"aGVsbG8="}]}}`)
	results, _, _, _, err := extractOpenAIImagesFromResponsesCompleted(valid)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 1, countOpenAIResponseImageOutputsFromJSONBytes(valid))
	require.True(t, hasOpenAIResponsesImageResultJSON(valid))
}
