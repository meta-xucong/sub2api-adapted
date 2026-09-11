package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type unifiedGatewayFixture struct {
	catalog *MemoryUnifiedGatewayRouteCatalog
	store   *MemoryUnifiedGatewayPriceSnapshotStore
	ledger  *MemoryUnifiedGatewayChargeLedger
	gateway *UnifiedGateway
	callsMu sync.Mutex
	calls   []UnifiedGatewayRouteSelection
	clock   time.Time
}

func newUnifiedGatewayFixture(t *testing.T) *unifiedGatewayFixture {
	t.Helper()
	clock := time.Date(2026, 9, 11, 4, 0, 0, 0, time.UTC)
	catalog := NewMemoryUnifiedGatewayRouteCatalog()
	fixture := &unifiedGatewayFixture{
		catalog: catalog,
		store:   NewMemoryUnifiedGatewayPriceSnapshotStore(),
		ledger:  NewMemoryUnifiedGatewayChargeLedger(map[int64]float64{7: 10, 8: 10}),
		clock:   clock,
	}
	fixture.gateway = NewUnifiedGateway(catalog, fixture.store, fixture.ledger, UnifiedGatewayUpstreamExecutorFunc(func(_ context.Context, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error) {
		fixture.callsMu.Lock()
		fixture.calls = append(fixture.calls, selection)
		fixture.callsMu.Unlock()
		var body struct {
			MeasuredUnits   float64 `json:"measured_units"`
			SimulateFailure bool    `json:"simulate_failure"`
		}
		_ = json.Unmarshal(request.RawBody, &body)
		if body.SimulateFailure {
			return UnifiedGatewayUpstreamResult{UpstreamRequestID: "failed-upstream"}, errors.New("simulated upstream failure")
		}
		units := body.MeasuredUnits
		return UnifiedGatewayUpstreamResult{
			Delivered:         true,
			MeasuredUnits:     units,
			UpstreamRequestID: "upstream-" + selection.ProviderIdentity(),
			ResponseBody:      []byte(`{"object":"simulated_completion","choices":[]}`),
		}, nil
	}))
	fixture.gateway.SetClock(func() time.Time { return clock })
	return fixture
}

func unifiedGatewayRule(mode string, basis UnifiedRateBasis, base, upstream, markup float64) *UnifiedRateRule {
	return &UnifiedRateRule{
		ProfileID:                "profile-" + mode,
		Version:                  "2026-09-11.1",
		BillingMode:              mode,
		UpstreamRateBasis:        basis,
		BasePriceSemantics:       UnifiedBasePriceProviderBase,
		ProviderBaseUnitPrice:    unifiedFloat(base),
		ManualUpstreamMultiplier: unifiedFloat(upstream),
		UserMarkupMultiplier:     unifiedFloat(markup),
		RoundingPrecision:        8,
	}
}

func addUnifiedGatewayRoute(t *testing.T, fixture *unifiedGatewayFixture, target UnifiedGatewayRouteTarget, binding UnifiedGatewayAccountBinding) int64 {
	t.Helper()
	id, err := fixture.catalog.CreateTarget(target)
	require.NoError(t, err)
	binding.RouteTargetID = id
	require.NoError(t, fixture.catalog.AddBinding(binding))
	return id
}

func TestMemoryUnifiedGatewayRouteCatalogDoesNotReuseExplicitIDs(t *testing.T) {
	catalog := NewMemoryUnifiedGatewayRouteCatalog()
	target := UnifiedGatewayRouteTarget{ID: 40, AccessGroupID: 42, BillingLaneID: "lane", PublicModel: "model", ProviderIdentity: "provider", UpstreamModel: "upstream", Endpoint: "chat_completions", BillingMode: string(BillingModePerRequest), RateMode: UnifiedRateModeManualOnly, RateBasis: UnifiedRateBasisProviderSpecific, Enabled: true}
	require.NoError(t, func() error { _, err := catalog.CreateTarget(target); return err }())
	autoID, err := catalog.CreateTarget(UnifiedGatewayRouteTarget{AccessGroupID: 42, BillingLaneID: "lane-2", PublicModel: "model-2", ProviderIdentity: "provider", UpstreamModel: "upstream", Endpoint: "chat_completions", BillingMode: string(BillingModePerRequest), RateMode: UnifiedRateModeManualOnly, RateBasis: UnifiedRateBasisProviderSpecific, Enabled: true})
	require.NoError(t, err)
	require.Equal(t, int64(41), autoID)
	require.NoError(t, catalog.AddBinding(UnifiedGatewayAccountBinding{RouteTargetID: 40, AccountID: 400, Enabled: true}))
	require.NoError(t, catalog.AddBinding(UnifiedGatewayAccountBinding{RouteTargetID: autoID, AccountID: 401, Enabled: true}))
}

