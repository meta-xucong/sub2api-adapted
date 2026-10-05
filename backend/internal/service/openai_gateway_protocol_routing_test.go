//go:build unit

package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIAPIKeyProtocolRoutingUsesIngressUnlessExplicitlyForced(t *testing.T) {
	gin.SetMode(gin.TestMode)
	probes := []struct {
		name  string
		extra map[string]any
	}{
		{name: "unknown"},
		{name: "supported", extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}},
		{name: "unsupported", extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false}},
	}

	for _, probe := range probes {
		for _, mode := range []openai_compat.ResponsesSupportMode{
			openai_compat.ResponsesSupportModeAuto,
			openai_compat.ResponsesSupportModeForceResponses,
			openai_compat.ResponsesSupportModeForceChatCompletions,
		} {
			t.Run(probe.name+"/"+string(mode), func(t *testing.T) {
				account := rawChatCompletionsTestAccount()
				account.Extra = make(map[string]any, len(probe.extra)+1)
				for key, value := range probe.extra {
					account.Extra[key] = value
				}
				account.Extra[openai_compat.ExtraKeyResponsesMode] = string(mode)

				// Chat ingress stays Chat in auto mode regardless of probe status.
				wantResponsesForChat := mode == openai_compat.ResponsesSupportModeForceResponses
				require.Equal(t, wantResponsesForChat, shouldRouteChatCompletionsViaResponses(account))

				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				wantChatForResponses := mode == openai_compat.ResponsesSupportModeForceChatCompletions
				require.Equal(t, wantChatForResponses, shouldRouteResponsesViaChatCompletions(ctx, account))
			})
		}
	}
}

func TestOpenAIAPIKeyCompactRoutingRetainsExistingCapabilityMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name     string
		extra    map[string]any
		wantChat bool
	}{
		{name: "auto unsupported", extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false}, wantChat: true},
		{name: "auto supported", extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}},
		{name: "force responses", extra: map[string]any{openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceResponses)}},
		{name: "force chat", extra: map[string]any{openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions)}, wantChat: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := rawChatCompletionsTestAccount()
			account.Extra = tc.extra
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
			require.Equal(t, tc.wantChat, shouldRouteResponsesViaChatCompletions(ctx, account))

			// Native Responses compaction v2 is deliberately outside legacy compact routing.
			ctx.Request.URL.Path = "/v1/responses"
			MarkOpenAINativeCompactionV2(ctx)
			require.False(t, shouldRouteResponsesViaChatCompletions(ctx, account))
		})
	}
}
