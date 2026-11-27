package nativeharness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestResponseLogicalAndWireLimitsHTTP(t *testing.T) {
	body := func(content string) string { return strings.Replace(completion, `"done"`, content, 1) }
	quoted := func(content string) string {
		raw, err := json.Marshal(content)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	cases := []struct{ name, raw, code string }{
		{"max-ascii", body(quoted(strings.Repeat("a", MaxResultText))), ""},
		{"max-six-byte-escape", body(`"` + strings.Repeat(`\u0061`, MaxResultText) + `"`), ""},
		{"max-control-escape", body(quoted(strings.Repeat("\x00", MaxResultText))), ""},
		{"max-utf8", body(quoted(strings.Repeat("😀", MaxResultText/4))), ""},
		{"max-surrogate-escape", body(`"` + strings.Repeat(`\ud83d\ude00`, MaxResultText/4) + `"`), ""},
		{"over-ascii", body(quoted(strings.Repeat("a", MaxResultText+1))), "NATIVE_INVALID_COMPLETION"},
		{"over-utf8-by-one-byte", body(quoted(strings.Repeat("😀", MaxResultText/4) + "a")), "NATIVE_INVALID_COMPLETION"},
		{"max-combined", strings.Replace(body(quoted(strings.Repeat("a", MaxResultText-1))), `"role":"assistant"`, `"role":"assistant","refusal":"b"`, 1), ""},
		{"over-combined", strings.Replace(body(quoted(strings.Repeat("a", MaxResultText))), `"role":"assistant"`, `"role":"assistant","refusal":"b"`, 1), "NATIVE_INVALID_COMPLETION"},
		{"max-refusal", strings.Replace(body(`null`), `"role":"assistant"`, `"role":"assistant","refusal":`+quoted(strings.Repeat("a", MaxResultText)), 1), ""},
		{"over-wire", completion + strings.Repeat(" ", MaxResponseBody-len(completion)+1), "NATIVE_RECEIPT_UNKNOWN"},
		{"exact-wire", completion + strings.Repeat(" ", MaxResponseBody-len(completion)), ""},
		{"extra-key-large", strings.TrimSuffix(body(quoted(strings.Repeat("a", MaxResultText))), "}") + `,"extra":null}`, "NATIVE_INVALID_COMPLETION"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", fmt.Sprint(len(c.raw)))
				_, _ = fmt.Fprint(w, c.raw)
			}))
			defer server.Close()
			result, err := newTestService(server.URL).Invoke(context.Background(), 7, 9, "stable", true, []byte(request))
			if c.code != "" {
				requireCode(t, err, c.code)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(result.Body) != c.raw {
				t.Fatal("response bytes changed")
			}
		})
	}
}

func TestRequestLimitNotExpandedWithResponseWire(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); _, _ = fmt.Fprint(w, completion) }))
	defer server.Close()
	raw := strings.Replace(request, "hello", strings.Repeat("a", MaxRequest), 1)
	if len(raw) <= MaxRequest || len(raw) >= MaxResponseBody {
		t.Fatal("invalid fixture length")
	}
	_, err := newTestService(server.URL).Invoke(context.Background(), 7, 9, "stable", true, []byte(raw))
	requireCode(t, err, "BODY_TOO_LARGE")
	if hits.Load() != 0 {
		t.Fatal("oversized request reached upstream")
	}
	if _, err := decode([]byte(raw)); err == nil {
		t.Fatal("default decoder accepted oversized request")
	}
	if _, err := RequestModel([]byte(raw)); err == nil {
		t.Fatal("request routing accepted oversized request")
	}
}
