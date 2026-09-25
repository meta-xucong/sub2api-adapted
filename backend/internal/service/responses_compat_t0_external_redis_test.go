package service

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Run write and read in separate go test processes against an isolated,
// loopback-only Redis. No production credentials, models or tool execution.
func TestT0Audit_ExternalRedisSeparateProcesses(t *testing.T) {
	addr := os.Getenv("T0_AUDIT_REDIS_ADDR")
	if addr == "" {
		t.Skip("requires an explicitly supplied isolated loopback Redis")
	}
	host, _, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	require.True(t, net.ParseIP(host).IsLoopback(), "this test must never use a remote/production Redis")
	role := os.Getenv("T0_AUDIT_REDIS_ROLE")
	require.Contains(t, []string{"write", "read"}, role)
	client := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, client.Ping(ctx).Err())
	cache := &t0AuditRedisCache{client: client}
	c, _ := t0AuditContext("/v1/responses", "")
	group := int64(17)
	c.Set("api_key", &APIKey{ID: 900, GroupID: &group})
	const id = "resp_t0_isolated_redis"
	if role == "write" {
		svc := &GatewayService{cache: cache}
		request := &apicompat.ResponsesRequest{Model: "synthetic-claude", Instructions: "Skills UTF-8 保留", Input: json.RawMessage(`"synthetic user"`), Tools: []apicompat.ResponsesTool{{Type: "function", Name: "unified_exec"}}}
		response := &apicompat.ResponsesResponse{ID: id, Output: []apicompat.ResponsesOutput{
			{Type: "reasoning", Summary: []apicompat.ResponsesSummary{{Type: "summary_text", Text: "synthetic reasoning"}}},
			{Type: "function_call", ID: "item_t0", CallID: "call_t0", Name: "unified_exec", Arguments: `{"command":"Write-Output synthetic"}`},
		}}
		require.NoError(t, svc.saveResponsesCompatResponse(ctx, c, request, response))
		t.Log("isolated_redis phase=write persistence=passed evidence=synthetic_protocol")
		return
	}
	// A fresh process has no writer-side sync.Map or request objects.
	svc := &OpenAIGatewayService{cache: cache}
	input := `[{"type":"function_call_output","item_id":"item_t0","output":"synthetic tool error","is_error":true},{"type":"function_call_output","item_id":"item_t0","output":"synthetic tool error","is_error":true}]`
	request := &apicompat.ResponsesRequest{PreviousResponseID: id, Input: json.RawMessage(input)}
	require.NoError(t, svc.prepareResponsesCompatContinuation(ctx, c, request))
	require.Empty(t, request.PreviousResponseID)
	require.Equal(t, "Skills UTF-8 保留", request.Instructions)
	require.Len(t, request.Tools, 1)
	require.Equal(t, int64(4), gjson.GetBytes(request.Input, "#").Int())
	require.Equal(t, "call_t0", gjson.GetBytes(request.Input, "3.call_id").String())
	require.True(t, gjson.GetBytes(request.Input, "3.is_error").Bool())
	_, err = apicompat.ResponsesToChatCompletionsRequest(request)
	require.NoError(t, err)
	require.NoError(t, cache.DeleteResponsesCompatState(ctx, responsesCompatSessionCacheKey(c, id)))
	t.Log("isolated_redis phase=read continuation=passed duplicate_tool_results=1 evidence=synthetic_protocol")
}
