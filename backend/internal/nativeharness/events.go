package nativeharness

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"
)

const EventsSchema = "native-events-v1"
const MaxEventSequence int64 = 9007199254740991

// Event data is a native snapshot/status, NOT an OpenAI append-only delta.
// Seq is the durable Bridge cursor, never a locally invented event identifier.
type Event struct {
	Seq  int64          `json:"seq"`
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}
type EventPage struct {
	TaskID      string
	Events      []Event
	Cursor      int64
	HasMore     bool
	TerminalSeq *int64
}

func ParseEventCursor(value string) (int64, error) {
	if value == "" {
		return 0, fail("INVALID_EVENT_CURSOR", 400)
	}
	if len(value) > 1 && value[0] == '0' {
		return 0, fail("INVALID_EVENT_CURSOR", 400)
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return 0, fail("INVALID_EVENT_CURSOR", 400)
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n > MaxEventSequence {
		return 0, fail("INVALID_EVENT_CURSOR", 400)
	}
	return n, nil
}
func eventInteger(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := ParseEventCursor(string(n))
	return i, err == nil
}

// ObserveEvents invokes admission at most once, then only observes the same
// frozen operation. Delivery failure and deadline never cancel/restart work.
// emit receives validated pages; it must flush each event without accumulating
// an unbounded client buffer. A resumed stream can never create missing work.
func (s *Service) ObserveEvents(ctx context.Context, userID, keyID int64, key string, canExecute bool, raw []byte, cursor int64, resume bool, emit func(*EventPage) error) error {
	seconds := s.Config.StandardWaitSeconds
	if seconds <= 0 || seconds > 300 {
		return fail("NATIVE_WAIT_CONFIG_INVALID", 503)
	}
	if cursor < 0 || cursor > MaxEventSequence {
		return fail("INVALID_EVENT_CURSOR", 400)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	taskID := ""
	admission := canExecute && !resume
	for {
		if ctx.Err() != nil {
			return fail("NATIVE_OBSERVATION_TIMEOUT", 504)
		}
		page, err := s.eventPage(ctx, userID, keyID, key, admission, raw, cursor)
		admission = false
		if err != nil {
			return err
		}
		if taskID != "" && taskID != page.TaskID {
			return fail("NATIVE_EVENT_INTEGRITY_ERROR", 502)
		}
		taskID = page.TaskID
		if err = emit(page); err != nil {
			return err
		}
		cursor = page.Cursor
		if page.TerminalSeq != nil && cursor >= *page.TerminalSeq {
			return nil
		}
		if page.HasMore {
			continue
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fail("NATIVE_OBSERVATION_TIMEOUT", 504)
		case <-timer.C:
		}
	}
}

func (s *Service) eventPage(ctx context.Context, userID, keyID int64, key string, canExecute bool, raw []byte, cursor int64) (*EventPage, error) {
	r, op, err := s.prepareRequest(ctx, userID, keyID, key, canExecute, raw, "/v1/native-harness/events")
	if err != nil {
		return nil, err
	}
	r.Header.Set("X-Native-Event-Cursor", strconv.FormatInt(cursor, 10))
	response, err := s.client.Do(r)
	if err != nil {
		return nil, fail("NATIVE_RECEIPT_UNKNOWN", 502)
	}
	defer func() { _ = response.Body.Close() }()
	if len(response.Header.Values("Content-Length")) != 1 || response.ContentLength < 0 || response.ContentLength > MaxResponseBody || len(response.TransferEncoding) != 0 {
		return nil, fail("NATIVE_EVENT_INTEGRITY_ERROR", 502)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBody+1))
	if err != nil || int64(len(body)) != response.ContentLength {
		return nil, fail("NATIVE_RECEIPT_UNKNOWN", 502)
	}
	value, err := decodeBounded(body, MaxResponseBody)
	if err != nil {
		return nil, fail("NATIVE_EVENT_INTEGRITY_ERROR", 502)
	}
	if response.StatusCode != http.StatusOK {
		code := "NATIVE_UPSTREAM_ERROR"
		if e, ok := value["error"].(map[string]any); ok {
			if c, ok := e["code"].(string); ok && identifier.MatchString(c) {
				code = c
			}
		}
		status := response.StatusCode
		if status < 400 || status > 599 {
			status = 502
		}
		return nil, fail(code, status)
	}
	return validateEventPage(value, cursor, op.Route.ModelAlias)
}

func validateEventPage(v map[string]any, cursor int64, alias string) (*EventPage, error) {
	bad := func() (*EventPage, error) { return nil, fail("NATIVE_EVENT_INTEGRITY_ERROR", 502) }
	if !exactFields(v, []string{"schema_version", "remote_task_id", "events", "next_cursor", "has_more", "terminal_seq"}) || v["schema_version"] != EventsSchema {
		return bad()
	}
	task, ok := v["remote_task_id"].(string)
	if !ok || !identifier.MatchString(task) {
		return bad()
	}
	next, ok := eventInteger(v["next_cursor"])
	if !ok || next < cursor {
		return bad()
	}
	more, ok := v["has_more"].(bool)
	if !ok {
		return bad()
	}
	page := &EventPage{TaskID: task, Cursor: next, HasMore: more}
	if v["terminal_seq"] != nil {
		n, ok := eventInteger(v["terminal_seq"])
		if !ok || n == 0 || n < next {
			return bad()
		}
		page.TerminalSeq = &n
	}
	list, ok := v["events"].([]any)
	if !ok || len(list) > 8 {
		return bad()
	}
	seen := cursor
	for _, item := range list {
		e, ok := item.(map[string]any)
		if !ok || !exactFields(e, []string{"seq", "type", "data"}) {
			return bad()
		}
		seq, ok := eventInteger(e["seq"])
		if !ok || seq != seen+1 {
			return bad()
		}
		typ, ok := e["type"].(string)
		if !ok {
			return bad()
		}
		data, ok := e["data"].(map[string]any)
		if !ok || !validEventData(typ, data, alias) {
			return bad()
		}
		terminal := typ == "completion" || typ == "task_error"
		if terminal && (page.TerminalSeq == nil || seq != *page.TerminalSeq) {
			return bad()
		}
		if page.TerminalSeq != nil && seq == *page.TerminalSeq && !terminal {
			return bad()
		}
		page.Events = append(page.Events, Event{seq, typ, data})
		seen = seq
	}
	if seen != next || (more && len(list) == 0) || (page.TerminalSeq != nil && next == *page.TerminalSeq && more) || (page.TerminalSeq != nil && next < *page.TerminalSeq && !more) {
		return bad()
	}
	return page, nil
}
func validEventData(typ string, d map[string]any, alias string) bool {
	switch typ {
	case "text_snapshot":
		if !exactFields(d, []string{"text"}) {
			return false
		}
		text, ok := d["text"].(string)
		return ok && len(text) <= MaxResultText
	case "native_status":
		if !exactFields(d, []string{"state"}, "stop_reason") {
			return false
		}
		state, ok := d["state"].(string)
		if !ok || (state != "running" && state != "requires_action" && state != "idle" && state != "unknown") {
			return false
		}
		if reason, exists := d["stop_reason"]; exists {
			s, ok := reason.(string)
			if !ok || (s != "end_turn" && s != "refusal" && s != "cancelled" && s != "_codebuddy.ai/error") {
				return false
			}
		}
		return true
	case "task_status":
		if !exactFields(d, []string{"outcome", "liveness"}, "code") {
			return false
		}
		for _, k := range []string{"outcome", "liveness"} {
			s, ok := d[k].(string)
			if !ok || !identifier.MatchString(s) {
				return false
			}
		}
		if c, exists := d["code"]; exists {
			s, ok := c.(string)
			if !ok || !identifier.MatchString(s) {
				return false
			}
		}
		return true
	case "completion":
		return validCompletion(d, alias)
	case "task_error":
		if !exactFields(d, []string{"code", "status"}) {
			return false
		}
		code, ok := d["code"].(string)
		status, sok := eventInteger(d["status"])
		return ok && identifier.MatchString(code) && sok && status >= 400 && status <= 599
	}
	return false
}
