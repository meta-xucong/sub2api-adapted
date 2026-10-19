package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mediaPreviewDirectiveValues(policy, name string) []string {
	for _, directive := range strings.Split(policy, ";") {
		fields := strings.Fields(directive)
		if len(fields) > 0 && fields[0] == name {
			return fields[1:]
		}
	}
	return nil
}

func TestSecurityHeadersMediaPreviewPolicy(t *testing.T) {
	for _, tc := range []struct{ name, policy string }{
		{"empty", ""},
		{"whitespace", " \t\n "},
		{"explicit_default", config.DefaultCSPPolicy},
	} {
		t.Run("default/"+tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/admin/accounts", nil)
			SecurityHeaders(config.CSPConfig{Enabled: true, Policy: tc.policy}, nil)(c)

			policy := w.Header().Get("Content-Security-Policy")
			// A narrow host source is intentional: arbitrary relay hosts, insecure HTTP,
			// subdomains and blob: are not added for this admin preview fix.
			assert.Equal(t, []string{"'self'", "data:", "https://vidgen.x.ai"}, mediaPreviewDirectiveValues(policy, "media-src"))
			assert.Equal(t, 1, strings.Count(policy, "media-src "))
			assert.Equal(t, []string{"'self'"}, mediaPreviewDirectiveValues(policy, "default-src"))
			assert.Equal(t, []string{"'none'"}, mediaPreviewDirectiveValues(policy, "frame-ancestors"))
			assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))

			nonce := GetNonceFromContext(c)
			require.NotEmpty(t, nonce)
			assert.NotContains(t, policy, NonceTemplate)
			for _, directive := range []string{"script-src", "frame-src", "connect-src"} {
				expected := mediaPreviewDirectiveValues(strings.ReplaceAll(config.DefaultCSPPolicy, NonceTemplate, "'nonce-"+nonce+"'"), directive)
				assert.Equal(t, expected, mediaPreviewDirectiveValues(policy, directive), directive)
				assert.NotContains(t, mediaPreviewDirectiveValues(policy, directive), "https://vidgen.x.ai", directive)
			}
			assert.NotContains(t, mediaPreviewDirectiveValues(policy, "script-src"), "data:")
			assert.NotContains(t, mediaPreviewDirectiveValues(policy, "script-src"), "'unsafe-inline'")
			assert.NotContains(t, mediaPreviewDirectiveValues(policy, "frame-src"), "data:")
		})
	}

	for _, policy := range []string{
		"default-src 'self'",
		"default-src 'none'",
		"default-src 'self'; media-src 'none'",
		"default-src 'self'; media-src 'self'",
		"default-src 'self'; media-src https://preview.example.test",
	} {
		t.Run("custom/"+policy, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/admin/accounts", nil)
			SecurityHeaders(config.CSPConfig{Enabled: true, Policy: policy}, nil)(c)

			actual := w.Header().Get("Content-Security-Policy")
			// Keep explicit custom media-src AND an absent directive's default-src
			// fallback unchanged. Runtime requirements must not inject preview sources.
			assert.Equal(t, mediaPreviewDirectiveValues(policy, "media-src"), mediaPreviewDirectiveValues(actual, "media-src"))
			assert.Equal(t, mediaPreviewDirectiveValues(policy, "default-src"), mediaPreviewDirectiveValues(actual, "default-src"))
		})
	}
}
