package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// unifiedGatewayRouteCatalogRepository reads only the unified tables.  It is
// intentionally not an adapter around CompositeRouteResolver: that resolver
// has legacy ownership/model-detection fallbacks which are unsafe for a
// price-bound unified request.
type unifiedGatewayRouteCatalogRepository struct {
	db *sql.DB
}

func NewUnifiedGatewayRouteCatalogRepository(db *sql.DB) service.UnifiedGatewayRouteCatalog {
	return &unifiedGatewayRouteCatalogRepository{db: db}
}

func (r *unifiedGatewayRouteCatalogRepository) Resolve(ctx context.Context, accessGroupID int64, publicModel, endpoint string) (service.UnifiedGatewayRouteSelection, error) {
	selections, err := r.List(ctx, accessGroupID, publicModel, endpoint)
	if err != nil {
		return service.UnifiedGatewayRouteSelection{}, err
	}
	if len(selections) == 0 {
		return service.UnifiedGatewayRouteSelection{}, service.ErrUnifiedGatewayRouteNotFound
	}
	return selections[0], nil
}

func (r *unifiedGatewayRouteCatalogRepository) List(ctx context.Context, accessGroupID int64, publicModel, endpoint string) ([]service.UnifiedGatewayRouteSelection, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("unified gateway route catalog db is nil")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			t.id, t.access_group_id, t.billing_lane_id, t.public_model,
			t.provider_identity, t.upstream_model, t.endpoint, t.pool_id,
			t.billing_mode, t.rate_mode, t.rate_basis, t.lane_rule, t.pool_rule,
			t.priority,
			b.id, b.route_target_id, b.account_id, b.provider_identity,
			b.upstream_model, b.endpoint, b.account_rule, b.probe_snapshot,
			b.priority
		FROM unified_route_targets t
		JOIN unified_route_account_bindings b ON b.route_target_id = t.id
		WHERE t.access_group_id = $1
			AND ($2 = '' OR t.public_model = $2)
			AND ($3 = '' OR t.endpoint = $3)
			AND t.enabled = TRUE AND b.enabled = TRUE
		ORDER BY t.priority ASC, t.id ASC, b.priority ASC, b.id ASC
	`, accessGroupID, strings.TrimSpace(publicModel), strings.TrimSpace(endpoint))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var selections []service.UnifiedGatewayRouteSelection
	for rows.Next() {
		var (
			target                                     service.UnifiedGatewayRouteTarget
			binding                                    service.UnifiedGatewayAccountBinding
			laneJSON, poolJSON, accountJSON, probeJSON []byte
		)
		if err := rows.Scan(
			&target.ID, &target.AccessGroupID, &target.BillingLaneID, &target.PublicModel,
			&target.ProviderIdentity, &target.UpstreamModel, &target.Endpoint, &target.PoolID,
			&target.BillingMode, &target.RateMode, &target.RateBasis, &laneJSON, &poolJSON,
			&target.Priority,
			&binding.ID, &binding.RouteTargetID, &binding.AccountID, &binding.ProviderIdentity,
			&binding.UpstreamModel, &binding.Endpoint, &accountJSON, &probeJSON,
			&binding.Priority,
		); err != nil {
			return nil, err
		}
		target.Enabled = true
		binding.Enabled = true
		if target.LaneRule, err = decodeUnifiedGatewayRule(laneJSON); err != nil {
			return nil, err
		}
		if target.PoolRule, err = decodeUnifiedGatewayRule(poolJSON); err != nil {
			return nil, err
		}
		if binding.AccountRule, err = decodeUnifiedGatewayRule(accountJSON); err != nil {
			return nil, err
		}
		if binding.Probe, err = decodeUnifiedGatewayProbe(probeJSON); err != nil {
			return nil, err
		}
		selections = append(selections, service.UnifiedGatewayRouteSelection{Target: target, Binding: binding})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return selections, nil
}

func decodeUnifiedGatewayRule(data []byte) (*service.UnifiedRateRule, error) {
	if len(data) == 0 || string(data) == "{}" || string(data) == "null" {
		return nil, nil
	}
	var rule service.UnifiedRateRule
	if err := json.Unmarshal(data, &rule); err != nil {
		return nil, err
	}
	return &rule, nil
}

func decodeUnifiedGatewayProbe(data []byte) (*service.UnifiedProbeSnapshot, error) {
	if len(data) == 0 || string(data) == "{}" || string(data) == "null" {
		return nil, nil
	}
	var probe service.UnifiedProbeSnapshot
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	return &probe, nil
}
