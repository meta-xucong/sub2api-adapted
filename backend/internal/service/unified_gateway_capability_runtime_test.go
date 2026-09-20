package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type unifiedGatewayCapabilityAccountReader struct {
	account *Account
}

func (r unifiedGatewayCapabilityAccountReader) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func TestUnifiedGatewayRuntimeCapabilityGuardRejectsUnsupportedEndpoint(t *testing.T) {
	gateway := &UnifiedGateway{}
	gateway.SetAccountReader(unifiedGatewayCapabilityAccountReader{account: &Account{
		ID:          1,
		Platform:    PlatformGemini,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
	}})
	err := gateway.validateSelectionCapability(context.Background(), UnifiedGatewayRouteSelection{
		Target:  UnifiedGatewayRouteTarget{ProviderIdentity: UnifiedGatewayProviderGemini, Endpoint: UnifiedGatewayEndpointResponses},
		Binding: UnifiedGatewayAccountBinding{AccountID: 1},
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUnifiedGatewayRuntimeUnsupported))
}

func TestUnifiedGatewayRuntimeCapabilityGuardAllowsDeclaredCombination(t *testing.T) {
	gateway := &UnifiedGateway{}
	gateway.SetAccountReader(unifiedGatewayCapabilityAccountReader{account: &Account{
		ID:          1,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
	}})
	err := gateway.validateSelectionCapability(context.Background(), UnifiedGatewayRouteSelection{
		Target:  UnifiedGatewayRouteTarget{ProviderIdentity: UnifiedGatewayProviderOpenAIAPIKey, Endpoint: UnifiedGatewayEndpointResponses},
		Binding: UnifiedGatewayAccountBinding{AccountID: 1},
	})
	require.NoError(t, err)
}

func TestUnifiedGatewayRuntimeRequiresAccountReader(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.UnifiedGatewayRuntimeEnabled = true
	runtime := NewUnifiedGatewayRuntimeService(
		NewUnifiedGateway(NewMemoryUnifiedGatewayRouteCatalog(), NewMemoryUnifiedGatewayPriceSnapshotStore(), NewMemoryUnifiedGatewayChargeLedger(map[int64]float64{7: 10}), UnifiedGatewayUpstreamExecutorFunc(func(context.Context, UnifiedGatewayRouteSelection, UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error) {
			return UnifiedGatewayUpstreamResult{}, nil
		})),
		NewMemoryUnifiedGatewayRouteCatalog(),
		NewMemoryUnifiedGatewayPriceSnapshotStore(),
		UnifiedGatewayUpstreamExecutorFunc(func(context.Context, UnifiedGatewayRouteSelection, UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error) {
			return UnifiedGatewayUpstreamResult{}, nil
		}),
		cfg,
	)
	_, err := runtime.Execute(context.Background(), UnifiedGatewayRuntimeRequest{
		RequestID:      "runtime-reader-required",
		AttemptID:      "public",
		APIKeyID:       1,
		UserID:         7,
		AccessGroupID:  42,
		PublicModel:    "model",
		Endpoint:       UnifiedGatewayEndpointChatCompletions,
		EstimatedUnits: 1,
		RawBody:        []byte(`{"model":"model"}`),
	})
	require.ErrorIs(t, err, ErrUnifiedGatewayRuntimeUnsupported)
}