func TestUnifiedGatewayCreatesOneAPIAndChargesActualLane(t *testing.T) {
	fixture := newUnifiedGatewayFixture(t)
	now := fixture.clock
	plusProbe := &UnifiedProbeSnapshot{
		Status:                 UnifiedProbeStatusOK,
		Basis:                  UnifiedRateBasisToken,
		ResolvedRateMultiplier: unifiedFloat(1.5),
		SnapshotRef:            "probe-plus-1",
		ReceivedAt:             now.Add(-time.Minute),
		FreshUntil:             now.Add(time.Hour),
	}
	plusID := addUnifiedGatewayRoute(t, fixture, UnifiedGatewayRouteTarget{
		ID: 1, AccessGroupID: 42, BillingLaneID: "chatgpt-plus", PublicModel: "gpt-5.5", ProviderIdentity: "openai-plus", UpstreamModel: "gpt-5.5", Endpoint: "chat_completions",
		BillingMode: string(BillingModeToken), RateMode: UnifiedRateModeProbePreferred, RateBasis: UnifiedRateBasisToken, LaneRule: unifiedGatewayRule(string(BillingModeToken), UnifiedRateBasisToken, 0.01, 1.3, 1.2), Enabled: true, Priority: 1,
	}, UnifiedGatewayAccountBinding{ID: 11, AccountID: 101, Enabled: true, Probe: plusProbe})
	proID := addUnifiedGatewayRoute(t, fixture, UnifiedGatewayRouteTarget{
		ID: 2, AccessGroupID: 42, BillingLaneID: "chatgpt-pro", PublicModel: "gpt-5.5", ProviderIdentity: "openai-pro", UpstreamModel: "gpt-5.5", Endpoint: "chat_completions",
		BillingMode: string(BillingModeToken), RateMode: UnifiedRateModeManualOnly, RateBasis: UnifiedRateBasisToken, LaneRule: unifiedGatewayRule(string(BillingModeToken), UnifiedRateBasisToken, 0.01, 2.0, 1.5), Enabled: true, Priority: 2,
	}, UnifiedGatewayAccountBinding{ID: 21, AccountID: 202, Enabled: true})

	requestBody := []byte(`{"model":"gpt-5.5","estimated_units":10,"measured_units":10}`)
	first, err := fixture.gateway.Execute(context.Background(), UnifiedGatewayRequest{
		RequestID: "req-plus-1", AttemptID: "1", AccessGroupID: 42, APIKeyID: 9, UserID: 7, PublicModel: "gpt-5.5", Endpoint: "chat_completions", EstimatedUnits: 10, RawBody: requestBody,
	})
	require.NoError(t, err)
	require.Equal(t, UnifiedGatewaySnapshotCaptured, first.Record.Status)
	require.Equal(t, int64(101), first.Record.Snapshot.AccountID)
	require.Equal(t, "chatgpt-plus", first.Record.Snapshot.BillingLaneID)
	require.Equal(t, UnifiedRateSourceProbeDeclared, first.Record.Snapshot.RateSource)
	require.InDelta(t, 0.18, first.Charge, 1e-12)
	balance, err := fixture.ledger.Balance(context.Background(), 7)
	require.NoError(t, err)
	require.InDelta(t, 9.82, balance, 1e-12)

	// A repeated request is served from the durable snapshot and does not
	// forward or charge again.
	repeated, err := fixture.gateway.Execute(context.Background(), UnifiedGatewayRequest{
		RequestID: "req-plus-1", AttemptID: "1", AccessGroupID: 42, APIKeyID: 9, UserID: 7, PublicModel: "gpt-5.5", Endpoint: "chat_completions", EstimatedUnits: 10, RawBody: requestBody,
	})
	require.NoError(t, err)
	require.Equal(t, first.Charge, repeated.Charge)
	fixture.callsMu.Lock()
	require.Len(t, fixture.calls, 1)
	fixture.callsMu.Unlock()

	// The same client-supplied idempotency key is scoped by API key/user/group;
	// it must not replay another principal's captured response or charge.
	otherPrincipal, err := fixture.gateway.Execute(context.Background(), UnifiedGatewayRequest{
		RequestID: "req-plus-1", AttemptID: "1", AccessGroupID: 42, APIKeyID: 10, UserID: 8, PublicModel: "gpt-5.5", Endpoint: "chat_completions", EstimatedUnits: 10, RawBody: requestBody,
	})
	require.NoError(t, err)
	require.InDelta(t, first.Charge, otherPrincipal.Charge, 1e-12)
	otherBalance, err := fixture.ledger.Balance(context.Background(), 8)
	require.NoError(t, err)
	require.InDelta(t, 9.82, otherBalance, 1e-12)
	fixture.callsMu.Lock()
	require.Len(t, fixture.calls, 2)
	fixture.callsMu.Unlock()

	// Disabling the first lane makes the same public model resolve to Pro.  The
	// resulting price is calculated from the selected lane, not from the access
	// group or the previous snapshot.
	require.NoError(t, fixture.catalog.SetTargetEnabled(plusID, false))
	second, err := fixture.gateway.Execute(context.Background(), UnifiedGatewayRequest{
		RequestID: "req-pro-1", AttemptID: "1", AccessGroupID: 42, APIKeyID: 9, UserID: 7, PublicModel: "gpt-5.5", Endpoint: "chat_completions", EstimatedUnits: 10, RawBody: requestBody,
	})
	require.NoError(t, err)
	require.Equal(t, proID, second.Record.Snapshot.RouteID)
	require.Equal(t, int64(202), second.Record.Snapshot.AccountID)
	require.Equal(t, "chatgpt-pro", second.Record.Snapshot.BillingLaneID)
	require.Equal(t, UnifiedRateSourceManualOnly, second.Record.Snapshot.RateSource)
	require.InDelta(t, 0.3, second.Charge, 1e-12)
	balance, err = fixture.ledger.Balance(context.Background(), 7)
	require.NoError(t, err)
	require.InDelta(t, 9.52, balance, 1e-12)
}

