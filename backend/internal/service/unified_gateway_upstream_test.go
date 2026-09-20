package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type unifiedGatewayUpstreamAccountRepoStub struct {
	AccountRepository
	account *Account
	err     error
}

func (s *unifiedGatewayUpstreamAccountRepoStub) GetByID(context.Context, int64) (*Account, error) {
	return s.account, s.err
}

type unifiedGatewayUpstreamHTTPStub struct {
	mu        sync.Mutex
	response  func(*http.Request, []byte) (*http.Response, error)
	request   *http.Request
	body      []byte
	proxyURL  string
	accountID int64
}

func (s *unifiedGatewayUpstreamHTTPStub) Do(req *http.Request, proxyURL string, accountID int64, _ int) (*http.Response, error) {
	return s.invoke(req, proxyURL, accountID)
}

func (s *unifiedGatewayUpstreamHTTPStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.invoke(req, proxyURL, accountID)
}

func (s *unifiedGatewayUpstreamHTTPStub) invoke(req *http.Request, proxyURL string, accountID int64) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.request = req.Clone(req.Context())
	s.body = append([]byte(nil), body...)
	s.proxyURL = proxyURL
	s.accountID = accountID
	s.mu.Unlock()
	if s.response == nil {
		return nil, errors.New("unexpected upstream call")
	}
	return s.response(req, body)
}

func unifiedGatewayUpstreamTestConfig() *config.Config {
	return &config.Config{
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{
				AllowPrivateHosts: true,
				AllowInsecureHTTP: true,
			},
		},
		Gateway: config.GatewayConfig{
			MaxBodySize:                  1 << 20,
			UpstreamResponseReadMaxBytes: 1 << 20,
		},
	}
}

func unifiedGatewayUpstreamTestAccount(id int64, platform, baseURL string) *Account {
	return &Account{
		ID:       id,
		Platform: platform,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-unified-test",
			"base_url": baseURL,
		},
	}
}

func unifiedGatewayUpstreamTestSelection(accountID int64, endpoint string, basis UnifiedRateBasis, billingMode BillingMode) UnifiedGatewayRouteSelection {
	return UnifiedGatewayRouteSelection{
		Target: UnifiedGatewayRouteTarget{
			PublicModel: "public-model",
			Endpoint:    endpoint,
			BillingMode: string(billingMode),
			RateBasis:   basis,
		},
		Binding: UnifiedGatewayAccountBinding{
			AccountID: accountID,
		},
	}
}

func unifiedGatewayUpstreamTestExecutor(account *Account, upstream *unifiedGatewayUpstreamHTTPStub, cfg *config.Config) *HTTPUnifiedGatewayUpstreamExecutor {
	repo := &unifiedGatewayUpstreamAccountRepoStub{account: account}
	return NewHTTPUnifiedGatewayUpstreamExecutor(repo, upstream, nil, cfg)
}

func TestHTTPUnifiedGatewayUpstreamExecutorEligibleUsesLiveAccountState(t *testing.T) {
	account := unifiedGatewayUpstreamTestAccount(40, PlatformOpenAI, "https://api.example.test")
	account.Status = StatusActive
	account.Schedulable = true
	executor := unifiedGatewayUpstreamTestExecutor(account, &unifiedGatewayUpstreamHTTPStub{}, &config.Config{})
	selection := unifiedGatewayUpstreamTestSelection(account.ID, UnifiedGatewayEndpointChatCompletions, UnifiedRateBasisToken, BillingModeToken)

	eligible, err := executor.Eligible(context.Background(), selection, UnifiedGatewayRequest{})
	require.NoError(t, err)
	require.True(t, eligible)

	account.Schedulable = false
	eligible, err = executor.Eligible(context.Background(), selection, UnifiedGatewayRequest{})
	require.NoError(t, err)
	require.False(t, eligible)

	account.Schedulable = true
	account.Status = "disabled"
	eligible, err = executor.Eligible(context.Background(), selection, UnifiedGatewayRequest{})
	require.NoError(t, err)
	require.False(t, eligible)

	account.Status = StatusActive
	until := time.Now().Add(time.Minute)
	account.OverloadUntil = &until
	eligible, err = executor.Eligible(context.Background(), selection, UnifiedGatewayRequest{})
	require.NoError(t, err)
	require.False(t, eligible)
}

