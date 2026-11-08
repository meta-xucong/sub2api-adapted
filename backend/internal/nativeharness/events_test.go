package nativeharness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func eventPageJSON(events string, next int, terminal any) string {
	term, _ := json.Marshal(terminal)
	return fmt.Sprintf(`{"schema_version":"native-events-v1","remote_task_id":"task-1","events":%s,"next_cursor":%d,"has_more":false,"terminal_seq":%s}`, events, next, term)
}
func TestLiveEventsReadOnlyCursorAndReplacement(t *testing.T) {
	flags := []string{}
	cursors := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/native-harness/events" {
			t.Error(r.URL.Path)
		}
		flags = append(flags, r.Header.Get("X-Native-Admission-Allowed"))
		cursors = append(cursors, r.Header.Get("X-Native-Event-Cursor"))
		if len(flags) == 1 {
			_, _ = fmt.Fprint(w, eventPageJSON(`[{"seq":1,"type":"text_snapshot","data":{"text":"draft long"}}]`, 1, nil))
		} else {
			_, _ = fmt.Fprint(w, eventPageJSON(`[{"seq":2,"type":"text_snapshot","data":{"text":"revised"}},{"seq":3,"type":"completion","data":`+completion+`}]`, 3, 3))
		}
	}))
	defer server.Close()
	s := newTestService(server.URL)
	s.Config.StandardWaitSeconds = 2
	texts := []string{}
	err := s.ObserveEvents(context.Background(), 7, 9, "one", true, []byte(request), 0, false, func(p *EventPage) error {
		for _, e := range p.Events {
			if e.Type == "text_snapshot" {
				text, ok := e.Data["text"].(string)
				if !ok {
					t.Fatal("text type")
				}
				texts = append(texts, text)
			}
		}
		return nil
	})
	if err != nil || strings.Join(flags, ",") != "true,false" || strings.Join(cursors, ",") != "0,1" || strings.Join(texts, ",") != "draft long,revised" {
		t.Fatalf("%v flags%v cursors%v texts%v", err, flags, cursors, texts)
	}
}
func TestLiveEventsResumeNeverAdmitsMissing(t *testing.T) {
	s := newTestService("http://127.0.0.1:1")
	s.Config.StandardWaitSeconds = 1
	err := s.ObserveEvents(context.Background(), 7, 9, "missing", true, []byte(request), 0, true, func(*EventPage) error { t.Fatal("emit"); return nil })
	requireCode(t, err, "NEW_ADMISSION_DISABLED")
}
func TestLiveEventsCancelStopsObservationNotTask(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = fmt.Fprint(w, eventPageJSON(`[]`, 0, nil))
	}))
	defer server.Close()
	s := newTestService(server.URL)
	s.Config.StandardWaitSeconds = 2
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := s.ObserveEvents(ctx, 7, 9, "one", true, []byte(request), 0, false, func(*EventPage) error { cancel(); return nil })
	requireCode(t, err, "NATIVE_OBSERVATION_TIMEOUT")
	if hits != 1 {
		t.Fatal(hits)
	}
}
func TestLiveEventValidationRejectsGapsAndFalseTerminal(t *testing.T) {
	valid := eventPageJSON(`[{"seq":1,"type":"text_snapshot","data":{"text":"abc"}}]`, 1, nil)
	cases := []string{
		strings.Replace(valid, `"seq":1`, `"seq":2`, 1),
		strings.Replace(valid, `"next_cursor":1`, `"next_cursor":2`, 1),
		strings.Replace(valid, `"terminal_seq":null`, `"terminal_seq":1`, 1),
		strings.Replace(valid, `"text_snapshot"`, `"tool_calls"`, 1),
		strings.Replace(valid, `{"text":"abc"}`, `{"text":"abc","private":"secret"}`, 1),
		eventPageJSON(`[{"seq":1,"type":"completion","data":`+completion+`}]`, 1, nil),
		eventPageJSON(`[]`, 0, 0),
	}
	for _, raw := range cases {
		m, err := decodeBounded([]byte(raw), MaxResponseBody)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = validateEventPage(m, 0, "synthetic-test"); err == nil {
			t.Fatal(raw)
		}
	}
	m, _ := decodeBounded([]byte(valid), MaxResponseBody)
	if _, err := validateEventPage(m, 0, "synthetic-test"); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"", "01", "-1", "1.0", "1\n", "9007199254740992"} {
		if _, err := ParseEventCursor(v); err == nil {
			t.Fatal(v)
		}
	}
}

func TestLiveNativeErrorStatusIsNotTerminal(t *testing.T) {
	raw := eventPageJSON(`[{"seq":1,"type":"native_status","data":{"state":"idle","stop_reason":"_codebuddy.ai/error"}}]`, 1, nil)
	value, err := decodeBounded([]byte(raw), MaxResponseBody)
	if err != nil {
		t.Fatal(err)
	}
	page, err := validateEventPage(value, 0, "synthetic-test")
	if err != nil || page.TerminalSeq != nil {
		t.Fatal(err)
	}
	value["terminal_seq"] = json.Number("2")
	if _, err = validateEventPage(value, 0, "synthetic-test"); err == nil {
		t.Fatal("missing has_more")
	}
}
