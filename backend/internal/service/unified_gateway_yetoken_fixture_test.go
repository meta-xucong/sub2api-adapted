package service

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type sanitizedYeTokenFixture struct {
	Accounts []struct {
		ID          int64    `json:"id"`
		Name        string   `json:"name"`
		Platform    string   `json:"platform"`
		Type        string   `json:"type"`
		Status      string   `json:"status"`
		Schedulable bool     `json:"schedulable"`
		Models      []string `json:"models"`
	} `json:"accounts"`
}

func TestUnifiedGatewaySanitizedYeTokenFixtureExposesMixedModelFamilies(t *testing.T) {
	data, err := os.ReadFile("testdata/yetoken_accounts_sanitized.json")
	require.NoError(t, err)
	fixtureText := strings.ToLower(string(data))
	for _, forbidden := range []string{"api_key", "access_token", "refresh_token", "password", "base_url", "custom_base_url"} {
		require.NotContains(t, fixtureText, forbidden, "sanitized Yetoken fixture must not contain credential or endpoint fields")
	}
	var fixture sanitizedYeTokenFixture
	require.NoError(t, json.Unmarshal(data, &fixture))

	catalog := NewMemoryUnifiedGatewayRouteCatalog()
	accounts := make(map[int64]*Account)
	nextTargetID := int64(1000)
	nextBindingID := int64(2000)
	for _, item := range fixture.Accounts {
		mapping := make(map[string]any, len(item.Models))
		for _, model := range item.Models {
			mapping[model] = model
		}
		accounts[item.ID] = &Account{
			ID: item.ID, Name: item.Name, Platform: item.Platform, Type: item.Type, Status: item.Status, Schedulable: item.Schedulable,
			Credentials: map[string]any{"model_mapping": mapping},
		}
		for _, model := range item.Models {
			endpoint := UnifiedGatewayEndpointChatCompletions
			provider := UnifiedGatewayProviderOpenAICompatible
			if item.Platform == PlatformAnthropic && item.Type == AccountTypeAPIKey {
				provider = UnifiedGatewayProviderAnthropicAPIKey
			}
			billingMode := string(BillingModeToken)
			basis := UnifiedRateBasisToken
			basePrice := 0.01
			if len(model) >= 10 && model[:10] == "gpt-image-" {
				endpoint = UnifiedGatewayEndpointImages
				billingMode = string(BillingModeImage)
				basis = UnifiedRateBasisImage
				basePrice = 0.04
			}
			targetID, createErr := catalog.CreateTarget(UnifiedGatewayRouteTarget{
				ID: nextTargetID, AccessGroupID: 42, BillingLaneID: "yetoken-" + model, PublicModel: model,
				ProviderIdentity: provider, UpstreamModel: model, Endpoint: endpoint,
				BillingMode: billingMode, RateMode: UnifiedRateModeManualOnly, RateBasis: basis,
				LaneRule: unifiedGatewayRule(billingMode, basis, basePrice, 1, 1), Enabled: true, Priority: 10,
			})
			require.NoError(t, createErr)
			require.NoError(t, catalog.AddBinding(UnifiedGatewayAccountBinding{ID: nextBindingID, RouteTargetID: targetID, AccountID: item.ID, Enabled: true}))
			nextTargetID++
			nextBindingID++
		}
	}

	gateway := NewUnifiedGateway(catalog, NewMemoryUnifiedGatewayPriceSnapshotStore(), NewMemoryUnifiedGatewayChargeLedger(map[int64]float64{7: 100}), UnifiedGatewayUpstreamExecutorFunc(func(context.Context, UnifiedGatewayRouteSelection, UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error) {
		return UnifiedGatewayUpstreamResult{}, nil
	}))
	gateway.SetAccountReader(unifiedGatewayLiveAccountReader{accounts: accounts})
	models, err := gateway.ListModels(context.Background(), 42)
	require.NoError(t, err)

	got := make([]string, 0, len(models))
	for _, model := range models {
		got = append(got, model.ID)
	}
	for _, expected := range []string{"gpt-5.5", "gpt-6-astra", "gpt-image-2", "deepseek-v4-pro-0813", "glm-5.3", "kimi-k3", "minimax-m3", "qwen3.8-max", "hy4", "claude-opus-4-6", "claude-sonnet-4-6", "claude-sonnet-5"} {
		require.Contains(t, got, expected)
	}
	// The only source for these retired models in the fixture is the error /
	// unschedulable account, so the live eligibility guard must keep them out.
	require.NotContains(t, got, "gpt-5.4")
	require.NotContains(t, got, "gpt-5.4-mini")
}
