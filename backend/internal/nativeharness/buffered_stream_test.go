package nativeharness

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestBufferedStreamCanonicalSameOperation(t *testing.T) {
	raw := []byte(`{"model":"synthetic-test","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	prepared, stream, err := PrepareBufferedStream(raw)
	if err != nil || !stream {
		t.Fatal(err)
	}
	expected, err := normalize([]byte(`{"model":"synthetic-test","messages":[{"role":"user","content":"hello"}],"stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(prepared, canonical(expected)) {
		t.Fatal("transport changes ledger digest")
	}
	for _, bad := range []string{`{"stream":1}`, `{"stream":true,"stream":false}`, `{"model":"synthetic-test","messages":[{"role":"user","content":"hi"}],"stream":true,"tools":[]}`, `{"model":"synthetic-test","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{}}`} {
		if _, _, err := PrepareBufferedStream([]byte(bad)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestBufferedStreamTerminalFramesNoInventedUsage(t *testing.T) {
	for _, message := range []map[string]any{{"role": "assistant", "content": "a\n\ndata: [DONE]\n\x00中"}, {"role": "assistant", "content": nil, "refusal": "not available"}, {"role": "assistant", "content": strings.Repeat("\x01", MaxResultText)}, {"role": "assistant", "content": strings.Repeat("中", MaxResultText/3)}} {
		raw := canonical(map[string]any{"id": "chatcmpl-test", "object": "chat.completion", "created": 1, "model": "synthetic-test", "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": "stop"}}})
		frames, err := BufferedCompletionSSE(raw)
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(string(frames), "\n\n")
		if len(parts) != 4 || parts[2] != "data: [DONE]" || parts[3] != "" {
			t.Fatal("SSE injection or framing", parts)
		}
		for i := 0; i < 2; i++ {
			var chunk map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(parts[i], "data: ")), &chunk); err != nil {
				t.Fatal(err)
			}
			if chunk["object"] != "chat.completion.chunk" || chunk["usage"] != nil || chunk["id"] != "chatcmpl-test" || chunk["model"] != "synthetic-test" || chunk["created"] != float64(1) {
				t.Fatal("identity/usage changed")
			}
			choices, ok := chunk["choices"].([]any)
			if !ok || len(choices) != 1 {
				t.Fatal("choices changed")
			}
			choice, ok := choices[0].(map[string]any)
			if !ok || choice["index"] != float64(0) {
				t.Fatal("index changed")
			}
			if i == 0 {
				if choice["finish_reason"] != nil || !reflect.DeepEqual(choice["delta"], message) {
					t.Fatal("content/refusal truncated or changed")
				}
			} else if choice["finish_reason"] != "stop" || !reflect.DeepEqual(choice["delta"], map[string]any{}) {
				t.Fatal("terminal changed")
			}
		}
	}
	for _, raw := range []string{`{}`, `{"error":{"code":"PENDING"}}`, `{"id":"chatcmpl-test","object":"chat.completion","created":1,"model":"synthetic-test","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[]},"finish_reason":"stop"}]}`} {
		if _, err := BufferedCompletionSSE([]byte(raw)); err == nil {
			t.Fatal("nonterminal accepted")
		}
	}
}
