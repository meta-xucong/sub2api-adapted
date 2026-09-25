package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func t0ReplayContext(key, body string, user, keyID, groupID int64) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	c.Request.Header.Set("Idempotency-Key", key)
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: keyID, GroupID: &groupID, User: &service.User{ID: user}})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: user})
	return c, rec
}

const t0ReplayJSON = `{"object":"response","id":"resp_test","status":"completed","output":[]}`

func TestT0Idempotency_ConcurrentScopeAndUnknown(t *testing.T) {
	server := miniredis.RunT(t)
	store := t0RedisStore(t, server).(service.ResponsesIdempotencyStore)
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	var calls atomic.Int32
	next := func(c *gin.Context) {
		calls.Add(1)
		close(started)
		<-release
		c.Data(200, "application/json", []byte(t0ReplayJSON))
	}
	first, _ := t0ReplayContext("in-flight", `{"model":"x"}`, 1, 2, 3)
	go func() { defer close(done); runResponsesIdempotently(first, store, nil, next) }()
	<-started
	retry, rec := t0ReplayContext("in-flight", `{"model":"x"}`, 1, 2, 3)
	runResponsesIdempotently(retry, store, nil, func(*gin.Context) { t.Error("duplicate dispatch") })
	require.Equal(t, 409, rec.Code)
	require.Equal(t, "idempotency_in_progress", gjson.Get(rec.Body.String(), "error.code").String())
	close(release)
	<-done
	for _, identity := range [][3]int64{{9, 2, 3}, {1, 9, 3}, {1, 2, 9}} {
		c, rec := t0ReplayContext("in-flight", `{"model":"x"}`, identity[0], identity[1], identity[2])
		runResponsesIdempotently(c, store, nil, func(c *gin.Context) { calls.Add(1); c.Data(200, "application/json", []byte(t0ReplayJSON)) })
		require.Equal(t, 200, rec.Code)
		require.Equal(t, "false", rec.Header().Get("Idempotency-Replayed"))
	}
	require.Equal(t, int32(4), calls.Load())
	// An ambiguous truncated result stays blocked; never reclaim for execution.
	c, _ := t0ReplayContext("unknown", `{"model":"x"}`, 1, 2, 3)
	runResponsesIdempotently(c, store, nil, func(c *gin.Context) {
		c.Data(200, "text/event-stream", []byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_partial\"}}\n\n"))
	})
	c, rec = t0ReplayContext("unknown", `{"model":"x"}`, 1, 2, 3)
	runResponsesIdempotently(c, store, nil, func(*gin.Context) { t.Error("ambiguous request reexecuted") })
	require.Equal(t, 409, rec.Code)
	require.Equal(t, "idempotency_outcome_unknown", gjson.Get(rec.Body.String(), "error.code").String())
}

func TestT0Idempotency_InvalidAndUnavailable(t *testing.T) {
	for _, key := range []string{"", " ", strings.Repeat("a", 129), "bad key"} {
		c, rec := t0ReplayContext(key, `{"model":"x"}`, 1, 2, 3)
		runResponsesIdempotently(c, nil, nil, func(*gin.Context) { t.Error("invalid key executed") })
		require.Equal(t, 400, rec.Code)
	}
	c, rec := t0ReplayContext("valid", `{"model":"x"}`, 1, 2, 3)
	runResponsesIdempotently(c, nil, nil, func(*gin.Context) { t.Error("missing store executed") })
	require.Equal(t, 503, rec.Code)
	c, rec = t0ReplayContext("valid", `{"model":"x"}`, 0, 0, 0)
	runResponsesIdempotently(c, nil, nil, func(*gin.Context) { t.Error("anonymous replay") })
	require.Equal(t, 401, rec.Code)
}

