package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type t0HTTPOptions struct {
	Cache                     service.GatewayCache
	Usage                     service.UsageLogRepository
	Platform, Protocol, Model string
	Composite                 bool
}

type t0CountUsage struct {
	service.UsageLogRepository
	count atomic.Int32
}

func (r *t0CountUsage) Create(context.Context, *service.UsageLog) (bool, error) {
	r.count.Add(1)
	return true, nil
}

func writeT0ToolFixture(w http.ResponseWriter, provider string, body []byte) {
	stream := gjson.GetBytes(body, "stream").Bool()
	if provider == "native" {
		payload := `{"id":"resp_native_fixture","object":"response","created_at":1,"status":"completed","model":"deepseek-v4-flash","output":[{"id":"item_native_fixture","type":"function_call","status":"completed","call_id":"call_fixture","name":"unified_exec","arguments":"{}"}],"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}`
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", payload)
		} else {
			_, _ = io.WriteString(w, payload)
		}
		return
	}
	if provider == "anthropic" {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_fixture\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture\",\"content\":[],\"usage\":{\"input_tokens\":4}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_fixture\",\"name\":\"unified_exec\",\"input\":{}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"command\\\":\\\"Write-Output test\\\"}\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		return
	}
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_fixture\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_fixture\",\"type\":\"function\",\"function\":{\"name\":\"unified_exec\",\"arguments\":\"{\\\"command\\\":\\\"Write-Output test\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2,\"total_tokens\":6}}\n\ndata: [DONE]\n\n")
	} else {
		_, _ = io.WriteString(w, `{"id":"chatcmpl_fixture","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_fixture","type":"function","function":{"name":"unified_exec","arguments":"{\"command\":\"Write-Output test\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
	}
}

func t0RedisStore(t *testing.T, server *miniredis.Miniredis) service.GatewayCache {
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { _ = client.Close() })
	return repository.NewGatewayCache(client)
}
func t0KeyedHTTP(t *testing.T, s *httptest.Server, path, key, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	require.NoError(t, err)
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.NoError(t, err)
	return resp, data
}

func TestT0RequestReplayHTTP_CrossInstanceWithoutSecondUsage(t *testing.T) {
	for _, provider := range []string{"chat", "anthropic", "native"} {
		t.Run(provider, func(t *testing.T) {
			redisServer := miniredis.RunT(t)
			usage := &t0CountUsage{}
			a, model, _, hitsA := newT0CompactHTTPServer(t, provider, "", t0HTTPOptions{Cache: t0RedisStore(t, redisServer), Usage: usage})
			b, _, _, hitsB := newT0CompactHTTPServer(t, provider, "", t0HTTPOptions{Cache: t0RedisStore(t, redisServer), Usage: usage})
			body := fmt.Sprintf(`{"model":%q,"stream":true,"input":"synthetic"}`, model)
			first, data := t0KeyedHTTP(t, a, "/v1/responses/compact", "stable-1", body)
			require.Equal(t, 200, first.StatusCode, string(data))
			require.Eventually(t, func() bool { return usage.count.Load() == 1 }, time.Second, 10*time.Millisecond)
			second, replayed := t0KeyedHTTP(t, b, "/v1/responses/compact", "stable-1", body)
			require.Equal(t, 200, second.StatusCode, string(replayed))
			require.Equal(t, "true", second.Header.Get("Idempotency-Replayed"))
			require.Equal(t, data, replayed)
			require.Equal(t, int32(1), hitsA.Load())
			require.Equal(t, int32(0), hitsB.Load())
			require.Equal(t, int32(1), usage.count.Load())
			conflict, _ := t0KeyedHTTP(t, b, "/v1/responses/compact", "stable-1", strings.Replace(body, "synthetic", "changed", 1))
			require.Equal(t, 409, conflict.StatusCode)
		})
	}
}

func TestT0RequestReplayHTTP_FirstResponseLost(t *testing.T) {
	redisServer := miniredis.RunT(t)
	usage := &t0CountUsage{}
	a, model, _, hitsA := newT0CompactHTTPServer(t, "chat", "slow_tool", t0HTTPOptions{Cache: t0RedisStore(t, redisServer), Usage: usage})
	b, _, _, hitsB := newT0CompactHTTPServer(t, "chat", "tool", t0HTTPOptions{Cache: t0RedisStore(t, redisServer), Usage: usage})
	body := fmt.Sprintf(`{"model":%q,"input":"synthetic","stream":true,"tools":[{"type":"function","name":"unified_exec","parameters":{"type":"object"}}]}`, model)
	conn, err := net.Dial("tcp", strings.TrimPrefix(a.URL, "http://"))
	require.NoError(t, err)
	_, err = fmt.Fprintf(conn, "POST /v1/responses HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nIdempotency-Key: lost-first\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return hitsA.Load() == 1 }, time.Second, 5*time.Millisecond)
	_ = conn.Close() // no response id or body was read
	require.Eventually(t, func() bool { return usage.count.Load() == 1 }, 3*time.Second, 10*time.Millisecond)
	resp, data := t0KeyedHTTP(t, b, "/v1/responses", "lost-first", body)
	require.Equal(t, 200, resp.StatusCode, string(data))
	require.Equal(t, "true", resp.Header.Get("Idempotency-Replayed"))
	require.Contains(t, string(data), "call_fixture")
	require.Equal(t, int32(0), hitsB.Load())
	require.Equal(t, int32(1), usage.count.Load())
	// A compliant client executes completed call IDs once, even if replayed.
	executed := map[string]bool{}
	executions := 0
	for i := 0; i < 2; i++ {
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			e := gjson.Parse(strings.TrimPrefix(line, "data: "))
			if e.Get("type").String() == "response.output_item.done" && e.Get("item.type").String() == "function_call" {
				id := e.Get("item.call_id").String()
				if !executed[id] {
					executed[id] = true
					executions++
				}
			}
		}
	}
	require.Equal(t, 1, executions)
}

func TestT0RequestReplayHTTP_StorageFailureNeverDispatches(t *testing.T) {
	redisServer := miniredis.RunT(t)
	store := t0RedisStore(t, redisServer)
	address := redisServer.Addr()
	redisServer.Close()
	s, model, _, hits := newT0CompactHTTPServer(t, "chat", "", t0HTTPOptions{Cache: store})
	resp, data := t0KeyedHTTP(t, s, "/v1/responses/compact", "offline", fmt.Sprintf(`{"model":%q,"input":"synthetic"}`, model))
	require.Equal(t, 503, resp.StatusCode, string(data))
	require.Equal(t, int32(0), hits.Load())
	require.Equal(t, "idempotency_store_unavailable", gjson.GetBytes(data, "error.code").String())
	require.NotContains(t, string(data), address)
}

func TestT0CNCompactHTTP_PlatformAndComposite(t *testing.T) {
	for _, platform := range []string{service.PlatformZhipu, service.PlatformDeepseek} {
		for _, protocol := range []string{service.APIProtocolChatCompletions, service.APIProtocolAnthropic} {
			for _, composite := range []bool{false, true} {
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/composite=%v/stream=%v", platform, protocol, composite, stream), func(t *testing.T) {
						model := "glm-5.3"
						if platform == service.PlatformDeepseek {
							model = "deepseek-v4-flash"
						}
						provider := "chat"
						if protocol == service.APIProtocolAnthropic {
							provider = "anthropic"
						}
						s, _, seen, hits := newT0CompactHTTPServer(t, provider, "", t0HTTPOptions{Platform: platform, Protocol: protocol, Composite: composite, Model: model})
						resp, data := t0ReadHTTP(t, s, "/v1/responses/compact", fmt.Sprintf(`{"model":%q,"input":"history","stream":%v}`, model, stream))
						require.Equal(t, 200, resp.StatusCode, string(data))
						require.Equal(t, int32(1), hits.Load())
						if stream {
							require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")
							require.Contains(t, string(data), "response.completed")
						} else {
							require.Equal(t, "compaction", gjson.GetBytes(data, "output.0.type").String())
						}
						got := <-seen
						require.False(t, gjson.GetBytes(got.body, "stream").Bool())
						require.True(t, bytes.Contains(data, []byte("compaction")))
					})
				}
			}
		}
	}
}
