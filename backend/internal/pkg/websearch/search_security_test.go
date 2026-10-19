package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type securityRoundTripper func(*http.Request) (*http.Response, error)

func (f securityRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func searchTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestTavilyRealSearch_BearerAndNoBodyCredential(t *testing.T) {
	for _, max := range []int{0, 3} {
		t.Run(fmt.Sprint(max), func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: securityRoundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, tavilySearchEndpoint, req.URL.String())
				require.Equal(t, http.MethodPost, req.Method)
				require.Equal(t, "Bearer tvly-fake-contract-token", req.Header.Get("Authorization"))
				require.Equal(t, "application/json", req.Header.Get("Content-Type"))
				body, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.NotContains(t, string(body), "tvly-fake-contract-token")
				var payload map[string]any
				require.NoError(t, json.Unmarshal(body, &payload))
				require.NotContains(t, payload, "api_key")
				require.Equal(t, "golang", payload["query"])
				require.Equal(t, "basic", payload["search_depth"])
				wantMax := max
				if wantMax == 0 {
					wantMax = defaultMaxResults
				}
				require.Equal(t, float64(wantMax), payload["max_results"])
				return searchTestResponse(200, `{"results":[{"url":"https://go.dev","title":"Go","content":"Go language"}]}`), nil
			})}
			resp, err := NewTavilyProvider(" \t\ntvly-fake-contract-token\r\n ", client).Search(context.Background(), SearchRequest{Query: "golang", MaxResults: max})
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			require.Equal(t, "golang", resp.Query)
			require.Equal(t, []SearchResult{{URL: "https://go.dev", Title: "Go", Snippet: "Go language"}}, resp.Results)
		})
	}
}

func TestProviderRealSearch_InvalidKeyNeverSent(t *testing.T) {
	for _, key := range []string{"fake\r\nsecret", "fake secret", "fake\tsecret", "fake\x00secret", "fake\u200bsecret", " \t\r\n"} {
		client := &http.Client{Transport: securityRoundTripper(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid API key must not reach a transport")
			return nil, nil
		})}
		for _, p := range []Provider{NewTavilyProvider(key, client), NewBraveProvider(key, client)} {
			_, err := p.Search(context.Background(), SearchRequest{Query: "test"})
			var diagnostic *ProviderError
			require.ErrorAs(t, err, &diagnostic)
			require.Equal(t, FailureInvalidKey, diagnostic.Kind)
			require.NotContains(t, err.Error(), "secret")
		}
	}
}

func TestProviderRealSearch_SafeFailureDiagnostics(t *testing.T) {
	secret := "fake-upstream-secret"
	cases := []struct {
		name   string
		status int
		body   string
		err    error
		kind   FailureKind
	}{
		{name: "unauthorized", status: 401, body: secret, kind: FailureAuthentication},
		{name: "forbidden", status: 403, body: secret, kind: FailureAuthentication},
		{name: "rate_limit", status: 429, body: secret, kind: FailureRateLimit},
		{name: "server", status: 500, body: secret, kind: FailureUpstream},
		{name: "invalid_json", status: 200, body: `{"results":["` + secret + `"`, kind: FailureResponse},
		{name: "network", err: errors.New("proxy failed: http://fake-user:" + secret + "@proxy.invalid?token=" + secret), kind: FailureProxy},
		{name: "timeout", err: fmt.Errorf("%s: %w", secret, context.DeadlineExceeded), kind: FailureTimeout},
		{name: "canceled", err: fmt.Errorf("%s: %w", secret, context.Canceled), kind: FailureCanceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: securityRoundTripper(func(*http.Request) (*http.Response, error) {
				if tc.err != nil {
					return nil, tc.err
				}
				return searchTestResponse(tc.status, tc.body), nil
			})}
			for _, p := range []Provider{NewTavilyProvider("tvly-fake-key", client), NewBraveProvider("fake-brave-key", client)} {
				_, err := p.Search(context.Background(), SearchRequest{Query: "fake-private-query"})
				var diagnostic *ProviderError
				require.ErrorAs(t, err, &diagnostic)
				require.Equal(t, tc.kind, diagnostic.Kind)
				wantStatus := tc.status
				if wantStatus == 200 {
					wantStatus = 0
				}
				require.Equal(t, wantStatus, diagnostic.StatusCode)
				for _, value := range []string{err.Error(), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
					for _, forbidden := range []string{secret, "fake-user", "fake-private-query", "tvly-fake-key", "fake-brave-key"} {
						require.NotContains(t, value, forbidden)
					}
				}
				require.Nil(t, errors.Unwrap(err))
			}
		})
	}
}

