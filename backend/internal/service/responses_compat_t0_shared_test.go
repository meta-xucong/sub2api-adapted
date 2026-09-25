package service

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Two real Redis clients against an in-process Redis emulator. This proves
// shared protocol state, not deployment/production Redis acceptance.
type t0AuditRedisCache struct {
	stubGatewayCache
	client *redis.Client
}

func (c *t0AuditRedisCache) GetResponsesCompatState(ctx context.Context, key string) ([]byte, error) {
	value, err := c.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	return value, err
}
func (c *t0AuditRedisCache) SetResponsesCompatState(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}
func (c *t0AuditRedisCache) DeleteResponsesCompatState(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}

func TestT0Audit_SharedRedisContinuationAcrossAdaptersAndScopes(t *testing.T) {
	server := miniredis.RunT(t)
	first := redis.NewClient(&redis.Options{Addr: server.Addr()})
	second := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = first.Close(); _ = second.Close() })
	writer := &OpenAIGatewayService{cache: &t0AuditRedisCache{client: first}}
	reader := &GatewayService{cache: &t0AuditRedisCache{client: second}}
	ctx := context.Background()
	c, _ := t0AuditContext("/v1/responses", "")
	group := int64(17)
	c.Set("api_key", &APIKey{ID: 900, GroupID: &group})
	request := &apicompat.ResponsesRequest{
		Model: "synthetic-model", Instructions: "Skills: preserve UTF-8 检查",
		Input: json.RawMessage(`"start"`),
		Tools: []apicompat.ResponsesTool{{Type: "function", Name: "unified_exec"}},
	}
	response := &apicompat.ResponsesResponse{ID: "resp_shared_a", Output: []apicompat.ResponsesOutput{
		{Type: "reasoning", Summary: []apicompat.ResponsesSummary{{Type: "summary_text", Text: "检查"}}},
		{Type: "function_call", ID: "item_exec", CallID: "call_exec", Name: "unified_exec", Arguments: `{"command":"Write-Output test"}`},
	}}
	require.NoError(t, writer.saveResponsesCompatResponse(ctx, c, request, response))
	nextContext, _ := t0AuditContext("/v1/responses", "")
	nextContext.Set("api_key", &APIKey{ID: 900, GroupID: &group})
	next := &apicompat.ResponsesRequest{PreviousResponseID: "resp_shared_a", Input: json.RawMessage(`[{"type":"function_call_output","item_id":"item_exec","output":"synthetic failure","is_error":true}]`)}
	require.NoError(t, reader.prepareResponsesCompatContinuation(ctx, nextContext, next))
	require.Empty(t, next.PreviousResponseID)
	require.Equal(t, request.Instructions, next.Instructions)
	require.Len(t, next.Tools, 1)
	require.Equal(t, "call_exec", gjson.GetBytes(next.Input, "3.call_id").String())
	require.True(t, gjson.GetBytes(next.Input, "3.is_error").Bool())
	anthropic, err := apicompat.ResponsesToAnthropicRequest(next)
	require.NoError(t, err)
	encoded, err := json.Marshal(anthropic)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "tool_result")
	require.Contains(t, string(encoded), "call_exec")
	require.Contains(t, string(encoded), "Skills")
	for _, identity := range [][2]int64{{901, 17}, {900, 18}} {
		isolated, _ := t0AuditContext("/v1/responses", "")
		g := identity[1]
		isolated.Set("api_key", &APIKey{ID: identity[0], GroupID: &g})
		var local sync.Map
		state, err := loadResponsesCompatSession(ctx, isolated, reader.cache, &local, "resp_shared_a")
		require.NoError(t, err)
		require.Nil(t, state, "another key/group must not access the history")
	}
	server.FastForward(2 * time.Hour)
	var empty sync.Map
	expired, err := loadResponsesCompatSession(ctx, nextContext, reader.cache, &empty, "resp_shared_a")
	require.NoError(t, err)
	require.Nil(t, expired)
}

func TestT0Audit_ContinuationErrorsAreClassified(t *testing.T) {
	for _, tc := range []struct {
		name, payload, input, code string
		status                     int
	}{
		{"missing", "", `"continue"`, "previous_response_not_found", http.StatusBadRequest},
		{"corrupt_json", `{"response_id":`, `"continue"`, "compat_session_corrupt", http.StatusInternalServerError},
		{"wrong_identity", `{"response_id":"resp_foreign"}`, `"continue"`, "compat_session_corrupt", http.StatusInternalServerError},
		{"invalid_input", `{"response_id":"resp_test"}`, `42`, "invalid_request_error", http.StatusBadRequest},
		{"unknown_call", `{"response_id":"resp_test"}`, `[{"type":"function_call_output","call_id":"unknown","output":"result"}]`, "invalid_tool_output", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := t0AuditContext("/v1/responses", "")
			cache := &t0AuditStateCache{values: map[string][]byte{}}
			if tc.payload != "" {
				cache.values[responsesCompatSessionCacheKey(c, "resp_test")] = []byte(tc.payload)
			}
			req := &apicompat.ResponsesRequest{PreviousResponseID: "resp_test", Input: json.RawMessage(tc.input)}
			var local sync.Map
			_, err := prepareResponsesCompatContinuation(context.Background(), c, cache, &local, req)
			require.Error(t, err)
			writeResponsesCompatError(c, err)
			require.Equal(t, tc.status, rec.Code)
			require.Equal(t, tc.code, gjson.Get(rec.Body.String(), "error.code").String())
			require.NotEmpty(t, gjson.Get(rec.Body.String(), "error.type").String())
			require.NotContains(t, rec.Body.String(), "resp_foreign")
			require.Equal(t, "resp_test", req.PreviousResponseID)
		})
	}
}