func TestUnifiedGatewayFailureReleasesReservationAndDoesNotCharge(t *testing.T) {
	fixture := newUnifiedGatewayFixture(t)
	addUnifiedGatewayRoute(t, fixture, UnifiedGatewayRouteTarget{
		ID: 10, AccessGroupID: 42, BillingLaneID: "ark-manual", PublicModel: "doubao-seed", ProviderIdentity: "ark", UpstreamModel: "doubao-seed", Endpoint: "chat_completions",
		BillingMode: string(BillingModePerRequest), RateMode: UnifiedRateModeManualOnly, RateBasis: UnifiedRateBasisProviderSpecific, LaneRule: unifiedGatewayRule(string(BillingModePerRequest), UnifiedRateBasisProviderSpecific, 0.002, 1, 1.2), Enabled: true,
	}, UnifiedGatewayAccountBinding{ID: 31, AccountID: 301, Enabled: true})
	_, err := fixture.gateway.Execute(context.Background(), UnifiedGatewayRequest{
		RequestID: "req-failed-1", AttemptID: "1", AccessGroupID: 42, APIKeyID: 9, UserID: 7, PublicModel: "doubao-seed", Endpoint: "chat_completions", EstimatedUnits: 1,
		RawBody: []byte(`{"model":"doubao-seed","estimated_units":1,"simulate_failure":true}`),
	})
	require.ErrorIs(t, err, ErrUnifiedGatewayUpstreamFailed)
	balance, balanceErr := fixture.ledger.Balance(context.Background(), 7)
	require.NoError(t, balanceErr)
	require.InDelta(t, 10, balance, 1e-12)
	record, getErr := fixture.store.Get(context.Background(), 9, 7, 42, "req-failed-1", "1")
	require.NoError(t, getErr)
	require.Equal(t, UnifiedGatewaySnapshotReleased, record.Status)
	require.Zero(t, record.UserCharge)
	_, replayErr := fixture.gateway.Execute(context.Background(), UnifiedGatewayRequest{
		RequestID: "req-failed-1", AttemptID: "1", AccessGroupID: 42, APIKeyID: 9, UserID: 7, PublicModel: "doubao-seed", Endpoint: "chat_completions", EstimatedUnits: 1,
		RawBody: []byte(`{"model":"doubao-seed","estimated_units":1,"simulate_failure":true}`),
	})
	require.ErrorIs(t, replayErr, ErrUnifiedGatewayUpstreamFailed)
	fixture.callsMu.Lock()
	require.Len(t, fixture.calls, 1)
	fixture.callsMu.Unlock()
}

