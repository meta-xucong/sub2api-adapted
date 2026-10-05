//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUnifiedGatewayAdminRepositoryAtomicLifecycleAndProjection(t *testing.T) {
	ctx := context.Background()
	repo := NewUnifiedGatewayAdminRepository(integrationDB)
	atomicRepo, ok := repo.(service.UnifiedGatewayAdminAtomicRepository)
	require.True(t, ok)
	pricingRepo, ok := repo.(service.UnifiedGatewayAdminPricingImportAtomicRepository)
	require.True(t, ok)

	configID := "mc_" + uuid.NewString()
	draftID := "draft_" + uuid.NewString()
	laneID := "lane_" + uuid.NewString()
	profileID := "profile_" + uuid.NewString()
	targetID := "target_" + uuid.NewString()
	bindingID := "binding_" + uuid.NewString()
	const groupID = int64(987654321)
	const accountID = int64(876543219)

	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM unified_route_account_bindings WHERE unified_config_id=$1`, configID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM unified_route_targets WHERE unified_config_id=$1`, configID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM unified_gateway_admin_idempotency WHERE resource_id IN ($1,$2,$3)`, configID, draftID, "")
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM unified_gateway_config_revisions WHERE config_id=$1`, configID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM unified_gateway_drafts WHERE config_id=$1`, configID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM unified_gateway_pricing_profiles WHERE lane_id=$1`, laneID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM unified_gateway_billing_lanes WHERE config_id=$1`, configID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM unified_gateway_model_configs WHERE id=$1`, configID)
	})

	readiness, err := repo.CheckSchema(ctx)
	require.NoError(t, err)
	require.True(t, readiness.Ready, "missing Phase5 schema objects: %v", readiness.Missing)
	require.Equal(t, service.UnifiedGatewayAdminSchemaVersion, readiness.Version)

	providerPrice, userPrice := "0.00001234", "0.00001999"
	document := service.UnifiedGatewayConfig{
		ID: configID, AccessGroupID: fmt.Sprintf("ag_%d", groupID), PublicModel: "phase5-admin-test", Endpoint: service.UnifiedGatewayEndpointChatCompletions,
		Lanes: []service.UnifiedGatewayBillingLane{{
			ID: laneID, Code: "primary", Name: "Primary", SelectionStrategy: "fixed_priority",
			Profile: service.UnifiedGatewayPricingProfile{
				ID: profileID, Version: "1", PricingModel: service.UnifiedGatewayPricingModelProviderMetered,
				BillingMode: "token", RateMode: "manual_only", RateBasis: "per_token", PricingSchemaID: "phase5-test",
				Currency: "USD", BasePriceSemantics: "provider_base", ProviderBaseUnitPrice: &providerPrice,
				FinalUserUnitPrice: &userPrice, RoundingMode: service.UnifiedGatewayRoundingHalfUp, Precision: 8,
				ChargeTrigger: service.UnifiedGatewayChargeTriggerSuccess, FailureCharge: service.UnifiedGatewayFailureChargeZero,
			},
			Targets: []service.UnifiedGatewayAdminRouteTarget{{
				ID: targetID, ProviderIdentity: "test-provider", UpstreamModel: "upstream-test", Endpoint: service.UnifiedGatewayEndpointChatCompletions,
				Priority: 1, Bindings: []service.UnifiedGatewayAdminAccountBinding{{
					ID: bindingID, AccountID: fmt.Sprintf("acct_%d", accountID), Endpoint: service.UnifiedGatewayEndpointChatCompletions,
					Enabled: true, Priority: 1, Revision: 1, Eligibility: "eligible",
				}},
			}},
		}},
	}
	now := time.Now().UTC()
	draft := &service.UnifiedGatewayDraft{ID: draftID, ConfigID: configID, Document: document, CreatedBy: "phase5-test", UpdatedBy: "phase5-test", CreatedAt: now, UpdatedAt: now}
	createKey := testUnifiedGatewayIdempotency("draft.create", "", "create-key", "create-digest")
	created, replayed, err := atomicRepo.CreateDraftAtomic(ctx, draft, createKey)
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, draftID, created.ID)

	otherDraft := *draft
	otherDraft.ID = "draft_replay_" + uuid.NewString()
	replayedDraft, replayed, err := atomicRepo.CreateDraftAtomic(ctx, &otherDraft, testUnifiedGatewayIdempotency("draft.create", "", "create-key", "create-digest"))
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, draftID, replayedDraft.ID)
	var configCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM unified_gateway_model_configs WHERE id=$1`, configID).Scan(&configCount))
	require.Equal(t, 1, configCount)

	updatedDoc := created.Document
	updatedDoc.Lanes[0].Name = "Primary updated"
	updated, replayed, err := atomicRepo.UpdateDraftAtomic(ctx, draftID, 0, updatedDoc, "phase5-test", testUnifiedGatewayIdempotency("draft.update", draftID, "update-key", "update-digest"))
	require.NoError(t, err)
	require.False(t, replayed)
	require.EqualValues(t, 1, updated.Revision)

	importDoc := updated.Document
	importDoc.Lanes[0].Profile.Version = "2"
	importResult := &service.UnifiedGatewayPricingImportResult{LaneID: laneID, SourceGroupID: "ag_55", SourceRevision: "r1", ImportDigest: "import-digest"}
	imported, replayed, err := pricingRepo.ApplyPricingImportAtomic(ctx, draftID, 1, importDoc, "phase5-test", importResult, testUnifiedGatewayIdempotency("pricing.import.apply", draftID, "import-key", "import-digest"))
	require.NoError(t, err)
	require.False(t, replayed)
	require.EqualValues(t, 2, imported.Draft.Revision)

	published, replayed, err := atomicRepo.PublishConfigAtomic(ctx, configID, draftID, 0, 2, imported.Draft.Document, "phase5-test", "publish", "digest-publish", testUnifiedGatewayIdempotency("config.publish", configID, "publish-key", "publish-digest"))
	require.NoError(t, err)
	require.False(t, replayed)
	require.EqualValues(t, 1, published.Revision)
	require.Equal(t, service.UnifiedGatewayLifecyclePublished, published.Lifecycle)
	var targetCount, bindingCount, laneCount, profileCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM unified_route_targets WHERE unified_config_id=$1 AND enabled`, configID).Scan(&targetCount))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM unified_route_account_bindings WHERE unified_config_id=$1 AND enabled`, configID).Scan(&bindingCount))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM unified_gateway_billing_lanes WHERE config_id=$1 AND enabled`, configID).Scan(&laneCount))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM unified_gateway_pricing_profiles WHERE lane_id=$1`, laneID).Scan(&profileCount))
	require.Equal(t, 1, targetCount)
	require.Equal(t, 1, bindingCount)
	require.Equal(t, 1, laneCount)
	require.Equal(t, 1, profileCount)

	replayedConfig, replayed, err := atomicRepo.PublishConfigAtomic(ctx, configID, draftID, 0, 2, imported.Draft.Document, "phase5-test", "publish", "digest-publish", testUnifiedGatewayIdempotency("config.publish", configID, "publish-key", "publish-digest"))
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, published.Revision, replayedConfig.Revision)

	disabled, replayed, err := atomicRepo.DisableConfigAtomic(ctx, configID, 1, "phase5-test", "disable", testUnifiedGatewayIdempotency("config.disable", configID, "disable-key", "disable-digest"))
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, service.UnifiedGatewayLifecycleDisabled, disabled.Lifecycle)
	for _, query := range []string{
		`SELECT COUNT(*) FROM unified_route_targets WHERE unified_config_id=$1 AND enabled`,
		`SELECT COUNT(*) FROM unified_route_account_bindings WHERE unified_config_id=$1 AND enabled`,
		`SELECT COUNT(*) FROM unified_gateway_billing_lanes WHERE config_id=$1 AND enabled`,
	} {
		var enabledCount int
		require.NoError(t, integrationDB.QueryRowContext(ctx, query, configID).Scan(&enabledCount))
		require.Zero(t, enabledCount)
	}

	restored, replayed, err := atomicRepo.RestoreConfigAtomic(ctx, configID, 1, 2, "phase5-test", "restore", testUnifiedGatewayIdempotency("config.restore", configID, "restore-key", "restore-digest"))
	require.NoError(t, err)
	require.False(t, replayed)
	require.EqualValues(t, 3, restored.Revision)
	require.Equal(t, service.UnifiedGatewayLifecyclePublished, restored.Lifecycle)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM unified_route_targets WHERE unified_config_id=$1 AND enabled`, configID).Scan(&targetCount))
	require.Equal(t, 1, targetCount)

	revisions, err := repo.ListRevisions(ctx, configID)
	require.NoError(t, err)
	require.Len(t, revisions, 3)
	require.EqualValues(t, 3, revisions[0].Revision)
	require.EqualValues(t, 1, revisions[2].Revision)

	record, err := repo.GetIdempotency(ctx, "phase5-test", "config.publish", configID, "publish-key")
	require.NoError(t, err)
	require.NotNil(t, record)
	require.Equal(t, 200, record.StatusCode)
	readRecord := testUnifiedGatewayIdempotency("admin.read", configID, "read-key", "read-digest")
	readRecord.StatusCode, readRecord.ResponseJSON = 200, []byte(`{}`)
	require.NoError(t, repo.PutIdempotency(ctx, readRecord))

	configs, total, err := repo.ListConfigs(ctx, service.UnifiedGatewayConfigListFilter{Page: 1, PageSize: 10, AccessGroupIDs: []int64{groupID}})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, configs, 1)
	require.Equal(t, service.UnifiedGatewayLifecyclePublished, configs[0].Lifecycle)

	got, err := repo.GetConfig(ctx, configID)
	require.NoError(t, err)
	require.EqualValues(t, 3, got.Revision)
	gotDraft, err := repo.GetDraft(ctx, draftID)
	require.NoError(t, err)
	require.EqualValues(t, 2, gotDraft.Revision)

	// Exercise the new-draft branch for an existing published config. The
	// earlier draft is removed only inside this disposable integration DB.
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM unified_gateway_drafts WHERE config_id=$1`, configID)
	require.NoError(t, err)
	fromConfig := &service.UnifiedGatewayDraft{
		ID: "draft_from_" + uuid.NewString(), ConfigID: configID, Document: *got,
		CreatedBy: "phase5-test", UpdatedBy: "phase5-test", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	opened, replayed, err := atomicRepo.CreateDraftFromConfigAtomic(ctx, configID, fromConfig, testUnifiedGatewayIdempotency("draft.create_from_config", configID, "from-config-key", "from-config-digest"))
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, fromConfig.ID, opened.ID)
	replayedDraft, replayed, err = atomicRepo.CreateDraftFromConfigAtomic(ctx, configID, fromConfig, testUnifiedGatewayIdempotency("draft.create_from_config", configID, "from-config-key", "from-config-digest"))
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, opened.ID, replayedDraft.ID)
}

func testUnifiedGatewayIdempotency(operation, resource, key, digest string) *service.UnifiedGatewayIdempotencyRecord {
	return &service.UnifiedGatewayIdempotencyRecord{
		ActorID: "phase5-test", Operation: operation, ResourceID: resource, Key: key,
		RequestDigest: digest, CreatedAt: time.Now().UTC(),
	}
}