func TestManagerTestSearch_TargetAndLegacyBehavior(t *testing.T) {
	for _, target := range []string{"brave", "tavily", ""} {
		t.Run("target_"+target, func(t *testing.T) {
			m := NewManager([]ProviderConfig{{Type: "brave", APIKey: "fake-brave"}, {Type: "tavily", APIKey: " \ttvly-fake-key\n"}}, nil)
			var hosts []string
			m.clientCache[""] = &http.Client{Transport: securityRoundTripper(func(r *http.Request) (*http.Response, error) {
				hosts = append(hosts, r.URL.Host)
				if r.URL.Host == "api.search.brave.com" {
					return searchTestResponse(401, "fake-secret"), nil
				}
				require.Equal(t, "Bearer tvly-fake-key", r.Header.Get("Authorization"))
				return searchTestResponse(200, `{"results":[]}`), nil
			})}
			resp, provider, err := m.TestSearchProvider(context.Background(), SearchRequest{Query: "test"}, target)
			if target == "brave" {
				var diagnostic *ProviderError
				require.ErrorAs(t, err, &diagnostic)
				require.Equal(t, FailureAuthentication, diagnostic.Kind)
				require.Equal(t, []string{"api.search.brave.com"}, hosts)
				require.Nil(t, resp)
				require.Empty(t, provider)
			} else {
				require.NoError(t, err)
				require.NotNil(t, resp)
				require.Equal(t, "tavily", provider)
				want := []string{"api.tavily.com"}
				if target == "" {
					want = []string{"api.search.brave.com", "api.tavily.com"}
				}
				require.Equal(t, want, hosts)
			}
		})
	}
}

func TestManagerTestSearch_NoCandidateVsAllFailed(t *testing.T) {
	expired := time.Now().Add(-time.Hour).Unix()
	for _, configs := range [][]ProviderConfig{nil, {{Type: "tavily", APIKey: "  "}}, {{Type: "tavily", APIKey: "fake-key", ExpiresAt: &expired}}} {
		_, _, err := NewManager(configs, nil).TestSearch(context.Background(), SearchRequest{Query: "test"})
		require.ErrorIs(t, err, ErrNoTestProvider)
	}
	m := NewManager([]ProviderConfig{{Type: "brave", APIKey: "fake-key"}, {Type: "tavily", APIKey: "fake-key"}}, nil)
	calls := 0
	m.clientCache[""] = &http.Client{Transport: securityRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return searchTestResponse(429, "fake-secret"), nil
	})}
	_, _, err := m.TestSearch(context.Background(), SearchRequest{Query: "test"})
	var allFailed *TestSearchError
	require.ErrorAs(t, err, &allFailed)
	require.Len(t, allFailed.Failures, 2)
	require.Equal(t, 2, calls)
	require.NotContains(t, err.Error(), "fake-secret")
	_, _, err = m.TestSearchProvider(context.Background(), SearchRequest{Query: "test"}, "fake-secret-invalid-type")
	require.ErrorIs(t, err, ErrInvalidTestProvider)
	require.Equal(t, 2, calls)
	m.configs = append(m.configs, m.configs[0])
	_, _, err = m.TestSearchProvider(context.Background(), SearchRequest{Query: "test"}, "brave")
	require.ErrorIs(t, err, ErrAmbiguousTestProvider)
	require.Equal(t, 2, calls)
}

