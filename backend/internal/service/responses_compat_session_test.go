//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResponsesCompatContinuationReplaysHistoryAndResolvesToolResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	ctx.Set("api_key", &APIKey{ID: 9, GroupID: int64PtrForCompatTest(4)})
	local := &sync.Map{}
	req := &apicompat.ResponsesRequest{
		Model:        "glm-5",
		Instructions: "保留 Skills instructions",
		Tools:        []apicompat.ResponsesTool{{Type: "function", Name: "exec", Parameters: json.RawMessage(`{"type":"object"}`)}},
		Input:        json.RawMessage(`[{"type":"message","role":"user","content":"执行 café"}]`),
	}
	resp := &apicompat.ResponsesResponse{
		ID: "resp_first",
		Output: []apicompat.ResponsesOutput{
			{Type: "reasoning", ID: "reason_1", Summary: []apicompat.ResponsesSummary{{Type: "summary_text", Text: "计划已完成"}}},
			{Type: "function_call", ID: "fc_item_1", CallID: "call_exec_1", Name: "exec", Arguments: `{"cmd":"Get-Date"}`},
		},
	}
	state, err := buildResponsesCompatSessionState(req, resp)
	require.NoError(t, err)
	require.NoError(t, saveResponsesCompatSession(context.Background(), ctx, nil, local, state, time.Hour))

	continued := &apicompat.ResponsesRequest{
		PreviousResponseID: "resp_first",
		Input: json.RawMessage(`[
			{"type":"function_call_output","item_reference":"fc_item_1","output":"permission denied","is_error":true},
			{"type":"function_call_output","item_reference":"fc_item_1","output":"permission denied","is_error":true}
		]`),
	}
	used, err := prepareResponsesCompatContinuation(context.Background(), ctx, nil, local, continued)
	require.NoError(t, err)
	require.True(t, used)
	require.Empty(t, continued.PreviousResponseID)
	require.Equal(t, "glm-5", continued.Model)
	require.Equal(t, "保留 Skills instructions", continued.Instructions)
	require.Len(t, continued.Tools, 1)
	var replayed []json.RawMessage
	require.NoError(t, json.Unmarshal(continued.Input, &replayed))
	require.Len(t, replayed, 4, "user, reasoning, tool call, and one de-duplicated tool result")
	require.Contains(t, string(continued.Input), "café")
	require.Contains(t, string(continued.Input), "计划已完成")
	require.Equal(t, "call_exec_1", gjson.GetBytes(continued.Input, "3.call_id").String())
	require.Equal(t, "permission denied", gjson.GetBytes(continued.Input, "3.output").String())
	require.True(t, gjson.GetBytes(continued.Input, "3.is_error").Bool())
}

func TestResponsesCompatContinuationPreservesClientResentHistoryWithoutDuplication(t *testing.T) {
	state := &responsesCompatSessionState{
		ResponseID: "resp_first",
		RequestInput: []json.RawMessage{
			json.RawMessage(`{"type":"message","role":"user","content":"first"}`),
		},
		OutputInput: []json.RawMessage{
			json.RawMessage(`{"type":"function_call","id":"item_1","call_id":"call_1","name":"exec","arguments":"{}"}`),
		},
		HistoryInput: []json.RawMessage{
			json.RawMessage(`{"type":"message","role":"user","content":"first"}`),
			json.RawMessage(`{"type":"function_call","id":"item_1","call_id":"call_1","name":"exec","arguments":"{}"}`),
		},
	}
	current, err := responsesCompatRequestInputRaw(json.RawMessage(`[
		{"type":"message","role":"user","content":"first"},
		{"type":"function_call","id":"item_1","call_id":"call_1","name":"exec","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_1","output":"done"}
	]`))
	require.NoError(t, err)
	merged := mergeResponsesCompatInput(state, current)
	require.Len(t, merged, 3)
	require.Equal(t, `{"type":"function_call_output","call_id":"call_1","output":"done"}`, string(merged[2]))
}

