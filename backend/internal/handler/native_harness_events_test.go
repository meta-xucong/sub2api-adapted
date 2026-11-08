//go:build unit

package handler

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/nativeharness"
	"github.com/gin-gonic/gin"
)

func TestNativeEventsFlushBeforeFinalAndResumeReadOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var mu sync.Mutex
	flags := []string{}
	released := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		flags = append(flags, r.Header.Get("X-Native-Admission-Allowed"))
		mu.Unlock()
		if r.Header.Get("X-Native-Event-Cursor") == "0" {
			_, _ = fmt.Fprint(w, `{"schema_version":"native-events-v1","remote_task_id":"task-1","events":[{"seq":1,"type":"text_snapshot","data":{"text":"draft before terminal"}}],"next_cursor":1,"has_more":false,"terminal_seq":null}`)
			return
		}
		select {
		case <-released:
		case <-r.Context().Done():
			return
		}
		_, _ = fmt.Fprint(w, `{"schema_version":"native-events-v1","remote_task_id":"task-1","events":[{"seq":2,"type":"completion","data":{"id":"chatcmpl-test","created":1,"object":"chat.completion","model":"synthetic-test","choices":[{"index":0,"message":{"role":"assistant","content":"corrected final"},"finish_reason":"stop"}]}}],"next_cursor":2,"has_more":false,"terminal_seq":2}`)
	}))
	defer upstream.Close()
	cfg := nativeConfig(upstream.URL)
	cfg.Gateway.NativeHarness.StandardWaitSeconds = 3
	svc := nativeharness.New(cfg.Gateway.NativeHarness, &nativeTestStore{})
	svc.Getenv = func(string) string { return "synthetic-test-service-token" }
	h := &OpenAIGatewayHandler{cfg: cfg, nativeHarness: svc}
	router := gin.New()
	router.POST("/events", func(c *gin.Context) { h.nativeHarnessEvents(c, 7, 9, "one", true, []byte(nativeRequest)) })
	server := httptest.NewServer(router)
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/events", nil)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	frame := ""
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		frame += line
		if line == "\n" {
			break
		}
	}
	if !strings.Contains(frame, "id: 1") || !strings.Contains(frame, "draft before terminal") || strings.Contains(frame, "completion") {
		t.Fatal(frame)
	}
	// The authoritative final is still blocked: first content truly arrived live.
	cancel()
	_ = response.Body.Close()
	close(released)
	req, _ = http.NewRequest("POST", server.URL+"/events", nil)
	req.Header.Set("Last-Event-ID", "1")
	response, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || strings.Contains(string(body), "id: 1") || !strings.Contains(string(body), "id: 2") || !strings.Contains(string(body), "corrected final") {
		t.Fatal(string(body))
	}
	mu.Lock()
	defer mu.Unlock()
	if flags[0] != "true" {
		t.Fatal(flags)
	}
	for _, v := range flags[1:] {
		if v != "false" {
			t.Fatal(flags)
		}
	}
}

type failedStreamWriter struct {
	*httptest.ResponseRecorder
	writes int
}

func (w *failedStreamWriter) SetWriteDeadline(time.Time) error { return nil }
func (w *failedStreamWriter) Write(p []byte) (int, error)      { w.writes++; return 0, io.ErrClosedPipe }
func (w *failedStreamWriter) FlushError() error                { return io.ErrClosedPipe }

func TestNativeEventsWriteFailureStopsObservation(t *testing.T) {
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = fmt.Fprint(w, `{"schema_version":"native-events-v1","remote_task_id":"task-1","events":[],"next_cursor":0,"has_more":false,"terminal_seq":null}`)
	}))
	defer upstream.Close()
	cfg := nativeConfig(upstream.URL)
	cfg.Gateway.NativeHarness.StandardWaitSeconds = 3
	svc := nativeharness.New(cfg.Gateway.NativeHarness, &nativeTestStore{})
	svc.Getenv = func(string) string { return "synthetic-test-service-token" }
	h := &OpenAIGatewayHandler{cfg: cfg, nativeHarness: svc}
	router := gin.New()
	router.POST("/events", func(c *gin.Context) { h.nativeHarnessEvents(c, 7, 9, "one", true, []byte(nativeRequest)) })
	w := &failedStreamWriter{ResponseRecorder: httptest.NewRecorder()}
	router.ServeHTTP(w, httptest.NewRequest("POST", "/events", nil))
	if hits != 1 || strings.Contains(w.Body.String(), "observation_end") {
		t.Fatalf("hits%d %s", hits, w.Body.String())
	}
}

type flushFailureRecorder struct{ *nativeStreamRecorder }

func (w *flushFailureRecorder) FlushError() error { return io.ErrClosedPipe }
func TestNativeEventsFlushFailureAndZeroBudget(t *testing.T) {
	for _, budget := range []int{0, 2} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			hits := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				_, _ = fmt.Fprint(w, `{"schema_version":"native-events-v1","remote_task_id":"task-1","events":[],"next_cursor":0,"has_more":false,"terminal_seq":null}`)
			}))
			defer upstream.Close()
			cfg := nativeConfig(upstream.URL)
			cfg.Gateway.NativeHarness.StandardWaitSeconds = budget
			svc := nativeharness.New(cfg.Gateway.NativeHarness, &nativeTestStore{})
			svc.Getenv = func(string) string { return "synthetic-test-service-token" }
			h := &OpenAIGatewayHandler{cfg: cfg, nativeHarness: svc}
			router := gin.New()
			router.POST("/events", func(c *gin.Context) { h.nativeHarnessEvents(c, 7, 9, "one", true, []byte(nativeRequest)) })
			record := httptest.NewRecorder()
			w := &flushFailureRecorder{&nativeStreamRecorder{record}}
			router.ServeHTTP(w, httptest.NewRequest("POST", "/events", nil))
			if budget == 0 {
				if hits != 0 || record.Code != 503 {
					t.Fatalf("hits%d status%d", hits, record.Code)
				}
			} else if hits != 1 || strings.Contains(record.Body.String(), "observation_end") {
				t.Fatalf("hits%d %s", hits, record.Body.String())
			}
		})
	}
}