func TestProxyErrors_NoSecretsAtClientManagerOrLogBoundary(t *testing.T) {
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(original)
	for _, raw := range []string{
		"http://fake-user:fake-password@proxy.invalid:fake-port?key=fake-query",
		"http://fake-user:fake-password%zz@proxy.invalid?key=fake-query",
		"http://fake-user:fake-password@/fake-path?key=fake-query",
		"fake-scheme://fake-user:fake-password@proxy.invalid?key=fake-query",
	} {
		client, err := newHTTPClient(raw)
		require.Nil(t, client)
		require.Error(t, err)
		m := NewManager([]ProviderConfig{{Type: "tavily", APIKey: "fake-api-key", ProxyURL: raw}}, nil)
		_, _, testErr := m.TestSearchProvider(context.Background(), SearchRequest{Query: "test"}, "tavily")
		require.Error(t, testErr)
		_, _, productionErr := m.SearchWithBestProvider(context.Background(), SearchRequest{Query: "test", ProxyURL: raw})
		require.ErrorIs(t, productionErr, ErrProxyUnavailable)
		for _, forbidden := range []string{"fake-user", "fake-password", "fake-query", "fake-path", "fake-port", "fake-scheme", "fake-api-key"} {
			for _, text := range []string{err.Error(), testErr.Error(), productionErr.Error(), logs.String()} {
				require.NotContains(t, text, forbidden)
			}
		}
	}
}

func TestNewHTTPClient_ValidTransportSemantics(t *testing.T) {
	for _, scheme := range []string{"http", "https", "socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			raw := scheme + "://fake-user:fake-password@[2001:db8::1]:1080"
			client, err := newHTTPClient(raw)
			require.NoError(t, err)
			transport, ok := client.Transport.(*http.Transport)
			require.True(t, ok)
			require.NotNil(t, transport.DialContext)
			if scheme == "http" || scheme == "https" {
				parsed, err := transport.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: "example.invalid"}})
				require.NoError(t, err)
				require.Equal(t, raw, parsed.String())
			} else {
				require.Nil(t, transport.Proxy)
			}
		})
	}
}

func TestProviderRealSearch_PreservesProductionProxyErrorClassification(t *testing.T) {
	for _, body := range []string{"rate limited", "proxy rejected fake-secret", "socks failure fake-secret", "TLS handshake fake-secret"} {
		client := &http.Client{Transport: securityRoundTripper(func(*http.Request) (*http.Response, error) {
			return searchTestResponse(http.StatusBadGateway, body), nil
		})}
		_, err := NewTavilyProvider("tvly-fake-key", client).Search(context.Background(), SearchRequest{Query: "test"})
		require.Error(t, err)
		legacyError := fmt.Errorf("tavily: status %d: %s", http.StatusBadGateway, truncateBody([]byte(body)))
		require.Equal(t, isProxyError(legacyError), isProxyError(err))
		require.NotContains(t, err.Error(), "fake-secret")
	}
}

// quotaSecurityHook exercises the real Manager quota code without a Redis socket.
type quotaSecurityHook struct {
	t        *testing.T
	usage    map[string]int64
	commands []string
}

func (h *quotaSecurityHook) DialHook(redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) {
		h.t.Fatal("Redis must not dial in this regression")
		return nil, errors.New("unexpected Redis dial")
	}
}
func (h *quotaSecurityHook) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(context.Context, []redis.Cmder) error {
		h.t.Fatal("unexpected Redis pipeline")
		return errors.New("unexpected pipeline")
	}
}
func (h *quotaSecurityHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		h.commands = append(h.commands, cmd.Name())
		switch cmd.Name() {
		case "get":
			key, ok := cmd.Args()[1].(string)
			require.True(h.t, ok)
			stringCmd, ok := cmd.(*redis.StringCmd)
			require.True(h.t, ok)
			stringCmd.SetVal(fmt.Sprint(h.usage[key]))
		case "evalsha":
			key, ok := cmd.Args()[3].(string)
			require.True(h.t, ok)
			h.usage[key]++
			valueCmd, ok := cmd.(*redis.Cmd)
			require.True(h.t, ok)
			valueCmd.SetVal(h.usage[key])
		case "decr":
			key, ok := cmd.Args()[1].(string)
			require.True(h.t, ok)
			h.usage[key]--
			intCmd, ok := cmd.(*redis.IntCmd)
			require.True(h.t, ok)
			intCmd.SetVal(h.usage[key])
		default:
			h.t.Fatalf("unexpected Redis command %q", cmd.Name())
		}
		return nil
	}
}

