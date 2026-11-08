package nativeharness

import (
	"encoding/json"
	"strings"
	"testing"
)

func standardEvent(typ string, data map[string]any) Event {
	return Event{Seq: 1, Type: typ, Data: data}
}
func completionEvent(t *testing.T, text string) Event {
	t.Helper()
	value, err := decodeBounded([]byte(completion), MaxResponseBody)
	if err != nil {
		t.Fatal(err)
	}
	choices, _ := value["choices"].([]any)
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	message["content"] = text
	return standardEvent("completion", value)
}
func TestStandardStreamAppendOnlyAndVerifiedFinal(t *testing.T) {
	s := NewStandardStream("synthetic-test", 123)
	content := ""
	for _, text := range []string{"中", "中文", "中文", "中文\n\ndata: [DONE]"} {
		frames, done, err := s.Project("task-1", standardEvent("text_snapshot", map[string]any{"text": text}))
		if err != nil || done {
			t.Fatal(err)
		}
		for _, f := range frames {
			if f["created"] != int64(123) || f["id"] != "chatcmpl-task-1" || f["object"] != "chat.completion.chunk" || f["usage"] != nil {
				t.Fatal(f)
			}
			choices, _ := f["choices"].([]any)
			choice, _ := choices[0].(map[string]any)
			delta, _ := choice["delta"].(map[string]any)
			piece, _ := delta["content"].(string)
			content += piece
		}
	}
	final := content + " tail"
	frames, done, err := s.Project("task-1", completionEvent(t, final))
	if err != nil || !done || len(frames) != 2 {
		t.Fatal(frames, done, err)
	}
	for _, f := range frames {
		choices, _ := f["choices"].([]any)
		choice, _ := choices[0].(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		piece, _ := delta["content"].(string)
		content += piece
	}
	if content != final {
		t.Fatal(content)
	}
}
func TestStandardStreamRewriteNeverSuccess(t *testing.T) {
	for _, final := range []bool{false, true} {
		for _, replacement := range []string{"", "short", "different text"} {
			s := NewStandardStream("synthetic-test", 1)
			_, _, err := s.Project("task-1", standardEvent("text_snapshot", map[string]any{"text": "original long text"}))
			if err != nil {
				t.Fatal(err)
			}
			event := standardEvent("text_snapshot", map[string]any{"text": replacement})
			if final {
				event = completionEvent(t, replacement)
				if replacement == "" {
					continue
				}
			}
			frames, done, err := s.Project("task-1", event)
			requireCode(t, err, "NATIVE_STREAM_REWRITE_REQUIRED")
			if done || len(frames) != 0 {
				t.Fatal("false terminal")
			}
		}
	}
}
func TestStandardStreamStatusRefusalAndFailure(t *testing.T) {
	s := NewStandardStream("synthetic-test", 1)
	for _, typ := range []string{"native_status", "task_status"} {
		frames, done, err := s.Project("task-1", standardEvent(typ, map[string]any{"state": "idle"}))
		if err != nil || done || len(frames) != 0 {
			t.Fatal("status became answer")
		}
	}
	event := completionEvent(t, "")
	choices, _ := event.Data["choices"].([]any)
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	message["content"] = nil
	message["refusal"] = "cannot"
	frames, done, err := s.Project("task-1", event)
	if err != nil || !done || !strings.Contains(string(canonical(frames)), `"refusal":"cannot"`) {
		t.Fatal(err)
	}
	s = NewStandardStream("synthetic-test", 1)
	_, _, _ = s.Project("task-1", standardEvent("text_snapshot", map[string]any{"text": "cannot"}))
	_, done, err = s.Project("task-1", event)
	requireCode(t, err, "NATIVE_STREAM_REWRITE_REQUIRED")
	if done {
		t.Fatal("refusal reinterpreted text")
	}
	s = NewStandardStream("synthetic-test", 1)
	_, done, err = s.Project("task-1", standardEvent("task_error", map[string]any{"code": "TASK_CANCELLED", "status": json.Number("409")}))
	requireCode(t, err, "TASK_CANCELLED")
	if done {
		t.Fatal("failure became success")
	}
}

func TestStandardStreamMaximumEscapingAndNegotiation(t *testing.T) {
	s := NewStandardStream("synthetic-test", 1)
	text := strings.Repeat("\x01", MaxResultText)
	frames, done, err := s.Project("task-1", standardEvent("text_snapshot", map[string]any{"text": text}))
	if err != nil || done || len(frames) != 1 || !json.Valid(canonical(frames[0])) {
		t.Fatal(err)
	}
	_, done, err = s.Project("task-1", completionEvent(t, text))
	if err != nil || !done {
		t.Fatal(err)
	}
	for _, values := range [][]string{{"unknown"}, {EventsSchema, StandardStreamProtocol}, {EventsSchema}, {StandardStreamProtocol}} {
		if _, err := StreamProtocol(values, false); err == nil {
			t.Fatal(values)
		}
	}
}