func TestHTTPUnifiedGatewayUpstreamExecutorForwardGeminiCompatibility(t *testing.T) {
	account := &Account{
		ID:       44,
		Platform: PlatformGemini,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"access_token": "ya29.unified-test",
			"project_id":   "unified-project",
		},
	}
	upstream := &geminiCompatHTTPUpstreamStub{
		response: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				`data: {"response":{"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3}}}` + "\n\n" +
					"data: [DONE]\n\n",
			)),
		},
	}
	gemini := &GeminiMessagesCompatService{
		tokenProvider: &GeminiTokenProvider{},
		httpUpstream:  upstream,
		cfg:           &config.Config{},
	}
	executor := unifiedGatewayUpstreamTestExecutor(account, nil, &config.Config{Gateway: config.GatewayConfig{MaxBodySize: 1 << 20}})
	executor.httpUpstream = upstream
	executor.SetGeminiCompatService(gemini)
	selection := unifiedGatewayUpstreamTestSelection(account.ID, UnifiedGatewayEndpointChatCompletions, UnifiedRateBasisToken, BillingModeToken)
	selection.Target.UpstreamModel = "gemini-2.5-flash"

	result, err := executor.Forward(context.Background(), selection, UnifiedGatewayRequest{
		PublicModel: "gemini-2.5-flash",
		RawBody:     []byte(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`),
	})

	require.NoError(t, err)
	require.True(t, result.Delivered)
	require.Equal(t, float64(10), result.MeasuredUnits)
	require.Contains(t, string(result.ResponseBody), "chat.completion")
	require.NotNil(t, upstream.lastReq)
	require.Contains(t, upstream.lastReq.URL.String(), "streamGenerateContent")
}

func TestHTTPUnifiedGatewayUpstreamExecutorForwardChatCompletions(t *testing.T) {
	account := unifiedGatewayUpstreamTestAccount(41, PlatformOpenAI, "https://api.example.test/relay/v1")
	account.Credentials["model_mapping"] = map[string]any{"public-model": "mapped-model"}
	upstream := &unifiedGatewayUpstreamHTTPStub{
		response: func(req *http.Request, body []byte) (*http.Response, error) {
			require.Equal(t, "/relay/v1/chat/completions", req.URL.Path)
			require.Equal(t, "Bearer sk-unified-test", req.Header.Get("Authorization"))
			require.Equal(t, "application/json", req.Header.Get("Content-Type"))
			require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(req.Context()))
			require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
			require.Equal(t, `{"model":"mapped-model","messages":[]}`, string(body))
			return unifiedGatewayUpstreamJSONResponse(http.StatusOK, `{"id":"chatcmpl_test","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":4,"completion_tokens":6}}`, "upstream-chat-1"), nil
		},
	}
	executor := unifiedGatewayUpstreamTestExecutor(account, upstream, &config.Config{})
	selection := unifiedGatewayUpstreamTestSelection(account.ID, UnifiedGatewayEndpointChatCompletions, UnifiedRateBasisToken, BillingModeToken)
	result, err := executor.Forward(context.Background(), selection, UnifiedGatewayRequest{
		PublicModel: "public-model",
		RawBody:     []byte(`{"model":"public-model","messages":[]}`),
	})

	require.NoError(t, err)
	require.True(t, result.Delivered)
	require.False(t, result.Pending)
	require.Equal(t, float64(10), result.MeasuredUnits)
	require.Equal(t, "upstream-chat-1", result.UpstreamRequestID)
}

func TestHTTPUnifiedGatewayUpstreamExecutorForwardResponsesUsesMappedEndpoint(t *testing.T) {
	account := unifiedGatewayUpstreamTestAccount(42, PlatformDeepseek, "https://api.example.test")
	upstream := &unifiedGatewayUpstreamHTTPStub{
		response: func(req *http.Request, body []byte) (*http.Response, error) {
			require.Equal(t, "/responses", req.URL.Path)
			require.Equal(t, `{"model":"deepseek-reasoner","input":[]}`, string(body))
			return unifiedGatewayUpstreamJSONResponse(http.StatusOK, `{"id":"resp_test","usage":{"input_tokens":8,"output_tokens":2}}`, "upstream-resp-1"), nil
		},
	}
	executor := unifiedGatewayUpstreamTestExecutor(account, upstream, &config.Config{})
	selection := unifiedGatewayUpstreamTestSelection(account.ID, UnifiedGatewayEndpointResponses, UnifiedRateBasisToken, BillingModeToken)
	selection.Target.UpstreamModel = "deepseek-reasoner"
	result, err := executor.Forward(context.Background(), selection, UnifiedGatewayRequest{
		PublicModel: "public-model",
		RawBody:     []byte(`{"model":"public-model","input":[]}`),
	})

	require.NoError(t, err)
	require.True(t, result.Delivered)
	require.Equal(t, float64(10), result.MeasuredUnits)
	// The provider request ID is taken from the response header when present;
	// the response object's ID remains part of the forwarded payload and is
	// only a fallback when no request-id header is available.
	require.Equal(t, "upstream-resp-1", result.UpstreamRequestID)
}

func TestHTTPUnifiedGatewayUpstreamExecutorExtractsUsageFromResponsesSSE(t *testing.T) {
	account := unifiedGatewayUpstreamTestAccount(49, PlatformOpenAI, "https://api.example.test")
	upstream := &unifiedGatewayUpstreamHTTPStub{
		response: func(*http.Request, []byte) (*http.Response, error) {
			body := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\"}}\n\n" +
				"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":3,\"output_tokens\":4}}}\n\n"
			return unifiedGatewayUpstreamSSEResponse(body, "sse-1"), nil
		},
	}
	executor := unifiedGatewayUpstreamTestExecutor(account, upstream, &config.Config{})
	selection := unifiedGatewayUpstreamTestSelection(account.ID, UnifiedGatewayEndpointResponses, UnifiedRateBasisToken, BillingModeToken)
	result, err := executor.Forward(context.Background(), selection, UnifiedGatewayRequest{
		PublicModel: "public-model",
		RawBody:     []byte(`{"input":[]}`),
	})

	require.NoError(t, err)
	require.True(t, result.Delivered)
	require.Equal(t, float64(7), result.MeasuredUnits)
}

func TestHTTPUnifiedGatewayUpstreamExecutorForwardImagesCountsCompletedOutputs(t *testing.T) {
	account := unifiedGatewayUpstreamTestAccount(43, PlatformOpenAI, "https://api.example.test")
	upstream := &unifiedGatewayUpstreamHTTPStub{
		response: func(req *http.Request, body []byte) (*http.Response, error) {
			require.Equal(t, "/v1/images/generations", req.URL.Path)
			require.Equal(t, `{"model":"image-model","prompt":"a test"}`, string(body))
			return unifiedGatewayUpstreamJSONResponse(http.StatusOK, `{"data":[{"b64_json":"one"},{"url":"https://cdn.example.test/two"}]}`, "image-1"), nil
		},
	}
	executor := unifiedGatewayUpstreamTestExecutor(account, upstream, &config.Config{})
	selection := unifiedGatewayUpstreamTestSelection(account.ID, UnifiedGatewayEndpointImages, UnifiedRateBasisImage, BillingModeImage)
	selection.Target.UpstreamModel = "image-model"
	result, err := executor.Forward(context.Background(), selection, UnifiedGatewayRequest{
		PublicModel: "public-model",
		RawBody:     []byte(`{"model":"public-model","prompt":"a test"}`),
	})

	require.NoError(t, err)
	require.True(t, result.Delivered)
	require.Equal(t, float64(2), result.MeasuredUnits)
}

func TestHTTPUnifiedGatewayUpstreamExecutorForwardAnthropicAPIKeyThroughSharedAdapter(t *testing.T) {
	account := &Account{
		ID:       50,
		Platform: PlatformAnthropic,
		Type:     AccountTypeAPIKey,
		Status:   StatusActive,
		Credentials: map[string]any{
			"api_key":       "sk-anthropic-unified-test",
			"base_url":      "https://api.anthropic.example",
			"model_mapping": map[string]any{"claude-public": "claude-upstream"},
		},
	}
	upstream := &unifiedGatewayUpstreamHTTPStub{
		response: func(req *http.Request, body []byte) (*http.Response, error) {
			require.Equal(t, "/v1/messages", req.URL.Path)
			require.Equal(t, "true", req.URL.Query().Get("beta"))
			require.Equal(t, "sk-anthropic-unified-test", req.Header.Get("x-api-key"))
			require.Equal(t, "claude-upstream", gjson.GetBytes(body, "model").String())
			require.True(t, gjson.GetBytes(body, "stream").Bool())
			return unifiedGatewayUpstreamSSEResponse(strings.Join([]string{
				"event: message_start",
				`data: {"type":"message_start","message":{"id":"msg_unified_claude","type":"message","role":"assistant","content":[],"model":"claude-upstream","stop_reason":null,"usage":{"input_tokens":7}}}`,
				"",
				"event: content_block_start",
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				"",
				"event: content_block_delta",
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
				"",
				"event: message_delta",
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
				"",
				"event: message_stop",
				`data: {"type":"message_stop"}`,
				"",
			}, "\n"), "anthropic-unified-1"), nil
		},
	}
	executor := unifiedGatewayUpstreamTestExecutor(account, upstream, unifiedGatewayUpstreamTestConfig())
	executor.SetAnthropicGatewayService(&GatewayService{
		httpUpstream: upstream,
		cfg:          unifiedGatewayUpstreamTestConfig(),
	})
	selection := unifiedGatewayUpstreamTestSelection(account.ID, UnifiedGatewayEndpointChatCompletions, UnifiedRateBasisToken, BillingModeToken)
	selection.Target.PublicModel = "claude-public"
	selection.Target.UpstreamModel = "claude-upstream"

	result, err := executor.Forward(context.Background(), selection, UnifiedGatewayRequest{
		PublicModel: "claude-public",
		RawBody:     []byte(`{"model":"claude-public","messages":[{"role":"user","content":"hi"}],"stream":false}`),
	})

	require.NoError(t, err)
	require.True(t, result.Delivered)
	require.Equal(t, float64(10), result.MeasuredUnits)
	require.Equal(t, "msg_unified_claude", result.UpstreamRequestID)
	require.Contains(t, string(result.ResponseBody), "chat.completion")
	require.Contains(t, string(result.ResponseBody), "claude-public")
	require.Contains(t, string(result.ResponseBody), "ok")
}

func TestHTTPUnifiedGatewayUpstreamExecutorForwardAnthropicAPIKeyResponses(t *testing.T) {
	account := &Account{
		ID:       51,
		Platform: PlatformAnthropic,
		Type:     AccountTypeAPIKey,
		Status:   StatusActive,
		Credentials: map[string]any{
			"api_key":       "sk-anthropic-responses-test",
			"base_url":      "https://api.anthropic.example",
			"model_mapping": map[string]any{"claude-responses": "claude-upstream"},
		},
	}
	upstream := &unifiedGatewayUpstreamHTTPStub{
		response: func(req *http.Request, body []byte) (*http.Response, error) {
			require.Equal(t, "/v1/messages", req.URL.Path)
			require.Equal(t, "sk-anthropic-responses-test", req.Header.Get("x-api-key"))
			require.Equal(t, "claude-upstream", gjson.GetBytes(body, "model").String())
			require.True(t, gjson.GetBytes(body, "stream").Bool())
			return unifiedGatewayUpstreamSSEResponse(strings.Join([]string{
				"event: message_start",
				`data: {"type":"message_start","message":{"id":"msg_unified_responses","type":"message","role":"assistant","content":[],"model":"claude-upstream","stop_reason":null,"usage":{"input_tokens":5}}}`,
				"",
				"event: content_block_start",
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				"",
				"event: content_block_delta",
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
				"",
				"event: content_block_stop",
				`data: {"type":"content_block_stop","index":0}`,
				"",
				"event: message_delta",
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
				"",
				"event: message_stop",
				`data: {"type":"message_stop"}`,
				"",
			}, "\n"), "anthropic-responses-1"), nil
		},
	}
	executor := unifiedGatewayUpstreamTestExecutor(account, upstream, unifiedGatewayUpstreamTestConfig())
	executor.SetAnthropicGatewayService(&GatewayService{
		httpUpstream: upstream,
		cfg:          unifiedGatewayUpstreamTestConfig(),
	})
	selection := unifiedGatewayUpstreamTestSelection(account.ID, UnifiedGatewayEndpointResponses, UnifiedRateBasisToken, BillingModeToken)
	selection.Target.PublicModel = "claude-responses"
	selection.Target.UpstreamModel = "claude-upstream"

	result, err := executor.Forward(context.Background(), selection, UnifiedGatewayRequest{
		PublicModel: "claude-responses",
		RawBody:     []byte(`{"model":"claude-responses","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"stream":false}`),
	})

	require.NoError(t, err)
	require.True(t, result.Delivered)
	require.Equal(t, float64(7), result.MeasuredUnits)
	require.Contains(t, string(result.ResponseBody), "claude-responses")
	require.Contains(t, string(result.ResponseBody), "ok")
}

