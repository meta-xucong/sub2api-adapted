package nativeharness

import "strings"

const StandardStreamProtocol = "openai-append-v1"

// StreamProtocol validates explicit negotiation before any route freeze/admission.
func StreamProtocol(values []string, streaming bool) (string, error) {
	if len(values) == 0 {
		return StandardStreamProtocol, nil
	}
	if len(values) != 1 || (values[0] != StandardStreamProtocol && values[0] != EventsSchema) {
		return "", fail("INVALID_STREAM_PROTOCOL", 400)
	}
	if !streaming {
		return "", fail("STREAM_PROTOCOL_REQUIRES_STREAM", 400)
	}
	return values[0], nil
}

// StandardStream projects only append-compatible native snapshots. It cannot
// retract bytes already delivered to a standard client; rewrites are errors,
// never corrected by appending a second answer or declaring false success.
type StandardStream struct {
	task, model, text  string
	created            int64
	roleSent, terminal bool
}

func NewStandardStream(model string, created int64) *StandardStream {
	return &StandardStream{model: model, created: created}
}
func (s *StandardStream) chunk(delta map[string]any, finish any) map[string]any {
	return map[string]any{"id": "chatcmpl-" + s.task, "object": "chat.completion.chunk", "created": s.created, "model": s.model,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
}
func (s *StandardStream) appendText(text string) (map[string]any, error) {
	if !strings.HasPrefix(text, s.text) {
		return nil, fail("NATIVE_STREAM_REWRITE_REQUIRED", 409)
	}
	delta := map[string]any{}
	if !s.roleSent {
		delta["role"] = "assistant"
		s.roleSent = true
	}
	if len(text) > len(s.text) {
		delta["content"] = text[len(s.text):]
	}
	s.text = text
	if len(delta) == 0 {
		return nil, nil
	}
	return s.chunk(delta, nil), nil
}

// Project returns standard data objects; done is true only for verified success.
// Status events yield no assistant content. The caller may send an SSE comment.
func (s *StandardStream) Project(task string, event Event) (frames []map[string]any, done bool, err error) {
	if s.terminal || !identifier.MatchString(task) || (s.task != "" && s.task != task) {
		return nil, false, fail("NATIVE_EVENT_INTEGRITY_ERROR", 502)
	}
	s.task = task
	switch event.Type {
	case "text_snapshot":
		text, ok := event.Data["text"].(string)
		if !ok || len(text) > MaxResultText {
			return nil, false, fail("NATIVE_EVENT_INTEGRITY_ERROR", 502)
		}
		frame, err := s.appendText(text)
		if err != nil {
			return nil, false, err
		}
		if frame != nil {
			frames = append(frames, frame)
		}
	case "completion":
		if !validCompletion(event.Data, s.model) {
			return nil, false, fail("NATIVE_INVALID_COMPLETION", 502)
		}
		choices, _ := event.Data["choices"].([]any)
		choice, _ := choices[0].(map[string]any)
		message, _ := choice["message"].(map[string]any)
		content, _ := message["content"].(string)
		refusal, _ := message["refusal"].(string)
		// Snapshots have no text/refusal channel distinction. Never reinterpret
		// previously delivered text as a refusal or retract it at terminal time.
		if refusal != "" && s.text != "" && content == "" {
			return nil, false, fail("NATIVE_STREAM_REWRITE_REQUIRED", 409)
		}
		frame, err := s.appendText(content)
		if err != nil {
			return nil, false, err
		}
		if frame != nil {
			frames = append(frames, frame)
		}
		if refusal != "" {
			frames = append(frames, s.chunk(map[string]any{"refusal": refusal}, nil))
		}
		frames = append(frames, s.chunk(map[string]any{}, "stop"))
		s.terminal = true
		return frames, true, nil
	case "task_error":
		code, ok := event.Data["code"].(string)
		status, sok := eventInteger(event.Data["status"])
		if !ok || !identifier.MatchString(code) || !sok || status < 400 || status > 599 {
			return nil, false, fail("NATIVE_EVENT_INTEGRITY_ERROR", 502)
		}
		return nil, false, fail(code, int(status))
	case "native_status", "task_status":
	default:
		return nil, false, fail("NATIVE_EVENT_INTEGRITY_ERROR", 502)
	}
	return frames, false, nil
}
