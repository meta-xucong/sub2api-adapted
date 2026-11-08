package nativeharness

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestStandardObservationOnlyAfterInitialAdmission(t *testing.T) {
	var mu sync.Mutex
	var flags, ids []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		flags = append(flags, r.Header.Get("X-Native-Admission-Allowed"))
		ids = append(ids, r.Header.Get("X-Native-Operation"))
		n := len(flags)
		mu.Unlock()
		if n < 3 {
			w.WriteHeader(504)
			_, _ = fmt.Fprint(w, `{"error":{"code":"TASK_WAIT_TIMEOUT"}}`)
		} else {
			_, _ = fmt.Fprint(w, completion)
		}
	}))
	defer upstream.Close()
	s := newTestService(upstream.URL)
	s.Config.StandardWaitSeconds = 3
	result, err := s.InvokeStandard(context.Background(), 7, 9, "one", true, []byte(request))
	if err != nil || result.Status != 200 {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(flags) != 3 || flags[0] != "true" || flags[1] != "false" || flags[2] != "false" || ids[0] != ids[1] || ids[0] != ids[2] {
		t.Fatalf("flags=%v ids=%v", flags, ids)
	}
}

func TestStandardObservationStopsOnNonwaitingError(t *testing.T) {
	for _, code := range []string{"EXECUTION_UNCERTAIN", "APPROVAL_PENDING", "NEW_ADMISSION_DISABLED", "TASK_WAIT_TIMEOUT"} {
		t.Run(code, func(t *testing.T) {
			hits := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				w.WriteHeader(503)
				_, _ = fmt.Fprintf(w, `{"error":{"code":%q}}`, code)
			}))
			defer upstream.Close()
			s := newTestService(upstream.URL)
			s.Config.StandardWaitSeconds = 2
			_, err := s.InvokeStandard(context.Background(), 7, 9, "one", true, []byte(request))
			requireCode(t, err, code)
			if hits != 1 {
				t.Fatal("repeated uncertain request")
			}
		})
	}
}

func TestStandardObservationDeadlineAndCancelDoNotReadmit(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		var mu sync.Mutex
		flags := []string{}
		ctx, cancel := context.WithCancel(context.Background())
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			flags = append(flags, r.Header.Get("X-Native-Admission-Allowed"))
			mu.Unlock()
			w.WriteHeader(504)
			_, _ = fmt.Fprint(w, `{"error":{"code":"TASK_WAIT_TIMEOUT"}}`)
			if cancelled {
				cancel()
			}
		}))
		s := newTestService(upstream.URL)
		s.Config.StandardWaitSeconds = 1
		start := time.Now()
		_, err := s.InvokeStandard(ctx, 7, 9, "one", true, []byte(request))
		requireCode(t, err, "NATIVE_OBSERVATION_TIMEOUT")
		if time.Since(start) > 3*time.Second {
			t.Fatal("deadline exceeded")
		}
		cancel()
		upstream.Close()
		mu.Lock()
		if len(flags) == 0 {
			t.Fatal("no initial attempt")
		}
		for i, f := range flags {
			if i > 0 && f != "false" {
				t.Fatal("readmission")
			}
		}
		mu.Unlock()
	}
}

func TestStandardObservationZeroAndInvalidBudget(t *testing.T) {
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(504)
		_, _ = fmt.Fprint(w, `{"error":{"code":"TASK_WAIT_TIMEOUT"}}`)
	}))
	defer upstream.Close()
	s := newTestService(upstream.URL)
	_, err := s.InvokeStandard(context.Background(), 7, 9, "one", true, []byte(request))
	requireCode(t, err, "TASK_WAIT_TIMEOUT")
	if hits != 1 {
		t.Fatal(hits)
	}
	for _, seconds := range []int{-1, 301} {
		s.Config.StandardWaitSeconds = seconds
		_, err = s.InvokeStandard(context.Background(), 7, 9, "one", true, []byte(request))
		requireCode(t, err, "NATIVE_WAIT_CONFIG_INVALID")
	}
	if hits != 1 {
		t.Fatal("invalid configuration reached upstream")
	}
}

func TestStandardObservationMissingReceiptCannotCreateAgain(t *testing.T) {
	store := &memoryStore{rows: map[string]Operation{}}
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		store.mu.Lock()
		store.rows = map[string]Operation{}
		store.mu.Unlock()
		w.WriteHeader(504)
		_, _ = fmt.Fprint(w, `{"error":{"code":"TASK_WAIT_TIMEOUT"}}`)
	}))
	defer upstream.Close()
	s := newTestService(upstream.URL)
	s.Store = store
	s.Config.StandardWaitSeconds = 2
	_, err := s.InvokeStandard(context.Background(), 7, 9, "one", true, []byte(request))
	requireCode(t, err, "NEW_ADMISSION_DISABLED")
	if hits != 1 {
		t.Fatal("missing route receipt caused repeat upstream create")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.rows) != 0 {
		t.Fatal("observation froze new operation")
	}
}

func TestStandardObservationPrecancelDoesNotFreeze(t *testing.T) {
	s := newTestService("http://127.0.0.1:1")
	s.Config.StandardWaitSeconds = 2
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.InvokeStandard(ctx, 7, 9, "one", true, []byte(request))
	requireCode(t, err, "NATIVE_OBSERVATION_TIMEOUT")
	if _, err = s.Store.Lookup(context.Background(), 7, 9, "one"); err != ErrNotFound {
		t.Fatal("cancelled call persisted new operation")
	}
}