func TestUnifiedGatewayCompatAccountOverridesSameNameAccountMapping(t *testing.T) {
	account := &Account{Credentials: map[string]any{
		"model_mapping": map[string]any{"claude-public": "stale-account-model"},
	}}
	compat := unifiedGatewayCompatAccount(account, "claude-public", "claude-public")
	require.NotSame(t, account, compat)
	require.Equal(t, "claude-public", compat.GetMappedModel("claude-public"))
	require.Equal(t, "stale-account-model", account.GetMappedModel("claude-public"))
}

func TestHTTPUnifiedGatewayUpstreamExecutorAdaptsVolcengineArkImages(t *testing.T) {
	account := unifiedGatewayUpstreamTestAccount(431, PlatformOpenAI, "https://ark.cn-beijing.volces.com/api/coding/v3")
	account.Extra = map[string]any{"provider": openAIProviderVolcengineArk}
	upstream := &unifiedGatewayUpstreamHTTPStub{
		response: func(req *http.Request, body []byte) (*http.Response, error) {
			require.Equal(t, "https://ark.cn-beijing.volces.com/api/v3/images/generations", req.URL.String())
			require.Equal(t, "doubao-seedream-4-0-250828", gjson.GetBytes(body, "model").String())
			require.Equal(t, "draw a cat", gjson.GetBytes(body, "prompt").String())
			require.False(t, gjson.GetBytes(body, "quality").Exists())
			require.False(t, gjson.GetBytes(body, "background").Exists())
			require.Equal(t, "b64_json", gjson.GetBytes(body, "response_format").String())
			return unifiedGatewayUpstreamJSONResponse(http.StatusOK, `{"data":[{"b64_json":"aGVsbG8="}]}`, "ark-image-1"), nil
		},
	}
	executor := unifiedGatewayUpstreamTestExecutor(account, upstream, &config.Config{})
	selection := unifiedGatewayUpstreamTestSelection(account.ID, UnifiedGatewayEndpointImages, UnifiedRateBasisImage, BillingModeImage)
	selection.Target.UpstreamModel = "doubao-seedream-4-0-250828"
	result, err := executor.Forward(context.Background(), selection, UnifiedGatewayRequest{
		PublicModel: "public-image",
		RawBody:     []byte(`{"model":"public-image","prompt":"draw a cat","quality":"high","background":"transparent"}`),
	})

	require.NoError(t, err)
	require.True(t, result.Delivered)
	require.Equal(t, float64(1), result.MeasuredUnits)
}

