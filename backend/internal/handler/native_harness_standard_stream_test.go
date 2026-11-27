//go:build unit

package handler

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/nativeharness"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNativeDefaultStandardLiveAndRewriteFailure(t *testing.T) {
	for _, rewrite := range []bool{false, true} {
		t.Run(fmt.Sprint(rewrite), func(t *testing.T) {
			release := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Native-Event-Cursor") == "0" {
					_, _ = fmt.Fprint(w, `{"schema_version":"native-events-v1","remote_task_id":"task-1","events":[{"seq":1,"type":"text_snapshot","data":{"text":"hello"}}],"next_cursor":1,"has_more":false,"terminal_seq":null}`)
					return
				}
				<-release
				final := "hello world"
				if rewrite {
					final = "corrected answer"
				}
				_, _ = fmt.Fprintf(w, `{"schema_version":"native-events-v1","remote_task_id":"task-1","events":[{"seq":2,"type":"completion","data":{"id":"chatcmpl-task-1","created":1,"object":"chat.completion","model":"synthetic-test","choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}]}}],"next_cursor":2,"has_more":false,"terminal_seq":2}`, final)
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
			response, err := client.Post(server.URL+"/events", "application/json", nil)
			if err != nil {
				t.Fatal(err)
			}
			if response.Header.Get("X-Native-Stream-Mode") != nativeharness.StandardStreamProtocol {
				t.Fatal(response.Header)
			}
			reader := bufio.NewReader(response.Body)
			first, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(first, "data: ") || !strings.Contains(first, `"content":"hello"`) || !strings.Contains(first, `"object":"chat.completion.chunk"`) {
				t.Fatal(first)
			}
			close(release)
			rest, err := io.ReadAll(reader)
			_ = response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			all := first + string(rest)
			if strings.Contains(all, "event:") || strings.Contains(all, "id: ") {
				t.Fatal("named/native frames leaked")
			}
			if rewrite {
				if !strings.Contains(all, "NATIVE_STREAM_REWRITE_REQUIRED") || strings.Contains(all, "[DONE]") || strings.Contains(all, `"finish_reason":"stop"`) || strings.Contains(all, "corrected answer") {
					t.Fatal(all)
				}
			} else {
				if !strings.Contains(all, `"content":" world"`) || !strings.Contains(all, "data: [DONE]") || !strings.Contains(all, `"finish_reason":"stop"`) {
					t.Fatal(all)
				}
			}
			for _, line := range strings.Split(all, "\n") {
				if strings.HasPrefix(line, "data: ") && line != "data: [DONE]" {
					if !json.Valid([]byte(strings.TrimPrefix(line, "data: "))) {
						t.Fatal(line)
					}
				}
			}
		})
	}
}
func TestNativeStandardProtocolValidationBeforeAdmission(t *testing.T) {
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer upstream.Close()
	for _, headers := range []map[string][]string{
		{"X-Native-Stream-Protocol": {"unknown"}},
		{"X-Native-Stream-Protocol": {"native-events-v1", "openai-append-v1"}},
		{"Last-Event-ID": {"1"}},
	} {
		cfg := nativeConfig(upstream.URL)
		cfg.Gateway.NativeHarness.StandardWaitSeconds = 3
		svc := nativeharness.New(cfg.Gateway.NativeHarness, &nativeTestStore{})
		svc.Getenv = func(string) string { return "synthetic-test-service-token" }
		h := &OpenAIGatewayHandler{cfg: cfg, nativeHarness: svc}
		router := gin.New()
		router.POST("/events", func(c *gin.Context) { h.nativeHarnessEvents(c, 7, 9, "one", true, []byte(nativeRequest)) })
		req := httptest.NewRequest("POST", "/events", nil)
		for name, values := range headers {
			for _, value := range values {
				req.Header.Add(name, value)
			}
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(&nativeStreamRecorder{w}, req)
		if w.Code != 400 || hits != 0 {
			t.Fatalf("%d %d", w.Code, hits)
		}
	}
}