func TestResponsesCompatContinuationRetainsFiveToolRoundsWithoutDuplicates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	local := &sync.Map{}
	firstReq := &apicompat.ResponsesRequest{
		Model:        "glm-5",
		Instructions: "five-round instructions",
		Tools:        []apicompat.ResponsesTool{{Type: "function", Name: "exec", Parameters: json.RawMessage(`{"type":"object"}`)}},
		Input:        json.RawMessage(`[{"type":"message","role":"user","content":"start"}]`),
	}
	firstResp := &apicompat.ResponsesResponse{
		ID:     "resp_round_0",
		Output: []apicompat.ResponsesOutput{{Type: "function_call", ID: "item_0", CallID: "call_0", Name: "exec", Arguments: `{"round":0}`}},
	}
	state, err := buildResponsesCompatSessionState(firstReq, firstResp)
	require.NoError(t, err)
	require.NoError(t, saveResponsesCompatSession(context.Background(), ctx, nil, local, state, time.Hour))
	previousID := firstResp.ID

	for round := 1; round <= 5; round++ {
		input := json.RawMessage(fmt.Sprintf(`[{"type":"function_call_output","call_id":"call_%d","output":"result_%d"},{"type":"message","role":"user","content":"turn_%d"}]`, round-1, round-1, round))
		continued := &apicompat.ResponsesRequest{PreviousResponseID: previousID, Input: input}
		used, err := prepareResponsesCompatContinuation(context.Background(), ctx, nil, local, continued)
		require.NoError(t, err)
		require.True(t, used)
		require.Empty(t, continued.PreviousResponseID)
		require.Equal(t, "glm-5", continued.Model)
		require.Equal(t, "five-round instructions", continued.Instructions)
		require.Len(t, continued.Tools, 1)

		var items []map[string]any
		require.NoError(t, json.Unmarshal(continued.Input, &items))
		seenOutputs := make(map[string]int)
		for _, item := range items {
			if item["type"] == "function_call_output" {
				seenOutputs[item["call_id"].(string)]++
			}
		}
		for i := 0; i < round; i++ {
			require.Equal(t, 1, seenOutputs[fmt.Sprintf("call_%d", i)], "round %d input: %s", round, continued.Input)
		}

		nextID := fmt.Sprintf("resp_round_%d", round)
		nextResp := &apicompat.ResponsesResponse{
			ID:     nextID,
			Output: []apicompat.ResponsesOutput{{Type: "function_call", ID: fmt.Sprintf("item_%d", round), CallID: fmt.Sprintf("call_%d", round), Name: "exec", Arguments: fmt.Sprintf(`{"round":%d}`, round)}},
		}
		nextState, err := buildResponsesCompatSessionState(continued, nextResp)
		require.NoError(t, err)
		require.NoError(t, saveResponsesCompatSession(context.Background(), ctx, nil, local, nextState, time.Hour))
		previousID = nextID
	}
}

func TestResponsesCompatSessionScopeSeparatesAPIKeysAndGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	first, _ := gin.CreateTestContext(nil)
	first.Set("api_key", &APIKey{ID: 9, GroupID: int64PtrForCompatTest(4)})
	otherKey, _ := gin.CreateTestContext(nil)
	otherKey.Set("api_key", &APIKey{ID: 10, GroupID: int64PtrForCompatTest(4)})
	otherGroup, _ := gin.CreateTestContext(nil)
	otherGroup.Set("api_key", &APIKey{ID: 9, GroupID: int64PtrForCompatTest(5)})
	require.NotEqual(t, responsesCompatSessionCacheKey(first, "resp_1"), responsesCompatSessionCacheKey(otherKey, "resp_1"))
	require.NotEqual(t, responsesCompatSessionCacheKey(first, "resp_1"), responsesCompatSessionCacheKey(otherGroup, "resp_1"))
}

func TestPrepareResponsesCompatContinuationRequiresSavedResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	var local sync.Map
	req := &apicompat.ResponsesRequest{PreviousResponseID: "missing", Input: json.RawMessage(`[]`)}
	_, err := prepareResponsesCompatContinuation(context.Background(), ctx, nil, &local, req)
	require.Error(t, err)
	require.EqualError(t, err, "Previous response is not available for this compatibility session")
}

func int64PtrForCompatTest(value int64) *int64 { return &value }

type failingResponsesCompatStateCache struct {
	stubGatewayCache
	getErr   error
	getCalls int
	setCalls int
}

func (c *failingResponsesCompatStateCache) GetResponsesCompatState(context.Context, string) ([]byte, error) {
	c.getCalls++
	return nil, c.getErr
}

