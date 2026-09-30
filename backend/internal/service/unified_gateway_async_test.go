package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnifiedGatewayAsyncCaptureIsIdempotentAndFailureReleases(t *testing.T) {
	catalog := NewMemoryUnifiedGatewayRouteCatalog()
	store := NewMemoryUnifiedGatewayPriceSnapshotStore()
	ledger := NewMemoryUnifiedGatewayChargeLedger(map[int64]float64{7: 10})
	_, err := catalog.CreateTarget(UnifiedGatewayRouteTarget{
		ID: 71, AccessGroupID: 42, BillingLaneID: "video-lane", PublicModel: "video-model", ProviderIdentity: "grok",
		UpstreamModel: "video-model", Endpoint: UnifiedGatewayEndpointVideos, BillingMode: string(BillingModePerRequest),
		RateMode: UnifiedRateModeManualOnly, RateBasis: UnifiedRateBasisProviderSpecific, Enabled: true,
		LaneRule: &UnifiedRateRule{ProfileID: "video-v1", Version: "1", BillingMode: string(BillingModePerRequest), UpstreamRateBasis: UnifiedRateBasisProviderSpecific, BasePriceSemantics: UnifiedBasePriceProviderBase, ManualBaseUnitPrice: unifiedFloat(0.02), UserMarkupMultiplier: unifiedFloat(1.5)},
	})
	require.NoError(t, err)
	require.NoError(t, catalog.AddBinding(UnifiedGatewayAccountBinding{ID: 711, RouteTargetID: 71, AccountID: 701, Enabled: true}))

	gateway := NewUnifiedGateway(catalog, store, ledger, UnifiedGatewayUpstreamExecutorFunc(func(context.Context, UnifiedGatewayRouteSelection, UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error) {
		return UnifiedGatewayUpstreamResult{Pending: true, UpstreamRequestID: "job-1", ResponseBody: []byte(`{"id":"job-1","status":"pending"}`)}, nil
	}))
	request := UnifiedGatewayRequest{RequestID: "async-1", AttemptID: "public-route-71-711", AccessGroupID: 42, APIKeyID: 9, UserID: 7, PublicModel: "video-model", Endpoint: UnifiedGatewayEndpointVideos, EstimatedUnits: 1}
	created, err := gateway.Execute(context.Background(), request)
	require.NoError(t, err)
	require.True(t, created.Pending)
	require.Equal(t, UnifiedGatewaySnapshotPending, created.Record.Status)
	require.Equal(t, "job-1", created.UpstreamRequestID)

	reservedBalance, err := ledger.Balance(context.Background(), 7)
	require.NoError(t, err)
	require.InDelta(t, 9.97, reservedBalance, 1e-12)

	refreshed, err := gateway.CompleteAsync(context.Background(), request, UnifiedGatewayUpstreamResult{
		Pending: true, UpstreamRequestID: "job-1-refreshed", ResponseBody: []byte(`{"id":"job-1-refreshed","status":"pending"}`),
	})
	require.NoError(t, err)
	require.True(t, refreshed.Pending)
	require.Equal(t, "job-1-refreshed", refreshed.UpstreamRequestID)
	refreshedRecord, err := store.FindByUpstreamRequestID(context.Background(), 9, 7, 42, "job-1-refreshed")
	require.NoError(t, err)
	require.Equal(t, "async-1", refreshedRecord.RequestID)

	completed, err := gateway.CompleteAsync(context.Background(), request, UnifiedGatewayUpstreamResult{
		Delivered: true, MeasuredUnits: 7, UpstreamRequestID: "job-1-refreshed", ResponseBody: []byte(`{"id":"job-1-refreshed","status":"done"}`),
	})
	require.NoError(t, err)
	require.False(t, completed.Pending)
	require.Equal(t, UnifiedGatewaySnapshotCaptured, completed.Record.Status)
	require.Equal(t, float64(1), completed.MeasuredUnits)
	require.InDelta(t, 0.03, completed.Charge, 1e-12)
	chargedBalance, err := ledger.Balance(context.Background(), 7)
	require.NoError(t, err)
	require.InDelta(t, reservedBalance, chargedBalance, 1e-12)

	replayed, err := gateway.CompleteAsync(context.Background(), request, UnifiedGatewayUpstreamResult{Delivered: true, MeasuredUnits: 1, UpstreamRequestID: "job-1"})
	require.NoError(t, err)
	require.Equal(t, completed.Charge, replayed.Charge)
	unchangedBalance, err := ledger.Balance(context.Background(), 7)
	require.NoError(t, err)
	require.InDelta(t, chargedBalance, unchangedBalance, 1e-12)

	// A second async job that fails after acceptance returns the reservation and
	// records zero user charge.
	failedRequest := request
	failedRequest.RequestID = "async-2"
	failedRequest.AttemptID = "public-route-71-711-2"
	failedRequest.RawBody = nil
	failureGateway := NewUnifiedGateway(catalog, store, ledger, UnifiedGatewayUpstreamExecutorFunc(func(context.Context, UnifiedGatewayRouteSelection, UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error) {
		return UnifiedGatewayUpstreamResult{Pending: true, UpstreamRequestID: "job-2"}, nil
	}))
	pending, err := failureGateway.Execute(context.Background(), failedRequest)
	require.NoError(t, err)
	require.True(t, pending.Pending)
	beforeFailure, err := ledger.Balance(context.Background(), 7)
	require.NoError(t, err)
	_, err = failureGateway.CompleteAsync(context.Background(), failedRequest, UnifiedGatewayUpstreamResult{Delivered: false, UpstreamRequestID: "job-2"})
	require.ErrorIs(t, err, ErrUnifiedGatewayUpstreamFailed)
	afterFailure, err := ledger.Balance(context.Background(), 7)
	require.NoError(t, err)
	require.InDelta(t, beforeFailure+0.03, afterFailure, 1e-12)
	record, err := store.Get(context.Background(), 9, 7, 42, "async-2", "public-route-71-711-2")
	require.NoError(t, err)
	require.Equal(t, UnifiedGatewaySnapshotReleased, record.Status)
	require.Zero(t, record.UserCharge)

	lookup, err := store.FindByUpstreamRequestID(context.Background(), 9, 7, 42, "job-1-refreshed")
	require.NoError(t, err)
	require.Equal(t, "async-1", lookup.RequestID)
}

func TestUnifiedRateRuleUnmarshalsAdminDecimalStrings(t *testing.T) {
	var rule UnifiedRateRule
	err := json.Unmarshal([]byte(`{"billing_mode":"token","upstream_rate_basis":"token","base_price_semantics":"provider_base","provider_base_unit_price":"0.000010000000","manual_upstream_multiplier":"1.25","user_markup_multiplier":"1.2","fixed_fee":"0"}`), &rule)
	require.NoError(t, err)
	require.NotNil(t, rule.ProviderBaseUnitPrice)
	require.NotNil(t, rule.ManualUpstreamMultiplier)
	require.NotNil(t, rule.UserMarkupMultiplier)
	require.NotNil(t, rule.FixedFee)
	require.InDelta(t, 0.00001, *rule.ProviderBaseUnitPrice, 1e-15)
	require.InDelta(t, 1.25, *rule.ManualUpstreamMultiplier, 1e-12)
	require.Zero(t, *rule.FixedFee)
}