func TestT0Idempotency_ReplayValidationAndHeaderAllowlist(t *testing.T) {
	server := miniredis.RunT(t)
	store := t0RedisStore(t, server).(service.ResponsesIdempotencyStore)
	first, _ := t0ReplayContext("headers", `{"model":"x"}`, 1, 2, 3)
	runResponsesIdempotently(first, store, nil, func(c *gin.Context) {
		c.Header("Set-Cookie", "must-not-replay")
		c.Header("Authorization", "must-not-replay")
		c.Data(200, "application/json", []byte(t0ReplayJSON))
	})
	second, rec := t0ReplayContext("headers", `{"model":"x"}`, 1, 2, 3)
	runResponsesIdempotently(second, store, nil, func(*gin.Context) { t.Error("replay dispatched") })
	require.Empty(t, rec.Header().Get("Set-Cookie"))
	require.Empty(t, rec.Header().Get("Authorization"))
	require.Equal(t, "true", rec.Header().Get("Idempotency-Replayed"))
	for _, payload := range []string{
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"a\"}}\n\n",
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"a\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"b\"}}\n\n",
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"a\"}}\n\ndata: {}\n\n",
	} {
		require.False(t, responsesReplayComplete(200, "text/event-stream", []byte(payload)))
	}
	c, _ := t0ReplayContext("oversized", `{"model":"x"}`, 1, 2, 3)
	runResponsesIdempotently(c, store, nil, func(c *gin.Context) {
		c.Data(200, "application/json", []byte(strings.Repeat("x", responsesReplayMaxBytes+1)))
	})
	c, rec = t0ReplayContext("oversized", `{"model":"x"}`, 1, 2, 3)
	runResponsesIdempotently(c, store, nil, func(*gin.Context) { t.Error("oversized reexecuted") })
	require.Equal(t, 409, rec.Code)
}

func TestT0NativeAnthropic_ContinuationAcrossHTTPInstances(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			redisServer := miniredis.RunT(t)
			opts := t0HTTPOptions{Cache: t0RedisStore(t, redisServer), Platform: service.PlatformZhipu, Protocol: service.APIProtocolAnthropic, Composite: true, Model: "glm-5.3"}
			a, model, _, _ := newT0CompactHTTPServer(t, "anthropic", "tool", opts)
			opts.Cache = t0RedisStore(t, redisServer)
			b, _, observed, _ := newT0CompactHTTPServer(t, "anthropic", "tool", opts)
			body := fmt.Sprintf(`{"model":%q,"stream":%v,"instructions":"Skills UTF-8 保留","input":"synthetic","tools":[{"type":"function","name":"unified_exec","parameters":{"type":"object"}}]}`, model, stream)
			resp, data := t0ReadHTTP(t, a, "/v1/responses", body)
			require.Equal(t, 200, resp.StatusCode, string(data))
			id := gjson.GetBytes(data, "id").String()
			if stream {
				for _, line := range strings.Split(string(data), "\n") {
					if strings.HasPrefix(line, "data: ") {
						e := gjson.Parse(strings.TrimPrefix(line, "data: "))
						if e.Get("type").String() == "response.completed" {
							id = e.Get("response.id").String()
						}
					}
				}
			}
			require.True(t, strings.HasPrefix(id, "resp_"), string(data))
			// Wait only for persistence, never execute a new upstream request while missing.
			body = fmt.Sprintf(`{"model":%q,"previous_response_id":%q,"stream":false,"input":[{"type":"function_call_output","call_id":"call_fixture","output":"synthetic error","is_error":true}]}`, model, id)
			resp, data = t0ReadHTTP(t, b, "/v1/responses", body)
			require.Equal(t, 200, resp.StatusCode, string(data))
			select {
			case got := <-observed:
				require.Contains(t, string(got.body), "tool_result")
				require.Contains(t, string(got.body), "call_fixture")
				require.Contains(t, string(got.body), "Skills UTF-8")
				require.NotContains(t, string(got.body), "previous_response_id")
			default:
				t.Fatal("continuation was not forwarded")
			}
		})
	}
}