func (c *failingResponsesCompatStateCache) SetResponsesCompatState(context.Context, string, []byte, time.Duration) error {
	c.setCalls++
	return c.getErr
}

func (c *failingResponsesCompatStateCache) DeleteResponsesCompatState(context.Context, string) error {
	return c.getErr
}

func TestResponsesCompatContinuationCacheErrorIsNotReturnedToClients(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cacheErr := errors.New("redis://:cache-secret@cache.internal:6379: connection refused")
	cache := &failingResponsesCompatStateCache{getErr: cacheErr}
	body := []byte(`{"model":"gpt-5.4","previous_response_id":"resp_previous","input":"continue","stream":false}`)

	t.Run("Anthropic Responses adapter", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		service := &GatewayService{cache: cache}
		_, err := service.ForwardAsResponses(context.Background(), ctx, &Account{ID: 3, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}, body, nil)
		require.Error(t, err)
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		require.Equal(t, "server_error", gjson.Get(recorder.Body.String(), "error.type").String())
		require.Equal(t, "compat_session_unavailable", gjson.Get(recorder.Body.String(), "error.code").String())
		require.Equal(t, "Compatibility session storage is temporarily unavailable", gjson.Get(recorder.Body.String(), "error.message").String())
		require.Equal(t, "previous_response_id", gjson.Get(recorder.Body.String(), "error.param").String())
		require.NotContains(t, recorder.Body.String(), "cache-secret")
		require.NotContains(t, recorder.Body.String(), "cache.internal")
		require.NotContains(t, recorder.Body.String(), "connection refused")
	})

	t.Run("Chat Completions Responses adapter", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		service := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: &httpUpstreamRecorder{}, cache: cache}
		_, err := service.Forward(context.Background(), ctx, forceChatResponsesFallbackAccount(), body)
		require.Error(t, err)
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		require.Equal(t, "server_error", gjson.Get(recorder.Body.String(), "error.type").String())
		require.Equal(t, "compat_session_unavailable", gjson.Get(recorder.Body.String(), "error.code").String())
		require.Equal(t, "Compatibility session storage is temporarily unavailable", gjson.Get(recorder.Body.String(), "error.message").String())
		require.Equal(t, "previous_response_id", gjson.Get(recorder.Body.String(), "error.param").String())
		require.NotContains(t, recorder.Body.String(), "cache-secret")
		require.NotContains(t, recorder.Body.String(), "cache.internal")
		require.NotContains(t, recorder.Body.String(), "connection refused")
	})
}

func TestResponsesCompatContinuationMissingHistoryRemainsNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.4","previous_response_id":"resp_missing","input":"continue","stream":false}`)

	t.Run("Anthropic Responses adapter", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		service := &GatewayService{}
		_, err := service.ForwardAsResponses(context.Background(), ctx, &Account{ID: 3, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}, body, nil)
		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Equal(t, "previous_response_not_found", gjson.Get(recorder.Body.String(), "error.code").String())
		require.Equal(t, "Previous response is not available for this compatibility session", gjson.Get(recorder.Body.String(), "error.message").String())
	})

	t.Run("Chat Completions Responses adapter", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		service := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: &httpUpstreamRecorder{}}
		_, err := service.Forward(context.Background(), ctx, forceChatResponsesFallbackAccount(), body)
		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Equal(t, "previous_response_not_found", gjson.Get(recorder.Body.String(), "error.code").String())
		require.Equal(t, "Previous response is not available for this compatibility session", gjson.Get(recorder.Body.String(), "error.message").String())
	})
}