func TestHTTPUnifiedGatewayUpstreamExecutorAdaptsVolcengineArkResponses(t *testing.T) {
	account := unifiedGatewayUpstreamTestAccount(432, PlatformOpenAI, "https://ark.cn-beijing.volces.com/api/coding/v3")
	account.Extra = map[string]any{
		"provider":                openAIProviderVolcengineArk,
		"openai_multimodal_model": "doubao-seed-2-0-lite-260428",
	}
	upstream := &unifiedGatewayUpstreamHTTPStub{
		response: func(req *http.Request, body []byte) (*http.Response, error) {
			require.Equal(t, "/api/coding/v3/responses", req.URL.Path)
			require.Equal(t, "doubao-seed-2-0-lite-260428", gjson.GetBytes(body, "model").String())
			require.False(t, gjson.GetBytes(body, "reasoning").Exists())
			require.False(t, gjson.GetBytes(body, "text").Exists())
			return unifiedGatewayUpstreamJSONResponse(http.StatusOK, `{"usage":{"input_tokens":2,"output_tokens":3}}`, "ark-responses-1"), nil
		},
	}
	executor := unifiedGatewayUpstreamTestExecutor(account, upstream, &config.Config{})
	selection := unifiedGatewayUpstreamTestSelection(account.ID, UnifiedGatewayEndpointResponses, UnifiedRateBasisToken, BillingModeToken)
	selection.Target.UpstreamModel = "doubao-seed-2.0-lite"
	result, err := executor.Forward(context.Background(), selection, UnifiedGatewayRequest{
		PublicModel: "public-responses",
		RawBody:     []byte(`{"model":"public-responses","input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,abc"}]}],"reasoning":{"effort":"medium"},"text":{"verbosity":"high"}}`),
	})

	require.NoError(t, err)
	require.True(t, result.Delivered)
	require.Equal(t, float64(5), result.MeasuredUnits)
}

