package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUnifiedGatewayRouteCatalogRepositoryResolvesExplicitAccountBinding(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewUnifiedGatewayRouteCatalogRepository(db)
	laneRule := service.UnifiedRateRule{ProfileID: "ark-v1", Version: "1", BillingMode: string(service.BillingModePerRequest), UpstreamRateBasis: service.UnifiedRateBasisProviderSpecific, BasePriceSemantics: service.UnifiedBasePriceProviderBase, ManualBaseUnitPrice: floatPointer(0.002), UserMarkupMultiplier: floatPointer(1.2)}
	probe := service.UnifiedProbeSnapshot{Status: service.UnifiedProbeStatusUnsupported, Basis: service.UnifiedRateBasisProviderSpecific, ReceivedAt: time.Date(2026, 9, 11, 4, 0, 0, 0, time.UTC)}
	laneJSON := mustMarshalJSON(t, laneRule)
	probeJSON := mustMarshalJSON(t, probe)
	rows := sqlmock.NewRows([]string{
		"target_id", "access_group_id", "billing_lane_id", "public_model", "provider_identity", "upstream_model", "endpoint", "pool_id", "billing_mode", "rate_mode", "rate_basis", "lane_rule", "pool_rule", "target_priority",
		"binding_id", "route_target_id", "account_id", "binding_provider", "binding_model", "binding_endpoint", "account_rule", "probe_snapshot", "binding_priority",
	}).AddRow(
		77, 42, "ark", "doubao", "ark", "doubao-seed", "chat_completions", "ark-pool", string(service.BillingModePerRequest), string(service.UnifiedRateModeManualOnly), string(service.UnifiedRateBasisProviderSpecific), laneJSON, []byte(`{}`), 1,
		88, 77, 99, "", "", "", []byte(`{}`), probeJSON, 1,
	)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT t.id, t.access_group_id, t.billing_lane_id, t.public_model")).
		WithArgs(int64(42), "doubao", "chat_completions").
		WillReturnRows(rows)
	selection, err := repo.Resolve(context.Background(), 42, "doubao", "chat_completions")
	require.NoError(t, err)
	require.Equal(t, int64(77), selection.Target.ID)
	require.Equal(t, int64(99), selection.Binding.AccountID)
	require.Equal(t, "ark", selection.ProviderIdentity())
	require.Equal(t, "doubao-seed", selection.UpstreamModel())
	require.Equal(t, service.UnifiedRateModeManualOnly, selection.Target.RateMode)
	require.Equal(t, 0.002, *selection.Target.LaneRule.ManualBaseUnitPrice)
	require.Equal(t, service.UnifiedProbeStatusUnsupported, selection.Binding.Probe.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func floatPointer(value float64) *float64 {
	return &value
}