func TestResponsesCompatToolOutputValidationMatchesFrozenT0(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	call := json.RawMessage(`{"type":"function_call","id":"item_call_1","call_id":"call_1","name":"exec","arguments":"{}"}`)
	priorOutput := json.RawMessage(`{"type":"function_call_output","call_id":"call_1","output":"done","is_error":false}`)
	state := &responsesCompatSessionState{
		ResponseID: "resp_tool_validation",
		HistoryInput: []json.RawMessage{
			json.RawMessage(`{"type":"message","role":"user","content":"run"}`), call, priorOutput,
		},
		RequestInput: []json.RawMessage{json.RawMessage(`{"type":"message","role":"user","content":"run"}`)},
		OutputInput:  []json.RawMessage{call, priorOutput},
	}
	var local sync.Map
	local.Store(responsesCompatSessionLocalKey(ctx, state.ResponseID), &responsesCompatLocalBinding{State: *state, ExpiresAt: time.Now().Add(time.Hour)})

	tests := []struct {
		name      string
		input     string
		wantCode  string
		wantItems int
	}{
		{name: "identical tool result is deduplicated", input: `[{"type":"function_call_output","call_id":"call_1","output":"done","is_error":false}]`, wantItems: 3},
		{name: "unknown call is rejected", input: `[{"type":"function_call_output","call_id":"call_unknown","output":"done"}]`, wantCode: "invalid_tool_output"},
		{name: "conflicting output is rejected", input: `[{"type":"function_call_output","call_id":"call_1","output":"different","is_error":false}]`, wantCode: "invalid_tool_output"},
		{name: "conflicting error flag is rejected", input: `[{"type":"function_call_output","call_id":"call_1","output":"done","is_error":true}]`, wantCode: "invalid_tool_output"},
		{name: "nonboolean error flag is rejected", input: `[{"type":"function_call_output","call_id":"call_1","output":"done","is_error":"true"}]`, wantCode: "invalid_tool_output"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &apicompat.ResponsesRequest{PreviousResponseID: state.ResponseID, Input: json.RawMessage(tt.input)}
			_, err := prepareResponsesCompatContinuation(context.Background(), ctx, nil, &local, req)
			if tt.wantCode == "" {
				require.NoError(t, err)
				var items []json.RawMessage
				require.NoError(t, json.Unmarshal(req.Input, &items))
				require.Len(t, items, tt.wantItems)
				return
			}
			require.Error(t, err)
			var compatErr *responsesCompatError
			require.ErrorAs(t, err, &compatErr)
			require.Equal(t, tt.wantCode, compatErr.code)
		})
	}
}

func TestResponsesCompatOutputItemsPreserveCompactionSummaryFromFrozenT0(t *testing.T) {
	for _, itemType := range []string{"compaction", "compaction_summary"} {
		t.Run(itemType, func(t *testing.T) {
			items, err := responsesCompatOutputItems([]apicompat.ResponsesOutput{{
				Type: itemType, ID: "item_compact_1", Status: "completed",
				Summary:          []apicompat.ResponsesSummary{{Type: "summary_text", Text: "portable summary"}},
				EncryptedContent: "opaque-payload",
			}})
			require.NoError(t, err)
			require.Len(t, items, 1)
			require.Equal(t, itemType, gjson.GetBytes(items[0], "type").String())
			require.Equal(t, "item_compact_1", gjson.GetBytes(items[0], "id").String())
			require.Equal(t, "completed", gjson.GetBytes(items[0], "status").String())
			require.Equal(t, "portable summary", gjson.GetBytes(items[0], "summary.0.text").String())
			require.Equal(t, "opaque-payload", gjson.GetBytes(items[0], "encrypted_content").String())
		})
	}
}

type fixedResponsesCompatStateCache struct {
	stubGatewayCache
	payload []byte
	err     error
}

func (c *fixedResponsesCompatStateCache) GetResponsesCompatState(context.Context, string) ([]byte, error) {
	return c.payload, c.err
}

func (c *fixedResponsesCompatStateCache) SetResponsesCompatState(context.Context, string, []byte, time.Duration) error {
	return nil
}

func (c *fixedResponsesCompatStateCache) DeleteResponsesCompatState(context.Context, string) error {
	return nil
}

func TestLoadResponsesCompatSessionRejectsCorruptAndMismatchedIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	requestedID := "resp_requested"

	t.Run("corrupt JSON", func(t *testing.T) {
		cache := &fixedResponsesCompatStateCache{payload: []byte(`{"response_id":`)}
		_, err := loadResponsesCompatSession(context.Background(), ctx, cache, nil, requestedID)
		require.Error(t, err)
		var compatErr *responsesCompatError
		require.ErrorAs(t, err, &compatErr)
		require.Equal(t, "compat_session_corrupt", compatErr.code)
	})

	t.Run("shared identity must match exactly", func(t *testing.T) {
		payload, err := json.Marshal(responsesCompatSessionState{ResponseID: requestedID + " "})
		require.NoError(t, err)
		cache := &fixedResponsesCompatStateCache{payload: payload}
		_, err = loadResponsesCompatSession(context.Background(), ctx, cache, nil, requestedID)
		require.Error(t, err)
		var compatErr *responsesCompatError
		require.ErrorAs(t, err, &compatErr)
		require.Equal(t, "compat_session_corrupt", compatErr.code)
	})

	t.Run("local identity must match exactly", func(t *testing.T) {
		var local sync.Map
		local.Store(responsesCompatSessionLocalKey(ctx, requestedID), &responsesCompatLocalBinding{
			State: responsesCompatSessionState{ResponseID: requestedID + " "}, ExpiresAt: time.Now().Add(time.Hour),
		})
		_, err := loadResponsesCompatSession(context.Background(), ctx, nil, &local, requestedID)
		require.Error(t, err)
		var compatErr *responsesCompatError
		require.ErrorAs(t, err, &compatErr)
		require.Equal(t, "compat_session_corrupt", compatErr.code)
	})
}

