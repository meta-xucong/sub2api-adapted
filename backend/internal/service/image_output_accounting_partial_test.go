package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

func completeAccountingPNG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	decoded, err := png.Decode(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	require.Equal(t, image.Rect(0, 0, 2, 2), decoded.Bounds())
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func accountingJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	require.NoError(t, err)
	return body
}

func TestImageOutputAccountingPartialTextIsNotPartialIdentity(t *testing.T) {
	png := completeAccountingPNG(t)
	for _, artifact := range []struct{ field, value string }{
		{"result", png},
		{"b64_json", png},
		{"url", "https://images.example/partial_image/final.png"},
	} {
		t.Run(artifact.field, func(t *testing.T) {
			item := map[string]any{"id": "image_partial_image_name", "type": "image_generation_call", "status": "completed", "revised_prompt": "include the words partial_image and PARTIAL_IMAGE", "size": "1024x1024", artifact.field: artifact.value}
			body := accountingJSON(t, map[string]any{"output": []any{item}})
			counter := newOpenAIImageOutputCounter()
			counter.AddJSONResponse(body)
			require.Equal(t, 1, counter.Count())
			require.Equal(t, []string{"1024x1024"}, counter.Sizes())
			require.True(t, counter.HasResponsesImageResult())
			sseCounter := newOpenAIImageOutputCounter()
			sseCounter.AddSSEData(accountingJSON(t, map[string]any{"type": "response.output_item.done", "item": item}))
			sseCounter.AddSSEData(accountingJSON(t, map[string]any{"type": "response.completed", "response": map[string]any{"output": []any{item}}}))
			require.Equal(t, 1, sseCounter.Count())
			require.Equal(t, counter.Sizes(), sseCounter.Sizes())
			require.True(t, sseCounter.HasResponsesImageResult())
		})
	}
}

func TestImageOutputAccountingStructuredPartialAndDedup(t *testing.T) {
	png := completeAccountingPNG(t)
	counter := newOpenAIImageOutputCounter()
	for _, typ := range []string{"response.image_generation_call.partial_image", "image_generation.partial_image", "image_edit.partial_image"} {
		counter.AddSSEData(accountingJSON(t, map[string]any{"type": typ, "id": "image_1", "b64_json": png, "data": []any{map[string]any{"b64_json": png}}}))
		require.Zero(t, counter.Count(), typ)
	}
	for _, status := range []string{"partial_image", "partial", "in_progress", "generating"} {
		counter.AddSSEData(accountingJSON(t, map[string]any{"type": "response.output_item.done", "item": map[string]any{"id": "image_1", "type": "image_generation_call", "status": status, "result": png}}))
		require.Zero(t, counter.Count(), status)
	}
	first := map[string]any{"id": "image_1", "type": "image_generation_call", "status": "completed", "result": png, "revised_prompt": "partial_image", "size": "1024x1024"}
	second := map[string]any{"id": "image_2", "type": "image_generation_call", "status": "completed", "result": png, "size": "2048x1152"}
	counter.AddSSEData(accountingJSON(t, map[string]any{"type": "response.output_item.done", "item": first}))
	require.Equal(t, 1, counter.Count())
	counter.AddSSEData(accountingJSON(t, map[string]any{"type": "response.completed", "response": map[string]any{"output": []any{first, second}}}))
	counter.AddJSONResponse(accountingJSON(t, map[string]any{"output": []any{first, second}}))
	require.Equal(t, 2, counter.Count(), "distinct completed IDs count even when PNG bytes match")
	require.Equal(t, []string{"1024x1024", "2048x1152"}, counter.Sizes())
	for _, invalid := range []any{nil, "", " ", 123, true, []any{}, map[string]any{"data": png}} {
		item := map[string]any{"id": "invalid", "type": "image_generation_call", "status": "completed", "result": invalid}
		counter.AddJSONResponse(accountingJSON(t, map[string]any{"output": []any{item}}))
		require.Equal(t, 2, counter.Count())
	}
}