func TestManagerRealSearch_ProductionQuotaAndDiagnosticBypass(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "unused.invalid:0"})
	t.Cleanup(func() { _ = rdb.Close() })
	hook := &quotaSecurityHook{t: t, usage: map[string]int64{}}
	rdb.AddHook(hook)
	m := NewManager([]ProviderConfig{{Type: "brave", APIKey: "fake-key", QuotaLimit: 1}, {Type: "tavily", APIKey: "fake-key", QuotaLimit: 100}}, rdb)
	var hosts []string
	m.clientCache[""] = &http.Client{Transport: securityRoundTripper(func(r *http.Request) (*http.Response, error) {
		hosts = append(hosts, r.URL.Host)
		if r.URL.Host == "api.tavily.com" {
			return searchTestResponse(429, "fake-secret"), nil
		}
		return searchTestResponse(200, `{"web":{"results":[]}}`), nil
	})}
	_, provider, err := m.SearchWithBestProvider(context.Background(), SearchRequest{Query: "test"})
	require.NoError(t, err)
	require.Equal(t, "brave", provider)
	require.Equal(t, []string{"api.tavily.com", "api.search.brave.com"}, hosts)
	require.Equal(t, int64(0), hook.usage[quotaRedisKey("tavily")], "failed reservation must roll back")
	require.Equal(t, int64(1), hook.usage[quotaRedisKey("brave")], "successful reservation must remain")
	commandCount := len(hook.commands)
	_, provider, err = m.TestSearchProvider(context.Background(), SearchRequest{Query: "test"}, "brave")
	require.NoError(t, err)
	require.Equal(t, "brave", provider)
	require.Equal(t, commandCount, len(hook.commands), "admin diagnostic must bypass quota and weighting")
	_, _, err = m.TestSearchProvider(context.Background(), SearchRequest{Query: "test"}, "tavily")
	require.Error(t, err)
	require.Equal(t, commandCount, len(hook.commands), "failed diagnostic must not reserve or roll back quota")
}

func TestNewHTTPClient_NonemptyWhitespaceFailsClosed(t *testing.T) {
	for _, raw := range []string{" ", "\t", "\r\n", " \t\r\n "} {
		client, err := newHTTPClient(raw)
		require.Error(t, err)
		require.Nil(t, client, "nonempty proxy configuration must never become a direct client")
	}
}

func TestManagerRealSearch_RuntimeTrimDoesNotMutateConfig(t *testing.T) {
	configs := []ProviderConfig{{Type: "tavily", APIKey: " \ttvly-fake-key\r\n"}}
	m := NewManager(configs, nil)
	require.Equal(t, "tvly-fake-key", m.configs[0].APIKey)
	require.Equal(t, " \ttvly-fake-key\r\n", configs[0].APIKey)
	m.clientCache[""] = &http.Client{Transport: securityRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("a missing target must not execute the other provider")
		return nil, nil
	})}
	_, _, err := m.TestSearchProvider(context.Background(), SearchRequest{Query: "test"}, "brave")
	require.ErrorIs(t, err, ErrNoTestProvider)
}

func TestManagerRealSearch_ProviderFailuresNeverLogSecrets(t *testing.T) {
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(original)
	for _, transportFailure := range []bool{false, true} {
		m := NewManager([]ProviderConfig{{Type: "tavily", APIKey: "tvly-fake-key"}}, nil)
		m.clientCache[""] = &http.Client{Transport: securityRoundTripper(func(*http.Request) (*http.Response, error) {
			if transportFailure {
				return nil, errors.New("proxy fake-password fake-query tvly-fake-key")
			}
			return searchTestResponse(401, "echoed tvly-fake-key fake-password fake-query"), nil
		})}
		_, _, err := m.SearchWithBestProvider(context.Background(), SearchRequest{Query: "fake-private-query"})
		require.Error(t, err)
		for _, secret := range []string{"fake-password", "fake-query", "tvly-fake-key", "fake-private-query"} {
			require.NotContains(t, err.Error(), secret)
			require.NotContains(t, logs.String(), secret)
		}
	}
}