func TestLoadResponsesCompatSessionNonPositiveExpiryUsesFrozenT0Default(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	for _, expiry := range []int64{0, -1} {
		t.Run(fmt.Sprintf("expires_at_%d", expiry), func(t *testing.T) {
			payload, err := json.Marshal(responsesCompatSessionState{ResponseID: "resp_expiry", ExpiresAtUnix: expiry})
			require.NoError(t, err)
			cache := &fixedResponsesCompatStateCache{payload: payload}
			state, err := loadResponsesCompatSession(context.Background(), ctx, cache, nil, "resp_expiry")
			require.NoError(t, err)
			require.NotNil(t, state)
			require.Equal(t, expiry, state.ExpiresAtUnix)
		})
	}
}

type recordingResponsesCompatStateCache struct {
	stubGatewayCache
	setTTL time.Duration
}

func (c *recordingResponsesCompatStateCache) GetResponsesCompatState(context.Context, string) ([]byte, error) {
	return nil, nil
}

func (c *recordingResponsesCompatStateCache) SetResponsesCompatState(_ context.Context, _ string, _ []byte, ttl time.Duration) error {
	c.setTTL = ttl
	return nil
}

func (c *recordingResponsesCompatStateCache) DeleteResponsesCompatState(context.Context, string) error {
	return nil
}

func TestResponsesCompatSessionTTLsMatchFrozenT0(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &apicompat.ResponsesResponse{ID: "resp_ttl", Output: []apicompat.ResponsesOutput{{Type: "message", Content: []apicompat.ResponsesContentPart{{Type: "output_text", Text: "ok"}}}}}
	req := &apicompat.ResponsesRequest{Model: "glm-5", Input: json.RawMessage(`"hello"`)}

	openAICache := &recordingResponsesCompatStateCache{}
	cfg := rawChatCompletionsTestConfig()
	cfg.Gateway.OpenAIWS.StickyResponseIDTTLSeconds = 97
	openAIService := &OpenAIGatewayService{cfg: cfg, cache: openAICache}
	require.NoError(t, openAIService.saveResponsesCompatResponse(context.Background(), ctx, req, resp))
	require.GreaterOrEqual(t, openAICache.setTTL, 96*time.Second)
	require.LessOrEqual(t, openAICache.setTTL, 97*time.Second)

	anthropicCache := &recordingResponsesCompatStateCache{}
	anthropicService := &GatewayService{cache: anthropicCache}
	require.NoError(t, anthropicService.saveResponsesCompatResponse(context.Background(), ctx, req, resp))
	require.GreaterOrEqual(t, anthropicCache.setTTL, time.Hour-time.Second)
	require.LessOrEqual(t, anthropicCache.setTTL, time.Hour)
}

func TestResponsesCompatLocalSessionIsReclaimedWhenUnusedUntilExpiry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	local := &sync.Map{}
	state := responsesCompatSessionState{ResponseID: "resp_idle_expiry", HistoryInput: []json.RawMessage{json.RawMessage(`{"type":"message","content":"sensitive history"}`)}}
	ttl := 25 * time.Millisecond
	require.NoError(t, saveResponsesCompatSession(context.Background(), ctx, nil, local, state, ttl))
	key := responsesCompatSessionLocalKey(ctx, state.ResponseID)
	_, exists := local.Load(key)
	require.True(t, exists)

	require.Eventually(t, func() bool {
		_, exists := local.Load(key)
		return !exists
	}, time.Second, 5*time.Millisecond, "expired local session should be reclaimed even if no continuation looks it up")
}

