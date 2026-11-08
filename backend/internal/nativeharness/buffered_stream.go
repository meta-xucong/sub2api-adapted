package nativeharness

import "bytes"

// PrepareBufferedStream changes transport only, never the native prompt or task.
// Both encodings normalize to the same ledger digest. Unknown fields, duplicate
// keys and unsupported request semantics still fail through the strict parser.
func PrepareBufferedStream(raw []byte) ([]byte, bool, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, false, err
	}
	stream, requested := m["stream"]
	if !requested || stream == false {
		return raw, false, nil
	}
	if stream != true {
		return nil, false, fail("UNSUPPORTED_STREAM", 400)
	}
	m["stream"] = false
	normalized, err := normalize(canonical(m))
	if err != nil {
		return nil, false, err
	}
	return canonical(normalized), true, nil
}

// BufferedCompletionSSE emits a completed response, not live model tokens.
// It is called only after Invoke validates a terminal HTTP 200. No early success
// event, artificial token delta, tool call, usage or second execution is created.
func BufferedCompletionSSE(raw []byte) ([]byte, error) {
	m, err := decodeBounded(raw, MaxResponseBody)
	if err != nil {
		return nil, fail("NATIVE_INVALID_COMPLETION", 502)
	}
	alias, ok := m["model"].(string)
	if !ok || !validCompletion(m, alias) {
		return nil, fail("NATIVE_INVALID_COMPLETION", 502)
	}
	// The validator above proves these exact one-choice shapes.
	choices, _ := m["choices"].([]any)
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	chunk := func(delta map[string]any, reason any) []byte {
		return canonical(map[string]any{"id": m["id"], "object": "chat.completion.chunk", "created": m["created"], "model": alias,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": reason}}})
	}
	var out bytes.Buffer
	out.WriteString("data: ")
	out.Write(chunk(message, nil))
	out.WriteString("\n\ndata: ")
	out.Write(chunk(map[string]any{}, "stop"))
	out.WriteString("\n\ndata: [DONE]\n\n")
	return out.Bytes(), nil
}