func TestHTTPUnifiedGatewayUpstreamExecutorFailsClosedForSafetyAndUsage(t *testing.T) {
	account := unifiedGatewayUpstreamTestAccount(44, PlatformOpenAI, "http://api.example.test")
	upstream := &unifiedGatewayUpstreamHTTPStub{
		response: func(*http.Request, []byte) (*http.Response, error) {
			return unifiedGatewayUpstreamJSONResponse(http.StatusOK, `{"id":"no-usage","choices":[]}`, "no-usage"), nil
		},
	}
	executor := unifiedGatewayUpstreamTestExecutor(account, upstream, &config.Config{})
	selection := unifiedGatewayUpstreamTestSelection(account.ID, UnifiedGatewayEndpointChatCompletions, UnifiedRateBasisToken, BillingModeToken)
	result, err := executor.Forward(context.Background(), selection, UnifiedGatewayRequest{
		PublicModel: "public-model",
		RawBody:     []byte(`{"model":"public-model","messages":[]}`),
	})

	require.Error(t, err)
	require.ErrorIs(t, err, ErrUnifiedGatewayUpstreamUnsupported)
	require.False(t, result.Delivered)

	account.Credentials["base_url"] = "https://api.example.test"
	result, err = executor.Forward(context.Background(), selection, UnifiedGatewayRequest{
		PublicModel: "public-model",
		RawBody:     []byte(`{"model":"public-model","messages":[]}`),
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrUnifiedGatewayUsageMissing)
	require.False(t, result.Delivered)
}

func TestHTTPUnifiedGatewayUpstreamExecutorSupportsExplicitLocalhostTestURL(t *testing.T) {
	account := unifiedGatewayUpstreamTestAccount(45, PlatformOpenAI, "http://127.0.0.1:18080")
	upstream := &unifiedGatewayUpstreamHTTPStub{
		response: func(*http.Request, []byte) (*http.Response, error) {
			return unifiedGatewayUpstreamJSONResponse(http.StatusOK, `{"usage":{"prompt_tokens":1,"completion_tokens":1}}`, "local-1"), nil
		},
	}
	selection := unifiedGatewayUpstreamTestSelection(account.ID, UnifiedGatewayEndpointChatCompletions, UnifiedRateBasisToken, BillingModeToken)
	request := UnifiedGatewayRequest{PublicModel: "public-model", RawBody: []byte(`{"messages":[]}`)}

	withoutOptIn := unifiedGatewayUpstreamTestExecutor(account, upstream, &config.Config{})
	_, err := withoutOptIn.Forward(context.Background(), selection, request)
	require.ErrorIs(t, err, ErrUnifiedGatewayUpstreamUnsupported)

	withOptIn := unifiedGatewayUpstreamTestExecutor(account, upstream, unifiedGatewayUpstreamTestConfig())
	result, err := withOptIn.Forward(context.Background(), selection, request)
	require.NoError(t, err)
	require.True(t, result.Delivered)
	require.Equal(t, float64(2), result.MeasuredUnits)
}

func TestHTTPUnifiedGatewayUpstreamExecutorEnforcesBodyLimitsAndHTTPFailures(t *testing.T) {
	account := unifiedGatewayUpstreamTestAccount(46, PlatformOpenAI, "https://api.example.test")
	selection := unifiedGatewayUpstreamTestSelection(account.ID, UnifiedGatewayEndpointChatCompletions, UnifiedRateBasisPerRequest, BillingModePerRequest)
	upstream := &unifiedGatewayUpstreamHTTPStub{
		response: func(*http.Request, []byte) (*http.Response, error) {
			return unifiedGatewayUpstreamJSONResponse(http.StatusTooManyRequests, `{"error":{"message":"rate limited"}}`, "rate-1"), nil
		},
	}
	cfg := unifiedGatewayUpstreamTestConfig()
	cfg.Gateway.MaxBodySize = 4
	executor := unifiedGatewayUpstreamTestExecutor(account, upstream, cfg)
	_, err := executor.Forward(context.Background(), selection, UnifiedGatewayRequest{
		PublicModel: "public-model",
		RawBody:     bytes.Repeat([]byte("x"), 5),
	})
	require.ErrorIs(t, err, ErrUnifiedGatewayUpstreamRequestBodyTooLarge)

	cfg.Gateway.MaxBodySize = 1 << 20
	executor = unifiedGatewayUpstreamTestExecutor(account, upstream, cfg)
	result, err := executor.Forward(context.Background(), selection, UnifiedGatewayRequest{
		PublicModel: "public-model",
		RawBody:     []byte(`{"messages":[]}`),
	})
	require.Error(t, err)
	var statusErr *UnifiedGatewayUpstreamHTTPError
	require.ErrorAs(t, err, &statusErr)
	require.Equal(t, http.StatusTooManyRequests, statusErr.StatusCode)
	require.Equal(t, "rate-1", statusErr.RequestID)
	require.ErrorIs(t, err, ErrUnifiedGatewayUpstreamFailed)
	require.False(t, result.Delivered)

	upstream.response = func(*http.Request, []byte) (*http.Response, error) {
		return unifiedGatewayUpstreamJSONResponse(http.StatusOK, `{"usage":{"prompt_tokens":1,"completion_tokens":1}}`, "large-1"), nil
	}
	cfg.Gateway.UpstreamResponseReadMaxBytes = 4
	limitedExecutor := unifiedGatewayUpstreamTestExecutor(account, upstream, cfg)
	result, err = limitedExecutor.Forward(context.Background(), selection, UnifiedGatewayRequest{
		PublicModel: "public-model",
		RawBody:     []byte(`{"messages":[]}`),
	})
	require.ErrorIs(t, err, ErrUnifiedGatewayUpstreamResponseBodyTooLarge)
	require.False(t, result.Delivered)
}

func TestHTTPUnifiedGatewayUpstreamExecutorAppliesTimeoutToTransport(t *testing.T) {
	account := unifiedGatewayUpstreamTestAccount(48, PlatformOpenAI, "https://api.example.test")
	upstream := &unifiedGatewayUpstreamHTTPStub{
		response: func(req *http.Request, _ []byte) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		},
	}
	executor := unifiedGatewayUpstreamTestExecutor(account, upstream, &config.Config{})
	executor.upstreamTimeout = 10 * time.Millisecond
	selection := unifiedGatewayUpstreamTestSelection(account.ID, UnifiedGatewayEndpointChatCompletions, UnifiedRateBasisPerRequest, BillingModePerRequest)
	_, err := executor.Forward(context.Background(), selection, UnifiedGatewayRequest{
		PublicModel: "public-model",
		RawBody:     []byte(`{"messages":[]}`),
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorIs(t, err, ErrUnifiedGatewayUpstreamFailed)
}

type unifiedGatewayUpstreamGrokMediaStub struct{}

func (unifiedGatewayUpstreamGrokMediaStub) Forward(context.Context, *Account, UnifiedGatewayRouteSelection, UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error) {
	return UnifiedGatewayUpstreamResult{Pending: true, UpstreamRequestID: "job-1"}, nil
}

func (unifiedGatewayUpstreamGrokMediaStub) Poll(context.Context, *Account, UnifiedGatewayRouteSelection, UnifiedGatewayRequest, string) (UnifiedGatewayUpstreamResult, error) {
	return UnifiedGatewayUpstreamResult{Delivered: true, MeasuredUnits: 1, UpstreamRequestID: "job-1", ResponseBody: []byte(`{"status":"completed"}`)}, nil
}

func (unifiedGatewayUpstreamGrokMediaStub) Content(context.Context, *Account, UnifiedGatewayRouteSelection, UnifiedGatewayRequest, string) (UnifiedGatewayContentResult, error) {
	return UnifiedGatewayContentResult{
		StatusCode:  http.StatusOK,
		ContentType: "video/mp4",
		Body:        []byte("video-bytes"),
		Headers:     http.Header{"X-Content-ID": []string{"content-1"}},
	}, nil
}

func TestHTTPUnifiedGatewayUpstreamExecutorGrokMediaAsyncSeam(t *testing.T) {
	account := unifiedGatewayUpstreamTestAccount(47, PlatformGrok, "https://api.example.test")
	account.Type = AccountTypeOAuth
	upstream := &unifiedGatewayUpstreamHTTPStub{}
	executor := unifiedGatewayUpstreamTestExecutor(account, upstream, &config.Config{})
	media := unifiedGatewayUpstreamGrokMediaStub{}
	executor.SetGrokMediaAdapter(media)
	selection := unifiedGatewayUpstreamTestSelection(account.ID, UnifiedGatewayEndpointVideos, UnifiedRateBasisVideo, BillingModeVideo)
	request := UnifiedGatewayRequest{PublicModel: "grok-imagine-video", RawBody: []byte(`{"model":"grok-imagine-video"}`)}

	forwarded, err := executor.Forward(context.Background(), selection, request)
	require.NoError(t, err)
	require.True(t, forwarded.Pending)
	require.Equal(t, "job-1", forwarded.UpstreamRequestID)

	polled, err := executor.Poll(context.Background(), selection, request, forwarded.UpstreamRequestID)
	require.NoError(t, err)
	require.True(t, polled.Delivered)
	require.Equal(t, float64(1), polled.MeasuredUnits)

	content, err := executor.Content(context.Background(), selection, request, forwarded.UpstreamRequestID)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, content.StatusCode)
	require.Equal(t, "video/mp4", content.ContentType)
	require.Equal(t, []byte("video-bytes"), content.Body)
}

func unifiedGatewayUpstreamJSONResponse(status int, body, requestID string) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Header:        http.Header{"Content-Type": []string{"application/json"}, "X-Request-ID": []string{requestID}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

func unifiedGatewayUpstreamSSEResponse(body, requestID string) *http.Response {
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-ID": []string{requestID}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}
