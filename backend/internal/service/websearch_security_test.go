//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	"github.com/stretchr/testify/require"
)

type webSearchSecurityRepo struct {
	SettingRepository
	raw    string
	writes int
}

func (r *webSearchSecurityRepo) GetValue(context.Context, string) (string, error) { return r.raw, nil }
func (r *webSearchSecurityRepo) Set(_ context.Context, _ string, value string) error {
	r.raw = value
	r.writes++
	return nil
}

func TestWebSearchSecurity_SaveAndRuntimeTrim(t *testing.T) {
	original := `{"enabled":true,"providers":[{"type":"tavily","api_key":" \ttvly-fake-saved-key\r\n"}]}`
	repo := &webSearchSecurityRepo{raw: original}
	s := &SettingService{settingRepo: repo}
	t.Cleanup(func() {
		webSearchEmulationCache.Store(&cachedWebSearchEmulationConfig{})
		webSearchEmulationSF.Forget(sfKeyWebSearchConfig)
	})
	cfg, err := s.loadWebSearchConfigFromDB()
	require.NoError(t, err)
	require.Equal(t, "tvly-fake-saved-key", cfg.Providers[0].APIKey)
	require.Equal(t, original, repo.raw, "loading legacy config must not rewrite persisted credentials")
	require.Zero(t, repo.writes)

	for _, incoming := range []string{"", " \t\r\n", " \ttvly-fake-new-key\r\n"} {
		cfg := &WebSearchEmulationConfig{Enabled: true, Providers: []WebSearchProviderConfig{{Type: "tavily", APIKey: incoming}}}
		require.NoError(t, s.SaveWebSearchEmulationConfig(context.Background(), cfg))
		want := "tvly-fake-saved-key"
		if strings.TrimSpace(incoming) != "" {
			want = "tvly-fake-new-key"
		}
		require.Equal(t, want, cfg.Providers[0].APIKey)
		require.NotContains(t, repo.raw, `\t`)
		require.NotContains(t, repo.raw, `\r`)
		require.NotContains(t, repo.raw, `\n`)
	}
}

func TestWebSearchSecurity_InvalidSavedKeyFailsWithoutWriting(t *testing.T) {
	for _, key := range []string{"fake\nsecret", "fake secret", "fake\tsecret", "fake\x00secret"} {
		repo := &webSearchSecurityRepo{}
		s := &SettingService{settingRepo: repo}
		err := s.SaveWebSearchEmulationConfig(context.Background(), &WebSearchEmulationConfig{Providers: []WebSearchProviderConfig{{Type: "tavily", APIKey: key}}})
		require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
		require.NotContains(t, err.Error(), "secret")
		require.Zero(t, repo.writes)
	}
}

func TestWebSearchSecurity_LocalDiagnosticStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		reason string
	}{
		{"authentication", &websearch.ProviderError{Provider: "tavily", Kind: websearch.FailureAuthentication, StatusCode: 401}, 502, "WEB_SEARCH_AUTHENTICATION_FAILED"},
		{"forbidden", &websearch.ProviderError{Provider: "tavily", Kind: websearch.FailureAuthentication, StatusCode: 403}, 502, "WEB_SEARCH_AUTHENTICATION_FAILED"},
		{"rate_limit", &websearch.ProviderError{Provider: "tavily", Kind: websearch.FailureRateLimit, StatusCode: 429}, 502, "WEB_SEARCH_RATE_LIMITED"},
		{"timeout", &websearch.ProviderError{Provider: "tavily", Kind: websearch.FailureTimeout}, 504, "WEB_SEARCH_TIMEOUT"},
		{"bad_key", &websearch.ProviderError{Provider: "tavily", Kind: websearch.FailureInvalidKey}, 400, "WEB_SEARCH_INVALID_API_KEY"},
		{"no_candidate", websearch.ErrNoTestProvider, 503, "WEB_SEARCH_NO_PROVIDER"},
		{"wrapped_no_candidate", fmt.Errorf("fake-secret: %w", websearch.ErrNoTestProvider), 503, "WEB_SEARCH_NO_PROVIDER"},
		{"invalid_target", websearch.ErrInvalidTestProvider, 400, "WEB_SEARCH_INVALID_TEST"},
		{"all_failed", &websearch.TestSearchError{Failures: []*websearch.ProviderError{{Provider: "tavily", Kind: websearch.FailureRateLimit, StatusCode: 429}}}, 502, "WEB_SEARCH_ALL_PROVIDERS_FAILED"},
		{"unknown", errors.New("fake-secret-raw-error"), 502, "WEB_SEARCH_TEST_FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := webSearchTestError(tc.err)
			require.Equal(t, tc.status, infraerrors.Code(err))
			require.Equal(t, tc.reason, infraerrors.Reason(err))
			require.NotContains(t, err.Error(), "fake-secret")
			require.Nil(t, errors.Unwrap(err), "diagnostics must not retain unsafe causes")
		})
	}
}
