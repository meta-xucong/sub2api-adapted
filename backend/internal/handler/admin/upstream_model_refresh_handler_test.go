package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelCatalogRefreshStatusUnavailableIsFailClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/model-catalog-refresh/status?query=private-query", nil)

	(&AccountHandler{}).GetUpstreamModelRefreshStatus(ctx)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "private-query")
	require.NotContains(t, recorder.Body.String(), "credential")
	require.NotContains(t, recorder.Body.String(), "base_url")
}

func TestModelCatalogPolicyPreviewValidatesBatchBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = "1"
	}
	tests := []struct {
		name string
		body string
	}{
		{name: "missing body", body: ``},
		{name: "empty IDs", body: `{"account_ids":[]}`},
		{name: "nonpositive ID", body: `{"account_ids":[1,0]}`},
		{name: "duplicate IDs", body: `{"account_ids":[1,1]}`},
		{name: "more than 100 IDs", body: `{"account_ids":[` + strings.Join(tooMany, ",") + `]}`},
		{name: "unknown credential fields", body: `{"account_ids":[7],"credentials":{"api_key":"secret"},"base_url":"https://secret.invalid"}`},
		{name: "multiple JSON values", body: `{"account_ids":[7]} {}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/model-catalog-refresh/policy-preview", strings.NewReader(tt.body))

			(&AccountHandler{}).PreviewUpstreamModelPolicies(ctx)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.NotContains(t, recorder.Body.String(), "secret")
			require.NotContains(t, recorder.Body.String(), "secret.invalid")
		})
	}
}

func TestModelCatalogPolicyPreviewReturns503WithoutServiceAndDoesNotEchoQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/model-catalog-refresh/policy-preview?base_url=https%3A%2F%2Fsecret.invalid%2F%3Ftoken%3Dx", strings.NewReader(`{"account_ids":[7]}`))

	(&AccountHandler{}).PreviewUpstreamModelPolicies(ctx)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "secret.invalid")
	require.NotContains(t, recorder.Body.String(), "token=x")
}

func TestModelCatalogPolicyOptInValidatesCompleteConfirmationRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	validHash := modelCatalogTestPlanHash()
	tests := []struct {
		name string
		body string
	}{
		{name: "missing fields", body: `{}`},
		{name: "missing preview id", body: `{"plan_hash":"` + validHash + `","confirm_account_ids":[7]}`},
		{name: "missing plan hash", body: `{"preview_id":"preview-1","confirm_account_ids":[7]}`},
		{name: "missing confirmation set", body: `{"preview_id":"preview-1","plan_hash":"` + validHash + `","confirm_account_ids":[]}`},
		{name: "duplicate confirmation IDs", body: `{"preview_id":"preview-1","plan_hash":"` + validHash + `","confirm_account_ids":[7,7]}`},
		{name: "nonpositive confirmation ID", body: `{"preview_id":"preview-1","plan_hash":"` + validHash + `","confirm_account_ids":[0]}`},
		{name: "malformed plan hash", body: `{"preview_id":"preview-1","plan_hash":"sha256:abc","confirm_account_ids":[7]}`},
		{name: "oversized token", body: `{"preview_id":"` + strings.Repeat("p", 257) + `","plan_hash":"` + validHash + `","confirm_account_ids":[7]}`},
		{name: "unknown credential fields", body: `{"preview_id":"preview-1","plan_hash":"` + validHash + `","confirm_account_ids":[7],"credentials":{"api_key":"secret"},"base_url":"https://secret.invalid"}`},
		{name: "multiple JSON values", body: `{"preview_id":"preview-1","plan_hash":"` + validHash + `","confirm_account_ids":[7]} {}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/model-catalog-refresh/policy-opt-in", strings.NewReader(tt.body))
			ctx.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 99})

			(&AccountHandler{}).OptInUpstreamModelPolicies(ctx)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.NotContains(t, recorder.Body.String(), "secret")
			require.NotContains(t, recorder.Body.String(), "secret.invalid")
		})
	}
}

func TestModelCatalogPolicyOptInRequiresAdminActorAndService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	validHash := modelCatalogTestPlanHash()
	makeContext := func(actorID int64) (*gin.Context, *httptest.ResponseRecorder) {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/model-catalog-refresh/policy-opt-in", strings.NewReader(`{"preview_id":"preview-1","plan_hash":"`+validHash+`","confirm_account_ids":[7]}`))
		if actorID > 0 {
			ctx.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: actorID})
		}
		return ctx, recorder
	}

	ctx, recorder := makeContext(0)
	(&AccountHandler{}).OptInUpstreamModelPolicies(ctx)
	require.Equal(t, http.StatusUnauthorized, recorder.Code)

	ctx, recorder = makeContext(99)
	(&AccountHandler{}).OptInUpstreamModelPolicies(ctx)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "plan_hash")
}

func modelCatalogTestPlanHash() string {
	return "sha256:" + strings.Repeat("a", 64)
}

func TestModelCatalogRefreshPayloadRedactsSensitiveFieldsAndURLsRecursively(t *testing.T) {
	payload, ok := sanitizeModelCatalogRefreshData(map[string]any{
		"status":      "ok",
		"base_url":    "https://upstream.invalid/?api_key=secret",
		"query":       "api_key=secret",
		"credentials": map[string]any{"api_key": "secret"},
		"accounts": []any{map[string]any{
			"account_id": 7,
			"source":     map[string]any{"endpoint_url": "https://upstream.invalid/path"},
			"diagnostic": "request to https://upstream.invalid/?token=secret failed",
			"last_error": "upstream request failed",
		}},
	})
	require.True(t, ok)
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	responseJSON := string(encoded)
	require.Contains(t, responseJSON, `"status":"ok"`)
	require.Contains(t, responseJSON, `"account_id":7`)
	require.NotContains(t, responseJSON, "base_url")
	require.NotContains(t, responseJSON, "query")
	require.NotContains(t, responseJSON, "credential")
	require.NotContains(t, responseJSON, "endpoint_url")
	require.NotContains(t, responseJSON, "last_error")
	require.NotContains(t, responseJSON, "upstream.invalid")
	require.NotContains(t, responseJSON, "secret")
}

func TestOpenAIAccountModelFallbackFiltersHiddenMappingIDs(t *testing.T) {
	svc := &availableModelsAdminService{
		stubAdminService: newStubAdminService(),
		account: service.Account{
			ID:       73,
			Name:     "openai-mapped-account",
			Platform: service.PlatformOpenAI,
			Type:     service.AccountTypeAPIKey,
			Status:   service.StatusActive,
			Credentials: map[string]any{
				"base_url": "https://api.openai.com/v1",
				"model_mapping": map[string]any{
					"codex-auto-review": "gpt-5.6-sol",
					"gpt-5.6":           "gpt-5.6-sol",
					"gpt-5.6-sol":       "gpt-5.6-sol",
					"tenant-custom":     "upstream-custom",
				},
			},
		},
	}
	router := setupAvailableModelsRouter(svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/73/models", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	ids := make([]string, 0, len(result.Data))
	for _, model := range result.Data {
		ids = append(ids, model.ID)
	}
	require.NotContains(t, ids, "codex-auto-review")
	require.NotContains(t, ids, "gpt-5.6")
	require.Contains(t, ids, "gpt-5.6-sol")
	require.Contains(t, ids, "tenant-custom")
}