func TestResponsesCompatLocalSessionExpiryDoesNotDeleteReplacement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	local := &sync.Map{}
	key := responsesCompatSessionLocalKey(ctx, "resp_replaced_expiry")
	require.NoError(t, saveResponsesCompatSession(context.Background(), ctx, nil, local, responsesCompatSessionState{ResponseID: "resp_replaced_expiry", Model: "old"}, 50*time.Millisecond))
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, saveResponsesCompatSession(context.Background(), ctx, nil, local, responsesCompatSessionState{ResponseID: "resp_replaced_expiry", Model: "new"}, 250*time.Millisecond))

	time.Sleep(80 * time.Millisecond)
	raw, ok := local.Load(key)
	require.True(t, ok, "the old expiry callback must not remove the replacement binding")
	binding, ok := raw.(*responsesCompatLocalBinding)
	require.True(t, ok)
	require.Equal(t, "new", binding.State.Model)
	require.Eventually(t, func() bool {
		_, ok := local.Load(key)
		return !ok
	}, time.Second, 5*time.Millisecond, "replacement should still be reclaimed at its own expiry")
}

func TestResponsesCompatExpiredReadDoesNotDeleteReplacement(t *testing.T) {
	var local sync.Map
	key := "group:1:key:2\x00resp_expired_read"
	old := &responsesCompatLocalBinding{
		State:     responsesCompatSessionState{ResponseID: "resp_expired_read", Model: "old"},
		ExpiresAt: time.Now().Add(-time.Second),
	}
	local.Store(key, old)
	observed, ok := local.Load(key)
	require.True(t, ok)

	// Model the interleaving where a load observes the expired value, then a
	// concurrent response replaces it before expiry cleanup runs.
	replacement := &responsesCompatLocalBinding{
		State:     responsesCompatSessionState{ResponseID: "resp_expired_read", Model: "new"},
		ExpiresAt: time.Now().Add(time.Hour),
	}
	local.Store(key, replacement)
	require.False(t, deleteResponsesCompatLocalBindingIfUnchanged(&local, key, observed))

	current, ok := local.Load(key)
	require.True(t, ok)
	require.Same(t, replacement, current)
}

func TestResponsesCompatSessionDoesNotTouchLegacyCompactPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &failingResponsesCompatStateCache{getErr: errors.New("compact must not load compatibility history")}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
	req := &apicompat.ResponsesRequest{PreviousResponseID: "resp_compact", Input: json.RawMessage(`"compact"`)}

	openAIService := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), cache: cache}
	require.NoError(t, openAIService.prepareResponsesCompatContinuation(context.Background(), ctx, req))
	require.NoError(t, openAIService.saveResponsesCompatResponse(context.Background(), ctx, req, &apicompat.ResponsesResponse{ID: "resp_compact"}))
	service := &GatewayService{cache: cache}
	require.NoError(t, service.prepareResponsesCompatContinuation(context.Background(), ctx, req))
	require.NoError(t, service.saveResponsesCompatResponse(context.Background(), ctx, req, &apicompat.ResponsesResponse{ID: "resp_compact"}))
	require.Zero(t, cache.getCalls)
	require.Zero(t, cache.setCalls)
}

func TestResponsesCompatSessionDoesNotTouchNativeCompactionV2(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &failingResponsesCompatStateCache{getErr: errors.New("native v2 must not load compatibility history")}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	MarkOpenAINativeCompactionV2(ctx)
	req := &apicompat.ResponsesRequest{
		PreviousResponseID: "resp_native_v2",
		Input:              json.RawMessage(`[{"type":"compaction_trigger"}]`),
	}

	openAIService := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), cache: cache}
	require.NoError(t, openAIService.prepareResponsesCompatContinuation(context.Background(), ctx, req))
	require.NoError(t, openAIService.saveResponsesCompatResponse(context.Background(), ctx, req, &apicompat.ResponsesResponse{ID: "resp_native_v2_next"}))
	service := &GatewayService{cache: cache}
	require.NoError(t, service.prepareResponsesCompatContinuation(context.Background(), ctx, req))
	require.NoError(t, service.saveResponsesCompatResponse(context.Background(), ctx, req, &apicompat.ResponsesResponse{ID: "resp_native_v2_next"}))

	require.Equal(t, "resp_native_v2", req.PreviousResponseID, "native compaction v2 must not be rewritten as compatibility history")
	require.JSONEq(t, `[{"type":"compaction_trigger"}]`, string(req.Input))
	require.Zero(t, cache.getCalls)
	require.Zero(t, cache.setCalls)
}