func TestUnifiedGatewayPerRequestUsesManualProviderRule(t *testing.T) {
	fixture := newUnifiedGatewayFixture(t)
	addUnifiedGatewayRoute(t, fixture, UnifiedGatewayRouteTarget{
		ID: 20, AccessGroupID: 42, BillingLaneID: "ark-request", PublicModel: "ark-request-model", ProviderIdentity: "volcengine-ark", UpstreamModel: "doubao-seed-1", Endpoint: "chat_completions",
		BillingMode: string(BillingModePerRequest), RateMode: UnifiedRateModeManualOnly, RateBasis: UnifiedRateBasisProviderSpecific, LaneRule: &UnifiedRateRule{
			ProfileID: "ark-request-v1", Version: "2026-09-11", BillingMode: string(BillingModePerRequest), UpstreamRateBasis: UnifiedRateBasisProviderSpecific, BasePriceSemantics: UnifiedBasePriceProviderBase,
			ManualBaseUnitPrice: unifiedFloat(0.002), UserMarkupMultiplier: unifiedFloat(1.2), RoundingPrecision: 8,
		}, Enabled: true,
	}, UnifiedGatewayAccountBinding{ID: 41, AccountID: 401, Enabled: true})
	result, err := fixture.gateway.Execute(context.Background(), UnifiedGatewayRequest{
		RequestID: "req-ark-1", AttemptID: "1", AccessGroupID: 42, APIKeyID: 9, UserID: 7, PublicModel: "ark-request-model", Endpoint: "chat_completions", EstimatedUnits: 0,
		RawBody: []byte(`{"model":"ark-request-model","measured_units":0}`),
	})
	require.NoError(t, err)
	// The per-request basis defaults a successful request to one unit when the
	// provider does not return token usage.
	require.Equal(t, UnifiedRateBasisProviderSpecific, result.Record.Snapshot.RateBasis)
	require.InDelta(t, 0.0024, result.Charge, 1e-12)
}

type failCapturedSnapshotStore struct {
	delegate *MemoryUnifiedGatewayPriceSnapshotStore
}

func (s *failCapturedSnapshotStore) Create(ctx context.Context, record *UnifiedGatewayPriceSnapshotRecord) (*UnifiedGatewayPriceSnapshotRecord, error) {
	return s.delegate.Create(ctx, record)
}

func (s *failCapturedSnapshotStore) Get(ctx context.Context, apiKeyID, userID, accessGroupID int64, requestID, attemptID string) (*UnifiedGatewayPriceSnapshotRecord, error) {
	return s.delegate.Get(ctx, apiKeyID, userID, accessGroupID, requestID, attemptID)
}

func (s *failCapturedSnapshotStore) Finalize(ctx context.Context, apiKeyID, userID, accessGroupID int64, requestID, attemptID string, status UnifiedGatewaySnapshotStatus, measuredUnits, userCharge float64, upstreamRequestID string, responseBody []byte, failureMessage string) error {
	if status == UnifiedGatewaySnapshotCaptured {
		return errors.New("simulated captured snapshot write failure")
	}
	return s.delegate.Finalize(ctx, apiKeyID, userID, accessGroupID, requestID, attemptID, status, measuredUnits, userCharge, upstreamRequestID, responseBody, failureMessage)
}

