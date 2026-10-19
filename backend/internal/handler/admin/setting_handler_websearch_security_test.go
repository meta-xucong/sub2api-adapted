package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWebSearchEmulation_TargetBoundaryAndSafeErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	defer service.SetWebSearchManager(nil)
	service.SetWebSearchManager(websearch.NewManager([]websearch.ProviderConfig{
		{Type: "brave", APIKey: "fake-key", ProxyURL: "http://fake-user:fake-password@proxy.invalid:bad?token=fake-query"},
		{Type: "tavily", APIKey: "fake\nsecret"},
	}, nil))
	for _, tc := range []struct {
		target    string
		status    int
		reason    string
		forbidden string
	}{
		{"tavily", 400, "WEB_SEARCH_INVALID_API_KEY", "brave"},
		{"brave", 502, "WEB_SEARCH_PROXY_FAILED", "tavily"},
		{"unknown", 400, "WEB_SEARCH_INVALID_TEST", "fake"},
		{"", 502, "WEB_SEARCH_ALL_PROVIDERS_FAILED", "fake-secret"},
	} {
		t.Run("target_"+tc.target, func(t *testing.T) {
			h := NewSettingHandler(&service.SettingService{}, nil, nil, nil, nil, nil, nil)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/admin/settings/web-search-emulation/test", strings.NewReader(`{"query":"test","provider_type":"`+tc.target+`"}`))
			c.Request.Header.Set("Content-Type", "application/json")
			h.TestWebSearchEmulation(c)
			require.Equal(t, tc.status, w.Code)
			require.Contains(t, w.Body.String(), tc.reason)
			for _, forbidden := range []string{tc.forbidden, "fake-password", "fake-query", "fake-user", "fake\\nsecret"} {
				require.NotContains(t, w.Body.String(), forbidden)
			}
		})
	}
}