// Run this test in two independent go test processes against a disposable,
// loopback-only Redis: first with role=write, then with role=read.
type p3ExternalRedisCompatCache struct {
	stubGatewayCache
	client *redis.Client
}

func (c *p3ExternalRedisCompatCache) GetResponsesCompatState(ctx context.Context, key string) ([]byte, error) {
	payload, err := c.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	return payload, err
}

func (c *p3ExternalRedisCompatCache) SetResponsesCompatState(ctx context.Context, key string, payload []byte, ttl time.Duration) error {
	return c.client.Set(ctx, key, payload, ttl).Err()
}

func (c *p3ExternalRedisCompatCache) DeleteResponsesCompatState(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}

func TestResponsesCompatSessionExternalRedisSeparateProcesses(t *testing.T) {
	addr := os.Getenv("P3_RESPONSES_REDIS_ADDR")
	if addr == "" {
		t.Skip("requires an explicitly supplied isolated loopback Redis")
	}
	host, _, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	require.NotNil(t, net.ParseIP(host), "Redis test address must be a literal loopback IP")
	require.True(t, net.ParseIP(host).IsLoopback(), "this test must never use a remote or production Redis")
	role := os.Getenv("P3_RESPONSES_REDIS_ROLE")
	require.Contains(t, []string{"write", "read"}, role)

	client := redis.NewClient(&redis.Options{
		Addr: addr, MaxRetries: -1,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second,
	})
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, client.Ping(ctx).Err())

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	groupID := int64(17)
	c.Set("api_key", &APIKey{ID: 900, GroupID: &groupID})
	cache := &p3ExternalRedisCompatCache{client: client}
	const responseID = "resp_p3_isolated_redis"
	cacheKey := responsesCompatSessionCacheKey(c, responseID)

	if role == "write" {
		require.NoError(t, cache.DeleteResponsesCompatState(ctx, cacheKey))
		service := &GatewayService{cache: cache}
		request := &apicompat.ResponsesRequest{
			Model: "synthetic-claude", Instructions: "Skills UTF-8 保留",
			Input: json.RawMessage(`"synthetic user"`),
			Tools: []apicompat.ResponsesTool{{Type: "function", Name: "unified_exec", Parameters: json.RawMessage(`{"type":"object"}`)}},
		}
		response := &apicompat.ResponsesResponse{ID: responseID, Output: []apicompat.ResponsesOutput{
			{Type: "reasoning", Summary: []apicompat.ResponsesSummary{{Type: "summary_text", Text: "synthetic reasoning"}}},
			{Type: "function_call", ID: "item_p3", CallID: "call_p3", Name: "unified_exec", Arguments: `{"command":"synthetic"}`},
		}}
		require.NoError(t, service.saveResponsesCompatResponse(ctx, c, request, response))
		t.Log("separate-process Redis write completed with synthetic session history")
		return
	}

	// The read process has a fresh sync.Map and reconstructs history only from Redis.
	service := &OpenAIGatewayService{cache: cache}
	request := &apicompat.ResponsesRequest{
		PreviousResponseID: responseID,
		Input: json.RawMessage(`[
			{"type":"function_call_output","item_id":"item_p3","output":"synthetic tool error","is_error":true},
			{"type":"function_call_output","item_id":"item_p3","output":"synthetic tool error","is_error":true}
		]`),
	}
	require.NoError(t, service.prepareResponsesCompatContinuation(ctx, c, request))
	require.Empty(t, request.PreviousResponseID)
	require.Equal(t, "Skills UTF-8 保留", request.Instructions)
	require.Len(t, request.Tools, 1)
	require.Equal(t, int64(4), gjson.GetBytes(request.Input, "#").Int())
	require.Equal(t, "call_p3", gjson.GetBytes(request.Input, "3.call_id").String())
	require.True(t, gjson.GetBytes(request.Input, "3.is_error").Bool())
	require.NoError(t, cache.DeleteResponsesCompatState(ctx, cacheKey))
	t.Log("separate-process Redis continuation passed; duplicate identical tool result replayed once")
}