func TestUnifiedGatewayDoesNotLeaveChargeWhenCapturedSnapshotWriteIsKnownUncommitted(t *testing.T) {
	fixture := newUnifiedGatewayFixture(t)
	addUnifiedGatewayRoute(t, fixture, UnifiedGatewayRouteTarget{
		ID: 25, AccessGroupID: 42, BillingLaneID: "atomicity-fixture", PublicModel: "atomicity-model", ProviderIdentity: "provider", UpstreamModel: "upstream", Endpoint: "chat_completions",
		BillingMode: string(BillingModeToken), RateMode: UnifiedRateModeManualOnly, RateBasis: UnifiedRateBasisToken, LaneRule: unifiedGatewayRule(string(BillingModeToken), UnifiedRateBasisToken, 0.01, 1, 1), Enabled: true,
	}, UnifiedGatewayAccountBinding{ID: 45, AccountID: 425, Enabled: true})
	store := &failCapturedSnapshotStore{delegate: NewMemoryUnifiedGatewayPriceSnapshotStore()}
	gateway := NewUnifiedGateway(fixture.catalog, store, fixture.ledger, fixture.gateway.upstream)
	gateway.SetClock(func() time.Time { return fixture.clock })
	_, err := gateway.Execute(context.Background(), UnifiedGatewayRequest{
		RequestID: "req-atomicity-1", AttemptID: "1", AccessGroupID: 42, APIKeyID: 9, UserID: 7, PublicModel: "atomicity-model", Endpoint: "chat_completions", EstimatedUnits: 2,
		RawBody: []byte(`{"model":"atomicity-model","estimated_units":2,"measured_units":2}`),
	})
	require.ErrorIs(t, err, ErrUnifiedGatewaySettlementFailed)
	balance, balanceErr := fixture.ledger.Balance(context.Background(), 7)
	require.NoError(t, balanceErr)
	require.InDelta(t, 10, balance, 1e-12)
	record, getErr := store.Get(context.Background(), 9, 7, 42, "req-atomicity-1", "1")
	require.NoError(t, getErr)
	require.Equal(t, UnifiedGatewaySnapshotSettlementFailed, record.Status)
	require.Zero(t, record.UserCharge)
}

func TestUnifiedGatewaySimulationHTTPModelsAndFailure(t *testing.T) {
	fixture := newUnifiedGatewayFixture(t)
	addUnifiedGatewayRoute(t, fixture, UnifiedGatewayRouteTarget{
		ID: 30, AccessGroupID: 42, BillingLaneID: "sim-text", PublicModel: "gpt-5.5", ProviderIdentity: "openai-plus", UpstreamModel: "gpt-5.5", Endpoint: "chat_completions",
		BillingMode: string(BillingModeToken), RateMode: UnifiedRateModeManualOnly, RateBasis: UnifiedRateBasisToken, LaneRule: unifiedGatewayRule(string(BillingModeToken), UnifiedRateBasisToken, 0.01, 1.2, 1.1), Enabled: true,
	}, UnifiedGatewayAccountBinding{ID: 51, AccountID: 501, Enabled: true})
	handler := &UnifiedGatewaySimulationHandler{Gateway: fixture.gateway, Authenticator: MemoryUnifiedGatewayAuthenticator{Tokens: map[string]UnifiedGatewayPrincipal{"local-key": {APIKeyID: 8, UserID: 7, AccessGroupID: 42}}}}
	server := httptest.NewServer(handler)
	defer server.Close()

	modelsRequest, err := http.NewRequest(http.MethodGet, server.URL+"/v1/models", nil)
	require.NoError(t, err)
	modelsRequest.Header.Set("Authorization", "Bearer local-key")
	modelsResponse, err := http.DefaultClient.Do(modelsRequest)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, modelsResponse.StatusCode)
	var modelsBody map[string]any
	require.NoError(t, json.NewDecoder(modelsResponse.Body).Decode(&modelsBody))
	_ = modelsResponse.Body.Close()
	require.Equal(t, "list", modelsBody["object"])
	require.Len(t, modelsBody["data"], 1)

	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-5.5","estimated_units":2,"measured_units":2}`))
	require.NoError(t, err)
	request.Header.Set("X-API-Key", "local-key")
	request.Header.Set("Idempotency-Key", "http-success-1")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	var responseBody map[string]any
	require.NoError(t, json.NewDecoder(response.Body).Decode(&responseBody))
	_ = response.Body.Close()
	require.Equal(t, "simulated_completion", responseBody["object"])

	failureRequest, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-5.5","estimated_units":2,"measured_units":2,"simulate_failure":true}`))
	require.NoError(t, err)
	failureRequest.Header.Set("X-API-Key", "local-key")
	failureRequest.Header.Set("Idempotency-Key", "http-failure-1")
	failureResponse, err := http.DefaultClient.Do(failureRequest)
	require.NoError(t, err)
	require.Equal(t, http.StatusBadGateway, failureResponse.StatusCode)
	_ = failureResponse.Body.Close()
}
