package repository

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func unifiedGatewaySnapshotRepoRecord() *service.UnifiedGatewayPriceSnapshotRecord {
	return &service.UnifiedGatewayPriceSnapshotRecord{
		APIKeyID:      10,
		UserID:        8,
		AccessGroupID: 42,
		RequestID:     "repo-request-1",
		AttemptID:     "1",
		Selection: service.UnifiedGatewayRouteSelection{
			Target:  service.UnifiedGatewayRouteTarget{ID: 77, AccessGroupID: 42, BillingLaneID: "ark", PublicModel: "doubao", ProviderIdentity: "ark", UpstreamModel: "doubao-seed", Endpoint: "chat_completions"},
			Binding: service.UnifiedGatewayAccountBinding{ID: 88, RouteTargetID: 77, AccountID: 99, Enabled: true},
		},
		Snapshot: service.UnifiedRoutePriceSnapshot{
			RouteID: 77, BillingLaneID: "ark", AccountID: 99, ProviderIdentity: "ark", PublicModel: "doubao", UpstreamModel: "doubao-seed", Endpoint: "chat_completions", ProfileID: "ark-v1", PolicyVersion: "1", BillingMode: string(service.BillingModePerRequest), RateMode: service.UnifiedRateModeManualOnly, RateBasis: service.UnifiedRateBasisProviderSpecific, RateSource: service.UnifiedRateSourceManualOnly, UserUnitPrice: 0.0024, ProviderUnitPrice: 0.002, UserMarkupMultiplier: 1.2, BasePriceSemantics: service.UnifiedBasePriceProviderBase, Digest: "digest-repo-1",
		},
		Status:    service.UnifiedGatewaySnapshotQuoted,
		CreatedAt: time.Date(2026, 9, 11, 4, 0, 0, 0, time.UTC),
	}
}

func TestUnifiedGatewaySnapshotRepositoryCreateGetAndFinalize(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewUnifiedGatewaySnapshotRepository(db)
	record := unifiedGatewaySnapshotRepoRecord()
	record.RequestID = "  repo-request-1  "
	record.AttemptID = " 1 "
	requestID := "repo-request-1"
	attemptID := "1"

	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO unified_route_price_snapshots")).
		WithArgs(
			record.APIKeyID, record.UserID, requestID, attemptID, record.Snapshot.RouteID, record.AccessGroupID, record.Snapshot.BillingLaneID,
			record.Snapshot.AccountID, record.Snapshot.ProviderIdentity, record.Snapshot.PublicModel, record.Snapshot.UpstreamModel, record.Snapshot.Endpoint,
			string(record.Status), sqlmock.AnyArg(), sqlmock.AnyArg(), nil, 0.0, 0.0, "", "", record.CreatedAt,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(123))
	created, err := repo.Create(context.Background(), record)
	require.NoError(t, err)
	require.Equal(t, int64(123), created.ID)
	require.Equal(t, requestID, created.RequestID)
	require.Equal(t, attemptID, created.AttemptID)

	selectionJSON := mustMarshalJSON(t, record.Selection)
	snapshotJSON := mustMarshalJSON(t, record.Snapshot)
	createdAt := record.CreatedAt
	finalizedAt := createdAt.Add(time.Minute)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, api_key_id, user_id, access_group_id, request_id, attempt_id, status, selection_json, snapshot_json")).
		WithArgs(record.APIKeyID, record.UserID, record.AccessGroupID, requestID, attemptID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "api_key_id", "user_id", "access_group_id", "request_id", "attempt_id", "status", "selection_json", "snapshot_json", "response_body", "measured_units", "user_charge", "upstream_request_id", "failure_message", "created_at", "finalized_at"}).
			AddRow(123, record.APIKeyID, record.UserID, record.AccessGroupID, requestID, attemptID, "captured", selectionJSON, snapshotJSON, []byte(`{"ok":true}`), 1.0, 0.0024, "upstream-1", "", createdAt, finalizedAt))
	got, err := repo.Get(context.Background(), record.APIKeyID, record.UserID, record.AccessGroupID, requestID, attemptID)
	require.NoError(t, err)
	require.Equal(t, service.UnifiedGatewaySnapshotCaptured, got.Status)
	require.Equal(t, record.Snapshot.Digest, got.Snapshot.Digest)
	require.Equal(t, []byte(`{"ok":true}`), got.ResponseBody)

	mock.ExpectExec(regexp.QuoteMeta("UPDATE unified_route_price_snapshots")).
		WithArgs(record.APIKeyID, record.UserID, record.AccessGroupID, requestID, attemptID, "captured", 1.0, 0.0024, "upstream-1", sqlmock.AnyArg(), "").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.Finalize(context.Background(), record.APIKeyID, record.UserID, record.AccessGroupID, requestID, attemptID, service.UnifiedGatewaySnapshotCaptured, 1, 0.0024, "upstream-1", []byte(`{"ok":true}`), ""))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUnifiedGatewaySnapshotRepositoryDoesNotGuessDriverFailureAsConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewUnifiedGatewaySnapshotRepository(db)
	record := unifiedGatewaySnapshotRepoRecord()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO unified_route_price_snapshots")).
		WithArgs(
			record.APIKeyID, record.UserID, record.RequestID, record.AttemptID, record.Snapshot.RouteID, record.AccessGroupID, record.Snapshot.BillingLaneID,
			record.Snapshot.AccountID, record.Snapshot.ProviderIdentity, record.Snapshot.PublicModel, record.Snapshot.UpstreamModel, record.Snapshot.Endpoint,
			string(record.Status), sqlmock.AnyArg(), sqlmock.AnyArg(), nil, 0.0, 0.0, "", "", record.CreatedAt,
		).
		WillReturnError(errors.New("driver unavailable"))
	_, err = repo.Create(context.Background(), record)
	require.EqualError(t, err, "driver unavailable")
	require.NoError(t, mock.ExpectationsWereMet())
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}
